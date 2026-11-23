package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// replanReport is the report of the blocked round of the release integrator the replan tests record.
const replanReport = "the release integrator stopped the merge of origin's commit o1 into release tip t1:\n" +
	"- web/src/main.test.tsx: side: candidate; decision: fix the candidate's test"

// replanTip is the release tip the blocked round of the replan tests merged into.
const replanTip = "t1"

// blockedRound returns the round of the release integrator at attempt 1 that returned blocked on replanReport.
func blockedRound(position int64) store.ReleaseRun {
	return store.ReleaseRun{
		Part: store.ReleasePartIntegrator, Position: position, Attempt: 1, Step: "release integrator",
		Start: replanTip, End: "returned", Reason: replanReport, At: releaseRunAt(),
	}
}

// replansOf returns the replan release runs of resumeEpic in s.
func replansOf(t *testing.T, s *store.Store) []store.ReleaseRun {
	t.Helper()
	flow, err := s.EpicFlow(context.Background(), resumeEpic)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	var replans []store.ReleaseRun
	for _, run := range flow.Releases {
		if run.Part == store.ReleasePartReplan {
			replans = append(replans, run)
		}
	}
	return replans
}

// TestRecordIntegratorRoundStoresOneReplanPerTip: a blocked round that asks for a replan is stored with a replan of its
// attempt that carries its tip and report; a round that does not ask stores none; and a later round that asks for a
// replan from a tip a replan already named stores none, so the release stops on it.
func TestRecordIntegratorRoundStoresOneReplanPerTip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openReleaseStore(t, resumeEpicIn(resumeEpic, store.PhaseClean))
	if err := s.RecordIntegratorRound(ctx, resumeEpic, blockedRound(1), false); err != nil {
		t.Fatalf("RecordIntegratorRound without replan: %v", err)
	}
	if got := replansOf(t, s); len(got) != 0 {
		t.Fatalf("replans = %+v, want none for a round that asks for none", got)
	}
	again := blockedRound(2)
	if err := s.RecordIntegratorRound(ctx, resumeEpic, again, true); err != nil {
		t.Fatalf("RecordIntegratorRound with replan: %v", err)
	}
	want := []store.ReleaseRun{{
		Part: store.ReleasePartReplan, Attempt: 1, Step: "replan", Start: replanTip, Reason: replanReport,
		Commits: nil, At: releaseRunAt(),
	}}
	if got := replansOf(t, s); !slices.EqualFunc(got, want, sameReleaseRun) {
		t.Fatalf("replans = %+v, want %+v", got, want)
	}
	later := blockedRound(1)
	later.Attempt = 2
	if err := s.RecordIntegratorRound(ctx, resumeEpic, later, true); err != nil {
		t.Fatalf("RecordIntegratorRound at the same tip: %v", err)
	}
	if got := replansOf(t, s); !slices.EqualFunc(got, want, sameReleaseRun) {
		t.Errorf("replans after a second round from tip %s = %+v, want only %+v", replanTip, got, want)
	}
}

// sameReleaseRun reports whether a and b record the same release run, whatever their commit lists' nil-ness.
func sameReleaseRun(a, b store.ReleaseRun) bool {
	return a.Part == b.Part && a.Position == b.Position && a.Attempt == b.Attempt && a.Step == b.Step &&
		a.Start == b.Start && a.Head == b.Head && a.Reason == b.Reason && a.Passed == b.Passed && a.At.Equal(b.At)
}

// TestRetryOfReleaseStoppedByIntegratorEndsTheStop: retry on a release its integrator stopped with a blocked round stores
// a replan of the round, turns the release node run running at the same attempt, ends the park and leaves the epic
// stopped for the release kind to replan.
func TestRetryOfReleaseStoppedByIntegratorEndsTheStop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := stoppedRelease(t, blockedRound(1))
	if err := s.RetryEpic(ctx, resumeEpic, store.Maker{By: store.MakerOwner}, time.Unix(1700000000, 0)); err != nil {
		t.Fatalf("RetryEpic: %v", err)
	}
	flow, err := s.EpicFlow(ctx, resumeEpic)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	if flow.Epic.Parked != "" || flow.Epic.ParkClass != "" || flow.Epic.Phase != store.PhaseStopped {
		t.Errorf("epic parked %q class %q phase %q, want unparked and still stopped",
			flow.Epic.Parked, flow.Epic.ParkClass, flow.Epic.Phase)
	}
	i := slices.IndexFunc(flow.Runs, func(run store.NodeRun) bool { return run.Path == "release" })
	if i < 0 || flow.Runs[i].State != store.NodeRunning || flow.Runs[i].Attempt != 1 || flow.Runs[i].Output != "" {
		t.Errorf("release runs = %+v, want the release running at attempt 1 with no output", flow.Runs)
	}
	replans := replansOf(t, s)
	if len(replans) != 1 || replans[0].Attempt != 1 || replans[0].Start != replanTip ||
		replans[0].Reason != replanReport {
		t.Errorf("replans = %+v, want one at attempt 1 from %s with the round's report", replans, replanTip)
	}
}

