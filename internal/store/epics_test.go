package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// epicCreated returns the unix second every test epic is created at.
func epicCreated() time.Time {
	return time.Unix(1700000000, 0)
}

// fullEpic returns a synthetic contract with every section at two or more rows. Its seed decisions carry a seq and
// a time that CreateEpic must ignore: the seeds are numbered from 1 and dated at the epic's creation.
func fullEpic(id string) EpicRow {
	return EpicRow{
		ID:          id,
		Repository:  "/work/synthetic-repo",
		Title:       "Synthetic epic",
		Approved:    "Approved sentence",
		Intent:      "Store the contract",
		Care:        "serious",
		Approach:    "Ordered rows",
		ReleaseThen: "Then line",
		Checks:      "just check",
		Phase:       "accepted",
		Created:     epicCreated(),
		Changed:     time.Unix(1700000060, 0),
		Failures: []EpicFailure{
			{ID: "F1", Text: "Rows come back reordered", Rating: "serious", Response: "Order by position"},
			{ID: "F2", Text: "A decision changes later", Rating: "severe", Response: "No update query"},
		},
		Additions: []EpicAddition{
			{Location: "R1", What: "Round-trip test", Why: "Proves order", Cost: "small"},
			{Location: "F2", What: "Trigger guard", Why: "Append only", Cost: "small"},
		},
		Requirements: []EpicRequirement{
			{ID: "R1", Text: "Stored as ordered rows", Evidence: "round-trip test", Kind: "check"},
			{ID: "R3", Text: "Append once per client id", Evidence: "store tests", Kind: "review"},
		},
		Premises: []EpicPremise{
			{ID: "P1", Text: "Position keeps insertion order", IfWrong: "Sort by rowid"},
			{ID: "P2", Text: "Seeds arrive in order", IfWrong: "Refuse unordered seeds"},
		},
		Exclusions: []string{"No event ledger", "No engine behavior"},
		Alternatives: []EpicAlternative{
			{Alternative: "One JSON column", WhyNot: "Not queryable by section", Reconsider: "Never"},
			{Alternative: "Event log", WhyNot: "Too much machinery", Reconsider: "If history is needed"},
		},
		ReleaseSteps: []EpicReleaseStep{
			{Step: "Merge", Target: "main", Undo: "Revert the merge"},
			{Step: "Tag", Target: "v1", Undo: "Delete the tag"},
		},
		Decisions: []EpicDecision{
			{Seq: 9, ID: "D1", Text: "Use the store", Time: time.Unix(5, 0)},
			{Seq: 9, ID: "D2", Text: "Rows, not JSON", Time: time.Unix(5, 0)},
		},
	}
}

// seededDecisions is what an epic created from fullEpic reads back as its decisions.
func seededDecisions() []EpicDecision {
	return []EpicDecision{
		{Seq: 1, ID: "D1", Text: "Use the store", Time: epicCreated()},
		{Seq: 2, ID: "D2", Text: "Rows, not JSON", Time: epicCreated()},
	}
}

// headOnly returns row with every section and its decisions cleared, the shape Epics returns.
func headOnly(row EpicRow) EpicRow {
	row.Failures, row.Additions, row.Requirements, row.Premises = nil, nil, nil, nil
	row.Exclusions, row.Alternatives, row.ReleaseSteps, row.Decisions = nil, nil, nil, nil
	return row
}

func openEpicStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedMigrated(t, path)
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

func createEpic(t *testing.T, s *Store, row EpicRow) {
	t.Helper()
	if err := s.CreateEpic(context.Background(), row); err != nil {
		t.Fatalf("CreateEpic %s: %v", row.ID, err)
	}
}

func TestCreatedEpicReadsBackEveryFieldInOrder(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-store"
	createEpic(t, s, fullEpic(id))

	got, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	want := fullEpic(id)
	want.Decisions = seededDecisions()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Epic round trip differs:\n got %+v\nwant %+v", got, want)
	}
}

func TestCreateEpicOfStoredIDIsErrEpicExistsAndChangesNothing(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-store"
	createEpic(t, s, fullEpic(id))

	again := fullEpic(id)
	again.Title = "Rewritten title"
	again.Failures = again.Failures[:1]
	again.Decisions = append(again.Decisions, EpicDecision{ID: "D3", Text: "Late decision"})
	if err := s.CreateEpic(ctx, again); !errors.Is(err, ErrEpicExists) {
		t.Fatalf("second CreateEpic err = %v, want ErrEpicExists", err)
	}

	got, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	want := fullEpic(id)
	want.Decisions = seededDecisions()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("refused CreateEpic changed the stored epic:\n got %+v\nwant %+v", got, want)
	}
	epics, err := s.Epics(ctx)
	if err != nil {
		t.Fatalf("Epics: %v", err)
	}
	if len(epics) != 1 {
		t.Errorf("Epics lists %d epics, want 1", len(epics))
	}
}

