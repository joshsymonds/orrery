package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"syscall"
	"testing"

	"connectrpc.com/connect"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// Synthetic values only. relayAgentName is the hosts.toml agent the relay admits, relayAgentFQDN is the node that agent
// is on the tailnet, and relayOwnerLogin is the login the controller and that node are both signed in as.
const (
	relayAgentName  = "b"
	relayAgentFQDN  = "synthetic-host-b.example"
	relayOwnerLogin = "synthetic-owner@example.com"
	relayHostsFile  = `[[hosts]]
name = "` + relayAgentName + `"
address = "` + relayAgentFQDN + `:7443"
`
)

// relayWhoIs is the WhoIs seam of the relay. It names the relay's own address as the controller's node, and every other
// address as the peer node, and signs both in as relayOwnerLogin.
type relayWhoIs struct {
	own  string
	peer *tailcfg.Node
}

// WhoIs implements node.WhoIs.
func (w relayWhoIs) WhoIs(_ context.Context, addr string) (*apitype.WhoIsResponse, error) {
	peerNode := w.peer
	if addr == w.own {
		peerNode = &tailcfg.Node{ComputedName: "synthetic-controller"}
	}
	return &apitype.WhoIsResponse{
		Node:        peerNode,
		UserProfile: &tailcfg.UserProfile{LoginName: relayOwnerLogin},
	}, nil
}

// relayFreeAddr returns a loopback address that no socket holds, for the relay to listen on.
func relayFreeAddr(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find a free loopback address: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("release the free loopback address: %v", err)
	}
	return addr
}

// relayHTTP returns an HTTP client that dials addr over TCP, as an agent's peer dials the relay.
func relayHTTP(t *testing.T, addr string) *http.Client {
	t.Helper()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "tcp", addr)
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport}
}

// TestRelayAuthOrrerydServesListedAgent checks that a --sessions orreryd with --relay-listen and a hosts file that
// lists agent b answers a Connect call through the relay address, from a peer that WhoIs names as b, signed in as the
// owner.
func TestRelayAuthOrrerydServesListedAgent(t *testing.T) {
	getenv, root := hostMigrationEnv(t, relayHostsFile)
	relayAddr := relayFreeAddr(t)
	args := append(hostMigrationSessionArgs(t, filepath.Join(root, "orreryd.sock")), "--relay-listen", relayAddr)
	opts, err := parseFlags(args, getenv)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	opts.relayWhoIs = relayWhoIs{own: relayAddr, peer: &tailcfg.Node{Name: relayAgentFQDN + "."}}
	startHostMigrationServe(t, getenv, opts)

	client := enginev1connect.NewEpicsClient(relayHTTP(t, relayAddr), "http://epics.invalid")
	if _, err := client.ListEpics(t.Context(), connect.NewRequest(&v1.ListEpicsRequest{})); err != nil {
		t.Fatalf("ListEpics through the relay at %s: %v", relayAddr, err)
	}
}

// TestRelayAuthOrrerydNoHostsFileOpensNothing checks that a --sessions orreryd with --relay-listen and no hosts file
// serves its sessions socket, and opens no relay: a dial of the relay address is refused.
func TestRelayAuthOrrerydNoHostsFileOpensNothing(t *testing.T) {
	getenv, root := hostMigrationEnv(t, "")
	relayAddr := relayFreeAddr(t)
	socket := filepath.Join(root, "orreryd.sock")
	args := append(hostMigrationSessionArgs(t, socket), "--relay-listen", relayAddr)
	opts, err := parseFlags(args, getenv)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	startHostMigrationServe(t, getenv, opts)

	epics := storeRetryEpics(t, protocol.SessionSocketPath(socket))
	if _, err := epics.ListEpics(t.Context(), connect.NewRequest(&v1.ListEpicsRequest{})); err != nil {
		t.Fatalf("ListEpics on the sessions socket: %v", err)
	}
	conn, dialErr := (&net.Dialer{}).DialContext(t.Context(), "tcp", relayAddr)
	if dialErr == nil {
		if err := conn.Close(); err != nil {
			t.Errorf("close the connection to %s: %v", relayAddr, err)
		}
		t.Fatalf("dial of %s succeeded: a relay listens with no hosts file", relayAddr)
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) {
		t.Fatalf("dial of %s = %v, want connection refused", relayAddr, dialErr)
	}
}
