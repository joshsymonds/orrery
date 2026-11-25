package interpret

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// refusedTriage is the line a failed gate's output starts with under a runner whose environment names no role file:
// its triage could not start, so it was recorded a change and the gate failed as it would with no triage.
const refusedTriage = "triage: change: the triage did not start: neither XDG_CONFIG_HOME nor HOME is set"

// triageAgent is the agent a role file's triage role names in the tests that set one.
const triageAgent = "triage-agent"

// loadedStderr is the stderr of a gate that ran out its test budget on a loaded host, as scripts/gate-remote.sh and
// the recipe it runs print it.
const loadedStderr = "gate-remote: running just check here (no box; load 139.20 121.48 131.33, nproc 32)\n" +
	"tests exceeded 360s budget\nerror: recipe `cover` failed with exit code 124\n" +
	"gate-remote: just check exited 124 after 647s here\n"

// loadLine is the line of loadedStderr an environment triage quotes.
const loadLine = "gate-remote: running just check here (no box; load 139.20 121.48 131.33, nproc 32)"

// triageAt returns the start of the triage step of the gate check that failed at revision, which must have started. The
// latest such start is returned, since a rerun at the same revision starts a triage of its own.
func (w *integrateWorld) triageAt(revision string) enginehost.StepStart {
	w.t.Helper()
	return triageStartAt(w.t, w.fakes.steps, revision)
}

// answerTriage ends the triage step of the gate check that failed at revision as its judge would, done with verdict,
// evidence and citation, and passes once.
func (w *integrateWorld) answerTriage(revision, verdict, evidence, citation string) {
	w.t.Helper()
	endTriage(w.t, w.s, w.triageAt(revision).ID, verdict, evidence, citation)
	w.pass()
}

// triageStartAt returns the latest start among steps' calls of a triage step at revision, which must have started.
func triageStartAt(t *testing.T, steps *fakeSteps, revision string) enginehost.StepStart {
	t.Helper()
	var found enginehost.StepStart
	for _, start := range steps.calls {
		if start.Schema == TriageSchema && start.Revision == revision {
			found = start
		}
	}
	if found.ID == "" {
		t.Fatalf("no triage started at %s among %+v", revision, steps.calls)
	}
	return found
}

// triageStarts counts the starts among steps' calls of triage steps, refused ones included.
func triageStarts(steps *fakeSteps) int {
	n := 0
	for _, start := range steps.calls {
		if start.Schema == TriageSchema {
			n++
		}
	}
	return n
}

// endTriage ends the triage step with id as its judge would: done, with verdict, evidence and citation.
func endTriage(t *testing.T, s *store.Store, id, verdict, evidence, citation string) {
	t.Helper()
	endTaskStep(t, s, id, "done", "done", map[string]any{
		"status": "done", "verdict": verdict, "evidence": evidence, "citation": citation, "reason": "judged so",
	})
}

// endCheckWith ends the running check with id with end, as its holder would.
func endCheckWith(t *testing.T, s *store.Store, id string, end store.NodeCheckEnd) {
	t.Helper()
	if err := s.EndNodeCheck(context.Background(), id, end, runClock()); err != nil {
		t.Fatalf("EndNodeCheck %s: %v", id, err)
	}
}

// roleFile writes a role file holding text, and returns the environment a runner reads it under.
func roleFile(t *testing.T, text string) func(string) string {
	t.Helper()
	config := t.TempDir()
	dir := filepath.Join(config, "orrery")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("make role directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "roles.toml"), []byte(text), 0o600); err != nil {
		t.Fatalf("write role file: %v", err)
	}
	return func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return config
		}
		return ""
	}
}

// triageOf returns the triage stored for the check with id of the test epic, which must be stored.
func triageOf(t *testing.T, s *store.Store, id string) store.GateTriage {
	t.Helper()
	triages, err := s.GateTriages(context.Background(), runEpic)
	if err != nil {
		t.Fatalf("GateTriages: %v", err)
	}
	i := slices.IndexFunc(triages, func(triage store.GateTriage) bool { return triage.Check == id })
	if i < 0 {
		t.Fatalf("no triage of check %s among %+v", id, triages)
	}
	return triages[i]
}

