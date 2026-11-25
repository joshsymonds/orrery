package interpret

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// The agents the verification tests' role files name for the verify and judge roles.
const (
	verifyAgent      = "verify-agent"
	verifyJudgeAgent = "judge-agent"
)

// verifyRoles writes a role file whose keys and agents are roles, and returns a getenv that points the runner at it.
func verifyRoles(t *testing.T, roles map[string]string) func(string) string {
	t.Helper()
	config := t.TempDir()
	dir := filepath.Join(config, "orrery")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("make role directory: %v", err)
	}
	var text strings.Builder
	for _, role := range slices.Sorted(maps.Keys(roles)) {
		fmt.Fprintf(&text, "%s = %q\n", role, roles[role])
	}
	if err := os.WriteFile(filepath.Join(dir, "roles.toml"), []byte(text.String()), 0o600); err != nil {
		t.Fatalf("write role file: %v", err)
	}
	return func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return config
		}
		return ""
	}
}

// verifyGates are the gates of the verification tests' epic: lint, a command gate with a note, and check, a command
// gate declared always, which the Done gates run in that order.
func verifyGates() []store.EpicGate {
	return []store.EpicGate{
		{
			Name:  "lint",
			Argv:  []string{"./bin/lint"},
			Limit: 60,
			Needs: []store.EpicGateNeed{{Pool: "lint", Count: 2}},
			Note:  "lints every Go package",
		},
		{Name: "check", Argv: []string{"./bin/check", "--all"}, Limit: 1800, Always: true, Note: "the full suite"},
	}
}

// newVerifyWorld returns an integrate world over the verification tests' epic, whose runner reads roles, with its
// verification steps left running for the test to end. Its batch is claimed, so its verification step has started.
func newVerifyWorld(t *testing.T, roles map[string]string) *integrateWorld {
	t.Helper()
	w := newIntegrateWorld(t, gatedBy(verifyGates(), "lint", "check"))
	w.runner.getenv = verifyRoles(t, roles)
	w.fakes.steps.verified = ""
	w.toGate()
	return w
}

// verifyStart returns the start of the verification step with id, which must have started.
func verifyStart(t *testing.T, w *integrateWorld, id string) enginehost.StepStart {
	t.Helper()
	i := slices.IndexFunc(w.fakes.steps.calls, func(start enginehost.StepStart) bool { return start.ID == id })
	if i < 0 {
		t.Fatalf("no start of verification step %s among %+v", id, w.fakes.steps.calls)
	}
	return w.fakes.steps.calls[i]
}

// recordedVerification returns the batch verification the result of the run at path records, which must be recorded.
func recordedVerification(t *testing.T, w *integrateWorld, path string) kinds.Verification {
	t.Helper()
	var batch batchResult
	if err := json.Unmarshal([]byte(runAt(t, w.s, path).Result), &batch); err != nil {
		t.Fatalf("decode result of %s: %v", path, err)
	}
	if batch.Verification == nil {
		t.Fatalf("result of %s records no verification: %s", path, runAt(t, w.s, path).Result)
	}
	return *batch.Verification
}

// checkStarted reports whether a check whose id is id was started.
func checkStarted(w *integrateWorld, id string) bool {
	return slices.ContainsFunc(w.fakes.checks.calls, func(check store.NodeCheck) bool { return check.ID == id })
}

// verifyReturn returns a verification return of gates and requirements, each a name, a choice and a reason.
func verifyReturn(gates, requirements [][3]string) map[string]any {
	gateEntries := make([]map[string]any, 0, len(gates))
	for _, gate := range gates {
		gateEntries = append(gateEntries, map[string]any{"gate": gate[0], "run": gate[1] == "run", "reason": gate[2]})
	}
	requirementEntries := make([]map[string]any, 0, len(requirements))
	for _, requirement := range requirements {
		requirementEntries = append(requirementEntries, map[string]any{
			"requirement": requirement[0], "capture": requirement[1] == "capture", "reason": requirement[2],
		})
	}
	return map[string]any{"status": "done", "gates": gateEntries, "requirements": requirementEntries}
}

