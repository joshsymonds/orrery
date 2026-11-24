package interpret

import (
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// phaseEndedWithGaps is the phase an epic ends in with a gap left, which a conclusion's ending carries with its
// question.
const phaseEndedWithGaps = "ended_with_gaps"

// gapTriggerPrefix is the prefix of the trigger a gap round writes for each live gap, which wakes the planner with the
// gap's cause.
const gapTriggerPrefix = "gap:"

// gapPlanRuns returns the plan runs the conclusion run started for its gap rounds, one for each round, in store order.
func (p pass) gapPlanRuns(run store.NodeRun) []store.NodeRun {
	var plans []store.NodeRun
	prefix := run.Path + "/G"
	for _, plan := range p.rows.Runs {
		if plan.Kind == kindPlan && strings.HasPrefix(plan.Path, prefix) {
			plans = append(plans, plan)
		}
	}
	return plans
}

// gapPlan returns the gap round's plan run of the conclusion run that has not ended, with replanPlan's node, and
// whether there is one. A workflow with no replan plan has none, since no gap round starts one.
func (p pass) gapPlan(run store.NodeRun) (workflow.Node, store.NodeRun, bool) {
	def, ok := replanPlan(p.flow.Nodes)
	if !ok {
		return workflow.Node{}, store.NodeRun{}, false
	}
	for _, plan := range p.gapPlanRuns(run) {
		if plan.State != store.NodeEnded {
			return def, plan, true
		}
	}
	return workflow.Node{}, store.NodeRun{}, false
}

// gapRounding decides a conclusion run whose gap round's plan has not ended: a running plan as replanning decides a
// release's plan, and a parked one gives no action. It reports false when the conclusion has no such plan, and its
// kind decides it.
func (p pass) gapRounding(run store.NodeRun) ([]Action, bool) {
	planDef, plan, open := p.gapPlan(run)
	if !open {
		return nil, false
	}
	if plan.State == store.NodeParked {
		return nil, true
	}
	return p.running(planDef, plan, scope{where: topLevel}), true
}

// gapRound returns the conclusion's action for act, a gap round. The round starts replanPlan's plan under the
// conclusion's path, as G<n>, where n is one more than the plan runs the conclusion started for its gap rounds before.
// The plan starts with the mark kinds.PlanMark gives, with a trigger for each live gap that carries the gap's cause,
// and each live gap is given a turn of its own. A workflow with no replan plan cannot plan a gap, so the conclusion
// ends with gaps and asks the owner.
func (p pass) gapRound(run store.NodeRun, act kinds.Action) Action {
	def, ok := replanPlan(p.flow.Nodes)
	if !ok {
		body, question := kinds.GapEnding(p.rows)
		return Action{
			Kind: ActConclude, Path: run.Path, Attempt: run.Attempt, Phase: phaseEndedWithGaps, Output: body,
			Question: question,
		}
	}
	path := fmt.Sprintf("%s/G%d/%s", run.Path, len(p.gapPlanRuns(run))+1, def.ID)
	epic := p.rows.Epic.ID
	out := Action{Kind: ActGapRound, Runs: []store.NodeRun{{
		Path: path, ID: store.NodeRunID(epic, path, run.Round, "start"), Node: def.ID, Kind: kindPlan,
		Round: run.Round, Mark: kinds.PlanMark(p.rows),
	}}}
	for _, gap := range act.Gaps {
		out.Triggers = append(out.Triggers, store.NodeTrigger{
			ID:    gapTriggerPrefix + store.NodeRunID(epic, path, run.Round, gap.Key),
			Cause: kinds.GapCause(gap, p.rows.Review),
		})
		out.GapTurns = append(out.GapTurns, store.GapTurn{
			Gap: gap.Key, Turn: int64(kinds.GapTurnCount(gap.Key, p.rows.GapTurns)) + 1, Reason: gap.Reason,
		})
	}
	return out
}
