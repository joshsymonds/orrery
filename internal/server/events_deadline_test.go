package server_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
)

const (
	// deadlineSendTimeout is the Watch send deadline the tests inject.
	deadlineSendTimeout = 300 * time.Millisecond
	// deadlineStalledSendTimeout is the send deadline the stalled-reader tests of the session and conversation streams
	// give their servers.
	deadlineStalledSendTimeout = 4 * time.Second
	// deadlineSocketBuffer is the kernel buffer, in bytes, of the stalled client's reads and of every accepted
	// server connection's writes, so a few kilobytes of replayed events fill the path between them.
	deadlineSocketBuffer = 4096
	// deadlineSummaryBytes is the size of each replayed summary in a seeded event.
	deadlineSummaryBytes = 32 << 10
	// deadlineBigEvents is the number of replayed events with large summaries, far more than the buffers hold.
	deadlineBigEvents = 40
	// deadlineIdleFactor is how many send deadlines a stream sits idle before a new event is written.
	deadlineIdleFactor = 4
	// deadlineConnCapacity is the buffer of the channel that receives each closed connection's address.
	deadlineConnCapacity = 16
)

// smallBufferListener keeps the kernel write buffer of each accepted connection at deadlineSocketBuffer bytes.
type smallBufferListener struct {
	net.Listener
}

// Accept returns the next connection with its write buffer set small.
func (l smallBufferListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("accepted %T, want *net.TCPConn", conn)
	}
	if err := tcp.SetWriteBuffer(deadlineSocketBuffer); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("set write buffer: %w", err)
	}
	return tcp, nil
}

// startDeadlineServer serves the guarded handler for the store at path with sendTimeout injected as the send deadline
// and small connection write buffers, and returns its URL.
func startDeadlineServer(t *testing.T, path string, sendTimeout time.Duration) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &httptest.Server{
		Listener: smallBufferListener{Listener: listener},
		Config:   &http.Server{Handler: deadlineHandler(t, path, sendTimeout)},
	}
	srv.Start()
	t.Cleanup(srv.Close)
	return srv.URL
}

// deadlineHandler is the guarded handler for the store at path with sendTimeout injected as the send deadline.
func deadlineHandler(t *testing.T, path string, sendTimeout time.Duration) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:           []string{allowedLogin},
		Tmux:             newTmuxClient(t),
		Push:             newPushStore(t),
		Sessions:         store.NewReader(path),
		EventPoll:        eventPoll,
		EventSendTimeout: sendTimeout,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// stalledTransport returns a transport whose connections read through deadlineSocketBuffer bytes of kernel buffer.
// Each dialed connection is sent on dialed, so the test can name it.
func stalledTransport(dialed chan<- net.Conn) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var dialer net.Dialer
			conn, err := dialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			tcp, ok := conn.(*net.TCPConn)
			if !ok {
				_ = conn.Close()
				return nil, fmt.Errorf("dialed %T, want *net.TCPConn", conn)
			}
			if err := tcp.SetReadBuffer(deadlineSocketBuffer); err != nil {
				_ = conn.Close()
				return nil, fmt.Errorf("set read buffer: %w", err)
			}
			dialed <- tcp
			return tcp, nil
		},
	}
}

// deadlineAlphabet is the alphabet of deadlineSummary: letters and digits, so the summaries are valid text.
const deadlineAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// deadlineSummary returns the summary of event i: deadlineSummaryBytes pseudo-random letters and digits. Repeated
// text would compress to almost nothing on the wire, so the replay would never fill the socket buffers.
func deadlineSummary(i int) string {
	r := rand.New(rand.NewPCG(uint64(i), 0))
	b := make([]byte, deadlineSummaryBytes)
	for j := range b {
		b[j] = deadlineAlphabet[r.IntN(len(deadlineAlphabet))]
	}
	return fmt.Sprintf("%d:%s", i, b)
}

// seedBigEvents writes the row of eventPane, which appends one session_added event, then count session_changed
// events whose summaries are deadlineSummaryBytes long.
func seedBigEvents(t *testing.T, s *store.Store, count int) {
	t.Helper()
	rows := []store.Pane{{ID: eventPane, Pane: "%1"}}
	if err := s.SyncServer(t.Context(), "-L a", rows, time.Unix(eventAt, 0)); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
	for i := 1; i <= count; i++ {
		summary := deadlineSummary(i)
		pane := store.Pane{ID: eventPane, Pane: "%1", Fields: store.Fields{Summary: summary}}
		if err := s.SyncPane(t.Context(), "-L a", pane, time.Unix(eventAt, 0)); err != nil {
			t.Fatalf("SyncPane summary %d: %v", i, err)
		}
	}
}

// waitClosed waits until the connection with remote address addr closes. It has no deadline of its own.
func waitClosed(t *testing.T, closed <-chan string, addr string) {
	t.Helper()
	for {
		got := <-closed
		if got == addr {
			return
		}
	}
}

// assertStillOpen fails the test if the connection with remote address addr has already closed. It does not wait.
func assertStillOpen(t *testing.T, closed <-chan string, addr string) {
	t.Helper()
	select {
	case got := <-closed:
		if got == addr {
			t.Fatalf("stalled connection %s closed before the healthy stream finished", addr)
		}
	default:
	}
}

