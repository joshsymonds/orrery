package store

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// decisionMakerTrigger returns the trigger of claimEpic with id, as the store holds it, and fails the test when none has
// that id.
func decisionMakerTrigger(t *testing.T, s *Store, id string) sqlcgen.EpicTrigger {
	t.Helper()
	triggers := triggersOf(t, s)
	i := slices.IndexFunc(triggers, func(trigger sqlcgen.EpicTrigger) bool { return trigger.ID == id })
	if i < 0 {
		t.Fatalf("triggers = %+v, want one with id %s", triggers, id)
	}
	return triggers[i]
}

// TestDecisionMakerKickByConversation checks that a kick a conversation makes is the decision kick:<seq>, and that its
// trigger's id and cause name the conversation and not the owner.
func TestDecisionMakerKickByConversation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))

	if err := s.KickEpic(ctx, claimEpic, "Try the sqlite plan", Maker{By: MakerConversation, Session: "engine-7"},
		claimedAt()); err != nil {
		t.Fatalf("KickEpic: %v", err)
	}
	epic := storedEpicRow(t, s)
	last := epic.Decisions[len(epic.Decisions)-1]
	if wantID := fmt.Sprintf("kick:%d", last.Seq); last.ID != wantID || last.Text != "Try the sqlite plan" {
		t.Errorf("kick decision = %+v, want id %s with the kick text", last, wantID)
	}
	want := sqlcgen.EpicTrigger{
		Epic: claimEpic, ID: last.ID, Cause: "kick by conversation engine-7: Try the sqlite plan",
		State: "pending", Turn: 0, Created: claimedAt().Unix(),
	}
	if got := decisionMakerTrigger(t, s, last.ID); !reflect.DeepEqual(got, want) {
		t.Errorf("kick trigger = %+v, want %+v", got, want)
	}
}

// TestDecisionMakerKickByOwner checks that an owner's kick keeps the decision owner-kick:<seq> and the cause
// `owner kick: <text>`.
func TestDecisionMakerKickByOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))

	if err := s.KickEpic(ctx, claimEpic, "Try the sqlite plan", Maker{By: MakerOwner}, claimedAt()); err != nil {
		t.Fatalf("KickEpic: %v", err)
	}
	epic := storedEpicRow(t, s)
	last := epic.Decisions[len(epic.Decisions)-1]
	if wantID := fmt.Sprintf("owner-kick:%d", last.Seq); last.ID != wantID || last.Text != "Try the sqlite plan" {
		t.Errorf("kick decision = %+v, want id %s with the kick text", last, wantID)
	}
	want := sqlcgen.EpicTrigger{
		Epic: claimEpic, ID: last.ID, Cause: "owner kick: Try the sqlite plan",
		State: "pending", Turn: 0, Created: claimedAt().Unix(),
	}
	if got := decisionMakerTrigger(t, s, last.ID); !reflect.DeepEqual(got, want) {
		t.Errorf("kick trigger = %+v, want %+v", got, want)
	}
}

// TestDecisionMakerGapsCorrectionByConversation checks that a gaps question a conversation answers correct adds a
// trigger whose cause names the conversation and not the owner.
func TestDecisionMakerGapsCorrectionByConversation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	endClaimEpicWithGaps(t, s)

	if _, err := s.AnswerQuestion(ctx, claimEpic, "conv-correct", "Keep the old tasks", 1, ChoiceCorrect,
		Maker{By: MakerConversation, Session: "engine-7"}, claimedAt()); err != nil {
		t.Fatalf("AnswerQuestion correct: %v", err)
	}
	want := sqlcgen.EpicTrigger{
		Epic: claimEpic, ID: "gaps:conv-correct",
		Cause: "correction of the gaps by conversation engine-7: Keep the old tasks",
		State: "pending", Turn: 0, Created: claimedAt().Unix(),
	}
	if got := decisionMakerTrigger(t, s, "gaps:conv-correct"); !reflect.DeepEqual(got, want) {
		t.Errorf("correction trigger = %+v, want %+v", got, want)
	}
}

// TestDecisionMakerReleaseWithGapsByConversation checks that a conversation's release of an epic that ended with gaps
// records the release-ended-with-gaps decision with text that names the conversation and not the owner.
func TestDecisionMakerReleaseWithGapsByConversation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	endClaimEpicWithGaps(t, s)

	if err := s.ReleaseEpic(ctx, claimEpic, nil, Maker{By: MakerConversation, Session: "engine-7"},
		claimedAt()); err != nil {
		t.Fatalf("ReleaseEpic: %v", err)
	}
	epic := storedEpicRow(t, s)
	last := epic.Decisions[len(epic.Decisions)-1]
	want := "conversation engine-7 released epic " + claimEpic + ", which ended with gaps"
	if last.ID != "release-ended-with-gaps" || last.Text != want {
		t.Errorf("release decision = %+v, want id release-ended-with-gaps with text %q", last, want)
	}
}
