package enginehost

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
)

// agentGateLimitStartUnix is the clock reading, in unix seconds, an agent gate limit test's run starts at.
const agentGateLimitStartUnix = 1_800_000_000

// limitClock is a clock a test moves by hand.
type limitClock struct{ at atomic.Int64 }

func (c *limitClock) now() time.Time { return time.Unix(c.at.Load(), 0) }

func (c *limitClock) set(at time.Time) { c.at.Store(at.Unix()) }

// newAgentGateLimitFixture returns a limit fixture whose run is an agent gate step with a one-minute limit, started at
// agentGateLimitStartUnix on clock, with its deadline timers the returned fake.
func newAgentGateLimitFixture(t *testing.T, clock *limitClock) (*stepLoopFixture, *stepDeadlineTimers) {
	t.Helper()
	clock.set(time.Unix(agentGateLimitStartUnix, 0))
	f := newStepLimitFixture(t)
	timers := &stepDeadlineTimers{}
	f.run.newTimer = timers.newTimer
	f.run.now = clock.now
	f.run.started = time.Unix(agentGateLimitStartUnix, 0)
	f.run.agentGate = &gates.Gate{Name: evidenceJudgeGate, Agent: "judge", Limit: time.Minute}
	return f, timers
}

// tickRun fires the fixture's armed deadline and waits until the loop has taken it and handled it. The run must have a
// deadline armed.
func tickRun(t *testing.T, f *stepLoopFixture, timers *stepDeadlineTimers) {
	t.Helper()
	stepDeadlineSettle(t, f.run)
	if !timers.fire(time.Time{}) {
		t.Fatal("no deadline armed for the run")
	}
	stepDeadlineSettle(t, f.run)
}

// TestStepLimitResumedAgentGateCountsItsLimitFromTheResume covers O3d1's rule for an agent gate step, such as an
// evidence judge: a resume an hour after the limit, far past the gate's one-minute limit, leaves the step open, and
// stores the resume as the time the gate's limit counts from. A check a minute after the resume ends it overtime.
func TestStepLimitResumedAgentGateCountsItsLimitFromTheResume(t *testing.T) {
	t.Parallel()
	clock := &limitClock{}
	f, timers := newAgentGateLimitFixture(t, clock)
	f.start(t)
	limitTurn(f.run)
	f.awaitLimited(t)

	resumed := time.Unix(agentGateLimitStartUnix, 0).Add(time.Hour)
	clock.set(resumed)
	if err := f.run.do(t.Context(), f.run.resumeLimited); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	tickRun(t, f, timers)
	row := f.row(t)
	if row.End != "" {
		t.Fatalf("End = %q, reason %q after a check right after the resume, want open", row.End, row.Reason)
	}
	if !row.LoopGateFrom.Equal(resumed) {
		t.Fatalf("LoopGateFrom = %v, want the resume at %v", row.LoopGateFrom, resumed)
	}

	clock.set(resumed.Add(time.Minute))
	tickRun(t, f, timers)
	row = f.awaitEnded(t)
	if row.End != string(watchEndOvertime) {
		t.Fatalf("End = %q a minute after the resume, want %q", row.End, watchEndOvertime)
	}
}

// TestStepLimitRestoredAgentGateCountsItsLimitFromTheStoredResume covers the same rule across a restart: a run
// restored from a row whose gate limit counts from a resume half a minute ago is not ended by a check, though its start
// is an hour back.
func TestStepLimitRestoredAgentGateCountsItsLimitFromTheStoredResume(t *testing.T) {
	t.Parallel()
	clock := &limitClock{}
	f, timers := newAgentGateLimitFixture(t, clock)
	resumed := time.Unix(agentGateLimitStartUnix, 0).Add(time.Hour)
	row := f.commit(t, store.StepLoop{ID: stepLoopID, Round: 1, GateFrom: resumed})
	clock.set(resumed.Add(30 * time.Second))
	// The restored turn counts as running, so the step gets a model limit its watch does not reach here.
	f.run.modelLimit = time.Hour
	f.resumeFrom(stepRestore{Row: row})
	f.run.started = time.Unix(agentGateLimitStartUnix, 0)
	f.start(t)

	tickRun(t, f, timers)
	if row = f.row(t); row.End != "" {
		t.Fatalf("End = %q, reason %q after a check half a minute after the stored resume, want open", row.End,
			row.Reason)
	}
}
