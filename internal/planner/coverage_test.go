package planner

import (
	"context"
	"encoding/json"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// coverageTask is a task of a return that claims requirements and contributes to contributes. It has the fields of
// validTask.
func coverageTask(title string, requirements, contributes []string) Task {
	task := validTask(title)
	task.Requirements = requirements
	task.Contributes = contributes
	return task
}

// coverageReturn is a return of outcome that carries tasks.
func coverageReturn(outcome string, tasks ...Task) Return {
	return Return{Status: "done", Outcome: outcome, Tasks: tasks}
}

// storedClaim is a stored task of seq and status that claims requirements.
func storedClaim(seq int64, status string, requirements ...string) store.EpicTask {
	return store.EpicTask{Seq: seq, Title: "stored", Requirements: requirements, Status: status}
}

func TestCoverageLines(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		tasks []store.EpicTask
		ret   Return
		want  []string
	}{
		{
			name: "passing work plan with a contribution claimed by a later task",
			ret: coverageReturn("work",
				coverageTask("A", []string{"R1"}, []string{"R2"}),
				coverageTask("B", []string{"R2"}, nil)),
		},
		{
			name:  "passing complete plan over a stored done claim and a contribution to it",
			tasks: []store.EpicTask{storedClaim(1, "done", "R1")},
			ret:   coverageReturn("complete", coverageTask("A", []string{"R2"}, []string{"R1"})),
		},
		{
			name:  "failed stored claim does not count toward a complete plan",
			tasks: []store.EpicTask{storedClaim(2, "failed", "R1")},
			ret: coverageReturn("complete",
				coverageTask("A", []string{"R1"}, nil),
				coverageTask("B", []string{"R2"}, nil)),
		},
		{
			name:  "failed stored claim does not satisfy a contribution",
			tasks: []store.EpicTask{storedClaim(2, "failed", "R1")},
			ret:   coverageReturn("work", coverageTask("A", []string{"R2"}, []string{"R1"})),
			want:  []string{`tasks[0] "A" contributes[0]: R1 is claimed by no task`},
		},
		{
			name:  "live stored claim refuses a claim of the return",
			tasks: []store.EpicTask{storedClaim(3, "open", "R1")},
			ret:   coverageReturn("work", coverageTask("A", []string{"R1"}, nil)),
			want:  []string{`tasks[0] "A" requirements[0]: R1 is claimed by T3`},
		},
		{
			name: "lowest seq names the stored claimant",
			tasks: []store.EpicTask{
				storedClaim(4, "open", "R1"),
				storedClaim(2, "done", "R1"),
			},
			ret:  coverageReturn("work", coverageTask("A", []string{"R1"}, nil)),
			want: []string{`tasks[0] "A" requirements[0]: R1 is claimed by T2`},
		},
		{
			name: "a claim of an earlier task of the return refuses a second claim",
			ret: coverageReturn("work",
				coverageTask("A", []string{"R1"}, nil),
				coverageTask("B", []string{"R1"}, nil)),
			want: []string{`tasks[1] "B" requirements[0]: R1 is claimed by tasks[0]`},
		},
		{
			name: "contribution to a Requirement outside the epic",
			ret:  coverageReturn("work", coverageTask("A", []string{"R1"}, []string{"R9"})),
			want: []string{`tasks[0] "A" contributes[0]: not a Requirement of the epic`},
		},
		{
			name: "contribution to the task's own claim",
			ret:  coverageReturn("work", coverageTask("A", []string{"R1"}, []string{"R1"})),
			want: []string{`tasks[0] "A" contributes[0]: R1 is claimed by this task`},
		},
		{
			name: "contribution with no claim anywhere",
			ret:  coverageReturn("work", coverageTask("A", []string{"R1"}, []string{"R2"})),
			want: []string{`tasks[0] "A" contributes[0]: R2 is claimed by no task`},
		},
		{
			name: "unknown Requirement claim is Check's refusal, not repeated",
			ret:  coverageReturn("work", coverageTask("A", []string{"R9"}, nil)),
		},
		{
			name: "lines in task order, then field order",
			ret: coverageReturn("complete",
				coverageTask("A", []string{"R1", "R2"}, []string{"R9"}),
				coverageTask("B", []string{"R1"}, []string{"R1"})),
			want: []string{
				`tasks[0] "A" contributes[0]: not a Requirement of the epic`,
				`tasks[1] "B" requirements[0]: R1 is claimed by tasks[0]`,
				`tasks[1] "B" contributes[0]: R1 is claimed by this task`,
			},
		},
		{
			name: "complete leaves a Requirement unclaimed",
			ret:  coverageReturn("complete", coverageTask("A", []string{"R1"}, nil)),
			want: []string{`outcome complete: R2 is claimed by no task`},
		},
		{
			name: "complete with nothing claimed names each Requirement in contract order",
			ret:  coverageReturn("complete"),
			want: []string{
				`outcome complete: R1 is claimed by no task`,
				`outcome complete: R2 is claimed by no task`,
			},
		},
		{
			name: "complete names an unclaimed Requirement after the task lines",
			ret:  coverageReturn("complete", coverageTask("A", []string{"R2"}, []string{"R1"})),
			want: []string{
				`tasks[0] "A" contributes[0]: R1 is claimed by no task`,
				`outcome complete: R1 is claimed by no task`,
			},
		},
		{
			name: "work leaves a Requirement unclaimed",
			ret:  coverageReturn("work", coverageTask("A", []string{"R1"}, nil)),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Coverage(tc.ret, epicRequirements(), tc.tasks)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Coverage() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReturnCheckAppendsCoverageLines(t *testing.T) {
	t.Parallel()
	epic := store.EpicRow{
		Requirements: []store.EpicRequirement{{ID: "R1"}, {ID: "R2"}},
		Tasks:        []store.EpicTask{storedClaim(1, "open", "R1")},
	}
	check := returnCheck(context.Background(), epic, testResolver{})
	const plan = `{"status":"done","decisions":[],"tasks":[{"title":"Touch a","deliverable":"d",` +
		`"files":["internal/a.go"],"requirements":["R1","R9"],"gate":"check",` +
		`"evidence":{"capture":["scripts/check"],"limit":60,"artifacts":[],"supplied":[],"conclusion":"c",` +
		`"judge":"a judge"}}],"outline":"o","questions":[],"outcome":"work"}`

	want := []string{
		`tasks[0] "Touch a" requirements[1]: not a Requirement of the epic`,
		`tasks[0] "Touch a" requirements[0]: R1 is claimed by T1`,
	}
	if got := check(json.RawMessage(plan)); !reflect.DeepEqual(got, want) {
		t.Errorf("ReturnCheck of a plan refused by Check and by coverage = %q, want %q", got, want)
	}
}

// contributingPlan is a planner return of two tasks: the first claims R1 and contributes to R2, which the second
// claims.
const contributingPlan = `{"status":"done","decisions":[],"tasks":[` +
	`{"title":"Build it","deliverable":"the runner","files":["internal/planner/runner.go"],` +
	`"requirements":["R1"],"contributes":["R2"],"gate":"check",` +
	`"evidence":{"capture":["scripts/check"],"limit":60,"artifacts":[],"supplied":[],` +
	`"conclusion":"the runner runs","judge":"a judge"}},` +
	`{"title":"Test it","deliverable":"the runner tests","files":["internal/planner/runner_test.go"],` +
	`"requirements":["R2"],"contributes":[],"gate":"check",` +
	`"evidence":{"capture":["scripts/check"],"limit":60,"artifacts":[],"supplied":[],` +
	`"conclusion":"the runner is tested","judge":"a judge"}}],"outline":"two tasks","questions":[],"outcome":"work"}`

func TestRunnerDoneReturnStoresContributes(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitFor(t, "a planner step to start", func() bool { return len(steps.started()) == 1 })
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	steps.end("step-1", store.StepRow{End: "done", Result: contributingPlan})
	waitFor(t, "turn 1 to succeed", func() bool { return epicOf(t, s).Turns[0].State == "succeeded" })

	tasks := epicOf(t, s).Tasks
	if len(tasks) != 2 {
		t.Fatalf("tasks = %+v, want two", tasks)
	}
	if want := []string{"R2"}; !reflect.DeepEqual(tasks[0].Contributes, want) {
		t.Errorf("task 1 contributes = %q, want %q", tasks[0].Contributes, want)
	}
}
