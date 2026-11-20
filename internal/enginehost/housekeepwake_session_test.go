package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic values only. The engine session and its events are invented.
const (
	sessionTestID    = "housekeep-session"
	sessionTestDir   = "/work/synthetic-session"
	sessionTestStamp = 1_700_000_000
)

// errHousekeepRead is the error a read the busy store refuses returns.
var errHousekeepRead = errors.New("begin store read: database is locked (5) (SQLITE_BUSY)")

// housekeepTitleStore is the SessionTitleStore of the title tests: a store that counts its full reads, its by-id reads
// and its writes. Its first write refuses when refuseFirst is set, and its first by-id read fails when failFirstIDRead
// is set.
type housekeepTitleStore struct {
	*store.Store
	housekeepCounts
	idReads         atomic.Int32
	failFirstIDRead bool
}

// UntitledSessions counts a full read, then reads as the store does.
func (s *housekeepTitleStore) UntitledSessions(ctx context.Context) ([]store.UntitledSession, error) {
	s.fullReads.Add(1)
	return s.Store.UntitledSessions(ctx)
}

// UntitledSession counts a by-id read, then fails the first one when failFirstIDRead is set, or reads as the store does.
func (s *housekeepTitleStore) UntitledSession(ctx context.Context, id string) (store.UntitledSession, bool, error) {
	if s.idReads.Add(1) == 1 && s.failFirstIDRead {
		return store.UntitledSession{}, false, errHousekeepRead
	}
	return s.Store.UntitledSession(ctx, id)
}

// SetSessionTitle counts a write, then refuses it or writes as the store does.
func (s *housekeepTitleStore) SetSessionTitle(ctx context.Context, id, title string) error {
	if s.refuses() {
		return errHousekeepRefusal
	}
	return s.Store.SetSessionTitle(ctx, id, title)
}

// housekeepHeadlineStore is the SessionHeadlineStore of the headline tests: a store that counts its full reads and its
// by-id reads.
type housekeepHeadlineStore struct {
	*store.Store
	housekeepCounts
	idReads atomic.Int32
}

// HeadlineDueSessions counts a full read, then reads as the store does.
func (s *housekeepHeadlineStore) HeadlineDueSessions(ctx context.Context) ([]store.HeadlineDueSession, error) {
	s.fullReads.Add(1)
	return s.Store.HeadlineDueSessions(ctx)
}

// HeadlineDueSession counts a by-id read, then reads as the store does.
func (s *housekeepHeadlineStore) HeadlineDueSession(
	ctx context.Context, id string,
) (store.HeadlineDueSession, bool, error) {
	s.idReads.Add(1)
	return s.Store.HeadlineDueSession(ctx, id)
}

// applySessionEvents stores the engine session sessionTestID with its first prompt and a turn end, at at.
func applySessionEvents(t *testing.T, st *store.Store, at time.Time) {
	t.Helper()
	if err := st.ApplyEngine(t.Context(), []store.EngineOp{
		store.EngineAdd{ID: sessionTestID, Dir: sessionTestDir, State: "idle"},
		store.EnginePrompt{ID: sessionTestID, Text: "Synthetic first prompt."},
		store.EngineTurnEnded{
			ID: sessionTestID, Subtype: "success", TerminalReason: "completed", Result: "Synthetic result.",
			Origin: "user",
		},
	}, at); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
}

// TestHousekeepWakeSessionTitlesReadNothingWithoutAWrite checks that with the clock advanced hours and no write, the
// titles read the store no further times than the start pass. The run is in a synctest bubble with the default timer,
// so a period or a ticker in Run fires during the three hours' sleep.
func TestHousekeepWakeSessionTitlesReadNothingWithoutAWrite(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	if err := st.ApplyEngine(t.Context(), []store.EngineOp{
		store.EngineAdd{ID: sessionTestID, Dir: sessionTestDir, State: "idle"},
		store.EnginePrompt{ID: sessionTestID, Text: "Synthetic first prompt."},
	}, time.Unix(sessionTestStamp, 0)); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	if err := st.SetSessionTitle(t.Context(), sessionTestID, "Synthetic title."); err != nil {
		t.Fatalf("SetSessionTitle: %v", err)
	}
	synctest.Test(t, func(t *testing.T) {
		counted := &housekeepTitleStore{Store: st}
		haiku := &housekeepHaiku{reply: json.RawMessage(`{"title":"Housekeep title."}`)}
		titles := NewSessionTitles(SessionTitlesConfig{Store: counted, Haiku: haiku})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		defer func() {
			cancel()
			<-done
		}()
		go func() {
			defer close(done)
			titles.Run(ctx)
		}()

		synctest.Wait()
		if got := counted.fullReads.Load(); got != 1 {
			t.Fatalf("full reads at start = %d, want 1", got)
		}
		time.Sleep(3 * time.Hour)
		synctest.Wait()
		if got := counted.fullReads.Load(); got != 1 {
			t.Fatalf("full reads after the clock advanced hours with no write = %d, want 1", got)
		}
		if got := counted.idReads.Load(); got != 0 {
			t.Fatalf("by-id reads after the clock advanced hours with no write = %d, want 0", got)
		}
		if got := haiku.calls.Load(); got != 0 {
			t.Fatalf("Haiku calls after the clock advanced hours with no write = %d, want 0", got)
		}
	})
}

