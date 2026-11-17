package integration_test

import (
	"database/sql"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// The flow tests run an epic pinned to a repository workflow through the real orreryd, with the fake claude in place
// of claude. The workflow is a loop of at most two rounds over a plan that holds build. Its one task is planned before
// any plan run exists, so the interpreter starts the loop, its round-1 plan and the plan's build child for task 1
// before the implementer step runs. The rig is the build rig's, seeded with that pin in place of the built-in one.
// These tests live in internal/integration because they SIGKILL the orreryd binary and restart it over the same store,
// which crosses the interpreter, store, planner, build and step packages and no package test of one of them can do.

// flowWorkflowName names the repository workflow the epic pins, and flowWorkflowFile is the path it is read from.
const (
	flowWorkflowName = "rounds"
	flowWorkflowFile = ".orrery/workflows/" + flowWorkflowName + ".yaml"
)

// flowWorkflowText is the workflow file: a loop named again, of at most two rounds, that ends when plan completes and
// parks when the rounds run out. Its body is a plan that holds a build.
const flowWorkflowText = `name: rounds
nodes:
  - id: again
    loop:
      max: 2
      until: plan.complete
      spent: park
    body:
      - id: plan
        kind: plan
        body:
          - id: build
            kind: build
`

// newFlowRig builds the rig's repository, roles file and agent as the build rig does, and seeds the epic pinned to the
// flow workflow.
func newFlowRig(t *testing.T) *buildRig {
	t.Helper()
	f, scope, _ := scopedFixture(t)
	home := filepath.Join(f.root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatalf("make home: %v", err)
	}
	config := filepath.Join(f.root, "config")
	f.env = append(f.env,
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"XDG_CONFIG_HOME="+config, "CLAUDE_CONFIG_DIR="+filepath.Join(home, ".claude"),
	)
	stepReadoptWrite(t, filepath.Join(config, "orrery", "roles.toml"),
		fmt.Sprintf("%s = %q\n", buildRigRole, buildRigAgent))
	stepReadoptWrite(t, filepath.Join(home, ".claude", "agents", buildRigAgent+".md"),
		"---\nname: "+buildRigAgent+"\ndescription: Implements a task\n---\n")

	repo := filepath.Join(home, "repo")
	stepReadoptGit(t, home, "init", "-q", "-b", "main", repo)
	stepReadoptWrite(t, filepath.Join(repo, ".gitignore"), "fakeclaude.*\n")
	stepReadoptWrite(t, filepath.Join(repo, "README"), "base\n")
	host := testHost
	stepReadoptWrite(t, filepath.Join(repo, ".orrery", "project.toml"), fmt.Sprintf(
		"[pools.%s]\nhost = %q\ncap = 1\nmembers = [\"m1\"]\n"+
			"wake = [%q, \"-c\", \"exit 0\"]\nwake_limit = 60\n"+
			"status = [%q, \"-c\", \"exit 0\"]\nstatus_limit = 60\n"+
			"admit = [%q, \"-c\", \"exit 0\"]\nadmit_limit = 60\n",
		gates.DefaultPool, host, f.shell, f.shell, f.shell))
	stepReadoptGit(t, repo, "add", "-A")
	stepReadoptGit(t, repo, "commit", "-q", "-m", "base")
	r := &buildRig{
		f:        f,
		scope:    scope,
		stateDir: filepath.Dir(f.dbPath),
		repo:     repo,
		base:     stepReadoptGit(t, repo, "rev-parse", "HEAD"),
	}
	seedFlowEpic(t, r)
	return r
}

// seedFlowEpic stores the rig's epic pinned to the flow workflow, with the planner's turn completed and one task at
// implement at the base. It is the build rig's seed with the pin.
func seedFlowEpic(t *testing.T, r *buildRig) {
	t.Helper()
	ctx := t.Context()
	st, err := store.Open(ctx, r.f.dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	created := time.Unix(1700000000, 0)
	if err = st.CreateEpic(ctx, store.EpicRow{
		ID:          buildRigEpic,
		Repository:  r.repo,
		Base:        r.base,
		Title:       "Run a pinned loop end to end",
		Approved:    "approved text",
		Intent:      "run one task's implementer step under a loop",
		Care:        "severe",
		Approach:    "one implementer",
		ReleaseThen: "release it",
		Checks:      "just check",
		Phase:       "accepted",
		Created:     created,
		Changed:     created,
		Workflow:    flowPin(t),
		Gates: []store.EpicGate{{
			Name: buildRigGate, Argv: []string{r.f.shell, "-c", "exit 0"}, Limit: 60,
		}},
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: buildRigRequirement, Evidence: "build test", Kind: "test"},
		},
		Exclusions: []string{buildRigExclusion},
		Decisions:  []store.EpicDecision{{Text: buildRigDecision}},
	}); err != nil {
		t.Fatalf("create epic: %v", err)
	}
	if _, err = st.ClaimTurn(ctx, buildRigEpic, created); err != nil {
		t.Fatalf("claim turn: %v", err)
	}
	if err = st.CompleteTurn(ctx, buildRigEpic, 1, buildRigPlan(), created); err != nil {
		t.Fatalf("complete turn: %v", err)
	}
	epic, err := st.Epic(ctx, buildRigEpic)
	if err != nil {
		t.Fatalf("read epic: %v", err)
	}
	if len(epic.Tasks) != 1 {
		t.Fatalf("epic %s has %d tasks, want 1", buildRigEpic, len(epic.Tasks))
	}
	task := epic.Tasks[0]
	if err = st.SetTaskBuild(ctx, buildRigEpic, task.Seq, task.Build, store.TaskBuild{
		Stage: build.StageImplement, Base: r.base, Attempt: 1,
	}); err != nil {
		t.Fatalf("set task build: %v", err)
	}
	if err = st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
}

