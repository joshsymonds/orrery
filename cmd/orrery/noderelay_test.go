package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/enginehost"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/hostrelay"
	"github.com/joshsymonds/orrery/internal/hosts"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// The names of the test controller's sessions and steps. nodeStepID is a step that no session claim names. nodeSessionC
// is a session that runs on host c, and nodeStepC is a step that runs on host c.
const (
	nodeLogin    = "josh@example.com"
	nodeStepID   = "step-1"
	nodeSessionC = "session-on-c"
	nodeStepC    = "step-on-c"
)

// nodeWhoIs names each peer of the test relay, as tailscaled's WhoIs names a connection's peer: the relay's own address
// is the controller, and every other address is agent b, since agent b's forwarder makes every connection to the relay a
// test opens. The forwarder dials from whatever port the kernel gives it. Dialing from one fixed port made a second
// connection reuse the address pair of the first, which the kernel holds in TIME_WAIT for 60 seconds.
type nodeWhoIs struct {
	relayAddr string
	relay     *apitype.WhoIsResponse
	peer      *apitype.WhoIsResponse
}

func (w nodeWhoIs) WhoIs(_ context.Context, addr string) (*apitype.WhoIsResponse, error) {
	if addr == w.relayAddr {
		return w.relay, nil
	}
	return w.peer, nil
}

// nodeAnswer is the WhoIs answer for a peer whose node is node, signed in as the owner.
func nodeAnswer(node *tailcfg.Node) *apitype.WhoIsResponse {
	return &apitype.WhoIsResponse{
		Node:        node,
		UserProfile: &tailcfg.UserProfile{LoginName: nodeLogin},
	}
}

// nodeSessions is the session store behind the test controller: it names the host that runs each session it knows.
type nodeSessions map[string]string

func (s nodeSessions) SessionHost(_ context.Context, id string) (string, bool, error) {
	host, ok := s[id]
	return host, ok, nil
}

// nodeSteps is the step store behind the test controller. Status answers each step by its id, and Held holds none. The
// other StepCommands are not reached by these tests.
type nodeSteps struct {
	enginehost.StepCommands
}

func (nodeSteps) Status(_ context.Context, id string) (store.StepRow, error) {
	return store.StepRow{ID: id}, nil
}

func (nodeSteps) Held(string) bool {
	return false
}

// nodeCall is one Connect call that reached the controller's handler: its procedure, and the Orrery-Host it carried.
type nodeCall struct {
	procedure string
	host      string
}

// nodeCalls records the calls that reach the controller's handler. Its interceptor runs before the service, so a call
// that the relay refuses is never recorded.
type nodeCalls struct {
	mu   sync.Mutex
	seen []nodeCall
}

// interceptor returns the interceptor that records each unary call before it is served.
func (c *nodeCalls) interceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			c.mu.Lock()
			c.seen = append(c.seen, nodeCall{
				procedure: req.Spec().Procedure,
				host:      req.Header().Get(protocol.HeaderHost),
			})
			c.mu.Unlock()
			return next(ctx, req)
		}
	}
}

// recorded returns the calls recorded so far.
func (c *nodeCalls) recorded() []nodeCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.seen)
}

// nodeRelay is a test controller whose relay admits agents b and c of hosts.toml, and agent b, whose sessions.sock
// forwards to the relay.
type nodeRelay struct {
	calls        *nodeCalls
	daemonSocket string // the daemon socket that names agent b's sessions.sock beside it
	sock         string // agent b's sessions.sock
}

// freeNodeAddr returns a loopback address that no socket holds, for a listener or a client to use.
func freeNodeAddr(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// startNodeRelay starts the test controller's relay, which serves a handler of recording fakes, and agent b's
// sessions.sock, which forwards to the relay as agent b. Both run until the test ends.
func startNodeRelay(t *testing.T) *nodeRelay {
	t.Helper()
	relayAddr := freeNodeAddr(t)
	whois := nodeWhoIs{
		relayAddr: relayAddr,
		relay:     nodeAnswer(&tailcfg.Node{ComputedName: "orreryd-box"}),
		peer: nodeAnswer(&tailcfg.Node{
			Name:         "b.tail1234.ts.net.",
			ComputedName: "b",
		}),
	}
	calls := &nodeCalls{}
	handler := enginehost.NewHandler(enginehost.ServeConfig{
		Steps:   nodeSteps{},
		Options: []connect.HandlerOption{connect.WithInterceptors(calls.interceptor())},
	})
	sessions := nodeSessions{nodeSessionC: "c", nodeStepC: "c"}
	relay, err := hostrelay.Listen(t.Context(), hostrelay.Config{
		Address: relayAddr,
		Hosts: []hosts.Host{
			{Name: "b", Address: "b.tail1234.ts.net:7443"},
			{Name: "c", Address: "c.tail1234.ts.net:7443"},
		},
		WhoIs:    whois,
		Sessions: sessions,
		Handler:  handler,
	})
	if err != nil {
		t.Fatal(err)
	}
	relayCtx, cancelRelay := context.WithCancel(t.Context())
	relayServed := make(chan error, 1)
	go func() { relayServed <- relay.Serve(relayCtx) }()
	t.Cleanup(func() {
		cancelRelay()
		if err := <-relayServed; err != nil {
			t.Error(err)
		}
	})

	daemonSocket := filepath.Join(t.TempDir(), "orreryd.sock")
	sock := protocol.SessionSocketPath(daemonSocket)
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	forwardCtx, cancelForward := context.WithCancel(t.Context())
	forwardCfg := hostrelay.ForwardConfig{Address: relayAddr, Dial: (&net.Dialer{}).DialContext}
	forwarded := make(chan error, 1)
	go func() { forwarded <- hostrelay.Forward(forwardCtx, ln, forwardCfg) }()
	t.Cleanup(func() {
		cancelForward()
		if err := <-forwarded; err != nil {
			t.Error(err)
		}
	})
	return &nodeRelay{calls: calls, daemonSocket: daemonSocket, sock: sock}
}

// getenv returns the environment of a CLI call on agent b: the daemon socket that names agent b's sessions.sock, and
// session as the ORRERY_SESSION of the session that makes the call, or none when session is empty.
func (r *nodeRelay) getenv(session string) func(string) string {
	values := map[string]string{protocol.EnvSocket: r.daemonSocket}
	if session != "" {
		values[protocol.EnvSession] = session
	}
	return envFrom(values)
}

// requireCalls checks that the controller's handler recorded exactly want.
func (r *nodeRelay) requireCalls(t *testing.T, want ...nodeCall) {
	t.Helper()
	if got := r.calls.recorded(); !slices.Equal(got, want) {
		t.Fatalf("controller recorded calls %+v, want %+v", got, want)
	}
}

// runNodeCLI runs `orrery args` in-process with getenv as its environment, and returns its exit code, stdout and
// stderr.
func runNodeCLI(t *testing.T, getenv func(string) string, args ...string) (int, string, string) {
	t.Helper()
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { closeFile(t, stdin) })
	var stdout, stderr bytes.Buffer
	code := run(args, stdin, &stdout, &stderr, getenv)
	return code, stdout.String(), stderr.String()
}

