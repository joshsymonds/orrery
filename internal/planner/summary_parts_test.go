package planner

import (
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// summaryPartsEpicID is an invented epic id, which the reading section names.
const summaryPartsEpicID = "epic-k3v8"

// summaryPartsPrompt returns the prompt of turn 1 of an epic whose only field is summaryPartsEpicID.
func summaryPartsPrompt(t *testing.T) string {
	t.Helper()
	return promptOf(t, store.EpicRow{ID: summaryPartsEpicID}, 1, nil)
}

// readingSectionOf returns the Reading the epic section of prompt without its heading, and fails the test when prompt
// has none.
func readingSectionOf(t *testing.T, prompt string) string {
	t.Helper()
	_, rest, found := strings.Cut(prompt, "\n## Reading the epic\n")
	if !found {
		t.Fatalf("prompt has no Reading the epic section:\n%s", prompt)
	}
	section, _, _ := strings.Cut(rest, "\n## ")
	return section
}

// TestSummaryNamesPartsReadCommandWithTheEpicID: the reading section names `orrery epic get <id> --part` with the id of
// the epic it is read for.
func TestSummaryNamesPartsReadCommandWithTheEpicID(t *testing.T) {
	t.Parallel()
	section := readingSectionOf(t, summaryPartsPrompt(t))
	if want := "orrery epic get " + summaryPartsEpicID + " --part"; !strings.Contains(section, want) {
		t.Fatalf("reading section does not name %q:\n%s", want, section)
	}
}

// TestSummaryNamesPartsEveryPartAndFlag: the reading section lists the parts, names the item flag of each item part,
// and names the paging flags.
func TestSummaryNamesPartsEveryPartAndFlag(t *testing.T) {
	t.Parallel()
	section := readingSectionOf(t, summaryPartsPrompt(t))
	wants := []string{
		"tasks, turns, decisions, questions and history",
		"contract, workflow and report",
		"--part task --task T<n>",
		"--part question --question Q<n>",
		"--part decision --decision <id>",
		"--part turn --turn <n>",
		"--offset",
		"--limit",
	}
	for _, want := range wants {
		if !strings.Contains(section, want) {
			t.Errorf("reading section does not name %q:\n%s", want, section)
		}
	}
}

// TestSummaryNamesPartsSaysThePlannerMayRunIt: the reading section says the prompt is a summary of the epic, that the
// planner may run the read command, and that the read changes nothing.
func TestSummaryNamesPartsSaysThePlannerMayRunIt(t *testing.T) {
	t.Parallel()
	section := readingSectionOf(t, summaryPartsPrompt(t))
	wants := []string{"This prompt is a summary of the epic", "The planner may run", "the read changes nothing"}
	for _, want := range wants {
		if !strings.Contains(section, want) {
			t.Errorf("reading section does not say %q:\n%s", want, section)
		}
	}
}

// TestSummaryNamesPartsBeforeContract: the reading section comes after the role text and before the Contract.
func TestSummaryNamesPartsBeforeContract(t *testing.T) {
	t.Parallel()
	inOrder(t, summaryPartsPrompt(t),
		strings.TrimRight(role, "\n"),
		"\n## Reading the epic\n",
		"\n## Contract\n",
	)
}
