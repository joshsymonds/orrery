package server

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// countingEpicStore is an EpicStoreClient that counts the ListEpics and GetEpic calls that reach inner.
type countingEpicStore struct {
	inner EpicStoreClient

	mu    sync.Mutex
	lists int
	gets  int
}

func (c *countingEpicStore) ListEpics(
	ctx context.Context,
	req *connect.Request[enginev1.ListEpicsRequest],
) (*connect.Response[enginev1.ListEpicsResponse], error) {
	c.mu.Lock()
	c.lists++
	c.mu.Unlock()
	return c.inner.ListEpics(ctx, req)
}

func (c *countingEpicStore) GetEpic(
	ctx context.Context,
	req *connect.Request[enginev1.GetEpicRequest],
) (*connect.Response[enginev1.GetEpicResponse], error) {
	c.mu.Lock()
	c.gets++
	c.mu.Unlock()
	return c.inner.GetEpic(ctx, req)
}

// counts returns how many ListEpics and GetEpic calls reached the store.
func (c *countingEpicStore) counts() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lists, c.gets
}

// loadSteps returns the labeled steps of the load tests: repoa/record, which only the records hold, runs one step and
// ended two, and repoa/stored, which orreryd's database holds, runs one held step and ended three, one of which is a
// writing step whose result carries red and green evidence.
func loadSteps() []*enginev1.Step {
	return []*enginev1.Step{
		{Id: "rec-run", Epic: "repoa/record", Task: "t1", Started: epicsAgo(time.Hour)},
		{
			Id:      "rec-a",
			Epic:    "repoa/record",
			End:     "done",
			Started: epicsAgo(5 * time.Hour),
			Ended:   epicsAgo(4 * time.Hour),
		},
		{
			Id:      "rec-b",
			Epic:    "repoa/record",
			End:     "failed",
			Started: epicsAgo(3 * time.Hour),
			Ended:   epicsAgo(2 * time.Hour),
		},
		{Id: "db-run", Epic: "repoa/stored", Held: true, Started: epicsAgo(time.Hour)},
		{
			Id:      "db-a",
			Epic:    "repoa/stored",
			Task:    "t1",
			End:     "done",
			Result:  `{"summary":"synthetic summary"}`,
			Started: epicsAgo(6 * time.Hour),
			Ended:   epicsAgo(5 * time.Hour),
			Checks:  []*enginev1.StepCheck{{Round: 1, Name: "gate", Ok: true}},
		},
		{
			Id:      "db-b",
			Epic:    "repoa/stored",
			End:     "done",
			Started: epicsAgo(4 * time.Hour),
			Ended:   epicsAgo(3 * time.Hour),
		},
		{
			Id:      "db-c",
			Epic:    "repoa/stored",
			End:     "stopped",
			Started: epicsAgo(3 * time.Hour),
			Ended:   epicsAgo(2 * time.Hour),
		},
	}
}

// TestEpicsListAsksOnlyTheRunningStepsOnce proves List reads no ended step: it asks ListSteps exactly once, for the
// running steps alone, and ListEpics once and GetEpic never. Each epic still answers its running steps and its count
// of ended steps, the ended ones counted by orreryd.
func TestEpicsListAsksOnlyTheRunningStepsOnce(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHead(t, root, "repoa", "record", `{"outcome":"shipped"}`, epicsNow().Add(-time.Hour))
	steps := &epicsFake{steps: loadSteps()}
	store := &countingEpicStore{inner: &epicStoreFake{stored: []*enginev1.StoredEpic{
		storedEpicOf("repoa/stored", "accepted", epicsAgo(2*time.Hour)),
	}}}
	client := epicStoreServe(t, root, steps, store)

	epics := epicsList(t, client)

	requests := steps.listRequests()
	if len(requests) != 1 || !proto.Equal(requests[0], &enginev1.ListStepsRequest{Running: true}) {
		t.Fatalf("ListSteps requests = %v, want exactly one request for the running steps of every epic", requests)
	}
	if lists, gets := store.counts(); lists != 1 || gets != 0 {
		t.Errorf("epic store calls = %d ListEpics and %d GetEpic, want 1 and 0", lists, gets)
	}
	want := map[string]struct {
		running []string
		ended   int32
	}{
		"repoa/record": {running: []string{"rec-run"}, ended: 2},
		"repoa/stored": {running: []string{"db-run"}, ended: 3},
	}
	if len(epics) != len(want) {
		t.Fatalf("epics = %v, want %d", epicIDs(epics), len(want))
	}
	for _, epic := range epics {
		w := want[epic.GetId()]
		if got := epicStepIDs(epic.GetRunningSteps()); !reflect.DeepEqual(got, w.running) ||
			epic.GetEndedSteps() != w.ended {
			t.Errorf("%s = running %v ended %d, want running %v ended %d",
				epic.GetId(), got, epic.GetEndedSteps(), w.running, w.ended)
		}
	}
}

// TestEpicsGetAsksTheStepsOfItsEpicOnce proves Get reads its epic's steps in one ListSteps narrowed to the epic, its
// stored epic in one GetEpic and its runs in one ListEvidence, and answers every step with the summary orreryd read
// from its result.
func TestEpicsGetAsksTheStepsOfItsEpicOnce(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	steps := &epicsFake{steps: loadSteps()}
	store := &countingEpicStore{inner: &epicStoreFake{stored: []*enginev1.StoredEpic{
		storedEpicOf("repoa/stored", "accepted", epicsAgo(2*time.Hour)),
	}}}
	evidence := &evidenceRunsFake{}
	client := stepDataServe(t, root, steps, store, evidence)

	got := epicsGet(t, client, "repoa/stored")

	requests := steps.listRequests()
	if len(requests) != 1 || !proto.Equal(requests[0], &enginev1.ListStepsRequest{Epic: "repoa/stored"}) {
		t.Fatalf("ListSteps requests = %v, want exactly one request for the steps of repoa/stored", requests)
	}
	if lists, gets := store.counts(); lists != 0 || gets != 1 {
		t.Errorf("epic store calls = %d ListEpics and %d GetEpic, want 0 and 1", lists, gets)
	}
	if epics := evidence.listedEpics(); !reflect.DeepEqual(epics, []string{"repoa/stored"}) {
		t.Errorf("ListEvidence epics = %v, want one call for repoa/stored", epics)
	}
	if ids := epicStepIDs(got.GetSteps()); !reflect.DeepEqual(ids, []string{"db-a", "db-b", "db-c", "db-run"}) {
		t.Errorf("steps = %v, want the epic's four steps oldest first", ids)
	}
	if summary := got.GetSteps()[0].GetSummary(); summary != "synthetic summary" {
		t.Errorf("db-a summary = %q, want %q", summary, "synthetic summary")
	}
	if epic := got.GetEpic(); epic.GetEndedSteps() != 3 || len(epic.GetRunningSteps()) != 1 {
		t.Errorf("epic = running %v ended %d, want one running step and 3 ended",
			epicStepIDs(epic.GetRunningSteps()), epic.GetEndedSteps())
	}
}
