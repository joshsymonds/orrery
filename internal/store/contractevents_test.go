package store_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// contractEventOther is the sessions row id of the session that makes the contract tests' changes other than the
// conversation's.
const contractEventOther = "orrery-8"

// addContractSessions stores the idle engine sessions contractWriter, the conversation, and contractEventOther.
func addContractSessions(t *testing.T, s *store.Store) {
	t.Helper()
	ops := make([]store.EngineOp, 0, 2)
	for _, id := range []string{contractWriter, contractEventOther} {
		ops = append(ops, store.EngineAdd{ID: id, Dir: "/work/engine", State: "idle", Kind: store.KindEngine})
	}
	if err := s.ApplyEngine(context.Background(), ops, time.Unix(contractProposedAt, 0)); err != nil {
		t.Fatalf("ApplyEngine add the contract sessions: %v", err)
	}
}

// proposeAnsweredContract proposes id by contractWriter, then revises it to version 2 with every decision answered, so
// the contract can be accepted.
func proposeAnsweredContract(t *testing.T, s *store.Store, id string) {
	t.Helper()
	ctx := context.Background()
	if err := s.ProposeContract(ctx, id, syntheticProposal(contractWriter, contractProposedAt)); err != nil {
		t.Fatalf("ProposeContract %s: %v", id, err)
	}
	if err := s.ReviseContract(ctx, id, contractRevision(2, contractWriter, contractProposedAt+30)); err != nil {
		t.Fatalf("ReviseContract %s: %v", id, err)
	}
}

// contractEventRecipient is the recipient of the contract tests' events: the conversation's sessions row, as
// addContractSessions adds it.
func contractEventRecipient() store.CallerSession {
	return store.CallerSession{ID: contractWriter, Kind: store.KindEngine, State: "idle"}
}

// requireContractEvents fails the test unless ContractEvents returns want.
func requireContractEvents(t *testing.T, s *store.Store, want []store.ContractEvent) {
	t.Helper()
	got, err := s.ContractEvents(context.Background())
	if err != nil {
		t.Fatalf("ContractEvents: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ContractEvents =\n %+v\nwant\n %+v", got, want)
	}
}

func TestContractEpicContractEventsListOthersChangesOnlyInTheirOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	addContractSessions(t, s)
	const id = "orrery-5e6f7a8/contract-events"
	proposeAnsweredContract(t, s, id)
	if err := s.AcceptContract(
		ctx,
		id,
		contractChangeBy("", "Accept the synthetic contract", contractProposedAt+100),
	); err != nil {
		t.Fatalf("AcceptContract by the owner: %v", err)
	}
	if err := s.SetAsideContract(
		ctx,
		id,
		contractChangeBy(contractEventOther, "Set it aside", contractProposedAt+110),
	); err != nil {
		t.Fatalf("SetAsideContract by another session: %v", err)
	}
	if err := s.ReopenContract(
		ctx,
		id,
		contractChangeBy("", "Reopen the synthetic contract", contractProposedAt+120),
	); err != nil {
		t.Fatalf("ReopenContract by the owner: %v", err)
	}

	// The conversation's own contract is set aside by the conversation: its change is owed nothing.
	const own = "orrery-5e6f7a8/contract-own"
	if err := s.ProposeContract(ctx, own, syntheticProposal(contractWriter, contractProposedAt)); err != nil {
		t.Fatalf("ProposeContract %s: %v", own, err)
	}
	if err := s.SetAsideContract(
		ctx,
		own,
		contractChangeBy(contractWriter, "Set my own aside", contractProposedAt+115),
	); err != nil {
		t.Fatalf("SetAsideContract by the conversation: %v", err)
	}

	requireContractEvents(t, s, []store.ContractEvent{
		{
			ID: store.ContractStateEvent(id, 2), Contract: id, Title: "Synthetic contract",
			Recipient: contractEventRecipient(), Seq: 2, State: store.ContractAccepted, Version: 2,
			Session: "", Words: "Accept the synthetic contract",
		},
		{
			ID: store.ContractStateEvent(id, 3), Contract: id, Title: "Synthetic contract",
			Recipient: contractEventRecipient(), Seq: 3, State: store.ContractSetAside, Version: 2,
			Session: contractEventOther, Words: "Set it aside",
		},
		{
			ID: store.ContractStateEvent(id, 4), Contract: id, Title: "Synthetic contract",
			Recipient: contractEventRecipient(), Seq: 4, State: store.ContractProposed, Version: 2,
			Session: "", Words: "Reopen the synthetic contract",
		},
	})
}

func TestContractEpicContractEventStopsOnceMarkedForConversation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	addContractSessions(t, s)
	const id = "orrery-5e6f7a8/contract-marked"
	proposeAnsweredContract(t, s, id)
	if err := s.AcceptContract(
		ctx,
		id,
		contractChangeBy("", "Accept the synthetic contract", contractProposedAt+100),
	); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}
	if got, err := s.ContractEvents(ctx); err != nil || len(got) != 1 {
		t.Fatalf("ContractEvents before the mark = %+v, %v, want the accept", got, err)
	}

	if err := s.MarkCallerEvent(
		ctx, store.ContractStateEvent(id, 2), contractWriter, time.Unix(contractProposedAt, 0),
	); err != nil {
		t.Fatalf("MarkCallerEvent: %v", err)
	}
	requireContractEvents(t, s, nil)
}

func TestContractEpicContractEventSkipsConversationWithoutSessionRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openEpicCallStore(t)
	addContractSessions(t, s)
	const id = "orrery-5e6f7a8/contract-orphaned"
	proposeAnsweredContract(t, s, id)
	if err := s.AcceptContract(
		ctx,
		id,
		contractChangeBy("", "Accept the synthetic contract", contractProposedAt+100),
	); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}
	deleteSessionRow(t, path, contractWriter)

	requireContractEvents(t, s, nil)
}

func TestContractEpicContractStateWriteWakesCallerEventsOnlyForStateChanges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	addContractSessions(t, s)
	woken := 0
	s.OnCallerEvent(func() { woken++ })
	const id = "orrery-5e6f7a8/contract-wake"

	if err := s.ProposeContract(ctx, id, syntheticProposal(contractWriter, contractProposedAt)); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	if err := s.AcceptContract(
		ctx,
		id,
		contractChangeBy("", "Accept too early", contractProposedAt+10),
	); err == nil {
		t.Fatalf("AcceptContract with an open decision succeeded, want a refusal")
	}
	if woken != 0 {
		t.Fatalf("writes that store no state woke caller events %d times, want 0", woken)
	}
	if err := s.ReviseContract(ctx, id, contractRevision(2, contractWriter, contractProposedAt+30)); err != nil {
		t.Fatalf("ReviseContract: %v", err)
	}
	if woken != 0 {
		t.Fatalf("ReviseContract woke caller events %d times, want 0", woken)
	}

	if err := s.AcceptContract(
		ctx,
		id,
		contractChangeBy("", "Accept the synthetic contract", contractProposedAt+100),
	); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}
	if err := s.SetAsideContract(
		ctx,
		id,
		contractChangeBy(contractEventOther, "Set it aside", contractProposedAt+110),
	); err != nil {
		t.Fatalf("SetAsideContract: %v", err)
	}
	if err := s.ReopenContract(
		ctx,
		id,
		contractChangeBy("", "Reopen the synthetic contract", contractProposedAt+120),
	); err != nil {
		t.Fatalf("ReopenContract: %v", err)
	}
	if woken != 3 {
		t.Fatalf("the accept, set aside and reopen woke caller events %d times, want 3", woken)
	}
}
