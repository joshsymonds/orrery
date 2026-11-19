package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// climbEpic is the epic the climb tests write, with task 1 open at the build climbFrom gives.
const climbEpic = "orrery-1a2b3c4/task-climb"

// climbCause is the failure the climb tests give every move.
const climbCause = "the claim is not met"

// climbedAt is the second the climb tests stamp their writes at.
func climbedAt() time.Time {
	return time.Unix(1700001000, 0)
}

// climbFrom is the build of an open task on rung 1 whose first attempt is running.
func climbFrom() TaskBuild {
	return TaskBuild{
		Stage: "implement", Base: "base-rev", Attempt: 1, Step: "step-1", Revision: "rev-1", Decisions: 2,
		Guard: "guard refusal",
	}
}

// seedClimbTask creates climbEpic with task 1 open and its stored build set to from.
func seedClimbTask(t *testing.T, s *Store, from TaskBuild) {
	t.Helper()
	ctx := context.Background()
	createEpic(t, s, fullEpic(climbEpic))
	if _, err := s.db.ExecContext(ctx, `INSERT INTO epic_tasks (epic, seq, turn, title, deliverable, status)
		VALUES (?, 1, 1, 'Climbs', 'the first change', 'open')`, climbEpic); err != nil {
		t.Fatalf("insert task 1: %v", err)
	}
	if err := s.SetTaskBuild(ctx, climbEpic, 1, TaskBuild{}, from); err != nil {
		t.Fatalf("SetTaskBuild of task 1: %v", err)
	}
}

// climbTaskOf reads task seq of climbEpic, and fails the test when the epic has no such task.
func climbTaskOf(ctx context.Context, t *testing.T, s *Store, seq int64) EpicTask {
	t.Helper()
	epic, err := s.Epic(ctx, climbEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	for _, task := range epic.Tasks {
		if task.Seq == seq {
			return task
		}
	}
	t.Fatalf("epic %s has no task %d", climbEpic, seq)
	return EpicTask{}
}

// TestTaskLadderClimbMovesOneRung checks that a finding climbs a task from rung 1 to rung 2 in one write: rung 1 ends
// failed with the cause, rung 2 is added moved by the finding with its gate, assertion and step, and the stored
// build is the climbed build, with the task still open.
func TestTaskLadderClimbMovesOneRung(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	from := climbFrom()
	seedClimbTask(t, s, from)
	move := RungMove{
		MovedBy: MovedByFinding, Cause: climbCause, Gate: "review", Assertion: "the claim", Step: "step-3",
	}
	to := ClimbedBuild(from, 2, move.Cause)
	if err := s.ClimbTask(ctx, climbEpic, 1, from, to, move, nil, nil, climbedAt()); err != nil {
		t.Fatalf("ClimbTask: %v", err)
	}

	got := climbTaskOf(ctx, t, s, 1)
	wantRungs := []TaskRung{
		{Rung: 1, Kind: RungAttempt, Created: climbedAt(), Outcome: RungFailed, Reason: climbCause, Ended: climbedAt()},
		{
			Rung: 2, Kind: RungAttempt, MovedBy: MovedByFinding, Cause: climbCause, Gate: "review",
			Assertion: "the claim", Step: "step-3", Created: climbedAt(),
		},
	}
	if !reflect.DeepEqual(got.Rungs, wantRungs) {
		t.Errorf("rungs:\n got %+v\nwant %+v", got.Rungs, wantRungs)
	}
	if !reflect.DeepEqual(got.Build, to) {
		t.Errorf("build = %+v, want %+v", got.Build, to)
	}
	if got.Status != "open" {
		t.Errorf("status = %q, want open", got.Status)
	}
}

// TestTaskLadderClimbCannotMeetSkipsToRescue checks that a cannot-meet move climbs a task from rung 1 straight to the
// rescue rung 3, leaving no rung 2, and that the rescue version and its decision are stored with the climb.
func TestTaskLadderClimbCannotMeetSkipsToRescue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	from := climbFrom()
	seedClimbTask(t, s, from)
	move := RungMove{MovedBy: MovedByCannotMeet, Cause: climbCause, To: RescueRung}
	to := ClimbedBuild(from, RescueRung, move.Cause)
	deliverable := "the rescued change"
	version := TaskVersionInput{Source: SourceRescue, Step: "step-rescue", Reason: "rescued", Deliverable: &deliverable}
	decision := TaskDecision{ID: "D3", Text: "Rescue the task"}
	if err := s.ClimbTask(ctx, climbEpic, 1, from, to, move, &version, &decision, climbedAt()); err != nil {
		t.Fatalf("ClimbTask: %v", err)
	}

	got := climbTaskOf(ctx, t, s, 1)
	wantRungs := []TaskRung{
		{Rung: 1, Kind: RungAttempt, Created: climbedAt(), Outcome: RungFailed, Reason: climbCause, Ended: climbedAt()},
		{Rung: RescueRung, Kind: RungRescue, MovedBy: MovedByCannotMeet, Cause: climbCause, Created: climbedAt()},
	}
	if !reflect.DeepEqual(got.Rungs, wantRungs) {
		t.Errorf("rungs:\n got %+v\nwant %+v", got.Rungs, wantRungs)
	}
	if len(got.Versions) != 1 {
		t.Fatalf("versions = %d, want 1", len(got.Versions))
	}
	if v := got.Versions[0]; v.Source != SourceRescue || v.Decision != decision.ID ||
		v.Deliverable == nil || *v.Deliverable != deliverable {
		t.Errorf("version = %+v, want source %q, decision %q and deliverable %q",
			v, SourceRescue, decision.ID, deliverable)
	}

	epic, err := s.Epic(ctx, climbEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	wantDecisions := append(seededDecisions(), EpicDecision{Seq: 3, ID: "D3", Text: decision.Text, Time: climbedAt()})
	if !reflect.DeepEqual(epic.Decisions, wantDecisions) {
		t.Errorf("decisions:\n got %+v\nwant %+v", epic.Decisions, wantDecisions)
	}
}

// TestTaskLadderClimbToApproachAddsTrigger checks that a climb from rung 4 to rung 5 adds the pending approach trigger
// with its cause, and that rung 4 ends failed as an orchestrator rung while rung 5 is an approach rung.
func TestTaskLadderClimbToApproachAddsTrigger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	from := climbFrom()
	from.Attempt = OrchestratorRung
	seedClimbTask(t, s, from)
	move := RungMove{MovedBy: MovedByFailDecision, Cause: climbCause, Step: "step-9"}
	to := ClimbedBuild(from, ApproachRung, move.Cause)
	if err := s.ClimbTask(ctx, climbEpic, 1, from, to, move, nil, nil, climbedAt()); err != nil {
		t.Fatalf("ClimbTask: %v", err)
	}

	got := climbTaskOf(ctx, t, s, 1)
	wantRungs := []TaskRung{
		{
			Rung: OrchestratorRung, Kind: RungOrchestrator, Created: climbedAt(),
			Outcome: RungFailed, Reason: climbCause, Ended: climbedAt(),
		},
		{
			Rung: ApproachRung, Kind: RungApproach, MovedBy: MovedByFailDecision, Cause: climbCause, Step: "step-9",
			Created: climbedAt(),
		},
	}
	if !reflect.DeepEqual(got.Rungs, wantRungs) {
		t.Errorf("rungs:\n got %+v\nwant %+v", got.Rungs, wantRungs)
	}

	history, err := s.EpicHistory(ctx, climbEpic)
	if err != nil {
		t.Fatalf("EpicHistory: %v", err)
	}
	wantTriggers := []EpicTrigger{
		{ID: "created", Cause: "epic created", State: triggerPending, Created: epicCreated()},
		{
			ID: "T1-approach", Cause: "task T1 needs a change of approach: " + climbCause, State: triggerPending,
			Created: climbedAt(),
		},
	}
	if !reflect.DeepEqual(history.Triggers, wantTriggers) {
		t.Errorf("triggers:\n got %+v\nwant %+v", history.Triggers, wantTriggers)
	}
}

