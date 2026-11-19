package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// orchAgent and plannerAgent are the agents the role files of the orchestrate pass tests name.
const (
	orchAgent    = "claude-orchestrator"
	plannerAgent = "claude-planner"
)

// writeRoles writes the fixture's role file with the implementer and verifier of newStartSetup and the lines given.
func (f *passFixture) writeRoles(lines string) {
	f.t.Helper()
	writeReadoptFile(f.t, f.rolesPath, "implementer = \"claude-impl\"\nverifier = \"claude-verify\"\n"+lines)
}

// orchID returns the derived id of task 1's orchestrator step at attempt 1, rerun and decisions.
func orchID(rerun, decisions int64) string {
	return StepID(passEpic, 1, StageOrchestrate, 1, rerun, decisions, 0)
}

// returnRequest ends task 1's implementer step at rerun as returned with status, the kept return of summary and needs,
// in its implement worktree.
func (f *passFixture) returnRequest(rerun int64, status, summary string, needs ...string) {
	f.t.Helper()
	ret, err := json.Marshal(map[string]any{"status": status, "summary": summary, "needs_files": needs})
	if err != nil {
		f.t.Fatal(err)
	}
	id := StepID(passEpic, 1, StageImplement, 1, rerun, 0, 0)
	f.steps.rows[id] = store.StepRow{
		ID: id, End: endReturned, Status: status, Result: string(ret),
		Workspace: ImplementWorktree(f.stateDir, passEpic, 1), Schema: ImplementerSchema,
	}
}

// endOrchestrator ends the orchestrator step id done with the return ret.
func (f *passFixture) endOrchestrator(id, ret string) {
	f.steps.rows[id] = store.StepRow{ID: id, End: endDone, Status: statusDone, Result: ret, Schema: OrchestratorSchema}
}

// startCount returns how many starts the fixture's steps recorded under id.
func (f *passFixture) startCount(id string) int {
	n := 0
	for _, start := range f.steps.starts {
		if start.ID == id {
			n++
		}
	}
	return n
}

// newRequestFixture returns a fixture whose task 1 has passed red, started its implementer, and returned blocked, with
// a role file naming the orchestrator, after one pass more: the build is at orchestrate and its orchestrator started.
func newRequestFixture(t *testing.T) *passFixture {
	t.Helper()
	f := newPassFixture(t)
	f.writeRoles("orchestrator = \"" + orchAgent + "\"\nplanner = \"" + plannerAgent + "\"\n")
	guardStartImplementer(f)
	f.returnRequest(0, "blocked", "the spec does not say which field")
	f.pass()
	return f
}

func TestOrchestratePassStartsAReadOnlyStepAtTheBaseThatReadsTheWorktree(t *testing.T) {
	t.Parallel()
	f := newRequestFixture(t)
	task := f.task()
	if task.Build.Stage != StageOrchestrate || task.Build.Step != passStep(StageImplement, 1, 0) {
		t.Fatalf(
			"build stage %q step %q, want orchestrate on the implementer's step",
			task.Build.Stage,
			task.Build.Step,
		)
	}
	start := f.lastStart()
	if start.ID != orchID(0, 0) || start.Agent != orchAgent || !start.ReadOnly || start.Revision != f.base ||
		start.Workspace != f.repo || start.Schema != OrchestratorSchema || start.ReturnCheck == nil ||
		start.DoneValue != "done" || start.Epic != passEpic || start.Task != "T1" {
		t.Fatalf("start = %+v, want a read-only orchestrator step at the base with its schema and check", start)
	}
	worktree := ImplementWorktree(f.stateDir, passEpic, 1)
	for _, want := range []string{
		strings.TrimRight(OrchestratorRole, "\n"), "\n## Contract\n", "\n## Decisions\n", "\n## Task\n",
		"T1: Build the thing", "\n## Worktree\n", "Path: " + worktree, "\n## Requested files\n", "\n## Tasks\n",
		"\n## Return\n", "Status: blocked", "the spec does not say which field", "\n## Gate\n",
		"Red at the base " + f.base, "\n## History\n",
		"Decisions left before orreryd settles the request without one: 3",
	} {
		if !strings.Contains(start.Prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, start.Prompt)
		}
	}
	if task.Build.Step == "" || f.startCount(orchID(0, 0)) != 1 {
		t.Errorf("orchestrator started %d times, want once", f.startCount(orchID(0, 0)))
	}
}

