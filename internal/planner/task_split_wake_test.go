package planner

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// wakeTwoFilePlan is the planner return of turn 1 of the split tests: one task on R1 that names two files, so that a
// split of its approach can give each file to a descendant.
const wakeTwoFilePlan = `{"status":"done","decisions":[],"tasks":[{"title":"Build it","deliverable":"the runner",` +
	`"files":["internal/planner/runner.go","internal/planner/plan.go"],` +
	`"requirements":["R1"],"gate":"check","evidence":{"capture":["scripts/check"],"limit":60,"artifacts":[],` +
	`"supplied":[],"conclusion":"the runner runs","judge":"a judge","kind":"capture"}}],"outline":"one task",` +
	`"questions":[],"outcome":"work"}`

// wakeSplitReturn returns the planner return of a turn that answers the approach trigger of task 1 with a split: a
// descendant on runner.go that claims R1, and one on plan.go that claims none.
func wakeSplitReturn(t *testing.T) string {
	t.Helper()
	runnerPart := splitPart("Runner", "internal/planner/runner.go")
	planPart := splitPart("Plan", "internal/planner/plan.go")
	planPart["claims"] = []string{}
	encoded, err := json.Marshal(map[string]any{
		"status": "done", "decisions": []string{}, "tasks": []any{}, "outline": "split the task", "questions": []any{},
		"actions": []map[string]any{{
			"kind": store.ActionSplit, "args": splitArgs(t, 1, []map[string]any{runnerPart, planPart}),
			"reason": "the change is too wide for one task",
		}},
		"outcome": "work",
	})
	if err != nil {
		t.Fatalf("encode split return: %v", err)
	}
	return string(encoded)
}

// wakeClimb climbs task T1 of the runner epic to rung, as the build's failure of the rung below it does.
func wakeClimb(t *testing.T, s *store.Store, rung int64) {
	t.Helper()
	build := epicOf(t, s).Tasks[0].Build
	const cause = "rung failed"
	move := store.RungMove{MovedBy: store.MovedByUnit, Cause: cause, To: rung}
	to := store.ClimbedBuild(build, rung, cause)
	err := s.ClimbTask(context.Background(), runnerEpicID, 1, build, to, move, nil, nil, claimedAt())
	if err != nil {
		t.Fatalf("ClimbTask of T1 to rung %d: %v", rung, err)
	}
}

// wakeTurnOne starts the runner epic's first turn, which plans wakeTwoFilePlan, and returns the store, the fake steps
// and the runner once turn 1 has succeeded. Its task T1 is open and holds R1.
func wakeTurnOne(t *testing.T) (*store.Store, *fakeSteps, *Runner) {
	t.Helper()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	steps.watchStore(s)
	var reads atomic.Int64
	runner := runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitFor(t, &steps.changeNotice, "a planner step to start", func() bool { return len(steps.started()) == 1 })
	steps.end("step-1", store.StepRow{End: "done", Result: wakeTwoFilePlan})
	waitFor(t, &steps.changeNotice, "turn 1 to succeed", func() bool {
		return epicOf(t, s).Turns[0].State == "succeeded"
	})
	return s, steps, runner
}

// wakeTurnTwoSettles waits until turn 2 of the runner epic, the approach turn, has left the running state, and fails
// the test unless the turn succeeded, so a refused turn fails the test rather than leaving the wait open.
func wakeTurnTwoSettles(t *testing.T, s *store.Store, steps *fakeSteps) {
	t.Helper()
	waitFor(t, &steps.changeNotice, "turn 2 to settle", func() bool { return epicOf(t, s).Turns[1].State != "running" })
	if state := epicOf(t, s).Turns[1].State; state != "succeeded" {
		t.Fatalf("turn 2 state = %q, want succeeded", state)
	}
}

// wakeArmed fails the test unless the runner epic has one plan state with one pending trigger, which is the wake a
// climb onto rung 4 arms.
func wakeArmed(t *testing.T, s *store.Store) {
	t.Helper()
	states, err := s.PlanStates(context.Background())
	if err != nil || len(states) != 1 || states[0].Pending != 1 {
		t.Fatalf("plan states = %+v (%v), want the one pending trigger of the approach wake", states, err)
	}
}

// wakePending returns the pending triggers of the runner epic, which no planner turn has claimed.
func wakePending(t *testing.T, s *store.Store) []store.EpicTrigger {
	t.Helper()
	triggers, err := s.TurnTriggers(context.Background(), runnerEpicID, 0)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	return triggers
}

