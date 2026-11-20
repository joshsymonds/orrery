package interpret

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/agents"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// The executor's fakes satisfy the services the runner takes.
var (
	_ Checks   = (*fakeChecks)(nil)
	_ Evidence = (*fakeEvidence)(nil)
	_ Steps    = (*fakeSteps)(nil)
)

// TestMain points git at no global or system config for the whole package, so the owner's git config never applies to
// the repositories the tests make or to the git the executor runs. It is set here once, since t.Setenv would keep the
// tests from running in parallel.
func TestMain(m *testing.M) {
	os.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	os.Exit(m.Run())
}

// nodeBranchName is the branch the test epic's work is committed to: the branch build names for runEpic.
const nodeBranchName = "epic/epic-runner"

// nodeFlow is the pinned workflow of the pass test: a gate at base, a command at head, an evidence item at base, an owned
// node and an agent at head, each at the top level after the one before it.
const nodeFlow = `name: nodes
nodes:
  - id: lint
    kind: gate
    gate: lint
    at: base
  - id: tests
    kind: command
    argv:
      - ./bin/tests
      - -v
    limit: 30
    needs:
      tests: 2
      build: 1
    at: head
  - id: evidence
    kind: evidence
    at: base
  - id: owned
    kind: owned
    paths:
      - internal/a/
      - b.go
  - id: review
    kind: agent
    agent: reviewer
    prompt: Review the change.
    schema:
      type: object
    at: head
`

// nodeLintFlow is a flow of one gate at base.
const nodeLintFlow = `name: nodes
nodes:
  - id: lint
    kind: gate
    gate: lint
    at: base
`

// nodeCommandFlow is a flow of one command at head.
const nodeCommandFlow = `name: nodes
nodes:
  - id: tests
    kind: command
    argv:
      - ./bin/tests
    limit: 30
    at: head
`

// nodeEvidenceFlow is a flow of one evidence item at base.
const nodeEvidenceFlow = `name: nodes
nodes:
  - id: evidence
    kind: evidence
    at: base
`

// nodeEvidenceHeadFlow is a flow of one evidence item at head.
const nodeEvidenceHeadFlow = `name: nodes
nodes:
  - id: evidence
    kind: evidence
    at: head
`

// nodeAgentFlow is a flow of one agent at head.
const nodeAgentFlow = `name: nodes
nodes:
  - id: review
    kind: agent
    agent: reviewer
    prompt: Review the change.
    schema:
      type: object
    at: head
`

// nodeOwnedFlow is a flow of one owned node.
const nodeOwnedFlow = `name: nodes
nodes:
  - id: owned
    kind: owned
    paths:
      - internal/a/
      - b.go
`

// nodePlanFlow is a flow of one plan whose body holds a build node and a gate at base.
const nodePlanFlow = `name: nodes
nodes:
  - id: plan
    kind: plan
    body:
      - id: build
        kind: build
      - id: lint
        kind: gate
        gate: lint
        at: base
`

// fakeChecks stands in for the check service. It stores each check it is given once, under its id, as the real service
// does, and returns the stored check. It records every call.
type fakeChecks struct {
	store *store.Store
	calls []store.NodeCheck
}

// Run records check, and stores it unless its id is stored already.
func (f *fakeChecks) Run(ctx context.Context, check store.NodeCheck) (store.NodeCheck, error) {
	f.calls = append(f.calls, check)
	stored, err := f.store.NodeCheck(ctx, check.ID)
	if err == nil {
		return stored, nil
	}
	if !errors.Is(err, store.ErrNoNodeCheck) {
		return store.NodeCheck{}, fmt.Errorf("read node check %s: %w", check.ID, err)
	}
	if err = f.store.AddNodeCheck(ctx, check, runClock()); err != nil {
		return store.NodeCheck{}, fmt.Errorf("add node check %s: %w", check.ID, err)
	}
	return f.store.NodeCheck(ctx, check.ID)
}

// evidenceCall is one RunItem call: the run's id, epic, Requirement, revision and phase.
type evidenceCall struct {
	id, epic, item, revision, phase string
	// spec is the item an amendment's run was asked for, and the zero item for a run of the item a Requirement holds.
	spec evidence.Item
}