// stepStatusProcedure is the procedure of the Steps call that `orrery step status` and step_status make.
const stepStatusProcedure = enginev1connect.StepsStepStatusProcedure

// TestNodeRelayCLIStepArrivesAsHost checks that `orrery step status` run on agent b reaches the controller as host b.
func TestNodeRelayCLIStepArrivesAsHost(t *testing.T) {
	t.Parallel()
	r := startNodeRelay(t)
	code, stdout, stderr := runNodeCLI(t, r.getenv(""), "step", "status", nodeStepID)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, nodeStepID) {
		t.Fatalf("stdout %q does not name step %s", stdout, nodeStepID)
	}
	r.requireCalls(t, nodeCall{procedure: stepStatusProcedure, host: "b"})
}

// TestNodeRelayMCPToolArrivesAsHost checks that a step_status tool call over `orrery mcp` on agent b reaches the
// controller as host b.
func TestNodeRelayMCPToolArrivesAsHost(t *testing.T) {
	t.Parallel()
	r := startNodeRelay(t)
	cmd := startCallerRun(t, r.getenv(""), "mcp")
	cmd.send(mcpInitialize)
	requireResult(t, cmd.receive(), 1)
	cmd.send(mcpInitialized)
	cmd.send(mcpStatus)
	requireResult(t, cmd.receive(), 3)
	if code := cmd.finish(); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr %q", code, cmd.stderr.String())
	}
	r.requireCalls(t, nodeCall{procedure: stepStatusProcedure, host: "b"})
}

// TestNodeRelayIgnoresStatedHost checks that a call that carries Orrery-Host: c through agent b's socket reaches the
// controller as host b, the host its connection was admitted as.
func TestNodeRelayIgnoresStatedHost(t *testing.T) {
	t.Parallel()
	r := startNodeRelay(t)
	client := enginev1connect.NewStepsClient(cli.SessionHTTPClient(r.sock), cli.SessionBaseURL)
	req := connect.NewRequest(&v1.StepStatusRequest{Id: nodeStepID})
	req.Header().Set(protocol.HeaderHost, "c")
	if _, err := client.StepStatus(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	r.requireCalls(t, nodeCall{procedure: stepStatusProcedure, host: "b"})
}

// TestNodeRelayRefusesSessionClaimOnOtherHost checks that a CLI call from agent b in a session that runs on host c
// fails as permission_denied, and that the controller sees no call. `orrery epic get` sends the session as its
// Orrery-Session, which `orrery step` does not.
func TestNodeRelayRefusesSessionClaimOnOtherHost(t *testing.T) {
	t.Parallel()
	r := startNodeRelay(t)
	code, _, stderr := runNodeCLI(t, r.getenv(nodeSessionC), "epic", "get", epicTestID)
	if code == 0 {
		t.Fatal("exit code = 0, want a refusal")
	}
	if !strings.Contains(stderr, connect.CodePermissionDenied.String()) {
		t.Fatalf("stderr %q does not name %s", stderr, connect.CodePermissionDenied)
	}
	r.requireCalls(t)
}

// TestNodeRelayServesTargetOnOtherHost checks that a call with no session in its environment that names a step stored
// on host c is served: the step it names is the target of the call, not a claim on it.
func TestNodeRelayServesTargetOnOtherHost(t *testing.T) {
	t.Parallel()
	r := startNodeRelay(t)
	code, stdout, stderr := runNodeCLI(t, r.getenv(""), "step", "status", nodeStepC)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, nodeStepC) {
		t.Fatalf("stdout %q does not name step %s", stdout, nodeStepC)
	}
	r.requireCalls(t, nodeCall{procedure: stepStatusProcedure, host: "b"})
}
