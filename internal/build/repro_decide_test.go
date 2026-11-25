package build

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// reproRevision is the implementer's commit a test item's reproduction runs its tests at, and reproBase the base red
// was judged fails at.
const (
	reproRevision = "commit-rev"
	reproBase     = "repro-base"
)

// TestDecideGreenReproduction table-drives Decide for the green unit of a task whose item is a test item: the build
// asks for the reproduction at the commit, waits for it, reruns one lost at a restart, notes one that did not run,
// starts the judge of one that exited 0, and starts the green run when its exit is what red predicts. A task of
// another kind starts the green run with no reproduction.
func TestDecideGreenReproduction(t *testing.T) {
	t.Parallel()
	reproTestItem := evidence.Item{Kind: evidence.KindTest, Tests: []string{"feature_test.go"}}
	build := store.TaskBuild{
		Stage: StageGreen, Base: reproBase, Attempt: 1, Rerun: 2, Revision: reproRevision, RedRun: "red-run",
	}
	lostRerun := build
	lostRerun.Rerun = 3
	secondAttempt := store.TaskBuild{
		Stage: StageGreen, Base: reproBase, Attempt: 2, Rerun: 2, Revision: reproRevision, RedRun: "red-run",
	}
	ended := func(exit int, timedOut bool, notRun string) *store.NodeCheck {
		return &store.NodeCheck{
			ID: RunID(decideEpic, 3, unitRepro, 1, 2), State: store.NodeEnded,
			Exit: exit, TimedOut: timedOut, NotRun: notRun,
		}
	}
	reproTask := store.EpicTask{Seq: 3, Build: build, Evidence: reproTestItem}
	startGreen := Action{
		Kind: ActStartRun, Phase: StageGreen, ID: RunID(decideEpic, 3, StageGreen, 1, 2), Revision: reproRevision,
	}
	tests := []struct {
		name string
		view View
		want Action
	}{
		{
			name: "no reproduction asks for it at the commit",
			view: View{Epic: decideEpic, Task: reproTask},
			want: Action{
				Kind: ActRepro, ID: RunID(decideEpic, 3, unitRepro, 1, 2), Revision: reproRevision,
			},
		},
		{
			name: "no reproduction while paused waits",
			view: View{Epic: decideEpic, Paused: true, Task: reproTask},
			want: Action{Kind: ActNone},
		},
		{
			name: "no reproduction while parked waits",
			view: View{Epic: decideEpic, Parked: true, Task: reproTask},
			want: Action{Kind: ActNone},
		},
		{
			name: "reproduction not ended waits",
			view: View{
				Epic: decideEpic,
				Repro: &store.NodeCheck{
					ID: RunID(decideEpic, 3, unitRepro, 1, 2), State: store.NodeRunning,
				},
				Task: reproTask,
			},
			want: Action{Kind: ActNone},
		},
		{
			name: "reproduction lost at a restart reruns the unit",
			view: View{
				Epic: decideEpic, Repro: ended(0, false, store.LostAtRestart), Task: reproTask,
			},
			want: Action{Kind: ActMove, Next: lostRerun},
		},
		{
			name: "reproduction not run is a note on the first attempt",
			view: View{
				Epic: decideEpic, Repro: ended(0, false, "no workdir"), Task: reproTask,
			},
			want: Action{
				Kind: ActMove, Next: build,
				Notes: []string{"T3 attempt 1 reproduction: did not run: no workdir"},
				Tells: []string{"T3 attempt 1 reproduction: did not run: no workdir"},
			},
		},
		{
			name: "reproduction not run on the second attempt is a note",
			view: View{
				Epic: decideEpic, Repro: ended(0, false, "no workdir"),
				Task: store.EpicTask{Seq: 3, Evidence: reproTestItem, Build: secondAttempt},
			},
			want: Action{
				Kind: ActMove, Next: secondAttempt,
				Notes: []string{"T3 attempt 2 reproduction: did not run: no workdir"},
				Tells: []string{"T3 attempt 2 reproduction: did not run: no workdir"},
			},
		},
		{
			name: "reproduction exit 0 starts its judge, not a note",
			view: View{
				Epic: decideEpic, Repro: ended(0, false, ""), Task: reproTask,
			},
			want: Action{Kind: ActStartStep, Unit: unitReproJudge, ID: reproJudgeID(decideEpic, 3, build, 0)},
		},
		{
			name: "reproduction exit 0 on the second attempt starts the judge of that attempt",
			view: View{
				Epic: decideEpic, Repro: ended(0, false, ""),
				Task: store.EpicTask{Seq: 3, Evidence: reproTestItem, Build: secondAttempt},
			},
			want: Action{Kind: ActStartStep, Unit: unitReproJudge, ID: reproJudgeID(decideEpic, 3, secondAttempt, 0)},
		},
		{
			name: "reproduction exit 1 starts the green run",
			view: View{Epic: decideEpic, Repro: ended(1, false, ""), Task: reproTask},
			want: startGreen,
		},
		{
			name: "reproduction timed out starts the green run",
			view: View{Epic: decideEpic, Repro: ended(0, true, ""), Task: reproTask},
			want: startGreen,
		},
		{
			name: "capture item starts the green run with no reproduction",
			view: View{
				Epic: decideEpic,
				Task: store.EpicTask{Seq: 3, Build: build, Evidence: evidence.Item{Kind: evidence.KindCapture}},
			},
			want: startGreen,
		},
		{
			name: "capture item ignores a reproduction that exited 0",
			view: View{
				Epic: decideEpic, Repro: ended(0, false, ""),
				Task: store.EpicTask{Seq: 3, Build: build, Evidence: evidence.Item{Kind: evidence.KindCapture}},
			},
			want: startGreen,
		},
		{
			name: "kindless item starts the green run with no reproduction",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: build}},
			want: startGreen,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Decide(tc.view)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Decide = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// reproJudgeReturn returns the kept return of a reproduction judge that gave its one assertion result, with evidence,
// and for a fails result the finding that cites it with claim and evidence.
func reproJudgeReturn(result, evidenceText, claim string) string {
	findings := "[]"
	if result == verdictFails {
		findings = fmt.Sprintf(`[{"citation":"a","claim":%q,"evidence":%q}]`, claim, evidenceText)
	}
	return fmt.Sprintf(`{"status":"done","results":[{"assertion":"a","kind":"must","result":%q,"evidence":%q}],`+
		`"findings":%s}`, result, evidenceText, findings)
}

// TestDecideGreenReproductionJudge table-drives Decide for the judge of a test item's reproduction that exited 0: the
// judge's ruling, never the exit, is the note, and it gates nothing. A judge that rules either way is a note with its
// ruling and reason, and the green run starts once the note is kept. A first judge with no verdict starts the second
// under the same rerun, so the reproduction does not run again, and a second with none is a note with its reason. A
// judge still running waits, a stopped one holds the build, and one ended at a restart reruns the unit.
func TestDecideGreenReproductionJudge(t *testing.T) {
	t.Parallel()
	build := store.TaskBuild{
		Stage: StageGreen, Base: reproBase, Attempt: 1, Rerun: 2, Revision: reproRevision, RedRun: "red-run",
	}
	repro := &store.NodeCheck{ID: RunID(decideEpic, 3, unitRepro, 1, 2), State: store.NodeEnded}
	task := store.EpicTask{Seq: 3, Build: build, Evidence: evidence.Item{Kind: evidence.KindTest}}
	first, second := reproJudgeID(decideEpic, 3, build, 0), reproJudgeID(decideEpic, 3, build, 1)
	const prefix = "T3 attempt 1 reproduction: exited 0 on the base with the commit's test files; "
	doesNot := prefix + "the judge ruled that it does not contradict red: the task renames the test"
	contradicts := prefix + "the judge ruled that it contradicts red: TestName passes on the base: it asserts nothing new"
	silent := prefix + "the judge gave no verdict twice: overtime: limit reached"
	noted := func(note string) Action {
		return Action{Kind: ActMove, Next: build, Notes: []string{note}, Tells: []string{note}}
	}
	startGreen := Action{
		Kind: ActStartRun, Phase: StageGreen, ID: RunID(decideEpic, 3, StageGreen, 1, 2), Revision: reproRevision,
	}
	overtime := store.StepRow{ID: first, End: "overtime", Reason: "limit reached"}
	failed := store.StepRow{
		ID: first, End: endGateFailed,
		Result: reproJudgeReturn(verdictFails, "it asserts nothing new", "TestName passes on the base"),
	}
	restarted := build
	restarted.Rerun = 3
	stopped := build
	stopped.Held = HeldStopped
	tests := []struct {
		name   string
		judges []store.StepRow
		notes  []string
		want   Action
	}{
		{
			name:   "a judge still running waits",
			judges: []store.StepRow{{ID: first}},
			want:   Action{Kind: ActNone},
		},
		{
			name: "a judge that holds notes that the reproduction does not contradict red",
			judges: []store.StepRow{{
				ID: first, End: endDone, Result: reproJudgeReturn(verdictHolds, "the task renames the test", ""),
			}},
			want: noted(doesNot),
		},
		{
			name:   "a judge that fails notes that the reproduction contradicts red, with its finding",
			judges: []store.StepRow{failed},
			want:   noted(contradicts),
		},
		{
			name:   "a kept ruling starts the green run",
			judges: []store.StepRow{failed},
			notes:  []string{contradicts},
			want:   startGreen,
		},
		{
			name:   "a first judge with no verdict starts the second under the same rerun",
			judges: []store.StepRow{overtime},
			want:   Action{Kind: ActStartStep, Unit: unitReproJudge, ID: second},
		},
		{
			name:   "a second judge with no verdict is a note with its reason",
			judges: []store.StepRow{overtime, {ID: second, End: "overtime", Reason: "limit reached"}},
			want:   noted(silent),
		},
		{
			name:   "a stopped judge holds the build",
			judges: []store.StepRow{{ID: first, End: endStopped}},
			want:   Action{Kind: ActPause, Next: stopped},
		},
		{
			name:   "a judge ended at a restart reruns the unit",
			judges: []store.StepRow{{ID: first, End: endEngineRestarted}},
			want:   Action{Kind: ActMove, Next: restarted},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			judged := task
			judged.Notes = tc.notes
			got := Decide(View{Epic: decideEpic, Repro: repro, ReproJudges: tc.judges, Task: judged})
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Decide = %+v, want %+v", got, tc.want)
			}
		})
	}
}