func TestOrchestratePassFallsBackToThePlannerAgent(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	f.writeRoles("planner = \"" + plannerAgent + "\"\n")
	guardStartImplementer(f)
	f.returnRequest(0, "blocked", "stuck")
	f.pass()
	if start := f.lastStart(); start.ID != orchID(0, 0) || start.Agent != plannerAgent {
		t.Fatalf("start %s under %q, want the orchestrator under the planner's agent %q", start.ID, start.Agent,
			plannerAgent)
	}
}

func TestOrchestratePassWithNoAgentParksOnTheRoleError(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	guardStartImplementer(f)
	f.returnRequest(0, "blocked", "stuck")
	f.pass()
	if build := f.task().Build; build.Held != HeldParked || build.Stage != StageOrchestrate {
		t.Fatalf("build held %q at %q, want parked at orchestrate", build.Held, build.Stage)
	}
	if !strings.Contains(f.epic().Parked, "names no planner agent") {
		t.Errorf("epic parked %q, want the role error", f.epic().Parked)
	}
}

// TestOrchestratePassAfterARestartFindsTheStepByItsDerivedID: a runner that starts after the orchestrator started
// finds the running step by its derived id and starts nothing, and the step's restored check is the orchestrator's.
func TestOrchestratePassAfterARestartFindsTheStepByItsDerivedID(t *testing.T) {
	t.Parallel()
	f := newRequestFixture(t)
	restarted := New(Config{
		Store: f.store, Steps: f.steps, Evidence: f.evidence, Getenv: f.getenv, StateDir: f.stateDir, Now: passClock,
	})
	restarted.pass(f.ctx)
	restarted.pass(f.ctx)
	if n := f.startCount(orchID(0, 0)); n != 1 {
		t.Fatalf("orchestrator started %d times across the restart, want once", n)
	}
	check, err := ReadoptCheck(f.store)(f.ctx, store.StepRow{
		ID: orchID(0, 0), Schema: OrchestratorSchema, Epic: passEpic, Task: "T1",
	})
	if err != nil || check == nil {
		t.Fatalf("ReadoptCheck nil = %v, error %v, want the orchestrator's check", check == nil, err)
	}
	got := check(
		json.RawMessage(orchReturn(t, map[string]any{"decision": "rerun", "after_tasks": []int64{1}})),
	)
	if want := []string{"after_tasks[0]: T1 is this task"}; !slices.Equal(got, want) {
		t.Errorf("restored check = %q, want %q", got, want)
	}
	if _, err := ReadoptCheck(f.store)(f.ctx, store.StepRow{
		ID: "x", Schema: OrchestratorSchema, Epic: passEpic, Task: "T9",
	}); err == nil {
		t.Error("ReadoptCheck of a row naming no task = nil error, want one")
	}
}

func TestOrchestratePassRerunRecordsTheDecisionAndRerunsTheImplementerWithTheNote(t *testing.T) {
	t.Parallel()
	f := newRequestFixture(t)
	const deliverable = "Thing holds: thing.go:3 already names the field; use it and return done."
	f.endOrchestrator(orchID(0, 0), orchReturn(t, map[string]any{
		"decision": "rerun", "version": versionWith(map[string]any{
			"changed": []string{"deliverable"}, "deliverable": deliverable,
		}),
	}))
	f.pass()
	build := f.task().Build
	if build.Stage != StageImplement || build.Rerun != 1 || build.Attempt != 1 || build.Decisions != 1 ||
		build.Failure != "returned blocked: the spec does not say which field" {
		t.Fatalf("build = %+v, want implement at attempt 1, rerun 1, one decision, orreryd's failure", build)
	}
	if task := f.task(); task.Deliverable != deliverable || task.Version != 2 {
		t.Errorf("task deliverable %q at version %d, want the version's deliverable at version 2",
			task.Deliverable, task.Version)
	}
	decisions := f.epic().Decisions
	if len(decisions) == 0 || decisions[len(decisions)-1].ID != "T1-request-1" ||
		decisions[len(decisions)-1].Text != "T1 rerun: the code says so" {
		t.Fatalf("epic decisions = %+v, want T1-request-1 recording the rerun", decisions)
	}
	start := f.lastStart()
	if start.ID != passStep(StageImplement, 1, 1) || !strings.Contains(start.Prompt, deliverable) {
		t.Fatalf("start %s, want the implementer at rerun 1 with the version's deliverable in its brief", start.ID)
	}
}

