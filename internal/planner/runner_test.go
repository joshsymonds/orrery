package planner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/agents"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// The Runner runs against the real store and the in-process Steps, and checks plans with testResolver.
var (
	_ Store    = (*store.Store)(nil)
	_ Steps    = (*enginehost.Steps)(nil)
	_ Resolver = testResolver{}
)

// testResolver is the Resolver the planner tests check plans with. It resolves the command gate check and the judge
// a judge, refuses the agent gate review, and refuses every other name with the text the real resolver gives.
type testResolver struct{}

// Gate returns nil for the command gate check, the agent gate refusal for review, and an unknown-gate error otherwise.
func (testResolver) Gate(_ context.Context, _ store.EpicRow, name string) error {
	switch name {
	case "check":
		return nil
	case "review":
		return fmt.Errorf("gate %q is an agent gate, not a command gate", name)
	default:
		return fmt.Errorf("resolve gate: %w: %q", gates.ErrUnknown, name)
	}
}

// Judge returns nil for the judge a judge, and an unresolved-agent error for any other name.
func (testResolver) Judge(_ store.EpicRow, name string) error {
	if name == "a judge" {
		return nil
	}
	return fmt.Errorf("resolve judge: %w: %q", agents.ErrUnresolved, name)
}

const (
	runnerEpicID = "orrery-1a2b3c4/planner-runner"
	runnerRepo   = "/work/synthetic-repo"
	runnerAgent  = "planner-agent"
	runnerTick   = 5 * time.Millisecond
)

// claimedAt is the time the runner's clock reads, so every claim and end is stamped at a known second.
func claimedAt() time.Time {
	return time.Unix(1700000500, 0)
}

// storeQuestion is the question validPlan asks: a shaped question with two choices and a recommendation.
const storeQuestion = `{"label":"which-store","text":"which store?","choices":[` +
	`{"id":"sqlite","does":"Keep the store in SQLite."},{"id":"files","does":"Write the store to files."}],` +
	`"recommended":"sqlite","why":"The tables already hold the epic.","catastrophe":"irreversible-step",` +
	`"confidence":"high","replaces":""}`

// validPlan is a planner return that passes the check: one task on R1 that names a file no task owns, a gate, an
// evidence item whose lists are empty, and the question storeQuestion.
const validPlan = `{"status":"done","decisions":["use the store"],` +
	`"tasks":[{"title":"Build it","deliverable":"the runner","files":["internal/planner/runner.go"],` +
	`"requirements":["R1"],"gate":"check","evidence":{"capture":["scripts/check"],"limit":60,"artifacts":[],` +
	`"supplied":[],"conclusion":"the runner runs","judge":"a judge","kind":"capture"}}],"outline":"one task","questions":[` +
	storeQuestion + `],` +
	`"outcome":"work"}`

// badQuestionPlan is validPlan with its question declaring one choice, which the store refuses.
const badQuestionPlan = `{"status":"done","decisions":["use the store"],` +
	`"tasks":[{"title":"Build it","deliverable":"the runner","files":["internal/planner/runner.go"],` +
	`"requirements":["R1"],"gate":"check","evidence":{"capture":["scripts/check"],"limit":60,"artifacts":[],` +
	`"supplied":[],"conclusion":"the runner runs","judge":"a judge","kind":"capture"}}],"outline":"one task","questions":[` +
	`{"label":"which-store","text":"which store?","choices":[{"id":"sqlite","does":"Keep the store in SQLite."}],` +
	`"recommended":"sqlite","why":"The tables already hold the epic.","catastrophe":"irreversible-step",` +
	`"confidence":"high","replaces":""}],` +
	`"outcome":"work"}`

// runnerEvidenceItem returns the evidence item validPlan's task stores and reads back as.
func runnerEvidenceItem() evidence.Item {
	return evidence.Item{
		Kind:    evidence.KindCapture,
		Capture: []string{"scripts/check"}, Limit: 60, Artifacts: []string{}, Supplied: []string{},
		Conclusion: "the runner runs", Judge: "a judge",
	}
}

