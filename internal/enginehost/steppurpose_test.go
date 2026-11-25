package enginehost

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// stepPurposeReplyLine is a success result line whose purpose is stepPurposeAnswer.
const (
	stepPurposeAnswer    = "Review the diff of T3."
	stepPurposeReplyLine = `{"type":"result","subtype":"success","is_error":false,` +
		`"structured_output":{"purpose":"` + stepPurposeAnswer + `"}}`
)

// stepPurposePrompt is the prompt each purpose test starts its step with.
const stepPurposePrompt = "Read the diff against the brief and say whether T3 is done."

// stepPurposeStart returns a writing start of a repository with prompt stepPurposePrompt and purpose.
func stepPurposeStart(t *testing.T, purpose string) StepStart {
	t.Helper()
	start := stepStartWriting(stepWorktreeTestAddLinked(t, stepGitTestRepo(t)))
	start.Prompt = stepPurposePrompt
	start.Purpose = purpose
	return start
}

// stepPurposeFixture returns a fixture whose Steps asks a Haiku caller that runs the fake claude of fake for purposes.
func stepPurposeFixture(t *testing.T, fake haikuFake) (*stepWireFixture, string) {
	t.Helper()
	dir := haikuDir(t)
	h := NewHaiku(HaikuConfig{Claude: haikuWriteFake(t, dir, fake), Environ: haikuEnviron()})
	return newStepWireFixture(t, h), dir
}

// stepPurposeWaitIdle waits until the purpose call a start made has returned, which the Steps' calls count shows. The
// count is raised before the start returns, so it cannot read idle before the call has run.
func stepPurposeWaitIdle(t *testing.T, f *stepWireFixture) {
	t.Helper()
	engineOf(f.steps).until(t, "the purpose call to return", func() bool { return !f.steps.calls.underWay() })
}

// stepPurposeRow returns the stored row of the step id.
func stepPurposeRow(t *testing.T, f *stepWireFixture, id string) store.StepRow {
	t.Helper()
	row, err := f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	return row
}

// TestStepPurposeStartStoresItsPurpose checks that a start which names a purpose stores it on its row, and that it asks
// Haiku for none, since the fake claude never runs.
func TestStepPurposeStartStoresItsPurpose(t *testing.T) {
	t.Parallel()
	f, dir := stepPurposeFixture(t, haikuFake{stdout: stepPurposeReplyLine})
	id, err := f.steps.Start(t.Context(), stepPurposeStart(t, "Check the change."))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := stepPurposeRow(t, f, id).Purpose; got != "Check the change." {
		t.Errorf("stored purpose = %q, want the one the start named", got)
	}
	stepPurposeWaitIdle(t, f)
	if _, err := os.Stat(filepath.Join(dir, "runs")); !os.IsNotExist(err) {
		t.Errorf("a start with a purpose ran the fake claude for one (stat error %v)", err)
	}
}

// TestStepPurposeStartWithoutOneStoresWhatHaikuReturns checks that a start with no purpose stores the purpose the
// Haiku call returns, and that the call is made on the step's prompt.
func TestStepPurposeStartWithoutOneStoresWhatHaikuReturns(t *testing.T) {
	t.Parallel()
	f, dir := stepPurposeFixture(t, haikuFake{stdout: stepPurposeReplyLine})
	id, err := f.steps.Start(t.Context(), stepPurposeStart(t, ""))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	stepPurposeWaitIdle(t, f)
	if got := stepPurposeRow(t, f, id).Purpose; got != stepPurposeAnswer {
		t.Errorf("stored purpose = %q, want the Haiku reply %q", got, stepPurposeAnswer)
	}
	if input := haikuReadFile(t, dir, "stdin"); !strings.Contains(input, stepPurposePrompt) {
		t.Errorf("the purpose call's input %q does not carry the step's prompt", input)
	}
}

// TestStepPurposeStartReturnsBeforeHaikuAnswers checks that a start with no purpose returns while its Haiku call is
// still blocked, and that the purpose the call later returns is stored. The fake claude blocks until the test opens its
// release FIFO for writing, which it does only once the fake has started, so the start cannot have waited for the call.
func TestStepPurposeStartReturnsBeforeHaikuAnswers(t *testing.T) {
	t.Parallel()
	dir := haikuDir(t)
	release := filepath.Join(dir, "release")
	if err := syscall.Mkfifo(release, 0o600); err != nil {
		t.Fatalf("make the release FIFO: %v", err)
	}
	// A failure before the release leaves the fake blocked, so the cleanup opens the FIFO without waiting for a reader.
	// With no reader left the open fails, and the fake has already exited.
	t.Cleanup(func() {
		if w, err := os.OpenFile(release, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = w.Close()
		}
	})
	fake := haikuFake{stdout: stepPurposeReplyLine, extra: `: > "$dir/started"
read -r _ < "$dir/release"`}
	h := NewHaiku(HaikuConfig{Claude: haikuWriteFake(t, dir, fake), Environ: haikuEnviron()})
	f := newStepWireFixture(t, h)

	id, err := f.steps.Start(t.Context(), stepPurposeStart(t, ""))
	if err != nil {
		t.Fatalf("Start with its Haiku call blocked: %v", err)
	}
	if got := stepPurposeRow(t, f, id).Purpose; got != "" {
		t.Fatalf("purpose = %q before Haiku answered, want none", got)
	}
	waittest.UntilIdle(t, func() bool { return !f.steps.calls.underWay() }, "the purpose call never started claude",
		func() bool {
			_, err := os.Stat(filepath.Join(dir, "started"))
			return err == nil
		})

	w, err := os.OpenFile(release, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open the release FIFO: %v", err)
	}
	if _, err := w.WriteString("go\n"); err != nil {
		t.Fatalf("release the fake: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close the release FIFO: %v", err)
	}
	stepPurposeWaitIdle(t, f)
	if got := stepPurposeRow(t, f, id).Purpose; got != stepPurposeAnswer {
		t.Errorf("stored purpose = %q, want the Haiku reply %q", got, stepPurposeAnswer)
	}
}

// TestStepPurposeFailedHaikuLeavesTheStepStarted checks that a purpose call that fails stores no purpose, and that the
// step runs on as started.
func TestStepPurposeFailedHaikuLeavesTheStepStarted(t *testing.T) {
	t.Parallel()
	f, dir := stepPurposeFixture(t, haikuFake{stderr: "claude: refused", status: 1})
	id, err := f.steps.Start(t.Context(), stepPurposeStart(t, ""))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	stepPurposeWaitIdle(t, f)
	if _, err := os.Stat(filepath.Join(dir, "runs")); err != nil {
		t.Fatalf("the purpose call never ran claude: %v", err)
	}
	row := stepPurposeRow(t, f, id)
	if row.Purpose != "" {
		t.Errorf("stored purpose = %q after a failed call, want none", row.Purpose)
	}
	if row.End != "" {
		t.Errorf("step ended %q after a failed purpose call, want it still started", row.End)
	}
}

// TestStepPurposeStepMessageCarriesIt checks that the Step message a step's row is served as carries the row's purpose.
func TestStepPurposeStepMessageCarriesIt(t *testing.T) {
	t.Parallel()
	msg, err := stepMessage(store.StepRow{ID: "s1", Purpose: stepPurposeAnswer}, false)
	if err != nil {
		t.Fatalf("stepMessage: %v", err)
	}
	if got := msg.GetPurpose(); got != stepPurposeAnswer {
		t.Errorf("Step message purpose = %q, want %q", got, stepPurposeAnswer)
	}
}
