package build

import (
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// The invented values the gambit tests use: the deliverable and reason of a change of approach, the file a recarve or a
// re-brief adds, the file a recarve removes, and the first line of each failure a re-brief answers.
const (
	ladderGambitDeliverable = "The thing holds through b.go."
	ladderGambitReason      = "the approach needs the second file"
	ladderGambitFile        = "b.go"
	ladderGambitRemoved     = "thing.go"
	ladderGambitFirst       = "the spec does not say which field"
	ladderGambitSecond      = "the spec names a field twice"
)

// ladderGambitTasks returns a second task of the epic that owns file, as a planner's turn plans it.
func ladderGambitTasks(file string) []store.EpicTaskInput {
	return []store.EpicTaskInput{{
		Title: "Build the other thing", Deliverable: "The other thing holds", Files: []string{file},
		Requirements: []string{"R1"}, Gate: "check",
		Evidence: evidence.Item{
			Capture: []string{"scripts/check"}, Limit: 60, Conclusion: "the other holds", Judge: "the judge",
		},
	}}
}

// ladderGambitTurn claims a planner turn on the epic of f, and completes it with plan.
func ladderGambitTurn(t *testing.T, f *passFixture, plan store.EpicPlan) {
	t.Helper()
	turn, err := f.store.ClaimTurn(f.ctx, passEpic, passClock())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = f.store.CompleteTurn(f.ctx, passEpic, turn.Seq, plan, passClock()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
}

// ladderGambitToRecarve climbs task 1 of f from its first attempt through its second and the orchestrator's own attempt
// on rung 3, and fails that attempt onto rung 4, the change of approach. The orchestrator's attempt runs the
// orchestrator's agent. Rung 4 starts no step until the planner's change of approach is stored.
func ladderGambitToRecarve(t *testing.T, f *passFixture) {
	t.Helper()
	redRun := passRunID(StageRed, 1)
	f.addRun(redRun, evidence.PhaseRed, f.base, 1, evidence.VerdictFails)
	f.setBuild(store.TaskBuild{Stage: StageImplement, Base: f.base, Attempt: 1, Revision: f.base, RedRun: redRun})
	ladderClimbExecute(f, Action{Kind: ActFail, Reason: "returned blocked: first cause"})
	ladderClimbCheck(f, 1, store.MovedByUnit)
	ladderClimbExecute(f, Action{Kind: ActFail, Reason: "returned blocked: second cause"})
	ladderClimbCheck(f, 2, store.MovedByUnit)

	f.writeRoles("orchestrator = \"" + orchAgent + "\"\nplanner = \"" + plannerAgent + "\"\n")
	f.pass()
	if got := f.lastStart(); got.ID != passStep(StageImplement, store.OrchestratorRung, 0) || got.Agent != orchAgent {
		t.Fatalf("last start %s under %q, want the orchestrator's own attempt on rung %d under %q",
			got.ID, got.Agent, store.OrchestratorRung, orchAgent)
	}
	ladderClimbExecute(f, Action{Kind: ActFail, Reason: "returned blocked: third cause"})
	ladderClimbCheck(f, store.OrchestratorRung, store.MovedByUnit)

	started := len(f.steps.starts)
	f.pass()
	if got := len(f.steps.starts); got != started {
		t.Fatalf("steps started = %d while awaiting the change of approach, want none", got-started)
	}
	if build := f.task().Build; build.Attempt != store.ApproachRung || build.Step != "" {
		t.Fatalf("build %+v, want rung %d with no step, awaiting the planner", build, store.ApproachRung)
	}
}

// ladderGambitApproach returns the change_approach action of task 1 that sets the deliverable, adds files and removes
// remove.
func ladderGambitApproach(files, remove []string) store.EpicActionInput {
	return store.EpicActionInput{
		Kind: store.ActionChangeApproach, Task: 1, Deliverable: ladderGambitDeliverable,
		Files: files, Remove: remove, Reason: ladderGambitReason,
	}
}

// ladderGambitCarryApproach carries out the pending change_approach action of f's epic, as the engine does after the
// planner's turn that returned it.
func ladderGambitCarryApproach(t *testing.T, f *passFixture) {
	t.Helper()
	pending, err := f.store.PendingActions(f.ctx)
	if err != nil {
		t.Fatalf("PendingActions: %v", err)
	}
	var action *store.PlannerAction
	for i := range pending {
		if pending[i].Epic == passEpic && pending[i].Kind == store.ActionChangeApproach {
			action = &pending[i]
		}
	}
	if action == nil {
		t.Fatal("no pending change_approach action after the planner's turn")
	}
	if _, err = f.store.CarryOutAction(f.ctx, *action, passClock()); err != nil {
		t.Fatalf("CarryOutAction: %v", err)
	}
}

// ladderGambitRecarve has the planner return one change_approach for task 1 of f that sets the deliverable, adds files
// and removes remove, and carries that action out. The turn plans no task.
func ladderGambitRecarve(t *testing.T, f *passFixture, files, remove []string) {
	t.Helper()
	ladderGambitTurn(t, f, store.EpicPlan{
		Outline: "The change of approach", Outcome: "work",
		Actions: []store.EpicActionInput{ladderGambitApproach(files, remove)},
	})
	ladderGambitCarryApproach(t, f)
}

// ladderGambitRefusal returns the decision the epic recorded for a refused change of approach, or fails the test when
// there is none.
func ladderGambitRefusal(t *testing.T, f *passFixture) string {
	t.Helper()
	for _, decision := range f.epic().Decisions {
		if strings.HasSuffix(decision.ID, "-refused") && strings.Contains(decision.Text, "change approach") {
			return decision.Text
		}
	}
	t.Fatalf("decisions = %+v, want a refusal of the change of approach", f.epic().Decisions)
	return ""
}

// ladderGambitReBrief returns the orchestrator's return of a re-brief that adds file and removes nothing.
func ladderGambitReBrief(t *testing.T, file string) string {
	t.Helper()
	return orchReturn(t, map[string]any{
		"decision": "re_brief", "version": versionWith(map[string]any{
			"changed": []string{"files"}, "files": []string{file},
		}),
	})
}

// TestLadderGambitClimbsThroughRecarveToGap: a task fails its two attempts, its orchestrator's own attempt on rung 3, then
// waits for the planner's recarve on rung 4. The recarve adds a file and removes one, and the task runs two attempts under
// it, on rungs 4 and 5, before a failure on rung 5 ends it as a gap.
func TestLadderGambitClimbsThroughRecarveToGap(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	ladderGambitToRecarve(t, f)
	ladderGambitRecarve(t, f, []string{ladderGambitFile}, []string{ladderGambitRemoved})

	f.pass()
	if got := f.lastStart().ID; got != passStep(StageImplement, store.ApproachRung, 0) {
		t.Fatalf("last start %s, want the implementer on rung %d under the recarve", got, store.ApproachRung)
	}
	if files := f.task().Files; !slices.Equal(files, []string{ladderGambitFile}) {
		t.Errorf("task files = %q, want the recarve's file alone", files)
	}

	f.setStep(passStep(StageImplement, store.ApproachRung, 0), endChecksFailed, "")
	f.pass()
	ladderClimbCheck(f, store.ApproachRung, store.MovedByUnit)
	f.pass()
	if got := f.lastStart().ID; got != passStep(StageImplement, store.LastRung, 0) {
		t.Fatalf("last start %s, want the second attempt of the recarve on rung %d", got, store.LastRung)
	}

	f.setStep(passStep(StageImplement, store.LastRung, 0), endChecksFailed, "")
	f.pass()
	if status := f.task().Status; status != store.TaskGap {
		t.Fatalf("task status = %q, want %q", status, store.TaskGap)
	}
	byRung := map[int64]store.TaskRung{}
	for _, rung := range f.task().Rungs {
		byRung[rung.Rung] = rung
	}
	if rung := byRung[store.GapRung]; rung.Kind != store.RungGap || rung.Outcome != store.RungGapped {
		t.Errorf("rung %d = %+v, want a gap rung that ended gapped", store.GapRung, rung)
	}
	if rung := byRung[store.ApproachRung]; rung.Kind != store.RungApproach || rung.MovedBy != store.MovedByUnit {
		t.Errorf("rung %d = %+v, want the recarve rung moved by the unit", store.ApproachRung, rung)
	}
}

// TestLadderGambitReBriefReruns: an orchestrator's re-brief on a request reruns the implementer on the task's rung with
// no attempt spent, and its files join the task's files.
func TestLadderGambitReBriefReruns(t *testing.T) {
	t.Parallel()
	f := newRequestFixture(t)
	f.endOrchestrator(orchID(0, 0), ladderGambitReBrief(t, "c.go"))
	f.pass()
	if build := f.task().Build; build.Stage != StageImplement || build.Rerun != 1 || build.Attempt != 1 {
		t.Fatalf("build = %+v, want implement at attempt 1 and rerun 1, with no attempt spent", build)
	}
	if files := f.task().Files; !slices.Equal(files, []string{"thing.go", "c.go"}) {
		t.Fatalf("task files = %q, want the re-brief's file added", files)
	}
	if got := f.lastStart().ID; got != passStep(StageImplement, 1, 1) {
		t.Errorf("last start %s, want the implementer at rerun 1 of attempt 1", got)
	}
}

// TestLadderGambitReBriefRefusedForASignatureAlreadyAnswered: a re-brief on a failure whose first line an earlier re-brief
// answered is refused, and the request climbs the task off that failure as an ordinary failure. A re-brief on another
// first line is allowed.
func TestLadderGambitReBriefRefusedForASignatureAlreadyAnswered(t *testing.T) {
	t.Parallel()
	t.Run("the same first line is refused", func(t *testing.T) {
		t.Parallel()
		f := newRequestFixture(t)
		f.endOrchestrator(orchID(0, 0), ladderGambitReBrief(t, "c.go"))
		f.pass()
		f.returnRequest(1, "blocked", ladderGambitFirst)
		f.pass()
		f.endOrchestrator(orchID(1, 1), ladderGambitReBrief(t, "d.go"))
		f.pass()
		build := f.task().Build
		if build.Attempt != 2 || build.Stage != StageImplement {
			t.Fatalf("build = %+v, want the task climbed to rung 2 after the refused re-brief", build)
		}
		if !strings.Contains(build.Failure, "already re-briefed") {
			t.Errorf("failure %q, want the refused re-brief recorded", build.Failure)
		}
	})
	t.Run("another first line is allowed", func(t *testing.T) {
		t.Parallel()
		f := newRequestFixture(t)
		f.endOrchestrator(orchID(0, 0), ladderGambitReBrief(t, "c.go"))
		f.pass()
		f.returnRequest(1, "blocked", ladderGambitSecond)
		f.pass()
		f.endOrchestrator(orchID(1, 1), ladderGambitReBrief(t, "d.go"))
		f.pass()
		if build := f.task().Build; build.Attempt != 1 || build.Rerun != 2 {
			t.Fatalf("build = %+v, want the second re-brief allowed at attempt 1, rerun 2", build)
		}
	})
}

// TestLadderGambitCannotMeetGoesToRecarve: an orchestrator's cannot_meet on rung 1 climbs the task straight to rung 4, the
// recarve, moved by cannot_meet, and starts no step there until the planner's change of approach is stored.
func TestLadderGambitCannotMeetGoesToRecarve(t *testing.T) {
	t.Parallel()
	f := newRequestFixture(t)
	f.endOrchestrator(orchID(0, 0), orchReturn(t, map[string]any{
		"decision": "cannot_meet", "failure": "the spec asks for two fields that cannot both hold",
	}))
	f.pass()
	build := f.task().Build
	if build.Attempt != store.ApproachRung || build.Stage != StageImplement || build.Step != "" {
		t.Fatalf("build = %+v, want rung %d at implement with no step, awaiting the recarve", build, store.ApproachRung)
	}
	var moved bool
	for _, rung := range f.task().Rungs {
		moved = moved || rung.Rung == store.ApproachRung && rung.Kind == store.RungApproach &&
			rung.MovedBy == store.MovedByCannotMeet
	}
	if !moved {
		t.Errorf("rungs = %+v, want rung %d an approach rung moved by cannot_meet", f.task().Rungs, store.ApproachRung)
	}
}

// TestLadderGambitRecarveRefusesAHeldFile: a recarve that adds a file an open task owns, or an accepted task has not
// integrated, is refused naming that task, and changes neither the task's files nor its rung. The planner's turn plans
// the holder and the recarve together, so the holder exists when the recarve is carried out.
func TestLadderGambitRecarveRefusesAHeldFile(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		accept bool
		holder string
	}{
		{"an open task owns the file", false, "owned by open T2"},
		{"an accepted task has not integrated the file", true, "owned by accepted T2, not yet integrated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newPassFixture(t)
			ladderGambitToRecarve(t, f)
			ladderGambitTurn(t, f, store.EpicPlan{
				Tasks: ladderGambitTasks(ladderGambitFile), Outcome: "work",
				Actions: []store.EpicActionInput{ladderGambitApproach([]string{ladderGambitFile}, nil)},
			})
			if tc.accept {
				other := f.epic().Tasks[1]
				if err := f.store.AcceptTask(f.ctx, passEpic, other.Seq, other.Build, "reviewed"); err != nil {
					t.Fatalf("AcceptTask: %v", err)
				}
			}
			ladderGambitCarryApproach(t, f)
			if text := ladderGambitRefusal(t, f); !strings.Contains(text, tc.holder) {
				t.Errorf("refusal %q, want it to name the holder %q", text, tc.holder)
			}
			if files := f.task().Files; slices.Contains(files, ladderGambitFile) {
				t.Errorf("task files = %q, want the held file not granted", files)
			}
			if build := f.task().Build; build.Attempt != store.ApproachRung {
				t.Errorf("build attempt = %d, want the task still on rung %d", build.Attempt, store.ApproachRung)
			}
		})
	}
}

