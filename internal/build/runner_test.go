package build

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/roles"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// passEpic is the epic the pass tests drive. Its branch is refs/heads/epic/epic.
const passEpic = "acme/epic"

// passFailedReview is the kept return of a review gate that failed with one finding, which its one assertion fails.
const passFailedReview = `{"status":"done","findings":[{"id":"F1","claim":"the thing is wrong","citation":"R1",` +
	`"location":"thing.go:3","evidence":"line 3","verify_by":"run it"}],"results":[{"result":"fails"}]}`

// passClock is the time every pass test runs at.
func passClock() time.Time {
	return time.Unix(1700001000, 0)
}

// passStep returns the derived id of the step of unit for attempt and rerun of task 1 of passEpic, at the first review
// gate and first finding.
func passStep(unit string, attempt, rerun int64) string {
	return StepID(passEpic, 1, unit, attempt, rerun, 0, 0)
}

// passRunID returns the derived id of the evidence run of phase for attempt of task 1 of passEpic, at its first
// rerun.
func passRunID(phase string, attempt int64) string {
	return RunID(passEpic, 1, phase, attempt, 0)
}

// passSteps is a Steps that records each start and answers Status from the rows a test sets. Start returns err when
// it is set.
type passSteps struct {
	rows   map[string]store.StepRow
	starts []enginehost.StepStart
	err    error
}

// Start records the start, and on success leaves the step open under its id, as the engine does.
func (f *passSteps) Start(_ context.Context, start enginehost.StepStart) (string, error) {
	f.starts = append(f.starts, start)
	if f.err != nil {
		return "", f.err
	}
	if _, ok := f.rows[start.ID]; !ok {
		f.rows[start.ID] = store.StepRow{ID: start.ID, Workspace: start.Workspace}
	}
	return start.ID, nil
}

func (f *passSteps) Status(_ context.Context, id string) (store.StepRow, error) {
	row, ok := f.rows[id]
	if !ok {
		return store.StepRow{}, fmt.Errorf("step %s: %w", id, store.ErrNoStep)
	}
	return row, nil
}

// passRun is one evidence run a Runner asked to start.
type passRun struct {
	id, epic, revision, phase string
	seq                       int64
}

// passEvidence is an Evidence that records each run it is asked to start and returns err when it is set. A run it
// starts is stored in the store capturing, as the engine stores it. onRun, when it is set, is called after each start.
type passEvidence struct {
	store *store.Store
	runs  []passRun
	err   error
	onRun func()
}

func (f *passEvidence) RunTask(
	ctx context.Context, id, epic string, seq int64, revision, phase string,
) (store.EvidenceRun, error) {
	f.runs = append(f.runs, passRun{id: id, epic: epic, seq: seq, revision: revision, phase: phase})
	if f.err != nil {
		return store.EvidenceRun{}, f.err
	}
	spec, err := taskItem(ctx, f.store, epic, seq)
	if err != nil {
		return store.EvidenceRun{}, err
	}
	if err := f.store.AddEvidenceRun(ctx, store.EvidenceRun{
		ID: id, Epic: epic, Item: taskLabel(seq), Revision: revision, Phase: phase, Source: evidence.SourceCaptured,
		Spec: spec, Created: passClock(),
	}); err != nil {
		return store.EvidenceRun{}, err
	}
	if f.onRun != nil {
		f.onRun()
	}
	return f.store.EvidenceRun(ctx, id)
}

// taskItem returns the evidence item task seq of epic holds in the store. A run of the task keeps that item as its
// spec, as enginehost.Evidence.RunTask does.
func taskItem(ctx context.Context, s *store.Store, epic string, seq int64) (evidence.Item, error) {
	row, err := s.Epic(ctx, epic)
	if err != nil {
		return evidence.Item{}, fmt.Errorf("read epic %s: %w", epic, err)
	}
	for _, task := range row.Tasks {
		if task.Seq == seq {
			return task.Evidence, nil
		}
	}
	return evidence.Item{}, fmt.Errorf("epic %s has no task %d", epic, seq)
}

func (f *passEvidence) Artifact(_ context.Context, _, _ string) ([]byte, string, error) {
	return nil, "", nil
}

func (f *passEvidence) StderrLog(_ context.Context, _ string) ([]byte, string, error) {
	return nil, "", nil
}

// passPlan is the planner's return for one task, task 1, which builds at check and whose evidence is its conclusion.
func passPlan() store.EpicPlan {
	return store.EpicPlan{
		Tasks: []store.EpicTaskInput{{
			Title: "Build the thing", Deliverable: "The thing holds", Files: []string{"thing.go"},
			Requirements: []string{"R1"}, Gate: "check",
			Evidence: evidence.Item{
				Capture: []string{"scripts/check"}, Limit: 60, Conclusion: "the thing holds", Judge: "the judge",
			},
		}},
		Outline: "One task",
		Outcome: "work",
	}
}

// passFixture is a Runner over a real store that holds one accepted epic with task 1 planned at its base, and the
// fakes it passes against.
type passFixture struct {
	t         *testing.T
	ctx       context.Context
	store     *store.Store
	dbPath    string
	repo      string
	base      string
	stateDir  string
	rolesPath string
	getenv    func(string) string
	steps     *passSteps
	evidence  *passEvidence
	runner    *Runner
}

// newPassFixture returns a fixture whose epic has an agent reviewer for each of reviewGates, in order, as its task-stage
// reviewers. Its repository has main at the base, and the epic has no branch yet.
func newPassFixture(t *testing.T, reviewGates ...string) *passFixture {
	t.Helper()
	revs := make([]reviewers.Reviewer, 0, len(reviewGates))
	for _, name := range reviewGates {
		revs = append(revs, agentReviewer(name))
	}
	return newPassFixtureWith(t, revs)
}

