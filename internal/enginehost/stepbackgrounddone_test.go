package enginehost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestStepBackgroundDoneReturnIsHandedWhenClaudeStartsTheTurn covers R1 through the full step loop. A prompted turn
// starts two background tasks and ends not-done, which is not handed. When one task ends, claude starts a turn by
// itself, with no stdin read, whose return is done while the other task is still listed. That return is handed: the
// step ends done with it, and its process stops.
func TestStepBackgroundDoneReturnIsHandedWhenClaudeStartsTheTurn(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "BGSELF")
	dir := filepath.Join(rig.worktrees, id)
	pid := fakePID(t, dir)

	waitHistory(t, rig.path, id, "the prompted turn done with both tasks listed", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake bgself 1") && lastState(t, h) == string(protocol.StateDone)
	})
	if row := stepRowOf(t, rig.steps, id); row.End != "" || row.Result != "" {
		t.Fatalf("step with tasks listed and a not-done return: end %q result %q, want open and unreturned",
			row.End, row.Result)
	}

	if err := os.WriteFile(filepath.Join(dir, "fakeclaude.release"), nil, 0o600); err != nil {
		t.Fatalf("write fakeclaude.release: %v", err)
	}
	var row store.StepRow
	waitUntil(t, "the step to end", func() bool {
		row = stepRowOf(t, rig.steps, id)
		return row.End != ""
	})
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
		t.Fatalf("stored return = %q, want the self-started turn's done return", row.Result)
	}
	requireGone(t, pid)
}
