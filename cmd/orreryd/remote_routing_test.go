package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/hostexec/hostexectest"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// Synthetic names and addresses only. routingController is the controller's own host name, which --host names, and
// routingNode is the controller's node on the tailnet, which each agent admits. Agents a and b listen on their own
// loopback addresses, so the fake WhoIs names each by its address. routingEpic is the epic stored on host b, and
// routingLocalEpic the epic stored on the controller.
const (
	routingController   = "synthetic-controller-host"
	routingNode         = "synthetic-controller"
	routingEpic         = "synthetic-repo-0a1b2c3/epic-remote-routing"
	routingLocalEpic    = "synthetic-repo-4d5e6f7/epic-local-routing"
	routingHostA        = "a"
	routingHostB        = "b"
	routingAddrA        = "127.0.0.2"
	routingAddrB        = "127.0.0.3"
	routingControllerIP = "127.0.0.1"
	// routingStepID is the id of the no-epic step the test starts on host b, in the form a step id takes.
	routingStepID = "5e0bb2c4d7a1490f8e3c6d2a9b1f0e74"
)

// Agents and roles the epics' steps run under. Their files are written under the claude config directory, which each
// agent admits.
const (
	routingImplementer = "orrery-implementer"
	routingReviewer    = "orrery-reviewer"
	routingJudge       = "orrery-judge"
	routingReleaser    = "orrery-releaser"
	routingPlanner     = "orrery-planner"
)

// routingStepTrigger is the keyword the fake claude answers as the implementer. It is the unused $0 of the gate's argv,
// which the implementer's brief names as the gate it runs, and which the reviewer's prompt does not carry, so the
// reviewer is not answered as an implementer that commits.
const routingStepTrigger = "STEPDONE"

// routingReleaseStep is the release step the epic on host b runs, and routingReleaseThen the promise its release judge
// checks. The step's text holds RELEASEDONE, which the fake claude answers with one commit and the evidence that releases
// the epic. The promise holds no keyword, since the release step's prompt carries it too.
const (
	routingReleaseStep = "RELEASEDONE tag the release"
	routingReleaseThen = "the release is tagged on main"
)

// routingStatusSchema is the schema of a step's return that the no-epic step names: an object whose status is a string.
const routingStatusSchema = `{"type":"object","additionalProperties":false,"required":["status"],` +
	`"properties":{"status":{"type":"string"},"summary":{"type":"string"}}}`

// routingConclusion is the conclusion the evidence judge checks. It holds JUDGEHOLDS, which the fake claude answers as
// a judge that holds it.
const routingConclusion = "JUDGEHOLDS"

// routingWhoIs names each peer by the IP of its address. The controller, at routingControllerIP, is routingNode, and
// every other address is an agent's own loopback address, which names no node the agents admit. Every peer is signed in
// as relayOwnerLogin, the login the agents admit.
type routingWhoIs struct{}

// WhoIs implements node.WhoIs.
func (routingWhoIs) WhoIs(_ context.Context, addr string) (*apitype.WhoIsResponse, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("split %s: %w", addr, err)
	}
	peer := &tailcfg.Node{}
	if host == routingControllerIP {
		peer.ComputedName = routingNode
	}
	return &apitype.WhoIsResponse{Node: peer, UserProfile: &tailcfg.UserProfile{LoginName: relayOwnerLogin}}, nil
}

// routingEnv is what the controller and the agents share: the shell and the fake claude, the environment holders run
// under, the directories the agents admit, and the fleet secret.
type routingEnv struct {
	root, home, config, claudeDir string
	shell, claude                 string
	environ                       []string
	secret                        string
}

// routingEvent is one thing the test watches: a session that an agent's holders launched, or a git call the controller
// made on a host. A launch names the host, the session and the claude agent it named. A git call names the host, the
// repository and the arguments.
type routingEvent struct {
	kind    string
	host    string
	session string
	agent   string
	dir     string
	args    []string
}

// routingLog records the events of a run in the order they happened. await waits on a signal that closes each time an
// event is added, so a waiter sees every event recorded before it waits as well as each one after.
type routingLog struct {
	mu      sync.Mutex
	events  []routingEvent
	changed chan struct{}
}

// newRoutingLog returns an empty routingLog.
func newRoutingLog() *routingLog {
	return &routingLog{changed: make(chan struct{})}
}

// add records e, and wakes each waiter.
func (l *routingLog) add(e routingEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
	close(l.changed)
	l.changed = make(chan struct{})
}

// snapshot returns the events recorded so far.
func (l *routingLog) snapshot() []routingEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.events)
}

// await returns once ok holds for the events recorded so far, and reports false when ctx ends first.
func (l *routingLog) await(ctx context.Context, ok func([]routingEvent) bool) bool {
	for {
		l.mu.Lock()
		if ok(l.events) {
			l.mu.Unlock()
			return true
		}
		changed := l.changed
		l.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return false
		}
	}
}