// newPassFixtureWith returns a fixture whose epic declares revs as its reviewers. Its review gates are the names of its
// enabled task-stage reviewers in order, as epic_create fills them. Its repository has main at the base, and the epic
// has no branch yet.
func newPassFixtureWith(t *testing.T, revs []reviewers.Reviewer) *passFixture {
	t.Helper()
	ctx := context.Background()
	taskRevs := reviewers.ForStage(revs, reviewers.StageTask)
	reviewGates := make([]string, 0, len(taskRevs))
	for _, rev := range taskRevs {
		reviewGates = append(reviewGates, rev.Name)
	}
	setup := newStartSetup(t, readoptCommandGate("check"), readoptReviewGate())
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(ctx, path, storetest.NoSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if err = s.CreateEpic(ctx, store.EpicRow{
		ID: passEpic, Repository: setup.epic.Repository, Base: setup.base, Title: "Build the thing",
		Phase: "accepted", Created: passClock(), Changed: passClock(), Gates: setup.epic.Gates,
		ReviewGates: reviewGates, Reviewers: revs,
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	if _, err = s.ClaimTurn(ctx, passEpic, passClock()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = s.CompleteTurn(ctx, passEpic, 1, passPlan(), passClock()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	buildRun(t, setup.epic.Repository, "update-ref", "refs/heads/main", setup.base)
	steps := &passSteps{rows: make(map[string]store.StepRow)}
	fakeEvidence := &passEvidence{store: s}
	return &passFixture{
		t: t, ctx: ctx, store: s, dbPath: path, repo: setup.epic.Repository, base: setup.base, stateDir: setup.stateDir,
		rolesPath: setup.rolesPath, getenv: setup.getenv, steps: steps, evidence: fakeEvidence,
		runner: New(Config{
			Store: s, Steps: steps, Evidence: fakeEvidence, Getenv: setup.getenv, StateDir: setup.stateDir,
			Now: passClock,
		}),
	}
}

// newImplementerFixture returns a fixture whose task has passed red and started its implementer at attempt 1, which
// the fake steps hold as open.
func newImplementerFixture(t *testing.T) *passFixture {
	t.Helper()
	f := newPassFixture(t)
	f.pass()
	f.addRun(f.lastRun().id, evidence.PhaseRed, f.base, 1, evidence.VerdictFails)
	f.pass()
	return f
}

// newVerifyingFixture returns a fixture whose task has a review at attempt 1 of gate review, stored with one finding,
// and whose build is at verify on that finding.
func newVerifyingFixture(t *testing.T) *passFixture {
	t.Helper()
	f := newPassFixture(t, "review")
	f.pass()
	f.setGreen(1, evidence.VerdictHolds)
	review := store.TaskReview{
		Attempt: 1, Position: 0, Gate: "review", Step: passStep(StageReview, 1, 0), Verdict: verdictFails,
		Findings: []store.TaskFinding{{
			Position: 0, ID: "F1", Claim: "the thing is wrong", Citation: "R1", Location: "thing.go:3",
			Evidence: "line 3", VerifyBy: "run it",
		}},
	}
	to := store.TaskBuild{
		Stage: StageVerify, Base: f.base, Attempt: 1, Revision: f.base, GreenRun: passRunID(StageGreen, 1),
	}
	if err := f.store.AddTaskReview(f.ctx, passEpic, 1, f.task().Build, to, review); err != nil {
		t.Fatalf("AddTaskReview: %v", err)
	}
	return f
}

// epic returns the stored epic.
func (f *passFixture) epic() store.EpicRow {
	f.t.Helper()
	row, err := f.store.Epic(f.ctx, passEpic)
	if err != nil {
		f.t.Fatalf("Epic: %v", err)
	}
	return row
}

// task returns task 1 of the stored epic.
func (f *passFixture) task() store.EpicTask {
	f.t.Helper()
	return f.epic().Tasks[0]
}

// pass runs one pass of the runner.
func (f *passFixture) pass() {
	f.runner.pass(f.ctx)
}

// setBuild stores build as task 1's build, moving it from the build stored now.
func (f *passFixture) setBuild(build store.TaskBuild) {
	f.t.Helper()
	if err := f.store.SetTaskBuild(f.ctx, passEpic, 1, f.task().Build, build); err != nil {
		f.t.Fatalf("SetTaskBuild: %v", err)
	}
}

// setGreen stores the build of task 1 at green for attempt at the base, with the green run of that attempt ended at
// the base with exit 0 and verdict.
func (f *passFixture) setGreen(attempt int64, verdict string) {
	f.t.Helper()
	f.setBuild(store.TaskBuild{Stage: StageGreen, Base: f.base, Attempt: attempt, Revision: f.base})
	f.addRun(passRunID(StageGreen, attempt), evidence.PhaseGreen, f.base, 0, verdict)
}

// setStep stores the row of the step id, ended with end and kept result, keeping the workspace its start recorded.
func (f *passFixture) setStep(id, end, result string) {
	f.steps.rows[id] = store.StepRow{ID: id, End: end, Result: result, Workspace: f.steps.rows[id].Workspace}
}

// addRun gives the evidence run id of phase at revision its capture, exit, and judged verdict, as the store holds a
// judged run. The run is added first when the runner did not start it.
func (f *passFixture) addRun(id, phase, revision string, exit int, verdict string) {
	f.t.Helper()
	at := passClock()
	_, err := f.store.EvidenceRun(f.ctx, id)
	switch {
	case errors.Is(err, store.ErrNoEvidenceRun):
		if err = f.store.AddEvidenceRun(f.ctx, store.EvidenceRun{
			ID: id, Epic: passEpic, Item: "T1", Revision: revision, Phase: phase, Source: evidence.SourceCaptured,
			Spec: f.task().Evidence, Created: at,
		}); err != nil {
			f.t.Fatalf("AddEvidenceRun: %v", err)
		}
	case err != nil:
		f.t.Fatalf("EvidenceRun: %v", err)
	}
	if err := f.store.RecordEvidenceCapture(f.ctx, id, store.EvidenceCapture{
		Exit: exit, LogHash: "loghash", LogBytes: 3, At: at,
	}); err != nil {
		f.t.Fatalf("RecordEvidenceCapture: %v", err)
	}
	if err := f.store.RecordEvidenceVerdict(f.ctx, id, store.EvidenceVerdict{
		Verdict: verdict, Reason: "the judge read the log", Passed: evidence.Passed(phase, "", exit, verdict), At: at,
	}); err != nil {
		f.t.Fatalf("RecordEvidenceVerdict: %v", err)
	}
}

// lastRun returns the last evidence run the runner asked to start, and fails the test when it asked for none.
func (f *passFixture) lastRun() passRun {
	f.t.Helper()
	if len(f.evidence.runs) == 0 {
		f.t.Fatal("RunTask was never called")
	}
	return f.evidence.runs[len(f.evidence.runs)-1]
}

// lastStart returns the last step start the runner made, and fails the test when it made none.
func (f *passFixture) lastStart() enginehost.StepStart {
	f.t.Helper()
	if len(f.steps.starts) == 0 {
		f.t.Fatal("Start was never called")
	}
	return f.steps.starts[len(f.steps.starts)-1]
}

// tells returns the texts of the epic's untold tells.
func (f *passFixture) tells() []string {
	f.t.Helper()
	entries, err := f.store.Attention(f.ctx)
	if err != nil {
		f.t.Fatalf("Attention: %v", err)
	}
	var texts []string
	for _, entry := range entries {
		if entry.Epic != passEpic {
			continue
		}
		for _, tell := range entry.Tells {
			texts = append(texts, tell.Text)
		}
	}
	return texts
}

// pendingTriggers returns the ids of the epic's pending triggers.
func (f *passFixture) pendingTriggers() []string {
	f.t.Helper()
	triggers, err := f.store.TurnTriggers(f.ctx, passEpic, 0)
	if err != nil {
		f.t.Fatalf("TurnTriggers: %v", err)
	}
	var ids []string
	for _, trigger := range triggers {
		if trigger.State == "pending" {
			ids = append(ids, trigger.ID)
		}
	}
	return ids
}

// addTaskWorktree adds a worktree named name under the task's directory at the base, and returns its path.
func (f *passFixture) addTaskWorktree(name string) string {
	f.t.Helper()
	path := filepath.Join(TaskDir(f.stateDir, passEpic, 1), name)
	if err := AddWorktree(f.ctx, f.repo, path, f.base); err != nil {
		f.t.Fatalf("AddWorktree: %v", err)
	}
	return path
}

// expectNoWorktrees fails the test unless the task's directory is gone and git no longer lists the worktree at path.
func (f *passFixture) expectNoWorktrees(path string) {
	f.t.Helper()
	if _, err := os.Stat(TaskDir(f.stateDir, passEpic, 1)); !errors.Is(err, fs.ErrNotExist) {
		f.t.Errorf("task directory stat = %v, want it removed", err)
	}
	if buildListsWorktree(f.t, f.repo, path) {
		f.t.Errorf("git still lists worktree %s", path)
	}
}

// expectGreenAt fails the test unless task 1 is at green at revision, with its green run started at revision.
func (f *passFixture) expectGreenAt(revision string) {
	f.t.Helper()
	build := f.task().Build
	if build.Stage != StageGreen || build.Revision != revision {
		f.t.Errorf("build stage %q revision %s, want green at %s", build.Stage, build.Revision, revision)
	}
	want := passRun{id: passRunID(StageGreen, 1), epic: passEpic, seq: 1, revision: revision, phase: StageGreen}
	if got := f.lastRun(); got != want {
		f.t.Errorf("last evidence run = %+v, want %+v", got, want)
	}
}

// TestPassCreatesEpicBranchAtBaseAndMarksItCreated covers R1: the branch of an epic with none is created at its base.
func TestPassCreatesEpicBranchAtBaseAndMarksItCreated(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	f.pass()
	if got := f.epic().Branch; got != "created" {
		t.Errorf("epic Branch = %q, want created", got)
	}
	if got := buildRun(t, f.repo, "rev-parse", BranchRef(passEpic)); got != f.base {
		t.Errorf("epic branch = %s, want the base %s", got, f.base)
	}
}

// TestPassAdoptsBranchLeftCreatingAtBase covers R1: a branch left creating at the base, checked out nowhere, is the
// epic's and becomes created.
func TestPassAdoptsBranchLeftCreatingAtBase(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	buildRun(t, f.repo, "update-ref", BranchRef(passEpic), f.base)
	if err := f.store.SetEpicBranch(f.ctx, passEpic, "creating"); err != nil {
		t.Fatalf("SetEpicBranch: %v", err)
	}
	f.pass()
	if got := f.epic(); got.Branch != "created" || got.Parked != "" {
		t.Errorf("epic Branch = %q, Parked = %q, want created and not parked", got.Branch, got.Parked)
	}
}

// TestPassParksWhenEpicBranchExistsElsewhere covers R1: a branch that already exists where the epic has none parks the
// epic, and the epic starts no step.
func TestPassParksWhenEpicBranchExistsElsewhere(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	elsewhere := buildObject(t, f.repo, f.base)
	buildRun(t, f.repo, "update-ref", BranchRef(passEpic), elsewhere)
	f.pass()
	const reason = "epic/epic exists"
	epic := f.epic()
	if epic.Parked != reason || epic.Branch != "" {
		t.Errorf("epic Parked = %q, Branch = %q, want parked %q with no branch state", epic.Parked, epic.Branch, reason)
	}
	if got := f.tells(); !slices.Contains(got, reason) {
		t.Errorf("tells = %v, want %q", got, reason)
	}
	if got := len(f.evidence.runs); got != 0 {
		t.Errorf("RunTask calls = %d, want none for a parked epic", got)
	}
	if got := buildRun(t, f.repo, "rev-parse", BranchRef(passEpic)); got != elsewhere {
		t.Errorf("epic branch = %s, want the branch that was there %s", got, elsewhere)
	}
}

// TestPassParksWhenCreatedBranchIsGone covers R1: a created epic whose branch is missing is parked.
func TestPassParksWhenCreatedBranchIsGone(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	if err := f.store.SetEpicBranch(f.ctx, passEpic, "created"); err != nil {
		t.Fatalf("SetEpicBranch: %v", err)
	}
	f.pass()
	const reason = "epic/epic is gone"
	if got := f.epic().Parked; got != reason {
		t.Errorf("epic Parked = %q, want %q", got, reason)
	}
	if got := f.tells(); !slices.Contains(got, reason) {
		t.Errorf("tells = %v, want %q", got, reason)
	}
}

// TestPassParkedEpicWithGoneBranchIsToldOnce covers R1: a later pass over the parked epic adds no second tell.
func TestPassParkedEpicWithGoneBranchIsToldOnce(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	if err := f.store.SetEpicBranch(f.ctx, passEpic, "created"); err != nil {
		t.Fatalf("SetEpicBranch: %v", err)
	}
	f.pass()
	f.pass()
	const reason = "epic/epic is gone"
	if got := f.tells(); !slices.Equal(got, []string{reason}) {
		t.Errorf("tells after two passes = %v, want only %q", got, reason)
	}
}

// TestPassPausedEpicCreatesNoBranch covers R1: a paused epic reads a created branch only and creates none.
func TestPassPausedEpicCreatesNoBranch(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	if err := f.store.PauseEpic(f.ctx, passEpic); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}
	f.pass()
	if got := f.epic().Branch; got != "" {
		t.Errorf("epic Branch = %q, want none while paused", got)
	}
	if _, exists, err := BranchTip(f.ctx, f.repo, passEpic); err != nil || exists {
		t.Errorf("BranchTip = exists %t, err %v, want no branch", exists, err)
	}
	if got := len(f.evidence.runs); got != 0 {
		t.Errorf("RunTask calls = %d, want none while paused", got)
	}
}

// TestPassStartsRedAtBaseUnderRunID: a fresh task's red run starts under its derived id at the base.
func TestPassStartsRedAtBaseUnderRunID(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	f.pass()
	want := passRun{id: passRunID(StageRed, 0), epic: passEpic, seq: 1, revision: f.base, phase: StageRed}
	if got := f.evidence.runs; !slices.Equal(got, []passRun{want}) {
		t.Fatalf("RunTask calls = %+v, want [%+v]", got, want)
	}
	if got := f.task().Build; got.Stage != StageRed || got.Base != f.base {
		t.Errorf("build = %+v, want red at the base %s", got, f.base)
	}
}

// TestRunPassesAtOnceThenReturnsWhenContextEnds: Run makes one pass at once and returns when its context
// ends.
func TestRunPassesAtOnceThenReturnsWhenContextEnds(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	f.evidence.onRun = cancel
	f.runner.Run(ctx)
	if got := len(f.evidence.runs); got != 1 {
		t.Errorf("RunTask calls = %d, want the one red run of the pass Run makes at once", got)
	}
}

// TestPassRedHoldingAtBaseIsANoteAndTheImplementerStarts: a red run whose conclusion holds at the base is a note on the
// task's build, not a climb: the build moves to implement with that run as its red run.
func TestPassRedHoldingAtBaseIsANoteAndTheImplementerStarts(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	f.pass()
	red := f.lastRun().id
	f.addRun(red, evidence.PhaseRed, f.base, 1, evidence.VerdictHolds)
	f.pass()
	task := f.task()
	note := taskLabel(task.Seq) + " red: the conclusion holds at the base (exit 1)"
	if task.Build.Stage != StageImplement || task.Build.RedRun != red || task.Build.Failure != "" ||
		!reflect.DeepEqual(task.Notes, []string{note}) {
		t.Errorf("build stage %q red run %q failure %q notes %q, want implement with red run %s and the note %q",
			task.Build.Stage, task.Build.RedRun, task.Build.Failure, task.Notes, red, note)
	}
	if got := f.pendingTriggers(); len(got) != 0 {
		t.Errorf("pending triggers = %v, want none", got)
	}
}

// TestPassRedFailingAtBaseSnapshotsRefsAndStartsImplementer: a red run that fails at the base snapshots the
// refs, without the epic's own branch, and starts the implementer under its derived id with its files and gate.
func TestPassRedFailingAtBaseSnapshotsRefsAndStartsImplementer(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	f.pass()
	red := f.lastRun()
	f.addRun(red.id, evidence.PhaseRed, f.base, 1, evidence.VerdictFails)
	f.pass()
	id := passStep(StageImplement, 1, 0)
	build := f.task().Build
	if build.Stage != StageImplement || build.Attempt != 1 || build.Step != id || build.RedRun != red.id {
		t.Errorf("build = %+v, want implement at attempt 1 with step %s and red run %s", build, id, red.id)
	}
	if build.Refs == "" || strings.Contains(build.Refs, BranchRef(passEpic)) {
		t.Errorf("Refs = %q, want a snapshot without %s", build.Refs, BranchRef(passEpic))
	}
	if len(f.steps.starts) != 1 {
		t.Fatalf("Start calls = %d, want 1", len(f.steps.starts))
	}
	start := f.lastStart()
	if start.ID != id || start.Agent != "claude-impl" || start.ReadOnly {
		t.Errorf("start ID %s, Agent %q, ReadOnly %t, want %s, claude-impl, writing", start.ID, start.Agent,
			start.ReadOnly, id)
	}
	if !slices.Equal(start.Owned, []string{"thing.go"}) {
		t.Errorf("start Owned = %v, want [thing.go]", start.Owned)
	}
	if start.CommandGate == nil || start.CommandGate.Name != "check" {
		t.Errorf("start CommandGate = %+v, want the check gate", start.CommandGate)
	}
	if want := ImplementWorktree(f.stateDir, passEpic, store.EpicTask{Seq: 1}); start.Workspace != want {
		t.Errorf("start Workspace = %s, want %s", start.Workspace, want)
	}
}

// TestPassRedRunOfChangedItemReadsMissing covers R3: a red run that ends passing at the base is kept only while its
// spec is current with the task's stored evidence item. Once the item changes, the run reads missing, so the build
// asks for the run again and stays at red, and no implementer starts.
func TestPassRedRunOfChangedItemReadsMissing(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	f.pass()
	red := f.lastRun()
	f.addRun(red.id, evidence.PhaseRed, f.base, 1, evidence.VerdictFails)
	item := f.task().Evidence
	item.Conclusion = "the thing is broken"
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatalf("open store file: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close store file: %v", err)
		}
	}()
	if _, err := db.ExecContext(f.ctx,
		"UPDATE epic_tasks SET evidence = ? WHERE epic = ? AND seq = 1", string(raw), passEpic); err != nil {
		t.Fatalf("UPDATE epic_tasks: %v", err)
	}
	f.pass()
	build := f.task().Build
	if build.Stage != StageRed || build.RedRun != "" {
		t.Errorf("build stage %q red run %q, want red with no red run", build.Stage, build.RedRun)
	}
	if len(f.steps.starts) != 0 {
		t.Errorf("Start calls = %d, want 0", len(f.steps.starts))
	}
}

// TestPassImplementerDoneStartsGreenAtItsCommit covers R4: an implementer that returns done with its commit
// moves the build to green at that commit, and the green run starts there.
func TestPassImplementerDoneStartsGreenAtItsCommit(t *testing.T) {
	t.Parallel()
	f := newImplementerFixture(t)
	commit := buildObject(t, f.repo, f.base)
	f.setStep(passStep(StageImplement, 1, 0), "done", `{"summary":"done","commit":"`+commit+`"}`)
	f.pass()
	f.expectGreenAt(commit)
}

// TestPassImplementerDoneWithoutCommitGreenAtWorktreeHead: an implementer return without a commit takes the
// HEAD of the implementer's worktree as the green revision.
func TestPassImplementerDoneWithoutCommitGreenAtWorktreeHead(t *testing.T) {
	t.Parallel()
	f := newImplementerFixture(t)
	id := passStep(StageImplement, 1, 0)
	head := buildCommit(t, ImplementWorktree(f.stateDir, passEpic, store.EpicTask{Seq: 1}))
	f.setStep(id, "done", `{"summary":"done"}`)
	f.pass()
	f.expectGreenAt(head)
}

// TestPassImplementerDoneWithoutCommitReadsHeadFromTheStepsWorkspace: an implementer step whose row records a worktree
// other than the task's implement worktree, as a step started by an earlier orreryd does, has its return without a
// commit taken at the HEAD of the worktree the row records.
func TestPassImplementerDoneWithoutCommitReadsHeadFromTheStepsWorkspace(t *testing.T) {
	t.Parallel()
	f := newImplementerFixture(t)
	id := passStep(StageImplement, 1, 0)
	elsewhere := f.addTaskWorktree(id)
	head := buildCommit(t, elsewhere)
	f.steps.rows[id] = store.StepRow{ID: id, End: "done", Result: `{"summary":"done"}`, Workspace: elsewhere}
	f.pass()
	f.expectGreenAt(head)
}

// TestPassImplementerMovingMainParksEpicWithTell covers R1 and R6: a move of refs/heads/main by the implementer parks
// the epic with the move as its reason, tells the owner, consumes the refs check, and starts no run while parked.
func TestPassImplementerMovingMainParksEpicWithTell(t *testing.T) {
	t.Parallel()
	f := newImplementerFixture(t)
	id := passStep(StageImplement, 1, 0)
	f.setStep(id, "done", `{"summary":"done"}`)
	moved := buildObject(t, f.repo, f.base)
	buildRun(t, f.repo, "update-ref", "refs/heads/main", moved)
	f.pass()
	reason := fmt.Sprintf("T1 implementer moved refs/heads/main (%s to %s)", f.base, moved)
	if got := f.epic().Parked; got != reason {
		t.Errorf("epic Parked = %q, want %q", got, reason)
	}
	if got := f.tells(); !slices.Contains(got, reason) {
		t.Errorf("tells = %v, want %q", got, reason)
	}
	if got := f.task().Build.Refs; got != "" {
		t.Errorf("build Refs = %q, want the refs check consumed", got)
	}
	if got := len(f.evidence.runs); got != 1 {
		t.Errorf("RunTask calls = %d, want only the red run while parked", got)
	}
}

// TestPassImplementerNonParkingRefMoveIsToldAndGreenStarts covers R6: a ref move that does not park is told to the
// owner, and the build goes on to green.
func TestPassImplementerNonParkingRefMoveIsToldAndGreenStarts(t *testing.T) {
	t.Parallel()
	f := newImplementerFixture(t)
	commit := buildObject(t, f.repo, f.base)
	detachStep(t, f, commit)
	buildRun(t, f.repo, "update-ref", "refs/heads/feature", commit)
	f.setStep(passStep(StageImplement, 1, 0), "done", `{"summary":"done","commit":"`+commit+`"}`)
	f.pass()
	told := slices.ContainsFunc(f.tells(), func(text string) bool {
		return strings.Contains(text, "T1 implementer moved refs/heads/feature") && strings.Contains(text, commit)
	})
	if !told {
		t.Errorf("tells = %v, want the move of refs/heads/feature to %s", f.tells(), commit)
	}
	if got := f.epic().Parked; got != "" {
		t.Errorf("epic Parked = %q, want the epic running", got)
	}
	f.expectGreenAt(commit)
}

// TestPassGreenPassedWithoutReviewGatesAcceptsUnreviewedAndPins covers R4: a green run that passes with no
// review gates accepts the task unreviewed, pins its revision under refs/orrery, and removes its worktrees.
func TestPassGreenPassedWithoutReviewGatesAcceptsUnreviewedAndPins(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	f.pass()
	f.setGreen(1, evidence.VerdictHolds)
	worktree := f.addTaskWorktree(passStep(StageImplement, 1, 0))
	f.pass()
	task := f.task()
	if task.Status != "done" || task.Build.Review != "unreviewed" {
		t.Errorf("task status %q review %q, want done and unreviewed", task.Status, task.Build.Review)
	}
	if got := buildRun(t, f.repo, "rev-parse", "refs/orrery/epic/T1"); got != f.base {
		t.Errorf("refs/orrery/epic/T1 = %s, want the accepted revision %s", got, f.base)
	}
	f.expectNoWorktrees(worktree)
}

// TestPassGreenPassedStartsReviewGate covers R4: a green run that passes with a review gate starts the gate's review
// step, read-only, under its derived id.
func TestPassGreenPassedStartsReviewGate(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t, "review")
	f.pass()
	f.setGreen(1, evidence.VerdictHolds)
	f.pass()
	if len(f.steps.starts) != 1 {
		t.Fatalf("Start calls = %d, want the review step", len(f.steps.starts))
	}
	start := f.lastStart()
	if start.ID != passStep(StageReview, 1, 0) || !start.ReadOnly ||
		start.AgentGate == nil || start.AgentGate.Name != "review" {
		t.Errorf("review start ID %s, ReadOnly %t, AgentGate %+v, want the review gate read-only under its id",
			start.ID, start.ReadOnly, start.AgentGate)
	}
	if build := f.task().Build; build.Stage != StageReview || build.GreenRun != passRunID(StageGreen, 1) {
		t.Errorf("build = %+v, want review with the green run kept", build)
	}
}

// TestPassReviewRunsEachTaskReviewerAsDeclared covers R3: a task's review steps are its enabled task-stage reviewers in
// merged order, and each step carries its reviewer's gate, a model reviewer's inline agent, and an agent reviewer's
// prompt heading the review prompt.
func TestPassReviewRunsEachTaskReviewerAsDeclared(t *testing.T) {
	t.Parallel()
	floor := reviewers.Reviewer{
		Name: "floor", Enabled: true, Stage: []string{reviewers.StageTask}, Model: "test-model",
		Tools: []string{"Read", "Grep"}, Effort: "high", Prompt: "Review the diff.", Limit: 900, Verify: true,
		Must: []string{"the diff is correct"}, MustNot: []string{"the tests were weakened"},
	}
	conformance := agentReviewer("conformance")
	conformance.Stage = []string{reviewers.StageEpic}
	disabled := agentReviewer("disabled")
	disabled.Enabled = false
	custom := reviewers.Reviewer{
		Name: "custom", Enabled: true, Stage: []string{reviewers.StageTask}, Agent: "custom-review",
		Prompt: "Check the migration.", Limit: 600, Verify: true, Must: []string{"the migration is reversible"},
	}
	f := newPassFixtureWith(t, []reviewers.Reviewer{floor, conformance, disabled, custom})
	f.pass()
	f.setGreen(1, evidence.VerdictHolds)
	f.pass()
	if len(f.steps.starts) != 1 {
		t.Fatalf("Start calls = %d, want the floor review step", len(f.steps.starts))
	}
	f.setStep(passStep(StageReview, 1, 0), "done", "")
	f.pass()
	if len(f.steps.starts) != 2 {
		t.Fatalf("Start calls = %d, want the floor and custom review steps", len(f.steps.starts))
	}

	epic, task := f.epic(), f.task()
	base, revision := task.Build.Base, task.Build.Revision
	first, second := f.steps.starts[0], f.steps.starts[1]
	if first.ID != passStep(StageReview, 1, 0) || second.ID != StepID(passEpic, 1, StageReview, 1, 0, 1, 0) {
		t.Fatalf("review step ids = %s, %s, want floor at position 0 then custom at position 1", first.ID, second.ID)
	}

	gate := first.AgentGate
	if !first.ReadOnly || gate == nil || gate.Name != "floor" || gate.Agent != "orrery-reviewer-floor" ||
		gate.Limit != 900*time.Second ||
		!slices.Equal(gate.Assertions, []gates.Assertion{
			{Text: "the diff is correct", Kind: gates.KindMust},
			{Text: "the tests were weakened", Kind: gates.KindMustNot},
		}) {
		t.Errorf("floor review ReadOnly %t, AgentGate %+v, want the floor gate read-only", first.ReadOnly, gate)
	}
	var inline map[string]struct {
		Model  string   `json:"model"`
		Tools  []string `json:"tools"`
		Effort string   `json:"effort"`
		Prompt string   `json:"prompt"`
	}
	if err := json.Unmarshal([]byte(first.Agents), &inline); err != nil {
		t.Fatalf("floor review Agents = %q, want a JSON object of agents: %v", first.Agents, err)
	}
	def := inline["orrery-reviewer-floor"]
	if def.Model != "test-model" || !slices.Equal(def.Tools, []string{"Read", "Grep", "StructuredOutput"}) ||
		def.Effort != "high" || def.Prompt != "Review the diff." {
		t.Errorf("floor review inline agent = %+v, want the floor's model, tools, effort and prompt", def)
	}
	if want := ReviewPrompt(epic, task, "floor", base, revision, store.StepCheck{}, nil); first.Prompt != want {
		t.Errorf("floor review Prompt = %q, want the review prompt %q", first.Prompt, want)
	}

	gate = second.AgentGate
	if gate == nil || gate.Name != "custom" || gate.Agent != "custom-review" || gate.Limit != 600*time.Second ||
		!slices.Equal(gate.Assertions, []gates.Assertion{{Text: "the migration is reversible", Kind: gates.KindMust}}) {
		t.Errorf("custom review AgentGate = %+v, want the custom gate", gate)
	}
	if second.Agents != "" {
		t.Errorf("custom review Agents = %s, want none for an agent reviewer", second.Agents)
	}
	want := "Check the migration.\n\n" + ReviewPrompt(epic, task, "custom", base, revision, store.StepCheck{}, nil)
	if second.Prompt != want {
		t.Errorf("custom review Prompt = %q, want %q", second.Prompt, want)
	}
}

// TestPassFailedReviewFindingStartsVerifier covers R4: a failed review stores its findings and starts the verifier of
// the first one, read-only, under its derived id.
func TestPassFailedReviewFindingStartsVerifier(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t, "review")
	f.pass()
	f.setGreen(1, evidence.VerdictHolds)
	f.setBuild(store.TaskBuild{
		Stage: StageReview, Base: f.base, Attempt: 1, Revision: f.base, GreenRun: passRunID(StageGreen, 1),
	})
	f.setStep(passStep(StageReview, 1, 0), "gate_failed", passFailedReview)
	f.pass()
	task := f.task()
	if len(task.Reviews) != 1 || task.Reviews[0].Verdict != verdictFails || len(task.Reviews[0].Findings) != 1 {
		t.Fatalf("reviews = %+v, want one failed review with one finding", task.Reviews)
	}
	if task.Build.Stage != StageVerify || task.Build.FindingPos != 0 {
		t.Errorf("build = %+v, want verify on finding 0", task.Build)
	}
	if len(f.steps.starts) != 1 {
		t.Fatalf("Start calls = %d, want the verifier", len(f.steps.starts))
	}
	start := f.lastStart()
	if start.ID != passStep(StageVerify, 1, 0) || start.Agent != "claude-verify" || !start.ReadOnly ||
		start.Schema != VerifierSchema || start.ReturnCheck == nil {
		t.Errorf("verifier start %+v, want claude-verify read-only under its id with the verifier schema and check",
			start)
	}
}

