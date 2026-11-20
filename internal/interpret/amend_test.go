package interpret

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// amendedTestItem returns the test item the amendment tests amend R1 to: the conclusion R1 already has, a capture of its
// own, and tests, its test files.
func amendedTestItem(tests ...string) evidence.Item {
	return evidence.Item{
		Capture: []string{"./bin/quiet-test", "-run", "QuietStepRestart"}, Limit: 60, Artifacts: []string{},
		Supplied: []string{}, Conclusion: "the runner runs", Judge: "a judge", Kind: evidence.KindTest, Tests: tests,
	}
}

// amendR1 asks, as the owner, for R1 of the test epic to hold item, and returns the stored amendment.
func amendR1(t *testing.T, s *store.Store, item evidence.Item) store.EvidenceAmendment {
	t.Helper()
	amended, err := s.AmendEvidence(context.Background(), runEpic, store.Amendment{
		Requirement: "R1", Item: item, Reason: "the test moved",
	}, store.Maker{By: store.MakerOwner}, runClock())
	if err != nil {
		t.Fatalf("AmendEvidence: %v", err)
	}
	return amended
}

// judge ends the evidence run with id as captured with exit and judged verdict, as the evidence runner records a run.
func judge(t *testing.T, s *store.Store, id string, exit int, verdict string) {
	t.Helper()
	ctx := context.Background()
	run, err := s.EvidenceRun(ctx, id)
	if err != nil {
		t.Fatalf("EvidenceRun %s: %v", id, err)
	}
	if err = s.RecordEvidenceCapture(ctx, id, store.EvidenceCapture{Exit: exit, At: runClock()}); err != nil {
		t.Fatalf("RecordEvidenceCapture %s: %v", id, err)
	}
	if err = s.RecordEvidenceVerdict(ctx, id, store.EvidenceVerdict{
		Verdict: verdict, Passed: evidence.Passed(run.Phase, "", exit, verdict), At: runClock(),
	}); err != nil {
		t.Fatalf("RecordEvidenceVerdict %s: %v", id, err)
	}
}

// firstAmendment returns amendment A1 of the test epic as the store reads it back.
func firstAmendment(t *testing.T, s *store.Store) store.EvidenceAmendment {
	t.Helper()
	for _, amended := range epicOf(t, s).Amendments {
		if amended.Seq == 1 {
			return amended
		}
	}
	t.Fatalf("epic %s has no amendment A1", runEpic)
	return store.EvidenceAmendment{}
}

// r1Item returns the item R1 of the test epic holds.
func r1Item(t *testing.T, s *store.Store) evidence.Item {
	t.Helper()
	return epicOf(t, s).Requirements[0].Item
}

// triggerOf returns the cause of trigger id of the test epic, and whether the epic has it.
func triggerOf(t *testing.T, s *store.Store, id string) (string, bool) {
	t.Helper()
	history, err := s.EpicHistory(context.Background(), runEpic)
	if err != nil {
		t.Fatalf("EpicHistory: %v", err)
	}
	for _, trigger := range history.Triggers {
		if trigger.ID == id {
			return trigger.Cause, true
		}
	}
	return "", false
}

// amendWorld is a test epic that owns its branch's one file, with a runner over the fakes that logs into logs.
type amendWorld struct {
	s     *store.Store
	base  string
	fakes nodeFakes
	logs  *bytes.Buffer
}

// newAmendWorld stores the test epic on a scratch repository at its base, pinned to the owned flow, so its passes start
// no evidence of their own.
func newAmendWorld(t *testing.T) *amendWorld {
	t.Helper()
	repo, base := nodeRepo(t)
	nodeEpicBranch(t, repo, "b.go")
	s := openRunStore(t)
	createNodeEpic(t, s, repo, base, nodeOwnedFlow)
	return &amendWorld{s: s, base: base, fakes: newNodeFakes(s), logs: &bytes.Buffer{}}
}

// pass runs one Runner.Pass over a new runner, as a fresh orreryd would after a restart, and fails the test when the pass
// logs an error.
func (w *amendWorld) pass(t *testing.T) {
	t.Helper()
	runner := runNodes(w.s, w.fakes)
	runner.logger = slog.New(slog.NewTextHandler(w.logs, nil))
	runner.Pass(context.Background())
	if w.logs.Len() > 0 {
		t.Fatalf("pass logged an error: %s", w.logs.String())
	}
}

