package server

import (
	"testing"
	"time"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// steererHeldClasses returns the park classes the owner answered directly before every park went to the epic's steerer
// first: each opens a park question.
func steererHeldClasses() []store.ParkClass {
	return []store.ParkClass{
		store.ParkClassReleaseUndoNone, store.ParkClassReleaseOwnerStop, store.ParkClassBranch, store.ParkClassRefMove,
		store.ParkClassMetAtBase, store.ParkClassOrchestrator, store.ParkClassRetryBound,
	}
}

// steerSyntheticEpic adds session as an engine session in state and makes it steer the synthetic epic, as the owner
// does.
func steerSyntheticEpic(t *testing.T, st *store.Store, session, state string) {
	t.Helper()
	at := time.Unix(1_700_000_050, 0)
	add := store.EngineAdd{ID: session, Dir: t.TempDir(), State: state, Kind: store.KindEngine}
	if err := st.ApplyEngine(t.Context(), []store.EngineOp{add}, at); err != nil {
		t.Fatalf("ApplyEngine add %s: %v", session, err)
	}
	if _, err := st.MoveEpic(t.Context(), syntheticEpicID, session, store.Maker{By: store.MakerOwner}, at); err != nil {
		t.Fatalf("MoveEpic to %s: %v", session, err)
	}
}

// parkQuestionOf returns the one open question Get served, and fails unless it is the park question.
func parkQuestionOf(t *testing.T, got *orreryv1.EpicsGetResponse) *orreryv1.EpicQuestion {
	t.Helper()
	if len(got.GetQuestions()) != 1 || got.GetQuestions()[0].GetLabel() != store.ParkLabel {
		t.Fatalf("questions = %+v, want the one open park question", got.GetQuestions())
	}
	return got.GetQuestions()[0]
}

// listedSynthetic returns the synthetic epic as List serves it.
func listedSynthetic(t *testing.T, got []*orreryv1.Epic) *orreryv1.Epic {
	t.Helper()
	for _, epic := range got {
		if epic.GetId() == syntheticEpicID {
			return epic
		}
	}
	t.Fatalf("List = %+v, want the synthetic epic", got)
	return nil
}

// TestEpicsGetSteererHoldsEachQuestionPark covers the rule that nothing goes over the steerer's head, as Get serves it
// to the owner's clients. A park of each question class on an epic a steering session hears is steerer_parked, with its
// park question steerer_held, so the clients do not ask the owner: no "wait on you" need, no count. Once the steerer
// escalates the question, neither holds, and the question carries the escalation the clients' line draws. On an epic
// with no steerer, and one whose steerer is stopped, the park is the owner's directly.
func TestEpicsGetSteererHoldsEachQuestionPark(t *testing.T) {
	t.Parallel()
	for _, class := range steererHeldClasses() {
		t.Run(string(class), func(t *testing.T) {
			t.Parallel()
			at := time.Unix(1_700_000_100, 0)
			reason := "a " + string(class) + " catastrophe"

			t.Run("heard", func(t *testing.T) {
				t.Parallel()
				ctx := t.Context()
				st := openEpicAnswerStore(t)
				steerSyntheticEpic(t, st, "steerer", "working")
				if err := st.ParkEpic(ctx, syntheticEpicID, class, reason, at); err != nil {
					t.Fatalf("ParkEpic: %v", err)
				}
				client := epicStoreServe(t, &epicsFake{}, serveEngineEpics(t, st))

				got := epicsGet(t, client, syntheticEpicID)
				if !got.GetSteererParked() || got.GetPlannerParked() || got.GetParked() != reason {
					t.Errorf("steerer_parked %v planner_parked %v parked %q, want the steerer's park %q",
						got.GetSteererParked(), got.GetPlannerParked(), got.GetParked(), reason)
				}
				if question := parkQuestionOf(t, got); !question.GetSteererHeld() {
					t.Errorf("park question %+v, want it steerer_held", question)
				}
				if listed := listedSynthetic(t, epicsList(t, client)); !listed.GetSteererHeard() {
					t.Errorf("listed epic %+v, want steerer_heard", listed)
				}

				const why = "this needs the owner"
				maker := store.Maker{By: store.MakerConversation, Session: "steerer"}
				if _, err := st.EscalateQuestion(ctx, syntheticEpicID, 1, why, maker, at); err != nil {
					t.Fatalf("EscalateQuestion: %v", err)
				}
				got = epicsGet(t, client, syntheticEpicID)
				if got.GetSteererParked() || got.GetParked() != reason {
					t.Errorf("steerer_parked %v parked %q after the escalation, want the owner's park %q",
						got.GetSteererParked(), got.GetParked(), reason)
				}
				question := parkQuestionOf(t, got)
				if question.GetSteererHeld() || question.GetEscalated() != at.Unix() ||
					question.GetEscalationReason() != why || question.GetEscalationBy() != store.MakerConversation ||
					question.GetEscalationSession() != "steerer" {
					t.Errorf("park question %+v after the escalation, want the owner's, escalated by steerer for %q",
						question, why)
				}
			})
			for _, unheard := range []struct {
				name  string
				steer func(t *testing.T, st *store.Store)
			}{
				{name: "no steerer", steer: func(*testing.T, *store.Store) {}},
				{name: "steerer stopped", steer: func(t *testing.T, st *store.Store) {
					t.Helper()
					steerSyntheticEpic(t, st, "steerer", "stopped")
				}},
			} {
				t.Run(unheard.name, func(t *testing.T) {
					t.Parallel()
					st := openEpicAnswerStore(t)
					unheard.steer(t, st)
					if err := st.ParkEpic(t.Context(), syntheticEpicID, class, reason, at); err != nil {
						t.Fatalf("ParkEpic: %v", err)
					}
					client := epicStoreServe(t, &epicsFake{}, serveEngineEpics(t, st))

					got := epicsGet(t, client, syntheticEpicID)
					if got.GetSteererParked() || got.GetPlannerParked() || got.GetParked() != reason {
						t.Errorf("steerer_parked %v planner_parked %v parked %q, want the owner's park %q",
							got.GetSteererParked(), got.GetPlannerParked(), got.GetParked(), reason)
					}
					if question := parkQuestionOf(t, got); question.GetSteererHeld() {
						t.Errorf("park question %+v, want it not steerer_held", question)
					}
					if listed := listedSynthetic(t, epicsList(t, client)); listed.GetSteererHeard() {
						t.Errorf("listed epic %+v, want not steerer_heard", listed)
					}
				})
			}
		})
	}
}
