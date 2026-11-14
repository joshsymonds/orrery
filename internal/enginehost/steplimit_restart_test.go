package enginehost

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// limitResetsUnix is the reset of the seven_day limit the LIMIT turn's fake claude reports.
const limitResetsUnix = 1900000000

// restartLimitedStep crashes rig with a limited step open, restarts it, and reconciles the restarted manager and Steps the
// way a restart does. It returns the restarted manager and Steps, with logs receiving the restarted Steps' log.
func restartLimitedStep(t *testing.T, rig stepsRig, logs *bytes.Buffer) (*Manager, *Steps) {
	t.Helper()
	rig.crash()
	restarted, steps := rig.restart(t, rig.environ, slog.New(slog.NewTextHandler(logs, nil)))
	if err := steps.Readopt(t.Context(), restarted); err != nil {
		t.Fatalf("Readopt: %v", err)
	}
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile of the manager: %v", err)
	}
	if err := steps.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile of the steps: %v", err)
	}
	return restarted, steps
}

// loggedRigSteps returns a Steps over rig's manager and store that logs to logs, with a writer of its own.
func loggedRigSteps(t *testing.T, rig stepsRig, logs *bytes.Buffer) *Steps {
	t.Helper()
	writer := NewWriter(WriterConfig{Applier: rig.store, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	return newRigSteps(t, rig, rig.manager, writer, rig.environ, slog.New(slog.NewTextHandler(logs, nil)))
}

// requireNoLoggedError fails the test when logs holds an error record.
func requireNoLoggedError(t *testing.T, logs *bytes.Buffer) {
	t.Helper()
	if strings.Contains(logs.String(), "level=ERROR") {
		t.Fatalf("Steps logged an error, want none:\n%s", logs.String())
	}
}

// TestStepLimitHoldsStepAcrossRestart covers R3: a LIMIT step, after a crash and a restart, reads open and limited with
// the kind and reset its limit was stored with, and no fake claude runs after the restart.
func TestStepLimitHoldsStepAcrossRestart(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "LIMIT")
	dir := filepath.Join(rig.worktrees, id)
	pid := fakePID(t, dir)
	waitUntil(t, "step limited", func() bool { return stepRowOf(t, rig.steps, id).LoopLimited })
	requireGone(t, pid)

	var logs bytes.Buffer
	_, steps := restartLimitedStep(t, rig, &logs)

	row := stepRowOf(t, steps, id)
	if row.End != "" {
		t.Fatalf("End = %q after the restart, want the step open", row.End)
	}
	if !row.LoopLimited {
		t.Fatal("LoopLimited = false after the restart, want the step limited")
	}
	if row.LoopLimitKind != "seven_day" {
		t.Fatalf("LoopLimitKind = %q after the restart, want seven_day", row.LoopLimitKind)
	}
	if got := row.LoopLimitResets.Unix(); got != limitResetsUnix {
		t.Fatalf("LoopLimitResets = %d after the restart, want %d", got, limitResetsUnix)
	}
	if got := fakeArgs(t, dir); len(got) != 1 {
		t.Fatalf("fake claude starts = %q, want only the start before the restart", got)
	}
	requireNoLoggedError(t, &logs)
}

// TestStepLimitResumedAfterRestartRespawnsWithResume covers R3: a limited step re-adopted after a restart is resumed
// with Resume, which respawns its process with --resume in its claude session, and the resumed turn ends the step done.
func TestStepLimitResumedAfterRestartRespawnsWithResume(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "LIMIT")
	dir := filepath.Join(rig.worktrees, id)
	waitUntil(t, "step limited", func() bool { return stepRowOf(t, rig.steps, id).LoopLimited })
	writeResumeFiles(t, dir)

	var logs bytes.Buffer
	_, steps := restartLimitedStep(t, rig, &logs)
	if err := steps.Resume(t.Context(), id); err != nil {
		t.Fatalf("Resume after the restart: %v", err)
	}
	var row store.StepRow
	waitUntil(t, "resumed step done", func() bool {
		row = stepRowOf(t, steps, id)
		return row.End != ""
	})
	if row.End != stepLoopEndDone {
		t.Fatalf("End = %q reason %q, want %q", row.End, row.Reason, stepLoopEndDone)
	}

	args := fakeArgs(t, dir)
	if len(args) != 2 {
		t.Fatalf("fake claude starts = %q, want two", args)
	}
	if !hasFlagValue(args[1], "--resume", "session_1") {
		t.Fatalf("respawn args = %q, want --resume session_1", args[1])
	}
	requireNoLoggedError(t, &logs)
}

// TestStepsStopOfLimitedStepEndsStoppedWithoutSessionStop covers R3: Stop of a limited step, before a restart, ends it
// stopped and logs no error, since the step's process was released and there is no session to stop.
func TestStepsStopOfLimitedStepEndsStoppedWithoutSessionStop(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	var logs bytes.Buffer
	steps := loggedRigSteps(t, rig, &logs)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	start := stepStartReadOnly(repo, revision)
	start.Prompt = "LIMIT"
	id, err := steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, "step limited", func() bool { return stepRowOf(t, steps, id).LoopLimited })

	if err := steps.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop of a limited step: %v", err)
	}
	if row := stepRowOf(t, steps, id); row.End != stepStoppedEnd {
		t.Fatalf("End = %q, want %q", row.End, stepStoppedEnd)
	}
	requireNoLoggedError(t, &logs)
}

