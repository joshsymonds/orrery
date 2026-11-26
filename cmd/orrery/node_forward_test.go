package main

import (
	"context"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"tailscale.com/tailcfg"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/exectest"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/hostrelay"
	"github.com/joshsymonds/orrery/internal/hosts"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// nodeEpics is the epic service behind the test controller. EpicListings answers that no epic exists. The other
// EpicCommands are not reached by these tests.
type nodeEpics struct {
	enginehost.EpicCommands
}

// EpicListings answers that no epic exists.
func (nodeEpics) EpicListings(context.Context) ([]store.EpicRow, error) {
	return nil, nil
}

// startNodeForwardController starts the test controller's relay on a free loopback address. The relay admits agent b of
// hosts.toml, and serves the step and epic fakes to the calls it admits, recording each one. It returns the relay's
// address and the record of its calls. The relay runs until the test ends.
func startNodeForwardController(t *testing.T) (string, *nodeCalls) {
	t.Helper()
	relayAddr := freeNodeAddr(t)
	whois := nodeWhoIs{
		relayAddr: relayAddr,
		relay:     nodeAnswer(&tailcfg.Node{ComputedName: "orreryd-box"}),
		peer:      nodeAnswer(&tailcfg.Node{Name: "b.tail1234.ts.net.", ComputedName: "b"}),
	}
	calls := &nodeCalls{}
	handler := enginehost.NewHandler(enginehost.ServeConfig{
		Steps:   nodeSteps{},
		Epics:   nodeEpics{},
		Options: []connect.HandlerOption{connect.WithInterceptors(calls.interceptor())},
	})
	relay, err := hostrelay.Listen(t.Context(), hostrelay.Config{
		Address: relayAddr,
		Hosts:   []hosts.Host{{Name: "b", Address: "b.tail1234.ts.net:7443"}},
		WhoIs:   whois,
		Handler: handler,
		Secret:  nodeSecret,
	})
	if err != nil {
		t.Fatal(err)
	}
	relayCtx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- relay.Serve(relayCtx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Error(err)
		}
	})
	return relayAddr, calls
}

// startNodeForward runs orrery node as agent b, with --relay at relayAddr and the fleet secret, and with its
// sessions.sock beside daemonSocket, the daemon socket that the global --socket names. It runs through parseNode and
// serveNode, with the session socket runNode gives it. It returns once the node has bound its TCP address, which it
// does only after it has bound sessions.sock. When the node returns before that, it returns the error and starts
// nothing. A node that started runs until the test ends.
func startNodeForward(t *testing.T, relayAddr, daemonSocket string) error {
	t.Helper()
	secret := filepath.Join(t.TempDir(), "fleet-secret")
	exectest.WriteFile(t, secret, []byte(nodeSecret+"\n"), 0o600)
	cfg, err := parseNode([]string{
		"--controller", nodeAgentController, "--relay", relayAddr, "--listen", "127.0.0.1:0",
		"--secret-file", secret,
	})
	if err != nil {
		t.Fatalf("parseNode: %v", err)
	}
	cfg.daemonSocket = daemonSocket
	cfg.serveSessions = true
	bound := make(chan struct{})
	cfg.seams = nodeSeams{
		// Serve asks WhoIs about the node's own listener, which names agent b's node under the test login.
		whoIs: nodeWhoIs{peer: nodeAnswer(&tailcfg.Node{Name: "b.tail1234.ts.net.", ComputedName: "b"})},
		listen: func(addr string) (net.Listener, error) {
			ln, listenErr := net.Listen("tcp", addr)
			if listenErr != nil {
				return nil, listenErr
			}
			close(bound)
			return ln, nil
		},
		stateDir: t.TempDir(),
	}
	getenv := envFrom(map[string]string{"HOME": t.TempDir(), "SHELL": nodeAgentLookPath(t, "sh")})
	// The node outlives t.Context, which is cancelled before the cleanups run, so that a cleanup can still stop it.
	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	done := make(chan error, 1)
	go func() { done <- serveNode(ctx, cfg, io.Discard, getenv) }()
	select {
	case <-bound:
	case serveErr := <-done:
		cancel()
		return serveErr
	}
	t.Cleanup(func() {
		cancel()
		if serveErr := <-done; serveErr != nil {
			t.Errorf("orrery node: %v", serveErr)
		}
	})
	return nil
}

// startNodeForwardAgent runs orrery node as agent b through startNodeForward, with a daemon socket of its own, and
// returns that daemon socket. The test fails when the node does not start.
func startNodeForwardAgent(t *testing.T, relayAddr string) string {
	t.Helper()
	daemonSocket := filepath.Join(t.TempDir(), "orreryd.sock")
	if err := startNodeForward(t, relayAddr, daemonSocket); err != nil {
		t.Fatalf("orrery node: %v", err)
	}
	return daemonSocket
}

// forwardGetenv returns the environment of a CLI call on agent b, which names the daemon socket beside which b's
// sessions.sock lies.
func forwardGetenv(daemonSocket string) func(string) string {
	return envFrom(map[string]string{protocol.EnvSocket: daemonSocket})
}

// requireForwardCalls checks that the controller's handler recorded exactly want.
func requireForwardCalls(t *testing.T, calls *nodeCalls, want ...nodeCall) {
	t.Helper()
	if got := calls.recorded(); !slices.Equal(got, want) {
		t.Fatalf("controller recorded calls %+v, want %+v", got, want)
	}
}

