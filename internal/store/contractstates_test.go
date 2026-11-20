package store_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// contractRevision returns the contract tests' proposal as version version of a revision written by writer at at, with
// decision D2 answered.
func contractRevision(version int64, writer string, at int64) store.ContractVersion {
	revision := syntheticProposal(writer, at)
	revision.Version = version
	revision.Decisions[1].Answer = "no"
	revision.Words = "Revise the synthetic contract"
	return revision
}

// contractChangeBy returns a state change made by session at at with words. A session of "" is the owner.
func contractChangeBy(session, words string, at int64) store.ContractChange {
	return store.ContractChange{Session: session, Words: words, At: at}
}

// contractStateOf returns the state change of a contract entered in state at version by session with words at at.
func contractStateOf(state string, version int64, session, words string, at int64) store.ContractStateChange {
	return store.ContractStateChange{State: state, Version: version, Session: session, Words: words, At: at}
}

func TestContractRecordReviseAppendsVersionAndKeepsEarlierVersions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	const id = "orrery-5e6f7a8/contract-revise"
	proposal := syntheticProposal(contractWriter, contractProposedAt)
	if err := s.ProposeContract(ctx, id, proposal); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	revision := contractRevision(2, contractWriter, contractProposedAt+30)
	revision.Title = "Revised contract"
	revision.ChangedRows = []string{"R2", "F2"}
	revision.ChangedSections = []string{"Goal"}
	if err := s.ReviseContract(ctx, id, revision); err != nil {
		t.Fatalf("ReviseContract: %v", err)
	}

	got, err := s.Contract(ctx, id)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	if len(got.Versions) != 2 {
		t.Fatalf("Versions = %+v, want versions 1 and 2", got.Versions)
	}
	wantFirst := store.ContractVersion{
		Version: 1, Repository: proposal.Repository, Title: proposal.Title, Care: proposal.Care, Body: proposal.Body,
		Decisions: proposal.Decisions, Words: proposal.Words, Writer: contractWriter, At: contractProposedAt,
	}
	if !reflect.DeepEqual(got.Versions[0], wantFirst) {
		t.Fatalf("Versions[0] = %+v, want %+v", got.Versions[0], wantFirst)
	}
	if !reflect.DeepEqual(got.Versions[1], revision) {
		t.Fatalf("Versions[1] = %+v, want %+v", got.Versions[1], revision)
	}
	wantStates := []store.ContractStateChange{
		contractStateOf(store.ContractProposed, 1, contractWriter, proposal.Words, contractProposedAt),
	}
	if !reflect.DeepEqual(got.States, wantStates) {
		t.Fatalf("States after a revision = %+v, want %+v", got.States, wantStates)
	}
	wantListing := store.ContractListing{
		ID: id, Repository: "/work/synthetic-repo", Title: "Revised contract", Care: "serious",
		State: store.ContractProposed, Version: 2, OpenDecisions: 0, Conversation: contractWriter,
		Created: contractProposedAt, Changed: contractProposedAt + 30,
	}
	if got.ContractListing != wantListing {
		t.Fatalf("Contract listing after a revision = %+v, want %+v", got.ContractListing, wantListing)
	}
}

func TestContractRecordReviseRefusesWrongWriterStaleVersionAndStateAndStoresNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	const id = "orrery-5e6f7a8/contract-refused"
	if err := s.ProposeContract(ctx, id, syntheticProposal(contractWriter, contractProposedAt)); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}

	other := contractRevision(2, "orrery-8", contractProposedAt+30)
	if err := s.ReviseContract(ctx, id, other); !errors.Is(err, store.ErrNotContractConversation) {
		t.Fatalf("ReviseContract by another writer = %v, want ErrNotContractConversation", err)
	}
	if err := s.ReviseContract(
		ctx,
		id,
		contractRevision(1, contractWriter, contractProposedAt+30),
	); !errors.Is(
		err,
		store.ErrContractVersion,
	) {
		t.Fatalf("ReviseContract of version 1 = %v, want ErrContractVersion", err)
	}
	if err := s.ReviseContract(
		ctx,
		id,
		contractRevision(3, contractWriter, contractProposedAt+30),
	); !errors.Is(
		err,
		store.ErrContractVersion,
	) {
		t.Fatalf("ReviseContract of version 3 = %v, want ErrContractVersion", err)
	}
	if err := s.ReviseContract(ctx, id, contractRevision(2, contractWriter, contractProposedAt+30)); err != nil {
		t.Fatalf("ReviseContract of version 2: %v", err)
	}
	if err := s.ReviseContract(
		ctx,
		id,
		contractRevision(2, contractWriter, contractProposedAt+40),
	); !errors.Is(
		err,
		store.ErrContractVersion,
	) {
		t.Fatalf("ReviseContract of version 2 again = %v, want ErrContractVersion", err)
	}
	if err := s.AcceptContract(
		ctx,
		id,
		contractChangeBy("", "Accept the synthetic contract", contractProposedAt+50),
	); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}
	if err := s.ReviseContract(
		ctx,
		id,
		contractRevision(3, contractWriter, contractProposedAt+60),
	); !errors.Is(
		err,
		store.ErrContractState,
	) {
		t.Fatalf("ReviseContract after accept = %v, want ErrContractState", err)
	}
	if err := s.ReviseContract(
		ctx,
		"orrery-5e6f7a8/contract-missing",
		contractRevision(2, contractWriter, contractProposedAt),
	); !errors.Is(
		err,
		store.ErrNoContract,
	) {
		t.Fatalf("ReviseContract of an unknown id = %v, want ErrNoContract", err)
	}

	got, err := s.Contract(ctx, id)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	if len(got.Versions) != 2 ||
		!reflect.DeepEqual(got.Versions[1], contractRevision(2, contractWriter, contractProposedAt+30)) {
		t.Fatalf("Versions after refused revisions = %+v, want versions 1 and 2 only", got.Versions)
	}
	if len(got.States) != 2 {
		t.Fatalf("States after refused revisions = %+v, want proposed and accepted", got.States)
	}
}

