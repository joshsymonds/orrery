package interpret

import (
	"context"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// misfireWorkID is the id the misfire tests store their staged check and evidence run under: 32 lowercase hex digits,
// as the check and evidence services give their work.
const misfireWorkID = "0123456789abcdef0123456789abcdef"

// requireNoMisfire fails the test unless the test epic is not parked and has exactly stalls stall rows, none parked,
// and as many stall triggers. moment names the point of the test the check is made at.
func requireNoMisfire(t *testing.T, s *store.Store, stalls int64, moment string) {
	t.Helper()
	latest, ok, err := s.LatestStall(context.Background(), runEpic)
	if err != nil {
		t.Fatalf("%s: LatestStall: %v", moment, err)
	}
	switch {
	case stalls == 0 && ok:
		t.Errorf("%s: latest stall = %+v, want none", moment, latest)
	case stalls > 0 && (!ok || latest.Seq != stalls || latest.Parked):
		t.Errorf("%s: latest stall = %+v (found %v), want stall %d not parked", moment, latest, ok, stalls)
	}
	if causes := stallCausesOf(t, s); int64(len(causes)) != stalls {
		t.Errorf("%s: stall causes = %q, want %d", moment, causes, stalls)
	}
	if row := epicOf(t, s); row.Parked != "" {
		t.Errorf("%s: epic parked with %q (class %q), want it not parked", moment, row.Parked, row.ParkClass)
	}
}

// TestEpicStallMisfireGatewayIntegrateMidBatch reproduces the epic the stall check misfired on. Three tasks are
// planned: first and second both change b.go, and third depends on second. Once first and second are accepted the
// batch composes first and leaves second out on its conflict at b.go, so second's task integrator round 1 runs as a
// step labeled with the batch's path, and third's build waits on second. Neither the walk nor the builder starts
// anything while that step runs, and the step is no row the decisions read. The test passes the epic through
// Runner.Pass at each moment the check misfired at, and the epic gets no stall trigger and is not parked:
//   - while the round's step runs, with no stall before it;
//   - while it runs after an earlier stall at the mark the epic has now, which the planner's turn waited on;
//   - when the round's Done gate check, under way as the walk decided, ends before the stall check reads the epic.
//
// The pass after the check's end then carries the next action, the captures at the round's commit.
func TestEpicStallMisfireGatewayIntegrateMidBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newIntegrateWorld(t, pinnedTo(builtinPin(t)), viaRunnerPass())
	w.mergeIdentity()
	w.runner.stateDir = t.TempDir()
	w.runner.getenv = integratorRoles(t)
	planner, ok := w.runner.planner.(*fakePlanner)
	if !ok {
		t.Fatalf("planner = %T, want the world's *fakePlanner", w.runner.planner)
	}

	w.pass()
	w.endRed()
	w.pass()
	endCost(t, w.s)
	w.pass()
	first := w.commit(w.base, "b.go", "one\n")
	second := w.commit(w.base, "b.go", "two\n")
	third := runTask("third", "c.go")
	third.DependsOn = []string{"second"}
	completeTurn(t, w.s, store.EpicPlan{
		Decisions: []string{}, Tasks: []store.EpicTaskInput{runTask("first", "b.go"), runTask("second", "b.go"), third},
		Outline: "three tasks", Outcome: "complete",
	})
	w.pass()
	w.accept(1, w.base, first)
	w.accept(2, w.base, second)
	w.pass()

	rw := roundWorld{integrateWorld: w, second: second}
	batch := runAt(t, w.s, integrateBatch)
	if batch.State != store.NodeRunning || !strings.Contains(batch.Result, `"task":2`) ||
		!strings.Contains(batch.Result, `"conflicts":["b.go"]`) {
		t.Fatalf("batch = %+v, want it running with task 2 left out on its conflict at b.go", batch)
	}
	round, err := w.s.Step(ctx, rw.roundID(1))
	if err != nil {
		t.Fatalf("Step of round 1: %v", err)
	}
	if round.End != "" || round.Epic != runEpic || round.Task != integrateBatch {
		t.Fatalf("round 1 step = %+v, want it running, labeled with the epic and %s", round, integrateBatch)
	}
	if build := runAt(t, w.s, "plan/build/T3"); build.State != store.NodeRunning || taskStatus(t, w.s, 3) != taskOpen {
		t.Fatalf("build of task 3 = %+v, want it running on its open task, waiting on task 2", build)
	}
	requireNoMisfire(t, w.s, 0, "the pass that left task 2 out")

	w.pass()
	requireNoMisfire(t, w.s, 0, "a pass while round 1's step runs")

	// An earlier stall at the mark the epic has now, whose trigger the planner's turn claimed and waited on: the next
	// idle pass would park the epic as one whose last stall changed nothing.
	mark, err := w.s.StallMark(ctx, runEpic)
	if err != nil {
		t.Fatalf("StallMark: %v", err)
	}
	if err = w.s.WakeStall(ctx, runEpic, "an earlier stall", mark, runClock()); err != nil {
		t.Fatalf("WakeStall: %v", err)
	}
	completeTurn(t, w.s, store.EpicPlan{Decisions: []string{}, Outline: "wait", Outcome: "wait"})
	if now, markErr := w.s.StallMark(ctx, runEpic); markErr != nil || now != mark {
		t.Fatalf("stall mark after the waiting turn = %+v, %v; want the earlier stall's %+v", now, markErr, mark)
	}
	w.pass()
	requireNoMisfire(t, w.s, 1, "a pass while round 1's step runs after an unchanged stall")

	head := rw.mergeRound(rw.roundStart(1), "both\n")
	endTaskStep(t, w.s, rw.roundID(1), "done", "done", taskReturn(verdictMerged, head, "b.go keeps both lines"))
	w.pass()
	gate := rw.gateAt(head)
	if check, checkErr := w.s.NodeCheck(ctx, gate); checkErr != nil || check.State != store.NodeRunning {
		t.Fatalf("Done gate check of round 1 = %+v, %v; want it running", check, checkErr)
	}
	requireNoMisfire(t, w.s, 1, "the pass that recorded round 1 and started its gate")

	// The check ends once the walk decided on it running and before the stall check reads the epic: the planner is
	// passed between the two.
	planner.afterPass = func() {
		planner.afterPass = nil
		nodeEndCheck(t, w.s, gate, 0, "")
	}
	w.pass()
	if check, checkErr := w.s.NodeCheck(ctx, gate); checkErr != nil || check.State == store.NodeRunning {
		t.Fatalf("Done gate check of round 1 = %+v, %v; want it ended during the pass", check, checkErr)
	}
	requireNoMisfire(t, w.s, 1, "a pass whose gate check ended between its walk and its stall check")

	w.pass()
	rw.captureAt(head)
	requireNoMisfire(t, w.s, 1, "the pass that started the captures at round 1's commit")
}