// TestPassDroppedFindingAcceptsReviewed covers R4: a finding the verifier drops is kept as dropped, and the
// task then accepts reviewed.
func TestPassDroppedFindingAcceptsReviewed(t *testing.T) {
	t.Parallel()
	f := newVerifyingFixture(t)
	f.setStep(passStep(StageVerify, 1, 0), "done",
		`{"results":[{"evidence":"the claim does not hold","result":"dropped"}]}`)
	f.pass()
	task := f.task()
	if task.Status != "done" || task.Build.Review != "reviewed" {
		t.Errorf("task status %q review %q, want done and reviewed", task.Status, task.Build.Review)
	}
	if len(task.Reviews) != 1 || len(task.Reviews[0].Findings) != 1 || task.Reviews[0].Findings[0].Result != "dropped" {
		t.Errorf("reviews = %+v, want the finding kept as dropped", task.Reviews)
	}
	if got := buildRun(t, f.repo, "rev-parse", "refs/orrery/epic/T1"); got != f.base {
		t.Errorf("refs/orrery/epic/T1 = %s, want %s", got, f.base)
	}
}

// TestPassConfirmedFindingSpendsAttemptAndStartsSecondImplementer: a confirmed finding spends attempt one,
// keeps its claim as the failure, and starts the implementer of attempt two fresh.
func TestPassConfirmedFindingSpendsAttemptAndStartsSecondImplementer(t *testing.T) {
	t.Parallel()
	f := newVerifyingFixture(t)
	f.setStep(passStep(StageVerify, 1, 0), "done",
		`{"results":[{"evidence":"the claim holds","result":"confirmed"}]}`)
	f.pass()
	const failure = "confirmed: F1 the thing is wrong"
	id := passStep(StageImplement, 2, 0)
	build := f.task().Build
	if build.Attempt != 2 || build.Stage != StageImplement || build.Failure != failure || build.Step != id {
		t.Errorf("build = %+v, want implement at attempt 2 with failure %q and step %s", build, failure, id)
	}
	if got := len(f.task().Reviews); got != 1 {
		t.Errorf("reviews = %d, want the review kept", got)
	}
	if len(f.steps.starts) != 1 || f.lastStart().ID != id {
		t.Errorf("Start calls = %d, want one start of %s", len(f.steps.starts), id)
	}
}

