package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// workContractID is the accepted contract the work tests read; its epic takes the same id. workProposedID is a proposed
// contract, which has no work.
const (
	workContractID = "synthetic-repo/work"
	workProposedID = "synthetic-repo/proposed"
)

// The times of the work tests, in unix seconds. The tests never read the wall clock.
const (
	workCreated  int64 = 1_700_000_000
	workChanged  int64 = 1_700_000_300
	workAccepted int64 = 1_700_000_200
	workRunStart int64 = 1_700_000_250
	workRunEnd   int64 = 1_700_000_260
)

// workBody returns the contract body of id with the requirements R1 to R4 in order.
func workBody(id string) *enginev1.EpicContract {
	return &enginev1.EpicContract{
		Id: id, Repository: "/synthetic/repo", Title: "Synthetic work", Care: "limited", Intent: "Synthetic intent.",
		Requirements: []*enginev1.EpicRequirement{
			{Id: "R1", Text: "Synthetic requirement one."},
			{Id: "R2", Text: "Synthetic requirement two."},
			{Id: "R3", Text: "Synthetic requirement three."},
			{Id: "R4", Text: "Synthetic requirement four."},
		},
	}
}

// workVersion returns the first version of the contract id, proposed in the synthetic writer's words.
func workVersion(t *testing.T, id string) store.ContractVersion {
	t.Helper()
	body, err := protojson.Marshal(workBody(id))
	if err != nil {
		t.Fatalf("Marshal body: %v", err)
	}
	return store.ContractVersion{
		Version: 1, Repository: "/synthetic/repo", Title: "Synthetic work", Care: "limited", Body: string(body),
		Words: "Synthetic proposal.", Writer: "synthetic-writer", At: workCreated,
	}
}

// workReader seeds a store at a temporary path with the proposed contract workProposedID and the accepted contract
// workContractID, and returns a reader of it.
func workReader(t *testing.T) *store.Reader {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	st, err := store.Open(t.Context(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close store: %v", err)
		}
	})
	ctx := t.Context()
	if err := st.ProposeContract(ctx, workProposedID, workVersion(t, workProposedID)); err != nil {
		t.Fatalf("ProposeContract proposed: %v", err)
	}
	if err := st.ProposeContract(ctx, workContractID, workVersion(t, workContractID)); err != nil {
		t.Fatalf("ProposeContract accepted: %v", err)
	}
	if err := st.AcceptContract(ctx, workContractID, store.ContractChange{
		Words: "Synthetic accept.", At: workAccepted,
	}); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}
	return store.NewReader(path)
}

// workClient serves newContractsHandler over reader, epics and evidence with a synthetic retry clock, and returns a
// generated Contracts client for it.
func workClient(
	t *testing.T,
	reader *store.Reader,
	epics EpicStoreClient,
	evidence EvidenceClient,
) orreryv1connect.ContractsClient {
	t.Helper()
	path, handler := newContractsHandler(reader, epics, evidence, nil, nil, 0, newRetryClock())
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return orreryv1connect.NewContractsClient(srv.Client(), srv.URL)
}

// workEpic returns the stored epic of the accepted contract in phase, with tasks as its build tasks. Each requirement
// of workBody carries an evidence item, so its runs are attached.
func workEpic(phase string, tasks ...*enginev1.EpicTask) *enginev1.StoredEpic {
	body := workBody(workContractID)
	requirements := make([]*enginev1.EpicRequirement, 0, len(body.GetRequirements()))
	for _, req := range body.GetRequirements() {
		requirements = append(requirements, evidenceRequirementOf(req.GetId(), req.GetText(), "holds"))
	}
	return &enginev1.StoredEpic{
		Contract: &enginev1.EpicContract{Id: workContractID, Title: "Synthetic work", Requirements: requirements},
		Phase:    phase,
		Created:  workCreated,
		Changed:  workChanged,
		Tasks:    tasks,
	}
}

