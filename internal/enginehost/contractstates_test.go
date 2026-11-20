package enginehost

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// The words the Contracts state tests revise, accept, set aside and reopen with.
const (
	contractTestReviseWords   = "Synthetic revision words."
	contractTestAcceptWords   = "Synthetic accept words."
	contractTestSetAsideWords = "Synthetic set aside words."
	contractTestReopenWords   = "Synthetic reopen words."
)

// contractTestMissing is an id well formed as a contract's, that no contract holds.
const contractTestMissing = "synthetic-repo-1d2e3f4/missing-contract"

// contractClock is the fake clock of the Contracts state tests. It reads the hour its test sets.
type contractClock struct{ unix atomic.Int64 }

// newContractClock returns a clock set to hour 0.
func newContractClock() *contractClock {
	clock := &contractClock{}
	clock.setHour(0)
	return clock
}

// setHour sets the clock to hours after contractTestAt.
func (c *contractClock) setHour(hours int64) { c.unix.Store(contractHour(hours)) }

// now returns the time the clock reads.
func (c *contractClock) now() time.Time { return time.Unix(c.unix.Load(), 0) }

// contractHour returns the unix time hours after contractTestAt.
func contractHour(hours int64) int64 { return contractTestAt().Unix() + hours*3600 }

// paneCaller sets the headers of a call from the pane contractTestPane.
func paneCaller(h http.Header) {
	h.Set(protocol.HeaderTmux, contractTestTmux+",4242,0")
	h.Set(protocol.HeaderTmuxPane, "%7")
}

// ownerCaller leaves the headers of a call empty, so the call is the owner's.
func ownerCaller(http.Header) {}

// reviseRequest returns a valid revise of the contract of the Contracts tests, based in repo, with the decisions
// contractTestDecisions and the words contractTestReviseWords.
func reviseRequest(repo string) *v1.ReviseContractRequest {
	return &v1.ReviseContractRequest{
		Id:        epicServiceID,
		Contract:  contractTestBody(repo),
		Decisions: contractTestDecisions(),
		Words:     contractTestReviseWords,
	}
}

// reviseContract sends req from the caller headers set, and returns the reply's contract.
func reviseContract(
	ctx context.Context,
	client enginev1connect.ContractsClient,
	headers func(http.Header),
	req *v1.ReviseContractRequest,
) (*v1.StoredContract, error) {
	call := connect.NewRequest(req)
	headers(call.Header())
	reply, err := client.ReviseContract(ctx, call)
	if err != nil {
		return nil, err
	}
	return reply.Msg.GetContract(), nil
}

// acceptContract sends an accept of id with contractTestAcceptWords from the caller headers set.
func acceptContract(
	ctx context.Context,
	client enginev1connect.ContractsClient,
	headers func(http.Header),
	id string,
) error {
	call := connect.NewRequest(&v1.AcceptContractRequest{Id: id, Words: contractTestAcceptWords})
	headers(call.Header())
	_, err := client.AcceptContract(ctx, call)
	return err
}

// setAsideContract sends a set aside of id with words from the caller headers set.
func setAsideContract(
	ctx context.Context,
	client enginev1connect.ContractsClient,
	headers func(http.Header),
	id, words string,
) error {
	call := connect.NewRequest(&v1.SetAsideContractRequest{Id: id, Words: words})
	headers(call.Header())
	_, err := client.SetAsideContract(ctx, call)
	return err
}

// reopenContract sends a reopen of id with contractTestReopenWords from the caller headers set.
func reopenContract(
	ctx context.Context,
	client enginev1connect.ContractsClient,
	headers func(http.Header),
	id string,
) error {
	call := connect.NewRequest(&v1.ReopenContractRequest{Id: id, Words: contractTestReopenWords})
	headers(call.Header())
	_, err := client.ReopenContract(ctx, call)
	return err
}

