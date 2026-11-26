package store

import (
	"context"
	"slices"
	"testing"
)

// anyStopEpic is the epic the stop tests stop at each cause, and the epic the owner-stop tests stop.
const anyStopEpic = "orrery-1a2b3c4/epic-any-stop"

// anyStopCause is a stop of the release of an epic at attempt 1: the runs the release recorded under that attempt, the
// reason it stopped with, and the release tip its replan starts from, which is empty for a stop with no tip.
type anyStopCause struct {
	name   string
	runs   []ReleaseRun
	reason string
	start  string
}

// anyStopStepCause is the stop of a release at a failed release step, the stop the send_back and reopen_design tests
// start from.
func anyStopStepCause() anyStopCause {
	return anyStopCause{
		name: "step",
		runs: []ReleaseRun{{
			Part: ReleasePartStep, Position: 1, Attempt: 1, Step: "push tag", Reason: "rejected", At: closeAt(),
		}},
		reason: "the release step push tag was rejected",
	}
}

// anyStopCauses returns a stop of each kind a release can end with: a failed release step, a judge's verdict that the
// release is short, a blocked round of the release integrator, a candidate that failed, and a refused review.
func anyStopCauses() []anyStopCause {
	at := closeAt()
	return []anyStopCause{
		anyStopStepCause(),
		{
			name: "judge",
			runs: []ReleaseRun{{
				Part: ReleasePartJudge, Position: 1, Attempt: 1, End: judgeEndDone, Reason: "the release is not whole",
				At: at,
			}},
			reason: "the release judge found the release short of its postconditions",
		},
		{
			name: "integrator",
			runs: []ReleaseRun{{
				Part: ReleasePartIntegrator, Position: ReleaseIntegratorPosition(1), Attempt: 1,
				Step: "release integrator", Start: "t1", End: integratorReturnedEnd,
				Reason: "the release integrator stopped the merge of origin's commit o1 into release tip t1", At: at,
			}},
			reason: "the release integrator stopped the merge of origin's commit o1 into release tip t1",
			start:  "t1",
		},
		{
			name: "candidate",
			runs: []ReleaseRun{{
				Part: ReleasePartCandidate, Position: 1, Attempt: 1, Head: "c1", Reason: "the candidate does not build",
				At: at,
			}},
			reason: "the candidate does not build",
		},
		{
			name: "review",
			runs: []ReleaseRun{{
				Part: ReleasePartReview, Position: 1, Attempt: 1, Step: "release", Head: "c1",
				Reason: "the release reviewer refused the candidate", At: at,
			}},
			reason: "the release reviewer refused the candidate",
			start:  "c1",
		},
	}
}

// stoppedAnyRelease returns a store whose anyStopEpic is stopped by cause.
func stoppedAnyRelease(t *testing.T, cause anyStopCause) *Store {
	t.Helper()
	s := openEpicStore(t)
	stopReleaseAfter(t, s, anyStopEpic, cause.reason, cause.runs...)
	return s
}

// ownerStops stops the release of epic as its owner does, and fails the test unless the stop is stored at attempt 1, the
// attempt of the release's node run.
func ownerStops(t *testing.T, s *Store, epic string) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.StopEpic(ctx, epic, Maker{By: MakerOwner}, closeAt()); err != nil {
		t.Fatalf("StopEpic of epic %s: %v", epic, err)
	}
	flow, err := s.EpicFlow(ctx, epic)
	if err != nil {
		t.Fatalf("EpicFlow of epic %s: %v", epic, err)
	}
	if !slices.ContainsFunc(flow.Releases, func(run ReleaseRun) bool {
		return run.Part == ReleasePartStop && run.Attempt == 1
	}) {
		t.Fatalf("release runs of epic %s = %+v, want the owner's stop at attempt 1", epic, flow.Releases)
	}
}

// ownerRetries retries the stopped epic anyStopEpic as its owner does.
func ownerRetries(t *testing.T, s *Store) {
	t.Helper()
	if err := s.RetryEpic(context.Background(), anyStopEpic, Maker{By: MakerOwner}, closeAt()); err != nil {
		t.Fatalf("RetryEpic of epic %s: %v", anyStopEpic, err)
	}
}

