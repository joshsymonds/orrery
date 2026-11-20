package interpret

import (
	"fmt"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// concluding decides a running conclusion run before its kind does, in this order: its gap round's plan, while that plan
// has not ended (gapRounding); no action while a planner action of the epic is pending, since a rerun_review the gap turn
// returned is carried out before the conclusion decides again; and the reruns of its review the planner asked for
// (reruns). It reports false when none of them decides the run, and the conclusion's kind then decides it.
func (p pass) concluding(run store.NodeRun) ([]Action, bool) {
	if acts, handled := p.gapRounding(run); handled {
		return acts, true
	}
	if p.rows.Actions > 0 {
		return nil, true
	}
	return p.reruns(run)
}

// reruns decides the reruns of the review the conclusion run at run's path asked for, in the order the rows hold them.
// While the run of an earlier request is not ended, that run is decided and no later request starts. The first request
// with no run at its path starts one. It reports false when every request's run has ended, or when none was asked for.
func (p pass) reruns(run store.NodeRun) ([]Action, bool) {
	for _, request := range p.rows.Reruns {
		path := rerunPath(run.Path, request)
		rerun, started := p.byPath[path]
		if !started {
			return []Action{p.startRerun(run, path)}, true
		}
		if rerun.State != store.NodeEnded {
			return p.rerunning(rerun, request), true
		}
	}
	return nil, false
}

// rerunPath returns the path of the rerun run that request starts under the conclusion run at path: the path, then
// /R, the request's turn and position joined by a dot, and /rerun.
func rerunPath(path string, request store.ReviewRerun) string {
	return fmt.Sprintf("%s/R%d.%d/rerun", path, request.Turn, request.Position)
}

// startRerun returns the start of the rerun run at path under the conclusion run, a run of kind rerun in the conclusion's
// round, under the id the start of a node run has.
func (p pass) startRerun(run store.NodeRun, path string) Action {
	return Action{Kind: ActStart, Runs: []store.NodeRun{{
		Path: path, ID: store.NodeRunID(p.rows.Epic.ID, path, run.Round, "start"), Node: kindRerun, Kind: kindRerun,
		Round: run.Round,
	}}}
}

// rerunning decides the rerun run that request asked for, while it has not ended. The review kind's rerun decision
// (kinds.RerunReview) gives the run's prepare and starts as work, a hold as a review's hold does, and its end: a passed
// end ends the rerun run with its verdict and findings, and a failed one ends the run failed. It gives no action when
// its reviewer's steps are still running.
func (p pass) rerunning(rerun store.NodeRun, request store.ReviewRerun) []Action {
	def := workflow.Node{ID: kindRerun, Kind: kindRerun}
	act := kinds.RerunReview(rerun, request.Reviewer, p.rows)
	switch act.Kind {
	case kinds.ActPrepare, kinds.ActStart:
		return []Action{p.work(def, rerun, act)}
	case kinds.ActHold:
		return []Action{{Kind: ActHold, Path: rerun.Path, IDs: act.IDs, Reason: act.Output}}
	case kinds.ActEnd:
		if act.Outcome == outcomePassed {
			return []Action{{
				Kind: ActRerun, Path: rerun.Path, Attempt: rerun.Attempt, Output: act.Output, Verdicts: act.Verdicts,
				Ledger: act.Ledger,
			}}
		}
		return []Action{
			{Kind: ActEnd, Ends: []store.NodeEnd{{Path: rerun.Path, Outcome: act.Outcome, Output: act.Output}}},
		}
	default:
		return nil
	}
}
