package planner

import (
	"strings"
	"testing"
)

// TestRoleSaysAReleaseItsJudgeStoppedGoesToTheSteerer asserts that the role text says resume_release is refused for a
// release its judge stopped with a verdict, names the park reasons such a stop gives, and tells the planner to ask the
// steerer a cannot-proceed question whose choice to resume is the steerer's own epic_release.
func TestRoleSaysAReleaseItsJudgeStoppedGoesToTheSteerer(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"It is refused too when the release judge stopped the release with its verdict",
		"`judge ended goal_unmet`, `uncovered postconditions` or `the judge did not release the epic`",
		"you do not overrule the judge of your own epic, so return no resume_release for it",
		"Ask the steerer a cannot-proceed question instead",
		"its text names each gap the judge listed and your reading of it",
		"its choice to resume says the steerer resumes the release with epic_release",
		"Only the steerer or the owner resumes such a release.",
	} {
		if !strings.Contains(role, want) {
			t.Errorf("role text lacks %q", want)
		}
	}
}
