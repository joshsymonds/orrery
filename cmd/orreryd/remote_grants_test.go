package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"

	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/pools"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// Synthetic names and secrets only. remoteGrantHostName is the controller's own host, the broker's Host, and
// remoteGrantAgent is host b, whose agent listens on loopback. remoteGrantPool is the pool both of b's pool files and the
// grants name.
const (
	remoteGrantHostName = "a"
	remoteGrantAgent    = "b"
	remoteGrantNode     = "synthetic-controller"
	remoteGrantLogin    = "synthetic-owner"
	remoteGrantSecret   = "fleet-secret-for-remote-grants-test"
	remoteGrantPool     = "p"
	remoteGrantMember   = "m1"
	// remoteGrantFires bounds the timers a wait fires before it fails, so a broker that never frees a grant fails the test.
	remoteGrantFires = 50
)

// remoteGrantWhoIs names each peer of the agent of b: the controller's node for every address but the agent's own, which
// names b. Every peer is signed in as remoteGrantLogin, the login the agent admits.
type remoteGrantWhoIs struct {
	own string
}

// WhoIs implements node.WhoIs.
func (w remoteGrantWhoIs) WhoIs(_ context.Context, addr string) (*apitype.WhoIsResponse, error) {
	name := remoteGrantNode
	if addr == w.own {
		name = remoteGrantAgent
	}
	return &apitype.WhoIsResponse{
		Node:        &tailcfg.Node{ComputedName: name},
		UserProfile: &tailcfg.UserProfile{LoginName: remoteGrantLogin},
	}, nil
}

// remoteGrantClock is the clock of the node client in this test. Its timers never fire, so no wait of the client is cut
// short by a real timer: each call ends when the agent answers, or when its connection fails.
type remoteGrantClock struct{}

// After returns a channel that never receives.
func (remoteGrantClock) After(time.Duration) <-chan time.Time {
	return make(chan time.Time)
}

// fakeClock is the broker's AfterFunc clock in this test. Each timer the broker arms is handed to armed, and runs only
// when the test fires it, so the test decides when each poll of b happens.
type fakeClock struct {
	armed chan *fakeTimer
}

// newFakeClock returns a fakeClock with room for the timers a test arms before it fires them.
func newFakeClock() *fakeClock {
	return &fakeClock{armed: make(chan *fakeTimer, 64)}
}

// afterFunc arms a timer that runs f once the test fires it, and returns its stop, which ends the timer as time.Timer's
// Stop does.
func (c *fakeClock) afterFunc(_ time.Duration, f func()) func() bool {
	timer := &fakeTimer{run: f}
	c.armed <- timer
	return timer.stop
}

// fakeTimer is one timer of a fakeClock. It runs once, unless it is stopped first.
type fakeTimer struct {
	mu      sync.Mutex
	run     func()
	fired   bool
	stopped bool
}

// fire runs the timer's function, unless the timer has fired or been stopped.
func (t *fakeTimer) fire() {
	t.mu.Lock()
	if t.fired || t.stopped {
		t.mu.Unlock()
		return
	}
	t.fired = true
	t.mu.Unlock()
	t.run()
}

// stop ends the timer, and reports whether it did before the timer fired.
func (t *fakeTimer) stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fired || t.stopped {
		return false
	}
	t.stopped = true
	return true
}

// remoteGrantRig is the controller's view of host b: an agent of b served in this process over a real listener, the node
// client that dials it, and the directories b's agent and its gates live under.
type remoteGrantRig struct {
	sh     string
	root   string
	stateB string
	host   hostexec.Host
	client *node.Client
	clock  *fakeClock
}

