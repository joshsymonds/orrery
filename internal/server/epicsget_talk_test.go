package server

import (
	"testing"
	"time"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

func TestEpicsGetRepositoryPrefersOrrerydContract(t *testing.T) {
	t.Parallel()
	stored := storedEpicOf("repoa/shared", "accepted", epicsAgo(2*time.Hour))
	stored.Contract.Repository = "/synthetic/orreryd-repo"
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{stored}}
	client := epicStoreServe(t, &epicsFake{}, store)

	if got := epicsGet(t, client, "repoa/shared").GetRepository(); got != "/synthetic/orreryd-repo" {
		t.Errorf("repository = %q, want %q", got, "/synthetic/orreryd-repo")
	}
}
