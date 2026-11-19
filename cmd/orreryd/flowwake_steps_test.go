package main

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/interpret"
	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic values only. The epics and the steps are invented. The steps run the engine host's fake claude, which
// answers HANG with a turn that runs until it is stopped.
const (
	flowWakeStepsEpicA = "orrery-9e0f1a2/steps-a"
	flowWakeStepsEpicB = "orrery-9e0f1a2/steps-b"
	flowWakeStepsEpicC = "orrery-9e0f1a2/steps-c"
	// flowWakeStepsAgent is the agent the steps run, defined inline so claude resolves it with no agent file. Its JSON
	// has no spaces, so it stays one field of the fake claude's argument line.
	flowWakeStepsAgent  = "reviewer"
	flowWakeStepsAgents = `{"reviewer":{"description":"reviews","prompt":"review"}}`
	flowWakeStepsSchema = `{"type":"object","properties":{"status":{"type":"string"}}}`
	// flowWakeStepsPrompt is the prompt that holds a step's turn open until the step is stopped.
	flowWakeStepsPrompt = "HANG"
	// flowWakeStepsSignals sizes the channel the test interpreter's passes report on, so a pass never waits on a test
	// that is not reading it.
	flowWakeStepsSignals = 16
)

// flowWakeStepsStore is the store the test interpreter reads through. It counts the flow reads of each epic.
type flowWakeStepsStore struct {
	*store.Store
	mu    sync.Mutex
	reads map[string]int
}

// EpicFlow counts the read of id, then reads it.
func (s *flowWakeStepsStore) EpicFlow(ctx context.Context, id string) (store.EpicFlow, error) {
	s.mu.Lock()
	s.reads[id]++
	s.mu.Unlock()
	return s.Store.EpicFlow(ctx, id)
}

// readsOf returns how many flow reads of id the test interpreter has made.
func (s *flowWakeStepsStore) readsOf(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads[id]
}

// flowWakeStepsPlanner is the planner of the test interpreter. It has no turns to pass.
type flowWakeStepsPlanner struct{}

// Recover does nothing: the planner has no turns a previous run left.
func (flowWakeStepsPlanner) Recover(context.Context) {}

// PassOnly does nothing: the planner has no turns to pass.
func (flowWakeStepsPlanner) PassOnly(context.Context, func(epic string) bool) {}

// WaitTurns does nothing: the planner has no turns to wait on.
func (flowWakeStepsPlanner) WaitTurns() {}

// flowWakeStepsBuilder is the builder of the test interpreter. It has no build runs to pass.
type flowWakeStepsBuilder struct{}

// PassTasks does nothing: the builder has no build runs to pass.
func (flowWakeStepsBuilder) PassTasks(context.Context, string, []int64) {}

// flowWakeStepsFlow is the test interpreter a step test runs: its runner, the store it counts reads through, the signal
// each of its passes sends once it returns, and the means to stop it.
type flowWakeStepsFlow struct {
	runner *interpret.Runner
	counts *flowWakeStepsStore
	passed chan struct{}
	cancel context.CancelFunc
	done   chan struct{}
}

// startFlowWakeStepsFlow starts the test interpreter over st and returns once its start pass has reported. Its timer
// never fires, so it passes only when a wake reaches it. Reviewers and Ending register the review kind, which the start
// pass's review node needs: an unregistered running review node is idle, so the epic would rest and no wake would read
// its flow. These epics name no reviewers, so the review node gives no action. It stops at cleanup.
func startFlowWakeStepsFlow(t *testing.T, st *store.Store) *flowWakeStepsFlow {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	flow := &flowWakeStepsFlow{
		counts: &flowWakeStepsStore{Store: st, reads: map[string]int{}},
		passed: make(chan struct{}, flowWakeStepsSignals),
		cancel: cancel,
		done:   make(chan struct{}),
	}
	flow.runner = interpret.New(interpret.Config{
		Store:     flow.counts,
		Planner:   flowWakeStepsPlanner{},
		Builder:   flowWakeStepsBuilder{},
		Reviewers: interpret.NewEpicReviewers(),
		Ending:    st,
		NewTimer:  neverFiringTimer,
		Passed:    func() { flow.passed <- struct{}{} },
	})
	go func() {
		flow.runner.Run(ctx)
		close(flow.done)
	}()
	t.Cleanup(flow.stop)
	flow.awaitPass()
	return flow
}

// awaitPass waits for the test interpreter's next pass to report.
func (f *flowWakeStepsFlow) awaitPass() {
	<-f.passed
}

// stop cancels the test interpreter and waits for Run to return, so no pass is in flight after it.
func (f *flowWakeStepsFlow) stop() {
	f.cancel()
	<-f.done
}

// startFlowWakeStepsHost starts a --sessions host over st, with the step cap stepCap, whose steps run the fake claude.
// Its own interpreter passes only at its start, which flowTicks makes the only pass. The host stops at cleanup.
func startFlowWakeStepsHost(
	t *testing.T, st *store.Store, getenv func(string) string, path string, stepCap int,
) *sessionHost {
	t.Helper()
	opts := options{
		socket:           filepath.Join(filepath.Dir(path), "orreryd.sock"),
		claude:           eventWakeClaude(t),
		sessionShell:     eventWakeShell(t),
		sessionCap:       defaultSessionCap,
		stepCap:          stepCap,
		flowTicks:        filepath.Join(t.TempDir(), "flow-passes"),
		callerEventTimer: neverFiringTimer,
	}
	host, err := startSessions(t.Context(), opts, flowWiringHost, st, nil, nil, slog.New(slog.DiscardHandler), getenv)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}
	t.Cleanup(func() {
		if err := host.stop(); err != nil {
			t.Errorf("stop sessions: %v", err)
		}
	})
	return host
}