// TestVerifyStartsUnderTheVerifyRoleAtTheCandidate: a claimed batch starts one read-only verification step at its
// candidate, in the epic's repository, under the verify role's agent, labeled with the batch's path, before any Done
// gate. Its prompt gives the change from the batch's base, the batch's tasks with their claims, each Done gate with its
// argv, limit, needs, always and note, the Requirements, and the floor.
func TestVerifyStartsUnderTheVerifyRoleAtTheCandidate(t *testing.T) {
	t.Parallel()
	w := newVerifyWorld(t, map[string]string{roleVerify: verifyAgent, roleJudge: verifyJudgeAgent})
	run := runAt(t, w.s, integrateBatch)
	start := verifyStart(t, w, kinds.VerifyID(run, 0, 0))
	if start.Agent != verifyAgent || !start.ReadOnly || start.Revision != run.Revision ||
		start.Workspace != w.repo || start.Task != integrateBatch || start.Epic != runEpic ||
		start.Schema != VerifySchema {
		t.Errorf("start = %+v, want a read-only %s step at %s in %s labeled %s with the verification schema",
			start, verifyAgent, run.Revision, w.repo, integrateBatch)
	}
	if len(w.fakes.checks.calls) != 0 {
		t.Errorf("checks = %+v, want no Done gate started before the choice is recorded", w.fakes.checks.calls)
	}
	var batch batchResult
	if err := json.Unmarshal([]byte(run.Result), &batch); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	for _, want := range []string{
		"\n## Commit\nCommit: " + run.Revision + ", the candidate of " + integrateBatch +
			"\nIts change runs from: " + batch.Base + "\n",
		"\n## Diff stat\n b.go",
		"\n## Changed paths\nA\tb.go\nA\tinternal/a/x.go\n",
		"\n## Conflict resolutions\nNone.\n",
		"\n## Tasks\n- T1 first\n  deliverable: the runner\n  claims: R1\n  contributes to: None.\n- T2 second\n",
		"\n## Done gates\n- lint: command [\"./bin/lint\"]\n  limit: 60s\n  needs: lint x 2\n" +
			"  note: lints every Go package\n- check: command [\"./bin/check\",\"--all\"]\n  limit: 1800s\n" +
			"  needs: gate x 1\n  always: runs on every verification, whatever the choice says\n  note: the full suite\n",
		"\n## Requirements\n- R1: the runner runs\n",
		"\n## Captured whatever you choose\nR1: a task above claims or contributes to each\n",
	} {
		if !strings.Contains(start.Prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, start.Prompt)
		}
	}
	if !strings.HasPrefix(start.Prompt, strings.TrimRight(verifyRole, "\n")) {
		t.Errorf("prompt does not open with the role text:\n%s", start.Prompt)
	}
}

// TestVerifyFallsBackToTheJudgeRole: a role file with no verify key runs the verification step under the judge role.
func TestVerifyFallsBackToTheJudgeRole(t *testing.T) {
	t.Parallel()
	w := newVerifyWorld(t, map[string]string{roleJudge: verifyJudgeAgent})
	start := verifyStart(t, w, kinds.VerifyID(runAt(t, w.s, integrateBatch), 0, 0))
	if start.Agent != verifyJudgeAgent {
		t.Errorf("agent = %q, want the judge role's %q", start.Agent, verifyJudgeAgent)
	}
}

// TestVerifyChoiceSkipsAGateAndKeepsTheFloors: a choice that skips both Done gates and every capture runs check anyway,
// since it is declared always, under its own check id done 2, and never starts lint's done 1. R1, which the batch's
// tasks claim, is captured though the choice says not to. The batch advances, its output carries each choice, and the
// Test cost reads only the check that ran.
func TestVerifyChoiceSkipsAGateAndKeepsTheFloors(t *testing.T) {
	t.Parallel()
	w := newVerifyWorld(t, map[string]string{roleVerify: verifyAgent})
	run := runAt(t, w.s, integrateBatch)
	id := kinds.VerifyID(run, 0, 0)
	endTaskStep(t, w.s, id, "done", "done", verifyReturn(
		[][3]string{{"lint", "skip", "no Go file changed"}, {"check", "skip", "only docs changed"}},
		[][3]string{{"R1", "skip", "untouched"}},
	))
	w.pass()

	chosen := recordedVerification(t, w, integrateBatch)
	if chosen.Step != id || chosen.Fallback != "" || !chosen.Skips("lint") || chosen.Skips("check") ||
		!chosen.Captures("R1") {
		t.Errorf("recorded = %+v, want lint skipped, check run as always and R1 captured", chosen)
	}
	if checkStarted(w, workID(t, w.s, integrateBatch, "done 1")) {
		t.Errorf("checks = %+v, want no check for the skipped lint", w.fakes.checks.calls)
	}
	nodeEndCheck(t, w.s, workID(t, w.s, integrateBatch, "done 2"), 0, "")
	w.pass()
	w.endCapture(integrateBatch)
	w.pass()

	ended := runAt(t, w.s, integrateBatch)
	if ended.State != store.NodeEnded || ended.Outcome != "passed" {
		t.Fatalf("batch = %+v, want ended passed", ended)
	}
	for _, want := range []string{
		"verification skipped Done gate lint: no Go file changed",
		"verification ran Done gate check: it is declared always; the step said: only docs changed",
		"verification captured R1: a task it verifies claims or contributes to it; the step said: untouched",
	} {
		if !strings.Contains(ended.Output, want) {
			t.Errorf("output %q lacks %q", ended.Output, want)
		}
	}
	checks, _, err := w.runner.doneGateChecks(t.Context(), runEpic, ended.Revision)
	if err != nil {
		t.Fatalf("doneGateChecks: %v", err)
	}
	ids := make([]string, 0, len(checks))
	for _, check := range checks {
		ids = append(ids, check.ID)
	}
	if want := []string{workID(t, w.s, integrateBatch, "done 2")}; !slices.Equal(ids, want) {
		t.Errorf("Done gate checks at the candidate = %v, want only check's %v", ids, want)
	}
}

