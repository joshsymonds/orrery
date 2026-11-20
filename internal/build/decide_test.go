package build

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// decideBranch is the epic branch a task's red unit runs at.
const decideBranch = "epic/acme-epic"

// decideCandidate is the red candidate of an epic whose batch failed, which a fresh task starts at.
const decideCandidate = "candidate-commit"

// refsSnapshot is the refs snapshot an implementer's step was checked against.
const refsSnapshot = "refs-snapshot"

// decideCase is one decision: the view it is made of and the action it returns.
type decideCase struct {
	name string
	view View
	want Action
}

// runDecideCases runs each case through decide and compares the action it returns with the one the case wants.
func runDecideCases(t *testing.T, decide func(View) Action, cases []decideCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := decide(tc.view)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("decision = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDecideHeld(t *testing.T) {
	t.Parallel()
	stopped := store.TaskBuild{
		Stage: StageImplement, Base: decideBranch, Attempt: 1, Rerun: 2, Step: "impl-step", Held: HeldStopped,
	}
	released := stopped
	released.Held = ""
	released.Rerun = 3
	parked := store.TaskBuild{
		Stage: StageGreen, Base: decideBranch, Attempt: 1, Rerun: 1, NoVerdict: 2, Held: HeldParked,
		Revision: "rev", RedRun: "red-run",
	}
	unparked := parked
	unparked.Held = ""
	unparked.Rerun = 2
	unparked.NoVerdict = 0
	runDecideCases(t, Decide, []decideCase{
		{
			name: "stopped and running is released with a rerun",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: stopped}},
			want: Action{Kind: ActMove, Next: released},
		},
		{
			name: "stopped and paused waits",
			view: View{Epic: decideEpic, Paused: true, Task: store.EpicTask{Seq: 3, Build: stopped}},
			want: Action{Kind: ActNone},
		},
		{
			name: "stopped and parked waits",
			view: View{Epic: decideEpic, Parked: true, Task: store.EpicTask{Seq: 3, Build: stopped}},
			want: Action{Kind: ActNone},
		},
		{
			name: "parked and unparked is released with a rerun and no verdicts",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: parked}},
			want: Action{Kind: ActMove, Next: unparked},
		},
		{
			name: "parked and parked waits",
			view: View{Epic: decideEpic, Parked: true, Task: store.EpicTask{Seq: 3, Build: parked}},
			want: Action{Kind: ActNone},
		},
	})
}

func TestDecideFresh(t *testing.T) {
	t.Parallel()
	runDecideCases(t, Decide, []decideCase{
		{
			name: "a fresh task with a branch starts at red on the branch",
			view: View{Epic: decideEpic, Branch: decideBranch, Task: store.EpicTask{Seq: 3}},
			want: Action{Kind: ActMove, Next: store.TaskBuild{Stage: StageRed, Base: decideBranch}},
		},
		{
			name: "a fresh task with a candidate starts at red on the candidate",
			view: View{
				Epic: decideEpic, Branch: decideBranch, Candidate: decideCandidate, Task: store.EpicTask{Seq: 3},
			},
			want: Action{Kind: ActMove, Next: store.TaskBuild{Stage: StageRed, Base: decideCandidate}},
		},
		{
			name: "a fresh task with no branch waits",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3}},
			want: Action{Kind: ActNone},
		},
		{
			name: "a fresh task while paused waits",
			view: View{Epic: decideEpic, Paused: true, Branch: decideBranch, Task: store.EpicTask{Seq: 3}},
			want: Action{Kind: ActNone},
		},
		{
			name: "a fresh task while parked waits",
			view: View{Epic: decideEpic, Parked: true, Branch: decideBranch, Task: store.EpicTask{Seq: 3}},
			want: Action{Kind: ActNone},
		},
	})
}