// TestPassSecondAttemptGreenNotMetClimbsTaskAndKeepsWorktrees: a green run that is not met at attempt two climbs the
// task to rung 3, the orchestrator's own attempt, with its reason, adds no trigger, and keeps its worktrees.
func TestPassSecondAttemptGreenNotMetClimbsTaskAndKeepsWorktrees(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	f.pass()
	f.setGreen(2, evidence.VerdictFails)
	worktree := f.addTaskWorktree(passStep(StageImplement, 2, 0))
	before := ladderClimbBefore(f, 2)
	f.writeRoles("planner = \"claude-planner\"\n")
	f.pass()
	const failure = "green not met: exit 0, verdict fails"
	if task := f.task(); task.Build.Failure != failure {
		t.Errorf("task failure %q, want %q", task.Build.Failure, failure)
	}
	ladderClimbCheck(f, store.RungOf(before), store.MovedByUnit)
	if got := f.pendingTriggers(); len(got) != 0 {
		t.Errorf("pending triggers = %v, want none: a climb to rung 3 adds no trigger", got)
	}
	ladderClimbKeepsWorktrees(f, worktree)
}

// TestPassStoppedStepPausesThenResumeRerunsUnderNewID covers R7: a stopped implementer pauses the epic and holds the
// build, and a resumed build reruns its unit under a new derived id.
func TestPassStoppedStepPausesThenResumeRerunsUnderNewID(t *testing.T) {
	t.Parallel()
	f := newImplementerFixture(t)
	f.setStep(passStep(StageImplement, 1, 0), "stopped", "")
	f.pass()
	if !f.epic().Paused || f.task().Build.Held != "stopped" {
		t.Fatalf("epic Paused = %t, build Held = %q, want paused with the build held stopped",
			f.epic().Paused, f.task().Build.Held)
	}
	if err := f.store.ResumeEpic(f.ctx, passEpic); err != nil {
		t.Fatalf("ResumeEpic: %v", err)
	}
	f.pass()
	second := passStep(StageImplement, 1, 1)
	if len(f.steps.starts) != 2 || f.lastStart().ID != second {
		t.Errorf("Start calls = %d, want a second start of %s", len(f.steps.starts), second)
	}
	if f.epic().Paused || f.task().Build.Held != "" {
		t.Errorf("after resume epic Paused and build Held = %t, %q, want running", f.epic().Paused, f.task().Build.Held)
	}
}