// newRemoteGrantRig serves the agent of b until the test's cleanup, and returns the rig. The agent admits the rig's root,
// where b's state and files live, and the controller's node client sends the fleet secret.
func newRemoteGrantRig(t *testing.T) remoteGrantRig {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	sh, err = filepath.Abs(sh)
	if err != nil {
		t.Fatalf("absolute path of sh: %v", err)
	}
	root, err := shortStateRoot()
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove %s: %v", root, err)
		}
	})
	stateB := filepath.Join(root, "state-b")
	if err := os.Mkdir(stateB, 0o700); err != nil {
		t.Fatalf("make state directory of b: %v", err)
	}
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for the agent of b: %v", err)
	}
	environ := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root}
	agent, err := node.NewAgent(node.AgentConfig{
		Controller: remoteGrantNode,
		WhoIs:      remoteGrantWhoIs{own: ln.Addr().String()},
		Holders: holderhost.NewLocal(holderhost.LocalConfig{
			Shell: sh, Claude: sh, Environ: environ, StateDir: stateB,
		}),
		StateDir: stateB,
		Roots:    []string{root},
		Secret:   remoteGrantSecret,
	})
	if err != nil {
		t.Fatalf("new agent of b: %v", err)
	}
	serveRemoteGrantAgent(t, agent, ln)
	client := node.NewClient(node.ClientConfig{
		Name:    remoteGrantAgent,
		Address: ln.Addr().String(),
		Dial:    (&net.Dialer{}).DialContext,
		Clock:   remoteGrantClock{},
		Secret:  remoteGrantSecret,
		Logger:  slog.New(slog.DiscardHandler),
	})
	return remoteGrantRig{
		sh: sh, root: root, stateB: stateB, host: node.NewRemoteHost(client), client: client, clock: newFakeClock(),
	}
}

