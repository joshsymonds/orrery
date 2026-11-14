package enginehost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// writeResumeFiles writes fakeclaude.next, which makes the resume prompt play STEPDONE, and fakeclaude.release, which
// lets that turn finish, into dir, the directory the step's fake claude runs in.
func writeResumeFiles(t *testing.T, dir string) {
	t.Helper()
	writeResumeNext(t, dir)
	releaseResumedTurn(t, dir)
}

// writeResumeNext writes fakeclaude.next into dir, so the resume prompt plays STEPDONE, which holds its turn until
// releaseResumedTurn lets it finish.
func writeResumeNext(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "fakeclaude.next"), []byte("STEPDONE"), 0o600); err != nil {
		t.Fatalf("write fakeclaude.next: %v", err)
	}
}

// releaseResumedTurn writes fakeclaude.release into dir, which lets the resumed STEPDONE turn finish.
func releaseResumedTurn(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "fakeclaude.release"), nil, 0o600); err != nil {
		t.Fatalf("write fakeclaude.release: %v", err)
	}
}

// requireResumedStart waits until the fake claude in dir has started twice and checks that the second start resumed
// session_1. It reads the starts before the resumed turn may end: a read-only step's worktree, where the starts are
// logged, is removed when the step ends.
func requireResumedStart(t *testing.T, dir string) {
	t.Helper()
	var args []string
	waitUntil(t, "the respawned fake claude start", func() bool {
		args = fakeArgs(t, dir)
		return len(args) >= 2
	})
	if len(args) != 2 {
		t.Fatalf("fake claude starts = %q, want two", args)
	}
	if !hasFlagValue(args[1], "--resume", "session_1") {
		t.Fatalf("respawn args = %q, want --resume session_1", args[1])
	}
}

// TestStepsResumeRespawnsLimitedStepInItsSession covers R2: Resume of a limited step respawns its process in the same
// session with --resume, stores the step as not limited, and the resumed turn ends the step done.
func TestStepsResumeRespawnsLimitedStepInItsSession(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "LIMIT")
	dir := filepath.Join(rig.worktrees, id)
	pid := fakePID(t, dir)
	waitUntil(t, "step limited", func() bool { return stepRowOf(t, rig.steps, id).LoopLimited })
	requireGone(t, pid)
	writeResumeNext(t, dir)

	if err := rig.steps.Resume(t.Context(), id); err != nil {
		t.Fatalf("Resume of a limited step: %v", err)
	}
	requireResumedStart(t, dir)
	releaseResumedTurn(t, dir)
	var row store.StepRow
	waitUntil(t, "resumed step done", func() bool {
		row = stepRowOf(t, rig.steps, id)
		return row.End != ""
	})
	if row.End != stepLoopEndDone {
		t.Fatalf("End = %q reason %q, want %q", row.End, row.Reason, stepLoopEndDone)
	}
	if row.LoopLimited {
		t.Fatal("LoopLimited = true after the resumed turn, want the limit cleared")
	}
}

// TestStepsResumeSendsOwnerResumePrompt covers R2: Resume sends the owner's prompt that the usage limit has reset, as
// the prompt of the respawned turn.
func TestStepsResumeSendsOwnerResumePrompt(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "LIMIT")
	waitUntil(t, "step limited", func() bool { return stepRowOf(t, rig.steps, id).LoopLimited })
	dir := filepath.Join(rig.worktrees, id)
	writeResumeFiles(t, dir)

	if err := rig.steps.Resume(t.Context(), id); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	waitUntil(t, "resumed step done", func() bool { return stepRowOf(t, rig.steps, id).End != "" })

	history := waitHistory(t, rig.path, id, "the resume prompt recorded", func(h store.EngineHistory) bool {
		return slices.Contains(conversationOf(t, h), "prompt: "+stepResumePrompt)
	})
	if !slices.Contains(conversationOf(t, history), "prompt: "+stepResumePrompt) {
		t.Fatalf("conversation = %q, want the owner resume prompt", conversationOf(t, history))
	}
}

// TestStepsResumeOfStepNotLimitedRefuses covers R2: Resume of a step whose turn is running and that no limit holds
// returns ErrStepNotLimited, and the step is left running.
func TestStepsResumeOfStepNotLimitedRefuses(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "STEPDONE")

	if err := rig.steps.Resume(t.Context(), id); !errors.Is(err, ErrStepNotLimited) {
		t.Fatalf("Resume of a step not limited = %v, want ErrStepNotLimited", err)
	}
	if row := stepRowOf(t, rig.steps, id); row.End != "" || row.LoopLimited {
		t.Fatalf("end %q, limited %t after the refused Resume, want open and not limited", row.End, row.LoopLimited)
	}
	if err := rig.steps.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestStepsResumeOfUnknownIDIsNoStep covers R2: Resume of an id with no step row returns an error matching
// store.ErrNoStep.
func TestStepsResumeOfUnknownIDIsNoStep(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)

	if err := rig.steps.Resume(t.Context(), "no-such-step"); !errors.Is(err, store.ErrNoStep) {
		t.Fatalf("Resume of an unknown id = %v, want an error matching store.ErrNoStep", err)
	}
}