// launchedAgents returns the claude agents named by the launches on host, each once.
func (l *routingLog) launchedAgents(host string) []string {
	var out []string
	for _, e := range l.snapshot() {
		if e.kind == "launch" && e.host == host && e.agent != "" && !slices.Contains(out, e.agent) {
			out = append(out, e.agent)
		}
	}
	return out
}

// routingHolders is an agent's holder host in these tests. Before a launch it writes the fake claude's release file into
// the session's directory, so the fake claude answers its turn as soon as it starts. After a launch it records the
// launch, and for a planner it answers the planner's turn. Every other op is the agent's Local's.
type routingHolders struct {
	*holderhost.Local
	host string
	log  *routingLog
	// planned answers a planner's launch, as the planner's own return would, with the planner's turn completed.
	planned func(ctx context.Context) error
}

// Launch releases the session's fake claude, starts the session as the Local does, records it, and, for a planner,
// answers its turn.
func (h routingHolders) Launch(ctx context.Context, spec holderhost.Spec) (*session.Session, error) {
	if err := os.WriteFile(filepath.Join(spec.Dir, fakeClaudeRelease), nil, 0o600); err != nil {
		return nil, fmt.Errorf("release the fake claude of %s: %w", spec.Session, err)
	}
	sess, err := h.Local.Launch(ctx, spec)
	if err != nil {
		return nil, err
	}
	h.log.add(routingEvent{kind: "launch", host: h.host, session: spec.Session, agent: spec.Agent})
	if spec.Agent == routingPlanner && h.planned != nil {
		if err = h.planned(ctx); err != nil {
			return nil, fmt.Errorf("answer the planner of %s: %w", spec.Session, err)
		}
	}
	return sess, nil
}

// fakeClaudeRelease is the file the fake claude waits for in its directory before it answers the turn that waits.
const fakeClaudeRelease = "fakeclaude.release"

// routingGit is a host the controller reaches in these tests: a Recorder of its calls, and the log each git call goes
// to after it returns. Once a worktree is added the fake claude of the step that runs in it is released, as the agents'
// holders release a session before it starts.
type routingGit struct {
	*hostexectest.Recorder
	host string
	log  *routingLog
}

// Git records the call through the Recorder, then logs it, and releases the fake claude of a worktree it adds.
func (g routingGit) Git(ctx context.Context, call hostexec.GitCall) ([]byte, error) {
	out, err := g.Recorder.Git(ctx, call)
	if err != nil {
		return out, err
	}
	if path := worktreePath(call.Args); path != "" {
		if err = os.WriteFile(filepath.Join(path, fakeClaudeRelease), nil, 0o600); err != nil {
			return out, fmt.Errorf("release the fake claude of worktree %s: %w", path, err)
		}
	}
	g.log.add(routingEvent{kind: "git", host: g.host, dir: call.Dir, args: slices.Clone(call.Args)})
	return out, nil
}

// worktreePath returns the directory that the git arguments args add as a worktree, which is the first absolute path
// after worktree add, and the empty string when args add none.
func worktreePath(args []string) string {
	if len(args) < 2 || args[0] != "worktree" || args[1] != "add" {
		return ""
	}
	for _, arg := range args[2:] {
		if filepath.IsAbs(arg) {
			return arg
		}
	}
	return ""
}

// startRoutingAgent serves an agent on ln until the test ends, with its holders under a state directory of its own. The
// agent admits the controller's node, its config and claude directories, the managed agent directory, and everything under
// the test's root, where the epics' repositories and the controller's state directory are. Its launches are logged to log,
// and planned answers each planner it launches.
func startRoutingAgent(
	t *testing.T, env routingEnv, name string, ln net.Listener, log *routingLog,
	planned func(ctx context.Context) error,
) {
	t.Helper()
	stateDir := filepath.Join(env.root, "agent-"+name)
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatalf("make agent %s state directory: %v", name, err)
	}
	holders := holderhost.NewLocal(holderhost.LocalConfig{
		Shell: env.shell, Claude: env.claude, Environ: env.environ, StateDir: stateDir, CronHook: true,
	})
	agent, err := node.NewAgent(node.AgentConfig{
		Controller: routingNode, WhoIs: routingWhoIs{},
		Holders:  routingHolders{Local: holders, host: name, log: log, planned: planned},
		StateDir: stateDir,
		Roots:    []string{env.root, managedDir}, ConfigDir: env.config, ClaudeDir: env.claudeDir,
		Local: hostexec.NewLocal(), Secret: env.secret,
	})
	if err != nil {
		t.Fatalf("new agent %s: %v", name, err)
	}
	// The agent serves until its cleanup, which runs after the controller's host stops, so the controller's calls end
	// before the agent leaves. The test's own context ends before the cleanups run, so the agent's does not derive from it.
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- agent.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("serve agent %s: %v", name, err)
		}
	})
}