// serveRemoteGrantAgent serves agent on ln until the test's cleanup, and waits for Serve to return then. The context is
// detached from the test's, which ends before the cleanups run, so the agent serves until the broker that uses it closes.
func serveRemoteGrantAgent(t *testing.T, agent *node.Agent, ln net.Listener) {
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

// remoteGrantStore opens a store of the test's own for the brokers to keep their grants in.
func remoteGrantStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	st, err := store.Open(t.Context(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return st
}

// remoteGrantBroker starts a broker of the controller over st, which follows host b through rig's client. The controller's
// own view of its processes reports none alive, so a grant of b that the broker keeps is kept by b's report alone: the
// client of the grants in this test is a process of this machine, which the controller would find alive. It is closed
// when the test ends, so its watches stop before the agent of b does.
func remoteGrantBroker(t *testing.T, rig remoteGrantRig, st *store.Store, hosts bool) *grants.Broker {
	t.Helper()
	var named map[string]grants.HostProcesses
	if hosts {
		named = map[string]grants.HostProcesses{remoteGrantAgent: rig.client}
	}
	b := grants.NewBroker(grants.Config{
		Host:        remoteGrantHostName,
		Hosts:       named,
		Store:       st,
		Environ:     os.Environ(),
		Logger:      slog.New(slog.DiscardHandler),
		Alive:       func(int, int64) bool { return false },
		HolderAlive: func(string) bool { return false },
		AfterFunc:   rig.clock.afterFunc,
	})
	t.Cleanup(b.Close)
	return b
}

// remoteGrantPoolOf returns the pool of this test: one member, on host b, whose commands succeed at once.
func remoteGrantPoolOf(sh string) pools.Pool {
	argv := []string{sh, "-c", "exit 0"}
	return pools.Pool{
		Name: remoteGrantPool, Host: remoteGrantAgent, Cap: 1, Members: []string{remoteGrantMember},
		Status: argv, StatusLimit: time.Minute, Wake: argv, WakeLimit: time.Minute,
		Admit: argv, AdmitLimit: time.Minute,
	}
}

// remoteGrantRequest returns a request from host b for the pool of this test, for the client process pid and the holder
// directory holder, which may be empty.
func remoteGrantRequest(pool pools.Pool, pid int, holder string) grants.Request {
	return grants.Request{
		Project: "remote-grants", Pools: []pools.Pool{pool}, Needs: []grants.Need{{Pool: pool.Name, Count: 1}},
		ClientPID: pid, Holder: holder, Host: remoteGrantAgent,
	}
}

// acquireCall is an Acquire of a request, run in its own goroutine. granted receives the grant once it is granted, and
// result receives what Acquire returns.
type acquireCall struct {
	cancel  context.CancelFunc
	granted chan *grants.Grant
	result  chan error
}

// startAcquire runs an Acquire of req on b, and returns its call.
func startAcquire(t *testing.T, b *grants.Broker, req grants.Request) *acquireCall {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	call := &acquireCall{cancel: cancel, granted: make(chan *grants.Grant, 1), result: make(chan error, 1)}
	go func() {
		call.result <- b.Acquire(ctx, req, func(event grants.Event) error {
			if event.Grant != nil {
				call.granted <- event.Grant
			}
			return nil
		})
	}()
	return call
}

// fireUntil fires the timers b's watches arm, each as it is armed, until granted receives a grant, and returns the grant.
// It waits only for a timer to be armed or for the grant, and fails once remoteGrantFires timers have fired without one.
func fireUntil(t *testing.T, clock *fakeClock, granted <-chan *grants.Grant) *grants.Grant {
	t.Helper()
	for range remoteGrantFires {
		select {
		case grant := <-granted:
			return grant
		case timer := <-clock.armed:
			timer.fire()
		}
	}
	t.Fatalf("no grant after %d timers fired", remoteGrantFires)
	return nil
}

// heldBy returns the id of the grant that holds the member of the test's pool in b's status, or "" when none does.
func heldBy(b *grants.Broker) string {
	for _, pool := range b.Status().Pools {
		if pool.Name != remoteGrantPool {
			continue
		}
		for _, member := range pool.Members {
			if member.Holder != nil {
				return member.Holder.GrantID
			}
		}
	}
	return ""
}

// startClient starts a process that lives until its stdin closes, as a client of a grant on b does, and returns it with
// its stdin. endClient closes the stdin and waits for the process to end.
func startClient(t *testing.T) (*exec.Cmd, io.WriteCloser) {
	t.Helper()
	cat, err := exec.LookPath("cat")
	if err != nil {
		t.Fatalf("find cat: %v", err)
	}
	cmd := exec.Command(cat)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin of client: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start client: %v", err)
	}
	return cmd, stdin
}

// endClient closes the stdin of the client cmd, and waits until the process has ended. It is safe to call once.
func endClient(t *testing.T, cmd *exec.Cmd, stdin io.WriteCloser) {
	t.Helper()
	if err := stdin.Close(); err != nil {
		t.Fatalf("close client stdin: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait for client: %v", err)
	}
}

// launchGate starts a fake gate on b, a sleep that runs under its holder, and returns the holder's directory and the
// child as the holder hands it over. The gate is stopped and cleared when the test ends.
func launchGate(t *testing.T, rig remoteGrantRig, session string) (string, hostexec.Held) {
	t.Helper()
	ctx := t.Context()
	held, err := rig.host.Launch(ctx, hostexec.Launch{
		StateDir: rig.stateB, Session: session, Path: rig.sh, Args: []string{"-c", "sleep 3600"}, Dir: rig.root,
		Env: []string{"PATH=" + os.Getenv("PATH")},
	})
	if err != nil {
		t.Fatalf("launch gate %s on b: %v", session, err)
	}
	dir, err := rig.host.SessionDir(filepath.Join(rig.stateB, "holders"), session)
	if err != nil {
		t.Fatalf("holder directory of gate %s: %v", session, err)
	}
	t.Cleanup(func() {
		if err := rig.host.Clear(context.WithoutCancel(t.Context()), dir); err != nil {
			t.Errorf("clear gate %s on b: %v", session, err)
		}
	})
	return dir, held
}

// TestRemoteGrants checks that the grants of host b follow b's processes: a pool b names is admitted for b, and a grant
// of b is kept across a restart of the controller while b reports its holder or its client alive, and is freed once b
// reports them gone.
func TestRemoteGrants(t *testing.T) {
	t.Run("pool in b's own pools.toml naming b is admitted", func(t *testing.T) {
		rig := newRemoteGrantRig(t)
		xdg := filepath.Join(rig.root, "xdg")
		if err := os.MkdirAll(filepath.Join(xdg, "orrery"), 0o700); err != nil {
			t.Fatalf("make config directory of b: %v", err)
		}
		argv := []string{rig.sh, "-c", "exit 0"}
		toml := "[pools.p]\nhost = \"b\"\ncap = 1\nmembers = [\"m1\"]\n" +
			"wake = [\"" + argv[0] + "\", \"-c\", \"exit 0\"]\nwake_limit = 60\n" +
			"status = [\"" + argv[0] + "\", \"-c\", \"exit 0\"]\nstatus_limit = 60\n" +
			"admit = [\"" + argv[0] + "\", \"-c\", \"exit 0\"]\nadmit_limit = 60\n"
		if err := os.WriteFile(filepath.Join(xdg, "orrery", "pools.toml"), []byte(toml), 0o600); err != nil {
			t.Fatalf("write pools.toml of b: %v", err)
		}
		// The workspace is a work tree, so the project lookup stops at its top, under the agent's roots, rather than at
		// the filesystem root, which lies outside them.
		workspace := filepath.Join(rig.root, "workspace")
		if err := os.Mkdir(workspace, 0o700); err != nil {
			t.Fatalf("make workspace: %v", err)
		}
		git, err := exec.LookPath("git")
		if err != nil {
			t.Fatalf("find git: %v", err)
		}
		init := exec.CommandContext(t.Context(), git, "-C", workspace, "init", "--quiet")
		if out, err := init.CombinedOutput(); err != nil {
			t.Fatalf("init workspace: %v: %s", err, out)
		}
		getenv := func(name string) string {
			if name == "XDG_CONFIG_HOME" {
				return xdg
			}
			return ""
		}
		project, defs, err := pools.Load(t.Context(), rig.host, workspace, getenv)
		if err != nil {
			t.Fatalf("load pools of b: %v", err)
		}
		req := grants.Request{
			Project: project, Pools: defs, Needs: []grants.Need{{Pool: remoteGrantPool, Count: 1}},
			ClientPID: os.Getpid(), Host: remoteGrantAgent,
		}

		refusing := remoteGrantBroker(t, rig, remoteGrantStore(t), false)
		refused := startAcquire(t, refusing, req)
		select {
		case err := <-refused.result:
			if !errors.Is(err, grants.ErrRefused) {
				t.Fatalf("Acquire with no host of b configured = %v, want ErrRefused", err)
			}
		case <-refused.granted:
			t.Fatal("Acquire with no host of b configured was granted, want ErrRefused")
		}

		b := remoteGrantBroker(t, rig, remoteGrantStore(t), true)
		call := startAcquire(t, b, req)
		<-call.granted
		call.cancel()
		if err := <-call.result; !errors.Is(err, context.Canceled) {
			t.Fatalf("Acquire ended with %v, want context.Canceled after its grant", err)
		}
	})

	t.Run("gate on b kept across a restart while its holder lives, freed once b reports it gone", func(t *testing.T) {
		rig := newRemoteGrantRig(t)
		st := remoteGrantStore(t)
		pool := remoteGrantPoolOf(rig.sh)
		client, clientStdin := startClient(t)
		dir, held := launchGate(t, rig, "gate-kept")
		if err := held.Detach(); err != nil {
			t.Fatalf("detach from gate: %v", err)
		}

		first := remoteGrantBroker(t, rig, st, true)
		call := startAcquire(t, first, remoteGrantRequest(pool, client.Process.Pid, dir))
		<-call.granted
		first.Close()
		if err := <-call.result; !errors.Is(err, grants.ErrClosed) {
			t.Fatalf("Acquire ended with %v, want ErrClosed when the broker closes", err)
		}
		endClient(t, client, clientStdin)

		second := remoteGrantBroker(t, rig, st, true)
		if err := second.Restore(t.Context()); err != nil {
			t.Fatalf("restore: %v", err)
		}
		if heldBy(second) == "" {
			t.Fatal("restored grant freed while b reports its holder live and its client gone")
		}

		if err := rig.host.Stop(t.Context(), dir); err != nil {
			t.Fatalf("stop gate on b: %v", err)
		}
		next := startAcquire(t, second, remoteGrantRequest(pool, os.Getpid(), ""))
		fireUntil(t, rig.clock, next.granted)
		next.cancel()
		<-next.result
	})

	t.Run("gate on b freed once its exit is read", func(t *testing.T) {
		rig := newRemoteGrantRig(t)
		st := remoteGrantStore(t)
		pool := remoteGrantPoolOf(rig.sh)
		client, clientStdin := startClient(t)
		dir, held := launchGate(t, rig, "gate-exit")

		first := remoteGrantBroker(t, rig, st, true)
		call := startAcquire(t, first, remoteGrantRequest(pool, client.Process.Pid, dir))
		<-call.granted
		first.Close()
		if err := <-call.result; !errors.Is(err, grants.ErrClosed) {
			t.Fatalf("Acquire ended with %v, want ErrClosed when the broker closes", err)
		}
		endClient(t, client, clientStdin)

		second := remoteGrantBroker(t, rig, st, true)
		if err := second.Restore(t.Context()); err != nil {
			t.Fatalf("restore: %v", err)
		}
		id := heldBy(second)
		if id == "" {
			t.Fatal("restored grant freed while b reports its holder live")
		}

		if err := rig.host.Stop(t.Context(), dir); err != nil {
			t.Fatalf("stop gate on b: %v", err)
		}
		if err := held.Wait(); err == nil {
			t.Fatal("gate ended without an error, want the stop to end its child")
		}
		if _, ok := held.Exit(); !ok {
			t.Fatal("exit of the gate was not read")
		}
		reclaim := startAcquire(t, second, grants.Request{
			GrantID: id, ClientPID: client.Process.Pid, Holder: dir, Host: remoteGrantAgent,
			Pools: []pools.Pool{pool}, Needs: []grants.Need{{Pool: pool.Name, Count: 1}},
		})
		<-reclaim.granted
		reclaim.cancel()
		if err := <-reclaim.result; !errors.Is(err, context.Canceled) {
			t.Fatalf("reclaim ended with %v, want context.Canceled once its exit is read", err)
		}
		next := startAcquire(t, second, remoteGrantRequest(pool, os.Getpid(), ""))
		<-next.granted
		next.cancel()
		<-next.result
	})

	t.Run("grant of a client on b kept across a restart while b reports the client alive, freed when it ends",
		func(t *testing.T) {
			rig := newRemoteGrantRig(t)
			st := remoteGrantStore(t)
			pool := remoteGrantPoolOf(rig.sh)
			client, clientStdin := startClient(t)

			first := remoteGrantBroker(t, rig, st, true)
			call := startAcquire(t, first, remoteGrantRequest(pool, client.Process.Pid, ""))
			<-call.granted
			first.Close()
			if err := <-call.result; !errors.Is(err, grants.ErrClosed) {
				t.Fatalf("Acquire ended with %v, want ErrClosed when the broker closes", err)
			}

			second := remoteGrantBroker(t, rig, st, true)
			if err := second.Restore(t.Context()); err != nil {
				t.Fatalf("restore: %v", err)
			}
			if heldBy(second) == "" {
				t.Fatal("restored grant freed while b reports its client alive")
			}
			fireUntilArmed(t, rig.clock)
			if heldBy(second) == "" {
				t.Fatal("grant freed by a poll that found its client alive")
			}

			endClient(t, client, clientStdin)
			next := startAcquire(t, second, remoteGrantRequest(pool, os.Getpid(), ""))
			fireUntil(t, rig.clock, next.granted)
			next.cancel()
			<-next.result
		})
}

// fireUntilArmed fires the next timer the broker's watches arm, and waits until the watch arms another, so the poll that
// fired has returned before the test goes on. The timer it waited for stays armed, for fireUntil to fire.
func fireUntilArmed(t *testing.T, clock *fakeClock) {
	t.Helper()
	timer := <-clock.armed
	timer.fire()
	select {
	case next := <-clock.armed:
		clock.armed <- next
	case <-t.Context().Done():
		t.Fatal("test ended before the watch armed its next poll")
	}
}
