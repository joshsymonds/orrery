package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"

	"github.com/joshsymonds/orrery/internal/exectest"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/holdertest"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
)

// TestMain puts a built orrery first on PATH when a run selects a test that needs it: TestNodeAgentServe and the node
// restart tests, since each session the agent launches runs under orrery holder, and TestNodeLocalVerbsDesk, since the
// desk runs orrery as a process (see holdertest.Main).
func TestMain(m *testing.M) {
	os.Exit(holdertest.Main(m, "TestNodeAgentServe", "TestNodeRestart", "TestNodeLocalVerbsDesk"))
}

const (
	// nodeAgentSecret is the fleet secret that the secret file of each agent holds.
	nodeAgentSecret = "fleet-secret-for-node-agent-tests"
	// nodeAgentController is the controller's node name, and nodeAgentOwner the login its node is signed in as.
	nodeAgentController = "controller-node"
	nodeAgentOwner      = "owner@example.test"
	// nodeAgentStranger is a login other than the owner's, and nodeAgentOtherNode a node of the owner that is not the
	// controller.
	nodeAgentStranger  = "stranger@example.test"
	nodeAgentOtherNode = "other-node"
	// nodeAgentName is the agent's own node name.
	nodeAgentName = "agent-node"
	// nodeAgentRelay is the --relay value. Nothing in these tests dials it.
	nodeAgentRelay = "127.0.0.1:7777"
	// nodeAgentSession is the session that the tests launch.
	nodeAgentSession = "agent-launch"
	// nodeAgentPeerNode is the loopback address that dials as a node of the owner that is not the controller, and
	// nodeAgentPeerLogin the one that dials as the controller under another login.
	nodeAgentPeerNode  = "127.0.0.2"
	nodeAgentPeerLogin = "127.0.0.3"
)

// nodeAgentFakeClaude stands in for claude. It writes two stdout lines, and then reads its stdin until the stdin ends.
const nodeAgentFakeClaude = `#!/bin/sh
printf '%s\n' '{"type":"system","subtype":"init"}'
printf '%s\n' '{"type":"fake","n":2}'
while IFS= read -r _; do :; done
`

// nodeAgentTailnet is the WhoIs of these tests, which names each peer by its loopback address. The agent's own address
// names the agent's node under the owner's login. A connection from the controller's address, 127.0.0.1, names the
// controller under the owner's login. The other two loopback addresses name a node that is not the controller, and the
// controller under another login.
type nodeAgentTailnet struct {
	// own is the address the agent listens on. The listen seam sets it before the agent asks WhoIs of anything.
	own string
}

// WhoIs names the peer at addr, as tailscaled would.
func (t *nodeAgentTailnet) WhoIs(_ context.Context, addr string) (*apitype.WhoIsResponse, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("split %s: %w", addr, err)
	}
	switch {
	case addr == t.own:
		return nodeAgentAnswer(nodeAgentName, nodeAgentOwner), nil
	case host == nodeAgentPeerNode:
		return nodeAgentAnswer(nodeAgentOtherNode, nodeAgentOwner), nil
	case host == nodeAgentPeerLogin:
		return nodeAgentAnswer(nodeAgentController, nodeAgentStranger), nil
	default:
		return nodeAgentAnswer(nodeAgentController, nodeAgentOwner), nil
	}
}

// nodeAgentAnswer is the WhoIs answer for a node named name, signed in as login.
func nodeAgentAnswer(name, login string) *apitype.WhoIsResponse {
	return &apitype.WhoIsResponse{
		Node:        &tailcfg.Node{ComputedName: name, Name: name + ".tail1234.ts.net."},
		UserProfile: &tailcfg.UserProfile{LoginName: login},
	}
}

// nodeAgentNeverClock is the Clock of the test clients. Its timers never fire, so no op ends on a bound: each op ends
// with the agent's answer, which the test waits on.
type nodeAgentNeverClock struct{}

// After returns a timer that never fires.
func (nodeAgentNeverClock) After(time.Duration) <-chan time.Time {
	return make(chan time.Time)
}

// nodeAgentRun is an agent that a test started through orrery node. Its listener is at addr, and its holders keep their
// sessions under stateDir.
type nodeAgentRun struct {
	addr     string
	stateDir string
}