// flowPin returns the flow workflow as an epic pins it: the canonical text Load gives, its sha256, and its nodes in
// preorder.
func flowPin(t *testing.T) store.EpicWorkflow {
	t.Helper()
	pinned, err := workflow.Load(flowWorkflowName, flowReader)
	if err != nil {
		t.Fatalf("load workflow %s: %v", flowWorkflowName, err)
	}
	return store.EpicWorkflow{
		Name:   pinned.Name,
		Text:   pinned.Text,
		SHA256: pinned.SHA256,
		Nodes:  flowNodeRows(pinned.Workflow.Nodes, ""),
	}
}

// flowReader reads the repository workflow file: the flow text at flowWorkflowFile, and no other path.
func flowReader(path string) ([]byte, bool, error) {
	if path != flowWorkflowFile {
		return nil, false, nil
	}
	return []byte(flowWorkflowText), true, nil
}

// flowNodeRows returns the rows of nodes in preorder, each under parent. A loop's row has the kind loop.
func flowNodeRows(nodes []workflow.Node, parent string) []store.EpicWorkflowNode {
	rows := make([]store.EpicWorkflowNode, 0, len(nodes))
	for _, node := range nodes {
		kind := node.Kind
		if node.Loop != nil {
			kind = "loop"
		}
		rows = append(rows, store.EpicWorkflowNode{ID: node.ID, Parent: parent, Kind: kind})
		rows = append(rows, flowNodeRows(node.Body, node.ID)...)
	}
	return rows
}

// flowRun is a node run the flow's first round starts on the rig's epic: its path, the node it runs, its kind, its
// round, its loop round, the task of a build child, and the cause its E3 id hashes.
type flowRun struct {
	path, node, kind, cause string
	round, loopRound, task  int64
}

// flowRuns returns the node runs the flow's first round holds, ordered by path: the loop, its round-1 plan, and the
// plan's build child for task 1.
func flowRuns() []flowRun {
	return []flowRun{
		{path: "again", node: "again", kind: "loop", cause: "start", loopRound: 1},
		{path: "again@1/plan", node: "plan", kind: "plan", cause: "start", round: 1},
		{path: "again@1/plan/build/T1", node: "build", kind: "build", cause: "turn 1 task 1", round: 1, task: 1},
	}
}

// nodeRuns returns the node runs the store holds for the rig's epic, ordered by path. The store file is read through a
// read-only handle that is closed at once, so orreryd keeps the file it writes. The handle's busy timeout is the
// largest SQLite takes, so a read waits out any lock orreryd holds; the test binary's timeout bounds it.
func (r *buildRig) nodeRuns(t *testing.T) []sqlcgen.EpicNodeRun {
	t.Helper()
	query := url.Values{}
	query.Set("mode", "ro")
	query.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", math.MaxInt32))
	dsn := (&url.URL{Scheme: "file", Path: r.f.dbPath, RawQuery: query.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open store read-only: %v", err)
	}
	runs, readErr := sqlcgen.New(db).ListEpicNodeRuns(t.Context(), buildRigEpic)
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("close store read-only: %v", closeErr)
	}
	if readErr != nil {
		t.Fatalf("list node runs of epic %s: %v", buildRigEpic, readErr)
	}
	return runs
}

