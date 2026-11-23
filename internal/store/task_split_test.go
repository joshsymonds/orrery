package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// splitReason is the planner's reason for the split actions of these tests.
const splitReason = "the change is too wide for one task"

// splitSeed creates climbEpic with task 1 on the approach rung, of kind kind. Task 1 owns index.go and store.go, and
// claims R1 and R2.
func splitSeed(t *testing.T, s *Store, kind string) {
	t.Helper()
	ctx := context.Background()
	ladderApproachOnRung5(t, s, nil)
	if _, err := s.fixtureExec(ctx, `UPDATE epic_tasks SET kind = ? WHERE epic = ? AND seq = 1`,
		kind, climbEpic); err != nil {
		t.Fatalf("set kind of task 1: %v", err)
	}
	ladderApproachOwn(t, s, 1, "index.go", "store.go")
	for i, requirement := range []string{"R1", "R2"} {
		if _, err := s.fixtureExec(ctx,
			`INSERT INTO epic_task_requirements (epic, task, position, requirement) VALUES (?, 1, ?, ?)`,
			climbEpic, i, requirement); err != nil {
			t.Fatalf("claim %s by task 1: %v", requirement, err)
		}
	}
}

// splitDescendants returns the split of task 1 the tests give: two descendants that divide index.go and store.go between
// them. The first claims R1 and contributes to R2, and the second claims R2. Each call returns fresh slices.
func splitDescendants() []SplitTask {
	return []SplitTask{
		{
			Title: "Index the store", Deliverable: "the store is indexed", Files: []string{"index.go"},
			TestChanges: []string{"index_test.go"}, Claims: []string{"R1"}, Contributes: []string{"R2"},
			Gate: "go test ./internal/index", Evidence: evidence.Item{
				Capture: []string{"go", "test", "./internal/index"}, Limit: 300, Conclusion: "the index holds",
			},
		},
		{
			Title: "Expose the store", Deliverable: "the store is exposed", Files: []string{"store.go"},
			Claims: []string{"R2"}, Gate: "go test ./internal/store", Evidence: evidence.Item{
				Capture: []string{"go", "test", "./internal/store"}, Limit: 300, Conclusion: "the store is exposed",
			},
		},
	}
}

// splitAction stores split as a split of task 1 of climbEpic, the pending action of the epic, and returns it.
func splitAction(t *testing.T, s *Store, split []SplitTask) PlannerAction {
	t.Helper()
	return storeActions(t, s, climbEpic, EpicActionInput{Kind: ActionSplit, Task: 1, Split: split, Reason: splitReason})[0]
}

// insertSplitStep stores an open step with id on task 1 of climbEpic, as the runner starts a unit on the task.
func insertSplitStep(t *testing.T, s *Store, id string) {
	t.Helper()
	if _, err := s.fixtureExec(context.Background(), `INSERT INTO steps (id, agent, prompt, output_schema, workspace,
		revision, worktree, done_value, read_only, owned, gate, gate_limit, started, checks, result, status, end_value,
		reason, ended, epic, task)
		VALUES (?, 'implementer', 'build the split', '', '', '', '', '', 0, 'null', 'null', 0, ?, '[]', '', '', '', '',
		0, ?, 'T1')`, id, closeAt().Unix(), climbEpic); err != nil {
		t.Fatalf("insert step %s: %v", id, err)
	}
}

