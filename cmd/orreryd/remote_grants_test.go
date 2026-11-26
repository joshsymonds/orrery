package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/gates"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/holdertest"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/hostrelay"
	"github.com/joshsymonds/orrery/internal/hosts"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/pools"
	"github.com/joshsymonds/orrery/internal/protocol"
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
	// remoteGrantEpic is the epic the gate steps run for. It is stored on host b, so its steps and their gate run there.
	remoteGrantEpic = "synthetic-repo/epic-on-b"
	// remoteGrantGate is the named gate a step runs, which its gates file defines.
	remoteGrantGate = "unit"
	// remoteGrantOwned is the one path the gate steps own, which their commits change.
	remoteGrantOwned = "owned.txt"
	// remoteGrantSchema is the schema of a gate step's return.
	remoteGrantSchema = `{"type":"object","properties":{"status":{"type":"string"},"commit":{"type":"string"}}}`
	// remoteGrantFires bounds the timers a wait fires before it fails, so a broker that never frees a grant fails the test.
	remoteGrantFires = 50
)

// remoteGrantTime is the time the Steps engine of this test reads as now, and the time its epic is created at. It is fixed,
// so no clock of the test runs, and the engine's deadline timers never fire.
func remoteGrantTime() time.Time {
	return time.Unix(1_800_000_000, 0)
}

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

// remoteGrantRelayWhoIs names every peer of the relay b, signed in as remoteGrantLogin, so the relay admits each connection of the
// test as host b.
type remoteGrantRelayWhoIs struct{}

