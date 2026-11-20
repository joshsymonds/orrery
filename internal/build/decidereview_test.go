package build

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
)

// decideEpic is the epic the decision tests build tasks of, and the task is task 3 of it.
const decideEpic = "acme/epic"

// gateFailedFindings is a review's kept return that failed two assertions, with a finding for each.
const gateFailedFindings = `{"status":"done",` +
	`"findings":[` +
	`{"id":"F1","claim":"claim one","citation":"assertion one","location":"a.go:1","evidence":"seen one",` +
	`"verify_by":"run one"},` +
	`{"id":"F2","claim":"claim two","citation":"assertion two","location":"b.go:2","evidence":"seen two",` +
	`"verify_by":"run two"}],` +
	`"observations":[],"missing_evidence":[],"test_runner_requests":[],` +
	`"results":[` +
	`{"assertion":"assertion one","kind":"must","result":"fails","evidence":"seen one"},` +
	`{"assertion":"assertion two","kind":"must","result":"fails","evidence":"seen two"}]}`

// gateFailedUndetermined is a kept return with a finding and one assertion the review could not judge.
const gateFailedUndetermined = `{"status":"done",` +
	`"findings":[{"id":"F1","claim":"claim one","citation":"assertion one","location":"a.go:1","evidence":"seen",` +
	`"verify_by":"run one"}],` +
	`"observations":[],"missing_evidence":["the test log"],"test_runner_requests":[],` +
	`"results":[` +
	`{"assertion":"assertion one","kind":"must","result":"fails","evidence":"seen"},` +
	`{"assertion":"assertion two","kind":"must not","result":"undetermined","evidence":"no log"}]}`

// gateBlocked is a kept return whose status is blocked, with a finding it should not have.
const gateBlocked = `{"status":"blocked",` +
	`"findings":[{"id":"F1","claim":"claim one","citation":"assertion one","location":"a.go:1","evidence":"seen",` +
	`"verify_by":"run one"}],` +
	`"observations":[],"missing_evidence":["the diff"],"test_runner_requests":[],` +
	`"results":[{"assertion":"assertion one","kind":"must","result":"fails","evidence":"seen"}]}`

// gateNoFindings is a kept return with done status and no failed assertion.
const gateNoFindings = `{"status":"done","findings":[],"observations":[],"missing_evidence":[],` +
	`"test_runner_requests":[],"results":[{"assertion":"assertion one","kind":"must","result":"holds","evidence":"ok"}]}`

// verifiedAs returns a verifier's done return that gave result for its one finding.
func verifiedAs(result string) string {
	return fmt.Sprintf(`{"status":"done","results":[{"id":"F1","evidence":"the claim holds","result":%q}]}`, result)
}

// verifyReviews returns the one review of attempt a task has, a fails verdict with findings F1 and F2 whose verified
// results are first and second.
func verifyReviews(attempt int64, first, second string) []store.TaskReview {
	return []store.TaskReview{{
		Attempt: attempt, Position: 0, Gate: "lint", Step: "review-step", Verdict: "fails",
		Findings: []store.TaskFinding{
			{Position: 0, ID: "F1", Claim: "claim one", Result: first},
			{Position: 1, ID: "F2", Claim: "claim two", Result: second},
		},
	}}
}

