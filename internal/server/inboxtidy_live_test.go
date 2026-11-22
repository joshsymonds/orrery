package server

import (
	"testing"
	"time"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

// inboxTidyLiveCases are one stored epic each, under invented ids, in the phase given and whether it reads live: a
// met_at_base epic is not live, and ended_with_gaps and stopped epics are.
var inboxTidyLiveCases = []struct {
	id, phase string
	live      bool
}{
	{id: "repoa/tidy-met", phase: "met_at_base", live: false},
	{id: "repoa/tidy-gaps", phase: "ended_with_gaps", live: true},
	{id: "repoa/tidy-stopped", phase: "stopped", live: true},
}

// inboxTidyLiveClient serves inboxTidyLiveCases as stored epics, changed an hour before the fixed clock.
func inboxTidyLiveClient(t *testing.T) orreryv1connect.EpicsClient {
	t.Helper()
	stored := make([]*enginev1.StoredEpic, 0, len(inboxTidyLiveCases))
	for _, c := range inboxTidyLiveCases {
		stored = append(stored, storedEpicOf(c.id, c.phase, epicsAgo(time.Hour)))
	}
	return epicStoreServe(t, t.TempDir(), &epicsFake{}, &epicStoreFake{stored: stored})
}

// TestInboxTidyMetAtBaseListLiveness lists stored epics in met_at_base, ended_with_gaps and stopped: the met_at_base
// epic is not live, and the other two are.
func TestInboxTidyMetAtBaseListLiveness(t *testing.T) {
	t.Parallel()
	client := inboxTidyLiveClient(t)

	listed := make(map[string]*orreryv1.Epic, len(inboxTidyLiveCases))
	for _, epic := range epicsList(t, client) {
		listed[epic.GetId()] = epic
	}
	for _, c := range inboxTidyLiveCases {
		epic, ok := listed[c.id]
		if !ok {
			t.Errorf("List has no %s", c.id)
			continue
		}
		if epic.GetLive() != c.live {
			t.Errorf("List %s in phase %s live = %v, want %v", c.id, c.phase, epic.GetLive(), c.live)
		}
	}
}

// TestInboxTidyMetAtBaseGetLiveness gets stored epics in met_at_base, ended_with_gaps and stopped: the met_at_base
// epic is not live, and the other two are.
func TestInboxTidyMetAtBaseGetLiveness(t *testing.T) {
	t.Parallel()
	client := inboxTidyLiveClient(t)

	for _, c := range inboxTidyLiveCases {
		epic := epicsGet(t, client, c.id).GetEpic()
		if epic.GetId() != c.id || epic.GetOutcome() != c.phase {
			t.Fatalf("Get %s answered id %q outcome %q, want outcome %q",
				c.id, epic.GetId(), epic.GetOutcome(), c.phase)
		}
		if epic.GetLive() != c.live {
			t.Errorf("Get %s in phase %s live = %v, want %v", c.id, c.phase, epic.GetLive(), c.live)
		}
	}
}