// unknownRequirementPlan is validPlan with its task on R9, which the epic does not have.
const unknownRequirementPlan = `{"status":"done","decisions":[],` +
	`"tasks":[{"title":"Build it","deliverable":"the runner","files":["internal/planner/runner.go"],` +
	`"requirements":["R9"],"gate":"check","evidence":{"capture":["scripts/check"],"limit":60,"artifacts":[],` +
	`"supplied":[],"conclusion":"the runner runs","judge":"a judge","kind":"capture"}}],"outline":"one task",` +
	`"questions":[],"outcome":"work"}`

// unknownGatePlan is validPlan with its task's gate named missing, which no gate defines.
const unknownGatePlan = `{"status":"done","decisions":[],` +
	`"tasks":[{"title":"Build it","deliverable":"the runner","files":["internal/planner/runner.go"],` +
	`"requirements":["R1"],"gate":"missing","evidence":{"capture":["scripts/check"],"limit":60,"artifacts":[],` +
	`"supplied":[],"conclusion":"the runner runs","judge":"a judge","kind":"capture"}}],"outline":"one task",` +
	`"questions":[],"outcome":"work"}`

// unknownJudgePlan is validPlan with its task's judge named nobody, which no agent answers to.
const unknownJudgePlan = `{"status":"done","decisions":[],` +
	`"tasks":[{"title":"Build it","deliverable":"the runner","files":["internal/planner/runner.go"],` +
	`"requirements":["R1"],"gate":"check","evidence":{"capture":["scripts/check"],"limit":60,"artifacts":[],` +
	`"supplied":[],"conclusion":"the runner runs","judge":"nobody","kind":"capture"}}],"outline":"one task",` +
	`"questions":[],"outcome":"work"}`

// fakeSteps is a Steps whose steps end when the test ends them. It records each start and each Stop. The first
// stopFailures Stops fail, and a Stop that succeeds ends its step as stopped, as the engine's Stop does. The row a step
// last stored is kept in ended, a channel that holds one row: Subscribe offers that row, so a row stored before the
// runner subscribes is not lost, and end offers each later row to the subscriptions open on its step.
type fakeSteps struct {
	mu           sync.Mutex
	starts       []enginehost.StepStart
	startErr     error
	stops        []string
	stopFailures int
	ended        map[string]chan store.StepRow
	subs         map[string][]*enginehost.StepSubscription
}

func newFakeSteps() *fakeSteps {
	return &fakeSteps{ended: map[string]chan store.StepRow{}, subs: map[string][]*enginehost.StepSubscription{}}
}

func (f *fakeSteps) Start(_ context.Context, start enginehost.StepStart) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return "", f.startErr
	}
	f.starts = append(f.starts, start)
	return fmt.Sprintf("step-%d", len(f.starts)), nil
}

func (f *fakeSteps) Status(_ context.Context, _ string) (store.StepRow, error) {
	return store.StepRow{}, errors.New("fakeSteps.Status is not used by the runner")
}

// Subscribe returns a subscription to each id, offered the row each id last stored, as the engine's Subscribe reads it.
func (f *fakeSteps) Subscribe(_ context.Context, ids []string) (*enginehost.StepSubscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sub := enginehost.NewStepSubscription(ids)
	for _, id := range ids {
		f.subs[id] = append(f.subs[id], sub)
		if row, ok := f.stored(id); ok {
			sub.Offer(row)
		}
	}
	return sub, nil
}

// subscribed reports whether the runner has subscribed to step id.
func (f *fakeSteps) subscribed(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.subs[id]) > 0
}

// stored returns the row last stored for step id, and whether there is one, leaving the row stored. It is called under
// f.mu.
func (f *fakeSteps) stored(id string) (store.StepRow, bool) {
	box := f.ended[id]
	select {
	case row := <-box:
		box <- row
		return row, true
	default:
		return store.StepRow{}, false
	}
}

// Stop records the call of id in stops. A call within the first stopFailures fails with errSyntheticStop, and any other
// call ends step id as stopped.
func (f *fakeSteps) Stop(_ context.Context, id string) error {
	f.mu.Lock()
	f.stops = append(f.stops, id)
	fail := len(f.stops) <= f.stopFailures
	f.mu.Unlock()
	if fail {
		return errSyntheticStop
	}
	f.end(id, store.StepRow{End: "stopped"})
	return nil
}