func TestDecideGreen(t *testing.T) {
	t.Parallel()
	ended := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	gates := []string{"lint", "security"}
	build := store.TaskBuild{
		Stage: StageGreen, Base: "base", Attempt: 1, Rerun: 2, NoVerdict: 1, Revision: "rev", RedRun: "red-run",
	}
	firstAttempt := build
	secondAttempt := build
	secondAttempt.Attempt = 2
	noVerdictOnce := build
	noVerdictOnce.NoVerdict = 0
	noVerdictOnce.Rerun = 2
	reviewed := store.TaskBuild{
		Stage: StageReview, Base: "base", Attempt: 1, Revision: "rev", RedRun: "red-run", GreenRun: "green-run",
	}
	rerunOnce := store.TaskBuild{
		Stage: StageGreen, Base: "base", Attempt: 1, Rerun: 3, NoVerdict: 1, Revision: "rev", RedRun: "red-run",
	}
	passed := &store.EvidenceRun{ID: "green-run", Ended: ended, Verdict: "holds", Passed: true}
	tests := []struct {
		name string
		view View
		want Action
	}{
		{
			name: "no run runs green at the build's revision",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: build}},
			want: Action{
				Kind: ActStartRun, Phase: StageGreen, ID: RunID(decideEpic, 3, StageGreen, 1, 2), Revision: "rev",
			},
		},
		{
			name: "no run while paused waits",
			view: View{Epic: decideEpic, Paused: true, Task: store.EpicTask{Seq: 3, Build: build}},
			want: Action{Kind: ActNone},
		},
		{
			name: "no run while parked waits",
			view: View{Epic: decideEpic, Parked: true, Task: store.EpicTask{Seq: 3, Build: build}},
			want: Action{Kind: ActNone},
		},
		{
			name: "run not ended waits",
			view: View{
				Epic: decideEpic,
				Run:  &store.EvidenceRun{ID: "green-run"},
				Task: store.EpicTask{Seq: 3, Build: build},
			},
			want: Action{Kind: ActNone},
		},
		{
			name: "run not captured spends the first attempt",
			view: View{Epic: decideEpic, Run: &store.EvidenceRun{
				ID: "green-run", Ended: ended, NotCaptured: "no log",
			}, Task: store.EpicTask{Seq: 3, Build: firstAttempt}},
			want: Action{Kind: ActFail, Reason: "green not captured: no log"},
		},
		{
			name: "run not captured on the second attempt fails",
			view: View{Epic: decideEpic, Run: &store.EvidenceRun{
				ID: "green-run", Ended: ended, NotCaptured: "no log",
			}, Task: store.EpicTask{Seq: 3, Build: secondAttempt}},
			want: Action{Kind: ActFail, Reason: "green not captured: no log"},
		},
		{
			name: "no verdict once reruns the green run",
			view: View{Epic: decideEpic, Run: &store.EvidenceRun{
				ID: "green-run", Ended: ended, JudgeReason: "judge wrote nothing",
			}, Task: store.EpicTask{Seq: 3, Build: noVerdictOnce}},
			want: Action{Kind: ActMove, Next: rerunOnce},
		},
		{
			name: "no verdict twice moves on to review with a note",
			view: View{Epic: decideEpic, Run: &store.EvidenceRun{
				ID: "green-run", Ended: ended, JudgeReason: "judge wrote nothing",
			}, Task: store.EpicTask{Seq: 3, Build: build}},
			want: Action{
				Kind: ActMove, Next: reviewed,
				Notes: []string{"T3 green: the judge gave no verdict twice: judge wrote nothing"},
				Tells: []string{"T3 green: the judge gave no verdict twice: judge wrote nothing"},
			},
		},
		{
			name: "passed without review gates keeps the green run and moves to review",
			view: View{Epic: decideEpic, Run: passed, Task: store.EpicTask{Seq: 3, Build: build}},
			want: Action{Kind: ActMove, Next: reviewed},
		},
		{
			name: "passed with review gates moves to review",
			view: View{Epic: decideEpic, ReviewGates: gates, Run: passed, Task: store.EpicTask{Seq: 3, Build: build}},
			want: Action{Kind: ActMove, Next: reviewed},
		},
		{
			name: "not met spends the first attempt",
			view: View{Epic: decideEpic, Run: &store.EvidenceRun{
				ID: "green-run", Ended: ended, Verdict: "fails", Exit: 1,
			}, Task: store.EpicTask{Seq: 3, Build: firstAttempt}},
			want: Action{Kind: ActFail, Reason: "green not met: exit 1, verdict fails"},
		},
		{
			name: "not met on the second attempt fails",
			view: View{Epic: decideEpic, Run: &store.EvidenceRun{
				ID: "green-run", Ended: ended, Verdict: "fails", Exit: 1,
			}, Task: store.EpicTask{Seq: 3, Build: secondAttempt}},
			want: Action{Kind: ActFail, Reason: "green not met: exit 1, verdict fails"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := decideGreen(tc.view)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("decideGreen = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDecideReview(t *testing.T) {
	t.Parallel()
	gates := []string{"lint", "security"}
	build := store.TaskBuild{
		Stage: StageReview, Base: "base", Attempt: 1, Rerun: 1, Revision: "rev", RedRun: "red-run",
		GreenRun: "green-run",
	}
	pastGates := build
	pastGates.GatePos = 2
	stopped := build
	stopped.Held = HeldStopped
	restarted := build
	restarted.Rerun = 2
	nextGate := build
	nextGate.GatePos = 1
	nextGate.Rerun = 0
	verifying := store.TaskBuild{
		Stage: StageVerify, Base: "base", Attempt: 1, Revision: "rev", RedRun: "red-run", GreenRun: "green-run",
	}
	noVerdictOnce := build
	noVerdictOnce.Rerun = 2
	noVerdictOnce.NoVerdict = 1
	noVerdictTwice := build
	noVerdictTwice.NoVerdict = 1
	reviewStep := func(end, result, reason string) *store.StepRow {
		return &store.StepRow{ID: "review-step", End: end, Result: result, Reason: reason}
	}
	fails := []store.TaskFinding{
		{
			Position: 0, ID: "F1", Claim: "claim one", Citation: "assertion one", Location: "a.go:1",
			Evidence: "seen one", VerifyBy: "run one",
		},
		{
			Position: 1, ID: "F2", Claim: "claim two", Citation: "assertion two", Location: "b.go:2",
			Evidence: "seen two", VerifyBy: "run two",
		},
	}
	tests := []struct {
		name string
		view View
		want Action
	}{
		{
			name: "past the last gate accepts reviewed",
			view: View{Epic: decideEpic, ReviewGates: gates, Task: store.EpicTask{Seq: 3, Build: pastGates}},
			want: Action{Kind: ActAccept, Next: pastGates, Reason: ReviewReviewed},
		},
		{
			name: "no review gates accepts unreviewed",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: pastGates}},
			want: Action{Kind: ActAccept, Next: pastGates, Reason: ReviewUnreviewed},
		},
		{
			name: "no step runs the gate's review",
			view: View{Epic: decideEpic, ReviewGates: gates, Task: store.EpicTask{Seq: 3, Build: build}},
			want: Action{Kind: ActStartStep, Unit: StageReview, ID: StepID(decideEpic, 3, StageReview, 1, 1, 0, 0)},
		},
		{
			name: "no step while paused waits",
			view: View{Epic: decideEpic, ReviewGates: gates, Paused: true, Task: store.EpicTask{Seq: 3, Build: build}},
			want: Action{Kind: ActNone},
		},
		{
			name: "no step while parked waits",
			view: View{Epic: decideEpic, ReviewGates: gates, Parked: true, Task: store.EpicTask{Seq: 3, Build: build}},
			want: Action{Kind: ActNone},
		},
		{
			name: "open step waits",
			view: View{
				Epic: decideEpic, ReviewGates: gates, Step: reviewStep("", "", ""),
				Task: store.EpicTask{Seq: 3, Build: build},
			},
			want: Action{Kind: ActNone},
		},
		{
			name: "stopped pauses with the build held stopped",
			view: View{
				Epic: decideEpic, ReviewGates: gates, Step: reviewStep("stopped", "", ""),
				Task: store.EpicTask{Seq: 3, Build: build},
			},
			want: Action{Kind: ActPause, Next: stopped},
		},
		{
			name: "engine restart reruns the review",
			view: View{
				Epic: decideEpic, ReviewGates: gates, Step: reviewStep("engine_restarted", "", ""),
				Task: store.EpicTask{Seq: 3, Build: build},
			},
			want: Action{Kind: ActMove, Next: restarted},
		},
		{
			name: "done holds and moves to the next gate",
			view: View{
				Epic: decideEpic, ReviewGates: gates, Step: reviewStep("done", "", ""),
				Task: store.EpicTask{Seq: 3, Build: build},
			},
			want: Action{
				Kind: ActReview,
				Review: store.TaskReview{
					Attempt: 1, Position: 0, Gate: "lint", Step: "review-step", Verdict: "holds",
				},
				Next: nextGate,
			},
		},
		{
			name: "gate failed with findings fails the review and verifies its findings",
			view: View{
				Epic: decideEpic, ReviewGates: gates,
				Step: reviewStep("gate_failed", gateFailedFindings, "gate failed"),
				Task: store.EpicTask{Seq: 3, Build: build},
			},
			want: Action{
				Kind: ActReview,
				Review: store.TaskReview{
					Attempt: 1, Position: 0, Gate: "lint", Step: "review-step", Verdict: "fails", Findings: fails,
				},
				Next: verifying,
			},
		},
		{
			name: "gate failed with findings and verify off settles them confirmed and unverified",
			view: View{
				Epic: decideEpic, ReviewGates: gates, NoVerify: true,
				Step: reviewStep("gate_failed", gateFailedFindings, "gate failed"),
				Task: store.EpicTask{Seq: 3, Build: build},
			},
			want: Action{
				Kind: ActReview,
				Review: store.TaskReview{
					Attempt: 1, Position: 0, Gate: "lint", Step: "review-step", Verdict: verdictFails,
					Findings: []store.TaskFinding{
						{
							Position: 0, ID: "F1", Claim: "claim one", Citation: "assertion one", Location: "a.go:1",
							Evidence: "seen one", VerifyBy: "run one", Step: "review-step",
							Result: resultConfirmed, Unverified: true,
						},
						{
							Position: 1, ID: "F2", Claim: "claim two", Citation: "assertion two", Location: "b.go:2",
							Evidence: "seen two", VerifyBy: "run two", Step: "review-step",
							Result: resultConfirmed, Unverified: true,
						},
					},
				},
				Next: store.TaskBuild{
					Stage: StageVerify, Base: "base", Attempt: 1, Revision: "rev", RedRun: "red-run",
					GreenRun: "green-run", FindingPos: 2,
				},
			},
		},
		{
			name: "gate failed with an undetermined result gives no verdict once",
			view: View{
				Epic: decideEpic, ReviewGates: gates,
				Step: reviewStep("gate_failed", gateFailedUndetermined, "limit ran out"),
				Task: store.EpicTask{Seq: 3, Build: build},
			},
			want: Action{Kind: ActMove, Next: noVerdictOnce},
		},
		{
			name: "gate failed with a blocked return gives no verdict once",
			view: View{
				Epic: decideEpic, ReviewGates: gates,
				Step: reviewStep("gate_failed", gateBlocked, "gate failed"),
				Task: store.EpicTask{Seq: 3, Build: build},
			},
			want: Action{Kind: ActMove, Next: noVerdictOnce},
		},
		{
			name: "gate failed with no failed assertion gives no verdict once",
			view: View{
				Epic: decideEpic, ReviewGates: gates,
				Step: reviewStep("gate_failed", gateNoFindings, "gate failed"),
				Task: store.EpicTask{Seq: 3, Build: build},
			},
			want: Action{Kind: ActMove, Next: noVerdictOnce},
		},
		{
			name: "failed gives no verdict once",
			view: View{
				Epic: decideEpic, ReviewGates: gates, Step: reviewStep("failed", "", "boom"),
				Task: store.EpicTask{Seq: 3, Build: build},
			},
			want: Action{Kind: ActMove, Next: noVerdictOnce},
		},
		{
			name: "failed twice stores the review undetermined and moves to the next gate",
			view: View{
				Epic: decideEpic, ReviewGates: gates, Step: reviewStep("failed", "", "boom"),
				Task: store.EpicTask{Seq: 3, Build: noVerdictTwice},
			},
			want: Action{
				Kind: ActReview,
				Review: store.TaskReview{
					Attempt: 1, Position: 0, Gate: "lint", Step: "review-step", Verdict: verdictUndetermined,
					Reason: "failed: boom",
				},
				Next: nextGate,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := decideReview(tc.view)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("decideReview = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestDecideReviewVerifyOffSpendsAttemptWithoutVerifier covers R4: with verify off a failed review stores its findings
// confirmed and unverified, and the verify unit after it spends the attempt with their claims and starts no verifier.
func TestDecideReviewVerifyOffSpendsAttemptWithoutVerifier(t *testing.T) {
	t.Parallel()
	build := store.TaskBuild{
		Stage: StageReview, Base: "base", Attempt: 1, Revision: "rev", RedRun: "red-run", GreenRun: "green-run",
	}
	review := decideReview(View{
		Epic: decideEpic, ReviewGates: []string{"lint"}, NoVerify: true,
		Step: &store.StepRow{ID: "review-step", End: endGateFailed, Result: gateFailedFindings, Reason: "gate failed"},
		Task: store.EpicTask{Seq: 3, Build: build},
	})
	if review.Kind != ActReview {
		t.Fatalf("decideReview kind = %s, want %s", review.Kind, ActReview)
	}
	got := decideVerify(View{
		Epic: decideEpic,
		Task: store.EpicTask{Seq: 3, Build: review.Next, Reviews: []store.TaskReview{review.Review}},
	})
	want := Action{
		Kind: ActFail, Reason: "confirmed: F1 claim one; confirmed: F2 claim two",
		Move: store.RungMove{MovedBy: store.MovedByFinding, Assertion: "F1 claim one; F2 claim two"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decideVerify = %+v, want %+v", got, want)
	}
}

// TestUnverifiedFindings covers R4 at epic stage: a failed epic-stage verdict's findings settle as confirmed and
// unverified, each with the step that raised it, and a verdict with no failed assertion settles none.
func TestUnverifiedFindings(t *testing.T) {
	t.Parallel()
	got, ok := UnverifiedFindings("epic-review-step", gateFailedFindings)
	want := []store.TaskFinding{
		{
			Position: 0, ID: "F1", Claim: "claim one", Citation: "assertion one", Location: "a.go:1",
			Evidence: "seen one", VerifyBy: "run one", Step: "epic-review-step",
			Result: resultConfirmed, Unverified: true,
		},
		{
			Position: 1, ID: "F2", Claim: "claim two", Citation: "assertion two", Location: "b.go:2",
			Evidence: "seen two", VerifyBy: "run two", Step: "epic-review-step",
			Result: resultConfirmed, Unverified: true,
		},
	}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("UnverifiedFindings = %+v, %t, want %+v, true", got, ok, want)
	}
	if _, ok := UnverifiedFindings("epic-review-step", gateNoFindings); ok {
		t.Errorf("UnverifiedFindings of a return with no findings = true, want false")
	}
}

// TestViewNoVerifyFollowsTheGatePosReviewer covers R4: a task's view has verify off when the task-stage reviewer at
// its gate position does. Epic-stage reviewers do not take a gate position.
func TestViewNoVerifyFollowsTheGatePosReviewer(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t, "lint", "security")
	epic := f.epic()
	epic.Reviewers = []reviewers.Reviewer{
		{Name: "judge", Enabled: true, Stage: []string{reviewers.StageEpic}, Verify: false},
		{Name: "lint", Enabled: true, Stage: []string{reviewers.StageTask}, Verify: false},
		{Name: "security", Enabled: true, Stage: []string{reviewers.StageTask}, Verify: true},
	}
	stored := f.task()
	tests := []struct {
		name    string
		gatePos int64
		want    bool
	}{
		{name: "verify off at the gate position", gatePos: 0, want: true},
		{name: "verify on at the gate position", gatePos: 1, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			task := stored
			task.Build = store.TaskBuild{Stage: StageReview, Base: f.base, Attempt: 1, GatePos: tc.gatePos}
			view, err := f.runner.view(f.ctx, epic, task, f.base)
			if err != nil {
				t.Fatalf("view: %v", err)
			}
			if view.NoVerify != tc.want {
				t.Errorf("view.NoVerify = %t, want %t", view.NoVerify, tc.want)
			}
		})
	}
}

func TestDecideVerify(t *testing.T) {
	t.Parallel()
	build := store.TaskBuild{
		Stage: StageVerify, Base: "base", Attempt: 1, Rerun: 1, Revision: "rev", RedRun: "red-run",
		GreenRun: "green-run",
	}
	pastFindings := build
	pastFindings.FindingPos = 2
	secondAttempt := pastFindings
	secondAttempt.Attempt = 2
	nextGate := store.TaskBuild{
		Stage: StageReview, Base: "base", Attempt: 1, Revision: "rev", RedRun: "red-run", GreenRun: "green-run",
		GatePos: 1,
	}
	noVerdictOnce := build
	noVerdictOnce.Rerun = 2
	noVerdictOnce.NoVerdict = 1
	noVerdictTwice := build
	noVerdictTwice.NoVerdict = 1
	stopped := build
	stopped.Held = HeldStopped
	restarted := build
	restarted.Rerun = 2
	nextFinding := build
	nextFinding.FindingPos = 1
	nextFinding.Rerun = 0
	verifyStep := func(end, result, reason string) *store.StepRow {
		return &store.StepRow{ID: "verify-step", End: end, Result: result, Reason: reason}
	}
	finding := func(result, reason string) store.TaskFindingResult {
		return store.TaskFindingResult{
			Attempt: 1, Review: 0, Position: 0, Step: "verify-step", Result: result, Reason: reason,
		}
	}
	tests := []struct {
		name string
		view View
		want Action
	}{
		{
			name: "past the last finding with none confirmed moves to the next gate",
			view: View{Epic: decideEpic, Task: store.EpicTask{
				Seq: 3, Build: pastFindings, Reviews: verifyReviews(1, "dropped", "undecided"),
			}},
			want: Action{Kind: ActMove, Next: nextGate},
		},
		{
			name: "past the last finding with a confirmed result spends the first attempt",
			view: View{Epic: decideEpic, Task: store.EpicTask{
				Seq: 3, Build: pastFindings, Reviews: verifyReviews(1, "confirmed", "dropped"),
			}},
			want: Action{
				Kind: ActFail, Reason: "confirmed: F1 claim one",
				Move: store.RungMove{MovedBy: store.MovedByFinding, Assertion: "F1 claim one"},
			},
		},
		{
			name: "several confirmed results join their claims",
			view: View{Epic: decideEpic, Task: store.EpicTask{
				Seq: 3, Build: pastFindings, Reviews: verifyReviews(1, "confirmed", "confirmed"),
			}},
			want: Action{
				Kind: ActFail, Reason: "confirmed: F1 claim one; confirmed: F2 claim two",
				Move: store.RungMove{MovedBy: store.MovedByFinding, Assertion: "F1 claim one; F2 claim two"},
			},
		},
		{
			name: "past the last finding with a confirmed result on the second attempt fails",
			view: View{Epic: decideEpic, Task: store.EpicTask{
				Seq: 3, Build: secondAttempt, Reviews: verifyReviews(2, "confirmed", "dropped"),
			}},
			want: Action{
				Kind: ActFail, Reason: "confirmed: F1 claim one",
				Move: store.RungMove{MovedBy: store.MovedByFinding, Assertion: "F1 claim one"},
			},
		},
		{
			name: "no step runs the finding's verifier",
			view: View{Epic: decideEpic, Task: store.EpicTask{
				Seq: 3, Build: build, Reviews: verifyReviews(1, "", ""),
			}},
			want: Action{Kind: ActStartStep, Unit: StageVerify, ID: StepID(decideEpic, 3, StageVerify, 1, 1, 0, 0)},
		},
		{
			name: "no step while paused waits",
			view: View{Epic: decideEpic, Paused: true, Task: store.EpicTask{
				Seq: 3, Build: build, Reviews: verifyReviews(1, "", ""),
			}},
			want: Action{Kind: ActNone},
		},
		{
			name: "no step while parked waits",
			view: View{Epic: decideEpic, Parked: true, Task: store.EpicTask{
				Seq: 3, Build: build, Reviews: verifyReviews(1, "", ""),
			}},
			want: Action{Kind: ActNone},
		},
		{
			name: "open step waits",
			view: View{Epic: decideEpic, Step: verifyStep("", "", ""), Task: store.EpicTask{
				Seq: 3, Build: build, Reviews: verifyReviews(1, "", ""),
			}},
			want: Action{Kind: ActNone},
		},
		{
			name: "stopped pauses with the build held stopped",
			view: View{Epic: decideEpic, Step: verifyStep("stopped", "", ""), Task: store.EpicTask{
				Seq: 3, Build: build, Reviews: verifyReviews(1, "", ""),
			}},
			want: Action{Kind: ActPause, Next: stopped},
		},
		{
			name: "engine restart reruns the verifier",
			view: View{Epic: decideEpic, Step: verifyStep("engine_restarted", "", ""), Task: store.EpicTask{
				Seq: 3, Build: build, Reviews: verifyReviews(1, "", ""),
			}},
			want: Action{Kind: ActMove, Next: restarted},
		},
		{
			name: "done confirmed records the finding confirmed",
			view: View{Epic: decideEpic, Step: verifyStep("done", verifiedAs("confirmed"), ""), Task: store.EpicTask{
				Seq: 3, Build: build, Reviews: verifyReviews(1, "", ""),
			}},
			want: Action{Kind: ActFinding, Finding: finding("confirmed", "the claim holds"), Next: nextFinding},
		},
		{
			name: "done dropped records the finding dropped",
			view: View{Epic: decideEpic, Step: verifyStep("done", verifiedAs("dropped"), ""), Task: store.EpicTask{
				Seq: 3, Build: build, Reviews: verifyReviews(1, "", ""),
			}},
			want: Action{Kind: ActFinding, Finding: finding("dropped", "the claim holds"), Next: nextFinding},
		},
		{
			name: "done undetermined records the finding undecided",
			view: View{Epic: decideEpic, Step: verifyStep("done", verifiedAs("undetermined"), ""), Task: store.EpicTask{
				Seq: 3, Build: build, Reviews: verifyReviews(1, "", ""),
			}},
			want: Action{Kind: ActFinding, Finding: finding("undecided", "the claim holds"), Next: nextFinding},
		},
		{
			name: "returned records the finding undecided as blocked",
			view: View{Epic: decideEpic, Step: verifyStep("returned", "", "needs context"), Task: store.EpicTask{
				Seq: 3, Build: build, Reviews: verifyReviews(1, "", ""),
			}},
			want: Action{Kind: ActFinding, Finding: finding("undecided", "blocked: needs context"), Next: nextFinding},
		},
		{
			name: "done without a result gives no verdict once",
			view: View{
				Epic: decideEpic,
				Step: verifyStep("done", `{"status":"done","results":[]}`, ""),
				Task: store.EpicTask{
					Seq: 3, Build: build, Reviews: verifyReviews(1, "", ""),
				},
			},
			want: Action{Kind: ActMove, Next: noVerdictOnce},
		},
		{
			name: "failed gives no verdict once",
			view: View{Epic: decideEpic, Step: verifyStep("failed", "", "boom"), Task: store.EpicTask{
				Seq: 3, Build: build, Reviews: verifyReviews(1, "", ""),
			}},
			want: Action{Kind: ActMove, Next: noVerdictOnce},
		},
		{
			name: "failed twice stores the finding undecided and moves on",
			view: View{Epic: decideEpic, Step: verifyStep("failed", "", "boom"), Task: store.EpicTask{
				Seq: 3, Build: noVerdictTwice, Reviews: verifyReviews(1, "", ""),
			}},
			want: Action{
				Kind: ActFinding,
				Finding: store.TaskFindingResult{
					Attempt:  1,
					Review:   0,
					Position: 0,
					Step:     "verify-step",
					Result:   resultUndecided,
					Reason:   "failed: boom",
				},
				Next: nextFinding,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := decideVerify(tc.view)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("decideVerify = %+v, want %+v", got, tc.want)
			}
		})
	}
}
