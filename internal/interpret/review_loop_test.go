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

// reviewLoopFlow is the workflow the review loop tests pin: the plan that holds the epic's first turn, then the review,
// then the correction loop of at most two rounds, each a fix plan then closure, then conclude. It is the built-in's review,
// correct and conclude nodes, with the plan before them that the first fix plan's ledger trigger needs, and without the
// red and release nodes, which these tests do not run.
const reviewLoopFlow = `name: review-loop
nodes:
  - id: plan
    kind: plan
    body:
      - id: build
        kind: build
      - id: integrate
        kind: integrate
  - id: review
    kind: review
  - id: correct
    loop:
      max: 2
      until: closure.passed
      spent: continue
    body:
      - id: fix
        kind: plan
        body:
          - id: rebuild
            kind: build
          - id: reintegrate
            kind: integrate
      - id: closure
        kind: closure
  - id: conclude
    kind: conclude
`

// The phase the test epic starts in, the path of its review run, and how many passes a drive may take before it fails.
const (
	reviewLoopPhase  = "built"
	reviewLoopReview = "review"
	reviewLoopPasses = 30
)

// The causes of the triggers the planner turns of the test epic claim, in turn order: the epic's creation, then each fix
// plan's ledger trigger. A ledger names the findings still open in its round, and the cases that confirm one share it.
const (
	reviewLoopCreatedCause  = "epic created"
	reviewLoopEmptyCause    = "ledger round 1: no confirmed findings; nothing to correct"
	reviewLoopFindingLine   = "security-1 (security): claim: the claim; citation: a.go:1; location: a.go; evidence: the evidence; reason: a reason"
	reviewLoopRoundOneCause = "ledger round 1: open findings to correct\n" + reviewLoopFindingLine
	reviewLoopRoundTwoCause = "ledger round 2: open findings to correct\n" + reviewLoopFindingLine
	reviewLoopGapCause      = "ledger round 1: no open findings; gaps to reconcile\n" +
		"gap reviewer:security: reviewer security: reviewer 1 gave no verdict twice: failed: " + reviewLoopGapFailure
)

// The returns the test steps keep: a reviewer's gate return with one finding, a verifier's return confirming it, and a
// closure verifier's return with the result the format is filled with.
const (
	reviewLoopFindingReturn = `{"status":"done","findings":[{"id":"f1","claim":"the claim","citation":"a.go:1",` +
		`"location":"a.go","evidence":"the evidence","verify_by":"the check"}]}`
	reviewLoopVerifyReturn = `{"status":"done","results":[{"evidence":"a reason","result":"confirmed"}]}`
	reviewLoopCloseFormat  = `{"status":"done","results":[{"evidence":"checked","result":"%s"}]}`
)

// reviewLoopGapFailure is the reason of each costly end a reviewer's tries give in the gap case.
const reviewLoopGapFailure = "lint gate crashed"

// reviewLoopEnd is how the agent of one step of the loop ends it: the node run path and part the step is for, the end,
// status and reason the step gets, and the return it keeps, which is empty when it keeps none.
type reviewLoopEnd struct {
	path, part, end, status, reason, result string
}

// reviewLoopOutcome is what one case's drive left in the store: the store, the epic's review and phase, the causes of the
// triggers its planner turns claimed in turn order, the tells the epic holds, and the reviewers the review started in start
// order.
type reviewLoopOutcome struct {
	s         *store.Store
	review    store.Review
	phase     string
	causes    []string
	tells     []string
	reviewers []string
}

// reviewLoopReviewers stands in for the reviewers service with two reviewers: security, which verifies its findings, and
// tests, which does not. It records each start as reviewWorkReviewers does.
type reviewLoopReviewers struct {
	*reviewWorkReviewers
}

// Epic returns the two reviewers in merged order.
func (*reviewLoopReviewers) Epic(store.EpicRow) []kinds.Reviewer {
	return []kinds.Reviewer{{Name: "security", Verify: true}, {Name: "tests"}}
}

// reviewLoopSteps stands in for the step service. It stores each step it starts as a step row labeled with its epic and
// the path of its node run, as the step service labels it, so the review reads the step's end. It records each start.
type reviewLoopSteps struct {
	store *store.Store
	calls []enginehost.StepStart
}

