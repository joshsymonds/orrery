package enginehost

import (
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// gathererTestJudgedRun is an earlier run of the gatherer tests' epic that a judge gave a verdict: its id, item, phase
// and commit, the argv its capture ran (none when it names no capture), its exit, verdict and judge reason, and when it
// was filed, in seconds after the tests' epoch.
type gathererTestJudgedRun struct {
	id, item, phase, commit string
	argv                    []string
	exit                    int
	verdict, reason         string
	created                 int64
}

// gathererTestAddJudged stores run in st, in the repository repo, as a run the judge has ruled on. A run with an argv
// has a capture that ran it, ended with the run's exit and attached to the run, and its brief names another argv, so
// the block shows which one the capture ran. A run with no argv is filed judging with no capture.
func gathererTestAddJudged(t *testing.T, st *store.Store, repo string, run gathererTestJudgedRun) {
	t.Helper()
	ctx := t.Context()
	created := time.Unix(1700000000+run.created, 0)
	state := store.RunWaiting
	if run.argv == nil {
		state = "judging"
	}
	if err := st.AddEvidenceRun(ctx, store.EvidenceRun{
		ID: run.id, Epic: gathererTestEpic, Item: run.item, Revision: run.commit, Phase: run.phase,
		Source: evidence.SourceCaptured, State: state,
		Spec: evidence.Item{
			Capture: []string{"/bin/sh", "-c", "brief " + run.id}, Limit: 60, Conclusion: "the marker says head",
		},
		Created: created,
	}); err != nil {
		t.Fatalf("AddEvidenceRun %s: %v", run.id, err)
	}
	if run.argv != nil {
		capture := "capture-" + run.id
		if err := st.AddCapture(ctx, store.Capture{
			ID: capture, Epic: gathererTestEpic, Repository: repo, Step: "step", Role: captureRoleStep,
			Runner: localRunnerName, Commit: run.commit, Argv: run.argv, Limit: 60, Created: created,
		}); err != nil {
			t.Fatalf("AddCapture %s: %v", capture, err)
		}
		if err := st.RecordCaptureEnd(ctx, capture, store.EvidenceCapture{Exit: run.exit, At: created}); err != nil {
			t.Fatalf("RecordCaptureEnd %s: %v", capture, err)
		}
		if _, err := st.AttachCapture(ctx, capture, []string{run.id}, created); err != nil {
			t.Fatalf("AttachCapture %s: %v", capture, err)
		}
	}
	if err := st.RecordEvidenceVerdict(ctx, run.id, store.EvidenceVerdict{
		Verdict: run.verdict, Reason: run.reason, Passed: run.verdict == "holds", At: created,
	}); err != nil {
		t.Fatalf("RecordEvidenceVerdict %s: %v", run.id, err)
	}
}

// gathererTestHistory stores, at commit in repo, four judged runs of R1 in turn, one of R2, and a run of R1 that ended
// with no verdict. The first of R1 is the oldest, so a block that lists the latest three leaves it out; the third has
// a judge reason of two lines; the fourth names no capture.
func gathererTestHistory(t *testing.T, st *store.Store, repo, commit string) {
	t.Helper()
	noTests := []string{"go", "test", "-run", "TestMissing", "./..."}
	for _, run := range []gathererTestJudgedRun{
		{id: "judged-1", item: "R1", phase: evidence.PhaseRed, argv: []string{"oldest"}, verdict: "fails",
			reason: "the oldest", created: 1},
		{id: "judged-2", item: "R1", phase: evidence.PhaseRed, argv: noTests, verdict: "fails",
			reason: "the run matched no test: every package printed [no tests to run]", created: 2},
		{id: "judged-other", item: "R2", phase: evidence.PhaseRed, argv: []string{"other"}, verdict: "fails",
			reason: "another item", created: 3},
		{id: "judged-3", item: "R1", phase: evidence.PhaseGreen, argv: noTests, exit: 1, verdict: "fails",
			reason: "no test matched TestMissing.\nThe log has no === RUN line.", created: 4},
		{id: "judged-4", item: "R1", phase: evidence.PhaseRed, verdict: "holds", reason: "supplied by hand",
			created: 5},
	} {
		run.commit = commit
		gathererTestAddJudged(t, st, repo, run)
	}
	if err := st.AddEvidenceRun(t.Context(), store.EvidenceRun{
		ID: "unjudged", Epic: gathererTestEpic, Item: "R1", Revision: commit, Phase: evidence.PhaseRed,
		Source: evidence.SourceCaptured, State: "ended", Created: time.Unix(1700000006, 0),
	}); err != nil {
		t.Fatalf("AddEvidenceRun unjudged: %v", err)
	}
}

// gathererTestHistoryLines are the lines a block of an R1 brief gives for the runs gathererTestHistory stores at
// commit: the latest three judged runs of R1, oldest first, with the argv each capture ran.
func gathererTestHistoryLines(commit string) string {
	return "  earlier judged runs of R1, oldest first:\n" +
		"    judged-2 at " + commit + ", phase red: argv [\"go\",\"test\",\"-run\",\"TestMissing\",\"./...\"], exit 0, " +
		"verdict fails: the run matched no test: every package printed [no tests to run]\n" +
		"    judged-3 at " + commit + ", phase green: argv [\"go\",\"test\",\"-run\",\"TestMissing\",\"./...\"], " +
		"exit 1, verdict fails: no test matched TestMissing.\n      The log has no === RUN line.\n" +
		"    judged-4 at " + commit + ", phase red: no capture, brief argv [\"/bin/sh\",\"-c\",\"brief judged-4\"], " +
		"exit 0, verdict holds: supplied by hand"
}

// TestGathererPromptListsTheEarlierJudgedRunsOfEachBriefsItem starts a gatherer for a brief of R1 over an epic that
// holds judged runs of R1 and R2 and a run of R1 with no verdict. The brief's block lists the latest three judged runs
// of R1, oldest first, each with its revision, phase, the argv its capture ran, its exit, verdict and judge reason, and
// leaves out the oldest, the run of R2 and the run with no verdict. The prompt states the rule for an earlier run whose
// command ran no test.
func TestGathererPromptListsTheEarlierJudgedRunsOfEachBriefsItem(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	st := epicTestStore(t)
	gathererTestEpicIn(t, st, repo, base)
	gathererTestHistory(t, st, repo, base)
	gathererTestAddBrief(t, st, "brief-1", base, []string{"/bin/sh", "-c", "cat marker"})
	steps := &gathererTestSteps{}
	g := gathererTestRunner(t, st, steps, &captureTestDeliveries{}, gathererTestRoles)

	g.Wake(t.Context(), gathererTestEpic)
	waittest.UntilIdle(t, g.idle, "the gatherer's step starts", func() bool { return len(steps.startedSteps()) == 1 })
	prompt := steps.startedSteps()[0].Prompt
	for _, want := range []string{
		gathererTestHistoryLines(base) + "\n\n",
		"  When an earlier judged run of a brief's item was judged not to show its conclusion because its command " +
			"ran no test, or not the tests the conclusion names, the same command at a commit with the same tests " +
			"shows the same. Change the command so it runs what the conclusion names: a capture may run any argv, " +
			"not only the brief's capture argv. When no command at the brief's commit can show it, leave the brief " +
			"without a capture, and say in the note that the item's evidence needs amending, naming the item and " +
			"the judge's reason.\n",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	for _, left := range []string{"    judged-1 at", "    judged-other at", "    unjudged at"} {
		if strings.Contains(prompt, left) {
			t.Errorf("prompt lists %q, which the brief's block leaves out:\n%s", left, prompt)
		}
	}
}

// TestGathererDeliversTheEarlierJudgedRunsWithABrief files a brief of R1 while the gatherer runs, over an epic that
// holds judged runs of R1. The block delivered for it lists the same earlier judged runs a prompt's block does.
func TestGathererDeliversTheEarlierJudgedRunsWithABrief(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	st := epicTestStore(t)
	gathererTestEpicIn(t, st, repo, base)
	gathererTestAddBrief(t, st, "brief-1", base, []string{"/bin/sh", "-c", "cat marker"})
	steps := &gathererTestSteps{}
	deliveries := &captureTestDeliveries{}
	g := gathererTestRunner(t, st, steps, deliveries, gathererTestRoles)

	g.Wake(t.Context(), gathererTestEpic)
	waittest.UntilIdle(t, g.idle, "the gatherer watches its step", func() bool { return steps.subscriptions() == 1 })
	gathererTestHistory(t, st, repo, base)
	gathererTestAddBrief(t, st, "brief-2", base, []string{"/bin/sh", "-c", "cat marker"})
	g.Wake(t.Context(), gathererTestEpic)
	waittest.UntilIdle(
		t,
		g.idle,
		"the brief reaches the gatherer",
		func() bool { return len(deliveries.requests()) == 1 },
	)
	g.Close()

	text := deliveries.requests()[0].Text
	if !strings.HasPrefix(text, "Brief brief-2:\n") || !strings.HasSuffix(text, "\n"+gathererTestHistoryLines(base)) {
		t.Fatalf("delivered block = %q, want brief-2's block ending in its earlier judged runs:\n%s",
			text, gathererTestHistoryLines(base))
	}
}