// workRuns returns the runs of the work epic in the order ListEvidence lists them: a red run of R3 that passed, then
// the green run of R3 that passed, and a green run of R4 that did not pass.
func workRuns() []*enginev1.EvidenceRun {
	return []*enginev1.EvidenceRun{
		{
			Id: "run-r3-red", Item: "R3", Phase: "red", Source: "captured", State: "ended", Verdict: "fails",
			Passed: true, Revision: "synthetic-rev-r3-red", Created: workRunStart, Ended: workRunEnd,
		},
		{
			Id: "run-r3-green", Item: "R3", Phase: "green", Source: "captured", State: "ended", Verdict: "holds",
			Passed: true, Revision: "synthetic-rev-r3-green", Created: workRunStart, Ended: workRunEnd, LogBytes: 64,
		},
		{
			Id: "run-r4-green", Item: "R4", Phase: "green", Source: "captured", State: "ended", Exit: 1,
			Verdict: "fails", Revision: "synthetic-rev-r4-green", Created: workRunStart, Ended: workRunEnd,
		},
	}
}

// workGet answers the Get of contract id through client, and fails t unless it answers without error.
func workGet(t *testing.T, client orreryv1connect.ContractsClient, id string) *orreryv1.ContractsGetResponse {
	t.Helper()
	reply, err := client.Get(t.Context(), connect.NewRequest(&orreryv1.ContractsGetRequest{Id: id}))
	if err != nil {
		t.Fatalf("Get %s: %v", id, err)
	}
	return reply.Msg
}

func TestContractRecordsRequirementWorkServesEachRequirementState(t *testing.T) {
	t.Parallel()
	epics := &epicStoreFake{stored: []*enginev1.StoredEpic{workEpic(
		"building",
		&enginev1.EpicTask{
			Id: "T1", Title: "Synthetic task one", Status: "open", Stage: "implement", Requirements: []string{"R2"},
		},
		&enginev1.EpicTask{
			Id: "T2", Title: "Synthetic task two", Status: "done", Integrated: true, Requirements: []string{"R3"},
		},
		&enginev1.EpicTask{
			Id: "T3", Title: "Synthetic task three", Status: "gap", Stage: "green", Requirements: []string{"R4"},
		},
		&enginev1.EpicTask{
			Id: "T4", Title: "Synthetic task four", Status: "open", Requirements: []string{"R1"},
		},
	)}}
	client := workClient(t, workReader(t), epics, &evidenceRunsFake{runs: workRuns()})

	got := workGet(t, client, workContractID).GetWork()
	want := []*orreryv1.ContractRequirementWork{
		{
			Id: "R1", Text: "Synthetic requirement one.", State: "not started",
			Tasks: []*orreryv1.ContractRequirementTask{
				{Id: "T4", Title: "Synthetic task four", Status: "open", Place: "planned"},
			},
		},
		{
			Id: "R2", Text: "Synthetic requirement two.", State: "being worked",
			Tasks: []*orreryv1.ContractRequirementTask{
				{Id: "T1", Title: "Synthetic task one", Status: "open", Place: "implement"},
			},
		},
		{
			Id: "R3", Text: "Synthetic requirement three.", State: "proven",
			Tasks: []*orreryv1.ContractRequirementTask{
				{Id: "T2", Title: "Synthetic task two", Status: "done", Place: "integrated"},
			},
			Proof: &orreryv1.EvidenceRun{
				Id: "run-r3-green", Phase: "green", Source: "captured", State: "ended", Verdict: "holds", Passed: true,
				Revision: "synthetic-rev-r3-green", Created: workRunStart, Ended: workRunEnd, LogBytes: 64,
			},
		},
		{
			Id: "R4", Text: "Synthetic requirement four.", State: "failed",
			Tasks: []*orreryv1.ContractRequirementTask{
				{Id: "T3", Title: "Synthetic task three", Status: "gap", Place: "green"},
			},
		},
	}
	if !proto.Equal(&orreryv1.ContractsGetResponse{Work: got}, &orreryv1.ContractsGetResponse{Work: want}) {
		t.Fatalf("Work = %v, want %v", got, want)
	}
}

