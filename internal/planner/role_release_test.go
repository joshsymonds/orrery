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

// TestRoleRecoveryStoppedReleaseIsThePlanners asserts that the role text says a stopped release is the planner's to
// recover: its park and trigger name the steps that landed and the failed step's output, the planner chooses among
// resume_release, a replan, send_back, reopen_design and close_epic, every release step runs again after a replan, and
// the planner keeps trying and asks the owner only for a choice that is the owner's.
func TestRoleRecoveryStoppedReleaseIsThePlanners(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"A stopped release is the planner's to recover.",
		"name each step that landed at the attempt, with its target and undo, and the failed step's exit and output.",
		"then chooses: resume_release, which resumes the release where it stopped; a replan with retry_epic;",
		"send_back or reopen_design, each of which also sends the release back to planning; or close_epic.",
		"After a replan, every release step runs again from the new candidate,",
		"each step that runs as code checks its postcondition before it acts,",
		"The planner keeps trying while it sees a way to land the change.",
		"It asks the owner, as a catastrophe question, only for a choice that is genuinely the owner's,",
		"such as running an irreversible step again or giving up on the change.",
	} {
		if !strings.Contains(role, want) {
			t.Errorf("role text lacks %q", want)
		}
	}
}