// TestTaskLadderClimbFromLastRungEndsAsGap checks that a climb from rung 6 past the last rung ends the task as a gap:
// rung 6 ends failed, rung 7 is a gap rung that ends gapped, the status is gap, and the gap trigger is pending.
func TestTaskLadderClimbFromLastRungEndsAsGap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	from := climbFrom()
	from.Attempt = LastRung
	seedClimbTask(t, s, from)
	move := RungMove{
		MovedBy: MovedByFinding, Cause: climbCause, Gate: "review", Assertion: "the claim", Step: "step-12",
	}
	to := ClimbedBuild(from, GapRung, move.Cause)
	if err := s.ClimbTask(ctx, climbEpic, 1, from, to, move, nil, nil, climbedAt()); err != nil {
		t.Fatalf("ClimbTask: %v", err)
	}

	got := climbTaskOf(ctx, t, s, 1)
	if got.Status != TaskGap {
		t.Errorf("status = %q, want %q", got.Status, TaskGap)
	}
	wantRungs := []TaskRung{
		{
			Rung: LastRung, Kind: RungApproach, Created: climbedAt(),
			Outcome: RungFailed, Reason: climbCause, Ended: climbedAt(),
		},
		{
			Rung: GapRung, Kind: RungGap, MovedBy: MovedByFinding, Cause: climbCause, Gate: "review",
			Assertion: "the claim", Step: "step-12", Created: climbedAt(),
			Outcome: RungGapped, Reason: climbCause, Ended: climbedAt(),
		},
	}
	if !reflect.DeepEqual(got.Rungs, wantRungs) {
		t.Errorf("rungs:\n got %+v\nwant %+v", got.Rungs, wantRungs)
	}

	history, err := s.EpicHistory(ctx, climbEpic)
	if err != nil {
		t.Fatalf("EpicHistory: %v", err)
	}
	wantTriggers := []EpicTrigger{
		{ID: "created", Cause: "epic created", State: triggerPending, Created: epicCreated()},
		{ID: "T1-gap", Cause: "task T1 ended as a gap: " + climbCause, State: triggerPending, Created: climbedAt()},
	}
	if !reflect.DeepEqual(history.Triggers, wantTriggers) {
		t.Errorf("triggers:\n got %+v\nwant %+v", history.Triggers, wantTriggers)
	}
}

