package server

import (
	"testing"
	"time"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

func TestEpicsGetRepositoryPrefersOrrerydContract(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHead(t, root, "repoa", "shared", `{"outcome":"shipped","workspace":"/synthetic/record-workspace"}`, epicsNow())
	stored := storedEpicOf("repoa/shared", "accepted", epicsAgo(2*time.Hour))
	stored.Contract.Repository = "/synthetic/orreryd-repo"
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{stored}}
	client := epicStoreServe(t, root, &epicsFake{}, store)

	if got := epicsGet(t, client, "repoa/shared").GetRepository(); got != "/synthetic/orreryd-repo" {
		t.Errorf("repository = %q, want %q", got, "/synthetic/orreryd-repo")
	}
}

func TestEpicsGetRepositoryFallsBackToRecordWorkspace(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	head := `{"outcome":"shipped","workspace":"/synthetic/record-workspace"}`
	writeHead(t, root, "repoa", "shared", head, epicsNow())
	writeHead(t, root, "repoa", "record", head, epicsNow())
	stored := storedEpicOf("repoa/shared", "accepted", epicsAgo(2*time.Hour))
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{stored}}
	client := epicStoreServe(t, root, &epicsFake{}, store)

	for _, id := range []string{"repoa/shared", "repoa/record"} {
		if got := epicsGet(t, client, id).GetRepository(); got != "/synthetic/record-workspace" {
			t.Errorf("%s repository = %q, want %q", id, got, "/synthetic/record-workspace")
		}
	}
}

func TestEpicsGetRepositoryEmptyWithoutWorkspace(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHead(t, root, "repoa", "null", `{"outcome":"shipped","workspace":null}`, epicsNow())
	writeHead(t, root, "repoa", "absent", `{"outcome":"shipped"}`, epicsNow())
	client := epicsServe(t, root, &epicsFake{})

	for _, id := range []string{"repoa/null", "repoa/absent"} {
		if got := epicsGet(t, client, id).GetRepository(); got != "" {
			t.Errorf("%s repository = %q, want empty", id, got)
		}
	}
}