func TestOrchestratePassFailKeepsTheLongerFailureAndRecordsTheDecision(t *testing.T) {
	t.Parallel()
	f := newRequestFixture(t)
	worktree := ImplementWorktree(f.stateDir, passEpic, 1)
	failure := "The task's files cannot hold its deliverable. " + strings.Repeat("evidence ", 600)
	f.endOrchestrator(orchID(0, 0), orchReturn(t, map[string]any{
		"decision": "fail", "failure": failure, "needs_files": []string{"internal/other/a.go"},
	}))
	before := ladderClimbBefore(f, 1)
	f.pass()
	task := f.task()
	if task.Build.Failure != failure[:store.TaskDecisionMax] {
		t.Fatalf("task failure of %d bytes, want the first %d bytes", len(task.Build.Failure), store.TaskDecisionMax)
	}
	ladderClimbCheck(f, store.RungOf(before), store.MovedByFailDecision)
	if want := []string{"internal/other/a.go"}; !reflect.DeepEqual(task.NeedsFiles, want) {
		t.Errorf("NeedsFiles = %v, want %v", task.NeedsFiles, want)
	}
	if got := f.pendingTriggers(); len(got) != 0 {
		t.Errorf("pending triggers = %v, want none: a climb to rung 2 adds no trigger", got)
	}
	if d := f.epic().Decisions; len(d) == 0 || d[len(d)-1].Text != "T1 fail: the code says so" {
		t.Errorf("decisions = %+v, want the fail recorded", d)
	}
	ladderClimbKeepsWorktrees(f, worktree)
}

func TestOrchestratePassAskHoldsTheTaskUntilTheOwnerAnswersThenOrchestratesAgain(t *testing.T) {
	t.Parallel()
	f := newRequestFixture(t)
	f.endOrchestrator(orchID(0, 0), orchReturn(t, map[string]any{"decision": "ask", "question": askQuestion()}))
	f.pass()
	build := f.task().Build
	questions := f.epic().Questions
	if build.Held != HeldAsked || build.Decisions != 1 || len(questions) == 0 {
		t.Fatalf("build held %q decisions %d, questions %d, want asked after one decision with a question",
			build.Held, build.Decisions, len(questions))
	}
	asked := questions[len(questions)-1]
	if asked.Seq != build.Question || asked.State != store.QuestionOpen || asked.Turn != 0 ||
		asked.Label != "capture-broken" || asked.Text != "T1 (contract-cannot-hold): R1's capture exits 2 as written" {
		t.Fatalf("question = %+v, want the open question the build holds on", asked)
	}
	if tells := f.tells(); !slices.Contains(tells, asked.Text) {
		t.Errorf("tells = %q, want the question's text", tells)
	}
	f.pass()
	if n := f.startCount(orchID(0, 1)); n != 0 {
		t.Fatalf("orchestrator started %d times before the answer, want none", n)
	}
	if _, err := f.store.AnswerQuestion(f.ctx, passEpic, "a1", "edit it", asked.Seq, "edit", passClock()); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}
	f.pass()
	if build := f.task().Build; build.Held != "" || build.Question != 0 || f.startCount(orchID(0, 1)) != 1 {
		t.Fatalf("build held %q question %d, orchestrator starts %d, want released and orchestrating again",
			build.Held, build.Question, f.startCount(orchID(0, 1)))
	}
	if !strings.Contains(f.lastStart().Prompt, "edit it") {
		t.Error("the second orchestrator's prompt lacks the owner's answer")
	}
}

// TestOrchestratePassBlockedReturnClimbsAsBefore: a blocked return the orchestrator does not decide climbs the task
// with the return as its failure, as it did before the orchestrator.
func TestOrchestratePassBlockedReturnClimbsAsBefore(t *testing.T) {
	t.Parallel()
	f := newRequestFixture(t)
	f.endOrchestrator(orchID(0, 0), orchReturn(t, map[string]any{"status": "blocked"}))
	before := ladderClimbBefore(f, 1)
	f.pass()
	if task := f.task(); task.Build.Failure != "returned blocked: the spec does not say which field" {
		t.Fatalf("task failure %q, want the blocked return as before", task.Build.Failure)
	}
	ladderClimbCheck(f, store.RungOf(before), store.MovedByUnit)
}