// routingFixture is the controller and its two agents, all in this process over real listeners. recorders holds the
// recorder of each host the controller reaches, by the host's name, so each host's calls are seen.
type routingFixture struct {
	env       routingEnv
	stateDir  string
	storePath string
	repo      string
	base      string
	localRepo string
	localBase string
	store     *store.Store
	host      *sessionHost
	recorders map[string]*hostexectest.Recorder
	// routed holds the host that startSessions puts in its hostexec.Map under each name, before the recorder wraps it.
	routed map[string]hostexec.Host
	log    *routingLog
	// ended closes when the store ends the epic on host b, which is when that epic is released.
	ended chan struct{}
}

// newRoutingFixture starts the controller over a store of its own, with agents a and b listening on their loopback
// addresses. Each agent is listed in the hosts file, and the controller reaches each one through a node.Client that
// sends the fleet secret. The epic on host b is seeded before the controller starts. The epic on the controller is seeded
// by its own subtest, after the checks of b's work, so the controller's Local has no work of its own while b's runs.
func newRoutingFixture(t *testing.T) *routingFixture {
	t.Helper()
	root, err := shortStateRoot()
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove %s: %v", root, err)
		}
	})
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
	getenv := func(key string) string { return stateEnv[key] }

	listenA, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", net.JoinHostPort(routingAddrA, "0"))
	if err != nil {
		t.Fatalf("listen for agent a: %v", err)
	}
	listenB, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", net.JoinHostPort(routingAddrB, "0"))
	if err != nil {
		t.Fatalf("listen for agent b: %v", err)
	}
	f := &routingFixture{
		env: env, recorders: map[string]*hostexectest.Recorder{}, routed: map[string]hostexec.Host{},
		log: newRoutingLog(), ended: make(chan struct{}),
	}
	planned := func(ctx context.Context) error { return f.answerPlanner(ctx) }
	startRoutingAgent(t, env, routingHostA, listenA, f.log, nil)
	startRoutingAgent(t, env, routingHostB, listenB, f.log, planned)

	writeRoutingFile(t, filepath.Join(env.config, "orrery", "hosts.toml"), fmt.Sprintf(
		"name = %q\n\n[[hosts]]\nname = %q\naddress = %q\n\n[[hosts]]\nname = %q\naddress = %q\n",
		routingController,
		routingHostA, listenA.Addr().String(),
		routingHostB, listenB.Addr().String(),
	))
	writeRoutingFile(t, filepath.Join(env.config, "orrery", "roles.toml"), fmt.Sprintf(
		"planner = %q\nimplementer = %q\nreviewer = %q\njudge = %q\nrelease = %q\n",
		routingPlanner, routingImplementer, routingReviewer, routingJudge, routingReleaser,
	))
	for _, agent := range []string{routingPlanner, routingImplementer, routingReviewer, routingJudge, routingReleaser} {
		writeRoutingFile(t, filepath.Join(env.claudeDir, "agents", agent+".md"),
			"---\nname: "+agent+"\ndescription: A synthetic agent\n---\n")
	}
	secretFile := relaySecretFile(t)

	f.repo, f.base = f.routingRepo(t, filepath.Join(env.home, "repo"))
	f.localRepo, f.localBase = f.routingRepo(t, filepath.Join(env.home, "local-repo"))
	f.storePath = mustStorePath(t, getenv)
	f.store = openPaneStore(t, f.storePath)
	f.seedEpic(t, routingEpic, routingHostB, f.repo, f.base, true)
	stateDir, err := stateDirOf(getenv)
	if err != nil {
		t.Fatalf("state directory: %v", err)
	}
	f.stateDir = stateDir

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	opts, err := parseFlags([]string{
		"--sessions", "--session-shell", env.shell, "--claude", env.claude,
		"--socket", filepath.Join(root, "orreryd.sock"), "--host", routingController, "--fleet-secret", secretFile,
	}, getenv)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	opts.orrery = routingController
	opts.hostWrap = func(name string, host hostexec.Host) hostexec.Host {
		f.routed[name] = host
		rec := hostexectest.NewRecorder(host)
		f.recorders[name] = rec
		return routingGit{Recorder: rec, host: name, log: f.log}
	}
	host, err := startSessions(t.Context(), opts, routingController, f.store, &recordingRouter{}, nil, logger, getenv)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}
	f.host = host
	t.Cleanup(func() {
		if err := host.stop(); err != nil {
			t.Errorf("stop sessions: %v", err)
		}
	})
	// startSessions wraps the controller's Local and host b's RemoteHost by name, so each one's calls are recorded.
	for _, name := range []string{routingController, routingHostB} {
		if f.recorders[name] == nil {
			t.Fatalf("startSessions reaches host %s through no recorder", name)
		}
	}
	// startSessions gives the store's epic-ended hook to the reports, so the test sets its own once the host runs. The
	// epic may have ended before the hook was set, so its phase is read once as well.
	var endOnce sync.Once
	endSignal := func() { endOnce.Do(func() { close(f.ended) }) }
	f.store.OnEpicEnded(func(epic string) {
		if epic == routingEpic {
			endSignal()
		}
	})
	if row, err := f.store.Epic(t.Context(), routingEpic); err != nil {
		t.Fatalf("read epic %s: %v", routingEpic, err)
	} else if row.Phase == "done" || row.Phase == "released" {
		endSignal()
	}
	return f
}

