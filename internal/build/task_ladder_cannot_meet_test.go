package build

import (
	"encoding/json"
	"maps"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// ladderCannotMeetFailure is the failure the cannot_meet returns of these tests name.
const ladderCannotMeetFailure = "the deliverable adds the field to a.go, but the task's files cannot hold the call"

// ladderCannotMeetReason is the reason the cannot_meet returns of these tests give.
const ladderCannotMeetReason = "the spec's files and deliverable contradict each other"

// ladderCannotMeetView returns the view of task 3 at orchestrate, at attempt, with decisions made, whose orchestrator
// returned cannot_meet with ladderCannotMeetFailure and needs_files b.go.
func ladderCannotMeetView(t *testing.T, attempt, decisions int64) View {
	t.Helper()
	view := orchView(decisions, orchDone(orchReturn(t, map[string]any{
		"decision": "cannot_meet", "failure": ladderCannotMeetFailure, "reason": ladderCannotMeetReason,
		"needs_files": []string{"b.go"},
	})))
	view.Task.Build.Attempt = attempt
	return view
}

// TestTaskLadderCannotMeetShape: a done cannot_meet with a failure passes the orchestrator's check, and one with a
// blank failure, or with a version, after_tasks or question, is refused with its exact line.
func TestTaskLadderCannotMeetShape(t *testing.T) {
	t.Parallel()
	task := store.EpicTask{Seq: 3, Files: []string{"a.go"}, Status: taskOpen}
	check := OrchestratorCheck(task, []Peer{{Task: task}})
	cases := []struct {
		name   string
		fields map[string]any
		want   []string
	}{
		{
			"a cannot_meet with a failure passes",
			map[string]any{"failure": ladderCannotMeetFailure, "needs_files": []string{"b.go"}},
			nil,
		},
		{
			"a cannot_meet with no failure is refused",
			map[string]any{"failure": ""},
			[]string{"failure: empty"},
		},
		{
			"a cannot_meet with a version is refused",
			map[string]any{"failure": ladderCannotMeetFailure, "version": versionWith(map[string]any{
				"changed": []string{"gate"}, "gate": "check",
			})},
			[]string{"version: not empty on cannot_meet"},
		},
		{
			"a cannot_meet with after_tasks is refused",
			map[string]any{"failure": ladderCannotMeetFailure, "after_tasks": []int64{2}},
			[]string{"after_tasks: not empty on cannot_meet"},
		},
		{
			"a cannot_meet with a question is refused",
			map[string]any{"failure": ladderCannotMeetFailure, "question": askQuestion()},
			[]string{"question: not empty on cannot_meet"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fields := map[string]any{"decision": "cannot_meet"}
			maps.Copy(fields, c.fields)
			if got := check(json.RawMessage(orchReturn(t, fields))); !reflect.DeepEqual(got, c.want) {
				t.Errorf("check = %q, want %q", got, c.want)
			}
		})
	}
}

// TestTaskLadderCannotMeetSkipsToRecarve: a cannot_meet on attempt 1, 2 or 3 climbs the task to the recarve rung, and on
// attempt 4 to the next rung. On attempt 1 the action is a fail that names the cannot_meet move, its failure, its needs
// files and its decision. A fail decision on attempt 1 still moves the task to the next rung.
func TestTaskLadderCannotMeetSkipsToRecarve(t *testing.T) {
	t.Parallel()
	got := Decide(ladderCannotMeetView(t, 1, 0))
	want := Action{
		Kind: ActFail, Reason: ladderCannotMeetFailure, NeedsFiles: []string{"b.go"},
		Decision: &store.TaskDecision{ID: "T3-request-1", Text: "T3 cannot_meet: " + ladderCannotMeetReason},
		Move:     store.RungMove{MovedBy: store.MovedByCannotMeet, To: store.ApproachRung},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cannot_meet on attempt 1 = %+v, want %+v", got, want)
	}
	for _, c := range []struct{ attempt, to int64 }{{2, store.ApproachRung}, {3, store.ApproachRung}, {4, 0}} {
		move := Decide(ladderCannotMeetView(t, c.attempt, 0)).Move
		wantMove := store.RungMove{MovedBy: store.MovedByCannotMeet, To: c.to}
		if !reflect.DeepEqual(move, wantMove) {
			t.Errorf("cannot_meet on attempt %d moves %+v, want %+v", c.attempt, move, wantMove)
		}
	}
	fail := orchView(0, orchDone(orchReturn(t, map[string]any{
		"decision": "fail", "failure": ladderCannotMeetFailure,
	})))
	if move := Decide(fail).Move; !reflect.DeepEqual(move, store.RungMove{MovedBy: store.MovedByFailDecision}) {
		t.Errorf("fail decision on attempt 1 moves %+v, want fail_decision to the next rung", move)
	}
}

// TestTaskLadderCannotMeetPassClimbsToRecarve: an orchestrator's cannot_meet on task 1 at attempt 1 ends the pass with
// the task open at rung 4, the recarve. Rung 1 ends failed with the stored failure as its reason, rung 4 is an approach
// rung moved by cannot_meet, no rung 2 is written, and the decision is appended to the epic's decisions.
func TestTaskLadderCannotMeetPassClimbsToRecarve(t *testing.T) {
	t.Parallel()
	f := newRequestFixture(t)
	f.endOrchestrator(orchID(0, 0), orchReturn(t, map[string]any{
		"decision": "cannot_meet", "failure": ladderCannotMeetFailure, "reason": ladderCannotMeetReason,
	}))
	ladderClimbBefore(f, 1)
	f.pass()
	task := f.task()
	if task.Status != taskOpen || task.Build.Attempt != store.ApproachRung {
		t.Fatalf("task status %q attempt %d, want open at attempt %d", task.Status, task.Build.Attempt,
			store.ApproachRung)
	}
	want := []store.TaskRung{
		{
			Rung: 1, Kind: store.RungAttempt, Created: passClock(), Outcome: store.RungFailed,
			Reason: ladderCannotMeetFailure, Ended: passClock(),
		},
		{
			Rung: store.ApproachRung, Kind: store.RungApproach, MovedBy: store.MovedByCannotMeet,
			Cause: ladderCannotMeetFailure, Step: passStep(StageImplement, 1, 0), Created: passClock(),
		},
	}
	if !reflect.DeepEqual(task.Rungs, want) {
		t.Errorf("rungs:\n got %+v\nwant %+v", task.Rungs, want)
	}
	decisions := f.epic().Decisions
	wantText := "T1 cannot_meet: " + ladderCannotMeetReason
	if n := len(decisions); n == 0 || decisions[n-1].ID != "T1-request-1" || decisions[n-1].Text != wantText {
		t.Errorf("decisions = %+v, want T1-request-1 recording %q last", decisions, wantText)
	}
}
