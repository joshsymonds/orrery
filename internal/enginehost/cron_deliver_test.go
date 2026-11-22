package enginehost

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// The sentinel is a one-shot job, due next year, of a session that is never started. Every pass arms the timer for it,
// so the timer a pass arms shows that the pass is over.
const (
	deliverSentinelJob     = "cron-deliver-sentinel"
	deliverSentinelSession = "cron-deliver-no-session"
)

// deliverBlock is the payload of an engine_block event, as far as these tests read it.
type deliverBlock struct {
	Text string `json:"text"`
}

// cronDeliverRig wires a real Manager to a real CronScheduler for one test. The scheduler reads and deletes the jobs
// through the manager's own store, as orreryd gives the scheduler and the manager one store, and its clock and timers
// are the test's.
type cronDeliverRig struct {
	m      *Manager
	path   string
	idle   *idleClock
	clock  *fireClock
	timers *fakeFireTimers
	st     *store.Store
	sched  *CronScheduler
}

// newCronDeliverRig returns a rig over an idle manager. Its scheduler is the manager's cron hook, set before
// the first start, and is not run: the test runs it with runFireScheduler once the jobs it fires are stored.
func newCronDeliverRig(t *testing.T) *cronDeliverRig {
	t.Helper()
	m, path, idle := newIdleManager(t)
	st := storeOf(t, m)
	rig := &cronDeliverRig{
		m: m, path: path, idle: idle, clock: &fireClock{at: cronTestNow()}, timers: newFakeFireTimers(), st: st,
	}
	rig.sched = NewCronScheduler(CronSchedulerConfig{
		Store: st, Deliver: m.DeliverCron, Now: rig.clock.read, NewTimer: rig.timers.arm,
	})
	m.cronWake = rig.sched.Wake
	return rig
}

// storeSentinel stores the sentinel job in the rig's store.
func (r *cronDeliverRig) storeSentinel(t *testing.T) {
	t.Helper()
	storeFireJob(t, r.st, store.CronJob{
		ID: deliverSentinelJob, Session: deliverSentinelSession, ToolUseID: "tool-" + deliverSentinelJob,
		Cron: "0 0 1 1 *", Prompt: "sentinel", Created: cronTestNow(),
	})
}

// deliverSentinelDue is when the sentinel job is due: 2027-01-01 00:00 in cronTestZone.
func deliverSentinelDue() time.Time {
	return time.Date(2027, time.January, 1, 0, 0, 0, 0, cronTestZone())
}

// deliverJob returns a one-shot job of session with the id id, made at cronTestNow, which fires at cron.
func deliverJob(session, id, cron string) store.CronJob {
	return store.CronJob{
		ID: id, Session: session, ToolUseID: "tool-" + id, Cron: cron, Prompt: cronFirePrompt, Created: cronTestNow(),
	}
}

// requireArmed checks that timer was armed for d.
func requireArmed(t *testing.T, timer *fakeFireTimer, d time.Duration) {
	t.Helper()
	if timer.d != d {
		t.Fatalf("timer armed for %v, want %v", timer.d, d)
	}
}

// requireUnfired checks that the job id is stored, with no fire recorded.
func requireUnfired(t *testing.T, st *store.Store, id string) {
	t.Helper()
	for _, job := range storedJobs(t, st) {
		if job.ID == id {
			if !job.LastFire.IsZero() {
				t.Fatalf("job %s last fire = %v, want none", id, job.LastFire)
			}
			return
		}
	}
	t.Fatalf("job %s is not stored, want it stored and unfired", id)
}

// requireStoredJobs checks that the jobs stored in st have exactly the ids want, in any order.
func requireStoredJobs(t *testing.T, st *store.Store, want ...string) {
	t.Helper()
	jobs := storedJobs(t, st)
	got := make([]string, 0, len(jobs))
	for _, job := range jobs {
		got = append(got, job.ID)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("stored job ids = %q, want %q", got, want)
	}
}

