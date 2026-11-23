package interpret

import (
	"context"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// heldQuestionSetAsideWorld stores the design of the set-aside tests: heldStartPredecessor is proposed and then set
// aside, with no epic, and heldStartEpic starts after it. Its epic is held, since its predecessor has no shipped epic.
func heldQuestionSetAsideWorld(t *testing.T, s *store.Store) {
	t.Helper()
	ctx := context.Background()
	if err := s.CreateDesign(ctx, store.Design{
		ID: heldStartDesign, Title: "Held starts", Conversation: heldStartWriter, Created: runClock(),
	}); err != nil {
		t.Fatalf("CreateDesign: %v", err)
	}
	if err := s.ProposeContractInDesign(ctx, heldStartDesign, heldStartPredecessor, store.ContractVersion{
		Version: 1, Repository: runRepo, Title: "Contract " + heldStartPredecessor, Care: "serious", Body: "{}",
		Words: "Propose the contract", Writer: heldStartWriter, At: runClock().Unix(),
	}); err != nil {
		t.Fatalf("ProposeContractInDesign %s: %v", heldStartPredecessor, err)
	}
	heldStartAccepted(t, s, heldStartEpic)
	if err := s.AddDesignEdge(ctx, heldStartDesign, heldStartEpic, heldStartPredecessor, runClock()); err != nil {
		t.Fatalf("AddDesignEdge after %s: %v", heldStartPredecessor, err)
	}
	if err := s.SetAsideContract(ctx, heldStartPredecessor, store.ContractChange{
		Words: "Set the contract aside", At: runClock().Unix() + 2,
	}); err != nil {
		t.Fatalf("SetAsideContract %s: %v", heldStartPredecessor, err)
	}
	heldStartCreateEpic(t, s, heldStartEpic, "")
	if got := heldStartEpicRow(t, s).Phase; got != store.PhaseHeld {
		t.Fatalf("epic %s phase = %q, want %q: its predecessor has no epic that ships",
			heldStartEpic, got, store.PhaseHeld)
	}
}

// heldQuestionAskedOnce checks that heldStartEpic is still held and has exactly one open question, a held question.
func heldQuestionAskedOnce(t *testing.T, s *store.Store) {
	t.Helper()
	row := heldStartEpicRow(t, s)
	if row.Phase != store.PhaseHeld {
		t.Errorf("epic phase = %q, want %q: a predecessor that will not ship keeps the epic held",
			row.Phase, store.PhaseHeld)
	}
	open, err := s.OpenQuestions(context.Background(), heldStartEpic)
	if err != nil {
		t.Fatalf("OpenQuestions %s: %v", heldStartEpic, err)
	}
	if len(open) != 1 || open[0].Label != store.HeldLabel {
		t.Errorf("open questions = %+v, want exactly one question labeled %q", open, store.HeldLabel)
	}
}

// TestDesignHeldEpicPredecessorSetAside asserts that when a predecessor's contract is set aside, the held epic is asked
// one held question over two passes and stays held.
func TestDesignHeldEpicPredecessorSetAside(t *testing.T) {
	t.Parallel()
	s := openRunStore(t)
	heldQuestionSetAsideWorld(t, s)
	run := startHeldRun(t, s, &heldOrigin{commit: heldStartCommit}, make(chan time.Time))
	run.pass()
	run.pass()
	run.stop()

	heldQuestionAskedOnce(t, s)
}

// TestDesignHeldEpicPredecessorClosedUnshipped asserts that when a predecessor's epic is closed abandoned, the held
// epic is asked one held question over two passes and stays held.
func TestDesignHeldEpicPredecessorClosedUnshipped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	heldStartWorld(t, s, heldStartPredecessor)
	if _, err := s.CloseEpic(
		ctx, heldStartPredecessor, "abandoned", "", store.CloserOwner, store.Maker{By: store.MakerOwner}, runClock(),
	); err != nil {
		t.Fatalf("CloseEpic %s: %v", heldStartPredecessor, err)
	}
	run := startHeldRun(t, s, &heldOrigin{commit: heldStartCommit}, make(chan time.Time))
	run.pass()
	run.pass()
	run.stop()

	heldQuestionAskedOnce(t, s)
}

// TestDesignHeldEpicPredecessorEndedWithGaps asserts that when a predecessor's epic ends with gaps, the held epic is
// asked one held question over two passes and stays held.
func TestDesignHeldEpicPredecessorEndedWithGaps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	heldStartWorld(t, s, heldStartPredecessor)
	if err := s.EndEpicWithGaps(ctx, heldStartPredecessor, "Ended with gaps.", store.GapsQuestion{
		Gaps: []string{"R2: no check ran"}, Recommended: store.ChoiceRelease, Why: "the check covers docs only",
	}, nil, runClock()); err != nil {
		t.Fatalf("EndEpicWithGaps %s: %v", heldStartPredecessor, err)
	}
	run := startHeldRun(t, s, &heldOrigin{commit: heldStartCommit}, make(chan time.Time))
	run.pass()
	run.pass()
	run.stop()

	heldQuestionAskedOnce(t, s)
}

// TestDesignHeldEpicAnswerStart asserts that once the owner answers start to the held question, the next pass starts
// the epic from the commit origin main returns, although its predecessor has not shipped.
func TestDesignHeldEpicAnswerStart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	heldQuestionSetAsideWorld(t, s)
	origin := &heldOrigin{commit: heldStartCommit}
	run := startHeldRun(t, s, origin, make(chan time.Time))
	heldQuestionAskedOnce(t, s)

	if _, err := s.AnswerQuestion(
		ctx, heldStartEpic, "owner-start", "", 1, store.ChoiceStart, store.Maker{By: store.MakerOwner}, runClock(),
	); err != nil {
		t.Fatalf("AnswerQuestion start: %v", err)
	}
	if n := origin.fetches.Load(); n != 0 {
		t.Fatalf("origin main fetches before the pass after the answer = %d, want none", n)
	}
	run.pass()
	run.stop()

	row := heldStartEpicRow(t, s)
	if row.Base != heldStartCommit || row.StoredBase != heldStartCommit {
		t.Errorf("base = %q, stored base = %q, want both %q: the commit origin main returned", row.Base,
			row.StoredBase, heldStartCommit)
	}
	if row.Phase != runPhase {
		t.Errorf("epic phase = %q, want %q", row.Phase, runPhase)
	}
	if n := origin.fetches.Load(); n != 1 {
		t.Errorf("origin main fetches = %d, want 1: the pass after the answer", n)
	}
}