// splitTasksOf returns the tasks of climbEpic, as Epic reads them.
func splitTasksOf(ctx context.Context, t *testing.T, s *Store) []EpicTask {
	t.Helper()
	epic, err := s.Epic(ctx, climbEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	return epic.Tasks
}

// splitRefusedAlone carries out action, which must be refused with refusal. The refusal is stored on the action, and the
// epic's tasks read back as they were, since a refused split writes nothing.
func splitRefusedAlone(t *testing.T, s *Store, action PlannerAction, refusal string) {
	t.Helper()
	ctx := context.Background()
	before := splitTasksOf(ctx, t, s)
	stop, err := s.CarryOutAction(ctx, action, closeAt())
	if err != nil || stop != nil {
		t.Fatalf("CarryOutAction = %v, %v; want no steps and no error", stop, err)
	}
	if state, got := ladderApproachState(t, s, action); state != actionRefused || got != refusal {
		t.Errorf("action %d state = %q refusal %q, want %q refused with %q",
			action.Position, state, got, actionRefused, refusal)
	}
	if after := splitTasksOf(ctx, t, s); !reflect.DeepEqual(after, before) {
		t.Errorf("tasks after the refused split = %+v, want %+v", after, before)
	}
}

// TestTaskSplitCreatesDescendantsAndEndsParent checks that a split of a task on the approach rung ends the task as split and
// inserts each descendant in one write: an open ordinary task with the next seq, planned on the action's turn and split
// from the task, with its title, deliverable, gate, evidence, files, test changes and contributions as given, and with no
// build. The task's open step is returned for the runner to stop, and the action is taken.
func TestTaskSplitCreatesDescendantsAndEndsParent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	splitSeed(t, s, "")
	insertSplitStep(t, s, "step-split")
	action := splitAction(t, s, splitDescendants())

	stop, err := s.CarryOutAction(ctx, action, closeAt())
	if err != nil || !reflect.DeepEqual(stop, []string{"step-split"}) {
		t.Fatalf("CarryOutAction = %v, %v; want the open step of task 1 and no error", stop, err)
	}
	if got := climbTaskOf(ctx, t, s, 1).Status; got != TaskSplit {
		t.Errorf("status of task 1 = %q, want %q", got, TaskSplit)
	}
	if state, refusal := ladderApproachState(t, s, action); state != actionTaken || refusal != "" {
		t.Errorf("action state = %q refusal %q, want taken", state, refusal)
	}
	for i, descendant := range splitDescendants() {
		seq := int64(i + 2)
		got := climbTaskOf(ctx, t, s, seq)
		checks := []struct {
			name      string
			got, want any
		}{
			{"Status", got.Status, taskOpen},
			{"Turn", got.Turn, action.Turn},
			{"Title", got.Title, descendant.Title},
			{"Deliverable", got.Deliverable, descendant.Deliverable},
			{"Gate", got.Gate, descendant.Gate},
			{"Evidence", got.Evidence, descendant.Evidence},
			{"Files", got.Files, descendant.Files},
			{"TestChanges", got.TestChanges, descendant.TestChanges},
			{"Contributes", got.Contributes, descendant.Contributes},
			{"Kind", got.Kind, ""},
			{"Revises", got.Revises, int64(0)},
			{"SplitFrom", got.SplitFrom, int64(1)},
			{"DependsOn", got.DependsOn, []int64(nil)},
			{"Build", got.Build, TaskBuild{}},
			{"Rungs", got.Rungs, []TaskRung(nil)},
		}
		for _, check := range checks {
			if !reflect.DeepEqual(check.got, check.want) {
				t.Errorf("task %d %s = %#v, want %#v", seq, check.name, check.got, check.want)
			}
		}
	}
}

// TestTaskSplitMovesClaims checks that a split moves the task's claims to its descendants: the task reads back with no
// Requirement, and each descendant holds the Requirements it claims.
func TestTaskSplitMovesClaims(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	splitSeed(t, s, "")
	action := splitAction(t, s, splitDescendants())
	if _, err := s.CarryOutAction(ctx, action, closeAt()); err != nil {
		t.Fatalf("CarryOutAction: %v", err)
	}

	checks := []struct {
		name      string
		got, want any
	}{
		{"task 1 Requirements", climbTaskOf(ctx, t, s, 1).Requirements, []string(nil)},
		{"task 2 Requirements", climbTaskOf(ctx, t, s, 2).Requirements, []string{"R1"}},
		{"task 3 Requirements", climbTaskOf(ctx, t, s, 3).Requirements, []string{"R2"}},
	}
	for _, check := range checks {
		if !reflect.DeepEqual(check.got, check.want) {
			t.Errorf("%s = %#v, want %#v", check.name, check.got, check.want)
		}
	}
}

