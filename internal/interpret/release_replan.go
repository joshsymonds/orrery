package interpret

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// replanTriggerPrefix is the prefix of the trigger a release's replan writes, which wakes the planner with the release
// integrator's report.
const replanTriggerPrefix = "release:"

// replanCause is the cause of the trigger a release's replan writes, and the text of its tell, before the report of the
// round of the release integrator that asked for it.
const replanCause = "the release goes back to planning for a change to the candidate: "

// replanPlan returns the plan node a release's replan runs, and whether the workflow has one: the last plan node among
// nodes, in the order the walk meets them, loop bodies included, whose body holds an integrate node, so the change it
// plans reaches the epic's branch. In the built-in epic that is the correct loop's fix plan.
func replanPlan(nodes []workflow.Node) (workflow.Node, bool) {
	var found workflow.Node
	ok := false
	for _, node := range nodes {
		if node.Kind == kindPlan && slices.ContainsFunc(node.Body, func(child workflow.Node) bool {
			return child.Kind == kindIntegrate
		}) {
			found, ok = node, true
		}
		if node.Loop != nil {
			if inner, has := replanPlan(node.Body); has {
				found, ok = inner, true
			}
		}
	}
	return found, ok
}

// replanning decides the plan run of the replan of the release run, when that plan runs: the run under the release's
// replan prefix at its attempt (kinds.ReplanPrefix), whose node is replanPlan's. It decides it as any plan run is
// decided, its settle walks, its fan-out and the planner's turn included, and reports true. A release with no running
// replan plan reports false, and its kind decides it.
func (p pass) replanning(run store.NodeRun) ([]Action, bool) {
	def, plan, ok := p.replanRun(run)
	if !ok {
		return nil, false
	}
	return p.running(def, plan, scope{where: topLevel}), true
}

// replanRun returns replanPlan's node and its running run under the replan prefix of the release run at its attempt,
// and false when the workflow has no such plan or that plan does not run.
func (p pass) replanRun(run store.NodeRun) (workflow.Node, store.NodeRun, bool) {
	def, ok := replanPlan(p.flow.Nodes)
	if !ok {
		return workflow.Node{}, store.NodeRun{}, false
	}
	plan, ok := p.byPath[kinds.ReplanPrefix(run.Path, run.Attempt)+def.ID]
	if !ok || plan.State != store.NodeRunning {
		return workflow.Node{}, store.NodeRun{}, false
	}
	return def, plan, true
}

// replan returns the work that sends the release run back to planning, for act, the replan its kind decided with the
// report of the round of the release integrator as its Output. The work starts replanPlan's plan at the release's
// replan prefix, with the epic's highest turn as its mark, and carries the trigger that wakes the planner with the
// report, whose id is derived from the plan's path and round, and the same text as the tell. A workflow with no plan
// whose body holds an integrate node cannot plan the change, so the release stops with the report instead.
func (p pass) replan(def workflow.Node, run store.NodeRun, act kinds.Action) Action {
	plan, ok := replanPlan(p.flow.Nodes)
	if !ok {
		stop := kinds.Action{
			Kind: kinds.ActStop,
			Output: act.Output + "; the workflow has no plan whose body holds an integrate node, so the release " +
				"cannot plan the change to the candidate",
		}
		return p.work(def, run, stop)
	}
	path := kinds.ReplanPrefix(run.Path, run.Attempt) + plan.ID
	epic := p.rows.Epic.ID
	text := replanCause + act.Output
	out := p.work(def, run, act)
	out.Runs = []store.NodeRun{{
		Path: path, ID: store.NodeRunID(epic, path, run.Round, "start"), Node: plan.ID, Kind: kindPlan,
		Round: run.Round, Mark: p.highestTurn(),
	}}
	out.Trigger = &store.NodeTrigger{
		ID: replanTriggerPrefix + store.NodeRunID(epic, path, run.Round, "replan"), Cause: text,
	}
	out.Reason = text
	return out
}

// replanRelease sends the release of act's run back to planning in one write (store.Store.ReplanRelease): the epic moves
// to correcting and frees its release slot, and the replan's plan run starts with its trigger and tell. The run's
// release integrator worktree, when it has one, is removed first, as a stop removes it.
func (r *Runner) replanRelease(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	if len(act.Runs) != 1 || act.Trigger == nil {
		return releaseRefused(act)
	}
	if err := r.removeIntegrator(ctx, epic, act.Run); err != nil {
		return err
	}
	if err := r.store.ReplanRelease(
		ctx, epic.ID, act.Path, act.Run.Attempt, act.Runs[0], *act.Trigger, act.Reason, at,
	); err != nil {
		return fmt.Errorf("replan release of epic %s: %w", epic.ID, err)
	}
	return nil
}

// resumeReplanned resumes the release of act's run once its replan plan has ended: the run moves to its next attempt
// and the epic back to clean, so the release takes the slot and prepares its candidate from the new branch tip.
func (r *Runner) resumeReplanned(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	if err := r.store.ResumeReplannedRelease(ctx, epic.ID, act.Path, act.Run.Attempt, at); err != nil {
		return fmt.Errorf("resume replanned release of epic %s: %w", epic.ID, err)
	}
	return nil
}
