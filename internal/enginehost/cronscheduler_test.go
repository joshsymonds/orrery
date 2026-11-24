package enginehost

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// Synthetic values for the fire tests. The session and the job are invented.
const (
	cronFireSession = "cron-fire-session"
	cronFireTool    = "toolu_cron_fire"
	cronFireJob     = "cron-fire-job"
	cronFirePrompt  = "the cron prompt"
	// cronFireDaily fires at 10:30 each day, and cronFireHourly at each hour.
	cronFireDaily  = "30 10 * * *"
	cronFireHourly = "0 * * * *"
	cronFireDay    = 24 * time.Hour
	// cronFireLifetime is how long a recurring job lives, seven days.
	cronFireLifetime = 7 * cronFireDay
	// cronFireMatchDays is how many daily matches fall within the lifetime of a job created on the test day.
	cronFireMatchDays = 7
	cronFireGap       = 30 * time.Minute
	cronFireFailure   = "resume failed"
	// cronFireBuffer sizes the channels the fakes record on, so a pass never blocks on them.
	cronFireBuffer = 64
)

// fireClock is the clock the fire tests read as now. A Run goroutine reads it while the test sets it, so it is guarded.
type fireClock struct {
	mu sync.Mutex
	at time.Time
}

// read returns the clock's time.
func (c *fireClock) read() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

// set moves the clock to at.
func (c *fireClock) set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = at
}

// fakeFireDeliver records every Deliver call on calls, in order, and returns the next scripted error, or nil once the
// script is spent. A call that returns nil is recorded in sent too.
type fakeFireDeliver struct {
	calls chan DeliverRequest
	errs  []error
	sent  []DeliverRequest
}

// newFakeFireDeliver returns a fakeFireDeliver that returns errs in order, and then nil.
func newFakeFireDeliver(errs ...error) *fakeFireDeliver {
	return &fakeFireDeliver{calls: make(chan DeliverRequest, cronFireBuffer), errs: errs}
}

// Deliver records req and returns the next scripted error.
func (d *fakeFireDeliver) Deliver(_ context.Context, req DeliverRequest) error {
	d.calls <- req
	var err error
	if len(d.errs) > 0 {
		err, d.errs = d.errs[0], d.errs[1:]
	}
	if err == nil {
		d.sent = append(d.sent, req)
	}
	return err
}

// next receives the next call, and waits for it when none is made yet.
func (d *fakeFireDeliver) next() DeliverRequest { return <-d.calls }

// taken returns the calls that next has not received and taken has not returned, without waiting.
func (d *fakeFireDeliver) taken() []DeliverRequest {
	var reqs []DeliverRequest
	for {
		select {
		case req := <-d.calls:
			reqs = append(reqs, req)
		default:
			return reqs
		}
	}
}

// fakeFireTimer is a CronTimer that the test fires by sending on c. d is the duration it was armed for.
type fakeFireTimer struct {
	c       chan time.Time
	d       time.Duration
	stopped bool
}

// C returns the channel the test fires the timer on.
func (t *fakeFireTimer) C() <-chan time.Time { return t.c }

// Stop records that the timer was stopped.
func (t *fakeFireTimer) Stop() bool {
	t.stopped = true
	return true
}

// fakeFireTimers records each timer the scheduler arms, in order.
type fakeFireTimers struct {
	armed chan *fakeFireTimer
}

// newFakeFireTimers returns a fakeFireTimers with no timer armed.
func newFakeFireTimers() *fakeFireTimers {
	return &fakeFireTimers{armed: make(chan *fakeFireTimer, cronFireBuffer)}
}

// arm returns a new fake timer armed for d, and records it.
func (f *fakeFireTimers) arm(d time.Duration) CronTimer {
	timer := &fakeFireTimer{c: make(chan time.Time, 1), d: d}
	f.armed <- timer
	return timer
}

// takeArmed returns the timer armed since the last take, and fails when none was armed.
func (f *fakeFireTimers) takeArmed(t *testing.T) *fakeFireTimer {
	t.Helper()
	select {
	case timer := <-f.armed:
		return timer
	default:
		t.Fatal("no timer was armed, want one")
		return nil
	}
}

// awaitArmed waits for the next timer the scheduler arms.
func (f *fakeFireTimers) awaitArmed() *fakeFireTimer { return <-f.armed }

// requireNoneArmed fails when a timer was armed since the last take.
func (f *fakeFireTimers) requireNoneArmed(t *testing.T) {
	t.Helper()
	select {
	case timer := <-f.armed:
		t.Fatalf("a timer was armed for %v, want none", timer.d)
	default:
	}
}

// fireTest is a scheduler over a store, with the clock, the delivery and the timers it fires through.
type fireTest struct {
	clock   *fireClock
	deliver *fakeFireDeliver
	timers  *fakeFireTimers
	sched   *CronScheduler
}

