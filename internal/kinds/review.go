package kinds

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/store"
)

// Review is the review kind. Its run is the epic's end-of-epic review at the revision it froze. Each enabled reviewer's
// try chain runs once, and a reviewer that fails with findings has each finding's verifier chain run once, unless its
// verify is off. The run ends passed with the verdicts and the ledger its chains settled.
type Review struct{}

// The ends of a step the try chains read, the status a finished gate return has, and the results and verdicts a review
// stores.
const (
	reviewEndEngineRestarted = "engine_restarted"
	reviewEndStopped         = "stopped"
	reviewEndGateFailed      = "gate_failed"
	reviewStatusDone         = "done"
	reviewVerdictHolds       = "holds"
	reviewVerdictFails       = "fails"
	reviewVerdictGap         = "gap"
	reviewResultConfirmed    = "confirmed"
	reviewResultDropped      = "dropped"
	reviewResultUndetermined = "undetermined"
)

// reviewGapEnds is the number of costly step ends at which a part of a review run is a gap.
const reviewGapEnds = 2

// tries is where one part's try chain stands. Exactly one of start, wait, hold, verdict and gap is set.
type tries struct {
	start   string         // the id of the try to start
	wait    bool           // a try is still running
	hold    string         // the id of a stopped step whose stop has not paused the epic yet
	verdict *store.StepRow // the step whose end gave a verdict
	gap     string         // the reason the part is a gap
	last    string         // the id of the last try that has a step ("" when none)
}

// walkTries walks the tries of part of run over rows.Steps; isVerdict reports whether an ended step gave a verdict.
// A restart is free and a stop is free once the epic is held for it. Any other end costs one try, and the second
// costly end is a gap.
func walkTries(run store.NodeRun, part string, rows Rows, isVerdict func(store.StepRow) bool) tries {
	var chain tries
	costly := 0
	for try := 1; ; try++ {
		id := reviewTryID(run, part, try)
		i := slices.IndexFunc(rows.Steps, func(step store.StepRow) bool { return step.ID == id })
		if i < 0 {
			chain.start = id
			return chain
		}
		step := rows.Steps[i]
		chain.last = id
		switch {
		case step.End == "":
			chain.wait = true
			return chain
		case isVerdict(step):
			chain.verdict = &step
			return chain
		case step.End == reviewEndEngineRestarted:
			continue
		case step.End == reviewEndStopped && !slices.Contains(rows.Review.Holds, id):
			chain.hold = id
			return chain
		case step.End == reviewEndStopped:
			continue
		}
		costly++
		if costly == reviewGapEnds {
			chain.gap = fmt.Sprintf("%s gave no verdict twice: %s: %s", part, step.End, step.Reason)
			return chain
		}
	}
}

// reviewTryID returns the id of try number try of part of run: the id of part for the first try, and the id of
// "<part> try <n>" for each try after it.
func reviewTryID(run store.NodeRun, part string, try int) string {
	if try == 1 {
		return store.NodeWorkID(run.ID, run.Attempt, part)
	}
	return store.NodeWorkID(run.ID, run.Attempt, fmt.Sprintf("%s try %d", part, try))
}

// reviewGateReturn is the part of a reviewer's kept return the review reads: its status, its findings, each as its schema
// keys map to strings, and the result each assertion got.
type reviewGateReturn struct {
	Status   string              `json:"status"`
	Findings []map[string]string `json:"findings"`
	Results  []struct {
		Result string `json:"result"`
	} `json:"results"`
}

// reviewVerifierReturn is the part of a verifier's kept return the review reads: the results it gave its finding.
type reviewVerifierReturn struct {
	Results []struct {
		Evidence string `json:"evidence"`
		Result   string `json:"result"`
	} `json:"results"`
}

