package deskstream

import (
	"bufio"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// clock is a fixed clock whose time a test sets. The hub reads it from its own goroutines, so it is atomic.
type clock struct{ sec atomic.Int64 }

// newClock returns a clock at start.
func newClock() *clock {
	c := &clock{}
	c.sec.Store(start)
	return c
}

func (c *clock) now() time.Time { return time.Unix(c.sec.Load(), 0) }

func (c *clock) set(sec int64) { c.sec.Store(sec) }

const start = int64(1700000000)

// startHub listens on the notices socket beside a daemon socket and serves hub on it. It returns the socket path and
// the channel Serve's error arrives on. The hub is closed at cleanup.
func startHub(t *testing.T, hub *Hub) (string, <-chan error) {
	t.Helper()
	path := SocketPath(filepath.Join(t.TempDir(), "orreryd.sock"))
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen on %s: %v", path, err)
	}
	errc := make(chan error, 1)
	go func() { errc <- hub.Serve(ln) }()
	t.Cleanup(hub.Close)
	return path, errc
}

// client is a stream reader connected to the hub.
type client struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func dial(t *testing.T, path string) *client {
	t.Helper()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial %s: %v", path, err)
	}
	t.Cleanup(func() { conn.Close() })
	return &client{t: t, conn: conn, r: bufio.NewReader(conn)}
}

// readLine returns the next line without its newline. It fails the test if no line arrives within five seconds.
func (c *client) readLine() string {
	c.t.Helper()
	if err := c.conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		c.t.Fatalf("set read deadline: %v", err)
	}
	line, err := c.r.ReadString('\n')
	if err != nil {
		c.t.Fatalf("read line: %v (partial %q)", err, line)
	}
	return strings.TrimSuffix(line, "\n")
}

// expectEOF fails the test unless the hub has closed the stream after the lines already read.
func (c *client) expectEOF() {
	c.t.Helper()
	if err := c.conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		c.t.Fatalf("set read deadline: %v", err)
	}
	line, err := c.r.ReadString('\n')
	if !errors.Is(err, io.EOF) {
		c.t.Fatalf("after last line: read %q, %v; want the stream closed (io.EOF)", line, err)
	}
}

// write sends raw bytes to the hub as the desk would.
func (c *client) write(s string) {
	c.t.Helper()
	if _, err := io.WriteString(c.conn, s); err != nil {
		c.t.Fatalf("write %q: %v", s, err)
	}
}

// waitFor polls cond until it holds, failing the test after five seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSocketPath(t *testing.T) {
	got := SocketPath("/run/user/1000/orrery/orreryd.sock")
	want := "/run/user/1000/orrery/notices.sock"
	if got != want {
		t.Fatalf("SocketPath = %q, want %q", got, want)
	}
}

func TestTitles(t *testing.T) {
	if got, want := PaneTitle("orrery", "main", 2), "orrery · main:2"; got != want {
		t.Fatalf("PaneTitle = %q, want %q", got, want)
	}
	if got, want := SessionTitle("orrery", "0123456789abcdef"), "orrery · session 01234567"; got != want {
		t.Fatalf("SessionTitle = %q, want %q", got, want)
	}
	if got, want := SessionTitle("orrery", "abc"), "orrery · session abc"; got != want {
		t.Fatalf("SessionTitle short id = %q, want %q", got, want)
	}
}

func TestHelloThenNoticesInOrder(t *testing.T) {
	hub := NewHub("gnomon", newClock().now, nil)
	path, _ := startHub(t, hub)
	c := dial(t, path)

	if got, want := c.readLine(), `{"kind":"hello","host":"gnomon"}`; got != want {
		t.Fatalf("hello = %s, want %s", got, want)
	}
	hub.Post(Notice{State: "waiting", Title: "T1", Body: "B1"})
	hub.Post(Notice{State: "done", Title: "T2", Body: "B2"})
	hub.Post(Notice{Title: "T3"})

	wants := []string{
		`{"kind":"notice","host":"gnomon","state":"waiting","title":"T1","body":"B1"}`,
		`{"kind":"notice","host":"gnomon","state":"done","title":"T2","body":"B2"}`,
		`{"kind":"notice","host":"gnomon","title":"T3"}`,
	}
	for _, want := range wants {
		if got := c.readLine(); got != want {
			t.Fatalf("notice = %s, want %s", got, want)
		}
	}
}

