package server_test

import (
	"path/filepath"
	"testing"
	"time"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// The steering tests' epics are steered by eventEngine and created by steerCreator, a second engine session. Each one
// holds one of the four epic events: an open question, a park, a release and an end.
const (
	steerCreator      = "engine-2"
	steerTitle        = "Steer epic"
	steerQuestionEpic = "orrery-1a2b3c4/steer-a-question"
	steerParkEpic     = "orrery-1a2b3c4/steer-b-park"
	steerReleaseEpic  = "orrery-1a2b3c4/steer-c-release"
	steerEndEpic      = "orrery-1a2b3c4/steer-d-end"
)

// addSteeredEpics writes eventEngine and steerCreator, then the four steering epics. steerCreator creates each epic,
// and eventEngine steers each. The question epic has one open question from its planner turn, the park epic is parked,
// the release epic is released and the end epic is done.
func addSteeredEpics(t *testing.T, s *store.Store) {
	t.Helper()
	ctx := t.Context()
	at := time.Unix(eventAt, 0)
	addEngine(t, s)
	applyEngineOps(t, s, store.EngineAdd{ID: steerCreator, Dir: "/b", State: "idle"})
	for _, epic := range []struct{ id, phase string }{
		{steerQuestionEpic, "accepted"},
		{steerParkEpic, "accepted"},
		{steerReleaseEpic, "released"},
		{steerEndEpic, "done"},
	} {
		row := store.EpicRow{
			ID: epic.id, Repository: "/work/steer", Title: steerTitle, Phase: epic.phase, Created: at, Changed: at,
			Steerer: eventEngine,
		}
		if err := s.CreateEpic(ctx, row); err != nil {
			t.Fatalf("CreateEpic %s: %v", epic.id, err)
		}
		recorded, err := s.RecordEpicCall(ctx, epic.id, store.EpicCaller{Session: steerCreator}, "epic_create", at)
		if err != nil || !recorded {
			t.Fatalf("RecordEpicCall %s = %v, %v, want true, nil", epic.id, recorded, err)
		}
	}

	turn, err := s.ClaimTurn(ctx, steerQuestionEpic, at)
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	plan := store.EpicPlan{
		Outline: "Synthetic outline.",
		Outcome: "wait",
		Questions: []store.EpicQuestionInput{{
			Label: "steer-question", Text: "Synthetic question?",
			Choices: []store.EpicChoice{
				{ID: "steer-a", Does: "Synthetic choice A."},
				{ID: "steer-b", Does: "Synthetic choice B."},
			},
			Recommended: "steer-a", Why: "Synthetic reason.", Confidence: "low",
		}},
	}
	if err = s.CompleteTurn(ctx, steerQuestionEpic, turn.Seq, plan, at); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	if err = s.ParkEpic(ctx, steerParkEpic, store.ParkClassBranch, "Synthetic park.", at); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}
}

// steerEvents is the pending list of the steering epics: the open question, the park, the release and the end, in epic
// order, each labeled waits.
func steerEvents(waits string) []*orreryv1.PendingEvent {
	return []*orreryv1.PendingEvent{
		{Id: store.EpicQuestionEvent(steerQuestionEpic, 1), Kind: "question", Epic: steerQuestionEpic,
			Title: steerTitle, Waits: waits},
		{Id: store.EpicQuestionEvent(steerParkEpic, 1), Kind: "park", Epic: steerParkEpic,
			Title: steerTitle, Waits: waits},
		{Id: store.EpicPhaseEvent(steerReleaseEpic, "released"), Kind: "release", Epic: steerReleaseEpic,
			Title: steerTitle, Waits: waits},
		{Id: store.EpicPhaseEvent(steerEndEpic, "done"), Kind: "end", Epic: steerEndEpic,
			Title: steerTitle, Waits: waits},
	}
}

// TestSessionWatchSteererEventsPendingNamesEventsOnlyForTheSteerer: the watch of the steering session lists each event of
// the steering epics, and the watch of its creator lists none.
func TestSessionWatchSteererEventsPendingNamesEventsOnlyForTheSteerer(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addSteeredEpics(t, s)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, nil, 0))

	steering := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	wantPendingEvents(t, steering.next(t), steerEvents(cardFree)...)

	creator := openSessionStream(t, client, sessionReq(steerCreator, 0, 0))
	wantPendingEvents(t, creator.next(t))
}