// wantStateChange returns the state change the Contracts tests expect: state at version, made by by (owner or session)
// with session and words, at hours after contractTestAt.
func wantStateChange(state string, version int64, by, session, words string, hours int64) *v1.ContractStateChange {
	return &v1.ContractStateChange{
		State:   state,
		Version: version,
		By:      by,
		Session: session,
		Words:   words,
		Time:    contractHour(hours),
	}
}

// seedProposed proposes the contract of the Contracts tests from the headless session, with the open decision D1.
func seedProposed(t *testing.T, server contractTestServer) {
	t.Helper()
	body := contractTestBody(server.repo)
	if _, err := proposeContract(t, server.contracts, headlessCaller, body, contractTestDecisions()); err != nil {
		t.Fatalf("seed ProposeContract: %v", err)
	}
}

// seedAccepted proposes the contract of the Contracts tests with no decisions, and accepts it as the owner.
func seedAccepted(t *testing.T, server contractTestServer) {
	t.Helper()
	body := contractTestBody(server.repo)
	if _, err := proposeContract(t, server.contracts, headlessCaller, body, nil); err != nil {
		t.Fatalf("seed ProposeContract: %v", err)
	}
	if err := acceptContract(t.Context(), server.contracts, ownerCaller, epicServiceID); err != nil {
		t.Fatalf("seed AcceptContract: %v", err)
	}
}

// seedSetAside proposes the contract of the Contracts tests, and sets it aside from the headless session.
func seedSetAside(t *testing.T, server contractTestServer) {
	t.Helper()
	seedProposed(t, server)
	err := setAsideContract(t.Context(), server.contracts, headlessCaller, epicServiceID, contractTestSetAsideWords)
	if err != nil {
		t.Fatalf("seed SetAsideContract: %v", err)
	}
}