// mustStorePath returns the store's path under getenv, failing the test when it has none.
func mustStorePath(t *testing.T, getenv func(string) string) string {
	t.Helper()
	path, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store path: %v", err)
	}
	return path
}

// writeRoutingFile writes content to path, making its directory first.
func writeRoutingFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make directory of %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// routingRepo makes a repository at dir with one commit, and returns its path and that commit. Its project file declares
// the pool of its gate on the controller, which owns the pool: the gate's grant is the controller's, and the gate runs on
// the host that the epic names. The repository has a bare origin, since a release reads it. The fake claude writes its
// files into the worktrees, so the repository excludes them.
func (f *routingFixture) routingRepo(t *testing.T, dir string) (string, string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("make repository: %v", err)
	}
	buildWiringGit(t, dir, "init", "-q", "-b", "main")
	buildWiringGit(t, dir, "config", "user.name", "Test")
	buildWiringGit(t, dir, "config", "user.email", "test@example.invalid")
	buildWiringGit(t, dir, "config", "commit.gpgsign", "false")
	writeRoutingFile(t, filepath.Join(dir, ".git", "info", "exclude"), "fakeclaude.*\n")
	writeRoutingFile(t, filepath.Join(dir, ".orrery", "project.toml"), fmt.Sprintf(
		"[pools.%s]\nhost = %q\ncap = 1\nmembers = [\"m1\"]\n"+
			"wake = [%q, \"-c\", \"exit 0\"]\nwake_limit = 60\n"+
			"status = [%q, \"-c\", \"exit 0\"]\nstatus_limit = 60\n"+
			"admit = [%q, \"-c\", \"exit 0\"]\nadmit_limit = 60\n",
		gates.DefaultPool, routingController, f.env.shell, f.env.shell, f.env.shell,
	))
	writeRoutingFile(t, filepath.Join(dir, "README"), "base\n")
	buildWiringGit(t, dir, "add", "-A")
	buildWiringGit(t, dir, "commit", "-q", "-m", "base")
	origin := dir + ".origin.git"
	buildWiringGit(t, filepath.Dir(dir), "clone", "-q", "--bare", dir, origin)
	buildWiringGit(t, dir, "remote", "add", "origin", origin)
	return dir, buildWiringGit(t, dir, "rev-parse", "HEAD")
}

// routingWorkflowFile is the path the routing workflow is read from, and routingWorkflowText is its text. The workflow
// runs the epic's base checks, plans and builds and integrates its task, reviews it, concludes, and releases. It has no
// correction loop, whose round the fake claude cannot end with a plan that plans no task.
const (
	routingWorkflowName = "routing"
	routingWorkflowFile = ".orrery/workflows/" + routingWorkflowName + ".yaml"
	routingWorkflowText = "name: " + routingWorkflowName + "\nnodes:\n" +
		"  - id: red\n    kind: evidence\n    at: base\n" +
		"  - id: cost\n    kind: cost\n    at: base\n" +
		"  - id: plan\n    kind: plan\n    body:\n      - id: build\n        kind: build\n" +
		"      - id: integrate\n        kind: integrate\n" +
		"  - id: review\n    kind: review\n" +
		"  - id: conclude\n    kind: conclude\n" +
		"  - id: release\n    kind: release\n"
)

// routingWorkflow returns the workflow an epic pins, as an epic stores it: its canonical text, its sha256 and its nodes
// in preorder.
func routingWorkflow(t *testing.T) store.EpicWorkflow {
	t.Helper()
	pinned, err := workflow.Load(routingWorkflowName, func(path string) ([]byte, bool, error) {
		if path != routingWorkflowFile {
			return nil, false, nil
		}
		return []byte(routingWorkflowText), true, nil
	})
	if err != nil {
		t.Fatalf("load workflow %s: %v", routingWorkflowName, err)
	}
	return store.EpicWorkflow{
		Name: pinned.Name, Text: pinned.Text, SHA256: pinned.SHA256, Nodes: routingNodeRows(pinned.Workflow.Nodes, ""),
	}
}

