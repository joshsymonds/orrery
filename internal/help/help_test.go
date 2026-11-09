package help_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/help"
	"github.com/joshsymonds/orrery/internal/keymap"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

const (
	goldenPath     = "testdata/card.golden"
	pageGoldenPath = "testdata/page.golden"
	readmePath     = "../../README.md"
	maxLines       = 40
	labelWidth     = 14
)

// row formats one card row the way the frozen layout does: two spaces, the label left-justified to labelWidth, then
// the text.
func row(label, text string) string {
	return fmt.Sprintf("  %-*s%s", labelWidth, label, text)
}

// bindingRows returns the card row each keymap binding must produce.
func bindingRows(t *testing.T) []string {
	t.Helper()
	var rows []string
	for _, b := range keymap.Bindings() {
		switch b.Surface {
		case keymap.Tree:
			rows = append(rows, row(strings.Join(b.Keys, ", "), b.Help))
		case keymap.Tmux:
			for _, key := range b.Keys {
				rows = append(rows, row("prefix "+key, b.Help))
			}
		default:
			t.Fatalf("binding surface %q has no card section", b.Surface)
		}
	}
	return rows
}

func TestCardMatchesGolden(t *testing.T) {
	var got bytes.Buffer
	if err := help.Card(&got, "C-a"); err != nil {
		t.Fatalf("Card: %v", err)
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Errorf("Card(w, \"C-a\") differs from %s:\n%s", goldenPath, got.String())
	}
}

func TestCardListsEveryKeymapBinding(t *testing.T) {
	var got bytes.Buffer
	if err := help.Card(&got, "C-a"); err != nil {
		t.Fatalf("Card: %v", err)
	}
	lines := strings.Split(got.String(), "\n")
	for _, want := range bindingRows(t) {
		if !slices.Contains(lines, want) {
			t.Errorf("card has no row %q", want)
		}
	}
}

func TestCardWithoutPrefixHeadingIsPlainTmux(t *testing.T) {
	var got bytes.Buffer
	if err := help.Card(&got, ""); err != nil {
		t.Fatalf("Card: %v", err)
	}
	out := got.String()
	if !slices.Contains(strings.Split(out, "\n"), "tmux") {
		t.Errorf("card has no plain \"tmux\" heading:\n%s", out)
	}
	if strings.Contains(out, "prefix is") {
		t.Errorf("card with empty prefix names a prefix:\n%s", out)
	}
	if lines := strings.Count(out, "\n"); lines > maxLines {
		t.Errorf("card has %d lines, want at most %d", lines, maxLines)
	}
}

func TestCardLineCountWithPrefix(t *testing.T) {
	var got bytes.Buffer
	if err := help.Card(&got, "C-a"); err != nil {
		t.Fatalf("Card: %v", err)
	}
	if lines := strings.Count(got.String(), "\n"); lines > maxLines {
		t.Errorf("card has %d lines, want at most %d", lines, maxLines)
	}
}

// failingWriter fails every write with errWrite.
type failingWriter struct{}

var errWrite = errors.New("write failed")

func (failingWriter) Write([]byte) (int, error) {
	return 0, errWrite
}

func TestCardReturnsWriteError(t *testing.T) {
	err := help.Card(failingWriter{}, "C-a")
	if !errors.Is(err, errWrite) {
		t.Fatalf("Card error = %v, want %v", err, errWrite)
	}
}

// readmeDailyUseBlock returns the text of the first fenced text block after README.md's Daily use heading, with a
// trailing newline.
func readmeDailyUseBlock(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	lines := strings.Split(string(raw), "\n")
	heading := slices.Index(lines, "## Daily use")
	if heading < 0 {
		t.Fatalf("README.md has no \"## Daily use\" heading")
	}
	rest := lines[heading+1:]
	open := slices.Index(rest, "```text")
	if open < 0 {
		t.Fatalf("README.md has no ```text block after the \"## Daily use\" heading")
	}
	rest = rest[open+1:]
	end := slices.Index(rest, "```")
	if end < 0 {
		t.Fatalf("README.md's ```text block after \"## Daily use\" is not closed")
	}
	return strings.Join(rest[:end], "\n") + "\n"
}

func TestReadmeDailyUseMatchesCard(t *testing.T) {
	var card bytes.Buffer
	if err := help.Card(&card, ""); err != nil {
		t.Fatalf("Card: %v", err)
	}
	if got := readmeDailyUseBlock(t); got != card.String() {
		t.Errorf("README.md Daily use block differs from Card:\n--- README.md\n%s--- Card\n%s",
			got, card.String())
	}
}

// newServerName returns a unique private server name for a test.
func newServerName(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("orrery-help-%d-%d", os.Getpid(), time.Now().UnixNano())
}

