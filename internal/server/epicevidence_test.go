package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

// evidenceRunsFake is the EvidenceClient of the Get tests. ListEvidence answers runs, after each queued failure in
// failures has answered one call, and answers failAll on every call when it is set. It records the epic of each call.
// GetEvidenceArtifact answers unimplemented, since the epics handler never calls it.
type evidenceRunsFake struct {
	mu       sync.Mutex
	runs     []*enginev1.EvidenceRun
	failures []error
	failAll  error
	epics    []string
}

func (f *evidenceRunsFake) ListEvidence(
	_ context.Context,
	req *connect.Request[enginev1.ListEvidenceRequest],
) (*connect.Response[enginev1.ListEvidenceResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.epics = append(f.epics, req.Msg.GetEpic())
	if len(f.failures) > 0 {
		err := f.failures[0]
		f.failures = f.failures[1:]
		return nil, err
	}
	if f.failAll != nil {
		return nil, f.failAll
	}
	return connect.NewResponse(&enginev1.ListEvidenceResponse{Runs: f.runs}), nil
}

// listedEpics returns the epic of each ListEvidence call, in order.
func (f *evidenceRunsFake) listedEpics() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.epics...)
}

func (f *evidenceRunsFake) GetEvidenceArtifact(
	context.Context,
	*connect.Request[enginev1.GetEvidenceArtifactRequest],
) (*connect.Response[enginev1.GetEvidenceArtifactResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

// epicEvidenceServe serves newEpicsHandler over root, store and evidence, with an empty step client, the fixed
// clock and a fake retry clock, and returns a generated Epics client.
func epicEvidenceServe(
	t *testing.T,
	root string,
	store EpicStoreClient,
	evidence EvidenceClient,
) orreryv1connect.EpicsClient {
	t.Helper()
	path, handler := newEpicsHandler(root, &epicsFake{}, store, evidence, 0, epicsNow, newRetryClock())
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return orreryv1connect.NewEpicsClient(srv.Client(), srv.URL)
}

// evidenceRequirementOf returns the requirement id with text, whose evidence item captures a command, keeps one
// artifact and is judged against conclusion.
func evidenceRequirementOf(id, text, conclusion string) *enginev1.EpicRequirement {
	return &enginev1.EpicRequirement{
		Id:   id,
		Text: text,
		Item: &enginev1.EvidenceItem{
			Capture:    []string{"go", "test", "./..."},
			Limit:      600,
			Artifacts:  []string{"out/screen.png"},
			Conclusion: conclusion,
			Judge:      "synthetic-judge",
		},
	}
}

// syntheticRuns returns the runs of the Get tests in the order ListEvidence lists them: run-1a, run-2a and run-1b. It
// also returns the EvidenceRun each run is answered as, by id.
func syntheticRuns() ([]*enginev1.EvidenceRun, map[string]*orreryv1.EvidenceRun) {
	runs := []*enginev1.EvidenceRun{
		{
			Id: "run-1a", Epic: "repoa/shared", Item: "R1", Revision: "synthetic-rev-1a", Phase: "red",
			Source: "captured", State: "ended", Exit: 1, TimedOut: true, NotCaptured: "killed at its limit",
			LogHash: "synthetic-log-hash", LogBytes: 2048,
			Artifacts: []*enginev1.EvidenceArtifact{
				{Path: "out/screen.png", State: "kept", Hash: "synthetic-png-hash", Size: 512},
				{Path: "out/result.json", State: "missing"},
			},
			JudgeStep: "synthetic-judge-step", Verdict: "holds", JudgeReason: "synthetic reason", Passed: true,
			Created: epicsAgo(3 * time.Hour), Ended: epicsAgo(2 * time.Hour),
		},
		{
			Id: "run-2a", Epic: "repoa/shared", Item: "R2", Revision: "synthetic-rev-2a", Phase: "red",
			Source: "supplied", State: "judging", Created: epicsAgo(time.Hour),
			Artifacts: []*enginev1.EvidenceArtifact{{Path: "out/run.log", State: "over_cap", Size: 9999}},
		},
		{
			Id: "run-1b", Epic: "repoa/shared", Item: "R1", Revision: "synthetic-rev-1b", Phase: "green",
			Source: "captured", State: "ended", LogBytes: 64, Verdict: "holds", Passed: true,
			Created: epicsAgo(time.Hour), Ended: epicsAgo(30 * time.Minute),
		},
	}
	want := map[string]*orreryv1.EvidenceRun{
		"run-1a": {
			Id: "run-1a", Phase: "red", Source: "captured", State: "ended", Exit: 1, TimedOut: true,
			NotCaptured: "killed at its limit", Verdict: "holds", JudgeReason: "synthetic reason", Passed: true,
			Revision: "synthetic-rev-1a", Created: epicsAgo(3 * time.Hour), Ended: epicsAgo(2 * time.Hour),
			LogBytes: 2048,
			Artifacts: []*orreryv1.EvidenceArtifact{
				{Path: "out/screen.png", State: "kept", Size: 512, MediaType: "image/png"},
				{Path: "out/result.json", State: "missing", MediaType: "application/json"},
			},
		},
		"run-2a": {
			Id: "run-2a", Phase: "red", Source: "supplied", State: "judging", Revision: "synthetic-rev-2a",
			Created: epicsAgo(time.Hour),
			Artifacts: []*orreryv1.EvidenceArtifact{
				{Path: "out/run.log", State: "over_cap", Size: 9999, MediaType: "text/plain; charset=utf-8"},
			},
		},
		"run-1b": {
			Id: "run-1b", Phase: "green", Source: "captured", State: "ended", Verdict: "holds", Passed: true,
			Revision: "synthetic-rev-1b", Created: epicsAgo(time.Hour), Ended: epicsAgo(30 * time.Minute),
			LogBytes: 64,
		},
	}
	return runs, want
}

// evidenceStoredEpic returns the stored epic of the Get tests, whose requirements are given in contract order.
func evidenceStoredEpic(requirements ...*enginev1.EpicRequirement) *enginev1.StoredEpic {
	return storedEpicOf("repoa/shared", "accepted", epicsAgo(2*time.Hour), requirements...)
}

func TestEpicsGetAttachesEachRequirementsRunsInOrder(t *testing.T) {
	t.Parallel()
	runs, want := syntheticRuns()
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{evidenceStoredEpic(
		evidenceRequirementOf("R1", "synthetic requirement one", "synthetic conclusion one"),
		evidenceRequirementOf("R2", "synthetic requirement two", "synthetic conclusion two"),
	)}}
	evidence := &evidenceRunsFake{runs: runs}
	client := epicEvidenceServe(t, t.TempDir(), store, evidence)

	got := epicsGet(t, client, "repoa/shared").GetRequirements()
	wantReqs := []*orreryv1.EpicRequirement{
		{
			Id: "R1", Text: "synthetic requirement one", Conclusion: "synthetic conclusion one",
			Runs: []*orreryv1.EvidenceRun{want["run-1a"], want["run-1b"]},
		},
		{
			Id: "R2", Text: "synthetic requirement two", Conclusion: "synthetic conclusion two",
			Runs: []*orreryv1.EvidenceRun{want["run-2a"]},
		},
	}
	if len(got) != len(wantReqs) {
		t.Fatalf("requirements = %d, want %d", len(got), len(wantReqs))
	}
	for i, w := range wantReqs {
		if !proto.Equal(got[i], w) {
			t.Errorf("requirement %d = %v, want %v", i, got[i], w)
		}
	}
	if calls := evidence.listedEpics(); len(calls) != 1 || calls[0] != "repoa/shared" {
		t.Errorf("ListEvidence epics = %v, want [repoa/shared] once", calls)
	}
}

func TestEpicsGetLegacyRequirementHasNoRuns(t *testing.T) {
	t.Parallel()
	runs, _ := syntheticRuns()
	legacy := &enginev1.EpicRequirement{
		Id: "R3", Text: "synthetic legacy requirement", Evidence: "synthetic check", Kind: "check",
	}
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{evidenceStoredEpic(
		evidenceRequirementOf("R1", "synthetic requirement one", "synthetic conclusion one"),
		legacy,
	)}}
	// run-3a names the legacy requirement's id as its item, which a legacy requirement with no item must not attach.
	runs = append(runs, &enginev1.EvidenceRun{Id: "run-3a", Epic: "repoa/shared", Item: "R3"})
	client := epicEvidenceServe(t, t.TempDir(), store, &evidenceRunsFake{runs: runs})

	got := epicsGet(t, client, "repoa/shared").GetRequirements()
	if len(got) != 2 {
		t.Fatalf("requirements = %d, want 2", len(got))
	}
	if len(got[0].GetRuns()) != 2 {
		t.Errorf("R1 runs = %d, want 2", len(got[0].GetRuns()))
	}
	r3 := got[1]
	if r3.GetId() != "R3" || r3.GetEvidence() != "synthetic check" || r3.GetKind() != "check" ||
		r3.GetConclusion() != "" || len(r3.GetRuns()) != 0 {
		t.Errorf("R3 = %v, want its evidence and kind, no conclusion and no runs", r3)
	}
}

