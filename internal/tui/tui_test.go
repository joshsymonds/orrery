package tui

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/joshsymonds/orrery/internal/keymap"
	"github.com/joshsymonds/orrery/internal/palette"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
)

// Golden frames render at this terminal size.
const (
	treeWidth  = 100
	treeHeight = 30
	// testNowUnix is the fixed clock every golden frame is rendered at.
	testNowUnix = 1_700_000_000
)

// treePanes is synthetic `orrery list --json` output: waiting, unseen done, seen working,
// unset state, and a popup-shell pane that the tree must leave out.
const treePanes = `[
 {"id":"%1","host":"","session":"s1","window":0,"pane":0,"cwd":"/home/user/src/alpha","command":"claude","state":"waiting","summary":"needs approval","label":"alpha-review","changed":1699999880,"seen":1699999880,"question":"","context":""},
 {"id":"%2","host":"","session":"s2","window":0,"pane":0,"cwd":"/home/user/src/alpha","command":"claude","state":"done","summary":"ran 14 tests","label":"alpha-tests","changed":1699999970,"seen":1699999700,"question":"","context":""},
 {"id":"%3","host":"","session":"s3","window":0,"pane":0,"cwd":"/home/user/src/beta","command":"claude","state":"working","summary":"compiling","label":"beta-build","changed":1699996400,"seen":1699999000,"question":"","context":""},
 {"id":"%4","host":"","session":"s4","window":0,"pane":0,"cwd":"/home/user/src/beta","command":"zsh","state":"","summary":"","label":"beta-notes","changed":0,"seen":0,"question":"","context":""},
 {"id":"%5","host":"","session":"_orrery-shell-1","window":0,"pane":0,"cwd":"/home/user/src/alpha","command":"zsh","state":"working","summary":"","label":"shell-label","changed":1699999990,"seen":0,"question":"","context":""}
]`

// settle delivers each command's message to m and repeats until no command remains.
func settle(t *testing.T, m tea.Model, cmd tea.Cmd) tea.Model {
	t.Helper()
	for cmd != nil {
		m, cmd = m.Update(cmd())
	}
	return m
}

// renderPanes loads panes and captures into a model at the golden size and returns its frame.
func renderPanes(t *testing.T, panes []protocol.Pane, captures map[string]string) string {
	t.Helper()
	source := func() ([]protocol.Pane, error) { return panes, nil }
	capture := func(id string) (string, error) { return captures[id], nil }
	m := newModel(source, capture, func() time.Time { return time.Unix(testNowUnix, 0) })

	next, _ := m.Update(tea.WindowSizeMsg{Width: treeWidth, Height: treeHeight})
	next = settle(t, next, m.loadPanes())
	return next.View().Content
}

// checkGolden compares got with testdata/<name>.golden, which holds got followed by one newline.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", name+".golden"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got+"\n" != string(want) {
		t.Fatalf("frame does not match %s.golden\ngot:\n%s\nwant:\n%s", name, got, want)
	}
}

func TestViewTree(t *testing.T) {
	var panes []protocol.Pane
	if err := json.Unmarshal([]byte(treePanes), &panes); err != nil {
		t.Fatalf("decode panes: %v", err)
	}
	captures := map[string]string{
		"%1": "$ claude\n> Approve the migration? [y/n]\n",
		"%2": "tests passed\n",
	}
	got := renderPanes(t, panes, captures)
	if strings.Contains(got, "shell-label") {
		t.Fatalf("popup-shell pane rendered:\n%s", got)
	}
	checkGolden(t, "TestViewTree", got)
}

func TestViewEmpty(t *testing.T) {
	checkGolden(t, "TestViewEmpty", renderPanes(t, nil, nil))
}

func TestViewErrorLine(t *testing.T) {
	var panes []protocol.Pane
	if err := json.Unmarshal([]byte(treePanes), &panes); err != nil {
		t.Fatalf("decode panes: %v", err)
	}
	cases := []struct {
		name string
		msg  tea.Msg
		want string
	}{
		{"source error", panesMsg{err: errors.New("tmux server gone")}, "error: tmux server gone"},
		{"capture error", previewMsg{paneID: "%1", err: errors.New("no such pane")}, "error: no such pane"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(
				func() ([]protocol.Pane, error) { return panes, nil },
				func(string) (string, error) { return "", nil },
				func() time.Time { return time.Unix(testNowUnix, 0) },
			)
			next, _ := m.Update(tea.WindowSizeMsg{Width: treeWidth, Height: treeHeight})
			next = settle(t, next, m.loadPanes())
			next, _ = next.Update(tc.msg)
			lines := strings.Split(next.View().Content, "\n")
			if last := lines[len(lines)-1]; last != tc.want {
				t.Fatalf("last line = %q, want %q", last, tc.want)
			}
			if !strings.Contains(next.View().Content, "alpha-review") {
				t.Fatalf("tree rows lost after error:\n%s", next.View().Content)
			}
		})
	}
}

