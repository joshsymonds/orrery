package interpret

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// stallWakeCause is the cause of the stall the test epic gets once it has the open task of stallOpenTask: its phase, its
// open task and its running plan run.
const stallWakeCause = "no progress since the last pass; phase accepted; " +
	`open tasks: T1 "Parse the flag" at build stage implement; running node runs: plan`

// stallWakeStore is the store a stall test gives the runner. Each EpicFlow it returns is the store's, changed by edit
// when edit is set, and OpenAmendments returns amending. A test sets them between passes, so each pass reads what the
// test set.
type stallWakeStore struct {
	Store
	edit     func(*store.EpicFlow)
	amending []store.EvidenceAmendment
}

// EpicFlow returns the store's flow of id, changed by edit when it is set.
func (s *stallWakeStore) EpicFlow(ctx context.Context, id string) (store.EpicFlow, error) {
	flow, err := s.Store.EpicFlow(ctx, id)
	if err != nil || s.edit == nil {
		return flow, err
	}
	s.edit(&flow)
	return flow, nil
}

// OpenAmendments returns the amendments the test set.
func (s *stallWakeStore) OpenAmendments(context.Context) ([]store.EvidenceAmendment, error) {
	return s.amending, nil
}

// claimingPlanner records the epics it is given, as fakePlanner does. While claims is set, it also claims a turn through
// the store's ClaimTurn for each of them with a pending trigger, no running turn, no pause and no park, as the planner's
// own pass does. A test sets claims to decide whether a pass leaves the triggers pending.
type claimingPlanner struct {
	*fakePlanner
	t      *testing.T
	store  *store.Store
	claims bool
}

// PassOnly records open's epics as fakePlanner does, then claims a turn for each of them that qualifies, when claims is
// set.
func (p *claimingPlanner) PassOnly(ctx context.Context, open func(epic string) bool) {
	p.fakePlanner.PassOnly(ctx, open)
	if !p.claims {
		return
	}
	states, err := p.store.PlanStates(ctx)
	if err != nil {
		p.t.Errorf("list planner states: %v", err)
		return
	}
	for _, state := range states {
		if !open(state.Epic) || state.Pending == 0 || state.Running || state.Paused || state.Parked != "" {
			continue
		}
		if _, err = p.store.ClaimTurn(ctx, state.Epic, runClock()); err != nil {
			p.t.Errorf("claim turn of %s: %v", state.Epic, err)
		}
	}
}

// stallRig is a runner over a store that holds the test epic, pinned to planBuildPin, whose plan run is running. Its
// planner has had its first turn, which returned wait, so no trigger is pending and nothing else is under way.
type stallRig struct {
	store   *store.Store
	wake    *stallWakeStore
	planner *claimingPlanner
	runner  *Runner
}

// newStallRig returns a rig at rest under a running plan run. The pass that starts the plan run gives the planner its
// first turn, which claims the epic-created trigger, and the turn then ends as a wait.
func newStallRig(t *testing.T) stallRig {
	t.Helper()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, planBuildPin())
	wake := &stallWakeStore{Store: s}
	planner := &claimingPlanner{fakePlanner: &fakePlanner{known: []string{runEpic}}, t: t, store: s, claims: true}
	runner := New(Config{Store: wake, Planner: planner, Builder: &fakeBuilder{}, Now: runClock})
	runner.Pass(ctx)
	if !hasRunningPlan(runsOf(t, s)) {
		t.Fatal("the pass that starts the plan run left no plan run running")
	}
	turns, err := s.RunningTurns(ctx)
	if err != nil || len(turns) != 1 {
		t.Fatalf("running turns after the first pass = %v, %v; want the first turn", turns, err)
	}
	wait := store.EpicPlan{Outcome: "wait", Outline: "wait"}
	if err = s.CompleteTurn(ctx, runEpic, turns[0].Seq, wait, runClock()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	planner.claims = false
	return stallRig{store: s, wake: wake, planner: planner, runner: runner}
}

// stallOpenTask gives the test epic an open task, T1. A task of turn 0 is at or below the plan run's mark of 0, so the
// plan starts no build for it, and the task stays open with nothing running for it.
func stallOpenTask(flow *store.EpicFlow) {
	flow.Epic.Tasks = []store.EpicTask{{
		Seq: 1, Title: "Parse the flag", Status: taskOpen, Build: store.TaskBuild{Stage: "implement"},
	}}
}

