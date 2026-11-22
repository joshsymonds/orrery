package main

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic values only. The step ids, the repository and the usage resets are invented. A limited step is restored
// as limited and runs no session, except the step the after-start test starts, whose fake claude plays a usage-limit
// turn.
const (
	limitWiringHost   = "synthetic-host"
	limitWiringRepo   = "/work/synthetic-repo"
	limitWiringBefore = "limit-before"
	limitWiringAfter  = "limit-after"
	limitWiringThird  = "limit-third"
	limitWiringEpic   = "synthetic-epic"
	limitWiringTitle  = "Synthetic contract title"
	limitWiringStamp  = 1_700_000_000
	// limitWiringNoEpic is the name a step with no epic is held under in a limit's notice and its mark.
	limitWiringNoEpic = "a step outside any epic"
	// limitWiringSchema is the output schema of the step the after-start test starts.
	limitWiringSchema = `{"type":"object","properties":{"status":{"type":"string"}}}`
)

// limitWiringReset is the usage reset of the step limited before start, and limitWiringLaterReset the reset of the step
// limited after start, so the two steps are in different limit groups.
func limitWiringReset() time.Time {
	return time.Date(2026, 11, 15, 17, 0, 0, 0, time.UTC)
}

func limitWiringLaterReset() time.Time {
	return time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
}

// limitWiringThirdReset is a reset after limitWiringLaterReset, for a step a restart test limits to sync on.
func limitWiringThirdReset() time.Time {
	return time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
}

// recordingRouter is the router a limit test hands startSessions. It keeps the notices and the seen reports it is
// given. The limit notices call it from their own goroutine, so both are guarded by a lock. moved holds one pending
// signal, sent after each is kept, so a test waits for the next one.
type recordingRouter struct {
	mu      sync.Mutex
	notices []route.Notice
	seens   []notice.Key
	moved   chan struct{}
}

// newRecordingRouter returns an empty recordingRouter.
func newRecordingRouter() *recordingRouter {
	return &recordingRouter{moved: make(chan struct{}, 1)}
}

// Notify keeps n.
func (r *recordingRouter) Notify(_ context.Context, n route.Notice) {
	r.mu.Lock()
	r.notices = append(r.notices, n)
	r.mu.Unlock()
	r.signal()
}

// Changed does nothing: the limit notices do not report pane changes.
func (r *recordingRouter) Changed(notice.Key) {}

// Seen keeps the key of the limit that ended.
func (r *recordingRouter) Seen(_ context.Context, key notice.Key, _ int64) {
	r.mu.Lock()
	r.seens = append(r.seens, key)
	r.mu.Unlock()
	r.signal()
}

// signal leaves one pending signal on moved, unless one is already pending.
func (r *recordingRouter) signal() {
	select {
	case r.moved <- struct{}{}:
	default:
	}
}

// received returns a copy of the notices kept so far.
func (r *recordingRouter) received() []route.Notice {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]route.Notice(nil), r.notices...)
}

// seenCount returns how many seen reports are kept so far.
func (r *recordingRouter) seenCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.seens)
}

// receivedSeens returns a copy of the keys of the seen reports kept so far.
func (r *recordingRouter) receivedSeens() []notice.Key {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]notice.Key(nil), r.seens...)
}

// holdingRouter is a recordingRouter whose Notify returns only once release is closed. Its first Notify closes entered
// before it waits, and closes canceled if its context ends while it waits: the limit notices route with their loop's
// context, which stop cancels just before it waits for the loop.
type holdingRouter struct {
	*recordingRouter
	release      chan struct{}
	entered      chan struct{}
	canceled     chan struct{}
	enteredOnce  sync.Once
	canceledOnce sync.Once
}