// TestAmendmentAppliedOnceItsRedFailsAtTheBase checks the core of an amendment: the pass runs the new item's red at the
// epic's base, nothing changes while that red is judged, and a red judged fails applies the new item, records the
// amendment and wakes the planner.
func TestAmendmentAppliedOnceItsRedFailsAtTheBase(t *testing.T) {
	t.Parallel()
	w := newAmendWorld(t)
	item := amendedTestItem("cmd/quiet_test.go")
	amended := amendR1(t, w.s, item)

	w.pass(t)
	want := []evidenceCall{{
		id: amended.RedRun, epic: runEpic, item: "R1", revision: w.base, phase: evidence.PhaseRed, spec: item,
	}}
	if !reflect.DeepEqual(w.fakes.evidence.calls, want) {
		t.Fatalf("evidence calls = %+v, want %+v", w.fakes.evidence.calls, want)
	}
	if got := firstAmendment(t, w.s).State; got != store.AmendmentChecking {
		t.Errorf("amendment while its red runs = %s, want checking", got)
	}
	if got := r1Item(t, w.s); reflect.DeepEqual(got, item) {
		t.Errorf("R1 holds the new item while its red runs, want the old item")
	}

	judge(t, w.s, amended.RedRun, 1, evidence.VerdictFails)
	w.pass(t)

	if got := firstAmendment(t, w.s).State; got != store.AmendmentApplied {
		t.Errorf("amendment after a red that fails = %s, want applied", got)
	}
	if got := r1Item(t, w.s); !reflect.DeepEqual(got, item) {
		t.Errorf("R1 item = %+v, want the new item %+v", got, item)
	}
	cause, ok := triggerOf(t, w.s, "amend-1")
	wantCause := "evidence of R1 amended by the owner (A1): the next integrate captures the new item at its candidate"
	if !ok || cause != wantCause {
		t.Errorf("trigger amend-1 = %q (%v), want %q", cause, ok, wantCause)
	}
}

// TestAmendmentRefusedWhenItsRedHoldsAtTheBase checks the guard against weakening: a new item whose red is judged to hold
// at the epic's base is refused, the old item stays, and the refusal is recorded and wakes the planner.
func TestAmendmentRefusedWhenItsRedHoldsAtTheBase(t *testing.T) {
	t.Parallel()
	w := newAmendWorld(t)
	old := r1Item(t, w.s)
	amended := amendR1(t, w.s, amendedTestItem("cmd/quiet_test.go"))
	w.pass(t)
	judge(t, w.s, amended.RedRun, 0, evidence.VerdictHolds)

	w.pass(t)

	stored := firstAmendment(t, w.s)
	if stored.State != store.AmendmentRefused {
		t.Fatalf("amendment after a red that holds = %s, want refused", stored.State)
	}
	if got := r1Item(t, w.s); !reflect.DeepEqual(got, old) {
		t.Errorf("R1 item after the refusal = %+v, want the old item %+v", got, old)
	}
	history, err := w.s.EpicHistory(context.Background(), runEpic)
	if err != nil {
		t.Fatalf("EpicHistory: %v", err)
	}
	tell := "Evidence amendment A1 of R1 refused: " + stored.Refusal
	if !slices.ContainsFunc(history.Tells, func(told store.EpicTell) bool { return told.Text == tell }) {
		t.Errorf("tells = %+v, want the owner told %q", history.Tells, tell)
	}
	w.pass(t)
	for _, call := range w.fakes.evidence.calls {
		if call.id != amended.RedRun {
			t.Errorf("evidence call %+v, want only the red run %s: a refused amendment runs nothing more", call,
				amended.RedRun)
		}
	}
}