// routingNodeRows returns the stored rows of nodes under parent, each followed by its body, as an epic stores them.
func routingNodeRows(nodes []workflow.Node, parent string) []store.EpicWorkflowNode {
	rows := make([]store.EpicWorkflowNode, 0, len(nodes))
	for _, node := range nodes {
		kind := node.Kind
		if node.Loop != nil {
			kind = "loop"
		}
		rows = append(rows, store.EpicWorkflowNode{ID: node.ID, Parent: parent, Kind: kind})
		rows = append(rows, routingNodeRows(node.Body, node.ID)...)
	}
	return rows
}

// seedEpic stores epic id on host, over repo at base, with one planned task at the implement stage. Its gate is a command
// gate that runs on host, and its review an agent gate, which the task and the epic each review through. The epic's
// workflow is routingWorkflow, and its plan node runs while the task's turn does, so the task's build and integrate nodes
// fan out from it. The full epic also has a requirement, whose evidence is judged, and the release step it ships with.
// The epic on the controller is seeded without them: the test answers the planners of host b only, and a requirement
// would wake a planner on the controller, which nothing answers.
func (f *routingFixture) seedEpic(t *testing.T, epic, host, repo, base string, full bool) {
	t.Helper()
	ctx := t.Context()
	created := time.Unix(plannerWiringStamp, 0)
	var requirements []store.EpicRequirement
	var releaseSteps []store.EpicReleaseStep
	var reqIDs []string
	if full {
		requirements = []store.EpicRequirement{{
			ID: "R1", Text: "the build runs", Evidence: "routing test", Kind: "test", Item: routingItem(),
		}}
		releaseSteps = []store.EpicReleaseStep{{Step: routingReleaseStep, Target: "main", Undo: "none"}}
		reqIDs = []string{"R1"}
	}
	if err := f.store.CreateEpic(ctx, store.EpicRow{
		ID: epic, Repository: repo, Base: base, Host: host,
		Title: "Route the epic", Approved: "approved text", Intent: "run the build on " + host,
		Care: "severe", Approach: "one implementer", ReleaseThen: routingReleaseThen, Checks: "just check",
		Phase: "accepted", Created: created, Changed: created,
		ReviewGates:  []string{"review"},
		Workflow:     routingWorkflow(t),
		ReleaseSteps: releaseSteps,
		Gates: []store.EpicGate{
			{Name: "check", Argv: []string{f.env.shell, "-c", "exit 0", routingStepTrigger}, Limit: 60,
				Needs: []store.EpicGateNeed{{Pool: gates.DefaultPool, Count: 1}}},
			{Name: "review", Agent: routingReviewer, Limit: 60, Must: []string{routingConclusion}},
		},
		Reviewers: []reviewers.Reviewer{{
			Name: "review", Enabled: true, Stage: []string{reviewers.StageTask, reviewers.StageEpic},
			Agent: routingReviewer, Limit: 60, Must: []string{routingConclusion}, Verify: true,
		}},
		Requirements: requirements,
		Exclusions:   []string{"Do not change the store schema"},
		Decisions:    []store.EpicDecision{{Text: "Use one implementer per task"}},
	}); err != nil {
		t.Fatalf("create epic %s: %v", epic, err)
	}
	// The plan node runs while its turn does, so the turn's task fans out to the plan's build and integrate nodes. The
	// red and cost checks end passed before the plan starts, and the plan is left running once its turn completes, so
	// the flow ends it after those nodes.
	runs := []store.NodeRun{
		{Path: "red", ID: store.NodeRunID(epic, "red", 0, "start"), Node: "red", Kind: "evidence", Started: created},
		{Path: "cost", ID: store.NodeRunID(epic, "cost", 0, "start"), Node: "cost", Kind: "cost", Started: created},
		{Path: "plan", ID: store.NodeRunID(epic, "plan", 0, "start"), Node: "plan", Kind: "plan", Started: created},
	}
	if err := f.store.StartNodes(ctx, epic, runs, nil, created); err != nil {
		t.Fatalf("start the node runs of %s: %v", epic, err)
	}
	if err := f.store.EndNodes(ctx, epic, []store.NodeEnd{
		{Path: "red", Outcome: "passed"}, {Path: "cost", Outcome: "passed"},
	}, created); err != nil {
		t.Fatalf("end the red and cost node runs of %s: %v", epic, err)
	}
	if _, err := f.store.ClaimTurn(ctx, epic, created); err != nil {
		t.Fatalf("claim turn of %s: %v", epic, err)
	}
	if err := f.store.CompleteTurn(ctx, epic, 1, routingPlan(reqIDs, epic == routingLocalEpic), created); err != nil {
		t.Fatalf("complete turn of %s: %v", epic, err)
	}
	stored, err := f.store.Epic(ctx, epic)
	if err != nil {
		t.Fatalf("read epic %s: %v", epic, err)
	}
	if len(stored.Tasks) != 1 {
		t.Fatalf("epic %s has %d tasks, want 1", epic, len(stored.Tasks))
	}
	task := stored.Tasks[0]
	if err := f.store.SetTaskBuild(ctx, epic, task.Seq, task.Build, store.TaskBuild{
		Stage: build.StageImplement, Base: base, Attempt: 1,
	}); err != nil {
		t.Fatalf("set task build of %s: %v", epic, err)
	}
}