// end stores row as the row of step id and offers it to the subscriptions open on the step. A row that ends the step is
// the step's end; a row that holds it is the hold a usage limit began.
func (f *fakeSteps) end(id string, row store.StepRow) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row.ID = id
	box, ok := f.ended[id]
	if !ok {
		box = make(chan store.StepRow, 1)
		f.ended[id] = box
	}
	select {
	case <-box:
	default:
	}
	box <- row
	for _, sub := range f.subs[id] {
		sub.Offer(row)
	}
}

// handTimers is the NewTimer of a Runner a test drives by hand. Each timer it arms is recorded in the order it was armed,
// and fires only when the test calls fire with its index, so no test waits on a real clock.
type handTimers struct {
	mu     sync.Mutex
	timers []*handTimer
}

// handTimer is a timer a handTimers armed: the channel it fires on, and whether the test has fired it.
type handTimer struct {
	ch    chan time.Time
	fired bool
}

func newHandTimers() *handTimers {
	return &handTimers{}
}

// newTimer arms a timer. Its stop reports whether the timer had not fired.
func (h *handTimers) newTimer(time.Duration) (<-chan time.Time, func() bool) {
	timer := &handTimer{ch: make(chan time.Time, 1)}
	h.mu.Lock()
	h.timers = append(h.timers, timer)
	h.mu.Unlock()
	return timer.ch, func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return !timer.fired
	}
}

// count returns how many timers have been armed.
func (h *handTimers) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.timers)
}

// fire fires the timer with index i, the i-th armed, at claimedAt. A timer fires once.
func (h *handTimers) fire(t *testing.T, i int) {
	t.Helper()
	h.mu.Lock()
	if i >= len(h.timers) || h.timers[i].fired {
		h.mu.Unlock()
		t.Fatalf("fire timer %d: it is not armed, or it has fired", i)
	}
	timer := h.timers[i]
	timer.fired = true
	h.mu.Unlock()
	timer.ch <- claimedAt()
}

// started returns the starts recorded so far, in order.
func (f *fakeSteps) started() []enginehost.StepStart {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]enginehost.StepStart(nil), f.starts...)
}

// stopCalls returns the ids Stop was called with so far, in order.
func (f *fakeSteps) stopCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.stops...)
}

// openRunnerStore opens a store in a temp dir and closes it when the test ends.
func openRunnerStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(context.Background(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

// createRunnerEpic stores runnerEpicID with Requirements R1 and R2, whose created trigger is pending.
func createRunnerEpic(t *testing.T, s *store.Store) {
	t.Helper()
	if err := s.CreateEpic(context.Background(), store.EpicRow{
		ID: runnerEpicID, Repository: runnerRepo, Title: "Plan the runner", Approved: "approved text",
		Intent: "plan the next tasks", Care: "serious", Approach: "one planner turn", ReleaseThen: "release it",
		Checks: "just check", Phase: "accepted", Created: claimedAt().Add(-time.Minute),
		Changed: claimedAt().Add(-time.Minute),
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: "a trigger is claimed", Evidence: "runner test", Kind: "test"},
			{ID: "R2", Text: "a pending trigger gets a turn", Evidence: "runner test", Kind: "test"},
		},
	}); err != nil {
		t.Fatalf("CreateEpic %s: %v", runnerEpicID, err)
	}
}

// agentReturning returns a Config Agent that counts each read in reads and returns agent, or err.
func agentReturning(agent string, err error, reads *atomic.Int64) func() (string, error) {
	return func() (string, error) {
		reads.Add(1)
		return agent, err
	}
}

// runnerConfig returns the Config of a runner over s and steps, read through agent, at the runner tick.
func runnerConfig(s *store.Store, steps Steps, agent func() (string, error)) Config {
	return Config{
		Store: s, Steps: steps, Agent: agent, Tick: runnerTick,
		Now: claimedAt, Resolve: testResolver{},
	}
}

// startRunner runs a Runner built from cfg in its own goroutine. Cancel ends Run, and finished closes when Run returns.
func startRunner(cfg Config) (context.CancelFunc, <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		New(cfg).Run(ctx)
	}()
	return cancel, finished
}