// TestRetryOfOtherStoppedReleaseIsRefused: retry on a release stopped by anything but a blocked round of its integrator,
// a failed release step or a round that failed another way, is refused and changes nothing.
func TestRetryOfOtherStoppedReleaseIsRefused(t *testing.T) {
	t.Parallel()
	refused := blockedRound(1)
	refused.End = "done"
	cases := map[string]store.ReleaseRun{"failed step": releasedStep(1, false), "refused commit": refused}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s, _ := stoppedRelease(t, run)
			err := s.RetryEpic(ctx, resumeEpic, store.Maker{By: store.MakerOwner}, time.Unix(1700000000, 0))
			want := "epic " + resumeEpic + " is stopped; retry ends a stopped release only when its release " +
				"integrator stopped it"
			var refusal store.RefusalError
			if !errors.As(err, &refusal) || err.Error() != want {
				t.Fatalf("RetryEpic = %v, want refusal %q", err, want)
			}
			row, readErr := s.Epic(ctx, resumeEpic)
			if readErr != nil {
				t.Fatalf("Epic: %v", readErr)
			}
			if row.Parked != resumeStopReason || row.Phase != store.PhaseStopped {
				t.Errorf("epic parked %q phase %q, want still stopped and parked", row.Parked, row.Phase)
			}
			if got := replansOf(t, s); len(got) != 0 {
				t.Errorf("replans = %+v, want none", got)
			}
		})
	}
}

// TestReplanReleaseThenResume: ReplanRelease moves a releasing epic to correcting, starts the plan run, and adds the
// trigger and the tell, once; ResumeReplannedRelease then moves the release run to its next attempt with its result
// cleared and the epic to clean, once. A replan of a run at another attempt changes nothing.
func TestReplanReleaseThenResume(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openReleaseStore(t, resumeEpicIn(resumeEpic, store.PhaseClean))
	if err := s.StartRelease(ctx, resumeEpic, resumeAt()); err != nil {
		t.Fatalf("StartRelease: %v", err)
	}
	release := store.NodeRun{Path: "release", ID: "id-resume", Node: "release", Kind: "release", Attempt: 1}
	if err := s.StartNodes(ctx, resumeEpic, []store.NodeRun{release}, nil, releaseRunAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if err := s.RecordNodeResult(ctx, resumeEpic, "release", 1, `{"conflicts":["README"]}`); err != nil {
		t.Fatalf("RecordNodeResult: %v", err)
	}
	plan := store.NodeRun{Path: "release/A1/fix", ID: "id-fix", Node: "fix", Kind: "plan", Mark: 3}
	trigger := store.NodeTrigger{ID: "release:id-fix", Cause: "replan: " + replanReport}
	if err := s.ReplanRelease(ctx, resumeEpic, "release", 0, plan, trigger, "tell", resumeAt()); err != nil {
		t.Fatalf("ReplanRelease at another attempt: %v", err)
	}
	requirePhase(t, s, resumeEpic, store.PhaseReleasing)
	for range 2 {
		if err := s.ReplanRelease(ctx, resumeEpic, "release", 1, plan, trigger, "tell", resumeAt()); err != nil {
			t.Fatalf("ReplanRelease: %v", err)
		}
	}
	flow, err := s.EpicFlow(ctx, resumeEpic)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	if flow.Epic.Phase != "correcting" || flow.Pending != 2 {
		t.Errorf("epic phase %q pending %d, want correcting, and the replan's trigger pending beside the created one",
			flow.Epic.Phase, flow.Pending)
	}
	i := slices.IndexFunc(flow.Runs, func(run store.NodeRun) bool { return run.Path == plan.Path })
	if i < 0 || flow.Runs[i].State != store.NodeRunning || flow.Runs[i].Mark != 3 || flow.Runs[i].Kind != "plan" {
		t.Errorf("runs = %+v, want the plan running at %s with mark 3", flow.Runs, plan.Path)
	}
	history, err := s.EpicHistory(ctx, resumeEpic)
	if err != nil {
		t.Fatalf("EpicHistory: %v", err)
	}
	replanTriggers := slices.DeleteFunc(slices.Clone(history.Triggers), func(got store.EpicTrigger) bool {
		return got.ID != trigger.ID
	})
	if len(replanTriggers) != 1 || replanTriggers[0].Cause != trigger.Cause ||
		replanTriggers[0].State != "pending" {
		t.Errorf("triggers = %+v, want %+v pending once", history.Triggers, trigger)
	}
	if len(history.Tells) != 1 || history.Tells[0].Text != "tell" {
		t.Errorf("tells = %+v, want one tell", history.Tells)
	}
	for range 2 {
		if err = s.ResumeReplannedRelease(ctx, resumeEpic, "release", 1, resumeAt()); err != nil {
			t.Fatalf("ResumeReplannedRelease: %v", err)
		}
	}
	flow, err = s.EpicFlow(ctx, resumeEpic)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	i = slices.IndexFunc(flow.Runs, func(run store.NodeRun) bool { return run.Path == "release" })
	if flow.Epic.Phase != store.PhaseClean || i < 0 || flow.Runs[i].Attempt != 2 || flow.Runs[i].Result != "" ||
		flow.Runs[i].State != store.NodeRunning {
		t.Errorf("epic phase %q, runs %+v, want clean with the release running at attempt 2 and no result",
			flow.Epic.Phase, flow.Runs)
	}
}