func TestContractRecordsRequirementWorkEndedEpicFailsWhatNoRunProves(t *testing.T) {
	t.Parallel()
	epics := &epicStoreFake{stored: []*enginev1.StoredEpic{workEpic(
		"done",
		&enginev1.EpicTask{
			Id: "T1", Title: "Synthetic task one", Status: "open", Stage: "implement", Requirements: []string{"R2"},
		},
	)}}
	client := workClient(t, workReader(t), epics, &evidenceRunsFake{runs: workRuns()})

	got := workGet(t, client, workContractID).GetWork()
	want := []*orreryv1.ContractRequirementWork{
		{Id: "R1", Text: "Synthetic requirement one.", State: "failed"},
		{
			Id: "R2", Text: "Synthetic requirement two.", State: "failed",
			Tasks: []*orreryv1.ContractRequirementTask{
				{Id: "T1", Title: "Synthetic task one", Status: "open", Place: "implement"},
			},
		},
		{
			Id: "R3", Text: "Synthetic requirement three.", State: "proven",
			Proof: &orreryv1.EvidenceRun{
				Id: "run-r3-green", Phase: "green", Source: "captured", State: "ended", Verdict: "holds", Passed: true,
				Revision: "synthetic-rev-r3-green", Created: workRunStart, Ended: workRunEnd, LogBytes: 64,
			},
		},
		{Id: "R4", Text: "Synthetic requirement four.", State: "failed"},
	}
	if !proto.Equal(&orreryv1.ContractsGetResponse{Work: got}, &orreryv1.ContractsGetResponse{Work: want}) {
		t.Fatalf("Work = %v, want %v", got, want)
	}
}

func TestContractRecordsRequirementWorkProposedContractHasNoWork(t *testing.T) {
	t.Parallel()
	client := workClient(t, workReader(t), &epicStoreFake{}, &evidenceRunsFake{runs: workRuns()})

	reply := workGet(t, client, workProposedID)
	if state := reply.GetContract().GetState(); state != store.ContractProposed {
		t.Fatalf("contract state = %q, want %q", state, store.ContractProposed)
	}
	if got := reply.GetWork(); len(got) != 0 {
		t.Fatalf("Work = %v, want none for a proposed contract", got)
	}
}

func TestContractRecordsRequirementWorkAcceptedWithoutEpicIsNotStarted(t *testing.T) {
	t.Parallel()
	client := workClient(t, workReader(t), &epicStoreFake{}, &evidenceRunsFake{runs: workRuns()})

	got := workGet(t, client, workContractID).GetWork()
	want := []*orreryv1.ContractRequirementWork{
		{Id: "R1", Text: "Synthetic requirement one.", State: "not started"},
		{Id: "R2", Text: "Synthetic requirement two.", State: "not started"},
		{Id: "R3", Text: "Synthetic requirement three.", State: "not started"},
		{Id: "R4", Text: "Synthetic requirement four.", State: "not started"},
	}
	if !proto.Equal(&orreryv1.ContractsGetResponse{Work: got}, &orreryv1.ContractsGetResponse{Work: want}) {
		t.Fatalf("Work = %v, want %v", got, want)
	}
}

func TestContractRecordsRequirementWorkFailedEpicReadIsUnavailable(t *testing.T) {
	t.Parallel()
	epics := &epicStoreFake{getErr: connect.NewError(connect.CodeInternal, errors.New("synthetic store failure"))}
	client := workClient(t, workReader(t), epics, nil)

	_, err := client.Get(t.Context(), connect.NewRequest(&orreryv1.ContractsGetRequest{Id: workContractID}))
	if code := connect.CodeOf(err); code != connect.CodeUnavailable {
		t.Fatalf("Get: code = %v, want %v (error %v)", code, connect.CodeUnavailable, err)
	}
}