// TestOrchestratePassBoundsDecisionsAtThree: a task whose implementer keeps returning is decided three times, and its
// fourth return climbs it as it did before the orchestrator.
func TestOrchestratePassBoundsDecisionsAtThree(t *testing.T) {
	t.Parallel()
	f := newRequestFixture(t)
	var before store.TaskBuild
	for decision := range int64(decisionLimit) {
		f.endOrchestrator(orchID(decision, decision), orchReturn(t, map[string]any{
			"decision": "rerun",
		}))
		f.pass()
		f.returnRequest(decision+1, "blocked", "still stuck")
		if decision == decisionLimit-1 {
			before = ladderClimbBefore(f, 1)
		}
		f.pass()
	}
	task := f.task()
	if task.Build.Failure != "returned blocked: still stuck" || task.Build.Decisions != 3 {
		t.Fatalf("task failure %q decisions %d, want the blocked return kept after three decisions",
			task.Build.Failure, task.Build.Decisions)
	}
	ladderClimbCheck(f, store.RungOf(before), store.MovedByUnit)
	if n := f.startCount(orchID(3, 3)); n != 0 {
		t.Errorf("a fourth orchestrator started %d times, want none", n)
	}
}

func TestOrchestratePassGrantsAFreeFile(t *testing.T) {
	t.Parallel()
	f := newRequestFixture(t)
	f.endOrchestrator(orchID(0, 0), orchReturn(t, map[string]any{
		"decision": "grant", "version": filesVersion("internal/b.go"),
	}))
	f.pass()
	task := f.task()
	if want := []string{"thing.go", "internal/b.go"}; !reflect.DeepEqual(task.Files, want) {
		t.Fatalf("Files = %v, want %v", task.Files, want)
	}
	if start := f.lastStart(); start.ID != passStep(StageImplement, 1, 1) ||
		!slices.Contains(start.Owned, "internal/b.go") {
		t.Errorf("start %s owning %v, want the implementer at rerun 1 owning the grant", start.ID, start.Owned)
	}
}

// waitFile is the file task 2 of the wait fixture owns, which task 1's implementer asks for.
const waitFile = "internal/other/a.go"

// newWaitFixture returns a fixture whose task 2 owns waitFile, and whose task 1 asked for it, could not be granted it,
// and is at orchestrate with its orchestrator started.
func newWaitFixture(t *testing.T) *passFixture {
	t.Helper()
	const file = waitFile
	f := newOwnerFixture(t, file)
	f.writeRoles("orchestrator = \"" + orchAgent + "\"\n")
	first := []int64{1}
	f.runner.PassTasks(f.ctx, passEpic, first)
	f.addRun(RunID(passEpic, 1, StageRed, 0, 0), evidence.PhaseRed, f.base, 1, evidence.VerdictFails)
	f.runner.PassTasks(f.ctx, passEpic, first)
	f.returnRequest(0, "needs_context", "needs the other file", file)
	f.runner.PassTasks(f.ctx, passEpic, first)
	if f.task().Build.Stage != StageOrchestrate {
		t.Fatalf("build at %q, want orchestrate after the grant was refused", f.task().Build.Stage)
	}
	return f
}

func TestOrchestratePassGrantsAnOwnedFileBehindItsOwnerAndWaits(t *testing.T) {
	t.Parallel()
	const file = waitFile
	f := newWaitFixture(t)
	start := f.lastStart()
	if start.ID != orchID(0, 0) || !strings.Contains(start.Prompt, "- "+file+": asked for; new at the base; "+
		"owned by open T2 (not started)") {
		t.Fatalf("start %s, want the orchestrator with the file's holder in Requested files:\n%s", start.ID,
			start.Prompt)
	}
	f.endOrchestrator(orchID(0, 0), orchReturn(t, map[string]any{
		"decision": "grant", "version": filesVersion(file),
		"after_tasks": []int64{2},
	}))
	f.runner.PassTasks(f.ctx, passEpic, []int64{1})
	task := f.task()
	if task.Build.Held != HeldWaiting || task.Build.After != "2" || !slices.Contains(task.Files, file) {
		t.Fatalf("build held %q after %q files %v, want waiting on T2 with the file granted", task.Build.Held,
			task.Build.After, task.Files)
	}
	if d := f.epic().Decisions; len(d) == 0 || d[len(d)-1].Text != "T1 grant "+file+" after T2: the code says so" {
		t.Errorf("decisions = %+v, want the grant behind T2 recorded", d)
	}
	if n := f.startCount(passStep(StageImplement, 1, 1)); n != 0 {
		t.Errorf("implementer started %d times while waiting, want none", n)
	}
}

