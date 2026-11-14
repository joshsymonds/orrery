package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// evidenceEpic returns fullEpic with a base, two gates named out of alphabetical order, a Done list and a review list,
// and two Requirements in R2, R1 order: one with a capture item and one with a supplied item.
func evidenceEpic(id string) EpicRow {
	row := fullEpic(id)
	row.Base = "main"
	row.Requirements = []EpicRequirement{
		{
			ID: "R2", Text: "Supplied review reads back", Evidence: "reviewer notes", Kind: "review",
			Item: evidence.Item{
				Supplied:   []string{"docs/review.md"},
				Conclusion: "The review passes",
				Judge:      "Read the notes",
			},
		},
		{
			ID: "R1", Text: "Captured run reads back", Evidence: "store round trip", Kind: "check",
			Item: evidence.Item{
				Capture:    []string{"go", "test", "./internal/store/"},
				Limit:      600,
				Artifacts:  []string{"out/store.json"},
				Conclusion: "The store round-trips",
				Judge:      "Read the test output",
			},
		},
	}
	row.Gates = []EpicGate{
		{
			Name: "verify", Argv: []string{"just", "check"}, Limit: 1800,
			Needs: []EpicGateNeed{{Pool: "gate", Count: 2}, {Pool: "heavy", Count: 1}},
		},
		{
			Name: "review", Agent: "reviewer",
			Must:    []string{"Cite file and line", "Name every finding"},
			MustNot: []string{"Edit files"},
		},
	}
	row.DoneGates = []string{"verify", "review"}
	row.ReviewGates = []string{"review"}
	return row
}

func TestCreatedEpicReadsBaseGatesListsAndItemsInOrder(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-evidence"
	createEpic(t, s, evidenceEpic(id))

	got, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	want := evidenceEpic(id)
	want.Decisions = seededDecisions()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Epic round trip differs:\n got %+v\nwant %+v", got, want)
	}
}

func TestEpicWithoutItemReadsZeroItemAndStoresEmptyColumn(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-plain"
	createEpic(t, s, fullEpic(id))

	got, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	for position, requirement := range got.Requirements {
		if !reflect.DeepEqual(requirement.Item, evidence.Item{}) {
			t.Errorf("requirement %d Item = %+v, want the zero Item", position+1, requirement.Item)
		}
	}
	if got.Base != "" || got.Gates != nil || got.DoneGates != nil || got.ReviewGates != nil {
		t.Errorf("epic without evidence reads base %q, gates %v, done %v, review %v; want empty",
			got.Base, got.Gates, got.DoneGates, got.ReviewGates)
	}

	rows, err := s.db.QueryContext(ctx, `SELECT item FROM epic_requirements WHERE epic = ? ORDER BY position`, id)
	if err != nil {
		t.Fatalf("read stored items: %v", err)
	}
	defer rows.Close()
	var stored []string
	for rows.Next() {
		var item string
		if err := rows.Scan(&item); err != nil {
			t.Fatalf("scan stored item: %v", err)
		}
		stored = append(stored, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate stored items: %v", err)
	}
	if want := []string{"", ""}; !reflect.DeepEqual(stored, want) {
		t.Errorf("stored items = %q, want %q: a zero Item is stored as the empty string", stored, want)
	}
}
