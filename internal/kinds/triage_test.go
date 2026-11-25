package kinds

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// changeNote is the line a failure's text carries for a change triage that cites nothing and gives no reason.
const changeNote = "triage: change"

// changeTriaged returns the triage of the check with id that found a change, citing nothing and giving no reason.
func changeTriaged(id string) store.GateTriage {
	return store.GateTriage{Check: id, Verdict: TriageChange}
}

// triaged returns the triage of the check with id that gave verdict, quoting evidence.
func triaged(id, verdict, evidence string) store.GateTriage {
	return store.GateTriage{Check: id, Verdict: verdict, Evidence: evidence}
}

// triageStepID returns the id of the triage step of the check whose work is part, of the test run at attempt 1.
func triageStepID(part string) string {
	return workID("triage " + part)
}

// triageStart returns the action that starts the triage of the check of part, with the checks before it, for gate,
// with allowed the verdicts a rerun may still follow.
func triageStart(part string, before []string, gate string, allowed ...string) Action {
	ids := append([]string{triageStepID(part), workID(part)}, before...)
	return Action{Kind: ActTriage, IDs: ids, Items: append([]string{TriageStart, gate}, allowed...)}
}

// triageCheckCases returns the decisions a gate or command node of kind makes for a check that ran and failed, with gate
// the name its triage action carries.
func triageCheckCases(kind, gate string) []decideCase {
	node := gateNode(kind, true)
	if gate != "" {
		node.Def.Settings = &workflow.Settings{Gate: gate}
	}
	failed := failedCheck("check")
	return []decideCase{
		{
			"failed check starts its triage",
			node,
			Rows{Checks: []store.NodeCheck{failed}},
			triageStart("check", nil, gate, TriageEnvironment, TriageFlake),
		},
		{
			"timed out check starts its triage",
			node,
			Rows{Checks: []store.NodeCheck{timedOutCheck("check")}},
			triageStart("check", nil, gate, TriageEnvironment, TriageFlake),
		},
		{
			"failed check of a paused epic waits for its triage",
			node,
			Rows{Epic: store.EpicRow{Paused: true}, Checks: []store.NodeCheck{failed}},
			Action{Kind: ActNone},
		},
		{
			"failed check of a parked epic waits for its triage",
			node,
			Rows{Epic: store.EpicRow{Parked: "waiting on the operator"}, Checks: []store.NodeCheck{failed}},
			Action{Kind: ActNone},
		},
		{
			"triage whose step runs waits",
			node,
			Rows{Checks: []store.NodeCheck{failed}, Steps: []store.StepRow{{ID: triageStepID("check")}}},
			Action{Kind: ActNone},
		},
		{
			"triage whose step ended is recorded",
			node,
			Rows{Checks: []store.NodeCheck{failed}, Steps: []store.StepRow{{ID: triageStepID("check"), End: "done"}}},
			Action{
				Kind: ActTriage, IDs: []string{triageStepID("check"), workID("check")},
				Items: []string{TriageRecord, gate, TriageEnvironment, TriageFlake},
			},
		},
		{
			"environment triage reruns the check",
			node,
			Rows{Checks: []store.NodeCheck{failed}, Triages: []store.GateTriage{
				triaged(workID("check"), TriageEnvironment, "load 139.20 121.48 131.33, nproc 32"),
			}},
			Action{Kind: ActStart, IDs: []string{workID("check rerun")}},
		},
		{
			"flake triage reruns the check",
			node,
			Rows{Checks: []store.NodeCheck{failed}, Triages: []store.GateTriage{
				triaged(workID("check"), TriageFlake, "--- FAIL: TestRace"),
			}},
			Action{Kind: ActStart, IDs: []string{workID("check rerun")}},
		},
		{
			"rerun that passes passes",
			node,
			Rows{
				Checks:  []store.NodeCheck{failed, passedCheck("check rerun")},
				Triages: []store.GateTriage{triaged(workID("check"), TriageEnvironment, "load 99")},
			},
			Action{Kind: ActEnd, Outcome: outcomePassed},
		},
		{
			"rerun that fails starts its own triage within what is left",
			node,
			Rows{
				Checks:  []store.NodeCheck{failed, failedCheck("check rerun")},
				Triages: []store.GateTriage{triaged(workID("check"), TriageFlake, "--- FAIL: TestRace")},
			},
			triageStart("check rerun", []string{workID("check")}, gate, TriageEnvironment),
		},
		{
			"change triage fails the check with the triage's line",
			node,
			Rows{Checks: []store.NodeCheck{failed}, Triages: []store.GateTriage{{
				Check: workID("check"), Verdict: TriageChange, Citation: "--- FAIL: TestX",
				Reason: "the batch broke it",
			}}},
			Action{
				Kind: ActEnd, Outcome: outcomeFailed,
				Output: `triage: change, citing "--- FAIL: TestX": the batch broke it`,
			},
		},
		{
			"change triage waits for the refs check",
			gateNode(kind, false),
			Rows{Checks: []store.NodeCheck{failed}, Triages: []store.GateTriage{changeTriaged(workID("check"))}},
			Action{Kind: ActRefs},
		},
	}
}

