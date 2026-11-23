package interpret

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// The epics of the wake tests. They share a repository, so a release slot one holds is the slot of the others.
const (
	flowWakeA = "orrery-1a2b3c4/wake-a"
	flowWakeB = "orrery-1a2b3c4/wake-b"
	flowWakeC = "orrery-1a2b3c4/wake-c"
	flowWakeH = "orrery-1a2b3c4/wake-holder"
)

// flowWakeClock is the runner's clock in the wake tests. The test sets it, and the runner reads it on Run's goroutine.
type flowWakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// Now returns the clock's time.
func (c *flowWakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set sets the clock's time to now.
func (c *flowWakeClock) Set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

// flowWakeStore is the store a wake test gives the runner: it counts the flow reads of each epic.
type flowWakeStore struct {
	Store
	mu    sync.Mutex
	reads map[string]int
}

// EpicFlow counts the read of id, then reads it.
func (c *flowWakeStore) EpicFlow(ctx context.Context, id string) (store.EpicFlow, error) {
	c.mu.Lock()
	c.reads[id]++
	c.mu.Unlock()
	return c.Store.EpicFlow(ctx, id)
}

// flowWakeReads returns how many flow reads of id the runner has made.
func (c *flowWakeStore) flowWakeReads(id string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads[id]
}

// flowWakeRig is a runner over a real store with a fake clock. Each pass is reported on passes, unless the test stopped
// the flow after it.
type flowWakeRig struct {
	s         *store.Store
	reads     *flowWakeStore
	clock     *flowWakeClock
	runner    *Runner
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	passes    chan struct{}
	passCount atomic.Int32
	stopAfter atomic.Bool
}

// newFlowWakeRig returns a rig over s, its clock at runClock, that has not started Run.
func newFlowWakeRig(t *testing.T, s *store.Store) *flowWakeRig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	rig := &flowWakeRig{
		s: s, reads: &flowWakeStore{Store: s, reads: map[string]int{}}, clock: &flowWakeClock{now: runClock()},
		ctx: ctx, cancel: cancel, done: make(chan struct{}), passes: make(chan struct{}),
	}
	rig.runner = New(Config{
		Store: rig.reads, Planner: &fakePlanner{}, Builder: &fakeBuilder{}, Now: rig.clock.Now, Passed: rig.passed,
	})
	return rig
}

// flowWakeCreate stores the epic id in the repository the wake tests share, at the built-in epic, in phase.
func flowWakeCreate(t *testing.T, s *store.Store, id, phase string) {
	t.Helper()
	if err := s.CreateEpic(context.Background(), store.EpicRow{
		ID: id, Repository: runRepo, Base: runBase, Title: "Wake " + id, Phase: phase,
		Created: runClock(), Changed: runClock(), Workflow: builtinPin(t),
	}); err != nil {
		t.Fatalf("CreateEpic %s: %v", id, err)
	}
}

// start wires the store's epic changes to the runner's wakes, then starts Run. The writes made before it are not wakes.
func (r *flowWakeRig) start() {
	r.s.OnEpicsChanged(r.runner.Wake)
	go func() {
		r.runner.Run(r.ctx)
		close(r.done)
	}()
}

// stop cancels the flow and waits for Run to return.
func (r *flowWakeRig) stop() {
	r.cancel()
	<-r.done
}

// passed is Config.Passed: it counts the pass, then reports it on passes, or, once the test has set stopAfter, stops the
// flow after it.
func (r *flowWakeRig) passed() {
	r.passCount.Add(1)
	if r.stopAfter.Load() {
		r.cancel()
		return
	}
	select {
	case r.passes <- struct{}{}:
	case <-r.ctx.Done():
	}
}

// awaitPass waits for the next pass to report.
func (r *flowWakeRig) awaitPass() {
	<-r.passes
}

// flowWakePark parks epic on an orchestrator park, which asks its owner one question, and returns the question's seq.
// The epic's flow rests while it is parked.
func flowWakePark(t *testing.T, s *store.Store, epic string) int64 {
	t.Helper()
	ctx := context.Background()
	if err := s.ParkEpic(ctx, epic, store.ParkClassOrchestrator, "waiting on the owner", runClock()); err != nil {
		t.Fatalf("ParkEpic %s: %v", epic, err)
	}
	open, err := s.OpenQuestions(ctx, epic)
	if err != nil || len(open) != 1 {
		t.Fatalf("OpenQuestions %s = %+v, %v, want the park question", epic, open, err)
	}
	return open[0].Seq
}

// TestFlowWakeIdleClockPassesNothing: a flow that is started, with the clock advanced hours and no write, passes no epic
// after its start pass.
func TestFlowWakeIdleClockPassesNothing(t *testing.T) {
	t.Parallel()
	s := openRunStore(t)
	flowWakeCreate(t, s, flowWakeA, runPhase)
	rig := newFlowWakeRig(t, s)
	rig.runner.Pass(context.Background())
	rig.start()
	rig.awaitPass()
	rig.clock.Set(runClock().Add(3 * time.Hour))
	rig.stop()
	if got := rig.passCount.Load(); got != 1 {
		t.Errorf("passes = %d, want the start pass only", got)
	}
}