// TestViewCandidate pins that a runner's view of a fresh task carries the epic's candidate, so Decide starts the task
// at it.
func TestViewCandidate(t *testing.T) {
	t.Parallel()
	epic := store.EpicRow{ID: decideEpic, Candidate: decideCandidate}
	view, err := New(Config{}).view(context.Background(), epic, store.EpicTask{Seq: 3}, decideBranch)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if view.Candidate != decideCandidate {
		t.Fatalf("view Candidate = %q, want %q", view.Candidate, decideCandidate)
	}
	want := Action{Kind: ActMove, Next: store.TaskBuild{Stage: StageRed, Base: decideCandidate}}
	if got := Decide(view); !reflect.DeepEqual(got, want) {
		t.Fatalf("Decide = %+v, want %+v", got, want)
	}
}

func TestDecideRed(t *testing.T) {
	t.Parallel()
	ended := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	red := store.TaskBuild{Stage: StageRed, Base: decideBranch, Rerun: 1}
	noVerdictOnce := red
	noVerdictOnce.Rerun = 2
	noVerdictOnce.NoVerdict = 1
	withVerdict := red
	withVerdict.NoVerdict = 1
	passed := store.TaskBuild{Stage: StageImplement, Base: decideBranch, Attempt: 1, RedRun: "red-run"}
	runDecideCases(t, Decide, []decideCase{
		{
			name: "no run starts red at the base",
			view: View{Epic: decideEpic, Branch: decideBranch, Task: store.EpicTask{Seq: 3, Build: red}},
			want: Action{
				Kind: ActStartRun, Phase: StageRed, Revision: decideBranch,
				ID: RunID(decideEpic, 3, StageRed, 0, 1),
			},
		},
		{
			name: "no run while paused waits",
			view: View{Epic: decideEpic, Branch: decideBranch, Paused: true, Task: store.EpicTask{Seq: 3, Build: red}},
			want: Action{Kind: ActNone},
		},
		{
			name: "run not ended waits",
			view: View{
				Epic: decideEpic, Run: &store.EvidenceRun{ID: "red-run"},
				Task: store.EpicTask{Seq: 3, Build: red},
			},
			want: Action{Kind: ActNone},
		},
		{
			name: "run not captured is a note and red moves on",
			view: View{Epic: decideEpic, Run: &store.EvidenceRun{
				ID: "red-run", Ended: ended, NotCaptured: "no log",
			}, Task: store.EpicTask{Seq: 3, Build: red}},
			want: Action{
				Kind: ActMove, Next: passed,
				Notes: []string{"T3 red: not captured: no log"}, Tells: []string{"T3 red: not captured: no log"},
			},
		},
		{
			name: "no verdict once reruns red",
			view: View{Epic: decideEpic, Run: &store.EvidenceRun{
				ID: "red-run", Ended: ended, JudgeReason: "judge wrote nothing",
			}, Task: store.EpicTask{Seq: 3, Build: red}},
			want: Action{Kind: ActMove, Next: noVerdictOnce},
		},
		{
			name: "no verdict twice is a note and red moves on",
			view: View{Epic: decideEpic, Run: &store.EvidenceRun{
				ID: "red-run", Ended: ended, JudgeReason: "judge wrote nothing",
			}, Task: store.EpicTask{Seq: 3, Build: withVerdict}},
			want: Action{
				Kind: ActMove, Next: passed,
				Notes: []string{"T3 red: the judge gave no verdict twice: judge wrote nothing"},
				Tells: []string{"T3 red: the judge gave no verdict twice: judge wrote nothing"},
			},
		},
		{
			name: "passed red moves to implement with the red run",
			view: View{Epic: decideEpic, Run: &store.EvidenceRun{
				ID: "red-run", Ended: ended, Verdict: "fails", Passed: true,
			}, Task: store.EpicTask{Seq: 3, Build: red}},
			want: Action{Kind: ActMove, Next: passed},
		},
		{
			name: "red whose conclusion holds at the base is a note and red moves on",
			view: View{Epic: decideEpic, Run: &store.EvidenceRun{
				ID: "red-run", Ended: ended, Verdict: "holds",
			}, Task: store.EpicTask{Seq: 3, Build: red}},
			want: Action{
				Kind: ActMove, Next: passed,
				Notes: []string{"T3 red: the conclusion holds at the base (exit 0)"},
				Tells: []string{"T3 red: the conclusion holds at the base (exit 0)"},
			},
		},
	})
}

