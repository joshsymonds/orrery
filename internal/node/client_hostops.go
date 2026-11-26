package node

import (
	"context"

	"github.com/joshsymonds/orrery/internal/grants"
)

// Compile-time check that *Client is the grants.HostProcesses of its machine.
var _ grants.HostProcesses = (*Client)(nil)

// ProcessAlive reports whether the process with pid lives on the remote machine under the start time start. It is not
// yet served.
func (*Client) ProcessAlive(_ context.Context, _ int, _ int64) (bool, error) {
	return false, notServed("Client.ProcessAlive")
}

// HolderAlive reports whether the holder directory dir lives on the remote machine. It is not yet served.
func (*Client) HolderAlive(_ context.Context, _ string) (bool, error) {
	return false, notServed("Client.HolderAlive")
}
