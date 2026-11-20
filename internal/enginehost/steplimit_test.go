package enginehost

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestStepsStartStoresModelLimit covers R1: the StartStep limit on model time is accepted and stored, and zero means
// the 60 minute default. Both writing and read-only starts store the resolved limit on their step row.
func TestStepsStartStoresModelLimit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		limit    time.Duration
		want     time.Duration
		readOnly bool
	}{
		{name: "writing zero is the default", limit: 0, want: 60 * time.Minute},
		{name: "writing 90 seconds is stored", limit: 90 * time.Second, want: 90 * time.Second},
		{name: "read-only zero is the default", limit: 0, want: 60 * time.Minute, readOnly: true},
		{name: "read-only 90 seconds is stored", limit: 90 * time.Second, want: 90 * time.Second, readOnly: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newStepStartFixture(t)
			repo := stepGitTestRepo(t)
			var start StepStart
			if tc.readOnly {
				revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
				start = stepStartReadOnly(repo, revision)
			} else {
				start = stepStartWriting(stepWorktreeTestAddLinked(t, repo))
			}
			start.ModelLimit = tc.limit

			id, err := f.steps.Start(t.Context(), start)
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			row, err := f.steps.Status(t.Context(), id)
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if row.ModelLimit != tc.want {
				t.Fatalf("ModelLimit = %v, want %v", row.ModelLimit, tc.want)
			}
		})
	}
}

// stepLimitSessions is a stepLoopSessions whose Release returns releaseErr.
type stepLimitSessions struct {
	*stepLoopSessions
	releaseErr error
}

func (f stepLimitSessions) Release(context.Context, string) error {
	return f.releaseErr
}

// rateLimitReadFails is a store whose StepRateLimit fails.
type rateLimitReadFails struct{ *store.Store }

func (rateLimitReadFails) StepRateLimit(context.Context, string) (store.RateLimit, bool, error) {
	return store.RateLimit{}, false, errors.New("rate limit table unreadable")
}

// newStepLimitFixture returns a step loop fixture whose run reads its rate limits from the fixture's store and releases
// its session through a stepLimitSessions with no release error. A test that needs another store or sessions sets them
// before it starts the run.
func newStepLimitFixture(t *testing.T) *stepLoopFixture {
	t.Helper()
	f := newStepLoopFixture(t, nil)
	f.run.store = f.store
	f.run.sessions = stepLimitSessions{stepLoopSessions: f.sessions}
	return f
}

// limitTurn hands the run a turn that ended on a usage limit: the API error its assistant message carries, then the turn
// end.
func limitTurn(run *stepRun) {
	run.Event(session.AssistantError{Error: "rate_limit"})
	run.Return(session.TurnEnded{IsError: true})
}

// awaitLimited waits until the fixture's step row is limited, and returns it.
func (f *stepLoopFixture) awaitLimited(t *testing.T) store.StepRow {
	t.Helper()
	var row store.StepRow
	waitUntil(t, engineOf(f.run), "step limited", func() bool {
		row = f.row(t)
		return row.LoopLimited
	})
	return row
}

// awaitEnded waits until the fixture's step row has an end, and returns it.
func (f *stepLoopFixture) awaitEnded(t *testing.T) store.StepRow {
	t.Helper()
	var row store.StepRow
	waitUntil(t, engineOf(f.run), "step ended", func() bool {
		row = f.row(t)
		return row.End != ""
	})
	return row
}

// stepRowOf returns the step row id of steps holds.
func stepRowOf(t *testing.T, steps *Steps, id string) store.StepRow {
	t.Helper()
	row, err := steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status of step %s: %v", id, err)
	}
	return row
}

// startLimitProbe starts a read-only step of repo at revision whose first prompt is prompt, in rig.
func startLimitProbe(t *testing.T, rig stepsRig, repo, revision, prompt string) string {
	t.Helper()
	start := stepStartReadOnly(repo, revision)
	start.Prompt = prompt
	id, err := rig.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start of a %s step: %v", prompt, err)
	}
	return id
}