// tuiNoConfig keeps the user's tmux configuration out of test servers.
const tuiNoConfig = "/dev/null"

// tuiServer returns a client for a unique private tmux server that is killed at cleanup.
func tuiServer(t *testing.T) *tmuxctl.Client {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	name := fmt.Sprintf("orrery-test-tui-%d-%x", os.Getpid(), suffix)
	client, err := tmuxctl.New(tmuxctl.Server{Name: name})
	if err != nil {
		t.Fatalf("New(Name=%q): %v", name, err)
	}
	t.Cleanup(func() {
		if err := tmuxtest.Stop(t, tmuxctl.Server{Name: name}); err != nil {
			t.Errorf("kill-server: %v", err)
		}
	})
	return client
}

// syncBuffer is an io.Writer that Run's render goroutine and the test can share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("buffer write: %w", err)
	}
	return n, nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type runOutcome struct {
	result Result
	err    error
}

func TestRun(t *testing.T) {
	client := tuiServer(t)
	dir := t.TempDir()
	const preview = "tui preview line"
	if _, err := client.Run(t.Context(), "-f", tuiNoConfig, "new-session", "-d", "-s", "s1", "-c", dir,
		"printf '"+preview+"\\n'; sleep 300"); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	out := &syncBuffer{}
	done := make(chan runOutcome, 1)
	go func() {
		res, runErr := Run(ctx, Options{
			Client: client,
			Panes:  client.ListPanes,
			Input:  strings.NewReader(""),
			Output: out,
		})
		done <- runOutcome{result: res, err: runErr}
	}()

	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), preview) {
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("preview %q never rendered; output:\n%s", preview, out.String())
		}
		time.Sleep(50 * time.Millisecond)
	}

	cancel()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Run: %v", got.err)
		}
		if got.result.Action != ActionClose {
			t.Fatalf("Run action = %q, want %q", got.result.Action, ActionClose)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after ctx was canceled")
	}
}

func TestViewHelp(t *testing.T) {
	m := pressAll(t, loadedModel(t, staticSource), "?")
	checkGolden(t, "TestViewHelp", m.View().Content)
}

func TestHelpListsEveryTreeKeyAndHelpWord(t *testing.T) {
	content := pressAll(t, loadedModel(t, staticSource), "?").View().Content
	lines := strings.Split(content, "\n")
	for _, b := range keymap.For(keymap.Tree) {
		keys := strings.Join(b.Keys, ", ")
		if !slices.ContainsFunc(lines, func(line string) bool { return strings.HasPrefix(ansi.Strip(line), keys) }) {
			t.Errorf("no overlay line starts with keys %q:\n%s", keys, content)
		}
		for _, word := range strings.Fields(b.Help) {
			if !strings.Contains(content, word) {
				t.Errorf("overlay lacks help word %q for keys %q:\n%s", word, keys, content)
			}
		}
	}
}

func TestHelpKeysAndTitleUsePaletteColours(t *testing.T) {
	content := pressAll(t, loadedModel(t, staticSource), "?").View().Content
	lines := strings.Split(content, "\n")
	if want := palette.Fg(palette.Mauve, "Keys"); lines[0] != want {
		t.Errorf("overlay title line = %q, want %q", lines[0], want)
	}
	for _, b := range keymap.For(keymap.Tree) {
		want := palette.Fg(palette.Lavender, strings.Join(b.Keys, ", "))
		if !slices.ContainsFunc(lines, func(line string) bool { return strings.HasPrefix(line, want) }) {
			t.Errorf("no overlay line starts with %q:\n%s", want, content)
		}
	}
}

func TestHelpClosesOnAnyKeyAndRunsNothingElse(t *testing.T) {
	tree := loadedModel(t, staticSource).View().Content
	for _, name := range []string{"j", "q", "enter", "?"} {
		t.Run(name, func(t *testing.T) {
			m := pressAll(t, loadedModel(t, staticSource), "?")
			if !strings.HasPrefix(ansi.Strip(m.View().Content), "Keys\n") {
				t.Fatal("? did not open the help overlay")
			}
			next, cmd := m.Update(keyMsg(name))
			if cmd != nil {
				t.Fatalf("key %q while help shows returned a command, want none", name)
			}
			m = modelOf(t, next)
			if got := m.View().Content; got != tree {
				t.Fatalf("frame after %q is not the tree frame:\n%s\nwant:\n%s", name, got, tree)
			}
			assertCursor(t, m, "%1")
		})
	}
}