// TestPassEngineRestartedStepRerunsUnderNewID covers R7: a step the engine ended on a restart reruns under a new
// derived id.
func TestPassEngineRestartedStepRerunsUnderNewID(t *testing.T) {
	t.Parallel()
	f := newImplementerFixture(t)
	f.setStep(passStep(StageImplement, 1, 0), "engine_restarted", "")
	f.pass()
	second := passStep(StageImplement, 1, 1)
	if len(f.steps.starts) != 2 || f.lastStart().ID != second {
		t.Errorf("Start calls = %d, want a second start of %s", len(f.steps.starts), second)
	}
}

// TestPassExistingDerivedStepIsNotRestarted covers R7: when the build lost its record of a step the engine already
// holds under its derived id, the pass waits on that step and starts nothing.
func TestPassExistingDerivedStepIsNotRestarted(t *testing.T) {
	t.Parallel()
	f := newImplementerFixture(t)
	f.setStep(passStep(StageImplement, 1, 0), "", "")
	build := f.task().Build
	build.Step = ""
	f.setBuild(build)
	f.pass()
	if got := len(f.steps.starts); got != 1 {
		t.Errorf("Start calls = %d, want the one start made before the record was lost", got)
	}
}

// TestPassCapReachedChangesNothingAndRetriesNextPass covers F6: a step start the cap refuses changes nothing, and the
// next pass retries the same start.
func TestPassCapReachedChangesNothingAndRetriesNextPass(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	f.pass()
	f.addRun(f.lastRun().id, evidence.PhaseRed, f.base, 1, evidence.VerdictFails)
	f.steps.err = fmt.Errorf("start step: %w", enginehost.ErrCapReached)
	f.pass()
	before := f.task().Build
	if before.Stage != StageImplement || before.Step != "" {
		t.Fatalf("build = %+v, want implement with no step after the refused start", before)
	}
	f.pass()
	if got := f.task().Build; got != before {
		t.Errorf("build after the retry = %+v, want %+v unchanged", got, before)
	}
	id := passStep(StageImplement, 1, 0)
	if len(f.steps.starts) != 2 || f.steps.starts[0].ID != id || f.lastStart().ID != id {
		t.Errorf("Start calls = %d, want two starts of %s", len(f.steps.starts), id)
	}
}

