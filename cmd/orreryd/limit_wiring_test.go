package main

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic values only. The step ids, the repository and the usage resets are invented. A limited step is restored
// as limited and runs no session, so no agent is started.
const (
	limitWiringHost   = "synthetic-host"
	limitWiringRepo   = "/work/synthetic-repo"
	limitWiringBefore = "limit-before"
	limitWiringAfter  = "limit-after"
	limitWiringStamp  = 1_700_000_000
)

// limitWiringReset is the usage reset of the step limited before start, and limitWiringLaterReset the reset of the step
// limited after start, so the two steps are in different limit groups.
func limitWiringReset() time.Time {
	return time.Date(2026, 11, 15, 17, 0, 0, 0, time.UTC)
}

func limitWiringLaterReset() time.Time {
	return time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
}

// recordingRouter is the router a limit test hands startSessions. It keeps the notices it is given. The limit notices
// call it from their own goroutine, so the notices are guarded by a lock.
type recordingRouter struct {
	mu      sync.Mutex
	notices []route.Notice
}

// Notify keeps n.
func (r *recordingRouter) Notify(_ context.Context, n route.Notice) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notices = append(r.notices, n)
}

// Changed does nothing: the limit notices do not report pane changes.
func (r *recordingRouter) Changed(notice.Key) {}

// Seen does nothing: these tests never end a limit.
func (r *recordingRouter) Seen(context.Context, notice.Key, int64) {}

// received returns a copy of the notices kept so far.
func (r *recordingRouter) received() []route.Notice {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]route.Notice(nil), r.notices...)
}

// holdingRouter is a recordingRouter whose Notify returns only once release is closed. Its first Notify closes entered
// before it waits, and closes canceled if its context ends while it waits: the limit notices route with their loop's
// context, which stop cancels just before it waits for the loop.
type holdingRouter struct {
	recordingRouter
	release      chan struct{}
	entered      chan struct{}
	canceled     chan struct{}
	enteredOnce  sync.Once
	canceledOnce sync.Once
}

// newHoldingRouter returns a holdingRouter whose channels are open.
func newHoldingRouter() *holdingRouter {
	return &holdingRouter{
		release: make(chan struct{}), entered: make(chan struct{}), canceled: make(chan struct{}),
	}
}

// Notify waits for release, then keeps n.
func (r *holdingRouter) Notify(ctx context.Context, n route.Notice) {
	r.enteredOnce.Do(func() { close(r.entered) })
	select {
	case <-ctx.Done():
		r.canceledOnce.Do(func() { close(r.canceled) })
	case <-r.release:
	}
	<-r.release
	r.recordingRouter.Notify(ctx, n)
}

// openLimitStore opens a new store in fresh XDG directories. It returns the store and the environment that names it.
func openLimitStore(t *testing.T) (*store.Store, func(string) string) {
	t.Helper()
	env := map[string]string{
		"XDG_STATE_HOME":  t.TempDir(),
		"XDG_CONFIG_HOME": t.TempDir(),
	}
	getenv := func(key string) string { return env[key] }
	path, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store path: %v", err)
	}
	return openPaneStore(t, path), getenv
}

// limitHost is a --sessions host whose limit notices check at start and then only when the test ticks them.
type limitHost struct {
	*sessionHost
	ticks chan time.Time
}

// startLimitHost starts a --sessions host over st that routes its notices to router, with its limit notices on ticks
// the test delivers.
func startLimitHost(t *testing.T, st *store.Store, getenv func(string) string, router enginehost.Router) limitHost {
	t.Helper()
	ticks := make(chan time.Time)
	opts := options{
		socket:     filepath.Join(t.TempDir(), "orreryd.sock"),
		sessionCap: defaultSessionCap,
		stepCap:    defaultStepCap,
		limitTicks: ticks,
		// The steps' resets are fixed dates, so the due pass reads a clock before them and resumes none, whatever
		// the real date is.
		dueNow: func() time.Time { return time.Unix(limitWiringStamp, 0) },
	}
	host, err := startSessions(
		t.Context(), opts, limitWiringHost, st, router, nil, slog.New(slog.DiscardHandler), getenv,
	)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}
	return limitHost{sessionHost: host, ticks: ticks}
}

// tick delivers one tick to the limit notices. It returns once their loop has taken it, so every check before it,
// the check at start among them, has finished; the check the tick starts may still run.
func (h limitHost) tick() {
	h.ticks <- time.Time{}
}

// checked returns once a whole check that started after the call has finished: the first tick starts it, and the loop
// takes the second only when it is done.
func (h limitHost) checked() {
	h.tick()
	h.tick()
}

// limitWiringKey is the key of the usage limit that resets at resets, as the limit notices and their marks name it.
func limitWiringKey(resets time.Time) string {
	return fmt.Sprintf("usage@%d", resets.Unix())
}

