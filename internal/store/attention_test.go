package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// attentionEpics returns the epic ids of attention in order.
func attentionEpics(attention []EpicAttention) []string {
	epics := make([]string, 0, len(attention))
	for _, entry := range attention {
		epics = append(epics, entry.Epic)
	}
	return epics
}

// resetsAt is the reset time the free-turn test stores on a usage limit.
func resetsAt() time.Time {
	return time.Unix(1700003600, 0)
}

func TestAttentionOmitsAnEpicWithNoTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const withTurn = "orrery-1a2b3c4/epic-other"
	createEpic(t, s, fullEpic(claimEpic))
	createEpic(t, s, fullEpic(withTurn))
	if _, err := s.ClaimTurn(ctx, withTurn, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}

	got, err := s.Attention(ctx)
	if err != nil {
		t.Fatalf("Attention: %v", err)
	}
	if epics := attentionEpics(got); !reflect.DeepEqual(epics, []string{withTurn}) {
		t.Errorf("Attention epics = %v, want only %s, the epic with a turn", epics, withTurn)
	}
}

func TestAttentionReportsParkPauseOpenQuestionsAndTheLatestEndedTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.CompleteTurn(ctx, id, 1, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	insertPendingTrigger(t, s, id, "t3", "later trigger")
	if _, err := s.ClaimTurn(ctx, id, claimedAt().Add(time.Minute)); err != nil {
		t.Fatalf("second ClaimTurn: %v", err)
	}
	later := EpicPlan{Questions: []string{"Is the second turn right?", "Which module?"}, Outcome: "wait"}
	if err := s.CompleteTurn(ctx, id, 2, later, completedAt().Add(time.Minute)); err != nil {
		t.Fatalf("second CompleteTurn: %v", err)
	}
	insertPendingTrigger(t, s, id, "t4", "third trigger")
	if _, err := s.ClaimTurn(ctx, id, claimedAt().Add(2*time.Minute)); err != nil {
		t.Fatalf("third ClaimTurn: %v", err)
	}
	if err := sqlcgen.New(s.db).SetEpicQuestionAnswered(ctx, sqlcgen.SetEpicQuestionAnsweredParams{
		Epic: id, Seq: 1,
	}); err != nil {
		t.Fatalf("answer question 1: %v", err)
	}
	if err := s.PauseEpic(ctx, id); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}
	parkEpic(t, s, id, "credit ran out")

	got, err := s.Attention(ctx)
	if err != nil {
		t.Fatalf("Attention: %v", err)
	}
	want := []EpicAttention{{
		Epic: id, Title: "Synthetic epic", Parked: "credit ran out", Paused: true,
		Open: []EpicQuestion{
			{Seq: 2, Turn: 2, Text: "Is the second turn right?", State: "open"},
			{Seq: 3, Turn: 2, Text: "Which module?", State: "open"},
		},
		Latest: EpicTurn{
			Epic: id, Seq: 2, State: "succeeded", Seen: int64(len(seededDecisions())), Outcome: "wait",
			Started: claimedAt().Add(time.Minute), Ended: completedAt().Add(time.Minute),
		},
		LatestAsked: 2,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Attention = %+v, want %+v", got, want)
	}
}

func TestMarkToldSetsToldAndRefusesAMissingTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.CompleteTurn(ctx, id, 1, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	if err := s.MarkTold(ctx, id, 1); err != nil {
		t.Fatalf("MarkTold: %v", err)
	}
	got, err := s.Attention(ctx)
	if err != nil {
		t.Fatalf("Attention: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Attention = %+v, want one entry for %s", got, id)
	}
	if !got[0].Latest.Told {
		t.Errorf("Latest.Told after MarkTold = false, want true")
	}

	if err := s.MarkTold(ctx, id, 9); !errors.Is(err, ErrNoTurn) {
		t.Errorf("MarkTold of a turn that does not exist err = %v, want ErrNoTurn", err)
	}
}

func TestFreeTurnStoresItsResetsAndLatestReadsThem(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const other = "orrery-1a2b3c4/epic-other"
	createEpic(t, s, fullEpic(claimEpic))
	createEpic(t, s, fullEpic(other))
	if _, err := s.ClaimTurn(ctx, claimEpic, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn of %s: %v", claimEpic, err)
	}
	if err := s.FreeTurn(ctx, claimEpic, 1, TurnFree{Reason: "usage limit", Pause: true, Resets: resetsAt()},
		completedAt()); err != nil {
		t.Fatalf("FreeTurn with Resets: %v", err)
	}
	if _, err := s.ClaimTurn(ctx, other, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn of %s: %v", other, err)
	}
	if err := s.FreeTurn(ctx, other, 1, TurnFree{Reason: "stopped"}, completedAt()); err != nil {
		t.Fatalf("FreeTurn without Resets: %v", err)
	}

	got, err := s.Attention(ctx)
	if err != nil {
		t.Fatalf("Attention: %v", err)
	}
	if epics := attentionEpics(got); !reflect.DeepEqual(epics, []string{other, claimEpic}) {
		t.Fatalf("Attention epics = %v, want %v", epics, []string{other, claimEpic})
	}
	if !got[1].Latest.Resets.Equal(resetsAt()) {
		t.Errorf("Latest.Resets of %s = %v, want %v", claimEpic, got[1].Latest.Resets, resetsAt())
	}
	if !got[0].Latest.Resets.IsZero() {
		t.Errorf("Latest.Resets of %s without Resets = %v, want zero", other, got[0].Latest.Resets)
	}
}
