package enginehost

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// contractTestAt is the time the fake clock of the Contracts tests reads.
func contractTestAt() time.Time { return time.Unix(1700004000, 0) }

// contractTestHeadless is the sessions row id of the headless session the Contracts tests propose from.
const contractTestHeadless = "engine-1"

// contractTestPane is the sessions row id of the pane the Contracts tests propose from.
const contractTestPane = "orrery-1"

// contractTestTmux is the tmux socket the pane of the Contracts tests runs on.
const contractTestTmux = "/tmp/tmux-1000/orrery"

// contractTestWords is the words the Contracts tests propose with.
const contractTestWords = "Synthetic words."

// contractTestServer is a sessions socket serving the Epics and Contracts services, with the clients that dial it.
type contractTestServer struct {
	st        *store.Store
	repo      string
	epics     enginev1connect.EpicsClient
	contracts enginev1connect.ContractsClient
}

// startContractServer serves epics with fixedSteering and contracts on a sessions socket in a temporary directory,
// over a git repository holding the contracts' base. The server stops at cleanup.
func startContractServer(t *testing.T, st *store.Store, contracts *Contracts) contractTestServer {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.sock")
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, nil, nil, st, fixedSteering{}, nil, nil, nil, nil, contracts) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve after cleanup: %v", err)
		}
	})
	client := unixHTTPClient(t, path)
	return contractTestServer{
		st:        st,
		repo:      stepGitTestRepo(t),
		epics:     enginev1connect.NewEpicsClient(client, "http://epics.invalid"),
		contracts: enginev1connect.NewContractsClient(client, "http://contracts.invalid"),
	}
}

// contractTestContracts returns the Contracts of the tests: the store st, the fake clock and the test environment.
func contractTestContracts(st *store.Store) *Contracts {
	return &Contracts{Store: st, Now: contractTestAt, Getenv: epicTestGetenv}
}

// contractTestBody returns a valid contract body with epicServiceID, based in repo.
func contractTestBody(repo string) *v1.EpicContract {
	return fullEpicRequest(repo, epicServiceID).GetContract()
}

// contractTestDecisions returns two decisions: D1, open, and D2, answered.
func contractTestDecisions() []*v1.ContractDecision {
	return []*v1.ContractDecision{
		{
			Id:             "D1",
			Question:       "Synthetic question one.",
			Options:        []string{"Synthetic option one.", "Synthetic option two."},
			Recommendation: "Synthetic option one.",
			Reason:         "Synthetic reason.",
			Row:            "F1",
		},
		{
			Id: "D2", Question: "Synthetic question two.", Options: []string{"Synthetic option three."},
			Recommendation: "Synthetic option three.", Reason: "Synthetic reason.", Answer: "Synthetic option three.",
		},
	}
}

// headlessCaller sets the headers of a call from the headless session contractTestHeadless.
func headlessCaller(h http.Header) { h.Set(protocol.HeaderSession, contractTestHeadless) }

// seedContractSessions seeds the headless session and the pane the Contracts tests propose from.
func seedContractSessions(t *testing.T, st *store.Store) {
	t.Helper()
	engine := store.EngineAdd{ID: contractTestHeadless, Dir: "/work/engine"}
	if err := st.ApplyEngine(t.Context(), []store.EngineOp{engine}, contractTestAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	pane := store.Pane{ID: contractTestPane, Pane: "%7", Fields: store.Fields{State: "working"}}
	if err := st.SyncPane(t.Context(), "-S "+contractTestTmux, pane, contractTestAt()); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
}

// wantStoredContract returns the StoredContract that proposing body by writer with decisions stores as version 1,
// read back with its versions and states.
func wantStoredContract(body *v1.EpicContract, decisions []*v1.ContractDecision, writer string) *v1.StoredContract {
	at := contractTestAt().Unix()
	open := int64(0)
	for _, decision := range decisions {
		if decision.GetAnswer() == "" {
			open++
		}
	}
	return &v1.StoredContract{
		Id:            body.GetId(),
		Repository:    body.GetRepository(),
		Title:         body.GetTitle(),
		Care:          body.GetCare(),
		State:         "proposed",
		Version:       1,
		OpenDecisions: open,
		Conversation:  writer,
		Created:       at,
		Changed:       at,
		Versions: []*v1.ContractVersion{{
			Version:   1,
			Contract:  body,
			Decisions: decisions,
			Words:     contractTestWords,
			Writer:    writer,
			Time:      at,
		}},
		States: []*v1.ContractStateChange{{
			State:   "proposed",
			Version: 1,
			By:      "session",
			Session: writer,
			Words:   contractTestWords,
			Time:    at,
		}},
	}
}

// proposeContract sends a propose of body with decisions from the caller headers set, and returns the reply's contract.
func proposeContract(
	t *testing.T,
	client enginev1connect.ContractsClient,
	headers func(http.Header),
	body *v1.EpicContract,
	decisions []*v1.ContractDecision,
) (*v1.StoredContract, error) {
	t.Helper()
	req := connect.NewRequest(&v1.ProposeContractRequest{
		Id:        body.GetId(),
		Contract:  body,
		Decisions: decisions,
		Words:     contractTestWords,
	})
	headers(req.Header())
	reply, err := client.ProposeContract(t.Context(), req)
	if err != nil {
		return nil, err
	}
	return reply.Msg.GetContract(), nil
}

// TestContractRecordProposeStoresVersionOneFromEachCaller checks that a propose from a headless session and from a pane
// stores the contract as version 1 in state proposed, with the fake clock's time, the caller's sessions row as writer and
// as the conversation, the words, and every field of each decision, and that the reply carries the stored contract.
func TestContractRecordProposeStoresVersionOneFromEachCaller(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		headers func(h http.Header)
		writer  string
	}{
		{
			name:    "headless session",
			headers: headlessCaller,
			writer:  contractTestHeadless,
		},
		{
			name: "pane",
			headers: func(h http.Header) {
				h.Set(protocol.HeaderTmux, contractTestTmux+",4242,0")
				h.Set(protocol.HeaderTmuxPane, "%7")
			},
			writer: contractTestPane,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := epicTestStore(t)
			seedContractSessions(t, st)
			server := startContractServer(t, st, contractTestContracts(st))
			body := contractTestBody(server.repo)
			decisions := contractTestDecisions()

			got, err := proposeContract(t, server.contracts, tc.headers, body, decisions)
			if err != nil {
				t.Fatalf("ProposeContract: %v", err)
			}
			want := wantStoredContract(body, decisions, tc.writer)
			if !proto.Equal(got, want) {
				t.Errorf("ProposeContract reply = %v, want %v", got, want)
			}
		})
	}
}