// TestTaskSplitApproachSplitReturnPassesCheck checks that the split a turn returns in place of change_approach reads as
// a split of task 1 into two descendants, and that the planner's check accepts the return.
func TestTaskSplitApproachSplitReturnPassesCheck(t *testing.T) {
	t.Parallel()
	ret, err := Parse(json.RawMessage(wakeSplitReturn(t)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := ret.Actions[0]
	if got.Kind != store.ActionSplit || got.Task != 1 || got.ArgsError != "" || len(got.Split) != 2 {
		t.Errorf("Parse split action = %+v, want a split of task 1 into two descendants", got)
	}
	checkLines(t, checkPlan(ret, nil))
}

// TestTaskSplitNoPlannerTurnBelowRungFour checks that a task failing on rungs 1, 2 and 3 adds no planner trigger and so
// no planner turn: the planner has no turn to claim until the climb onto rung 4 adds the approach trigger.
func TestTaskSplitNoPlannerTurnBelowRungFour(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, steps, runner := wakeTurnOne(t)
	for _, rung := range []int64{2, 3} {
		wakeClimb(t, s, rung)
		runner.Pass(ctx)
		runner.WaitTurns()
		if pending := wakePending(t, s); len(pending) != 0 {
			t.Errorf("pending triggers after the climb to rung %d = %+v, want none", rung, pending)
		}
		if states, err := s.PlanStates(ctx); err != nil || len(states) != 0 {
			t.Errorf("plan states after the climb to rung %d = %+v (%v), want none", rung, states, err)
		}
		if got := len(steps.started()); got != 1 {
			t.Errorf("planner steps started after the climb to rung %d = %d, want turn 1's only", rung, got)
		}
		if got := len(epicOf(t, s).Turns); got != 1 {
			t.Errorf("planner turns after the climb to rung %d = %d, want turn 1's only", rung, got)
		}
	}

	wakeClimb(t, s, 4)
	if pending := wakePending(t, s); len(pending) != 1 || pending[0].ID != "T1-approach" {
		t.Errorf("pending triggers after the climb onto rung 4 = %+v, want the one T1-approach trigger", pending)
	}
}

// TestTaskSplitApproachWakeAcceptsSplitAndCarriesItOut checks that the approach trigger of task T1 wakes the planner,
// and that a turn returning split in place of change_approach is accepted and carried out: T1 ends split, and its two
// descendants are open on the turn that split it, with their files and claims.
func TestTaskSplitApproachWakeAcceptsSplitAndCarriesItOut(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, steps, runner := wakeTurnOne(t)
	wakeClimb(t, s, 2)
	wakeClimb(t, s, 3)
	wakeClimb(t, s, 4)
	wakeArmed(t, s)

	passRunner(t, runner, 1)
	waitFor(t, &steps.changeNotice, "the approach wake's planner step to start", func() bool {
		return len(steps.started()) == 2
	})
	if prompt := steps.started()[1].Prompt; !strings.Contains(prompt, "T1-approach") {
		t.Errorf("approach turn prompt does not name the T1-approach trigger:\n%s", prompt)
	}

	steps.end("step-2", store.StepRow{End: "done", Result: wakeSplitReturn(t)})
	wakeTurnTwoSettles(t, s, steps)
	passRunner(t, runner, 1)
	runner.WaitTurns()

	epic := epicOf(t, s)
	if turn := epic.Turns[1]; turn.Outcome != "work" || turn.Outline != "split the task" {
		t.Errorf("turn 2 = %+v, want the split turn's outline and outcome", turn)
	}
	triggers, err := s.TurnTriggers(ctx, runnerEpicID, 2)
	if err != nil {
		t.Fatalf("TurnTriggers of turn 2: %v", err)
	}
	if len(triggers) != 1 || triggers[0].ID != "T1-approach" || triggers[0].State != "consumed" {
		t.Errorf("triggers of turn 2 = %+v, want the T1-approach trigger consumed", triggers)
	}
	if status := epic.Tasks[0].Status; status != "split" {
		t.Errorf("T1 status = %q, want split", status)
	}
	want := []struct {
		seq   int64
		files []string
		reqs  []string
	}{
		{2, []string{"internal/planner/runner.go"}, []string{"R1"}},
		{3, []string{"internal/planner/plan.go"}, nil},
	}
	for _, w := range want {
		task := epic.Tasks[w.seq-1]
		if task.Status != "open" || task.Turn != 2 || task.SplitFrom != 1 ||
			!reflect.DeepEqual(task.Files, w.files) || !reflect.DeepEqual(task.Requirements, w.reqs) {
			t.Errorf("descendant T%d = status %q turn %d split from T%d files %v requirements %v, want open on turn 2 "+
				"split from T1 with files %v and requirements %v", w.seq, task.Status, task.Turn, task.SplitFrom,
				task.Files, task.Requirements, w.files, w.reqs)
		}
	}
	if states, err := s.PlanStates(ctx); err != nil || len(states) != 0 {
		t.Errorf("plan states after the split = %+v (%v), want none", states, err)
	}
}

// TestTaskSplitSurvivesPlannerRestart checks that a new planner runner over the same store, started after the split is
// carried out, leaves the split in place and claims no turn: the descendants stay open and T1 stays split.
func TestTaskSplitSurvivesPlannerRestart(t *testing.T) {
	t.Parallel()
	s, steps, runner := wakeTurnOne(t)
	wakeClimb(t, s, 2)
	wakeClimb(t, s, 3)
	wakeClimb(t, s, 4)
	wakeArmed(t, s)
	passRunner(t, runner, 1)
	waitFor(t, &steps.changeNotice, "the approach wake's planner step to start", func() bool {
		return len(steps.started()) == 2
	})
	steps.end("step-2", store.StepRow{End: "done", Result: wakeSplitReturn(t)})
	wakeTurnTwoSettles(t, s, steps)
	passRunner(t, runner, 1)
	runner.WaitTurns()

	restarted := runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, new(atomic.Int64))))
	restarted.WaitTurns()
	if got := len(steps.started()); got != 2 {
		t.Errorf("planner steps started after the restart = %d, want the two from before it", got)
	}
	epic := epicOf(t, s)
	if got := len(epic.Turns); got != 2 {
		t.Errorf("planner turns after the restart = %d, want the two from before it", got)
	}
	if epic.Tasks[0].Status != "split" || epic.Tasks[1].Status != "open" || epic.Tasks[2].Status != "open" {
		t.Errorf("task statuses after the restart = T1 %q T2 %q T3 %q, want split, open, open",
			epic.Tasks[0].Status, epic.Tasks[1].Status, epic.Tasks[2].Status)
	}
}
