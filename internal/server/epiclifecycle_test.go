package server

import (
	"path/filepath"
	"testing"
	"time"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// lifeStep returns a synthetic step of repoa/life labeled task, started minutes before epicsNow, that ended with end
// (empty while it runs).
func lifeStep(id, task, end string, minutes int) *enginev1.Step {
	step := &enginev1.Step{
		Id:      id,
		Epic:    "repoa/life",
		Task:    task,
		End:     end,
		Started: epicsAgo(time.Duration(minutes) * time.Minute),
	}
	if end != "" {
		step.Ended = epicsAgo(time.Duration(minutes-1) * time.Minute)
	}
	return step
}

// lifeOf serves the stored epic repoa/life in phase, with task as its one build task, over steps, and returns the place
// and tone Get answers for the task. adjust, when given, changes the stored epic before it is served.
func lifeOf(
	t *testing.T, phase string, task *enginev1.EpicTask, steps []*enginev1.Step, adjust ...func(*enginev1.StoredEpic),
) (string, string) {
	t.Helper()
	epic := storedEpicOf("repoa/life", phase, epicsAgo(time.Hour))
	epic.Tasks = []*enginev1.EpicTask{task}
	for _, change := range adjust {
		change(epic)
	}
	client := epicStoreServe(
		t,
		t.TempDir(),
		&epicsFake{steps: steps},
		&epicStoreFake{stored: []*enginev1.StoredEpic{epic}},
	)
	got := epicsGet(t, client, "repoa/life").GetBuildTasks()
	if len(got) != 1 {
		t.Fatalf("build tasks = %v, want one", got)
	}
	return got[0].GetPlace(), got[0].GetTone()
}

// TestEpicsGetAcceptedTaskWhoseAttemptEndedGoalUnmetWaitsToIntegrate is the T5 case: the implementer's first attempt
// ended goal_unmet, the retry's green evidence held and its review passed, so the task is accepted. It waits at
// integrate, accepted, and nothing reads the old attempt's end as a failure.
func TestEpicsGetAcceptedTaskWhoseAttemptEndedGoalUnmetWaitsToIntegrate(t *testing.T) {
	t.Parallel()
	green := &enginev1.EvidenceRun{Id: "green-5", Phase: "green", State: "ended", Passed: true}
	task := &enginev1.EpicTask{Id: "T5", Status: "done", Stage: "review", Review: "reviewed", Green: green}
	reviewer := lifeStep("review-5", "T5", "done", 10)
	reviewer.ReadOnly = true
	steps := []*enginev1.Step{
		lifeStep("impl-5a", "T5", "goal_unmet", 60),
		lifeStep("impl-5b", "T5", "done", 40),
		lifeStep("judge-5", "evidence-green-5", "done", 20),
		reviewer,
	}
	if place, tone := lifeOf(t, "building", task, steps); place != "integrate" || tone != "acc" {
		t.Errorf("accepted T5 = %s %s, want integrate acc", place, tone)
	}

	// The same holds when the goal_unmet attempt is the task's latest writing step: an accepted task is accepted.
	steps = []*enginev1.Step{lifeStep("impl-5a", "T5", "goal_unmet", 60), reviewer}
	if place, tone := lifeOf(t, "building", task, steps); place != "integrate" || tone != "acc" {
		t.Errorf("accepted T5 after goal_unmet = %s %s, want integrate acc", place, tone)
	}
}

// TestEpicsGetIntegratedTaskIsIntegrated proves a done task the integrate run passed with is integrated.
func TestEpicsGetIntegratedTaskIsIntegrated(t *testing.T) {
	t.Parallel()
	task := &enginev1.EpicTask{Id: "T1", Status: "done", Stage: "review", Integrated: true}
	if place, tone := lifeOf(t, "built", task, nil); place != "integrated" || tone != "ok" {
		t.Errorf("integrated T1 = %s %s, want integrated ok", place, tone)
	}
}

// TestEpicsGetOpenTaskStandsAtItsStage proves an open task's place is the ring of its build's stage, and that a running
// step of the task gives the tone: run, mine while held, limited while a usage limit holds it.
func TestEpicsGetOpenTaskStandsAtItsStage(t *testing.T) {
	t.Parallel()
	held := lifeStep("impl", "T1", "", 5)
	held.Held = true
	limited := lifeStep("impl", "T1", "", 5)
	limited.Limited = true
	cases := []struct {
		name, stage string
		steps       []*enginev1.Step
		place, tone string
	}{
		{"no stage is planned", "", nil, "planned", "plan"},
		{"red", "red", nil, "red", "run"},
		{
			"implement, its step running",
			"implement",
			[]*enginev1.Step{lifeStep("impl", "T1", "", 5)},
			"implement",
			"run",
		},
		{"orchestrate is implement", "orchestrate", nil, "implement", "run"},
		{"green", "green", nil, "green", "run"},
		{"review", "review", nil, "review", "run"},
		{"verify is review", "verify", nil, "review", "run"},
		{"design is review", "design", nil, "review", "run"},
		{"held step", "implement", []*enginev1.Step{held}, "implement", "mine"},
		{"limited step", "implement", []*enginev1.Step{limited}, "implement", "limited"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			task := &enginev1.EpicTask{Id: "T1", Status: "open", Stage: tc.stage}
			if place, tone := lifeOf(t, "building", task, tc.steps); place != tc.place || tone != tc.tone {
				t.Errorf("T1 = %s %s, want %s %s", place, tone, tc.place, tc.tone)
			}
		})
	}
}

