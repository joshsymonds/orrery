package integration_test

import (
	"path/filepath"
	"testing"
)

// TestBuildImplementerStartsWithItsBrief proves that orreryd starts a planned task's implementer step from the build
// the store holds. The step runs the agent the roles file names, in a worktree under the task's directory at the base.
// The fake claude reads a brief with the task's title, deliverable, files and gate, its Requirement, and the epic's
// exclusion, and no decision the epic holds. The step's owned paths are not in its row, so the brief's list of files is
// their observable form.
func TestBuildImplementerStartsWithItsBrief(t *testing.T) {
	t.Parallel()
	r := newBuildRig(t)
	daemon, ack := r.startPassDaemon(t, "first")
	id := r.stepID()
	dir := r.stepDir()
	t.Cleanup(func() { clearHolder(t, filepath.Join(r.stateDir, "holders", id)) })

	r.awaitStep(t, daemon, ack, id)
	row := r.stepRow(t, id)
	if got := row.GetAgent(); got != buildRigAgent {
		t.Errorf("agent of %s = %q, want %q from the roles file", id, got, buildRigAgent)
	}
	if got := row.GetWorkspace(); got != dir {
		t.Errorf("workspace of %s = %q, want %q", id, got, dir)
	}
	if got := row.GetRevision(); got != r.base {
		t.Errorf("revision of %s = %q, want the base %s", id, got, r.base)
	}
	if row.GetEpic() != buildRigEpic || row.GetTask() != "T1" {
		t.Errorf("epic and task of %s = %q and %q, want %q and T1", id, row.GetEpic(), row.GetTask(), buildRigEpic)
	}
	if row.GetReadOnly() || row.GetEnd() != "" {
		t.Errorf("step %s read only %t and ended %q, want a running writing step", id, row.GetReadOnly(), row.GetEnd())
	}
	if head := stepReadoptGit(t, dir, "rev-parse", "HEAD"); head != r.base {
		t.Errorf("worktree of %s is at %s, want the base %s", id, head, r.base)
	}

	stdin := filepath.Join(dir, "fakeclaude.stdin")
	waitUntil(t, func() bool {
		return fileContains(t, stdin, "Owned files (change only these):")
	})

	if !fileContains(t, filepath.Join(dir, "fakeclaude.args"), "--agent "+buildRigAgent) {
		t.Errorf("fake claude was not started with --agent %s", buildRigAgent)
	}
	wantBrief := []string{
		"Task: " + buildRigTitle,
		"Deliverable: " + buildRigDeliverable,
		// The brief's newlines are JSON escapes in the line the fake reads, so the file list is matched with them escaped.
		`Owned files (change only these):\n- ` + buildRigFile,
		"Gate: " + buildRigGate + ". After you return done, orreryd runs",
		"R1: " + buildRigRequirement,
		"- " + buildRigExclusion,
	}
	for _, want := range wantBrief {
		if !fileContains(t, stdin, want) {
			t.Errorf("the brief the fake claude read lacks %q", want)
		}
	}
	if fileContains(t, stdin, buildRigDecision) {
		t.Errorf("the brief the fake claude read carries the epic decision %q", buildRigDecision)
	}
	wantExit(t, daemon.stop(t), 0)
}