// ladderGambitReBriefFiles returns the orchestrator's return of a re-brief whose version adds add and removes remove.
func ladderGambitReBriefFiles(t *testing.T, add, remove []string) string {
	t.Helper()
	return orchReturn(t, map[string]any{
		"decision": "re_brief", "version": versionWith(map[string]any{
			"changed": []string{"files"}, "files": add, "remove": remove,
		}),
	})
}

// TestLadderGambitReBriefRemovesFiles: a re-brief drops a file the task owns and adds another, and reruns the implementer
// on the same rung with no attempt spent, the task then holding the files its version leaves it.
func TestLadderGambitReBriefRemovesFiles(t *testing.T) {
	t.Parallel()
	f := newRequestFixture(t)
	f.endOrchestrator(orchID(0, 0), ladderGambitReBriefFiles(t, []string{"c.go"}, []string{"thing.go"}))
	f.pass()
	if build := f.task().Build; build.Stage != StageImplement || build.Rerun != 1 || build.Attempt != 1 {
		t.Fatalf("build = %+v, want implement at attempt 1 and rerun 1, with no attempt spent", build)
	}
	if files := f.task().Files; !slices.Equal(files, []string{"c.go"}) {
		t.Errorf("task files = %q, want the re-brief's file alone", files)
	}
}