// seedAcceptedWithEpic accepts the contract of the Contracts tests, then stores an epic with its id.
func seedAcceptedWithEpic(t *testing.T, server contractTestServer) {
	t.Helper()
	seedAccepted(t, server)
	if err := server.st.CreateEpic(t.Context(), store.EpicRow{
		ID: epicServiceID, Repository: server.repo, Title: "Synthetic epic", Intent: "Synthetic intent.",
		Care: "serious", Phase: "accepted", Created: contractTestAt(), Changed: contractTestAt(),
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
}

// movingContractStore is a contract store that, once armed, revises the contract it reads once more, as its
// conversation, before it returns the record it read. The service then writes over a version that has moved.
type movingContractStore struct {
	*store.Store
	moveNext atomic.Bool
}

// Contract reads the contract from the store. When armed it first appends a version to the contract, and returns the
// record from before that append.
func (m *movingContractStore) Contract(ctx context.Context, id string) (store.ContractRecord, error) {
	record, err := m.Store.Contract(ctx, id)
	if err != nil || !m.moveNext.Swap(false) {
		return record, err
	}
	moved := record.Versions[len(record.Versions)-1]
	moved.Version++
	moved.Words = "Synthetic moved words."
	if err := m.Store.ReviseContract(ctx, id, moved); err != nil {
		return store.ContractRecord{}, fmt.Errorf("move version of contract %s: %w", id, err)
	}
	return record, nil
}

// TestContractRecordReviseAppendsVersion checks that a revise from the conversation stores version 2 at the clock's time,
// with its writer, words, changed rows and changed sections, that the reply and a read carry it, and that version 1
// reads back unchanged.
func TestContractRecordReviseAppendsVersion(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	clock := newContractClock()
	server := startContractServer(t, st, &Contracts{Store: st, Now: clock.now, Getenv: epicTestGetenv})
	proposed, err := proposeContract(t, server.contracts, headlessCaller, contractTestBody(server.repo),
		contractTestDecisions())
	if err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}

	clock.setHour(1)
	req := reviseRequest(server.repo)
	req.Contract.Title = "Synthetic revised title."
	req.Contract.Requirements[0].Text = "Synthetic requirement one, revised."
	req.Decisions[0].Answer = "Synthetic option one."
	revised, err := reviseContract(t.Context(), server.contracts, headlessCaller, req)
	if err != nil {
		t.Fatalf("ReviseContract: %v", err)
	}

	want := &v1.StoredContract{
		Id:            epicServiceID,
		Repository:    proposed.GetRepository(),
		Title:         "Synthetic revised title.",
		Care:          proposed.GetCare(),
		State:         "proposed",
		Version:       2,
		OpenDecisions: 0,
		Conversation:  contractTestHeadless,
		Created:       contractHour(0),
		Changed:       contractHour(1),
		Versions: []*v1.ContractVersion{
			proposed.GetVersions()[0],
			{
				Version:         2,
				Contract:        req.Contract,
				Decisions:       req.Decisions,
				Words:           contractTestReviseWords,
				Writer:          contractTestHeadless,
				Time:            contractHour(1),
				ChangedRows:     []string{"R1", "D1"},
				ChangedSections: []string{"title", "requirements", "decisions"},
			},
		},
		States: proposed.GetStates(),
	}
	if !proto.Equal(revised, want) {
		t.Errorf("ReviseContract reply = %v, want %v", revised, want)
	}

	got, err := server.contracts.GetContract(t.Context(), connect.NewRequest(&v1.GetContractRequest{Id: epicServiceID}))
	if err != nil {
		t.Fatalf("GetContract: %v", err)
	}
	if !proto.Equal(got.Msg.GetContract(), want) {
		t.Errorf("GetContract after revise = %v, want %v", got.Msg.GetContract(), want)
	}
}

// TestContractRecordStateChangesReadBackInOrder checks that a contract set aside and reopened while proposed, revised,
// accepted by the owner, set aside and reopened again, then accepted from the pane reads back with its two versions
// unchanged and its seven state changes in the order they were made, each with its time, who and words.
func TestContractRecordStateChangesReadBackInOrder(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	clock := newContractClock()
	server := startContractServer(t, st, &Contracts{Store: st, Now: clock.now, Getenv: epicTestGetenv})
	ctx := t.Context()
	proposed, err := proposeContract(t, server.contracts, headlessCaller, contractTestBody(server.repo),
		contractTestDecisions())
	if err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}

	clock.setHour(1)
	err = setAsideContract(t.Context(), server.contracts, headlessCaller, epicServiceID, contractTestSetAsideWords)
	if err != nil {
		t.Fatalf("SetAsideContract from proposed: %v", err)
	}
	clock.setHour(2)
	if err = reopenContract(t.Context(), server.contracts, paneCaller, epicServiceID); err != nil {
		t.Fatalf("ReopenContract: %v", err)
	}
	clock.setHour(3)
	req := reviseRequest(server.repo)
	req.Decisions[0].Answer = "Synthetic option one."
	if _, err = reviseContract(t.Context(), server.contracts, headlessCaller, req); err != nil {
		t.Fatalf("ReviseContract: %v", err)
	}
	clock.setHour(4)
	if err = acceptContract(t.Context(), server.contracts, ownerCaller, epicServiceID); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}
	clock.setHour(5)
	err = setAsideContract(t.Context(), server.contracts, headlessCaller, epicServiceID, contractTestSetAsideWords)
	if err != nil {
		t.Fatalf("SetAsideContract from accepted: %v", err)
	}
	clock.setHour(6)
	if err = reopenContract(t.Context(), server.contracts, paneCaller, epicServiceID); err != nil {
		t.Fatalf("ReopenContract: %v", err)
	}
	clock.setHour(7)
	if err = acceptContract(t.Context(), server.contracts, paneCaller, epicServiceID); err != nil {
		t.Fatalf("AcceptContract from the pane: %v", err)
	}

	want := &v1.StoredContract{
		Id:            epicServiceID,
		Repository:    proposed.GetRepository(),
		Title:         proposed.GetTitle(),
		Care:          proposed.GetCare(),
		State:         "accepted",
		Version:       2,
		OpenDecisions: 0,
		Conversation:  contractTestHeadless,
		Created:       contractHour(0),
		Changed:       contractHour(7),
		Versions: []*v1.ContractVersion{
			proposed.GetVersions()[0],
			{
				Version:         2,
				Contract:        req.Contract,
				Decisions:       req.Decisions,
				Words:           contractTestReviseWords,
				Writer:          contractTestHeadless,
				Time:            contractHour(3),
				ChangedRows:     []string{"D1"},
				ChangedSections: []string{"decisions"},
			},
		},
		States: []*v1.ContractStateChange{
			wantStateChange("proposed", 1, "session", contractTestHeadless, contractTestWords, 0),
			wantStateChange("set_aside", 1, "session", contractTestHeadless, contractTestSetAsideWords, 1),
			wantStateChange("proposed", 1, "session", contractTestPane, contractTestReopenWords, 2),
			wantStateChange("accepted", 2, "owner", "", contractTestAcceptWords, 4),
			wantStateChange("set_aside", 2, "session", contractTestHeadless, contractTestSetAsideWords, 5),
			wantStateChange("proposed", 2, "session", contractTestPane, contractTestReopenWords, 6),
			wantStateChange("accepted", 2, "session", contractTestPane, contractTestAcceptWords, 7),
		},
	}
	got, err := server.contracts.GetContract(ctx, connect.NewRequest(&v1.GetContractRequest{Id: epicServiceID}))
	if err != nil {
		t.Fatalf("GetContract: %v", err)
	}
	if !proto.Equal(got.Msg.GetContract(), want) {
		t.Errorf("GetContract = %v, want %v", got.Msg.GetContract(), want)
	}
}