// addLimitedStep stores an open step row whose loop is limited by usage until resets, as a usage limit leaves it.
func addLimitedStep(t *testing.T, st *store.Store, id string, resets time.Time) {
	t.Helper()
	if err := st.ApplyEngine(t.Context(), []store.EngineOp{
		store.StepAdd{
			ID: id, Agent: "implementer", Prompt: "synthetic prompt", Schema: "{}", Workspace: limitWiringRepo,
			ReadOnly: true, DoneValue: "done",
		},
		store.StepLoop{ID: id, Limited: true, LimitKind: "usage", LimitResets: resets},
	}, time.Unix(limitWiringStamp, 0)); err != nil {
		t.Fatalf("add limited step %s: %v", id, err)
	}
}

// TestSessionsHostTellsStepLimitedBeforeStartOnce checks that a step a usage limit holds when orreryd starts is told
// once, and marked as told in the store, so a restart does not tell it again. It then stops without error.
func TestSessionsHostTellsStepLimitedBeforeStartOnce(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, getenv := openLimitStore(t)
	addLimitedStep(t, st, limitWiringBefore, limitWiringReset())

	router := &recordingRouter{}
	host := startLimitHost(t, st, getenv, router)
	host.tick()
	got := router.received()
	if len(got) != 1 {
		t.Fatalf("got %d notices for the step limited before start, want 1", len(got))
	}
	desk, err := got[0].Desk(ctx)
	if err != nil {
		t.Fatalf("desk notice: %v", err)
	}
	const want = "1 step held by the usage limit, reset 2026-11-15T17:00:00Z: " + limitWiringBefore
	if desk.Body != want {
		t.Errorf("notice body = %q, want %q", desk.Body, want)
	}

	host.checked()
	if got := router.received(); len(got) != 1 {
		t.Fatalf("got %d notices after a tick, want 1: the told step is not told again", len(got))
	}
	marks, err := st.ToldLimits(ctx)
	if err != nil {
		t.Fatalf("read told limits: %v", err)
	}
	if steps := marks[limitWiringKey(limitWiringReset())]; !slices.Equal(steps, []string{limitWiringBefore}) {
		t.Fatalf("marked steps = %v, want [%s]", steps, limitWiringBefore)
	}

	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// TestSessionsHostTellsNothingAfterRestartForStepTold checks that a step told before a restart is not told again when
// orreryd starts over the same store.
func TestSessionsHostTellsNothingAfterRestartForStepTold(t *testing.T) {
	t.Parallel()
	st, getenv := openLimitStore(t)
	addLimitedStep(t, st, limitWiringBefore, limitWiringReset())

	first := &recordingRouter{}
	host := startLimitHost(t, st, getenv, first)
	host.tick()
	if got := first.received(); len(got) != 1 {
		t.Fatalf("got %d notices before the restart, want 1", len(got))
	}
	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}

	second := &recordingRouter{}
	host = startLimitHost(t, st, getenv, second)
	host.checked()
	if got := second.received(); len(got) != 0 {
		t.Fatalf("got %d notices after the restart, want none: the step was told before it", len(got))
	}
	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions after restart: %v", err)
	}
}

// TestSessionsHostTellsAStepLimitedAgainAfterItsResume checks that a step told on a limit, resumed at its reset as the
// due pass resumes it, and then limited again on a later reset is told again, and that a restart after that tells
// nothing: the resume's loop state empties the first limit, whose mark is forgotten, and the second limit is marked.
func TestSessionsHostTellsAStepLimitedAgainAfterItsResume(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, getenv := openLimitStore(t)
	addLimitedStep(t, st, limitWiringBefore, limitWiringReset())

	first := &recordingRouter{}
	host := startLimitHost(t, st, getenv, first)
	host.tick()
	if got := first.received(); len(got) != 1 {
		t.Fatalf("got %d notices before the resume, want 1", len(got))
	}
	// A resume stores the step's loop state with no limit and closes its due rows, as resumeWith writes it.
	if err := st.ApplyEngine(ctx, []store.EngineOp{
		store.StepDueResumesClosed{ID: limitWiringBefore},
		store.StepLoop{ID: limitWiringBefore},
	}, time.Unix(limitWiringStamp+1, 0)); err != nil {
		t.Fatalf("resume step: %v", err)
	}
	// A check sees the resume, so the first limit empties and its mark is forgotten.
	host.checked()
	if err := st.ApplyEngine(ctx, []store.EngineOp{
		store.StepLoop{ID: limitWiringBefore, Limited: true, LimitKind: "usage", LimitResets: limitWiringLaterReset()},
	}, time.Unix(limitWiringStamp+2, 0)); err != nil {
		t.Fatalf("limit step again: %v", err)
	}
	host.checked()
	got := first.received()
	if len(got) != 2 {
		t.Fatalf("got %d notices after the step was limited again, want 2", len(got))
	}
	desk, err := got[1].Desk(ctx)
	if err != nil {
		t.Fatalf("desk notice: %v", err)
	}
	const want = "1 step held by the usage limit, reset 2026-11-16T09:00:00Z: " + limitWiringBefore
	if desk.Body != want {
		t.Errorf("notice body after the resume = %q, want %q", desk.Body, want)
	}
	// The check that routed the notice wrote the limit's mark after it (see LimitNotices.update), and checked returned
	// only once that check had finished.
	marks, err := st.ToldLimits(ctx)
	if err != nil {
		t.Fatalf("read told limits: %v", err)
	}
	if wantMarks := map[string][]string{limitWiringKey(limitWiringLaterReset()): {limitWiringBefore}}; !maps.EqualFunc(
		marks, wantMarks, slices.Equal[[]string]) {
		t.Fatalf("told limits = %v, want %v", marks, wantMarks)
	}
	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}

	second := &recordingRouter{}
	host = startLimitHost(t, st, getenv, second)
	host.checked()
	if got := second.received(); len(got) != 0 {
		t.Fatalf("got %d notices after the restart, want none: the second limit was told before it", len(got))
	}
	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions after restart: %v", err)
	}
}