// requireFlowRuns fails unless got is exactly the flow's first-round node runs, each running under the id E3 derives
// for it.
func requireFlowRuns(t *testing.T, got []sqlcgen.EpicNodeRun) {
	t.Helper()
	want := flowRuns()
	if len(got) != len(want) {
		t.Fatalf("node runs of epic %s = %d, want %d: %+v", buildRigEpic, len(got), len(want), got)
	}
	for i, run := range want {
		id := store.NodeRunID(buildRigEpic, run.path, run.round, run.cause)
		row := got[i]
		if row.Path != run.path || row.ID != id || row.Node != run.node || row.Kind != run.kind ||
			row.Task != run.task || row.Round != run.round || row.LoopRound != run.loopRound ||
			row.State != store.NodeRunning {
			t.Fatalf("node run %s = %+v, want node %s kind %s id %s task %d round %d loop round %d running",
				run.path, row, run.node, run.kind, id, run.task, run.round, run.loopRound)
		}
	}
}

// TestFlowSigkillRestartStartsEachNodeOnce proves that orreryd killed while an epic's pinned loop runs its round-1
// plan and that plan's build child for task 1 starts each node run once. The restarted orreryd finds the same node
// runs under the same ids, and the implementer step under its derived id, still running. Once the fake claude is
// released, the step ends done, the task reaches green, and the epic holds exactly one implementer step.
func TestFlowSigkillRestartStartsEachNodeOnce(t *testing.T) {
	t.Parallel()
	r := newFlowRig(t)
	daemon, ack := r.startPassDaemon(t, "first")
	id := r.stepID()
	dir := r.stepDir()
	t.Cleanup(func() { clearHolder(t, filepath.Join(r.stateDir, "holders", id)) })

	r.awaitStep(t, daemon, ack, id)
	stdin := filepath.Join(dir, "fakeclaude.stdin")
	waitUntil(t, func() bool { return fileContains(t, stdin, buildRigMarker) })
	pid := fakeClaudePID(t, dir)
	requireFlowRuns(t, r.nodeRuns(t))

	killOrreryd(t, daemon)
	restarted, restartedAck := r.startPassDaemon(t, "restarted")
	requireFlowRuns(t, r.nodeRuns(t))
	if end := r.stepRow(t, id).GetEnd(); end != "" {
		t.Fatalf("step %s ended %q after the restart, want it still running its turn", id, end)
	}
	requireProcessRunning(t, pid)
	if got := r.task(t).GetStep(); got != id {
		t.Fatalf("step of task 1 after the restart = %q, want %q", got, id)
	}
	// A restart that started the step again would fail the task: the engine refuses a start under an id already in use.
	if task := r.task(t); task.GetStatus() != "open" || task.GetFailure() != "" {
		t.Fatalf("task 1 after the restart is %q with failure %q, want open with no failure",
			task.GetStatus(), task.GetFailure())
	}
	// The epic is paused before the release, so the task stops at green and no evidence run starts after it.
	r.pause(t)

	stepReadoptTouch(t, filepath.Join(dir, "fakeclaude.release"))
	commit := requireBuildStepDone(t, dir, r.awaitEnd(t, restarted, id))
	r.awaitGreen(t, restarted, restartedAck)
	requireFlowRuns(t, r.nodeRuns(t))
	if got := r.task(t).GetRevision(); got != commit {
		t.Fatalf("revision of task 1 at green = %q, want the fake's commit %s", got, commit)
	}
	if steps := r.taskSteps(t); len(steps) != 1 || steps[0].GetId() != id {
		t.Fatalf("labeled steps of task 1 = %d, want exactly the implementer step %s", len(steps), id)
	}
	if n := promptsWritten(t, dir, buildRigMarker); n != 1 {
		t.Fatalf("fake claude read the brief %d times, want 1", n)
	}
	waitGone(t, pid)
	wantExit(t, restarted.stop(t), 0)
}