// routingItem is the evidence item of the epic's requirement and of its task. It is supplied: the README of the commit,
// which the judge checks against the conclusion, so no capture runs.
func routingItem() evidence.Item {
	return evidence.Item{
		Kind: evidence.KindSupplied, Supplied: []string{"README"}, Limit: 60,
		Conclusion: routingConclusion, Judge: routingJudge,
	}
}

// routingPlan is the planner's return for the epic's one task. Its deliverable names no keyword, since the gate carries the
// step's trigger. Its requirements are reqs, and its evidence is judged. When complete is set, the plan's outcome is
// complete, so the plan ends once its task builds, with no further planner turn.
func routingPlan(reqs []string, complete bool) store.EpicPlan {
	outcome := "the build runs"
	if complete {
		outcome = "complete"
	}
	return store.EpicPlan{
		Tasks: []store.EpicTaskInput{{
			Title:        "Route the build",
			Deliverable:  "the build runs",
			Files:        []string{"wiring.go"},
			Requirements: reqs,
			Gate:         "check",
			Evidence:     routingItem(),
		}},
		Outline: "One task",
		Outcome: outcome,
	}
}

// answerPlanner completes the running turn of the epic on host b with a plan that plans no task, as the planner's own
// return would. The turn ends at the time it started, so the test reads no clock.
func (f *routingFixture) answerPlanner(ctx context.Context) error {
	epic, err := f.store.Epic(ctx, routingEpic)
	if err != nil {
		return err
	}
	for _, turn := range epic.Turns {
		if turn.State != "running" {
			continue
		}
		plan := store.EpicPlan{Outline: "nothing more to plan", Outcome: "complete"}
		return f.store.CompleteTurn(ctx, routingEpic, turn.Seq, plan, turn.Started)
	}
	return nil
}

// routingRows returns the host and kind of every session row of the store, by id, as the store holds them now.
func (f *routingFixture) routingRows(t *testing.T) map[string][2]string {
	t.Helper()
	db, err := sql.Open("sqlite3", f.storePath)
	if err != nil {
		t.Fatalf("open store to read rows: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close store read: %v", err)
		}
	}()
	rows, err := db.Query(`SELECT id, host, kind FROM sessions`)
	if err != nil {
		t.Fatalf("read session rows: %v", err)
	}
	defer rows.Close()
	out := map[string][2]string{}
	for rows.Next() {
		var id, host, kind string
		if err := rows.Scan(&id, &host, &kind); err != nil {
			t.Fatalf("scan session row: %v", err)
		}
		out[id] = [2]string{host, kind}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate session rows: %v", err)
	}
	return out
}

// routingCalls returns the calls rec recorded whose op is op, in order.
func routingCalls(calls []hostexectest.Call, op string) []hostexectest.Call {
	var out []hostexectest.Call
	for _, call := range calls {
		if call.Op == op {
			out = append(out, call)
		}
	}
	return out
}

// routingGitCall reports whether calls holds a git call in dir whose arguments begin with args.
func routingGitCall(calls []hostexectest.Call, dir string, args ...string) bool {
	return slices.ContainsFunc(calls, func(call hostexectest.Call) bool {
		return call.Op == "Git" && call.Path == dir && len(call.Args) >= len(args) &&
			slices.Equal(call.Args[:len(args)], args)
	})
}

