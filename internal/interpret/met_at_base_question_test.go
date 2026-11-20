package interpret

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// metFlow is a flow of an evidence node at base, which both Requirements of the met-at-base tests run under, followed by
// a gate at base. The gate starts only once the evidence node has passed, so it shows that the walk went on.
const metFlow = `name: met
nodes:
  - id: red
    kind: evidence
    at: base
  - id: lint
    kind: gate
    gate: lint
    at: base
`

// metRequirements are the Requirements of the met-at-base tests: R1 and R2, each with a capture item.
func metRequirements() []store.EpicRequirement {
	item := func(conclusion string) evidence.Item {
		return evidence.Item{
			Capture: []string{"scripts/check"}, Limit: 60, Artifacts: []string{}, Supplied: []string{},
			Conclusion: conclusion, Judge: "a judge",
		}
	}
	return []store.EpicRequirement{
		{ID: "R1", Text: "the runner runs", Item: item("the runner runs")},
		{ID: "R2", Text: "the parser reads", Item: item("the parser reads")},
	}
}

// metWorld is a met-at-base test's world: a store with the test epic stored at its base, pinned to metFlow, and a runner
// over the check, evidence and step fakes. Its logger keeps what a pass logs, so a pass that logs an error fails the test.
type metWorld struct {
	t      *testing.T
	s      *store.Store
	path   string
	base   string
	fakes  nodeFakes
	runner *Runner
	logs   *bytes.Buffer
}

