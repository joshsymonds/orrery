package enginehost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepReadoptMetLine is a Haiku reply whose judge verdict is met.
const stepReadoptMetLine = `{"type":"result","subtype":"success","is_error":false,` +
	`"structured_output":{"verdict":"met","reason":"the report is complete"}}`

// stepsRig is a manager and the Steps that start its steps, over one store. The manager's sessions hold their holders
// under root. Gates hold theirs under gateRoot, a directory apart from root, since the manager's Reconcile clears every
// holder under its own state directory that no session it re-adopts owns, which would clear a gate before Readopt
// re-adopts it.
type stepsRig struct {
	manager   *Manager
	steps     *Steps
	store     *store.Store
	path      string
	root      string
	gateRoot  string
	worktrees string
	environ   []string
	haiku     *Haiku
	applier   *crashApplier
}

// newStepsRig returns a rig whose manager runs steps and whose Steps starts them with haiku as its Haiku, nil for none.
// Its store refuses each batch trigger accepts. Its holders are cleared when the test ends.
func newStepsRig(t *testing.T, trigger func([]store.EngineOp) bool, haiku *Haiku) stepsRig {
	t.Helper()
	s, path := openStore(t)
	root, gateRoot := holderRoot(t), holderRoot(t)
	t.Cleanup(func() {
		clearHolderDirs(t, root)
		clearHolderDirs(t, gateRoot)
	})
	applier := &crashApplier{inner: s, trigger: trigger}
	writer := NewWriter(WriterConfig{Applier: applier, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	home := t.TempDir()
	writeStepAgents(t, home)
	rig := stepsRig{
		store: s, path: path, root: root, gateRoot: gateRoot, worktrees: t.TempDir(), haiku: haiku,
		environ: []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}, applier: applier,
	}
	rig.manager = newRigManager(t, rig, writer)
	rig.steps = newRigSteps(t, rig, rig.manager, writer, rig.environ, slog.New(slog.DiscardHandler))
	return rig
}

// newRigManager returns a manager over rig's store and state directory that runs steps through rig's environment, with
// writer. Close runs at the end of the test.
func newRigManager(t *testing.T, rig stepsRig, writer *Writer) *Manager {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	m := NewManager(Config{
		Shell:       sh,
		Claude:      fakeClaudePath(t),
		Environ:     rig.environ,
		Cap:         1,
		StepCap:     1,
		Writer:      writer,
		Now:         fixedClock,
		Notifier:    &fakeSink{},
		Store:       rig.store,
		HolderScope: fakeSystemdRunPath(t),
		StateDir:    rig.root,
	})
	t.Cleanup(m.Close)
	return m
}

// newRigSteps returns a Steps over rig's store that starts its steps through sessions and writes through writer, under
// environ, and logs to logger. Its gates hold their holders in rig's gate root under the fake systemd-run scope. It is
// closed at cleanup.
func newRigSteps(
	t *testing.T, rig stepsRig, sessions stepSessions, writer *Writer, environ []string, logger *slog.Logger,
) *Steps {
	t.Helper()
	steps, err := NewSteps(StepsConfig{
		Sessions:     sessions,
		Store:        rig.store,
		Writer:       writer,
		Worktrees:    rig.worktrees,
		Environ:      environ,
		Logger:       logger,
		Haiku:        rig.haiku,
		GateStateDir: rig.gateRoot,
		GateScope:    fakeSystemdRunPath(t),
	})
	if err != nil {
		t.Fatalf("NewSteps: %v", err)
	}
	t.Cleanup(steps.Close)
	return steps
}

// crash ends the manager of rig as a crash leaves it: the store refuses every later batch, and the manager's sessions
// detach, leaving their holders running.
func (r stepsRig) crash() {
	r.applier.refuse()
	r.manager.Close()
}

// restart returns the manager and Steps a restart starts over rig's store and directories, with a fresh writer. The
// restarted Steps runs under environ and logs to logger.
func (r stepsRig) restart(t *testing.T, environ []string, logger *slog.Logger) (*Manager, *Steps) {
	t.Helper()
	writer := NewWriter(WriterConfig{Applier: r.store, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	m := newRigManager(t, r, writer)
	return m, newRigSteps(t, r, m, writer, environ, logger)
}

// startStalledStep starts a read-only step whose first prompt stalls its turn, so no turn end is stored for it, and
// waits until its unfinished text is stored. It returns the step's id and its worktree.
func (r stepsRig) startStalledStep(t *testing.T) (string, string) {
	t.Helper()
	start := stepStartReadOnly(stepGitTestRepo(t), "")
	start.Prompt = "STALL"
	id, err := r.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start stalled step: %v", err)
	}
	waitUntil(t, "the stalled block's unfinished text", func() bool {
		return countRows(t, r.path, "engine_text") == 1
	})
	return id, filepath.Join(r.worktrees, id)
}

// stepsRunOf returns the run of the loop steps runs for id. The test fails if no loop runs it.
func stepsRunOf(t *testing.T, steps *Steps, id string) *stepRun {
	t.Helper()
	steps.mu.Lock()
	defer steps.mu.Unlock()
	run := steps.runs[id]
	if run == nil {
		t.Fatalf("no loop runs step %s", id)
	}
	return run
}

// newGateScopedSteps returns a Steps over f's store, writer and sessions whose gates hold their holders in root under
// the fake systemd-run scope. It is closed at cleanup.
func (f *stepCtlFixture) newGateScopedSteps(t *testing.T, root string) *Steps {
	t.Helper()
	steps, err := NewSteps(StepsConfig{
		Sessions:     f.sessions,
		Store:        f.store,
		Writer:       f.writer,
		Pools:        f.pools,
		Worktrees:    f.worktrees,
		Environ:      f.environ,
		Logger:       slog.New(slog.DiscardHandler),
		GateStateDir: root,
		GateScope:    fakeSystemdRunPath(t),
	})
	if err != nil {
		t.Fatalf("NewSteps: %v", err)
	}
	t.Cleanup(steps.Close)
	return steps
}

// startScopedGate starts a writing step on steps in a linked worktree that names gate unit. The gate writes its pid and
// sleeps a minute. It returns the step's id and the gate's pid once the gate runs. Cleanup ends the gate's holder in root
// if one is left, so a test that detaches the gate leaves nothing running.
func (f *stepCtlFixture) startScopedGate(t *testing.T, steps *Steps, root string) (string, int) {
	t.Helper()
	linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
	pidFile := filepath.Join(t.TempDir(), "gate.pid")
	gatesFile := filepath.Join(t.TempDir(), "gates.toml")
	script := fmt.Sprintf(`echo $$ > %q; sleep 60`, pidFile)
	stepGateWriteFile(t, gatesFile, stepGateFileText(stepGateShell(t), script, 300))
	id, err := steps.Start(t.Context(), stepGateNamedStart(linked, "unit", gatesFile))
	if err != nil {
		t.Fatalf("Start writing step: %v", err)
	}
	head := stepGitTestRun(t, linked, "rev-parse", "HEAD")
	raw := fmt.Sprintf(`{"status":%q,"commit":%q}`, stepLoopDone, head)
	f.started(t, id).Observer.Return(session.TurnEnded{StructuredOutput: []byte(raw)})
	pid := stepCtlAwaitGatePid(t, pidFile)
	dir := holderDir(t, root, id+"-gate")
	t.Cleanup(func() {
		ctx := context.WithoutCancel(t.Context())
		if _, err := os.Stat(dir); err != nil {
			return
		}
		if err := gateEnd(ctx, dir); err != nil {
			t.Errorf("end gate holder %s: %v", dir, err)
		}
	})
	return id, pid
}

func TestReadoptStepReattachedRunsLoopAndJudgesNextReturnOnce(t *testing.T) {
	haiku := NewHaiku(HaikuConfig{
		Claude:  haikuWriteFake(t, haikuDir(t), haikuFake{stdout: stepReadoptMetLine}),
		Environ: haikuEnviron(),
	})
	rig := newStepsRig(t, nil, haiku)
	id, wt := rig.startStalledStep(t)
	pid := fakePID(t, wt)

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

	requireProcessRunning(t, pid)
	if !restarted.StepLive(id) {
		t.Fatalf("step %s is not live after the restart", id)
	}
	run := stepsRunOf(t, steps, id)
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
		t.Fatalf("stored %d verdicts, want the next return judged once: %+v", len(row.Verdicts), row.Verdicts)
	}
	judgeLoopCheckVerdict(t, row.Verdicts[0], 1, "met", "the report is complete", "end")
}

func TestReadoptStepWhoseHolderIsGoneEndsEngineRestarted(t *testing.T) {
	rig := newStepsRig(t, nil, nil)
	id, wt := rig.startStalledStep(t)
	pid := fakePID(t, wt)

	rig.crash()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), managerGuard)
	defer cancel()
	if err := holder.Stop(ctx, holderDir(t, rig.root, id)); err != nil {
		t.Fatalf("stop holder of step %s: %v", id, err)
	}
	gateWaitGone(t, pid)

	restarted, steps := rig.restart(t, rig.environ, slog.New(slog.DiscardHandler))
	if err := steps.Readopt(t.Context(), restarted); err != nil {
		t.Fatalf("Readopt: %v", err)
	}
	if !restarted.expectsStep(id) {
		t.Fatalf("step %s has no observer registered by Readopt before the manager's Reconcile", id)
	}
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile of the manager: %v", err)
	}
	if err := steps.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile of the steps: %v", err)
	}

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
	if restarted.StepLive(id) {
		t.Errorf("step %s is live after the restart, though its holder ended", id)
	}
	requireStepSlots(t, restarted, 0)
	if restarted.expectsStep(id) {
		t.Errorf("step %s still has an observer after the restart", id)
	}
	requireDirGone(t, holderDir(t, rig.root, id))
}

