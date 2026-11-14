package enginehost

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
)

// verdictTestMust and verdictTestMustNot are the texts of the two assertions stepAgentGate defines.
const (
	verdictTestMust    = "the change keeps the API"
	verdictTestMustNot = "the change adds a dependency"
)

// verdictTestEntry is one result of a verdict return, spelled as the verdict schema spells it.
type verdictTestEntry struct {
	Assertion string `json:"assertion"`
	Kind      string `json:"kind"`
	Result    string `json:"result"`
	Evidence  string `json:"evidence"`
}

// verdictTestFinding is one finding of a verdict return, spelled as the verdict schema spells it.
type verdictTestFinding struct {
	ID       string `json:"id"`
	Claim    string `json:"claim"`
	Citation string `json:"citation"`
	Location string `json:"location"`
	Evidence string `json:"evidence"`
	VerifyBy string `json:"verify_by"`
}

// verdictTestReturn is a verdict return with every key the verdict schema requires.
type verdictTestReturn struct {
	Status             string               `json:"status"`
	Results            []verdictTestEntry   `json:"results"`
	Findings           []verdictTestFinding `json:"findings"`
	Observations       []string             `json:"observations"`
	MissingEvidence    []string             `json:"missing_evidence"`
	TestRunnerRequests []string             `json:"test_runner_requests"`
}

// verdictTestResult returns one result of a verdict return with the given assertion, kind and result.
func verdictTestResult(assertion, kind, result string) verdictTestEntry {
	return verdictTestEntry{Assertion: assertion, Kind: kind, Result: result, Evidence: "checked"}
}

// verdictTestCiting returns a finding that cites citation.
func verdictTestCiting(citation string) verdictTestFinding {
	return verdictTestFinding{
		ID: "f1", Claim: "the change breaks it", Citation: citation, Location: "api.go:1", Evidence: "seen",
		VerifyBy: "run the API test",
	}
}

// verdictTestHolds returns the results of a return that holds both assertions of stepAgentGate, in order.
func verdictTestHolds() []verdictTestEntry {
	return []verdictTestEntry{
		verdictTestResult(verdictTestMust, gates.KindMust, "holds"),
		verdictTestResult(verdictTestMustNot, gates.KindMustNot, "holds"),
	}
}

// verdictTestJSON returns a verdict return with status, results and findings, which the verdict schema accepts when
// its results match the gate's assertions. A nil findings is an empty array.
func verdictTestJSON(t *testing.T, status string, results []verdictTestEntry,
	findings []verdictTestFinding) json.RawMessage {
	t.Helper()
	if findings == nil {
		findings = []verdictTestFinding{}
	}
	raw, err := json.Marshal(verdictTestReturn{
		Status: status, Results: results, Findings: findings, Observations: []string{},
		MissingEvidence: []string{}, TestRunnerRequests: []string{},
	})
	if err != nil {
		t.Fatalf("marshal verdict return: %v", err)
	}
	return raw
}

// newVerdictFixture returns a step loop fixture whose step is an agent gate step of stepAgentGate, with haiku as its
// judge. A nil haiku makes no judge call.
func newVerdictFixture(t *testing.T, haiku haikuCaller) *stepLoopFixture {
	t.Helper()
	gate := stepAgentGate("reviewer")
	return newStepDoneFixture(t, func(cfg *stepRunConfig) {
		cfg.schema = gates.VerdictSchema
		cfg.doneValue = stepDefaultDone
		cfg.agentGate = &gate
		cfg.haiku = haiku
	})
}

