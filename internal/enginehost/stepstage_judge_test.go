package enginehost

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// stepJudgeRig is a manager and the Steps that start its steps, over one store, on a fake clock that the Steps and its
// writer both read. Its Haiku is a fake judge, which answers from judgeDir when the test releases it.
type stepJudgeRig struct {
	stepsRig
	clock    *watchLoopClock
	judgeDir string
}

// stepJudgeHold is the shared fake claude's extra fragment that holds each judge call until dir/release exists. The
// release is never removed, so a call after the first answers at once.
const stepJudgeHold = `while [ ! -e "$dir/release" ]; do sleep 0.05; done`

// newStepJudgeRig returns a rig whose clock reads watchEpoch and whose judge answers from its own directory.
func newStepJudgeRig(t *testing.T) *stepJudgeRig {
	t.Helper()
	s, path := openStore(t)
	root, gateRoot := holderRoot(t), holderRoot(t)
	t.Cleanup(func() {
		clearHolderDirs(t, root)
		clearHolderDirs(t, gateRoot)
	})
	home := t.TempDir()
	writeStepAgents(t, home)
	judgeDir := t.TempDir()
	rig := &stepJudgeRig{
		stepsRig: stepsRig{
			store: s, path: path, root: root, gateRoot: gateRoot, worktrees: t.TempDir(),
			environ: []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home},
			haiku: NewHaiku(HaikuConfig{
				Claude:  haikuWriteFake(t, judgeDir, haikuFake{extra: stepJudgeHold}),
				Environ: haikuEnviron(),
			}),
		},
		clock:    &watchLoopClock{at: watchEpoch()},
		judgeDir: judgeDir,
	}
	rig.build(t)
	return rig
}

// build starts the manager and the Steps of the rig, with a writer over a new crashApplier of the store that reads the
// rig's clock. The applier becomes the rig's, so a later crash refuses every batch this build's writer stores.
func (r *stepJudgeRig) build(t *testing.T) {
	t.Helper()
	r.applier = &crashApplier{inner: r.store}
	writer := NewWriter(WriterConfig{Applier: r.applier, Tick: 5 * time.Millisecond, Now: r.clock.now})
	startRun(t, writer)
	r.manager = newRigManager(t, r.stepsRig, writer)
	steps, err := NewSteps(StepsConfig{
		Sessions: r.manager, Store: r.store, Writer: writer, Worktrees: r.worktrees, Environ: r.environ,
		Logger: slog.New(slog.DiscardHandler), Haiku: r.haiku, Now: r.clock.now, GateStateDir: r.gateRoot,
		GateScope: fakeSystemdRunPath(t),
	})
	if err != nil {
		t.Fatalf("NewSteps: %v", err)
	}
	t.Cleanup(steps.Close)
	r.steps = steps
}

// restartJudgeRig crashes the rig as a crash leaves it, with its live sessions detached under their holders, and restarts
// it over the store as restart does: a new writer, manager and Steps, then Readopt and Reconcile.
func (r *stepJudgeRig) restartJudgeRig(t *testing.T) {
	t.Helper()
	r.crash()
	r.steps.Close()
	r.build(t)
	if err := r.steps.Readopt(t.Context(), r.manager); err != nil {
		t.Fatalf("Readopt: %v", err)
	}
	if err := r.manager.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile of the manager: %v", err)
	}
	if err := r.steps.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile of the steps: %v", err)
	}
}

// stepJudgeAnswer sets the answer of the fake judge in judgeDir to a verdict with reason, without releasing a call.
func stepJudgeAnswer(t *testing.T, judgeDir, verdict, reason string) {
	t.Helper()
	line := fmt.Sprintf(`{"type":"result","subtype":"success","is_error":false,`+
		`"structured_output":{"verdict":%q,"reason":%q}}`, verdict, reason)
	stepStageWrite(t, filepath.Join(judgeDir, "stdout"), line)
}

// stepJudgeRelease lets the fake judge in judgeDir answer its waiting call.
func stepJudgeRelease(t *testing.T, judgeDir string) {
	t.Helper()
	stepStageWrite(t, filepath.Join(judgeDir, "release"), "")
}

// awaitStepStage waits until the stored row of id holds its loop in stage.
func awaitStepStage(t *testing.T, steps *Steps, id, stage string) {
	t.Helper()
	waitUntil(t, "stored stage "+stage, func() bool { return stepRowOf(t, steps, id).LoopStage == stage })
}