// TestSessionsHostSendsNothingForStepMarkedBeforeStart checks that a step a usage limit holds when orreryd starts gets
// no notice when the store already marks it as told.
func TestSessionsHostSendsNothingForStepMarkedBeforeStart(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, getenv := openLimitStore(t)
	addLimitedStep(t, st, limitWiringBefore, limitWiringReset())
	if err := st.TellLimit(ctx, limitWiringKey(limitWiringReset()), []string{limitWiringBefore}); err != nil {
		t.Fatalf("mark limit told: %v", err)
	}

	router := &recordingRouter{}
	host := startLimitHost(t, st, getenv, router)
	host.checked()
	if got := router.received(); len(got) != 0 {
		t.Fatalf("got %d notices, want none for a step marked as told", len(got))
	}
	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// TestSessionsHostRoutesLimitNoticeForStepLimitedAfterStart checks that a --sessions orreryd routes one limit notice for
// a step a usage limit holds when the step is limited after start. The step limited before start is marked as told, so
// it gets no notice. The host then stops without error.
func TestSessionsHostRoutesLimitNoticeForStepLimitedAfterStart(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, getenv := openLimitStore(t)
	addLimitedStep(t, st, limitWiringBefore, limitWiringReset())
	if err := st.TellLimit(ctx, limitWiringKey(limitWiringReset()), []string{limitWiringBefore}); err != nil {
		t.Fatalf("mark limit told: %v", err)
	}

	router := &recordingRouter{}
	host := startLimitHost(t, st, getenv, router)

	before, err := st.Step(ctx, limitWiringBefore)
	if err != nil {
		t.Fatalf("read step %s: %v", limitWiringBefore, err)
	}
	if !before.LoopLimited || before.End != "" {
		t.Fatalf("step %s limited = %v, end = %q, want still limited and open",
			limitWiringBefore, before.LoopLimited, before.End)
	}

	host.checked()
	if got := router.received(); len(got) != 0 {
		t.Fatalf("got %d notices before the new limit, want none: a limit marked as told is not told again", len(got))
	}

	addLimitedStep(t, st, limitWiringAfter, limitWiringLaterReset())
	host.checked()
	got := router.received()
	if len(got) != 1 {
		t.Fatalf("got %d notices after the new limit, want 1", len(got))
	}
	desk, err := got[0].Desk(ctx)
	if err != nil {
		t.Fatalf("desk notice: %v", err)
	}
	const want = "1 step held by the usage limit, reset 2026-11-16T09:00:00Z: " + limitWiringAfter
	if desk.Body != want {
		t.Errorf("notice body = %q, want %q", desk.Body, want)
	}

	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// TestSessionsHostStopWaitsForLimitNotices checks that stop does not return while a limit notice is still routing, so
// the router is closed only after the notice has routed.
func TestSessionsHostStopWaitsForLimitNotices(t *testing.T) {
	t.Parallel()
	st, getenv := openLimitStore(t)
	router := newHoldingRouter()
	host := startLimitHost(t, st, getenv, router)

	// The check at start finds no limited step, so it routes nothing. A later check routes the new limit's notice,
	// which holds in Notify until release. Ticks go in until it does: a check already running may route it, and the
	// loop takes no tick while it holds.
	host.tick()
	addLimitedStep(t, st, limitWiringAfter, limitWiringLaterReset())
	for routed := false; !routed; {
		select {
		case <-router.entered:
			routed = true
		case host.ticks <- time.Time{}:
		}
	}

	// Stop cancels the limit notices' context and then waits for their loop, which is held in Notify. Once the held
	// Notify sees the cancel, stop is at that wait, so it must not have returned.
	stopped := make(chan error, 1)
	go func() { stopped <- host.stop() }()
	<-router.canceled
	select {
	case err := <-stopped:
		close(router.release)
		t.Fatalf("stop returned (%v) while a notice was still routing", err)
	default:
	}

	close(router.release)
	if err := <-stopped; err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
	select {
	case <-host.limitsDone:
	default:
		t.Fatal("limit notices were still running when stop returned")
	}
}