// TestStepsStopOfLimitedStepAfterRestartEndsStopped covers R3: Stop of a limited step re-adopted after a restart ends it
// stopped and logs no error.
func TestStepsStopOfLimitedStepAfterRestartEndsStopped(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "LIMIT")
	waitUntil(t, "step limited", func() bool { return stepRowOf(t, rig.steps, id).LoopLimited })

	var logs bytes.Buffer
	_, steps := restartLimitedStep(t, rig, &logs)
	if err := steps.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop after the restart: %v", err)
	}
	if row := stepRowOf(t, steps, id); row.End != stepStoppedEnd {
		t.Fatalf("End = %q, want %q", row.End, stepStoppedEnd)
	}
	requireNoLoggedError(t, &logs)
}

// TestStepsStopOfLimitedRowWithoutLoopEndsStopped covers R3: Stop of a limited open row that no loop runs ends it stopped
// and logs no error, since its process was released and no session is left to stop.
func TestStepsStopOfLimitedRowWithoutLoopEndsStopped(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "LIMIT")
	waitUntil(t, "step limited", func() bool { return stepRowOf(t, rig.steps, id).LoopLimited })

	var logs bytes.Buffer
	rig.crash()
	_, steps := rig.restart(t, rig.environ, slog.New(slog.NewTextHandler(&logs, nil)))
	if err := steps.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop of a limited row with no loop: %v", err)
	}
	if row := stepRowOf(t, steps, id); row.End != stepStoppedEnd {
		t.Fatalf("End = %q, want %q", row.End, stepStoppedEnd)
	}
	requireNoLoggedError(t, &logs)
}

// TestStepLimitExitAfterLimitTurnStaysLimited covers R3: a limit turn end followed at once by the session's exit leaves
// the step limited. run picks at random between a queued turn end and a queued exit, so the test hands the exit to
// exitReceived, run's exit path, as run does when it picks the exit first, with the limit turn's end still queued.
func TestStepLimitExitAfterLimitTurnStaysLimited(t *testing.T) {
	t.Parallel()
	f := newStepLimitFixture(t)
	f.run.Event(session.AssistantError{Error: apiErrorRateLimit})
	f.run.Return(session.TurnEnded{IsError: true})

	if !f.run.exitReceived(t.Context(), "claude died") {
		t.Fatal("the run ended on an exit after its limit turn, want it to go on limited")
	}
	if row := f.row(t); row.End != "" || !row.LoopLimited {
		t.Fatalf("limited %t, end %q reason %q after a limit turn and its exit, want limited and open",
			row.LoopLimited, row.End, row.Reason)
	}
}

// TestStepLimitRestoredRunDoesNotHandleItsLimitTurnAgain covers R3: a run re-adopted from a limited row restores the
// limit, Limited, LimitKind and LimitResets, and does not handle the limit turn's stored end again, so its released
// process is not released a second time and the step stays limited.
func TestStepLimitRestoredRunDoesNotHandleItsLimitTurnAgain(t *testing.T) {
	t.Parallel()
	f := newStepLimitFixture(t)
	if err := f.store.ApplyEngine(t.Context(), []store.EngineOp{store.EngineRateLimited{
		ID: stepLoopID, Status: "rejected", Kind: "seven_day", ResetsAt: time.Unix(limitResetsUnix, 0),
	}}, time.Unix(1, 0)); err != nil {
		t.Fatalf("store rate limit: %v", err)
	}
	cancel, done := f.start(t)
	limitTurn(f.run)
	row := f.awaitLimited(t)
	cancel()
	waitRunEnd(t, done)

	cfg := f.run.stepRunConfig
	cfg.sessions = stepLimitSessions{stepLoopSessions: f.sessions, releaseErr: errors.New("release refused")}
	cfg.restore = &stepRestore{Row: row, Turn: store.StepTurnEnd{IsError: true}, Found: true}
	f.run = newStepRun(cfg)
	f.start(t)

	var limited bool
	var kind string
	var resets time.Time
	err := f.run.do(t.Context(), func(context.Context) error {
		limited, kind, resets = f.run.limited, f.run.limitKind, f.run.limitResets
		return nil
	})
	if err != nil {
		t.Fatalf("owner action on the restored run: %v, want it still running", err)
	}
	if !limited || kind != "seven_day" || resets.Unix() != limitResetsUnix {
		t.Fatalf("restored limited %t, kind %q, resets %d, want limited, seven_day and %d",
			limited, kind, resets.Unix(), limitResetsUnix)
	}
	if got := f.row(t); got.End != "" || !got.LoopLimited {
		t.Fatalf("End %q, limited %t after the restored run, want open and limited", got.End, got.LoopLimited)
	}
}
