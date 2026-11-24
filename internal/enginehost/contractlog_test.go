package enginehost

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/store"
)

// contractLogContracts returns the Contracts of the design log tests: contractStore, read from clock, with the test
// environment and the fake origin.
func contractLogContracts(contractStore ContractStore, clock *contractClock) *Contracts {
	return &Contracts{
		Store: contractStore, Now: clock.now, Getenv: epicTestGetenv, OriginMain: contractTestOriginMain,
	}
}

// createContractLogDesign stores the design id in st, of the headless session, at the fake clock's start. It fails the
// test on error.
func createContractLogDesign(t *testing.T, st *store.Store, id string) {
	t.Helper()
	design := store.Design{
		ID: id, Title: "Synthetic design", Conversation: contractTestHeadless, Created: contractTestAt(),
	}
	if err := st.CreateDesign(t.Context(), design); err != nil {
		t.Fatalf("CreateDesign %s: %v", id, err)
	}
}

// wantDesignLog checks that the design log of design reads as want, oldest first.
func wantDesignLog(t *testing.T, st *store.Store, design string, want []store.DesignLogLine) {
	t.Helper()
	got, err := st.DesignLog(t.Context(), design)
	if err != nil {
		t.Fatalf("DesignLog %s: %v", design, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DesignLog(%s) = %+v, want %+v", design, got, want)
	}
}

// TestContractLogRecordsLogContractProposedThenAccepted checks that a contract proposed into a design from the headless
// session logs 'proposed' as made by the conversation, and that the owner's accept, which has no caller session, logs
// 'accepted' as made by the owner. Each line carries the fake clock's time and the words of its call, and the design's
// log reads both lines in seq order.
func TestContractLogRecordsLogContractProposedThenAccepted(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	clock := newContractClock()
	server := startContractServer(t, st, contractLogContracts(st, clock))
	createContractLogDesign(t, st, draftTestDesign)

	body := contractTestBody(server.repo)
	if err := proposeIntoDesign(t, server.contracts, draftTestDesign, body); err != nil {
		t.Fatalf("ProposeContract into %s: %v", draftTestDesign, err)
	}
	clock.setHour(1)
	if err := acceptContract(t.Context(), server.contracts, ownerCaller, body.GetId()); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}

	wantDesignLog(t, st, draftTestDesign, []store.DesignLogLine{
		{
			Design: draftTestDesign, Seq: 1, At: contractHour(0), Maker: store.LogMakerConversation,
			Session: contractTestHeadless, What: "proposed", IDs: []string{body.GetId()}, Words: contractTestWords,
		},
		{
			Design: draftTestDesign, Seq: 2, At: contractHour(1), Maker: store.LogMakerOwner,
			What: "accepted", IDs: []string{body.GetId()}, Words: contractTestAcceptWords,
		},
	})
}