// TestVerifyFallsBack: a verification step that did not end done, a return whose entry gives no reason, and a start
// the engine refuses for good each record the fallback with why, and every Done gate runs, lint first under done 1.
func TestVerifyFallsBack(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		end  func(t *testing.T, w *integrateWorld, id string)
		why  string
	}{
		{
			"a step that did not end done",
			func(t *testing.T, w *integrateWorld, id string) {
				t.Helper()
				endTaskStep(t, w.s, id, "failed", "", nil)
			},
			"the verification step did not end done: it ended failed",
		},
		{
			"a return whose entry gives no reason",
			func(t *testing.T, w *integrateWorld, id string) {
				t.Helper()
				endTaskStep(t, w.s, id, "done", "done", verifyReturn([][3]string{{"lint", "skip", ""}}, nil))
			},
			"gates[0] (lint) gives no reason",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newVerifyWorld(t, map[string]string{roleVerify: verifyAgent})
			id := kinds.VerifyID(runAt(t, w.s, integrateBatch), 0, 0)
			tc.end(t, w, id)
			w.pass()
			chosen := recordedVerification(t, w, integrateBatch)
			if !strings.HasPrefix(chosen.Fallback, tc.why) || chosen.Skips("lint") || chosen.Skips("check") {
				t.Errorf("recorded = %+v, want a fallback that starts %q and runs every gate", chosen, tc.why)
			}
			if !checkStarted(w, workID(t, w.s, integrateBatch, "done 1")) {
				t.Errorf("checks = %+v, want lint started under done 1", w.fakes.checks.calls)
			}
		})
	}
	t.Run("a start refused for good", func(t *testing.T) {
		t.Parallel()
		w := newIntegrateWorld(t, gatedBy(verifyGates(), "lint", "check"))
		w.runner.getenv = verifyRoles(t, map[string]string{roleVerify: verifyAgent})
		w.fakes.steps.refuse = enginehost.ErrBadAgent
		w.toGate()
		chosen := recordedVerification(t, w, integrateBatch)
		if !strings.HasPrefix(chosen.Fallback, "the verification step did not start: ") {
			t.Errorf("fallback = %q, want it to say the step did not start", chosen.Fallback)
		}
		if !checkStarted(w, workID(t, w.s, integrateBatch, "done 1")) {
			t.Errorf("checks = %+v, want lint started in the pass that recorded the fallback", w.fakes.checks.calls)
		}
	})
}

