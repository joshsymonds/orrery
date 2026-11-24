package node

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
// the controller's node name on the tailnet, and login is the tailnet login its owner is signed in as. It refuses, with
// an error that says why, a nil resp, a response with no Node, a tagged node, a node whose ComputedName and whose Name
// with its trailing dot trimmed both differ from controller, and a response whose UserProfile is missing or names
// another login.
func Authorize(resp *apitype.WhoIsResponse, controller, login string) error {
	switch {
	case controller == "" || login == "":
		return errors.New("node: nothing to admit, the controller or the login is empty")
	case resp == nil:
		return errors.New("node: WhoIs names no peer")
	case resp.Node == nil:
		return errors.New("node: WhoIs names the peer with no node")
	case resp.Node.IsTagged():
		return fmt.Errorf("node: peer %s is a tagged node, not a device of its owner", resp.Node.Name)
	case resp.Node.ComputedName != controller && strings.TrimSuffix(resp.Node.Name, ".") != controller:
		return fmt.Errorf("node: peer %s is not the controller %s", resp.Node.Name, controller)
	case resp.UserProfile == nil:
		return fmt.Errorf("node: peer %s names no user profile", resp.Node.Name)
	case resp.UserProfile.LoginName != login:
		return fmt.Errorf("node: peer %s is signed in as %s, not %s", resp.Node.Name, resp.UserProfile.LoginName, login)
	}
	return nil
}