// fakeEvidence stands in for the evidence service. It stores each run it is asked for once, under its id, in the
// capturing state, and records every call.
type fakeEvidence struct {
	store *store.Store
	calls []evidenceCall
}

// RunItem records the call, and stores the run unless its id is stored already.
func (f *fakeEvidence) RunItem(
	ctx context.Context, id, epic, item, revision, phase string,
) (store.EvidenceRun, error) {
	f.calls = append(f.calls, evidenceCall{id: id, epic: epic, item: item, revision: revision, phase: phase})
	// The run keeps the Requirement's item as its spec, and the fixture's items capture, so the run's source is
	// captured, as the real service gives them.
	return f.add(ctx, id, epic, item, revision, phase, func(row store.EpicRow) evidence.Item {
		var spec evidence.Item
		for _, requirement := range row.Requirements {
			if requirement.ID == item {
				spec = requirement.Item
			}
		}
		return spec
	})
}

// RunAmendment records the call with its spec, and stores the run of spec unless its id is stored already.
func (f *fakeEvidence) RunAmendment(
	ctx context.Context, id, epic, requirement string, spec evidence.Item, revision, phase string,
) (store.EvidenceRun, error) {
	f.calls = append(f.calls, evidenceCall{
		id: id, epic: epic, item: requirement, revision: revision, phase: phase, spec: spec,
	})
	return f.add(ctx, id, epic, requirement, revision, phase, func(store.EpicRow) evidence.Item { return spec })
}

// add stores the run of the spec specOf gives under id, unless a run with id is stored already, and returns the stored run.
func (f *fakeEvidence) add(
	ctx context.Context, id, epic, item, revision, phase string, specOf func(store.EpicRow) evidence.Item,
) (store.EvidenceRun, error) {
	stored, err := f.store.EvidenceRun(ctx, id)
	if err == nil {
		return stored, nil
	}
	if !errors.Is(err, store.ErrNoEvidenceRun) {
		return store.EvidenceRun{}, fmt.Errorf("read evidence run %s: %w", id, err)
	}
	row, err := f.store.Epic(ctx, epic)
	if err != nil {
		return store.EvidenceRun{}, fmt.Errorf("read epic %s: %w", epic, err)
	}
	run := store.EvidenceRun{
		ID: id, Epic: epic, Item: item, Revision: revision, Phase: phase, Source: evidence.SourceCaptured,
		Spec: specOf(row), Created: runClock(),
	}
	if err = f.store.AddEvidenceRun(ctx, run); err != nil {
		return store.EvidenceRun{}, fmt.Errorf("add evidence run %s: %w", id, err)
	}
	return f.store.EvidenceRun(ctx, id)
}

// fakeSteps stands in for the step service. It stores each step it starts as a step row, under its id, and records every
// start. A fake given refuse refuses every start with it and stores nothing, as the real service does.
type fakeSteps struct {
	store  *store.Store
	refuse error
	calls  []enginehost.StepStart
}

// Start records start, and stores its step row unless the fake refuses.
func (f *fakeSteps) Start(ctx context.Context, start enginehost.StepStart) (string, error) {
	f.calls = append(f.calls, start)
	if f.refuse != nil {
		return "", f.refuse
	}
	err := f.store.ApplyEngine(ctx, []store.EngineOp{store.StepAdd{
		ID: start.ID, Agent: start.Agent, Prompt: start.Prompt, Schema: start.Schema, Workspace: start.Workspace,
		Revision: start.Revision, DoneValue: "done", ReadOnly: start.ReadOnly, Epic: start.Epic, Task: start.Task,
	}}, runClock())
	if err != nil {
		return "", fmt.Errorf("store step %s: %w", start.ID, err)
	}
	return start.ID, nil
}

// nodeFakes are the services a runner over an executor test is given.
type nodeFakes struct {
	checks   *fakeChecks
	evidence *fakeEvidence
	steps    *fakeSteps
}

