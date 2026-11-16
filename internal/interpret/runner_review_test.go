package interpret

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// The review step a hold test stops, the tell reason its stop gives, and the outputs and ledger the review, closure and
// conclusion runs of the review tests end with.
const (
	runnerHeldStep      = "step-held-security"
	runnerHoldReason    = "security stopped: lint gate failed"
	runnerReviewOutput  = "security: fails\ntests: holds"
	runnerClosureOutput = "closed 1 of 1"
	runnerConcludeBody  = "epic " + runEpic + " ended clean at " + runBase
)

// runnerVerdicts returns the verdicts a review run ends with.
func runnerVerdicts() []store.ReviewVerdict {
	return []store.ReviewVerdict{
		{Position: 1, Reviewer: "security", Step: "step-security", Verdict: "fails", Reason: "a finding"},
		{Position: 2, Reviewer: "tests", Step: "step-tests", Verdict: "holds"},
	}
}

// runnerLedger returns the ledger a review run ends with, before closures.
func runnerLedger() []store.LedgerFinding {
	return []store.LedgerFinding{{
		ID: "security-1", Position: 1, Reviewer: "security", Finding: "f1", Claim: "the claim", Citation: "a.go:1",
		Location: "a.go", Evidence: "the evidence", VerifyBy: "the check", Step: "step-verify-1", Result: "confirmed",
		Reason: "a reason",
	}}
}

// runnerClosure returns the closure a closure run ends with.
func runnerClosure() store.Closure {
	return store.Closure{Finding: "security-1", Round: 1, Step: "step-close-1", Result: "closed", Reason: "fixed"}
}

// runnerRound returns the round a closure run ends with.
func runnerRound() store.ReviewRound {
	return store.ReviewRound{Round: 1, Revision: runBase, Tasks: []int64{4, 5}, Outcome: "passed"}
}

// runnerReviewers is the Reviewers a test gives a runner: its epic-stage reviewers are the ones it holds. Its steps are
// not started by these tests.
type runnerReviewers struct {
	reviewers []kinds.Reviewer
}

// Epic returns the reviewers the fake holds.
func (r runnerReviewers) Epic(store.EpicRow) []kinds.Reviewer {
	return r.reviewers
}

// Start fails: the review tests start no reviewer step.
func (runnerReviewers) Start(
	context.Context, store.EpicRow, string, string, string,
) (enginehost.StepStart, error) {
	return enginehost.StepStart{}, errors.New("runnerReviewers: no step is started in these tests")
}

// runnerKindFunc is a kind a test gives a node kind in place of the registered one: it decides with the function.
type runnerKindFunc func(node kinds.Node, rows kinds.Rows) kinds.Action

// Decide calls the function.
func (f runnerKindFunc) Decide(node kinds.Node, rows kinds.Rows) kinds.Action {
	return f(node, rows)
}

// runnerEnds returns a kind that decides act for its node run, whatever its rows.
func runnerEnds(act kinds.Action) runnerKindFunc {
	return func(kinds.Node, kinds.Rows) kinds.Action { return act }
}

// runnerHold is one HoldReviewStep call a runner made: the step and the reason.
type runnerHold struct {
	step, reason string
}

// runnerHoldCounter is the store a test wraps to record the review steps the runner holds.
type runnerHoldCounter struct {
	Store
	holds []runnerHold
}

// HoldReviewStep records the hold, then writes it.
func (c *runnerHoldCounter) HoldReviewStep(ctx context.Context, epic, step, reason string, at time.Time) error {
	c.holds = append(c.holds, runnerHold{step: step, reason: reason})
	if err := c.Store.HoldReviewStep(ctx, epic, step, reason, at); err != nil {
		return fmt.Errorf("hold review step: %w", err)
	}
	return nil
}

// runnerSeam is a pair of review seams a test gives a runner. Both says whether the pair is the one the review kinds
// need: Reviewers and Ending both set.
type runnerSeam struct {
	name      string
	reviewers Reviewers
	ending    Ending
	both      bool
}

// runnerSeams returns each pair of review seams a runner may be given, fresh on each call.
func runnerSeams() []runnerSeam {
	return []runnerSeam{
		{name: "reviewers and ending", reviewers: runnerReviewers{}, ending: &recordingEnding{}, both: true},
		{name: "reviewers without ending", reviewers: runnerReviewers{}},
		{name: "ending without reviewers", ending: &recordingEnding{}},
		{name: "neither"},
	}
}

// runnerReviewRunner returns a runner over s with the review seams given. s may be nil when no store is read.
func runnerReviewRunner(s Store, reviewers Reviewers, ending Ending) *Runner {
	return New(Config{
		Store: s, Planner: &fakePlanner{}, Builder: &fakeBuilder{}, Reviewers: reviewers, Ending: ending,
		Tick: time.Hour, Now: runClock,
	})
}