// TestEpicsGetRetryingTaskRuns proves an open task whose latest unit ended goal_unmet or checks_failed, in a live epic,
// runs: the build retries or climbs the ladder, so the end is no failure of the task, whether or not the next unit has
// started.
func TestEpicsGetRetryingTaskRuns(t *testing.T) {
	t.Parallel()
	for _, end := range []string{"goal_unmet", "checks_failed", "failed"} {
		t.Run(end, func(t *testing.T) {
			t.Parallel()
			task := &enginev1.EpicTask{
				Id:      "T2",
				Status:  "open",
				Stage:   "implement",
				Attempt: 2,
				Failure: "synthetic cause",
			}
			ended := lifeStep("impl-a", "T2", end, 30)
			if place, tone := lifeOf(
				t,
				"building",
				task,
				[]*enginev1.Step{ended},
			); place != "implement" ||
				tone != "run" {
				t.Errorf("T2 between attempts = %s %s, want implement run", place, tone)
			}
			retrying := []*enginev1.Step{ended, lifeStep("impl-b", "T2", "", 5)}
			if place, tone := lifeOf(t, "building", task, retrying); place != "implement" || tone != "run" {
				t.Errorf("T2 retrying = %s %s, want implement run", place, tone)
			}
		})
	}
}

// TestEpicsGetGapIsBad proves a gap is bad at the ring of the stage it ended on, and at implement with no stage.
func TestEpicsGetGapIsBad(t *testing.T) {
	t.Parallel()
	task := &enginev1.EpicTask{Id: "T3", Status: "gap", Stage: "green"}
	steps := []*enginev1.Step{lifeStep("impl", "T3", "goal_unmet", 30)}
	if place, tone := lifeOf(t, "building", task, steps); place != "green" || tone != "bad" {
		t.Errorf("gap T3 = %s %s, want green bad", place, tone)
	}
	task = &enginev1.EpicTask{Id: "T3", Status: "gap"}
	if place, tone := lifeOf(t, "building", task, nil); place != "implement" || tone != "bad" {
		t.Errorf("gap T3 with no stage = %s %s, want implement bad", place, tone)
	}
}

// TestEpicsGetHeldTaskIsMine proves an open task with no running step is mine while its build is held and while its
// epic is paused or parked.
func TestEpicsGetHeldTaskIsMine(t *testing.T) {
	t.Parallel()
	steps := []*enginev1.Step{lifeStep("impl", "T4", "goal_unmet", 30)}
	for _, held := range []string{"stopped", "parked", "waiting", "asked"} {
		task := &enginev1.EpicTask{Id: "T4", Status: "open", Stage: "implement", Held: held}
		if place, tone := lifeOf(t, "building", task, steps); place != "implement" || tone != "mine" {
			t.Errorf("T4 held %s = %s %s, want implement mine", held, place, tone)
		}
	}
	task := &enginev1.EpicTask{Id: "T4", Status: "open", Stage: "green"}
	paused := func(epic *enginev1.StoredEpic) { epic.Paused = true }
	if place, tone := lifeOf(t, "building", task, steps, paused); place != "green" || tone != "mine" {
		t.Errorf("T4 of a paused epic = %s %s, want green mine", place, tone)
	}
	parked := func(epic *enginev1.StoredEpic) { epic.Parked = "synthetic park" }
	if place, tone := lifeOf(t, "building", task, steps, parked); place != "green" || tone != "mine" {
		t.Errorf("T4 of a parked epic = %s %s, want green mine", place, tone)
	}
}

