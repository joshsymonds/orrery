package enginehost

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
)

// restartGated restarts rig's manager and a Steps over its store, as restart does, but with readopt as the Steps'
// ReadoptGate, now as its clock and tick as its watch tick, zero for the default. The Steps is closed at cleanup.
func (r stepsRig) restartGated(
	t *testing.T, readopt func(context.Context, store.StepRow) (*gates.Gate, error), now func() time.Time,
	tick time.Duration,
) (*Manager, *Steps) {
	t.Helper()
	writer := NewWriter(WriterConfig{Applier: r.store, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	m := newRigManager(t, r, writer)
	steps, err := NewSteps(StepsConfig{
		Sessions: m, Store: r.store, Writer: writer, Worktrees: r.worktrees, Environ: r.environ,
		Logger: slog.New(slog.DiscardHandler), Haiku: r.haiku, Now: now, WatchTick: tick,
		GateStateDir: r.gateRoot, GateScope: fakeSystemdRunPath(t), ReadoptGate: readopt,
	})
	if err != nil {
		t.Fatalf("NewSteps: %v", err)
	}
	t.Cleanup(steps.Close)
	return m, steps
}

// startStalledAgentGate starts a read-only step that runs the review agent gate of stepAgentGate, from a gates file,
// with a prompt that stalls its turn, so no turn end is stored for it. It waits until the stalled block's unfinished
// text is stored, and returns the step's id and its worktree.
func startStalledAgentGate(t *testing.T, rig stepsRig) (string, string) {
	t.Helper()
	gatesFile := filepath.Join(t.TempDir(), "gates.toml")
	stepGateWriteFile(t, gatesFile, stepAgentGateText("reviewer"))
	start := stepAgentGateStart(stepGitTestRepo(t), gatesFile)
	start.Prompt = "STALL"
	id, err := rig.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start stalled agent gate step: %v", err)
	}
	waitUntil(t, "the stalled block's unfinished text", func() bool {
		return countRows(t, rig.path, "engine_text") == 1
	})
	return id, filepath.Join(rig.worktrees, id)
}

func TestReadoptAgentGateStepReattachedJudgesNextReturnOnce(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	id, wt := startStalledAgentGate(t, rig)
	pid := fakePID(t, wt)

	rig.crash()
	gate := stepAgentGate("reviewer")
	var asked []store.StepRow
	restarted, steps := rig.restartGated(t, func(_ context.Context, row store.StepRow) (*gates.Gate, error) {
		asked = append(asked, row)
		return &gate, nil
	}, nil, 0)
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

	if len(asked) != 1 || asked[0].ID != id || asked[0].GateName != gate.Name {
		t.Fatalf("ReadoptGate was asked for %d rows, want one for step %s gate %s", len(asked), id, gate.Name)
	}
	requireProcessRunning(t, pid)
	if !restarted.StepLive(id) {
		t.Fatalf("step %s is not live after the restart", id)
	}
	run := stepsRunOf(t, steps, id)
	if run.agentGate == nil || !reflect.DeepEqual(*run.agentGate, stepAgentGate("reviewer")) {
		t.Fatalf("re-adopted run agent gate = %+v, want the gate ReadoptGate returned", run.agentGate)
	}

	run.Return(session.TurnEnded{StructuredOutput: verdictTestJSON(t, stepDefaultDone, verdictTestHolds(), nil)})
	stepStartAwait(t, "step "+id+" to end", func() bool {
		row, err := steps.Status(t.Context(), id)
		return err == nil && row.End != ""
	})

	row, err := steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if row.End != stepLoopEndDone || row.Status != stepDefaultDone || row.Reason != "" {
		t.Errorf("end = %q status %q reason %q, want done done with no reason", row.End, row.Status, row.Reason)
	}
	if row.LoopRound != 1 {
		t.Errorf("loop round = %d, want 1: the next return is the only round", row.LoopRound)
	}
	var assertions []store.StepCheck
	for _, check := range row.Checks {
		if check.Name == stepLoopAssertionsName {
			assertions = append(assertions, check)
		}
	}
	if len(assertions) != 1 || !assertions[0].OK || assertions[0].Round != 1 {
		t.Errorf("assertions checks = %+v, want one passing check of round 1", assertions)
	}
}

func TestReadoptAgentGateStepThatCannotBeRebuiltEndsEngineRestarted(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		readopt func(context.Context, store.StepRow) (*gates.Gate, error)
	}{
		{name: "no ReadoptGate", readopt: nil},
		{name: "nil gate", readopt: func(context.Context, store.StepRow) (*gates.Gate, error) {
			var rebuilt *gates.Gate
			return rebuilt, nil
		}},
		{name: "error", readopt: func(context.Context, store.StepRow) (*gates.Gate, error) {
			return nil, errors.New("the gate definition is gone")
		}},
		{name: "another gate's name", readopt: func(context.Context, store.StepRow) (*gates.Gate, error) {
			gate := stepAgentGate("reviewer")
			gate.Name = "other"
			return &gate, nil
		}},
		{name: "no agent", readopt: func(context.Context, store.StepRow) (*gates.Gate, error) {
			gate := stepAgentGate("")
			return &gate, nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rig := newStepsRig(t, nil)
			id, wt := startStalledAgentGate(t, rig)
			pid := fakePID(t, wt)

			rig.crash()
			restarted, steps := rig.restartGated(t, tc.readopt, nil, 0)
			if err := steps.Readopt(t.Context(), restarted); err != nil {
				t.Fatalf("Readopt: %v", err)
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
			if restarted.StepLive(id) {
				t.Errorf("step %s is live after the restart, though its run was not rebuilt", id)
			}
			requireStepSlots(t, restarted, 0)
		})
	}
}

// readoptTurnStore is a StepStore whose only read is the turn end of a step, which is what readoptRun reads.
type readoptTurnStore struct {
	StepStore
	turn  store.StepTurnEnd
	found bool
}

// StepTurn returns the turn end the store was given.
func (s readoptTurnStore) StepTurn(context.Context, string) (store.StepTurnEnd, bool, error) {
	return s.turn, s.found, nil
}

func TestReadoptRunRefusesAnAgentGateStepWhoseReturnIsStored(t *testing.T) {
	t.Parallel()
	gate := stepAgentGate("reviewer")
	steps := &Steps{
		store: readoptTurnStore{turn: store.StepTurnEnd{Seq: 4, Returned: true}, found: true},
		readoptGate: func(context.Context, store.StepRow) (*gates.Gate, error) {
			return &gate, nil
		},
	}
	row := store.StepRow{ID: "0123456789abcdef0123456789abcdef", GateName: "review", ReadOnly: true}
	if _, err := steps.readoptRun(t.Context(), row); !errors.Is(err, errStepAgentGateRestarted) {
		t.Fatalf("readoptRun of an agent gate step whose return is stored = %v, want errStepAgentGateRestarted", err)
	}
}

func TestReadoptRunKeepsAgentGateStartTime(t *testing.T) {
	t.Parallel()
	gate := stepAgentGate("reviewer")
	started := time.Date(2026, time.November, 14, 9, 0, 0, 0, time.UTC)
	steps := &Steps{
		store: readoptTurnStore{},
		readoptGate: func(context.Context, store.StepRow) (*gates.Gate, error) {
			return &gate, nil
		},
		now: func() time.Time { return started.Add(time.Hour) },
	}
	row := store.StepRow{
		ID: "0123456789abcdef0123456789abcdef", GateName: "review", ReadOnly: true, Started: started,
	}
	run, err := steps.readoptRun(t.Context(), row)
	if err != nil {
		t.Fatalf("readoptRun: %v", err)
	}
	if !run.started.Equal(started) {
		t.Errorf("re-adopted run started at %s, want the step's start %s: its wall-time limit counts from it",
			run.started, started)
	}
}
