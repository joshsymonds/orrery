package server

import (
	"testing"
	"time"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestEpicsGetSupersededByServesTheStoredReplacement answers the superseded_by a closed stored epic holds, as Get's
// superseded_by.
func TestEpicsGetSupersededByServesTheStoredReplacement(t *testing.T) {
	t.Parallel()
	closed := storedEpicOf("repoa/shared", store.PhaseClosedSuperseded, epicsAgo(2*time.Hour))
	closed.SupersededBy = "repoa/successor"
	fake := &epicStoreFake{stored: []*enginev1.StoredEpic{closed}}
	client := epicStoreServe(t, &epicsFake{}, fake)

	if got := epicsGet(t, client, "repoa/shared").GetSupersededBy(); got != "repoa/successor" {
		t.Errorf("superseded by = %q, want %q", got, "repoa/successor")
	}
}