// runnerFlow returns a flow whose top-level nodes are kind nodes, one for each of ids in order, each of the kind of its id.
func runnerFlow(ids ...string) workflow.Workflow {
	nodes := make([]workflow.Node, 0, len(ids))
	for _, id := range ids {
		nodes = append(nodes, kindNode(id, id))
	}
	return workflow.Workflow{Name: "review-loop", Nodes: nodes}
}

// runnerWaitsForFreeze returns a review kind that waits while its review is not frozen, and decides act once it is. A
// review's prepare freezes it before the review decides anything, so the tests freeze it with runnerFreezeReview.
func runnerWaitsForFreeze(act kinds.Action) runnerKindFunc {
	return func(_ kinds.Node, rows kinds.Rows) kinds.Action {
		if rows.Review.Frozen == "" {
			return kinds.Action{Kind: kinds.ActNone}
		}
		return act
	}
}

// runnerFreezeReview freezes the review of the test epic at runBase on its running review run, as the review's prepare
// does once the run has started.
func runnerFreezeReview(t *testing.T, s *store.Store) {
	t.Helper()
	run := runAt(t, s, reviewNodeKind)
	if err := s.FreezeReview(context.Background(), runEpic, run.Path, run.Attempt, runBase); err != nil {
		t.Fatalf("FreezeReview: %v", err)
	}
}

// TestRunnerRegistersReviewKindsOnlyWithReviewersAndEnding: the review, closure and conclude kinds are registered when the
// runner has both Reviewers and Ending, and not when either one is missing.
func TestRunnerRegistersReviewKindsOnlyWithReviewersAndEnding(t *testing.T) {
	t.Parallel()
	for _, seam := range runnerSeams() {
		t.Run(seam.name, func(t *testing.T) {
			t.Parallel()
			runner := runnerReviewRunner(nil, seam.reviewers, seam.ending)
			_, review := runner.kinds[reviewNodeKind].(kinds.Review)
			_, closure := runner.kinds[closureNodeKind].(kinds.Closure)
			_, conclude := runner.kinds[concludeNodeKind].(kinds.Conclude)
			if review != seam.both || closure != seam.both || conclude != seam.both {
				t.Errorf("review %t, closure %t, conclude %t registered; want all %t",
					review, closure, conclude, seam.both)
			}
		})
	}
}

// TestRunnerReviewWithoutReviewersOrEndingStartsNoWork: a flow holding a review, with either seam missing, starts the
// review run and no work for it: the run has no revision and no step is started.
func TestRunnerReviewWithoutReviewersOrEndingStartsNoWork(t *testing.T) {
	t.Parallel()
	for _, seam := range runnerSeams() {
		if seam.both {
			continue
		}
		t.Run(seam.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openRunStore(t)
			createRunEpic(t, s, store.EpicWorkflow{})
			runner := runnerReviewRunner(s, seam.reviewers, seam.ending)
			runner.passFlow(ctx, store.EpicRow{ID: runEpic}, runnerFlow(reviewNodeKind))

			if run := runAt(t, s, reviewNodeKind); run.State != store.NodeRunning || run.Revision != "" {
				t.Errorf("review run state %q revision %q, want running with no revision", run.State, run.Revision)
			}
			read, err := s.EpicFlow(ctx, runEpic)
			if err != nil {
				t.Fatalf("EpicFlow: %v", err)
			}
			if len(read.Steps) != 0 {
				t.Errorf("steps = %d, want none", len(read.Steps))
			}
		})
	}
}

// TestRunnerReviewEndLandsVerdictsAndLedger: with both seams, a review run that ends passed lands its verdicts and ledger
// in the store, and its epic moves to correcting. The kind sees the epic's reviewers in the rows it decides on.
func TestRunnerReviewEndLandsVerdictsAndLedger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	reviewers := []kinds.Reviewer{{Name: "security", Verify: true}, {Name: "tests"}}
	runner := runnerReviewRunner(s, runnerReviewers{reviewers: reviewers}, &recordingEnding{})
	var seen []kinds.Reviewer
	runner.kinds[reviewNodeKind] = runnerKindFunc(func(_ kinds.Node, rows kinds.Rows) kinds.Action {
		seen = rows.Reviewers
		if rows.Review.Frozen == "" {
			return kinds.Action{Kind: kinds.ActNone}
		}
		return kinds.Action{
			Kind: kinds.ActEnd, Output: runnerReviewOutput, Verdicts: runnerVerdicts(), Ledger: runnerLedger(),
		}
	})
	flow := runnerFlow(reviewNodeKind)
	runner.passFlow(ctx, store.EpicRow{ID: runEpic}, flow)
	runnerFreezeReview(t, s)
	runner.passFlow(ctx, store.EpicRow{ID: runEpic}, flow)

	if !reflect.DeepEqual(seen, reviewers) {
		t.Errorf("reviewers the kind saw = %+v, want %+v", seen, reviewers)
	}
	review, err := s.EpicReview(ctx, runEpic)
	if err != nil {
		t.Fatalf("EpicReview: %v", err)
	}
	if !reflect.DeepEqual(review.Verdicts, runnerVerdicts()) {
		t.Errorf("verdicts = %+v, want %+v", review.Verdicts, runnerVerdicts())
	}
	if !reflect.DeepEqual(review.Ledger, runnerLedger()) {
		t.Errorf("ledger = %+v, want %+v", review.Ledger, runnerLedger())
	}
	if phase := epicOf(t, s).Phase; phase != "correcting" {
		t.Errorf("epic phase = %q, want correcting", phase)
	}
	if run := runAt(t, s, reviewNodeKind); run.State != store.NodeEnded || run.Outcome != "passed" ||
		run.Output != runnerReviewOutput {
		t.Errorf("review run = %+v, want ended passed with its output", run)
	}
}

