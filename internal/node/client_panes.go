package node

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"

	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

// Name returns the host's name, as hosts.toml gives it.
func (c *Client) Name() string {
	return c.cfg.Name
}

// HTTPClient returns an http.Client whose transport reaches the agent's Panes handler through an OpPanes connection.
// Each request takes a connection of its own. Its dial and its OpPanes Request with the Response that answers it come
// within Bound on the client's Clock, and so do its response headers. A request that misses either bound fails with an
// error that matches holderhost.ErrUnreachable.
func (c *Client) HTTPClient() *http.Client {
	return &http.Client{Transport: panesTransport{c: c}}
}

// Panes returns the orrery.v1.Panes client of the host's agent, over HTTPClient.
//
//nolint:ireturn // The generated Connect client is an interface, and the pane seam names it as its return.
func (c *Client) Panes() orreryv1connect.PanesClient {
	return orreryv1connect.NewPanesClient(c.HTTPClient(), "http://"+c.cfg.Name)
}

// WatchPanes opens the agent's pane watch, and returns once the agent's Response arrives within Bound. Each Next of
// the returned PaneWatch reads one PaneEvent, and Close ends the watch. The watch also ends when parent ends.
func (c *Client) WatchPanes(parent context.Context) (*PaneWatch, error) {
	req := Request{Version: Version, Op: OpWatchPanes}
	conn, err := c.open(parent, req)
	if err != nil {
		return nil, err
	}
	return &PaneWatch{
		host:    c.cfg.Name,
		conn:    conn,
		lines:   json.NewDecoder(conn),
		stopCtx: context.AfterFunc(parent, func() { c.closeConn(conn) }),
	}, nil
}

// Load returns the host's Load from its agent, within Bound.
func (c *Client) Load(parent context.Context) (Load, error) {
	req := Request{Version: Version, Op: OpLoad}
	resp, err := c.call(parent, req)
	if err != nil {
		return Load{}, err
	}
	if resp.Load == nil {
		return Load{}, c.opErr(req, errors.New("the response carries no load"))
	}
	return *resp.Load, nil
}

// open dials the agent and sends it req, and returns the connection once the agent's Response carries no Code. The dial
// and the Response come within Bound on the client's Clock. A failure of either matches holderhost.ErrUnreachable,
// unless parent was cancelled, and an answer with a Code keeps the error that the Code classes.
func (c *Client) open(parent context.Context, req Request) (net.Conn, error) {
	ctx, cancel := c.bound(parent)
	defer cancel()
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, c.fail(parent, req, err)
	}
	resp, err := c.exchange(ctx, conn, req)
	if err != nil {
		return nil, c.fail(parent, req, err)
	}
	if err = respErr(resp); err != nil {
		c.closeConn(conn)
		return nil, c.opErr(req, err)
	}
	return conn, nil
}

// PaneWatch is a pane watch of one host's agent, which WatchPanes opens. The agent writes each PaneEvent as one JSON
// line on the watch's connection.
type PaneWatch struct {
	host  string
	conn  net.Conn
	lines *json.Decoder
	// stopCtx stops the closing of conn on the ctx that WatchPanes was called with. Close calls it.
	stopCtx func() bool
}

// Next returns the next PaneEvent of the watch, or the error that ends it.
func (w *PaneWatch) Next() (PaneEvent, error) {
	var event PaneEvent
	if err := w.lines.Decode(&event); err != nil {
		return PaneEvent{}, fmt.Errorf("node: pane watch of %s: %w", w.host, err)
	}
	return event, nil
}

// Close ends the watch, and closes the connection it reads from. A connection that is already closed is not an error.
func (w *PaneWatch) Close() error {
	w.stopCtx()
	if err := w.conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("node: close the pane watch of %s: %w", w.host, err)
	}
	return nil
}

// panesTransport is the http.RoundTripper of HTTPClient. Each request takes a connection to the agent, on which it
// sends the OpPanes Request. Once the agent answers with no Code, the request runs as HTTP/1.1 on that connection.
type panesTransport struct {
	c *Client
}

// RoundTrip sends req to the agent's Panes handler, as panesTransport describes. The response body owns the connection
// until it is closed.
func (t panesTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	conn, err := t.c.open(req.Context(), Request{Version: Version, Op: OpPanes})
	if err != nil {
		if req.Body != nil {
			err = errors.Join(err, req.Body.Close())
		}
		return nil, err
	}
	ctx, cancel := t.c.bound(req.Context())
	defer cancel()
	stop := context.AfterFunc(ctx, func() { t.c.closeConn(conn) })
	resp, err := writeRequest(conn, req)
	if stopped := stop(); err != nil || !stopped {
		t.c.closeConn(conn)
		if err == nil {
			err = fmt.Errorf("the bound ended before the response headers: %w", ctx.Err())
		}
		return nil, t.c.fail(req.Context(), Request{Version: Version, Op: OpPanes}, err)
	}
	resp.Body = &connBody{
		ReadCloser: resp.Body,
		conn:       conn,
		stop:       context.AfterFunc(req.Context(), func() { t.c.closeConn(conn) }),
	}
	return resp, nil
}

// writeRequest sends req on conn as HTTP/1.1, and reads the response headers that answer it. It asks the agent to close
// the connection after its response, since a connection carries one request. The body of the response is read through
// the reader that reads the headers, so no byte is lost between them.
func writeRequest(conn net.Conn, req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	out.Close = true
	if err := out.Write(conn); err != nil {
		return nil, fmt.Errorf("send the %s request: %w", req.Method, err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return nil, fmt.Errorf("read the response to the %s request: %w", req.Method, err)
	}
	return resp, nil
}

// connBody is the body of a response that RoundTrip read from its connection. Closing it closes the connection.
type connBody struct {
	io.ReadCloser
	conn net.Conn
	// stop stops the closing of conn on the request's ctx. Close calls it.
	stop func() bool
}

// Close closes the body and then its connection. A connection that is already closed is not an error.
func (b *connBody) Close() error {
	b.stop()
	err := b.ReadCloser.Close()
	if closeErr := b.conn.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
		err = errors.Join(err, closeErr)
	}
	return err
}