// TestVerifyRoundStartsAtItsMergeWithItsResolution: a task integrator round that merged starts its own verification
// step at its merged commit before its Done gates. Its change runs from the candidate the round merged into, its
// resolution names the conflicted path, the integrator's account and each change outside the conflicts, and its tasks
// are the round's task and the one composed before it.
func TestVerifyRoundStartsAtItsMergeWithItsResolution(t *testing.T) {
	t.Parallel()
	w := newRoundWorld(t)
	w.fakes.steps.verified = ""
	candidate := w.candidate(integrateBatch)
	start := w.roundStart(1)
	head := w.mergeRound(start, "both\n")
	endTaskStep(t, w.s, w.roundID(1), "done", "done", taskReturn(verdictMerged, head, "b.go keeps both lines",
		map[string]string{"path": "c.go", "reason": "T2's caller moved"}))
	w.pass()

	verify := verifyStart(t, w.integrateWorld, kinds.VerifyID(runAt(t, w.s, integrateBatch), 2, 1))
	if verify.Revision != head || !verify.ReadOnly || verify.Task != integrateBatch {
		t.Errorf("start = %+v, want a read-only step at the round's merge %s labeled %s", verify, head, integrateBatch)
	}
	if slices.ContainsFunc(w.fakes.checks.calls, func(check store.NodeCheck) bool { return check.Revision == head }) {
		t.Errorf("checks = %+v, want no Done gate at the merge before its choice", w.fakes.checks.calls)
	}
	for _, want := range []string{
		"Commit: " + head + ", T2's round 1 merge in " + integrateBatch + "\nIts change runs from: " + candidate + "\n",
		"\n## Conflict resolutions\n- T2 round 1, conflicted at: b.go\n  account: b.go keeps both lines\n" +
			"  changed c.go outside the conflicts: T2's caller moved\n",
		"\n## Tasks\n- T2 second (the task this round merges)\n",
		"\n- T1 first\n",
		"\n## Changed paths\nM\tb.go\n",
	} {
		if !strings.Contains(verify.Prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, verify.Prompt)
		}
	}
}

// TestWithVerificationKeepsTheBatch: recording a batch's verification and then a round's keeps every field the batch
// result held, and composedRound keeps both when it moves the round's task to the composed tasks.
func TestWithVerificationKeepsTheBatch(t *testing.T) {
	t.Parallel()
	result := `{"base":"b","candidate":"c","composed":[{"task":1,"commit":"c1"}],` +
		`"leftOut":[{"task":2,"reason":"conflict","conflicts":["b.go"]}],"migrations":{"dir":"m"}}`
	batch := kinds.Verification{Step: "v", Gates: []kinds.GateChoice{{Gate: "lint", Reason: "x"}}}
	round := kinds.Verification{Step: "r", Fallback: "it ended failed"}
	withBatch, err := withVerification(result, verifyTarget{}, batch)
	if err != nil {
		t.Fatalf("withVerification batch: %v", err)
	}
	withRound, err := withVerification(withBatch, verifyTarget{seq: 2, round: 1}, round)
	if err != nil {
		t.Fatalf("withVerification round: %v", err)
	}
	composed, err := composedRound(withRound, 2, "m1")
	if err != nil {
		t.Fatalf("composedRound: %v", err)
	}
	var got batchResult
	if err = json.Unmarshal([]byte(composed), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Base != "b" || got.Migrations == nil || got.Migrations.Dir != "m" || len(got.Composed) != 2 ||
		len(got.LeftOut) != 0 {
		t.Errorf("result = %+v, want the batch kept with T2 composed", got)
	}
	if got.Verification == nil || got.Verification.Step != "v" || !got.Verification.Skips("lint") {
		t.Errorf("verification = %+v, want the batch's kept", got.Verification)
	}
	if want := []kinds.RoundVerification{{Task: 2, Round: 1, Verification: round}}; len(got.RoundVerifications) != 1 ||
		got.RoundVerifications[0].Task != want[0].Task || got.RoundVerifications[0].Fallback != round.Fallback {
		t.Errorf("round verifications = %+v, want %+v", got.RoundVerifications, want)
	}
}

// TestVerifySchemaIsAStepSchema: the verification schema is a JSON object schema whose status is a string, as the step
// rules require, and requires every entry's reason.
func TestVerifySchemaIsAStepSchema(t *testing.T) {
	t.Parallel()
	var schema struct {
		Type       string `json:"type"`
		Required   []string
		Properties map[string]struct {
			Type  string `json:"type"`
			Items struct {
				Required []string `json:"required"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(VerifySchema), &schema); err != nil {
		t.Fatalf("decode VerifySchema: %v", err)
	}
	if schema.Type != "object" || schema.Properties["status"].Type != "string" {
		t.Errorf("schema = %+v, want an object with a string status", schema)
	}
	for _, list := range []string{"gates", "requirements"} {
		if !slices.Contains(schema.Properties[list].Items.Required, "reason") {
			t.Errorf("%s entries require %v, want reason among them", list, schema.Properties[list].Items.Required)
		}
	}
}
