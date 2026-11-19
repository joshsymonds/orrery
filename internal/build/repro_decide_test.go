package build

import (
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
// asks for the reproduction at the commit, waits for it, reruns one lost at a restart, spends the attempt when it did
// not run or exited 0, and starts the green run when its exit holds. A task of another kind starts the green run
// with no reproduction.
func TestDecideGreenReproduction(t *testing.T) {
	t.Parallel()
	reproTestItem := evidence.Item{Kind: evidence.KindTest, Tests: []string{"feature_test.go"}}
	build := store.TaskBuild{
		Stage: StageGreen, Base: reproBase, Attempt: 1, Rerun: 2, Revision: reproRevision, RedRun: "red-run",
	}
	lostRerun := build
	lostRerun.Rerun = 3
	disagreement := "green not met: the judge and the exit disagree: red was judged fails at the base repro-base, " +
		"but its reproduction with the commit's tests exited 0"
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
			name: "reproduction not run spends the first attempt with its reason",
			view: View{
				Epic: decideEpic, Repro: ended(0, false, "no workdir"), Task: reproTask,
			},
			want: Action{Kind: ActFail, Reason: "green not captured: reproduction did not run: no workdir"},
		},
		{
			name: "reproduction not run on the second attempt fails",
			view: View{
				Epic: decideEpic, Repro: ended(0, false, "no workdir"),
				Task: store.EpicTask{Seq: 3, Evidence: reproTestItem, Build: store.TaskBuild{
					Stage:    StageGreen,
					Base:     reproBase,
					Attempt:  2,
					Rerun:    2,
					Revision: reproRevision,
					RedRun:   "red-run",
				}},
			},
			want: Action{Kind: ActFail, Reason: "green not captured: reproduction did not run: no workdir"},
		},
		{
			name: "reproduction exit 0 spends the first attempt as a disagreement",
			view: View{
				Epic: decideEpic, Repro: ended(0, false, ""), Task: reproTask,
			},
			want: Action{Kind: ActFail, Reason: disagreement},
		},
		{
			name: "reproduction exit 0 on the second attempt fails",
			view: View{
				Epic: decideEpic, Repro: ended(0, false, ""),
				Task: store.EpicTask{Seq: 3, Evidence: reproTestItem, Build: store.TaskBuild{
					Stage:    StageGreen,
					Base:     reproBase,
					Attempt:  2,
					Rerun:    2,
					Revision: reproRevision,
					RedRun:   "red-run",
				}},
			},
			want: Action{Kind: ActFail, Reason: disagreement},
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
