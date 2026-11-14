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