// TestRunnerClosureEndLandsItsRound: with both seams, a closure run that ends lands its round and its closures in the
// store, after the review that holds the findings they close.
func TestRunnerClosureEndLandsItsRound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	runner := runnerReviewRunner(s, runnerReviewers{}, &recordingEnding{})
	runner.kinds[reviewNodeKind] = runnerWaitsForFreeze(kinds.Action{
		Kind: kinds.ActEnd, Output: runnerReviewOutput, Verdicts: runnerVerdicts(), Ledger: runnerLedger(),
	})
	runner.kinds[closureNodeKind] = runnerEnds(kinds.Action{
		Kind: kinds.ActEnd, Outcome: "passed", Output: runnerClosureOutput, Round: runnerRound(),
		Closures: []store.Closure{runnerClosure()},
	})
	flow := runnerFlow(reviewNodeKind, closureNodeKind)
	runner.passFlow(ctx, store.EpicRow{ID: runEpic}, flow)
	runnerFreezeReview(t, s)
	runner.passFlow(ctx, store.EpicRow{ID: runEpic}, flow)

	review, err := s.EpicReview(ctx, runEpic)
	if err != nil {
		t.Fatalf("EpicReview: %v", err)
	}
	if want := []store.ReviewRound{runnerRound()}; !reflect.DeepEqual(review.Rounds, want) {
		t.Errorf("rounds = %+v, want %+v", review.Rounds, want)
	}
	wantLedger := runnerLedger()
	wantLedger[0].Closures = []store.Closure{runnerClosure()}
	if !reflect.DeepEqual(review.Ledger, wantLedger) {
		t.Errorf("ledger = %+v, want %+v", review.Ledger, wantLedger)
	}
	if run := runAt(t, s, closureNodeKind); run.State != store.NodeEnded || run.Outcome != "passed" ||
		run.Output != runnerClosureOutput {
		t.Errorf("closure run = %+v, want ended passed with its output", run)
	}
}

// epicTellTexts returns the texts of the test epic's tells, in order.
func epicTellTexts(t *testing.T, s *store.Store) []string {
	t.Helper()
	history, err := s.EpicHistory(context.Background(), runEpic)
	if err != nil {
		t.Fatalf("EpicHistory: %v", err)
	}
	texts := make([]string, 0, len(history.Tells))
	for _, tell := range history.Tells {
		texts = append(texts, tell.Text)
	}
	return texts
}

// TestRunnerConcludeEndsEpicOnceAndRunPassed: a conclusion run that ends ends its epic once, with its phase and body,
// and ends its run passed, the store being the runner's Ending. A pass after the run ended ends nothing again. It ends
// the epic ended_with_gaps, a phase that tells, so a second ending would show as a second tell.
func TestRunnerConcludeEndsEpicOnceAndRunPassed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	body := kinds.EndingBody("ended_with_gaps", []string{"reviewer security: gave no verdict twice"})
	runner := runnerReviewRunner(s, runnerReviewers{}, s)
	runner.kinds[concludeNodeKind] = runnerEnds(kinds.Action{
		Kind: kinds.ActEnd, Outcome: "passed", Phase: "ended_with_gaps", Output: body,
	})
	runner.passFlow(ctx, store.EpicRow{ID: runEpic}, runnerFlow(concludeNodeKind))
	runner.passFlow(ctx, store.EpicRow{ID: runEpic}, runnerFlow(concludeNodeKind))

	if got := epicTellTexts(t, s); !slices.Equal(got, []string{body}) {
		t.Errorf("tells = %q, want the one ending %q", got, body)
	}
	if phase := epicOf(t, s).Phase; phase != "ended_with_gaps" {
		t.Errorf("epic phase = %q, want ended_with_gaps", phase)
	}
	if run := runAt(t, s, concludeNodeKind); run.State != store.NodeEnded || run.Outcome != "passed" {
		t.Errorf("conclude run = %+v, want ended passed", run)
	}
}

