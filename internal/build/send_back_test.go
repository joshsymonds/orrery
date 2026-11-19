package build

import (
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// sendBackReason is the reason the planner gives for sending task 1 back.
const sendBackReason = "the implementer's commit breaks the ordering the store keeps"

// TestSendBackReopenedImplementStartsUnderNewID covers E2-R1: a done task whose attempt-1 implementer ended with a commit,
// once the planner sends it back, starts a new implement step under an id its earlier attempt did not use, with the
// reason in its brief. The earlier step is ended, so an id reused from it adopts that step and starts no implementer.
func TestSendBackReopenedImplementStartsUnderNewID(t *testing.T) {
	t.Parallel()
	plan := passPlan()
	plan.Actions = []store.EpicActionInput{{Kind: store.ActionSendBack, Task: 1, Reason: sendBackReason}}
	f := newPlanFixture(t, plan)
	f.pass()
	f.addRun(f.lastRun().id, evidence.PhaseRed, f.base, 1, evidence.VerdictFails)
	f.pass()
	old := f.lastStart().ID
	commit := buildObject(t, f.repo, f.base)
	f.setStep(old, "done", guardDone(commit))
	if err := f.store.AcceptTask(f.ctx, passEpic, 1, f.task().Build, "reviewed"); err != nil {
		t.Fatalf("AcceptTask: %v", err)
	}
	f.carryPendingSendBack()

	before := len(f.steps.starts)
	f.pass()
	if got := len(f.steps.starts) - before; got != 1 {
		t.Fatalf("Start calls after the send_back = %d, want one new implement step", got)
	}
	start := f.steps.starts[before]
	if start.ID == old {
		t.Errorf("implement step id = %s, the attempt-1 id the earlier step ended under", start.ID)
	}
	if !strings.Contains(start.Prompt, sendBackReason) {
		t.Errorf("prompt lacks the send_back reason:\n%s", start.Prompt)
	}
}

// carryPendingSendBack carries out the one pending send_back action of the fixture's epic, and fails the test when
// there is none.
func (f *passFixture) carryPendingSendBack() {
	f.t.Helper()
	actions, err := f.store.PendingActions(f.ctx)
	if err != nil {
		f.t.Fatalf("PendingActions: %v", err)
	}
	for _, action := range actions {
		if action.Kind != store.ActionSendBack {
			continue
		}
		if _, err = f.store.CarryOutAction(f.ctx, action, passClock()); err != nil {
			f.t.Fatalf("CarryOutAction: %v", err)
		}
		return
	}
	f.t.Fatal("no pending send_back action to carry out")
}