// TestPassRoleErrorParksTaskHeldParked: a role file that names no implementer parks the task held
// parked with the role error as the reason, and starts nothing.
func TestPassRoleErrorParksTaskHeldParked(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	f.pass()
	f.addRun(f.lastRun().id, evidence.PhaseRed, f.base, 1, evidence.VerdictFails)
	if err := os.Remove(f.rolesPath); err != nil {
		t.Fatal(err)
	}
	f.pass()
	_, roleErr := roles.Agent(f.getenv, roleImplementer)
	if roleErr == nil {
		t.Fatal("roles.Agent with no role file = nil error, want one")
	}
	if got := f.task().Build.Held; got != HeldParked {
		t.Errorf("build Held = %q, want %q", got, HeldParked)
	}
	if got := f.epic().Parked; got != roleErr.Error() {
		t.Errorf("epic Parked = %q, want the role error %q", got, roleErr.Error())
	}
	if got := len(f.steps.starts); got != 0 {
		t.Errorf("Start calls = %d, want none", got)
	}
}

// TestPassRefusedRedStartClimbsTask: an evidence run the evidence refuses climbs a red unit with the refusal. The
// refusal repeats on each rung the pass reaches, so the task climbs rung by rung within the pass to the recarve rung,
// where it waits for the planner's change of approach, with the refusal as its failure.
func TestPassRefusedRedStartClimbsTask(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	f.evidence.err = fmt.Errorf("refused: %w", enginehost.ErrEvidenceRefused)
	ladderClimbBefore(f, 0)
	f.writeRoles("planner = \"claude-planner\"\n")
	f.pass()
	want := "red not captured: " + f.evidence.err.Error()
	if task := f.task(); task.Build.Failure != want {
		t.Errorf("task failure %q, want %q", task.Build.Failure, want)
	}
	ladderClimbCheck(f, store.OrchestratorRung, store.MovedByUnit)
}