// client returns the node.Client that the controller uses to reach the agent, holding nodeAgentSecret.
func (r nodeAgentRun) client() *node.Client {
	return node.NewClient(node.ClientConfig{
		Name:    nodeAgentName,
		Address: r.addr,
		Dial:    nodeAgentDial(""),
		Clock:   nodeAgentNeverClock{},
		Secret:  nodeAgentSecret,
	})
}

// startNodeAgent runs orrery node in this process, through parseNode and serveNode. Its flags name the controller, the
// relay, a listener on a loopback port, a secret file that holds nodeAgentSecret, and one --root for each of roots. Its
// seams are nodeAgentTailnet for WhoIs, a listener that reports the address it bound, a state directory of its own, and
// the fake claude. It returns once the agent listens, and the agent serves until the test ends.
func startNodeAgent(t *testing.T, server tmuxctl.Server, roots ...string) nodeAgentRun {
	t.Helper()
	sh := nodeAgentLookPath(t, "sh")
	claude := filepath.Join(t.TempDir(), "claude")
	exectest.WriteFile(t, claude, []byte(nodeAgentFakeClaude), 0o700)
	args := make([]string, 0, 8+2*len(roots))
	args = append(args,
		"--controller", nodeAgentController, "--relay", nodeAgentRelay, "--listen", "127.0.0.1:0",
		"--secret-file", nodeAgentSecretFile(t, nodeAgentSecret))
	for _, root := range roots {
		args = append(args, "--root", root)
	}
	cfg, err := parseNode(args)
	if err != nil {
		t.Fatalf("parseNode: %v", err)
	}
	cfg.server = server
	tailnet := &nodeAgentTailnet{}
	listening := make(chan string, 1)
	stateDir := t.TempDir()
	cfg.seams = nodeSeams{
		whoIs: tailnet,
		listen: func(addr string) (net.Listener, error) {
			ln, listenErr := net.Listen("tcp", addr)
			if listenErr != nil {
				return nil, listenErr
			}
			tailnet.own = ln.Addr().String()
			listening <- tailnet.own
			return ln, nil
		},
		stateDir: stateDir,
		claude:   claude,
	}
	getenv := envFrom(map[string]string{"HOME": t.TempDir(), "SHELL": sh})
	// The agent outlives t.Context, which is cancelled before the cleanups run, so that a cleanup can still reach
	// it. The cleanup below ends it.
	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	done := make(chan error, 1)
	go func() { done <- serveNode(ctx, cfg, io.Discard, getenv) }()
	var addr string
	select {
	case addr = <-listening:
	case serveErr := <-done:
		t.Fatalf("orrery node returned before it listened: %v", serveErr)
	}
	t.Cleanup(func() {
		cancel()
		if serveErr := <-done; serveErr != nil {
			t.Errorf("orrery node: %v", serveErr)
		}
	})
	return nodeAgentRun{addr: addr, stateDir: stateDir}
}

// nodeAgentLookPath returns the path of the program name, which the test fails without.
func nodeAgentLookPath(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("find %s: %v", name, err)
	}
	return path
}

// nodeAgentSecretFile writes secret to a new file that only its owner can read, and returns the file's path.
func nodeAgentSecretFile(t *testing.T, secret string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fleet-secret")
	exectest.WriteFile(t, path, []byte(secret+"\n"), 0o600)
	return path
}

// nodeAgentDial returns the dialer of a connection from the loopback address from, or from an address the kernel
// chooses when from is empty.
func nodeAgentDial(from string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		var dialer net.Dialer
		if from != "" {
			dialer.LocalAddr = &net.TCPAddr{IP: net.ParseIP(from)}
		}
		conn, err := dialer.DialContext(ctx, network, address)
		if err != nil {
			return nil, fmt.Errorf("dial %s from %q: %w", address, from, err)
		}
		return conn, nil
	}
}