// stallCausesOf returns the cause of each stall trigger of the test epic, in the order the store lists the triggers. A
// stall trigger's id starts with stall:, and the epic's other triggers are not stalls.
func stallCausesOf(t *testing.T, s *store.Store) []string {
	t.Helper()
	history, err := s.EpicHistory(context.Background(), runEpic)
	if err != nil {
		t.Fatalf("EpicHistory of %s: %v", runEpic, err)
	}
	causes := make([]string, 0, len(history.Triggers))
	for _, trigger := range history.Triggers {
		if strings.HasPrefix(trigger.ID, "stall:") {
			causes = append(causes, trigger.Cause)
		}
	}
	return causes
}

// TestEpicStallWakesIdleEpicGetsOneStallTriggerThePlannerClaims checks that an idle epic under a running plan run gets
// one pending stall trigger, whose cause names its phase, open task and running node run, with the stall recorded at
// the runner's clock, and that the planner claims the trigger in its turn on the following pass.
func TestEpicStallWakesIdleEpicGetsOneStallTriggerThePlannerClaims(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rig := newStallRig(t)
	if got := stallCausesOf(t, rig.store); len(got) != 0 {
		t.Fatalf("stall causes before the idle pass = %q, want none", got)
	}

	rig.wake.edit = stallOpenTask
	rig.runner.Pass(ctx)
	if want := []string{stallWakeCause}; !reflect.DeepEqual(stallCausesOf(t, rig.store), want) {
		t.Errorf("stall causes = %q, want %q", stallCausesOf(t, rig.store), want)
	}
	latest, ok, err := rig.store.LatestStall(ctx, runEpic)
	if err != nil || !ok {
		t.Fatalf("LatestStall = %v, %v; want the stall the pass wrote", ok, err)
	}
	wantStall := store.EpicStall{Seq: 1, At: runClock(), Mark: store.StallMark{Runs: 1}}
	if !reflect.DeepEqual(latest, wantStall) {
		t.Errorf("LatestStall = %+v, want %+v", latest, wantStall)
	}

	// The planner's first turn is turn 1, so the stall is claimed by turn 2.
	rig.planner.claims = true
	rig.runner.Pass(ctx)
	triggers, err := rig.store.TurnTriggers(ctx, runEpic, 2)
	if err != nil {
		t.Fatalf("TurnTriggers of turn 2: %v", err)
	}
	claimed := make([]string, 0, len(triggers))
	for _, trigger := range triggers {
		claimed = append(claimed, trigger.ID)
	}
	if !slices.Contains(claimed, "stall:1") {
		t.Errorf("triggers claimed by turn 2 = %q, want stall:1: the planner claims the stall on the following pass",
			claimed)
	}
	if got := stallCausesOf(t, rig.store); len(got) != 1 {
		t.Errorf("stall causes after the claim = %q, want the one stall", got)
	}
}

