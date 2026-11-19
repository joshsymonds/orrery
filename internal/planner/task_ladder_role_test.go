package planner

import (
	"strings"
	"testing"
)

// ladderRoleOldFailTaskSentence is the fail_task sentence role.md gave before a failing task climbed a ladder. The role
// text no longer says it.
const ladderRoleOldFailTaskSentence = "fails it, so a new turn replaces it"

// ladderRoleFailTaskSentence is the sentence role.md gives the planner about fail_task, which climbs a task one rung.
const ladderRoleFailTaskSentence = "fail_task names an open task by its number and climbs it one rung up its ladder, " +
	"with the reason as the failure that moved it; from rung 5 it ends the task as a gap."

// ladderRoleArgsSentence is the change_approach form role.md gives in the list of action args after amend_evidence's.
const ladderRoleArgsSentence = `change_approach {"task": <number>, "deliverable": "<the whole new deliverable>"}, ` +
	`with "item": {<the whole evidence item>} added when the approach changes the evidence, and with "files": ` +
	`[<the repository-relative paths the approach adds>] and "remove": [<the paths of the task's own files it drops>] ` +
	`when it changes the files.`

// ladderRoleChangeApproachSentence is the sentence role.md gives the planner about the change_approach action.
const ladderRoleChangeApproachSentence = "change_approach writes the one change of approach of a task on rung 4, its " +
	"recarve: a version with source approach, which every agent on the task reads, holding deliverable, the whole new " +
	"deliverable, and item, when given, the whole new evidence item, which red judges at the task's base; and files and " +
	"remove, the files the version adds to the task and drops from it, where a file that another open task owns, or an " +
	"accepted task has not yet integrated, is refused and the refusal names its holder. Its reason states what changes " +
	"and why."

// ladderRoleNeverReplacedSentence is the first sentence of the paragraph on the ladder: a failing task is never
// replaced.
const ladderRoleNeverReplacedSentence = "A failing task is never replaced: it keeps its worktree and its work and " +
	"climbs a ladder, one rung for each failure."

// ladderRoleRungsSentence is the sentence on what each rung of the ladder is.
const ladderRoleRungsSentence = "Rungs 1 and 2 are its two attempts, rung 3 the orchestrator's own writing attempt, " +
	"rungs 4 and 5 the two attempts of one change of approach, and a failure on rung 5 ends the task as a gap."

// ladderRoleApproachTriggerSentence is the sentence on the trigger a task climbing onto rung 4 wakes the planner with.
const ladderRoleApproachTriggerSentence = "A task that climbs onto rung 4 wakes you with the trigger T<n>-approach: " +
	"return one change_approach action for it, written from its rungs, its versions and its failure, and plan no task " +
	"in its place."

// ladderRoleGapTriggerSentence is the sentence on the trigger a task ended as a gap wakes the planner with, and on what
// a gap is.
const ladderRoleGapTriggerSentence = "A task that ends as a gap wakes you with the trigger T<n>-gap. A gap is never " +
	"planned again: it keeps the Requirements it claims, a plan whose only unbuilt Requirements are held by gaps ends " +
	"without parking, and the epic ends ended_with_gaps, naming each gap."

// ladderRoleClaimsSentence is the sentence on how a task on the ladder or ended as a gap holds its claims.
const ladderRoleClaimsSentence = "A task on the ladder or ended as a gap holds its claims, so the check refuses a new " +
	"task that claims one of its Requirements."

// TestTaskLadderPlannerRoleDescribesLadder asserts that the role text describes the task ladder the build runs: the
// fail_task climb, change_approach and its T<n>-approach and T<n>-gap triggers, and the rungs.
func TestTaskLadderPlannerRoleDescribesLadder(t *testing.T) {
	t.Parallel()
	if strings.Contains(role, ladderRoleOldFailTaskSentence) {
		t.Errorf("role text still says %q", ladderRoleOldFailTaskSentence)
	}
	for _, sentence := range []string{
		ladderRoleFailTaskSentence,
		ladderRoleArgsSentence,
		ladderRoleChangeApproachSentence,
		ladderRoleNeverReplacedSentence,
		ladderRoleRungsSentence,
		ladderRoleApproachTriggerSentence,
		ladderRoleGapTriggerSentence,
		ladderRoleClaimsSentence,
	} {
		assertRoleHasSentence(t, sentence)
	}
}
