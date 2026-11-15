package integration_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/gates"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
	"github.com/joshsymonds/orrery/internal/storetest"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// The tests in this file run a pinned workflow of check nodes through the real orreryd, with the holder scope a
// restart re-adopts a check's gate under. The workflow plans nothing, so the epic holds no task and no planner pass
// runs for it. Each check writes to a file outside the repository, which the test counts: a check that ran twice
// writes twice. They live in internal/integration because they SIGKILL the orreryd binary and restart it over the
// same store.

// nodesName is the name of the workflow the epics pin, and nodesFile is the repository file its text is read from.
const (
	nodesName = "nodes"
	nodesFile = ".orrery/workflows/" + nodesName + ".yaml"
)

// nodesCommandNode and nodesGateNode are the ids of the command and gate nodes the tests run. nodesGate is the epic
// gate the gate node names.
const (
	nodesCommandNode = "append"
	nodesGateNode    = "verify"
	nodesGate        = "check"
)

// nodesCommandText returns a workflow whose one command node runs argv at the base.
func nodesCommandText(argv []string) string {
	text := "name: " + nodesName + "\nnodes:\n  - id: " + nodesCommandNode +
		"\n    kind: command\n    argv:\n"
	for _, arg := range argv {
		text += fmt.Sprintf("      - %q\n", arg)
	}
	return text + "    limit: 60\n    at: base\n"
}

// nodesGateText returns a workflow whose one gate node runs the epic gate nodesGate at the base.
func nodesGateText() string {
	return "name: " + nodesName + "\nnodes:\n  - id: " + nodesGateNode +
		"\n    kind: gate\n    gate: " + nodesGate + "\n    at: base\n"
}

// nodesPin returns the workflow text as an epic pins it: the canonical text Load gives, its sha256 and its nodes in
// preorder.
func nodesPin(t *testing.T, text string) store.EpicWorkflow {
	t.Helper()
	pinned, err := workflow.Load(nodesName, func(path string) ([]byte, bool, error) {
		if path != nodesFile {
			return nil, false, nil
		}
		return []byte(text), true, nil
	})
	if err != nil {
		t.Fatalf("load workflow %s: %v", nodesName, err)
	}
	return store.EpicWorkflow{
		Name:   pinned.Name,
		Text:   pinned.Text,
		SHA256: pinned.SHA256,
		Nodes:  flowNodeRows(pinned.Workflow.Nodes, ""),
	}
}

// newNodesRig builds the rig's repository under the fixture's root, with the default gate pool the project file
// declares for this host, and one commit. The machine's pool file is kept out by an empty config directory.
func newNodesRig(t *testing.T) *buildRig {
	t.Helper()
	f, scope, _ := scopedFixture(t)
	f.env = append(f.env,
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"XDG_CONFIG_HOME="+filepath.Join(f.root, "config"),
	)
	repo := filepath.Join(f.root, "repo")
	stepReadoptGit(t, f.root, "init", "-q", "-b", "main", repo)
	stepReadoptWrite(t, filepath.Join(repo, "README"), "base\n")
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("read host name: %v", err)
	}
	stepReadoptWrite(t, filepath.Join(repo, ".orrery", "project.toml"), fmt.Sprintf(
		"[pools.%s]\nhost = %q\ncap = 1\nmembers = [\"m1\"]\n"+
			"wake = [%q, \"-c\", \"exit 0\"]\nwake_limit = 60\n"+
			"status = [%q, \"-c\", \"exit 0\"]\nstatus_limit = 60\n"+
			"admit = [%q, \"-c\", \"exit 0\"]\nadmit_limit = 60\n",
		gates.DefaultPool, host, f.shell, f.shell, f.shell))
	stepReadoptGit(t, repo, "add", "-A")
	stepReadoptGit(t, repo, "commit", "-q", "-m", "base")
	return &buildRig{
		f:        f,
		scope:    scope,
		stateDir: filepath.Dir(f.dbPath),
		repo:     repo,
		base:     stepReadoptGit(t, repo, "rev-parse", "HEAD"),
	}
}

