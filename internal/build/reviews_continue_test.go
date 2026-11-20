package build

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
)

// reviewsContinueEnded is when the evidence runs of the continue tests ended.
func reviewsContinueEnded() time.Time {
	return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
}

// TestReviewsContinueUndeterminedReviewIsStoredAndMovesOn: a review gate whose second run gives no verdict is stored
// as undetermined with the reason it ended so, and the task moves on to its next gate.
func TestReviewsContinueUndeterminedReviewIsStoredAndMovesOn(t *testing.T) {
	t.Parallel()
	view := View{
		Epic: decideEpic, ReviewGates: []string{"lint", "security"},
		Task: store.EpicTask{Seq: 3, Build: store.TaskBuild{
			Stage: StageReview, Base: "base", Attempt: 1, Rerun: 2, NoVerdict: 1, Revision: "rev", RedRun: "red-run",
		}},
		Step: &store.StepRow{ID: "review-step", End: endGateFailed, Result: "not json", Reason: "limit ran out"},
	}
	got := decideReview(view)
	want := Action{
		Kind: ActReview,
		Review: store.TaskReview{
			Attempt: 1, Position: 0, Gate: "lint", Step: "review-step", Verdict: verdictUndetermined,
			Reason: "gate_failed: limit ran out",
		},
		Next: store.TaskBuild{
			Stage:    StageReview,
			Base:     "base",
			Attempt:  1,
			GatePos:  1,
			Revision: "rev",
			RedRun:   "red-run",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decideReview with a second silent run = %+v, want %+v", got, want)
	}
}

// TestReviewsContinueFirstSilentReviewReruns: a review gate's first run that gives no verdict still reruns the gate.
func TestReviewsContinueFirstSilentReviewReruns(t *testing.T) {
	t.Parallel()
	view := View{
		Epic: decideEpic, ReviewGates: []string{"lint"},
		Task: store.EpicTask{Seq: 3, Build: store.TaskBuild{
			Stage: StageReview, Base: "base", Attempt: 1, Rerun: 2, Revision: "rev", RedRun: "red-run",
		}},
		Step: &store.StepRow{ID: "review-step", End: endGateFailed, Result: "not json", Reason: "limit ran out"},
	}
	got := decideReview(view)
	want := Action{Kind: ActMove, Next: store.TaskBuild{
		Stage: StageReview, Base: "base", Attempt: 1, Rerun: 3, NoVerdict: 1, Revision: "rev", RedRun: "red-run",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decideReview with a first silent run = %+v, want %+v", got, want)
	}
}

// TestReviewsContinueFindingStillSendsTheTaskBack: a finding a verifier confirmed still climbs the task, so only a
// named defect goes back.
func TestReviewsContinueFindingStillSendsTheTaskBack(t *testing.T) {
	t.Parallel()
	view := View{
		Epic: decideEpic, ReviewGates: []string{"lint", "security"},
		Task: store.EpicTask{Seq: 3, Build: store.TaskBuild{
			Stage: StageVerify, Base: "base", Attempt: 1, FindingPos: 2, Revision: "rev",
		}, Reviews: verifyReviews(1, "confirmed", "dropped")},
	}
	got := decideVerify(view)
	want := Action{
		Kind:   ActFail,
		Reason: "confirmed: F1 claim one",
		Move:   store.RungMove{MovedBy: store.MovedByFinding, Gate: "lint", Assertion: "F1 claim one"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decideVerify with a confirmed finding = %+v, want %+v", got, want)
	}
}

// TestReviewsContinueSilentVerifierStoresItsFindingUndecided: a verifier silent twice leaves its finding undecided
// with the reason, which does not send the task back.
func TestReviewsContinueSilentVerifierStoresItsFindingUndecided(t *testing.T) {
	t.Parallel()
	view := View{
		Epic: decideEpic, ReviewGates: []string{"lint"},
		Task: store.EpicTask{Seq: 3, Build: store.TaskBuild{
			Stage: StageVerify, Base: "base", Attempt: 1, Rerun: 2, NoVerdict: 1, Revision: "rev",
		}, Reviews: verifyReviews(1, "", "")},
		Step: &store.StepRow{ID: "verify-step", End: endGateFailed, Reason: "limit ran out"},
	}
	got := decideVerify(view)
	want := Action{
		Kind: ActFinding,
		Finding: store.TaskFindingResult{
			Attempt: 1, Review: 0, Position: 0, Step: "verify-step", Result: resultUndecided,
			Reason: "gate_failed: limit ran out",
		},
		Next: store.TaskBuild{Stage: StageVerify, Base: "base", Attempt: 1, FindingPos: 1, Revision: "rev"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decideVerify with a second silent run = %+v, want %+v", got, want)
	}
}

// TestReviewsContinueSilentJudgeProceedsWithANote: a red or green judge silent twice proceeds, and the note of its
// silence is stored with the move and told to the owner.
func TestReviewsContinueSilentJudgeProceedsWithANote(t *testing.T) {
	t.Parallel()
	red := View{
		Epic: decideEpic, Branch: decideBranch,
		Run: &store.EvidenceRun{ID: "red-run", Ended: reviewsContinueEnded(), JudgeReason: "judge wrote nothing"},
		Task: store.EpicTask{Seq: 3, Build: store.TaskBuild{
			Stage: StageRed, Base: decideBranch, Rerun: 1, NoVerdict: 1,
		}},
	}
	redNote := "T3 red: the judge gave no verdict twice: judge wrote nothing"
	if got, want := Decide(red), (Action{
		Kind:  ActMove,
		Next:  store.TaskBuild{Stage: StageImplement, Base: decideBranch, Attempt: 1, RedRun: "red-run"},
		Notes: []string{redNote}, Tells: []string{redNote},
	}); !reflect.DeepEqual(got, want) {
		t.Errorf("Decide of a red judge silent twice = %+v, want %+v", got, want)
	}

	green := View{
		Epic: decideEpic,
		Run:  &store.EvidenceRun{ID: "green-run", Ended: reviewsContinueEnded(), JudgeReason: "judge wrote nothing"},
		Task: store.EpicTask{Seq: 3, Build: store.TaskBuild{
			Stage: StageGreen, Base: "base", Attempt: 1, Rerun: 2, NoVerdict: 1, Revision: "rev", RedRun: "red-run",
		}},
	}
	greenNote := "T3 green: the judge gave no verdict twice: judge wrote nothing"
	if got, want := Decide(green), (Action{
		Kind: ActMove,
		Next: store.TaskBuild{
			Stage: StageReview, Base: "base", Attempt: 1, Revision: "rev", RedRun: "red-run", GreenRun: "green-run",
		},
		Notes: []string{greenNote}, Tells: []string{greenNote},
	}); !reflect.DeepEqual(got, want) {
		t.Errorf("Decide of a green judge silent twice = %+v, want %+v", got, want)
	}
}

// TestReviewsContinueRedThatDoesNotHoldIsANote: a red run whose conclusion holds at the base, or that was not
// captured, is recorded as a note and the build goes on to implement, with nothing spent.
func TestReviewsContinueRedThatDoesNotHoldIsANote(t *testing.T) {
	t.Parallel()
	red := store.TaskBuild{Stage: StageRed, Base: decideBranch, Rerun: 1}
	implement := store.TaskBuild{Stage: StageImplement, Base: decideBranch, Attempt: 1, RedRun: "red-run"}
	tests := []struct {
		name string
		run  store.EvidenceRun
		note string
	}{
		{
			name: "conclusion holds at the base",
			run:  store.EvidenceRun{ID: "red-run", Ended: reviewsContinueEnded(), Verdict: "holds", Exit: 0},
			note: "T3 red: the conclusion holds at the base (exit 0)",
		},
		{
			name: "not captured",
			run:  store.EvidenceRun{ID: "red-run", Ended: reviewsContinueEnded(), NotCaptured: "no log"},
			note: "T3 red: not captured: no log",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := tc.run
			view := View{Epic: decideEpic, Branch: decideBranch, Run: &run, Task: store.EpicTask{Seq: 3, Build: red}}
			got := Decide(view)
			want := Action{Kind: ActMove, Next: implement, Notes: []string{tc.note}, Tells: []string{tc.note}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Decide = %+v, want %+v", got, want)
			}
		})
	}
}

// TestReviewsContinueCheckingVersionAppliesWhateverRedSays: a checking version whose red does not hold applies anyway.
// The note lands first, on a move that keeps the build at red, and the settle follows once the note is kept.
func TestReviewsContinueCheckingVersionAppliesWhateverRedSays(t *testing.T) {
	t.Parallel()
	build := store.TaskBuild{Stage: StageRed, Base: decideBranch, Attempt: 1}
	versions := []store.TaskVersion{{Version: 2, State: store.VersionChecking}}
	run := &store.EvidenceRun{ID: "red-run", Ended: reviewsContinueEnded(), Verdict: "holds", Exit: 0}
	note := "T3 red: the conclusion holds at the base (exit 0)"
	view := View{Epic: decideEpic, Branch: decideBranch, Run: run, Task: store.EpicTask{
		Seq: 3, Build: build, Versions: versions,
	}}
	noted := Action{Kind: ActMove, Next: build, Notes: []string{note}, Tells: []string{note}}
	if got := Decide(view); !reflect.DeepEqual(got, noted) {
		t.Fatalf("Decide before the note is kept = %+v, want %+v", got, noted)
	}
	view.Task.Notes = []string{note}
	got := Decide(view)
	want := Action{
		Kind:   ActSettleVersion,
		Next:   store.TaskBuild{Stage: StageImplement, Base: decideBranch, Attempt: 1, RedRun: "red-run"},
		Settle: &store.TaskVersionSettle{Version: 2, State: store.VersionApplied, RedRun: "red-run"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Decide after the note is kept = %+v, want %+v", got, want)
	}
}

// TestReviewsContinueReproductionExitZeroIsANote: a test item's reproduction that exits 0 is recorded as a note, not
// spent, and the green run starts once the note is kept. The same holds for a reproduction that did not run.
func TestReviewsContinueReproductionExitZeroIsANote(t *testing.T) {
	t.Parallel()
	build := store.TaskBuild{Stage: StageGreen, Base: "base", Attempt: 1, Revision: "rev", RedRun: "red-run"}
	item := evidence.Item{Kind: evidence.KindTest}
	repro := RunID(decideEpic, 3, unitRepro, 1, 0)
	greenRun := Action{
		Kind:     ActStartRun,
		Phase:    StageGreen,
		Revision: "rev",
		ID:       RunID(decideEpic, 3, StageGreen, 1, 0),
	}
	tests := []struct {
		name  string
		check store.NodeCheck
		note  string
	}{
		{
			name:  "exited 0",
			check: store.NodeCheck{ID: repro, State: store.NodeEnded, Exit: 0},
			note: "T3 attempt 1 reproduction: exited 0 on the base with the commit's test files, so they pass " +
				"without the rest of the commit",
		},
		{
			name:  "did not run",
			check: store.NodeCheck{ID: repro, State: store.NodeEnded, NotRun: "boom"},
			note:  "T3 attempt 1 reproduction: did not run: boom",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			check := tc.check
			view := View{Epic: decideEpic, Repro: &check, Task: store.EpicTask{Seq: 3, Build: build, Evidence: item}}
			got := Decide(view)
			want := Action{Kind: ActMove, Next: build, Notes: []string{tc.note}, Tells: []string{tc.note}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Decide before the note is kept = %+v, want %+v", got, want)
			}
			view.Task.Notes = []string{tc.note}
			if got := Decide(view); !reflect.DeepEqual(got, greenRun) {
				t.Errorf("Decide after the note is kept = %+v, want %+v", got, greenRun)
			}
		})
	}
}

// TestReviewsContinueSilentImplementerSpendsItsAttempt: an implementer whose return gives no verdict twice spends the
// attempt, with the reason, rather than parking the task.
func TestReviewsContinueSilentImplementerSpendsItsAttempt(t *testing.T) {
	t.Parallel()
	view := View{
		Epic: decideEpic,
		Task: store.EpicTask{Seq: 3, Build: store.TaskBuild{
			Stage: StageImplement, Base: "base", Attempt: 1, Rerun: 1, NoVerdict: 1, Step: "impl-step",
		}},
		Step: &store.StepRow{ID: "impl-step", End: endDone, Result: "not json", Reason: "bad return"},
	}
	got := decideImplement(view)
	want := Action{Kind: ActFail, Reason: "T3 implement gave no verdict twice: done: bad return"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decideImplement with a second silent return = %+v, want %+v", got, want)
	}
}

// TestReviewsContinueFloorPromptCarriesNotesAndFacts: the floor reviewer's prompt carries the build's notes and the
// facts it is given, and no other reviewer's prompt carries the notes.
func TestReviewsContinueFloorPromptCarriesNotesAndFacts(t *testing.T) {
	t.Parallel()
	epic := briefEpic()
	task := briefTask()
	note := "T4 red: the conclusion holds at the base (exit 0)"
	fact := "a_test.go: TestOld: test present at the base removed"
	task.Notes = []string{note}
	floor := ReviewPrompt(epic, task, "floor", "base1", "rev1", store.StepCheck{}, nil, fact)
	if !strings.Contains(floor, note) || !strings.Contains(floor, fact) {
		t.Errorf("floor prompt = %q, want the note %q and the fact %q", floor, note, fact)
	}
	other := ReviewPrompt(epic, task, "review", "base1", "rev1", store.StepCheck{}, nil)
	if strings.Contains(other, note) {
		t.Errorf("review prompt carries the note %q, want the floor reviewer's prompt alone", note)
	}
}

// TestReviewsContinueFloorReviewStartCarriesTheGuardFacts: the floor reviewer's start carries the facts the guard
// gives the commit it reviews, read at the review's start, while the other reviewers' starts carry none.
func TestReviewsContinueFloorReviewStartCarriesTheGuardFacts(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t)
	repo := setup.epic.Repository
	const guarded = "internal/build/guarded_test.go"
	if err := os.MkdirAll(filepath.Join(repo, "internal", "build"), 0o755); err != nil {
		t.Fatalf("make guarded directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, guarded), []byte("package build\n"), 0o600); err != nil {
		t.Fatalf("write guarded file: %v", err)
	}
	buildRun(t, repo, "add", guarded)
	buildRun(t, repo, "commit", "-q", "-m", "add guarded test")
	base := buildHead(t, repo)
	buildRun(t, repo, "rm", "-q", guarded)
	buildRun(t, repo, "commit", "-q", "-m", "remove guarded test")
	revision := buildHead(t, repo)

	setup.epic.ReviewGates = []string{"floor"}
	setup.epic.Reviewers = []reviewers.Reviewer{agentReviewer("floor")}
	setup.epic.Tasks = []store.EpicTask{{Seq: 4, Status: "open", Evidence: evidence.Item{Tests: []string{guarded}}}}
	task := reviewTask(base, revision, 0)
	id := StepID(setup.epic.ID, task.Seq, StageReview, task.Build.Attempt, task.Build.Rerun, 0, 0)
	steps := &startSteps{}
	if _, err := startRunner(setup, steps, &startEvidence{}).startStep(
		context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageReview, ID: id},
	); err != nil {
		t.Fatalf("startStep(floor) = %v, want nil", err)
	}
	start := onlyStart(t, steps)
	fact := guarded + ": guarded file deleted"
	if !strings.Contains(start.Prompt, fact) {
		t.Errorf("floor start Prompt = %q, want the guard's fact %q", start.Prompt, fact)
	}
}
