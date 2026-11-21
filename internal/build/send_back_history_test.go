package build

import (
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// sendBackHistoryReview is a failing review of gate review at attempt 1, position 0, run by step, with one finding
// whose id, claim and result are id, claim and result.
func sendBackHistoryReview(step, id, claim, result string) store.TaskReview {
	return store.TaskReview{
		Attempt: 1, Position: 0, Gate: "review", Step: step, Verdict: verdictFails,
		Findings: []store.TaskFinding{{
			Position: 0, ID: id, Claim: claim, Citation: "R1", Location: "thing.go:3", Evidence: "line 3",
			VerifyBy: "run it", Result: result,
		}},
	}
}

// TestSendBackHistoryEngineVerifiesOnlyTheReopenedReview checks that the engine reads only the current generation of a
// task sent back. Task 1 is reviewed at attempt 1 position 0 with finding F1 confirmed, then sent back. Its reopened
// build is reviewed at attempt 1 position 0 again, with finding F2, and is at verify on its first finding. The next
// pass starts the verifier of F2: the earlier review's F1, kept in the store under the earlier generation, is neither
// in the task the engine reads nor in the verifier's prompt.
func TestSendBackHistoryEngineVerifiesOnlyTheReopenedReview(t *testing.T) {
	t.Parallel()
	const earlierClaim, reopenedClaim = "the earlier claim", "the reopened claim"
	plan := passPlan()
	plan.Actions = []store.EpicActionInput{{Kind: store.ActionSendBack, Task: 1, Reason: sendBackReason}}
	f := newPlanFixture(t, plan)
	f.pass()
	settled := store.TaskBuild{Stage: StageVerify, Base: f.base, Attempt: 1, Revision: f.base, FindingPos: 1}
	if err := f.store.AddTaskReview(f.ctx, passEpic, 1, f.task().Build, settled,
		sendBackHistoryReview("step-review-0", "F1", earlierClaim, "confirmed")); err != nil {
		t.Fatalf("AddTaskReview before the send_back: %v", err)
	}
	if err := f.store.AcceptTask(f.ctx, passEpic, 1, f.task().Build, "reviewed"); err != nil {
		t.Fatalf("AcceptTask: %v", err)
	}
	f.carryPendingSendBack()

	verifying := store.TaskBuild{Stage: StageVerify, Base: f.base, Attempt: 1, Revision: f.base}
	if err := f.store.AddTaskReview(f.ctx, passEpic, 1, f.task().Build, verifying,
		sendBackHistoryReview("step-review-1", "F2", reopenedClaim, "")); err != nil {
		t.Fatalf("AddTaskReview at attempt 1 position 0 after the send_back: %v", err)
	}
	task := f.task()
	if len(task.Reviews) != 1 || len(task.Reviews[0].Findings) != 1 || task.Reviews[0].Findings[0].ID != "F2" {
		t.Fatalf("reviews the engine reads = %+v, want only the reopened review with F2", task.Reviews)
	}

	before := len(f.steps.starts)
	f.pass()
	if got := len(f.steps.starts) - before; got != 1 {
		t.Fatalf("Start calls after the reopened review = %d, want the verifier of F2", got)
	}
	start := f.steps.starts[before]
	if !strings.Contains(start.Prompt, reopenedClaim) || strings.Contains(start.Prompt, earlierClaim) {
		t.Errorf("verifier prompt does not verify F2 alone:\n%s", start.Prompt)
	}
}
