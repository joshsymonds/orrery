package kinds

import (
	"strconv"

	"github.com/joshsymonds/orrery/internal/store"
)

// integratorWork is the part of the work of a release run that its release integrator runs under. The step's id is
// store.NodeWorkID of the run's id at the generation of its candidate (gatedRun), its attempt and this part, so a
// restart finds the step under that id. The integrator once ran up to three rounds of a generation, under integrator 1
// to integrator 3, and its one round keeps the first round's part, so a step a live store holds is found again.
const integratorWork = "integrator 1"

// integratorAction decides a releasing epic whose prepare found origin's commit not in the tip, whether git's merge of
// the two conflicts or not. The release integrator merges origin's commit into the tip, runs the epic's Done gates on
// the merged tree unless it shows a skip is safe, says whether each release reviewer's earlier verdict still stands
// when only origin's main moved, and commits the merge, in one round at the generation of the run's candidate
// (store.ReleaseGeneration), recorded at its position (store.ReleaseIntegratorPosition).
// A round with no release run is recorded once its step ends, waits while its step runs, or starts (integratorStart).
// A round recorded as failed stops the release with its reason, unless a replan release run of the attempt was
// recorded with it: then the release goes back to planning with that run's reason (replanAction). A round recorded as
// passed has its commit recorded as the candidate, at that commit.
func integratorAction(run store.NodeRun, rows Rows) Action {
	generation := store.ReleaseGeneration(rows.Releases, run.Attempt)
	id := store.NodeWorkID(gatedRun(run, generation).ID, run.Attempt, integratorWork)
	position := store.ReleaseIntegratorPosition(generation)
	recorded, ok := firstMatch(rows.Releases, func(r store.ReleaseRun) bool {
		return r.Part == store.ReleasePartIntegrator && r.Attempt == run.Attempt && r.Position == position
	})
	if !ok {
		return integratorStart(rows, run.Attempt, id, position)
	}
	if !recorded.Passed {
		if act := replanAction(run, rows); act.Kind == ActReplan {
			return act
		}
		return Action{Kind: ActStop, Output: recorded.Reason}
	}
	return Action{Kind: ActRecord, Items: []string{store.ReleasePartCandidate, recorded.Head}, Revision: recorded.Head}
}

// integratorStart decides the round of the release integrator whose step is id and whose release run is at position,
// which has no release run. It records the step once its end is stored, and waits while it runs. With no step, it
// stops the release when the owner stopped it at the run's attempt, waits while the epic is paused or parked, and
// starts the round otherwise, with its position as its item.
func integratorStart(rows Rows, attempt int64, id string, position int64) Action {
	item := strconv.FormatInt(position, 10)
	if row, found := firstMatch(rows.Steps, func(s store.StepRow) bool { return s.ID == id }); found {
		return recordAction(row, []string{store.ReleasePartIntegrator, item})
	}
	if ownerStopped(rows, attempt) {
		return Action{
			Kind: ActStop, Output: "the release integrator did not start: the owner stopped the release",
		}
	}
	if rows.Epic.Paused || rows.Epic.Parked != "" {
		return Action{Kind: ActNone}
	}
	return Action{Kind: ActIntegrator, IDs: []string{id}, Items: []string{item}}
}