func TestGateTriage(t *testing.T) {
	t.Parallel()
	checkDecisions(t, Gate{}, triageCheckCases("gate", "lint"))
}

func TestCommandTriage(t *testing.T) {
	t.Parallel()
	checkDecisions(t, Command{}, triageCheckCases("command", ""))
}

// TestCostIsNeverTriaged: a cost check that exits nonzero ends passed with its output, with no triage started, since
// the cost is measured and never judged.
func TestCostIsNeverTriaged(t *testing.T) {
	t.Parallel()
	got := Cost{}.Decide(gateNode("cost", true), Rows{Checks: []store.NodeCheck{failedCheck("check")}})
	want := Action{Kind: ActEnd, Outcome: outcomePassed}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Decide() = %+v, want %+v", got, want)
	}
}

// spentChecks returns the checks of part of the test run, the first and then count reruns, each failed, with the
// triages verdicts give the first len(verdicts) of them.
func spentChecks(part string, count int, verdicts ...string) ([]store.NodeCheck, []store.GateTriage) {
	checks := make([]store.NodeCheck, 0, count+1)
	triages := make([]store.GateTriage, 0, len(verdicts))
	for k := range count + 1 {
		checks = append(checks, failedCheck(runWork(part, k)))
		if k < len(verdicts) {
			triages = append(triages, triaged(workID(runWork(part, k)), verdicts[k], "load 300"))
		}
	}
	return checks, triages
}

// TestTriageBudgets: environment verdicts rerun a part twice and a flake verdict once, three reruns in all, and a check
// that fails once no rerun could follow it is not triaged and fails its part with what its triages found.
func TestTriageBudgets(t *testing.T) {
	t.Parallel()
	node := gateNode("gate", true)
	twoEnvironment, twoEnvironmentTriages := spentChecks("check", 2, TriageEnvironment, TriageEnvironment)
	allSpent, allSpentTriages := spentChecks("check", 3, TriageEnvironment, TriageEnvironment, TriageFlake)
	flakeSpent, flakeSpentTriages := spentChecks("check", 1, TriageFlake)
	cases := []decideCase{
		{
			"third failure after two environment reruns is triaged for a flake only",
			node,
			Rows{Checks: twoEnvironment, Triages: twoEnvironmentTriages},
			triageStart("check rerun rerun", []string{workID("check"), workID("check rerun")}, "",
				TriageFlake),
		},
		{
			"failure after every triage rerun fails with no triage",
			node,
			Rows{Checks: allSpent, Triages: allSpentTriages},
			Action{
				Kind: ActEnd, Outcome: outcomeFailed,
				Output: "triage: the gate was rerun on its earlier triages' verdicts (environment, environment, " +
					"flake) and failed again",
			},
		},
		{
			"a third environment verdict past its budget fails",
			node,
			Rows{
				Checks: append(twoEnvironment, failedCheck("check rerun rerun rerun")),
				Triages: append(slices.Clone(twoEnvironmentTriages),
					triaged(workID("check rerun rerun"), TriageEnvironment, "load 300")),
			},
			Action{
				Kind: ActEnd, Outcome: outcomeFailed,
				Output: `triage: environment, quoting "load 300"; the gate was rerun on its earlier triages' ` +
					"verdicts (environment, environment) and failed again",
			},
		},
		{
			"failure after the flake rerun is triaged for an environment only",
			node,
			Rows{Checks: flakeSpent, Triages: flakeSpentTriages},
			triageStart("check rerun", []string{workID("check")}, "", TriageEnvironment),
		},
	}
	checkDecisions(t, Gate{}, cases)
}