func TestOrchestratePassGrantRefusedByTheStoreSettlesAsBefore(t *testing.T) {
	t.Parallel()
	const file = waitFile
	f := newWaitFixture(t)
	// The step's check saw T2 as accepted and pending, and T2 opened again before the decision was carried out: the
	// store refuses the grant of its file, since after_tasks does not name it.
	peers := []Peer{{Task: f.task()}}
	ret := orchReturn(t, map[string]any{"decision": "grant", "version": filesVersion("internal/c.go", file)})
	if got := OrchestratorCheck(f.task(), peers)(json.RawMessage(ret)); len(got) != 0 {
		t.Fatalf("check with T2 unseen = %q, want none", got)
	}
	view := View{Epic: passEpic, Task: f.task(), Returned: &store.StepRow{
		ID: passStep(StageImplement, 1, 0), End: endReturned, Status: "needs_context",
		Result: `{"status":"needs_context","summary":"needs the other file","needs_files":["` + file + `"]}`,
	}, Step: orchDone(ret), Peers: peers}
	before := ladderClimbBefore(f, 1)
	if !f.runner.execute(f.ctx, f.epic(), view, Decide(view)) {
		t.Fatal("execute changed nothing, want the fallback carried out")
	}
	want := "returned needs_context: needs the other file; orreryd refused the orchestrator's grant: " +
		file + " is owned by open task T2"
	if task := f.task(); task.Build.Failure != want {
		t.Fatalf("task failure %q, want %q", task.Build.Failure, want)
	}
	ladderClimbCheck(f, store.RungOf(before), store.MovedByUnit)
}

// integrateTask accepts open task seq of the fixture's epic and ends a passed integrate batch that composed it as
// commit, which becomes the epic's stored base.
func (f *passFixture) integrateTask(seq int64, commit string) {
	f.t.Helper()
	var from store.TaskBuild
	for _, task := range f.epic().Tasks {
		if task.Seq == seq {
			from = task.Build
		}
	}
	if err := f.store.AcceptTask(f.ctx, passEpic, seq, from, ReviewUnreviewed); err != nil {
		f.t.Fatalf("AcceptTask: %v", err)
	}
	path := "plan/S" + decimal(seq) + "/integrate"
	if err := f.store.StartNodes(f.ctx, passEpic, []store.NodeRun{{
		Path: path, ID: "run-" + decimal(seq), Node: "integrate", Kind: "integrate",
	}}, nil, passClock()); err != nil {
		f.t.Fatalf("StartNodes: %v", err)
	}
	runs, err := f.store.NodeRuns(f.ctx, passEpic)
	if err != nil {
		f.t.Fatalf("NodeRuns: %v", err)
	}
	attempt := runs[slices.IndexFunc(runs, func(run store.NodeRun) bool { return run.Path == path })].Attempt
	result := `{"base":"","candidate":"` + commit + `","composed":[{"task":` + decimal(seq) + `,"commit":"` + commit +
		`"}],"leftOut":[]}`
	if err := f.store.ClaimBatch(f.ctx, passEpic, path, attempt, commit, "", result, []int64{seq}); err != nil {
		f.t.Fatalf("ClaimBatch: %v", err)
	}
	if err := f.store.EndBatch(f.ctx, passEpic, store.BatchEnd{
		Path: path, Attempt: attempt, Outcome: "passed", Over: commit,
	}, nil, passClock()); err != nil {
		f.t.Fatalf("EndBatch: %v", err)
	}
}

// commitOff makes a commit on base that writes content to path, without moving any ref, and returns its id.
func commitOff(t *testing.T, repo, base, path, content string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "scratch")
	buildRun(t, repo, "worktree", "add", "-q", "--detach", dir, base)
	writeFile(t, filepath.Join(dir, path), content)
	buildRun(t, dir, "add", "--all")
	buildRun(t, dir, "commit", "-q", "-m", "write "+path)
	commit := buildHead(t, dir)
	buildRun(t, repo, "worktree", "remove", "--force", dir)
	return commit
}

