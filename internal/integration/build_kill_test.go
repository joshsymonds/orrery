package integration_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// TestBuildSigkillDuringImplementerStartsItOnce proves that orreryd killed while a task's implementer step runs starts
// that step once. The restarted orreryd finds the step under its derived id and leaves it running. Once the fake claude
// is released, the step ends done with a return that names the commit the fake made, the task reaches green at that
// commit, and the epic holds exactly one implementer step.
func TestBuildSigkillDuringImplementerStartsItOnce(t *testing.T) {
	t.Parallel()
	r := newBuildRig(t)
	daemon, ack := r.startPassDaemon(t, "first")
	id := r.stepID()
	dir := r.stepDir()
	t.Cleanup(func() { clearHolder(t, filepath.Join(r.stateDir, "holders", id)) })

	r.awaitStep(t, daemon, ack, id)
	stdin := filepath.Join(dir, "fakeclaude.stdin")
	waitUntil(t, func() bool { return fileContains(t, stdin, buildRigMarker) })
	pid := fakeClaudePID(t, dir)

	killOrreryd(t, daemon)
	restarted, restartedAck := r.startPassDaemon(t, "restarted")
	if end := r.stepRow(t, id).GetEnd(); end != "" {
		t.Fatalf("step %s ended %q after the restart, want it still running its turn", id, end)
	}
	requireProcessRunning(t, pid)
	if got := r.task(t).GetStep(); got != id {
		t.Fatalf("step of task 1 after the restart = %q, want %q", got, id)
	}
	// A restart that started the step again would fail the task: the engine refuses a start under an id already in use.
	if task := r.task(t); task.GetStatus() != "open" || task.GetFailure() != "" {
		t.Fatalf("task 1 after the restart is %q with failure %q, want open with no failure",
			task.GetStatus(), task.GetFailure())
	}
	// The epic is paused before the release, so the task stops at green and no evidence run starts after it.
	r.pause(t)

	stepReadoptTouch(t, filepath.Join(dir, "fakeclaude.release"))
	commit := requireBuildStepDone(t, dir, r.awaitEnd(t, restarted, id))
	r.awaitGreen(t, restarted, restartedAck)
	if got := r.task(t).GetRevision(); got != commit {
		t.Fatalf("revision of task 1 at green = %q, want the fake's commit %s", got, commit)
	}
	if steps := r.taskSteps(t); len(steps) != 1 || steps[0].GetId() != id {
		t.Fatalf("labeled steps of task 1 = %d, want exactly the implementer step %s", len(steps), id)
	}
	if n := promptsWritten(t, dir, buildRigMarker); n != 1 {
		t.Fatalf("fake claude read the brief %d times, want 1", n)
	}
	waitGone(t, pid)
	wantExit(t, restarted.stop(t), 0)
}

// requireBuildStepDone fails unless step ended done, its return is done at the commit HEAD names in dir, and it
// recorded one passing gate check. It returns that commit.
func requireBuildStepDone(t *testing.T, dir string, step *v1.Step) string {
	t.Helper()
	if step.GetEnd() != "done" {
		var failed []string
		for _, check := range step.GetChecks() {
			if !check.GetOk() {
				failed = append(
					failed,
					fmt.Sprintf("round %d %s: %s", check.GetRound(), check.GetName(), check.GetDetail()),
				)
			}
		}
		t.Fatalf(
			"step %s ended %q (%s), want done; failed checks %q",
			step.GetId(),
			step.GetEnd(),
			step.GetReason(),
			failed,
		)
	}
	var ret struct {
		Status string `json:"status"`
		Commit string `json:"commit"`
	}
	if err := json.Unmarshal([]byte(step.GetResult()), &ret); err != nil {
		t.Fatalf("decode return %q: %v", step.GetResult(), err)
	}
	head := stepReadoptGit(t, dir, "rev-parse", "HEAD")
	if ret.Status != "done" || ret.Commit != head {
		t.Fatalf("return = %+v, want status done at HEAD %s", ret, head)
	}
	gateChecks := 0
	for _, check := range step.GetChecks() {
		if check.GetName() != "gate" {
			continue
		}
		gateChecks++
		if !check.GetOk() {
			t.Fatalf("gate check failed: %s", check.GetDetail())
		}
	}
	if gateChecks != 1 {
		t.Fatalf("gate checks of step %s = %d, want 1", step.GetId(), gateChecks)
	}
	return head
}
