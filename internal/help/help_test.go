package help_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	goldenPath = "testdata/card.golden"
	maxLines   = 40
	labelWidth = 14
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

// newServerName returns a unique private server name for a test.
func newServerName(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("orrery-help-%d-%d", os.Getpid(), time.Now().UnixNano())
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
