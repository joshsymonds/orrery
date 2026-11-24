package node

import (
	"context"
	"errors"
)

// ErrRefused is the error of an op that an agent answered with a Code, so that the agent refused it. The Response's
// Message says why.
var ErrRefused = errors.New("node: agent refused the op")

// Folders asks the host's agent for its known folders, as OpFolders does, and returns them.
func (c *Client) Folders(_ context.Context) ([]string, error) {
	return nil, errors.New("node: Client.Folders is not built yet")
}

// Folder asks the host's agent to check that dir is an existing directory under its Root, as OpFolder does, and returns
// the resolved path of dir.
func (c *Client) Folder(_ context.Context, _ string) (string, error) {
	return "", errors.New("node: Client.Folder is not built yet")
}

// WakePane asks the host's agent to type text into pane of the tmux server column server when the pane is idle, as
// OpWakePane does, and reports whether it typed the text.
func (c *Client) WakePane(_ context.Context, _, _, _ string) (bool, error) {
	return false, errors.New("node: Client.WakePane is not built yet")
}