// newHoldingRouter returns a holdingRouter whose channels are open.
func newHoldingRouter() *holdingRouter {
	return &holdingRouter{
		recordingRouter: newRecordingRouter(),
		release:         make(chan struct{}), entered: make(chan struct{}), canceled: make(chan struct{}),
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
// The state directory is a short one, since a step's holder socket lies under it and a socket path has a length limit.
func openLimitStore(t *testing.T) (*store.Store, func(string) string) {
	t.Helper()
	root, err := shortStateRoot()
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove %s: %v", root, err)
		}
	})
	env := map[string]string{
		"XDG_STATE_HOME":  root,
		"XDG_CONFIG_HOME": t.TempDir(),
	}
	getenv := func(key string) string { return env[key] }
	path, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store path: %v", err)
	}
	return openPaneStore(t, path), getenv
}

// startLimitHost starts a --sessions host over st that routes its notices to router. Its limit notices check at start
// and then on each wake, which the tests give when they store a limit (see sessionHost.limits). Its sessions run the
// engine host's fake claude, so a step the test starts can end its turn on a usage limit.
func startLimitHost(t *testing.T, st *store.Store, getenv func(string) string, router enginehost.Router) *sessionHost {
	t.Helper()
	opts := options{
		socket:       filepath.Join(t.TempDir(), "orreryd.sock"),
		claude:       eventWakeClaude(t),
		sessionShell: eventWakeShell(t),
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
	return host
}

// awaitNotices waits until router holds at least n notices, and returns them. It has no deadline of its own: a notice
// that never comes is the test run's to time out.
func awaitNotices(router *recordingRouter, n int) []route.Notice {
	for {
		if got := router.received(); len(got) >= n {
			return got
		}
		<-router.moved
	}
}

// awaitSeens waits until router holds at least n seen reports. Like awaitNotices, it has no deadline of its own.
func awaitSeens(router *recordingRouter, n int) {
	for router.seenCount() < n {
		<-router.moved
	}
}

// wakeOnLimitedStep stores a step with no epic, limited until resets, wakes the limit notices, and returns the notice
// the check that the wake starts routes for it. Checks run one at a time, in order, so a notice that the checks before
// the wake were to route is in router by the time this returns. The notice it returns is the one after those.
func wakeOnLimitedStep(
	t *testing.T, st *store.Store, host *sessionHost, router *recordingRouter, id string, resets time.Time,
) route.Notice {
	t.Helper()
	before := len(router.received())
	addLimitedStep(t, st, id, resets)
	host.limits.Wake()
	return awaitNotices(router, before+1)[before]
}

// limitWiringKey is the key of the usage limit that resets at resets, as the limit notices and their marks name it.
func limitWiringKey(resets time.Time) string {
	return fmt.Sprintf("usage@%d", resets.Unix())
}

// addLimitedStep stores an open step row with no epic whose loop is limited by usage until resets, as a usage limit
// leaves it.
func addLimitedStep(t *testing.T, st *store.Store, id string, resets time.Time) {
	t.Helper()
	addLimitedStepInEpic(t, st, id, "", resets)
}

// addLimitedStepInEpic stores an open step row labeled with epic, whose loop is limited by usage until resets, as a
// usage limit leaves it. An empty epic stores a step with no epic.
func addLimitedStepInEpic(t *testing.T, st *store.Store, id, epic string, resets time.Time) {
	t.Helper()
	if err := st.ApplyEngine(t.Context(), []store.EngineOp{
		store.StepAdd{
			ID: id, Agent: "implementer", Prompt: "synthetic prompt", Schema: "{}", Workspace: limitWiringRepo,
			ReadOnly: true, DoneValue: "done", Epic: epic,
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

	router := newRecordingRouter()
	host := startLimitHost(t, st, getenv, router)
	got := awaitNotices(router, 1)
	if len(got) != 1 {
		t.Fatalf("got %d notices for the step limited before start, want 1", len(got))
	}
	desk, err := got[0].Desk(ctx)
	if err != nil {
		t.Fatalf("desk notice: %v", err)
	}
	const want = "Held by the usage limit, reset 2026-11-15T17:00:00Z: " + limitWiringNoEpic
	if desk.Body != want {
		t.Errorf("notice body = %q, want %q", desk.Body, want)
	}

	// The notice's check stores its mark before it returns, and stop waits for the checks to finish, so the mark is stored
	// once stop has returned.
	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
	marks, err := st.ToldLimits(ctx)
	if err != nil {
		t.Fatalf("read told limits: %v", err)
	}
	wantMarks := map[string][]string{limitWiringKey(limitWiringReset()): {limitWiringNoEpic}}
	if !maps.EqualFunc(marks, wantMarks, slices.Equal[[]string]) {
		t.Fatalf("told limits = %v, want %v", marks, wantMarks)
	}
	if got := router.received(); len(got) != 1 {
		t.Fatalf("got %d notices once the mark is stored, want 1: the told step is not told again", len(got))
	}
}

// TestSessionsHostTellsNothingAfterRestartForStepTold checks that a step told before a restart is not told again when
// orreryd starts over the same store.
func TestSessionsHostTellsNothingAfterRestartForStepTold(t *testing.T) {
	t.Parallel()
	st, getenv := openLimitStore(t)
	addLimitedStep(t, st, limitWiringBefore, limitWiringReset())

	first := newRecordingRouter()
	host := startLimitHost(t, st, getenv, first)
	if got := awaitNotices(first, 1); len(got) != 1 {
		t.Fatalf("got %d notices before the restart, want 1", len(got))
	}
	// The step counts as told before the restart once its mark is stored, which stop waits for.
	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}

	second := newRecordingRouter()
	host = startLimitHost(t, st, getenv, second)
	// The restarted notices tell nothing for the step told before the restart, so the first notice a wake of a new limit
	// gives is the new limit's.
	got := wakeOnLimitedStep(t, st, host, second, limitWiringAfter, limitWiringLaterReset())
	desk, err := got.Desk(t.Context())
	if err != nil {
		t.Fatalf("desk notice: %v", err)
	}
	const want = "Held by the usage limit, reset 2026-11-16T09:00:00Z: " + limitWiringNoEpic
	if desk.Body != want {
		t.Errorf("first notice after the restart = %q, want %q: the step was told before it", desk.Body, want)
	}
	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions after restart: %v", err)
	}
	if n := len(second.received()); n != 1 {
		t.Fatalf("got %d notices after the restart, want 1: the step was told before it", n)
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

	first := newRecordingRouter()
	host := startLimitHost(t, st, getenv, first)
	if got := awaitNotices(first, 1); len(got) != 1 {
		t.Fatalf("got %d notices before the resume, want 1", len(got))
	}
	// A resume stores the step's loop state with no limit and closes its due rows, as resumeWith writes it. The wake
	// after it sees the step is no longer limited, so the first limit empties and its mark is forgotten.
	if err := st.ApplyEngine(ctx, []store.EngineOp{
		store.StepDueResumesClosed{ID: limitWiringBefore},
		store.StepLoop{ID: limitWiringBefore},
	}, time.Unix(limitWiringStamp+1, 0)); err != nil {
		t.Fatalf("resume step: %v", err)
	}
	host.limits.Wake()
	awaitSeens(first, 1)

	if err := st.ApplyEngine(ctx, []store.EngineOp{
		store.StepLoop{ID: limitWiringBefore, Limited: true, LimitKind: "usage", LimitResets: limitWiringLaterReset()},
	}, time.Unix(limitWiringStamp+2, 0)); err != nil {
		t.Fatalf("limit step again: %v", err)
	}
	host.limits.Wake()
	got := awaitNotices(first, 2)
	if len(got) != 2 {
		t.Fatalf("got %d notices after the step was limited again, want 2", len(got))
	}
	desk, err := got[1].Desk(ctx)
	if err != nil {
		t.Fatalf("desk notice: %v", err)
	}
	const want = "Held by the usage limit, reset 2026-11-16T09:00:00Z: " + limitWiringNoEpic
	if desk.Body != want {
		t.Errorf("notice body after the resume = %q, want %q", desk.Body, want)
	}
	// The check that routed the notice writes the limit's mark after it, and a check after a refused write writes it
	// again (see LimitNotices.update), so the marks come to name only the second limit. stop returns once they do.
	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
	marks, err := st.ToldLimits(ctx)
	if err != nil {
		t.Fatalf("read told limits: %v", err)
	}
	wantMarks := map[string][]string{limitWiringKey(limitWiringLaterReset()): {limitWiringNoEpic}}
	if !maps.EqualFunc(marks, wantMarks, slices.Equal[[]string]) {
		t.Fatalf("told limits = %v, want %v", marks, wantMarks)
	}

	second := newRecordingRouter()
	host = startLimitHost(t, st, getenv, second)
	// The second limit is marked, so the restart tells it nothing, and the first notice a wake of a new limit gives is
	// the new limit's.
	third := wakeOnLimitedStep(t, st, host, second, limitWiringThird, limitWiringThirdReset())
	desk, err = third.Desk(ctx)
	if err != nil {
		t.Fatalf("desk notice after the restart: %v", err)
	}
	const wantThird = "Held by the usage limit, reset 2026-11-17T09:00:00Z: " + limitWiringNoEpic
	if desk.Body != wantThird {
		t.Errorf("first notice after the restart = %q, want %q: the second limit was told before it",
			desk.Body, wantThird)
	}
	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions after restart: %v", err)
	}
	if n := len(second.received()); n != 1 {
		t.Fatalf("got %d notices after the restart, want 1: the second limit was told before it", n)
	}
}

// TestSessionsHostSendsNothingForStepMarkedBeforeStart checks that a step a usage limit holds when orreryd starts gets
// no notice when the store already marks it as told.
func TestSessionsHostSendsNothingForStepMarkedBeforeStart(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, getenv := openLimitStore(t)
	addLimitedStep(t, st, limitWiringBefore, limitWiringReset())
	if err := st.TellLimit(ctx, limitWiringKey(limitWiringReset()), []string{limitWiringNoEpic}); err != nil {
		t.Fatalf("mark limit told: %v", err)
	}

	router := newRecordingRouter()
	host := startLimitHost(t, st, getenv, router)
	// A limit no mark holds is told by the wake that follows it. The check it starts runs after the check at start, so
	// its notice being the first means the check at start told nothing.
	got := wakeOnLimitedStep(t, st, host, router, limitWiringAfter, limitWiringLaterReset())
	desk, err := got.Desk(ctx)
	if err != nil {
		t.Fatalf("desk notice: %v", err)
	}
	const want = "Held by the usage limit, reset 2026-11-16T09:00:00Z: " + limitWiringNoEpic
	if desk.Body != want {
		t.Errorf("first notice = %q, want %q: a limit marked as told was told again", desk.Body, want)
	}
	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
	if n := len(router.received()); n != 1 {
		t.Fatalf("got %d notices, want 1 for the new limit alone", n)
	}
}

// TestLimitNoticeWakeSessionsHostTellsHoldAndResumeStoredBySteps checks that a --sessions orreryd tells one limit notice
// when the Steps engine stores a step's hold, and ends that notice when the step resumes. The step limited before start
// is not marked told, so the check at start tells it, and that notice shows the check at start has read the steps: any
// later notice comes from a check that a stored wake started. The test never wakes the limit notices by hand. The hold
// and the resume reach them through the Steps engine's Stored hook, which calls sessionHost.storedWake. The resumed turn
// stalls, so the seen report can come only from the wake the stored resume calls.
func TestLimitNoticeWakeSessionsHostTellsHoldAndResumeStoredBySteps(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, getenv := openLimitStore(t)
	addLimitedStep(t, st, limitWiringBefore, limitWiringReset())

	router := newRecordingRouter()
	host := startLimitHost(t, st, getenv, router)
	if got := awaitNotices(router, 1); len(got) != 1 {
		t.Fatalf("got %d notices at start, want 1", len(got))
	}

	repo, revision := buildWiringRepo(t)
	id, err := host.steps.Start(ctx, enginehost.StepStart{
		Agent:     "reviewer",
		Agents:    `{"reviewer":{"description":"Checks a step","prompt":"look"}}`,
		Prompt:    "LIMIT",
		Schema:    limitWiringSchema,
		Workspace: repo,
		Revision:  revision,
		ReadOnly:  true,
	})
	if err != nil {
		t.Fatalf("start the limited step: %v", err)
	}
	held := awaitNotices(router, 2)
	desk, err := held[1].Desk(ctx)
	if err != nil {
		t.Fatalf("desk notice: %v", err)
	}
	// The fake claude's seven-day limit resets at 1900000000, which is 2030-03-17T17:46:40 UTC.
	const wantHeld = "Held by the seven_day limit, reset 2030-03-17T17:46:40Z: " + limitWiringNoEpic
	if desk.Body != wantHeld {
		t.Errorf("held notice body = %q, want %q", desk.Body, wantHeld)
	}

	// The resumed turn plays STALL, which never ends, so the turn's end stores nothing and calls no Stored. The only
	// wake after the resume is the one resumeWith calls once it stores the resume.
	stateDir, err := stateDirOf(getenv)
	if err != nil {
		t.Fatalf("state dir: %v", err)
	}
	next := filepath.Join(stateDir, "step-worktrees", id, "fakeclaude.next")
	if err := os.WriteFile(next, []byte("STALL"), 0o600); err != nil {
		t.Fatalf("write fake claude next prompt: %v", err)
	}
	if err := host.steps.Resume(ctx, id); err != nil {
		t.Fatalf("resume the held step: %v", err)
	}
	awaitSeens(router, 1)
	seen := router.receivedSeens()
	if len(seen) != 1 || seen[0].Limit != "seven_day@1900000000" {
		t.Errorf("seen keys = %+v, want exactly the seven_day limit that resets at 1900000000", seen)
	}

	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
	if n := len(router.received()); n != 2 {
		t.Fatalf("got %d notices, want 2: the limit before start and the held step's limit", n)
	}
}

// TestSessionsHostNamesTheContractTitleOfAHeldStepsEpic checks that a step a usage limit holds under a stored epic is
// named in its notice by the epic's contract title, and that the step's id is not in the notice.
func TestSessionsHostNamesTheContractTitleOfAHeldStepsEpic(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, getenv := openLimitStore(t)
	created := time.Unix(limitWiringStamp, 0)
	if err := st.CreateEpic(ctx, store.EpicRow{
		ID:         limitWiringEpic,
		Repository: limitWiringRepo,
		Title:      limitWiringTitle,
		Phase:      "accepted",
		Created:    created,
		Changed:    created,
	}); err != nil {
		t.Fatalf("create epic: %v", err)
	}
	addLimitedStepInEpic(t, st, limitWiringBefore, limitWiringEpic, limitWiringReset())

	router := newRecordingRouter()
	host := startLimitHost(t, st, getenv, router)
	got := awaitNotices(router, 1)
	if len(got) != 1 {
		t.Fatalf("got %d notices for the step held under the epic, want 1", len(got))
	}
	desk, err := got[0].Desk(ctx)
	if err != nil {
		t.Fatalf("desk notice: %v", err)
	}
	const want = "Held by the usage limit, reset 2026-11-15T17:00:00Z: " + limitWiringTitle
	if desk.Body != want {
		t.Errorf("notice body = %q, want %q", desk.Body, want)
	}
	if strings.Contains(desk.Body, limitWiringBefore) {
		t.Errorf("notice body %q names step %s, want no step id", desk.Body, limitWiringBefore)
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

	// The check at start finds no limited step, so it routes nothing. The wake after the new limit is stored routes its
	// notice, which holds in Notify until release.
	addLimitedStep(t, st, limitWiringAfter, limitWiringLaterReset())
	host.limits.Wake()
	<-router.entered

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
