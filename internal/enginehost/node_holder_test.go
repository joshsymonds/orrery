package enginehost

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"

	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/store"
)

// nodeController is the controller's node name on the tailnet, which the agent of host b admits.
const nodeController = "controller"

// nodeLogin is the login that the controller's node and the agent's node are both signed in as.
const nodeLogin = "owner@example.com"

// nodePrompt is the prompt the Start test sends to b, which the fake claude on b must read.
const nodePrompt = "prompt for the agent of b"

// nodeEnvB is the environment entry only the agent of b gives its sessions, so a child that runs under b's environment
// has it, and one that runs under the controller's does not.
const nodeEnvB = "ORRERY_TEST_NODE=b"

// nodeWhoIs answers WhoIs as tailscaled does for the rig: the address own is the agent's own node, and every other
// address is the controller's node. Both are signed in as nodeLogin.
type nodeWhoIs struct {
	own string
}

// WhoIs names the node at addr, as nodeWhoIs describes.
func (w nodeWhoIs) WhoIs(_ context.Context, addr string) (*apitype.WhoIsResponse, error) {
	name := nodeController
	if addr == w.own {
		name = "b"
	}
	return &apitype.WhoIsResponse{
		Node:        &tailcfg.Node{ComputedName: name},
		UserProfile: &tailcfg.UserProfile{LoginName: nodeLogin},
	}, nil
}

// neverClock is the clock of the client in these tests. Its timers never fire, so no wait of the client is cut short by
// a real timer: each op ends when the agent answers, or when its connection fails.
type neverClock struct{}

// After returns a channel that never receives.
func (neverClock) After(time.Duration) <-chan time.Time {
	return make(chan time.Time)
}

// nodeRig is a controller whose host b is a node.Client to an agent on b. The agent runs b's holders, in b's own state
// directory, under b's own environment and the fake claude, while the controller's claude does not exist.
type nodeRig struct {
	m *Manager
	// path is the store's path.
	path string
	// stateA and stateB are the state directories of the controller and of b.
	stateA, stateB string
}

// newNodeRig starts the agent of b on a loopback listener and returns the controller's manager, which names b as its
// host. The rig's controller runs a claude that does not exist, so no session of it can run on the controller.
func newNodeRig(t *testing.T) nodeRig {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for the agent of b: %v", err)
	}
	stateA, stateB := holderRoot(t), holderRoot(t)
	local := holderhost.NewLocal(holderhost.LocalConfig{
		Shell:    sh,
		Claude:   fakeClaudePath(t),
		Environ:  []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), nodeEnvB},
		StateDir: stateB,
	})
	agent, err := node.NewAgent(node.AgentConfig{
		Controller: nodeController,
		WhoIs:      nodeWhoIs{own: ln.Addr().String()},
		Holders:    local,
		StateDir:   stateB,
	})
	if err != nil {
		t.Fatalf("new agent of b: %v", err)
	}
	serveAgent(t, agent, ln)
	client := node.NewClient(node.ClientConfig{
		Name:    "b",
		Address: ln.Addr().String(),
		Dial:    (&net.Dialer{}).DialContext,
		Clock:   neverClock{},
	})
	claude := filepath.Join(t.TempDir(), "no-claude")
	if _, err := os.Stat(claude); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the controller's claude %s exists or cannot be checked: %v, want it absent", claude, err)
	}
	s, path := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s})
	startRun(t, writer)
	m := NewManager(Config{
		Shell:    sh,
		Claude:   claude,
		Environ:  []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
		StateDir: stateA,
		HostName: "a",
		Hosts:    map[string]holderhost.Host{"b": client},
		Writer:   writer,
		Now:      fixedClock,
		Store:    s,
	})
	t.Cleanup(m.Close)
	return nodeRig{m: m, path: path, stateA: stateA, stateB: stateB}
}

// serveAgent serves agent on ln until the test's cleanup, and waits for Serve to return then. The context is detached
// from the test's, which ends before the cleanups run, so the agent serves until the manager that uses it has closed.
func serveAgent(t *testing.T, agent *node.Agent, ln net.Listener) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	served := make(chan error, 1)
	go func() { served <- agent.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("serve agent of b: %v", err)
		}
	})
}

// requireEnviron checks that the process pid runs with the environment entry want.
func requireEnviron(t *testing.T, pid int, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
	if err != nil {
		t.Fatalf("environment of process %d: %v", pid, err)
	}
	if !slices.Contains(strings.Split(string(data), "\x00"), want) {
		t.Fatalf("environment of process %d lacks %q", pid, want)
	}
}

func TestNodeHolderStartRunsSessionOnAgent(t *testing.T) {
	t.Parallel()
	rig := newNodeRig(t)
	dir := t.TempDir()
	id, err := rig.m.Start(t.Context(), StartRequest{Dir: dir, Prompt: nodePrompt, Host: "b"})
	if err != nil {
		t.Fatalf("Start on b: %v", err)
	}
	if _, err := os.Stat(holderDir(t, rig.stateB, id)); err != nil {
		t.Fatalf("holder of %s under b's state directory: %v", id, err)
	}
	if _, err := os.Stat(holderDir(t, rig.stateA, id)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("holder of %s under the controller's state directory: stat = %v, want none", id, err)
	}
	waitHistory(t, engineOf(rig.m), rig.path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	// The fake claude appends each line it reads from stdin to fakeclaude.stdin in its directory, before it replies.
	stdin, err := os.ReadFile(filepath.Join(dir, "fakeclaude.stdin"))
	if err != nil {
		t.Fatalf("prompts the child on b read: %v", err)
	}
	if !strings.Contains(string(stdin), nodePrompt) {
		t.Fatalf("prompts the child on b read = %q, want the prompt %q", stdin, nodePrompt)
	}
	requireEnviron(t, fakePID(t, dir), nodeEnvB)
	if host := historyOf(t, rig.path, id).Host; host != "b" {
		t.Fatalf("row host = %q, want b", host)
	}
}

func TestNodeHolderStopEndsChildAndClearsHolder(t *testing.T) {
	t.Parallel()
	rig := newNodeRig(t)
	dir := t.TempDir()
	id, err := rig.m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi", Host: "b"})
	if err != nil {
		t.Fatalf("Start on b: %v", err)
	}
	waitHistory(t, engineOf(rig.m), rig.path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	pid := fakePID(t, dir)
	if err := rig.m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop of the session on b: %v", err)
	}
	requireGone(t, pid)
	requireDirGone(t, holderDir(t, rig.stateB, id))
}

func TestNodeHolderSayResumesSessionOnAgent(t *testing.T) {
	t.Parallel()
	rig := newNodeRig(t)
	dir := t.TempDir()
	id, err := rig.m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi", Host: "b"})
	if err != nil {
		t.Fatalf("Start on b: %v", err)
	}
	waitHistory(t, engineOf(rig.m), rig.path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if err := rig.m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := rig.m.Say(t.Context(), SayRequest{ID: id, Text: "again"}); err != nil {
		t.Fatalf("Say of the stopped session on b: %v", err)
	}
	waitHistory(t, engineOf(rig.m), rig.path, id, "the resumed reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	if _, err := os.Stat(holderDir(t, rig.stateB, id)); err != nil {
		t.Fatalf("holder of %s under b's state directory after the resume: %v", id, err)
	}
	if _, err := os.Stat(holderDir(t, rig.stateA, id)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("resume of %s on b launched a holder under the controller's state directory: stat = %v", id, err)
	}
}