func TestContractRecordAcceptRefusesOpenDecisionsAndAcceptsAnsweredVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	const id = "orrery-5e6f7a8/contract-accepted"
	if err := s.ProposeContract(ctx, id, syntheticProposal(contractWriter, contractProposedAt)); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}

	err := s.AcceptContract(ctx, id, contractChangeBy("", "Accept the synthetic contract", contractProposedAt+20))
	if !errors.Is(err, store.ErrOpenDecisions) || !strings.HasSuffix(err.Error(), ": D2") {
		t.Fatalf("AcceptContract with D2 open = %v, want ErrOpenDecisions naming D2", err)
	}
	open := contractRevision(2, contractWriter, contractProposedAt+30)
	open.Decisions = append(open.Decisions, store.ContractDecision{
		ID:             "D3",
		Question:       "Ship it?",
		Options:        []string{"yes", "no"},
		Recommendation: "yes",
		Reason:         "Ready",
		Row:            "R3",
	})
	open.Decisions[1].Answer = ""
	if err := s.ReviseContract(ctx, id, open); err != nil {
		t.Fatalf("ReviseContract with D2 and D3 open: %v", err)
	}
	err = s.AcceptContract(ctx, id, contractChangeBy("", "Accept the synthetic contract", contractProposedAt+40))
	if !errors.Is(err, store.ErrOpenDecisions) || !strings.HasSuffix(err.Error(), ": D2, D3") {
		t.Fatalf("AcceptContract with D2 and D3 open = %v, want ErrOpenDecisions naming D2, D3", err)
	}
	got, err := s.Contract(ctx, id)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	if len(got.States) != 1 {
		t.Fatalf("States after refused accepts = %+v, want only proposed", got.States)
	}

	answered := contractRevision(3, contractWriter, contractProposedAt+50)
	answered.Decisions = open.Decisions
	answered.Decisions[1].Answer = "no"
	answered.Decisions[2].Answer = "yes"
	if err := s.ReviseContract(ctx, id, answered); err != nil {
		t.Fatalf("ReviseContract with every decision answered: %v", err)
	}
	if err := s.AcceptContract(
		ctx,
		id,
		contractChangeBy("", "Accept the synthetic contract", contractProposedAt+100),
	); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}

	got, err = s.Contract(ctx, id)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	wantStates := []store.ContractStateChange{
		contractStateOf(
			store.ContractProposed,
			1,
			contractWriter,
			"Propose the synthetic contract",
			contractProposedAt,
		),
		contractStateOf(store.ContractAccepted, 3, "", "Accept the synthetic contract", contractProposedAt+100),
	}
	if !reflect.DeepEqual(got.States, wantStates) {
		t.Fatalf("States = %+v, want %+v", got.States, wantStates)
	}
	if got.State != store.ContractAccepted || got.Version != 3 || got.OpenDecisions != 0 {
		t.Fatalf(
			"Contract listing after accept = %+v, want accepted at version 3 with no open decisions",
			got.ContractListing,
		)
	}
}

