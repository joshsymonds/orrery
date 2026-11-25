package interpret

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/store"
)

// The design and contracts of the held-start tests. Each contract's epic has the contract's id. heldStartEpic starts
// after heldStartPredecessor, and after heldStartOther in the test that has two predecessors. heldStartCommit is the
// commit the fake origin main returns.
const (
	heldStartDesign      = "orrery-3c4d5e6/design-held-start"
	heldStartEpic        = "orrery-3c4d5e6/contract-held"
	heldStartPredecessor = "orrery-3c4d5e6/contract-predecessor"
	heldStartOther       = "orrery-3c4d5e6/contract-other"
	heldStartWriter      = "conversation-held-start"
	heldStartCommit      = "5f0c1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b"
)

// heldOrigin is the OriginMain of the held-start tests: it returns commit for every fetch, and counts the fetches.
type heldOrigin struct {
	commit  string
	fetches atomic.Int64
}

// OriginMain returns the commit the origin was given, and counts the call.
func (o *heldOrigin) OriginMain(_ context.Context, _ hostexec.Host, _ string) (string, error) {
	o.fetches.Add(1)
	return o.commit, nil
}

// heldRun is a Runner over a store that Run drives on its own goroutine. Passed reports each pass on passed, so
// awaitPass waits for the pass that has just run, and pass sends one tick and waits for the pass it starts.
type heldRun struct {
	ticks  chan time.Time
	passed chan struct{}
	done   chan struct{}
	cancel context.CancelFunc
}

// startHeldRun starts a Runner over s with origin as its OriginMain and waits for its start pass. With ticks set, the
// runner passes at start and once per tick; with ticks nil, it passes on the store's writes made after its start pass.
func startHeldRun(t *testing.T, s *store.Store, origin *heldOrigin, ticks chan time.Time) *heldRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	run := &heldRun{ticks: ticks, passed: make(chan struct{}), done: make(chan struct{}), cancel: cancel}
	runner := New(Config{
		Store: s, Planner: &fakePlanner{}, Builder: &fakeBuilder{}, Now: runClock, OriginMain: origin.OriginMain,
		Ticks: ticks,
		Passed: func() {
			select {
			case run.passed <- struct{}{}:
			case <-ctx.Done():
			}
		},
	})
	go func() {
		runner.Run(ctx)
		close(run.done)
	}()
	run.awaitPass()
	if ticks == nil {
		// The writes of the start pass are not wakes: the hook is set once that pass has reported, so only the writes the
		// test makes after it wake the runner.
		s.OnEpicsChanged(runner.Wake)
	}
	return run
}

// awaitPass waits for the pass that has just run to report.
func (h *heldRun) awaitPass() {
	<-h.passed
}

// pass sends one tick and waits for the pass it starts.
func (h *heldRun) pass() {
	h.ticks <- time.Time{}
	h.awaitPass()
}

// stop cancels the runner and waits for Run to return.
func (h *heldRun) stop() {
	h.cancel()
	<-h.done
}

// heldStartWorld stores the design of the held-start tests. Each predecessor is a contract of the design with an accepted
// epic, and heldStartEpic is a contract of the design that starts after every predecessor, so its epic is held: no
// predecessor has shipped.
func heldStartWorld(t *testing.T, s *store.Store, predecessors ...string) {
	t.Helper()
	ctx := context.Background()
	if err := s.CreateDesign(ctx, store.Design{
		ID: heldStartDesign, Title: "Held starts", Conversation: heldStartWriter, Created: runClock(),
	}); err != nil {
		t.Fatalf("CreateDesign: %v", err)
	}
	for _, id := range predecessors {
		heldStartAccepted(t, s, id)
		heldStartCreateEpic(t, s, id, runBase)
	}
	heldStartAccepted(t, s, heldStartEpic)
	for _, predecessor := range predecessors {
		if err := s.AddDesignEdge(ctx, heldStartDesign, heldStartEpic, predecessor, runClock()); err != nil {
			t.Fatalf("AddDesignEdge after %s: %v", predecessor, err)
		}
	}
	heldStartCreateEpic(t, s, heldStartEpic, "")
	if got := heldStartEpicRow(t, s).Phase; got != store.PhaseHeld {
		t.Fatalf("epic %s phase = %q, want %q: a predecessor has not shipped", heldStartEpic, got, store.PhaseHeld)
	}
}