// quietPanes is synthetic panes for the Quiet section. alpha keeps a waiting pane and an unseen
// pane, so it stays active. gamma's only pane is done and seen, with activity more than
// QuietAfter before testNowUnix, so it folds into Quiet.
func quietPanes() []protocol.Pane {
	return []protocol.Pane{
		{ID: "%1", Session: "s1", Cwd: "/home/user/src/alpha", State: protocol.StateWaiting,
			Label: "alpha-review", Summary: "needs approval",
			Changed: 1699999880, Seen: 1699999880, Activity: 1699999880},
		{ID: "%2", Session: "s2", Cwd: "/home/user/src/alpha", State: protocol.StateDone,
			Label: "alpha-tests", Summary: "ran 14 tests",
			Changed: 1699999970, Seen: 1699999700, Activity: 1699999970},
		{ID: "%3", Session: "s3", Cwd: "/home/user/src/gamma", State: protocol.StateDone,
			Label: "gamma-notes", Summary: "wrote notes",
			Changed: 1699100000, Seen: 1699100000, Activity: 1699100000},
	}
}

func quietSource() ([]protocol.Pane, error) {
	return quietPanes(), nil
}

// assertQuietRow fails unless the cursor is on the Quiet row and the frame shows it collapsed.
func assertQuietRow(t *testing.T, m model) {
	t.Helper()
	if _, ok := m.current(); ok {
		t.Fatal("cursor is on a pane, want the Quiet row")
	}
	lines := strings.Split(m.View().Content, "\n")
	if !slices.ContainsFunc(lines, func(line string) bool {
		return strings.TrimRight(ansi.Strip(line), " ") == "> Quiet (1)"
	}) {
		t.Fatalf("no collapsed Quiet row under the cursor:\n%s", m.View().Content)
	}
}

func TestViewQuiet(t *testing.T) {
	captures := map[string]string{"%1": "Approve the migration? [y/n]\n"}
	got := renderPanes(t, quietPanes(), captures)
	if strings.Contains(got, "gamma-notes") {
		t.Fatalf("quiet pane rendered while Quiet is collapsed:\n%s", got)
	}
	checkGolden(t, "TestViewQuiet", got)
}

// rowLine returns the frame line whose text, without escapes, contains label. It fails when no line does.
func rowLine(t *testing.T, content, label string) string {
	t.Helper()
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(ansi.Strip(line), label) {
			return line
		}
	}
	t.Fatalf("no row contains %q:\n%s", label, content)
	return ""
}

func TestTreeRowsUsePaletteColours(t *testing.T) {
	var panes []protocol.Pane
	if err := json.Unmarshal([]byte(treePanes), &panes); err != nil {
		t.Fatalf("decode panes: %v", err)
	}
	content := renderPanes(t, panes, map[string]string{})
	cases := []struct {
		label string
		want  []string
	}{
		{"alpha-review", []string{palette.Fg(palette.Peach, "waiting")}},
		{"alpha-tests", []string{
			palette.Fg(palette.Mauve, "*"),
			palette.Fg(palette.Green, "done"),
			palette.Fg(palette.Overlay0, "ran 14 tests"),
		}},
		{"beta-build", []string{palette.Fg(palette.Yellow, "working"), palette.Fg(palette.Overlay0, "compiling")}},
	}
	for _, tc := range cases {
		line := rowLine(t, content, tc.label)
		for _, want := range tc.want {
			if !strings.Contains(line, want) {
				t.Errorf("row %q lacks %q:\n%q", tc.label, want, line)
			}
		}
	}
}

func TestCursorRowFillsWidthAndKeepsPrefix(t *testing.T) {
	var panes []protocol.Pane
	if err := json.Unmarshal([]byte(treePanes), &panes); err != nil {
		t.Fatalf("decode panes: %v", err)
	}
	line := rowLine(t, renderPanes(t, panes, map[string]string{}), "alpha-review")
	if w := ansi.StringWidth(line); w != treeWidth {
		t.Fatalf("cursor row width = %d, want %d: %q", w, treeWidth, line)
	}
	if !strings.HasPrefix(ansi.Strip(line), "> ") {
		t.Fatalf("cursor row lost its prefix: %q", ansi.Strip(line))
	}
	open, _, _ := strings.Cut(palette.Bg(palette.Surface0, "|"), "|")
	if !strings.HasPrefix(line, open+"> ") {
		t.Fatalf("cursor row is not on the Surface0 bar: %q", line)
	}
}