// TestContractRecordReviseRefusesAMovedVersion checks that a revise whose version another revise moved after the
// service read the contract is refused as moved, and that the store keeps the other revise and nothing of this one.
func TestContractRecordReviseRefusesAMovedVersion(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	mover := &movingContractStore{Store: st}
	server := startContractServer(t, st, &Contracts{Store: mover, Now: contractTestAt, Getenv: epicTestGetenv})
	seedProposed(t, server)

	mover.moveNext.Store(true)
	_, err := reviseContract(t.Context(), server.contracts, headlessCaller, reviseRequest(server.repo))
	wantContractRefusal(t, err, connect.CodeAborted, "version: moved, read the contract again")

	record, err := st.Contract(t.Context(), epicServiceID)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	if n := len(record.Versions); n != 2 {
		t.Fatalf("store holds %d versions, want 2", n)
	}
	if words := record.Versions[1].Words; words != "Synthetic moved words." {
		t.Errorf("version 2 words = %q, want the other revise's words", words)
	}
}

// contractStateRefusal is one call of a Contracts state RPC the server refuses: the rows the store holds before it, the
// call, and the refusal it gets.
type contractStateRefusal struct {
	name    string
	seed    func(t *testing.T, server contractTestServer)
	call    func(t *testing.T, server contractTestServer) error
	code    connect.Code
	message string
}

// checkContractStateRefusals runs each refusal of a Contracts state RPC against a fresh store. Each gets the refusal its
// reason names, and the store holds the same contracts after it as before.
func checkContractStateRefusals(t *testing.T, cases []contractStateRefusal) {
	t.Helper()
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

			wantContractRefusal(
				t, tc.call(t, server), tc.code, strings.ReplaceAll(tc.message, contractTestRepoToken, server.repo),
			)

			after, err := st.Contracts(t.Context())
			if err != nil {
				t.Fatalf("Contracts: %v", err)
			}
			if !slices.Equal(before, after) {
				t.Errorf("refused call changed the stored contracts: %v, want %v", after, before)
			}
		})
	}
}