// seedNodesEpic stores the rig's epic pinned to the workflow text, with the epic gates the text names. The epic has no
// planner turn: its workflow plans nothing, so the planner never passes for it.
func seedNodesEpic(t *testing.T, r *buildRig, text string, epicGates []store.EpicGate) {
	t.Helper()
	ctx := t.Context()
	storetest.Seed(t, r.f.dbPath)
	st, err := store.Open(ctx, r.f.dbPath, storetest.NoSync())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	created := time.Unix(1700000000, 0)
	if err = st.CreateEpic(ctx, store.EpicRow{
		ID:          buildRigEpic,
		Repository:  r.repo,
		Base:        r.base,
		Title:       "Run a pinned check workflow",
		Approved:    "approved text",
		Intent:      "run each check node of a workflow once",
		Care:        "severe",
		Approach:    "check nodes",
		ReleaseThen: "release it",
		Checks:      "just check",
		Phase:       "accepted",
		Created:     created,
		Changed:     created,
		Workflow:    nodesPin(t, text),
		Gates:       epicGates,
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: buildRigRequirement, Evidence: "build test", Kind: "test"},
		},
		Exclusions: []string{buildRigExclusion},
		Decisions:  []store.EpicDecision{{Text: buildRigDecision}},
	}); err != nil {
		t.Fatalf("create epic: %v", err)
	}
	if err = st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
}

// nodeRun returns the node run at path in the rig's epic, failing the test when the epic holds none there.
func (r *buildRig) nodeRun(t *testing.T, path string) sqlcgen.EpicNodeRun {
	t.Helper()
	for _, run := range r.nodeRuns(t) {
		if run.Path == path {
			return run
		}
	}
	t.Fatalf("epic %s holds no node run at %s", buildRigEpic, path)
	return sqlcgen.EpicNodeRun{}
}

// retryNode reruns the parked run of node in the rig's epic through the Epics service, as `orrery epic retry` does.
func (r *buildRig) retryNode(t *testing.T, node string) {
	t.Helper()
	if _, err := r.epics().RetryEpic(t.Context(), connect.NewRequest(&v1.RetryEpicRequest{
		Id:   buildRigEpic,
		Node: node,
	})); err != nil {
		t.Fatalf("retry node %s of epic %s: %v", node, buildRigEpic, err)
	}
}

// waitNodeEnds waits for the node run at path to check its refs and leave running. The flow passes once per tick, and
// the pass after the node's check ends does both, so a caller first waits for the check to run, and each wait spans one
// pass.
func (r *buildRig) waitNodeEnds(t *testing.T, path string) {
	t.Helper()
	waitUntil(t, "node "+path+" to check its refs", func() bool { return r.nodeRun(t, path).RefsChecked != 0 })
	waitUntil(t, "node "+path+" to end or park", func() bool {
		return r.nodeRun(t, path).State != store.NodeRunning
	})
}

// TestWorkflowNodesSigkillCommandRunsOnce proves that orreryd killed while a pinned workflow's command node runs starts
// that node once. The command appends a line to a file outside the repository, then holds until a release file exists.
// The restarted orreryd takes the command up under its holder, so the node ends passed once the command is released,
// and the file holds exactly one line.
func TestWorkflowNodesSigkillCommandRunsOnce(t *testing.T) {
	t.Parallel()
	r := newNodesRig(t)
	lines := filepath.Join(r.f.root, "command.lines")
	release := filepath.Join(r.f.root, "command.release")
	script := fmt.Sprintf("echo run >> '%s'; while [ ! -e '%s' ]; do sleep 0.05; done", lines, release)
	seedNodesEpic(t, r, nodesCommandText([]string{r.f.shell, "-c", script}), nil)
	daemon := r.f.startOrreryd(t, true, "--holder-scope", r.scope)
	t.Cleanup(func() { clearHolders(t, r.stateDir) })
	// A command still held at the end of a failed test exits at once, so its holder is not left waiting.
	t.Cleanup(func() { stepReadoptTouch(t, release) })
	waitUntil(t, "the command to append its line", func() bool { return gateRunCount(t, lines) == 1 })

	killOrreryd(t, daemon)
	restarted := r.f.startOrreryd(t, true, "--holder-scope", r.scope)
	if run := r.nodeRun(t, nodesCommandNode); run.State != store.NodeRunning {
		t.Fatalf("command node after the restart is %q, want running", run.State)
	}
	if n := gateRunCount(t, lines); n != 1 {
		t.Fatalf("command lines after the restart = %d, want 1", n)
	}

	stepReadoptTouch(t, release)
	r.waitNodeEnds(t, nodesCommandNode)
	if run := r.nodeRun(t, nodesCommandNode); run.State != store.NodeEnded || run.Outcome != "passed" {
		t.Fatalf("command node %q with outcome %q, want ended passed; output %q", run.State, run.Outcome, run.Output)
	}
	if n := gateRunCount(t, lines); n != 1 {
		t.Fatalf("command lines after the node ended = %d, want 1", n)
	}
	wantExit(t, restarted.stop(t), 0)
}