// newNodeFakes returns the fakes over s.
func newNodeFakes(s *store.Store) nodeFakes {
	return nodeFakes{checks: &fakeChecks{store: s}, evidence: &fakeEvidence{store: s}, steps: &fakeSteps{store: s}}
}

// nodeConfig returns the config of a runner over st with fakes, the clock at runClock and no environment for gates to read.
func nodeConfig(st Store, fakes nodeFakes) Config {
	return Config{
		Store: st, Planner: &fakePlanner{}, Builder: &fakeBuilder{}, Checks: fakes.checks, Evidence: fakes.evidence,
		Steps: fakes.steps, Getenv: func(string) string { return "" }, Tick: time.Hour, Now: runClock,
	}
}

// runNodes returns a runner over st with fakes, configured by nodeConfig.
func runNodes(st Store, fakes nodeFakes) *Runner {
	return New(nodeConfig(st, fakes))
}

// endCounter is the store a test wraps to record the node ends the runner writes: the plain ones and those that wake
// the planner.
type endCounter struct {
	Store
	plain  [][]store.NodeEnd
	waking [][]store.NodeEnd
}

// EndNodes records ends, then writes them.
func (c *endCounter) EndNodes(ctx context.Context, epic string, ends []store.NodeEnd, at time.Time) error {
	c.plain = append(c.plain, ends)
	if err := c.Store.EndNodes(ctx, epic, ends, at); err != nil {
		return fmt.Errorf("end nodes: %w", err)
	}
	return nil
}

// EndNodesWaking records ends, then writes them with trigger.
func (c *endCounter) EndNodesWaking(
	ctx context.Context, epic string, ends []store.NodeEnd, trigger store.NodeTrigger, at time.Time,
) error {
	c.waking = append(c.waking, ends)
	if err := c.Store.EndNodesWaking(ctx, epic, ends, trigger, at); err != nil {
		return fmt.Errorf("end nodes waking: %w", err)
	}
	return nil
}

// nodeGit runs git in dir with the test identity and returns its trimmed output. It fails the test on error.
func nodeGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "user.name=Test",
		"-c", "user.email=test@example.invalid",
		"-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// nodeWrite writes text to file under repo, making its directories.
