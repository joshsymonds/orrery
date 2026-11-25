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

// designCreatedAt is the second the design tests create their designs at.
const designCreatedAt int64 = 1700005000

// newDesign creates the design id, titled "Synthetic design" and written by contractWriter, at designCreatedAt.
func newDesign(t *testing.T, s *store.Store, id string) {
	t.Helper()
	if err := s.CreateDesign(t.Context(), store.Design{
		ID: id, Title: "Synthetic design", Conversation: contractWriter, Created: time.Unix(designCreatedAt, 0),
	}); err != nil {
		t.Fatalf("CreateDesign %s: %v", id, err)
	}
}

// proposeInDesign proposes the contract id into the design with proposal.
func proposeInDesign(t *testing.T, s *store.Store, design, id string, proposal store.ContractVersion) {
	t.Helper()
	if err := s.ProposeContractInDesign(t.Context(), design, id, proposal); err != nil {
		t.Fatalf("ProposeContractInDesign %s into %s: %v", id, design, err)
	}
}

// designProposal returns the synthetic proposal of a contract in repository repo, titled title, proposed at at. Its
// open decision is answered, so the contract can be accepted.
func designProposal(repo, title string, at int64) store.ContractVersion {
	proposal := syntheticProposal(contractWriter, at)
	proposal.Repository = repo
	proposal.Title = title
	proposal.Decisions[1].Answer = "no"
	return proposal
}

// TestDesignEdgesWithinDesignStored asserts that AddDesignEdge stores an edge between two contracts of one design, that
// the store and the Reader both read it back, and that adding the stored edge again returns nil and leaves the edge as
// it was.
func TestDesignEdgesWithinDesignStored(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openEpicCallStore(t)
	const design = "orrery-4d5e6f7/design-stored"
	const first, second = "orrery-4d5e6f7/contract-first", "orrery-4d5e6f7/contract-second"
	newDesign(t, s, design)
	proposeInDesign(t, s, design, first, designProposal("/work/synthetic-repo", "First contract", contractProposedAt))
	proposeInDesign(
		t, s, design, second, designProposal("/work/synthetic-repo", "Second contract", contractProposedAt+5),
	)

	if err := s.AddDesignEdge(ctx, design, second, first, time.Unix(contractProposedAt+10, 0)); err != nil {
		t.Fatalf("AddDesignEdge: %v", err)
	}
	if err := s.AddDesignEdge(ctx, design, second, first, time.Unix(contractProposedAt+20, 0)); err != nil {
		t.Fatalf("AddDesignEdge of a stored edge: %v", err)
	}

	want := []store.DesignContract{
		{
			ID: first, Title: "First contract", Repository: "/work/synthetic-repo", State: store.ContractProposed,
			Position: 1,
		},
		{
			ID: second, Title: "Second contract", Repository: "/work/synthetic-repo", State: store.ContractProposed,
			Position: 2, After: []string{first},
		},
	}
	got, err := s.Design(ctx, design)
	if err != nil {
		t.Fatalf("Design: %v", err)
	}
	if !reflect.DeepEqual(got.Contracts, want) {
		t.Fatalf("Design contracts = %+v, want %+v", got.Contracts, want)
	}
	fromReader, err := store.NewReader(path).Design(ctx, design)
	if err != nil {
		t.Fatalf("Reader.Design: %v", err)
	}
	if !reflect.DeepEqual(fromReader.Contracts, want) {
		t.Fatalf("Reader.Design contracts = %+v, want %+v", fromReader.Contracts, want)
	}
}

// TestDesignEdgesAcrossDesignsRefused asserts that AddDesignEdge refuses an edge from a contract of one design to a
// contract of another with ErrDesignAcross, names both contracts and both designs in the error, and stores nothing.
func TestDesignEdgesAcrossDesignsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	const left, right = "orrery-4d5e6f7/design-left", "orrery-4d5e6f7/design-right"
	const leftContract, rightContract = "orrery-4d5e6f7/contract-left", "orrery-4d5e6f7/contract-right"
	newDesign(t, s, left)
	newDesign(t, s, right)
	proposeInDesign(
		t, s, left, leftContract, designProposal("/work/synthetic-repo", "Left contract", contractProposedAt),
	)
	proposeInDesign(
		t, s, right, rightContract, designProposal("/work/synthetic-repo", "Right contract", contractProposedAt),
	)

	err := s.AddDesignEdge(ctx, left, leftContract, rightContract, time.Unix(contractProposedAt+10, 0))
	if !errors.Is(err, store.ErrDesignAcross) {
		t.Fatalf("AddDesignEdge across designs = %v, want ErrDesignAcross", err)
	}
	t.Logf("refused edge across designs: %v", err)
	for _, name := range []string{leftContract, left, rightContract, right} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("refusal %q does not name %s", err, name)
		}
	}
	got, err := s.Design(ctx, left)
	if err != nil {
		t.Fatalf("Design: %v", err)
	}
	if len(got.Contracts[0].After) != 0 {
		t.Fatalf("refused edge stored: After = %v, want none", got.Contracts[0].After)
	}
}