// TestAmendmentSurvivesARestartMidRed checks that a restart while the red runs starts nothing twice, and that a red the
// restart lost runs again under the next attempt and then settles the amendment.
func TestAmendmentSurvivesARestartMidRed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newAmendWorld(t)
	item := amendedTestItem("cmd/quiet_test.go")
	amended := amendR1(t, w.s, item)
	w.pass(t)

	// A new runner and new fakes stand for the next orreryd: its pass asks for the same run, which is stored already.
	w.fakes = newNodeFakes(w.s)
	w.pass(t)
	runs, err := w.s.EvidenceRuns(ctx, runEpic)
	if err != nil {
		t.Fatalf("EvidenceRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != amended.RedRun {
		t.Fatalf("evidence runs after the restart = %+v, want the one red run %s", runs, amended.RedRun)
	}

	// The restart could not re-adopt the capture, so the red was lost: the next pass names the next attempt, and the pass
	// after it runs the red again under that id.
	if err = w.s.RecordEvidenceCapture(ctx, amended.RedRun, store.EvidenceCapture{
		NotCaptured: store.LostAtRestart, At: runClock(),
	}); err != nil {
		t.Fatalf("RecordEvidenceCapture: %v", err)
	}
	w.pass(t)
	next := store.AmendmentWorkID(runEpic, 1, store.AmendmentRed, 1)
	if stored := firstAmendment(t, w.s); stored.State != store.AmendmentChecking || stored.RedRun != next {
		t.Fatalf("amendment after a lost red = %s %s, want checking under %s", stored.State, stored.RedRun, next)
	}
	w.pass(t)
	if last := w.fakes.evidence.calls[len(w.fakes.evidence.calls)-1]; last.id != next || last.revision != w.base {
		t.Fatalf("last evidence call = %+v, want the red rerun %s at the base", last, next)
	}

	judge(t, w.s, next, 1, evidence.VerdictFails)
	w.pass(t)
	if got := firstAmendment(t, w.s).State; got != store.AmendmentApplied {
		t.Errorf("amendment after its rerun failed at the base = %s, want applied", got)
	}
	if got := r1Item(t, w.s); !reflect.DeepEqual(got, item) {
		t.Errorf("R1 item = %+v, want the new item", got)
	}
}

// TestAmendmentWaitsForAnEvidenceNodeUnderWay checks that an amendment whose red passed is not applied while an evidence
// node run of the epic has its work under way, since that work runs R1 under ids a new item would leave stale, and is
// applied once the node has ended.
func TestAmendmentWaitsForAnEvidenceNodeUnderWay(t *testing.T) {
	t.Parallel()
	repo, base := nodeRepo(t)
	nodeEpicBranch(t, repo, "b.go")
	s := openRunStore(t)
	createNodeEpic(t, s, repo, base, nodeEvidenceFlow)
	w := &amendWorld{s: s, base: base, fakes: newNodeFakes(s), logs: &bytes.Buffer{}}
	w.pass(t)
	w.pass(t)
	node := runAt(t, s, "evidence")
	if node.State != store.NodeRunning || node.Revision == "" {
		t.Fatalf("evidence node = %+v, want running with its revision", node)
	}
	amended := amendR1(t, s, amendedTestItem("cmd/quiet_test.go"))
	w.pass(t)
	judge(t, s, amended.RedRun, 1, evidence.VerdictFails)

	w.pass(t)
	if got := firstAmendment(t, s).State; got != store.AmendmentChecking {
		t.Fatalf("amendment while the evidence node runs = %s, want checking", got)
	}

	judge(t, s, workID(t, s, "evidence", "R1"), 1, evidence.VerdictFails)
	for range 4 {
		w.pass(t)
	}
	if node = runAt(t, s, "evidence"); node.State != store.NodeEnded {
		t.Fatalf("evidence node = %+v, want ended", node)
	}
	if got := firstAmendment(t, s).State; got != store.AmendmentApplied {
		t.Errorf("amendment once the evidence node ended = %s, want applied", got)
	}
}

