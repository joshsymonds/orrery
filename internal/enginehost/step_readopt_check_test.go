package enginehost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// readoptCheckHook is the type of StepsConfig.ReadoptCheck.
type readoptCheckHook = func(ctx context.Context, row store.StepRow) (func(value json.RawMessage) []string, error)

// restartWithReadoptCheck is rig.restart with a Steps whose StepsConfig sets ReadoptCheck to readoptCheck. It is closed
// at cleanup.
func (r stepsRig) restartWithReadoptCheck(
	t *testing.T, environ []string, logger *slog.Logger, readoptCheck readoptCheckHook,
) (*Manager, *Steps) {
	t.Helper()
	writer := NewWriter(WriterConfig{Applier: r.store, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	m := newRigManager(t, r, writer)
	steps, err := NewSteps(StepsConfig{
		Sessions:     m,
		Store:        r.store,
		Writer:       writer,
		Worktrees:    r.worktrees,
		Environ:      environ,
		Logger:       logger,
		Haiku:        r.haiku,
		GateStateDir: r.gateRoot,
		GateScope:    fakeSystemdRunPath(t),
		ReadoptCheck: readoptCheck,
	})
	if err != nil {
		t.Fatalf("NewSteps: %v", err)
	}
	t.Cleanup(steps.Close)
	return m, steps
}

func TestReadoptStepRestoresReturnCheckAndEndsDoneWithoutJudge(t *testing.T) {
	t.Parallel()
	haiku := NewHaiku(HaikuConfig{
		Claude:  haikuWriteFake(t, haikuDir(t), haikuFake{stdout: stepReadoptMetLine}),
		Environ: haikuEnviron(),
	})
	rig := newStepsRig(t, nil, haiku)
	id, wt := rig.startStalledStep(t)
	pid := fakePID(t, wt)
	check := &returnCheckRecorder{}
	var asked []string
	hook := func(_ context.Context, row store.StepRow) (func(json.RawMessage) []string, error) {
		asked = append(asked, row.ID)
		return check.check, nil
	}

	rig.crash()
	restarted, steps := rig.restartWithReadoptCheck(t, rig.environ, slog.New(slog.DiscardHandler), hook)
	if err := steps.Readopt(t.Context(), restarted); err != nil {
		t.Fatalf("Readopt: %v", err)
	}
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile of the manager: %v", err)
	}
	if err := steps.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile of the steps: %v", err)
	}

	requireProcessRunning(t, pid)
	if !restarted.StepLive(id) {
		t.Fatalf("step %s is not live after the restart", id)
	}
	if !reflect.DeepEqual(asked, []string{id}) {
		t.Fatalf("ReadoptCheck asked for rows %q, want only step %s", asked, id)
	}
	run := stepsRunOf(t, steps, id)
	if run.returnCheck == nil {
		t.Fatalf("re-adopted run of step %s has no return check, want the hook's", id)
	}

	run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":"done","note":"refuse"}`)})
	run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":"done","note":"fixed"}`)})
	stepStartAwait(t, "step "+id+" to end", func() bool {
		row, err := steps.Status(t.Context(), id)
		return err == nil && row.End != ""
	})

	row, err := steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if row.End != stepLoopEndDone || row.Status != stepDefaultDone {
		t.Errorf("end = %q status %q, want done done", row.End, row.Status)
	}
	if len(row.Verdicts) != 0 {
		t.Errorf("stored %d verdicts, want none for a step with a return check: %+v", len(row.Verdicts), row.Verdicts)
	}
	wantRefused := store.StepCheck{
		Round: 1, Name: stepLoopReturnName, OK: false, Detail: returnCheckNoFile + "; " + returnCheckNoRequirement,
	}
	if got := stepDoneCheck(t, stepDoneRoundChecks(row.Checks, 1), stepLoopReturnName); got != wantRefused {
		t.Errorf("round 1 return check = %+v, want %+v", got, wantRefused)
	}
	wantPassed := store.StepCheck{Round: 2, Name: stepLoopReturnName, OK: true}
	if got := stepDoneCheck(t, stepDoneRoundChecks(row.Checks, 2), stepLoopReturnName); got != wantPassed {
		t.Errorf("round 2 return check = %+v, want %+v", got, wantPassed)
	}
}

func TestReadoptStepWhoseReturnCheckHookFailsEndsEngineRestarted(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil, nil)
	id, wt := rig.startStalledStep(t)
	pid := fakePID(t, wt)

	rig.crash()
	var logs bytes.Buffer
	hook := func(context.Context, store.StepRow) (func(json.RawMessage) []string, error) {
		return nil, errors.New("no return check for this step")
	}
	restarted, steps := rig.restartWithReadoptCheck(
		t, rig.environ, slog.New(slog.NewTextHandler(&logs, nil)), hook,
	)
	if err := steps.Readopt(t.Context(), restarted); err != nil {
		t.Fatalf("Readopt: %v", err)
	}
	logged := logs.String()
	if !strings.Contains(logged, "re-adopt step") || !strings.Contains(logged, "no return check for this step") {
		t.Errorf("Readopt did not log the hook's error for step %s: %q", id, logged)
	}
	if restarted.expectsStep(id) {
		t.Fatalf("step %s has an observer registered for a run whose return check could not be restored", id)
	}
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile of the manager: %v", err)
	}
	if err := steps.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile of the steps: %v", err)
	}

	gateWaitGone(t, pid)
	row, err := steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if row.End != stepRestartedEnd {
		t.Errorf("end = %q, want %s", row.End, stepRestartedEnd)
	}
	if !stepStartGone(wt) {
		t.Errorf("worktree %s still exists after the restart", wt)
	}
	requireStepSlots(t, restarted, 0)
}

func TestReadoptStepWithoutHookIsRebuiltAsBefore(t *testing.T) {
	t.Parallel()
	haiku := NewHaiku(HaikuConfig{
		Claude:  haikuWriteFake(t, haikuDir(t), haikuFake{stdout: stepReadoptMetLine}),
		Environ: haikuEnviron(),
	})
	rig := newStepsRig(t, nil, haiku)
	id, wt := rig.startStalledStep(t)
	pid := fakePID(t, wt)

	rig.crash()
	restarted, steps := rig.restartWithReadoptCheck(t, rig.environ, slog.New(slog.DiscardHandler), nil)
	if err := steps.Readopt(t.Context(), restarted); err != nil {
		t.Fatalf("Readopt: %v", err)
	}
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile of the manager: %v", err)
	}
	if err := steps.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile of the steps: %v", err)
	}

	requireProcessRunning(t, pid)
	if !restarted.StepLive(id) {
		t.Fatalf("step %s is not live after the restart", id)
	}
	run := stepsRunOf(t, steps, id)
	if run.returnCheck != nil {
		t.Fatalf("re-adopted run of step %s has a return check, want none", id)
	}

	run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":"done"}`)})
	stepStartAwait(t, "step "+id+" to end", func() bool {
		row, err := steps.Status(t.Context(), id)
		return err == nil && row.End != ""
	})

	row, err := steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if row.End != stepDefaultDone || row.Status != stepDefaultDone {
		t.Errorf("end = %q status %q, want done done", row.End, row.Status)
	}
	if len(row.Verdicts) != 1 {
		t.Fatalf("stored %d verdicts, want the return judged once: %+v", len(row.Verdicts), row.Verdicts)
	}
	judgeLoopCheckVerdict(t, row.Verdicts[0], 1, "met", "the report is complete", "end")
}
