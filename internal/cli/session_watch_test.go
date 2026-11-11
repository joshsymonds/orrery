package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

const (
	// watchTestID is the engine session the watch tests write.
	watchTestID = "watch-1"
	// watchTestAt is the unix second every watch test event is stored at.
	watchTestAt = 1_700_000_000
	// watchTestPoll is the poll interval the watch tests inject.
	watchTestPoll = 5 * time.Millisecond
	// watchTestWait bounds every wait, so a stalled watch fails its test instead of hanging the run.
	watchTestWait = 10 * time.Second
)

// watchOutput collects what watch writes. Each write signals wrote without blocking, so a test waits for output
// without sleeping.
type watchOutput struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	wrote chan struct{}
}

func newWatchOutput() *watchOutput {
	return &watchOutput{wrote: make(chan struct{}, 1)}
}

// Write appends p to the output and signals wrote.
func (o *watchOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	n, err := o.buf.Write(p)
	o.mu.Unlock()
	select {
	case o.wrote <- struct{}{}:
	default:
	}
	return n, err
}

// lines returns the lines written so far, without their newlines.
func (o *watchOutput) lines() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	text := strings.TrimSuffix(o.buf.String(), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// watchCall is one watch running on its own goroutine.
type watchCall struct {
	out  *watchOutput
	done chan error
}

// startWatch runs watch on its own goroutine, writing to a new watchOutput.
func startWatch(watch func(out io.Writer) error) *watchCall {
	call := &watchCall{out: newWatchOutput(), done: make(chan error, 1)}
	go func() { call.done <- watch(call.out) }()
	return call
}

// await waits for the watch to return and reports what it returned.
func (c *watchCall) await(t *testing.T) error {
	t.Helper()
	select {
	case err := <-c.done:
		return err
	case <-time.After(watchTestWait):
		t.Fatalf("watch did not return within %v, with %d lines", watchTestWait, len(c.out.lines()))
		return nil
	}
}

// awaitLines waits until the watch has written want lines. It fails when the watch returns first.
func (c *watchCall) awaitLines(t *testing.T, want int) {
	t.Helper()
	deadline := time.After(watchTestWait)
	for len(c.out.lines()) < want {
		select {
		case <-c.out.wrote:
		case err := <-c.done:
			t.Fatalf("watch returned %v with %d lines, want %d", err, len(c.out.lines()), want)
		case <-deadline:
			t.Fatalf("waited %v for %d lines, have %d", watchTestWait, len(c.out.lines()), want)
		}
	}
}

// watchStore opens the store in a new state directory and returns it with the getenv that points at it.
func watchStore(t *testing.T) (*store.Store, func(string) string) {
	t.Helper()
	stateHome := t.TempDir()
	getenv := func(key string) string {
		if key == "XDG_STATE_HOME" {
			return stateHome
		}
		return ""
	}
	s, err := store.Open(t.Context(), watchPath(t, getenv))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close store: %v", err)
		}
	})
	return s, getenv
}

// watchPath returns the store file getenv resolves to.
func watchPath(t *testing.T, getenv func(string) string) string {
	t.Helper()
	path, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store.Path: %v", err)
	}
	return path
}

// writeWatchOps applies ops to the store as one batch, stamped at watchTestAt.
func writeWatchOps(t *testing.T, s *store.Store, ops ...store.EngineOp) {
	t.Helper()
	if err := s.ApplyEngine(t.Context(), ops, time.Unix(watchTestAt, 0)); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
}

// watchAdd is the op that starts the watch test session in state working.
func watchAdd() store.EngineOp {
	return store.EngineAdd{ID: watchTestID, Dir: "/work", State: "working"}
}

// watchStopped is the op that stops the watch test session.
func watchStopped() store.EngineOp {
	state := "stopped"
	return store.EngineSet{ID: watchTestID, Changes: store.EngineChanges{State: &state}}
}

// watchLine is one output line of watch.
type watchLine struct {
	Seq     int64           `json:"seq"`
	Kind    string          `json:"kind"`
	At      int64           `json:"at"`
	Payload json.RawMessage `json:"payload"`
}

// wantWatchLine is the kind and payload a watch line must carry.
type wantWatchLine struct {
	kind    string
	payload string
}

