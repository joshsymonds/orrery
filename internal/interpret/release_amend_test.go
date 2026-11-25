package interpret

import (
	"reflect"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// passClean passes the world once and fails the test when the pass logs anything.
func (w conflictWorld) passClean() {
	w.t.Helper()
	if logged := w.pass(); logged != "" {
		w.t.Fatalf("pass logged: %s", logged)
	}
}

// TestAmendmentAppliesWhileTheReleaseReplans checks that an owner's amendment settles while the release its integrator
// sent back to planning waits on it. The epic is correcting, its release run still runs at its first attempt and the
// replan's plan runs under it, and that plan does not complete while the amendment is under way, so a release that held
// the amendment back would hold the epic for good. The amendment is left checking with a red that failed at the base,
// the rows a deferred amendment is stuck in, and the next pass, which drives every open amendment, applies it with
// nothing else done: R1 takes the new item, and since the stored base has moved past the base, the amendment captures
// its green at the stored base.
func TestAmendmentAppliesWhileTheReleaseReplans(t *testing.T) {
	t.Parallel()
	w := newReplanWorld(t, nodeRequirements()...)
	w.pass()
	w.block("candidate")
	w.requireReplanned(w.report("candidate"))

	item := amendedTestItem("cmd/quiet_test.go")
	amended := amendR1(t, w.s, item)
	w.passClean()
	if !slices.ContainsFunc(w.fakes.evidence.calls, func(call evidenceCall) bool { return call.id == amended.RedRun }) {
		t.Fatalf("evidence calls = %+v, want the red run %s of the amendment", w.fakes.evidence.calls, amended.RedRun)
	}
	judge(t, w.s, amended.RedRun, 1, evidence.VerdictFails)
	if stored := firstAmendment(t, w.s); stored.State != store.AmendmentChecking {
		t.Fatalf("amendment before the pass = %s, want checking with its red failed at the base", stored.State)
	}

	w.passClean()

	if stored := firstAmendment(t, w.s); stored.State != store.AmendmentCapturing || stored.StoredBase != w.tip {
		t.Errorf("amendment while the release replans = %s at %q, want its item applied and capturing at the "+
			"stored base %s", stored.State, stored.StoredBase, w.tip)
	}
	if got := r1Item(t, w.s); !reflect.DeepEqual(got, item) {
		t.Errorf("R1 item = %+v, want the new item %+v", got, item)
	}
	if run := runAt(t, w.s, releaseNodePath); run.State != store.NodeRunning {
		t.Errorf("release run = %+v, want it still running under its replan", run)
	}
	if plan := runAt(t, w.s, replanPlanPath); plan.State != store.NodeRunning {
		t.Errorf("replan plan = %+v, want it running", plan)
	}
}