// runRunner runs a Runner built from cfg until the test ends, then waits for Run to return.
func runRunner(t *testing.T, cfg Config) {
	t.Helper()
	cancel, finished := startRunner(cfg)
	t.Cleanup(func() {
		cancel()
		<-finished
	})
}

// waitFor polls cond until it holds; what names the condition. It has no deadline of its own: the runner's store work
// runs at the machine's pace, and the test binary's timeout ends a wait that never ends.
func waitFor(t *testing.T, _ string, cond func() bool) {
	t.Helper()
	for !cond() {
		time.Sleep(time.Millisecond)
	}
}

// epicOf returns the stored runner epic.
func epicOf(t *testing.T, s *store.Store) store.EpicRow {
	t.Helper()
	epic, err := s.Epic(context.Background(), runnerEpicID)
	if err != nil {
		t.Fatalf("Epic %s: %v", runnerEpicID, err)
	}
	return epic
}

// stepOneOnTurnOne reports whether the runner epic has exactly one turn and that turn's step is step-1.
func stepOneOnTurnOne(t *testing.T, s *store.Store) bool {
	t.Helper()
	turns := epicOf(t, s).Turns
	return len(turns) == 1 && turns[0].Step == "step-1"
}

func TestRunnerClaimsOneTurnAndStartsReadOnlyPlannerStep(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	waitFor(t, "a planner step to start", func() bool { return len(steps.started()) == 1 })
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	start := steps.started()[0]
	epic := epicOf(t, s)
	triggers, err := s.TurnTriggers(context.Background(), runnerEpicID, 1)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	if !start.ReadOnly {
		t.Errorf("step ReadOnly = false, want true")
	}
	if start.Agent != runnerAgent {
		t.Errorf("step Agent = %q, want %q", start.Agent, runnerAgent)
	}
	if start.Schema != Schema {
		t.Errorf("step Schema is not the planner Schema")
	}
	if start.Workspace != runnerRepo {
		t.Errorf("step Workspace = %q, want the epic repository %q", start.Workspace, runnerRepo)
	}
	if start.Epic != runnerEpicID {
		t.Errorf("step Epic = %q, want %q", start.Epic, runnerEpicID)
	}
	if want := promptOf(t, epic, 1, triggers); start.Prompt != want {
		t.Errorf("step Prompt differs from Prompt of the claimed turn:\n got %s\nwant %s", start.Prompt, want)
	}
	if start.ReturnCheck == nil {
		t.Errorf("step ReturnCheck is nil, want the planner return check")
	}
	if got := epic.Turns[0].State; got != "running" {
		t.Errorf("turn 1 state = %q, want running", got)
	}
}

func TestRunnerReturnCheckRefusesUnknownRequirementAndPassesValidPlan(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitFor(t, "a planner step to start", func() bool { return len(steps.started()) == 1 })
	check := steps.started()[0].ReturnCheck

	want := []string{`tasks[0] "Build it" requirements[0]: not a Requirement of the epic`}
	if got := check(json.RawMessage(unknownRequirementPlan)); !reflect.DeepEqual(got, want) {
		t.Errorf("ReturnCheck of a plan naming R9 = %q, want %q", got, want)
	}
	if got := check(json.RawMessage(validPlan)); got != nil {
		t.Errorf("ReturnCheck of a valid plan = %q, want none", got)
	}
	if got := check(json.RawMessage(`{`)); len(got) != 1 {
		t.Errorf("ReturnCheck of invalid JSON = %q, want one line", got)
	}
}

func TestRunnerReturnCheckRefusesUnknownGateAndJudge(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitFor(t, "a planner step to start", func() bool { return len(steps.started()) == 1 })
	check := steps.started()[0].ReturnCheck

	wantGate := []string{`tasks[0] "Build it" gate: resolve gate: unknown gate: "missing"`}
	if got := check(json.RawMessage(unknownGatePlan)); !reflect.DeepEqual(got, wantGate) {
		t.Errorf("ReturnCheck of a plan naming gate missing = %q, want %q", got, wantGate)
	}
	wantJudge := []string{`tasks[0] "Build it" evidence.judge: resolve judge: agent name does not resolve: "nobody"`}
	if got := check(json.RawMessage(unknownJudgePlan)); !reflect.DeepEqual(got, wantJudge) {
		t.Errorf("ReturnCheck of a plan naming judge nobody = %q, want %q", got, wantJudge)
	}
}