// TestHousekeepWakeSessionHeadlinesReadNothingWithoutAWrite checks that with the clock advanced hours and no write, the
// headlines read the store no further times than the start pass. The run is in a synctest bubble with the default
// timers, so a period or a ticker in Run fires during the three hours' sleep. The session's headline is newer than its
// turn end, so no session is due.
func TestHousekeepWakeSessionHeadlinesReadNothingWithoutAWrite(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	applySessionEvents(t, st, time.Unix(sessionTestStamp, 0))
	if err := st.SetSessionHeadline(
		t.Context(), sessionTestID, "Synthetic headline.", time.Unix(sessionTestStamp+60, 0),
	); err != nil {
		t.Fatalf("SetSessionHeadline: %v", err)
	}
	synctest.Test(t, func(t *testing.T) {
		counted := &housekeepHeadlineStore{Store: st}
		haiku := &housekeepHaiku{reply: json.RawMessage(`{"headline":"Housekeep headline."}`)}
		headlines := NewSessionHeadlines(SessionHeadlinesConfig{Store: counted, Haiku: haiku})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		defer func() {
			cancel()
			<-done
		}()
		go func() {
			defer close(done)
			headlines.Run(ctx)
		}()

		synctest.Wait()
		if got := counted.fullReads.Load(); got != 1 {
			t.Fatalf("full reads at start = %d, want 1", got)
		}
		time.Sleep(3 * time.Hour)
		synctest.Wait()
		if got := counted.fullReads.Load(); got != 1 {
			t.Fatalf("full reads after the clock advanced hours with no write = %d, want 1", got)
		}
		if got := counted.idReads.Load(); got != 0 {
			t.Fatalf("by-id reads after the clock advanced hours with no write = %d, want 0", got)
		}
		if got := haiku.calls.Load(); got != 0 {
			t.Fatalf("Haiku calls after the clock advanced hours with no write = %d, want 0", got)
		}
	})
}

// TestHousekeepWakeSessionTitlesTitleTheFirstPrompt checks that an engine session whose first prompt the store stores is
// titled by the store's hook alone: the hook wakes the titles, which read the session by id once and call Haiku once, and
// the start pass made the only full read.
func TestHousekeepWakeSessionTitlesTitleTheFirstPrompt(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	counted := &housekeepTitleStore{Store: st}
	haiku := &housekeepHaiku{reply: json.RawMessage(`{"title":"Housekeep title."}`)}
	passes := make(chan struct{}, 16)
	titles := NewSessionTitles(SessionTitlesConfig{
		Store: counted, Haiku: haiku, NewTimer: unfiredTimer, Passed: func() { passes <- struct{}{} },
	})
	st.OnFirstPrompt(titles.Wake)
	runHousekeep(t, titles.Run)
	<-passes

	applySessionEvents(t, st, time.Unix(sessionTestStamp, 0))
	<-passes
	if got := counted.idReads.Load(); got != 1 {
		t.Fatalf("by-id reads after the store stored the first prompt = %d, want 1", got)
	}
	if got := haiku.calls.Load(); got != 1 {
		t.Fatalf("Haiku calls = %d, want 1", got)
	}
	if got := counted.fullReads.Load(); got != 1 {
		t.Fatalf("full reads = %d, want 1: the start pass's", got)
	}
	if _, untitled, err := st.UntitledSession(t.Context(), sessionTestID); err != nil || untitled {
		t.Fatalf("UntitledSession after the title = %t, %v; want the title stored", untitled, err)
	}
}