// flowWakeStepsStepOf returns a read-only step labeled with epic, in repo at HEAD, whose turn runs until it is stopped.
func flowWakeStepsStepOf(repo, epic string) enginehost.StepStart {
	return enginehost.StepStart{
		Agent:     flowWakeStepsAgent,
		Agents:    flowWakeStepsAgents,
		Prompt:    flowWakeStepsPrompt,
		Schema:    flowWakeStepsSchema,
		Workspace: repo,
		ReadOnly:  true,
		Epic:      epic,
	}
}

// TestOrrerydFlowWakeLabeledStepEndPassesItsEpic checks that the end of a step labeled with an epic, through orreryd's
// own wiring, makes exactly one pass of the interpreter. That pass reads the labeled epic's flow once, and the other
// open epic's flow not at all.
func TestOrrerydFlowWakeLabeledStepEndPassesItsEpic(t *testing.T) {
	st, getenv, path := openEventWakeStore(t)
	repo, _ := buildWiringRepo(t)
	created := time.Unix(plannerWiringStamp, 0)
	for _, epic := range []string{flowWakeStepsEpicA, flowWakeStepsEpicB} {
		createFlowWiringEpic(t, st, epic, created)
		endFlowWiringPlan(t, st, epic, created)
	}
	host := startFlowWakeStepsHost(t, st, getenv, path, defaultStepCap)
	flow := startFlowWakeStepsFlow(t, st)
	host.flowWake.set(flow.runner.Wake)
	readsA := flow.counts.readsOf(flowWakeStepsEpicA)
	readsB := flow.counts.readsOf(flowWakeStepsEpicB)

	id, err := host.steps.Start(t.Context(), flowWakeStepsStepOf(repo, flowWakeStepsEpicB))
	if err != nil {
		t.Fatalf("start step labeled %s: %v", flowWakeStepsEpicB, err)
	}
	if err := host.steps.Stop(t.Context(), id); err != nil {
		t.Fatalf("stop step %s: %v", id, err)
	}
	flow.awaitPass()
	flow.stop()

	if n := len(flow.passed); n != 0 {
		t.Errorf("passes after the step end = %d more, want exactly one", n)
	}
	if got := flow.counts.readsOf(flowWakeStepsEpicB) - readsB; got != 1 {
		t.Errorf("reads of epic %s's flow = %d, want 1", flowWakeStepsEpicB, got)
	}
	if got := flow.counts.readsOf(flowWakeStepsEpicA) - readsA; got != 0 {
		t.Errorf("reads of epic %s's flow = %d, want none", flowWakeStepsEpicA, got)
	}
}

// TestOrrerydFlowWakeCapRefusedStartRetriedWhenAnotherStepEnds checks that a start the step cap refuses, labeled with
// an epic, is handed to the interpreter once another step's end frees a slot, so the interpreter's pass that reads the
// refused epic's flow can retry the start and have it succeed.
func TestOrrerydFlowWakeCapRefusedStartRetriedWhenAnotherStepEnds(t *testing.T) {
	st, getenv, path := openEventWakeStore(t)
	repo, _ := buildWiringRepo(t)
	created := time.Unix(plannerWiringStamp, 0)
	for _, epic := range []string{flowWakeStepsEpicA, flowWakeStepsEpicC} {
		createFlowWiringEpic(t, st, epic, created)
		endFlowWiringPlan(t, st, epic, created)
	}
	host := startFlowWakeStepsHost(t, st, getenv, path, 1)
	flow := startFlowWakeStepsFlow(t, st)
	host.flowWake.set(flow.runner.Wake)

	idA, err := host.steps.Start(t.Context(), flowWakeStepsStepOf(repo, flowWakeStepsEpicA))
	if err != nil {
		t.Fatalf("start step labeled %s: %v", flowWakeStepsEpicA, err)
	}
	_, err = host.steps.Start(t.Context(), flowWakeStepsStepOf(repo, flowWakeStepsEpicC))
	if !errors.Is(err, enginehost.ErrCapReached) {
		t.Fatalf("start step labeled %s past the step cap: %v, want %v", flowWakeStepsEpicC, err,
			enginehost.ErrCapReached)
	}
	readsC := flow.counts.readsOf(flowWakeStepsEpicC)
	if err := host.steps.Stop(t.Context(), idA); err != nil {
		t.Fatalf("stop step %s: %v", idA, err)
	}

	// The end of A's step wakes epic A, and the slot it frees hands epic C on. Those wakes may make one pass or two, so
	// the test waits on the passes until the one that reads C's flow has reported.
	for flow.counts.readsOf(flowWakeStepsEpicC) == readsC {
		flow.awaitPass()
	}
	if got := flow.counts.readsOf(flowWakeStepsEpicC) - readsC; got != 1 {
		t.Fatalf("reads of epic %s's flow = %d, want 1", flowWakeStepsEpicC, got)
	}
	idC, err := host.steps.Start(t.Context(), flowWakeStepsStepOf(repo, flowWakeStepsEpicC))
	if err != nil {
		t.Fatalf("retry start of step labeled %s: %v", flowWakeStepsEpicC, err)
	}
	if err := host.steps.Stop(t.Context(), idC); err != nil {
		t.Fatalf("stop step %s: %v", idC, err)
	}
}
