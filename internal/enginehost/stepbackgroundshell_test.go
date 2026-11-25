package enginehost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestStepBackgroundBashWaitsForItsCommand plays a step whose turn starts a long command with Bash's run_in_background,
// such as a check, and ends with a not-done return while the command still runs. That return is not handed: the step
// stays open. When the command ends, claude starts a turn by itself, and that turn's done return ends the step done.
func TestStepBackgroundBashWaitsForItsCommand(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "BGBASH")
	dir := filepath.Join(rig.worktrees, id)

	waitHistory(
		t,
		engineOf(rig.manager, rig.steps),
		rig.path,
		id,
		"the prompted turn done with its background command listed",
		func(h store.EngineHistory) bool {
			return hasBlock(t, h, "fake bgtool 1") && lastState(t, h) == string(protocol.StateDone)
		},
	)
	if row := stepRowOf(t, rig.steps, id); row.End != "" || row.Result != "" {
		t.Fatalf("step with its background command running and a not-done return: end %q result %q, want open",
			row.End, row.Result)
	}

	if err := os.WriteFile(filepath.Join(dir, "fakeclaude.release"), nil, 0o600); err != nil {
		t.Fatalf("write fakeclaude.release: %v", err)
	}
	row := awaitStepEnd(t, rig.steps, id)
	if row.End != stepLoopEndDone || row.Status != stepDefaultDone {
		t.Fatalf("end = %q status %q, want done %q", row.End, row.Status, stepDefaultDone)
	}
	var ret struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(row.Result), &ret); err != nil {
		t.Fatalf("decode stored return %q: %v", row.Result, err)
	}
	if ret.Summary != "fake done" {
		t.Fatalf("stored return = %q, want the done return of the turn claude started when the command ended",
			row.Result)
	}
}

// TestStepBackgroundMonitorReturnIsHandedAtOnce plays the same turn with the background task started by a Monitor
// call, a watch that may never end. The step does not wait on it: the turn's not-done return ends the step returned.
func TestStepBackgroundMonitorReturnIsHandedAtOnce(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "BGMONITOR")

	row := awaitStepEnd(t, rig.steps, id)
	if row.End != stepLoopEndReturned || row.Status != "blocked" {
		t.Fatalf("end = %q status %q, want returned blocked", row.End, row.Status)
	}
}