// requireReplanned fails unless epic, stopped by cause, holds the replan its retry stores: one replan release run of the
// stop's attempt that holds the stop's reason, its release node run running at that attempt with no output, its park
// ended, and the epic still stopped for the release kind to replan.
func requireReplanned(t *testing.T, s *Store, epic string, cause anyStopCause) {
	t.Helper()
	flow, err := s.EpicFlow(context.Background(), epic)
	if err != nil {
		t.Fatalf("EpicFlow of epic %s: %v", epic, err)
	}
	var replans []ReleaseRun
	for _, run := range flow.Releases {
		if run.Part == ReleasePartReplan {
			replans = append(replans, run)
		}
	}
	if len(replans) != 1 || replans[0].Attempt != 1 || replans[0].Step != replanStepName ||
		replans[0].Start != cause.start || replans[0].Reason != cause.reason {
		t.Errorf("replans of epic %s = %+v, want one at attempt 1 from %q with reason %q",
			epic, replans, cause.start, cause.reason)
	}
	i := slices.IndexFunc(flow.Runs, func(run NodeRun) bool { return run.Path == "release" })
	if i < 0 || flow.Runs[i].State != NodeRunning || flow.Runs[i].Attempt != 1 || flow.Runs[i].Output != "" {
		t.Errorf("release run of epic %s = %+v, want running at attempt 1 with no output", epic, flow.Runs)
	}
	if flow.Epic.Parked != "" || flow.Epic.ParkClass != "" {
		t.Errorf("park of epic %s = %q %q, want none", epic, flow.Epic.ParkClass, flow.Epic.Parked)
	}
	if flow.Epic.Phase != PhaseStopped {
		t.Errorf("phase of epic %s = %q, want %q until the release kind replans it",
			epic, flow.Epic.Phase, PhaseStopped)
	}
}

// requireStillStopped fails unless epic is stopped at cause as it was before a refused action: its release node run is
// parked at attempt 1, its park holds cause's reason, and no replan release run is stored.
func requireStillStopped(t *testing.T, s *Store, epic string, cause anyStopCause) {
	t.Helper()
	flow, err := s.EpicFlow(context.Background(), epic)
	if err != nil {
		t.Fatalf("EpicFlow of epic %s: %v", epic, err)
	}
	if flow.Epic.Phase != PhaseStopped || flow.Epic.Parked != cause.reason {
		t.Errorf("epic %s phase %q parked %q, want stopped and parked with %q",
			epic, flow.Epic.Phase, flow.Epic.Parked, cause.reason)
	}
	i := slices.IndexFunc(flow.Runs, func(run NodeRun) bool { return run.Path == "release" })
	if i < 0 || flow.Runs[i].State != NodeParked || flow.Runs[i].Attempt != 1 {
		t.Errorf("release run of epic %s = %+v, want parked at attempt 1", epic, flow.Runs)
	}
	if slices.ContainsFunc(flow.Releases, func(run ReleaseRun) bool { return run.Part == ReleasePartReplan }) {
		t.Errorf("release runs of epic %s = %+v, want no replan", epic, flow.Releases)
	}
}

// TestAnyStopReplansOnPlannerRetry checks that each stop of a release, then the planner's retry_epic, leaves a replan
// release run at the stop's attempt holding the stop's reason, the release run running and the park ended.
func TestAnyStopReplansOnPlannerRetry(t *testing.T) {
	t.Parallel()
	for _, cause := range anyStopCauses() {
		t.Run(cause.name, func(t *testing.T) {
			t.Parallel()
			s := stoppedAnyRelease(t, cause)
			action := storeActions(t, s, anyStopEpic, EpicActionInput{
				Kind: ActionRetryEpic, Reason: "the release can run again",
			})[0]

			stop, err := s.CarryOutAction(context.Background(), action, closeAt())
			if err != nil || stop != nil {
				t.Fatalf("CarryOutAction = %v, %v; want no steps and no error", stop, err)
			}
			requireReplanned(t, s, anyStopEpic, cause)
		})
	}
}