// triageWorld returns an integrate world whose role file names a judge and no triage role, run up to its first batch's
// Done gate, with the candidate the batch was claimed at.
func triageWorld(t *testing.T) (*integrateWorld, string) {
	t.Helper()
	w := newIntegrateWorld(t)
	w.runner.getenv = roleFile(t, fmt.Sprintf("judge = %q\n", judgeAgent))
	candidate := w.toGate()
	return w, candidate
}

// TestTriageEnvironmentRerunsTheDoneGate: a Done gate that ran out its test budget on a loaded host starts one
// read-only triage step at the candidate, under the judge agent when the role file names no triage role, labeled with
// the batch and checked by its return check. Its prompt states the facts of the failure. An environment verdict that
// quotes the load line is recorded and reruns the gate at the candidate, and the rerun that passes lets the batch
// advance.
func TestTriageEnvironmentRerunsTheDoneGate(t *testing.T) {
	t.Parallel()
	w, candidate := triageWorld(t)
	gate := workID(t, w.s, integrateBatch, "done 1")
	endCheckWith(t, w.s, gate, store.NodeCheckEnd{Exit: 124, Stdout: "ok  \tgithub.com/x/y\t257.075s\n",
		Stderr: loadedStderr})
	w.pass()

	start := w.triageAt(candidate)
	if start.ID != workID(t, w.s, integrateBatch, "triage done 1") || start.Agent != judgeAgent || !start.ReadOnly ||
		start.Workspace != w.repo || start.Epic != runEpic || start.Task != integrateBatch ||
		start.ReturnCheck == nil {
		t.Errorf("start = %+v, want a read-only judge step at %s labeled %s with a return check", start, candidate,
			integrateBatch)
	}
	for _, want := range []string{
		"The Done gate lint of integrate run " + integrateBatch + ", at commit " + candidate + ".\n",
		"It ran: ./bin/lint\n",
		"It exited 124 without running past its limit.\n",
		"It is the first run of this gate at this commit.\n",
		"- " + loadLine + "\n",
		"- gate-remote: just check exited 124 after 647s here\n",
		"git diff --stat " + w.base + " " + candidate + ":\n",
		"- tests exceeded 360s budget\n",
		"No run of the same command at the base " + w.base + " is recorded.\n",
		"### stderr\n```\n" + strings.TrimRight(loadedStderr, "\n") + "\n```\n",
	} {
		if !strings.Contains(start.Prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, start.Prompt)
		}
	}
	paraphrase := []byte(`{"status":"done","verdict":"environment","evidence":"the load was high","citation":"",` +
		`"reason":"loaded"}`)
	if failures := start.ReturnCheck(paraphrase); len(failures) != 1 {
		t.Errorf("return check of a paraphrased load = %q, want one failure", failures)
	}

	w.answerTriage(candidate, kinds.TriageEnvironment, loadLine, "")
	if got := triageOf(t, w.s, gate); got.Verdict != kinds.TriageEnvironment || got.Evidence != loadLine ||
		got.Step != start.ID || got.Path != integrateBatch {
		t.Errorf("triage = %+v, want environment quoting the load line", got)
	}
	rerun := workID(t, w.s, integrateBatch, "done 1 rerun")
	check, err := w.s.NodeCheck(context.Background(), rerun)
	if err != nil || check.Revision != candidate {
		t.Fatalf("rerun = %+v, %v; want the Done gate rerun at the candidate %s", check, err, candidate)
	}
	nodeEndCheck(t, w.s, rerun, 0, "")
	w.pass()
	w.endCapture(integrateBatch)
	w.pass()
	if tip := w.git("rev-parse", integrateBranch); tip != candidate {
		t.Errorf("branch = %s, want it advanced to the candidate %s", tip, candidate)
	}
	if n := triageStarts(w.fakes.steps); n != 1 {
		t.Errorf("triage starts = %d, want one", n)
	}
}

