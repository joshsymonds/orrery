package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// restartStepID is the id of the no-epic step the test starts on host b, in the form a step id takes.
const restartStepID = "3c6f9a2e8b1d4f705e4a9c2b7d1f8e06"

// restartLostStepID is the id of a second step the test starts on host b beside the first. Its holder is cleared while
// b does not answer, so it is gone when b answers, and the step ends failed.
const restartLostStepID = "e5d2c8b1a7f04c3e9b6d1a8f2c7e4b90"

// restartPrompt is the prompt of each step. The fake claude answers it as a read-only step whose turn waits until the
// release file exists in its worktree, so the test holds each step open through each restart and outage, and releases
// it last.
const restartPrompt = "READDONE the step that survives restarts"

// restartReachMsg is the message the client of a host logs when the host's reach changes. The controller's logger
// carries each such record to the test, so the test waits on the change itself.
const restartReachMsg = "node: host reachability changed"

// restartLog records each change of a host's reach that the controller logs, and wakes each waiter. A test waits on a
// change by its count, so a change logged before the wait is seen as well as one logged after it.
type restartLog struct {
	mu      sync.Mutex
	reaches []restartReach
	changed chan struct{}
}

// restartReach is one logged change of a host's reach: the host, and whether it now reads unreachable.
type restartReach struct {
	host        string
	unreachable bool
}

// newRestartLog returns an empty restartLog.
func newRestartLog() *restartLog {
	return &restartLog{changed: make(chan struct{})}
}

// add records r, and wakes each waiter.
func (l *restartLog) add(r restartReach) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reaches = append(l.reaches, r)
	close(l.changed)
	l.changed = make(chan struct{})
}

// count returns how many changes of host's reach to unreachable, or back, have been logged.
func (l *restartLog) count(host string, unreachable bool) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return countReaches(l.reaches, host, unreachable)
}

// await returns once at least n changes of host's reach to unreachable, or back, have been logged, and reports false
// when ctx ends first.
func (l *restartLog) await(ctx context.Context, host string, unreachable bool, n int) bool {
	for {
		l.mu.Lock()
		got := countReaches(l.reaches, host, unreachable)
		changed := l.changed
		l.mu.Unlock()
		if got >= n {
			return true
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return false
		}
	}
}

// countReaches returns how many of reaches are changes of host's reach to unreachable, or back.
func countReaches(reaches []restartReach, host string, unreachable bool) int {
	n := 0
	for _, r := range reaches {
		if r.host == host && r.unreachable == unreachable {
			n++
		}
	}
	return n
}

// restartLogHandler is the slog.Handler of the controller's logger in these tests. It records each reach change that a
// client logs, and ignores every other record.
type restartLogHandler struct {
	log   *restartLog
	attrs []slog.Attr
}

// Enabled reports whether a record at level is handled: the reach changes are logged at Info.
func (h restartLogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelInfo
}

// Handle records r when it is a reach change, with the host and unreachable attributes it carries.
func (h restartLogHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Message != restartReachMsg {
		return nil
	}
	var reach restartReach
	visit := func(a slog.Attr) bool {
		switch a.Key {
		case "host":
			reach.host = a.Value.String()
		case "unreachable":
			reach.unreachable = a.Value.Bool()
		}
		return true
	}
	for _, a := range h.attrs {
		visit(a)
	}
	r.Attrs(visit)
	h.log.add(reach)
	return nil
}

// WithAttrs returns a handler that carries attrs with each record it handles.
func (h restartLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.attrs = append(slices.Clip(h.attrs), attrs...)
	return h
}

// WithGroup returns h, since the reach changes carry no group.
func (h restartLogHandler) WithGroup(string) slog.Handler {
	return h
}

// restartHolders is agent b's holder host: its Local, which launches each session under a holder of its own, and which
// records each launch in log. A launch is not released here, so the fake claude waits for the test.
type restartHolders struct {
	*holderhost.Local
	log *routingLog
}

