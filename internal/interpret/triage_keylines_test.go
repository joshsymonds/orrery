package interpret

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/keylines"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// longFailingTest is the failing test a long gate output names at its start, long before its tails begin.
const longFailingTest = "--- FAIL: TestSlow (3.46s)"

// longGateEnd returns the end of a gate run that printed its load line and its failing test early and then much more,
// as its holder ends it: its whole stdout and stderr, which EndNodeCheck cuts to their newest store.NodeOutputMax
// bytes, and the key lines a keylines.Collector kept of the two streams as they came.
func longGateEnd(t *testing.T) store.NodeCheckEnd {
	t.Helper()
	filler := strings.Repeat("ok  \tgithub.com/x/filler\t0.01s\n", 300)
	stdout := longFailingTest + "\n    slow_test.go:40: still waiting\n" + filler + "FAIL\n"
	stderr := loadLine + "\n" + strings.Repeat("building the next package\n", 300) + "tests exceeded 360s budget\n" +
		"gate-remote: just check exited 124 after 647s here\n"
	var collector keylines.Collector
	for _, stream := range []string{stdout, stderr} {
		w := collector.Writer()
		if _, err := w.Write([]byte(stream)); err != nil {
			t.Fatalf("collect key lines: %v", err)
		}
		w.Flush()
	}
	return store.NodeCheckEnd{Exit: 124, Stdout: stdout, Stderr: stderr, KeyLines: collector.Lines()}
}

// longGateWorld returns a triage world whose batch's Done gate ended with longGateEnd, with the candidate and the
// gate's check id, once the pass has started the gate's triage. It checks that the stored tails lost the load line and
// the failing test, and that the stored key lines kept them.
func longGateWorld(t *testing.T) (*integrateWorld, string, string) {
	t.Helper()
	w, candidate := triageWorld(t)
	gate := workID(t, w.s, integrateBatch, "done 1")
	endCheckWith(t, w.s, gate, longGateEnd(t))
	check, err := w.s.NodeCheck(context.Background(), gate)
	if err != nil {
		t.Fatalf("NodeCheck %s: %v", gate, err)
	}
	if strings.Contains(check.Stderr, loadLine) || strings.Contains(check.Stdout, longFailingTest) {
		t.Fatalf("stored tails hold the early lines, want them cut off:\nstdout %q\nstderr %q", check.Stdout,
			check.Stderr)
	}
	for _, want := range []string{loadLine, longFailingTest} {
		if !slices.Contains(check.KeyLines, want) {
			t.Fatalf("stored key lines = %q, want them to hold %q", check.KeyLines, want)
		}
	}
	w.pass()
	return w, candidate, gate
}

// TestTriagePromptHoldsTheKeyLinesTheTailsCutOff: a gate whose tails lost its load line and its failing test to a long
// output still gives its triage both: the prompt lists the load line where the gate ran with the load against the CPU
// count read from it, names the failing test, and shows the key lines in their own section. The return check passes
// a quote of the load line, and refuses a paraphrase.
func TestTriagePromptHoldsTheKeyLinesTheTailsCutOff(t *testing.T) {
	t.Parallel()
	w, candidate, _ := longGateWorld(t)
	start := w.triageAt(candidate)
	for _, want := range []string{
		"- " + loadLine + "\n",
		"It ran on the epic's host itself, not on a gate box. As it started, the load average there was 139.20, " +
			"121.48 and 131.33 over 1, 5 and 15 minutes, on 32 CPUs: 4.35 times the CPU count over the last minute.",
		"Failing tests:\n- " + longFailingTest + "\n",
		"- slow_test.go:40 (the change does not touch it)\n",
	} {
		if !strings.Contains(start.Prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, start.Prompt)
		}
	}
	_, section, found := strings.Cut(start.Prompt, "## Key lines\n")
	section, _, _ = strings.Cut(section, "\n## ")
	for _, want := range []string{"\n" + loadLine + "\n", "\n" + longFailingTest + "\n"} {
		if !found || !strings.Contains(section, want) {
			t.Errorf("Key lines section lacks %q:\n%s", want, section)
		}
	}
	quote := triageReturn(t, kinds.TriageEnvironment, loadLine, "")
	if failures := start.ReturnCheck(quote); len(failures) != 0 {
		t.Errorf("return check of the load line = %q, want it to pass", failures)
	}
	paraphrase := triageReturn(t, kinds.TriageEnvironment, "the load was high", "")
	if failures := start.ReturnCheck(paraphrase); len(failures) != 1 {
		t.Errorf("return check of a paraphrase = %q, want one failure", failures)
	}
}

// TestTriageEnvironmentQuotingAKeyLineStands: an environment verdict whose evidence is the load line, which only the
// key lines hold, is recorded and reruns the Done gate.
func TestTriageEnvironmentQuotingAKeyLineStands(t *testing.T) {
	t.Parallel()
	w, candidate, gate := longGateWorld(t)
	w.answerTriage(candidate, kinds.TriageEnvironment, loadLine, "")
	if got := triageOf(t, w.s, gate); got.Verdict != kinds.TriageEnvironment || got.Evidence != loadLine {
		t.Errorf("triage = %+v, want environment quoting the load line", got)
	}
	rerun, err := w.s.NodeCheck(context.Background(), workID(t, w.s, integrateBatch, "done 1 rerun"))
	if err != nil || rerun.Revision != candidate {
		t.Errorf("rerun = %+v, %v; want the Done gate rerun at the candidate %s", rerun, err, candidate)
	}
}

// TestTriageEnvironmentQuotingALineInNeitherCountsAsAChange: an environment verdict whose evidence neither the tails
// nor the key lines hold is recorded a change with why, and fails the batch with no rerun.
func TestTriageEnvironmentQuotingALineInNeitherCountsAsAChange(t *testing.T) {
	t.Parallel()
	w, candidate, gate := longGateWorld(t)
	invented := "gate-remote: running just check here (no box; load 300.00 300.00 300.00, nproc 32)"
	w.answerTriage(candidate, kinds.TriageEnvironment, invented, "")
	if got := triageOf(t, w.s, gate); got.Verdict != kinds.TriageChange ||
		!strings.Contains(got.Reason, `"`+invented+`" does not appear in the gate's output`) {
		t.Errorf("triage = %+v, want a change naming the evidence it could not find", got)
	}
	if run := runAt(t, w.s, integrateBatch); run.State != store.NodeEnded || run.Outcome != outcomeFailed {
		t.Errorf("batch = %+v, want ended failed", run)
	}
}

// triageReturn returns the return a triage step gives, done with verdict, evidence and citation.
func triageReturn(t *testing.T, verdict, evidence, citation string) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(map[string]string{
		"status": "done", "verdict": verdict, "evidence": evidence, "citation": citation, "reason": "judged so",
	})
	if err != nil {
		t.Fatalf("encode triage return: %v", err)
	}
	return encoded
}