func TestQuietNoticeLineCarriesQuietKey(t *testing.T) {
	hub := NewHub("gnomon", newClock().now, nil)
	path, _ := startHub(t, hub)
	c := dial(t, path)
	c.readLine()

	hub.Post(Notice{State: "done", Title: "T", Body: "B", Quiet: true})
	want := `{"kind":"notice","host":"gnomon","state":"done","title":"T","body":"B","quiet":true}`
	if got := c.readLine(); got != want {
		t.Fatalf("quiet notice = %s, want %s", got, want)
	}
}

func TestNoticeWithoutQuietHasNoQuietKey(t *testing.T) {
	hub := NewHub("gnomon", newClock().now, nil)
	path, _ := startHub(t, hub)
	c := dial(t, path)
	c.readLine()

	hub.Post(Notice{State: "done", Title: "T", Body: "B"})
	want := `{"kind":"notice","host":"gnomon","state":"done","title":"T","body":"B"}`
	if got := c.readLine(); got != want {
		t.Fatalf("notice = %s, want %s (no quiet key when Quiet is false)", got, want)
	}
}

func TestTwoStreamsReceiveNotices(t *testing.T) {
	hub := NewHub("gnomon", newClock().now, nil)
	path, _ := startHub(t, hub)
	a := dial(t, path)
	b := dial(t, path)
	a.readLine()
	b.readLine()

	hub.Post(Notice{State: "waiting", Title: "T", Body: "B"})
	want := `{"kind":"notice","host":"gnomon","state":"waiting","title":"T","body":"B"}`
	if got := a.readLine(); got != want {
		t.Fatalf("first stream notice = %s, want %s", got, want)
	}
	if got := b.readLine(); got != want {
		t.Fatalf("second stream notice = %s, want %s", got, want)
	}
}

func TestStalledStreamClosedWhilePostReturns(t *testing.T) {
	hub := NewHub("gnomon", newClock().now, nil)
	path, _ := startHub(t, hub)
	stalled := dial(t, path)
	stalled.readLine() // hello, so the stream is registered before the posts

	body := strings.Repeat("x", 4096)
	posted := make(chan struct{})
	go func() {
		defer close(posted)
		for i := range 500 {
			hub.Post(Notice{State: "waiting", Title: "T", Body: body + strings.Repeat("y", i%7)})
		}
	}()
	select {
	case <-posted:
	case <-time.After(5 * time.Second):
		t.Fatal("Post blocked behind a stalled stream")
	}

	// The stalled stream must be closed after whatever was queued: reads drain to EOF, not to a timeout. ReadAll
	// reports io.EOF as nil, so any error here is a read that ended without the hub closing the stream.
	stalled.drainToClose("stalled stream after Post")
}

// drainToClose reads everything the hub sent until the stream closes. It fails the test if the read ends in anything
// other than the hub closing the stream, such as a timeout.
func (c *client) drainToClose(what string) {
	c.t.Helper()
	if err := c.conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		c.t.Fatalf("set read deadline: %v", err)
	}
	if _, err := io.ReadAll(c.r); err != nil {
		c.t.Fatalf("%s: read ended with %v; want the hub to close the stream", what, err)
	}
}

// closeWrite half-closes the stream so the hub's reader sees end of input after the lines already sent.
func (c *client) closeWrite() {
	c.t.Helper()
	conn, ok := c.conn.(*net.UnixConn)
	if !ok {
		c.t.Fatalf("stream is %T, want *net.UnixConn", c.conn)
	}
	if err := conn.CloseWrite(); err != nil {
		c.t.Fatalf("close write: %v", err)
	}
}

func TestNoticePostedBeforeCloseDelivered(t *testing.T) {
	hub := NewHub("gnomon", newClock().now, nil)
	path, _ := startHub(t, hub)
	c := dial(t, path)
	c.readLine()

	hub.Post(Notice{State: "waiting", Title: "last", Body: "before close"})
	hub.Close()

	want := `{"kind":"notice","host":"gnomon","state":"waiting","title":"last","body":"before close"}`
	if got := c.readLine(); got != want {
		t.Fatalf("notice after Close = %s, want %s", got, want)
	}
	c.expectEOF()
}