// TestContractRecordReviseRefusesAndStoresNothing checks that each revise the Contracts service refuses gets the refusal
// its reason names, and that the store holds the same contracts after it as before.
func TestContractRecordReviseRefusesAndStoresNothing(t *testing.T) {
	t.Parallel()
	checkContractStateRefusals(t, []contractStateRefusal{
		{
			name: "another conversation",
			seed: seedProposed,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				_, err := reviseContract(t.Context(), server.contracts, paneCaller, reviseRequest(server.repo))
				return err
			},
			code:    connect.CodePermissionDenied,
			message: "caller: not the contract's conversation",
		},
		{
			name: "contract not proposed",
			seed: seedAccepted,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				_, err := reviseContract(t.Context(), server.contracts, headlessCaller, reviseRequest(server.repo))
				return err
			},
			code:    connect.CodeFailedPrecondition,
			message: "state: not proposed",
		},
		{
			name: "without words",
			seed: seedProposed,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				req := reviseRequest(server.repo)
				req.Words = ""
				_, err := reviseContract(t.Context(), server.contracts, headlessCaller, req)
				return err
			},
			code:    connect.CodeInvalidArgument,
			message: "words: empty",
		},
		{
			name: "no such contract",
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				req := reviseRequest(server.repo)
				req.Id = contractTestMissing
				req.Contract.Id = contractTestMissing
				_, err := reviseContract(t.Context(), server.contracts, headlessCaller, req)
				return err
			},
			code:    connect.CodeNotFound,
			message: "contract " + contractTestMissing + " not found",
		},
		{
			name: "caller resolves to no session",
			seed: seedProposed,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				_, err := reviseContract(t.Context(), server.contracts, ownerCaller, reviseRequest(server.repo))
				return err
			},
			code:    connect.CodePermissionDenied,
			message: "caller: resolves to no session",
		},
		{
			name: "id differs from the body's",
			seed: seedProposed,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				req := reviseRequest(server.repo)
				req.Id = "synthetic-other/id"
				_, err := reviseContract(t.Context(), server.contracts, headlessCaller, req)
				return err
			},
			code:    connect.CodeInvalidArgument,
			message: "id: differs from the contract's id",
		},
		{
			name: "body is refused",
			seed: seedProposed,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				req := reviseRequest(server.repo)
				req.Contract.Intent = ""
				_, err := reviseContract(t.Context(), server.contracts, headlessCaller, req)
				return err
			},
			code:    connect.CodeInvalidArgument,
			message: "intent: empty",
		},
		{
			name: "capture element holds white space",
			seed: seedProposed,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				req := reviseRequest(server.repo)
				req.Contract.Requirements[0].Item.Capture = []string{"scripts/in-devenv", "go test -run X ./pkg/"}
				_, err := reviseContract(t.Context(), server.contracts, headlessCaller, req)
				return err
			},
			code: connect.CodeInvalidArgument,
			message: "requirements[0].item.capture[1]: holds white space: split the element into argv elements, " +
				"or pass the line to a shell after -c",
		},
		{
			name: "reviewer the merge refuses",
			seed: seedProposed,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				req := reviseRequest(server.repo)
				req.Contract.Reviewers = []*v1.EpicReviewer{{Name: "floor", Limit: new(int64(0))}}
				_, err := reviseContract(t.Context(), server.contracts, headlessCaller, req)
				return err
			},
			code: connect.CodeInvalidArgument,
			message: "merge the reviewers of " + contractTestRepoToken +
				": epic_create: reviewers[0] floor: limit: must be at least 1 second",
		},
		{
			name: "decision has an empty id",
			seed: seedProposed,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				req := reviseRequest(server.repo)
				req.Decisions[0].Id = ""
				_, err := reviseContract(t.Context(), server.contracts, headlessCaller, req)
				return err
			},
			code:    connect.CodeInvalidArgument,
			message: "decisions[0].id: empty",
		},
	})
}

