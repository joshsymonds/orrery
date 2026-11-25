package server

import (
	"testing"
	"time"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// epicMarkID is the id of the orreryd epic the derivation tests read.
const epicMarkID = "repoa/shared"

// epicMarkStored returns an orreryd epic of epicMarkID in phase, whose contract names the Requirements ids, in order.
func epicMarkStored(phase string, ids ...string) *enginev1.StoredEpic {
	requirements := make([]*enginev1.EpicRequirement, 0, len(ids))
	for _, id := range ids {
		requirements = append(requirements, &enginev1.EpicRequirement{Id: id, Text: "synthetic requirement " + id})
	}
	return storedEpicOf(epicMarkID, phase, epicsAgo(2*time.Hour), requirements...)
}

// epicMarkGet serves stored with the labeled steps and evidence runs given, and returns the answer for its id.
func epicMarkGet(
	t *testing.T,
	stored *enginev1.StoredEpic,
	steps []*enginev1.Step,
	runs []*enginev1.EvidenceRun,
) *orreryv1.EpicsGetResponse {
	t.Helper()
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{stored}}
	client := stepDataServe(t, t.TempDir(), &epicsFake{steps: steps}, store, &evidenceRunsFake{runs: runs})
	return epicsGet(t, client, epicMarkID)
}

// epicMarkRun returns an evidence run of the Requirement R1 in phase and state, passed or not, created at created.
func epicMarkRun(id, phase, state string, passed bool, created int64) *enginev1.EvidenceRun {
	return &enginev1.EvidenceRun{
		Id: id, Epic: epicMarkID, Item: "R1", Phase: phase, Source: "captured", State: state, Passed: passed,
		Created: created,
	}
}

// epicMarkTask returns a build task with id, status and stage, naming the Requirement R1.
func epicMarkTask(id, status, stage string) *enginev1.EpicTask {
	return &enginev1.EpicTask{Id: id, Status: status, Stage: stage, Requirements: []string{"R1"}}
}

// epicMarkStep returns a running step labeled with task, held or not.
func epicMarkStep(task string, held bool) *enginev1.Step {
	return &enginev1.Step{Id: "step-" + task, Epic: epicMarkID, Task: task, Held: held, Started: epicsAgo(time.Hour)}
}

// epicMarkStateOf returns the state Get gives the Requirement R1 of an accepted epic with the tasks, steps and runs
// given.
func epicMarkStateOf(
	t *testing.T,
	tasks []*enginev1.EpicTask,
	steps []*enginev1.Step,
	runs []*enginev1.EvidenceRun,
) string {
	t.Helper()
	stored := epicMarkStored("accepted", "R1")
	stored.Tasks = tasks
	reqs := epicMarkGet(t, stored, steps, runs).GetRequirements()
	if len(reqs) != 1 {
		t.Fatalf("requirements = %v, want one", reqs)
	}
	return reqs[0].GetState()
}

