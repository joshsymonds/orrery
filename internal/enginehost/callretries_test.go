package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// scriptedHaiku is a haikuCaller that fails its first failures calls and answers each later call with reply.
type scriptedHaiku struct {
	failures int
	reply    json.RawMessage

	mu    sync.Mutex
	calls int
}

// Call counts the call, and fails it while the count is within failures.
func (h *scriptedHaiku) Call(context.Context, string, string) (json.RawMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls++
	if h.calls <= h.failures {
		return nil, errors.New("haiku: claude run: exit status 1: result error_max_turns")
	}
	return h.reply, nil
}

// count returns how many calls the fake has answered.
func (h *scriptedHaiku) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

// TestCallRetryWaitsFollowTheSchedule checks the wait after each failure in a row: 5 seconds, 30 seconds, a minute,
// then 5 minutes for every failure after the third.
func TestCallRetryWaitsFollowTheSchedule(t *testing.T) {
	t.Parallel()
	want := []time.Duration{5 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute, 5 * time.Minute,
		5 * time.Minute}
	for i, wait := range want {
		if got := callRetryWait(i + 1); got != wait {
			t.Errorf("wait after failure %d = %v, want %v", i+1, got, wait)
		}
	}
}

// TestCallRetriesHoldAKeyUntilItsDeadlineFires checks that a failed key is not ready while its deadline waits, that a
// second failure in that wait arms no second deadline, and that the deadline makes the key ready and runs its wake.
func TestCallRetriesHoldAKeyUntilItsDeadlineFires(t *testing.T) {
	t.Parallel()
	after := &deadlineClock{armed: make(chan armedDeadline, 16)}
	retries := newCallRetries(after.afterFunc)
	if !retries.ready("k") {
		t.Fatal("a key with no failure is not ready")
	}
	woke := 0
	retries.failed("k", func() { woke++ })
	retries.failed("k", func() { woke++ })
	armed := after.take(t)
	after.noneArmed(t)
	if armed.d != 5*time.Second || retries.ready("k") {
		t.Fatalf("after a failure: wait %v, ready %t; want 5s and not ready", armed.d, retries.ready("k"))
	}
	armed.fire()
	if woke != 1 || !retries.ready("k") {
		t.Fatalf("after the deadline fired: woke %d, ready %t; want 1 and ready", woke, retries.ready("k"))
	}
	retries.failed("k", func() {})
	if next := after.take(t); next.d != 30*time.Second {
		t.Fatalf("wait after the second failure in a row = %v, want 30s", next.d)
	}
}

// TestCallRetriesForgetStopsTheDeadlineAndStartsTheScheduleOver checks that forget stops an armed deadline, makes the
// key ready, and that the key's next failure waits the first wait again.
func TestCallRetriesForgetStopsTheDeadlineAndStartsTheScheduleOver(t *testing.T) {
	t.Parallel()
	after := &deadlineClock{armed: make(chan armedDeadline, 16)}
	retries := newCallRetries(after.afterFunc)
	retries.failed("k", func() {})
	first := after.take(t)
	first.fire()
	retries.failed("k", func() {})
	second := after.take(t)
	retries.forget("k")
	if !second.stopped.Load() || !retries.ready("k") {
		t.Fatalf("after forget: stopped %t, ready %t; want both", second.stopped.Load(), retries.ready("k"))
	}
	retries.failed("k", func() {})
	if again := after.take(t); again.d != 5*time.Second {
		t.Fatalf("wait after a forgotten key fails again = %v, want 5s", again.d)
	}
}

// TestCallRetriesForgetPrefixAndStop checks that forgetPrefix forgets only the keys under its prefix, and that stop
// stops every deadline still armed.
func TestCallRetriesForgetPrefixAndStop(t *testing.T) {
	t.Parallel()
	after := &deadlineClock{armed: make(chan armedDeadline, 16)}
	retries := newCallRetries(after.afterFunc)
	retries.failed("epic-a"+endingSeparator+"done", func() {})
	retries.failed("epic-b"+endingSeparator+"done", func() {})
	a, b := after.take(t), after.take(t)
	retries.forgetPrefix("epic-a" + endingSeparator)
	if !a.stopped.Load() || b.stopped.Load() {
		t.Fatalf("after forgetPrefix of epic-a: a stopped %t, b stopped %t; want only a",
			a.stopped.Load(), b.stopped.Load())
	}
	if !retries.ready("epic-a"+endingSeparator+"done") || retries.ready("epic-b"+endingSeparator+"done") {
		t.Fatal("after forgetPrefix of epic-a, want epic-a ready and epic-b still held")
	}
	retries.stop()
	if !b.stopped.Load() {
		t.Fatal("stop left epic-b's deadline armed")
	}
}

