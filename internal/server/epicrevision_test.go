package server

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// listedEpic lists the epics through client and returns the one with id, failing the test when List lacks it.
func listedEpic(t *testing.T, client orreryv1connect.EpicsClient, id string) *orreryv1.Epic {
	t.Helper()
	for _, epic := range epicsList(t, client) {
		if epic.GetId() == id {
			return epic
		}
	}
	t.Fatalf("List lacks %s", id)
	return nil
}

// assertRevisionMoved fails the test unless after, the epic as List answers it after a write, differs from before and
// carries a later revision, which is what makes the page's tick ask Epics.Get for the epic.
func assertRevisionMoved(t *testing.T, what string, before, after *orreryv1.Epic) {
	t.Helper()
	if after.GetRevision() <= before.GetRevision() {
		t.Errorf("%s: revision %d, want past %d", what, after.GetRevision(), before.GetRevision())
	}
	if proto.Equal(before, after) {
		t.Errorf("%s: List answers the epic unchanged: %v", what, after)
	}
}

// TestEpicsListRevisionMovesWithAPlannerQuestionAndAPark runs Epics.List over orreryd's Epics service and a real store,
// and proves that a planner turn that asks a question, and a park, each change what List answers for the epic, by its
// revision, though neither starts or ends a step. The page asks Epics.Get on its next tick for an epic whose listed
// message changed, so each reaches what the page shows within a tick.
func TestEpicsListRevisionMovesWithAPlannerQuestionAndAPark(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := openEpicAnswerStore(t)
	client := epicStoreServe(t, t.TempDir(), &epicsFake{}, serveEngineEpics(t, st))

	turn, err := st.ClaimTurn(ctx, syntheticEpicID, time.Unix(1_700_000_100, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	beforeQuestion := listedEpic(t, client, syntheticEpicID)
	plan := store.EpicPlan{
		Outline: "Synthetic outline.",
		Outcome: "Synthetic outcome.",
		Questions: []store.EpicQuestionInput{{
			Label: "synthetic-question", Text: "Synthetic question?",
			Choices: []store.EpicChoice{
				{ID: "synthetic-a", Does: "Synthetic choice A."},
				{ID: "synthetic-b", Does: "Synthetic choice B."},
			},
			Recommended: "synthetic-a", Why: "Synthetic reason.", Confidence: "low",
		}},
	}
	if err = st.CompleteTurn(ctx, syntheticEpicID, turn.Seq, plan, time.Unix(1_700_000_200, 0)); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	afterQuestion := listedEpic(t, client, syntheticEpicID)
	assertRevisionMoved(t, "a planner question", beforeQuestion, afterQuestion)

	if err = st.ParkEpic(ctx, syntheticEpicID, store.ParkClassBranch, "Synthetic park.",
		time.Unix(1_700_000_300, 0)); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}
	assertRevisionMoved(t, "a park", afterQuestion, listedEpic(t, client, syntheticEpicID))
}