// heldStartAccepted proposes the contract id into the held-start design, and accepts it as its owner.
func heldStartAccepted(t *testing.T, s *store.Store, id string) {
	t.Helper()
	ctx := context.Background()
	if err := s.ProposeContractInDesign(ctx, heldStartDesign, id, store.ContractVersion{
		Version: 1, Repository: runRepo, Title: "Contract " + id, Care: "serious", Body: "{}",
		Words: "Propose the contract", Writer: heldStartWriter, At: runClock().Unix(),
	}); err != nil {
		t.Fatalf("ProposeContractInDesign %s: %v", id, err)
	}
	if err := s.AcceptContract(ctx, id, store.ContractChange{
		Words: "Accept the contract", At: runClock().Unix() + 1,
	}); err != nil {
		t.Fatalf("AcceptContract %s: %v", id, err)
	}
}

// heldStartCreateEpic stores the epic of the contract id at base, through CreateContractEpic, which holds the epic when a
// predecessor has not shipped.
func heldStartCreateEpic(t *testing.T, s *store.Store, id, base string) {
	t.Helper()
	at := runClock()
	if err := s.CreateContractEpic(context.Background(), store.EpicRow{
		ID: id, Repository: runRepo, Base: base, Title: "Epic " + id, Care: "serious", Phase: runPhase,
		Created: at, Changed: at, PhaseAt: at, Workflow: planBuildPin(),
	}, 1); err != nil {
		t.Fatalf("CreateContractEpic %s: %v", id, err)
	}
}

// heldStartRelease moves the epic of id to released, as a release that ships it leaves it.
func heldStartRelease(t *testing.T, s *store.Store, id string) {
	t.Helper()
	if err := s.EndEpic(context.Background(), id, "released", "Released.", nil, runClock()); err != nil {
		t.Fatalf("EndEpic %s released: %v", id, err)
	}
}

// heldStartEpicRow returns the held-start epic as the store holds it.
func heldStartEpicRow(t *testing.T, s *store.Store) store.EpicRow {
	t.Helper()
	row, err := s.Epic(context.Background(), heldStartEpic)
	if err != nil {
		t.Fatalf("Epic %s: %v", heldStartEpic, err)
	}
	return row
}

// heldStartPlanRunning reports whether the plan run of epic is running.
func heldStartPlanRunning(t *testing.T, s *store.Store, epic string) bool {
	t.Helper()
	runs, err := s.NodeRuns(context.Background(), epic)
	if err != nil {
		t.Fatalf("NodeRuns %s: %v", epic, err)
	}
	for _, run := range runs {
		if run.Path == "plan" && run.State == "running" {
			return true
		}
	}
	return false
}

// TestDesignHeldEpicStartsFromOriginMain asserts that when the last predecessor's epic moves to released, the next pass
// stores the commit OriginMain returns as the held epic's base and stored base, in phase accepted, with a decision that
// names that commit, and that the epic's plan run starts.
func TestDesignHeldEpicStartsFromOriginMain(t *testing.T) {
	t.Parallel()
	s := openRunStore(t)
	heldStartWorld(t, s, heldStartPredecessor)
	origin := &heldOrigin{commit: heldStartCommit}
	run := startHeldRun(t, s, origin, make(chan time.Time))
	if n := origin.fetches.Load(); n != 0 {
		t.Fatalf("origin main fetches before the predecessor shipped = %d, want none", n)
	}

	heldStartRelease(t, s, heldStartPredecessor)
	run.pass()
	run.stop()

	row := heldStartEpicRow(t, s)
	if row.Base != heldStartCommit || row.StoredBase != heldStartCommit {
		t.Errorf("base = %q, stored base = %q, want both %q: the commit origin main returned", row.Base,
			row.StoredBase, heldStartCommit)
	}
	if row.Phase != runPhase {
		t.Errorf("epic phase = %q, want %q", row.Phase, runPhase)
	}
	if n := origin.fetches.Load(); n != 1 {
		t.Errorf("origin main fetches = %d, want 1: the pass that started the epic", n)
	}
	started := false
	for _, decision := range row.Decisions {
		if decision.ID == "held-start" && decision.Maker.By == store.MakerOrreryd &&
			strings.Contains(decision.Text, heldStartCommit) {
			started = true
		}
	}
	if !started {
		t.Errorf("decisions = %+v, want a held-start decision by orreryd that names %s", row.Decisions, heldStartCommit)
	}
	if !heldStartPlanRunning(t, s, heldStartEpic) {
		t.Errorf("plan run of epic %s is not running after it started", heldStartEpic)
	}
}