// TestStepsResumeOfEndedStepIsNotRunning covers R2: Resume of a step that has ended returns ErrStepNotRunning.
func TestStepsResumeOfEndedStepIsNotRunning(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "DIE")
	waitUntil(t, "step ended", func() bool { return stepRowOf(t, rig.steps, id).End != "" })

	if err := rig.steps.Resume(t.Context(), id); !errors.Is(err, ErrStepNotRunning) {
		t.Fatalf("Resume of an ended step = %v, want ErrStepNotRunning", err)
	}
}

// TestStepsResumeWithStepCapFullLeavesStepLimited covers R2: with the step cap full by another live step, Resume
// returns ErrCapReached and the step stays limited, and a later Resume once the slot frees succeeds.
func TestStepsResumeWithStepCapFullLeavesStepLimited(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "LIMIT")
	dir := filepath.Join(rig.worktrees, id)
	pid := fakePID(t, dir)
	waitUntil(t, "step limited", func() bool { return stepRowOf(t, rig.steps, id).LoopLimited })
	// The loop stores the limit before it releases the process, so the slot is free only once the release finishes.
	requireGone(t, pid)
	writeResumeFiles(t, dir)
	holder := startLimitProbe(t, rig, repo, revision, "STEPDONE")

	err := rig.steps.Resume(t.Context(), id)
	if !errors.Is(err, ErrCapReached) {
		t.Fatalf("Resume with the step cap full = %v, want ErrCapReached", err)
	}
	if row := stepRowOf(t, rig.steps, id); !row.LoopLimited || row.End != "" {
		t.Fatalf("limited %t, end %q after the refused Resume, want limited and open", row.LoopLimited, row.End)
	}

	if err := rig.steps.Stop(t.Context(), holder); err != nil {
		t.Fatalf("Stop of the step holding the slot: %v", err)
	}
	if err := rig.steps.Resume(t.Context(), id); err != nil {
		t.Fatalf("Resume once the slot frees: %v", err)
	}
	waitUntil(t, "resumed step done", func() bool { return stepRowOf(t, rig.steps, id).End != "" })
	if row := stepRowOf(t, rig.steps, id); row.End != stepLoopEndDone {
		t.Fatalf("End = %q reason %q, want %q", row.End, row.Reason, stepLoopEndDone)
	}
}

// resumeFixtureRun makes the fixture's Continue return err, limits the fixture's run and resumes it in its loop.
func resumeFixtureRun(t *testing.T, f *stepLoopFixture, err error) error {
	t.Helper()
	f.sessions.mu.Lock()
	f.sessions.continueErr = err
	f.sessions.mu.Unlock()
	limitTurn(f.run)
	f.awaitLimited(t)
	return f.run.do(t.Context(), f.run.resumeLimited)
}

// TestStepResumeCapRefusalLeavesRunLimited covers R2: a Continue that the step cap refuses returns an error matching
// ErrCapReached, and the run stays limited and open.
func TestStepResumeCapRefusalLeavesRunLimited(t *testing.T) {
	t.Parallel()
	f := newStepLimitFixture(t)
	f.start(t)

	err := resumeFixtureRun(t, f, fmt.Errorf("%w: 1 live sessions, cap 1", ErrCapReached))
	if !errors.Is(err, ErrCapReached) {
		t.Fatalf("Resume = %v, want ErrCapReached", err)
	}
	row := f.row(t)
	if !row.LoopLimited || row.End != "" {
		t.Fatalf("limited %t, end %q, want limited and open after the refused Continue", row.LoopLimited, row.End)
	}
}

// TestStepResumeOtherContinueErrorEndsStepFailed covers R2: a Continue that fails for any reason other than the step
// cap ends the step failed and returns that error.
func TestStepResumeOtherContinueErrorEndsStepFailed(t *testing.T) {
	t.Parallel()
	f := newStepLimitFixture(t)
	f.start(t)
	refused := errors.New("claude would not start")

	err := resumeFixtureRun(t, f, refused)
	if !errors.Is(err, refused) {
		t.Fatalf("Resume = %v, want the continue error", err)
	}
	row := f.awaitEnded(t)
	if row.End != stepLoopEndFailed || !strings.Contains(row.Reason, refused.Error()) {
		t.Fatalf("End = %q, reason %q, want failed naming the continue error", row.End, row.Reason)
	}
}

// TestStepResumeClearsLimitOnSuccess covers R2: a Continue that succeeds stores the loop state with the limit cleared.
func TestStepResumeClearsLimitOnSuccess(t *testing.T) {
	t.Parallel()
	f := newStepLimitFixture(t)
	f.start(t)
	limitTurn(f.run)
	f.awaitLimited(t)

	if err := f.run.do(t.Context(), f.run.resumeLimited); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	row := f.row(t)
	if row.LoopLimited || !row.LoopLimitResets.IsZero() || row.LoopLimitKind != "" {
		t.Fatalf("limited %t, kind %q, resets %v, want the limit cleared", row.LoopLimited, row.LoopLimitKind,
			row.LoopLimitResets)
	}
	if got := f.sessions.continuedPrompts(); len(got) != 1 || got[0].Text != stepResumePrompt ||
		got[0].Sender != store.PromptSenderOwner {
		t.Fatalf("continue prompts = %+v, want the owner resume prompt once", got)
	}
}