// escapeText returns text as the page writes it. html/template escapes '+' as well as what html.EscapeString escapes.
func escapeText(text string) string {
	return strings.ReplaceAll(html.EscapeString(text), "+", "&#43;")
}

// pageRow returns the table row the page writes for one label and text.
func pageRow(label, text string) string {
	return fmt.Sprintf("<tr><th>%s</th><td>%s</td></tr>", escapeText(label), escapeText(text))
}

// renderPage returns the page for a prefix of C-a.
func renderPage(t *testing.T) string {
	t.Helper()
	var got bytes.Buffer
	if err := help.Page(&got, "C-a"); err != nil {
		t.Fatalf("Page: %v", err)
	}
	return got.String()
}

func TestPageMatchesGolden(t *testing.T) {
	got := renderPage(t)
	want, err := os.ReadFile(pageGoldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got != string(want) {
		t.Errorf("Page(w, \"C-a\") differs from %s:\n%s", pageGoldenPath, got)
	}
}

func TestPageRepeatsEveryCardHeadingAndRow(t *testing.T) {
	var card bytes.Buffer
	if err := help.Card(&card, "C-a"); err != nil {
		t.Fatalf("Card: %v", err)
	}
	page := renderPage(t)

	lines := strings.Split(card.String(), "\n")
	title := lines[0]
	if want := "<h1>" + escapeText(title) + "</h1>"; !strings.Contains(page, want) {
		t.Errorf("page has no %q", want)
	}
	for _, line := range lines[1:] {
		switch {
		case line == "":
		case !strings.HasPrefix(line, "  "):
			if want := "<h2>" + escapeText(line) + "</h2>"; !strings.Contains(page, want) {
				t.Errorf("page has no %q", want)
			}
		default:
			if len(line) < labelWidth+2 {
				t.Fatalf("card row %q is shorter than its label column", line)
			}
			label := strings.TrimSpace(line[:labelWidth+2])
			if want := pageRow(label, line[labelWidth+2:]); !strings.Contains(page, want) {
				t.Errorf("page has no row %q", want)
			}
		}
	}
}

func TestPageListsEveryKeymapBinding(t *testing.T) {
	page := renderPage(t)
	for _, b := range keymap.Bindings() {
		switch b.Surface {
		case keymap.Tree:
			if want := pageRow(strings.Join(b.Keys, ", "), b.Help); !strings.Contains(page, want) {
				t.Errorf("page has no row %q", want)
			}
		case keymap.Tmux:
			for _, key := range b.Keys {
				if want := pageRow("prefix "+key, b.Help); !strings.Contains(page, want) {
					t.Errorf("page has no row %q", want)
				}
			}
		default:
			t.Fatalf("binding surface %q has no page section", b.Surface)
		}
	}
}

func TestPageHasNoExternalAssets(t *testing.T) {
	page := renderPage(t)
	if !strings.HasPrefix(page, "<!doctype html>") {
		t.Fatalf("page does not start with <!doctype html>:\n%s", page)
	}
	lower := strings.ToLower(page)
	for _, banned := range []string{"<script", "<link", "<img", "src", "url(", "@import", "http://", "https://"} {
		if strings.Contains(lower, banned) {
			t.Errorf("page contains %q, want no external assets", banned)
		}
	}
}

func TestPageReturnsWriteError(t *testing.T) {
	err := help.Page(failingWriter{}, "C-a")
	if !errors.Is(err, errWrite) {
		t.Fatalf("Page error = %v, want %v", err, errWrite)
	}
}

func TestPrefixReadsLiveServerPrefix(t *testing.T) {
	name := newServerName(t)
	client, err := tmuxctl.New(tmuxctl.Server{Name: name})
	if err != nil {
		t.Fatalf("New(Name=%q): %v", name, err)
	}
	t.Cleanup(func() {
		if _, killErr := client.Run(context.Background(), "kill-server"); killErr != nil {
			t.Errorf("kill-server: %v", killErr)
		}
	})
	if _, err := client.Run(t.Context(), "-f", "/dev/null", "new-session", "-d", "-s", "help"); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	if _, err := client.Run(t.Context(), "set-option", "-g", "prefix", "C-a"); err != nil {
		t.Fatalf("set-option prefix: %v", err)
	}

	if got := help.Prefix(t.Context(), client); got != "C-a" {
		t.Errorf("Prefix = %q, want %q", got, "C-a")
	}
}

func TestPrefixIsEmptyWithoutServer(t *testing.T) {
	client, err := tmuxctl.New(tmuxctl.Server{Name: newServerName(t)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := help.Prefix(t.Context(), client); got != "" {
		t.Errorf("Prefix on an unstarted server = %q, want empty", got)
	}
}
