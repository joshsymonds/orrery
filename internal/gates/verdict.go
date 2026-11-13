package gates

import (
	_ "embed"
	"fmt"
	"strings"
	"time"
)

// VerdictSchema is the schema an agent gate's return must match: the gate-reviewer return of gambit be628ef, copied
// byte for byte into verdict.json.
//
//go:embed verdict.json
var VerdictSchema string

// verdictInstruction opens every agent gate's first prompt. It carries the judging rules the verdict schema's results
// and findings depend on.
const verdictInstruction = `You are a read-only gate agent. You check one gate's assertions against a frozen revision and change set, and you return a verdict.

Judge only the declared assertions. Do not judge anything beyond what an assertion names, and do not add assertions of your own. Inspect the frozen revision and change set the caller supplies, not a moving branch. Read surrounding code when needed to establish an assertion. Do not edit files or perform corrections.

The gate's limit, in seconds, bounds the review. When it runs out, end judging and mark each unjudged assertion undetermined, naming in its evidence what was not reached.

Give each declared assertion exactly one entry in results, in declaration order. Copy the assertion's text exactly into assertion, and set kind to the assertion's kind, must or must not. Set result to one of:
- holds: the revision satisfies the assertion. Put the observed evidence in evidence.
- fails: the revision violates the assertion. Also add one entry to findings for it, with citation set to the assertion's text, and id, claim, location as file:line, evidence as the observed behavior, and verify_by as a concrete check that would confirm or disprove the claim. For an introduced security or data-loss claim, fill precondition and consequence.
- undetermined: the revision does not supply the evidence needed to judge the assertion, or the limit ran out first. Name what is missing in evidence, and list it in missing_evidence.

Only a fails result becomes a finding. An empty findings array means no assertion failed. Anything else you notice, including preferences and opportunities beyond the assertions, goes in observations with its location and the reason it is not a finding.

Commands needing writable scratch state go in test_runner_requests, each with its command and revision.

Return one JSON object matching the step's schema. Set status to done when the review finished with a result for every declared assertion, undetermined results included. Set it to blocked when required input was missing or the review needed a forbidden action, and name which in missing_evidence. Do not invent evidence.`

// Prompt returns the first prompt of an agent gate: the fixed instruction, then the gate's name, its limit in whole
// seconds, and its assertions numbered from 1 with their kinds, then the caller's prompt when it is not empty.
func Prompt(gate Gate, caller string) string {
	sections := []string{
		verdictInstruction,
		fmt.Sprintf("Gate: %s\nLimit: %d seconds", gate.Name, int64(gate.Limit/time.Second)),
		assertionList(gate.Assertions),
	}
	if caller != "" {
		sections = append(sections, caller)
	}
	return strings.Join(sections, "\n\n")
}

// assertionList numbers the assertions from 1, each with its kind.
func assertionList(assertions []Assertion) string {
	lines := make([]string, 0, len(assertions)+1)
	lines = append(lines, "Assertions:")
	for i, a := range assertions {
		lines = append(lines, fmt.Sprintf("%d. %s: %s", i+1, a.Kind, a.Text))
	}
	return strings.Join(lines, "\n")
}
