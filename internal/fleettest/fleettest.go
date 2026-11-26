// Package fleettest starts a fleet of node agents over real loopback listeners, with a controller and a bare origin git
// repository, for the tests of several packages that run work across machines. It is imported only by tests.
package fleettest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"

	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/node"
)

const (
	// controllerName is the controller's node name, which every agent admits.
	controllerName = "controller"
	// fleetSecret is the fleet secret that every agent and client of the fleet shares.
	fleetSecret = "fleet-secret"
	// fleetLogin is the login that every node of the fleet is signed in as.
	fleetLogin = "owner@fleet.test"
	// clientBound is the Bound of each client. The clients run on the fleet's clock, so it sets no wall-clock wait.
	clientBound = 3 * time.Second
	// maxClockRounds bounds how many times WaitReach fires a stopped agent's clock. A stopped agent's client reads
	// unreachable within a round or two, so a client that has not changed after this many rounds has failed.
	maxClockRounds = 100
)

// Fleet is a controller and the agents that a test runs its work over.
type Fleet struct {
	// Controller is the controller's node name, "controller".
	Controller string
	// Hosts resolves "controller" to this machine's hostexec.Local, and each agent's name to a node.RemoteHost of the
	// agent's client.
	Hosts hostexec.Map
	// Clients holds the client of each agent, by the agent's name.
	Clients map[string]*node.Client
	// Roots holds the root of each agent, by the agent's name. An agent's git and file ops act under its own root.
	Roots map[string]string
	// Origin is a bare git repository whose main holds one commit at the start. PushCommit adds commits to it.
	Origin string
	// agents holds each started agent, by the agent's name.
	agents map[string]*agentRig
}

// agentRig is one started agent: its listener, the cancel and the end of its Serve, its client and the clock of that
// client, and whether it has been stopped.
type agentRig struct {
	listener net.Listener
	cancel   context.CancelFunc
	served   chan error
	client   *node.Client
	clock    *fleetClock
	stopped  atomic.Bool
	stop     sync.Once
}

// Start starts one node.Agent per name in agents, each on its own 127.0.0.1:0 listener. It returns the Fleet of them,
// with a bare Origin that holds one commit on main. Each agent is stopped when tb ends.
func Start(tb testing.TB, agents ...string) *Fleet {
	tb.Helper()
	base := tb.TempDir()
	fleet := &Fleet{
		Controller: controllerName,
		Hosts:      hostexec.Map{controllerName: hostexec.NewLocal()},
		Clients:    make(map[string]*node.Client, len(agents)),
		Roots:      make(map[string]string, len(agents)),
		Origin:     filepath.Join(base, "origin.git"),
		agents:     make(map[string]*agentRig, len(agents)),
	}
	runGit(tb, base, "init", "-q", "--bare", "--initial-branch=main", "origin.git")
	fleet.PushCommit(tb, "initial commit")
	for _, name := range agents {
		fleet.startAgent(tb, name)
	}
	return fleet
}

// startAgent starts the agent named name over its own listener, and registers its client, root and host in f.
func (f *Fleet) startAgent(tb testing.TB, name string) {
	tb.Helper()
	listener, err := (&net.ListenConfig{}).Listen(tb.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("listen for agent %s: %v", name, err)
	}
	addr := listener.Addr().String()
	root := tb.TempDir()
	stateDir := shortDir(tb)
	agent, err := node.NewAgent(node.AgentConfig{
		Controller: controllerName,
		WhoIs:      fleetWhoIs{own: addr, name: name},
		Holders:    holderhost.NewLocal(holderhost.LocalConfig{StateDir: stateDir}),
		StateDir:   stateDir,
		Roots:      []string{root},
		Local:      hostexec.NewLocal(),
		Secret:     fleetSecret,
	})
	if err != nil {
		tb.Fatalf("new agent %s: %v", name, err)
	}
	clock := newFleetClock()
	client := node.NewClient(node.ClientConfig{
		Name:    name,
		Address: addr,
		Dial:    (&net.Dialer{}).DialContext,
		Bound:   clientBound,
		Clock:   clock,
		Secret:  fleetSecret,
	})
	ctx, cancel := context.WithCancel(tb.Context())
	served := make(chan error, 1)
	go func() { served <- agent.Serve(ctx, listener) }()
	f.agents[name] = &agentRig{listener: listener, cancel: cancel, served: served, client: client, clock: clock}
	f.Clients[name] = client
	f.Roots[name] = root
	f.Hosts[name] = node.NewRemoteHost(client)
	tb.Cleanup(func() { f.StopAgent(tb, name) })
}

// StopAgent stops the agent named name. It closes the agent's listener and connections, and returns once its Serve has
// returned. A stopped agent stays stopped, so a second call does nothing.
func (f *Fleet) StopAgent(tb testing.TB, name string) {
	tb.Helper()
	rig := f.rig(tb, name)
	rig.stop.Do(func() {
		rig.cancel()
		if err := <-rig.served; err != nil {
			tb.Errorf("agent %s: serve: %v", name, err)
		}
		if err := rig.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			tb.Errorf("agent %s: close the listener: %v", name, err)
		}
		rig.stopped.Store(true)
	})
}