// TestTriageChangeFailsTheDoneGateWithItsCitation: a change verdict that cites the failing test fails the batch as a
// failed gate always did, its output carrying the citation, and starts no rerun.
func TestTriageChangeFailsTheDoneGateWithItsCitation(t *testing.T) {
	t.Parallel()
	w, candidate := triageWorld(t)
	gate := workID(t, w.s, integrateBatch, "done 1")
	endCheckWith(t, w.s, gate, store.NodeCheckEnd{Exit: 1,
		Stdout: "--- FAIL: TestX (0.10s)\n    b_test.go:12: boom\nFAIL\tgithub.com/x/b\t0.2s\n"})
	w.pass()
	prompt := w.triageAt(candidate).Prompt
	for _, want := range []string{
		"Failing tests:\n- --- FAIL: TestX (0.10s)\n",
		"Failing packages:\n- FAIL\tgithub.com/x/b\t0.2s\n",
		"- b_test.go:12 (the change does not touch it)\n",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}

	w.answerTriage(candidate, kinds.TriageChange, "", "--- FAIL: TestX (0.10s)")
	run := runAt(t, w.s, integrateBatch)
	want := `Done gate lint failed; triage: change, citing "--- FAIL: TestX (0.10s)": judged so`
	if run.State != store.NodeEnded || run.Outcome != outcomeFailed || run.Output != want {
		t.Errorf("batch = %+v, want ended failed with output %q", run, want)
	}
	if _, err := w.s.NodeCheck(context.Background(), workID(t, w.s, integrateBatch, "done 1 rerun")); err == nil {
		t.Error("a change verdict started a rerun of the Done gate")
	}
}

// TestTriageEnvironmentWithoutItsEvidenceCountsAsAChange: an environment verdict whose evidence the gate's output does
// not hold is recorded a change with why, and fails the batch with no rerun.
func TestTriageEnvironmentWithoutItsEvidenceCountsAsAChange(t *testing.T) {
	t.Parallel()
	w, candidate := triageWorld(t)
	gate := workID(t, w.s, integrateBatch, "done 1")
	endCheckWith(t, w.s, gate, store.NodeCheckEnd{Exit: 124, Stderr: loadedStderr})
	w.pass()
	w.answerTriage(candidate, kinds.TriageEnvironment, "the host was very loaded", "")

	if got := triageOf(t, w.s, gate); got.Verdict != kinds.TriageChange ||
		!strings.Contains(got.Reason, `"the host was very loaded" does not appear in the gate's output`) {
		t.Errorf("triage = %+v, want a change naming the evidence it could not find", got)
	}
	if run := runAt(t, w.s, integrateBatch); run.State != store.NodeEnded || run.Outcome != outcomeFailed {
		t.Errorf("batch = %+v, want ended failed", run)
	}
}

// TestTriageRefusedStartRecordsAChangeOnce: a triage whose start is refused for good is recorded a change with the
// refusal at once, so the gate fails in the same pass and its start is not tried again.
func TestTriageRefusedStartRecordsAChangeOnce(t *testing.T) {
	t.Parallel()
	w, _ := triageWorld(t)
	w.fakes.steps.refuse = enginehost.ErrBadAgent
	gate := workID(t, w.s, integrateBatch, "done 1")
	nodeEndCheck(t, w.s, gate, 1, "lint failed")
	w.pass()
	w.pass()

	if got := triageOf(t, w.s, gate); got.Verdict != kinds.TriageChange ||
		!strings.HasPrefix(got.Reason, "the triage did not start: ") {
		t.Errorf("triage = %+v, want a change with the refusal", got)
	}
	if n := triageStarts(w.fakes.steps); n != 1 {
		t.Errorf("triage starts = %d, want one: a refusal is recorded, not retried", n)
	}
	if run := runAt(t, w.s, integrateBatch); run.State != store.NodeEnded || run.Outcome != outcomeFailed {
		t.Errorf("batch = %+v, want ended failed", run)
	}
}