// Start records start and stores its step row under its id, labeled with its epic and task.
func (f *reviewLoopSteps) Start(ctx context.Context, start enginehost.StepStart) (string, error) {
	f.calls = append(f.calls, start)
	err := f.store.ApplyEngine(ctx, []store.EngineOp{store.StepAdd{
		ID: start.ID, Agent: start.Agent, Prompt: start.Prompt, Schema: start.Schema, Workspace: start.Workspace,
		Revision: start.Revision, DoneValue: "done", ReadOnly: start.ReadOnly, Epic: start.Epic, Task: start.Task,
	}}, runClock())
	if err != nil {
		return "", fmt.Errorf("store step %s: %w", start.ID, err)
	}
	return start.ID, nil
}

// reviewLoopHoldsScript returns the answers of a loop whose two reviewers both hold.
func reviewLoopHoldsScript() []reviewLoopEnd {
	return []reviewLoopEnd{
		{path: reviewLoopReview, part: "reviewer 1", end: "done", status: "done"},
		{path: reviewLoopReview, part: "reviewer 2", end: "done", status: "done"},
	}
}

// reviewLoopFindingScript returns the answers of a loop whose security reviewer raises one finding that its verifier
// confirms, and whose tests reviewer holds. Its closure verifier answers in each round with the results given, in order.
func reviewLoopFindingScript(closures ...string) []reviewLoopEnd {
	script := make([]reviewLoopEnd, 0, len(closures)+3)
	script = append(
		script,
		reviewLoopEnd{
			path:   reviewLoopReview,
			part:   "reviewer 1",
			end:    "gate_failed",
			status: "done",
			result: reviewLoopFindingReturn,
		},
		reviewLoopEnd{
			path:   reviewLoopReview,
			part:   "verify 1 1",
			end:    "done",
			status: "done",
			result: reviewLoopVerifyReturn,
		},
		reviewLoopEnd{path: reviewLoopReview, part: "reviewer 2", end: "done", status: "done"},
	)
	for round, result := range closures {
		script = append(script, reviewLoopCloseEnd(round+1, result))
	}
	return script
}

// reviewLoopCloseEnd returns the answer of the closure verifier of the finding in round, which closes or leaves it open
// as result says.
func reviewLoopCloseEnd(round int, result string) reviewLoopEnd {
	return reviewLoopEnd{
		path: fmt.Sprintf("correct@%d/closure", round), part: "close security-1", end: "done", status: "done",
		result: fmt.Sprintf(reviewLoopCloseFormat, result),
	}
}

// reviewLoopCostlyScript returns the answers of a loop whose security reviewer's two tries both end failed, so it gives no
// verdict, and whose tests reviewer holds.
func reviewLoopCostlyScript() []reviewLoopEnd {
	return []reviewLoopEnd{
		{path: reviewLoopReview, part: "reviewer 1", end: "failed", status: "failed", reason: reviewLoopGapFailure},
		{
			path:   reviewLoopReview,
			part:   "reviewer 1 try 2",
			end:    "failed",
			status: "failed",
			reason: reviewLoopGapFailure,
		},
		{path: reviewLoopReview, part: "reviewer 2", end: "done", status: "done"},
	}
}

// reviewLoopRestartScript returns the answers of a loop whose security reviewer's first two tries the engine restarts,
// and whose third try holds, and whose tests reviewer holds.
func reviewLoopRestartScript() []reviewLoopEnd {
	return []reviewLoopEnd{
		{path: reviewLoopReview, part: "reviewer 1", end: "engine_restarted"},
		{path: reviewLoopReview, part: "reviewer 1 try 2", end: "engine_restarted"},
		{path: reviewLoopReview, part: "reviewer 1 try 3", end: "done", status: "done"},
		{path: reviewLoopReview, part: "reviewer 2", end: "done", status: "done"},
	}
}