// Launch starts the session as the Local does, and records the launch.
func (h restartHolders) Launch(ctx context.Context, spec holderhost.Spec) (*session.Session, error) {
	sess, err := h.Local.Launch(ctx, spec)
	if err != nil {
		return nil, err
	}
	h.log.add(routingEvent{kind: "launch", host: routingHostB, session: spec.Session, agent: spec.Agent})
	return sess, nil
}

// restartFixture is the controller and agent b of these tests, in this process over a real listener. The controller
// restarts over the same store and state directory. Agent b restarts over the same state directory and address, so its
// holders and the address the controller dials outlive each restart of either side.
type restartFixture struct {
	env       routingEnv
	getenv    func(string) string
	args      []string
	root      string
	storePath string
	agentDir  string
	addr      string
	holders   *holderhost.Local
	log       *routingLog
	reaches   *restartLog
	repo      string
	base      string
	store     *store.Store
	host      *sessionHost
	// agentCancel and agentServed stop the agent that serves b now, and receive the error its Serve returned. Both
	// are nil while no agent serves.
	agentCancel context.CancelFunc
	agentServed chan error
}

// newRestartFixture starts agent b and the controller, which lists b in its hosts file, over a store and state
// directory of their own. The repository the steps run in is made before the controller starts.
func newRestartFixture(t *testing.T) *restartFixture {
	t.Helper()
	root, err := shortStateRoot()
	if err != nil {
		t.Fatalf("%v", err)
	}
	f := &restartFixture{root: root, log: newRoutingLog(), reaches: newRestartLog()}
	t.Cleanup(func() { f.cleanup(t) })
	env := routingEnv{
		root: root, home: filepath.Join(root, "home"), config: filepath.Join(root, "config"),
		shell: eventWakeShell(t), claude: eventWakeClaude(t),
	}
	env.claudeDir = filepath.Join(env.home, ".claude")
	env.secret = relaySecret
	stateEnv := map[string]string{
		"XDG_STATE_HOME":      filepath.Join(root, "state"),
		"XDG_CONFIG_HOME":     env.config,
		"HOME":                env.home,
		"CLAUDE_CONFIG_DIR":   env.claudeDir,
		"GIT_CONFIG_GLOBAL":   "/dev/null",
		"GIT_CONFIG_NOSYSTEM": "1",
	}
	for key, value := range stateEnv {
		t.Setenv(key, value)
	}
	env.environ = os.Environ()
	f.env = env
	f.getenv = func(key string) string { return stateEnv[key] }
	f.agentDir = filepath.Join(root, "agent-"+routingHostB)
	if err = os.Mkdir(f.agentDir, 0o700); err != nil {
		t.Fatalf("make agent %s state directory: %v", routingHostB, err)
	}
	f.holders = holderhost.NewLocal(holderhost.LocalConfig{
		Shell: env.shell, Claude: env.claude, Environ: env.environ, StateDir: f.agentDir, CronHook: true,
	})
	f.startAgent(t)
	writeRoutingFile(t, filepath.Join(env.config, "orrery", "hosts.toml"), fmt.Sprintf(
		"name = %q\n\n[[hosts]]\nname = %q\naddress = %q\n", routingController, routingHostB, f.addr,
	))
	writeRoutingFile(t, filepath.Join(env.claudeDir, "agents", routingImplementer+".md"),
		"---\nname: "+routingImplementer+"\ndescription: A synthetic agent\n---\n")
	f.repo, f.base = restartRepo(t, filepath.Join(env.home, "repo"))
	f.storePath = mustStorePath(t, f.getenv)
	storetest.Seed(t, f.storePath)
	f.args = []string{
		"--sessions", "--session-shell", env.shell, "--claude", env.claude,
		"--socket", filepath.Join(root, "orreryd.sock"), "--host", routingController,
		"--fleet-secret", relaySecretFile(t),
	}
	f.startController(t)
	return f
}