func nodeWrite(t *testing.T, repo, file, text string) {
	t.Helper()
	path := filepath.Join(repo, file)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("make directories of %s: %v", file, err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
}

// nodeRepo makes a scratch repository on main with one commit, and returns the repository and that commit.
func nodeRepo(t *testing.T) (string, string) {
	t.Helper()
	repo := t.TempDir()
	nodeGit(t, repo, "init", "-q", "-b", "main")
	nodeWrite(t, repo, "README", "base\n")
	nodeGit(t, repo, "add", "README")
	nodeGit(t, repo, "commit", "-q", "-m", "base")
	return repo, nodeGit(t, repo, "rev-parse", "HEAD")
}

// nodeEpicBranch points the test epic's branch at one commit on main that writes files, and leaves main checked out. It
// returns the branch's head, which is an empty commit on main when no file is written.
func nodeEpicBranch(t *testing.T, repo string, files ...string) string {
	t.Helper()
	nodeGit(t, repo, "checkout", "-q", "-b", nodeBranchName)
	for _, file := range files {
		nodeWrite(t, repo, file, "change\n")
		nodeGit(t, repo, "add", file)
	}
	nodeGit(t, repo, "commit", "-q", "--allow-empty", "-m", "epic work")
	head := nodeGit(t, repo, "rev-parse", "HEAD")
	nodeGit(t, repo, "checkout", "-q", "main")
	return head
}

// nodeGates are the epic's gates the flows name: lint, with a pool of its own.
func nodeGates() []store.EpicGate {
	return []store.EpicGate{{
		Name: "lint", Argv: []string{"./bin/lint"}, Limit: 60, Needs: []store.EpicGateNeed{{Pool: "lint", Count: 2}},
	}}
}

// nodeRequirements are the Requirements of the test epic: R1, whose evidence item captures scripts/check.
func nodeRequirements() []store.EpicRequirement {
	return []store.EpicRequirement{{
		ID: "R1", Text: "the runner runs", Item: evidence.Item{
			Capture: []string{"scripts/check"}, Limit: 60, Artifacts: []string{}, Supplied: []string{},
			Conclusion: "the runner runs", Judge: "a judge",
		},
	}}
}

// pinnedNodes returns nodes and the nodes under them in preorder, each with the id of its parent.
func pinnedNodes(nodes []workflow.Node, parent string) []store.EpicWorkflowNode {
	out := make([]store.EpicWorkflowNode, 0, len(nodes))
	for _, node := range nodes {
		kind := node.Kind
		if node.Loop != nil {
			kind = kindLoop
		}
		out = append(out, store.EpicWorkflowNode{ID: node.ID, Parent: parent, Kind: kind})
		out = append(out, pinnedNodes(node.Body, node.ID)...)
	}
	return out
}

// createNodeEpic stores the test epic on repo at base, accepted and pinned to the workflow text flow.
func createNodeEpic(t *testing.T, s *store.Store, repo, base, flow string) {
	t.Helper()
	pinned, err := workflow.Load("nodes", func(string) ([]byte, bool, error) { return []byte(flow), true, nil })
	if err != nil {
		t.Fatalf("Load nodes: %v", err)
	}
	if err = s.CreateEpic(context.Background(), store.EpicRow{
		ID: runEpic, Repository: repo, Base: base, Title: "Run the nodes", Phase: runPhase, Created: runClock(),
		Changed: runClock(), Gates: nodeGates(), Requirements: nodeRequirements(),
		Workflow: store.EpicWorkflow{
			Name: pinned.Name, Text: pinned.Text, SHA256: pinned.SHA256, Nodes: pinnedNodes(pinned.Workflow.Nodes, ""),
		},
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
}

// workID returns the id of part of the work of the run at path, as the executor derives it.
func workID(t *testing.T, s *store.Store, path, part string) string {
	t.Helper()
	run := runAt(t, s, path)
	return store.NodeWorkID(run.ID, run.Attempt, part)
}

// nodeEndCheck ends the running check with id as its holder would: with exit and stderr.
func nodeEndCheck(t *testing.T, s *store.Store, id string, exit int, stderr string) {
	t.Helper()
	end := store.NodeCheckEnd{Exit: exit, Stderr: stderr}
	if err := s.EndNodeCheck(context.Background(), id, end, runClock()); err != nil {
		t.Fatalf("EndNodeCheck %s: %v", id, err)
	}
}

// nodeEndEvidence ends the evidence run with id passed: its capture is taken and its judge holds it.
func nodeEndEvidence(t *testing.T, s *store.Store, id string) {
	t.Helper()
	ctx := context.Background()
	if err := s.RecordEvidenceCapture(ctx, id, store.EvidenceCapture{At: runClock()}); err != nil {
		t.Fatalf("RecordEvidenceCapture %s: %v", id, err)
	}
	verdict := store.EvidenceVerdict{Verdict: evidence.VerdictHolds, Passed: true, At: runClock()}
	if err := s.RecordEvidenceVerdict(ctx, id, verdict); err != nil {
		t.Fatalf("RecordEvidenceVerdict %s: %v", id, err)
	}
}

// nodeEndStep ends the step with id done, as a step whose agent returns done does.
func nodeEndStep(t *testing.T, s *store.Store, id string) {
	t.Helper()
	end := store.StepEnd{ID: id, End: "done", Status: "done"}
	if err := s.ApplyEngine(context.Background(), []store.EngineOp{end}, runClock()); err != nil {
		t.Fatalf("end step %s: %v", id, err)
	}
}

// openNodeStore opens a migrated store file for one executor test and returns the store and the file's path, which the
// tells are read from.
func openNodeStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(context.Background(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s, path
}

// triggerCausesOf returns the cause of each trigger of epic, in the order the store lists them.
func triggerCausesOf(t *testing.T, s *store.Store, epic string) []string {
	t.Helper()
	history, err := s.EpicHistory(context.Background(), epic)
	if err != nil {
		t.Fatalf("EpicHistory of %s: %v", epic, err)
	}
	causes := make([]string, 0, len(history.Triggers))
	for _, trigger := range history.Triggers {
		causes = append(causes, trigger.Cause)
	}
	return causes
}

// nodeTells returns the tells of the test epic in seq order, read from the store file at path. Attention lists only the
// epics that have a turn, so the file is read.
func nodeTells(t *testing.T, path string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open store file: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close store file: %v", err)
		}
	}()
	const query = "SELECT text FROM epic_tells WHERE epic = ? ORDER BY seq"
	rows, err := db.QueryContext(context.Background(), query, runEpic)
	if err != nil {
		t.Fatalf("query tells: %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close rows: %v", err)
		}
	}()
	var texts []string
	for rows.Next() {
		var text string
		if err = rows.Scan(&text); err != nil {
			t.Fatalf("scan tell: %v", err)
		}
		texts = append(texts, text)
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("read tells: %v", err)
	}
	return texts
}

// TestPassCarriesOutNodeWorkOnce: a pass over an epic whose pinned workflow holds a gate, a command, an evidence item, an
// owned node and an agent at the top level starts each node's work once, under the id derived from its run, at the
// revision the node names, with the needs, phase and step fields it takes. Each pass after the work ends carries the
// flow on, and a restarted pass while the work runs starts nothing again.
func TestPassCarriesOutNodeWorkOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, base := nodeRepo(t)
	head := nodeEpicBranch(t, repo, "internal/a/x.go", "b.go")
	s := openRunStore(t)
	createNodeEpic(t, s, repo, base, nodeFlow)
	fakes := newNodeFakes(s)
	runner := runNodes(s, fakes)

	// Each node's work is started, then a restarted runner passes while that work still runs, before it ends. The calls
	// below hold one start per node, so a restart starts nothing again.
	runner.Pass(ctx)
	lint := workID(t, s, "lint", "check")
	runNodes(s, fakes).Pass(ctx)
	nodeEndCheck(t, s, lint, 0, "")
	runner.Pass(ctx)
	tests := workID(t, s, "tests", "check")
	runNodes(s, fakes).Pass(ctx)
	nodeEndCheck(t, s, tests, 0, "")
	runner.Pass(ctx)
	evidenceRun := workID(t, s, "evidence", "R1")
	runNodes(s, fakes).Pass(ctx)
	nodeEndEvidence(t, s, evidenceRun)
	runner.Pass(ctx)
	step := workID(t, s, "review", "step")
	runNodes(s, fakes).Pass(ctx)
	nodeEndStep(t, s, step)
	runner.Pass(ctx)

	wantChecks := []store.NodeCheck{
		{
			ID: lint, Epic: runEpic, Path: "lint", Revision: base, Argv: []string{"./bin/lint"}, Limit: 60,
			Needs: []store.EpicGateNeed{{Pool: "lint", Count: 2}},
		},
		{
			ID: tests, Epic: runEpic, Path: "tests", Revision: head, Argv: []string{"./bin/tests", "-v"}, Limit: 30,
			Needs: []store.EpicGateNeed{{Pool: "build", Count: 1}, {Pool: "tests", Count: 2}},
		},
	}
	if !reflect.DeepEqual(fakes.checks.calls, wantChecks) {
		t.Errorf("check calls = %+v, want %+v", fakes.checks.calls, wantChecks)
	}
	wantEvidence := []evidenceCall{{
		id: evidenceRun, epic: runEpic, item: "R1", revision: base, phase: evidence.PhaseRed,
	}}
	if !reflect.DeepEqual(fakes.evidence.calls, wantEvidence) {
		t.Errorf("evidence calls = %+v, want %+v", fakes.evidence.calls, wantEvidence)
	}
	wantSteps := []enginehost.StepStart{{
		ID: step, Agent: "reviewer", Prompt: "Review the change.", Schema: `{"type":"object"}`, Workspace: repo,
		Revision: head, ReadOnly: true, Epic: runEpic,
	}}
	if !reflect.DeepEqual(fakes.steps.calls, wantSteps) {
		t.Errorf("step calls = %+v, want %+v", fakes.steps.calls, wantSteps)
	}
	wantRevisions := map[string]string{"lint": base, "tests": head, "evidence": base, "review": head}
	for path, want := range wantRevisions {
		if got := runAt(t, s, path).Revision; got != want {
			t.Errorf("revision of %s = %q, want %q", path, got, want)
		}
	}
	if got, want := runAt(t, s, "owned").Result, `["b.go","internal/a/x.go"]`; got != want {
		t.Errorf("owned result = %q, want %q", got, want)
	}
	for _, path := range []string{"lint", "tests", "evidence", "owned", "review"} {
		if run := runAt(t, s, path); run.State != store.NodeEnded || run.Outcome != "passed" {
			t.Errorf("node %s = %+v, want ended passed", path, run)
		}
	}
}

