package enginehost

import (
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/session"
)

// stepScratchState returns what a scratch step must leave alone in repo: its refs, which hold every branch, and its
// status, which holds the workspace's files.
func stepScratchState(t *testing.T, repo string) string {
	t.Helper()
	refs := stepGitTestRun(t, repo, "for-each-ref", "--format=%(refname) %(objectname)")
	status := stepGitTestRun(t, repo, "status", "--porcelain", "--untracked-files=all")
	return refs + "\n--\n" + status
}

// stepScratchWriteAndCommit writes and modifies files in the scratch worktree wt, then commits them, so the step's
// checkout moves in every way a checkout check watches.
func stepScratchWriteAndCommit(t *testing.T, wt string) {
	t.Helper()
	stepGitTestWrite(t, wt, "README", "changed\n")
	stepGitTestWrite(t, wt, "notes.txt", "notes\n")
	stepGitTestRun(t, wt, "add", "notes.txt")
	stepGitTestRun(t, wt, "commit", "-q", "-m", "scratch commit")
}

// TestTestRunnerRequestsScratchStepKeepsNoWrites starts a scratch step whose agent writes in its worktree and returns
// done. The return must end done, not checks_failed, and once the step ends the repository's refs and workspace are as
// they were and the worktree is gone.
func TestTestRunnerRequestsScratchStepKeepsNoWrites(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	repo := stepCheckoutRepo(t)
	before := stepScratchState(t, repo)
	start := stepStartReadOnly(repo, "")
	start.Scratch = true
	id, wt := stepCheckoutStart(t, f, start)
	stepScratchWriteAndCommit(t, wt)

	row := stepCheckoutReturn(t, f, id, json.RawMessage(`{"status":"done"}`))
	if row.End != stepDefaultDone {
		t.Fatalf("end = %q, want %q; checks %+v", row.End, stepDefaultDone, row.Checks)
	}
	if _, ok := stepCheckoutCheckIn(row.Checks); ok {
		t.Errorf("checks = %+v, want no checkout check for a scratch step", row.Checks)
	}
	stepStartAwait(t, engineOf(f.steps), "scratch worktree "+wt+" to be removed", func() bool {
		return stepStartGone(wt)
	})
	if after := stepScratchState(t, repo); after != before {
		t.Errorf("repository state after the scratch step = %q, want %q", after, before)
	}
}

// TestTestRunnerRequestsScratchReadoptKeepsNoCheckoutCheck starts a scratch step, crashes the manager, re-adopts the step
// from its row, and writes in its worktree before it returns done. A scratch row stores no checkout state, so the return
// ends done with no checkout check.
func TestTestRunnerRequestsScratchReadoptKeepsNoCheckoutCheck(t *testing.T) {
	t.Parallel()
	haiku := NewHaiku(HaikuConfig{
		Claude:  haikuWriteFake(t, haikuDir(t), haikuFake{stdout: stepReadoptMetLine}),
		Environ: haikuEnviron(),
	})
	rig := newStepsRig(t, haiku)
	start := stepStartReadOnly(stepGitTestRepo(t), "")
	start.Scratch = true
	id := rig.startStalled(t, start)
	wt := filepath.Join(rig.worktrees, id)

	rig.crash()
	restarted, steps := rig.restart(t, rig.environ, slog.New(slog.DiscardHandler))
	if err := steps.Readopt(t.Context(), restarted); err != nil {
		t.Fatalf("Readopt: %v", err)
	}
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile of the manager: %v", err)
	}
	if err := steps.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile of the steps: %v", err)
	}
	stepScratchWriteAndCommit(t, wt)

	run := stepsRunOf(t, steps, id)
	run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":"done"}`)})
	stepStartAwait(t, engineOf(rig.manager, rig.steps, restarted, steps, run), "step "+id+" to end", func() bool {
		row, err := steps.Status(t.Context(), id)
		return err == nil && row.End != ""
	})

	row, err := steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if row.End != stepDefaultDone {
		t.Fatalf("end = %q, want %q; checks %+v", row.End, stepDefaultDone, row.Checks)
	}
	if _, ok := stepCheckoutCheckIn(row.Checks); ok {
		t.Errorf("checks = %+v, want no checkout check for a re-adopted scratch step", row.Checks)
	}
}

// TestTestRunnerRequestsScratchRefusedOnWritingStart starts a writing step with Scratch set. The start must be refused
// with ErrBadOwned before anything is started.
func TestTestRunnerRequestsScratchRefusedOnWritingStart(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
	start := stepStartWriting(linked)
	start.Scratch = true

	id, err := f.steps.Start(t.Context(), start)
	if !errors.Is(err, ErrBadOwned) {
		t.Fatalf("Start of a writing scratch step = %v, want ErrBadOwned", err)
	}
	if id != "" {
		t.Errorf("id = %q, want none for a refused start", id)
	}
	if n := f.sessions.startCalls(); n != 0 {
		t.Errorf("StartStep was called %d times, want none", n)
	}
}

// TestTestRunnerRequestsScratchReadOnlyStillChecked writes the same change in a plain read-only step's worktree and in a
// scratch step's. The plain step's return must still end checks_failed on its moved checkout, and the scratch step's
// must end done, so the exemption is the scratch flag's alone.
func TestTestRunnerRequestsScratchReadOnlyStillChecked(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	repo := stepCheckoutRepo(t)
	plainID, plainWt := stepCheckoutStart(t, f, stepStartReadOnly(repo, ""))
	scratch := stepStartReadOnly(repo, "")
	scratch.Scratch = true
	scratchID, scratchWt := stepCheckoutStart(t, f, scratch)
	stepGitTestWrite(t, plainWt, "README", "changed\n")
	stepGitTestWrite(t, scratchWt, "README", "changed\n")

	plain := stepCheckoutReturn(t, f, plainID, json.RawMessage(`{"status":"done"}`))
	if plain.End != stepLoopEndChecksFailed {
		t.Fatalf("plain step end = %q, want %q", plain.End, stepLoopEndChecksFailed)
	}
	check, ok := stepCheckoutCheckIn(plain.Checks)
	if !ok || check.OK || !strings.Contains(check.Detail, "changed paths: README") {
		t.Errorf("plain step checks = %+v, want a failed checkout check naming README", plain.Checks)
	}

	scratchRow := stepCheckoutReturn(t, f, scratchID, json.RawMessage(`{"status":"done"}`))
	if scratchRow.End != stepDefaultDone {
		t.Errorf("scratch step end = %q, want %q", scratchRow.End, stepDefaultDone)
	}
}