// eventIndexes returns the index in history of each event of kind whose payload, decoded into T, match accepts, in seq
// order.
func eventIndexes[T any](t *testing.T, history store.EngineHistory, kind string, match func(T) bool) []int {
	t.Helper()
	var indexes []int
	for i, event := range history.Events {
		if event.Kind != kind {
			continue
		}
		var payload T
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatalf("decode %s payload %q: %v", kind, event.Payload, err)
		}
		if match(payload) {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

func TestCronDeliverFireWaitsForTurnEnd(t *testing.T) {
	t.Parallel()
	rig := newCronDeliverRig(t)
	dir := t.TempDir()
	id, err := rig.m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "TRICKLEHELD"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// TRICKLEHELD holds its last block until fakeclaude.release exists, so its turn runs until the test releases it.
	waitHistory(
		t,
		engineOf(rig.m),
		rig.path,
		id,
		"the held turn's block before its last",
		func(h store.EngineHistory) bool {
			return hasBlock(t, h, "fake trickle 1.59")
		},
	)
	job := deliverJob(id, cronFireJob, cronFireDaily)
	storeFireJob(t, rig.st, job)
	rig.storeSentinel(t)
	runFireScheduler(t, rig.sched)
	due := fireTime(0, 10, 30)

	// The start pass arms the timer for the job's due fire.
	timer := rig.timers.awaitArmed()
	requireArmed(t, timer, due.Sub(cronTestNow()))

	// Fired at due while the turn runs, the pass delivers nothing: the job stays stored and unfired, and the timer is
	// armed for the sentinel alone.
	rig.clock.set(due)
	timer.c <- time.Time{}
	requireArmed(t, rig.timers.awaitArmed(), deliverSentinelDue().Sub(due))
	requireUnfired(t, rig.st, job.ID)
	if got := enginePromptsOf(
		t,
		historyOf(t, rig.path, id),
	); !reflect.DeepEqual(
		got,
		[]deliveredPrompt{{Text: "TRICKLEHELD"}},
	) {
		t.Fatalf("prompts while the turn runs = %+v, want only the start's prompt", got)
	}
	if n := stdinLinesWith(t, dir, cronFirePrompt); n != 0 {
		t.Fatalf("fake claude read the cron prompt %d times while its turn ran, want none", n)
	}

	// Releasing the held turn ends it. The end wakes the scheduler, and the pass it starts delivers the fire.
	if err := os.WriteFile(filepath.Join(dir, "fakeclaude.release"), nil, 0o600); err != nil {
		t.Fatalf("release the held turn: %v", err)
	}
	rig.timers.awaitArmed()
	requireStoredJobs(t, rig.st, deliverSentinelJob)
	history := waitHistory(
		t,
		engineOf(rig.m),
		rig.path,
		id,
		"the reply to the cron prompt",
		func(h store.EngineHistory) bool {
			return hasBlock(t, h, "fake reply 2")
		},
	)
	want := []deliveredPrompt{
		{Text: "TRICKLEHELD"},
		{Text: cronFirePrompt, PromptID: firePromptID(job, due), Sender: store.PromptSenderCron},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
	if n := stdinLinesWith(t, dir, cronFirePrompt); n != 1 {
		t.Fatalf("fake claude read the cron prompt %d times, want once", n)
	}

	// The cron prompt is stored after the held turn's last block and after the turn's end.
	last := eventIndexes(t, history, store.EventEngineBlock, func(b deliverBlock) bool {
		return b.Text == "fake trickle 1.60"
	})
	ended := eventIndexes(t, history, store.EventEngineTurnEnded, func(any) bool { return true })
	cron := eventIndexes(t, history, store.EventEnginePrompt, func(p deliveredPrompt) bool {
		return p.Sender == store.PromptSenderCron
	})
	if len(last) != 1 || len(ended) == 0 || len(cron) != 1 || last[0] >= ended[0] || ended[0] >= cron[0] {
		t.Fatalf(
			"last block at %v, turn ends at %v, cron prompt at %v, want the cron prompt after the turn's end",
			last,
			ended,
			cron,
		)
	}
}

func TestCronDeliverFireWaitsForAnswer(t *testing.T) {
	t.Parallel()
	rig := newCronDeliverRig(t)
	dir := t.TempDir()
	id, err := rig.m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	history := waitHistory(t, engineOf(rig.m), rig.path, id, "the open question", func(h store.EngineHistory) bool {
		return h.State == string(protocol.StateWaiting)
	})
	requestID := requestIDOf(t, history)
	job := deliverJob(id, cronFireJob, cronFireDaily)
	storeFireJob(t, rig.st, job)
	rig.storeSentinel(t)
	runFireScheduler(t, rig.sched)
	due := fireTime(0, 10, 30)

	timer := rig.timers.awaitArmed()
	requireArmed(t, timer, due.Sub(cronTestNow()))

	// Fired at due with the question open, the pass delivers nothing.
	rig.clock.set(due)
	timer.c <- time.Time{}
	requireArmed(t, rig.timers.awaitArmed(), deliverSentinelDue().Sub(due))
	requireUnfired(t, rig.st, job.ID)
	if got := enginePromptsOf(
		t,
		historyOf(t, rig.path, id),
	); !reflect.DeepEqual(
		got,
		[]deliveredPrompt{{Text: "ASK"}},
	) {
		t.Fatalf("prompts with the question open = %+v, want only the start's prompt", got)
	}
	if n := stdinLinesWith(t, dir, cronFirePrompt); n != 0 {
		t.Fatalf("fake claude read the cron prompt %d times with the question open, want none", n)
	}

	// The owner's answer ends the turn, and that end wakes the scheduler. The pass it starts delivers the fire.
	if err := rig.m.Answer(t.Context(), id, requestID, "red"); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	history = waitHistory(
		t,
		engineOf(rig.m),
		rig.path,
		id,
		"the reply to the cron prompt",
		func(h store.EngineHistory) bool {
			return len(enginePromptsOf(t, h)) == 2 && h.State == string(protocol.StateDone)
		},
	)
	rig.timers.awaitArmed()
	requireStoredJobs(t, rig.st, deliverSentinelJob)
	want := []deliveredPrompt{
		{Text: "ASK"},
		{Text: cronFirePrompt, PromptID: firePromptID(job, due), Sender: store.PromptSenderCron},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
	if n := stdinLinesWith(t, dir, cronFirePrompt); n != 1 {
		t.Fatalf("fake claude read the cron prompt %d times, want once", n)
	}
	ended := eventIndexes(t, history, store.EventEngineTurnEnded, func(any) bool { return true })
	cron := eventIndexes(t, history, store.EventEnginePrompt, func(p deliveredPrompt) bool {
		return p.Sender == store.PromptSenderCron
	})
	if len(ended) == 0 || len(cron) != 1 || cron[0] < ended[0] {
		t.Fatalf("turn ends at %v, cron prompt at %v, want the cron prompt after the answered turn's end", ended, cron)
	}
}

func TestCronDeliverFireQuietUntilSay(t *testing.T) {
	t.Parallel()
	rig := newCronDeliverRig(t)
	dir := t.TempDir()
	id, err := rig.m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "HANG"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(rig.m), rig.path, id, "the hanging reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake hanging 1")
	})
	job := deliverJob(id, cronFireJob, cronFireDaily)
	storeFireJob(t, rig.st, job)
	rig.storeSentinel(t)
	runFireScheduler(t, rig.sched)
	due := fireTime(0, 10, 30)
	requireArmed(t, rig.timers.awaitArmed(), due.Sub(cronTestNow()))

	// The owner's interrupt ends the turn, and that end wakes the scheduler. The pass arms the job's timer again.
	if _, err := rig.m.OwnerInterrupt(t.Context(), id); err != nil {
		t.Fatalf("OwnerInterrupt: %v", err)
	}
	waitHistory(t, engineOf(rig.m), rig.path, id, "the interrupted turn's end", func(h store.EngineHistory) bool {
		return turnEndsWithError(t, h) && h.State == string(protocol.StateDone)
	})
	timer := rig.timers.awaitArmed()
	requireArmed(t, timer, due.Sub(cronTestNow()))

	// Fired at due, the quiet session takes nothing, so the job stays stored and unfired.
	rig.clock.set(due)
	timer.c <- time.Time{}
	again := rig.timers.awaitArmed()
	requireArmed(t, again, deliverSentinelDue().Sub(due))
	requireUnfired(t, rig.st, job.ID)
	if got := enginePromptsOf(
		t,
		historyOf(t, rig.path, id),
	); !reflect.DeepEqual(
		got,
		[]deliveredPrompt{{Text: "HANG"}},
	) {
		t.Fatalf("prompts while quiet = %+v, want only the start's prompt", got)
	}

	// Fired again, the pass still takes nothing.
	again.c <- time.Time{}
	requireArmed(t, rig.timers.awaitArmed(), deliverSentinelDue().Sub(due))
	requireUnfired(t, rig.st, job.ID)
	if got := enginePromptsOf(
		t,
		historyOf(t, rig.path, id),
	); !reflect.DeepEqual(
		got,
		[]deliveredPrompt{{Text: "HANG"}},
	) {
		t.Fatalf("prompts after the timer fired again = %+v, want only the start's prompt", got)
	}
	if n := stdinLinesWith(t, dir, cronFirePrompt); n != 0 {
		t.Fatalf("fake claude read the cron prompt %d times while quiet, want none", n)
	}

	// The owner's Say clears the quiet, and the cron prompt follows the Say's turn.
	if err := rig.m.Say(t.Context(), SayRequest{ID: id, Text: "owner words", PromptID: "p-owner"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	history := waitHistory(
		t,
		engineOf(rig.m),
		rig.path,
		id,
		"the reply to the cron prompt",
		func(h store.EngineHistory) bool {
			return hasBlock(t, h, "fake reply 3")
		},
	)
	want := []deliveredPrompt{
		{Text: "HANG"},
		{Text: "owner words", PromptID: "p-owner", Sender: store.PromptSenderOwner},
		{Text: cronFirePrompt, PromptID: firePromptID(job, due), Sender: store.PromptSenderCron},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
	owner := eventIndexes(t, history, store.EventEnginePrompt, func(p deliveredPrompt) bool {
		return p.Text == "owner words"
	})
	ended := eventIndexes(t, history, store.EventEngineTurnEnded, func(any) bool { return true })
	cron := eventIndexes(t, history, store.EventEnginePrompt, func(p deliveredPrompt) bool {
		return p.Sender == store.PromptSenderCron
	})
	if len(owner) != 1 || len(ended) < 2 || len(cron) != 1 || owner[0] >= ended[1] || ended[1] >= cron[0] {
		t.Fatalf("owner prompt at %v, turn ends at %v, cron prompt at %v, want the cron prompt after the Say's turn",
			owner, ended, cron)
	}
}

func TestCronDeliverFireStopAndArchiveRemoveJobs(t *testing.T) {
	t.Parallel()
	rig := newCronDeliverRig(t)
	rig.storeSentinel(t)
	runFireScheduler(t, rig.sched)
	requireArmed(t, rig.timers.awaitArmed(), deliverSentinelDue().Sub(cronTestNow()))

	// A runs its first turn, and its end wakes the scheduler, whose pass arms the sentinel.
	dirA := t.TempDir()
	a, err := rig.m.Start(t.Context(), StartRequest{Dir: dirA, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start A: %v", err)
	}
	waitHistory(t, engineOf(rig.m), rig.path, a, "A's first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && lastState(t, h) == string(protocol.StateDone)
	})
	rig.timers.awaitArmed()
	jobA := deliverJob(a, "cron-deliver-job-a", "30 10 * * *")
	storeFireJob(t, rig.st, jobA)
	rig.idle.advance(defaultIdleAfter)
	rig.m.checkIdle(t.Context())
	timerA := rig.timers.awaitArmed()
	requireArmed(t, timerA, fireTime(0, 10, 30).Sub(cronTestNow()))

	// B runs its first turn, and then its job is stored. B goes idle-stopped, and the pass that its exit wakes arms the
	// timer for A's job, the earlier of the two.
	b, err := rig.m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start B: %v", err)
	}
	waitHistory(t, engineOf(rig.m), rig.path, b, "B's first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && lastState(t, h) == string(protocol.StateDone)
	})
	rig.timers.awaitArmed()
	jobB := deliverJob(b, "cron-deliver-job-b", "45 10 * * *")
	storeFireJob(t, rig.st, jobB)
	rig.idle.advance(defaultIdleAfter)
	rig.m.checkIdle(t.Context())
	timerA = rig.timers.awaitArmed()
	requireArmed(t, timerA, fireTime(0, 10, 30).Sub(cronTestNow()))

	// The owner's stop of idle-stopped A returns ErrNoSession, and removes A's job.
	if err := rig.m.Stop(t.Context(), a); !errors.Is(err, ErrNoSession) {
		t.Fatalf("Stop of an idle-stopped session error = %v, want ErrNoSession", err)
	}
	requireStoredJobs(t, rig.st, jobB.ID, deliverSentinelJob)

	// Archiving B removes its job.
	if err := rig.st.ArchiveRow(t.Context(), "headless", b, cronTestNow()); err != nil {
		t.Fatalf("ArchiveRow B: %v", err)
	}
	requireStoredJobs(t, rig.st, deliverSentinelJob)

	// Past both due times, the timer fires, and neither session takes a fire.
	rig.clock.set(fireTime(0, 11, 0))
	timerA.c <- time.Time{}
	requireArmed(t, rig.timers.awaitArmed(), deliverSentinelDue().Sub(fireTime(0, 11, 0)))
	requireStoredJobs(t, rig.st, deliverSentinelJob)
	for _, id := range []string{a, b} {
		if got := enginePromptsOf(
			t,
			historyOf(t, rig.path, id),
		); !reflect.DeepEqual(
			got,
			[]deliveredPrompt{{Text: "hi"}},
		) {
			t.Fatalf("prompts of %s after its job was removed = %+v, want only the start's prompt", id, got)
		}
	}
	if args := fakeArgs(t, dirA); len(args) != 1 {
		t.Fatalf("fake claude starts of A = %q, want the start only", args)
	}
}

func TestCronDeliverFireMissedDuringStopOnceAtStart(t *testing.T) {
	t.Parallel()
	// The rig's scheduler is the one orreryd ran before the restart, and it never runs: the restart below builds a new one.
	rig := newCronDeliverRig(t)
	dir := t.TempDir()
	id, err := rig.m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(rig.m), rig.path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && lastState(t, h) == string(protocol.StateDone)
	})
	rig.idle.advance(defaultIdleAfter)
	rig.m.checkIdle(t.Context())
	if _, err := rig.m.live(id); !errors.Is(err, ErrNoSession) {
		t.Fatalf("live after idle stop: err = %v, want ErrNoSession", err)
	}

	// The job is made at 10:02:30, and its matches at 10:30, 11:30 and 12:30 pass while no scheduler runs.
	job := deliverJob(id, cronFireJob, "30 * * * *")
	job.Recurring = true
	storeFireJob(t, rig.st, job)

	// orreryd restarts: a new store handle and a new scheduler, whose clock is past those matches, start.
	st, err := store.Open(t.Context(), rig.path, storetest.NoSync())
	if err != nil {
		t.Fatalf("reopen store after the restart: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close reopened store: %v", err)
		}
	})
	clock := &fireClock{at: fireTime(0, 13, 0)}
	timers := newFakeFireTimers()
	sched := NewCronScheduler(CronSchedulerConfig{
		Store: st, Deliver: rig.m.DeliverCron, Now: clock.read, NewTimer: timers.arm,
	})
	runFireScheduler(t, sched)

	// The start pass resumes the session, delivers the fire for the earliest missed match once, and arms the timer for
	// the match at 13:30.
	requireArmed(t, timers.awaitArmed(), cronFireGap)
	history := waitHistory(
		t,
		engineOf(rig.m),
		rig.path,
		id,
		"the reply to the cron prompt",
		func(h store.EngineHistory) bool {
			return hasBlock(t, h, "fake reply 2")
		},
	)
	want := []deliveredPrompt{
		{Text: "hi"},
		{Text: cronFirePrompt, PromptID: firePromptID(job, fireTime(0, 10, 30)), Sender: store.PromptSenderCron},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
	requireLastFire(t, st, fireTime(0, 13, 0))
	if args := fakeArgs(t, dir); len(args) != 2 || !strings.Contains(args[1], "--resume session_1") {
		t.Fatalf("fake claude starts = %q, want a second start resuming session_1", args)
	}
}