// TestCommandMovingMainParksNodeAndEpicTold: a command whose work moves refs/heads/main parks its node and the epic with
// the move as the reason, and tells the owner of the move and of the HEAD it moves with.
func TestCommandMovingMainParksNodeAndEpicTold(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, base := nodeRepo(t)
	s, dbPath := openNodeStore(t)
	createNodeEpic(t, s, repo, base, nodeCommandFlow)
	nodeEpicBranch(t, repo, "b.go")
	runner := runNodes(s, newNodeFakes(s))
	runner.Pass(ctx)
	check := workID(t, s, "tests", "check")

	nodeGit(t, repo, "commit", "-q", "--allow-empty", "-m", "work moves main")
	moved := nodeGit(t, repo, "rev-parse", "HEAD")
	nodeEndCheck(t, s, check, 0, "")
	runner.Pass(ctx)

	park := fmt.Sprintf("workflow node tests moved refs/heads/main (%s to %s)", base, moved)
	head := fmt.Sprintf("workflow node tests moved HEAD (refs/heads/main %s to refs/heads/main %s)", base, moved)
	if run := runAt(t, s, "tests"); run.State != store.NodeParked {
		t.Errorf("node tests = %+v, want parked", run)
	}
	if got := epicOf(t, s).Parked; got != park {
		t.Errorf("epic Parked = %q, want %q", got, park)
	}
	if got := nodeTells(t, dbPath); !reflect.DeepEqual(got, []string{park, head}) {
		t.Errorf("tells = %q, want %q", got, []string{park, head})
	}
}