// TestContractRecordGetAndListReadTheContractBack checks that GetContract returns the contract a propose stored, with its
// versions and states, that ListContracts lists it with neither, and that GetContract refuses an id no contract has.
func TestContractRecordGetAndListReadTheContractBack(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	server := startContractServer(t, st, contractTestContracts(st))
	body := contractTestBody(server.repo)
	decisions := contractTestDecisions()
	proposed, err := proposeContract(t, server.contracts, headlessCaller, body, decisions)
	if err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}

	got, err := server.contracts.GetContract(
		t.Context(), connect.NewRequest(&v1.GetContractRequest{Id: epicServiceID}))
	if err != nil {
		t.Fatalf("GetContract: %v", err)
	}
	if !proto.Equal(got.Msg.GetContract(), proposed) {
		t.Errorf("GetContract = %v, want %v", got.Msg.GetContract(), proposed)
	}

	listed, err := server.contracts.ListContracts(t.Context(), connect.NewRequest(&v1.ListContractsRequest{}))
	if err != nil {
		t.Fatalf("ListContracts: %v", err)
	}
	if n := len(listed.Msg.GetContracts()); n != 1 {
		t.Fatalf("ListContracts lists %d contracts, want 1", n)
	}
	listing := listed.Msg.GetContracts()[0]
	if listing.GetId() != epicServiceID || listing.GetState() != "proposed" || listing.GetVersion() != 1 {
		t.Errorf("listed contract %s is state %q version %d, want %s proposed version 1",
			listing.GetId(), listing.GetState(), listing.GetVersion(), epicServiceID)
	}
	if len(listing.GetVersions()) != 0 || len(listing.GetStates()) != 0 {
		t.Errorf("listed contract carries %d versions and %d states, want none",
			len(listing.GetVersions()), len(listing.GetStates()))
	}

	_, err = server.contracts.GetContract(
		t.Context(), connect.NewRequest(&v1.GetContractRequest{Id: "synthetic-missing/none"}))
	wantContractRefusal(t, err, connect.CodeNotFound, "contract synthetic-missing/none not found")
}

// wantContractRefusal fails the test unless err is a Connect error with code and message.
func wantContractRefusal(t *testing.T, err error, code connect.Code, message string) {
	t.Helper()
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("error = %v, want a Connect error", err)
	}
	if connectErr.Code() != code {
		t.Errorf("code = %v, want %v", connectErr.Code(), code)
	}
	if connectErr.Message() != message {
		t.Errorf("message = %q, want %q", connectErr.Message(), message)
	}
}

// contractRefusal is one propose the server refuses: the rows the store holds before it, the caller headers it sends,
// how the request is changed from a valid propose, and the refusal it gets.
type contractRefusal struct {
	name    string
	seed    func(t *testing.T, server contractTestServer)
	headers func(h http.Header)
	change  func(req *v1.ProposeContractRequest)
	code    connect.Code
	message string
}