func TestDecideImplement(t *testing.T) {
	t.Parallel()
	implement := store.TaskBuild{
		Stage: StageImplement, Base: decideBranch, Attempt: 1, Rerun: 1, NoVerdict: 1, Step: "impl-step",
		RedRun: "red-run",
	}
	snapped := implement
	snapped.Refs = refsSnapshot
	secondAttempt := implement
	secondAttempt.Attempt = 2
	unverdicted := implement
	unverdicted.NoVerdict = 0
	exhaustedImplement := implement
	exhaustedImplement.Decisions = decisionLimit
	orchestrating := implement
	orchestrating.Stage = StageOrchestrate
	orchestrating.NoVerdict = 0
	// implementStep is the implementer's step as ended: its end, status, kept return and reason.
	implementStep := func(end, status, result, reason string) *store.StepRow {
		return &store.StepRow{ID: "impl-step", End: end, Status: status, Result: result, Reason: reason}
	}
	stepID := StepID(decideEpic, 3, StageImplement, 1, 1, 0, 0)
	const doneCommit = `{"status":"done","summary":"built it","commit":"abc123"}`
	const doneNoCommit = `{"status":"done","summary":"built it"}`
	const parkingMove = "T3 implementer moved refs/heads/main (old to new)"
	runDecideCases(t, Decide, []decideCase{
		{
			name: "no step while paused waits",
			view: View{Epic: decideEpic, Paused: true, Task: store.EpicTask{Seq: 3, Build: implement}},
			want: Action{Kind: ActNone},
		},
		{
			name: "no step without refs takes the refs snapshot",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: implement}},
			want: Action{Kind: ActSnapshot},
		},
		{
			name: "no step with refs starts the implementer",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: snapped}},
			want: Action{Kind: ActStartStep, Unit: StageImplement, ID: stepID},
		},
		{
			name: "open step waits",
			view: View{
				Epic: decideEpic, Step: implementStep("", "", "", ""),
				Task: store.EpicTask{Seq: 3, Build: snapped},
			},
			want: Action{Kind: ActNone},
		},
		{
			name: "ended with a parking move reports it and clears the refs",
			view: View{
				Epic: decideEpic, Step: implementStep("done", "", doneCommit, ""),
				RefMoves: []RefMove{{Ref: "refs/heads/main", From: "old", To: "new", Parks: true}},
				Task:     store.EpicTask{Seq: 3, Build: snapped},
			},
			want: Action{Kind: ActRefs, Next: implement, Reason: parkingMove},
		},
		{
			name: "ended with a non-parking move tells it",
			view: View{
				Epic: decideEpic, Step: implementStep("done", "", doneCommit, ""),
				RefMoves: []RefMove{{Ref: "refs/remotes/origin/main", From: "old", To: "new"}},
				Task:     store.EpicTask{Seq: 3, Build: snapped},
			},
			want: Action{
				Kind: ActRefs, Next: implement,
				Tells: []string{"T3 implementer moved refs/remotes/origin/main (old to new)"},
			},
		},
		{
			name: "ended with a parking and a non-parking move parks on the first and tells the other",
			view: View{
				Epic: decideEpic, Step: implementStep("done", "", doneCommit, ""),
				RefMoves: []RefMove{
					{Ref: "refs/heads/main", From: "old", To: "new", Parks: true},
					{Ref: "refs/remotes/origin/main", From: "a", To: "b"},
					{Ref: "refs/heads/other", From: "c", To: "d", Parks: true},
				},
				Task: store.EpicTask{Seq: 3, Build: snapped},
			},
			want: Action{
				Kind: ActRefs, Next: implement, Reason: parkingMove,
				Tells: []string{"T3 implementer moved refs/remotes/origin/main (a to b)"},
			},
		},
		{
			name: "ended with no ref moves clears the refs",
			view: View{
				Epic: decideEpic, Step: implementStep("done", "", doneCommit, ""),
				Task: store.EpicTask{Seq: 3, Build: snapped},
			},
			want: Action{Kind: ActRefs, Next: implement},
		},
		{
			name: "done with a commit guards the commit before green at the commit",
			view: View{
				Epic: decideEpic, Head: "head-sha", Step: implementStep("done", "", doneCommit, ""),
				Task: store.EpicTask{Seq: 3, Build: implement},
			},
			want: Action{Kind: ActGuard, Next: store.TaskBuild{
				Stage: StageGreen, Base: decideBranch, Attempt: 1, RedRun: "red-run", Revision: "abc123",
			}},
		},
		{
			name: "goal unmet without a commit guards the head before green at the head",
			view: View{
				Epic: decideEpic, Head: "head-sha", Step: implementStep("goal_unmet", "", doneNoCommit, ""),
				Task: store.EpicTask{Seq: 3, Build: implement},
			},
			want: Action{Kind: ActGuard, Next: store.TaskBuild{
				Stage: StageGreen, Base: decideBranch, Attempt: 1, RedRun: "red-run", Revision: "head-sha",
			}},
		},
		{
			name: "done with no readable return gives no verdict once and reruns",
			view: View{
				Epic: decideEpic, Step: implementStep("done", "", "not json", ""),
				Task: store.EpicTask{Seq: 3, Build: unverdicted},
			},
			want: Action{Kind: ActMove, Next: store.TaskBuild{
				Stage: StageImplement, Base: decideBranch, Attempt: 1, Rerun: 2, NoVerdict: 1,
				Step: "impl-step", RedRun: "red-run",
			}},
		},
		{
			name: "returned with no orchestrator decisions left fails with the return's status and summary",
			view: View{
				Epic: decideEpic,
				Step: implementStep(
					"returned", "needs_context", `{"status":"needs_context","summary":"the spec is short"}`, "",
				),
				Task: store.EpicTask{Seq: 3, Build: exhaustedImplement},
			},
			want: Action{Kind: ActFail, Reason: "returned needs_context: the spec is short"},
		},
		{
			name: "returned with decisions left moves to orchestrate keeping the implementer's step",
			view: View{
				Epic: decideEpic,
				Step: implementStep(
					"returned", "needs_context", `{"status":"needs_context","summary":"the spec is short"}`, "",
				),
				Task: store.EpicTask{Seq: 3, Build: implement},
			},
			want: Action{Kind: ActMove, Next: orchestrating},
		},
		{
			name: "stopped pauses with the build held stopped",
			view: View{
				Epic: decideEpic,
				Step: implementStep("stopped", "", "", ""),
				Task: store.EpicTask{Seq: 3, Build: implement},
			},
			want: Action{Kind: ActPause, Next: store.TaskBuild{
				Stage: StageImplement, Base: decideBranch, Attempt: 1, Rerun: 1, NoVerdict: 1, Step: "impl-step",
				RedRun: "red-run", Held: HeldStopped,
			}},
		},
		{
			name: "engine restart reruns the implementer",
			view: View{
				Epic: decideEpic, Step: implementStep("engine_restarted", "", "", ""),
				Task: store.EpicTask{Seq: 3, Build: implement},
			},
			want: Action{Kind: ActMove, Next: store.TaskBuild{
				Stage: StageImplement, Base: decideBranch, Attempt: 1, Rerun: 2, NoVerdict: 1, Step: "impl-step",
				RedRun: "red-run",
			}},
		},
		{
			name: "checks failed on the first attempt moves to a second",
			view: View{
				Epic: decideEpic, Step: implementStep("checks_failed", "", "", "gate: lint failed"),
				Task: store.EpicTask{Seq: 3, Build: implement},
			},
			want: Action{Kind: ActFail, Reason: "checks_failed: gate: lint failed"},
		},
		{
			name: "checks failed on the second attempt fails",
			view: View{
				Epic: decideEpic, Step: implementStep("checks_failed", "", "", "gate: lint failed"),
				Task: store.EpicTask{Seq: 3, Build: secondAttempt},
			},
			want: Action{Kind: ActFail, Reason: "checks_failed: gate: lint failed"},
		},
		{
			name: "overtime on the first attempt moves to a second",
			view: View{
				Epic: decideEpic, Step: implementStep("overtime", "", "", "wall time limit reached"),
				Task: store.EpicTask{Seq: 3, Build: implement},
			},
			want: Action{Kind: ActFail, Reason: "overtime: wall time limit reached"},
		},
	})
}