// routingUnder reports whether path is dir or lies under it.
func routingUnder(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// routingFileCalls reports whether calls holds a call that reads a file or a directory under dir.
func routingFileCalls(calls []hostexectest.Call, dir string) bool {
	fileOps := []string{"ReadFile", "ReadRooted", "Stat", "Lstat", "ReadDir"}
	return slices.ContainsFunc(calls, func(call hostexectest.Call) bool {
		return slices.Contains(fileOps, call.Op) && routingUnder(call.Path, dir)
	})
}

// routingSite is a line of the NewLocal grep that the test allows: the file the line is in, a snippet of its text, and the
// controller-only work the line serves.
type routingSite struct {
	file, snippet, work string
}

// routingLocalSites returns the lines of the NewLocal grep that the test allows. A line is matched by its text, so an
// edit that moves it keeps its entry.
func routingLocalSites() []routingSite {
	return []routingSite{
		{
			file:    "cmd/orreryd/hosts.go",
			snippet: "routed := hostexec.Map{hostName: opts.wrapHost(hostName, hostexec.NewLocal())}",
			work:    "the controller's own host, which serves the work whose host is the controller",
		},
		{
			file:    "cmd/orreryd/main.go",
			snippet: "hostexec.NewLocal(), os.Environ(), stateDir,",
			work:    "the controller's probe of the Gradle environment its own children build under",
		},
		{
			file:    "internal/enginehost/contractstates.go",
			snippet: "fetchOriginMain(ctx, hostexec.NewLocal(), repository)",
			work:    "the contract stage's fetch of origin, which runs on the controller",
		},
		{
			file:    "internal/enginehost/manager.go",
			snippet: "hostexec.NewLocal(), // serves the controller's own work",
			work:    "the files that the controller's own sessions read agents from",
		},
	}
}

// routingNewLocalLines returns the lines that the NewLocal grep prints, each as its file and its text.
func routingNewLocalLines(t *testing.T) (file []string, text []string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", "grep", "-n", "hostexec.NewLocal()", "--",
		"internal/enginehost", "internal/build", "internal/agents", "cmd/orreryd", ":!*_test.go")
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return nil, nil
	}
	if err != nil {
		t.Fatalf("git grep NewLocal: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			t.Fatalf("git grep line %q has no file, line and text", line)
		}
		file = append(file, parts[0])
		text = append(text, parts[2])
	}
	return file, text
}