// nodeAgentCall sends req from the loopback address from to the agent at addr, as a controller's raw request would, and
// returns the Response that answers it. An error means the agent closed the connection with no answer.
func nodeAgentCall(t *testing.T, from, addr string, req node.Request) (node.Response, error) {
	t.Helper()
	conn, err := nodeAgentDial(from)(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatalf("dial the agent: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err = json.NewEncoder(conn).Encode(req); err != nil {
		return node.Response{}, fmt.Errorf("send %s: %w", req.Op, err)
	}
	var resp node.Response
	if err = json.NewDecoder(conn).Decode(&resp); err != nil {
		return node.Response{}, fmt.Errorf("read the answer to %s: %w", req.Op, err)
	}
	return resp, nil
}

// nodeAgentWithout returns args with the flag name and its value taken out.
func nodeAgentWithout(args []string, name string) []string {
	i := slices.Index(args, name)
	return slices.Concat(args[:i], args[i+2:])
}

// nodeAgentWith returns args with the value of the flag name replaced by value.
func nodeAgentWith(args []string, name, value string) []string {
	out := slices.Clone(args)
	out[slices.Index(out, name)+1] = value
	return out
}

// nodeAgentHolderDir is the holder directory of session id under the agent's state directory.
func nodeAgentHolderDir(agent nodeAgentRun, id string) string {
	return filepath.Join(agent.stateDir, "holders", id)
}

// requireNoHolder fails the test when the agent made a holder directory for session id, which a refused call must not.
func requireNoHolder(t *testing.T, agent nodeAgentRun, id string) {
	t.Helper()
	_, err := os.Stat(nodeAgentHolderDir(agent, id))
	switch {
	case err == nil:
		t.Fatalf("holder directory of %s exists, want none: a refused call ran an op", id)
	case !errors.Is(err, fs.ErrNotExist):
		t.Fatalf("check the holder directory of %s: %v", id, err)
	}
}

// nodeAgentTmux starts a detached session on a private tmux server, which the test stops at its end, and returns the
// server and the id of the session's one pane. tmux prints its list-panes separator only under a UTF-8 locale, so the
// test sets one, as serve's tests do.
func nodeAgentTmux(t *testing.T) (tmuxctl.Server, string) {
	t.Helper()
	t.Setenv("LC_ALL", "C.UTF-8")
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	server := tmuxctl.Server{Name: fmt.Sprintf("orrery-node-agent-%d-%x", os.Getpid(), suffix)}
	t.Cleanup(func() {
		if err := tmuxtest.Stop(t, server); err != nil {
			t.Errorf("stop tmux: %v", err)
		}
	})
	client, err := tmuxctl.New(server)
	if err != nil {
		t.Fatalf("tmuxctl.New: %v", err)
	}
	if _, err = client.Run(t.Context(), "-f", "/dev/null", "new-session", "-d", "-s", "node-agent",
		"-c", t.TempDir(), "sleep 300"); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	out, err := client.Run(t.Context(), "list-panes", "-t", "node-agent", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	return server, strings.TrimSpace(out)
}

// TestNodeAgentServeAnswersPing asks the agent for a ping with the fleet secret, which it answers without a failure,
// and then for its sessions, which are none before any launch.
func TestNodeAgentServeAnswersPing(t *testing.T) {
	agent := startNodeAgent(t, tmuxctl.Server{})
	ping, err := nodeAgentCall(t, "", agent.addr, node.Request{
		Version: node.Version, Op: node.OpPing, Secret: nodeAgentSecret,
	})
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if ping.Code != "" {
		t.Fatalf("ping code = %q (%s), want none", ping.Code, ping.Message)
	}
	sessions, err := agent.client().Sessions(t.Context())
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions = %v, want none before a launch", sessions)
	}
}

// TestNodeAgentServeLaunchesAndAttaches has the controller launch a session on the agent, lists it among the sessions,
// and then attaches to its holder, which the agent splices onto the holder's socket.
func TestNodeAgentServeLaunchesAndAttaches(t *testing.T) {
	agent := startNodeAgent(t, tmuxctl.Server{})
	client := agent.client()
	sess, err := client.Launch(t.Context(), holderhost.Spec{Session: nodeAgentSession, Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Kill(context.WithoutCancel(t.Context()), nodeAgentSession); err != nil {
			t.Errorf("kill %s: %v", nodeAgentSession, err)
		}
	})
	if err = sess.Detach(); err != nil {
		t.Fatalf("detach the launched session: %v", err)
	}
	sessions, err := client.Sessions(t.Context())
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if !slices.Contains(sessions, nodeAgentSession) {
		t.Fatalf("sessions = %v, want %s among them", sessions, nodeAgentSession)
	}
	process, _, err := client.Reattach(t.Context(), nodeAgentSession, 0, holder.WireVersion)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err = process.Detach(); err != nil {
		t.Fatalf("detach the attached session: %v", err)
	}
}

// TestNodeAgentServeListsAndWatchesPanes lists the panes of the agent's tmux server through the controller, and then
// opens the pane watch, whose first event is a full listing that names the same pane.
func TestNodeAgentServeListsAndWatchesPanes(t *testing.T) {
	server, pane := nodeAgentTmux(t)
	agent := startNodeAgent(t, server)
	client := agent.client()
	listed, err := client.Panes().List(t.Context(), connect.NewRequest(&orreryv1.PanesListRequest{}))
	if err != nil {
		t.Fatalf("list panes: %v", err)
	}
	if !slices.ContainsFunc(listed.Msg.GetPanes(), func(p *orreryv1.Pane) bool { return p.GetId() == pane }) {
		t.Fatalf("listed panes %v, want pane %s among them", listed.Msg.GetPanes(), pane)
	}
	watch, err := client.WatchPanes(t.Context())
	if err != nil {
		t.Fatalf("watch panes: %v", err)
	}
	t.Cleanup(func() {
		if err := watch.Close(); err != nil {
			t.Errorf("close the pane watch: %v", err)
		}
	})
	event, err := watch.Next()
	if err != nil {
		t.Fatalf("first pane event: %v", err)
	}
	if !event.Full || !slices.ContainsFunc(event.Listing, func(p node.PaneSnapshot) bool { return p.Pane == pane }) {
		t.Fatalf("first pane event = %+v, want a full listing that names pane %s", event, pane)
	}
}

// TestNodeAgentServeReportsLoadAndFolders asks the agent for its load, and for its folders under each of two roots.
func TestNodeAgentServeReportsLoadAndFolders(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	wantFirst := filepath.Join(first, "alpha")
	wantSecond := filepath.Join(second, "beta")
	for _, dir := range []string{wantFirst, wantSecond} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatalf("make folder: %v", err)
		}
	}
	agent := startNodeAgent(t, tmuxctl.Server{}, first, second)
	client := agent.client()
	load, err := client.Load(t.Context())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if load.MemoryPercent <= 0 || load.MemoryPercent > 100 {
		t.Errorf("memory percent = %v, want within (0, 100]", load.MemoryPercent)
	}
	folders, err := client.Folders(t.Context())
	if err != nil {
		t.Fatalf("folders: %v", err)
	}
	for _, want := range []string{wantFirst, wantSecond} {
		if !slices.Contains(folders, want) {
			t.Errorf("folders = %v, want %s among them", folders, want)
		}
	}
}

