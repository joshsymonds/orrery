package store_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// leftBody returns the body the contract-left tests propose: a contract at care, with one addition and two release
// steps.
func leftBody(care string) *enginev1.EpicContract {
	return &enginev1.EpicContract{
		Title: "Synthetic left contract", Care: care, Intent: "Synthetic intent.",
		Additions: []*enginev1.EpicAddition{
			{Location: "internal/store", What: "first", Why: "Needed", Cost: "None"},
		},
		ReleaseSteps: []*enginev1.EpicReleaseStep{
			{Step: "Ship the build", Target: "origin", Undo: "Revert the merge"},
			{Step: "Tag the release", Target: "origin", Undo: "Delete the tag"},
		},
	}
}

// leftVersion returns the contract version number of body, written by contractWriter at at. Its decisions are those of
// syntheticProposal: D1 answered and D2 open.
func leftVersion(
	t *testing.T,
	number int64,
	body *enginev1.EpicContract,
	at int64,
) store.ContractVersion {
	t.Helper()
	encoded, err := protojson.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal body: %v", err)
	}
	version := syntheticProposal(contractWriter, at)
	version.Version = number
	version.Care = body.GetCare()
	version.Body = string(encoded)
	return version
}

// leftOf returns the ContractLeft the tests expect for kind and ref, with the phrase the server gives kind.
func leftOf(kind, ref string) store.ContractLeft {
	phrases := map[string]string{
		store.LeftQuestion: "design question not yet answered",
		store.LeftDecision: "decision not yet answered",
		store.LeftAddition: "addition neither kept nor struck",
		store.LeftRefused:  "write refused, and no version stored after it",
		store.LeftRevising: "working copy not yet proposed",
		store.LeftNotShown: "release step not yet shown to the owner",
	}
	return store.ContractLeft{Kind: kind, Ref: ref, Text: phrases[kind]}
}

// unchangedBody is the edit a row write makes when it leaves the body it is given as it is.
func unchangedBody(current store.ContractBody) (store.ContractBody, error) {
	return current, nil
}