// TestAnyStopReplansOnOwnerRetry checks that the owner's RetryEpic does what the planner's retry does for each stop of a
// release, and for a stop the owner made of a release that its stop already stopped.
func TestAnyStopReplansOnOwnerRetry(t *testing.T) {
	t.Parallel()
	for _, cause := range anyStopCauses() {
		t.Run(cause.name, func(t *testing.T) {
			t.Parallel()
			s := stoppedAnyRelease(t, cause)
			ownerRetries(t, s)
			requireReplanned(t, s, anyStopEpic, cause)
		})
		t.Run(cause.name+" owner stopped", func(t *testing.T) {
			t.Parallel()
			s := stoppedAnyRelease(t, cause)
			ownerStops(t, s, anyStopEpic)
			ownerRetries(t, s)
			requireReplanned(t, s, anyStopEpic, cause)
		})
	}
}

// TestAnyStopReplansOnSendBack checks that a planner's send_back of a task on a release stopped at a failed step reopens
// the task, and stores the replan of the stop, which leaves the release node run running and ends its park.
func TestAnyStopReplansOnSendBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := sendBackStore(t)
	sendBackDone(t, s, 1, TaskBuild{Stage: "implement", Base: sendBackBase, Attempt: 1, Revision: "rev-1"})
	if _, err := s.fixtureExec(ctx, `UPDATE epics SET phase = 'clean' WHERE id = ?`, sendBackEpic); err != nil {
		t.Fatalf("make epic %s clean: %v", sendBackEpic, err)
	}
	cause := anyStopStepCause()
	stopReleaseOf(t, s, sendBackEpic, cause.reason, cause.runs...)

	sendBackCarry(t, s, sendBackAction(t, s, 2, sendBackInput()))

	sendBackRequireReopened(t, s, sendBackBase)
	requireReplanned(t, s, sendBackEpic, cause)
}

// TestAnyStopReplansOnReopenDesign checks that a planner's reopen_design of a design task on a release stopped at a failed
// step adds the revising design task, and stores the replan of the stop, which leaves the release node run running and
// ends its park.
func TestAnyStopReplansOnReopenDesign(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	seedReopenTask(t, s, TaskKindDesign, taskDone)
	if _, err := s.fixtureExec(ctx, `UPDATE epics SET phase = 'clean' WHERE id = ?`, climbEpic); err != nil {
		t.Fatalf("make epic %s clean: %v", climbEpic, err)
	}
	cause := anyStopStepCause()
	stopReleaseOf(t, s, climbEpic, cause.reason, cause.runs...)

	stop, err := s.CarryOutAction(ctx, reopenDesignAction(t, s, 1), closeAt())
	if err != nil || stop != nil {
		t.Fatalf("CarryOutAction = %v, %v; want no steps and no error", stop, err)
	}

	revision := climbTaskOf(ctx, t, s, 2)
	if revision.Kind != TaskKindDesign || revision.Revises != 1 {
		t.Errorf("revision = kind %q revises %d, want a design task revising task 1", revision.Kind, revision.Revises)
	}
	requireReplanned(t, s, climbEpic, cause)
}

// TestAnyStopReplansRefusesPlannerOnOwnerStop checks that the planner's retry_epic, send_back and reopen_design of a release
// its owner stopped are refused, with a refusal that names the owner, and that the release stays stopped as it was, with no
// replan stored.
func TestAnyStopReplansRefusesPlannerOnOwnerStop(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		input   EpicActionInput
		summary string
	}{
		{
			name:    "retry_epic",
			input:   EpicActionInput{Kind: ActionRetryEpic, Reason: "the release can run again"},
			summary: "retry epic",
		},
		{
			name:    "send_back",
			input:   EpicActionInput{Kind: ActionSendBack, Task: 1, Reason: sendBackReason},
			summary: "send back task 1",
		},
		{
			name: "reopen_design",
			input: EpicActionInput{
				Kind:        ActionReopenDesign,
				Task:        1,
				Deliverable: reopenDeliverable,
				Reason:      reopenReason,
			},
			summary: "reopen design of task 1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			cause := anyStopStepCause()
			s := stoppedAnyRelease(t, cause)
			ownerStops(t, s, anyStopEpic)
			action := storeActions(t, s, anyStopEpic, c.input)[0]

			stop, err := s.CarryOutAction(context.Background(), action, closeAt())
			if err != nil || stop != nil {
				t.Fatalf("CarryOutAction = %v, %v; want no steps and no error for a refusal", stop, err)
			}
			requireRefusedAction(t, s, action, c.summary,
				"epic "+anyStopEpic+": its owner stopped the release; only the owner retries it")
			requireStillStopped(t, s, anyStopEpic, cause)
		})
	}
}
