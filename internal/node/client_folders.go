package node

import (
	"context"
	"errors"
	"fmt"
)

// ErrRefused is the error of an op that an agent answered with a Code, so that the agent refused it. The Response's
// Message says why.
var ErrRefused = errors.New("node: agent refused the op")

// Folders asks the host's agent for its known folders, as OpFolders does, and returns them.
func (c *Client) Folders(ctx context.Context) ([]string, error) {
	resp, err := c.answer(ctx, Request{Version: Version, Op: OpFolders})
	if err != nil {
		return nil, err
	}
	return resp.Folders, nil
}

// Folder asks the host's agent to check that dir is an existing directory under its Root, as OpFolder does, and returns
// the resolved path of dir.
func (c *Client) Folder(ctx context.Context, dir string) (string, error) {
	resp, err := c.answer(ctx, Request{Version: Version, Op: OpFolder, Dir: dir})
	if err != nil {
		return "", err
	}
	return resp.Folder, nil
}

// WakePane asks the host's agent to type text into pane of the tmux server column server when the pane is idle, as
// OpWakePane does, and reports whether it typed the text.
func (c *Client) WakePane(ctx context.Context, server, pane, text string) (bool, error) {
	resp, err := c.answer(ctx, Request{Version: Version, Op: OpWakePane, Server: server, Pane: pane, Text: text})
	if err != nil {
		return false, err
	}
	return resp.Woke, nil
}

// answer sends req within Bound, and returns the Response that answers it. A Response that carries a Code is an error that
// wraps ErrRefused and holds the agent's message. A dial or read failure is an error that does not wrap ErrRefused.
func (c *Client) answer(parent context.Context, req Request) (Response, error) {
	resp, err := c.request(parent, req)
	if err != nil {
		return Response{}, err
	}
	if resp.Code != "" {
		return Response{}, c.opErr(req, fmt.Errorf("%w: %s", ErrRefused, resp.Message))
	}
	return resp, nil
}