// restartRepo makes a repository at dir with one commit, and returns its path and that commit. The fake claude writes
// its release file into the worktree, so the repository excludes it.
func restartRepo(t *testing.T, dir string) (string, string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("make repository: %v", err)
	}
	buildWiringGit(t, dir, "init", "-q", "-b", "main")
	buildWiringGit(t, dir, "config", "user.name", "Test")
	buildWiringGit(t, dir, "config", "user.email", "test@example.invalid")
	buildWiringGit(t, dir, "config", "commit.gpgsign", "false")
	writeRoutingFile(t, filepath.Join(dir, ".git", "info", "exclude"), "fakeclaude.*\n")
	writeRoutingFile(t, filepath.Join(dir, "README"), "base\n")
	buildWiringGit(t, dir, "add", "-A")
	buildWiringGit(t, dir, "commit", "-q", "-m", "base")
	return dir, buildWiringGit(t, dir, "rev-parse", "HEAD")
}

// startAgent starts agent b's Serve on a listener. The first start takes a loopback port of its own, and each later
// start listens on that same address, so the controller's hosts file still names it. The agent keeps the holders under
// its state directory, and its launches are recorded in the fixture's log.
func (f *restartFixture) startAgent(t *testing.T) {
	t.Helper()
	listenAddr := net.JoinHostPort(routingAddrB, "0")
	if f.addr != "" {
		listenAddr = f.addr
	}
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", listenAddr)
	if err != nil {
		t.Fatalf("listen for agent %s on %s: %v", routingHostB, listenAddr, err)
	}
	f.addr = ln.Addr().String()
	agent, err := node.NewAgent(node.AgentConfig{
		Controller: routingNode, WhoIs: routingWhoIs{},
		Holders:  restartHolders{Local: f.holders, log: f.log},
		StateDir: f.agentDir,
		Roots:    []string{f.env.root, managedDir}, ConfigDir: f.env.config, ClaudeDir: f.env.claudeDir,
		Local: hostexec.NewLocal(), Secret: f.env.secret,
	})
	if err != nil {
		t.Fatalf("new agent %s: %v", routingHostB, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- agent.Serve(ctx, ln) }()
	f.agentCancel, f.agentServed = cancel, served
}

// stopAgent stops the agent that serves b, and returns the error its Serve returned. Its listener closes, so b stops
// answering. It does nothing when no agent serves.
func (f *restartFixture) stopAgent() error {
	if f.agentCancel == nil {
		return nil
	}
	cancel, served := f.agentCancel, f.agentServed
	f.agentCancel, f.agentServed = nil, nil
	cancel()
	return <-served
}

// startController starts the controller over the fixture's store and state directory, with b listed in its hosts
// file. The controller's logger carries its reach changes to the fixture's restartLog.
func (f *restartFixture) startController(t *testing.T) {
	t.Helper()
	st, err := store.Open(t.Context(), f.storePath, storetest.NoSync())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	f.store = st
	opts, err := parseFlags(f.args, f.getenv)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	opts.orrery = routingController
	logger := slog.New(restartLogHandler{log: f.reaches})
	host, err := startSessions(t.Context(), opts, routingController, st, &recordingRouter{}, nil, logger, f.getenv)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}
	f.host = host
}

// restartController stops the controller and starts it again over the same store and state directory, as a restart
// does.
func (f *restartFixture) restartController(t *testing.T) {
	t.Helper()
	if err := f.host.stop(); err != nil {
		t.Fatalf("stop controller: %v", err)
	}
	f.host = nil
	if err := f.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	f.store = nil
	f.startController(t)
}

// cleanup stops the controller, closes its store, stops agent b and removes the state directory, in that order, so the
// controller's calls end before the agent leaves.
func (f *restartFixture) cleanup(t *testing.T) {
	t.Helper()
	if f.host != nil {
		if err := f.host.stop(); err != nil {
			t.Errorf("stop controller: %v", err)
		}
	}
	if f.store != nil {
		if err := f.store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	}
	if err := f.stopAgent(); err != nil {
		t.Errorf("stop agent %s: %v", routingHostB, err)
	}
	if err := os.RemoveAll(f.root); err != nil {
		t.Errorf("remove %s: %v", f.root, err)
	}
}

// launches returns how many times b launched the session id.
func (f *restartFixture) launches(id string) int {
	n := 0
	for _, e := range f.log.snapshot() {
		if e.kind == "launch" && e.host == routingHostB && e.session == id {
			n++
		}
	}
	return n
}