// errKilled is what killedAfterEnding returns in place of the process that a kill ends.
var errKilled = errors.New("orreryd killed after the write")

// killedAfterEnding is an Ending over the store that ends the epic as the store does, then fails: what the store wrote
// stays, and the conclude that called it carries out nothing more, as when orreryd is killed right after that write.
type killedAfterEnding struct {
	store *store.Store
}

// EndEpic ends the epic in the store, then reports the kill.
func (k killedAfterEnding) EndEpic(
	ctx context.Context, epic, phase, body string, ends []store.NodeEnd, at time.Time,
) error {
	if err := k.store.EndEpic(ctx, epic, phase, body, ends, at); err != nil {
		return fmt.Errorf("end epic: %w", err)
	}
	return errKilled
}

// TestRunnerConcludeKilledAfterItsEndingThenReleasedEndsEpicOnce forces the crash window of conclude: orreryd is killed
// right after the write that ends the epic ended_with_gaps, the owner releases it (store.Store.ReleaseEpic moves it to
// clean) before the restart, and the restarted runner passes the epic again. The conclusion run ended in the ending's
// write, so the restart decides no second ending: the epic keeps the release's phase and holds the conclusion's tell
// once.
func TestRunnerConcludeKilledAfterItsEndingThenReleasedEndsEpicOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	conclude := runnerEnds(kinds.Action{
		Kind: kinds.ActEnd, Outcome: "passed", Phase: "ended_with_gaps", Output: runnerConcludeBody,
	})
	killed := runnerReviewRunner(s, runnerReviewers{}, killedAfterEnding{store: s})
	killed.kinds[concludeNodeKind] = conclude
	killed.passFlow(ctx, store.EpicRow{ID: runEpic}, runnerFlow(concludeNodeKind))

	if err := s.ReleaseEpic(ctx, runEpic, nil, runClock().Add(time.Minute)); err != nil {
		t.Fatalf("ReleaseEpic: %v", err)
	}
	restarted := runnerReviewRunner(s, runnerReviewers{}, s)
	restarted.kinds[concludeNodeKind] = conclude
	restarted.passFlow(ctx, store.EpicRow{ID: runEpic}, runnerFlow(concludeNodeKind))

	if got, want := epicTellTexts(t, s), []string{runnerConcludeBody}; !slices.Equal(got, want) {
		t.Errorf("tells after the restart = %q, want %q: the ending told once", got, want)
	}
	if phase := epicOf(t, s).Phase; phase != "clean" {
		t.Errorf("epic phase after the restart = %q, want clean, as the release left it", phase)
	}
	if run := runAt(t, s, concludeNodeKind); run.State != store.NodeEnded || run.Outcome != "passed" {
		t.Errorf("conclude run = %+v, want ended passed by the ending's write", run)
	}
}

// TestRunnerHoldPausesEpicOnce: a review that holds a stopped step pauses its epic once, with the step recorded as held.
// The kind sees the hold in the rows of the next pass and holds nothing again.
func TestRunnerHoldPausesEpicOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	holds := &runnerHoldCounter{Store: s}
	runner := runnerReviewRunner(holds, runnerReviewers{}, &recordingEnding{})
	runner.kinds[reviewNodeKind] = runnerKindFunc(func(_ kinds.Node, rows kinds.Rows) kinds.Action {
		if rows.Review.Frozen == "" || slices.Contains(rows.Review.Holds, runnerHeldStep) {
			return kinds.Action{Kind: kinds.ActNone}
		}
		return kinds.Action{Kind: kinds.ActHold, IDs: []string{runnerHeldStep}, Output: runnerHoldReason}
	})
	flow := runnerFlow(reviewNodeKind)
	runner.passFlow(ctx, store.EpicRow{ID: runEpic}, flow)
	runnerFreezeReview(t, s)
	runner.passFlow(ctx, store.EpicRow{ID: runEpic}, flow)
	runner.passFlow(ctx, store.EpicRow{ID: runEpic}, flow)

	if want := []runnerHold{{step: runnerHeldStep, reason: runnerHoldReason}}; !reflect.DeepEqual(holds.holds, want) {
		t.Errorf("holds = %+v, want %+v", holds.holds, want)
	}
	review, err := s.EpicReview(ctx, runEpic)
	if err != nil {
		t.Fatalf("EpicReview: %v", err)
	}
	if !slices.Equal(review.Holds, []string{runnerHeldStep}) {
		t.Errorf("review holds = %v, want [%s]", review.Holds, runnerHeldStep)
	}
	if !epicOf(t, s).Paused {
		t.Error("epic is not paused, want paused by the held step")
	}
}
