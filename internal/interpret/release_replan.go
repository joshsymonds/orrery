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
// decided, its settle walks, its fan-out and the planner's turn included, and reports true. A correcting epic whose
// workflow has no such plan reports true with no action: there is no replan plan to start, and nothing to stop, so the
// release waits. Any other release with no running replan plan reports false, and its kind decides it.
func (p pass) replanning(run store.NodeRun) ([]Action, bool) {
	def, plan, ok := p.replanRun(run)
	if ok {
		return p.running(def, plan, scope{where: topLevel}), true
	}
	if _, has := replanPlan(p.flow.Nodes); !has && p.rows.Epic.Phase == ledgerPhase {
		return nil, true
	}
	return nil, false
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

// replan returns the work that starts the replan of the release run, for act, the replan its kind decided. The work
// starts replanPlan's plan at the release's replan prefix, with the epic's highest turn as its mark. While the epic is
// correcting, the owner's correction already holds the trigger that wakes the planner, so the work carries no trigger
// and no reason, and the store marks that plan below the turn that claimed the correction, so that turn's tasks build
// (store.Store.StartCorrectionPlan). Otherwise the replan sends the release back to planning: the work carries the
// trigger that wakes the planner with the report, whose id is derived from the plan's path and round, and the same text
// as the tell, and the report as its Output. A workflow with no plan whose body holds an integrate node cannot plan the
// change, so the release stops with the report instead. A correcting epic whose workflow has no such plan is decided by
// replanning, which gives it no action, so this function sees a correcting epic only with the plan.
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
	out := p.work(def, run, act)
	out.Runs = []store.NodeRun{{
		Path: path, ID: store.NodeRunID(epic, path, run.Round, "start"), Node: plan.ID, Kind: kindPlan,
		Round: run.Round, Mark: p.highestTurn(),
	}}
	if p.rows.Epic.Phase == ledgerPhase {
		return out
	}
	text := replanCause + act.Output
	out.Trigger = &store.NodeTrigger{
		ID: replanTriggerPrefix + store.NodeRunID(epic, path, run.Round, "replan"), Cause: text,
	}
	out.Reason = text
	return out
}

// replanRelease carries out the replan of act's run. An act with a trigger sends the release back to planning in one
// write (store.Store.ReplanRelease): the epic moves to correcting, and the replan's plan run starts with its trigger
// and tell. An act with no trigger is the replan of a correcting epic, whose owner's correction already woke the
// planner, so only the plan run starts, marked below the turn that claimed the correction so that turn's tasks build
// (store.Store.StartCorrectionPlan). Either way the run's release integrator worktree, when it has one, is removed
// first, as a stop removes it.
func (r *Runner) replanRelease(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	if len(act.Runs) != 1 {
		return releaseRefused(act)
	}
	if err := r.removeIntegrator(ctx, epic, act.Run); err != nil {
		return err
	}
	if act.Trigger == nil {
		if err := r.store.StartCorrectionPlan(ctx, epic.ID, act.Runs[0], at); err != nil {
			return fmt.Errorf("start replan plan of epic %s: %w", epic.ID, err)
		}
		return nil
	}
	if err := r.store.ReplanRelease(
		ctx, epic.ID, act.Path, act.Run.Attempt, act.Runs[0], *act.Trigger, act.Reason, at,
	); err != nil {
		return fmt.Errorf("replan release of epic %s: %w", epic.ID, err)
	}
	return nil
}

// resumeReplanned resumes the release of act's run once its replan plan has ended: the run moves to its next attempt
// and the epic back to clean, so the release begins again and prepares its candidate from the new branch tip.
func (r *Runner) resumeReplanned(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	if err := r.store.ResumeReplannedRelease(ctx, epic.ID, act.Path, act.Run.Attempt, at); err != nil {
		return fmt.Errorf("resume replanned release of epic %s: %w", epic.ID, err)
	}
	return nil
}