func TestCloseStopsAcceptingAndServeReturnsNil(t *testing.T) {
	hub := NewHub("gnomon", newClock().now, nil)
	path, errc := startHub(t, hub)
	c := dial(t, path)
	c.readLine()

	hub.Close()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("Serve = %v, want nil after Close", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after Close")
	}
	if conn, err := net.Dial("unix", path); err == nil {
		conn.Close()
		t.Fatal("dial after Close succeeded; the hub must stop accepting")
	}
}

func TestCloseWithStalledStreamReturnsWithin2s(t *testing.T) {
	hub := NewHub("gnomon", newClock().now, nil)
	path, _ := startHub(t, hub)
	stalled := dial(t, path)
	stalled.readLine()

	// About 1 MB queued for a client that never reads: the socket buffer fills and the queued lines cannot flush.
	body := strings.Repeat("x", 100*1024)
	for range 10 {
		hub.Post(Notice{State: "waiting", Title: "T", Body: body})
	}

	began := time.Now()
	hub.Close()
	elapsed := time.Since(began)
	if elapsed < 2*time.Second || elapsed > 4*time.Second {
		t.Fatalf("Close with a stalled stream took %v, want about the 2 s flush budget", elapsed)
	}
	stalled.drainToClose("stalled stream after Close")
}

func TestDeskPresenceWindow(t *testing.T) {
	clk := newClock()
	hub := NewHub("gnomon", clk.now, nil)
	path, _ := startHub(t, hub)
	c := dial(t, path)
	c.readLine()

	if input, present := hub.Desk(); present || input != 0 {
		t.Fatalf("Desk with a stream and no activity = (%d, %v), want (0, false)", input, present)
	}

	c.write(`{"input":1700000000}` + "\n")
	waitFor(t, "activity", func() bool {
		input, present := hub.Desk()
		return input == start && present
	})

	clk.set(start + 90)
	if input, present := hub.Desk(); input != start || !present {
		t.Fatalf("Desk at 90 s = (%d, %v), want (%d, true)", input, present, start)
	}
	clk.set(start + 91)
	if input, present := hub.Desk(); input != start || present {
		t.Fatalf("Desk at 91 s = (%d, %v), want (%d, false)", input, present, start)
	}

	// Activity within 90 s but no open stream is away.
	clk.set(start + 90)
	c.closeWrite()
	waitFor(t, "stream to end", func() bool {
		_, present := hub.Desk()
		return !present
	})
	if input, present := hub.Desk(); input != start || present {
		t.Fatalf("Desk with no stream = (%d, %v), want (%d, false)", input, present, start)
	}
}

func TestDeskAwayWithoutStream(t *testing.T) {
	hub := NewHub("gnomon", newClock().now, nil)
	if input, present := hub.Desk(); input != 0 || present {
		t.Fatalf("Desk on a fresh hub = (%d, %v), want (0, false)", input, present)
	}
}

func TestDeskReturnsNewestInput(t *testing.T) {
	hub := NewHub("gnomon", newClock().now, nil)
	path, _ := startHub(t, hub)
	c := dial(t, path)
	c.readLine()

	c.write(`{"input":1700000000}` + "\n")
	waitFor(t, "first activity", func() bool {
		input, _ := hub.Desk()
		return input == start
	})
	c.write(`{"input":1700000030}` + "\n")
	waitFor(t, "second activity", func() bool {
		input, _ := hub.Desk()
		return input == start+30
	})
}

func TestMalformedAndNonActivityLinesIgnored(t *testing.T) {
	clk := newClock()
	hub := NewHub("gnomon", clk.now, nil)
	path, _ := startHub(t, hub)
	c := dial(t, path)
	c.readLine()

	c.write(`{"input":1700000000}` + "\n")
	waitFor(t, "activity", func() bool {
		input, present := hub.Desk()
		return input == start && present
	})

	// These lines follow the activity on the same stream. None may change the desk's input. Closing the write side ends
	// the stream only after the reader has consumed every line, so the stream ending is the sync point.
	c.write("not json\n" +
		`{"kind":"notice","title":"T"}` + "\n" +
		`{"input":0}` + "\n" +
		`{"input":-5}` + "\n" +
		"[]\n")
	c.closeWrite()
	waitFor(t, "stream to end", func() bool {
		_, present := hub.Desk()
		return !present
	})
	if input, _ := hub.Desk(); input != start {
		t.Fatalf("Desk input after ignored lines = %d, want %d", input, start)
	}
}