// TestDesignEdgesCycleRefused asserts that AddDesignEdge refuses an edge that closes a cycle with ErrDesignCycle, names
// the cycle as a path in the error, refuses a self edge the same way, and stores neither.
func TestDesignEdgesCycleRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	const design = "orrery-4d5e6f7/design-cycle"
	const a, b, c = "orrery-4d5e6f7/contract-a", "orrery-4d5e6f7/contract-b", "orrery-4d5e6f7/contract-c"
	newDesign(t, s, design)
	proposeInDesign(t, s, design, a, designProposal("/work/synthetic-repo", "Cycle contract A", contractProposedAt))
	proposeInDesign(t, s, design, b, designProposal("/work/synthetic-repo", "Cycle contract B", contractProposedAt+5))
	proposeInDesign(t, s, design, c, designProposal("/work/synthetic-repo", "Cycle contract C", contractProposedAt+10))
	if err := s.AddDesignEdge(ctx, design, b, a, time.Unix(contractProposedAt+20, 0)); err != nil {
		t.Fatalf("AddDesignEdge b after a: %v", err)
	}
	if err := s.AddDesignEdge(ctx, design, c, b, time.Unix(contractProposedAt+30, 0)); err != nil {
		t.Fatalf("AddDesignEdge c after b: %v", err)
	}

	err := s.AddDesignEdge(ctx, design, a, c, time.Unix(contractProposedAt+40, 0))
	if !errors.Is(err, store.ErrDesignCycle) {
		t.Fatalf("AddDesignEdge closing a cycle = %v, want ErrDesignCycle", err)
	}
	t.Logf("refused edge closing a cycle: %v", err)
	if cycle := strings.Join([]string{a, c, b, a}, " -> "); !strings.Contains(err.Error(), cycle) {
		t.Fatalf("refusal %q does not name the cycle %s", err, cycle)
	}

	err = s.AddDesignEdge(ctx, design, b, b, time.Unix(contractProposedAt+50, 0))
	if !errors.Is(err, store.ErrDesignCycle) {
		t.Fatalf("AddDesignEdge self edge = %v, want ErrDesignCycle", err)
	}
	t.Logf("refused self edge: %v", err)
	if self := strings.Join([]string{b, b}, " -> "); !strings.Contains(err.Error(), self) {
		t.Fatalf("refusal %q does not name the self edge %s", err, self)
	}

	got, err := s.Design(ctx, design)
	if err != nil {
		t.Fatalf("Design: %v", err)
	}
	want := []store.DesignContract{
		{
			ID:         a,
			Title:      "Cycle contract A",
			Repository: "/work/synthetic-repo",
			State:      store.ContractProposed,
			Position:   1,
		},
		{
			ID: b, Title: "Cycle contract B", Repository: "/work/synthetic-repo", State: store.ContractProposed,
			Position: 2, After: []string{a},
		},
		{
			ID: c, Title: "Cycle contract C", Repository: "/work/synthetic-repo", State: store.ContractProposed,
			Position: 3, After: []string{b},
		},
	}
	if !reflect.DeepEqual(got.Contracts, want) {
		t.Fatalf("Design contracts = %+v, want %+v", got.Contracts, want)
	}
}