// TestHousekeepWakeSessionHeadlinesHeadlineTheEndedTurn checks that an engine session whose turn the store ends is
// headlined by the store's hook alone: the hook wakes the headlines, which read the session by id once and call Haiku
// once, and the start pass made the only full read. No deadline is armed, since the session is not held or inside its
// window.
func TestHousekeepWakeSessionHeadlinesHeadlineTheEndedTurn(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	counted := &housekeepHeadlineStore{Store: st}
	haiku := &housekeepHaiku{reply: json.RawMessage(`{"headline":"Housekeep headline."}`)}
	passes := make(chan struct{}, 16)
	deadlines := &deadlineClock{armed: make(chan armedDeadline, 16)}
	headlines := NewSessionHeadlines(SessionHeadlinesConfig{
		Store:     counted,
		Haiku:     haiku,
		NewTimer:  unfiredTimer,
		AfterFunc: deadlines.afterFunc,
		Passed:    func() { passes <- struct{}{} },
	})
	st.OnTurnEnded(headlines.Wake)
	runHousekeep(t, headlines.Run)
	<-passes

	applySessionEvents(t, st, time.Unix(sessionTestStamp, 0))
	<-passes
	if got := counted.idReads.Load(); got != 1 {
		t.Fatalf("by-id reads after the store ended the turn = %d, want 1", got)
	}
	if got := haiku.calls.Load(); got != 1 {
		t.Fatalf("Haiku calls = %d, want 1", got)
	}
	if got := counted.fullReads.Load(); got != 1 {
		t.Fatalf("full reads = %d, want 1: the start pass's", got)
	}
	deadlines.noneArmed(t)
	if _, due, err := st.HeadlineDueSession(t.Context(), sessionTestID); err != nil || due {
		t.Fatalf("HeadlineDueSession after the headline = %t, %v; want the headline stored", due, err)
	}
}

// TestHousekeepDeadlineHeadlineHeldUntilItsIntervalPasses checks that a session headlined at T whose turn ends at T+1
// minute is woken and not called: its deadline is armed for the 4 minutes left in its window, and once the clock reaches
// T+5 minutes and the deadline fires, the session is called once.
func TestHousekeepDeadlineHeadlineHeldUntilItsIntervalPasses(t *testing.T) {
	t.Parallel()
	start := headlineStart()
	st := newHeadlineFakeStore()
	st.addSession("e-held", "p")
	st.endTurn("e-held", start, store.RecentTurn{Role: "assistant", Text: "first"})
	if err := st.SetSessionHeadline(t.Context(), "e-held", "Headlined", start); err != nil {
		t.Fatalf("SetSessionHeadline: %v", err)
	}
	haiku := &headlineFakeHaiku{reply: json.RawMessage(`{"headline":"Held headline."}`)}
	loop := startHeadlineLoop(t, st, haiku, start)

	st.endTurn("e-held", start.Add(time.Minute), store.RecentTurn{Role: "assistant", Text: "second"})
	loop.wakeAt(start.Add(time.Minute), "e-held")
	if got := haiku.calls(); got != 0 {
		t.Fatalf("calls inside the window = %d, want 0", got)
	}
	dl := loop.deadlines.take(t)
	if dl.d != 4*time.Minute {
		t.Fatalf("deadline armed for %v, want the 4 minutes left in the window", dl.d)
	}
	loop.deadlines.noneArmed(t)

	loop.fire(dl, start.Add(5*time.Minute))
	if got := haiku.calls(); got != 1 {
		t.Fatalf("calls once the deadline fires = %d, want 1", got)
	}
	if got := st.headlineOf("e-held"); got != "Held headline." {
		t.Fatalf("headline once the deadline fires = %q, want the reply", got)
	}
}

// TestHousekeepDeadlineFailedHeadlineCalledAgainWhenItsHoldEnds checks that a failed headline call arms a deadline of
// sessionHeadlineInterval. A wake before it fires makes no second call, and when it fires while the session is still due
// the session is called exactly once more.
func TestHousekeepDeadlineFailedHeadlineCalledAgainWhenItsHoldEnds(t *testing.T) {
	t.Parallel()
	start := headlineStart()
	st := newHeadlineFakeStore()
	st.addSession("e-failed", "p")
	st.endTurn("e-failed", start, store.RecentTurn{Role: "assistant", Text: "first"})
	haiku := &headlineFakeHaiku{err: errors.New("claude exited 3")}
	loop := startHeadlineLoop(t, st, haiku, start)
	if got := haiku.calls(); got != 1 {
		t.Fatalf("calls after the failed call = %d, want 1", got)
	}
	dl := loop.deadlines.take(t)
	if dl.d != sessionHeadlineInterval {
		t.Fatalf("deadline armed for %v after the failed call, want %v", dl.d, sessionHeadlineInterval)
	}

	loop.wakeAt(start.Add(time.Minute), "e-failed")
	if got := haiku.calls(); got != 1 {
		t.Fatalf("calls before the deadline fires = %d, want still 1", got)
	}

	haiku.answer(json.RawMessage(`{"headline":"Called again."}`))
	loop.fire(dl, start.Add(sessionHeadlineInterval))
	if got := haiku.calls(); got != 2 {
		t.Fatalf("calls once the deadline fires = %d, want exactly one more", got)
	}
	if got := st.headlineOf("e-failed"); got != "Called again." {
		t.Fatalf("headline once the deadline fires = %q, want the reply", got)
	}
}