func TestEpicMarkRequirementStateEachState(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		tasks []*enginev1.EpicTask
		steps []*enginev1.Step
		runs  []*enginev1.EvidenceRun
		want  string
	}{
		{
			name: "proven by a passed green run",
			runs: []*enginev1.EvidenceRun{epicMarkRun("green-passed", "green", "ended", true, epicsAgo(2*time.Hour))},
			want: "proven",
		},
		{
			name: "failed by its latest green run",
			runs: []*enginev1.EvidenceRun{epicMarkRun("green-failed", "green", "ended", false, epicsAgo(2*time.Hour))},
			want: "failed",
		},
		{
			name:  "failed by a task naming it",
			tasks: []*enginev1.EpicTask{epicMarkTask("T1", "failed", "")},
			want:  "failed",
		},
		{
			name:  "held by a held running step",
			tasks: []*enginev1.EpicTask{epicMarkTask("T1", "open", "implement")},
			steps: []*enginev1.Step{epicMarkStep("T1", true)},
			want:  "held",
		},
		{
			name:  "in_flight by a started task",
			tasks: []*enginev1.EpicTask{epicMarkTask("T1", "open", "red")},
			want:  "in_flight",
		},
		{
			name:  "in_flight by a labeled step of a task with no stage",
			tasks: []*enginev1.EpicTask{epicMarkTask("T1", "open", "")},
			steps: []*enginev1.Step{epicMarkStep("T1", false)},
			want:  "in_flight",
		},
		{
			name: "in_flight by a running run",
			runs: []*enginev1.EvidenceRun{epicMarkRun("red-judging", "red", "judging", false, epicsAgo(time.Hour))},
			want: "in_flight",
		},
		{
			name: "not_reached with nothing naming or running it",
			want: "not_reached",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := epicMarkStateOf(t, tc.tasks, tc.steps, tc.runs); got != tc.want {
				t.Errorf("state = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEpicMarkRequirementStatesFirstMatchWins(t *testing.T) {
	t.Parallel()
	passedGreen := []*enginev1.EvidenceRun{epicMarkRun("green-passed", "green", "ended", true, epicsAgo(2*time.Hour))}
	failedGreen := []*enginev1.EvidenceRun{epicMarkRun("green-failed", "green", "ended", false, epicsAgo(2*time.Hour))}
	judging := []*enginev1.EvidenceRun{epicMarkRun("red-judging", "red", "judging", false, epicsAgo(time.Hour))}
	cases := []struct {
		name  string
		tasks []*enginev1.EpicTask
		steps []*enginev1.Step
		runs  []*enginev1.EvidenceRun
		want  string
	}{
		{
			name:  "proven over a failed task",
			tasks: []*enginev1.EpicTask{epicMarkTask("T1", "failed", "")},
			runs:  passedGreen,
			want:  "proven",
		},
		{
			name:  "proven over a held step",
			tasks: []*enginev1.EpicTask{epicMarkTask("T1", "open", "implement")},
			steps: []*enginev1.Step{epicMarkStep("T1", true)},
			runs:  passedGreen,
			want:  "proven",
		},
		{
			name:  "failed over a held step",
			tasks: []*enginev1.EpicTask{epicMarkTask("T1", "open", "implement")},
			steps: []*enginev1.Step{epicMarkStep("T1", true)},
			runs:  failedGreen,
			want:  "failed",
		},
		{
			name: "failed by a task over a held step",
			tasks: []*enginev1.EpicTask{
				epicMarkTask("T1", "failed", ""),
				epicMarkTask("T2", "open", "implement"),
			},
			steps: []*enginev1.Step{epicMarkStep("T2", true)},
			want:  "failed",
		},
		{
			name:  "held over in_flight",
			tasks: []*enginev1.EpicTask{epicMarkTask("T1", "open", "implement")},
			steps: []*enginev1.Step{epicMarkStep("T1", true)},
			runs:  judging,
			want:  "held",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := epicMarkStateOf(t, tc.tasks, tc.steps, tc.runs); got != tc.want {
				t.Errorf("state = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEpicMarkLatestGreenRunDecidesFailed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		runs []*enginev1.EvidenceRun
		want string
	}{
		{
			name: "an older failed green run with a newer one still judging is in_flight",
			runs: []*enginev1.EvidenceRun{
				epicMarkRun("green-old", "green", "ended", false, epicsAgo(2*time.Hour)),
				epicMarkRun("green-new", "green", "judging", false, epicsAgo(time.Hour)),
			},
			want: "in_flight",
		},
		{
			name: "an older green run still judging with a newer failed one is failed",
			runs: []*enginev1.EvidenceRun{
				epicMarkRun("green-old", "green", "judging", false, epicsAgo(2*time.Hour)),
				epicMarkRun("green-new", "green", "ended", false, epicsAgo(time.Hour)),
			},
			want: "failed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := epicMarkStateOf(t, nil, nil, tc.runs); got != tc.want {
				t.Errorf("state = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEpicMarkCentreValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		phase  string
		paused bool
		want   string
	}{
		{name: "running when nothing asks or ends", phase: "accepted", want: "running"},
		{name: "paused", phase: "accepted", paused: true, want: "paused"},
		{name: "paused over releasing", phase: "releasing", paused: true, want: "paused"},
		{name: "releasing", phase: "releasing", want: "releasing"},
		{name: "shipped when released", phase: "released", want: "shipped"},
		{name: "ended_short for a done epic", phase: "done", want: "ended_short"},
		{name: "ended_short for a closed epic", phase: "closed_abandoned", want: "ended_short"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stored := epicMarkStored(tc.phase, "R1")
			stored.Paused = tc.paused
			if got := epicMarkGet(t, stored, nil, nil).GetCentre(); got != tc.want {
				t.Errorf("center = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEpicMarkCentreAsksForEachCause(t *testing.T) {
	t.Parallel()
	openQuestion := func(stored *enginev1.StoredEpic) {
		stored.Questions = []*enginev1.EpicQuestion{{Id: "Q1", Turn: 1, Text: "synthetic question", State: "open"}}
	}
	cases := []struct {
		name  string
		phase string
		setup func(stored *enginev1.StoredEpic)
		steps []*enginev1.Step
		want  string
	}{
		{name: "an open question", phase: "accepted", setup: openQuestion, want: "asks"},
		{
			name:  "a failed release step",
			phase: "stopped",
			setup: func(stored *enginev1.StoredEpic) {
				stored.ReleaseProof = []*enginev1.EpicReleaseProof{{
					Position: 1, Step: "synthetic step", Target: "synthetic target", Undo: "synthetic undo",
					State: "failed",
				}}
			},
			want: "asks",
		},
		{
			name:  "a park the owner holds",
			phase: "accepted",
			setup: func(stored *enginev1.StoredEpic) {
				stored.Parked = "synthetic park"
				stored.ParkClass = "branch"
			},
			want: "asks",
		},
		{
			name:  "a held running step",
			phase: "accepted",
			steps: []*enginev1.Step{{Id: "held", Epic: epicMarkID, Held: true, Started: epicsAgo(time.Hour)}},
			want:  "asks",
		},
		{name: "an ending that asks the owner to release or close", phase: "ended_with_gaps", want: "asks"},
		{
			name:  "an open question over a paused epic",
			phase: "accepted",
			setup: func(stored *enginev1.StoredEpic) {
				openQuestion(stored)
				stored.Paused = true
			},
			want: "asks",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stored := epicMarkStored(tc.phase, "R1")
			if tc.setup != nil {
				tc.setup(stored)
			}
			if got := epicMarkGet(t, stored, tc.steps, nil).GetCentre(); got != tc.want {
				t.Errorf("center = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEpicMarkPlannerParkAndUnheldStepAreNotAsks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		setup func(stored *enginev1.StoredEpic)
		steps []*enginev1.Step
	}{
		{
			name: "a park the planner holds",
			setup: func(stored *enginev1.StoredEpic) {
				stored.Parked = "synthetic park"
				stored.ParkClass = "node"
			},
		},
		{
			name:  "a running step that is not held",
			steps: []*enginev1.Step{epicMarkStep("T1", false)},
		},
		{
			name: "an answered question",
			setup: func(stored *enginev1.StoredEpic) {
				stored.Questions = []*enginev1.EpicQuestion{{Id: "Q1", Turn: 1, Text: "synthetic", State: "answered"}}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stored := epicMarkStored("accepted", "R1")
			if tc.setup != nil {
				tc.setup(stored)
			}
			if got := epicMarkGet(t, stored, tc.steps, nil).GetCentre(); got != "running" {
				t.Errorf("center = %q, want running", got)
			}
		})
	}
}