func TestEpicsGetAnswersEpicWithoutRunsWhenListEvidenceFails(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		code    connect.Code
		retried bool
	}{
		{name: "internal failure", code: connect.CodeInternal, retried: false},
		{name: "unavailable after retries", code: connect.CodeUnavailable, retried: true},
	}
	for _, tc := range cases {
		evidence := &evidenceRunsFake{
			failAll: connect.NewError(tc.code, errors.New("synthetic evidence failure")),
		}
		store := &epicStoreFake{stored: []*enginev1.StoredEpic{evidenceStoredEpic(
			evidenceRequirementOf("R1", "synthetic requirement one", "synthetic conclusion one"),
		)}}
		client := epicEvidenceServe(t, t.TempDir(), store, evidence)

		got := epicsGet(t, client, "repoa/shared")
		if got.GetEpic().GetId() != "repoa/shared" || got.GetEpic().GetSource() != "orreryd" {
			t.Errorf("%s: epic = %v, want repoa/shared from orreryd", tc.name, got.GetEpic())
		}
		reqs := got.GetRequirements()
		if len(reqs) != 1 || len(reqs[0].GetRuns()) != 0 || reqs[0].GetConclusion() != "synthetic conclusion one" {
			t.Errorf("%s: requirements = %v, want one R1 with its conclusion and no runs", tc.name, reqs)
		}
		calls := len(evidence.listedEpics())
		if calls == 0 || (calls > 1) != tc.retried {
			t.Errorf("%s: ListEvidence calls = %d, want retried %v", tc.name, calls, tc.retried)
		}
	}
}

