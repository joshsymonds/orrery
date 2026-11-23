package node

import (
	"context"
	"errors"
	"net/http"

	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

// Name returns the host's name, as hosts.toml gives it.
func (c *Client) Name() string {
	return c.cfg.Name
}

// HTTPClient returns an http.Client whose transport reaches the agent's Panes handler through an OpPanes connection.
// A request whose dial, or whose response headers, do not come within Bound on the client's Clock fails with an error
// that matches holderhost.ErrUnreachable. Until the pane tasks give it that transport, every request fails.
func (c *Client) HTTPClient() *http.Client {
	return &http.Client{Transport: unimplementedTransport{}}
}

// Panes returns the orrery.v1.Panes client of the host's agent, over HTTPClient.
//
//nolint:ireturn // The generated Connect client is an interface, and the pane seam names it as its return.
func (c *Client) Panes() orreryv1connect.PanesClient {
	return orreryv1connect.NewPanesClient(c.HTTPClient(), "http://"+c.cfg.Name)
}

// WatchPanes opens the agent's pane watch, and returns once the agent's Response arrives within Bound. Each Next of
// the returned PaneWatch reads one PaneEvent, and Close ends the watch. Until the pane tasks implement it, WatchPanes
// fails.
func (c *Client) WatchPanes(_ context.Context) (*PaneWatch, error) {
	return nil, errors.New("node: watch panes not implemented")
}

// Load returns the host's Load from its agent, within Bound. Until the pane tasks implement it, Load fails.
func (c *Client) Load(_ context.Context) (Load, error) {
	return Load{}, errors.New("node: load not implemented")
}

// PaneWatch is a pane watch of one host's agent, which WatchPanes opens.
type PaneWatch struct{}

// Next returns the next PaneEvent of the watch, or the error that ends it. Until the pane tasks implement it, Next
// fails.
func (w *PaneWatch) Next() (PaneEvent, error) {
	return PaneEvent{}, errors.New("node: pane watch not implemented")
}

// Close ends the watch, and closes the connection it reads from. Until the pane tasks implement it, Close has
// nothing to close.
func (w *PaneWatch) Close() error {
	return nil
}

// unimplementedTransport is the transport of HTTPClient until the pane tasks implement it. It fails every request.
type unimplementedTransport struct{}

// RoundTrip fails the request, since the pane transport is not implemented.
func (unimplementedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("node: pane transport not implemented")
}