// reviewFailedFindings returns the findings of a gate return that finished and failed with findings: its status is done,
// no assertion is undetermined and it raised at least one. It reports false for any other return.
func reviewFailedFindings(result string) ([]map[string]string, bool) {
	var ret reviewGateReturn
	if err := json.Unmarshal([]byte(result), &ret); err != nil {
		return nil, false
	}
	if ret.Status != reviewStatusDone || len(ret.Findings) == 0 {
		return nil, false
	}
	for _, assertion := range ret.Results {
		if assertion.Result == reviewResultUndetermined {
			return nil, false
		}
	}
	return ret.Findings, true
}

// reviewIsReviewerVerdict reports whether a reviewer step's end gives a verdict: a done step holds, and a gate_failed step
// whose return failed with findings fails.
func reviewIsReviewerVerdict(step store.StepRow) bool {
	switch step.End {
	case stepDone:
		return true
	case reviewEndGateFailed:
		_, failed := reviewFailedFindings(step.Result)
		return failed
	}
	return false
}

// reviewVerifierVerdict returns the result and the evidence a done verifier step gave its finding, from its first result.
// ok is false when the step is not done, its return gives no result, or its first result is not confirmed, dropped or
// undetermined.
func reviewVerifierVerdict(step store.StepRow) (result, evidence string, ok bool) {
	if step.End != stepDone {
		return "", "", false
	}
	var ret reviewVerifierReturn
	if err := json.Unmarshal([]byte(step.Result), &ret); err != nil || len(ret.Results) == 0 {
		return "", "", false
	}
	first := ret.Results[0]
	switch first.Result {
	case reviewResultConfirmed, reviewResultDropped, reviewResultUndetermined:
		return first.Result, first.Evidence, true
	}
	return "", "", false
}

// reviewIsVerifierVerdict reports whether a verifier step's end gives a verdict.
func reviewIsVerifierVerdict(step store.StepRow) bool {
	_, _, ok := reviewVerifierVerdict(step)
	return ok
}

// reviewPass is one walk of a review run's try chains. It holds the units the walk asks to start, the hold it asks for
// first, whether a chain waits, and the verdicts and findings the chains have settled.
type reviewPass struct {
	run      store.NodeRun
	rows     Rows
	starts   []Unit
	hold     *Action
	waiting  bool
	verdicts []store.ReviewVerdict
	findings []store.LedgerFinding
}

