package node

import (
	"context"
	"fmt"

	"tailscale.com/client/local"
	"tailscale.com/client/tailscale/apitype"
)

// WhoIs names the tailnet peer that a remote address belongs to. *local.Client from tailscale.com/client/local satisfies
// it, as its WhoIs method does.
type WhoIs interface {
	WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
}

// Tailnet returns the WhoIs of this machine's tailscaled, through its local client. An agent asks it who each connection's
// peer is.
func Tailnet() WhoIs {
	return &local.Client{}
}

// Authorize admits a peer only when resp, the peer's WhoIs answer, names the controller and the login. controller is
// the controller's node name on the tailnet, and login is the tailnet login its owner is signed in as. A nil resp, or
// one that names neither, is refused with an error that says why.
//
// Authorize is not implemented yet and always returns an error, so an agent admits no peer until it is.
func Authorize(_ *apitype.WhoIsResponse, _, _ string) error {
	return notImplemented("Authorize")
}

// notImplemented is the error a stub of this package returns, naming the function or method.
func notImplemented(name string) error {
	return fmt.Errorf("node: %s is not implemented", name)
}
