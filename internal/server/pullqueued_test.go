package server_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

const (
	// pqUp is the byte sequence tmux sends for the Up key to a pane that has not enabled application cursor keys.
	pqUp = "\x1b[A"
	// pqDel is the Backspace byte the pane receives for each rune the pull takes out of the input box.
	pqDel = "\x7f"
	// pqDim is the dim placeholder Claude Code shows in an empty input box while messages are queued.
	pqDim = "\x1b[2mPress up to edit queued messages\x1b[0m"
	// pqQuiet is how long a negative assertion waits for a stray key to reach the pane.
	pqQuiet = 200 * time.Millisecond
)

// pqRecord is one line of a transcript that the queue rules read: a queue record or a user line.
type pqRecord struct {
	Type      string `json:"type"`
	Operation string `json:"operation"`
	Content   string `json:"content"`
	Reason    string `json:"reason,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

// pqLine returns the transcript line of one queue record with operation and content, and reason when it is set.
func pqLine(t *testing.T, operation, content, reason string) string {
	t.Helper()
	raw, err := json.Marshal(pqRecord{
		Type:      "queue-operation",
		Operation: operation,
		Content:   content,
		Reason:    reason,
		Timestamp: "2026-11-11T09:00:00Z",
	})
	if err != nil {
		t.Fatalf("marshal queue record: %v", err)
	}
	return string(raw)
}

// pqAppend appends lines, each newline-terminated, to transcript.
func pqAppend(t *testing.T, transcript string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

// pqPane starts an input pane whose box shows box, marks it working, and points its transcript option at transcript.
// It returns the pane ID and the file the pane records its input in.
func pqPane(t *testing.T, tmux *tmuxctl.Client, session, box, transcript string) (string, string) {
	t.Helper()
	rule := strings.Repeat("─", typingWidth)
	pane, file := startInputPane(t, tmux, session, rule+"\n❯ "+box+"\n"+rule)
	for name, value := range liveOptions(transcript) {
		if err := tmux.SetPaneOption(t.Context(), pane, name, value); err != nil {
			t.Fatalf("SetPaneOption(%s, %s): %v", pane, name, err)
		}
	}
	return pane, file
}

// pqAwait polls file until ok reports true for its content, and returns that content. It fails after screenWait.
func pqAwait(t *testing.T, file string, ok func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(screenWait)
	for {
		data, err := os.ReadFile(file)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("read input: %v", err)
		}
		if ok(string(data)) {
			return string(data)
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane input = %q, never reached the awaited state", data)
		}
		time.Sleep(screenPoll)
	}
}

// pqAssertQuiet requires that no key reached the pane in file during pqQuiet.
func pqAssertQuiet(t *testing.T, file string) {
	t.Helper()
	time.Sleep(pqQuiet)
	data, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("read input: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("pane received %q, want no key", data)
	}
}

// pqCall runs Panes.PullQueued for pane in a goroutine, and returns the channel its result arrives on.
func pqCall(t *testing.T, client orreryv1connect.PanesClient, pane string) <-chan pqResult {
	t.Helper()
	ch := make(chan pqResult, 1)
	go func() {
		resp, err := client.PullQueued(t.Context(), connect.NewRequest(&orreryv1.PullQueuedRequest{Pane: pane}))
		if err != nil {
			ch <- pqResult{err: err}
			return
		}
		ch <- pqResult{text: resp.Msg.GetText()}
	}()
	return ch
}

// pqResult is what a Panes.PullQueued call answered.
type pqResult struct {
	text string
	err  error
}

// pqWait returns the result of the call on ch, failing the test when it does not arrive within screenWait.
func pqWait(t *testing.T, ch <-chan pqResult) pqResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(screenWait):
		t.Fatalf("Panes.PullQueued did not return")
		return pqResult{}
	}
}

func TestPanesPullQueuedTakesQueuedMessages(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	transcript := writeNewestTranscript(t,
		pqLine(t, "enqueue", "alpha line", ""),
		pqLine(t, "enqueue", "beta line", ""),
	)
	pane, file := pqPane(t, tmux, "s1", pqDim, transcript)
	client := panesClient(t, newHandler(t, tmux))

	ch := pqCall(t, client, pane)
	pqAwait(t, file, func(s string) bool { return strings.Contains(s, pqUp) })
	pqAppend(t, transcript, pqLine(t, "popAll", "alpha line", ""), pqLine(t, "popAll", "beta line", ""))
	got := pqWait(t, ch)
	if got.err != nil {
		t.Fatalf("Panes.PullQueued: %v", got.err)
	}
	const want = "alpha line\nbeta line"
	if got.text != want {
		t.Fatalf("Panes.PullQueued text = %q, want %q", got.text, want)
	}
	awaitInput(t, file, pqUp+strings.Repeat(pqDel, len(want)))
}

func TestPanesPullQueuedHoldsTextLock(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	transcript := writeNewestTranscript(t, pqLine(t, "enqueue", "alpha", ""))
	pane, file := pqPane(t, tmux, "s1", pqDim, transcript)
	client := panesClient(t, newHandler(t, tmux))

	pulled := pqCall(t, client, pane)
	pqAwait(t, file, func(s string) bool { return strings.Contains(s, pqUp) })

	// The send starts while the pull holds the pane's text lock, so it must wait until the pull has cleared the box.
	sent := make(chan error, 1)
	go func() {
		_, err := client.SendText(t.Context(),
			connect.NewRequest(&orreryv1.SendTextRequest{Pane: pane, Text: "zq"}))
		sent <- err
	}()
	pqAppend(t, transcript, pqLine(t, "popAll", "alpha", ""))

	if got := pqWait(t, pulled); got.err != nil {
		t.Fatalf("Panes.PullQueued: %v", got.err)
	}
	select {
	case err := <-sent:
		if err != nil {
			t.Fatalf("Panes.SendText: %v", err)
		}
	case <-time.After(screenWait):
		t.Fatalf("Panes.SendText did not return")
	}
	got := pqAwait(t, file, func(s string) bool {
		return strings.Contains(s, "zq") && strings.Count(s, pqDel) == len("alpha")
	})
	if strings.Index(got, "zq") < strings.LastIndex(got, pqDel) {
		t.Fatalf("pane received %q: the send's text arrived before the pull's Backspaces", got)
	}
}

func TestPanesPullQueuedFailedClearAnswersAborted(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	transcript := writeNewestTranscript(t, pqLine(t, "enqueue", "alpha line", ""))
	// A second session keeps the tmux server up after the pane under test is killed.
	startSession(t, tmux, "keep", longCommand)
	pane, file := pqPane(t, tmux, "s1", pqDim, transcript)
	client := panesClient(t, newHandler(t, tmux))

	ch := pqCall(t, client, pane)
	pqAwait(t, file, func(s string) bool { return strings.Contains(s, pqUp) })
	pqAppend(t, transcript, pqLine(t, "popAll", "alpha line", ""))
	// The queue has given up the message and the pane is gone, so the Backspaces that empty the box fail after the
	// text was taken. The call must answer aborted, because the box holds that text, and never internal.
	if _, err := tmux.Run(t.Context(), "kill-pane", "-t", pane); err != nil {
		t.Fatalf("kill-pane %s: %v", pane, err)
	}
	got := pqWait(t, ch)
	var connectErr *connect.Error
	if !errors.As(got.err, &connectErr) {
		t.Fatalf("Panes.PullQueued error = %v, want a *connect.Error", got.err)
	}
	if connectErr.Code() != connect.CodeAborted {
		t.Fatalf("Panes.PullQueued code = %v, want %v", connectErr.Code(), connect.CodeAborted)
	}
	if connectErr.Message() != "input not empty" {
		t.Fatalf("Panes.PullQueued message = %q, want %q", connectErr.Message(), "input not empty")
	}
}

func TestPanesPullQueuedRefusesWithoutTouchingPane(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	noneQueued := writeNewestTranscript(t)
	canceledOnly := writeNewestTranscript(t,
		pqLine(t, "enqueue", "dropped", ""),
		pqLine(t, "remove", "dropped", "cleared_on_cancel"),
	)
	queued := writeNewestTranscript(t, pqLine(t, "enqueue", "waiting", ""))
	nothingPane, nothingFile := pqPane(t, tmux, "nothing", pqDim, noneQueued)
	canceledPane, canceledFile := pqPane(t, tmux, "canceled", pqDim, canceledOnly)
	busyPane, busyFile := pqPane(t, tmux, "busy", "typed words", queued)
	client := panesClient(t, newHandler(t, tmux))

	cases := []struct {
		name    string
		pane    string
		file    string
		code    connect.Code
		message string
	}{
		{"nothing queued", nothingPane, nothingFile, connect.CodeFailedPrecondition, "nothing queued"},
		{"only canceled messages", canceledPane, canceledFile, connect.CodeFailedPrecondition, "nothing queued"},
		{"input not empty", busyPane, busyFile, connect.CodeAborted, "input not empty"},
	}
	for _, c := range cases {
		_, err := client.PullQueued(t.Context(), connect.NewRequest(&orreryv1.PullQueuedRequest{Pane: c.pane}))
		var connectErr *connect.Error
		if !errors.As(err, &connectErr) {
			t.Fatalf("%s: Panes.PullQueued error = %v, want a *connect.Error", c.name, err)
		}
		if connectErr.Code() != c.code {
			t.Fatalf("%s: Panes.PullQueued code = %v, want %v", c.name, connectErr.Code(), c.code)
		}
		if connectErr.Message() != c.message {
			t.Fatalf("%s: Panes.PullQueued message = %q, want %q", c.name, connectErr.Message(), c.message)
		}
		pqAssertQuiet(t, c.file)
	}
}

func TestPanesPullQueuedErrorParity(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	transcript := writeNewestTranscript(t, pqLine(t, "enqueue", "waiting", ""))
	startSession(t, tmux, "unlive", longCommand)
	unlive := paneID(t, tmux, "unlive")
	if err := tmux.SetPaneOption(t.Context(), unlive, protocol.OptionTranscript, transcript); err != nil {
		t.Fatalf("SetPaneOption(%s): %v", unlive, err)
	}
	startSession(t, tmux, "untranscribed", longCommand)
	untranscribed := paneID(t, tmux, "untranscribed")
	if err := tmux.SetPaneOption(t.Context(), untranscribed, protocol.OptionState,
		string(protocol.StateWorking)); err != nil {
		t.Fatalf("SetPaneOption(%s): %v", untranscribed, err)
	}
	client := panesClient(t, newHandler(t, tmux))

	cases := []struct {
		name    string
		pane    string
		code    connect.Code
		message string
	}{
		{"malformed pane id", "abc", connect.CodeInvalidArgument, "malformed pane id"},
		{"absent pane", "%999999", connect.CodeNotFound, "pane not found"},
		{"no live session", unlive, connect.CodeNotFound, "no live session"},
		{"no transcript", untranscribed, connect.CodeNotFound, "no transcript"},
	}
	for _, c := range cases {
		_, err := client.PullQueued(t.Context(), connect.NewRequest(&orreryv1.PullQueuedRequest{Pane: c.pane}))
		var connectErr *connect.Error
		if !errors.As(err, &connectErr) {
			t.Fatalf("%s: Panes.PullQueued error = %v, want a *connect.Error", c.name, err)
		}
		if connectErr.Code() != c.code {
			t.Fatalf("%s: Panes.PullQueued code = %v, want %v", c.name, connectErr.Code(), c.code)
		}
		if connectErr.Message() != c.message {
			t.Fatalf("%s: Panes.PullQueued message = %q, want %q", c.name, connectErr.Message(), c.message)
		}
	}
}
