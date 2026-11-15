package server

import (
	"testing"
	"time"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

func TestEpicsGetServesStoredSummary(t *testing.T) {
	t.Parallel()
	stored := storedEpicOf("repoa/shared", "accepted", epicsAgo(2*time.Hour))
	stored.Summary = "synthetic summary paragraph"
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{stored}}
	client := epicStoreServe(t, t.TempDir(), &epicsFake{}, store)

	if got := epicsGet(t, client, "repoa/shared").GetSummary(); got != "synthetic summary paragraph" {
		t.Errorf("summary = %q, want %q", got, "synthetic summary paragraph")
	}
}

func TestEpicsGetRecordOnlyEpicHasEmptySummary(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHead(t, root, "repoa", "record", `{"outcome":"shipped"}`, epicsNow())
	other := storedEpicOf("repoa/other", "accepted", epicsAgo(time.Hour))
	other.Summary = "synthetic other summary"
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{other}}
	client := epicStoreServe(t, root, &epicsFake{}, store)

	if got := epicsGet(t, client, "repoa/record").GetSummary(); got != "" {
		t.Errorf("record epic summary = %q, want empty", got)
	}
}
