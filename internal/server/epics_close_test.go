package server

import (
	"testing"
	"time"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// TestCloseEpicStoredEpicIsNotLiveAndShowsItsKind answers each closed epic through List and Get: each is not live, and
// its outcome is the closed phase of its kind.
func TestCloseEpicStoredEpicIsNotLiveAndShowsItsKind(t *testing.T) {
	t.Parallel()
	phases := map[string]string{
		"repoa/superseded": "closed_superseded",
		"repoa/shipped":    "closed_shipped",
		"repoa/abandoned":  "closed_abandoned",
	}
	stored := make([]*enginev1.StoredEpic, 0, len(phases))
	for id, phase := range phases {
		stored = append(stored, storedEpicOf(id, phase, epicsAgo(time.Hour)))
	}
	client := epicStoreServe(t, &epicsFake{}, &epicStoreFake{stored: stored})

	listed := 0
	for _, epic := range epicsList(t, client) {
		phase, ok := phases[epic.GetId()]
		if !ok {
			continue
		}
		listed++
		if epic.GetLive() || epic.GetOutcome() != phase {
			t.Errorf("List %s live %v outcome %q, want not live with outcome %q",
				epic.GetId(), epic.GetLive(), epic.GetOutcome(), phase)
		}
	}
	if listed != len(phases) {
		t.Errorf("List named %d of the %d closed epics", listed, len(phases))
	}
	for id, phase := range phases {
		got := epicsGet(t, client, id).GetEpic()
		if got.GetLive() || got.GetOutcome() != phase {
			t.Errorf(
				"Get %s live %v outcome %q, want not live with outcome %q",
				id,
				got.GetLive(),
				got.GetOutcome(),
				phase,
			)
		}
	}
}