// assertWatchLines checks that each line is a JSON object with exactly seq, kind, at and payload, that the lines
// match want in order, and that their seqs increase.
func assertWatchLines(t *testing.T, lines []string, want []wantWatchLine) {
	t.Helper()
	if len(lines) != len(want) {
		t.Fatalf("lines: got %d, want %d\n got: %q", len(lines), len(want), lines)
	}
	var prev int64
	for i, line := range lines {
		var keys map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &keys); err != nil {
			t.Fatalf("line %d %q is not a JSON object: %v", i, line, err)
		}
		if len(keys) != 4 {
			t.Fatalf("line %d has %d keys, want seq, kind, at and payload: %q", i, len(keys), line)
		}
		var got watchLine
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("line %d %q: %v", i, line, err)
		}
		if got.Seq <= prev {
			t.Fatalf("line %d seq %d is not after seq %d", i, got.Seq, prev)
		}
		prev = got.Seq
		if got.Kind != want[i].kind || got.At != watchTestAt {
			t.Fatalf("line %d: got kind %s at %d, want kind %s at %d", i, got.Kind, got.At, want[i].kind, watchTestAt)
		}
		var gotPayload, wantPayload any
		if err := json.Unmarshal(got.Payload, &gotPayload); err != nil {
			t.Fatalf("line %d payload %s: %v", i, got.Payload, err)
		}
		if err := json.Unmarshal([]byte(want[i].payload), &wantPayload); err != nil {
			t.Fatalf("test payload %q: %v", want[i].payload, err)
		}
		if !reflect.DeepEqual(gotPayload, wantPayload) {
			t.Fatalf("line %d payload: got %v, want %v", i, gotPayload, wantPayload)
		}
	}
}

func TestSessionWatchPrintsStoppedSessionInSeqOrder(t *testing.T) {
	s, getenv := watchStore(t)
	writeWatchOps(t, s,
		watchAdd(),
		store.EnginePrompt{ID: watchTestID, Text: "hi"},
		watchStopped(),
	)

	call := startWatch(func(out io.Writer) error {
		return SessionWatch(t.Context(), []string{watchTestID}, out, getenv)
	})
	if err := call.await(t); err != nil {
		t.Fatalf("SessionWatch on a stopped session: %v", err)
	}
	assertWatchLines(t, call.out.lines(), []wantWatchLine{
		{kind: store.EventEngineAdded, payload: `{"dir":"/work","state":"working"}`},
		{kind: store.EventEnginePrompt, payload: `{"text":"hi"}`},
		{kind: store.EventEngineChanged, payload: `{"fields":{"changed":"1700000000","state":"stopped"}}`},
	})
}

func TestSessionWatchFollowsWorkingSessionUntilStopped(t *testing.T) {
	s, getenv := watchStore(t)
	path := watchPath(t, getenv)
	writeWatchOps(t, s, watchAdd(), store.EnginePrompt{ID: watchTestID, Text: "one"})

	call := startWatch(func(out io.Writer) error {
		return sessionWatch(t.Context(), store.NewReader(path), watchTestID, out, watchTestPoll)
	})
	call.awaitLines(t, 2)

	writeWatchOps(t, s, store.EnginePrompt{ID: watchTestID, Text: "two"})
	call.awaitLines(t, 3)

	writeWatchOps(t, s, watchStopped())
	if err := call.await(t); err != nil {
		t.Fatalf("sessionWatch after stop: %v", err)
	}
	assertWatchLines(t, call.out.lines(), []wantWatchLine{
		{kind: store.EventEngineAdded, payload: `{"dir":"/work","state":"working"}`},
		{kind: store.EventEnginePrompt, payload: `{"text":"one"}`},
		{kind: store.EventEnginePrompt, payload: `{"text":"two"}`},
		{kind: store.EventEngineChanged, payload: `{"fields":{"changed":"1700000000","state":"stopped"}}`},
	})
}

func TestSessionWatchEndsWhenContextIsCancelled(t *testing.T) {
	s, getenv := watchStore(t)
	writeWatchOps(t, s, watchAdd())

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	call := startWatch(func(out io.Writer) error {
		return SessionWatch(ctx, []string{watchTestID}, out, getenv)
	})
	call.awaitLines(t, 1)
	cancel()
	if err := call.await(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("SessionWatch after cancel: got %v, want context.Canceled", err)
	}
}

func TestSessionWatchRejectsUnknownSession(t *testing.T) {
	_, getenv := watchStore(t)

	call := startWatch(func(out io.Writer) error {
		return SessionWatch(t.Context(), []string{"no-such-session"}, out, getenv)
	})
	err := call.await(t)
	if !errors.Is(err, store.ErrNoEngineSession) {
		t.Fatalf("SessionWatch on an unknown id: got %v, want ErrNoEngineSession", err)
	}
	if !strings.Contains(err.Error(), "no-such-session") {
		t.Fatalf("error %q does not name the session id", err)
	}
	if got := call.out.lines(); len(got) != 0 {
		t.Fatalf("SessionWatch on an unknown id wrote %q", got)
	}
}

func TestSessionWatchRejectsWrongArgumentCount(t *testing.T) {
	_, getenv := watchStore(t)
	cases := []struct {
		name string
		args []string
	}{
		{name: "no id", args: nil},
		{name: "two ids", args: []string{watchTestID, "other"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := SessionWatch(t.Context(), tc.args, &out, getenv)
			if !errors.Is(err, ErrUsage) {
				t.Fatalf("SessionWatch(%q): got %v, want ErrUsage", tc.args, err)
			}
			if out.Len() != 0 {
				t.Fatalf("SessionWatch(%q) wrote %q", tc.args, out.String())
			}
		})
	}
}
