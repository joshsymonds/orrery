package kinds

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/store"
)

// Closure is the closure kind. Its run is a closure run in a correction round, and it checks each finding that is open in
// that round once, through a close chain of its own. The run ends passed when no closure is open, and failed when one is,
// with the round it ran and each finding's closure.
type Closure struct{}

// closureKindPlan is the kind of a plan node run. The fix plan of a correction round is the plan run in the round's prefix.
const closureKindPlan = "plan"

// The results a closure verifier gives a finding that the closure run reads. A finding is open when its closure is open.
const (
	closureResultClosed = "closed"
	closureResultOpen   = "open"
)

// Decide decides a closure run. Without a revision it prepares the run. Otherwise it walks the close chain of each open
// finding of its round, then holds the epic for a stopped step, starts the work the chains ask for, waits while a chain
// runs, or ends with the closures its chains settled.
func (Closure) Decide(node Node, rows Rows) Action {
	if node.Run.Revision == "" {
		return prepare(rows)
	}
	pass := reviewPass{run: node.Run, rows: rows}
	var closures []store.Closure
	for _, finding := range OpenFindings(rows.Review, node.Run.Round) {
		closures = append(closures, pass.settleClose(node.Run.Round, finding))
	}
	switch {
	case pass.hold != nil:
		return *pass.hold
	case len(pass.starts) > 0:
		if rows.Epic.Paused || rows.Epic.Parked != "" {
			return Action{Kind: ActNone}
		}
		ids := make([]string, 0, len(pass.starts))
		for _, unit := range pass.starts {
			ids = append(ids, unit.ID)
		}
		return Action{Kind: ActStart, IDs: ids, Units: pass.starts}
	case pass.waiting:
		return Action{Kind: ActNone}
	}
	outcome := outcomePassed
	if slices.ContainsFunc(closures, func(closure store.Closure) bool { return closure.Result == closureResultOpen }) {
		outcome = outcomeFailed
	}
	return Action{
		Kind: ActEnd, Outcome: outcome, Closures: closures,
		Round: store.ReviewRound{
			Round:    node.Run.Round,
			Revision: node.Run.Revision,
			Tasks:    closureTasks(rows, node.Run.Path),
			Outcome:  outcome,
		},
	}
}

// OpenFindings returns the ledger findings a closure round checks, in ledger order: each finding a verifier confirmed
// that no closure in a round below round closed or left undetermined.
func OpenFindings(review store.Review, round int64) []store.LedgerFinding {
	var open []store.LedgerFinding
	for _, finding := range review.Ledger {
		if finding.Result == reviewResultConfirmed && !closureSettled(finding, round) {
			open = append(open, finding)
		}
	}
	return open
}

// closureSettled reports whether a closure in a round below round closed or left undetermined finding.
func closureSettled(finding store.LedgerFinding, round int64) bool {
	return slices.ContainsFunc(finding.Closures, func(closure store.Closure) bool {
		return closure.Round < round &&
			(closure.Result == closureResultClosed || closure.Result == reviewResultUndetermined)
	})
}

// settleClose returns the closure of finding in round. A verdict of its close chain is the closure, and a gap leaves the
// finding undetermined. Any other state of the chain is recorded on p as a start, a wait or a hold.
func (p *reviewPass) settleClose(round int64, finding store.LedgerFinding) store.Closure {
	part := "close " + finding.ID
	chain := walkTries(p.run, part, p.rows, closureIsVerdict)
	closure := store.Closure{Finding: finding.ID, Round: round}
	switch {
	case chain.gap != "":
		closure.Step, closure.Result, closure.Reason = chain.last, reviewResultUndetermined, chain.gap
	case chain.verdict == nil:
		p.pending(chain, Unit{Part: part, Finding: finding})
	default:
		// closureIsVerdict has parsed this return, so its result is read without a second check.
		closure.Step = chain.verdict.ID
		closure.Result, closure.Reason, _ = closureVerdict(*chain.verdict)
	}
	return closure
}

// closureVerdict returns the result and the evidence a done closure verifier step gave its finding, from its first
// result. ok is false when the step is not done, its return gives no result, or its first result is not closed, open or
// undetermined.
func closureVerdict(step store.StepRow) (result, evidence string, ok bool) {
	if step.End != stepDone {
		return "", "", false
	}
	var ret reviewVerifierReturn
	if err := json.Unmarshal([]byte(step.Result), &ret); err != nil || len(ret.Results) == 0 {
		return "", "", false
	}
	first := ret.Results[0]
	switch first.Result {
	case closureResultClosed, closureResultOpen, reviewResultUndetermined:
		return first.Result, first.Evidence, true
	}
	return "", "", false
}

// closureIsVerdict reports whether a closure verifier step's end gives a verdict.
func closureIsVerdict(step store.StepRow) bool {
	_, _, ok := closureVerdict(step)
	return ok
}

// closureTasks returns the seqs of the tasks a correction round ran, in seq order: those of the turns above the mark of
// the fix plan that shares the prefix of the closure run at path. A round with no fix plan ran no tasks.
func closureTasks(rows Rows, path string) []int64 {
	mark, ok := closureMark(rows.Runs, path)
	if !ok {
		return nil
	}
	var seqs []int64
	for _, task := range rows.Epic.Tasks {
		if task.Turn > mark {
			seqs = append(seqs, task.Seq)
		}
	}
	return seqs
}

// closureMark returns the mark of the plan run whose path begins with the prefix of the closure run at path, which is the
// path up to its last slash. It reports false when no plan run does.
func closureMark(runs []store.NodeRun, path string) (int64, bool) {
	prefix := path[:strings.LastIndex(path, "/")+1]
	i := slices.IndexFunc(runs, func(run store.NodeRun) bool {
		return run.Kind == closureKindPlan && strings.HasPrefix(run.Path, prefix)
	})
	if i < 0 {
		return 0, false
	}
	return runs[i].Mark, true
}
