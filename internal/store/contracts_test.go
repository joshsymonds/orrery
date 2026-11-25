package store_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// contractWriter is the sessions row id that proposes the contract tests' contracts.
const contractWriter = "orrery-7"

// contractProposedAt is the second the contract tests propose their first contract at.
const contractProposedAt int64 = 1700003000

// syntheticProposal returns a contract version the contract tests propose: one answered decision and one open one.
func syntheticProposal(writer string, at int64) store.ContractVersion {
	return store.ContractVersion{
		Version:    1,
		Repository: "/work/synthetic-repo",
		Title:      "Synthetic contract",
		Care:       "serious",
		Body:       `{"id":"synthetic-contract"}`,
		Decisions: []store.ContractDecision{
			{
				ID: "D1", Question: "Which store?", Options: []string{"sqlite", "postgres"},
				Recommendation: "sqlite", Reason: "Already here", Row: "R1", Answer: "sqlite",
			},
			{
				ID: "D2", Question: "Keep the log?", Options: []string{"yes", "no"},
				Recommendation: "no", Reason: "Unneeded", Row: "F2",
			},
		},
		Words:  "Propose the synthetic contract",
		Writer: writer,
		At:     at,
	}
}

// deleteSessionRow removes the sessions row with id from the store file at path, as a session whose pane is gone has
// its row removed.
func deleteSessionRow(t *testing.T, path, id string) {
	t.Helper()
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open raw handle: %v", err)
	}
	defer func() {
		if err := raw.Close(); err != nil {
			t.Errorf("close raw handle: %v", err)
		}
	}()
	if _, err := raw.Exec(`DELETE FROM sessions WHERE id = ?`, id); err != nil {
		t.Fatalf("delete sessions row %s: %v", id, err)
	}
}

func TestContractRecordProposeStoresVersionOneAndProposedState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	const id = "orrery-5e6f7a8/contract-one"
	proposal := syntheticProposal(contractWriter, contractProposedAt)
	if err := s.ProposeContract(ctx, id, proposal); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}

	got, err := s.Contract(ctx, id)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	wantVersion := store.ContractVersion{
		Version: 1, Repository: proposal.Repository, Title: proposal.Title, Care: proposal.Care, Body: proposal.Body,
		Decisions: proposal.Decisions, Words: proposal.Words, Writer: contractWriter, At: contractProposedAt,
		ChangedRows: nil, ChangedSections: nil,
	}
	if !reflect.DeepEqual(got.Versions, []store.ContractVersion{wantVersion}) {
		t.Fatalf("Versions = %+v, want %+v", got.Versions, []store.ContractVersion{wantVersion})
	}
	wantState := store.ContractStateChange{
		State: store.ContractProposed, Version: 1, Session: contractWriter, Words: proposal.Words,
		At: contractProposedAt,
	}
	if !reflect.DeepEqual(got.States, []store.ContractStateChange{wantState}) {
		t.Fatalf("States = %+v, want %+v", got.States, []store.ContractStateChange{wantState})
	}
}

func TestContractRecordReadsListingFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	const id = "orrery-5e6f7a8/contract-listed"
	if err := s.ProposeContract(ctx, id, syntheticProposal(contractWriter, contractProposedAt)); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}

	want := store.ContractListing{
		ID: id, Repository: "/work/synthetic-repo", Title: "Synthetic contract", Care: "serious",
		State: store.ContractProposed, Version: 1, OpenDecisions: 1, Conversation: contractWriter,
		Created: contractProposedAt, Changed: contractProposedAt,
	}
	got, err := s.Contract(ctx, id)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	if got.ContractListing != want {
		t.Fatalf("Contract listing = %+v, want %+v", got.ContractListing, want)
	}
	listed, err := s.Contracts(ctx)
	if err != nil {
		t.Fatalf("Contracts: %v", err)
	}
	if !reflect.DeepEqual(listed, []store.ContractListing{want}) {
		t.Fatalf("Contracts = %+v, want %+v", listed, []store.ContractListing{want})
	}
}

func TestContractRecordContractsListByCreatedThenID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	proposals := []struct {
		id string
		at int64
	}{
		{"orrery-5e6f7a8/contract-b", contractProposedAt + 60},
		{"orrery-5e6f7a8/contract-c", contractProposedAt},
		{"orrery-5e6f7a8/contract-a", contractProposedAt},
	}
	for _, p := range proposals {
		if err := s.ProposeContract(ctx, p.id, syntheticProposal(contractWriter, p.at)); err != nil {
			t.Fatalf("ProposeContract %s: %v", p.id, err)
		}
	}

	listed, err := s.Contracts(ctx)
	if err != nil {
		t.Fatalf("Contracts: %v", err)
	}
	got := make([]string, 0, len(listed))
	for _, row := range listed {
		got = append(got, row.ID)
	}
	want := []string{
		"orrery-5e6f7a8/contract-a", "orrery-5e6f7a8/contract-c", "orrery-5e6f7a8/contract-b",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Contracts order = %v, want %v", got, want)
	}
}

