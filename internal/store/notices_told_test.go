package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// toldRowOf is the epic notice row the told tests put: a row of id 1 under claimEpic, with told as its mark.
func toldRowOf(told *ToldMark) NoticeRow {
	return NoticeRow{
		ID: 1, State: "waiting", Time: 1_700_000_000, Phase: "desk", Due: 1_700_000_300_000,
		SourceKind: "epic", Source: []byte(`{"id":"synthetic"}`), Epic: claimEpic, Told: told,
	}
}

// endTurnOfClaimEpic claims the next turn of claimEpic and completes it as work with one planned task, so the epic has
// one ended, untold turn and a planned task its tells are written to.
func endTurnOfClaimEpic(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	turn, err := s.ClaimTurn(ctx, claimEpic, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	plan := EpicPlan{Outcome: "work", Tasks: []EpicTaskInput{{
		Title: "Write the tell", Deliverable: "the tell lands", Files: []string{"a.go"},
		Requirements: []string{"R1"}, Gate: "check",
		Evidence: evidence.Item{
			Capture: []string{"scripts/check"}, Limit: 60, Conclusion: "the tell lands", Judge: "read it",
		},
	}}}
	if err := s.CompleteTurn(ctx, claimEpic, turn.Seq, plan, completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
}

// untoldTellsOfClaimEpic returns the tells of claimEpic that Attention reports as untold.
func untoldTellsOfClaimEpic(t *testing.T, s *Store) []EpicTell {
	t.Helper()
	attention, err := s.Attention(context.Background())
	if err != nil {
		t.Fatalf("Attention: %v", err)
	}
	for _, entry := range attention {
		if entry.Epic == claimEpic {
			return entry.Tells
		}
	}
	t.Fatalf("Attention has no entry for %s", claimEpic)
	return nil
}

// turnToldOf reports whether turn seq of claimEpic is marked told.
func turnToldOf(t *testing.T, s *Store, seq int64) bool {
	t.Helper()
	epic, err := s.Epic(context.Background(), claimEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	for _, turn := range epic.Turns {
		if turn.Seq == seq {
			return turn.Told
		}
	}
	t.Fatalf("epic %s has no turn %d: %+v", claimEpic, seq, epic.Turns)
	return false
}

func TestPutNoticeMarksItsTurnToldInTheWriteThatStoresTheRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	endTurnOfClaimEpic(t, s)

	if err := s.PutNotice(ctx, toldRowOf(&ToldMark{Epic: claimEpic, Seq: 1})); err != nil {
		t.Fatalf("PutNotice: %v", err)
	}

	if !turnToldOf(t, s, 1) {
		t.Errorf("turn 1 told after the put = false, want true")
	}
	got, err := s.Notices(ctx)
	if err != nil {
		t.Fatalf("Notices: %v", err)
	}
	if want := []NoticeRow{toldRowOf(nil)}; !reflect.DeepEqual(got, want) {
		t.Errorf("Notices = %+v, want %+v: the mark is not a column and reads back as none", got, want)
	}
}

func TestPutNoticeMarksItsTellToldInTheWriteThatStoresTheRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	endTurnOfClaimEpic(t, s)
	if err := s.SetTaskBuildTelling(
		ctx, claimEpic, 1, TaskBuild{}, TaskBuild{}, []string{"synthetic tell"}, claimedAt(),
	); err != nil {
		t.Fatalf("SetTaskBuildTelling: %v", err)
	}

	if err := s.PutNotice(ctx, toldRowOf(&ToldMark{Epic: claimEpic, Seq: 1, Tell: true})); err != nil {
		t.Fatalf("PutNotice: %v", err)
	}

	if tells := untoldTellsOfClaimEpic(t, s); len(tells) != 0 {
		t.Errorf("untold tells after the put = %+v, want none", tells)
	}
	if rows, err := s.Notices(ctx); err != nil || len(rows) != 1 {
		t.Errorf("Notices after the put = %d rows, %v, want the one row", len(rows), err)
	}
}

func TestPutNoticeWithAMarkOfAMissingTurnStoresNothingAndMarksNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	endTurnOfClaimEpic(t, s)

	err := s.PutNotice(ctx, toldRowOf(&ToldMark{Epic: claimEpic, Seq: 2}))
	if !errors.Is(err, ErrNoTurn) {
		t.Fatalf("PutNotice with a mark of turn 2 = %v, want ErrNoTurn", err)
	}
	if rows, err := s.Notices(ctx); err != nil || len(rows) != 0 {
		t.Errorf("Notices after the refused put = %+v, %v, want none", rows, err)
	}
	if turnToldOf(t, s, 1) {
		t.Errorf("turn 1 told after the refused put = true, want false")
	}
}

func TestPutNoticeWithAMarkOfAMissingTellStoresNothingAndMarksNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	endTurnOfClaimEpic(t, s)
	if err := s.SetTaskBuildTelling(
		ctx, claimEpic, 1, TaskBuild{}, TaskBuild{}, []string{"synthetic tell"}, claimedAt(),
	); err != nil {
		t.Fatalf("SetTaskBuildTelling: %v", err)
	}

	err := s.PutNotice(ctx, toldRowOf(&ToldMark{Epic: claimEpic, Seq: 2, Tell: true}))
	if !errors.Is(err, ErrNoTell) {
		t.Fatalf("PutNotice with a mark of tell 2 = %v, want ErrNoTell", err)
	}
	if rows, err := s.Notices(ctx); err != nil || len(rows) != 0 {
		t.Errorf("Notices after the refused put = %+v, %v, want none", rows, err)
	}
	if tells := untoldTellsOfClaimEpic(t, s); len(tells) != 1 {
		t.Errorf("untold tells after the refused put = %+v, want the tell still untold", tells)
	}
}

func TestPutNoticeThatFailsMarksNothing(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	endTurnOfClaimEpic(t, s)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.PutNotice(canceled, toldRowOf(&ToldMark{Epic: claimEpic, Seq: 1})); err == nil {
		t.Fatalf("PutNotice on a canceled context = nil, want an error")
	}
	if turnToldOf(t, s, 1) {
		t.Errorf("turn 1 told after the failed put = true, want false")
	}
}