// TestStepJudgeStagesSurviveRestart covers R1: one read-only step goes through a usage limit, its resume, a judge that
// sends it back, a hold, an owner resume and a second judge, on a fake clock, and is restarted at each stage it holds.
// Each stored stage carries the clock reading of its change, the send-back and both resumes store turn, and every restart
// keeps the stage and its start.
func TestStepJudgeStagesSurviveRestart(t *testing.T) {
	t.Parallel()
	rig := newStepJudgeRig(t)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")

	// The first turn ends on a usage limit at a minute, and the step is stored as limited from there.
	rig.clock.set(time.Minute)
	id := startLimitProbe(t, rig.stepsRig, repo, revision, "LIMIT")
	dir := filepath.Join(rig.worktrees, id)
	pid := fakePID(t, dir)
	awaitStepStage(t, rig.steps, id, store.StepStageLimited)
	requireGone(t, pid)
	requireStepStage(t, rig.steps, id, store.StepStageLimited, time.Minute)
	rig.restartJudgeRig(t)
	requireStepStage(t, rig.steps, id, store.StepStageLimited, time.Minute)

	// The owner resumes the limited step at two minutes. Its resumed turn plays READDONE, which waits for the release.
	rig.clock.set(2 * time.Minute)
	stepStageWrite(t, filepath.Join(dir, "fakeclaude.next"), "READDONE")
	if err := rig.steps.Resume(t.Context(), id); err != nil {
		t.Fatalf("Resume of the limited step: %v", err)
	}
	requireStepStage(t, rig.steps, id, store.StepStageTurn, 2*time.Minute)
	rig.restartJudgeRig(t)
	requireStepStage(t, rig.steps, id, store.StepStageTurn, 2*time.Minute)

	// The resumed turn ends done at three minutes. Its judge waits for the test, so the judge stage is stored while the
	// judge is called, and the restart during that call keeps the stage and its start.
	rig.clock.set(3 * time.Minute)
	stepStageWrite(t, filepath.Join(dir, "fakeclaude.release"), "")
	awaitStepStage(t, rig.steps, id, store.StepStageJudge)
	requireStepStage(t, rig.steps, id, store.StepStageJudge, 3*time.Minute)
	rig.clock.set(4 * time.Minute)
	rig.restartJudgeRig(t)
	requireStepStage(t, rig.steps, id, store.StepStageJudge, 3*time.Minute)

	// The judge finds the return unmet at five minutes, so the step is sent back. The send-back is stored in turn before it
	// is sent, and it plays HANG, which streams until an interrupt.
	rig.clock.set(5 * time.Minute)
	stepStageWrite(t, filepath.Join(dir, "fakeclaude.next"), "HANG")
	stepJudgeAnswer(t, rig.judgeDir, "unmet", "the summary is missing")
	stepJudgeRelease(t, rig.judgeDir)
	awaitStepStage(t, rig.steps, id, store.StepStageTurn)
	requireStepStage(t, rig.steps, id, store.StepStageTurn, 5*time.Minute)

	// The hold lands at six minutes, once the send-back turn runs, and the held stage starts there.
	rig.clock.set(6 * time.Minute)
	waitUntil(t, "the send-back turn to hold", func() bool {
		err := rig.steps.Hold(t.Context(), id)
		if errors.Is(err, ErrStepNoTurn) {
			return false
		}
		if err != nil {
			t.Fatalf("Hold of the running turn: %v", err)
		}
		return true
	})
	awaitStepStage(t, rig.steps, id, store.StepStageHeld)
	requireStepStage(t, rig.steps, id, store.StepStageHeld, 6*time.Minute)
	rig.clock.set(7 * time.Minute)
	rig.restartJudgeRig(t)
	requireStepStage(t, rig.steps, id, store.StepStageHeld, 6*time.Minute)

	// The owner resumes the held step at eight minutes. Its resumed turn plays READDONE, so the release from the first
	// resumed turn is removed for it to wait on.
	rig.clock.set(8 * time.Minute)
	if err := os.Remove(filepath.Join(dir, "fakeclaude.release")); err != nil {
		t.Fatalf("remove fakeclaude.release: %v", err)
	}
	stepStageWrite(t, filepath.Join(dir, "fakeclaude.next"), "READDONE")
	if err := rig.steps.Nudge(t.Context(), id, "owner resumes"); err != nil {
		t.Fatalf("Nudge of the held step: %v", err)
	}
	requireStepStage(t, rig.steps, id, store.StepStageTurn, 8*time.Minute)
	rig.restartJudgeRig(t)
	requireStepStage(t, rig.steps, id, store.StepStageTurn, 8*time.Minute)

	// The resumed turn ends done at nine minutes. The judge, which has sent the step back once, finds the return met.
	rig.clock.set(9 * time.Minute)
	stepJudgeAnswer(t, rig.judgeDir, "met", "the summary is there now")
	stepStageWrite(t, filepath.Join(dir, "fakeclaude.release"), "")
	waitUntil(t, "the step to end", func() bool { return stepRowOf(t, rig.steps, id).End != "" })
	row := stepRowOf(t, rig.steps, id)
	if row.End != stepLoopEndDone {
		t.Fatalf("End = %q reason %q, want %q", row.End, row.Reason, stepLoopEndDone)
	}
	if len(row.Verdicts) != 2 {
		t.Fatalf("stored %d verdicts, want 2: %+v", len(row.Verdicts), row.Verdicts)
	}
	judgeLoopCheckVerdict(t, row.Verdicts[0], 1, "unmet", "the summary is missing", "continue")
	judgeLoopCheckVerdict(t, row.Verdicts[1], 2, "met", "the summary is there now", "end")
}
