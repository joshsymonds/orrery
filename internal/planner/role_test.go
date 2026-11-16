package planner

import (
	"regexp"
	"strings"
	"testing"
)

// TestRoleNamesTheTaskGateAndEvidenceItem asserts that the role text tells the planner a task names a gate and an
// evidence item, and the parts of that item: capture, supplied, conclusion and judge.
func TestRoleNamesTheTaskGateAndEvidenceItem(t *testing.T) {
	t.Parallel()
	for _, word := range []string{"gate", "evidence", "capture", "supplied", "conclusion", "judge"} {
		pattern := regexp.MustCompile(`\b` + word + `\b`)
		if !pattern.MatchString(role) {
			t.Errorf("role text does not name %q", word)
		}
	}
}

// TestRoleSaysTheCheckSendsBackAnUnknownGateOrJudge asserts that the paragraph on the check's send-back names an
// unknown gate and a judge that does not resolve.
func TestRoleSaysTheCheckSendsBackAnUnknownGateOrJudge(t *testing.T) {
	t.Parallel()
	for _, want := range []string{"sends back", "an unknown gate", "a judge that does not resolve"} {
		if !strings.Contains(role, want) {
			t.Errorf("role text lacks %q", want)
		}
	}
}

// TestRoleNoLongerAsksForACheckCommand asserts that the role text no longer asks a task for a check command.
func TestRoleNoLongerAsksForACheckCommand(t *testing.T) {
	t.Parallel()
	if strings.Contains(role, "check command") {
		t.Errorf("role text still asks for a check command")
	}
}

// TestRoleNamesTheEvidenceKindAndItsThreeValues asserts that the role text names the kind of an evidence item and says
// it is test, capture or supplied.
func TestRoleNamesTheEvidenceKindAndItsThreeValues(t *testing.T) {
	t.Parallel()
	pattern := regexp.MustCompile(`\bkind is test, capture or supplied\b`)
	if !pattern.MatchString(role) {
		t.Errorf("role text does not say the evidence kind is test, capture or supplied")
	}
}

// TestRoleNamesTheTestsOfATestItem asserts that the role text names the tests field and the test item that lists it.
func TestRoleNamesTheTestsOfATestItem(t *testing.T) {
	t.Parallel()
	for _, pattern := range []string{`\btests\b`, `\btest item\b`} {
		if !regexp.MustCompile(pattern).MatchString(role) {
			t.Errorf("role text does not match %q", pattern)
		}
	}
}

// TestRoleSaysEachRequirementIsClaimedByExactlyOneTask asserts that the role text says a Requirement is claimed by
// exactly one task.
func TestRoleSaysEachRequirementIsClaimedByExactlyOneTask(t *testing.T) {
	t.Parallel()
	if !strings.Contains(role, "claimed by exactly one task") {
		t.Errorf("role text does not say each Requirement is claimed by exactly one task")
	}
}

// TestRoleNamesContributes asserts that the role text names contributes, the list of Requirements a task advances
// without claiming.
func TestRoleNamesContributes(t *testing.T) {
	t.Parallel()
	if !regexp.MustCompile(`\bcontributes\b`).MatchString(role) {
		t.Errorf("role text does not name contributes")
	}
}

// TestRoleSaysOutcomeCompleteNeedsEveryRequirementClaimed asserts that the role text ties outcome complete to every
// Requirement of the contract being claimed.
func TestRoleSaysOutcomeCompleteNeedsEveryRequirementClaimed(t *testing.T) {
	t.Parallel()
	if !strings.Contains(role, "complete when every Requirement of the contract is claimed") {
		t.Errorf("role text does not say outcome complete needs every Requirement claimed")
	}
}

// TestRoleSaysTheCheckSendsBackClaimAndContributeFaults asserts that the paragraph on the check's send-back names a
// Requirement claimed twice, a contributed Requirement with no claim, and a Requirement left unclaimed at complete.
func TestRoleSaysTheCheckSendsBackClaimAndContributeFaults(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"a Requirement claimed twice",
		"a contributed Requirement that no task claims",
		"a Requirement left unclaimed at complete",
	} {
		if !strings.Contains(role, want) {
			t.Errorf("role text lacks %q", want)
		}
	}
}

// TestRoleSaysALedgerTriggerCorrectsItsFindingsAndNothingElse asserts that the role text names a ledger trigger, says to
// correct each listed finding and nothing else, and says an empty ledger is outcome complete with no tasks.
func TestRoleSaysALedgerTriggerCorrectsItsFindingsAndNothingElse(t *testing.T) {
	t.Parallel()
	for _, want := range []string{`"ledger:"`, "correct each listed finding", "nothing else", "an empty ledger", "outcome complete with no tasks"} {
		if !strings.Contains(role, want) {
			t.Errorf("role text lacks %q", want)
		}
	}
}

// TestRoleNamesTestChangesAndNoTaskSkipsItsEvidence asserts that the role text names test_changes, says each of its files
// must be one of the task's files, and says a task never skips its own evidence test.
func TestRoleNamesTestChangesAndNoTaskSkipsItsEvidence(t *testing.T) {
	t.Parallel()
	for _, want := range []string{"test_changes", "one of the task's files", "a task never skips its own evidence test"} {
		if !strings.Contains(role, want) {
			t.Errorf("role text lacks %q", want)
		}
	}
}