func TestVerdictReturnThatMisreadsAssertionsIsSentBack(t *testing.T) {
	mustHolds := verdictTestResult(verdictTestMust, gates.KindMust, "holds")
	mustNotHolds := verdictTestResult(verdictTestMustNot, gates.KindMustNot, "holds")
	cases := []struct {
		name    string
		results []verdictTestEntry
		want    string
	}{
		{
			name:    "missing result",
			results: []verdictTestEntry{mustHolds},
			want:    "results has 1 entries for 2 assertions",
		},
		{
			name:    "extra result",
			results: []verdictTestEntry{mustHolds, mustNotHolds, mustHolds},
			want:    "results has 3 entries for 2 assertions",
		},
		{
			name:    "reordered results",
			results: []verdictTestEntry{mustNotHolds, mustHolds},
			want:    `result 1 is for assertion "the change adds a dependency", want "the change keeps the API"`,
		},
		{
			name: "reworded assertion",
			results: []verdictTestEntry{
				verdictTestResult("the change keeps the public API", gates.KindMust, "holds"), mustNotHolds,
			},
			want: `result 1 is for assertion "the change keeps the public API", want "the change keeps the API"`,
		},
		{
			name: "wrong kind",
			results: []verdictTestEntry{
				verdictTestResult(verdictTestMust, gates.KindMustNot, "holds"), mustNotHolds,
			},
			want: `result 1 of assertion "the change keeps the API" has kind "must not", want "must"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newVerdictFixture(t, nil)
			_, done := f.start(t)

			f.run.Return(session.TurnEnded{StructuredOutput: verdictTestJSON(t, "done", tc.results, nil)})
			f.run.Return(session.TurnEnded{StructuredOutput: verdictTestJSON(t, "done", verdictTestHolds(), nil)})
			waitRunEnd(t, done)

			continued := f.sessions.continuedTexts()
			if len(continued) != 1 {
				t.Fatalf("Continue called %d times, want 1", len(continued))
			}
			if !strings.Contains(continued[0], stepLoopAssertionsName+": "+tc.want) {
				t.Errorf("Continue text %q does not name the assertions mismatch %q", continued[0], tc.want)
			}
			row := f.row(t)
			if row.End != stepLoopEndDone {
				t.Fatalf("end = %q, want done once the corrected return holds", row.End)
			}
			failed := stepDoneCheck(t, stepDoneRoundChecks(row.Checks, 1), stepLoopAssertionsName)
			if failed.OK || failed.Detail != tc.want {
				t.Errorf("round 1 assertions check = %+v, want failing with detail %q", failed, tc.want)
			}
			if passed := stepDoneCheck(t, stepDoneRoundChecks(row.Checks, 2), stepLoopAssertionsName); !passed.OK {
				t.Errorf("round 2 assertions check = %+v, want passing", passed)
			}
		})
	}
}

func TestVerdictAllHoldsEndsDone(t *testing.T) {
	f := newVerdictFixture(t, nil)
	_, done := f.start(t)
	raw := verdictTestJSON(t, "done", verdictTestHolds(), nil)

	f.run.Return(session.TurnEnded{StructuredOutput: raw})
	waitRunEnd(t, done)

	row := f.row(t)
	if row.End != stepLoopEndDone || row.Status != "done" || row.Reason != "" {
		t.Fatalf("end = %q status %q reason %q, want done with no reason", row.End, row.Status, row.Reason)
	}
	if row.Result != string(raw) {
		t.Errorf("result = %s, want %s", row.Result, raw)
	}
	wantChecks := []store.StepCheck{
		{Round: 1, Name: stepLoopSchemaName, OK: true},
		{Round: 1, Name: stepLoopAssertionsName, OK: true},
	}
	if !reflect.DeepEqual(row.Checks, wantChecks) {
		t.Errorf("checks = %+v, want %+v", row.Checks, wantChecks)
	}
	if got := f.sessions.continuedTexts(); len(got) != 0 {
		t.Errorf("Continue called %d times for a return that holds, want 0", len(got))
	}
}

func TestVerdictNotAllHeldEndsGateFailed(t *testing.T) {
	mustHolds := verdictTestResult(verdictTestMust, gates.KindMust, "holds")
	mustNotHolds := verdictTestResult(verdictTestMustNot, gates.KindMustNot, "holds")
	cases := []struct {
		name     string
		status   string
		results  []verdictTestEntry
		findings []verdictTestFinding
		reason   string
	}{
		{
			name:     "fails with its finding",
			status:   "done",
			results:  []verdictTestEntry{mustHolds, verdictTestResult(verdictTestMustNot, gates.KindMustNot, "fails")},
			findings: []verdictTestFinding{verdictTestCiting(verdictTestMustNot)},
			reason:   "fails: the change adds a dependency",
		},
		{
			name:   "undetermined",
			status: "done",
			results: []verdictTestEntry{
				verdictTestResult(verdictTestMust, gates.KindMust, "undetermined"), mustNotHolds,
			},
			reason: "undetermined: the change keeps the API",
		},
		{
			name:   "fails and undetermined",
			status: "done",
			results: []verdictTestEntry{
				verdictTestResult(verdictTestMust, gates.KindMust, "fails"),
				verdictTestResult(verdictTestMustNot, gates.KindMustNot, "undetermined"),
			},
			findings: []verdictTestFinding{verdictTestCiting(verdictTestMust)},
			reason:   "fails: the change keeps the API; undetermined: the change adds a dependency",
		},
		{
			name:    "blocked with every assertion held",
			status:  "blocked",
			results: verdictTestHolds(),
			reason:  "status blocked",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newVerdictFixture(t, nil)
			_, done := f.start(t)
			raw := verdictTestJSON(t, tc.status, tc.results, tc.findings)

			f.run.Return(session.TurnEnded{StructuredOutput: raw})
			waitRunEnd(t, done)

			row := f.row(t)
			if row.End != stepLoopEndGateFailed || row.Status != tc.status || row.Reason != tc.reason {
				t.Fatalf("end = %q status %q reason %q, want gate_failed %q with reason %q",
					row.End, row.Status, row.Reason, tc.status, tc.reason)
			}
			if row.Result != string(raw) {
				t.Errorf("result = %s, want the return kept as %s", row.Result, raw)
			}
			if got := f.sessions.continuedTexts(); len(got) != 0 {
				t.Errorf("Continue called %d times for a gate verdict, want 0", len(got))
			}
			if len(row.Verdicts) != 0 {
				t.Errorf("stored %d judge verdicts for an agent gate, want 0", len(row.Verdicts))
			}
		})
	}
}

func TestVerdictEarlierFailsEndsGateFailed(t *testing.T) {
	mustNotHolds := verdictTestResult(verdictTestMustNot, gates.KindMustNot, "holds")
	cases := []struct {
		name string
		// first is the earlier return, sent back with the assertions mismatch want.
		first []verdictTestEntry
		want  string
		// reason is the gate_failed reason the corrected return that holds every assertion ends with.
		reason string
	}{
		{
			name: "fails without its finding",
			first: []verdictTestEntry{
				verdictTestResult(verdictTestMust, gates.KindMust, "fails"), mustNotHolds,
			},
			want:   `result 1 fails assertion "the change keeps the API" and no finding cites it`,
			reason: "an earlier return held fails: the change keeps the API",
		},
		{
			name: "fails with a blank assertion",
			first: []verdictTestEntry{
				verdictTestResult("", gates.KindMust, "fails"), mustNotHolds,
			},
			want:   `result 1 is for assertion "", want "the change keeps the API"`,
			reason: "an earlier return held fails: ",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newVerdictFixture(t, nil)
			_, done := f.start(t)

			f.run.Return(session.TurnEnded{StructuredOutput: verdictTestJSON(t, "done", tc.first, nil)})
			f.run.Return(session.TurnEnded{StructuredOutput: verdictTestJSON(t, "done", verdictTestHolds(), nil)})
			waitRunEnd(t, done)

			continued := f.sessions.continuedTexts()
			if len(continued) != 1 {
				t.Fatalf("Continue called %d times, want 1 for the earlier return", len(continued))
			}
			if !strings.Contains(continued[0], stepLoopAssertionsName+": "+tc.want) {
				t.Errorf("Continue text %q does not name the assertions mismatch %q", continued[0], tc.want)
			}
			row := f.row(t)
			if row.End != stepLoopEndGateFailed || row.Reason != tc.reason {
				t.Fatalf("end = %q reason %q, want gate_failed with reason %q", row.End, row.Reason, tc.reason)
			}
		})
	}
}

func TestVerdictAgentGateReturnIsNotJudged(t *testing.T) {
	cases := []struct {
		name     string
		judge    judgeLoopReply
		results  []verdictTestEntry
		findings []verdictTestFinding
		end      string
		reason   string
	}{
		{
			name:    "every assertion held, judge unmet",
			judge:   judgeLoopVerdict("unmet", "the judge would send it back"),
			results: verdictTestHolds(),
			end:     stepLoopEndDone,
		},
		{
			name:  "assertion failed, judge met",
			judge: judgeLoopVerdict("met", "the judge would end it done"),
			results: []verdictTestEntry{
				verdictTestResult(verdictTestMust, gates.KindMust, "fails"),
				verdictTestResult(verdictTestMustNot, gates.KindMustNot, "holds"),
			},
			findings: []verdictTestFinding{verdictTestCiting(verdictTestMust)},
			end:      stepLoopEndGateFailed,
			reason:   "fails: the change keeps the API",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			haiku := &judgeLoopHaiku{replies: []judgeLoopReply{tc.judge}}
			f := newVerdictFixture(t, haiku)
			_, done := f.start(t)

			f.run.Return(session.TurnEnded{StructuredOutput: verdictTestJSON(t, "done", tc.results, tc.findings)})
			waitRunEnd(t, done)

			row := f.row(t)
			if row.End != tc.end || row.Reason != tc.reason {
				t.Fatalf("end = %q reason %q, want %q with reason %q", row.End, row.Reason, tc.end, tc.reason)
			}
			if got := len(haiku.callInputs()); got != 0 {
				t.Errorf("judge called %d times for an agent gate step, want 0", got)
			}
			if len(row.Verdicts) != 0 {
				t.Errorf("stored %d judge verdicts for an agent gate step, want 0", len(row.Verdicts))
			}
		})
	}
}

func TestVerdictOrdinaryReturnIsJudged(t *testing.T) {
	haiku := &judgeLoopHaiku{replies: []judgeLoopReply{judgeLoopVerdict("met", "the report is complete")}}
	f := newJudgeLoopFixture(t, haiku, judgeLoopNoConfig)
	_, done := f.start(t)

	f.run.Return(judgeLoopDone("only"))
	waitRunEnd(t, done)

	if got := len(haiku.callInputs()); got != 1 {
		t.Fatalf("judge called %d times for an ordinary step, want 1", got)
	}
	if row := f.row(t); row.End != stepLoopEndDone {
		t.Errorf("end = %q, want done", row.End)
	}
}