// Decide decides a review run. Without a revision it prepares the run. Otherwise it walks each reviewer's try chain and
// the verifier chains of the findings reviewers raise. It then holds the epic for a stopped step, starts the work the
// chains ask for, waits while a chain runs, or ends passed with what the chains settled.
func (Review) Decide(node Node, rows Rows) Action {
	if node.Run.Revision == "" {
		return prepare(rows)
	}
	pass := reviewPass{run: node.Run, rows: rows}
	for i, reviewer := range rows.Reviewers {
		pass.settleReviewer(i+1, reviewer)
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
	return Action{
		Kind: ActEnd, Outcome: outcomePassed, Output: reviewLines(pass.verdicts), Verdicts: pass.verdicts,
		Ledger: pass.ledger(),
	}
}

// settleReviewer walks the try chain of the reviewer at position. A done step holds. A gate_failed step with findings
// fails, and each of its findings is settled. A gap is a verdict too.
func (p *reviewPass) settleReviewer(position int, reviewer Reviewer) {
	part := fmt.Sprintf("reviewer %d", position)
	chain := walkTries(p.run, part, p.rows, reviewIsReviewerVerdict)
	switch {
	case chain.gap != "":
		p.addVerdict(position, reviewer, store.ReviewVerdict{
			Step: chain.last, Verdict: reviewVerdictGap, Reason: chain.gap,
		})
	case chain.verdict == nil:
		p.pending(chain, Unit{Part: part, Reviewer: reviewer.Name})
	case chain.verdict.End == stepDone:
		p.addVerdict(position, reviewer, store.ReviewVerdict{Step: chain.verdict.ID, Verdict: reviewVerdictHolds})
	default:
		// reviewIsReviewerVerdict has parsed this return, so the findings are read without a second check.
		findings, _ := reviewFailedFindings(chain.verdict.Result)
		p.addVerdict(position, reviewer, store.ReviewVerdict{Step: chain.verdict.ID, Verdict: reviewVerdictFails})
		for i, raised := range findings {
			p.settleFinding(position, reviewer, i+1, raised)
		}
	}
}

// addVerdict appends verdict as the verdict of the reviewer at position.
func (p *reviewPass) addVerdict(position int, reviewer Reviewer, verdict store.ReviewVerdict) {
	verdict.Position, verdict.Reviewer = int64(position), reviewer.Name
	p.verdicts = append(p.verdicts, verdict)
}

// settleFinding settles finding number place that the reviewer at position raised. With verify off it is confirmed
// unverified. Otherwise its verifier chain decides it: a verdict gives its result, and a gap leaves it undetermined.
func (p *reviewPass) settleFinding(position int, reviewer Reviewer, place int, raised map[string]string) {
	finding := store.LedgerFinding{
		ID:       fmt.Sprintf("%s-%d", reviewer.Name, place),
		Reviewer: reviewer.Name,
		Finding:  raised["id"],
		Claim:    raised["claim"],
		Citation: raised["citation"],
		Location: raised["location"],
		Evidence: raised["evidence"],
		VerifyBy: raised["verify_by"],
	}
	if !reviewer.Verify {
		finding.Result, finding.Unverified = reviewResultConfirmed, true
		finding.Reason = fmt.Sprintf("unverified: reviewer %s has verify off", reviewer.Name)
		p.findings = append(p.findings, finding)
		return
	}
	part := fmt.Sprintf("verify %d %d", position, place)
	chain := walkTries(p.run, part, p.rows, reviewIsVerifierVerdict)
	switch {
	case chain.gap != "":
		finding.Step, finding.Result, finding.Reason = chain.last, reviewResultUndetermined, chain.gap
	case chain.verdict == nil:
		p.pending(chain, Unit{Part: part, Reviewer: reviewer.Name, Finding: finding})
	default:
		result, evidence, _ := reviewVerifierVerdict(*chain.verdict)
		finding.Step, finding.Result, finding.Reason = chain.verdict.ID, result, evidence
	}
	p.findings = append(p.findings, finding)
}

// pending records what a chain with neither a verdict nor a gap asks for: a start, which Decide gathers into one start, a
// wait, or a hold. The first hold is the action the pass returns.
func (p *reviewPass) pending(chain tries, unit Unit) {
	switch {
	case chain.start != "":
		unit.ID = chain.start
		p.starts = append(p.starts, unit)
	case chain.wait:
		p.waiting = true
	case chain.hold != "" && p.hold == nil:
		p.hold = &Action{
			Kind: ActHold, IDs: []string{chain.hold},
			Output: unit.Part + " stopped: " + reviewStepReason(p.rows.Steps, chain.hold),
		}
	}
}

// ledger returns the findings in ledger order, each with its place in the ledger.
func (p *reviewPass) ledger() []store.LedgerFinding {
	for i := range p.findings {
		p.findings[i].Position = int64(i + 1)
	}
	return p.findings
}

// reviewLines returns one line per verdict: the reviewer's name, then its verdict.
func reviewLines(verdicts []store.ReviewVerdict) string {
	lines := make([]string, 0, len(verdicts))
	for _, verdict := range verdicts {
		lines = append(lines, verdict.Reviewer+": "+verdict.Verdict)
	}
	return strings.Join(lines, "\n")
}

// reviewStepReason returns the reason of the step with id among steps, or "" when no step has the id.
func reviewStepReason(steps []store.StepRow, id string) string {
	i := slices.IndexFunc(steps, func(step store.StepRow) bool { return step.ID == id })
	if i < 0 {
		return ""
	}
	return steps[i].Reason
}