func TestQuietCursorRowFillsWidth(t *testing.T) {
	m := pressAll(t, loadedModel(t, quietSource), "n", "j")
	assertQuietRow(t, m)
	line := rowLine(t, m.View().Content, "Quiet (1)")
	if w := ansi.StringWidth(line); w != treeWidth {
		t.Fatalf("Quiet cursor row width = %d, want %d: %q", w, treeWidth, line)
	}
	if !strings.HasPrefix(ansi.Strip(line), "> Quiet (1)") {
		t.Fatalf("Quiet cursor row lost its prefix: %q", ansi.Strip(line))
	}
}

func TestTruncatedCursorRowEndsInReset(t *testing.T) {
	var panes []protocol.Pane
	if err := json.Unmarshal([]byte(treePanes), &panes); err != nil {
		t.Fatalf("decode panes: %v", err)
	}
	const narrow = 30
	m := newModel(
		func() ([]protocol.Pane, error) { return panes, nil },
		func(string) (string, error) { return "", nil },
		func() time.Time { return time.Unix(testNowUnix, 0) },
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: narrow, Height: treeHeight})
	next = settle(t, next, m.loadPanes())
	line := rowLine(t, next.View().Content, "alpha-review")
	if w := ansi.StringWidth(line); w != narrow {
		t.Fatalf("truncated row width = %d, want %d: %q", w, narrow, line)
	}
	if !strings.HasSuffix(line, "\x1b[49m") {
		t.Fatalf("truncated cursor row does not end in a background reset: %q", line)
	}
}

func TestQuietRowIsReachedByJAndSkippedByN(t *testing.T) {
	m := pressAll(t, loadedModel(t, quietSource), "n")
	assertCursor(t, m, "%2")
	m = pressAll(t, m, "j")
	assertQuietRow(t, m)
	m = press(t, m, "n")
	assertCursor(t, m, "%2")
}

func TestEnterOnQuietRowExpandsAndMovesToFirstQuietPane(t *testing.T) {
	m := pressAll(t, loadedModel(t, quietSource), "j", "j")
	assertQuietRow(t, m)
	m = press(t, m, "enter")
	assertCursor(t, m, "%3")
	content := m.View().Content
	lines := strings.Split(content, "\n")
	if !slices.Contains(lines, "  Quiet") {
		t.Fatalf("no expanded Quiet header after Enter:\n%s", content)
	}
	if !slices.Contains(lines, "/home/user/src/gamma") || !strings.Contains(content, "gamma-notes") {
		t.Fatalf("quiet pane not shown after Enter:\n%s", content)
	}
}

func TestQuietSectionStaysExpandedAcrossReload(t *testing.T) {
	m := pressAll(t, loadedModel(t, quietSource), "j", "j", "enter")
	m = modelOf(t, settle(t, m, m.loadPanes()))
	assertCursor(t, m, "%3")
	content := m.View().Content
	if !slices.Contains(strings.Split(content, "\n"), "  Quiet") || !strings.Contains(content, "gamma-notes") {
		t.Fatalf("quiet section collapsed after reload:\n%s", content)
	}
}

func TestPaneActionsDoNothingOnQuietRow(t *testing.T) {
	m := pressAll(t, loadedModel(t, quietSource), "j", "j")
	before := m.View().Content
	for _, name := range []string{"x", "c", "s", "N"} {
		next, cmd := m.Update(keyMsg(name))
		if cmd != nil {
			t.Fatalf("key %q on the Quiet row returned a command, want none", name)
		}
		if got := modelOf(t, next).View().Content; got != before {
			t.Fatalf("frame changed after %q on the Quiet row:\n%s\nwant:\n%s", name, got, before)
		}
	}
}

func TestFilterAppliesToQuietGroups(t *testing.T) {
	m := pressAll(t, loadedModel(t, quietSource), "/")
	m = press(t, typeText(t, m, "notes"), "enter")
	assertQuietRow(t, m)

	m = pressAll(t, loadedModel(t, quietSource), "/")
	m = press(t, typeText(t, m, "alpha"), "enter")
	if slices.ContainsFunc(strings.Split(m.View().Content, "\n"), func(line string) bool {
		return strings.Contains(line, "Quiet")
	}) {
		t.Fatalf("Quiet row shown with no quiet group matching the filter:\n%s", m.View().Content)
	}
}