func TestRunnerDoneReturnStoresTasksAndConsumesTrigger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitFor(t, "a planner step to start", func() bool { return len(steps.started()) == 1 })
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	steps.end("step-1", store.StepRow{End: "done", Result: validPlan})
	waitFor(t, "turn 1 to succeed", func() bool { return epicOf(t, s).Turns[0].State == "succeeded" })

	epic := epicOf(t, s)
	turn := epic.Turns[0]
	if want := []string{"use the store"}; !reflect.DeepEqual(turn.Decisions, want) {
		t.Errorf("turn 1 decisions = %q, want %q", turn.Decisions, want)
	}
	if turn.Outline != "one task" || turn.Outcome != "work" {
		t.Errorf("turn 1 outline, outcome = %q, %q, want one task, work", turn.Outline, turn.Outcome)
	}
	wantTasks := []store.EpicTask{{
		Seq: 1, Turn: 1, Title: "Build it", Deliverable: "the runner", Files: []string{"internal/planner/runner.go"},
		Requirements: []string{"R1"}, Gate: "check", Evidence: runnerEvidenceItem(), Status: "open", Version: 1,
	}}
	if !reflect.DeepEqual(epic.Tasks, wantTasks) {
		t.Errorf("tasks = %+v, want %+v", epic.Tasks, wantTasks)
	}
	triggers, err := s.TurnTriggers(ctx, runnerEpicID, 1)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	if len(triggers) != 1 || triggers[0].State != "consumed" {
		t.Errorf("triggers of turn 1 = %+v, want one consumed trigger", triggers)
	}
	states, err := s.PlanStates(ctx)
	if err != nil {
		t.Fatalf("PlanStates: %v", err)
	}
	if len(states) != 0 {
		t.Errorf("PlanStates after the return = %+v, want none: the trigger is consumed", states)
	}
}

func TestRunnerPromptOfNextTurnShowsTheFollowCountOfAnsweredRecommendation(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitFor(t, "a planner step to start", func() bool { return len(steps.started()) == 1 })
	steps.end("step-1", store.StepRow{End: "done", Result: validPlan})
	waitFor(t, "turn 1 to succeed", func() bool { return epicOf(t, s).Turns[0].State == "succeeded" })

	if _, err := s.AnswerQuestion(
		context.Background(),
		runnerEpicID,
		"answer-1",
		"use sqlite",
		1,
		"sqlite",
		store.Maker{},
		claimedAt(),
	); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}
	waitFor(t, "a second planner step to start", func() bool { return len(steps.started()) == 2 })

	want := "## Advice followed\n- which-store: 1 of 1 answers took the recommendation\n"
	if prompt := steps.started()[1].Prompt; !strings.Contains(prompt, want) {
		t.Errorf("prompt of turn 2 lacks %q; prompt is:\n%s", want, prompt)
	}
}

func TestRunnerPassWhileTurnRunsStartsNoSecondStep(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitFor(t, "a planner step to start", func() bool { return len(steps.started()) == 1 })
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	passesAtStart := reads.Load()
	waitFor(t, "three more passes", func() bool { return reads.Load() >= passesAtStart+3 })
	if n := len(steps.started()); n != 1 {
		t.Errorf("steps started = %d while turn 1 runs, want 1", n)
	}
	if n := len(epicOf(t, s).Turns); n != 1 {
		t.Errorf("turns = %d while turn 1 runs, want 1", n)
	}
}

func TestRunnerPausedEpicGetsNoTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	if _, err := s.ClaimTurn(ctx, runnerEpicID, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.FreeTurn(ctx, runnerEpicID, 1, store.TurnFree{Reason: "owner paused the epic", Pause: true},
		claimedAt()); err != nil {
		t.Fatalf("FreeTurn: %v", err)
	}
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitFor(t, "three passes", func() bool { return reads.Load() >= 3 })

	if n := len(steps.started()); n != 0 {
		t.Errorf("steps started for a paused epic = %d, want 0", n)
	}
	epic := epicOf(t, s)
	if !epic.Paused {
		t.Errorf("epic Paused = false, want true")
	}
	if n := len(epic.Turns); n != 1 {
		t.Errorf("turns of a paused epic = %d, want only the freed turn 1", n)
	}
}