// restartState is what the test reads of the step on b: its engine row and its step row.
type restartState struct {
	engine store.EngineRow
	step   store.StepRow
}

// state reads the engine row and the step row of the step id on b.
func (f *restartFixture) state(t *testing.T, id string) restartState {
	t.Helper()
	engine, err := f.store.EngineSession(t.Context(), id)
	if err != nil {
		t.Fatalf("read engine row of step %s: %v", id, err)
	}
	step, err := f.host.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("read step row of step %s: %v", id, err)
	}
	return restartState{engine: engine, step: step}
}

// same reports whether s and o hold the same host, state, reason, end and return of the step, which a restart or an
// outage leaves as it is.
func (s restartState) same(o restartState) bool {
	return s.engine.Host == o.engine.Host && s.engine.State == o.engine.State && s.engine.Reason == o.engine.Reason &&
		s.step.End == o.step.End && s.step.Status == o.step.Status && s.step.Result == o.step.Result &&
		s.step.Reason == o.step.Reason
}

// awaitStepEnds returns the rows of the steps that the subscription sub reports ended, by id, once n of them have
// ended, and fails the test when the test's context ends first. The subscription reports each end once.
func awaitStepEnds(t *testing.T, sub *enginehost.StepSubscription, n int) map[string]store.StepRow {
	t.Helper()
	ended := map[string]store.StepRow{}
	for len(ended) < n {
		select {
		case <-sub.Ready():
		case <-t.Context().Done():
			t.Fatalf("%d of %d steps ended before the test did", len(ended), n)
		}
		for _, row := range sub.Take() {
			if row.End != "" {
				ended[row.ID] = row
			}
		}
	}
	return ended
}

