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

// TestRoleNamesTheUnclaimedRequirementsSection asserts that the role text names the prompt section that lists each
// Requirement no live task claims, under the heading the prompt renders it with.
func TestRoleNamesTheUnclaimedRequirementsSection(t *testing.T) {
	t.Parallel()
	if !strings.Contains(role, "Unclaimed requirements") {
		t.Errorf("role text does not name the Unclaimed requirements section")
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

// TestRoleBriefCarriesTaskDecisionsInTheDeliverable asserts that the role text says the implementer reads only its task,
// so every decision it must follow goes in the Deliverable.
func TestRoleBriefCarriesTaskDecisionsInTheDeliverable(t *testing.T) {
	t.Parallel()
	t.Run(
		"the planner role says every decision the implementer must follow goes in the Deliverable",
		func(t *testing.T) {
			t.Parallel()
			for _, want := range []string{
				"implementer reads only its task",
				"every decision it must follow goes in the Deliverable",
			} {
				if !strings.Contains(role, want) {
					t.Errorf("role text lacks %q", want)
				}
			}
		},
	)
}

// TestRoleDeliverableStatesOutcomesAndChecksRuntimeFacts asserts that the role text says a deliverable states outcomes,
// and that a runtime fact it names is either checked, with the command or file:line that showed it at the base, or
// marked unchecked in the deliverable's own words.
func TestRoleDeliverableStatesOutcomesAndChecksRuntimeFacts(t *testing.T) {
	t.Parallel()
	t.Run(
		"the planner role says a deliverable states outcomes and checks or marks its runtime facts",
		func(t *testing.T) {
			t.Parallel()
			for _, want := range []string{
				"a deliverable states outcomes",
				"the command or file:line that showed it at the base",
				"marked unchecked in the deliverable's own words",
			} {
				if !strings.Contains(role, want) {
					t.Errorf("role text lacks %q", want)
				}
			}
		},
	)
}

// TestRoleEvidenceShown: the role text says how the evidence judge reads a capture, so a conclusion is a list of what
// the output shows.
func TestRoleEvidenceShown(t *testing.T) {
	t.Parallel()
	t.Run("the planner role says the judge reads the capture's exit and output strictly", func(t *testing.T) {
		t.Parallel()
		want := "The evidence judge reads the capture's exit and output strictly and fails any clause they do not " +
			"visibly show"
		if !strings.Contains(role, want) {
			t.Errorf("role text lacks %q", want)
		}
	})
}

// TestRoleTellsThePlannerWhatFailsATaskBeforeItsCodeDoes: the role text names the plan mistakes that fail a task whose
// code is right: a conclusion that holds at the base, a test item for a change of tests only, a spent line tried again,
// and a deliverable that forbids the change its gate needs.
func TestRoleTellsThePlannerWhatFailsATaskBeforeItsCodeDoes(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"So each conclusion names at least one thing the base's output cannot show",
		"Before you return a task, read the base and confirm its conclusion is false there.",
		"So a task that only adds tests for behavior the base already has, rewrites a test, or fixes a flake takes " +
			"kind capture with no tests. The check refuses kind test on a task whose files are all test files.",
		"A further try of a spent line is your judgment",
		"Otherwise ask the owner a catastrophe question",
		"So a deliverable never forbids such a change.",
		"its implementer declares its changes as gate-needed, and the epic's conformance review reads that " +
			"declaration.",
	} {
		if !strings.Contains(role, want) {
			t.Errorf("role text lacks %q", want)
		}
	}
}

// TestRoleInterfaceWave asserts that the prompt tells the planner the first wave fixes the epic's interfaces and code
// design, its schemas, generated code, exported types, signatures and test seams, unless the epic has none, and that a
// plan skipping the wave says why in a decision.
func TestRoleInterfaceWave(t *testing.T) {
	t.Parallel()
	prompt := promptOf(t, syntheticEpic(), 2, syntheticTriggers())
	for _, want := range []string{
		"The first wave must fix the epic's interfaces and code design: its schemas, generated code, exported types, " +
			"signatures and test seams, unless the epic has none.",
		"A plan that skips the wave says why in a decision.",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
}

// TestRoleDependsOn asserts that the prompt describes depends_on, with each entry a T<n> or the exact title of another
// task in the return, says a task starts once every task it depends on is integrated, carries the shared-file rule, and
// no longer carries the start-now rule or the rule against naming an open task's file.
func TestRoleDependsOn(t *testing.T) {
	t.Parallel()
	prompt := promptOf(t, syntheticEpic(), 2, syntheticTriggers())
	for _, want := range []string{
		"depends_on: a JSON array of strings written as a string",
		"Each entry is T<n> for a task of an earlier turn, or the exact title of another task in this return",
		"starts once every task it depends on is integrated",
		"Two tasks may name the same file only when one depends on the other through depends_on.",
		"A task may name a file an open task owns only when it depends on that task.",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	for _, gone := range []string{
		"Plan only tasks that can start now",
		"Never name a file that an open task already owns",
	} {
		if strings.Contains(prompt, gone) {
			t.Errorf("prompt still contains %q", gone)
		}
	}
}