// createReviewLoopEpic stores the test epic at phase built, at its stored base and with no Requirements, pinned to
// reviewLoopFlow.
func createReviewLoopEpic(t *testing.T, s *store.Store) {
	t.Helper()
	read := func(string) ([]byte, bool, error) { return []byte(reviewLoopFlow), true, nil }
	pinned, err := workflow.Load("review-loop", read)
	if err != nil {
		t.Fatalf("Load review-loop: %v", err)
	}
	if err = s.CreateEpic(context.Background(), store.EpicRow{
		ID: runEpic, Repository: runRepo, Base: runBase, Title: "Run the review loop", Phase: reviewLoopPhase,
		Created: runClock(), Changed: runClock(),
		Workflow: store.EpicWorkflow{
			Name: pinned.Name, Text: pinned.Text, SHA256: pinned.SHA256, Nodes: pinnedNodes(pinned.Workflow.Nodes, ""),
		},
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
}

// reviewLoopFlowOf returns the test epic as its flow reads from the store.
func reviewLoopFlowOf(t *testing.T, s *store.Store) store.EpicFlow {
	t.Helper()
	flow, err := s.EpicFlow(context.Background(), runEpic)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	return flow
}

// reviewLoopRunAt returns the node run at path among runs, and whether there is one.
func reviewLoopRunAt(runs []store.NodeRun, path string) (store.NodeRun, bool) {
	i := slices.IndexFunc(runs, func(run store.NodeRun) bool { return run.Path == path })
	if i < 0 {
		return store.NodeRun{}, false
	}
	return runs[i], true
}

// reviewLoopAnswer ends each running step the script names with the end, status, reason and return the script gives it,
// as the step's agent would. A step whose node run has not started yet is left for a later answer.
func reviewLoopAnswer(t *testing.T, s *store.Store, script []reviewLoopEnd) {
	t.Helper()
	running := make(map[string]bool)
	for _, step := range reviewLoopFlowOf(t, s).Steps {
		running[step.ID] = step.End == ""
	}
	runs := runsOf(t, s)
	for _, answer := range script {
		run, ok := reviewLoopRunAt(runs, answer.path)
		if !ok {
			continue
		}
		id := store.NodeWorkID(run.ID, run.Attempt, answer.part)
		if !running[id] {
			continue
		}
		ops := []store.EngineOp{store.StepEnd{ID: id, End: answer.end, Status: answer.status, Reason: answer.reason}}
		if answer.result != "" {
			ops = append([]store.EngineOp{store.StepReturn{ID: id, Result: answer.result}}, ops...)
		}
		if err := s.ApplyEngine(context.Background(), ops, runClock()); err != nil {
			t.Fatalf("answer step %s: %v", id, err)
		}
	}
}

// reviewLoopPlanTurn claims the planner turn the test epic waits on, if it waits on one, and completes it with a plan of
// no task. It returns the cause of each trigger the turn claimed, and none when the epic waits on no turn.
func reviewLoopPlanTurn(t *testing.T, s *store.Store) []string {
	t.Helper()
	ctx := context.Background()
	turn, err := s.ClaimTurn(ctx, runEpic, runClock())
	if errors.Is(err, store.ErrNotClaimable) {
		return nil
	}
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	triggers, err := s.TurnTriggers(ctx, runEpic, turn.Seq)
	if err != nil {
		t.Fatalf("TurnTriggers of turn %d: %v", turn.Seq, err)
	}
	causes := make([]string, 0, len(triggers))
	for _, trigger := range triggers {
		causes = append(causes, trigger.Cause)
	}
	plan := store.EpicPlan{Decisions: []string{}, Outline: "no task", Outcome: "complete"}
	if err = s.CompleteTurn(ctx, runEpic, turn.Seq, plan, runClock()); err != nil {
		t.Fatalf("CompleteTurn of turn %d: %v", turn.Seq, err)
	}
	return causes
}

// reviewLoopTurn completes the planner turn the test epic waits on, if it waits on one, and returns the cause of each
// trigger the turn claimed. reviewLoopPlanTurn is the one most cases use.
type reviewLoopTurn func(t *testing.T, s *store.Store) []string

// reviewLoopDrive passes runner over the test epic, answers the steps of script, and completes each planner turn the epic
// waits on with turn, until a round of those changes nothing. It returns the causes of the triggers the turns claimed, in
// turn order.
func reviewLoopDrive(
	t *testing.T, s *store.Store, runner *Runner, script []reviewLoopEnd, turn reviewLoopTurn,
) []string {
	t.Helper()
	var causes []string
	// Nothing writes between one pass's last read of the flow and the next pass, so that read is the next one's before.
	before := reviewLoopFlowOf(t, s)
	for range reviewLoopPasses {
		runner.Pass(context.Background())
		reviewLoopAnswer(t, s, script)
		causes = append(causes, turn(t, s)...)
		after := reviewLoopFlowOf(t, s)
		if reflect.DeepEqual(after, before) {
			return causes
		}
		before = after
	}
	t.Fatalf("the flow still changes after %d passes", reviewLoopPasses)
	return nil
}

// reviewLoopCase creates the test epic, drives its review loop with the answers of script, and returns what the store then
// holds of it. Each planner turn completes with no task (reviewLoopPlanTurn).
func reviewLoopCase(t *testing.T, script []reviewLoopEnd) reviewLoopOutcome {
	t.Helper()
	return reviewLoopCaseWith(t, script, reviewLoopPlanTurn)
}

// reviewLoopCaseWith is reviewLoopCase with each planner turn completed by turn.
func reviewLoopCaseWith(t *testing.T, script []reviewLoopEnd, turn reviewLoopTurn) reviewLoopOutcome {
	t.Helper()
	s, path := openNodeStore(t)
	createReviewLoopEpic(t, s)
	reviewers := &reviewLoopReviewers{reviewWorkReviewers: &reviewWorkReviewers{}}
	runner := New(Config{
		Store: s, Planner: &fakePlanner{}, Builder: &fakeBuilder{}, Steps: &reviewLoopSteps{store: s},
		Reviewers: reviewers, Ending: s, Getenv: reviewWorkGetenv(t), Tick: time.Hour, Now: runClock,
	})
	causes := reviewLoopDrive(t, s, runner, script, turn)
	review, err := s.EpicReview(context.Background(), runEpic)
	if err != nil {
		t.Fatalf("EpicReview: %v", err)
	}
	started := make([]string, 0, len(reviewers.calls))
	for _, call := range reviewers.calls {
		started = append(started, call.name)
	}
	return reviewLoopOutcome{
		s: s, review: review, phase: epicOf(t, s).Phase, causes: causes, tells: nodeTells(t, path), reviewers: started,
	}
}

// reviewLoopWantEnd fails the test unless the epic of got is in phase and its tells hold body.
func reviewLoopWantEnd(t *testing.T, got reviewLoopOutcome, phase, body string) {
	t.Helper()
	if got.phase != phase {
		t.Errorf("epic phase = %q, want %q", got.phase, phase)
	}
	if !slices.Contains(got.tells, body) {
		t.Errorf("epic tells = %q, want %q among them", got.tells, body)
	}
}

// reviewLoopWantClean fails the test unless the epic of got is clean. A clean ending adds no tell (store.Store.EndEpic),
// so there is no body to look for.
func reviewLoopWantClean(t *testing.T, got reviewLoopOutcome) {
	t.Helper()
	if got.phase != "clean" {
		t.Errorf("epic phase = %q, want clean", got.phase)
	}
}

// reviewLoopWantCauses fails the test unless the planner turns claimed the triggers with causes, in turn order.
func reviewLoopWantCauses(t *testing.T, got reviewLoopOutcome, causes []string) {
	t.Helper()
	if !reflect.DeepEqual(got.causes, causes) {
		t.Errorf("turn causes = %q, want %q", got.causes, causes)
	}
}

// reviewLoopWantStarts fails the test unless the review started the reviewers of want, each as many times as it appears
// in want. The order of the starts is the runner's, so the names are compared sorted.
func reviewLoopWantStarts(t *testing.T, got reviewLoopOutcome, want []string) {
	t.Helper()
	started, expected := slices.Clone(got.reviewers), slices.Clone(want)
	slices.Sort(started)
	slices.Sort(expected)
	if !reflect.DeepEqual(started, expected) {
		t.Errorf("reviewer starts = %q, want %q", got.reviewers, want)
	}
}

// TestReviewLoopAllHoldsEndsCleanAfterOneRound: when both reviewers hold, the review ends with no finding and the first
// round's fix plan is woken with the empty-ledger trigger. Its turn completes with no task, so the fix plan ends built, the
// closure passes with nothing open, and the epic ends clean after one round at its stored base.
func TestReviewLoopAllHoldsEndsCleanAfterOneRound(t *testing.T) {
	t.Parallel()
	got := reviewLoopCase(t, reviewLoopHoldsScript())

	want := []store.ReviewVerdict{
		{Position: 1, Reviewer: "security", Step: workID(t, got.s, reviewLoopReview, "reviewer 1"), Verdict: "holds"},
		{Position: 2, Reviewer: "tests", Step: workID(t, got.s, reviewLoopReview, "reviewer 2"), Verdict: "holds"},
	}
	if !reflect.DeepEqual(got.review.Verdicts, want) {
		t.Errorf("verdicts = %+v, want %+v", got.review.Verdicts, want)
	}
	if len(got.review.Ledger) != 0 {
		t.Errorf("ledger = %+v, want none", got.review.Ledger)
	}
	wantRounds := []store.ReviewRound{{Round: 1, Revision: runBase, Outcome: "passed"}}
	if !reflect.DeepEqual(got.review.Rounds, wantRounds) {
		t.Errorf("rounds = %+v, want %+v", got.review.Rounds, wantRounds)
	}
	if got.review.Clean != runBase {
		t.Errorf("clean revision = %q, want %q", got.review.Clean, runBase)
	}
	reviewLoopWantStarts(t, got, []string{"security", "tests"})
	reviewLoopWantCauses(t, got, []string{reviewLoopCreatedCause, reviewLoopEmptyCause})
	reviewLoopWantClean(t, got)
}

// TestReviewLoopClosedFindingEndsCleanInRoundOne: a finding the security reviewer raises and its verifier confirms is
// woken into the round one fix plan, and its closure in that round closes it, so the loop passes and the epic ends clean
// after one round.
func TestReviewLoopClosedFindingEndsCleanInRoundOne(t *testing.T) {
	t.Parallel()
	got := reviewLoopCase(t, reviewLoopFindingScript("closed"))

	want := []store.ReviewVerdict{
		{Position: 1, Reviewer: "security", Step: workID(t, got.s, reviewLoopReview, "reviewer 1"), Verdict: "fails"},
		{Position: 2, Reviewer: "tests", Step: workID(t, got.s, reviewLoopReview, "reviewer 2"), Verdict: "holds"},
	}
	if !reflect.DeepEqual(got.review.Verdicts, want) {
		t.Errorf("verdicts = %+v, want %+v", got.review.Verdicts, want)
	}
	wantLedger := []store.LedgerFinding{
		{
			ID:       "security-1",
			Position: 1,
			Reviewer: "security",
			Finding:  "f1",
			Claim:    "the claim",
			Citation: "a.go:1",
			Location: "a.go",
			Evidence: "the evidence",
			VerifyBy: "the check",
			Step:     workID(t, got.s, reviewLoopReview, "verify 1 1"),
			Result:   "confirmed",
			Reason:   "a reason",
			Closures: []store.Closure{{
				Finding: "security-1", Round: 1, Step: workID(t, got.s, "correct@1/closure", "close security-1"),
				Result: "closed", Reason: "checked",
			}},
		},
	}
	if !reflect.DeepEqual(got.review.Ledger, wantLedger) {
		t.Errorf("ledger = %+v, want %+v", got.review.Ledger, wantLedger)
	}
	wantRounds := []store.ReviewRound{{Round: 1, Revision: runBase, Outcome: "passed"}}
	if !reflect.DeepEqual(got.review.Rounds, wantRounds) {
		t.Errorf("rounds = %+v, want %+v", got.review.Rounds, wantRounds)
	}
	if got.review.Clean != runBase {
		t.Errorf("clean revision = %q, want %q", got.review.Clean, runBase)
	}
	reviewLoopWantCauses(t, got, []string{reviewLoopCreatedCause, reviewLoopRoundOneCause})
	reviewLoopWantClean(t, got)
}

// TestReviewLoopFindingOpenAfterTwoRoundsEndsWithGaps: a confirmed finding that its closure leaves open in both rounds
// wakes a fix plan in each round, and once the rounds run out the epic ends ended_with_gaps, naming the finding.
func TestReviewLoopFindingOpenAfterTwoRoundsEndsWithGaps(t *testing.T) {
	t.Parallel()
	got := reviewLoopCase(t, reviewLoopFindingScript("open", "open"))

	wantRounds := []store.ReviewRound{
		{Round: 1, Revision: runBase, Outcome: "failed"},
		{Round: 2, Revision: runBase, Outcome: "failed"},
	}
	if !reflect.DeepEqual(got.review.Rounds, wantRounds) {
		t.Errorf("rounds = %+v, want %+v", got.review.Rounds, wantRounds)
	}
	if len(got.review.Ledger) != 1 || got.review.Ledger[0].ID != "security-1" {
		t.Fatalf("ledger = %+v, want the one finding security-1", got.review.Ledger)
	}
	closures := got.review.Ledger[0].Closures
	if len(closures) != 2 || closures[0].Result != "open" || closures[1].Result != "open" {
		t.Errorf("closures of security-1 = %+v, want open in both rounds", closures)
	}
	if got.review.Clean != "" {
		t.Errorf("clean revision = %q, want none", got.review.Clean)
	}
	reviewLoopWantCauses(t, got, []string{reviewLoopCreatedCause, reviewLoopRoundOneCause, reviewLoopRoundTwoCause})
	reviewLoopWantEnd(t, got, "ended_with_gaps",
		kinds.EndingBody("ended_with_gaps", []string{"security-1: open after 2 rounds"}, nil))
}

// TestReviewLoopCostlyReviewerEndsWithGap: a reviewer whose two tries both end costly gives a gap, which the review keeps
// as its verdict. The reviewer is started once more than the others, its second try is the last, and the epic ends
// ended_with_gaps naming the reviewer.
func TestReviewLoopCostlyReviewerEndsWithGap(t *testing.T) {
	t.Parallel()
	got := reviewLoopCase(t, reviewLoopCostlyScript())

	reason := "reviewer 1 gave no verdict twice: failed: " + reviewLoopGapFailure
	want := []store.ReviewVerdict{
		{
			Position: 1, Reviewer: "security", Step: workID(t, got.s, reviewLoopReview, "reviewer 1 try 2"),
			Verdict: "gap", Reason: reason,
		},
		{Position: 2, Reviewer: "tests", Step: workID(t, got.s, reviewLoopReview, "reviewer 2"), Verdict: "holds"},
	}
	if !reflect.DeepEqual(got.review.Verdicts, want) {
		t.Errorf("verdicts = %+v, want %+v", got.review.Verdicts, want)
	}
	reviewLoopWantStarts(t, got, []string{"security", "security", "tests"})
	reviewLoopWantCauses(t, got, []string{reviewLoopCreatedCause, reviewLoopGapCause})
	reviewLoopWantEnd(t, got, "ended_with_gaps",
		kinds.EndingBody("ended_with_gaps", []string{"reviewer security: " + reason}, nil))
}

// TestReviewLoopRestartedReviewerRerunsFreeAndEndsClean: a reviewer step the engine restarts reruns without a cost. Two
// restarts and then a holding third try still end the review with the reviewer holding, so the epic ends clean.
func TestReviewLoopRestartedReviewerRerunsFreeAndEndsClean(t *testing.T) {
	t.Parallel()
	got := reviewLoopCase(t, reviewLoopRestartScript())

	want := []store.ReviewVerdict{
		{
			Position: 1, Reviewer: "security", Step: workID(t, got.s, reviewLoopReview, "reviewer 1 try 3"),
			Verdict: "holds",
		},
		{Position: 2, Reviewer: "tests", Step: workID(t, got.s, reviewLoopReview, "reviewer 2"), Verdict: "holds"},
	}
	if !reflect.DeepEqual(got.review.Verdicts, want) {
		t.Errorf("verdicts = %+v, want %+v", got.review.Verdicts, want)
	}
	reviewLoopWantStarts(t, got, []string{"security", "security", "security", "tests"})
	if got.review.Clean != runBase {
		t.Errorf("clean revision = %q, want %q", got.review.Clean, runBase)
	}
	reviewLoopWantCauses(t, got, []string{reviewLoopCreatedCause, reviewLoopEmptyCause})
	reviewLoopWantClean(t, got)
}