// TestStepLimitSevenDayHoldsStepAndFreesSlot covers R1 for a seven-day limit: the step stays open and limited with the
// kind and reset the rejected rate limit gave, it spends no round or continue, its fake claude is gone, and a second step
// starts under the step cap of one.
func TestStepLimitSevenDayHoldsStepAndFreesSlot(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "LIMIT")
	pid := fakePID(t, filepath.Join(rig.worktrees, id))

	var row store.StepRow
	waitUntil(t, engineOf(rig.manager, rig.steps), "step limited", func() bool {
		row = stepRowOf(t, rig.steps, id)
		return row.LoopLimited
	})
	if row.End != "" {
		t.Fatalf("End = %q, want the step open", row.End)
	}
	if row.LoopLimitKind != "seven_day" {
		t.Fatalf("LoopLimitKind = %q, want seven_day", row.LoopLimitKind)
	}
	if got := row.LoopLimitResets.Unix(); got != 1900000000 {
		t.Fatalf("LoopLimitResets = %d, want 1900000000", got)
	}
	if row.LoopRound != 0 || row.LoopContinues != 0 {
		t.Fatalf("round %d, continues %d, want 0 and 0 for a limited turn", row.LoopRound, row.LoopContinues)
	}
	// The limit is stored before the step's session is released, and the release frees the slot only once the
	// session's pump has ended, after its fake claude has exited. So the second Start waits for the slot itself.
	waitUntil(t, engineOf(rig.manager, rig.steps), "the limited step's slot to be freed", func() bool {
		return stepSlotsTaken(rig.manager) == 0
	})
	requireGone(t, pid)

	if _, err := rig.steps.Start(t.Context(), stepStartReadOnly(repo, revision)); err != nil {
		t.Fatalf("Start of a second step under step cap 1: %v", err)
	}
}

// stepSlotsTaken returns how many step slots m holds.
func stepSlotsTaken(m *Manager) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stepsRunning
}

// holdRelease is the stepSessions of a Manager whose Release, the one a limited turn makes, waits after signaling entered
// until release closes, and closes returned once the Manager's Release has returned.
type holdRelease struct {
	*Manager
	entered  chan struct{}
	release  chan struct{}
	returned chan struct{}
}

func (h *holdRelease) Release(ctx context.Context, id string) error {
	close(h.entered)
	<-h.release
	defer close(h.returned)
	return h.Manager.Release(ctx, id)
}

// A limited step holds its slot until its release has returned: the limit is stored first, so a Start between the
// stored limit and the release is refused at the step cap, and a Start once the release has returned is not. The test
// pauses the release to force a Start into that gap.
func TestStepLimitHoldsItsSlotUntilTheReleaseReturns(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	held := &holdRelease{
		Manager: rig.manager, entered: make(chan struct{}), release: make(chan struct{}), returned: make(chan struct{}),
	}
	releaseOnce := sync.OnceFunc(func() { close(held.release) })
	defer releaseOnce()
	steps := newRigSteps(t, rig, held, rig.manager.writer, rig.environ, slog.New(slog.DiscardHandler))
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	start := stepStartReadOnly(repo, revision)
	start.Prompt = "LIMIT"
	id, err := steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start of a LIMIT step: %v", err)
	}

	receive(t, held.entered)
	if row := stepRowOf(t, steps, id); !row.LoopLimited {
		t.Fatalf("step %s is not stored limited when its release starts", id)
	}
	if _, err = steps.Start(t.Context(), stepStartReadOnly(repo, revision)); !errors.Is(err, ErrCapReached) {
		t.Fatalf("Start while the limited step's release waits = %v, want ErrCapReached: its slot is still held", err)
	}
	releaseOnce()
	receive(t, held.returned)
	if _, err = steps.Start(t.Context(), stepStartReadOnly(repo, revision)); err != nil {
		t.Fatalf("Start once the limited step's release returned: %v", err)
	}
}

// TestStepLimitWithoutResetHoldsStepWithNoKind covers R1 for a limit whose turn carries the error but no rejected rate
// limit: the step is limited with an empty kind and a zero reset.
func TestStepLimitWithoutResetHoldsStepWithNoKind(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "LIMITNORESET")

	var row store.StepRow
	waitUntil(t, engineOf(rig.manager, rig.steps), "step limited", func() bool {
		row = stepRowOf(t, rig.steps, id)
		return row.LoopLimited
	})
	if row.LoopLimitKind != "" {
		t.Fatalf("LoopLimitKind = %q, want empty", row.LoopLimitKind)
	}
	if !row.LoopLimitResets.IsZero() {
		t.Fatalf("LoopLimitResets = %v, want zero", row.LoopLimitResets)
	}
	if row.End != "" {
		t.Fatalf("End = %q, want the step open", row.End)
	}
}

// TestStepErrorWithoutLimitIsNotLimited covers R1 for an error turn that is no limit: the step is not limited or held,
// and ends failed as it does for any exit.
func TestStepErrorWithoutLimitIsNotLimited(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "DIE")

	var row store.StepRow
	waitUntil(t, engineOf(rig.manager, rig.steps), "step ended", func() bool {
		row = stepRowOf(t, rig.steps, id)
		return row.End != ""
	})
	if row.End != stepLoopEndFailed {
		t.Fatalf("End = %q, want %q", row.End, stepLoopEndFailed)
	}
	if row.LoopLimited || row.LoopHeld {
		t.Fatalf("limited %t, held %t, want neither", row.LoopLimited, row.LoopHeld)
	}
}