// writeFile writes content to path, making its directory.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// identify gives repo the committer identity the engine's rebase uses.
func identify(t *testing.T, repo string) {
	t.Helper()
	buildRun(t, repo, "config", "user.name", "Test")
	buildRun(t, repo, "config", "user.email", "test@example.invalid")
	buildRun(t, repo, "config", "commit.gpgsign", "false")
}

// TestOrchestratePassWaitingTaskMovesOntoTheNewBaseOnceItsTaskIsIntegrated: a build waiting on T2 waits while T2 is
// open, and once a batch integrates T2 its worktree moves onto the stored base with the uncommitted work kept, its base
// moves with it, and its implementer starts there.
func TestOrchestratePassWaitingTaskMovesOntoTheNewBaseOnceItsTaskIsIntegrated(t *testing.T) {
	t.Parallel()
	const file = waitFile
	f := newWaitFixture(t)
	identify(t, f.repo)
	worktree := ImplementWorktree(f.stateDir, passEpic, 1)
	writeFile(t, filepath.Join(worktree, "thing.go"), "package thing // kept\n")
	f.endOrchestrator(orchID(0, 0), orchReturn(t, map[string]any{
		"decision": "rerun", "after_tasks": []int64{2},
	}))
	first := []int64{1}
	f.runner.PassTasks(f.ctx, passEpic, first)
	f.runner.PassTasks(f.ctx, passEpic, first)
	if build := f.task().Build; build.Held != HeldWaiting || build.Base != f.base {
		t.Fatalf("build held %q at %s, want waiting at the old base", build.Held, build.Base)
	}
	newBase := commitOff(t, f.repo, f.base, file, "package other\n")
	f.integrateTask(2, newBase)
	f.runner.PassTasks(f.ctx, passEpic, first)
	build := f.task().Build
	if build.Held != "" || build.Base != newBase || build.After != "2" {
		t.Fatalf("build held %q base %s after %q, want resumed at %s keeping After", build.Held, build.Base,
			build.After, newBase)
	}
	if head := buildHead(t, worktree); head != newBase {
		t.Errorf("worktree HEAD %s, want %s", head, newBase)
	}
	if data, err := os.ReadFile(
		filepath.Join(worktree, "thing.go"),
	); err != nil ||
		string(data) != "package thing // kept\n" {
		t.Errorf("uncommitted work = %q, %v, want it kept", data, err)
	}
	start := f.lastStart()
	if start.ID != passStep(StageImplement, 1, 1) || start.Revision != newBase {
		t.Errorf("start %s at %s, want the implementer at rerun 1 on %s", start.ID, start.Revision, newBase)
	}
}

// TestOrchestratePassWaitingTaskWhoseMoveFailsClimbsWithTheReason: a waiting build whose worktree git cannot move
// onto its new base climbs the task, with the move's failure naming the base as its failure.
func TestOrchestratePassWaitingTaskWhoseMoveFailsClimbsWithTheReason(t *testing.T) {
	t.Parallel()
	const file = waitFile
	f := newWaitFixture(t)
	identify(t, f.repo)
	worktree := ImplementWorktree(f.stateDir, passEpic, 1)
	writeFile(t, filepath.Join(worktree, file), "package mine\n")
	buildRun(t, worktree, "add", file)
	f.endOrchestrator(orchID(0, 0), orchReturn(t, map[string]any{
		"decision": "rerun", "after_tasks": []int64{2},
	}))
	f.runner.PassTasks(f.ctx, passEpic, []int64{1})
	newBase := commitOff(t, f.repo, f.base, file, "package other\n")
	f.integrateTask(2, newBase)
	before := ladderClimbBefore(f, 1)
	f.runner.PassTasks(f.ctx, passEpic, []int64{1})
	task := f.task()
	if !strings.HasPrefix(task.Build.Failure, "T1 could not move onto the new base "+newBase+": ") {
		t.Fatalf("task failure %q, want the move's failure naming the base", task.Build.Failure)
	}
	ladderClimbCheck(f, store.RungOf(before), store.MovedByUnit)
}
