package interpret

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// stopRunsOf returns the release runs of flow that are stop runs, in the order EpicFlow reads them.
func stopRunsOf(flow store.EpicFlow) []store.ReleaseRun {
	var stops []store.ReleaseRun
	for _, run := range flow.Releases {
		if run.Part == store.ReleasePartStop {
			stops = append(stops, run)
		}
	}
	return stops
}

// TestReleaseControlsNoSlot checks the owner's stop of a releasing epic, with no release slot involved. The owner's
// StopEpic returns the open release step and records a stop run at the attempt. Once that step ends stopped the release
// stops, classed release_owner_stop, and starts no further step, reviewer or judge. A planner's resume of it is refused,
// and the owner's ReleaseEpic resumes it at the next attempt. The test takes, reads and frees no release slot.
func TestReleaseControlsNoSlot(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	w.createEpic(runEpic, store.PhaseClean, tagStep())
	w.pass()
	w.pass()

	run := runAt(t, w.s, releaseNodePath)
	step := workID(t, w.s, releaseNodePath, "release 1")
	if got := w.fakes.steps.calls; len(got) != 1 || got[0].ID != step {
		t.Fatalf("step starts = %+v, want the one first release step %s started", got, step)
	}

	// The planner is woken and returns resume_release before the owner stops the release, so the action is pending.
	ctx := t.Context()
	kick := store.Maker{By: store.MakerOwner}
	if err := w.s.KickEpic(ctx, runEpic, "the owner has a word", kick, runClock()); err != nil {
		t.Fatalf("KickEpic: %v", err)
	}
	turn, err := w.s.ClaimTurn(ctx, runEpic, runClock())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = w.s.CompleteTurn(ctx, runEpic, turn.Seq, store.EpicPlan{
		Decisions: []string{}, Outline: "resume the release", Outcome: "work",
		Actions: []store.EpicActionInput{{Kind: store.ActionResumeRelease, Reason: "the step can run again"}},
	}, runClock()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	open, err := w.s.StopEpic(ctx, runEpic, store.Maker{By: store.MakerOwner}, runClock())
	if err != nil {
		t.Fatalf("StopEpic: %v", err)
	}
	if open != step {
		t.Errorf("StopEpic returned step %q, want the open step %q", open, step)
	}
	wantStop := []store.ReleaseRun{{
		Part: store.ReleasePartStop, Attempt: run.Attempt, Reason: "owner stopped the release", At: runClock(),
	}}
	if got := stopRunsOf(w.flowOf(runEpic)); !reflect.DeepEqual(got, wantStop) {
		t.Fatalf("stop runs = %+v, want %+v", got, wantStop)
	}

	// The open step ends stopped, so the release records it as failed and stops, and no further step starts.
	end := store.StepEnd{ID: step, End: "stopped", Status: "stopped"}
	if err = w.s.ApplyEngine(ctx, []store.EngineOp{end}, runClock()); err != nil {
		t.Fatalf("end step %s: %v", step, err)
	}
	w.pass()
	w.pass()
	flow := w.flowOf(runEpic)
	if flow.Epic.Phase != store.PhaseStopped || flow.Epic.ParkClass != store.ParkClassReleaseOwnerStop {
		t.Errorf("epic phase = %q and park class = %q, want stopped and %q",
			flow.Epic.Phase, flow.Epic.ParkClass, store.ParkClassReleaseOwnerStop)
	}
	if state := runAt(t, w.s, releaseNodePath).State; state != store.NodeParked {
		t.Errorf("release run state = %q, want parked", state)
	}
	if got := len(w.fakes.steps.calls); got != 1 {
		t.Errorf("step starts = %d after the stop, want only the step that was open", got)
	}

	// The planner's pending resume_release is carried out and refused, so the release stays stopped.
	pending, err := w.s.PendingActions(ctx)
	if err != nil {
		t.Fatalf("PendingActions: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending actions = %+v, want the planner's one resume_release", pending)
	}
	if _, err = w.s.CarryOutAction(ctx, pending[0], runClock()); err != nil {
		t.Fatalf("CarryOutAction: %v", err)
	}
	refusal := "planner resume release refused: epic " + runEpic + ": its owner stopped the release; " +
		"only the owner resumes it"
	row, err := w.s.Epic(ctx, runEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if !slices.ContainsFunc(row.Decisions, func(d store.EpicDecision) bool {
		return strings.HasPrefix(d.Text, refusal)
	}) {
		t.Errorf("decisions = %+v, want the refusal %q recorded", row.Decisions, refusal)
	}
	if phase := w.flowOf(runEpic).Epic.Phase; phase != store.PhaseStopped {
		t.Errorf("epic phase = %q after the refused resume, want stopped", phase)
	}

	// The owner's release resumes the stopped release at its next attempt, naming the undo-none step that failed.
	if err = w.s.ReleaseEpic(ctx, runEpic, []int64{1}, store.Maker{By: store.MakerOwner}, runClock()); err != nil {
		t.Fatalf("ReleaseEpic: %v", err)
	}
	if phase := w.flowOf(runEpic).Epic.Phase; phase != store.PhaseReleasing {
		t.Errorf("epic phase = %q after ReleaseEpic, want releasing", phase)
	}
	if got := runAt(t, w.s, releaseNodePath).Attempt; got != run.Attempt+1 {
		t.Errorf("release attempt = %d after ReleaseEpic, want %d", got, run.Attempt+1)
	}
}
