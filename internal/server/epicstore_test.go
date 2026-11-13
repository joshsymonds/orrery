package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

// epicStoreFake is orreryd's Epics client for the merge tests. ListEpics lists each stored epic once, and GetEpic
// answers the stored epic of its id, or not_found when none holds it. listErr and getErr, when set, fail their call.
type epicStoreFake struct {
	stored  []*enginev1.StoredEpic
	listErr error
	getErr  error
}

func (f *epicStoreFake) ListEpics(
	context.Context,
	*connect.Request[enginev1.ListEpicsRequest],
) (*connect.Response[enginev1.ListEpicsResponse], error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	summaries := make([]*enginev1.EpicSummary, 0, len(f.stored))
	for _, epic := range f.stored {
		summaries = append(summaries, &enginev1.EpicSummary{
			Id:      epic.GetContract().GetId(),
			Phase:   epic.GetPhase(),
			Created: epic.GetCreated(),
			Changed: epic.GetChanged(),
		})
	}
	return connect.NewResponse(&enginev1.ListEpicsResponse{Epics: summaries}), nil
}

func (f *epicStoreFake) GetEpic(
	_ context.Context,
	req *connect.Request[enginev1.GetEpicRequest],
) (*connect.Response[enginev1.GetEpicResponse], error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	for _, epic := range f.stored {
		if epic.GetContract().GetId() == req.Msg.GetId() {
			return connect.NewResponse(&enginev1.GetEpicResponse{Epic: epic}), nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("synthetic not found"))
}

// storedEpicOf returns the stored epic id, in phase, last changed at changed unix seconds, with the contract's
// requirements in the order given.
func storedEpicOf(id, phase string, changed int64, requirements ...*enginev1.EpicRequirement) *enginev1.StoredEpic {
	return &enginev1.StoredEpic{
		Contract: &enginev1.EpicContract{Id: id, Title: "synthetic epic", Requirements: requirements},
		Phase:    phase,
		Created:  changed - 3600,
		Changed:  changed,
	}
}

// epicStoreServe serves newEpicsHandler over root, steps and store with the fixed clock, and returns a generated
// Epics client.
func epicStoreServe(t *testing.T, root string, steps StepClient, store EpicStoreClient) orreryv1connect.EpicsClient {
	t.Helper()
	path, handler := newEpicsHandler(root, steps, store, 0, epicsNow)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return orreryv1connect.NewEpicsClient(srv.Client(), srv.URL)
}

// epicIDs returns the ids of epics, in order.
func epicIDs(epics []*orreryv1.Epic) []string {
	ids := make([]string, 0, len(epics))
	for _, epic := range epics {
		ids = append(ids, epic.GetId())
	}
	return ids
}

func TestEpicsListMergesDatabaseEpics(t *testing.T) {
	root := t.TempDir()
	writeHead(t, root, "repoa", "shared", `{"outcome":"shipped"}`, epicsNow().Add(-48*time.Hour))
	writeHead(t, root, "repoa", "record", `{"outcome":"shipped"}`, epicsNow().Add(-time.Hour))
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{
		storedEpicOf("repoa/dbonly", "accepted", epicsAgo(30*24*time.Hour)),
		storedEpicOf("repoa/shared", "accepted", epicsAgo(2*time.Hour)),
		storedEpicOf("repob/done", "released", epicsAgo(time.Minute)),
	}}
	client := epicStoreServe(t, root, &epicsFake{}, store)

	got := epicsList(t, client)
	wantIDs := []string{"repoa/dbonly", "repoa/record", "repoa/shared", "repob/done"}
	if ids := epicIDs(got); !reflect.DeepEqual(ids, wantIDs) {
		t.Fatalf("ids = %v, want %v", ids, wantIDs)
	}

	want := map[string]struct {
		repository, slug, source, outcome string
		changed                           int64
		live                              bool
	}{
		"repoa/dbonly": {
			repository: "repoa", slug: "dbonly", source: "orreryd", outcome: "accepted",
			changed: epicsAgo(30 * 24 * time.Hour), live: true,
		},
		"repoa/record": {
			repository: "repoa", slug: "record", source: "record", outcome: "shipped",
			changed: epicsAgo(time.Hour), live: true,
		},
		"repoa/shared": {
			repository: "repoa", slug: "shared", source: "orreryd", outcome: "accepted",
			changed: epicsAgo(2 * time.Hour), live: true,
		},
		"repob/done": {
			repository: "repob", slug: "done", source: "orreryd", outcome: "released",
			changed: epicsAgo(time.Minute), live: false,
		},
	}
	for _, epic := range got {
		w := want[epic.GetId()]
		if epic.GetRepository() != w.repository || epic.GetSlug() != w.slug || epic.GetSource() != w.source ||
			epic.GetOutcome() != w.outcome || epic.GetChanged() != w.changed || epic.GetLive() != w.live ||
			epic.GetProblem() != "" {
			t.Errorf("%s = repository %q slug %q source %q outcome %q changed %d live %v problem %q, "+
				"want repository %q slug %q source %q outcome %q changed %d live %v problem empty",
				epic.GetId(), epic.GetRepository(), epic.GetSlug(), epic.GetSource(), epic.GetOutcome(),
				epic.GetChanged(), epic.GetLive(), epic.GetProblem(),
				w.repository, w.slug, w.source, w.outcome, w.changed, w.live)
		}
	}
}

