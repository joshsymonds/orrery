package enginehost

import (
	"testing"
	"time"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// The synthetic times the task's rungs are written and end at, in unix seconds.
const (
	rungOneWritten   = 1700000500
	rungOneEnded     = 1700000550
	rungTwoWritten   = 1700000600
	rungTwoEnded     = 1700000650
	rungThreeWritten = 1700000700
)

// The statements that write a task's rung and the outcome of a rung into the store file.
const (
	insertRungSQL = `INSERT INTO epic_task_rungs (epic, task, rung, kind, moved_by, cause, gate, assertion, step, created)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	insertOutcomeSQL = `INSERT INTO epic_task_rung_outcomes (epic, task, rung, outcome, reason, ended)
		VALUES (?, ?, ?, ?, ?, ?)`
)

// TestTaskLadderGetEpicListsRungs checks that GetEpic lists each task's ladder rungs in rung order, with every field: a
// rung moved by a finding carries the finding's gate and claim, an open rung has no outcome and reads ended as 0, and a
// task that never climbed lists none.
func TestTaskLadderGetEpicListsRungs(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, path := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	first, err := st.ClaimTurn(ctx, epicServiceID, time.Unix(turnStarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = st.SetTurnStep(ctx, epicServiceID, first.Seq, "step-1"); err != nil {
		t.Fatalf("SetTurnStep: %v", err)
	}
	if err = st.CompleteTurn(ctx, epicServiceID, first.Seq, plannedReturn(), time.Unix(turnCompleted, 0)); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	// Rung 3 is written first, and the outcomes are written out of order, so the read is the one that orders the rungs.
	execStoreFile(t, path, insertRungSQL, epicServiceID, 1, 3, "rescue", "fail_decision",
		"the decision failed the task", "", "", "step-6", rungThreeWritten)
	execStoreFile(t, path, insertRungSQL, epicServiceID, 1, 1, "attempt", "", "", "", "", "", rungOneWritten)
	execStoreFile(t, path, insertRungSQL, epicServiceID, 1, 2, "attempt", "finding", "the claim is unmet", "review",
		"the claim", "step-4", rungTwoWritten)
	execStoreFile(t, path, insertOutcomeSQL, epicServiceID, 1, 2, "failed", "the build failed again", rungTwoEnded)
	execStoreFile(t, path, insertOutcomeSQL, epicServiceID, 1, 1, "failed", "the first build failed", rungOneEnded)

	tasks := getEpicOK(t, client).GetTasks()
	if len(tasks) != 2 || tasks[0].GetId() != "T1" {
		t.Fatalf("tasks = %v, want T1 then T2", tasks)
	}
	wantProtos(t, "task 1 rungs", tasks[0].GetRungs(), []*v1.EpicTaskRung{
		{
			Rung: 1, Kind: "attempt", Outcome: "failed", Reason: "the first build failed",
			Created: rungOneWritten, Ended: rungOneEnded,
		},
		{
			Rung: 2, Kind: "attempt", MovedBy: "finding", Cause: "the claim is unmet", Gate: "review",
			Assertion: "the claim", Step: "step-4", Outcome: "failed", Reason: "the build failed again",
			Created: rungTwoWritten, Ended: rungTwoEnded,
		},
		{
			Rung: 3, Kind: "rescue", MovedBy: "fail_decision", Cause: "the decision failed the task", Step: "step-6",
			Created: rungThreeWritten,
		},
	})
	if got := tasks[1].GetRungs(); len(got) != 0 {
		t.Errorf("task 2 rungs = %v, want none for a task that never climbed", got)
	}
}