func TestRunnerChecksFailedEndFailsTurnWithReasonAndFreesTrigger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitFor(t, "a planner step to start", func() bool { return len(steps.started()) == 1 })
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	const reason = `tasks[0] "Build it" requirements[0]: not a Requirement of the epic`
	steps.end("step-1", store.StepRow{End: "checks_failed", Reason: reason})
	waitFor(t, "a second planner step to start on the freed trigger", func() bool {
		return len(steps.started()) == 2
	})

	turns := epicOf(t, s).Turns
	if got, want := turns[0].State, "failed"; got != want {
		t.Errorf("turn 1 state = %q, want %q", got, want)
	}
	if want := "checks_failed: " + reason; turns[0].Reason != want {
		t.Errorf("turn 1 reason = %q, want %q", turns[0].Reason, want)
	}
	triggers, err := s.TurnTriggers(ctx, runnerEpicID, 2)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	if len(triggers) != 1 || triggers[0].ID != "created" {
		t.Errorf("triggers of turn 2 = %+v, want the created trigger claimed again", triggers)
	}
}

func TestRunnerDoneReturnWithBadQuestionFailsTurnWithRefusal(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitFor(t, "a planner step to start", func() bool { return len(steps.started()) == 1 })
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	steps.end("step-1", store.StepRow{End: "done", Result: badQuestionPlan})
	waitFor(t, "turn 1 to fail", func() bool { return epicOf(t, s).Turns[0].State == "failed" })

	epic := epicOf(t, s)
	const want = "complete turn 1 of epic " + runnerEpicID + `: bad planner question: ` +
		`question 1 "which-store": choices: fewer than two choices`
	if got := epic.Turns[0].Reason; got != want {
		t.Errorf("turn 1 reason = %q, want %q", got, want)
	}
	if len(epic.Questions) != 0 {
		t.Errorf("questions = %+v, want none stored", epic.Questions)
	}
}

func TestRunnerStartErrorFailsTurnWithErrorText(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	steps.startErr = errors.New("start refused")
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	waitFor(t, "turn 1 to fail", func() bool {
		turns := epicOf(t, s).Turns
		return len(turns) >= 1 && turns[0].State == "failed"
	})
	const want = "start planner step: start refused"
	if got := epicOf(t, s).Turns[0].Reason; got != want {
		t.Errorf("turn 1 reason = %q, want the Start error text %q", got, want)
	}
}

func TestRunnerParksEpicWhenPlannerRoleFails(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	roleErr := errors.New("planner role: no agent")
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning("", roleErr, &reads)))

	waitFor(t, "the epic to be parked with the role error", func() bool {
		return epicOf(t, s).Parked == roleErr.Error()
	})
	if n := len(steps.started()); n != 0 {
		t.Errorf("steps started for a parked epic = %d, want 0", n)
	}
	turns := epicOf(t, s).Turns
	if len(turns) != 1 {
		t.Fatalf("turns of a parked epic = %d, want 1: the park frees a claimed turn", len(turns))
	}
	if turns[0].State != "free" || turns[0].Reason != roleErr.Error() {
		t.Errorf("turn 1 = state %q reason %q, want free %q", turns[0].State, turns[0].Reason, roleErr.Error())
	}
	// A pending trigger belongs to turn 0; the freed turn 1 keeps none.
	triggers, err := s.TurnTriggers(context.Background(), runnerEpicID, 0)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	if len(triggers) != 1 || triggers[0].ID != "created" || triggers[0].State != "pending" {
		t.Errorf("pending triggers = %+v, want the created trigger pending again", triggers)
	}
}

