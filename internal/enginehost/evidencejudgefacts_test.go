package enginehost

import (
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// factsTestGoLog is go test -v output in which one test passed and one failed.
const factsTestGoLog = "=== RUN   TestKept\n--- PASS: TestKept (0.00s)\n=== RUN   TestBroken\n" +
	"--- FAIL: TestBroken (0.00s)\nFAIL\nFAIL\texample.com/p\t0.01s\n"

// factsTestNothingLog is go test -v output whose -run pattern matched no test.
const factsTestNothingLog = "testing: warning: no tests to run\nPASS\nok  \texample.com/p\t0.01s [no tests to run]\n"

// factsTestReport is a JUnit report with one skipped case.
const factsTestReport = `<testsuite><testcase name="waits" classname="io.UsageTest"><skipped/></testcase></testsuite>`

// factsTestEvidence returns an Evidence over a scratch state directory, whose blobs the facts tests store their logs in.
func factsTestEvidence(t *testing.T) *Evidence {
	t.Helper()
	return NewEvidence(EvidenceConfig{StateDir: t.TempDir()})
}

// factsTestRun returns a green captured run whose stdout log is stdout and stderr log is stderr, each kept in ev's blobs,
// with no artifact.
func factsTestRun(t *testing.T, ev *Evidence, stdout, stderr string) store.EvidenceRun {
	t.Helper()
	return store.EvidenceRun{
		ID: "facts-run", Phase: evidence.PhaseGreen, Source: evidence.SourceCaptured,
		LogHash: evidenceLogsBlob(t, ev, stdout, ".log"), StderrHash: evidenceLogsBlob(t, ev, stderr, ".log"),
	}
}

// TestJudgeFactsLinesReadLogsAndJUnitArtifacts reads a run whose stdout log shows go tests and whose kept XML artifact is a
// JUnit report. The lines open with the heading that says the facts are orrery's reading the judge may overrule, then name
// what each source shows, indented.
func TestJudgeFactsLinesReadLogsAndJUnitArtifacts(t *testing.T) {
	t.Parallel()
	ev := factsTestEvidence(t)
	run := factsTestRun(t, ev, factsTestGoLog, "")
	hash, blob, err := ev.blobs.put([]byte(factsTestReport), ".xml")
	if err != nil {
		t.Fatalf("store report: %v", err)
	}
	run.Artifacts = []store.EvidenceArtifact{
		{Path: "build/TEST-io.UsageTest.xml", State: judgeStateKept, Hash: hash, Blob: blob},
		{Path: "build/other.xml", State: judgeStateMissing},
	}

	got := ev.judgeFactsLines(run)
	want := []string{
		judgeFactsHeading,
		"  Formats recognized: go test, JUnit XML",
		"  Tests seen: 3",
		"  Failed (1): TestBroken",
		"  Skipped (1): io.UsageTest > waits",
		"  Passed (1): TestKept",
		"  Go packages: 1 failed (example.com/p)",
	}
	if !slices.Equal(got, want) {
		t.Errorf("judgeFactsLines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, phrase := range []string{"orrery's own reading", "overrule", "The verdict is yours"} {
		if !strings.Contains(judgeFactsHeading, phrase) {
			t.Errorf("heading %q lacks %q", judgeFactsHeading, phrase)
		}
	}
}

// TestJudgeFactsLinesSayNoTestRan reads a run whose go test pattern matched nothing: the lines say no test ran.
func TestJudgeFactsLinesSayNoTestRan(t *testing.T) {
	t.Parallel()
	ev := factsTestEvidence(t)
	got := ev.judgeFactsLines(factsTestRun(t, ev, factsTestNothingLog, "devenv noise\n"))
	if !slices.Contains(got, "  No test ran: the output says no test ran, and shows none running.") {
		t.Errorf("judgeFactsLines = %q, want the no-test-ran line", got)
	}
}

// TestJudgeFactsLinesNoteACutLogAndAnUnreadableOne reads a run whose stdout log filled the capture's tail and whose stderr
// log is not kept. The lines note that tests before the cut are not counted, and that the stderr log could not be read.
func TestJudgeFactsLinesNoteACutLogAndAnUnreadableOne(t *testing.T) {
	t.Parallel()
	ev := factsTestEvidence(t)
	ev.captures.logTail = len(factsTestGoLog)
	run := factsTestRun(t, ev, factsTestGoLog, "")
	run.StderrHash = judgeTestHash("d")

	got := strings.Join(ev.judgeFactsLines(run), "\n")
	for _, want := range []string{
		"  The stdout log was cut at its start, so tests before the cut are not counted.",
		"  The stderr log could not be read for these facts: ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("judgeFactsLines lacks %q:\n%s", want, got)
		}
	}
}

// TestJudgeFactsLinesNoneForARunWithNoLog reads a run with no log and no XML artifact, which gives no lines.
func TestJudgeFactsLinesNoneForARunWithNoLog(t *testing.T) {
	t.Parallel()
	ev := factsTestEvidence(t)
	run := store.EvidenceRun{ID: "facts-run", Artifacts: []store.EvidenceArtifact{
		{Path: "out/report.txt", State: judgeStateKept, Blob: judgeTestHash("e") + ".txt"},
	}}
	if got := ev.judgeFactsLines(run); got != nil {
		t.Errorf("judgeFactsLines = %q, want none", got)
	}
}