// TestTriageKeepsTheEnvironmentExitRule: an exit of 75 keeps its own two reruns beside the triage reruns, and a check
// that exits 75 is never triaged, timed out or not.
func TestTriageKeepsTheEnvironmentExitRule(t *testing.T) {
	t.Parallel()
	node := gateNode("gate", false)
	flaked := []store.GateTriage{triaged(workID("check"), TriageFlake, "--- FAIL: TestRace")}
	cases := []decideCase{
		{
			"exit 75 after a flake rerun reruns",
			node,
			Rows{
				Checks:  []store.NodeCheck{failedCheck("check"), environmentCheck("check rerun")},
				Triages: flaked,
			},
			Action{Kind: ActStart, IDs: []string{workID("check rerun rerun")}},
		},
		{
			"second exit 75 after a flake rerun reruns once more",
			node,
			Rows{
				Checks: []store.NodeCheck{
					failedCheck("check"), environmentCheck("check rerun"), environmentCheck("check rerun rerun"),
				},
				Triages: flaked,
			},
			Action{Kind: ActStart, IDs: []string{workID("check rerun rerun rerun")}},
		},
		{
			"third exit 75 after a flake rerun parks",
			node,
			Rows{
				Checks: []store.NodeCheck{
					failedCheck("check"), environmentCheck("check rerun"), environmentCheck("check rerun rerun"),
					environmentCheck("check rerun rerun rerun"),
				},
				Triages: flaked,
			},
			Action{Kind: ActPark, Output: "gate plan/S1/unit exited 75 on its last run: " + environmentReason},
		},
		{
			"timed out exit 75 is not triaged",
			gateNode("gate", true),
			Rows{Checks: []store.NodeCheck{{
				ID: workID("check"), State: store.NodeEnded, Exit: store.EnvironmentExit, TimedOut: true,
			}}},
			Action{Kind: ActEnd, Outcome: outcomeFailed},
		},
	}
	checkDecisions(t, Gate{}, cases)
}

// TestIntegrateDoneGateTriage: a Done gate that fails starts its triage under the integrate run's id, an environment
// verdict reruns it, and a change ends the batch red with the triage's line.
func TestIntegrateDoneGateTriage(t *testing.T) {
	t.Parallel()
	epic := integrateEpic()
	cases := []decideCase{
		{
			"failed Done gate starts its triage",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: []store.NodeCheck{failedCheck("done 1")}},
			triageStart("done 1", nil, "just check", TriageEnvironment, TriageFlake),
		},
		{
			"environment triage reruns the Done gate",
			batchNode(testRev, composedOne, true),
			Rows{
				Epic: epic, Checks: []store.NodeCheck{failedCheck("done 1")},
				Triages: []store.GateTriage{triaged(workID("done 1"), TriageEnvironment, "load 300")},
			},
			Action{Kind: ActStart, IDs: []string{workID("done 1 rerun")}, Items: []string{"just check"}},
		},
		{
			"change triage ends the batch red with its line",
			batchNode(testRev, composedOne, true),
			Rows{
				Epic: epic, Checks: []store.NodeCheck{failedCheck("done 1")},
				Triages: []store.GateTriage{{
					Check: workID("done 1"), Verdict: TriageChange, Citation: "a.go:3: unused", Reason: "lint finding",
				}},
			},
			Action{
				Kind: ActEnd, Outcome: outcomeFailed,
				Output: `Done gate just check failed; triage: change, citing "a.go:3: unused": lint finding`,
			},
		},
	}
	checkDecisions(t, Integrate{}, cases)
}