// TestNodeAgentServeWakesAPane asks the agent to wake a pane of its tmux server through the controller. The pane runs
// sleep, not an idle claude, so the agent answers that it typed nothing, and with no failure.
func TestNodeAgentServeWakesAPane(t *testing.T) {
	server, pane := nodeAgentTmux(t)
	agent := startNodeAgent(t, server)
	woke, err := agent.client().WakePane(t.Context(), server.Column(), pane, "wake up")
	if err != nil {
		t.Fatalf("wake pane: %v", err)
	}
	if woke {
		t.Fatalf("woke pane %s, want nothing typed into a pane that runs no idle claude", pane)
	}
}

// TestNodeAgentServeRefusesACallWithoutTheSecret sends a launch from the controller's address with no secret, and then
// with another secret. Each is answered as refused, and neither makes a holder, so no op ran.
func TestNodeAgentServeRefusesACallWithoutTheSecret(t *testing.T) {
	agent := startNodeAgent(t, tmuxctl.Server{})
	launch := node.LaunchSpec{Dir: t.TempDir()}
	for _, tc := range []struct{ name, secret string }{
		{name: "no secret", secret: ""},
		{name: "another secret", secret: "another-fleet-secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := nodeAgentCall(t, "", agent.addr, node.Request{
				Version: node.Version, Op: node.OpLaunch, Session: nodeAgentSession, Launch: &launch, Secret: tc.secret,
			})
			if err != nil {
				t.Fatalf("launch: %v", err)
			}
			if resp.Code != node.CodeFailed {
				t.Fatalf("launch code = %q, want %q", resp.Code, node.CodeFailed)
			}
			requireNoHolder(t, agent, nodeAgentSession)
		})
	}
}