// TestTriageRunsUnderTheTriageRole: a role file that names a triage role runs the triage under it, not under the
// judge.
func TestTriageRunsUnderTheTriageRole(t *testing.T) {
	t.Parallel()
	w := newIntegrateWorld(t)
	w.runner.getenv = roleFile(t, fmt.Sprintf("judge = %q\ntriage = %q\n", judgeAgent, triageAgent))
	candidate := w.toGate()
	nodeEndCheck(t, w.s, workID(t, w.s, integrateBatch, "done 1"), 1, "lint failed")
	w.pass()
	if agent := w.triageAt(candidate).Agent; agent != triageAgent {
		t.Errorf("triage agent = %q, want %q", agent, triageAgent)
	}
}

// TestTriageFlakeRerunsAGateNode: the check of a top-level gate node that fails starts a triage labeled with the node,
// whose step the node's rows read back. A flake verdict reruns the check, and the rerun that passes passes the node.
func TestTriageFlakeRerunsAGateNode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, base := nodeRepo(t)
	s, _ := openNodeStore(t)
	createNodeEpic(t, s, repo, base, nodeLintFlow)
	fakes := newNodeFakes(s)
	cfg := nodeConfig(s, fakes)
	cfg.Getenv = roleFile(t, fmt.Sprintf("judge = %q\n", judgeAgent))
	runner := New(cfg)
	runner.Pass(ctx)
	check := workID(t, s, "lint", "check")
	endCheckWith(t, s, check, store.NodeCheckEnd{Exit: 1, Stdout: "--- FAIL: TestRace (0.01s)\n"})
	runner.Pass(ctx)

	start := triageStartAt(t, fakes.steps, base)
	if start.Task != "lint" || start.ID != workID(t, s, "lint", "triage check") {
		t.Errorf("start = %+v, want the triage of check labeled lint", start)
	}
	runner.Pass(ctx)
	if n := triageStarts(fakes.steps); n != 1 {
		t.Fatalf("triage starts = %d, want one: a running triage waits", n)
	}
	endTriage(t, s, start.ID, kinds.TriageFlake, "--- FAIL: TestRace (0.01s)", "")
	runner.Pass(ctx)
	rerun := workID(t, s, "lint", "check rerun")
	nodeEndCheck(t, s, rerun, 0, "")
	runner.Pass(ctx)
	if run := runAt(t, s, "lint"); run.State != store.NodeEnded || run.Outcome != outcomePassed {
		t.Errorf("node lint = %+v, want ended passed on its rerun", run)
	}
}

