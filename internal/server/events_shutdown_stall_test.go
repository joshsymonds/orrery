package server_test

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

// stallSendTimeout is the Watch send deadline of the blocked-send tests. It is far longer than the test binary's
// timeout, so the send deadline never ends a blocked send during a test: only the shutdown signal can, and a server
// that waits out its send deadline at shutdown holds the test until the binary's timeout fails it.
const stallSendTimeout = 24 * time.Hour

// stallSamples is how many consecutive event ticks must show the same write in progress with the same byte count
// before the send counts as blocked. A write the client is not reading stays in progress indefinitely, so the count
// holds; a write that completes changes it.
const stallSamples = 5

// observedConn records the writes of an accepted connection: whether one is in progress, and the bytes of the
// writes that have completed. While held, it records the write deadline the server sets instead of applying it.
type observedConn struct {
	net.Conn
	mu      sync.Mutex
	writing bool
	written int64
	held    bool // whether write deadlines are recorded instead of applied
	armed   bool // whether a deadline recorded while held is in force
	closed  bool // whether the server has closed the connection
}

// Close records that the connection is closed, then closes it.
func (c *observedConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return c.Conn.Close()
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

// SetWriteDeadline applies deadline to the connection. While the connection is held it records the deadline instead:
// a non-zero deadline stays in force until release, and the zero time clears it.
func (c *observedConn) SetWriteDeadline(deadline time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.held {
		c.armed = !deadline.IsZero()
		return nil
	}
	return c.Conn.SetWriteDeadline(deadline)
}

// release lifts the hold. A deadline in force from the hold is applied as already due, so a write it governs that is
// blocked fails at once. A connection held from accept has applied no deadline, so no other state needs restoring.
func (c *observedConn) release() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.held = false
	if !c.armed {
		return nil
	}
	c.armed = false
	return c.Conn.SetWriteDeadline(time.Now())
}

// observedListener wraps each accepted connection in an observedConn. Connections accepted while the listener holds
// are held from the start, so no deadline the server sets on them is applied before release.
type observedListener struct {
	net.Listener
	mu    sync.Mutex
	hold  bool
	conns []*observedConn
}

// Accept returns the next connection, wrapped so its writes are observed.
func (l *observedListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	obs := &observedConn{Conn: conn, held: l.hold}
	l.conns = append(l.conns, obs)
	l.mu.Unlock()
	return obs, nil
}

// setHold sets whether connections accepted from now on hold their write deadlines.
func (l *observedListener) setHold(hold bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hold = hold
}

// releaseAll releases every connection, applying each deadline in force from a hold as already due. A connection
// that is not held has no such deadline, so its release changes nothing.
func (l *observedListener) releaseAll() error {
	l.mu.Lock()
	conns := append([]*observedConn(nil), l.conns...)
	l.mu.Unlock()
	for _, c := range conns {
		if err := c.release(); err != nil {
			return fmt.Errorf("release %v: %w", c.RemoteAddr(), err)
		}
	}
	return nil
}

// writeState reports whether any accepted connection has a write in progress, the total bytes of the writes that
// have completed on all of them, and whether the server has closed every one it accepted, at least one. Once all are
// closed, no write can start, so a wait for one would never end.
func (l *observedListener) writeState() (bool, int64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	writing, allClosed := false, len(l.conns) > 0
	var written int64
	for _, c := range l.conns {
		c.mu.Lock()
		writing = writing || c.writing
		written += c.written
		allClosed = allClosed && c.closed
		c.mu.Unlock()
	}
	return writing, written, allClosed
}

// waitBlockedSend polls the server's writes on the event tick until a write has stayed in progress for stallSamples
// consecutive ticks with no byte completing, after the first replayed message has been written in full. That write is
// a send the client is not reading. It has no deadline of its own, and fails at once if the server closes every
// connection first.
func waitBlockedSend(t *testing.T, obs *observedListener) {
	t.Helper()
	tick := time.NewTicker(eventPoll)
	defer tick.Stop()
	last, stable := int64(-1), 0
	for stable < stallSamples {
		<-tick.C
		writing, written, allClosed := obs.writeState()
		if allClosed {
			t.Fatalf("no send blocked on the unread client: the server closed every connection first, %d bytes written",
				written)
		}
		if writing && written > deadlineSummaryBytes && written == last {
			stable++
		} else {
			stable = 0
		}
		last = written
	}
}

// TestShutdownEndsBlockedSendWithinGrace holds a replay blocked in a send on a client that stopped reading. Closing
// the shutdown signal must end the blocked send and the stream's end-of-stream write, so the server closes. The send
// deadline is stallSendTimeout, so only the shutdown can end the send.
func TestShutdownEndsBlockedSendWithinGrace(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	openFilledEventStore(t, path, "big", shutdownReplayCount, writeBigEvents)
	shutdown := make(chan struct{})
	srv := httptest.NewUnstartedServer(newShutdownHandler(t, path, nil, shutdown, stallSendTimeout))
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
	<-closed
}