// WaitReach waits until the client of the agent named name reads want. It waits on the client's Reach change channel.
// The client's clock passes time only while WaitReach waits on a stopped agent: then the client's pings fail, and its
// expiry comes due. A running agent answers its pings, so its client does not go unreachable by waiting. WaitReach
// fails the test for a want that a running agent cannot reach, and for a want that a stopped agent does not reach within
// maxClockRounds firings of its clock.
func (f *Fleet) WaitReach(tb testing.TB, name string, want bool) {
	tb.Helper()
	rig := f.rig(tb, name)
	for rounds := 0; ; rounds++ {
		reach, changed := rig.client.Reach()
		if reach == want {
			return
		}
		if !rig.stopped.Load() {
			tb.Fatalf("WaitReach(%q, %t): the agent runs and its client reads %t; "+
				"WaitReach passes time only on a stopped agent", name, want, reach)
		}
		if rounds == maxClockRounds {
			tb.Fatalf("WaitReach(%q, %t): the client still reads %t after %d firings of its clock",
				name, want, reach, rounds)
		}
		rig.clock.fire()
		select {
		case <-changed:
		case <-rig.clock.armed:
		}
	}
}

// PushCommit commits an empty commit with message onto Origin's main, through a scratch clone of Origin, and returns the
// id of the commit.
func (f *Fleet) PushCommit(tb testing.TB, message string) string {
	tb.Helper()
	root := tb.TempDir()
	runGit(tb, root, "clone", "-q", f.Origin, "scratch")
	scratch := filepath.Join(root, "scratch")
	runGit(tb, scratch, "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", message)
	runGit(tb, scratch, "push", "-q", "origin", "HEAD:refs/heads/main")
	return runGit(tb, scratch, "rev-parse", "HEAD")
}

// rig returns the started agent named name, and fails the test when the fleet has no such agent.
func (f *Fleet) rig(tb testing.TB, name string) *agentRig {
	tb.Helper()
	rig, ok := f.agents[name]
	if !ok {
		tb.Fatalf("the fleet has no agent %q", name)
	}
	return rig
}

// shortDir returns a state directory that is removed when tb ends. It is made by makeShortDir, not by tb.TempDir, since a
// holder's socket path must fit the socket address limit, which a TempDir path can exceed.
func shortDir(tb testing.TB) string {
	tb.Helper()
	dir, err := makeShortDir()
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			tb.Errorf("remove %s: %v", dir, rmErr)
		}
	})
	return dir
}

// makeShortDir makes a directory under the system's temporary directory, and returns its path. The caller removes it.
func makeShortDir() (string, error) {
	dir, err := os.MkdirTemp("", "fleet")
	if err != nil {
		return "", fmt.Errorf("make a state directory: %w", err)
	}
	return dir, nil
}

// runGit runs git with args in dir, with the fleet's commit identity, and returns its stdout with the surrounding space
// trimmed. It fails the test when git fails.
func runGit(tb testing.TB, dir string, args ...string) string {
	tb.Helper()
	env := []string{
		"GIT_AUTHOR_NAME=fleettest",
		"GIT_AUTHOR_EMAIL=fleettest@fleet.test",
		"GIT_COMMITTER_NAME=fleettest",
		"GIT_COMMITTER_EMAIL=fleettest@fleet.test",
	}
	out, err := hostexec.NewLocal().Git(tb.Context(), hostexec.GitCall{Dir: dir, Args: args, Env: env})
	if err != nil {
		tb.Fatalf("git %s in %s: %v", strings.Join(args, " "), dir, err)
	}
	return strings.TrimSpace(string(out))
}

// fleetWhoIs names an agent's own address as the agent's own node, and every other address as the controller. Every node
// is signed in as fleetLogin.
type fleetWhoIs struct {
	own  string
	name string
}

// WhoIs names the node that addr belongs to, as the fleet's topology gives it.
func (w fleetWhoIs) WhoIs(_ context.Context, addr string) (*apitype.WhoIsResponse, error) {
	profile := &tailcfg.UserProfile{LoginName: fleetLogin}
	if addr == w.own {
		return &apitype.WhoIsResponse{Node: &tailcfg.Node{ComputedName: w.name}, UserProfile: profile}, nil
	}
	return &apitype.WhoIsResponse{Node: &tailcfg.Node{ComputedName: controllerName}, UserProfile: profile}, nil
}

// fleetClock is the holder.Clock of a fleet's client. A timer fires only when fire fires the timers that are set, so no
// wait of a client runs on the wall clock.
type fleetClock struct {
	mu sync.Mutex
	// pending holds the timers that are set and not yet fired.
	pending []chan time.Time
	// armed receives a signal at each After, so a WaitReach that waits on the clock looks again.
	armed chan struct{}
}

// newFleetClock returns a fleetClock with no timers set.
func newFleetClock() *fleetClock {
	return &fleetClock{armed: make(chan struct{}, 1)}
}

// After sets a timer and returns the channel it fires on. The timer fires at the next fire, whatever d is.
func (c *fleetClock) After(time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.mu.Lock()
	c.pending = append(c.pending, ch)
	c.mu.Unlock()
	select {
	case c.armed <- struct{}{}:
	default:
	}
	return ch
}

// fire fires every timer that is set, each once.
func (c *fleetClock) fire() {
	c.mu.Lock()
	pending := c.pending
	c.pending = nil
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- time.Time{}
	}
}