// TestOwnedRecordsAnEmptyArrayWhenNothingChanged: an owned node whose epic branch changes nothing over its base records
// the empty JSON array, so its kind ends it passed rather than starting it again.
func TestOwnedRecordsAnEmptyArrayWhenNothingChanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, base := nodeRepo(t)
	nodeEpicBranch(t, repo)
	s := openRunStore(t)
	createNodeEpic(t, s, repo, base, nodeOwnedFlow)
	runNodes(s, newNodeFakes(s)).Pass(ctx)

	run := runAt(t, s, "owned")
	if run.Result != `[]` {
		t.Errorf("owned result = %q, want []", run.Result)
	}
	if run.State != store.NodeEnded || run.Outcome != "passed" {
		t.Errorf("owned = %+v, want ended passed", run)
	}
}

// TestFailedTopLevelCheckParksAndTells: a gate at the top level whose check fails parks its node and the epic with the
// failure as the reason, and tells it.
func TestFailedTopLevelCheckParksAndTells(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, base := nodeRepo(t)
	s, dbPath := openNodeStore(t)
	createNodeEpic(t, s, repo, base, nodeLintFlow)
	runner := runNodes(s, newNodeFakes(s))
	runner.Pass(ctx)
	check := workID(t, s, "lint", "check")

	nodeEndCheck(t, s, check, 1, "lint failed")
	runner.Pass(ctx)

	reason := "workflow node lint failed: lint failed"
	if run := runAt(
		t,
		s,
		"lint",
	); run.State != store.NodeParked || run.Outcome != "failed" ||
		run.Output != "lint failed" {
		t.Errorf("node lint = %+v, want parked failed with the check's output", run)
	}
	if got := epicOf(t, s).Parked; got != reason {
		t.Errorf("epic Parked = %q, want %q", got, reason)
	}
	if got := nodeTells(t, dbPath); len(got) != 0 {
		t.Errorf("tells = %q, want none: a failed node is a planner park", got)
	}
	if got := triggerCausesOf(t, s, runEpic); !slices.Contains(got, reason) {
		t.Errorf("trigger causes = %q, want one with cause %q", got, reason)
	}
}