func TestReadoptStepThatCannotBeRebuiltEndsEngineRestarted(t *testing.T) {
	haiku := NewHaiku(HaikuConfig{
		Claude:  haikuWriteFake(t, haikuDir(t), haikuFake{stdout: stepReadoptMetLine}),
		Environ: haikuEnviron(),
	})
	rig := newStepsRig(t, nil, haiku)
	id, wt := rig.startStalledStep(t)
	pid := fakePID(t, wt)

	rig.crash()
	// A HOME that is not absolute has no stamp to bind the run's haiku to, so the run cannot be rebuilt.
	var logs bytes.Buffer
	environ := []string{"PATH=" + os.Getenv("PATH"), "HOME=relative-home"}
	restarted, steps := rig.restart(t, environ, slog.New(slog.NewTextHandler(&logs, nil)))
	if err := steps.Readopt(t.Context(), restarted); err != nil {
		t.Fatalf("Readopt: %v", err)
	}
	if !strings.Contains(logs.String(), "re-adopt step") {
		t.Errorf("Readopt logged no refusal of step %s: %q", id, logs.String())
	}
	if restarted.expectsStep(id) {
		t.Fatalf("step %s has an observer registered for a run that could not be rebuilt", id)
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

func TestStepsCloseWithGateScopeLeavesGateRunning(t *testing.T) {
	f := newStepCtlFixture(t)
	root := holderRoot(t)
	steps := f.newGateScopedSteps(t, root)
	id, pid := f.startScopedGate(t, steps, root)

	steps.Close()

	if gateProcessGone(pid) {
		t.Fatalf("gate process %d is gone after Close with a gate scope, want it running under its holder", pid)
	}
	if _, err := os.Stat(holderDir(t, root, id+"-gate")); err != nil {
		t.Fatalf("gate holder directory after Close: %v", err)
	}
	open, err := f.store.OpenSteps(t.Context())
	if err != nil {
		t.Fatalf("OpenSteps: %v", err)
	}
	if len(open) != 1 || open[0].ID != id {
		t.Errorf("open step rows after Close = %v, want [%s]", stepCtlRowIDs(open), id)
	}
}

func TestStepsStopKillsGateWhenGateScopeIsSet(t *testing.T) {
	f := newStepCtlFixture(t)
	root := holderRoot(t)
	steps := f.newGateScopedSteps(t, root)
	id, pid := f.startScopedGate(t, steps, root)

	if err := steps.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	gateWaitGone(t, pid)
	requireDirGone(t, holderDir(t, root, id+"-gate"))
	row, err := steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if row.End != stepStoppedEnd {
		t.Errorf("end = %q, want %s", row.End, stepStoppedEnd)
	}
}

func TestStepsReconcileEndsGatedRowAndClearsItsGateHolder(t *testing.T) {
	f := newStepCtlFixture(t)
	root := holderRoot(t)
	steps := f.newGateScopedSteps(t, root)
	id, pid := f.startScopedGate(t, steps, root)
	steps.Close()
	if gateProcessGone(pid) {
		t.Fatalf("gate process %d is gone after Close with a gate scope, so no gate runs to restart over", pid)
	}
	stored, err := f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if stored.GateHolder == "" {
		t.Fatalf("step %s stores no gate holder after Close, so there is none for Reconcile to end", id)
	}

	restarted := f.newGateScopedSteps(t, root)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	gateWaitGone(t, pid)
	requireDirGone(t, holderDir(t, root, id+"-gate"))
	row, err := restarted.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if row.End != stepRestartedEnd {
		t.Errorf("end = %q, want %s", row.End, stepRestartedEnd)
	}
}

func TestReadoptRunRefusesAnAgentGateStep(t *testing.T) {
	steps := &Steps{}
	row := store.StepRow{ID: "0123456789abcdef0123456789abcdef", GateName: "review", ReadOnly: true}
	if _, err := steps.readoptRun(t.Context(), row); !errors.Is(err, errStepAgentGateRestarted) {
		t.Fatalf("readoptRun of an agent gate step = %v, want errStepAgentGateRestarted", err)
	}
	if stepRowRunsAgentGate(store.StepRow{GateName: "check", Gate: []string{"/bin/true"}}) {
		t.Fatal("a named argv gate's step reads as an agent gate step")
	}
	if stepRowRunsAgentGate(store.StepRow{}) {
		t.Fatal("a step with no gate reads as an agent gate step")
	}
}