// TestRemoteRestart runs the controller and agent b in this process, and checks that step sessions on b outlive a
// restart of the controller, a restart of b's agent and an outage of b. Both steps run on b throughout, and nothing
// relaunches them: each restart re-attaches them through b. While b does not answer, neither step ends or fails. Once b
// answers, the step whose holder is still there resumes and ends once with its return stored, and the step whose holder
// is gone ends failed.
func TestRemoteRestart(t *testing.T) {
	f := newRestartFixture(t)
	steps := []string{restartStepID, restartLostStepID}
	t.Run("a controller restart re-attaches the steps on b, which keep running", func(t *testing.T) {
		for _, id := range steps {
			if _, err := f.host.steps.Start(t.Context(), enginehost.StepStart{
				ID: id, Agent: routingImplementer, Prompt: restartPrompt,
				Workspace: f.repo, Revision: f.base, ReadOnly: true, Host: routingHostB, Schema: routingStatusSchema,
			}); err != nil {
				t.Fatalf("start step %s on b: %v", id, err)
			}
			if n := f.launches(id); n != 1 {
				t.Fatalf("b launched step %s %d times before the restart, want 1", id, n)
			}
		}
		f.restartController(t)
		holders, err := f.holders.Sessions(t.Context())
		if err != nil {
			t.Fatalf("list holders on %s: %v", routingHostB, err)
		}
		for _, id := range steps {
			state := f.state(t, id)
			if state.engine.Host != routingHostB || state.engine.State != "working" {
				t.Errorf("the restarted controller stores step %s as host %q in state %q, want host %q working",
					id, state.engine.Host, state.engine.State, routingHostB)
			}
			if state.step.End != "" {
				t.Errorf("the controller restart ended step %s: %q", id, state.step.End)
			}
			if !f.host.manager.StepLive(id) {
				t.Errorf("the restarted controller holds no live session for step %s", id)
			}
			if n := f.launches(id); n != 1 {
				t.Errorf("the controller restart launched step %s again: %d launches, want 1", id, n)
			}
			if !slices.Contains(holders, id) {
				t.Errorf("b has no holder of step %s after the controller restart: holders %v", id, holders)
			}
		}
	})
	t.Run("a restart of b's agent keeps the steps running on b", func(t *testing.T) {
		// The controller's link to each holder is dialed again through the new agent. That the link re-attached is
		// shown by the first step's return reaching the controller at the end of the test, since that return can come
		// only through the link.
		if err := f.stopAgent(); err != nil {
			t.Fatalf("stop agent %s: %v", routingHostB, err)
		}
		f.startAgent(t)
		for _, id := range steps {
			state := f.state(t, id)
			if state.engine.State != "working" || state.step.End != "" {
				t.Errorf("the restart of b's agent left step %s %q, ended %q, want working and not ended",
					id, state.engine.State, state.step.End)
			}
			if n := f.launches(id); n != 1 {
				t.Errorf("the restart of b's agent launched step %s again: %d launches, want 1", id, n)
			}
		}
	})
	t.Run("an outage of b neither ends nor fails the steps, through a controller restart", func(t *testing.T) {
		before := map[string]restartState{}
		for _, id := range steps {
			before[id] = f.state(t, id)
		}
		reached := f.reaches.count(routingHostB, true)
		if err := f.stopAgent(); err != nil {
			t.Fatalf("stop agent %s: %v", routingHostB, err)
		}
		if !f.reaches.await(t.Context(), routingHostB, true, reached+1) {
			t.Fatalf("the controller never read %s unreachable", routingHostB)
		}
		// The holder of the second step is cleared while b does not answer. The holder is under b's state directory,
		// which the clear reaches without the agent, and the clear stops the holder's process. So the step's holder is
		// gone when b answers.
		if err := f.holders.Clear(t.Context(), restartLostStepID); err != nil {
			t.Fatalf("clear the holder of step %s: %v", restartLostStepID, err)
		}
		f.restartController(t)
		// The restarted controller reads b unreachable too, which the test waits for before it reads the steps.
		if !f.reaches.await(t.Context(), routingHostB, true, reached+2) {
			t.Fatalf("the restarted controller never read %s unreachable", routingHostB)
		}
		for _, id := range steps {
			after := f.state(t, id)
			if !before[id].same(after) {
				t.Errorf("the outage of %s and the controller restart changed step %s: before %+v, after %+v",
					routingHostB, id, before[id], after)
			}
			if after.step.End != "" {
				t.Errorf("step %s ended during the outage of %s: %q", id, routingHostB, after.step.End)
			}
		}
	})
	t.Run("b answering resumes the step with its holder, and fails the step whose holder is gone", func(t *testing.T) {
		reachable := f.reaches.count(routingHostB, false)
		f.startAgent(t)
		if !f.reaches.await(t.Context(), routingHostB, false, reachable+1) {
			t.Fatalf("the controller never read %s reachable again", routingHostB)
		}
		sub, err := f.host.steps.Subscribe(t.Context(), steps)
		if err != nil {
			t.Fatalf("subscribe to the steps: %v", err)
		}
		defer sub.Close()
		state := f.state(t, restartStepID)
		if state.step.End != "" {
			t.Fatalf("step %s ended before it was released: %q", restartStepID, state.step.End)
		}
		if err = os.WriteFile(filepath.Join(state.step.Worktree, fakeClaudeRelease), nil, 0o600); err != nil {
			t.Fatalf("release the fake claude of step %s: %v", restartStepID, err)
		}
		ended := awaitStepEnds(t, sub, len(steps))
		end := ended[restartStepID]
		if end.End != "done" || end.Result == "" {
			t.Errorf("step %s ended %q with %q, want done with its return stored", restartStepID, end.End, end.Result)
		}
		lost := ended[restartLostStepID]
		if lost.End != "failed" || lost.Reason == "" {
			t.Errorf("step %s ended %q for %q, want failed with its reason", restartLostStepID, lost.End, lost.Reason)
		}
		for _, id := range steps {
			if n := f.launches(id); n != 1 {
				t.Errorf("step %s was launched %d times, want 1", id, n)
			}
		}
		// Each end is stored once: a restart after the ends leaves each ended step as it is.
		f.restartController(t)
		for _, id := range steps {
			want, after := ended[id], f.state(t, id)
			if after.step.End != want.End || after.step.Result != want.Result || !after.step.Ended.Equal(want.Ended) {
				t.Errorf("step %s changed after its end and a restart: ended %q with %q, now %q with %q",
					id, want.End, want.Result, after.step.End, after.step.Result)
			}
		}
	})
}