// TestFailedPlanBodyCheckEndsWithItsTrigger: a check in a plan body that fails ends its node and adds the trigger that
// wakes the planner in one write, and parks nothing.
func TestFailedPlanBodyCheckEndsWithItsTrigger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, base := nodeRepo(t)
	s := openRunStore(t)
	createNodeEpic(t, s, repo, base, nodePlanFlow)
	counter := &endCounter{Store: s}
	runner := runNodes(counter, newNodeFakes(s))

	runner.Pass(ctx)
	completeTurn(t, s, runPlan("work"))
	runner.Pass(ctx)
	for seq := int64(1); seq <= 2; seq++ {
		if err := s.AcceptTask(ctx, runEpic, seq, store.TaskBuild{}, "reviewed"); err != nil {
			t.Fatalf("AcceptTask %d: %v", seq, err)
		}
	}
	runner.Pass(ctx)
	check := workID(t, s, "plan/S2/lint", "check")
	nodeEndCheck(t, s, check, 1, "lint failed")
	runner.Pass(ctx)

	lint := runAt(t, s, "plan/S2/lint")
	if lint.State != store.NodeEnded || lint.Outcome != "failed" || lint.Output != "lint failed" {
		t.Errorf("node plan/S2/lint = %+v, want ended failed with the check's output", lint)
	}
	end := []store.NodeEnd{{Path: "plan/S2/lint", Outcome: "failed", Output: "lint failed"}}
	if !reflect.DeepEqual(counter.waking, [][]store.NodeEnd{end}) {
		t.Errorf("ends that wake the planner = %+v, want %+v", counter.waking, [][]store.NodeEnd{end})
	}
	for _, plain := range counter.plain {
		for _, written := range plain {
			if written.Path == "plan/S2/lint" {
				t.Errorf("node plan/S2/lint ended in a write without its trigger: %+v", plain)
			}
		}
	}
	triggerID := "workflow:" + store.NodeRunID(runEpic, "plan/S2/lint", 0, "failed attempt 0")
	wantCause := "workflow node plan/S2/lint failed: lint failed"
	triggers, err := s.TurnTriggers(ctx, runEpic, 0)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	found := false
	for _, trigger := range triggers {
		if trigger.ID == triggerID {
			found = trigger.Cause == wantCause
		}
	}
	if !found {
		t.Errorf("pending triggers = %+v, want %s with cause %q", triggers, triggerID, wantCause)
	}
	if got := epicOf(t, s).Parked; got != "" {
		t.Errorf("epic Parked = %q, want not parked", got)
	}
}

// TestNodeWorkWithoutItsServiceStartsNothing: a runner that was not given the service a node's work runs on carries
// nothing for that node. The pass returns with the node running and its work unstarted, rather than failing on a service
// the runner does not have.
func TestNodeWorkWithoutItsServiceStartsNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, flow, path string
		minus            func(cfg *Config)
	}{
		{name: "gate", flow: nodeLintFlow, path: "lint", minus: func(cfg *Config) { cfg.Checks = nil }},
		{name: "evidence", flow: nodeEvidenceFlow, path: "evidence", minus: func(cfg *Config) { cfg.Evidence = nil }},
		{name: "agent", flow: nodeAgentFlow, path: "review", minus: func(cfg *Config) { cfg.Steps = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			repo, base := nodeRepo(t)
			nodeEpicBranch(t, repo, "b.go")
			s := openRunStore(t)
			createNodeEpic(t, s, repo, base, tc.flow)
			cfg := nodeConfig(s, newNodeFakes(s))
			tc.minus(&cfg)
			New(cfg).Pass(ctx)

			if run := runAt(t, s, tc.path); run.State != store.NodeRunning {
				t.Errorf("node %s = %+v, want running", tc.path, run)
			}
		})
	}
}