func TestEpicsListsEveryEpicByIDWithHeadFields(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const storeID = "orrery-1a2b3c4/epic-store"
	const alphaID = "orrery-1a2b3c4/epic-alpha"
	createEpic(t, s, fullEpic(storeID))
	alpha := fullEpic(alphaID)
	alpha.Title = "Alpha"
	alpha.Phase = "released"
	alpha.Changed = time.Unix(1700000900, 0)
	createEpic(t, s, alpha)

	got, err := s.Epics(ctx)
	if err != nil {
		t.Fatalf("Epics: %v", err)
	}
	want := []EpicRow{headOnly(alpha), headOnly(fullEpic(storeID))}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Epics = %+v, want %+v", got, want)
	}
}

func TestEpicOfUnknownIDIsErrNoEpic(t *testing.T) {
	s := openEpicStore(t)
	if _, err := s.Epic(context.Background(), "orrery-1a2b3c4/missing"); !errors.Is(err, ErrNoEpic) {
		t.Fatalf("Epic of unknown id err = %v, want ErrNoEpic", err)
	}
}

func TestAnswerEpicAppendsSeqThreeAndMovesChanged(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-store"
	createEpic(t, s, fullEpic(id))

	at := time.Unix(1700000500, 0)
	got, err := s.AnswerEpic(ctx, id, "D3", "Answer", at)
	if err != nil {
		t.Fatalf("AnswerEpic: %v", err)
	}
	answer := EpicDecision{Seq: 3, ID: "D3", Text: "Answer", Time: at}
	if !reflect.DeepEqual(got, answer) {
		t.Errorf("AnswerEpic = %+v, want %+v", got, answer)
	}

	epic, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if want := append(seededDecisions(), answer); !reflect.DeepEqual(epic.Decisions, want) {
		t.Errorf("decisions = %+v, want %+v", epic.Decisions, want)
	}
	if !epic.Changed.Equal(at) {
		t.Errorf("Changed = %v, want %v", epic.Changed, at)
	}
}

func TestAnswerEpicWithStoredIDAndTextReturnsStoredRowAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-store"
	createEpic(t, s, fullEpic(id))

	first := time.Unix(1700000500, 0)
	stored, err := s.AnswerEpic(ctx, id, "D3", "Answer", first)
	if err != nil {
		t.Fatalf("first AnswerEpic: %v", err)
	}
	got, err := s.AnswerEpic(ctx, id, "D3", "Answer", first.Add(time.Hour))
	if err != nil {
		t.Fatalf("repeated AnswerEpic: %v", err)
	}
	if !reflect.DeepEqual(got, stored) {
		t.Errorf("repeated AnswerEpic = %+v, want stored %+v", got, stored)
	}

	epic, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if len(epic.Decisions) != 3 {
		t.Errorf("epic has %d decisions, want 3", len(epic.Decisions))
	}
	if !epic.Changed.Equal(first) {
		t.Errorf("Changed = %v, want %v: a repeated answer moved it", epic.Changed, first)
	}
}

func TestAnswerEpicWithStoredIDAndOtherTextIsErrDecisionConflict(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-store"
	createEpic(t, s, fullEpic(id))

	first := time.Unix(1700000500, 0)
	if _, err := s.AnswerEpic(ctx, id, "D3", "Answer", first); err != nil {
		t.Fatalf("first AnswerEpic: %v", err)
	}
	_, err := s.AnswerEpic(ctx, id, "D3", "Other answer", first.Add(time.Hour))
	if !errors.Is(err, ErrDecisionConflict) {
		t.Fatalf("AnswerEpic with other text err = %v, want ErrDecisionConflict", err)
	}

	epic, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	want := append(seededDecisions(), EpicDecision{Seq: 3, ID: "D3", Text: "Answer", Time: first})
	if !reflect.DeepEqual(epic.Decisions, want) {
		t.Errorf("decisions = %+v, want %+v", epic.Decisions, want)
	}
	if !epic.Changed.Equal(first) {
		t.Errorf("Changed = %v, want %v: a refused answer moved it", epic.Changed, first)
	}
}

func TestAnswerEpicOfUnknownEpicIsErrNoEpic(t *testing.T) {
	s := openEpicStore(t)
	_, err := s.AnswerEpic(context.Background(), "orrery-1a2b3c4/missing", "D1", "text", epicCreated())
	if !errors.Is(err, ErrNoEpic) {
		t.Fatalf("AnswerEpic of unknown epic err = %v, want ErrNoEpic", err)
	}
}
