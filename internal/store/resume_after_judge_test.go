package store

import (
	"context"
	"testing"
)

// judgeStopRefusal is the refusal of a planner's resume_release of actionEpic, whose release its judge stopped with a
// verdict.
const judgeStopRefusal = "epic " + actionEpic + ": its release judge found the release short of its postconditions, " +
	"and the planner does not overrule the judge of its own epic; ask the steerer a cannot-proceed question that " +
	"names each gap the judge listed with your reading of it, and whose choice to resume says the steerer resumes " +
	"the release with epic_release"

// judgeGoalUnmetReason is the reason of a judge whose step ended goal_unmet, as the release stops with it.
const judgeGoalUnmetReason = "judge ended goal_unmet: the return reports released false with the summary uncovered"

// judgeResumeReason is the reason the planner's resume_release gives in the judge tests.
const judgeResumeReason = "the judge read the release promise more broadly than the contract does"

// stopReleaseAtJudge stores actionEpic as a release whose two release steps passed at attempt 1 and whose judge then
// recorded judge at that attempt, which stopped the release with the judge's reason as the release kind stops it. The
// stop takes the planner class release_step, since no release step failed.
func stopReleaseAtJudge(t *testing.T, s *Store, judge ReleaseRun) {
	t.Helper()
	judge.Part, judge.Attempt, judge.At = ReleasePartJudge, 1, closeAt()
	stopReleaseAfter(t, s, actionEpic, judge.Reason,
		ReleaseRun{
			Part: ReleasePartStep, Position: 1, Attempt: 1, Step: "push tag", Evidence: "run-push-tag", Passed: true,
			At: closeAt(),
		},
		ReleaseRun{
			Part: ReleasePartStep, Position: 2, Attempt: 1, Step: "publish", Evidence: "run-publish", Passed: true,
			At: closeAt(),
		},
		judge,
	)
}

// requireReleaseRunAt fails unless the release node run of actionEpic is at attempt in state.
func requireReleaseRunAt(t *testing.T, s *Store, attempt int64, state string) {
	t.Helper()
	runs, err := s.NodeRuns(context.Background(), actionEpic)
	if err != nil {
		t.Fatalf("NodeRuns of epic %s: %v", actionEpic, err)
	}
	if len(runs) != 1 || runs[0].Path != "release" || runs[0].Attempt != attempt || runs[0].State != state {
		t.Errorf("node runs of epic %s = %+v, want the release run at attempt %d, %s", actionEpic, runs, attempt, state)
	}
}

// TestPlannerResumeReleaseIsRefusedAfterTheJudgesVerdict checks that a planner's resume_release of a release its judge
// stopped with a verdict is refused, whether the judge's step ended goal_unmet or ended done with the release unmet:
// the action is recorded refused with the refusal that sends the planner to the steerer, and the release stays stopped
// with its park and its parked run at the judge's attempt.
func TestPlannerResumeReleaseIsRefusedAfterTheJudgesVerdict(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		judge ReleaseRun
	}{
		{
			name:  "a judge whose step ended goal_unmet",
			judge: ReleaseRun{End: "goal_unmet", Reason: judgeGoalUnmetReason},
		},
		{
			name:  "a judge that left a postcondition uncovered",
			judge: ReleaseRun{End: "done", Reason: "uncovered postconditions: a summary closes a call"},
		},
		{
			name:  "a judge that did not release the epic",
			judge: ReleaseRun{End: "done", Reason: "the judge did not release the epic"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openEpicStore(t)
			stopReleaseAtJudge(t, s, c.judge)
			action := storeActions(t, s, actionEpic, EpicActionInput{
				Kind: ActionResumeRelease, Reason: judgeResumeReason,
			})[0]

			stop, err := s.CarryOutAction(ctx, action, closeAt())
			if err != nil || stop != nil {
				t.Fatalf("CarryOutAction = %v, %v; want no steps and no error for a refusal", stop, err)
			}
			requireRefusedAction(t, s, action, "resume release", judgeStopRefusal)
			row := closeRow(t, s, actionEpic)
			if row.Phase != PhaseStopped || row.Parked != c.judge.Reason || row.ParkClass != ParkClassReleaseStep {
				t.Errorf("epic %s after the refused resume = phase %q parked %q class %q, want %q parked %q class %q",
					actionEpic, row.Phase, row.Parked, row.ParkClass, PhaseStopped, c.judge.Reason,
					ParkClassReleaseStep)
			}
			requireReleaseRunAt(t, s, 1, NodeParked)
		})
	}
}