// TestEpicsGetEvidenceJudgeStepIsTheTasks proves the judge step of a task's red or green run is the task's running
// step: its hold is the task's.
func TestEpicsGetEvidenceJudgeStepIsTheTasks(t *testing.T) {
	t.Parallel()
	red := &enginev1.EvidenceRun{Id: "red-6", Phase: "red", State: "running"}
	task := &enginev1.EpicTask{Id: "T6", Status: "open", Stage: "red", Red: red}
	judge := lifeStep("judge-6", "evidence-red-6", "", 5)
	judge.ReadOnly = true
	judge.Held = true
	if place, tone := lifeOf(t, "building", task, []*enginev1.Step{judge}); place != "red" || tone != "mine" {
		t.Errorf("T6 with its held judge = %s %s, want red mine", place, tone)
	}
	other := lifeStep("judge-x", "evidence-red-other", "", 5)
	other.Held = true
	if place, tone := lifeOf(t, "building", task, []*enginev1.Step{other}); place != "red" || tone != "run" {
		t.Errorf("T6 with another run's held judge = %s %s, want red run", place, tone)
	}
}

// TestEpicsGetOpenTaskOfAnEndedEpic proves an open task of an epic whose builds no longer run is bad when its last unit
// failed, and plan when it did not.
func TestEpicsGetOpenTaskOfAnEndedEpic(t *testing.T) {
	t.Parallel()
	task := &enginev1.EpicTask{Id: "T7", Status: "open", Stage: "implement"}
	failed := []*enginev1.Step{lifeStep("impl", "T7", "goal_unmet", 30)}
	for _, phase := range []string{"stopped", "ended_with_gaps", "closed_abandoned"} {
		if place, tone := lifeOf(t, phase, task, failed); place != "implement" || tone != "bad" {
			t.Errorf("T7 failed in %s = %s %s, want implement bad", phase, place, tone)
		}
	}
	passed := []*enginev1.Step{lifeStep("impl", "T7", "done", 30)}
	if place, tone := lifeOf(t, "stopped", task, passed); place != "implement" || tone != "plan" {
		t.Errorf("T7 unfailed in stopped = %s %s, want implement plan", place, tone)
	}
	climbed := &enginev1.EpicTask{Id: "T7", Status: "open", Stage: "red", Failure: "synthetic cause"}
	if place, tone := lifeOf(t, "stopped", climbed, nil); place != "red" || tone != "bad" {
		t.Errorf("T7 climbed in stopped = %s %s, want red bad", place, tone)
	}
}

// TestEpicsGetRecordTaskLifecycle proves a record task's place and tone, read from its status and the steps its slug
// labels.
func TestEpicsGetRecordTaskLifecycle(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHead(t, root, "repoa", "life", `{"outcome":"active"}`, epicsNow())
	writeEffortState(t, filepath.Join(root, "repoa", "life"), "1", `{"tasks":[
		{"slug":"done","subject":"S","status":"Done"},
		{"slug":"gap","subject":"S","status":"gap"},
		{"slug":"running","subject":"S","status":"dispatched"},
		{"slug":"reviewing","subject":"S","status":"dispatched"},
		{"slug":"gated","subject":"S","status":"gated"},
		{"slug":"accepted","subject":"S","status":"dispatched"},
		{"slug":"reviewed","subject":"S","status":"dispatched"},
		{"slug":"retrying","subject":"S","status":"dispatched"},
		{"slug":"dispatched","subject":"S","status":"dispatched"},
		{"slug":"pending","subject":"S","status":"pending"}
	]}`)
	reviewing := lifeStep("rev", "reviewing", "", 5)
	reviewing.ReadOnly = true
	reviewed := lifeStep("rev-done", "reviewed", "done", 10)
	reviewed.ReadOnly = true
	steps := []*enginev1.Step{
		lifeStep("gap", "gap", "goal_unmet", 30),
		lifeStep("run", "running", "", 5),
		reviewing,
		lifeStep("acc", "accepted", "done", 20),
		lifeStep("returned", "reviewed", "returned", 30),
		reviewed,
		lifeStep("retry", "retrying", "checks_failed", 20),
	}
	client := epicsServe(t, root, &epicsFake{steps: steps})
	want := map[string][2]string{
		"done":       {"integrated", "ok"},
		"gap":        {"green", "bad"},
		"running":    {"implement", "run"},
		"reviewing":  {"review", "run"},
		"gated":      {"integrate", "acc"},
		"accepted":   {"integrate", "acc"},
		"reviewed":   {"integrate", "acc"},
		"retrying":   {"green", "run"},
		"dispatched": {"implement", "run"},
		"pending":    {"planned", "plan"},
	}
	got := epicsGet(t, client, "repoa/life").GetTasks()
	if len(got) != len(want) {
		t.Fatalf("tasks = %v, want %d", got, len(want))
	}
	for _, task := range got {
		if w := want[task.GetSlug()]; task.GetPlace() != w[0] || task.GetTone() != w[1] {
			t.Errorf("%s = %s %s, want %s %s", task.GetSlug(), task.GetPlace(), task.GetTone(), w[0], w[1])
		}
	}
}
