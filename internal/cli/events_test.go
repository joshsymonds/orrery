package cli_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/joshsymonds/orrery/internal/cli"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/rpcretry"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

const (
	// eventsLogin is the login the test serve admits. The test sets it on every request, playing tailscale serve.
	eventsLogin = "events-test-login"
	// eventsWatchPath is the Connect path of Events.Watch.
	eventsWatchPath = "/" + orreryv1connect.EventsName + "/Watch"
	// eventsAt is the unix second every test event is stored at.
	eventsAt = 1_700_000_000
	// eventsPoll is the Watch poll interval the tests inject.
	eventsPoll = 10 * time.Millisecond
	// eventsWait bounds every wait, so a stalled command fails its test instead of hanging the run.
	eventsWait = 10 * time.Second
	// eventsServer is the synthetic tmux server name the store rows belong to.
	eventsServer = "-L events"
)

// eventsOutput collects what the command writes. Each write signals wrote without blocking, so a test waits for
// output without sleeping.
type eventsOutput struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	wrote chan struct{}
}

// Write appends p to the output and signals wrote.
func (o *eventsOutput) Write(p []byte) (int, error) {
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
func (o *eventsOutput) lines() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	text := strings.TrimSuffix(o.buf.String(), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// eventsCall is one Events call running on its own goroutine.
type eventsCall struct {
	out  *eventsOutput
	done chan struct{}
	err  error
}

// startEvents runs cli.Events with args on its own goroutine, writing to a new eventsOutput.
func startEvents(t *testing.T, args ...string) *eventsCall {
	t.Helper()
	call := &eventsCall{out: &eventsOutput{wrote: make(chan struct{}, 1)}, done: make(chan struct{})}
	go func() {
		call.err = cli.Events(t.Context(), args, call.out)
		close(call.done)
	}()
	return call
}

// startEventsOnClock is startEvents with the retry clock moving forward by step on every wait.
func startEventsOnClock(t *testing.T, step time.Duration, args ...string) *eventsCall {
	t.Helper()
	call := &eventsCall{out: &eventsOutput{wrote: make(chan struct{}, 1)}, done: make(chan struct{})}
	go func() {
		call.err = cli.EventsOnSteppedClock(t.Context(), step, args, call.out)
		close(call.done)
	}()
	return call
}

// wait returns the error the call returned, and fails the test when the call runs past eventsWait.
func (c *eventsCall) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-c.done:
		return c.err
	case <-time.After(eventsWait):
		t.Fatalf("Events did not return within %v; wrote %d lines", eventsWait, len(c.out.lines()))
		return nil
	}
}

// waitLines waits until the call has written want lines. It fails the test when the call returns first or the wait
// runs past eventsWait.
func (c *eventsCall) waitLines(t *testing.T, want int) {
	t.Helper()
	deadline := time.After(eventsWait)
	for len(c.out.lines()) < want {
		select {
		case <-c.out.wrote:
		case <-c.done:
			t.Fatalf("Events returned with %d lines, want %d: %v", len(c.out.lines()), want, c.err)
		case <-deadline:
			t.Fatalf("waited %v for %d lines, have %d", eventsWait, want, len(c.out.lines()))
		}
	}
}

// openEventsStore opens the writer for the store at path and closes it at cleanup.
func openEventsStore(t *testing.T, path string) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close store: %v", err)
		}
	})
	return s
}

// seedEvents writes three events: session_added for orrery-1 and orrery-2 (seqs 1 and 2), then a state change of
// orrery-1 (seq 3).
func seedEvents(t *testing.T, s *store.Store) {
	t.Helper()
	panes := []store.Pane{
		{ID: "orrery-1", Pane: "%1"},
		{ID: "orrery-2", Pane: "%2"},
	}
	if err := s.SyncServer(t.Context(), eventsServer, panes, time.Unix(eventsAt, 0)); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
	writeEventsState(t, s, "state-1")
}

// writeEventsState sets the state of orrery-1 to state, which appends one session_changed event when it differs.
func writeEventsState(t *testing.T, s *store.Store, state string) {
	t.Helper()
	pane := store.Pane{ID: "orrery-1", Pane: "%1", Fields: store.Fields{State: state}}
	if err := s.SyncPane(t.Context(), eventsServer, pane, time.Unix(eventsAt, 0)); err != nil {
		t.Fatalf("SyncPane %s: %v", state, err)
	}
}

// eventsStored returns every event the store at path holds, in seq order.
func eventsStored(t *testing.T, path string) []store.Event {
	t.Helper()
	page, err := store.NewReader(path).Events(t.Context(), 0, 1000)
	if err != nil {
		t.Fatalf("read stored events: %v", err)
	}
	return page.Events
}