func TestDecideStages(t *testing.T) {
	t.Parallel()
	runDecideCases(t, Decide, []decideCase{
		{
			name: "green with no run starts green at the revision",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: store.TaskBuild{
				Stage: StageGreen, Base: decideBranch, Attempt: 1, Revision: "rev",
			}}},
			want: Action{
				Kind:     ActStartRun,
				Phase:    StageGreen,
				Revision: "rev",
				ID:       RunID(decideEpic, 3, StageGreen, 1, 0),
			},
		},
		{
			name: "review with no step starts the first gate's review",
			view: View{
				Epic:        decideEpic,
				ReviewGates: []string{"lint"},
				Task: store.EpicTask{Seq: 3, Build: store.TaskBuild{
					Stage: StageReview, Base: decideBranch, Attempt: 1, Revision: "rev",
				}},
			},
			want: Action{Kind: ActStartStep, Unit: StageReview, ID: StepID(decideEpic, 3, StageReview, 1, 0, 0, 0)},
		},
		{
			name: "verify with no step starts the first finding's verifier",
			view: View{Epic: decideEpic, Task: store.EpicTask{
				Seq: 3,
				Build: store.TaskBuild{
					Stage: StageVerify, Base: decideBranch, Attempt: 1, Revision: "rev", GreenRun: "green-run",
				},
				Reviews: verifyReviews(1, "", ""),
			}},
			want: Action{Kind: ActStartStep, Unit: StageVerify, ID: StepID(decideEpic, 3, StageVerify, 1, 0, 0, 0)},
		},
	})
}