// TestContractRecordAcceptRefusesAndStoresNothing checks that an accept with an open decision, from another state, or of
// a contract no one holds gets the refusal its reason names, and that a refused accept stores nothing.
func TestContractRecordAcceptRefusesAndStoresNothing(t *testing.T) {
	t.Parallel()
	checkContractStateRefusals(t, []contractStateRefusal{
		{
			name: "open decision",
			seed: seedProposed,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				return acceptContract(t.Context(), server.contracts, ownerCaller, epicServiceID)
			},
			code:    connect.CodeFailedPrecondition,
			message: "decisions: contract " + epicServiceID + ": contract has unanswered decisions: D1",
		},
		{
			name: "already accepted",
			seed: seedAccepted,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				return acceptContract(t.Context(), server.contracts, ownerCaller, epicServiceID)
			},
			code:    connect.CodeFailedPrecondition,
			message: "state: not proposed",
		},
		{
			name: "no such contract",
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				return acceptContract(t.Context(), server.contracts, ownerCaller, contractTestMissing)
			},
			code:    connect.CodeNotFound,
			message: "contract " + contractTestMissing + " not found",
		},
	})
}

// TestContractRecordSetAsideRefusesAndStoresNothing checks that each set aside the Contracts service refuses gets the
// refusal its reason names, and that the store holds the same contracts after it as before.
func TestContractRecordSetAsideRefusesAndStoresNothing(t *testing.T) {
	t.Parallel()
	checkContractStateRefusals(t, []contractStateRefusal{
		{
			name: "without words",
			seed: seedProposed,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				return setAsideContract(t.Context(), server.contracts, headlessCaller, epicServiceID, "")
			},
			code:    connect.CodeInvalidArgument,
			message: "words: empty",
		},
		{
			name: "accepted with an epic",
			seed: seedAcceptedWithEpic,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				return setAsideContract(
					t.Context(), server.contracts, headlessCaller, epicServiceID, contractTestSetAsideWords,
				)
			},
			code:    connect.CodeFailedPrecondition,
			message: "state: accepted, and an epic holds its id",
		},
		{
			name: "already set aside",
			seed: seedSetAside,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				return setAsideContract(
					t.Context(), server.contracts, headlessCaller, epicServiceID, contractTestSetAsideWords,
				)
			},
			code:    connect.CodeFailedPrecondition,
			message: "state: not proposed, or accepted with no epic",
		},
		{
			name: "no such contract",
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				return setAsideContract(t.Context(), server.contracts, headlessCaller, contractTestMissing,
					contractTestSetAsideWords)
			},
			code:    connect.CodeNotFound,
			message: "contract " + contractTestMissing + " not found",
		},
	})
}

// TestContractRecordReopenRefusesAndStoresNothing checks that each reopen the Contracts service refuses gets the refusal
// its reason names, and that the store holds the same contracts after it as before.
func TestContractRecordReopenRefusesAndStoresNothing(t *testing.T) {
	t.Parallel()
	checkContractStateRefusals(t, []contractStateRefusal{
		{
			name: "from proposed",
			seed: seedProposed,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				return reopenContract(t.Context(), server.contracts, headlessCaller, epicServiceID)
			},
			code:    connect.CodeFailedPrecondition,
			message: "state: not set aside",
		},
		{
			name: "from accepted",
			seed: seedAccepted,
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				return reopenContract(t.Context(), server.contracts, headlessCaller, epicServiceID)
			},
			code:    connect.CodeFailedPrecondition,
			message: "state: not set aside",
		},
		{
			name: "no such contract",
			call: func(t *testing.T, server contractTestServer) error {
				t.Helper()
				return reopenContract(t.Context(), server.contracts, headlessCaller, contractTestMissing)
			},
			code:    connect.CodeNotFound,
			message: "contract " + contractTestMissing + " not found",
		},
	})
}
