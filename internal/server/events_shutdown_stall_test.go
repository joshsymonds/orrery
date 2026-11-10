package server_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

// stallSendTimeout is the Watch send deadline of the blocked-send test, far above the shutdown grace, so a server
// that waits out its send deadline at shutdown misses closeBudget.
const stallSendTimeout = 30 * time.Second

// stallSamples is how many consecutive event ticks must show the same write in progress with the same byte count
// before the send counts as blocked. A write the client is not reading stays in progress indefinitely, so the count
// holds; a write that completes changes it.
const stallSamples = 5

// closeBudget bounds how long the server may take to close after the shutdown signal closes a stream blocked in a
// send. It is well above the shutdown grace and well below stallSendTimeout.
const closeBudget = 5 * time.Second

// observedConn records the writes of an accepted connection: whether one is in progress, and the bytes of the
// writes that have completed.
type observedConn struct {
	net.Conn
	mu      sync.Mutex
	writing bool
	written int64
}

// Write writes p to the connection and records the write as in progress until it returns.
func (c *observedConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.writing = true
	c.mu.Unlock()
	n, err := c.Conn.Write(p)
	c.mu.Lock()
	c.writing = false
	c.written += int64(n)
	c.mu.Unlock()
	return n, err
}

// observedListener wraps each accepted connection in an observedConn.
type observedListener struct {
	net.Listener
	mu    sync.Mutex
	conns []*observedConn
}

// Accept returns the next connection, wrapped so its writes are observed.
func (l *observedListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	obs := &observedConn{Conn: conn}
	l.mu.Lock()
	l.conns = append(l.conns, obs)
	l.mu.Unlock()
	return obs, nil
}

// writeState reports whether any accepted connection has a write in progress, and the total bytes of the writes
// that have completed on all of them.
func (l *observedListener) writeState() (bool, int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	writing := false
	var written int64
	for _, c := range l.conns {
		c.mu.Lock()
		writing = writing || c.writing
		written += c.written
		c.mu.Unlock()
	}
	return writing, written
}

// waitBlockedSend polls the server's writes on the event tick until a write has stayed in progress for stallSamples
// consecutive ticks with no byte completing, after the first replayed message has been written in full. That write is
// a send the client is not reading. It fails the test if no such send appears within watchWait.
func waitBlockedSend(t *testing.T, obs *observedListener) {
	t.Helper()
	timeout := time.After(watchWait)
	tick := time.NewTicker(eventPoll)
	defer tick.Stop()
	last, stable := int64(-1), 0
	for stable < stallSamples {
		select {
		case <-timeout:
			t.Fatalf("no send blocked mid-replay within %v", watchWait)
		case <-tick.C:
		}
		writing, written := obs.writeState()
		if writing && written > deadlineSummaryBytes && written == last {
			stable++
		} else {
			stable = 0
		}
		last = written
	}
}

// TestShutdownEndsBlockedSendWithinGrace holds a replay blocked in a send on a client that stopped reading. Closing
// the shutdown signal must end the blocked send and the stream's end-of-stream write within the shutdown grace, so
// the server closes well before the send deadline.
func TestShutdownEndsBlockedSendWithinGrace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedBigEvents(t, s, shutdownReplayCount)
	shutdown := make(chan struct{})
	srv := httptest.NewUnstartedServer(newShutdownHandler(t, path, shutdown, stallSendTimeout))
	obs := &observedListener{Listener: smallBufferListener{Listener: srv.Listener}}
	srv.Listener = obs
	srv.Start()
	t.Cleanup(srv.Close)
	stalledHTTP := &http.Client{Transport: headerTransport{
		base:  stalledTransport(make(chan net.Conn, deadlineConnCapacity)),
		login: allowedLogin,
	}}
	replaying := openWatch(t, orreryv1connect.NewEventsClient(stalledHTTP, srv.URL), 0)
	if got := replaying.next(t).GetSeq(); got != 1 {
		t.Fatalf("replayed message: seq %d, want 1", got)
	}

	// The client reads no further, so the replay's next send blocks on the full socket buffers.
	waitBlockedSend(t, obs)
	close(shutdown)
	closed := make(chan struct{})
	go func() {
		srv.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(closeBudget):
		t.Fatalf("server still open %v after shutdown with a blocked send, want it to end within the shutdown grace "+
			"(send deadline %v)", closeBudget, stallSendTimeout)
	}
}
