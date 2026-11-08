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
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
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
		if _, err := client.Run(context.Background(), "kill-server"); err != nil {
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