// drainUntilEnd receives and discards the messages of w until its stream ends, and returns the error it ended with.
func drainUntilEnd(t *testing.T, w *watch) error {
	t.Helper()
	for {
		_, ok := <-w.msgs
		if !ok {
			return w.err
		}
	}
}

// openStalledWatch opens a Watch from seq 0 through a client that never reads, on the server at url. It returns the
// stream, the client's connection, and the address the server's closed channel names it by.
func openStalledWatch(t *testing.T, url string) (*watch, net.Conn, string) {
	t.Helper()
	dialed := make(chan net.Conn, 1)
	stalledHTTP := &http.Client{Transport: headerTransport{base: stalledTransport(dialed), login: allowedLogin}}
	stalled := openWatch(t, orreryv1connect.NewEventsClient(stalledHTTP, url), 0)
	conn := <-dialed
	return stalled, conn, conn.LocalAddr().String()
}

// openHeldStall opens, on a server that holds the write deadlines of the connections it accepts, a Watch from seq 0
// through a client that never reads, and returns once the server's send to it is blocked. The hold stays on, so a
// connection accepted later is held as well: its deadlines are never applied, and no send to a client that reads can
// miss its deadline however slowly the machine runs. It returns the stream, the client's connection, the address the
// server's closed channel names it by, the channel, and the listener that holds it.
func openHeldStall(t *testing.T, path string) (*watch, net.Conn, string, <-chan string, *observedListener) {
	t.Helper()
	url, closed, obs := startConvDeadlineServer(t, deadlineHandler(t, path, deadlineSendTimeout))
	obs.setHold(true)
	stalled, conn, addr := openStalledWatch(t, url)
	waitBlockedSend(t, obs)
	return stalled, conn, addr, closed, obs
}

// TestStalledStreamLeavesOtherStreamsServed proves that a stream whose client never reads does not hold up another
// stream: while the stalled send is blocked, its deadline held unapplied, a healthy stream replays every event and
// receives a new one, and the stalled connection is still open after. Closing the stalled client then ends its
// connection.
func TestStalledStreamLeavesOtherStreamsServed(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedBigEvents(t, s, deadlineBigEvents)
	_, stalledConn, stalledAddr, closed, obs := openHeldStall(t, path)
	url := "http://" + obs.Addr().String()

	healthy := openWatch(t, orreryv1connect.NewEventsClient(guardedHTTPClient(), url), 0)
	for i := range deadlineBigEvents + 1 {
		if got := healthy.next(t).GetSeq(); got != int64(i+1) {
			t.Fatalf("replayed message %d: seq %d, want %d", i, got, i+1)
		}
	}
	writeState(t, s, deadlineBigEvents+1)
	if got := healthy.next(t).GetSeq(); got != deadlineBigEvents+2 {
		t.Fatalf("healthy stream message after the stall: seq %d, want %d", got, deadlineBigEvents+2)
	}
	assertStillOpen(t, closed, stalledAddr)

	if err := stalledConn.Close(); err != nil {
		t.Fatalf("close the stalled client: %v", err)
	}
	waitClosed(t, closed, stalledAddr)
}

// TestStalledStreamEndsAtSendDeadline proves that a stream whose client never reads is ended by its send deadline:
// once the server's send is blocked, the deadline it set is applied as already due, the server closes the connection,
// and the stream ends with a transport error and no end-of-stream message. No real deadline is waited out.
func TestStalledStreamEndsAtSendDeadline(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedBigEvents(t, s, deadlineBigEvents)
	stalled, _, stalledAddr, closed, obs := openHeldStall(t, path)

	if err := obs.releaseAll(); err != nil {
		t.Fatalf("release the stalled connection: %v", err)
	}
	waitClosed(t, closed, stalledAddr)
	if err := drainUntilEnd(t, stalled); err == nil {
		t.Fatal("stalled stream ended cleanly, want a transport error with no end-of-stream message")
	}
}

func TestIdleStreamReceivesEventAfterSeveralDeadlines(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedEvents(t, s, 0)
	url := startDeadlineServer(t, path, deadlineSendTimeout)
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), url)

	w := openWatch(t, events, 0)
	if got := w.next(t).GetSeq(); got != 1 {
		t.Fatalf("replayed message: seq %d, want 1", got)
	}

	// The deadline bounds each send, so a stream that sends nothing for longer than it still delivers later events.
	idle := time.After(deadlineIdleFactor * deadlineSendTimeout)
	<-idle

	writeState(t, s, 1)
	if got := w.next(t).GetSeq(); got != 2 {
		t.Fatalf("message after the idle period: seq %d, want 2", got)
	}
}

func TestIdleStreamStillEndsWithItsCodeAfterSeveralDeadlines(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedEvents(t, s, 0)
	url := startDeadlineServer(t, path, deadlineSendTimeout)
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), url)

	w := openWatch(t, events, 0)
	if got := w.next(t).GetSeq(); got != 1 {
		t.Fatalf("replayed message: seq %d, want 1", got)
	}

	// A send's deadline must not outlive the send: the end of a stream that has been quiet for longer than the
	// deadline still reaches the client with its code.
	<-time.After(deadlineIdleFactor * deadlineSendTimeout)
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove store: %v", err)
	}
	wantCode(t, w.finish(t), connect.CodeUnavailable)
}