func TestRunnerRejectedRateLimitFreeStoresResetTime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitForStarts(t, steps, 1)
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	resets := time.Unix(1700018000, 0)
	if err := s.ApplyEngine(ctx, []store.EngineOp{store.EngineRateLimited{
		ID: "step-1", Status: "rejected", Kind: "five_hour", ResetsAt: resets,
	}}, claimedAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	steps.end("step-1", store.StepRow{End: "checks_failed", Reason: "tasks[0] wrong"})
	waitFor(t, "the epic to be paused", func() bool { return epicOf(t, s).Paused })

	if got := epicOf(t, s).Turns[0].Resets; !got.Equal(resets) {
		t.Errorf("turn 1 resets = %v, want the limit's reset time %v", got, resets)
	}
}

func TestRunnerLeavesTurnRunningWhenItsContextEnds(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	cancel, finished := startRunner(runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	t.Cleanup(func() {
		cancel()
		<-finished
	})
	waitFor(t, "a planner step to start", func() bool { return len(steps.started()) == 1 })
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	cancel()
	<-finished
	turn := epicOf(t, s).Turns[0]
	if turn.State != "running" || turn.Step != "step-1" {
		t.Errorf("turn 1 after its context ended = state %q step %q, want running step-1", turn.State, turn.Step)
	}
}

func TestReturnCheckRefusesFilesOfUnfinishedTasksOnly(t *testing.T) {
	t.Parallel()
	epic := store.EpicRow{
		Requirements: []store.EpicRequirement{{ID: "R1"}},
		Tasks: []store.EpicTask{
			{Seq: 1, Title: "open", Files: []string{"internal/a.go"}, Status: "open"},
			{Seq: 2, Title: "done", Files: []string{"internal/b.go"}, Status: "done"},
		},
	}
	check := returnCheck(context.Background(), epic, store.Review{}, testResolver{}, nil)
	const touchA = `{"status":"done","decisions":[],` +
		`"tasks":[{"title":"Touch a","deliverable":"d","files":["internal/a.go"],` +
		`"requirements":["R1"],"gate":"check","evidence":{"capture":["scripts/check"],"limit":60,"artifacts":[],` +
		`"supplied":[],"conclusion":"c","judge":"a judge","kind":"capture"}}],"outline":"o",` +
		`"questions":[],"outcome":"work"}`
	const touchB = `{"status":"done","decisions":[],` +
		`"tasks":[{"title":"Touch b","deliverable":"d","files":["internal/b.go"],` +
		`"requirements":["R1"],"gate":"check","evidence":{"capture":["scripts/check"],"limit":60,"artifacts":[],` +
		`"supplied":[],"conclusion":"c","judge":"a judge","kind":"capture"}}],"outline":"o",` +
		`"questions":[],"outcome":"work"}`

	want := []string{`tasks[0] "Touch a" files[0]: owned by an unfinished task`}
	if got := check(json.RawMessage(touchA)); !reflect.DeepEqual(got, want) {
		t.Errorf("ReturnCheck of a file of an open task = %q, want %q", got, want)
	}
	if got := check(json.RawMessage(touchB)); got != nil {
		t.Errorf("ReturnCheck of a file of a done task = %q, want none", got)
	}
}

func TestRunnerClaimsEveryPendingTriggerInOneTurn(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(context.Background(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	createRunnerEpic(t, s)
	// No store call adds a second trigger before O3c2, so the test writes one beside the created trigger.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close %s: %v", path, err)
		}
	})
	if _, err = db.ExecContext(context.Background(), `INSERT INTO epic_triggers (epic, id, cause, state, turn, created)
		VALUES (?, 'answer-D9', 'synthetic answer', 'pending', 0, 1700000400)`, runnerEpicID); err != nil {
		t.Fatalf("insert second trigger: %v", err)
	}
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })
	triggers, err := s.TurnTriggers(context.Background(), runnerEpicID, 1)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	ids := make([]string, 0, len(triggers))
	for _, trigger := range triggers {
		ids = append(ids, trigger.ID+":"+trigger.State)
	}
	if want := []string{"answer-D9:claimed", "created:claimed"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("triggers of turn 1 = %v, want %v", ids, want)
	}
	if got := len(steps.started()); got != 1 {
		t.Errorf("started %d steps, want 1 turn for both triggers", got)
	}
}