// TestFlowWakeStoreWritePassesOnlyItsEpic: with epics A and B open, a store write that answers a question of A gives one
// pass, which reads A's flow and not B's.
func TestFlowWakeStoreWritePassesOnlyItsEpic(t *testing.T) {
	t.Parallel()
	s := openRunStore(t)
	flowWakeCreate(t, s, flowWakeA, runPhase)
	flowWakeCreate(t, s, flowWakeB, runPhase)
	seq := flowWakePark(t, s, flowWakeA)
	rig := newFlowWakeRig(t, s)
	rig.runner.Pass(context.Background())
	rig.start()
	rig.awaitPass()
	readsA, readsB := rig.reads.flowWakeReads(flowWakeA), rig.reads.flowWakeReads(flowWakeB)

	if _, err := s.AnswerQuestion(context.Background(), flowWakeA, "wake-answer", "", seq, store.ChoiceRetry,
		store.Maker{}, runClock()); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}
	rig.awaitPass()
	rig.stop()
	if got := rig.reads.flowWakeReads(flowWakeA) - readsA; got != 1 {
		t.Errorf("reads of A's flow = %d, want 1", got)
	}
	if got := rig.reads.flowWakeReads(flowWakeB) - readsB; got != 0 {
		t.Errorf("reads of B's flow = %d, want none", got)
	}
	if got := rig.passCount.Load(); got != 2 {
		t.Errorf("passes = %d, want the start pass and one pass", got)
	}
}

// TestFlowWakeWriteMovingNoEpicPassesNothing: a store write that moves no epic gives no pass. The write that follows
// moves B alone, and the one pass it gives reads B's flow and not A's, so the first write woke nothing.
func TestFlowWakeWriteMovingNoEpicPassesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	flowWakeCreate(t, s, flowWakeA, runPhase)
	flowWakeCreate(t, s, flowWakeB, runPhase)
	rig := newFlowWakeRig(t, s)
	rig.runner.Pass(ctx)
	rig.start()
	rig.awaitPass()
	readsA, readsB := rig.reads.flowWakeReads(flowWakeA), rig.reads.flowWakeReads(flowWakeB)

	if err := s.PutOffQuestion(ctx, store.PutOffPane, "wake-session", "wake-question", runClock()); err != nil {
		t.Fatalf("PutOffQuestion: %v", err)
	}
	if err := s.SetEpicBranch(ctx, flowWakeB, "created"); err != nil {
		t.Fatalf("SetEpicBranch: %v", err)
	}
	rig.awaitPass()
	rig.stop()
	if got := rig.reads.flowWakeReads(flowWakeA) - readsA; got != 0 {
		t.Errorf("reads of A's flow = %d, want none", got)
	}
	if got := rig.reads.flowWakeReads(flowWakeB) - readsB; got != 1 {
		t.Errorf("reads of B's flow = %d, want 1", got)
	}
	if got := rig.passCount.Load(); got != 2 {
		t.Errorf("passes = %d, want the start pass and one pass", got)
	}
}

// TestFlowWakeNamedEpicPasses: a Wake naming B, as a step end calls it, gives one pass, which reads B's flow and not A's.
func TestFlowWakeNamedEpicPasses(t *testing.T) {
	t.Parallel()
	s := openRunStore(t)
	flowWakeCreate(t, s, flowWakeA, runPhase)
	flowWakeCreate(t, s, flowWakeB, runPhase)
	rig := newFlowWakeRig(t, s)
	rig.runner.Pass(context.Background())
	rig.start()
	rig.awaitPass()
	readsA, readsB := rig.reads.flowWakeReads(flowWakeA), rig.reads.flowWakeReads(flowWakeB)

	rig.runner.Wake([]string{flowWakeB})
	rig.awaitPass()
	rig.stop()
	if got := rig.reads.flowWakeReads(flowWakeA) - readsA; got != 0 {
		t.Errorf("reads of A's flow = %d, want none", got)
	}
	if got := rig.reads.flowWakeReads(flowWakeB) - readsB; got != 1 {
		t.Errorf("reads of B's flow = %d, want 1", got)
	}
	if got := rig.passCount.Load(); got != 2 {
		t.Errorf("passes = %d, want the start pass and one pass", got)
	}
}

// TestFlowWakeFreedReleaseSlotWakesWaiter: C reads the release slot of its repository held by H, and waits on it. A
// store write that frees H's slot wakes H, which wakes C with it, so C's flow is read again.
func TestFlowWakeFreedReleaseSlotWakesWaiter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	flowWakeCreate(t, s, flowWakeH, store.PhaseClean)
	flowWakeCreate(t, s, flowWakeC, runPhase)
	holder, err := s.TakeReleaseSlot(ctx, flowWakeH, runClock())
	if err != nil || holder != flowWakeH {
		t.Fatalf("TakeReleaseSlot = %q, %v, want the slot held by %s", holder, err, flowWakeH)
	}
	rig := newFlowWakeRig(t, s)
	rig.runner.Pass(ctx)
	rig.start()
	rig.awaitPass()
	readsC := rig.reads.flowWakeReads(flowWakeC)

	if err = s.FreeReleaseSlot(ctx, flowWakeH); err != nil {
		t.Fatalf("FreeReleaseSlot: %v", err)
	}
	rig.awaitPass()
	rig.stop()
	if got := rig.reads.flowWakeReads(flowWakeC) - readsC; got != 1 {
		t.Errorf("reads of C's flow = %d, want 1", got)
	}
	if got := rig.passCount.Load(); got != 2 {
		t.Errorf("passes = %d, want the start pass and one pass", got)
	}
}