// newFireTest returns a scheduler over st whose clock reads now and whose Deliver is deliver.
func newFireTest(st *store.Store, deliver *fakeFireDeliver, now time.Time) *fireTest {
	clock := &fireClock{at: now}
	timers := newFakeFireTimers()
	sched := NewCronScheduler(CronSchedulerConfig{
		Store:    st,
		Deliver:  deliver.Deliver,
		Now:      clock.read,
		NewTimer: timers.arm,
	})
	return &fireTest{clock: clock, deliver: deliver, timers: timers, sched: sched}
}

// runFireScheduler runs sched's Run on its own goroutine until the test ends.
func runFireScheduler(t *testing.T, sched *CronScheduler) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sched.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// fireTime returns the time at hour:minute on the day-th day after 2026-11-18, in cronTestZone.
func fireTime(day, hour, minute int) time.Time {
	return time.Date(2026, time.November, 18+day, hour, minute, 0, 0, cronTestZone())
}

// fireJob returns a job of cronFireSession that cron makes at cronTestNow, recurring or one-shot.
func fireJob(recurring bool, cron string) store.CronJob {
	return store.CronJob{
		ID: cronFireJob, Session: cronFireSession, ToolUseID: cronFireTool, Cron: cron, Prompt: cronFirePrompt,
		Recurring: recurring, Created: cronTestNow(),
	}
}

// storeFireJob stores job in st, and fails unless the store takes it as a new job.
func storeFireJob(t *testing.T, st *store.Store, job store.CronJob) {
	t.Helper()
	if _, created, err := st.CreateCronJob(t.Context(), job); err != nil || !created {
		t.Fatalf("CreateCronJob %s: created %v, err %v", job.ID, created, err)
	}
}

// storedJobs returns the cron jobs stored in st.
func storedJobs(t *testing.T, st *store.Store) []store.CronJob {
	t.Helper()
	jobs, err := st.AllCronJobs(t.Context())
	if err != nil {
		t.Fatalf("AllCronJobs: %v", err)
	}
	return jobs
}

// requireLastFire checks that st holds exactly one job, and that its last fire is want.
func requireLastFire(t *testing.T, st *store.Store, want time.Time) {
	t.Helper()
	jobs := storedJobs(t, st)
	if len(jobs) != 1 || !jobs[0].LastFire.Equal(want) {
		t.Fatalf("stored jobs = %+v, want one job with last fire %v", jobs, want)
	}
}

// firePromptID returns the prompt id of job's fire due at due.
func firePromptID(job store.CronJob, due time.Time) string {
	return fmt.Sprintf("cron:%s:%d", job.ID, due.Unix())
}

// requireFired checks that the calls made since the last check are exactly one delivery of job's fire due at due.
func requireFired(t *testing.T, deliver *fakeFireDeliver, job store.CronJob, due time.Time) {
	t.Helper()
	want := DeliverRequest{ID: job.Session, Text: job.Prompt, PromptID: firePromptID(job, due)}
	if calls := deliver.taken(); len(calls) != 1 || calls[0] != want {
		t.Fatalf("deliveries = %+v, want exactly %+v", calls, want)
	}
}

func TestCronFireAtDueTimeNotBefore(t *testing.T) {
	t.Parallel()
	st, _ := openStore(t)
	job := fireJob(false, cronFireDaily)
	storeFireJob(t, st, job)
	due := fireTime(0, 10, 30)
	fx := newFireTest(st, newFakeFireDeliver(), cronTestNow())
	runFireScheduler(t, fx.sched)

	// The timer is armed for the time from now to the due fire.
	first := fx.timers.awaitArmed()
	if want := due.Sub(cronTestNow()); first.d != want {
		t.Fatalf("timer armed for %v, want %v, the time to the due fire", first.d, want)
	}

	// Fired a minute before due, the timer delivers nothing. The pass stops the timer it fired and arms one for that
	// minute.
	fx.clock.set(due.Add(-time.Minute))
	first.c <- time.Time{}
	armed := fx.timers.awaitArmed()
	if !first.stopped {
		t.Fatalf("the timer armed before the re-arm was not stopped")
	}
	if armed.d != time.Minute {
		t.Fatalf("timer re-armed for %v, want one minute to the due fire", armed.d)
	}
	if got := fx.deliver.taken(); len(got) != 0 {
		t.Fatalf("deliveries before due = %+v, want none", got)
	}

	// Fired at due, the timer delivers the job's prompt to its session.
	fx.clock.set(due)
	armed.c <- time.Time{}
	want := DeliverRequest{ID: cronFireSession, Text: cronFirePrompt, PromptID: firePromptID(job, due)}
	if got := fx.deliver.next(); got != want {
		t.Fatalf("delivered %+v at due, want %+v", got, want)
	}
}

