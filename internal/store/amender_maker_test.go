package store

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// makerAmendment asks, as maker, for R1 of the amended epic to hold item, and returns the amendment as it is stored.
func makerAmendment(t *testing.T, s *Store, maker Maker, item evidence.Item) EvidenceAmendment {
	t.Helper()
	amended, err := s.AmendEvidence(context.Background(), amendEpic, Amendment{
		Requirement: "R1", Item: item, Reason: "the test moved to cmd/orreryd",
	}, maker, amendAt())
	if err != nil {
		t.Fatalf("AmendEvidence as %+v: %v", maker, err)
	}
	return storedAmendment(t, s, amended.Seq)
}

// amendRefused asks, as maker, for R1 of the amended epic to hold item, and returns the error the store gives. It fails
// the test when the ask stores an amendment or logs an action of the epic.
func amendRefused(t *testing.T, s *Store, maker Maker, item evidence.Item) error {
	t.Helper()
	actions := len(actionLogOf(t, s, amendEpic))
	_, err := s.AmendEvidence(context.Background(), amendEpic, Amendment{
		Requirement: "R1", Item: item, Reason: "the test moved to cmd/orreryd",
	}, maker, amendAt())
	if amendments := closeRow(t, s, amendEpic).Amendments; len(amendments) != 0 {
		t.Errorf("amendments after the refusal = %+v, want none stored", amendments)
	}
	if got := len(actionLogOf(t, s, amendEpic)); got != actions {
		t.Errorf("actions logged after the refusal = %d, want the %d before it", got, actions)
	}
	return err
}

// TestAmenderMakerOwner checks that an amendment the owner makes is stored with the owner as its amender.
func TestAmenderMakerOwner(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	amended := makerAmendment(t, s, Maker{By: MakerOwner}, captureAmendItem())
	if amended.Amender != AmenderOwner {
		t.Errorf("amender of the owner's amendment = %q, want %q", amended.Amender, AmenderOwner)
	}
}

// TestAmenderMakerConversation checks that an amendment a conversation makes is stored with conversation as its amender.
func TestAmenderMakerConversation(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	amended := makerAmendment(t, s, Maker{By: MakerConversation, Session: "engine-1"}, captureAmendItem())
	if amended.Amender != AmenderConversation {
		t.Errorf("amender of the conversation's amendment = %q, want %q", amended.Amender, AmenderConversation)
	}
}

// TestAmenderMakerOrreryd checks that an amendment orreryd makes is stored with orreryd as its amender.
func TestAmenderMakerOrreryd(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	amended := makerAmendment(t, s, Maker{By: MakerOrreryd}, captureAmendItem())
	if amended.Amender != AmenderOrreryd {
		t.Errorf("amender of orreryd's amendment = %q, want %q", amended.Amender, AmenderOrreryd)
	}
}

// TestAmenderMakerPlanner checks that an amend_evidence action carried out stores its amendment with the planner as its
// amender.
func TestAmenderMakerPlanner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	action := storeActionsOf(t, s, amendAction(captureAmendItem()))[0]
	if _, err := s.CarryOutAction(ctx, action, amendAt()); err != nil {
		t.Fatalf("CarryOutAction: %v", err)
	}
	amendments := closeRow(t, s, amendEpic).Amendments
	if len(amendments) != 1 || amendments[0].Amender != AmenderPlanner {
		t.Errorf("amendments after the planner's action = %+v, want the one amendment by the planner", amendments)
	}
}

// TestAmenderMakerConversationChangesConclusion checks that a conversation's amendment that changes the conclusion of R1 is
// stored checking, with the conversation as its amender, and that R1 still holds its old item while its red run is judged.
func TestAmenderMakerConversationChangesConclusion(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	amended := makerAmendment(t, s, Maker{By: MakerConversation, Session: "engine-1"}, newAmendItem())
	if amended.Amender != AmenderConversation {
		t.Errorf("amender of the conversation's conclusion change = %q, want %q", amended.Amender, AmenderConversation)
	}
	if amended.State != AmendmentChecking {
		t.Errorf("state of the conversation's conclusion change = %s, want checking", amended.State)
	}
	if item := r1Item(t, s); !reflect.DeepEqual(item, oldAmendItem()) {
		t.Errorf("R1 item while checking = %+v, want the old item %+v", item, oldAmendItem())
	}
}

// TestAmenderMakerConversationChangesKind checks that a conversation's amendment that changes the kind of R1 is stored
// checking, with the conversation as its amender, and that R1 still holds its old item while its red run is judged.
func TestAmenderMakerConversationChangesKind(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	kind := captureAmendItem()
	kind.Kind, kind.Tests = evidence.KindCapture, nil
	amended := makerAmendment(t, s, Maker{By: MakerConversation, Session: "engine-1"}, kind)
	if amended.Amender != AmenderConversation {
		t.Errorf("amender of the conversation's kind change = %q, want %q", amended.Amender, AmenderConversation)
	}
	if amended.State != AmendmentChecking {
		t.Errorf("state of the conversation's kind change = %s, want checking", amended.State)
	}
	if item := r1Item(t, s); !reflect.DeepEqual(item, oldAmendItem()) {
		t.Errorf("R1 item while checking = %+v, want the old item %+v", item, oldAmendItem())
	}
}

// TestAmenderMakerConversationAppliedTellsOwner checks that a conversation's amendment that keeps the conclusion and kind
// of R1 applies, and that its tell tells the owner of it, naming the amendment.
func TestAmenderMakerConversationAppliedTellsOwner(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createAmendEpic(t, s)
	amended := makerAmendment(t, s, Maker{By: MakerConversation, Session: "engine-1"}, captureAmendItem())
	if amended.State != AmendmentApplied {
		t.Fatalf("state of the conversation's amendment = %s, want applied", amended.State)
	}
	want := "Evidence of R1 amended by the conversation (A1): the next integrate captures the new item at its candidate"
	if tells := amendTells(t, s); !slices.Equal(tells, []string{want}) {
		t.Errorf("tells after the conversation's applied amendment = %q, want [%q]", tells, want)
	}
}

// TestAmenderMakerNoMakerRefused checks that an amendment whose maker has no By, or one that is none of the owner, a
// conversation or orreryd, is a RefusalError naming that maker, and stores nothing.
func TestAmenderMakerNoMakerRefused(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		maker   Maker
		refusal string
	}{
		{"no maker", Maker{}, `the amendment's maker "" is not the owner, a conversation or orreryd`},
		{"another maker", Maker{By: "planner"},
			`the amendment's maker "planner" is not the owner, a conversation or orreryd`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := openEpicStore(t)
			createAmendEpic(t, s)
			requireRefusal(t, amendRefused(t, s, tc.maker, captureAmendItem()), tc.refusal)
		})
	}
}
