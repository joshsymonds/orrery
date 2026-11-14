// Package deskstream serves the desk's notice stream. Each accepted connection gets a hello line, then each notice posted
// while it is open, as JSON lines. The desk is present while a stream is open. Lines the desk sends back up the same
// connection are read and discarded. The hub never listens itself: Serve takes a listener the daemon opened.
package deskstream

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"sync"
	"time"
)

const (
	// KindHello is the first line of every stream.
	KindHello = "hello"
	// KindNotice is a line carrying one notice.
	KindNotice = "notice"
)

const (
	// flushTimeout bounds how long Close lets queued lines reach their streams before it closes them.
	flushTimeout = 2 * time.Second
	// queueLen is how many lines a stream may hold unsent. A stream that falls this far behind is closed.
	queueLen = 64
	// shortIDLen is how many characters of a session id a session title shows.
	shortIDLen = 8
	socketName = "notices.sock"
)

// Notice is one notice to show on the desk. A quiet notice shows without a sound.
type Notice struct {
	State, Title, Body string
	Quiet              bool
}

// Line is one line of the stream. State, Title, Body and Quiet are omitted when empty or false.
type Line struct {
	Kind  string `json:"kind"`
	Host  string `json:"host"`
	State string `json:"state,omitempty"`
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
	Quiet bool   `json:"quiet,omitempty"`
}

// SocketPath names the notices socket beside the daemon socket, as protocol.SessionSocketPath does.
func SocketPath(daemonSocket string) string {
	return filepath.Join(filepath.Dir(daemonSocket), socketName)
}

// PaneTitle names a notice about a pane: project · session:window.
func PaneTitle(project, session string, window int) string {
	return fmt.Sprintf("%s · %s:%d", project, session, window)
}

// SessionTitle names a notice about a session by the first characters of its id.
func SessionTitle(project, id string) string {
	runes := []rune(id)
	if len(runes) > shortIDLen {
		runes = runes[:shortIDLen]
	}
	return fmt.Sprintf("%s · session %s", project, string(runes))
}

// Hub holds the open streams and the newest web input. It is safe for concurrent use.
type Hub struct {
	host   string
	logger *slog.Logger

	mu      sync.Mutex
	closed  bool
	ln      net.Listener
	streams map[*subscriber]struct{}
	input   int64
	handler sync.WaitGroup
}

// subscriber is one accepted connection. lines is closed once closing is set, which happens under Hub.mu, so Post never
// sends on a closed channel.
type subscriber struct {
	conn    net.Conn
	lines   chan []byte
	closing bool
}

// NewHub returns a hub for host that logs with logger. A nil logger discards.
func NewHub(host string, logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Hub{host: host, logger: logger, streams: map[*subscriber]struct{}{}}
}

// Serve accepts streams on ln until Close, then returns nil. Any other accept error is returned.
func (h *Hub) Serve(ln net.Listener) error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		if err := ln.Close(); err != nil {
			return fmt.Errorf("close desk listener: %w", err)
		}
		return nil
	}
	h.ln = ln
	h.mu.Unlock()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if h.isClosed() {
				return nil
			}
			return fmt.Errorf("accept desk stream: %w", err)
		}
		h.accept(conn)
	}
}