// TestTaskSplitRefusesFilesNotAPartition checks that a split is refused, and writes nothing, when its descendants' files do
// not partition the task's files: a descendant with no files, a file in two descendants, a file named twice in one, and a
// descendant set whose files are not exactly the task's.
func TestTaskSplitRefusesFilesNotAPartition(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		files   [][]string
		refusal string
	}{
		{
			name:    "a descendant with no files",
			files:   [][]string{{"index.go", "store.go"}, nil},
			refusal: "descendant 2 of the split of task 1 has no files",
		},
		{
			name:    "a file in two descendants",
			files:   [][]string{{"index.go", "store.go"}, {"store.go"}},
			refusal: "file store.go is in descendants 1 and 2 of the split of task 1",
		},
		{
			name:    "a file named twice in one descendant",
			files:   [][]string{{"index.go", "index.go"}, {"store.go"}},
			refusal: "descendant 1 of the split of task 1 names index.go twice",
		},
		{
			name:    "a file the task does not own",
			files:   [][]string{{"index.go"}, {"store.go", "extra.go"}},
			refusal: "the files of the split of task 1 are not exactly its files",
		},
		{
			name:    "a file of the task that no descendant holds",
			files:   [][]string{{"index.go"}, {"new.go"}},
			refusal: "the files of the split of task 1 are not exactly its files",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := openEpicStore(t)
			splitSeed(t, s, "")
			split := splitDescendants()
			for i := range split {
				split[i].Files = c.files[i]
			}
			splitRefusedAlone(t, s, splitAction(t, s, split), c.refusal)
		})
	}
}

// TestTaskSplitRefusesClaimsNotAPartition checks that a split is refused, and writes nothing, when its descendants' claims do
// not partition the task's claims: a Requirement no descendant claims, a Requirement two claim entries name, and a
// descendant claim the task does not hold.
func TestTaskSplitRefusesClaimsNotAPartition(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		claims  [][]string
		refusal string
	}{
		{
			name:    "a requirement no descendant claims",
			claims:  [][]string{{"R1"}, nil},
			refusal: "requirement R2 of task 1 is claimed 0 times by the split, not exactly once",
		},
		{
			name:    "a requirement two descendants claim",
			claims:  [][]string{{"R1", "R2"}, {"R2"}},
			refusal: "requirement R2 of task 1 is claimed 2 times by the split, not exactly once",
		},
		{
			name:    "a descendant claim the task does not hold",
			claims:  [][]string{{"R1"}, {"R2", "R9"}},
			refusal: "descendant 2 of the split of task 1 claims R9, which the task does not",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := openEpicStore(t)
			splitSeed(t, s, "")
			split := splitDescendants()
			for i := range split {
				split[i].Claims = c.claims[i]
			}
			splitRefusedAlone(t, s, splitAction(t, s, split), c.refusal)
		})
	}
}

// TestTaskSplitRefusesOneDescendant checks that a split with fewer than two descendants is refused, and writes nothing.
func TestTaskSplitRefusesOneDescendant(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	splitSeed(t, s, "")
	action := splitAction(t, s, splitDescendants()[:1])
	splitRefusedAlone(t, s, action, "the split of task 1 of epic "+climbEpic+" has fewer than two descendants")
}

// TestTaskSplitRefusesDescendantSplit checks that a split of a task that is itself a descendant of a split is refused, and
// writes nothing. The task is on the approach rung with its files and claims, so only its SplitFrom refuses the split.
func TestTaskSplitRefusesDescendantSplit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	splitSeed(t, s, "")
	if err := s.fixtureQueries().SetEpicTaskSplitFrom(ctx, sqlcgen.SetEpicTaskSplitFromParams{
		SplitFrom: 9, Epic: climbEpic, Seq: 1,
	}); err != nil {
		t.Fatalf("set split from of task 1: %v", err)
	}
	action := splitAction(t, s, splitDescendants())
	splitRefusedAlone(t, s, action, "task 1 of epic "+climbEpic+" is a descendant of a split and may not split")
}