// amendmentRecomputesGreenAtTheStoredBase checks that an amendment applied after a batch moved the stored base runs
// the new item's green there and reproduces its test item at the base, and that once both have run, Unbuilt finds R1's
// proof in the amendment: its red at the base, its green at the stored base and its reproduction.
func amendmentRecomputesGreenAtTheStoredBase(images *worldImages) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		w, candidate := images.integrateWorld(t, atCaptured)
		w.pass()
		if stored := epicOf(t, w.s).StoredBase; stored != candidate {
			t.Fatalf("stored base = %s, want the candidate %s", stored, candidate)
		}
		item := amendedTestItem("b.go")
		amended := amendR1(t, w.s, item)
		drive := func() {
			t.Helper()
			epic, err := w.s.Epic(ctx, runEpic)
			if err != nil {
				t.Fatalf("Epic: %v", err)
			}
			w.runner.passAmendments(ctx, []store.EpicRow{epic})
			if w.logs.Len() > 0 {
				t.Fatalf("pass logged an error: %s", w.logs.String())
			}
		}

		drive()
		judge(t, w.s, amended.RedRun, 1, evidence.VerdictFails)
		drive()
		stored := firstAmendment(t, w.s)
		if stored.State != store.AmendmentCapturing || stored.StoredBase != candidate {
			t.Fatalf("amendment after its red = %+v, want capturing at %s", stored, candidate)
		}

		drive()
		green := evidenceCall{
			id: stored.GreenRun, epic: runEpic, item: "R1", revision: candidate, phase: evidence.PhaseGreen, spec: item,
		}
		if !slices.ContainsFunc(w.fakes.evidence.calls, func(call evidenceCall) bool {
			return reflect.DeepEqual(call, green)
		}) {
			t.Errorf("evidence calls = %+v, want the green run %+v", w.fakes.evidence.calls, green)
		}
		i := slices.IndexFunc(
			w.fakes.checks.calls,
			func(check store.NodeCheck) bool { return check.ID == stored.ReproCheck },
		)
		if i < 0 {
			t.Fatalf("checks = %+v, want the reproduction %s", w.fakes.checks.calls, stored.ReproCheck)
		}
		repro := w.fakes.checks.calls[i]
		if repro.Path != "amendment A1" || !slices.Equal(repro.Argv, item.Capture) || repro.Revision == w.base ||
			repro.Revision == candidate {
			t.Errorf("reproduction = %+v, want the new capture at an overlay of the base", repro)
		}
		if parent := w.git("rev-parse", repro.Revision+"^"); parent != w.base {
			t.Errorf("reproduction overlay's parent = %s, want the base %s", parent, w.base)
		}
		if got, ok := triggerOf(t, w.s, "amend-1"); ok {
			t.Errorf("trigger amend-1 = %q while green runs, want none yet", got)
		}

		judge(t, w.s, stored.GreenRun, 0, evidence.VerdictHolds)
		nodeEndCheck(t, w.s, stored.ReproCheck, 1, "")
		drive()

		if got := firstAmendment(t, w.s).State; got != store.AmendmentApplied {
			t.Errorf("amendment once green and its reproduction ran = %s, want applied", got)
		}
		cause, _ := triggerOf(t, w.s, "amend-1")
		wantCause := "evidence of R1 amended by the owner (A1): the green run " + stored.GreenRun + " at the stored base " +
			candidate + " passed; its reproduction " + stored.ReproCheck + " at the epic's base exited 1"
		if cause != wantCause {
			t.Errorf("trigger amend-1 = %q, want %q", cause, wantCause)
		}
		flow, err := w.s.EpicFlow(ctx, runEpic)
		if err != nil {
			t.Fatalf("EpicFlow: %v", err)
		}
		rows := kinds.Rows{Epic: flow.Epic, Runs: flow.Runs, Checks: flow.Checks, Evidence: flow.Evidence}
		if missing := kinds.Unbuilt(rows); len(missing) != 0 {
			t.Errorf("Unbuilt after the amendment = %q, want R1's proof found", missing)
		}
	}
}

// refusingEvidence is the evidence fake whose amendment runs the evidence runner refuses for good, as it refuses a
// revision that does not resolve.
type refusingEvidence struct {
	*fakeEvidence
}

// RunAmendment refuses every amendment run with an error matching enginehost.ErrEvidenceRefused, and stores no run.
func (refusingEvidence) RunAmendment(
	_ context.Context, _, _, _ string, _ evidence.Item, revision, _ string,
) (store.EvidenceRun, error) {
	return store.EvidenceRun{}, fmt.Errorf("%w: revision %q does not resolve", enginehost.ErrEvidenceRefused, revision)
}

// TestAmendmentWhoseRedCannotStartIsRefused checks that an amendment whose red run the evidence runner refuses for good
// is refused with that reason, rather than retried on every pass, and that the Requirement keeps its item.
func TestAmendmentWhoseRedCannotStartIsRefused(t *testing.T) {
	t.Parallel()
	w := newAmendWorld(t)
	old := r1Item(t, w.s)
	amended := amendR1(t, w.s, amendedTestItem("cmd/quiet_test.go"))
	cfg := nodeConfig(w.s, w.fakes)
	cfg.Evidence = refusingEvidence{w.fakes.evidence}
	New(cfg).Pass(context.Background())

	stored := firstAmendment(t, w.s)
	want := "the red run " + amended.RedRun + " of the new item at the epic's base " + w.base + " could not start: " +
		"evidence run is refused: revision \"" + w.base + "\" does not resolve"
	if stored.State != store.AmendmentRefused || stored.Refusal != want {
		t.Errorf("amendment = %s %q, want refused %q", stored.State, stored.Refusal, want)
	}
	if got := r1Item(t, w.s); !reflect.DeepEqual(got, old) {
		t.Errorf("R1 item = %+v, want the old item kept", got)
	}
}
