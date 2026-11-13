package enginehost

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
)

// The results of an agent gate's return that the verdict reads. A result that is neither holds nor fails is
// undetermined, so it never ends done.
const (
	verdictHolds = "holds"
	verdictFails = "fails"
)

// verdictReturn is the part of an agent gate step's schema-valid return that the verdict reads.
type verdictReturn struct {
	Status   string           `json:"status"`
	Results  []verdictResult  `json:"results"`
	Findings []verdictFinding `json:"findings"`
}

// verdictResult is one entry of a return's results.
type verdictResult struct {
	Assertion string `json:"assertion"`
	Kind      string `json:"kind"`
	Result    string `json:"result"`
}

// verdictFinding is one entry of a return's findings. Only the assertion it cites is read.
type verdictFinding struct {
	Citation string `json:"citation"`
}

// assertionsCheck checks a schema-valid return of an agent gate step against the gate's assertions. It returns the
// assertions check and the return it read, which is empty when the return does not read. The first fails result of any
// such return is kept in firstFails when none is yet, whether or not the check passes, so no later return ends done.
func (r *stepRun) assertionsCheck(round int, value json.RawMessage) (store.StepCheck, verdictReturn) {
	check := store.StepCheck{Round: round, Name: stepLoopAssertionsName, OK: true}
	var ret verdictReturn
	if err := json.Unmarshal(value, &ret); err != nil {
		check.OK = false
		check.Detail = fmt.Sprintf("read verdict: %v", err)
		return check, verdictReturn{}
	}
	if r.firstFails == nil {
		for i := range ret.Results {
			if ret.Results[i].Result == verdictFails {
				r.firstFails = &ret.Results[i]
				break
			}
		}
	}
	if detail := assertionsMismatch(r.agentGate.Assertions, ret); detail != "" {
		check.OK = false
		check.Detail = detail
	}
	return check, ret
}

// assertionsMismatch returns the first way ret's results differ from assertions, or "" when they agree. The results
// must be one per assertion, in order, with its text and kind, and a fails result needs a finding that cites its text.
func assertionsMismatch(assertions []gates.Assertion, ret verdictReturn) string {
	if len(ret.Results) != len(assertions) {
		return fmt.Sprintf("results has %d entries for %d assertions", len(ret.Results), len(assertions))
	}
	for i, assertion := range assertions {
		res := ret.Results[i]
		switch {
		case res.Assertion != assertion.Text:
			return fmt.Sprintf("result %d is for assertion %q, want %q", i+1, res.Assertion, assertion.Text)
		case res.Kind != assertion.Kind:
			return fmt.Sprintf("result %d of assertion %q has kind %q, want %q",
				i+1, assertion.Text, res.Kind, assertion.Kind)
		case res.Result == verdictFails && !citesAssertion(ret.Findings, assertion.Text):
			return fmt.Sprintf("result %d fails assertion %q and no finding cites it", i+1, assertion.Text)
		}
	}
	return ""
}

// citesAssertion reports whether a finding among findings cites the assertion text.
func citesAssertion(findings []verdictFinding, text string) bool {
	return slices.ContainsFunc(findings, func(f verdictFinding) bool { return f.Citation == text })
}

// verdictReasons returns the reasons a return with status and ret does not end done, in order: each result that is
// not holds, the status when it is not the step's done value, and the first fails an earlier return held when ret
// holds none. It is empty when the return ends done.
func (r *stepRun) verdictReasons(status string, ret verdictReturn) []string {
	var reasons []string
	held := false
	for _, res := range ret.Results {
		switch res.Result {
		case verdictHolds:
			continue
		case verdictFails:
			held = true
			reasons = append(reasons, "fails: "+res.Assertion)
		default:
			reasons = append(reasons, "undetermined: "+res.Assertion)
		}
	}
	if status != r.doneValue {
		reasons = append(reasons, "status "+status)
	}
	if !held && r.firstFails != nil {
		reasons = append(reasons, "an earlier return held fails: "+r.firstFails.Assertion)
	}
	return reasons
}

// endVerdict ends an agent gate step whose return passed its checks. It ends done when no reason keeps the return from
// it, and gate_failed otherwise, with the reasons joined and the return kept. It makes no judge call either way.
func (r *stepRun) endVerdict(ctx context.Context, status string, ret verdictReturn) {
	reasons := r.verdictReasons(status, ret)
	if len(reasons) == 0 {
		r.finish(ctx, stepLoopEndDone, status, "")
		return
	}
	r.finish(ctx, stepLoopEndGateFailed, status, strings.Join(reasons, "; "))
}