// WhoIs implements node.WhoIs.
func (remoteGrantRelayWhoIs) WhoIs(context.Context, string) (*apitype.WhoIsResponse, error) {
	return &apitype.WhoIsResponse{
		Node:        &tailcfg.Node{ComputedName: remoteGrantAgent},
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

// remoteGrantBootSkew is how many clock ticks later than the controller's the start times of b's processes read, as they
// do on another machine, which booted at another time.
const remoteGrantBootSkew = 1000

// otherMachine is the processes of b as the controller sees them across machines. The agent of b runs in this process and
// shares the controller's /proc, so otherMachine stands for b's own /proc: each of its start times reads
// remoteGrantBootSkew ticks later than the controller's own /proc reads it. A start time the controller read from its own
// /proc then names no process of b, as on another machine, and a start of 0, which names none, asks only whether the pid
// lives.
type otherMachine struct {
	grants.HostProcesses
}

// ProcessAlive asks the agent of b whether the process with pid lives under start, a start time of b's /proc.
func (m otherMachine) ProcessAlive(ctx context.Context, pid int, start int64) (bool, error) {
	if start != 0 {
		start -= remoteGrantBootSkew
	}
	alive, err := m.HostProcesses.ProcessAlive(ctx, pid, start)
	if err != nil {
		return false, fmt.Errorf("process %d on b: %w", pid, err)
	}
	return alive, nil
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

// remoteGrantBroker starts a broker of the controller over st, which follows host b through rig's client, as another
// machine's processes (see otherMachine). The controller's own view of its processes reports none alive, so a grant of b
// that the broker keeps is kept by b's report alone: the client of the grants in this test is a process of this machine,
// which the controller would find alive. It is closed when the test ends, so its watches stop before the agent of b does.
// A follow of false starts the broker without b in its Hosts, so it refuses b's pools.
func remoteGrantBroker(t *testing.T, rig remoteGrantRig, st *store.Store, follow bool) *grants.Broker {
	t.Helper()
	var named map[string]grants.HostProcesses
	if follow {
		named = map[string]grants.HostProcesses{remoteGrantAgent: otherMachine{rig.client}}
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

// remoteGrantPoolOf returns the pool of the relay test: one member, on host b, whose commands succeed at once.
func remoteGrantPoolOf(sh string) pools.Pool {
	argv := []string{sh, "-c", "exit 0"}
	return pools.Pool{
		Name: remoteGrantPool, Host: remoteGrantAgent, Cap: 1, Members: []string{remoteGrantMember},
		Status: argv, StatusLimit: time.Minute, Wake: argv, WakeLimit: time.Minute,
		Admit: argv, AdmitLimit: time.Minute,
	}
}

// remoteGrantPoolsFile writes the machine pool file of b under xdg, which declares one pool named name on host b, whose
// commands are sh's exit 0.
func remoteGrantPoolsFile(t *testing.T, xdg, name, sh string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(xdg, "orrery"), 0o700); err != nil {
		t.Fatalf("make config directory of b: %v", err)
	}
	toml := "[pools." + name + "]\nhost = \"b\"\ncap = 1\nmembers = [\"m1\"]\n" +
		"wake = [\"" + sh + "\", \"-c\", \"exit 0\"]\nwake_limit = 60\n" +
		"status = [\"" + sh + "\", \"-c\", \"exit 0\"]\nstatus_limit = 60\n" +
		"admit = [\"" + sh + "\", \"-c\", \"exit 0\"]\nadmit_limit = 60\n"
	if err := os.WriteFile(filepath.Join(xdg, "orrery", "pools.toml"), []byte(toml), 0o600); err != nil {
		t.Fatalf("write pools.toml of b: %v", err)
	}
}

// remoteGrantRequest returns a request from host b for the pool of the relay test, for the client process pid and the
// holder directory holder, which may be empty.
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
	// done closes once Acquire has returned, after it sent its result.
	done chan struct{}
}

// startAcquire runs an Acquire of req on b, and returns its call.
func startAcquire(t *testing.T, b *grants.Broker, req grants.Request) *acquireCall {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	call := &acquireCall{
		cancel: cancel, granted: make(chan *grants.Grant, 1), result: make(chan error, 1), done: make(chan struct{}),
	}
	go func() {
		call.result <- b.Acquire(ctx, req, func(event grants.Event) error {
			if event.Grant != nil {
				call.granted <- event.Grant
			}
			return nil
		})
		close(call.done)
	}()
	return call
}

// fireUntil fires the timers b's watches arm, each as it is armed, until granted receives a value, and returns it. It
// waits only for a timer to be armed, for the value, or for ended to close, which is what says the call that would send
// the value has returned without one, so the test fails rather than waits. It fails too once remoteGrantFires timers have
// fired without one.
func fireUntil[T any](t *testing.T, clock *fakeClock, granted <-chan T, ended <-chan struct{}) T {
	t.Helper()
	value, failure := awaitValue(clock, granted, ended)
	if failure != "" {
		t.Fatal(failure)
	}
	return value
}

// awaitValue is the wait of fireUntil: it returns the value granted sends, or the reason the wait ends without one, which
// is empty when it has a value.
func awaitValue[T any](clock *fakeClock, granted <-chan T, ended <-chan struct{}) (T, string) {
	var zero T
	for range remoteGrantFires {
		select {
		case value := <-granted:
			return value, ""
		case <-ended:
			// A value sent before the call ended is still there to take.
			select {
			case value := <-granted:
				return value, ""
			default:
			}
			return zero, "the call ended without the value it was waiting for"
		case timer := <-clock.armed:
			timer.fire()
		}
	}
	return zero, fmt.Sprintf("no grant after %d timers fired", remoteGrantFires)
}

// heldBy returns the id of the grant that holds a member of a pool in b's status, or "" when none does.
func heldBy(b *grants.Broker) string {
	for _, pool := range b.Status().Pools {
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

// remoteGrantStepSessions are the sessions a Steps of this test starts its steps on. As the manager of sessions does, each
// StartStep stores the step's row and its ops through writer, and then hands the step to started, which the test reads.
// A step's agent is the test itself, which returns the step's turn with Observer.
type remoteGrantStepSessions struct {
	writer  *enginehost.Writer
	started chan enginehost.StepSession
}

// StartStep stores the step's session row and ops, and hands the started step to the test.
func (s *remoteGrantStepSessions) StartStep(ctx context.Context, step enginehost.StepSession) error {
	ops := append([]store.EngineOp{
		store.EngineAdd{ID: step.ID, Dir: step.Dir, State: "working", Kind: store.KindStep},
	}, step.Ops...)
	if err := s.writer.Commit(ctx, step.ID, ops...); err != nil {
		return fmt.Errorf("store step %s: %w", step.ID, err)
	}
	s.started <- step
	return nil
}

// Interrupt does nothing, since no step's agent runs.
func (*remoteGrantStepSessions) Interrupt(context.Context, string) error { return nil }

// Continue does nothing, since no step's agent runs.
func (*remoteGrantStepSessions) Continue(context.Context, string, enginehost.StepPrompt) error {
	return nil
}

// Release does nothing, since no step's agent runs.
func (*remoteGrantStepSessions) Release(context.Context, string) error { return nil }

// Stop does nothing, since no step's agent runs.
func (*remoteGrantStepSessions) Stop(context.Context, string) error { return nil }

// grantSignal is the Pools of the Steps engine in this test. Each Acquire goes to the test's broker, and each grant the
// broker sends is also handed to granted, so the test waits for a gate's grant as a signal.
type grantSignal struct {
	enginehost.PoolCommands
	granted chan *grants.Grant
}

// Acquire forwards the request to the broker, and hands each grant the broker sends to granted as well. The hand-off
// never blocks, so a grant nobody waits for does not hold the broker.
func (g *grantSignal) Acquire(ctx context.Context, req grants.Request, send func(grants.Event) error) error {
	return g.PoolCommands.Acquire(ctx, req, func(event grants.Event) error {
		if event.Grant != nil {
			select {
			case g.granted <- event.Grant:
			default:
			}
		}
		return send(event)
	})
}

// launchSignal is host b as the Steps engine in this test sees it: each Launch goes to b's host, and a launch that succeeds
// is signaled on launched, so the test knows that a gate runs on b as a signal.
type launchSignal struct {
	hostexec.Host
	launched chan struct{}
}

// Launch launches on the host b, and signals launched once the launch succeeds.
func (h launchSignal) Launch(ctx context.Context, cfg hostexec.Launch) (hostexec.Held, error) {
	held, err := h.Host.Launch(ctx, cfg)
	if err == nil {
		select {
		case h.launched <- struct{}{}:
		default:
		}
	}
	return held, err
}

// remoteGrantNow is the clock of the Steps engine in this test.
func remoteGrantNow() time.Time {
	return remoteGrantTime()
}

// remoteGrantNoTimer returns a deadline timer that never fires, and whose stop reports that it stopped it first.
func remoteGrantNoTimer(time.Duration) (<-chan time.Time, func() bool) {
	return make(chan time.Time), func() bool { return true }
}

// remoteGrantWriter runs a Writer over st until the test's cleanup, which runs after the Steps the writer serves.
func remoteGrantWriter(t *testing.T, st *store.Store) *enginehost.Writer {
	t.Helper()
	writer := enginehost.NewWriter(enginehost.WriterConfig{Applier: st})
	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	done := make(chan struct{})
	go func() {
		defer close(done)
		writer.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return writer
}

// remoteGrantGit runs git in dir with the test's identity, and returns its trimmed output. It fails the test on error.
func remoteGrantGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("find git: %v", err)
	}
	full := append([]string{
		"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.CommandContext(t.Context(), git, full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// remoteGrantRepo makes a repository under root with one commit on main, and a linked worktree of it under root, detached
// at that commit. It returns the repository and the worktree, with symlinks resolved as git lists them.
func remoteGrantRepo(t *testing.T, root string) (string, string) {
	t.Helper()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatalf("make repository: %v", err)
	}
	remoteGrantGit(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("base\n"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	remoteGrantGit(t, repo, "add", "README")
	remoteGrantGit(t, repo, "commit", "-q", "-m", "base")
	linked := filepath.Join(root, "linked")
	remoteGrantGit(t, repo, "worktree", "add", "--detach", linked, "HEAD")
	resolved, err := filepath.EvalSymlinks(linked)
	if err != nil {
		t.Fatalf("resolve linked worktree: %v", err)
	}
	return repo, resolved
}

// gateStepFixture is a Steps of the controller, whose steps run for the epic stored on b, and whose gate grants go to the
// test's broker over the store. first is that broker, which signal forwards to and which a restart closes. launched
// receives a value each time a gate launches on b.
type gateStepFixture struct {
	rig      remoteGrantRig
	store    *store.Store
	first    *grants.Broker
	signal   *grantSignal
	starts   *remoteGrantStepSessions
	launched chan struct{}
	// stepEnded receives a value when the Steps engine stores an end, so a wait for the gate's grant fails once the step
	// ends without one.
	stepEnded chan struct{}
	steps     *enginehost.Steps
	linked    string
	gatesFile string
	project   string
	defs      []pools.Pool
}

// newGateStepFixture returns a fixture whose gate is unit, which runs script on b, with the epic stored on b and its
// workspace a linked worktree on b. The gate's pools are the default gate pool, declared in b's pool file.
func newGateStepFixture(t *testing.T, script string) gateStepFixture {
	t.Helper()
	rig := newRemoteGrantRig(t)
	st := remoteGrantStore(t)
	xdg := filepath.Join(rig.root, "xdg")
	remoteGrantPoolsFile(t, xdg, gates.DefaultPool, rig.sh)
	gatesFile := filepath.Join(rig.root, "gates.toml")
	gatesText := fmt.Sprintf("[gates.%s]\nargv = [%q, \"-c\", %q]\nlimit = 300\n", remoteGrantGate, rig.sh, script)
	if err := os.WriteFile(gatesFile, []byte(gatesText), 0o600); err != nil {
		t.Fatalf("write gates file: %v", err)
	}
	repo, linked := remoteGrantRepo(t, rig.root)
	if err := st.CreateEpic(t.Context(), store.EpicRow{
		ID:         remoteGrantEpic,
		Repository: repo,
		Host:       remoteGrantAgent,
		Created:    remoteGrantTime(),
		Changed:    remoteGrantTime(),
	}); err != nil {
		t.Fatalf("create epic on b: %v", err)
	}
	getenv := func(name string) string {
		if name == "XDG_CONFIG_HOME" {
			return xdg
		}
		return ""
	}
	project, defs, err := pools.Load(t.Context(), rig.host, linked, getenv)
	if err != nil {
		t.Fatalf("load pools of b: %v", err)
	}
	first := remoteGrantBroker(t, rig, st, true)
	signal := &grantSignal{PoolCommands: first, granted: make(chan *grants.Grant, 8)}
	writer := remoteGrantWriter(t, st)
	starts := &remoteGrantStepSessions{writer: writer, started: make(chan enginehost.StepSession, 4)}
	launched := make(chan struct{}, 4)
	stepEnded := make(chan struct{}, 1)
	environ := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + rig.root, "XDG_CONFIG_HOME=" + xdg}
	steps := enginehost.NewSteps(enginehost.StepsConfig{
		Sessions:  starts,
		Store:     st,
		Writer:    writer,
		Pools:     signal,
		Worktrees: t.TempDir(),
		Environ:   environ,
		Logger:    slog.New(slog.DiscardHandler),
		Now:       remoteGrantNow,
		NewTimer:  remoteGrantNoTimer,
		Stored: func() {
			select {
			case stepEnded <- struct{}{}:
			default:
			}
		},
		GateStateDir: rig.stateB,
		GateScope:    holdertest.FakeScope(t),
		Host:         remoteGrantHostName,
		Hosts: hostexec.Map{
			remoteGrantHostName: hostexec.NewLocal(),
			remoteGrantAgent:    launchSignal{Host: rig.host, launched: launched},
		},
	})
	t.Cleanup(steps.Close)
	return gateStepFixture{
		rig: rig, store: st, first: first, signal: signal, starts: starts, launched: launched, stepEnded: stepEnded,
		steps: steps, linked: linked,
		gatesFile: gatesFile, project: project, defs: defs,
	}
}

// start starts the gate step of the epic on b, whose gate is unit from the fixture's gates file, and returns the session
// the step runs in. Its agent is the test, which returns the step's turn through returnDone.
func (f gateStepFixture) start(t *testing.T) enginehost.StepSession {
	t.Helper()
	if _, err := f.steps.Start(t.Context(), enginehost.StepStart{
		Agent: "implementer", Prompt: "do it", Schema: remoteGrantSchema, Workspace: f.linked,
		Owned: []string{remoteGrantOwned}, Epic: remoteGrantEpic, GateName: remoteGrantGate, GatesFile: f.gatesFile,
	}); err != nil {
		t.Fatalf("start gate step on b: %v", err)
	}
	return <-f.starts.started
}

// returnDone commits the owned path in the fixture's linked worktree, and returns the step's turn as done at that commit.
// The step then runs its commit checks on b, and then its gate, which queues the grant the test waits for.
func (f gateStepFixture) returnDone(t *testing.T, step enginehost.StepSession) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.linked, remoteGrantOwned), []byte("owned\n"), 0o600); err != nil {
		t.Fatalf("write owned path: %v", err)
	}
	remoteGrantGit(t, f.linked, "add", remoteGrantOwned)
	remoteGrantGit(t, f.linked, "commit", "-q", "-m", "change")
	commit := remoteGrantGit(t, f.linked, "rev-parse", "HEAD")
	raw := fmt.Sprintf(`{"status":"done","commit":%q}`, commit)
	step.Observer.Return(session.TurnEnded{StructuredOutput: json.RawMessage(raw)})
}

// probe returns a request for the default gate pool on b, for this process: the pools the fixture's workspace declares,
// as the Steps engine asks for a gate's grant. It is used to find out whether that pool's member is free.
func (f gateStepFixture) probe() grants.Request {
	return grants.Request{
		Project: f.project, Pools: f.defs, Needs: []grants.Need{{Pool: gates.DefaultPool, Count: 1}},
		ClientPID: os.Getpid(), Host: remoteGrantAgent,
	}
}

// remoteGrantRowOf returns the stored grant row of the grant id, and fails the test unless the row names host b.
func remoteGrantRowOf(t *testing.T, st *store.Store, id string) store.GrantRow {
	t.Helper()
	rows, err := st.Grants(t.Context())
	if err != nil {
		t.Fatalf("read grants: %v", err)
	}
	i := slices.IndexFunc(rows, func(row store.GrantRow) bool { return row.ID == id })
	if i < 0 {
		t.Fatalf("grant %s is not stored", id)
	}
	if rows[i].Host != remoteGrantAgent {
		t.Fatalf("grant %s is stored with host %q, want %q", id, rows[i].Host, remoteGrantAgent)
	}
	return rows[i]
}

// poolDefinitionOf returns the wire definition of pool, as a relayed Acquire carries it.
func poolDefinitionOf(pool pools.Pool) *v1.PoolDefinition {
	return &v1.PoolDefinition{
		Name: pool.Name, Host: pool.Host, Cap: int32(pool.Cap), Members: pool.Members,
		Wake: pool.Wake, WakeLimitSeconds: int64(pool.WakeLimit / time.Second),
		Status: pool.Status, StatusLimitSeconds: int64(pool.StatusLimit / time.Second),
		Admit: pool.Admit, AdmitLimitSeconds: int64(pool.AdmitLimit / time.Second),
	}
}

// startRelay serves a relay of the controller over broker until the stop it returns or the test's cleanup, as the
// controller's relay admits host b: its WhoIs names every peer b, and it serves the Pools service of enginehost over
// broker. It returns the relay's address and its stop, which waits for the relay to end and may be called twice.
func startRelay(t *testing.T, broker *grants.Broker) (string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	relay, err := hostrelay.Listen(ctx, hostrelay.Config{
		Address: "127.0.0.1:0",
		Hosts:   []hosts.Host{{Name: remoteGrantAgent, Address: remoteGrantAgent + ":0"}},
		WhoIs:   remoteGrantRelayWhoIs{},
		Handler: enginehost.NewHandler(enginehost.ServeConfig{Pools: broker}),
		Secret:  remoteGrantSecret,
		Logger:  slog.New(slog.DiscardHandler),
	})
	if err != nil {
		cancel()
		t.Fatalf("listen for the relay: %v", err)
	}
	served := make(chan error, 1)
	go func() { served <- relay.Serve(ctx) }()
	stop := sync.OnceFunc(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("serve relay: %v", err)
		}
	})
	t.Cleanup(stop)
	return relay.Addr().String(), stop
}

// relayAcquire asks the relay at addr for the grant of pool for the client process pid, as a forwarder on b relays it: the
// fleet secret in the header, and no host, which the relay sets from the connection it admitted. It calls granted with
// the id of the grant once it holds, and returns the error that ends the call.
func relayAcquire(ctx context.Context, addr string, pool pools.Pool, pid int, granted func(string)) error {
	client := enginev1connect.NewPoolsClient(http.DefaultClient, "http://"+addr)
	req := connect.NewRequest(&v1.AcquireRequest{
		Project:   "remote-grants",
		Pools:     []*v1.PoolDefinition{poolDefinitionOf(pool)},
		Needs:     []*v1.PoolNeed{{Pool: pool.Name, Count: 1}},
		ClientPid: int32(pid),
	})
	req.Header().Set(protocol.HeaderSecret, remoteGrantSecret)
	stream, err := client.Acquire(ctx, req)
	if err != nil {
		return err
	}
	for stream.Receive() {
		if id := stream.Msg().GetGranted().GetGrantId(); id != "" {
			granted(id)
		}
	}
	return stream.Err()
}

// TestRemoteGrants checks that the grants of host b follow b's processes. A pool b names is admitted for b. A gate of b,
// started through the Steps engine for an epic stored on b, is stored with host b, and is kept across a restart of the
// controller while b reports its holder alive, and freed once b reports the holder gone or the gate's exit is read. A
// grant asked through the relay as host b is stored with host b, and is kept across a restart while b reports its client
// alive, and freed once the client ends.
func TestRemoteGrants(t *testing.T) {
	t.Run("pool in b's own pools.toml naming b is admitted", func(t *testing.T) {
		rig := newRemoteGrantRig(t)
		xdg := filepath.Join(rig.root, "xdg")
		remoteGrantPoolsFile(t, xdg, remoteGrantPool, rig.sh)
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

	t.Run("gate of b started through Steps kept across a restart while b reports its holder live, freed once b reports it gone",
		func(t *testing.T) {
			f := newGateStepFixture(t, "sleep 3600")
			f.returnDone(t, f.start(t))
			grant := fireUntil(t, f.rig.clock, f.signal.granted, f.stepEnded)
			row := remoteGrantRowOf(t, f.store, grant.ID)
			t.Cleanup(func() {
				if err := f.rig.host.Clear(context.WithoutCancel(t.Context()), row.Holder); err != nil {
					t.Errorf("clear gate on b: %v", err)
				}
			})
			<-f.launched

			// The controller restarts: its broker closes, and the gate, which runs under its scope, keeps its holder on b.
			f.first.Close()
			second := remoteGrantBroker(t, f.rig, f.store, true)
			if err := second.Restore(t.Context()); err != nil {
				t.Fatalf("restore: %v", err)
			}
			if heldBy(second) == "" {
				t.Fatal("restored gate grant freed while b reports its holder live")
			}
			fireUntilArmed(t, f.rig.clock)
			if heldBy(second) == "" {
				t.Fatal("gate grant freed by a poll that found its holder live")
			}

			if err := f.rig.host.Stop(t.Context(), row.Holder); err != nil {
				t.Fatalf("stop gate on b: %v", err)
			}
			probe := startAcquire(t, second, f.probe())
			fireUntil(t, f.rig.clock, probe.granted, probe.done)
			probe.cancel()
			if err := <-probe.result; !errors.Is(err, context.Canceled) {
				t.Fatalf("probe ended with %v, want context.Canceled after its grant", err)
			}
		})

	t.Run("gate of b started through Steps freed once its exit is read", func(t *testing.T) {
		f := newGateStepFixture(t, "exit 0")
		f.returnDone(t, f.start(t))
		grant := fireUntil(t, f.rig.clock, f.signal.granted, f.stepEnded)
		remoteGrantRowOf(t, f.store, grant.ID)

		// The gate exits on its own, and the Steps engine reads its exit and ends its grant, so the member is free for
		// the probe, which waits for it.
		probe := startAcquire(t, f.first, f.probe())
		fireUntil(t, f.rig.clock, probe.granted, probe.done)
		probe.cancel()
		if err := <-probe.result; !errors.Is(err, context.Canceled) {
			t.Fatalf("probe ended with %v, want context.Canceled after its grant", err)
		}
	})

	t.Run("grant asked through the relay as host b kept across a restart while b reports the client alive, freed when it ends",
		func(t *testing.T) {
			rig := newRemoteGrantRig(t)
			st := remoteGrantStore(t)
			pool := remoteGrantPoolOf(rig.sh)
			client, clientStdin := startClient(t)

			first := remoteGrantBroker(t, rig, st, true)
			addr, stopRelay := startRelay(t, first)
			granted := make(chan string, 1)
			result := make(chan error, 1)
			relayed := make(chan struct{})
			go func() {
				defer close(relayed)
				result <- relayAcquire(t.Context(), addr, pool, client.Process.Pid, func(id string) {
					select {
					case granted <- id:
					default:
					}
				})
			}()
			id := fireUntil(t, rig.clock, granted, relayed)
			remoteGrantRowOf(t, st, id)

			// The controller restarts: its broker and its relay close, so the relayed call ends.
			first.Close()
			if err := <-result; err == nil {
				t.Fatal("relayed Acquire ended without an error when its broker closed")
			}
			stopRelay()
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
			probe := startAcquire(t, second, remoteGrantRequest(pool, os.Getpid(), ""))
			fireUntil(t, rig.clock, probe.granted, probe.done)
			probe.cancel()
			if err := <-probe.result; !errors.Is(err, context.Canceled) {
				t.Fatalf("probe ended with %v, want context.Canceled after its grant", err)
			}
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