// TestNodeAgentServeRefusesAPeerThatIsNotTheController asks the agent from a node of the owner that is not the
// controller, with the fleet secret. The agent closes that connection with no answer, and makes no holder. The
// controller's own call first shows that the same request is answered from the controller's address.
func TestNodeAgentServeRefusesAPeerThatIsNotTheController(t *testing.T) {
	agent := startNodeAgent(t, tmuxctl.Server{})
	ping := node.Request{Version: node.Version, Op: node.OpPing, Secret: nodeAgentSecret}
	if _, err := nodeAgentCall(t, "", agent.addr, ping); err != nil {
		t.Fatalf("the controller's ping: %v", err)
	}
	launch := node.Request{
		Version: node.Version, Op: node.OpLaunch, Session: nodeAgentSession,
		Launch: &node.LaunchSpec{Dir: t.TempDir()}, Secret: nodeAgentSecret,
	}
	if _, err := nodeAgentCall(t, nodeAgentPeerNode, agent.addr, launch); err == nil {
		t.Fatal("a node that is not the controller got an answer")
	}
	requireNoHolder(t, agent, nodeAgentSession)
}

// TestNodeAgentServeRefusesAnotherLogin asks the agent as the controller node, under another login, with the fleet
// secret. The agent closes that connection with no answer, and makes no holder.
func TestNodeAgentServeRefusesAnotherLogin(t *testing.T) {
	agent := startNodeAgent(t, tmuxctl.Server{})
	launch := node.Request{
		Version: node.Version, Op: node.OpLaunch, Session: nodeAgentSession,
		Launch: &node.LaunchSpec{Dir: t.TempDir()}, Secret: nodeAgentSecret,
	}
	if _, err := nodeAgentCall(t, nodeAgentPeerLogin, agent.addr, launch); err == nil {
		t.Fatal("the controller under another login got an answer")
	}
	requireNoHolder(t, agent, nodeAgentSession)
}

// TestNodeAgentServeNeedsItsFlagsAndSecretFile runs orrery node through run with each start that the verb refuses. A
// missing --controller, --relay or --listen is a usage error that names the flag, and a --secret-file that is unset or
// that node.ReadSecret refuses fails with an error that names the flag or the file. None of them starts an agent.
func TestNodeAgentServeNeedsItsFlagsAndSecretFile(t *testing.T) {
	secret := nodeAgentSecretFile(t, nodeAgentSecret)
	loose := filepath.Join(t.TempDir(), "loose-secret")
	exectest.WriteFile(t, loose, []byte(nodeAgentSecret+"\n"), 0o644)
	missing := filepath.Join(t.TempDir(), "absent-secret")
	full := []string{
		"--controller", nodeAgentController, "--relay", nodeAgentRelay, "--listen", "127.0.0.1:0",
		"--secret-file", secret,
	}
	cases := []struct {
		name string
		args []string
		code int
		want string
	}{
		{name: "no controller", args: nodeAgentWithout(full, "--controller"), code: exitUsage, want: "--controller"},
		{name: "no relay", args: nodeAgentWithout(full, "--relay"), code: exitUsage, want: "--relay"},
		{name: "no listen", args: nodeAgentWithout(full, "--listen"), code: exitUsage, want: "--listen"},
		{name: "no secret file", args: nodeAgentWithout(full, "--secret-file"), code: exitUsage, want: "--secret-file"},
		{
			name: "secret file missing",
			args: nodeAgentWith(full, "--secret-file", missing),
			code: exitFailure, want: missing,
		},
		{
			name: "secret file readable by others",
			args: nodeAgentWith(full, "--secret-file", loose),
			code: exitFailure, want: loose,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(append([]string{"node"}, tc.args...), nil, &stdout, &stderr, envFrom(nil))
			if code != tc.code {
				t.Fatalf("exit = %d, want %d; stderr %q", code, tc.code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("stderr = %q, want it to name %q", stderr.String(), tc.want)
			}
		})
	}
}