func TestEpicsGetRetriesUnavailableListEvidence(t *testing.T) {
	t.Parallel()
	runs, want := syntheticRuns()
	evidence := &evidenceRunsFake{
		runs:     runs,
		failures: []error{connect.NewError(connect.CodeUnavailable, errors.New("synthetic restart"))},
	}
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{evidenceStoredEpic(
		evidenceRequirementOf("R1", "synthetic requirement one", "synthetic conclusion one"),
		evidenceRequirementOf("R2", "synthetic requirement two", "synthetic conclusion two"),
	)}}
	client := epicEvidenceServe(t, t.TempDir(), store, evidence)

	got := epicsGet(t, client, "repoa/shared").GetRequirements()
	if len(got) != 2 || len(got[0].GetRuns()) != 2 || len(got[1].GetRuns()) != 1 {
		t.Fatalf("requirements = %v, want R1 with two runs and R2 with one after the retry", got)
	}
	if !proto.Equal(got[0].GetRuns()[0], want["run-1a"]) || !proto.Equal(got[0].GetRuns()[1], want["run-1b"]) {
		t.Errorf("R1 runs = %v, want run-1a then run-1b", got[0].GetRuns())
	}
	if calls := len(evidence.listedEpics()); calls != 2 {
		t.Errorf("ListEvidence calls = %d, want 2: the unavailable call and its retry", calls)
	}
}

func TestEpicsGetWithoutEvidenceClientAnswersRequirementsWithoutRuns(t *testing.T) {
	t.Parallel()
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{evidenceStoredEpic(
		evidenceRequirementOf("R1", "synthetic requirement one", "synthetic conclusion one"),
	)}}
	client := epicEvidenceServe(t, t.TempDir(), store, nil)

	got := epicsGet(t, client, "repoa/shared").GetRequirements()
	if len(got) != 1 || len(got[0].GetRuns()) != 0 || got[0].GetConclusion() != "synthetic conclusion one" {
		t.Errorf("requirements = %v, want one R1 with its conclusion and no runs", got)
	}
}