func TestDecideStartError(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	withStage := func(build store.TaskBuild) View {
		return View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: build}}
	}
	runDecideCases(t, func(view View) Action { return DecideStartError(view, boom) }, []decideCase{
		{
			name: "red that did not start fails",
			view: withStage(store.TaskBuild{Stage: StageRed, Base: decideBranch}),
			want: Action{Kind: ActFail, Reason: "red not captured: boom"},
		},
		{
			name: "green that did not start on the first attempt spends it",
			view: withStage(store.TaskBuild{Stage: StageGreen, Base: decideBranch, Attempt: 1, RedRun: "red-run"}),
			want: Action{Kind: ActFail, Reason: "green not captured: boom"},
		},
		{
			name: "green that did not start on the second attempt fails",
			view: withStage(store.TaskBuild{Stage: StageGreen, Base: decideBranch, Attempt: 2, RedRun: "red-run"}),
			want: Action{Kind: ActFail, Reason: "green not captured: boom"},
		},
		{
			name: "implement that did not start fails",
			view: withStage(store.TaskBuild{Stage: StageImplement, Base: decideBranch, Attempt: 1}),
			want: Action{Kind: ActFail, Reason: "implement did not start: boom"},
		},
		{
			name: "review that did not start fails",
			view: withStage(store.TaskBuild{Stage: StageReview, Base: decideBranch, Attempt: 1}),
			want: Action{Kind: ActFail, Reason: "review did not start: boom"},
		},
		{
			name: "verify that did not start fails",
			view: withStage(store.TaskBuild{Stage: StageVerify, Base: decideBranch, Attempt: 1}),
			want: Action{Kind: ActFail, Reason: "verify did not start: boom"},
		},
	})
}