// TestLadderGambitReBriefRefusesAnUnownedOrLastFileRemoval: a re-brief that removes a file the task does not own, or
// that would leave the task no files, is refused, and the request climbs the task as an ordinary failure with the
// refusal in its failure. The task's files do not change.
func TestLadderGambitReBriefRefusesAnUnownedOrLastFileRemoval(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		add    []string
		remove []string
		refuse string
	}{
		{"a file the task does not own", nil, []string{"nope.go"}, "version.remove[0]: not one of the task's files"},
		{"the last file of the task", nil, []string{"thing.go"}, "version.remove: the task would be left no files"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newRequestFixture(t)
			f.endOrchestrator(orchID(0, 0), ladderGambitReBriefFiles(t, tc.add, tc.remove))
			f.pass()
			build := f.task().Build
			if build.Attempt != 2 || build.Stage != StageImplement {
				t.Fatalf("build = %+v, want the task climbed to rung 2 after the refused re-brief", build)
			}
			if !strings.Contains(build.Failure, tc.refuse) {
				t.Errorf("failure %q, want the refusal %q", build.Failure, tc.refuse)
			}
			if files := f.task().Files; !slices.Equal(files, []string{"thing.go"}) {
				t.Errorf("task files = %q, want them unchanged", files)
			}
		})
	}
}

// TestLadderGambitReBriefChangingTheEvidenceRunsRed: a re-brief that changes the evidence stores its version checking and
// moves the build to red, as a rerun that changes the evidence does, keeping its attempt.
func TestLadderGambitReBriefChangingTheEvidenceRunsRed(t *testing.T) {
	t.Parallel()
	f := newRequestFixture(t)
	f.endOrchestrator(orchID(0, 0), orchReturn(t, map[string]any{
		"decision": "re_brief", "version": versionWith(map[string]any{
			"changed": []string{"evidence"}, "evidence": redCheckItem(),
		}),
	}))
	f.pass()
	task := f.task()
	if task.Build.Stage != StageRed || task.Build.Attempt != 1 || task.Build.Rerun != 1 {
		t.Fatalf("build stage %q attempt %d rerun %d, want red at attempt 1 and rerun 1",
			task.Build.Stage, task.Build.Attempt, task.Build.Rerun)
	}
	if _, ok := task.Checking(); !ok {
		t.Errorf("Checking() = false, want the re-brief's version checking")
	}
}