// triageOutput is the output of a failed check the tests judge triages of: a test budget cut short under load.
func triageOutput() store.NodeCheck {
	return store.NodeCheck{
		ID: workID("check"), State: store.NodeEnded, Exit: 124,
		Stdout: "ok  \tgithub.com/x/y\t257.075s\n",
		Stderr: "gate-remote: running just check here (no box; load 139.20 121.48 131.33, nproc 32)\n" +
			"\x1b[35;1mwarning:\x1b[0m slow\ntests exceeded 360s budget\n--- FAIL: TestSlow (3.46s)\n" +
			"error: recipe `cover` failed with exit code 124\n",
	}
}

// triageRow returns the ended step row of a triage whose step ended end with ret as its return.
func triageRow(t *testing.T, end string, ret map[string]string) store.StepRow {
	t.Helper()
	encoded, err := json.Marshal(ret)
	if err != nil {
		t.Fatalf("encode return: %v", err)
	}
	return store.StepRow{ID: triageStepID("check"), End: end, Result: string(encoded)}
}

func TestJudgeTriage(t *testing.T) {
	t.Parallel()
	both := []string{TriageEnvironment, TriageFlake}
	cases := []struct {
		name    string
		end     string
		ret     map[string]string
		allowed []string
		want    store.GateTriage
	}{
		{
			"environment whose evidence is in the tails stands",
			"done",
			map[string]string{
				"verdict":  "environment",
				"evidence": "load 139.20   121.48 131.33, nproc 32\ntests exceeded 360s budget",
				"reason":   "the host was loaded",
			},
			both,
			store.GateTriage{
				Verdict:  TriageEnvironment,
				Evidence: "load 139.20   121.48 131.33, nproc 32\ntests exceeded 360s budget",
				Reason:   "the host was loaded",
			},
		},
		{
			"evidence quoting a line with its escapes taken out stands",
			"done",
			map[string]string{"verdict": "flake", "evidence": "warning: slow", "reason": "timing"},
			both,
			store.GateTriage{Verdict: TriageFlake, Evidence: "warning: slow", Reason: "timing"},
		},
		{
			"environment with evidence not in the tails counts as a change",
			"done",
			map[string]string{"verdict": "environment", "evidence": "load was very high", "reason": "loaded"},
			both,
			store.GateTriage{Verdict: TriageChange, Reason: `the triage answered environment, but its evidence is ` +
				`refused (evidence: "load was very high" does not appear in the gate's output; quote its lines ` +
				`exactly as they stand), so the failure counts as a change; its reason: loaded`},
		},
		{
			"environment with no evidence counts as a change",
			"done",
			map[string]string{"verdict": "environment", "evidence": "  ", "reason": "loaded"},
			both,
			store.GateTriage{Verdict: TriageChange, Reason: "the triage answered environment, but its evidence is " +
				"refused (evidence: a verdict of environment quotes the line or lines of the gate's output that " +
				"show it), so the failure counts as a change; its reason: loaded"},
		},
		{
			"flake past its budget counts as a change",
			"done",
			map[string]string{"verdict": "flake", "evidence": "--- FAIL: TestSlow", "reason": "racy"},
			[]string{TriageEnvironment},
			store.GateTriage{
				Verdict: TriageChange, Evidence: "--- FAIL: TestSlow",
				Reason: "the triage answered flake, but this gate's flake reruns are spent, so the failure counts " +
					"as a change; its reason: racy",
			},
		},
		{
			"change with a citation in the tails keeps it",
			"done",
			map[string]string{"verdict": "change", "citation": " --- FAIL: TestSlow (3.46s) ", "reason": "it hangs"},
			both,
			store.GateTriage{Verdict: TriageChange, Citation: "--- FAIL: TestSlow (3.46s)", Reason: "it hangs"},
		},
		{
			"change with a citation not in the tails drops it",
			"done",
			map[string]string{"verdict": "change", "citation": "--- FAIL: TestOther", "reason": "it hangs"},
			both,
			store.GateTriage{
				Verdict: TriageChange,
				Reason:  `it hangs (its citation "--- FAIL: TestOther" does not appear in the gate's output)`,
			},
		},
		{
			"change with no citation stands without one",
			"done",
			map[string]string{"verdict": "change", "citation": "", "reason": "the tails show none"},
			both,
			store.GateTriage{
				Verdict: TriageChange, Reason: "the tails show none (it cites no failing test or lint line)",
			},
		},
		{
			"unknown verdict counts as a change",
			"done",
			map[string]string{"verdict": "maybe", "reason": "unsure"},
			both,
			store.GateTriage{
				Verdict: TriageChange, Reason: `the triage named no known verdict ("maybe"), so the failure counts ` +
					"as a change",
			},
		},
		{
			"step that did not end done counts as a change",
			"stopped",
			nil,
			both,
			store.GateTriage{
				Verdict: TriageChange,
				Reason:  "the triage step ended stopped, so the failure counts as a change",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := JudgeTriage(triageOutput(), triageRow(t, tc.end, tc.ret), tc.allowed)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("JudgeTriage() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestJudgeTriageRefusesAReturnThatDoesNotDecode: a done step whose return is not a JSON object counts as a change.
func TestJudgeTriageRefusesAReturnThatDoesNotDecode(t *testing.T) {
	t.Parallel()
	got := JudgeTriage(triageOutput(), store.StepRow{End: "done", Result: "not json"}, []string{TriageEnvironment})
	if got.Verdict != TriageChange || !strings.HasPrefix(got.Reason, "the triage's return is refused") {
		t.Errorf("JudgeTriage() = %+v, want a change with the refusal", got)
	}
}

func TestTriageFailures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ret  string
		want []string
	}{
		{"environment quoting the tails passes", `{"verdict":"environment","evidence":"nproc 32"}`, nil},
		{"change citing nothing passes", `{"verdict":"change","citation":""}`, nil},
		{
			"environment quoting nothing fails",
			`{"verdict":"environment","evidence":""}`,
			[]string{"evidence: a verdict of environment quotes the line or lines of the gate's output that show it"},
		},
		{
			"flake quoting a line not in the tails fails on that line",
			`{"verdict":"flake","evidence":"nproc 32\nTestRace failed"}`,
			[]string{`evidence: "TestRace failed" does not appear in the gate's output; quote its lines exactly as ` +
				`they stand`},
		},
		{
			"change citing a line not in the tails fails",
			`{"verdict":"change","citation":"main.go:9: boom"}`,
			[]string{`citation: "main.go:9: boom" does not appear in the gate's output; cite the failing line ` +
				`exactly as it stands, or leave citation empty when the output shows none`},
		},
		{
			"unknown verdict fails",
			`{"verdict":"unsure"}`,
			[]string{`verdict: "unsure" is none of environment, flake and change`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := TriageFailures(triageOutput(), []byte(tc.ret)); !slices.Equal(got, tc.want) {
				t.Errorf("TriageFailures() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTriageText(t *testing.T) {
	t.Parallel()
	got := TriageText("\x1b[35;1mwarning:\x1b[0m a\r\nb\rc\x07\td")
	if want := "warning: a\nb\nc\td"; got != want {
		t.Errorf("TriageText() = %q, want %q", got, want)
	}
}
