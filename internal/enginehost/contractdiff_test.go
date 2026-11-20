package enginehost

import (
	"slices"
	"testing"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// contractRecordVersion returns a fresh version 1 of a contract with two failures, two requirements, three premises
// and one decision, each test changing the copy it needs.
func contractRecordVersion() *v1.ContractVersion {
	return &v1.ContractVersion{
		Version: 1,
		Contract: &v1.EpicContract{
			Id:         "lumen-3c9a1d2/ledger-sync",
			Repository: "/srv/lumen",
			Title:      "Ledger sync",
			Intent:     "Keep the ledger in step with the bank feed.",
			Approach:   "Pull the feed on a schedule and merge by transaction id.",
			Failures: []*v1.EpicFailure{
				{
					Id:       "F1",
					Text:     "A pull drops a transaction.",
					Rating:   "serious",
					Response: "Count rows after each pull.",
				},
				{Id: "F2", Text: "A merge double counts.", Rating: "limited", Response: "Merge on the transaction id."},
			},
			Requirements: []*v1.EpicRequirement{
				{Id: "R1", Text: "Every feed row lands once."},
				{Id: "R2", Text: "A rerun changes nothing."},
			},
			Premises: []*v1.EpicPremise{
				{Id: "P1", Text: "The feed keeps transaction ids.", IfWrong: "Merge on the amount and date."},
				{Id: "P2", Text: "The feed is in UTC.", IfWrong: "Convert before the merge."},
				{Id: "P3", Text: "The feed never reuses an id.", IfWrong: "Keep a second key."},
			},
		},
		Decisions: []*v1.ContractDecision{
			{
				Id:             "D1",
				Question:       "Which merge key?",
				Options:        []string{"id", "amount"},
				Recommendation: "id",
				Reason:         "The feed sends it.",
				Row:            "R1",
			},
		},
	}
}

// TestContractRecordChangesIdentical asserts that two copies of the same version give no changed rows and no changed
// sections.
func TestContractRecordChangesIdentical(t *testing.T) {
	t.Parallel()
	rows, sections := contractChanges(contractRecordVersion(), contractRecordVersion())
	if len(rows) != 0 {
		t.Fatalf("contractChanges rows = %q, want none", rows)
	}
	if len(sections) != 0 {
		t.Fatalf("contractChanges sections = %q, want none", sections)
	}
}

// TestContractRecordChangesRows asserts that a changed failure, an added requirement, a changed premise with two
// removed premises after it, and a changed decision answer give their ids as rows in section 5's order: failures,
// requirements, premises, decisions. Within a list the changed and added ids come in next's order, then the removed
// ids in prev's order. It also asserts the sections the same change makes.
func TestContractRecordChangesRows(t *testing.T) {
	t.Parallel()
	prev := contractRecordVersion()
	next := contractRecordVersion()
	next.Contract.Failures[0].Text = "A pull drops a transaction on retry."
	next.Contract.Requirements = append(
		next.Contract.Requirements,
		&v1.EpicRequirement{Id: "R3", Text: "A late row lands."},
	)
	next.Contract.Premises = next.Contract.Premises[:1]
	next.Contract.Premises[0].IfWrong = "Merge on the amount, then the date."
	next.Decisions[0].Answer = "id"

	rows, sections := contractChanges(prev, next)
	wantRows := []string{"F1", "R3", "P1", "P2", "P3", "D1"}
	if !slices.Equal(rows, wantRows) {
		t.Fatalf("contractChanges rows = %q, want %q", rows, wantRows)
	}
	wantSections := []string{"failures", "requirements", "premises", "decisions"}
	if !slices.Equal(sections, wantSections) {
		t.Fatalf("contractChanges sections = %q, want %q", sections, wantSections)
	}
}

// TestContractRecordChangesSectionsInFieldOrder asserts that a changed approach and a changed intent give their section
// names in field-number order, intent (5) before approach (12), and no row.
func TestContractRecordChangesSectionsInFieldOrder(t *testing.T) {
	t.Parallel()
	next := contractRecordVersion()
	next.Contract.Approach = "Pull the feed hourly and merge by transaction id."
	next.Contract.Intent = "Keep the ledger in step with the bank feed, hourly."

	rows, sections := contractChanges(contractRecordVersion(), next)
	if len(rows) != 0 {
		t.Fatalf("contractChanges rows = %q, want none", rows)
	}
	want := []string{"intent", "approach"}
	if !slices.Equal(sections, want) {
		t.Fatalf("contractChanges sections = %q, want %q", sections, want)
	}
}

// TestContractRecordChangesIDIsNeverASection asserts that a changed contract id gives neither a row nor a section.
func TestContractRecordChangesIDIsNeverASection(t *testing.T) {
	t.Parallel()
	next := contractRecordVersion()
	next.Contract.Id = "lumen-3c9a1d2/ledger-sync-two"

	rows, sections := contractChanges(contractRecordVersion(), next)
	if len(rows) != 0 || len(sections) != 0 {
		t.Fatalf("contractChanges = rows %q, sections %q, want none", rows, sections)
	}
}

// TestContractRecordChangesDecisionAnswer asserts that a changed decision answer alone gives its id as a row and the
// decisions section, and no contract section.
func TestContractRecordChangesDecisionAnswer(t *testing.T) {
	t.Parallel()
	next := contractRecordVersion()
	next.Decisions[0].Answer = "amount"

	rows, sections := contractChanges(contractRecordVersion(), next)
	if !slices.Equal(rows, []string{"D1"}) {
		t.Fatalf("contractChanges rows = %q, want [D1]", rows)
	}
	if !slices.Equal(sections, []string{"decisions"}) {
		t.Fatalf("contractChanges sections = %q, want [decisions]", sections)
	}
}
