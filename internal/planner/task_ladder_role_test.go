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
	"remove, the files the version adds to the task and drops from it. Its reason states what changes and why."

// ladderRoleNeverReplacedSentence is the first sentence of the paragraph on the ladder: a failing task is never
// replaced.
const ladderRoleNeverReplacedSentence = "A failing task is never replaced: it keeps its worktree and its work and " +
	"climbs a ladder, one rung for each failure."

// ladderRoleRungsSentence is the sentence on what each rung of the ladder is.
const ladderRoleRungsSentence = "Rungs 1 and 2 are its two attempts, rung 3 one writing attempt by the attempt role, " +
	"rungs 4 and 5 the two attempts of one change of approach, and a failure on rung 5 ends the task as a gap."

// ladderRoleApproachTriggerSentence is the sentence on the trigger a task climbing onto rung 4 wakes the planner with,
// and on when it returns split in place of change_approach.
const ladderRoleApproachTriggerSentence = "A task that climbs onto rung 4 wakes you with the trigger T<n>-approach: " +
	"return split in place of change_approach when the task's failures show it is two or more separable behaviours " +
	"whose files part cleanly, and otherwise return one change_approach action for it, written from its rungs, its " +
	"versions and its failure, and plan no task in its place."

// ladderRoleSplitArgsSentence is the sentence role.md gives the planner about the split action's args and descendants.
const ladderRoleSplitArgsSentence = `split {"task": <number>, "tasks": [<descendants>]} splits a task on rung 4 into ` +
	`descendants in place of a change of approach, where each descendant is an object with title, deliverable, files, ` +
	`test_changes, claims, contributes, gate and evidence.`

// ladderRoleSplitEndsSentence is the sentence role.md gives the planner about what a split leaves of its task.
const ladderRoleSplitEndsSentence = "Between them the descendants take each of the task's files and claims once, " +
	"and the task ends split and never runs again."

// ladderRoleSplitOnceSentence is the sentence role.md gives the planner about a descendant's split.
const ladderRoleSplitOnceSentence = "A descendant cannot be split again."

// ladderRoleGapTriggerSentence is the sentence on the trigger a task ended as a gap wakes the planner with, and on what
// a gap is.
const ladderRoleGapTriggerSentence = "A task that ends as a gap wakes you with the trigger T<n>-gap. A gap is never " +
	"planned again: it keeps the Requirements it claims, a plan whose only unbuilt Requirements are held by gaps ends " +
	"without parking, and the epic ends ended_with_gaps, naming each gap."

// ladderRoleClaimsSentence is the sentence on how a task on the ladder or ended as a gap holds its claims.
const ladderRoleClaimsSentence = "A task on the ladder or ended as a gap holds its claims."

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

// TestTaskLadderPlannerRoleDescribesSplit asserts that the role text tells the planner the split action's args and
// descendants, what a split leaves of its task, when to return it in place of change_approach, and that a descendant
// cannot be split again.
func TestTaskLadderPlannerRoleDescribesSplit(t *testing.T) {
	t.Parallel()
	for _, sentence := range []string{
		ladderRoleSplitArgsSentence,
		ladderRoleSplitEndsSentence,
		ladderRoleSplitOnceSentence,
	} {
		assertRoleHasSentence(t, sentence)
	}
}