// newMetWorld returns a world with the test epic stored at its base, and no run started.
func newMetWorld(t *testing.T) *metWorld {
	t.Helper()
	ctx := context.Background()
	s, path := openNodeStore(t)
	repo, base := nodeRepo(t)
	pinned, err := workflow.Load("met", func(string) ([]byte, bool, error) { return []byte(metFlow), true, nil })
	if err != nil {
		t.Fatalf("Load met: %v", err)
	}
	if err = s.CreateEpic(ctx, store.EpicRow{
		ID: runEpic, Repository: repo, Base: base, Title: "Met at base", Phase: runPhase, Created: runClock(),
		Changed: runClock(), Gates: nodeGates(), Requirements: metRequirements(),
		Workflow: store.EpicWorkflow{
			Name: pinned.Name, Text: pinned.Text, SHA256: pinned.SHA256, Nodes: pinnedNodes(pinned.Workflow.Nodes, ""),
		},
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	fakes := newNodeFakes(s)
	logs := &bytes.Buffer{}
	runner := runNodes(s, fakes)
	runner.logger = slog.New(slog.NewTextHandler(logs, nil))
	return &metWorld{t: t, s: s, path: path, base: base, fakes: fakes, runner: runner, logs: logs}
}

// pass runs one Runner.Pass over the test epic and fails the test on any logged error.
func (w *metWorld) pass() {
	w.t.Helper()
	w.runner.Pass(context.Background())
	if w.logs.Len() > 0 {
		w.t.Fatalf("pass logged an error: %s", w.logs.String())
	}
}

// judge ends the red run id as its judge of verdict holds it, with step as the judge step. Its capture is taken first, as
// a capture that ran is.
func (w *metWorld) judge(id, verdict, step string) {
	w.t.Helper()
	ctx := context.Background()
	if err := w.s.RecordEvidenceCapture(ctx, id, store.EvidenceCapture{At: runClock()}); err != nil {
		w.t.Fatalf("RecordEvidenceCapture %s: %v", id, err)
	}
	if err := w.s.SetEvidenceJudge(ctx, id, step); err != nil {
		w.t.Fatalf("SetEvidenceJudge %s: %v", id, err)
	}
	verdicts := store.EvidenceVerdict{
		Verdict: verdict, Passed: evidence.Passed(evidence.PhaseRed, "", 0, verdict), At: runClock(),
	}
	if err := w.s.RecordEvidenceVerdict(ctx, id, verdicts); err != nil {
		w.t.Fatalf("RecordEvidenceVerdict %s: %v", id, err)
	}
}

// metContinueTell returns the tell the red run of the test epic writes when R1 is judged holding at the base and the
// epic goes on.
func (w *metWorld) metContinueTell() string {
	return fmt.Sprintf(
		"workflow node red: R1 met at the epic's base %s, recorded as met there, and the epic goes on", w.base,
	)
}

// metReason returns the reason the park of the red run of the test epic gives when R1 is judged holding at the base.
func (w *metWorld) metReason() string {
	return fmt.Sprintf(
		"workflow node red: R1 met at the epic's base %s; you can retry (run the red check of R1 again with a fresh "+
			"judge), drop (drop R1 as already met, recorded as your decision, and go on without their red) or end "+
			"(end the epic)",
		w.base,
	)
}

// parkRedMet parks the test epic as an evidence node at base did before a met-at-base red continued: its red run has
// judged R1 holding at the base and R2 failing it, and the run and the epic are parked with the question that offers
// retry, drop and end. The store writes that park here, since the walk no longer writes it. It returns the id of the
// red run's R1 run, at attempt 0.
func (w *metWorld) parkRedMet() string {
	w.t.Helper()
	w.pass()
	first := workID(w.t, w.s, "red", "R1")
	w.judge(first, evidence.VerdictHolds, "judge-1")
	w.judge(workID(w.t, w.s, "red", "R2"), evidence.VerdictFails, "judge-1")
	err := w.s.ParkNodeMetAtBase(
		context.Background(),
		runEpic,
		"red",
		0,
		outcomeFailed,
		"R1 red: failed",
		w.metReason(),
		[]string{"R1"},
		runClock(),
	)
	if err != nil {
		w.t.Fatalf("ParkNodeMetAtBase: %v", err)
	}
	return first
}

// answer records the owner's answer choice to the open park question, as decision id with text.
func (w *metWorld) answer(id, text, choice string) store.EpicDecision {
	w.t.Helper()
	open, err := w.s.OpenQuestions(context.Background(), runEpic)
	if err != nil || len(open) != 1 {
		w.t.Fatalf("open questions = %+v and %v, want one open park question", open, err)
	}
	decision, err := w.s.AnswerQuestion(
		context.Background(),
		runEpic,
		id,
		text,
		open[0].Seq,
		choice,
		store.Maker{},
		runClock(),
	)
	if err != nil {
		w.t.Fatalf("AnswerQuestion %s: %v", choice, err)
	}
	return decision
}

// evidenceCallsOf returns the RunItem calls the fakes recorded for the red run ids, in call order.
func (w *metWorld) evidenceCallsOf(ids ...string) []evidenceCall {
	var calls []evidenceCall
	for _, call := range w.fakes.evidence.calls {
		if slices.Contains(ids, call.id) {
			calls = append(calls, call)
		}
	}
	return calls
}

// TestMetAtBaseRedContinuesRedJudgedHoldsEndsPassedWithATell checks that a red run judged holding at the base is
// recorded as met at the base with a tell, that the red run ends passed, and that the epic goes on to its next node
// with no park and no question. R4: it used to park the run and the epic with a question that offered retry, drop and
// end.
func TestMetAtBaseRedContinuesRedJudgedHoldsEndsPassedWithATell(t *testing.T) {
	t.Parallel()
	w := newMetWorld(t)
	w.pass()
	first := workID(t, w.s, "red", "R1")
	w.judge(first, evidence.VerdictHolds, "judge-1")
	w.judge(workID(t, w.s, "red", "R2"), evidence.VerdictFails, "judge-1")
	w.pass()

	if run := runAt(t, w.s, "red"); run.State != store.NodeEnded || run.Outcome != outcomePassed {
		t.Errorf("red run = %+v, want ended passed", run)
	}
	if run := runAt(t, w.s, "lint"); run.State != store.NodeRunning {
		t.Errorf("gate run after the evidence node = %+v, want it running", run)
	}
	epic := epicOf(t, w.s)
	if epic.Parked != "" || epic.Phase != runPhase {
		t.Errorf("parked and phase = %q and %q, want none and %s", epic.Parked, epic.Phase, runPhase)
	}
	open, err := w.s.OpenQuestions(context.Background(), runEpic)
	if err != nil {
		t.Fatalf("OpenQuestions: %v", err)
	}
	if len(open) != 0 {
		t.Errorf("open questions = %+v, want none: a met-at-base red asks the owner nothing", open)
	}
	if tells := nodeTells(t, w.path); !reflect.DeepEqual(tells, []string{w.metContinueTell()}) {
		t.Errorf("tells = %q, want the met-at-base tell once", tells)
	}
}

// TestMetAtBaseQuestionRetryStartsANewRedRunJudgedByANewStep checks that answering retry reruns the red run at its next
// attempt, so each Requirement gets a new red run under a new id, judged by a judge step of its own.
func TestMetAtBaseQuestionRetryStartsANewRedRunJudgedByANewStep(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newMetWorld(t)
	first := w.parkRedMet()
	w.answer("D3", "", store.ChoiceRetry)

	w.pass()
	if run := runAt(t, w.s, "red"); run.State != store.NodeRunning || run.Attempt != 1 {
		t.Fatalf("red run after retry = %+v, want running at attempt 1", run)
	}
	again := workID(t, w.s, "red", "R1")
	if again == first {
		t.Fatalf("red run of R1 after retry has the id %s it had before", again)
	}
	wantCalls := []evidenceCall{
		{id: again, epic: runEpic, item: "R1", revision: w.base, phase: evidence.PhaseRed},
		{id: workID(t, w.s, "red", "R2"), epic: runEpic, item: "R2", revision: w.base, phase: evidence.PhaseRed},
	}
	if got := w.evidenceCallsOf(again, workID(t, w.s, "red", "R2")); !reflect.DeepEqual(got, wantCalls) {
		t.Errorf("red runs started after retry = %+v, want %+v", got, wantCalls)
	}

	w.judge(again, evidence.VerdictHolds, "judge-2")
	w.judge(workID(t, w.s, "red", "R2"), evidence.VerdictFails, "judge-2")
	firstRun, err := w.s.EvidenceRun(ctx, first)
	if err != nil {
		t.Fatalf("EvidenceRun %s: %v", first, err)
	}
	newRun, err := w.s.EvidenceRun(ctx, again)
	if err != nil {
		t.Fatalf("EvidenceRun %s: %v", again, err)
	}
	if firstRun.JudgeStep != "judge-1" || newRun.JudgeStep != "judge-2" {
		t.Errorf("judge steps = %q and %q, want judge-1 for the first run and judge-2 for the new one",
			firstRun.JudgeStep, newRun.JudgeStep)
	}
}

// TestMetAtBaseQuestionDropPassesTheRemainingRequirementsAndWalkGoesOn checks that answering drop marks R1 dropped by the
// answer's decision and reruns the red run at its next attempt, which starts no red run for R1 and ends passed on R2
// alone, so the walk goes on to the gate after it.
func TestMetAtBaseQuestionDropPassesTheRemainingRequirementsAndWalkGoesOn(t *testing.T) {
	t.Parallel()
	w := newMetWorld(t)
	w.parkRedMet()
	decision := w.answer("D3", "R1 is already met", store.ChoiceDrop)

	dropped := make(map[string]int64)
	for _, requirement := range epicOf(t, w.s).Requirements {
		dropped[requirement.ID] = requirement.DroppedBy
	}
	if dropped["R1"] != decision.Seq || dropped["R2"] != 0 {
		t.Fatalf("dropped by = %+v, want R1 by decision %d and R2 by none", dropped, decision.Seq)
	}

	w.pass()
	if run := runAt(t, w.s, "red"); run.State != store.NodeRunning || run.Attempt != 1 {
		t.Fatalf("red run after drop = %+v, want running at attempt 1", run)
	}
	red := runAt(t, w.s, "red")
	if calls := w.evidenceCallsOf(store.NodeWorkID(red.ID, red.Attempt, "R1")); len(calls) != 0 {
		t.Errorf("red runs of R1 started after drop = %+v, want none", calls)
	}
	again := workID(t, w.s, "red", "R2")
	if calls := w.evidenceCallsOf(again); len(calls) != 1 || calls[0].item != "R2" {
		t.Fatalf("red runs started after drop for R2 = %+v, want one", calls)
	}

	w.judge(again, evidence.VerdictFails, "judge-2")
	w.pass()
	if run := runAt(t, w.s, "red"); run.State != store.NodeEnded || run.Outcome != outcomePassed {
		t.Errorf("red run after the remaining judge = %+v, want ended passed", run)
	}
	if run := runAt(t, w.s, "lint"); run.State != store.NodeRunning {
		t.Errorf("gate run after the evidence node = %+v, want it running", run)
	}
	calls := w.fakes.checks.calls
	if len(calls) == 0 || !reflect.DeepEqual(calls[len(calls)-1].Argv, []string{"./bin/lint"}) {
		t.Errorf("checks run = %+v, want the gate after the evidence node", calls)
	}
	if epic := epicOf(t, w.s); epic.Parked != "" {
		t.Errorf("epic parked = %q after the walk went on, want none", epic.Parked)
	}
}

// TestMetAtBaseQuestionEndEndsTheEpic checks that answering end sets the epic's phase to met_at_base, and that the epic
// then runs no further work: its park stays, its red run stays parked and no run starts.
func TestMetAtBaseQuestionEndEndsTheEpic(t *testing.T) {
	t.Parallel()
	w := newMetWorld(t)
	w.parkRedMet()
	w.answer("D3", "", store.ChoiceEnd)
	evidenceBefore := len(w.fakes.evidence.calls)
	checksBefore := len(w.fakes.checks.calls)

	w.pass()
	epic := epicOf(t, w.s)
	if epic.Phase != "met_at_base" || epic.Parked != w.metReason() {
		t.Errorf("phase and parked = %q and %q, want met_at_base and the park reason", epic.Phase, epic.Parked)
	}
	if run := runAt(t, w.s, "red"); run.State != store.NodeParked || run.Attempt != 0 {
		t.Errorf("red run after end = %+v, want parked at attempt 0", run)
	}
	if got := len(w.fakes.evidence.calls); got != evidenceBefore {
		t.Errorf("%d evidence runs after end, want %d: the epic runs no more", got, evidenceBefore)
	}
	if got := len(w.fakes.checks.calls); got != checksBefore {
		t.Errorf("%d checks after end, want %d: the epic runs no more", got, checksBefore)
	}
	for _, run := range nodeRunsOf(t, w.s) {
		if run.Node == "lint" {
			t.Errorf("gate run %+v started after end, want none", run)
		}
	}
	err := w.s.RetryEpic(context.Background(), runEpic, store.Maker{By: store.MakerOwner}, time.Unix(1700000000, 0))
	if err == nil || !strings.Contains(err.Error(), "parked for good") {
		t.Errorf("RetryEpic after end = %v, want the met-at-base refusal", err)
	}
}

// nodeRunsOf returns the node runs of the test epic.
func nodeRunsOf(t *testing.T, s *store.Store) []store.NodeRun {
	t.Helper()
	runs, err := s.NodeRuns(context.Background(), runEpic)
	if err != nil {
		t.Fatalf("NodeRuns: %v", err)
	}
	return runs
}
