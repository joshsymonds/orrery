package server_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
)

// shutdownReplayCount is more events than one page holds (eventPageSize is 100), so the replaying stream is
// mid-replay across a page boundary when the signal closes. The store holds shutdownReplayCount+1 events: the
// session_added event of seedBigEvents' row, then shutdownReplayCount session_changed events.
const shutdownReplayCount = 150

// shutdownReplayTotal is the number of events a replay from seq 0 delivers when nothing ends it early.
const shutdownReplayTotal = shutdownReplayCount + 1

// shutdownMessage is the fixed message every stream ends with at shutdown.
const shutdownMessage = "serve is shutting down"

// newShutdownHandler returns the guarded handler for the store at path, with shutdown as its shutdown signal and
// sendTimeout as its Watch send deadline, or zero for the default.
func newShutdownHandler(t *testing.T, path string, shutdown <-chan struct{}, sendTimeout time.Duration) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:           []string{allowedLogin},
		Tmux:             newTmuxClient(t),
		Push:             newPushStore(t),
		Sessions:         store.NewReader(path),
		EventPoll:        eventPoll,
		EventSendTimeout: sendTimeout,
		Shutdown:         shutdown,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// startShutdownServer serves the guarded handler for the store at path with shutdown as its shutdown signal, on an
// httptest server that closes at cleanup. It returns the server, so a test can wait for its handlers to return.
func startShutdownServer(t *testing.T, path string, shutdown <-chan struct{}) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(newShutdownHandler(t, path, shutdown, 0))
	t.Cleanup(srv.Close)
	return srv
}

// startStalledShutdownServer serves the guarded handler as startShutdownServer does, but over connections whose
// write buffers are deadlineSocketBuffer bytes and with a send deadline of watchWait, so a replay blocks on the
// client's reads and no send deadline fires while the test holds the stream.
func startStalledShutdownServer(t *testing.T, path string, shutdown <-chan struct{}) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(newShutdownHandler(t, path, shutdown, watchWait))
	srv.Listener = smallBufferListener{Listener: srv.Listener}
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// wantShutdownError fails the test unless err carries Unavailable and the fixed shutdown message.
func wantShutdownError(t *testing.T, err error) {
	t.Helper()
	wantCode(t, err, connect.CodeUnavailable)
	if !strings.Contains(err.Error(), shutdownMessage) {
		t.Fatalf("stream end message: %v, want it to say %q", err, shutdownMessage)
	}
}

// wantShutdownEnd fails the test unless w ends with Unavailable and the fixed shutdown message.
func wantShutdownEnd(t *testing.T, w *watch) {
	t.Helper()
	wantShutdownError(t, drainUntilEnd(t, w))
}

// countUntilEnd receives the messages of w until its stream ends, and returns how many arrived and the error the
// stream ended with. Each receive waits up to watchWait, so a long replay that keeps sending does not time out.
func countUntilEnd(t *testing.T, w *watch) (int, error) {
	t.Helper()
	count := 0
	for {
		select {
		case _, ok := <-w.msgs:
			if !ok {
				return count, w.err
			}
			count++
		case <-time.After(watchWait):
			t.Fatalf("no message or end within %v after %d", watchWait, count)
		}
	}
}

func TestShutdownEndsEveryOpenStream(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedBigEvents(t, s, shutdownReplayCount)
	shutdown := make(chan struct{})
	srv := startShutdownServer(t, path, shutdown)
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), srv.URL)

	replaying := openWatch(t, events, 0)
	if got := replaying.next(t).GetSeq(); got != 1 {
		t.Fatalf("replayed message: seq %d, want 1", got)
	}

	caught := openWatch(t, events, shutdownReplayTotal)
	writeState(t, s, shutdownReplayTotal)
	if got := caught.next(t).GetSeq(); got != shutdownReplayTotal+1 {
		t.Fatalf("caught-up stream message: seq %d, want %d", got, shutdownReplayTotal+1)
	}

	close(shutdown)
	wantShutdownEnd(t, replaying)
	wantShutdownEnd(t, caught)

	srv.Close()
}

// TestShutdownEndsReplayBetweenSends holds a replay mid-page: the client reads one message, then stops reading, so
// the server blocks on a send with the rest of the page unsent. Closing the signal then must end the stream before
// the replay reaches its last event. Without the check before each send, the server would send the rest of the
// replay once the client drains it, and the stream would deliver every event before the end.
func TestShutdownEndsReplayBetweenSends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedBigEvents(t, s, shutdownReplayCount)
	shutdown := make(chan struct{})
	srv := startStalledShutdownServer(t, path, shutdown)
	stalledHTTP := &http.Client{Transport: headerTransport{
		base:  stalledTransport(make(chan net.Conn, deadlineConnCapacity)),
		login: allowedLogin,
	}}
	replaying := openWatch(t, orreryv1connect.NewEventsClient(stalledHTTP, srv.URL), 0)
	if got := replaying.next(t).GetSeq(); got != 1 {
		t.Fatalf("replayed message: seq %d, want 1", got)
	}

	close(shutdown)
	rest, err := countUntilEnd(t, replaying)
	wantShutdownError(t, err)
	if delivered := 1 + rest; delivered >= shutdownReplayTotal {
		t.Fatalf("replay delivered %d of %d events before the end, want the shutdown to end it mid-replay",
			delivered, shutdownReplayTotal)
	}

	srv.Close()
}

func TestNilShutdownKeepsFollowing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedEvents(t, s, 2)
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), startServer(t, path))

	w := openWatch(t, events, 3)
	writeState(t, s, 3)
	if got := w.next(t).GetSeq(); got != 4 {
		t.Fatalf("message on a nil shutdown signal: seq %d, want 4", got)
	}
	writeState(t, s, 4)
	if got := w.next(t).GetSeq(); got != 5 {
		t.Fatalf("second message on a nil shutdown signal: seq %d, want 5", got)
	}
}