// TestHousekeepDeadlineRefusedTitleStoredWhenRetryFires checks that a title the store refuses is kept and its retry is
// armed for housekeepRetry. A pass before the retry fires writes nothing. When the retry fires, the kept title is written
// once, with no second call, and that write arms no further retry.
func TestHousekeepDeadlineRefusedTitleStoredWhenRetryFires(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	applySessionEvents(t, st, time.Unix(sessionTestStamp, 0))
	counted := &housekeepTitleStore{Store: st, housekeepCounts: housekeepCounts{refuseFirst: true}}
	haiku := &housekeepHaiku{reply: json.RawMessage(`{"title":"Housekeep title."}`)}
	clock := &retryClock{armed: make(chan armedRetry, 8)}
	passes := make(chan struct{}, 16)
	titles := NewSessionTitles(SessionTitlesConfig{
		Store: counted, Haiku: haiku, NewTimer: clock.newTimer, Passed: func() { passes <- struct{}{} },
	})
	runHousekeep(t, titles.Run)
	<-passes
	retry := <-clock.armed
	if retry.d != housekeepRetry {
		t.Fatalf("retry armed for %v, want %v", retry.d, housekeepRetry)
	}
	if _, untitled, err := st.UntitledSession(t.Context(), sessionTestID); err != nil || !untitled {
		t.Fatalf("UntitledSession after a refused write = %t, %v; want still untitled", untitled, err)
	}

	titles.Wake(sessionTestID)
	<-passes
	if got := counted.writes.Load(); got != 1 {
		t.Fatalf("writes before the retry fires = %d, want 1: the kept title waits for the timer", got)
	}
	if got := haiku.calls.Load(); got != 1 {
		t.Fatalf("Haiku calls before the retry fires = %d, want 1", got)
	}

	retry.fire <- time.Time{}
	<-passes
	if got := counted.writes.Load(); got != 2 {
		t.Fatalf("writes after the retry fires = %d, want 2", got)
	}
	if _, untitled, err := st.UntitledSession(t.Context(), sessionTestID); err != nil || untitled {
		t.Fatalf("UntitledSession after the retry fires = %t, %v; want the title stored", untitled, err)
	}
	if got := haiku.calls.Load(); got != 1 {
		t.Fatalf("Haiku calls after the retry fires = %d, want still 1", got)
	}

	titles.Wake(sessionTestID)
	<-passes
	clock.noRetryArmed(t)
}

// TestHousekeepDeadlineFailedSessionReadIsReadAgainWhenRetryFires checks that a session whose by-id read fails on a wake
// is kept, and that the retry reads it again: the failed read makes no call, and the retry titles the session once.
func TestHousekeepDeadlineFailedSessionReadIsReadAgainWhenRetryFires(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	counted := &housekeepTitleStore{Store: st, failFirstIDRead: true}
	haiku := &housekeepHaiku{reply: json.RawMessage(`{"title":"Housekeep title."}`)}
	clock := &retryClock{armed: make(chan armedRetry, 8)}
	passes := make(chan struct{}, 16)
	titles := NewSessionTitles(SessionTitlesConfig{
		Store: counted, Haiku: haiku, NewTimer: clock.newTimer, Passed: func() { passes <- struct{}{} },
	})
	st.OnFirstPrompt(titles.Wake)
	runHousekeep(t, titles.Run)
	<-passes
	clock.noRetryArmed(t)

	applySessionEvents(t, st, time.Unix(sessionTestStamp, 0))
	<-passes
	if got := haiku.calls.Load(); got != 0 {
		t.Fatalf("Haiku calls after the failed read = %d, want 0", got)
	}
	retry := <-clock.armed
	if retry.d != housekeepRetry {
		t.Fatalf("retry armed for %v, want %v", retry.d, housekeepRetry)
	}

	retry.fire <- time.Time{}
	<-passes
	if got := counted.idReads.Load(); got != 2 {
		t.Fatalf("by-id reads after the retry fires = %d, want 2: the failed one and the retry's", got)
	}
	if got := haiku.calls.Load(); got != 1 {
		t.Fatalf("Haiku calls after the retry fires = %d, want 1", got)
	}
	if _, untitled, err := st.UntitledSession(t.Context(), sessionTestID); err != nil || untitled {
		t.Fatalf("UntitledSession after the retry fires = %t, %v; want the title stored", untitled, err)
	}
}
