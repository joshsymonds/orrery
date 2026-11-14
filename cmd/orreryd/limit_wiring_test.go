package main

import (
	"context"
	"log/slog"
	"path/filepath"
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
	limitWiringWait   = 2 * limitTick
	limitWiringPoll   = 10 * time.Millisecond
	// limitWiringHold is how long stop is given to return while a notice is still routing. A stop that waited for the
	// notice would not return within it.
	limitWiringHold = 200 * time.Millisecond
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
// before it waits.
type holdingRouter struct {
	recordingRouter
	release chan struct{}
	entered chan struct{}
	once    sync.Once
}

// Notify waits for release, then keeps n.
func (r *holdingRouter) Notify(ctx context.Context, n route.Notice) {
	r.once.Do(func() { close(r.entered) })
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

// startLimitHost starts a --sessions host over st that routes its notices to router.
func startLimitHost(t *testing.T, st *store.Store, getenv func(string) string, router enginehost.Router) *sessionHost {
	t.Helper()
	opts := options{
		socket:     filepath.Join(t.TempDir(), "orreryd.sock"),
		sessionCap: defaultSessionCap,
		stepCap:    defaultStepCap,
	}
	host, err := startSessions(t.Context(), opts, limitWiringHost, st, router, slog.New(slog.DiscardHandler), getenv)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}
	return host
}

// pastStartCheck waits out one tick. The check at start runs before the first tick, so once the tick has passed, the
// check at start is done and a step limited after it is not remembered by it.
func pastStartCheck() {
	time.Sleep(limitTick + time.Second)
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

// waitNotices reads router's notices until there are n of them or limitWiringWait has passed, and returns the last read.
func waitNotices(t *testing.T, router *recordingRouter, n int) []route.Notice {
	t.Helper()
	deadline := time.Now().Add(limitWiringWait)
	for {
		got := router.received()
		if len(got) >= n || time.Now().After(deadline) {
			return got
		}
		time.Sleep(limitWiringPoll)
	}
}

// TestSessionsHostRoutesLimitNoticeForStepLimitedAfterStart checks that a --sessions orreryd routes one limit notice for
// a step a usage limit holds when the step is limited after start. A step limited before start is remembered, so it
// gets no notice. The host then stops without error.
func TestSessionsHostRoutesLimitNoticeForStepLimitedAfterStart(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, getenv := openLimitStore(t)
	addLimitedStep(t, st, limitWiringBefore, limitWiringReset())

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

	pastStartCheck()
	if got := router.received(); len(got) != 0 {
		t.Fatalf("got %d notices before the new limit, want none: a limit held at start is remembered", len(got))
	}

	addLimitedStep(t, st, limitWiringAfter, limitWiringLaterReset())
	got := waitNotices(t, router, 1)
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
	router := &holdingRouter{release: make(chan struct{}), entered: make(chan struct{})}
	host := startLimitHost(t, st, getenv, router)

	pastStartCheck()
	addLimitedStep(t, st, limitWiringAfter, limitWiringLaterReset())
	select {
	case <-router.entered:
	case <-time.After(limitWiringWait):
		close(router.release)
		t.Fatal("no notice was routed for the new limit")
	}

	stopped := make(chan error, 1)
	go func() { stopped <- host.stop() }()
	select {
	case err := <-stopped:
		close(router.release)
		t.Fatalf("stop returned (%v) while a notice was still routing", err)
	case <-time.After(limitWiringHold):
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
