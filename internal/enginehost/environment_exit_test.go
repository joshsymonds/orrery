package enginehost

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// environmentExitShell returns a capture item that runs sh -c script, for 60 seconds.
func environmentExitShell(script string) evidence.Item {
	return evidence.Item{Capture: []string{"/bin/sh", "-c", script}, Limit: 60}
}

// environmentExitLines returns the lines a gate script appended to path, or none when it wrote nothing.
func environmentExitLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// TestEvidenceCaptureEnvironmentExitIsNotCaptured runs a capture that exits store.EnvironmentExit. It keeps the exit, and
// records the capture as not captured, so nothing it left is judged.
func TestEvidenceCaptureEnvironmentExitIsNotCaptured(t *testing.T) {
	t.Parallel()
	repo, head := evidenceCaptureRepo(t)
	c := evidenceCaptureFor(t, &fakeGatePools{})
	got, err := c.run(t.Context(), repo, head, "env", environmentExitShell("exit 75"), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.Exit != store.EnvironmentExit || got.TimedOut || got.NotCaptured != store.NotCapturedEnvironment {
		t.Fatalf("capture = %+v, want exit %d not captured as %q", got, store.EnvironmentExit,
			store.NotCapturedEnvironment)
	}
}

// TestEvidenceCaptureEnvironmentExitOneIsCaptured runs a capture that exits 1. That exit is judged as it always was, so the
// capture is captured.
func TestEvidenceCaptureEnvironmentExitOneIsCaptured(t *testing.T) {
	t.Parallel()
	repo, head := evidenceCaptureRepo(t)
	c := evidenceCaptureFor(t, &fakeGatePools{})
	got, err := c.run(t.Context(), repo, head, "fails", environmentExitShell("exit 1"), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.Exit != 1 || got.NotCaptured != "" {
		t.Fatalf("capture = %+v, want exit 1 captured", got)
	}
}

// TestEvidenceRunEnvironmentExitIsNotJudged runs an item that exits store.EnvironmentExit. The run ends not captured with
// its exit, and no judge step starts.
func TestEvidenceRunEnvironmentExitIsNotJudged(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	item := evidence.Item{
		Capture: []string{"/bin/sh", "-c", "exit 75"}, Limit: 60,
		Conclusion: "the build passes", Judge: "reviewer",
	}
	evidenceRunSeed(t, st, repo, item, item)
	steps := &evidenceRunSteps{}
	ev := evidenceRunNew(t, st, steps)

	_, got := evidenceRunOnce(t, ev, st, head, evidence.PhaseGreen)
	if got.Exit != store.EnvironmentExit || got.NotCaptured != store.NotCapturedEnvironment ||
		got.Passed || got.Verdict != "" {
		t.Fatalf("run = exit %d not captured %q passed %t verdict %q, want exit %d not captured and no verdict",
			got.Exit, got.NotCaptured, got.Passed, got.Verdict, store.EnvironmentExit)
	}
	if n := steps.startCount(); n != 0 {
		t.Fatalf("%d judge steps started, want none for a capture that was not captured", n)
	}
}

// TestStepsGateEnvironmentExitReruns runs a task gate that exits store.EnvironmentExit on its first run and passes on its
// second. The rerun passes the gate, so the step ends done, and the gate ran twice.
func TestStepsGateEnvironmentExitReruns(t *testing.T) {
	t.Parallel()
	count := filepath.Join(t.TempDir(), "count")
	script := fmt.Sprintf(
		"n=$(cat %[1]s 2>/dev/null || echo 0); n=$((n + 1)); echo $n > %[1]s; if [ $n -lt 2 ]; then exit 75; fi; exit 0",
		count,
	)
	f, sessions, id := stepCommandGateSubmit(t, script)

	stepStartAwait(t, engineOf(f.steps), "step end", func() bool {
		row, err := f.store.Step(t.Context(), id)
		return err == nil && row.End != ""
	})
	row, err := f.store.Step(t.Context(), id)
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if row.End != stepLoopEndDone || row.Status != stepLoopDone {
		t.Fatalf("end = %q status %q, want done DONE", row.End, row.Status)
	}
	if gate := stepDoneCheck(t, row.Checks, stepLoopGateName); !gate.OK {
		t.Errorf("gate check = %+v, want passing", gate)
	}
	if got := environmentExitLines(t, count); len(got) != 1 || got[0] != "2" {
		t.Errorf("gate runs = %q, want the gate run twice", got)
	}
	if n := sessions.continuedCount(); n != 0 {
		t.Errorf("%d messages sent back, want none for a gate whose rerun passed", n)
	}
}

// TestStepsGateEnvironmentExitThirdRunFailsStep runs a task gate that exits store.EnvironmentExit every time. It runs three
// times, then the step ends failed with a reason that names the gate and says the result is the environment's, and no
// message goes back to the session.
func TestStepsGateEnvironmentExitThirdRunFailsStep(t *testing.T) {
	t.Parallel()
	runs := filepath.Join(t.TempDir(), "runs")
	script := fmt.Sprintf("echo run >> %s; exit %d", runs, store.EnvironmentExit)
	f, sessions, id := stepCommandGateSubmit(t, script)

	stepStartAwait(t, engineOf(f.steps), "step end", func() bool {
		row, err := f.store.Step(t.Context(), id)
		return err == nil && row.End != ""
	})
	row, err := f.store.Step(t.Context(), id)
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if row.End != stepLoopEndFailed {
		t.Fatalf("end = %q, want %q", row.End, stepLoopEndFailed)
	}
	if !strings.Contains(row.Reason, "gate unit") || !strings.Contains(row.Reason, "environment") {
		t.Errorf("reason %q does not name gate unit and the environment", row.Reason)
	}
	if got := environmentExitLines(t, runs); len(got) != 3 {
		t.Errorf("gate runs = %d, want 3", len(got))
	}
	if n := sessions.continuedCount(); n != 0 {
		t.Errorf("%d messages sent back, want none once the gate exits store.EnvironmentExit three times", n)
	}
}

// TestStepsGateEnvironmentExitOneIsSentBack runs a task gate that exits 1. It runs once, and its return goes back to the
// session at once, as a failed gate always did.
func TestStepsGateEnvironmentExitOneIsSentBack(t *testing.T) {
	t.Parallel()
	runs := filepath.Join(t.TempDir(), "runs")
	f, sessions, id := stepCommandGateSubmit(t, fmt.Sprintf("echo run >> %s; exit 1", runs))

	stepStartAwait(t, engineOf(f.steps), "return sent back", func() bool { return sessions.continuedCount() == 1 })
	row, err := f.store.Step(t.Context(), id)
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if row.End != "" {
		t.Fatalf("end = %q, want the step open after a failed gate", row.End)
	}
	if got := environmentExitLines(t, runs); len(got) != 1 {
		t.Errorf("gate runs = %d, want 1", len(got))
	}
}

// TestStepsGateEnvironmentExitRestoredAttempt checks that a restored gate names the attempt its holder session is, so a
// restart continues the reruns where they stopped, and that a holder of another gate is refused.
func TestStepsGateEnvironmentExitRestoredAttempt(t *testing.T) {
	t.Parallel()
	r := &stepRun{stepRunConfig: stepRunConfig{id: "step-1"}}
	for attempt := 0; attempt <= store.EnvironmentReruns; attempt++ {
		r.resumeGate = store.StepGate{Holder: filepath.Join("holders", stepGateSession("step-1", attempt)), Round: 1}
		got, err := r.gateAttemptOf(1)
		if err != nil || got != attempt {
			t.Fatalf("gateAttemptOf for attempt %d = %d, %v, want %d", attempt, got, err, attempt)
		}
	}
	r.resumeGate = store.StepGate{Holder: filepath.Join("holders", "other-gate"), Round: 1}
	if _, err := r.gateAttemptOf(1); err == nil {
		t.Errorf("gateAttemptOf for another gate's holder = nil error, want a refusal")
	}
	r.resumeGate = store.StepGate{Holder: filepath.Join("holders", stepGateSession("step-1", 2)), Round: 2}
	if got, err := r.gateAttemptOf(1); err != nil || got != 0 {
		t.Errorf("gateAttemptOf for a gate of another round = %d, %v, want 0 and no error", got, err)
	}
}