// TestTriagePromptStatesTheEarlierRunsAndTheBase: the prompt names the runs of the gate before the triaged one with
// what their triages found, the same command's last run at the base with its last lines, and whether the change touches
// each file the failure names.
func TestTriagePromptStatesTheEarlierRunsAndTheBase(t *testing.T) {
	t.Parallel()
	created := time.Unix(1_700_000_000, 0)
	facts := triageFacts{
		nodeKind: kindGate, gate: "check", path: "lint", host: "vermissian", base: "base-1",
		check: store.NodeCheck{
			ID: "c2", Revision: "rev-1", Argv: []string{"just", "check"}, Limit: 1800, Exit: -1, TimedOut: true,
			Stderr: "internal/a/x_test.go:9: hang\n", Created: created, Ended: created.Add(1800 * time.Second),
		},
		earlier: []triageEarlier{{
			check:   store.NodeCheck{ID: "c1", Exit: 4},
			triage:  store.GateTriage{Verdict: kinds.TriageEnvironment, Evidence: "load 300", Reason: "loaded"},
			triaged: true,
		}},
		diffStat: " internal/a/x_test.go | 2 +-\n 1 file changed",
		changed:  []string{"internal/a/x_test.go"},
		atBase:   store.NodeCheck{ID: "c0", Exit: 0, Stdout: "ok\nall passed\n"},
		hasBase:  true,
	}
	prompt := triagePrompt(facts)
	for _, want := range []string{
		"The gate check of gate run lint, at commit rev-1.\n",
		"Its limit is 1800s.\n",
		"It ran past its limit and was killed (exit -1).\n",
		"It ended 1800s after it was queued, its wait for pool slots included.\n",
		"It is run 2 of this gate at this commit. The runs before it:\n" +
			`- run 1 exited 4 without running past its limit, and its triage found environment, quoting "load 300": ` +
			"loaded.\n",
		"The epic lives on host vermissian, as the store names it.\n",
		"Neither the output tails nor the key lines hold a gate-remote line",
		"- internal/a/x_test.go:9 (the change touches this file)\n",
		"The latest run of the same command at the base base-1 exited 0 without running past its limit.\n",
		"The last lines of its stdout:\n```\nok\nall passed\n```\n",
		"### stdout\n(empty)\n",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
}

// TestBaseRunIsTheLatestSameCommandAtTheBase: the base run is the latest ended check that ran the same argv at the base,
// never the triaged check itself, one that is running, one that did not run, or one of another command.
func TestBaseRunIsTheLatestSameCommandAtTheBase(t *testing.T) {
	t.Parallel()
	argv := []string{"just", "check"}
	triaged := store.NodeCheck{ID: "now", Revision: "base-1", Argv: argv, State: store.NodeEnded, Exit: 1}
	checks := []store.NodeCheck{
		{ID: "older", Revision: "base-1", Argv: argv, State: store.NodeEnded, Exit: 2},
		{ID: "latest", Revision: "base-1", Argv: argv, State: store.NodeEnded},
		{ID: "other", Revision: "base-1", Argv: []string{"just", "lint"}, State: store.NodeEnded},
		{ID: "unrun", Revision: "base-1", Argv: argv, State: store.NodeEnded, NotRun: "no gate"},
		{ID: "running", Revision: "base-1", Argv: argv, State: store.NodeRunning},
		{ID: "elsewhere", Revision: "rev-2", Argv: argv, State: store.NodeEnded},
		triaged,
	}
	got, ok := baseRun(checks, triaged, "base-1")
	if !ok || got.ID != "latest" {
		t.Errorf("baseRun() = %+v, %v; want the check latest", got, ok)
	}
	if _, ok = baseRun(checks[2:3], triaged, "base-1"); ok {
		t.Error("baseRun() found a run of another command")
	}
}

// TestGateLoadStatesWhereAndHowLoaded: the gate-remote line that starts the command gives where the gate ran and the
// load there against the CPU count, whether it ran here with a reason or on a box. Other lines give nothing.
func TestGateLoadStatesWhereAndHowLoaded(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, line, want string }{
		{
			"here, with a reason that holds a semicolon",
			loadLine,
			"It ran on the epic's host itself, not on a gate box. As it started, the load average there was 139.20, " +
				"121.48 and 131.33 over 1, 5 and 15 minutes, on 32 CPUs: 4.35 times the CPU count over the last minute.",
		},
		{
			"on a box",
			"gate-remote: running just check on orrery@orrery-gate-khw (load 3.10 2.00 1.50, nproc 64)",
			"It ran on the gate box orrery@orrery-gate-khw. As it started, the load average there was 3.10, 2.00 and " +
				"1.50 over 1, 5 and 15 minutes, on 64 CPUs: 0.05 times the CPU count over the last minute.",
		},
		{"another gate-remote line", "gate-remote: just check exited 124 after 647s here", ""},
	}
	for _, tc := range cases {
		if got := gateLoad([]string{tc.line}); got != tc.want {
			t.Errorf("%s: gateLoad() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestCapStatKeepsItsSummary: a diff stat longer than the cap keeps its first lines, says how many it left out, and
// keeps its summary line.
func TestCapStatKeepsItsSummary(t *testing.T) {
	t.Parallel()
	lines := make([]string, 0, triageStatLines+6)
	for i := range triageStatLines + 5 {
		lines = append(lines, fmt.Sprintf(" f%d.go | 1 +", i))
	}
	lines = append(lines, " 65 files changed")
	got := strings.Split(capStat(strings.Join(lines, "\n")+"\n"), "\n")
	if len(got) != triageStatLines+2 || got[triageStatLines] != " ... 5 more files" ||
		got[len(got)-1] != " 65 files changed" {
		t.Errorf("capStat() = %q, want %d lines, then the count left out, then the summary", got, triageStatLines)
	}
}