func TestCronFireOneShotRemovedAfterStored(t *testing.T) {
	t.Parallel()
	st, _ := openStore(t)
	job := fireJob(false, cronFireDaily)
	storeFireJob(t, st, job)
	due := fireTime(0, 10, 30)
	fx := newFireTest(st, newFakeFireDeliver(errors.New(cronFireFailure)), due)
	ctx := t.Context()

	// The delivery fails, so the one-shot job stays stored, due and unfired.
	fx.sched.pass(ctx)
	requireFired(t, fx.deliver, job, due)
	if stored := storedJobs(t, st); len(stored) != 1 || stored[0].ID != job.ID || !stored[0].LastFire.IsZero() {
		t.Fatalf("stored jobs after a failed delivery = %+v, want the one-shot job, unfired", stored)
	}

	// A later pass delivers it once, and the job is gone once the fire is made.
	fx.sched.pass(ctx)
	requireFired(t, fx.deliver, job, due)
	if stored := storedJobs(t, st); len(stored) != 0 {
		t.Fatalf("stored jobs after the fire = %+v, want none", stored)
	}
	fx.timers.requireNoneArmed(t)
}

func TestCronFireFailedDeliveryStaysDue(t *testing.T) {
	t.Parallel()
	st, _ := openStore(t)
	job := fireJob(true, cronFireDaily)
	storeFireJob(t, st, job)
	due := fireTime(0, 10, 30)
	deliver := newFakeFireDeliver(errors.New(cronFireFailure))
	fx := newFireTest(st, deliver, due)
	runFireScheduler(t, fx.sched)

	// The start pass's resume fails, so the job keeps no last fire and stays due.
	failed := deliver.next()
	requireLastFire(t, st, time.Time{})

	// Wake runs a pass that delivers the same prompt. The fire it makes arms the timer for the next day's match, and
	// that arming comes after the fire is stored.
	fx.sched.Wake("")
	if got := deliver.next(); got != failed {
		t.Fatalf("Wake delivered %+v, want the failed delivery again: %+v", got, failed)
	}
	armed := fx.timers.awaitArmed()
	if armed.d != cronFireDay {
		t.Fatalf("timer armed for %v after the fire, want %v, the next match", armed.d, cronFireDay)
	}
	requireLastFire(t, st, due)

	// A further pass, at the timer's fire before the next match, delivers nothing more.
	armed.c <- time.Time{}
	if next := fx.timers.awaitArmed(); next.d != cronFireDay {
		t.Fatalf("timer re-armed for %v, want %v", next.d, cronFireDay)
	}
	if got := deliver.taken(); len(got) != 0 {
		t.Fatalf("deliveries after the fire = %+v, want none", got)
	}
}

func TestCronFireRecurringSevenDayFinal(t *testing.T) {
	t.Parallel()
	st, _ := openStore(t)
	job := fireJob(true, cronFireDaily)
	storeFireJob(t, st, job)
	fx := newFireTest(st, newFakeFireDeliver(), cronTestNow())
	ctx := t.Context()

	// Before the first match the timer is armed for it.
	fx.sched.pass(ctx)
	armed := fx.timers.takeArmed(t)
	for day := range cronFireMatchDays {
		match := fireTime(day, 10, 30)
		if want := match.Sub(fx.clock.read()); armed.d != want {
			t.Fatalf("timer armed for %v before the match of day %d, want %v", armed.d, day, want)
		}
		fx.clock.set(match)
		fx.sched.pass(ctx)
		requireFired(t, fx.deliver, job, match)
		requireLastFire(t, st, match)
		armed = fx.timers.takeArmed(t)
	}

	// The last match is followed by the end, so the timer is armed for the end. There the job fires its final time and
	// is gone.
	end := job.Created.Add(cronFireLifetime)
	if want := end.Sub(fx.clock.read()); armed.d != want {
		t.Fatalf("timer armed for %v after the last match, want %v, the end", armed.d, want)
	}
	fx.clock.set(end)
	fx.sched.pass(ctx)
	requireFired(t, fx.deliver, job, end)
	if stored := storedJobs(t, st); len(stored) != 0 {
		t.Fatalf("stored jobs after the final fire = %+v, want none", stored)
	}
	fx.timers.requireNoneArmed(t)
}