func TestEpicsSharedIDTakesProblemFromRecord(t *testing.T) {
	root := t.TempDir()
	writeHead(t, root, "repoa", "shared", `{not json`, epicsNow())
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{
		storedEpicOf("repoa/shared", "accepted", epicsAgo(time.Hour)),
	}}
	client := epicStoreServe(t, root, &epicsFake{}, store)
	const wantProblem = "repoa/shared/state.json: does not parse"

	got := epicsList(t, client)
	if ids := epicIDs(got); !reflect.DeepEqual(ids, []string{"repoa/shared"}) {
		t.Fatalf("ids = %v, want repoa/shared once", ids)
	}
	if got[0].GetOutcome() != "accepted" || got[0].GetSource() != "orreryd" || got[0].GetProblem() != wantProblem {
		t.Errorf("List shared = outcome %q source %q problem %q, want accepted, orreryd, %q",
			got[0].GetOutcome(), got[0].GetSource(), got[0].GetProblem(), wantProblem)
	}

	answer := epicsGet(t, client, "repoa/shared").GetEpic()
	if answer.GetOutcome() != "accepted" || answer.GetSource() != "orreryd" || answer.GetProblem() != wantProblem {
		t.Errorf("Get shared = outcome %q source %q problem %q, want accepted, orreryd, %q",
			answer.GetOutcome(), answer.GetSource(), answer.GetProblem(), wantProblem)
	}
}

func TestEpicsGetSharedEpicCarriesRequirementsAndRecordTasks(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "repoa", "shared")
	writeHead(t, root, "repoa", "shared", `{"outcome":"shipped"}`, epicsNow().Add(-48*time.Hour))
	writeEffortState(t, dir, "1", `{"tasks":[{"slug":"one-a","subject":"One A","status":"gated"}]}`)
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{
		storedEpicOf("repoa/shared", "accepted", epicsAgo(2*time.Hour),
			&enginev1.EpicRequirement{
				Id: "R2", Text: "second in contract", Evidence: "synthetic evidence two", Kind: "review",
			},
			&enginev1.EpicRequirement{
				Id: "R1", Text: "first in contract", Evidence: "synthetic evidence one", Kind: "check",
			},
		),
	}}
	steps := &epicsFake{steps: []*enginev1.Step{
		{Id: "run", Epic: "repoa/shared", Started: epicsAgo(time.Hour)},
	}}
	client := epicStoreServe(t, root, steps, store)

	got := epicsGet(t, client, "repoa/shared")
	epic := got.GetEpic()
	if epic.GetSource() != "orreryd" || epic.GetOutcome() != "accepted" || epic.GetChanged() != epicsAgo(2*time.Hour) ||
		!epic.GetLive() {
		t.Errorf("epic = source %q outcome %q changed %d live %v, want orreryd, accepted, %d, true",
			epic.GetSource(), epic.GetOutcome(), epic.GetChanged(), epic.GetLive(), epicsAgo(2*time.Hour))
	}
	if ran := epicStepIDs(epic.GetRunningSteps()); !reflect.DeepEqual(ran, []string{"run"}) {
		t.Errorf("running_steps = %v, want [run]", ran)
	}

	wantReqs := []struct{ id, text, evidence, kind string }{
		{"R2", "second in contract", "synthetic evidence two", "review"},
		{"R1", "first in contract", "synthetic evidence one", "check"},
	}
	reqs := got.GetRequirements()
	if len(reqs) != len(wantReqs) {
		t.Fatalf("requirements = %v, want %d in contract order", reqs, len(wantReqs))
	}
	for i, w := range wantReqs {
		r := reqs[i]
		if r.GetId() != w.id || r.GetText() != w.text || r.GetEvidence() != w.evidence || r.GetKind() != w.kind {
			t.Errorf("requirement %d = %v, want id %q text %q evidence %q kind %q",
				i, r, w.id, w.text, w.evidence, w.kind)
		}
	}

	tasks := got.GetTasks()
	if len(tasks) != 1 || tasks[0].GetSlug() != "one-a" || tasks[0].GetEffort() != 1 {
		t.Errorf("tasks = %v, want the record's one-a", tasks)
	}
}