// TestNodeForwardSocketIsOwnerOnly checks that agent b's sessions.sock exists, as a socket that only its owner can use.
func TestNodeForwardSocketIsOwnerOnly(t *testing.T) {
	t.Parallel()
	relayAddr, _ := startNodeForwardController(t)
	daemonSocket := startNodeForwardAgent(t, relayAddr)
	sock := protocol.SessionSocketPath(daemonSocket)
	info, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("stat sessions.sock: %v", err)
	}
	if info.Mode().Type() != fs.ModeSocket {
		t.Fatalf("%s is %v, want a socket", sock, info.Mode().Type())
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("sessions.sock mode = %o, want 600", perm)
	}
}

// TestNodeForwardStepArrivesAsHostB checks that `orrery step status` run on agent b reaches the controller's relay
// through sessions.sock, and arrives as host b.
func TestNodeForwardStepArrivesAsHostB(t *testing.T) {
	t.Parallel()
	relayAddr, calls := startNodeForwardController(t)
	daemonSocket := startNodeForwardAgent(t, relayAddr)
	code, stdout, stderr := runNodeCLI(t, forwardGetenv(daemonSocket), "step", "status", nodeStepID)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, nodeStepID) {
		t.Fatalf("stdout %q does not name step %s", stdout, nodeStepID)
	}
	requireForwardCalls(t, calls, nodeCall{procedure: stepStatusProcedure, host: "b"})
}

// TestNodeForwardEpicArrivesAsHostB checks that `orrery epic list` run on agent b reaches the controller's relay
// through sessions.sock, and arrives as host b.
func TestNodeForwardEpicArrivesAsHostB(t *testing.T) {
	t.Parallel()
	relayAddr, calls := startNodeForwardController(t)
	daemonSocket := startNodeForwardAgent(t, relayAddr)
	code, _, stderr := runNodeCLI(t, forwardGetenv(daemonSocket), "epic", "list")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr %q", code, stderr)
	}
	requireForwardCalls(t, calls, nodeCall{procedure: enginev1connect.EpicsListEpicsProcedure, host: "b"})
}

// TestNodeForwardMCPArrivesAsHostB checks that a step_status tool call over `orrery mcp` run on agent b reaches the
// controller's relay through sessions.sock, and arrives as host b.
func TestNodeForwardMCPArrivesAsHostB(t *testing.T) {
	t.Parallel()
	relayAddr, calls := startNodeForwardController(t)
	daemonSocket := startNodeForwardAgent(t, relayAddr)
	cmd := startCallerRun(t, forwardGetenv(daemonSocket), "mcp")
	cmd.send(mcpInitialize)
	requireResult(t, cmd.receive(), 1)
	cmd.send(mcpInitialized)
	cmd.send(mcpStatus)
	requireResult(t, cmd.receive(), 3)
	if code := cmd.finish(); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr %q", code, cmd.stderr.String())
	}
	requireForwardCalls(t, calls, nodeCall{procedure: stepStatusProcedure, host: "b"})
}

// TestNodeForwardRefusesCallWithoutSecret calls the controller's relay straight from b's node, with no fleet secret.
// The fake WhoIs names the test's own connection as b's node. The relay refuses the call as unauthenticated before its
// handler runs, so the controller records no call. Only the forwarder adds the secret, so the calls of the other tests
// reach the handler only through it.
func TestNodeForwardRefusesCallWithoutSecret(t *testing.T) {
	t.Parallel()
	relayAddr, calls := startNodeForwardController(t)
	client := enginev1connect.NewStepsClient(http.DefaultClient, "http://"+relayAddr)
	_, err := client.StepStatus(t.Context(), connect.NewRequest(&v1.StepStatusRequest{Id: nodeStepID}))
	if code := connect.CodeOf(err); code != connect.CodeUnauthenticated {
		t.Fatalf("code = %v (err %v), want %v", code, err, connect.CodeUnauthenticated)
	}
	requireForwardCalls(t, calls)
}

// TestNodeForwardReplacesAStaleSocket checks that a sessions.sock file that nothing answers on is replaced, so that
// agent b starts and its CLI calls reach the controller through the new socket.
func TestNodeForwardReplacesAStaleSocket(t *testing.T) {
	t.Parallel()
	relayAddr, calls := startNodeForwardController(t)
	daemonSocket := filepath.Join(t.TempDir(), "orreryd.sock")
	sock := protocol.SessionSocketPath(daemonSocket)
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatalf("listen for the stale socket: %v", err)
	}
	stale.SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatalf("close the stale socket: %v", err)
	}
	if err := startNodeForward(t, relayAddr, daemonSocket); err != nil {
		t.Fatalf("orrery node over a stale socket: %v", err)
	}
	code, _, stderr := runNodeCLI(t, forwardGetenv(daemonSocket), "step", "status", nodeStepID)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr %q", code, stderr)
	}
	requireForwardCalls(t, calls, nodeCall{procedure: stepStatusProcedure, host: "b"})
}

// TestNodeForwardRefusesALiveSocket checks that agent b does not replace a sessions.sock that a listener answers
// on. The node returns an error naming the live listener, and the socket file stays.
func TestNodeForwardRefusesALiveSocket(t *testing.T) {
	t.Parallel()
	relayAddr, _ := startNodeForwardController(t)
	daemonSocket := filepath.Join(t.TempDir(), "orreryd.sock")
	sock := protocol.SessionSocketPath(daemonSocket)
	live, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", sock)
	if err != nil {
		t.Fatalf("listen on the live socket: %v", err)
	}
	t.Cleanup(func() {
		if err := live.Close(); err != nil {
			t.Errorf("close the live socket: %v", err)
		}
	})
	err = startNodeForward(t, relayAddr, daemonSocket)
	if err == nil || !strings.Contains(err.Error(), "live listener") {
		t.Fatalf("orrery node over a live socket: err = %v, want one naming the live listener", err)
	}
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("the live socket was removed: %v", err)
	}
}