func TestContractRecordProposeOfHeldIDIsErrContractExistsAndStoresNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	const contractID = "orrery-5e6f7a8/contract-held"
	const epicID = "orrery-5e6f7a8/epic-held"
	if err := s.ProposeContract(ctx, contractID, syntheticProposal(contractWriter, contractProposedAt)); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	other := syntheticProposal("orrery-8", contractProposedAt+60)
	other.Words = "A second proposal of the same id"
	err := s.ProposeContract(ctx, contractID, other)
	if !errors.Is(err, store.ErrContractExists) {
		t.Fatalf("ProposeContract of a held contract id = %v, want ErrContractExists", err)
	}
	got, err := s.Contract(ctx, contractID)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	if len(got.Versions) != 1 || len(got.States) != 1 || got.Versions[0].Words != "Propose the synthetic contract" {
		t.Fatalf("held contract changed: versions %+v, states %+v", got.Versions, got.States)
	}

	epic := store.EpicRow{
		ID: epicID, Repository: "/work/synthetic-repo", Title: "Synthetic epic", Care: "serious", Phase: "accepted",
		Created: time.Unix(contractProposedAt, 0), Changed: time.Unix(contractProposedAt, 0),
		PhaseAt: time.Unix(contractProposedAt, 0),
	}
	if err := s.CreateEpic(ctx, epic); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	err = s.ProposeContract(ctx, epicID, syntheticProposal(contractWriter, contractProposedAt))
	if !errors.Is(err, store.ErrContractExists) {
		t.Fatalf("ProposeContract of a stored epic id = %v, want ErrContractExists", err)
	}
	listed, err := s.Contracts(ctx)
	if err != nil {
		t.Fatalf("Contracts: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != contractID {
		t.Fatalf("Contracts after a refused epic id = %+v, want only %s", listed, contractID)
	}
}

func TestContractRecordUnknownIDIsErrNoContract(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openEpicCallStore(t)

	if _, err := s.Contract(ctx, "orrery-5e6f7a8/contract-missing"); !errors.Is(err, store.ErrNoContract) {
		t.Fatalf("Contract of an unknown id = %v, want ErrNoContract", err)
	}
	_, err := store.NewReader(path).Contract(ctx, "orrery-5e6f7a8/contract-missing")
	if !errors.Is(err, store.ErrNoContract) {
		t.Fatalf("Reader.Contract of an unknown id = %v, want ErrNoContract", err)
	}
}

func TestContractRecordWriterSessionDeletedStillReadsWithWriterID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openEpicCallStore(t)
	const id = "orrery-5e6f7a8/contract-orphaned"
	pane := store.Pane{ID: contractWriter, Pane: "%7"}
	if err := s.SyncPane(ctx, "-L orrery", pane, time.Unix(contractProposedAt, 0)); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	if err := s.ProposeContract(ctx, id, syntheticProposal(contractWriter, contractProposedAt)); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	deleteSessionRow(t, path, contractWriter)

	got, err := s.Contract(ctx, id)
	if err != nil {
		t.Fatalf("Contract after the writer's row is gone: %v", err)
	}
	if got.Conversation != contractWriter || got.Versions[0].Writer != contractWriter ||
		got.States[0].Session != contractWriter {
		t.Fatalf("Contract after the writer's row is gone = %+v, want writer %s throughout", got, contractWriter)
	}
	listed, err := s.Contracts(ctx)
	if err != nil {
		t.Fatalf("Contracts: %v", err)
	}
	if len(listed) != 1 || listed[0].Conversation != contractWriter {
		t.Fatalf("Contracts after the writer's row is gone = %+v, want one contract with writer %s",
			listed, contractWriter)
	}
}

func TestContractRecordReaderReadsWhatStoreReads(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openEpicCallStore(t)
	const id = "orrery-5e6f7a8/contract-read"
	if err := s.ProposeContract(ctx, id, syntheticProposal(contractWriter, contractProposedAt)); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}

	reader := store.NewReader(path)
	fromStore, err := s.Contract(ctx, id)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	fromReader, err := reader.Contract(ctx, id)
	if err != nil {
		t.Fatalf("Reader.Contract: %v", err)
	}
	if !reflect.DeepEqual(fromReader, fromStore) {
		t.Fatalf("Reader.Contract = %+v, want %+v", fromReader, fromStore)
	}
	listedByStore, err := s.Contracts(ctx)
	if err != nil {
		t.Fatalf("Contracts: %v", err)
	}
	listedByReader, err := reader.Contracts(ctx)
	if err != nil {
		t.Fatalf("Reader.Contracts: %v", err)
	}
	if !reflect.DeepEqual(listedByReader, listedByStore) {
		t.Fatalf("Reader.Contracts = %+v, want %+v", listedByReader, listedByStore)
	}
}