// TestTaskSplitRefusesTaskNotAwaitingApproach checks that a split is refused, and writes nothing, for a task that is not
// awaiting approach: a task on rung 2, a task whose change of approach stands, a task that is not open, and a task that does
// not exist.
func TestTaskSplitRefusesTaskNotAwaitingApproach(t *testing.T) {
	t.Parallel()
	t.Run("a task on rung 2", func(t *testing.T) {
		t.Parallel()
		s := openEpicStore(t)
		from := climbFrom()
		from.Attempt = 2
		seedClimbTask(t, s, from)
		action := splitAction(t, s, splitDescendants())
		splitRefusedAlone(t, s, action, "task 1 of epic "+climbEpic+" is on rung 2, not rung 4")
	})
	t.Run("a change of approach that stands", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := openEpicStore(t)
		splitSeed(t, s, "")
		pending := storeActions(t, s, climbEpic,
			EpicActionInput{
				Kind:        ActionChangeApproach,
				Task:        1,
				Deliverable: ladderApproachDeliverable,
				Reason:      ladderApproachReason,
			},
			EpicActionInput{Kind: ActionSplit, Task: 1, Split: splitDescendants(), Reason: splitReason},
		)
		if _, err := s.CarryOutAction(ctx, pending[0], closeAt()); err != nil {
			t.Fatalf("change approach of task 1: %v", err)
		}
		splitRefusedAlone(t, s, pending[1], "task 1 of epic "+climbEpic+" already has a change of approach")
	})
	t.Run("a task that is not open", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := openEpicStore(t)
		splitSeed(t, s, "")
		if _, err := s.fixtureExec(ctx, `UPDATE epic_tasks SET status = 'done' WHERE epic = ? AND seq = 1`,
			climbEpic); err != nil {
			t.Fatalf("set status of task 1: %v", err)
		}
		splitRefusedAlone(t, s, splitAction(t, s, splitDescendants()), "task 1 of epic "+climbEpic+" is not open")
	})
	t.Run("a task that does not exist", func(t *testing.T) {
		t.Parallel()
		s := openEpicStore(t)
		splitSeed(t, s, "")
		action := storeActions(t, s, climbEpic, EpicActionInput{
			Kind: ActionSplit, Task: 2, Split: splitDescendants(), Reason: splitReason,
		})[0]
		splitRefusedAlone(t, s, action, "task 2 of epic "+climbEpic+" does not exist")
	})
}

// TestTaskSplitRewiresDependents checks that a task whose depends_on names the split task depends on the descendants in its
// place, in seq order, with no entry repeated, and that a task which names no split task keeps its dependencies.
func TestTaskSplitRewiresDependents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	splitSeed(t, s, "")
	dependents := []struct {
		seq       int64
		dependsOn string
	}{
		{seq: 2, dependsOn: "1"},
		{seq: 3, dependsOn: "1,2,1"},
		{seq: 4, dependsOn: ""},
	}
	for _, dependent := range dependents {
		if _, err := s.fixtureExec(
			ctx,
			`INSERT INTO epic_tasks (epic, seq, turn, title, deliverable, status, depends_on)
			VALUES (?, ?, 1, 'Dependent', 'the dependent change', 'open', ?)`,
			climbEpic,
			dependent.seq,
			dependent.dependsOn,
		); err != nil {
			t.Fatalf("insert task %d: %v", dependent.seq, err)
		}
	}
	action := splitAction(t, s, splitDescendants())
	if _, err := s.CarryOutAction(ctx, action, closeAt()); err != nil {
		t.Fatalf("CarryOutAction: %v", err)
	}

	// The descendants of task 1 are 5 and 6, after the four tasks above.
	want := []struct {
		seq       int64
		dependsOn []int64
	}{
		{seq: 2, dependsOn: []int64{5, 6}},
		{seq: 3, dependsOn: []int64{5, 6, 2}},
		{seq: 4, dependsOn: nil},
	}
	for _, check := range want {
		if got := climbTaskOf(ctx, t, s, check.seq).DependsOn; !reflect.DeepEqual(got, check.dependsOn) {
			t.Errorf("task %d DependsOn = %v, want %v", check.seq, got, check.dependsOn)
		}
	}
	if got := climbTaskOf(ctx, t, s, 5).SplitFrom; got != 1 {
		t.Errorf("task 5 SplitFrom = %d, want 1", got)
	}
}

