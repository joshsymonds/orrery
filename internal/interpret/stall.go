package interpret

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/store"
)

// The phases an epic ends in, or stops in, besides the closed phases and the held one. An epic in one of them is not
// live, so no stall is found for it.
const (
	phaseMetAtBase = "met_at_base"
	phaseDone      = "done"
	phaseReleased  = "released"
)

// turnRunning is the state of a planner turn that has not ended. A turn runs its planner step, which the epic's steps
// do not list.
const turnRunning = "running"

// evidenceEnded is the state of an evidence run that has ended. Its other states, capturing and judging, are running.
const evidenceEnded = "ended"

// stallable is an epic a pass may find stalled: one it started nothing for and that may stall. decided holds the rows
// the pass's walk of the epic last decided on, and is nil when the pass made no decision for it, as for an epic at rest.
type stallable struct {
	epic    string
	decided *store.EpicFlow
}

// passStalls checks each of the stallable epics a pass covered: one that started nothing. It parks or wakes the planner
// of each idle one (see stallCheck). The open amendments are read once for the pass, and a read that fails is logged,
// so no epic is judged this pass. An epic whose check fails is logged, and a later pass judges it again.
func (r *Runner) passStalls(ctx context.Context, candidates []stallable) {
	if len(candidates) == 0 {
		return
	}
	amendments, err := r.store.OpenAmendments(ctx)
	if err != nil {
		r.logger.ErrorContext(ctx, "list open evidence amendments", "err", err)
		return
	}
	amending := make(map[string]bool, len(amendments))
	for _, amended := range amendments {
		amending[amended.Epic] = true
	}
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return
		}
		if err = r.stallCheck(ctx, candidate, amending[candidate.epic]); err != nil {
			r.logger.ErrorContext(ctx, "check epic for a stall", "epic", candidate.epic, "err", err)
		}
	}
}

// stallCheck judges the epic of candidate idle only when both the rows the pass's walk last decided on and the epic's
// flow read again now are idle (stallIdle). Work that was under way when the walk decided, and has ended since, is
// what the walk waited on, and its end wakes a pass that decides the next action, so the epic is not idle. An epic the
// pass did not walk is judged on the flow read now alone. An idle epic is judged as follows. An epic with no running
// plan run is parked at once with a stall park (see parkStall). Otherwise, when its latest stall has the mark the epic
// has now, so no task, node run or action came since that stall, it is parked too. Any other idle epic gets its stall:
// a pending stall trigger whose cause names the epic's phase, open tasks and running node runs, so the next pass gives
// the plan run its turn. amending reports whether one of the epic's amendments is checking or capturing.
func (r *Runner) stallCheck(ctx context.Context, candidate stallable, amending bool) error {
	if candidate.decided != nil && !stallIdle(*candidate.decided, amending) {
		return nil
	}
	epic := candidate.epic
	flow, err := r.store.EpicFlow(ctx, epic)
	if err != nil {
		return fmt.Errorf("read epic %s: %w", epic, err)
	}
	if !stallIdle(flow, amending) {
		return nil
	}
	mark, err := r.store.StallMark(ctx, epic)
	if err != nil {
		return fmt.Errorf("read stall mark of epic %s: %w", epic, err)
	}
	if !hasRunningPlan(flow.Runs) {
		return r.parkStall(ctx, epic, flow, mark, "no plan run was running")
	}
	latest, stalled, err := r.store.LatestStall(ctx, epic)
	if err != nil {
		return fmt.Errorf("read latest stall of epic %s: %w", epic, err)
	}
	if stalled && latest.Mark == mark {
		return r.parkStall(ctx, epic, flow, mark, "the last stall changed nothing")
	}
	if err = r.store.WakeStall(ctx, epic, stallCause(flow), mark, r.now()); err != nil {
		return fmt.Errorf("wake epic %s with a stall: %w", epic, err)
	}
	return nil
}

// parkStall parks epic with a stall park whose reason is the stall cause of flow followed by why, the reason the epic
// is parked. The park question the steerer is sent carries that reason.
func (r *Runner) parkStall(
	ctx context.Context, epic string, flow store.EpicFlow, mark store.StallMark, why string,
) error {
	reason := stallCause(flow) + "; " + why
	if err := r.store.ParkStall(ctx, epic, reason, mark, r.now()); err != nil {
		return fmt.Errorf("park epic %s with a stall: %w", epic, err)
	}
	return nil
}