// Post queues n on every open stream and returns without waiting. A stream whose queue is full is closed.
func (h *Hub) Post(n Notice) {
	line, err := encode(
		Line{Kind: KindNotice, Host: h.host, State: n.State, Title: n.Title, Body: n.Body, Quiet: n.Quiet},
	)
	if err != nil {
		h.logger.Error("encode desk notice", "error", err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.streams {
		if sub.closing {
			continue
		}
		select {
		case sub.lines <- line:
		default:
			h.logger.Warn("closing desk stream that fell behind")
			h.dropLocked(sub)
			h.closeConn(sub)
		}
	}
}

// Close stops accepting streams and ends the open ones. Queued lines get up to flushTimeout to reach their clients,
// then any stream still open is closed. Close returns once every stream has ended.
func (h *Hub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	for sub := range h.streams {
		h.dropLocked(sub)
		h.stopReading(sub)
	}
	ln := h.ln
	h.mu.Unlock()

	if ln != nil {
		if err := ln.Close(); err != nil {
			h.logger.Warn("close desk listener", "error", err)
		}
	}
	ended := make(chan struct{})
	go func() {
		h.handler.Wait()
		close(ended)
	}()
	select {
	case <-ended:
		return
	case <-time.After(flushTimeout):
	}
	h.mu.Lock()
	for sub := range h.streams {
		h.closeConn(sub)
	}
	h.mu.Unlock()
	<-ended
}

// Input records the web input at input, unix seconds. The hub keeps the newest input it has been given.
func (h *Hub) Input(input int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if input > h.input {
		h.input = input
	}
}

// Desk returns the newest web input and whether a stream is open.
func (h *Hub) Desk() (input int64, present bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	open := false
	for sub := range h.streams {
		if !sub.closing {
			open = true
			break
		}
	}
	return h.input, open
}

func (h *Hub) isClosed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}

// accept registers a stream for conn, queues its hello, and starts its handler. A connection that arrives after Close
// is closed at once.
func (h *Hub) accept(conn net.Conn) {
	hello, err := encode(Line{Kind: KindHello, Host: h.host})
	if err != nil {
		h.logger.Error("encode desk hello", "error", err)
		h.closeConnRaw(conn)
		return
	}
	sub := &subscriber{conn: conn, lines: make(chan []byte, queueLen)}
	sub.lines <- hello

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		h.closeConnRaw(conn)
		return
	}
	h.streams[sub] = struct{}{}
	h.handler.Add(1)
	go h.serve(sub)
}

// serve runs one stream until it ends. A writer sends the stream's queue while this goroutine reads the desk's lines.
func (h *Hub) serve(sub *subscriber) {
	defer h.handler.Done()
	written := make(chan struct{})
	go h.write(sub, written)

	h.read(sub)
	h.mu.Lock()
	h.dropLocked(sub)
	h.mu.Unlock()
	select {
	case <-written:
	case <-time.After(flushTimeout):
	}
	h.closeConn(sub)

	h.mu.Lock()
	delete(h.streams, sub)
	h.mu.Unlock()
}

// write sends the stream's queue in order until the queue is closed and drained, or a write fails. A failed write
// closes the stream.
func (h *Hub) write(sub *subscriber, written chan<- struct{}) {
	defer close(written)
	for line := range sub.lines {
		if _, err := sub.conn.Write(line); err != nil {
			h.logger.Debug("write desk stream", "error", err)
			h.closeConn(sub)
			return
		}
	}
}

// read discards what the desk sends until the stream ends.
func (h *Hub) read(sub *subscriber) {
	if _, err := io.Copy(io.Discard, sub.conn); err != nil {
		h.logger.Debug("read desk stream", "error", err)
	}
}

// dropLocked ends s's queue. The writer sends what is queued and stops. Callers hold h.mu.
func (h *Hub) dropLocked(sub *subscriber) {
	if sub.closing {
		return
	}
	sub.closing = true
	close(sub.lines)
}

// stopReading makes s's pending and future reads fail, so its handler ends while its writer keeps flushing. Callers
// hold h.mu.
func (h *Hub) stopReading(sub *subscriber) {
	if err := sub.conn.SetReadDeadline(time.Now()); err != nil {
		h.logger.Debug("stop reading desk stream", "error", err)
	}
}

// closeConn closes s's connection, which fails a blocked write or read.
func (h *Hub) closeConn(sub *subscriber) {
	h.closeConnRaw(sub.conn)
}

func (h *Hub) closeConnRaw(conn net.Conn) {
	if err := conn.Close(); err != nil {
		h.logger.Debug("close desk stream", "error", err)
	}
}

// encode returns l as one JSON line, newline included.
func encode(l Line) ([]byte, error) {
	b, err := json.Marshal(l)
	if err != nil {
		return nil, fmt.Errorf("encode desk line: %w", err)
	}
	return append(b, '\n'), nil
}