// TestContractRecordProposeRefusesAndStoresNothing checks that each propose the Contracts service refuses gets the
// refusal its reason names, and that the store holds the same contracts after it as before.
func TestContractRecordProposeRefusesAndStoresNothing(t *testing.T) {
	t.Parallel()
	cases := []contractRefusal{
		{
			name:    "id differs from the body's",
			headers: headlessCaller,
			change:  func(req *v1.ProposeContractRequest) { req.Id = "synthetic-other/id" },
			code:    connect.CodeInvalidArgument,
			message: "id: differs from the contract's id",
		},
		{
			name:    "caller headers absent",
			headers: func(http.Header) {},
			code:    connect.CodePermissionDenied,
			message: "caller: resolves to no session",
		},
		{
			name: "caller resolves to no session",
			headers: func(h http.Header) {
				h.Set(protocol.HeaderSession, "engine-missing")
			},
			code:    connect.CodePermissionDenied,
			message: "caller: resolves to no session",
		},
		{
			name:    "body is refused",
			headers: headlessCaller,
			change:  func(req *v1.ProposeContractRequest) { req.Contract.Intent = "" },
			code:    connect.CodeInvalidArgument,
			message: "intent: empty",
		},
		{
			name:    "decision has an empty id",
			headers: headlessCaller,
			change:  func(req *v1.ProposeContractRequest) { req.Decisions[0].Id = "" },
			code:    connect.CodeInvalidArgument,
			message: "decisions[0].id: empty",
		},
		{
			name:    "decision id is repeated",
			headers: headlessCaller,
			change:  func(req *v1.ProposeContractRequest) { req.Decisions[1].Id = "D1" },
			code:    connect.CodeInvalidArgument,
			message: "decisions[1].id: duplicate",
		},
		{
			name:    "decision has an empty question",
			headers: headlessCaller,
			change:  func(req *v1.ProposeContractRequest) { req.Decisions[0].Question = "" },
			code:    connect.CodeInvalidArgument,
			message: "decisions[0].question: empty",
		},
		{
			name:    "id a contract holds",
			headers: headlessCaller,
			seed: func(t *testing.T, server contractTestServer) {
				t.Helper()
				body := contractTestBody(server.repo)
				if _, err := proposeContract(t, server.contracts, headlessCaller, body, nil); err != nil {
					t.Fatalf("seed ProposeContract: %v", err)
				}
			},
			code:    connect.CodeAlreadyExists,
			message: "id: " + epicServiceID + " is already held by a contract or an epic",
		},
		{
			name:    "id an epic holds",
			headers: headlessCaller,
			seed: func(t *testing.T, server contractTestServer) {
				t.Helper()
				createEpicOK(t, server.epics, fullEpicRequest(server.repo, epicServiceID))
			},
			code:    connect.CodeAlreadyExists,
			message: "id: " + epicServiceID + " is already held by a contract or an epic",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := epicTestStore(t)
			seedContractSessions(t, st)
			server := startContractServer(t, st, contractTestContracts(st))
			if tc.seed != nil {
				tc.seed(t, server)
			}
			before, err := st.Contracts(t.Context())
			if err != nil {
				t.Fatalf("Contracts: %v", err)
			}

			body := contractTestBody(server.repo)
			req := connect.NewRequest(&v1.ProposeContractRequest{
				Id:        body.GetId(),
				Contract:  body,
				Decisions: contractTestDecisions(),
				Words:     contractTestWords,
			})
			if tc.change != nil {
				tc.change(req.Msg)
			}
			tc.headers(req.Header())
			_, err = server.contracts.ProposeContract(t.Context(), req)
			wantContractRefusal(t, err, tc.code, tc.message)

			after, err := st.Contracts(t.Context())
			if err != nil {
				t.Fatalf("Contracts: %v", err)
			}
			if !slices.Equal(before, after) {
				t.Errorf("refused propose changed the stored contracts: %v, want %v", after, before)
			}
		})
	}
}

// TestContractRecordNilContractsAnswersUnavailable checks that a server with no Contracts refuses each Contracts RPC it
// serves as unavailable, with the message the service names.
func TestContractRecordNilContractsAnswersUnavailable(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	server := startContractServer(t, st, nil)
	body := contractTestBody(server.repo)

	_, err := proposeContract(t, server.contracts, headlessCaller, body, nil)
	wantContractRefusal(t, err, connect.CodeUnavailable, "contract store unavailable")
	_, err = server.contracts.GetContract(
		t.Context(), connect.NewRequest(&v1.GetContractRequest{Id: epicServiceID}))
	wantContractRefusal(t, err, connect.CodeUnavailable, "contract store unavailable")
	_, err = server.contracts.ListContracts(t.Context(), connect.NewRequest(&v1.ListContractsRequest{}))
	wantContractRefusal(t, err, connect.CodeUnavailable, "contract store unavailable")
}