// TestStoreDesignsReadBack asserts that Design and Designs read a design whose members come from two repositories, with
// an edge and an epic, and read a contract in no design as a design of one, through the store and the Reader alike.
func TestStoreDesignsReadBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openEpicCallStore(t)
	const design = "orrery-4d5e6f7/design-read"
	const member, other = "orrery-4d5e6f7/contract-local", "orrery-4d5e6f7/contract-remote"
	const lone = "orrery-4d5e6f7/contract-alone"
	newDesign(t, s, design)
	proposeInDesign(t, s, design, member, designProposal("/work/synthetic-repo", "Local contract", contractProposedAt))
	proposeInDesign(t, s, design, other, designProposal("/work/other-repo", "Remote contract", contractProposedAt+5))
	if err := s.AddDesignEdge(ctx, design, other, member, time.Unix(contractProposedAt+6, 0)); err != nil {
		t.Fatalf("AddDesignEdge: %v", err)
	}
	if err := s.AcceptContract(
		ctx, member, contractChangeBy("", "Accept the local contract", contractProposedAt+10),
	); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}
	if err := s.CreateContractEpic(ctx, contractEpicRow(member, contractProposedAt+20), 1); err != nil {
		t.Fatalf("CreateContractEpic: %v", err)
	}
	if err := s.ProposeContract(ctx, lone, syntheticProposal(contractWriter, contractProposedAt+30)); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}

	wantDesign := store.DesignRecord{
		Design: store.Design{
			ID: design, Title: "Synthetic design", Conversation: contractWriter, Created: time.Unix(designCreatedAt, 0),
		},
		State: "drafting: 1 of 2 accepted",
		Contracts: []store.DesignContract{
			{
				ID: member, Title: "Local contract", Repository: "/work/synthetic-repo", State: store.ContractAccepted,
				Position: 1, Epic: member, EpicPhase: "accepted",
			},
			{
				ID: other, Title: "Remote contract", Repository: "/work/other-repo", State: store.ContractProposed,
				Position: 2, After: []string{member},
			},
		},
	}
	wantLone := store.DesignRecord{
		Design: store.Design{
			ID: lone, Title: "Synthetic contract", Conversation: contractWriter,
			Created: time.Unix(contractProposedAt+30, 0),
		},
		OfOne: true,
		State: "drafting: 0 of 1 accepted",
		Contracts: []store.DesignContract{
			{
				ID:         lone,
				Title:      "Synthetic contract",
				Repository: "/work/synthetic-repo",
				State:      store.ContractProposed,
				Position:   1,
			},
		},
	}

	reader := store.NewReader(path)
	for _, read := range []struct {
		name string
		get  func() (store.DesignRecord, error)
		want store.DesignRecord
	}{
		{"Design", func() (store.DesignRecord, error) { return s.Design(ctx, design) }, wantDesign},
		{"Reader.Design", func() (store.DesignRecord, error) { return reader.Design(ctx, design) }, wantDesign},
		{"Design of one", func() (store.DesignRecord, error) { return s.Design(ctx, lone) }, wantLone},
		{"Reader.Design of one", func() (store.DesignRecord, error) { return reader.Design(ctx, lone) }, wantLone},
	} {
		got, err := read.get()
		if err != nil {
			t.Fatalf("%s: %v", read.name, err)
		}
		if !reflect.DeepEqual(got, read.want) {
			t.Fatalf("%s = %+v, want %+v", read.name, got, read.want)
		}
	}

	wantAll := []store.DesignRecord{wantDesign, wantLone}
	listed, err := s.Designs(ctx)
	if err != nil {
		t.Fatalf("Designs: %v", err)
	}
	if !reflect.DeepEqual(listed, wantAll) {
		t.Fatalf("Designs = %+v, want %+v", listed, wantAll)
	}
	listedByReader, err := reader.Designs(ctx)
	if err != nil {
		t.Fatalf("Reader.Designs: %v", err)
	}
	if !reflect.DeepEqual(listedByReader, wantAll) {
		t.Fatalf("Reader.Designs = %+v, want %+v", listedByReader, wantAll)
	}
}

// TestDesignMembershipAndIDRefusals asserts the refusals a design call makes with its sentinel errors: an unknown
// design, a contract that is in no design, and an id a design or a contract already holds. Each refused call stores
// nothing.
func TestDesignMembershipAndIDRefusals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	const design = "orrery-4d5e6f7/design-refused"
	const member, loner = "orrery-4d5e6f7/contract-member", "orrery-4d5e6f7/contract-loner"
	const missing, fresh = "orrery-4d5e6f7/design-missing", "orrery-4d5e6f7/contract-new"
	newDesign(t, s, design)
	proposeInDesign(t, s, design, member, designProposal("/work/synthetic-repo", "Member contract", contractProposedAt))
	if err := s.ProposeContract(ctx, loner, syntheticProposal(contractWriter, contractProposedAt+5)); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	at := time.Unix(contractProposedAt+10, 0)

	cases := []struct {
		name string
		want error
		call func() error
	}{
		{"propose into an unknown design", store.ErrNoDesign, func() error {
			return s.ProposeContractInDesign(ctx, missing, fresh, syntheticProposal(contractWriter, contractProposedAt))
		}},
		{"edge in an unknown design", store.ErrNoDesign, func() error {
			return s.AddDesignEdge(ctx, missing, member, loner, at)
		}},
		{"read an unknown design", store.ErrNoDesign, func() error {
			_, err := s.Design(ctx, missing)
			return err
		}},
		{"edge from a contract in no design", store.ErrDesignMember, func() error {
			return s.AddDesignEdge(ctx, design, loner, member, at)
		}},
		{"create a design with a contract's id", store.ErrDesignExists, func() error {
			return s.CreateDesign(
				ctx,
				store.Design{ID: loner, Title: "Clash", Conversation: contractWriter, Created: at},
			)
		}},
		{"create a design with a design's id", store.ErrDesignExists, func() error {
			return s.CreateDesign(
				ctx,
				store.Design{ID: design, Title: "Clash", Conversation: contractWriter, Created: at},
			)
		}},
		{"propose a contract with a design's id", store.ErrContractExists, func() error {
			return s.ProposeContract(ctx, design, syntheticProposal(contractWriter, contractProposedAt))
		}},
		{"propose into a design a contract's id", store.ErrContractExists, func() error {
			return s.ProposeContractInDesign(ctx, design, member, syntheticProposal(contractWriter, contractProposedAt))
		}},
	}
	for _, tc := range cases {
		err := tc.call()
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
		t.Logf("%s refused: %v", tc.name, err)
	}

	got, err := s.Design(ctx, design)
	if err != nil {
		t.Fatalf("Design: %v", err)
	}
	if len(got.Contracts) != 1 || got.Contracts[0].ID != member || len(got.Contracts[0].After) != 0 {
		t.Fatalf("refused calls changed the design: contracts = %+v, want only %s", got.Contracts, member)
	}
}