// requireContractLeft fails t unless the store and a Reader of the store at path both list want as what is left before
// id is accepted.
func requireContractLeft(t *testing.T, s *store.Store, path, id string, want []store.ContractLeft) {
	t.Helper()
	got, err := s.ContractLeft(t.Context(), id)
	if err != nil {
		t.Fatalf("ContractLeft %s: %v", id, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ContractLeft %s = %+v, want %+v", id, got, want)
	}
	read, err := store.NewReader(path).ContractLeft(t.Context(), id)
	if err != nil {
		t.Fatalf("Reader.ContractLeft %s: %v", id, err)
	}
	if !reflect.DeepEqual(read, want) {
		t.Fatalf("Reader.ContractLeft %s = %+v, want %+v", id, read, want)
	}
}

// requireContractShown fails t unless the store and a Reader of the store at path both list want as the shown marks of
// id.
func requireContractShown(t *testing.T, s *store.Store, path, id string, want []store.ContractShown) {
	t.Helper()
	got, err := s.ContractShown(t.Context(), id)
	if err != nil {
		t.Fatalf("ContractShown %s: %v", id, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ContractShown %s = %+v, want %+v", id, got, want)
	}
	read, err := store.NewReader(path).ContractShown(t.Context(), id)
	if err != nil {
		t.Fatalf("Reader.ContractShown %s: %v", id, err)
	}
	if !reflect.DeepEqual(read, want) {
		t.Fatalf("Reader.ContractShown %s = %+v, want %+v", id, read, want)
	}
}

// TestContractRecordsLeftSevereListsEachKindWithItsRef checks that a severe contract with an open design question
// naming it, an open design question naming none, an open decision, an undecided addition, a working copy and two
// release steps no mark shows lists each kind with its ref, in that order, and that a question naming another contract
// is left out.
func TestContractRecordsLeftSevereListsEachKindWithItsRef(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, path := openEpicCallStore(t)
	const design, id = "orrery-7b8c9d0/design-left", "orrery-7b8c9d0/contract-severe"
	newDesign(t, s, design)
	proposeInDesign(t, s, design, id, leftVersion(t, 1, leftBody("severe"), contractProposedAt))
	for _, named := range []string{id, "", "orrery-7b8c9d0/contract-other"} {
		if _, err := s.AskDesignQuestion(ctx, store.DesignQuestion{
			Design: design, Contract: named, Text: "Which path?", Options: []string{"Path one.", "Path two."},
			Recommendation: "Path one.", Asker: contractWriter, Asked: time.Unix(contractProposedAt, 0),
		}); err != nil {
			t.Fatalf("AskDesignQuestion naming %q: %v", named, err)
		}
	}
	if _, _, err := s.WriteContractRow(ctx, store.ContractRowChange{
		Contract: id, Row: "R1", Action: store.RowActionWrite, Words: "Add a requirement", Session: contractWriter,
		At: contractProposedAt + 20,
	}, unchangedBody); err != nil {
		t.Fatalf("WriteContractRow R1: %v", err)
	}

	requireContractLeft(t, s, path, id, []store.ContractLeft{
		leftOf(store.LeftQuestion, "Q1"),
		leftOf(store.LeftQuestion, "Q2"),
		leftOf(store.LeftDecision, "D2"),
		leftOf(store.LeftAddition, "A1"),
		leftOf(store.LeftRevising, id),
		leftOf(store.LeftNotShown, "S1"),
		leftOf(store.LeftNotShown, "S2"),
	})
}

// TestContractRecordsLeftRefusedWriteLeftUntilAVersionFollows checks that the newest refusal of a write is left while
// no version newer than the version it would have made is stored, that an older refusal is not, and that a revision to
// that version clears it.
func TestContractRecordsLeftRefusedWriteLeftUntilAVersionFollows(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, path := openEpicCallStore(t)
	const id = "orrery-7b8c9d0/contract-refused"
	// designProposal answers D2, so the contract starts with nothing left.
	if err := s.ProposeContract(ctx, id,
		designProposal("/work/synthetic-repo", "Refused contract", contractProposedAt)); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	requireContractLeft(t, s, path, id, nil)

	// A refusal of version 1 would have made no version newer than the newest, so it leaves nothing.
	if err := s.RefuseContract(ctx, id, store.ContractRefusal{
		Version: 1, Field: "title", Text: "title: refused", Session: contractWriter, At: contractProposedAt + 10,
	}); err != nil {
		t.Fatalf("RefuseContract version 1: %v", err)
	}
	requireContractLeft(t, s, path, id, nil)

	if err := s.RefuseContract(ctx, id, store.ContractRefusal{
		Version: 2, Field: "care", Text: "care: refused", Session: contractWriter, At: contractProposedAt + 20,
	}); err != nil {
		t.Fatalf("RefuseContract version 2: %v", err)
	}
	requireContractLeft(t, s, path, id, []store.ContractLeft{leftOf(store.LeftRefused, "care")})

	if err := s.ReviseContract(ctx, id, contractRevision(2, contractWriter, contractProposedAt+30)); err != nil {
		t.Fatalf("ReviseContract: %v", err)
	}
	requireContractLeft(t, s, path, id, nil)
}

// TestContractRecordsLeftSettledContractHasNothingLeft checks that a set-aside contract and an accepted one list
// nothing left, though each had items left while it was proposed, and that the accept goes through with its addition
// undecided and its release steps unshown, as the accept's own rules allow it to.
func TestContractRecordsLeftSettledContractHasNothingLeft(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, path := openEpicCallStore(t)
	const setAside, accepted = "orrery-7b8c9d0/contract-set-aside", "orrery-7b8c9d0/contract-accepted"
	body := leftBody("severe")
	if err := s.ProposeContract(ctx, setAside, leftVersion(t, 1, body, contractProposedAt)); err != nil {
		t.Fatalf("ProposeContract set aside: %v", err)
	}
	requireContractLeft(t, s, path, setAside, []store.ContractLeft{
		leftOf(store.LeftDecision, "D2"),
		leftOf(store.LeftAddition, "A1"),
		leftOf(store.LeftNotShown, "S1"),
		leftOf(store.LeftNotShown, "S2"),
	})
	if err := s.SetAsideContract(ctx, setAside, store.ContractChange{
		Words: "Set it aside", At: contractProposedAt + 40,
	}); err != nil {
		t.Fatalf("SetAsideContract: %v", err)
	}
	requireContractLeft(t, s, path, setAside, nil)

	if err := s.ProposeContract(ctx, accepted, leftVersion(t, 1, body, contractProposedAt)); err != nil {
		t.Fatalf("ProposeContract accepted: %v", err)
	}
	revision := leftVersion(t, 2, body, contractProposedAt+30)
	revision.Decisions[1].Answer = "no"
	if err := s.ReviseContract(ctx, accepted, revision); err != nil {
		t.Fatalf("ReviseContract: %v", err)
	}
	requireContractLeft(t, s, path, accepted, []store.ContractLeft{
		leftOf(store.LeftAddition, "A1"),
		leftOf(store.LeftNotShown, "S1"),
		leftOf(store.LeftNotShown, "S2"),
	})
	if err := s.AcceptContract(ctx, accepted, store.ContractChange{
		Words: "Accept it", At: contractProposedAt + 40,
	}); err != nil {
		t.Fatalf("AcceptContract with items left: %v", err)
	}
	requireContractLeft(t, s, path, accepted, nil)
}

// TestContractRecordsLeftKeptAndShownClearOnlyAtTheirVersion checks that a keep clears an addition and a shown mark
// clears a release step over the version each names alone, so a revision leaves both to be settled again, and that a
// serious contract lists no release step as left.
func TestContractRecordsLeftKeptAndShownClearOnlyAtTheirVersion(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, path := openEpicCallStore(t)
	const severe, serious = "orrery-7b8c9d0/contract-keep-severe", "orrery-7b8c9d0/contract-keep-serious"
	body := leftBody("severe")
	if err := s.ProposeContract(ctx, severe, leftVersion(t, 1, body, contractProposedAt)); err != nil {
		t.Fatalf("ProposeContract severe: %v", err)
	}
	if err := s.ProposeContract(ctx, serious, leftVersion(t, 1, leftBody("serious"), contractProposedAt)); err != nil {
		t.Fatalf("ProposeContract serious: %v", err)
	}
	requireContractLeft(t, s, path, serious, []store.ContractLeft{
		leftOf(store.LeftDecision, "D2"),
		leftOf(store.LeftAddition, "A1"),
	})

	if _, _, err := s.KeepContractAddition(ctx, store.ContractRowChange{
		Contract: severe, Row: "A1", Action: store.RowActionKeep, Words: "Keep the first", Session: contractWriter,
		At: contractProposedAt + 10,
	}, func(store.ContractBody) error { return nil }); err != nil {
		t.Fatalf("KeepContractAddition: %v", err)
	}
	if err := s.MarkContractShown(ctx, severe, 1, []string{"S1"}, contractProposedAt+15); err != nil {
		t.Fatalf("MarkContractShown S1: %v", err)
	}
	requireContractLeft(t, s, path, severe, []store.ContractLeft{
		leftOf(store.LeftDecision, "D2"),
		leftOf(store.LeftNotShown, "S2"),
	})

	revision := leftVersion(t, 2, body, contractProposedAt+30)
	revision.Decisions[1].Answer = "no"
	if err := s.ReviseContract(ctx, severe, revision); err != nil {
		t.Fatalf("ReviseContract: %v", err)
	}
	requireContractLeft(t, s, path, severe, []store.ContractLeft{
		leftOf(store.LeftAddition, "A1"),
		leftOf(store.LeftNotShown, "S1"),
		leftOf(store.LeftNotShown, "S2"),
	})
}

// TestContractRecordsLeftMarkShownKeepsFirstMarkAndRefusesOtherStates checks that MarkContractShown stores a mark for
// each row, keeps the first mark of a row, and is refused for a version that is not the newest and for a contract that
// is not proposed, and that each refusal stores nothing.
func TestContractRecordsLeftMarkShownKeepsFirstMarkAndRefusesOtherStates(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, path := openEpicCallStore(t)
	const id = "orrery-7b8c9d0/contract-marks"
	if err := s.ProposeContract(ctx, id, leftVersion(t, 1, leftBody("severe"), contractProposedAt)); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	first := contractProposedAt + 40
	if err := s.MarkContractShown(ctx, id, 1, []string{"S1", "S1", "S2"}, first); err != nil {
		t.Fatalf("MarkContractShown: %v", err)
	}
	if err := s.MarkContractShown(ctx, id, 1, []string{"S1"}, first+60); err != nil {
		t.Fatalf("MarkContractShown again: %v", err)
	}
	want := []store.ContractShown{
		{Contract: id, Version: 1, Row: "S1", At: first},
		{Contract: id, Version: 1, Row: "S2", At: first},
	}
	requireContractShown(t, s, path, id, want)

	if err := s.MarkContractShown(ctx, id, 2, []string{"S1"}, first+70); !errors.Is(err, store.ErrContractVersion) {
		t.Fatalf("MarkContractShown of version 2 = %v, want ErrContractVersion", err)
	}
	requireContractShown(t, s, path, id, want)

	if err := s.SetAsideContract(ctx, id, store.ContractChange{Words: "Set it aside", At: first + 80}); err != nil {
		t.Fatalf("SetAsideContract: %v", err)
	}
	if err := s.MarkContractShown(ctx, id, 1, []string{"S2"}, first+90); !errors.Is(err, store.ErrContractState) {
		t.Fatalf("MarkContractShown of a set-aside contract = %v, want ErrContractState", err)
	}
	requireContractShown(t, s, path, id, want)
}