// TestContractLogRecordsLogContractEachChange checks that each change to a contract in a design logs its line: a draft
// into the design 'drafted', its propose 'proposed', a revise 'revised to version 2', a set aside 'set aside', a reopen
// 'reopened' and an accept 'accepted', with the maker and session of each call and the words it was given. It also
// checks that a draft with no design logs nothing, and that a propose of a contract in no design logs under the
// contract's own id.
func TestContractLogRecordsLogContractEachChange(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	clock := newContractClock()
	server := startContractServer(t, st, contractLogContracts(st, clock))
	createContractLogDesign(t, st, draftTestDesign)

	body := contractTestBody(server.repo)
	id := body.GetId()
	if _, err := server.contracts.DraftContract(
		t.Context(), draftTestRequest(body, nil, contractTestWords, draftTestDesign)); err != nil {
		t.Fatalf("DraftContract into %s: %v", draftTestDesign, err)
	}
	clock.setHour(1)
	if _, err := proposeContract(t, server.contracts, headlessCaller, body, nil); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	clock.setHour(2)
	revision := reviseRequest(server.repo)
	revision.Decisions = nil
	if _, err := reviseContract(t.Context(), server.contracts, headlessCaller, revision); err != nil {
		t.Fatalf("ReviseContract: %v", err)
	}
	clock.setHour(3)
	if err := setAsideContract(
		t.Context(), server.contracts, headlessCaller, id, contractTestSetAsideWords); err != nil {
		t.Fatalf("SetAsideContract: %v", err)
	}
	clock.setHour(4)
	if err := reopenContract(t.Context(), server.contracts, ownerCaller, id); err != nil {
		t.Fatalf("ReopenContract: %v", err)
	}
	clock.setHour(5)
	if err := acceptContract(t.Context(), server.contracts, ownerCaller, id); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}

	conversation := store.LogMakerConversation
	owner := store.LogMakerOwner
	wantDesignLog(t, st, draftTestDesign, []store.DesignLogLine{
		{Design: draftTestDesign, Seq: 1, At: contractHour(0), Maker: conversation,
			Session: contractTestHeadless, What: "drafted", IDs: []string{id}, Words: contractTestWords},
		{Design: draftTestDesign, Seq: 2, At: contractHour(1), Maker: conversation,
			Session: contractTestHeadless, What: "proposed", IDs: []string{id}, Words: contractTestWords},
		{Design: draftTestDesign, Seq: 3, At: contractHour(2), Maker: conversation,
			Session: contractTestHeadless, What: "revised to version 2", IDs: []string{id},
			Words: contractTestReviseWords},
		{Design: draftTestDesign, Seq: 4, At: contractHour(3), Maker: conversation,
			Session: contractTestHeadless, What: "set aside", IDs: []string{id}, Words: contractTestSetAsideWords},
		{Design: draftTestDesign, Seq: 5, At: contractHour(4), Maker: owner,
			What: "reopened", IDs: []string{id}, Words: contractTestReopenWords},
		{Design: draftTestDesign, Seq: 6, At: contractHour(5), Maker: owner,
			What: "accepted", IDs: []string{id}, Words: contractTestAcceptWords},
	})

	lone := contractTestBody(server.repo)
	lone.Id = designTestSecondID
	if _, err := server.contracts.DraftContract(
		t.Context(), draftTestRequest(lone, nil, contractTestWords, "")); err != nil {
		t.Fatalf("DraftContract with no design: %v", err)
	}
	wantDesignLog(t, st, designTestSecondID, []store.DesignLogLine{})

	clock.setHour(6)
	if _, err := proposeContract(t, server.contracts, headlessCaller, lone, nil); err != nil {
		t.Fatalf("ProposeContract of a contract in no design: %v", err)
	}
	wantDesignLog(t, st, designTestSecondID, []store.DesignLogLine{
		{Design: designTestSecondID, Seq: 1, At: contractHour(6), Maker: conversation,
			Session: contractTestHeadless, What: "proposed", IDs: []string{designTestSecondID},
			Words: contractTestWords},
	})
}

// failingAppendStore is a contract store whose design log appends all fail.
type failingAppendStore struct{ *store.Store }

// AppendDesignLog fails with a fixed error and stores nothing.
func (failingAppendStore) AppendDesignLog(context.Context, store.DesignLogLine) error {
	return errors.New("synthetic append failure")
}

// TestContractLogRecordsLogContractAppendFailureAnswersInternal checks that a propose into a design whose design log
// append fails is answered internal, as the store's other failed writes are.
func TestContractLogRecordsLogContractAppendFailureAnswersInternal(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	clock := newContractClock()
	server := startContractServer(t, st, contractLogContracts(failingAppendStore{st}, clock))
	createContractLogDesign(t, st, draftTestDesign)

	err := proposeIntoDesign(t, server.contracts, draftTestDesign, contractTestBody(server.repo))
	if code := connect.CodeOf(err); err == nil || code != connect.CodeInternal {
		t.Fatalf("ProposeContract with a failed log append: error %v (code %v), want code %v",
			err, code, connect.CodeInternal)
	}
}
