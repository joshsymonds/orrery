package server

import (
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestEpicsGetOrchestratorParkGivesNeedsYouNoQuestionAndThePlannerMarkForAPlannerPark shows what Get serves for a park
// the store holds. web/src/needs.ts epicNeeds raises a need from an epic's open questions and from a park that is not
// plannerParked. So a planner park that reaches Get with no open question and with planner_parked true raises no
// Needs you need. An owner park keeps its open question and is not planner_parked, and both keep their reason in
// parked.
func TestEpicsGetOrchestratorParkGivesNeedsYouNoQuestionAndThePlannerMarkForAPlannerPark(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		class         store.ParkClass
		reason        string
		wantPlanner   bool
		wantQuestions int
	}{
		{
			name:          "a node park (no verdict twice) has no open question and is planner_parked",
			class:         store.ParkClassNode,
			reason:        "no verdict twice",
			wantPlanner:   true,
			wantQuestions: 0,
		},
		{
			name:          "a branch park keeps its open question and is not planner_parked",
			class:         store.ParkClassBranch,
			reason:        "epic branch is gone",
			wantPlanner:   false,
			wantQuestions: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			st := openEpicAnswerStore(t)
			at := time.Unix(1_700_000_100, 0)
			if err := st.ParkEpic(t.Context(), syntheticEpicID, tt.class, tt.reason, at); err != nil {
				t.Fatalf("ParkEpic: %v", err)
			}
			client := epicStoreServe(t, &epicsFake{}, serveEngineEpics(t, st))

			got := epicsGet(t, client, syntheticEpicID)
			if got.GetPlannerParked() != tt.wantPlanner {
				t.Errorf("planner_parked = %v, want %v", got.GetPlannerParked(), tt.wantPlanner)
			}
			if got.GetParked() != tt.reason {
				t.Errorf("parked = %q, want the park reason %q", got.GetParked(), tt.reason)
			}
			if n := len(got.GetQuestions()); n != tt.wantQuestions {
				t.Errorf("open questions = %d, want %d", n, tt.wantQuestions)
			}
		})
	}
}