// serveEvents serves the guarded handler for the store at path on an httptest server, and returns its URL. The
// handler plays tailscale serve by setting the login header, and it sends on watched each Watch request it passes
// on. Closing the returned stop function ends every open stream, so the server never waits on one at cleanup.
func serveEvents(t *testing.T, path string, watched chan<- struct{}) (string, func()) {
	t.Helper()
	tmux, err := tmuxctl.New(tmuxctl.Server{Name: "orrery-events-test"})
	if err != nil {
		t.Fatalf("tmuxctl.New: %v", err)
	}
	pushStore, err := push.NewStore(filepath.Join(t.TempDir(), "push.json"), []string{push.DefaultPushHost})
	if err != nil {
		t.Fatalf("push.NewStore: %v", err)
	}
	shutdown := make(chan struct{})
	handler, err := server.New(server.Config{
		Logins:    []string{eventsLogin},
		Tmux:      tmux,
		Push:      pushStore,
		Sessions:  store.NewReader(path),
		EventPoll: eventsPoll,
		Shutdown:  shutdown,
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	serve := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set(server.HeaderLogin, eventsLogin)
		if r.URL.Path == eventsWatchPath {
			select {
			case watched <- struct{}{}:
			default:
			}
		}
		handler.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(serve)
	t.Cleanup(srv.Close)
	stop := sync.OnceFunc(func() { close(shutdown) })
	t.Cleanup(stop)
	return srv.URL, stop
}

// checkEventLine decodes line as a WatchResponse and checks it against the stored event want.
func checkEventLine(t *testing.T, line string, want store.Event) {
	t.Helper()
	var msg orreryv1.WatchResponse
	if err := protojson.Unmarshal([]byte(line), &msg); err != nil {
		t.Fatalf("decode line %q: %v", line, err)
	}
	if msg.GetSeq() != want.Seq || msg.GetSessionId() != want.SessionID || msg.GetAt() != want.At {
		t.Fatalf("line %q: seq %d session %q at %d, want seq %d session %q at %d",
			line, msg.GetSeq(), msg.GetSessionId(), msg.GetAt(), want.Seq, want.SessionID, want.At)
	}
	switch want.Kind {
	case store.EventSessionAdded:
		added := msg.GetSessionAdded()
		if added == nil || added.GetServer() != want.Server || added.GetPane() != want.Pane {
			t.Fatalf("line %q: session_added %+v, want server %q pane %q", line, added, want.Server, want.Pane)
		}
	case store.EventSessionChanged:
		changed := msg.GetSessionChanged()
		if changed == nil || changed.GetState() != *want.Changes.State {
			t.Fatalf("line %q: session_changed %+v, want state %q", line, changed, *want.Changes.State)
		}
	default:
		t.Fatalf("stored event %d has unexpected kind %q", want.Seq, want.Kind)
	}
}

func TestEventsAfterWithoutFollowWritesThroughNewest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedEvents(t, openEventsStore(t, path))
	stored := eventsStored(t, path)
	if len(stored) != 3 {
		t.Fatalf("store holds %d events, want 3", len(stored))
	}
	url, _ := serveEvents(t, path, nil)

	call := startEvents(t, "--url", url, "--after", "1")
	if err := call.wait(t); err != nil {
		t.Fatalf("Events: %v", err)
	}
	lines := call.out.lines()
	if len(lines) != 2 {
		t.Fatalf("Events wrote %d lines, want 2 (seqs 2 and 3): %q", len(lines), lines)
	}
	checkEventLine(t, lines[0], stored[1])
	checkEventLine(t, lines[1], stored[2])
}

func TestEventsWithoutAfterOrFollowWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedEvents(t, openEventsStore(t, path))
	url, _ := serveEvents(t, path, nil)

	// A caught-up Watch does not return until the next event, so this call returns only if it opens no stream.
	call := startEvents(t, "--url", url)
	if err := call.wait(t); err != nil {
		t.Fatalf("Events: %v", err)
	}
	if lines := call.out.lines(); len(lines) != 0 {
		t.Fatalf("Events wrote %q, want nothing", lines)
	}
}

func TestEventsFollowWithoutAfterWritesOnlyEventsAfterList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventsStore(t, path)
	seedEvents(t, s)
	watched := make(chan struct{}, 1)
	url, stop := serveEvents(t, path, watched)

	// Each retry wait moves the clock a whole window, so serve's Unavailable end is retried once and then given up.
	call := startEventsOnClock(t, rpcretry.Window, "--url", url, "--follow")
	select {
	case <-watched:
	case <-time.After(eventsWait):
		t.Fatalf("no Watch request reached the server within %v", eventsWait)
	}
	// The Watch request follows the command's List, so every event written from here on is past List's seq.
	writeEventsState(t, s, "state-2")
	writeEventsState(t, s, "state-3")
	call.waitLines(t, 2)
	stop()

	err := call.wait(t)
	if err == nil || !strings.Contains(err.Error(), "serve is shutting down") {
		t.Fatalf("Events returned %v, want an error naming the shutdown", err)
	}
	lines := call.out.lines()
	if len(lines) != 2 {
		t.Fatalf("Events wrote %d lines, want 2 (seqs 4 and 5): %q", len(lines), lines)
	}
	stored := eventsStored(t, path)
	if len(stored) != 5 {
		t.Fatalf("store holds %d events, want 5", len(stored))
	}
	checkEventLine(t, lines[0], stored[3])
	checkEventLine(t, lines[1], stored[4])
}

func TestEventsCursorPastEndIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedEvents(t, openEventsStore(t, path))
	url, _ := serveEvents(t, path, nil)

	call := startEvents(t, "--url", url, "--after", "99")
	err := call.wait(t)
	if err == nil || !strings.Contains(err.Error(), "99") {
		t.Fatalf("Events returned %v, want an error naming cursor 99", err)
	}
	if lines := call.out.lines(); len(lines) != 0 {
		t.Fatalf("Events wrote %q before the error, want nothing", lines)
	}
}

func TestEventsWithoutURLIsUsageError(t *testing.T) {
	err := cli.Events(t.Context(), nil, io.Discard)
	if !errors.Is(err, cli.ErrUsage) {
		t.Fatalf("Events without --url returned %v, want a usage error", err)
	}
}