// stallIdle reports whether the epic whose rows are flow is idle, so the pass that read them left it as it was. Its
// phase is live, it is neither paused nor parked, and no step, node check, evidence run, planner turn or amendment
// (amending) is under way. The steps read are every step labeled with the epic, whatever its task label, so the step
// of a task integrator, a build, an evidence judge, a planner turn or a conversation's start for the epic counts, and
// the evidence runs read are every run of the epic, current or not (store.Underway). It has no pending planner action
// and no open question, and it has no pending trigger while a plan run is running. A step at stage limited has not
// ended, so it is running; a step that ended keeps its stage, so the stage is not read.
func stallIdle(flow store.EpicFlow, amending bool) bool {
	epic := flow.Epic
	if !mayStall(epic) || amending {
		return false
	}
	if flow.Actions != 0 || epic.OpenQuestions != 0 || slices.ContainsFunc(epic.Turns, turnIsRunning) {
		return false
	}
	if len(flow.Underway.Steps) != 0 || len(flow.Underway.Evidence) != 0 {
		return false
	}
	if slices.ContainsFunc(flow.Steps, stepRunning) || slices.ContainsFunc(flow.Checks, checkRunning) ||
		slices.ContainsFunc(flow.Evidence, evidenceRunning) {
		return false
	}
	return flow.Pending == 0 || !hasRunningPlan(flow.Runs)
}

// mayStall reports whether epic can be idle at all: its phase is live, and it is neither paused nor parked. A pass
// reads an epic for a stall only when mayStall holds, so no epic in a phase that ends or stops is read for one.
func mayStall(epic store.EpicRow) bool {
	return livePhase(epic.Phase) && !epic.Paused && epic.Parked == ""
}

// livePhase reports whether an epic in phase is live: it is neither closed, held, nor one of the phases it ends in or
// stops in.
func livePhase(phase string) bool {
	if store.IsClosedPhase(phase) || phase == store.PhaseHeld {
		return false
	}
	switch phase {
	case phaseMetAtBase, phaseEndedWithGaps, phaseDone, phaseReleased, store.PhaseStopped:
		return false
	}
	return true
}

// stepRunning reports whether step has not ended.
func stepRunning(step store.StepRow) bool {
	return step.End == ""
}

// checkRunning reports whether check is running, which a done gate's check is too.
func checkRunning(check store.NodeCheck) bool {
	return check.State == store.NodeRunning
}

// evidenceRunning reports whether run has not ended.
func evidenceRunning(run store.EvidenceRun) bool {
	return run.State != evidenceEnded
}

// turnIsRunning reports whether turn has not ended.
func turnIsRunning(turn store.EpicTurn) bool {
	return turn.State == turnRunning
}

// hasRunningPlan reports whether runs holds a plan run that is running.
func hasRunningPlan(runs []store.NodeRun) bool {
	return slices.ContainsFunc(runs, func(run store.NodeRun) bool {
		return run.Kind == kindPlan && run.State == store.NodeRunning
	})
}

// stallCause returns the cause of the stall trigger of the epic whose rows are flow: its phase, each open task as
// T<seq> with its title and build stage, and each node run still running, by path.
func stallCause(flow store.EpicFlow) string {
	var tasks []string
	for _, task := range flow.Epic.Tasks {
		if task.Status != taskOpen {
			continue
		}
		stage := task.Build.Stage
		if stage == "" {
			stage = "unstarted"
		}
		tasks = append(tasks, fmt.Sprintf("T%d %q at build stage %s", task.Seq, task.Title, stage))
	}
	runs := make([]string, 0, len(flow.Runs))
	for _, run := range flow.Runs {
		if run.State == store.NodeRunning {
			runs = append(runs, run.Path)
		}
	}
	return fmt.Sprintf("no progress since the last pass; phase %s; open tasks: %s; running node runs: %s",
		flow.Epic.Phase, listOrNone(tasks), listOrNone(runs))
}

// listOrNone joins items with commas, or returns none when there are no items.
func listOrNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}