// TestRemoteRouting runs the controller and agents a and b in this process, and checks that work whose host is a
// hosts.toml host runs on that host's agent, and that work on the controller runs on its own Local. The epic stored on b
// runs its git, file, worktree, gate and evidence work, and its implementer, reviewer, judge and release sessions, through
// b. A no-epic step that arrives as host b runs on b. The epic stored on the controller runs its work on the controller's
// Local. Each NewLocal call the routed packages make is one the test names.
func TestRemoteRouting(t *testing.T) {
	f := newRoutingFixture(t)
	t.Run("epic on b runs its work through b", func(t *testing.T) {
		select {
		case <-f.ended:
		case <-f.host.flowDone:
			t.Fatalf("the flow loop ended before epic %s was released", routingEpic)
		}
		// The work of the epic reaches b through the host that startSessions maps b's name to, which is b's agent.
		if _, ok := f.routed[routingHostB].(*node.RemoteHost); !ok {
			t.Errorf("startSessions maps host b to %T, want its agent's *node.RemoteHost", f.routed[routingHostB])
		}
		bCalls := f.recorders[routingHostB].Calls()
		if !routingGitCall(bCalls, f.repo, "rev-parse") {
			t.Errorf("host b recorded no git call in %s", f.repo)
		}
		if !routingGitCall(bCalls, f.repo, "worktree", "add") {
			t.Errorf("host b recorded no worktree add in %s", f.repo)
		}
		// The epic's worktrees are made under the state directory, where b reads its files for the steps and gates.
		if !routingFileCalls(bCalls, f.stateDir) {
			t.Errorf("host b recorded no file access under the epic's worktrees in %s", f.stateDir)
		}
		if len(routingCalls(bCalls, "Launch")) == 0 {
			t.Errorf("host b recorded no gate launch")
		}
		agents := f.log.launchedAgents(routingHostB)
		for _, want := range []string{routingImplementer, routingReviewer, routingJudge, routingReleaser} {
			if !slices.Contains(agents, want) {
				t.Errorf("host b launched no %s session: launched %v", want, agents)
			}
		}
		// Each session that b launched for the epic stores b as its host, and the four roles run as steps.
		rows := f.routingRows(t)
		steps := map[string]bool{}
		for _, e := range f.log.snapshot() {
			if e.kind != "launch" || e.host != routingHostB {
				continue
			}
			row, ok := rows[e.session]
			if !ok {
				continue
			}
			if row[0] != routingHostB {
				t.Errorf("session %s launched on b stores host %q, want %q", e.session, row[0], routingHostB)
			}
			if row[1] == "step" {
				steps[e.agent] = true
			}
		}
		for _, want := range []string{routingImplementer, routingReviewer, routingJudge, routingReleaser} {
			if !steps[want] {
				t.Errorf("no step row of the %s session stores host b", want)
			}
		}
		// The evidence runs of the epic passed under judges that b launched, so its evidence work ran through b.
		runs, err := f.store.EvidenceRuns(t.Context(), routingEpic)
		if err != nil {
			t.Fatalf("read the evidence runs of %s: %v", routingEpic, err)
		}
		passed := 0
		for _, run := range runs {
			if !run.Passed {
				continue
			}
			if !slices.ContainsFunc(f.log.snapshot(), func(e routingEvent) bool {
				return e.kind == "launch" && e.host == routingHostB && e.session == run.JudgeStep
			}) {
				t.Errorf("evidence run %s passed under judge %s, which b did not launch", run.ID, run.JudgeStep)
				continue
			}
			passed++
		}
		if passed == 0 {
			t.Errorf("no evidence run of the epic passed under a judge that b launched: %d runs", len(runs))
		}
		// The epic on the controller is not seeded until its own subtest, so the controller's Local has no work of its own
		// here: any call it recorded is work of the epic on b.
		if calls := f.recorders[routingController].Calls(); len(calls) != 0 {
			t.Errorf("the controller's Local recorded %d calls for the epic on b: %v", len(calls), calls)
		}
	})
	t.Run("no-epic step from b runs on b", func(t *testing.T) {
		ctx := t.Context()
		beforeB := len(f.recorders[routingHostB].Calls())
		id, err := f.host.steps.Start(ctx, enginehost.StepStart{
			ID: routingStepID, Agent: routingImplementer, Prompt: "READDONE the step with no epic",
			Workspace: f.repo, Revision: f.base, ReadOnly: true, Host: routingHostB,
			Schema: routingStatusSchema,
		})
		if err != nil {
			t.Fatalf("start the no-epic step on b: %v", err)
		}
		bCalls := f.recorders[routingHostB].Calls()[beforeB:]
		if !routingGitCall(bCalls, f.repo, "rev-parse") {
			t.Errorf("the no-epic step resolved no revision on b: %v", bCalls)
		}
		launched := f.log.await(ctx, func(events []routingEvent) bool {
			return slices.ContainsFunc(events, func(e routingEvent) bool {
				return e.kind == "launch" && e.host == routingHostB && e.session == id
			})
		})
		if !launched {
			t.Errorf("the no-epic step %s was not launched on b", id)
		}
		// Stop ends the step and returns once its loop has, so the step's calls, its worktree's removal included, are all
		// recorded when it returns.
		if err = f.host.steps.Stop(ctx, id); err != nil {
			t.Fatalf("stop the no-epic step %s: %v", id, err)
		}
		if calls := f.recorders[routingController].Calls(); len(calls) != 0 {
			t.Errorf("the controller's Local recorded %d calls for the no-epic step on b: %v", len(calls), calls)
		}
		if got := f.routingRows(t)[id]; got[0] != routingHostB {
			t.Errorf("the no-epic step row %s stores host %q, want %q", id, got[0], routingHostB)
		}
	})
	t.Run("epic on the controller runs its work on the controller's Local", func(t *testing.T) {
		ctx := t.Context()
		f.seedEpic(t, routingLocalEpic, routingController, f.localRepo, f.localBase, false)
		added := f.log.await(ctx, func(events []routingEvent) bool {
			return slices.ContainsFunc(events, func(e routingEvent) bool {
				return e.kind == "git" && e.host == routingController && e.dir == f.localRepo &&
					slices.Contains(e.args, "add")
			})
		})
		if !added {
			t.Fatalf("the epic on the controller added no worktree in %s", f.localRepo)
		}
		if !routingGitCall(f.recorders[routingController].Calls(), f.localRepo, "worktree", "add") {
			t.Errorf("the controller's Local recorded no worktree add in %s", f.localRepo)
		}
		if calls := f.recorders[routingHostB].Calls(); routingTouches(calls, f.localRepo) {
			t.Errorf("host b recorded calls for the epic on the controller: %v", calls)
		}
		row, err := f.store.Epic(ctx, routingLocalEpic)
		if err != nil {
			t.Fatalf("read epic %s: %v", routingLocalEpic, err)
		}
		if row.Host != routingController {
			t.Errorf("epic %s stores host %q, want %q", routingLocalEpic, row.Host, routingController)
		}
	})
	t.Run("NewLocal grep prints only the lines the test names", func(t *testing.T) {
		files, texts := routingNewLocalLines(t)
		sites := routingLocalSites()
		printed := make([]bool, len(sites))
		for i, file := range files {
			matched := false
			for j, site := range sites {
				if file == site.file && strings.Contains(texts[i], site.snippet) {
					matched, printed[j] = true, true
				}
			}
			if !matched {
				t.Errorf("the NewLocal grep prints %s: %s, which the test does not name",
					file, strings.TrimSpace(texts[i]))
			}
		}
		for j, site := range sites {
			if !printed[j] {
				t.Errorf("the test names %s: %s (%s), which the grep no longer prints",
					site.file, site.snippet, site.work)
			}
		}
	})
}

// routingTouches reports whether any call in calls has a path under dir.
func routingTouches(calls []hostexectest.Call, dir string) bool {
	return slices.ContainsFunc(calls, func(call hostexectest.Call) bool { return routingUnder(call.Path, dir) })
}