// TestSessionTitleFailedCallIsMadeAgainOnItsSchedule checks that a session whose title call keeps failing is called
// for again each time its deadline fires, at 5 seconds, 30 seconds, a minute and then 5 minutes, that a wake within a
// wait makes no call, and that the call that succeeds stores the title and arms no deadline.
func TestSessionTitleFailedCallIsMadeAgainOnItsSchedule(t *testing.T) {
	t.Parallel()
	st := &titleFakeStore{untitled: []store.UntitledSession{{ID: "e-delta", Dir: "/work/delta", FirstPrompt: "p"}}}
	haiku := &scriptedHaiku{failures: 5, reply: json.RawMessage(`{"title":"Delta work"}`)}
	after := &deadlineClock{armed: make(chan armedDeadline, 16)}
	passes := make(chan struct{}, 16)
	titles := NewSessionTitles(SessionTitlesConfig{
		Store: st, Haiku: haiku, NewTimer: (&retryClock{armed: make(chan armedRetry, 8)}).newTimer,
		AfterFunc: after.afterFunc, Passed: func() { passes <- struct{}{} },
	})
	runHousekeep(t, titles.Run)
	<-passes

	titles.Wake("e-delta")
	<-passes
	if got := haiku.count(); got != 1 {
		t.Fatalf("calls after a wake within the first wait = %d, want 1", got)
	}
	for i, want := range []time.Duration{5 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute,
		5 * time.Minute} {
		armed := after.take(t)
		if armed.d != want {
			t.Fatalf("wait after failure %d = %v, want %v", i+1, armed.d, want)
		}
		armed.fire()
		<-passes
		if got := haiku.count(); got != i+2 {
			t.Fatalf("calls after deadline %d = %d, want %d", i+1, got, i+2)
		}
	}
	if got := st.titleOf("e-delta"); got != "Delta work" {
		t.Fatalf("title after the call that succeeded = %q, want Delta work", got)
	}
	after.noneArmed(t)
}

// TestEpicSummaryFailedCallIsMadeAgainWhenItsDeadlineFires checks that an epic whose summary call failed is called for
// again when its 5-second deadline fires, and that the call that succeeds stores the summary and arms no deadline.
func TestEpicSummaryFailedCallIsMadeAgainWhenItsDeadlineFires(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	createSummaryEpic(t, st, summaryEpicA, summaryStamp)
	haiku := &scriptedHaiku{failures: 1, reply: json.RawMessage(`{"summary":"Retried summary."}`)}
	after := &deadlineClock{armed: make(chan armedDeadline, 16)}
	passes := make(chan struct{}, 16)
	summaries := NewEpicSummaries(EpicSummariesConfig{
		Store: st, Haiku: haiku, NewTimer: (&retryClock{armed: make(chan armedRetry, 8)}).newTimer,
		AfterFunc: after.afterFunc, Passed: func() { passes <- struct{}{} },
	})
	runHousekeep(t, summaries.Run)
	<-passes

	armed := after.take(t)
	if armed.d != 5*time.Second || storedSummaryOf(t, st, summaryEpicA) != "" {
		t.Fatalf("after the failed call: wait %v, want 5s with the summary still empty", armed.d)
	}
	armed.fire()
	<-passes
	if got := haiku.count(); got != 2 {
		t.Fatalf("calls after the deadline = %d, want 2", got)
	}
	if got := storedSummaryOf(t, st, summaryEpicA); got != "Retried summary." {
		t.Fatalf("summary after the retried call = %q, want Retried summary.", got)
	}
	after.noneArmed(t)
}

// TestEpicReportFailedCallIsMadeAgainWhenItsDeadlineFires checks that an ended epic whose report call failed is called
// for again when its 5-second deadline fires, and that the call that succeeds stores the report and arms no deadline.
func TestEpicReportFailedCallIsMadeAgainWhenItsDeadlineFires(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	createReportEpic(t, st, reportEpicA, "released")
	haiku := &scriptedHaiku{failures: 1, reply: json.RawMessage(`{"why":"Retried why."}`)}
	after := &deadlineClock{armed: make(chan armedDeadline, 16)}
	passes := make(chan struct{}, 16)
	reports := NewEpicReports(EpicReportsConfig{
		Store: st, Haiku: haiku, NewTimer: (&retryClock{armed: make(chan armedRetry, 8)}).newTimer,
		AfterFunc: after.afterFunc, Passed: func() { passes <- struct{}{} },
	})
	runHousekeep(t, reports.Run)
	<-passes

	armed := after.take(t)
	if armed.d != 5*time.Second || storedReportOf(t, st, reportEpicA).Why != "" {
		t.Fatalf("after the failed call: wait %v, want 5s with no report stored", armed.d)
	}
	armed.fire()
	<-passes
	if got := haiku.count(); got != 2 {
		t.Fatalf("calls after the deadline = %d, want 2", got)
	}
	if got := storedReportOf(t, st, reportEpicA).Why; got != "Retried why." {
		t.Fatalf("report why after the retried call = %q, want Retried why.", got)
	}
	after.noneArmed(t)
}