func TestCronFireWaitingCollapses(t *testing.T) {
	t.Parallel()
	st, _ := openStore(t)
	job := fireJob(true, cronFireHourly)
	storeFireJob(t, st, job)
	// The session is busy at the matches of 11:00, 12:00 and 13:00, and takes the fire at 14:00.
	fx := newFireTest(st, newFakeFireDeliver(ErrSessionBusy, ErrSessionBusy, ErrSessionBusy), cronTestNow())
	ctx := t.Context()
	fx.sched.pass(ctx)
	if armed := fx.timers.takeArmed(t); armed.d != fireTime(0, 11, 0).Sub(cronTestNow()) {
		t.Fatalf("timer armed for %v, want the time to the 11:00 match", armed.d)
	}

	for _, at := range []time.Time{fireTime(0, 11, 0), fireTime(0, 12, 0), fireTime(0, 13, 0)} {
		fx.clock.set(at)
		fx.sched.pass(ctx)
	}
	fx.timers.requireNoneArmed(t)

	fx.clock.set(fireTime(0, 14, 0))
	fx.sched.pass(ctx)
	// Every attempt is for the 11:00 fire, and only the one that succeeds delivers a prompt.
	want := firePromptID(job, fireTime(0, 11, 0))
	attempts := fx.deliver.taken()
	if len(attempts) != 4 {
		t.Fatalf("delivery attempts = %d, want 4, one per pass while the fire waited", len(attempts))
	}
	for _, attempt := range attempts {
		if attempt.PromptID != want {
			t.Fatalf("attempt prompt id %q, want %q for the waiting 11:00 fire", attempt.PromptID, want)
		}
	}
	if len(fx.deliver.sent) != 1 {
		t.Fatalf("prompts delivered = %+v, want exactly one", fx.deliver.sent)
	}
	requireLastFire(t, st, fireTime(0, 14, 0))
	if armed := fx.timers.takeArmed(t); armed.d != time.Hour {
		t.Fatalf("timer armed for %v after the fire, want an hour, the 15:00 match", armed.d)
	}
}

func TestCronFireAfterRestart(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	first, err := store.Open(ctx, path, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	job := fireJob(true, cronFireHourly)
	storeFireJob(t, first, job)
	if err := first.Close(); err != nil {
		t.Fatalf("close store before the restart: %v", err)
	}
	second, err := store.Open(ctx, path, storetest.NoSync())
	if err != nil {
		t.Fatalf("reopen store after the restart: %v", err)
	}
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("close reopened store: %v", err)
		}
	})

	// The clock is past the matches of 11:00, 12:00 and 13:00, which passed while the store was closed.
	deliver := newFakeFireDeliver()
	fx := newFireTest(second, deliver, fireTime(0, 13, 30))
	runFireScheduler(t, fx.sched)

	// The start pass delivers the job once, for its earliest missed fire, and arms the timer for the next match.
	want := DeliverRequest{ID: cronFireSession, Text: cronFirePrompt, PromptID: firePromptID(job, fireTime(0, 11, 0))}
	if got := deliver.next(); got != want {
		t.Fatalf("start pass delivered %+v, want %+v", got, want)
	}
	armed := fx.timers.awaitArmed()
	if armed.d != cronFireGap {
		t.Fatalf("timer armed for %v after the start pass, want %v, the 14:00 match", armed.d, cronFireGap)
	}
	if got := deliver.taken(); len(got) != 0 {
		t.Fatalf("deliveries after the start pass = %+v, want the one delivery", got)
	}
	requireLastFire(t, second, fireTime(0, 13, 30))
}

func TestCronFireResumesIdleStopped(t *testing.T) {
	t.Parallel()
	m, path, clock := newIdleManager(t)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && lastState(t, h) == string(protocol.StateDone)
	})
	clock.advance(defaultIdleAfter)
	m.checkIdle(t.Context())
	if _, err := m.live(id); !errors.Is(err, ErrNoSession) {
		t.Fatalf("live after idle stop: err = %v, want ErrNoSession", err)
	}

	st := storeOf(t, m)
	job := store.CronJob{
		ID: cronFireJob, Session: id, ToolUseID: cronFireTool, Cron: cronFireDaily, Prompt: cronFirePrompt,
		Created: cronTestNow(),
	}
	storeFireJob(t, st, job)
	due := fireTime(0, 10, 30)
	sched := NewCronScheduler(CronSchedulerConfig{
		Store:   st,
		Deliver: m.DeliverCron,
		Now:     func() time.Time { return due },
	})
	sched.pass(t.Context())
	if stored := storedJobs(t, st); len(stored) != 0 {
		t.Fatalf("stored jobs after the fire = %+v, want the one-shot job gone", stored)
	}

	history := waitHistory(t, engineOf(m), path, id, "the reply to the cron prompt", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	want := []deliveredPrompt{
		{Text: "hi"},
		{Text: cronFirePrompt, PromptID: firePromptID(job, due), Sender: store.PromptSenderCron},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
	if args := fakeArgs(t, dir); len(args) != 2 || !strings.Contains(args[1], "--resume session_1") {
		t.Fatalf("fake claude starts = %q, want a second start resuming session_1", args)
	}
}
