package enginehost

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	// The blank import registers the "sqlite3" driver clearSummaries opens the database with.
	_ "github.com/mattn/go-sqlite3"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// summaryContractEpic is the id of the epic the input tests store with every section filled.
const summaryContractEpic = "orrery-1a2b3c4/epic-contract"

// createSummaryContractEpic stores an accepted epic with id, created at the unix second created, whose title, intent and
// approach are the synthetic ones createSummaryEpic stores, and whose other sections each hold a marker text.
func createSummaryContractEpic(t *testing.T, st *store.Store, id string, created int64) {
	t.Helper()
	at := time.Unix(created, 0)
	if err := st.CreateEpic(t.Context(), store.EpicRow{
		ID: id, Repository: "/work/synthetic-repo", Title: "Synthetic epic", Approved: "Approved marker line.",
		Intent: "Synthetic intent.", Care: "Care marker text.", Approach: "Synthetic approach.",
		Failures: []store.EpicFailure{
			{ID: "F1", Text: "Failure marker text.", Rating: "serious", Response: "Response marker text."},
		},
		Additions: []store.EpicAddition{
			{Location: "R1", What: "Addition marker text.", Why: "Addition why marker.", Cost: "Addition cost marker."},
		},
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: "Requirement marker text.", Evidence: "Evidence marker text.", Kind: "check"},
		},
		Premises: []store.EpicPremise{
			{ID: "P1", Text: "Premise marker text.", IfWrong: "If wrong marker."},
		},
		Exclusions: []string{"Exclusion marker text."},
		Alternatives: []store.EpicAlternative{
			{Alternative: "Alternative marker text.", WhyNot: "Why not marker.", Reconsider: "Reconsider marker."},
		},
		ReleaseSteps: []store.EpicReleaseStep{
			{Step: "Release marker step.", Target: "Target marker.", Undo: "Undo marker."},
		},
		ReleaseThen: "Release then marker.", Checks: "Checks marker text.",
		Phase: "accepted", Created: at, Changed: at,
	}); err != nil {
		t.Fatalf("CreateEpic %s: %v", id, err)
	}
}

// TestEpicSummaryInputHoldsTitleIntentAndApproachOnly covers R1: the summary call's input holds the title, intent and
// approach of the epic, and none of the text of its other sections.
func TestEpicSummaryInputHoldsTitleIntentAndApproachOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	claude := haikuWriteFake(t, dir, haikuFake{stdout: summaryResult, status: 0})
	st := epicTestStore(t)
	createSummaryContractEpic(t, st, summaryContractEpic, summaryStamp)

	startSummaryLoop(t, st, claude)
	stdin := haikuReadFile(t, dir, "stdin")
	for _, line := range []string{"Title: Synthetic epic\n", "Intent: Synthetic intent.\n", "Approach: Synthetic approach.\n"} {
		if !strings.Contains(stdin, line) {
			t.Fatalf("stdin = %q, want it to hold %q", stdin, line)
		}
	}
	// The text of each section a summary's input must not hold, and the label each renders under: the approved line,
	// care, failures, additions, requirements, premises, exclusions, alternatives, release steps, the release then line
	// and checks.
	otherSectionText := []string{
		"Approved:", "Approved marker line.",
		"Care:", "Care marker text.",
		"Failures:", "Failure marker text.", "Response marker text.",
		"Additions:", "Addition marker text.", "Addition why marker.", "Addition cost marker.",
		"Requirements:", "Requirement marker text.", "Evidence marker text.",
		"Premises:", "Premise marker text.", "If wrong marker.",
		"Exclusions:", "Exclusion marker text.",
		"Alternatives:", "Alternative marker text.", "Why not marker.", "Reconsider marker.",
		"Release steps:", "Release marker step.", "Target marker.", "Undo marker.",
		"Release then:", "Release then marker.",
		"Checks:", "Checks marker text.",
	}
	for _, text := range otherSectionText {
		if strings.Contains(stdin, text) {
			t.Fatalf("stdin = %q, want no text of the other sections, but it holds %q", stdin, text)
		}
	}
}

// TestEpicSummaryInstructionAsksForOneOrTwoSentencesOfFortyWords covers R1: the instruction the call's input carries
// asks for one or two plain sentences of at most 40 words saying what the epic changes and why, and names no tests,
// no evidence and no release.
func TestEpicSummaryInstructionAsksForOneOrTwoSentencesOfFortyWords(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	claude := haikuWriteFake(t, dir, haikuFake{stdout: summaryResult, status: 0})
	st := epicTestStore(t)
	createSummaryEpic(t, st, summaryEpicA, summaryStamp)

	startSummaryLoop(t, st, claude)
	stdin := haikuReadFile(t, dir, "stdin")
	for _, ask := range []string{
		"one or two plain sentences",
		"at most 40 words",
		"what the epic changes and why",
		"no tests",
		"no evidence",
		"no release",
	} {
		if !strings.Contains(stdin, ask) {
			t.Fatalf("stdin = %q, want the instruction to hold %q", stdin, ask)
		}
	}
}

// TestEpicSummaryWritesAClearedEpicAgainAtStart covers R1: an epic whose summary the rewrite cleared reads with no
// summary at the start, so the start writes its summary again. The summary before the clear is stored through the store,
// then cleared with the same statement the rewrite migration runs.
func TestEpicSummaryWritesAClearedEpicAgainAtStart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	claude := haikuWriteFake(t, dir, haikuFake{stdout: summaryResult, status: 0})
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	st, err := store.Open(t.Context(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	createSummaryEpic(t, st, summaryEpicA, summaryStamp)
	if err := st.SetEpicSummary(t.Context(), summaryEpicA, "Summary before the rewrite."); err != nil {
		t.Fatalf("SetEpicSummary: %v", err)
	}
	clearSummaries(t, path)

	startSummaryLoop(t, st, claude)
	if got := haikuRuns(t, dir); got != 1 {
		t.Fatalf("calls after start = %d, want 1: the cleared epic has no summary", got)
	}
	if got := storedSummaryOf(t, st, summaryEpicA); got != "Fake summary." {
		t.Fatalf("summary after start = %q, want the fake's echo", got)
	}
}

// clearSummaries empties the summary of every epic in the database at path, the statement the rewrite migration runs.
func clearSummaries(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open database for the clear: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database after the clear: %v", err)
		}
	}()
	if _, err := db.Exec(`UPDATE epics SET summary = ''`); err != nil {
		t.Fatalf("clear summaries: %v", err)
	}
}