func TestEpicsGetRecordOnlyEpicHasNoRequirements(t *testing.T) {
	root := t.TempDir()
	writeHead(t, root, "repoa", "record", `{"outcome":"shipped"}`, epicsNow())
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{
		storedEpicOf("repoa/other", "accepted", epicsAgo(time.Hour)),
	}}
	client := epicStoreServe(t, root, &epicsFake{}, store)

	got := epicsGet(t, client, "repoa/record")
	if got.GetEpic().GetSource() != "record" || got.GetEpic().GetOutcome() != "shipped" {
		t.Errorf("epic = source %q outcome %q, want record, shipped",
			got.GetEpic().GetSource(), got.GetEpic().GetOutcome())
	}
	if len(got.GetRequirements()) != 0 {
		t.Errorf("requirements = %v, want none for a record epic", got.GetRequirements())
	}
}

func TestEpicsStoreMissingOrOlderListsRecordsAsToday(t *testing.T) {
	root := t.TempDir()
	writeHead(t, root, "repo", "epic", `{"outcome":"shipped"}`, epicsNow())
	// A server with no Epics service answers every call with 404, which the generated client reports as unimplemented:
	// the answer of an orreryd older than the Epics service.
	older := httptest.NewServer(http.NewServeMux())
	t.Cleanup(older.Close)
	cases := []struct {
		name  string
		store EpicStoreClient
	}{
		{name: "nil client", store: nil},
		{name: "older orreryd", store: enginev1connect.NewEpicsClient(older.Client(), older.URL)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := epicStoreServe(t, root, &epicsFake{}, tc.store)

			got := epicsList(t, client)
			if len(got) != 1 || got[0].GetId() != "repo/epic" || got[0].GetSource() != "record" ||
				got[0].GetOutcome() != "shipped" {
				t.Errorf("List = %v, want the one record epic repo/epic, source record, outcome shipped", got)
			}
			answer := epicsGet(t, client, "repo/epic")
			if answer.GetEpic().GetSource() != "record" || len(answer.GetRequirements()) != 0 {
				t.Errorf("Get = source %q requirements %v, want record and none",
					answer.GetEpic().GetSource(), answer.GetRequirements())
			}
		})
	}
}

func TestEpicsStoreFailureAnswersUnavailable(t *testing.T) {
	root := t.TempDir()
	writeHead(t, root, "repo", "epic", `{"outcome":"shipped"}`, epicsNow())
	failure := connect.NewError(connect.CodeInternal, errors.New("synthetic store failure"))

	t.Run("ListEpics", func(t *testing.T) {
		client := epicStoreServe(t, root, &epicsFake{}, &epicStoreFake{listErr: failure})
		_, err := client.List(context.Background(), connect.NewRequest(&orreryv1.EpicsListRequest{}))
		requireConnectError(t, err, connect.CodeUnavailable, "epic store unavailable")
	})
	t.Run("GetEpic", func(t *testing.T) {
		client := epicStoreServe(t, root, &epicsFake{}, &epicStoreFake{getErr: failure})
		_, err := client.Get(context.Background(), connect.NewRequest(&orreryv1.EpicsGetRequest{Id: "repo/epic"}))
		requireConnectError(t, err, connect.CodeUnavailable, "epic store unavailable")
	})
}