// TestStepLimitDropsHoldAndNudgesAndSpendsNothing covers R1: a limited turn drops the hold and both nudges pending for
// it, stores that state, and sends no continue, so it spends no round.
func TestStepLimitDropsHoldAndNudgesAndSpendsNothing(t *testing.T) {
	t.Parallel()
	f := newStepLimitFixture(t)
	f.run.held.Store(true)
	f.run.nudge = "watchdog nudge"
	f.run.ownerNudge = "owner nudge"
	f.start(t)

	limitTurn(f.run)
	row := f.awaitLimited(t)
	if row.LoopHeld || row.LoopNudge != "" || row.LoopOwnerNudge != "" {
		t.Fatalf("held %t, nudge %q, owner nudge %q, want the hold and both nudges dropped",
			row.LoopHeld, row.LoopNudge, row.LoopOwnerNudge)
	}
	if row.LoopRound != 0 || row.LoopContinues != 0 || row.End != "" {
		t.Fatalf("round %d, continues %d, end %q, want 0, 0 and open", row.LoopRound, row.LoopContinues, row.End)
	}
	f.sessions.mu.Lock()
	continued := len(f.sessions.continued)
	f.sessions.mu.Unlock()
	if continued != 0 {
		t.Fatalf("continues sent = %d, want none for a limited turn", continued)
	}
}

// TestStepLimitedRunIgnoresExit covers R1: a limited run ignores the exit of its released process and stays open.
func TestStepLimitedRunIgnoresExit(t *testing.T) {
	t.Parallel()
	f := newStepLimitFixture(t)
	f.start(t)

	limitTurn(f.run)
	f.awaitLimited(t)
	f.run.Exited("claude died")
	waitUntil(t, engineOf(f.run), "the exit taken", func() bool { return len(f.run.exits) == 0 })
	if err := f.run.do(t.Context(), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("owner action on a limited run after its exit: %v, want the run still running", err)
	}
	if row := f.row(t); row.End != "" {
		t.Fatalf("End = %q after an exit of a limited run, want open", row.End)
	}
}

// TestStepLimitedRunSkipsTicks covers R1: a limited run arms no deadline, so the agent gate's limit, passed by the
// clock's reading, ends nothing.
func TestStepLimitedRunSkipsTicks(t *testing.T) {
	t.Parallel()
	clock := &watchLoopClock{at: watchEpoch()}
	timers := &stepDeadlineTimers{}
	f := newStepLimitFixture(t)
	f.run.newTimer = timers.newTimer
	f.run.now = clock.now
	f.run.started = watchEpoch()
	f.run.agentGate = &gates.Gate{Name: "reviewer", Agent: "reviewer", Limit: time.Minute}
	f.start(t)

	limitTurn(f.run)
	f.awaitLimited(t)
	clock.set(time.Hour)
	if err := f.run.do(t.Context(), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("owner action after the gate limit passed a limited run: %v, want the run still running", err)
	}
	if _, armed := timers.armedDuration(); armed {
		t.Fatal("limited run has a deadline armed, want none")
	}
	if row := f.row(t); row.End != "" {
		t.Fatalf("End = %q after the gate limit passed a limited run, want open", row.End)
	}
}

// TestStepLimitStoreReadErrorEndsFailed covers R1: a store read of the rate limit that fails ends the step failed, and
// names the read.
func TestStepLimitStoreReadErrorEndsFailed(t *testing.T) {
	t.Parallel()
	f := newStepLimitFixture(t)
	f.run.store = rateLimitReadFails{Store: f.store}
	f.start(t)

	f.run.Return(session.TurnEnded{IsError: true})
	row := f.awaitEnded(t)
	if row.End != stepLoopEndFailed || !strings.Contains(row.Reason, "read rate limit") {
		t.Fatalf("End = %q, reason %q, want failed naming the rate limit read", row.End, row.Reason)
	}
}

// TestStepLimitReleaseErrorEndsFailed covers R1: a Release that fails after a limit is committed ends the step failed,
// and names the release.
func TestStepLimitReleaseErrorEndsFailed(t *testing.T) {
	t.Parallel()
	f := newStepLimitFixture(t)
	f.run.sessions = stepLimitSessions{stepLoopSessions: f.sessions, releaseErr: errors.New("release refused")}
	f.start(t)

	limitTurn(f.run)
	row := f.awaitEnded(t)
	if row.End != stepLoopEndFailed || !strings.Contains(row.Reason, "release step") {
		t.Fatalf("End = %q, reason %q, want failed naming the release", row.End, row.Reason)
	}
}