// TestPlannerResumeReleaseResumesAJudgeThatGaveNoVerdict checks that a planner's resume_release still resumes a release
// whose judge stopped it without a verdict: a judge that did not start, and a judge whose step failed or failed its
// checks. The action is taken, and the release resumes at the judge at its release node run's next attempt.
func TestPlannerResumeReleaseResumesAJudgeThatGaveNoVerdict(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		judge ReleaseRun
	}{
		{
			name:  "a judge that did not start",
			judge: ReleaseRun{Reason: "the judge did not start: role judge does not resolve"},
		},
		{
			name:  "a judge whose step failed",
			judge: ReleaseRun{End: "failed", Reason: "judge ended failed: the session exited"},
		},
		{
			name: "a judge whose step failed its checks",
			judge: ReleaseRun{
				End: "checks_failed", Reason: "judge ended checks_failed, failed check schema",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openEpicStore(t)
			stopReleaseAtJudge(t, s, c.judge)
			action := storeActions(t, s, actionEpic, EpicActionInput{
				Kind: ActionResumeRelease, Reason: "the judge reached no verdict, so it can judge again",
			})[0]

			if _, err := s.CarryOutAction(ctx, action, closeAt()); err != nil {
				t.Fatalf("CarryOutAction: %v", err)
			}
			if got, want := decisionNamed(t, s, actionEpic, "action-1-1").Text,
				"planner resume release: the judge reached no verdict, so it can judge again"; got != want {
				t.Errorf("decision of resume_release = %q, want %q", got, want)
			}
			if phase := closeRow(t, s, actionEpic).Phase; phase != PhaseReleasing {
				t.Errorf("phase of epic %s after resume_release = %q, want %q", actionEpic, phase, PhaseReleasing)
			}
			requireReleaseRunAt(t, s, 2, NodeRunning)
			if got, want := lastTellText(t, s, actionEpic), "release resumed at the judge"; got != want {
				t.Errorf("last tell of epic %s = %q, want %q", actionEpic, got, want)
			}
		})
	}
}

// TestSteererResumesAReleaseItsJudgeStoppedAfterThePlannersRefusal checks that the epic's steering conversation
// resumes a release its judge stopped with a verdict, once the planner's resume_release of it was refused: ReleaseEpic
// by the conversation moves the release to releasing at the judge, at its release node run's next attempt, and unparks
// the epic.
func TestSteererResumesAReleaseItsJudgeStoppedAfterThePlannersRefusal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	stopReleaseAtJudge(t, s, ReleaseRun{End: "goal_unmet", Reason: judgeGoalUnmetReason})
	action := storeActions(t, s, actionEpic, EpicActionInput{
		Kind: ActionResumeRelease, Reason: judgeResumeReason,
	})[0]
	if _, err := s.CarryOutAction(ctx, action, closeAt()); err != nil {
		t.Fatalf("CarryOutAction: %v", err)
	}
	requireRefusedAction(t, s, action, "resume release", judgeStopRefusal)

	if err := s.ReleaseEpic(ctx, actionEpic, nil, Maker{By: MakerConversation, Session: "engine-7"},
		closeAt()); err != nil {
		t.Fatalf("ReleaseEpic by the steering conversation: %v", err)
	}
	row := closeRow(t, s, actionEpic)
	if row.Phase != PhaseReleasing || row.Parked != "" {
		t.Errorf("epic %s after the steerer's release = phase %q parked %q, want %q and not parked",
			actionEpic, row.Phase, row.Parked, PhaseReleasing)
	}
	requireReleaseRunAt(t, s, 2, NodeRunning)
	if got, want := lastTellText(t, s, actionEpic), "release resumed at the judge"; got != want {
		t.Errorf("last tell of epic %s = %q, want %q", actionEpic, got, want)
	}
}