// TestDesignHeldEpicWaitsForLastPredecessor asserts that with two predecessors, one released keeps the epic held and
// starts nothing from origin main, and that the epic starts once the second predecessor is released too.
func TestDesignHeldEpicWaitsForLastPredecessor(t *testing.T) {
	t.Parallel()
	s := openRunStore(t)
	heldStartWorld(t, s, heldStartPredecessor, heldStartOther)
	origin := &heldOrigin{commit: heldStartCommit}
	run := startHeldRun(t, s, origin, make(chan time.Time))

	heldStartRelease(t, s, heldStartPredecessor)
	run.pass()
	if got := heldStartEpicRow(t, s).Phase; got != store.PhaseHeld {
		t.Errorf("epic phase = %q with one of two predecessors released, want %q", got, store.PhaseHeld)
	}
	if n := origin.fetches.Load(); n != 0 {
		t.Errorf("origin main fetches = %d with one predecessor unshipped, want none", n)
	}

	heldStartRelease(t, s, heldStartOther)
	run.pass()
	run.stop()
	if row := heldStartEpicRow(t, s); row.Phase != runPhase || row.Base != heldStartCommit {
		t.Errorf("epic phase = %q, base = %q once both predecessors shipped, want %q and %q",
			row.Phase, row.Base, runPhase, heldStartCommit)
	}
}

// TestDesignHeldEpicHoldSurvivesRestart asserts that a Runner started over a store whose held epic's predecessor has not
// shipped still holds the epic, and fetches nothing.
func TestDesignHeldEpicHoldSurvivesRestart(t *testing.T) {
	t.Parallel()
	s := openRunStore(t)
	heldStartWorld(t, s, heldStartPredecessor)
	origin := &heldOrigin{commit: heldStartCommit}
	startHeldRun(t, s, origin, make(chan time.Time)).stop()

	restarted := startHeldRun(t, s, origin, make(chan time.Time))
	restarted.stop()
	if got := heldStartEpicRow(t, s); got.Phase != store.PhaseHeld || got.Base != "" {
		t.Errorf("epic phase = %q, base = %q after a restart, want %q with no base",
			got.Phase, got.Base, store.PhaseHeld)
	}
	if n := origin.fetches.Load(); n != 0 {
		t.Errorf("origin main fetches after a restart = %d, want none", n)
	}
}

// TestDesignHeldEpicStartSurvivesRestart asserts that a predecessor released while no Runner ran is seen by the first
// pass of a new Runner, which starts the held epic from origin main.
func TestDesignHeldEpicStartSurvivesRestart(t *testing.T) {
	t.Parallel()
	s := openRunStore(t)
	heldStartWorld(t, s, heldStartPredecessor)
	origin := &heldOrigin{commit: heldStartCommit}
	startHeldRun(t, s, origin, make(chan time.Time)).stop()
	heldStartRelease(t, s, heldStartPredecessor)

	restarted := startHeldRun(t, s, origin, make(chan time.Time))
	restarted.stop()
	if row := heldStartEpicRow(t, s); row.Phase != runPhase || row.Base != heldStartCommit {
		t.Errorf("epic phase = %q, base = %q after the restart's first pass, want %q and %q",
			row.Phase, row.Base, runPhase, heldStartCommit)
	}
}

// TestDesignHeldEpicStartsOnClosedWake asserts that a wake that names only a closed epic still runs the held pass: the
// predecessor closes as shipped, and the write that closes it wakes the runner for that epic alone, so the held epic
// starts from origin main on the pass that wake gives.
func TestDesignHeldEpicStartsOnClosedWake(t *testing.T) {
	t.Parallel()
	s := openRunStore(t)
	heldStartWorld(t, s, heldStartPredecessor)
	origin := &heldOrigin{commit: heldStartCommit}
	run := startHeldRun(t, s, origin, nil)

	if err := s.EndEpic(
		context.Background(), heldStartPredecessor, store.PhaseClosedShipped, "Shipped.", nil, runClock(),
	); err != nil {
		t.Fatalf("EndEpic %s closed shipped: %v", heldStartPredecessor, err)
	}
	run.awaitPass()
	run.stop()
	if row := heldStartEpicRow(t, s); row.Phase != runPhase || row.Base != heldStartCommit {
		t.Errorf("epic phase = %q, base = %q after the closed wake, want %q and %q",
			row.Phase, row.Base, runPhase, heldStartCommit)
	}
}