// stageStep returns a stage that stores a running step labeled with the test epic and task.
func stageStep(id, task string) func(*testing.T, *store.Store) {
	return func(t *testing.T, s *store.Store) {
		t.Helper()
		add := store.StepAdd{
			ID: id, Agent: "implementer", Prompt: "work for the epic", Schema: `{"type":"object"}`,
			Workspace: runRepo, Revision: runBase, DoneValue: "done", Epic: runEpic, Task: task,
		}
		if err := s.ApplyEngine(context.Background(), []store.EngineOp{add}, runClock()); err != nil {
			t.Fatalf("store step %s: %v", id, err)
		}
	}
}

// stageCapture stores a capturing evidence run of R1, which the test epic does not hold, so its kept spec is not current
// with any item the epic holds: a run whose item changed after it started.
func stageCapture(t *testing.T, s *store.Store) {
	t.Helper()
	run := store.EvidenceRun{
		ID: misfireWorkID, Epic: runEpic, Item: "R1", Revision: runBase, Phase: evidence.PhaseGreen,
		Source: evidence.SourceCaptured, Created: runClock(),
	}
	if err := s.AddEvidenceRun(context.Background(), run); err != nil {
		t.Fatalf("AddEvidenceRun: %v", err)
	}
}

// TestEpicStallMisfireWorkTheDecidedRowsLeaveOut checks that work of the epic under way that the rows its node runs are
// decided on leave out keeps an otherwise idle epic under a running plan run from stalling. Each case stores one such
// row: a step labeled with the epic whose task label is no review, closure or release run, or an evidence run whose
// item changed after it started. The same epic without the row gets a stall (see
// TestEpicStallWakesIdleEpicGetsOneStallTriggerThePlannerClaims).
func TestEpicStallMisfireWorkTheDecidedRowsLeaveOut(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		stage func(*testing.T, *store.Store)
	}{
		{name: "a task integrator's step, labeled with its integrate run", stage: stageStep(
			"step-task-integrator", "plan/S1/integrate",
		)},
		{name: "a build's step, labeled with its task", stage: stageStep("step-build", "T1")},
		{name: "an evidence judge's step, labeled with its run", stage: stageStep(
			"step-judge", "evidence-"+misfireWorkID,
		)},
		{name: "a step a conversation started for the epic, with no task", stage: stageStep("step-conversation", "")},
		{name: "an evidence run whose item changed after it started", stage: stageCapture},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rig := newStallRig(t)
			rig.wake.edit = stallOpenTask
			tc.stage(t, rig.store)
			rig.runner.Pass(context.Background())
			requireNoMisfire(t, rig.store, 0, "a pass with the work under way")
		})
	}
}

