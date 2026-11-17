package store_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// attentionOfBuildEpic returns the attention entry of buildEpic, failing when Attention has none.
func attentionOfBuildEpic(ctx context.Context, t *testing.T, s *store.Store) store.EpicAttention {
	t.Helper()
	attention, err := s.Attention(ctx)
	if err != nil {
		t.Fatalf("Attention: %v", err)
	}
	for _, entry := range attention {
		if entry.Epic == buildEpic {
			return entry
		}
	}
	t.Fatalf("Attention has no entry for %s: %+v", buildEpic, attention)
	return store.EpicAttention{}
}

func TestAttentionListsUntoldTellsInSeqOrderAndTheLatestTellTime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openBuildStore(ctx, t)
	first, second := time.Unix(1700000900, 0), time.Unix(1700000950, 0)
	tellBuildEpic(ctx, t, s, "first tell", first)
	tellBuildEpic(ctx, t, s, "second tell", second)

	got := attentionOfBuildEpic(ctx, t, s)
	want := []store.EpicTell{{Seq: 1, Text: "first tell", At: first}, {Seq: 2, Text: "second tell", At: second}}
	if !reflect.DeepEqual(got.Tells, want) {
		t.Errorf("Tells = %+v, want %+v", got.Tells, want)
	}
	if !got.LatestTell.Equal(second) {
		t.Errorf("LatestTell = %v, want %v, the newest tell's time", got.LatestTell, second)
	}
}

func TestMarkTellToldDropsTheTellFromAttentionAndKeepsTheLatestTellTime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openBuildStore(ctx, t)
	first, second := time.Unix(1700000900, 0), time.Unix(1700000950, 0)
	tellBuildEpic(ctx, t, s, "first tell", first)
	tellBuildEpic(ctx, t, s, "second tell", second)

	if err := s.MarkTellTold(ctx, buildEpic, 1); err != nil {
		t.Fatalf("MarkTellTold of tell 1: %v", err)
	}
	got := attentionOfBuildEpic(ctx, t, s)
	want := []store.EpicTell{{Seq: 2, Text: "second tell", At: second}}
	if !reflect.DeepEqual(got.Tells, want) {
		t.Errorf("Tells after MarkTellTold of tell 1 = %+v, want %+v", got.Tells, want)
	}
	if !got.LatestTell.Equal(second) {
		t.Errorf("LatestTell after MarkTellTold = %v, want %v, unchanged", got.LatestTell, second)
	}
}

func TestAttentionHasNoTellsAndAZeroLatestTellForAnEpicWithoutOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openBuildStore(ctx, t)

	got := attentionOfBuildEpic(ctx, t, s)
	if len(got.Tells) != 0 {
		t.Errorf("Tells = %+v, want none", got.Tells)
	}
	if !got.LatestTell.IsZero() {
		t.Errorf("LatestTell = %v, want the zero time", got.LatestTell)
	}
}

func TestMarkTellToldOfAnUnknownTellIsErrNoTell(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openBuildStore(ctx, t)
	tellBuildEpic(ctx, t, s, "only tell", writtenAt())

	if err := s.MarkTellTold(ctx, buildEpic, 9); !errors.Is(err, store.ErrNoTell) {
		t.Errorf("MarkTellTold of a tell that does not exist err = %v, want ErrNoTell", err)
	}
}

func TestAttentionCarriesTheQuestionOfAParkTellAndNoQuestionForAPlainTell(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openBuildStore(ctx, t)
	tellBuildEpic(ctx, t, s, "plain tell", writtenAt())
	if err := s.ParkEpic(ctx, buildEpic, store.ParkClassBranch, "credit ran out", writtenAt()); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}

	got := attentionOfBuildEpic(ctx, t, s)
	if len(got.Tells) != 2 {
		t.Fatalf("Tells = %+v, want the plain tell and the park's tell", got.Tells)
	}
	if got.Tells[0].Question != nil {
		t.Errorf("plain tell Question = %+v, want nil", got.Tells[0].Question)
	}
	var parkQuestion *store.EpicQuestion
	for i := range got.Open {
		if got.Open[i].Label == store.ParkLabel {
			parkQuestion = &got.Open[i]
		}
	}
	if parkQuestion == nil {
		t.Fatalf("Open = %+v, want the park question", got.Open)
	}
	if question := got.Tells[1].Question; question == nil || !reflect.DeepEqual(*question, *parkQuestion) {
		t.Errorf("park tell Question = %+v, want the open park question %+v", question, *parkQuestion)
	}
}
