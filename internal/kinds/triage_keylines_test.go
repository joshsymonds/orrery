package kinds

import (
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// keyedLoadLine is the line of a gate's output that gives its load, which keyedOutput holds in its key lines alone.
const keyedLoadLine = "gate-remote: running just check here (no box; load 139.20 121.48 131.33, nproc 32)"

// keyedOutput is the output of a failed check whose tails were cut after its load line and its failing test: they hold
// only the end of a long run, and its key lines hold the lines that were cut off.
func keyedOutput() store.NodeCheck {
	return store.NodeCheck{
		ID: workID("check"), State: store.NodeEnded, Exit: 124,
		Stdout:   strings.Repeat("ok  \tgithub.com/x/filler\t0.01s\n", 3),
		Stderr:   "tests exceeded 360s budget\nerror: recipe `cover` failed with exit code 124\n",
		KeyLines: []string{keyedLoadLine, "--- FAIL: TestSlow (3.46s)", "tests exceeded 360s budget"},
	}
}

// TestTriageEvidenceInTheKeyLinesStands: a quote of a line that the check's key lines hold and its tails do not passes
// the return check and stands, an environment verdict's evidence and a change's citation alike, and a quote of a line
// that neither holds is still refused.
func TestTriageEvidenceInTheKeyLinesStands(t *testing.T) {
	t.Parallel()
	check := keyedOutput()
	both := []string{TriageEnvironment, TriageFlake}
	environment := map[string]string{"verdict": "environment", "evidence": keyedLoadLine, "reason": "loaded"}
	quoted := []byte(`{"verdict":"environment","evidence":"` + keyedLoadLine + `"}`)
	if failures := TriageFailures(check, quoted); failures != nil {
		t.Errorf("TriageFailures() of the load line = %q, want none", failures)
	}
	got := JudgeTriage(check, triageRow(t, "done", environment), both)
	if got.Verdict != TriageEnvironment || got.Evidence != keyedLoadLine {
		t.Errorf("JudgeTriage() of the load line = %+v, want environment quoting it", got)
	}

	change := map[string]string{"verdict": "change", "citation": "--- FAIL: TestSlow (3.46s)", "reason": "it hangs"}
	if got = JudgeTriage(check, triageRow(t, "done", change), both); got.Citation != "--- FAIL: TestSlow (3.46s)" {
		t.Errorf("JudgeTriage() of a citation in the key lines = %+v, want it kept", got)
	}

	missing := map[string]string{"verdict": "environment", "evidence": "load 300 on 4 CPUs", "reason": "loaded"}
	got = JudgeTriage(check, triageRow(t, "done", missing), both)
	if got.Verdict != TriageChange ||
		!strings.Contains(got.Reason, `"load 300 on 4 CPUs" does not appear in the gate's output`) {
		t.Errorf("JudgeTriage() of a line in neither = %+v, want a change naming the line", got)
	}
	unheld := []byte(`{"verdict":"environment","evidence":"load 300 on 4 CPUs"}`)
	if failures := TriageFailures(check, unheld); len(failures) != 1 {
		t.Errorf("TriageFailures() of a line in neither = %q, want one failure", failures)
	}
}