// TestEpicStallMisfireWorkEndingBetweenTheWalkAndTheStallCheck checks that work of the epic that is under way when the
// pass's walk decides on the epic, and ends before the pass's stall check reads it, keeps the epic from stalling that
// pass: the walk waited on the work, and the pass after its end decides on it. The work ends as the planner is passed,
// which is between the walk and the stall check. The next pass finds the epic idle and gives it its stall, so an idle
// epic is still caught.
func TestEpicStallMisfireWorkEndingBetweenTheWalkAndTheStallCheck(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		stage func(*testing.T, *store.Store)
		end   func(*testing.T, *store.Store)
	}{
		{
			name: "a node check",
			stage: func(t *testing.T, s *store.Store) {
				t.Helper()
				check := store.NodeCheck{
					ID: misfireWorkID, Epic: runEpic, Path: "done/tests", Revision: runBase, Argv: []string{"true"},
				}
				if err := s.AddNodeCheck(context.Background(), check, runClock()); err != nil {
					t.Fatalf("AddNodeCheck: %v", err)
				}
			},
			end: func(t *testing.T, s *store.Store) {
				t.Helper()
				nodeEndCheck(t, s, misfireWorkID, 0, "")
			},
		},
		{
			name:  "a step",
			stage: stageStep("step-ending", "plan/S1/integrate"),
			end: func(t *testing.T, s *store.Store) {
				t.Helper()
				nodeEndStep(t, s, "step-ending")
			},
		},
		{
			name:  "an evidence run",
			stage: stageCapture,
			end: func(t *testing.T, s *store.Store) {
				t.Helper()
				nodeEndEvidence(t, s, misfireWorkID)
			},
		},
		{
			name: "a planner turn",
			stage: func(t *testing.T, s *store.Store) {
				t.Helper()
				ctx := context.Background()
				trigger := store.NodeTrigger{ID: "workflow:misfire", Cause: "workflow node done/tests ended"}
				if err := s.StartNodes(ctx, runEpic, nil, &trigger, runClock()); err != nil {
					t.Fatalf("StartNodes of the turn's trigger: %v", err)
				}
				if _, err := s.ClaimTurn(ctx, runEpic, runClock()); err != nil {
					t.Fatalf("ClaimTurn: %v", err)
				}
			},
			end: endRunningTurn,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			rig := newStallRig(t)
			rig.wake.edit = stallOpenTask
			tc.stage(t, rig.store)
			rig.planner.afterPass = func() {
				rig.planner.afterPass = nil
				tc.end(t, rig.store)
			}
			rig.runner.Pass(ctx)
			if rig.planner.afterPass != nil {
				t.Fatal("the pass did not pass the planner, so the work did not end between its walk and stall check")
			}
			requireNoMisfire(t, rig.store, 0, "the pass the work ended in")

			rig.runner.Pass(ctx)
			if got := stallCausesOf(t, rig.store); len(got) != 1 || got[0] != stallWakeCause {
				t.Errorf("stall causes after the next pass = %q, want the one stall %q: the idle epic is caught", got,
					stallWakeCause)
			}
		})
	}
}