func TestContractRecordSetAsideAndReopenFollowTheirStates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	const id = "orrery-5e6f7a8/contract-set-aside"
	const proposedAt = contractProposedAt
	if err := s.ProposeContract(ctx, id, syntheticProposal(contractWriter, proposedAt)); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}

	if err := s.SetAsideContract(
		ctx,
		id,
		contractChangeBy(contractWriter, "Set the contract aside", proposedAt+10),
	); err != nil {
		t.Fatalf("SetAsideContract from proposed: %v", err)
	}
	if err := s.SetAsideContract(
		ctx,
		id,
		contractChangeBy(contractWriter, "Set it aside again", proposedAt+11),
	); !errors.Is(
		err,
		store.ErrContractState,
	) {
		t.Fatalf("SetAsideContract from set_aside = %v, want ErrContractState", err)
	}
	if err := s.ReopenContract(
		ctx,
		id,
		contractChangeBy(contractWriter, "Reopen the contract", proposedAt+20),
	); err != nil {
		t.Fatalf("ReopenContract from set_aside: %v", err)
	}
	if err := s.ReopenContract(
		ctx,
		id,
		contractChangeBy(contractWriter, "Reopen it again", proposedAt+21),
	); !errors.Is(
		err,
		store.ErrContractState,
	) {
		t.Fatalf("ReopenContract from proposed = %v, want ErrContractState", err)
	}
	if err := s.ReviseContract(ctx, id, contractRevision(2, contractWriter, proposedAt+30)); err != nil {
		t.Fatalf("ReviseContract: %v", err)
	}
	if err := s.AcceptContract(
		ctx,
		id,
		contractChangeBy("", "Accept the synthetic contract", proposedAt+40),
	); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}
	if err := s.SetAsideContract(ctx, id, contractChangeBy("", "Set the contract aside", proposedAt+50)); err != nil {
		t.Fatalf("SetAsideContract from accepted: %v", err)
	}
	if err := s.AcceptContract(
		ctx,
		id,
		contractChangeBy("", "Accept it from set_aside", proposedAt+51),
	); !errors.Is(
		err,
		store.ErrContractState,
	) {
		t.Fatalf("AcceptContract from set_aside = %v, want ErrContractState", err)
	}
	if err := s.ReopenContract(ctx, id, contractChangeBy("", "Reopen the contract", proposedAt+60)); err != nil {
		t.Fatalf("ReopenContract from set_aside at version 2: %v", err)
	}

	got, err := s.Contract(ctx, id)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	wantStates := []store.ContractStateChange{
		contractStateOf(store.ContractProposed, 1, contractWriter, "Propose the synthetic contract", proposedAt),
		contractStateOf(store.ContractSetAside, 1, contractWriter, "Set the contract aside", proposedAt+10),
		contractStateOf(store.ContractProposed, 1, contractWriter, "Reopen the contract", proposedAt+20),
		contractStateOf(store.ContractAccepted, 2, "", "Accept the synthetic contract", proposedAt+40),
		contractStateOf(store.ContractSetAside, 2, "", "Set the contract aside", proposedAt+50),
		contractStateOf(store.ContractProposed, 2, "", "Reopen the contract", proposedAt+60),
	}
	if !reflect.DeepEqual(got.States, wantStates) {
		t.Fatalf("States = %+v, want %+v", got.States, wantStates)
	}
	if got.State != store.ContractProposed || got.Version != 2 || got.Changed != proposedAt+60 {
		t.Fatalf("Contract listing = %+v, want proposed at version 2 changed at the reopen", got.ContractListing)
	}
}

func TestContractRecordSetAsideRefusedWhenEpicHoldsIDAndUnknownIDStoresNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	const id = "orrery-5e6f7a8/contract-epic"
	if err := s.ProposeContract(ctx, id, syntheticProposal(contractWriter, contractProposedAt)); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	if err := s.ReviseContract(ctx, id, contractRevision(2, contractWriter, contractProposedAt+30)); err != nil {
		t.Fatalf("ReviseContract: %v", err)
	}
	if err := s.AcceptContract(
		ctx,
		id,
		contractChangeBy("", "Accept the synthetic contract", contractProposedAt+40),
	); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}
	epic := store.EpicRow{
		ID: id, Repository: "/work/synthetic-repo", Title: "Synthetic epic", Care: "serious", Phase: "accepted",
		Created: time.Unix(contractProposedAt+40, 0), Changed: time.Unix(contractProposedAt+40, 0),
		PhaseAt: time.Unix(contractProposedAt+40, 0),
	}
	if err := s.CreateEpic(ctx, epic); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}

	if err := s.SetAsideContract(
		ctx,
		id,
		contractChangeBy("", "Set the contract aside", contractProposedAt+50),
	); !errors.Is(
		err,
		store.ErrContractEpic,
	) {
		t.Fatalf("SetAsideContract of a contract an epic holds = %v, want ErrContractEpic", err)
	}
	const missing = "orrery-5e6f7a8/contract-missing"
	change := contractChangeBy("", "Settle an unknown contract", contractProposedAt+60)
	if err := s.AcceptContract(ctx, missing, change); !errors.Is(err, store.ErrNoContract) {
		t.Fatalf("AcceptContract of an unknown id = %v, want ErrNoContract", err)
	}
	if err := s.SetAsideContract(ctx, missing, change); !errors.Is(err, store.ErrNoContract) {
		t.Fatalf("SetAsideContract of an unknown id = %v, want ErrNoContract", err)
	}
	if err := s.ReopenContract(ctx, missing, change); !errors.Is(err, store.ErrNoContract) {
		t.Fatalf("ReopenContract of an unknown id = %v, want ErrNoContract", err)
	}

	got, err := s.Contract(ctx, id)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	if len(got.States) != 2 || got.State != store.ContractAccepted {
		t.Fatalf("States after refused set aside = %+v, want proposed then accepted", got.States)
	}
	listed, err := s.Contracts(ctx)
	if err != nil {
		t.Fatalf("Contracts: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != id {
		t.Fatalf("Contracts after unknown-id writes = %+v, want only %s", listed, id)
	}
}