// TestTaskSplitRefusesActionsOnSplitTask checks that send_back, fail_task, change_approach and reopen_design on a split task
// are refused, and write nothing. The task is a design task, so reopen_design reaches its status.
func TestTaskSplitRefusesActionsOnSplitTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	splitSeed(t, s, TaskKindDesign)
	const reason = "the split is wrong"
	pending := storeActions(t, s, climbEpic,
		EpicActionInput{Kind: ActionSplit, Task: 1, Split: splitDescendants(), Reason: splitReason},
		EpicActionInput{Kind: ActionSendBack, Task: 1, Reason: reason},
		EpicActionInput{Kind: ActionFailTask, Task: 1, Reason: reason},
		EpicActionInput{
			Kind: ActionChangeApproach, Task: 1, Deliverable: ladderApproachDeliverable, Reason: reason,
		},
		EpicActionInput{Kind: ActionReopenDesign, Task: 1, Deliverable: reopenDeliverable, Reason: reason},
	)
	if _, err := s.CarryOutAction(ctx, pending[0], closeAt()); err != nil {
		t.Fatalf("split of task 1: %v", err)
	}
	if got := climbTaskOf(ctx, t, s, 1).Status; got != TaskSplit {
		t.Fatalf("status of task 1 = %q, want %q", got, TaskSplit)
	}
	const split = "task 1 of epic " + climbEpic + " was split into its descendants"
	const notOpen = "task 1 of epic " + climbEpic + " is not open"
	refusals := []string{split, notOpen, notOpen, split}
	for i, refusal := range refusals {
		splitRefusedAlone(t, s, pending[i+1], refusal)
	}
}

// splitRecord is what a split keeps of a task: its seq, its status, the task it was split from and its Requirements.
type splitRecord struct {
	Seq          int64
	Status       string
	SplitFrom    int64
	Requirements []string
}

// splitRecordsOf returns the split record of each task of climbEpic, as Epic reads them.
func splitRecordsOf(ctx context.Context, t *testing.T, s *Store) []splitRecord {
	t.Helper()
	return mapInOrder(splitTasksOf(ctx, t, s), func(task EpicTask) splitRecord {
		return splitRecord{
			Seq:          task.Seq,
			Status:       task.Status,
			SplitFrom:    task.SplitFrom,
			Requirements: task.Requirements,
		}
	})
}

// TestTaskSplitSurvivesReopen checks that a split reads back unchanged after the store is closed and reopened over the same
// file: each task's status, its SplitFrom and its claims.
func TestTaskSplitSurvivesReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedMigrated(t, path)
	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	splitSeed(t, s, "")
	action := splitAction(t, s, splitDescendants())
	if _, err = s.CarryOutAction(ctx, action, closeAt()); err != nil {
		t.Fatalf("CarryOutAction: %v", err)
	}
	before := splitRecordsOf(ctx, t, s)
	want := []splitRecord{
		{Seq: 1, Status: TaskSplit, SplitFrom: 0, Requirements: nil},
		{Seq: 2, Status: taskOpen, SplitFrom: 1, Requirements: []string{"R1"}},
		{Seq: 3, Status: taskOpen, SplitFrom: 1, Requirements: []string{"R2"}},
	}
	if !reflect.DeepEqual(before, want) {
		t.Fatalf("records before the reopen = %+v, want %+v", before, want)
	}
	if err = s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open after close: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close after reopen: %v", err)
		}
	})
	if after := splitRecordsOf(ctx, t, reopened); !reflect.DeepEqual(after, before) {
		t.Errorf("records after the reopen = %+v, want %+v", after, before)
	}
}