// TestEpicStallWakesBusyStatesKeepAnIdleEpicFromStalling checks that none of the states that keep an epic from being
// idle gives it a stall trigger. Each is set over the rows of an epic that is otherwise idle under a running plan run,
// so the pass that reads them finds the one busy state and nothing else.
func TestEpicStallWakesBusyStatesKeepAnIdleEpicFromStalling(t *testing.T) {
	t.Parallel()
	busy := []struct {
		name     string
		edit     func(*store.EpicFlow)
		amending []store.EvidenceAmendment
	}{
		{name: "a running step", edit: func(flow *store.EpicFlow) {
			flow.Steps = append(flow.Steps, store.StepRow{ID: "step-running", Epic: runEpic})
		}},
		{name: "a limited step", edit: func(flow *store.EpicFlow) {
			flow.Steps = append(flow.Steps, store.StepRow{
				ID: "step-limited", Epic: runEpic, LoopStage: store.StepStageLimited,
			})
		}},
		{name: "a running node check", edit: func(flow *store.EpicFlow) {
			flow.Checks = append(flow.Checks, store.NodeCheck{
				ID: "check-running", Epic: runEpic, Path: "done/tests", State: store.NodeRunning,
			})
		}},
		{name: "a running evidence run", edit: func(flow *store.EpicFlow) {
			flow.Evidence = append(flow.Evidence, store.EvidenceRun{
				ID: "evidence-running", Epic: runEpic, Item: "R1", State: "capturing",
			})
		}},
		{name: "an amendment checking", amending: []store.EvidenceAmendment{
			{Epic: runEpic, Seq: 1, Requirement: "R1", State: store.AmendmentChecking},
		}},
		{name: "an amendment capturing", amending: []store.EvidenceAmendment{
			{Epic: runEpic, Seq: 1, Requirement: "R1", State: store.AmendmentCapturing},
		}},
		{name: "a pending planner action", edit: func(flow *store.EpicFlow) { flow.Actions = 1 }},
		{name: "a pending trigger under a running plan", edit: func(flow *store.EpicFlow) { flow.Pending = 1 }},
		{name: "an open question", edit: func(flow *store.EpicFlow) { flow.Epic.OpenQuestions = 1 }},
		{name: "a running planner turn", edit: func(flow *store.EpicFlow) {
			flow.Epic.Turns = []store.EpicTurn{{Epic: runEpic, Seq: 2, State: "running"}}
		}},
		{name: "a pause", edit: func(flow *store.EpicFlow) { flow.Epic.Paused = true }},
		{name: "a park", edit: func(flow *store.EpicFlow) { flow.Epic.Parked = "waiting for the owner" }},
		{name: "the met_at_base phase", edit: setPhase(phaseMetAtBase)},
		{name: "the ended_with_gaps phase", edit: setPhase(phaseEndedWithGaps)},
		{name: "the done phase", edit: setPhase(phaseDone)},
		{name: "the released phase", edit: setPhase(phaseReleased)},
		{name: "the stopped phase", edit: setPhase(store.PhaseStopped)},
	}
	for _, tc := range busy {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rig := newStallRig(t)
			rig.wake.edit = tc.edit
			rig.wake.amending = tc.amending
			rig.runner.Pass(context.Background())
			if got := stallCausesOf(t, rig.store); len(got) != 0 {
				t.Errorf("stall causes = %q, want none: the epic is not idle", got)
			}
		})
	}
}

// setPhase returns an edit that sets the epic's phase to phase.
func setPhase(phase string) func(*store.EpicFlow) {
	return func(flow *store.EpicFlow) { flow.Epic.Phase = phase }
}

// TestEpicStallWakesNoSecondStallWhileTheFirstIsPending checks that later passes add no stall while the first one is
// pending: the epic is not idle while its trigger waits under its running plan run.
func TestEpicStallWakesNoSecondStallWhileTheFirstIsPending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rig := newStallRig(t)
	rig.wake.edit = stallOpenTask
	rig.runner.Pass(ctx)
	rig.runner.Pass(ctx)
	rig.runner.Pass(ctx)

	if want := []string{stallWakeCause}; !reflect.DeepEqual(stallCausesOf(t, rig.store), want) {
		t.Errorf("stall causes after three passes = %q, want %q", stallCausesOf(t, rig.store), want)
	}
	latest, _, err := rig.store.LatestStall(ctx, runEpic)
	if err != nil {
		t.Fatalf("LatestStall: %v", err)
	}
	if latest.Seq != 1 {
		t.Errorf("latest stall seq = %d, want 1: no second stall while the first is pending", latest.Seq)
	}
}

// stallBuildTask gives the test epic an open task, T1, of turn 1, which its plan run builds: the plan starts a build run
// for it, and the pass hands that run to the builder.
func stallBuildTask(flow *store.EpicFlow) {
	flow.Epic.Tasks = []store.EpicTask{{
		Seq: 1, Turn: 1, Title: "Parse the flag", Status: taskOpen, Build: store.TaskBuild{Stage: "implement"},
	}}
}

// TestEpicStallWakesBuildHandedToTheBuilderIsNotStalled checks that a pass which hands a build run to the builder starts
// work for its epic, so the running build run does not stall the epic while the builder works on it.
func TestEpicStallWakesBuildHandedToTheBuilderIsNotStalled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rig := newStallRig(t)
	rig.wake.edit = stallBuildTask
	for range 2 {
		rig.runner.Pass(ctx)
		if got := stallCausesOf(t, rig.store); len(got) != 0 {
			t.Errorf("stall causes = %q, want none while the builder works on the task", got)
		}
	}
	runs := runsOf(t, rig.store)
	if !slices.ContainsFunc(runs, func(run store.NodeRun) bool {
		return run.Path == "plan/build/T1" && run.State == store.NodeRunning
	}) {
		t.Errorf("node runs = %+v, want the build run of T1 running", runs)
	}
}