// TestWorkflowNodesGateRetryAfterRestartRunsEachAttemptOnce proves that a gate node that fails parks its epic, and that
// after a restart `epic retry` of the node reruns it once under its next attempt. The gate appends a line to a file
// outside the repository on each run, and passes only once the allow file exists. The file holds one line per attempt.
func TestWorkflowNodesGateRetryAfterRestartRunsEachAttemptOnce(t *testing.T) {
	t.Parallel()
	r := newNodesRig(t)
	lines := filepath.Join(r.f.root, "gate.lines")
	allow := filepath.Join(r.f.root, "gate.allow")
	script := fmt.Sprintf("echo run >> '%s'; test -e '%s'", lines, allow)
	gate := store.EpicGate{Name: nodesGate, Argv: []string{r.f.shell, "-c", script}, Limit: 60}
	seedNodesEpic(t, r, nodesGateText(), []store.EpicGate{gate})
	daemon := r.f.startOrreryd(t, true, "--holder-scope", r.scope)
	t.Cleanup(func() { clearHolders(t, r.stateDir) })

	waitUntil(t, "the gate to run", func() bool { return gateRunCount(t, lines) == 1 })
	r.waitNodeEnds(t, nodesGateNode)
	if run := r.nodeRun(t, nodesGateNode); run.State != store.NodeParked || r.epic(t).GetParked() == "" {
		t.Fatalf("gate node %q with the epic parked %q, want both parked", run.State, r.epic(t).GetParked())
	}
	if run := r.nodeRun(t, nodesGateNode); run.Outcome != "failed" || run.Attempt != 0 {
		t.Fatalf("parked gate node = outcome %q attempt %d, want failed at attempt 0", run.Outcome, run.Attempt)
	}
	if reason := r.epic(t).GetParked(); !strings.Contains(reason, "workflow node "+nodesGateNode+" failed") {
		t.Fatalf("park reason %q does not name node %s", reason, nodesGateNode)
	}
	if n := gateRunCount(t, lines); n != 1 {
		t.Fatalf("gate lines before the restart = %d, want 1", n)
	}

	killOrreryd(t, daemon)
	restarted := r.f.startOrreryd(t, true, "--holder-scope", r.scope)
	if run := r.nodeRun(t, nodesGateNode); run.State != store.NodeParked {
		t.Fatalf("gate node after the restart is %q, want parked", run.State)
	}
	if n := gateRunCount(t, lines); n != 1 {
		t.Fatalf("gate lines after the restart = %d, want 1", n)
	}

	stepReadoptTouch(t, allow)
	r.retryNode(t, nodesGateNode)
	waitUntil(t, "the retried gate to run", func() bool { return gateRunCount(t, lines) == 2 })
	r.waitNodeEnds(t, nodesGateNode)
	if run := r.nodeRun(t, nodesGateNode); run.State != store.NodeEnded || run.Outcome != "passed" || run.Attempt != 1 {
		t.Fatalf("gate node %q with outcome %q at attempt %d, want ended passed at its next attempt, 1",
			run.State, run.Outcome, run.Attempt)
	}
	if parked := r.epic(t).GetParked(); parked != "" {
		t.Fatalf("epic parked after the retry: %q, want unparked", parked)
	}
	if n := gateRunCount(t, lines); n != 2 {
		t.Fatalf("gate lines after the retry = %d, want one per attempt, 2", n)
	}
	wantExit(t, restarted.stop(t), 0)
}