// TestTaskLadderClimbRefusesAndWritesNothing checks that a climb refuses a stale build with ErrTaskMoved, a to on the
// wrong rung, and an unknown moved_by, and that each refusal leaves the task with no rung rows and its build unchanged.
func TestTaskLadderClimbRefusesAndWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	from := climbFrom()
	seedClimbTask(t, s, from)
	move := RungMove{MovedBy: MovedByUnit, Cause: climbCause, Step: "step-2"}
	stale := from
	stale.Step = "step-0"

	cases := []struct {
		name  string
		from  TaskBuild
		to    TaskBuild
		move  RungMove
		moved bool
	}{
		{name: "stale build", from: stale, to: ClimbedBuild(stale, 2, climbCause), move: move, moved: true},
		{name: "to off the target", from: from, to: ClimbedBuild(from, 3, climbCause), move: move},
		{
			name: "unknown moved_by", from: from, to: ClimbedBuild(from, 2, climbCause),
			move: RungMove{MovedBy: "guess", Cause: climbCause},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := s.ClimbTask(ctx, climbEpic, 1, tc.from, tc.to, tc.move, nil, nil, climbedAt())
			if err == nil {
				t.Fatal("ClimbTask = nil, want an error")
			}
			if moved := errors.Is(err, ErrTaskMoved); moved != tc.moved {
				t.Errorf("ClimbTask error %v: ErrTaskMoved = %t, want %t", err, moved, tc.moved)
			}
			got := climbTaskOf(ctx, t, s, 1)
			if got.Rungs != nil {
				t.Errorf("rungs = %+v, want none after a refused climb", got.Rungs)
			}
			if !reflect.DeepEqual(got.Build, from) {
				t.Errorf("build = %+v, want unchanged %+v", got.Build, from)
			}
		})
	}
}

// TestTaskLadderAcceptEndsTheRung checks that accepting a task that climbed to rung 2 ends rung 2 accepted at the
// rung's own created time, and that a task accepted without climbing has no ladder rows.
func TestTaskLadderAcceptEndsTheRung(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	from := climbFrom()
	seedClimbTask(t, s, from)
	move := RungMove{MovedBy: MovedByFinding, Cause: climbCause, Gate: "review", Assertion: "the claim", Step: "step-3"}
	climbed := ClimbedBuild(from, 2, move.Cause)
	if err := s.ClimbTask(ctx, climbEpic, 1, from, climbed, move, nil, nil, climbedAt()); err != nil {
		t.Fatalf("ClimbTask: %v", err)
	}
	if err := s.AcceptTask(ctx, climbEpic, 1, climbed, "reviewed"); err != nil {
		t.Fatalf("AcceptTask of climbed task: %v", err)
	}

	got := climbTaskOf(ctx, t, s, 1)
	wantRungs := []TaskRung{
		{Rung: 1, Kind: RungAttempt, Created: climbedAt(), Outcome: RungFailed, Reason: climbCause, Ended: climbedAt()},
		{
			Rung: 2, Kind: RungAttempt, MovedBy: MovedByFinding, Cause: climbCause, Gate: "review",
			Assertion: "the claim", Step: "step-3", Created: climbedAt(), Outcome: RungAccepted, Ended: climbedAt(),
		},
	}
	if !reflect.DeepEqual(got.Rungs, wantRungs) {
		t.Errorf("climbed task rungs:\n got %+v\nwant %+v", got.Rungs, wantRungs)
	}
	if got.Status != "done" {
		t.Errorf("climbed task status = %q, want done", got.Status)
	}

	if _, err := s.db.ExecContext(ctx, `INSERT INTO epic_tasks (epic, seq, turn, title, deliverable, status)
		VALUES (?, 2, 1, 'Never climbs', 'the second change', 'open')`, climbEpic); err != nil {
		t.Fatalf("insert task 2: %v", err)
	}
	if err := s.SetTaskBuild(ctx, climbEpic, 2, TaskBuild{}, from); err != nil {
		t.Fatalf("SetTaskBuild of task 2: %v", err)
	}
	if err := s.AcceptTask(ctx, climbEpic, 2, from, "reviewed"); err != nil {
		t.Fatalf("AcceptTask of unclimbed task: %v", err)
	}
	if rungs := climbTaskOf(ctx, t, s, 2).Rungs; rungs != nil {
		t.Errorf("unclimbed task rungs = %+v, want nil", rungs)
	}
}