// TestEvidenceAtHeadRunsGreenAtBranchHead: an evidence node at head starts each Requirement's run in phase green, at
// the head of the epic's branch.
func TestEvidenceAtHeadRunsGreenAtBranchHead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, base := nodeRepo(t)
	head := nodeEpicBranch(t, repo, "b.go")
	s := openRunStore(t)
	createNodeEpic(t, s, repo, base, nodeEvidenceHeadFlow)
	fakes := newNodeFakes(s)
	runNodes(s, fakes).Pass(ctx)

	wantEvidence := []evidenceCall{{
		id: workID(t, s, "evidence", "R1"), epic: runEpic, item: "R1", revision: head, phase: evidence.PhaseGreen,
	}}
	if !reflect.DeepEqual(fakes.evidence.calls, wantEvidence) {
		t.Errorf("evidence calls = %+v, want %+v", fakes.evidence.calls, wantEvidence)
	}
	if got := runAt(t, s, "evidence").Revision; got != head {
		t.Errorf("revision of evidence = %q, want %q", got, head)
	}
}

// TestRefusedAgentStartParksAndTells: an agent node at the top level whose start is refused for good records the
// refusal as its result in the pass that refused it, and the next pass ends it failed with that result. That parks the
// node and the epic with the refusal as the reason, and tells it. The refused start is not retried.
func TestRefusedAgentStartParksAndTells(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
	}{
		{name: "bad schema", err: enginehost.ErrBadSchema},
		{name: "bad agent", err: enginehost.ErrBadAgent},
		{name: "unresolved agent", err: agents.ErrUnresolved},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			repo, base := nodeRepo(t)
			nodeEpicBranch(t, repo, "b.go")
			s, dbPath := openNodeStore(t)
			createNodeEpic(t, s, repo, base, nodeAgentFlow)
			fakes := newNodeFakes(s)
			fakes.steps.refuse = tc.err
			runner := runNodes(s, fakes)

			runner.Pass(ctx)
			refusal := "not run: " + tc.err.Error()
			if run := runAt(t, s, "review"); run.State != store.NodeRunning || run.Result != refusal {
				t.Errorf("node review = %+v, want running with result %q", run, refusal)
			}

			runner.Pass(ctx)
			reason := "workflow node review failed: " + refusal
			if run := runAt(t, s, "review"); run.State != store.NodeParked || run.Outcome != "failed" ||
				run.Output != refusal {
				t.Errorf("node review = %+v, want parked failed with the refusal", run)
			}
			if got := epicOf(t, s).Parked; got != reason {
				t.Errorf("epic Parked = %q, want %q", got, reason)
			}
			if got := nodeTells(t, dbPath); len(got) != 0 {
				t.Errorf("tells = %q, want none: a refused node is a planner park", got)
			}
			if got := triggerCausesOf(t, s, runEpic); !slices.Contains(got, reason) {
				t.Errorf("trigger causes = %q, want one with cause %q", got, reason)
			}
			if got := len(fakes.steps.calls); got != 1 {
				t.Errorf("step starts = %d, want 1: a refused start is not retried", got)
			}
		})
	}
}

// TestOtherAgentStartErrorIsRetried: an agent node whose start fails with an error that is not a refusal for good
// records nothing, stays running, and starts again on the next pass.
func TestOtherAgentStartErrorIsRetried(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, base := nodeRepo(t)
	nodeEpicBranch(t, repo, "b.go")
	s := openRunStore(t)
	createNodeEpic(t, s, repo, base, nodeAgentFlow)
	fakes := newNodeFakes(s)
	fakes.steps.refuse = errors.New("spawn claude: no such file")
	runner := runNodes(s, fakes)

	runner.Pass(ctx)
	runner.Pass(ctx)

	if run := runAt(t, s, "review"); run.State != store.NodeRunning || run.Result != "" {
		t.Errorf("node review = %+v, want running with no result", run)
	}
	if got := epicOf(t, s).Parked; got != "" {
		t.Errorf("epic Parked = %q, want not parked", got)
	}
	if got := len(fakes.steps.calls); got != 2 {
		t.Errorf("step starts = %d, want 2: a start that is not refused for good is retried", got)
	}
}
