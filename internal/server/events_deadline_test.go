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
	// deadlineStalledSendTimeout is the send deadline of the stalled stream's server in the test that proves a stuck
	// send leaves other streams alone. It is long enough that the healthy stream's whole replay finishes inside it.
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
// and small connection write buffers. It returns the URL, and a channel that receives the remote address of each
// connection as it closes.
func startDeadlineServer(t *testing.T, path string, sendTimeout time.Duration) (string, <-chan string) {
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
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	closed := make(chan string, deadlineConnCapacity)
	srv := &httptest.Server{
		Listener: smallBufferListener{Listener: listener},
		Config: &http.Server{Handler: handler, ConnState: func(conn net.Conn, state http.ConnState) {
			if state == http.StateClosed {
				closed <- conn.RemoteAddr().String()
			}
		}},
	}
	srv.Start()
	t.Cleanup(srv.Close)
	return srv.URL, closed
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

// waitClosed fails the test unless the connection with remote address addr closes within watchWait.
func waitClosed(t *testing.T, closed <-chan string, addr string) {
	t.Helper()
	timeout := time.After(watchWait)
	for {
		select {
		case got := <-closed:
			if got == addr {
				return
			}
		case <-timeout:
			t.Fatalf("connection %s still open after %v", addr, watchWait)
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
	timeout := time.After(watchWait)
	for {
		select {
		case _, ok := <-w.msgs:
			if !ok {
				return w.err
			}
		case <-timeout:
			t.Fatalf("stream did not end within %v", watchWait)
		}
	}
}

func TestStalledStreamEndsAloneAtSendDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedBigEvents(t, s, deadlineBigEvents)
	url, closed := startDeadlineServer(t, path, deadlineStalledSendTimeout)

	dialed := make(chan net.Conn, 1)
	stalledHTTP := &http.Client{Transport: headerTransport{base: stalledTransport(dialed), login: allowedLogin}}
	// The stalled stream's request has not been sent yet, so its blocked send's deadline starts after this. A healthy
	// stream that finishes within the deadline from here finished while the stalled send was still blocked.
	started := time.Now()
	stalled := openWatch(t, orreryv1connect.NewEventsClient(stalledHTTP, url), 0)
	stalledAddr := (<-dialed).LocalAddr().String()

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
	if elapsed := time.Since(started); elapsed >= deadlineStalledSendTimeout {
		t.Fatalf("healthy stream finished %v after the stalled stream started, want inside the %v send deadline",
			elapsed, deadlineStalledSendTimeout)
	}
	assertStillOpen(t, closed, stalledAddr)

	waitClosed(t, closed, stalledAddr)
	if err := drainUntilEnd(t, stalled); err == nil {
		t.Fatal("stalled stream ended cleanly, want a transport error with no end-of-stream message")
	}
}

func TestIdleStreamReceivesEventAfterSeveralDeadlines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedEvents(t, s, 0)
	url, _ := startDeadlineServer(t, path, deadlineSendTimeout)
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
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedEvents(t, s, 0)
	url, _ := startDeadlineServer(t, path, deadlineSendTimeout)
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
