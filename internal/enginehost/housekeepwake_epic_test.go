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

// errHousekeepRefusal is the error a refused write returns, as a busy store's write does.
var errHousekeepRefusal = errors.New("begin store write: database is locked (5) (SQLITE_BUSY)")

// housekeepCounts counts the reads and writes a counting store is asked for. Its first write refuses when refuseFirst
// is set.
type housekeepCounts struct {
	fullReads   atomic.Int32
	epicReads   atomic.Int32
	writes      atomic.Int32
	refuseFirst bool
}

// refuses counts a write and reports whether the store refuses it: the first one does when refuseFirst is set.
func (c *housekeepCounts) refuses() bool {
	return c.writes.Add(1) == 1 && c.refuseFirst
}

// housekeepSummaryStore is the EpicSummaryStore of the summary tests: a store that counts its reads and writes.
type housekeepSummaryStore struct {
	*store.Store
	housekeepCounts
}

// UnsummarizedEpics counts a full read, then reads as the store does.
func (s *housekeepSummaryStore) UnsummarizedEpics(ctx context.Context) ([]store.EpicRow, error) {
	s.fullReads.Add(1)
	return s.Store.UnsummarizedEpics(ctx)
}

// Epic counts a by-id read, then reads as the store does.
func (s *housekeepSummaryStore) Epic(ctx context.Context, id string) (store.EpicRow, error) {
	s.epicReads.Add(1)
	return s.Store.Epic(ctx, id)
}

// SetEpicSummary counts a write, then refuses it or writes as the store does.
func (s *housekeepSummaryStore) SetEpicSummary(ctx context.Context, id, summary string) error {
	if s.refuses() {
		return errHousekeepRefusal
	}
	return s.Store.SetEpicSummary(ctx, id, summary)
}

// housekeepReportStore is the EpicReportStore of the report tests: a store that counts its reads and writes.
type housekeepReportStore struct {
	*store.Store
	housekeepCounts
}

// UnreportedEpics counts a full read, then reads as the store does.
func (s *housekeepReportStore) UnreportedEpics(ctx context.Context) ([]store.EpicRow, error) {
	s.fullReads.Add(1)
	return s.Store.UnreportedEpics(ctx)
}

// UnreportedEpic counts a by-id read, then reads as the store does.
func (s *housekeepReportStore) UnreportedEpic(ctx context.Context, id string) (store.EpicRow, bool, error) {
	s.epicReads.Add(1)
	return s.Store.UnreportedEpic(ctx, id)
}

// SetEpicReport counts a write, then refuses it or writes as the store does.
func (s *housekeepReportStore) SetEpicReport(ctx context.Context, id string, report store.EpicReport) error {
	if s.refuses() {
		return errHousekeepRefusal
	}
	return s.Store.SetEpicReport(ctx, id, report)
}

// housekeepHaiku is the haikuCaller of the housekeeping tests. It counts its calls and answers each with reply.
type housekeepHaiku struct {
	reply json.RawMessage
	calls atomic.Int32
}

// Call counts the call and answers with the reply.
func (h *housekeepHaiku) Call(context.Context, string, string) (json.RawMessage, error) {
	h.calls.Add(1)
	return h.reply, nil
}

// runHousekeep runs run on its own goroutine and waits for it to return at cleanup.
func runHousekeep(t *testing.T, run func(ctx context.Context)) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// TestHousekeepWakeEpicSummariesReadNothingWithoutAWrite checks that with the clock advanced hours and no write, the
// summaries read the store no further times than the start pass. The run is in a synctest bubble with the default
// timer, so a period or a ticker in Run fires as the clock advances three hours.
func TestHousekeepWakeEpicSummariesReadNothingWithoutAWrite(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	createSummaryEpic(t, st, summaryEpicA, summaryStamp)
	if err := st.SetEpicSummary(t.Context(), summaryEpicA, "Synthetic summary."); err != nil {
		t.Fatalf("SetEpicSummary: %v", err)
	}
	synctest.Test(t, func(t *testing.T) {
		counted := &housekeepSummaryStore{Store: st}
		haiku := &housekeepHaiku{reply: json.RawMessage(`{"summary":"Housekeep summary."}`)}
		summaries := NewEpicSummaries(EpicSummariesConfig{Store: counted, Haiku: haiku})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		defer func() {
			cancel()
			<-done
		}()
		go func() {
			defer close(done)
			summaries.Run(ctx)
		}()

		synctest.Wait()
		if got := counted.fullReads.Load(); got != 1 {
			t.Fatalf("full reads at start = %d, want 1", got)
		}
		<-time.After(3 * time.Hour)
		synctest.Wait()
		if got := counted.fullReads.Load(); got != 1 {
			t.Fatalf("full reads after the clock advanced hours with no write = %d, want 1", got)
		}
		if got := counted.epicReads.Load(); got != 0 {
			t.Fatalf("by-id reads after the clock advanced hours with no write = %d, want 0", got)
		}
		if got := haiku.calls.Load(); got != 0 {
			t.Fatalf("Haiku calls after the clock advanced hours with no write = %d, want 0", got)
		}
	})
}

// TestHousekeepWakeEpicReportsReadNothingWithoutAWrite checks that with the clock advanced hours and no write, the
// reports read the store no further times than the start pass. The run is in a synctest bubble with the default timer,
// so a period or a ticker in Run fires as the clock advances three hours.
func TestHousekeepWakeEpicReportsReadNothingWithoutAWrite(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	createReportEpic(t, st, reportEpicA, "accepted")
	synctest.Test(t, func(t *testing.T) {
		counted := &housekeepReportStore{Store: st}
		haiku := &housekeepHaiku{reply: json.RawMessage(`{"why":"Housekeep why."}`)}
		reports := NewEpicReports(EpicReportsConfig{Store: counted, Haiku: haiku})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		defer func() {
			cancel()
			<-done
		}()
		go func() {
			defer close(done)
			reports.Run(ctx)
		}()

		synctest.Wait()
		if got := counted.fullReads.Load(); got != 1 {
			t.Fatalf("full reads at start = %d, want 1", got)
		}
		<-time.After(3 * time.Hour)
		synctest.Wait()
		if got := counted.fullReads.Load(); got != 1 {
			t.Fatalf("full reads after the clock advanced hours with no write = %d, want 1", got)
		}
		if got := counted.epicReads.Load(); got != 0 {
			t.Fatalf("by-id reads after the clock advanced hours with no write = %d, want 0", got)
		}
		if got := haiku.calls.Load(); got != 0 {
			t.Fatalf("Haiku calls after the clock advanced hours with no write = %d, want 0", got)
		}
	})
}

// TestHousekeepWakeEpicSummariesSummarizeTheStoredEpic checks that an epic the store stores is summarized by the
// store's hook alone: the hook wakes the summaries, which read the epic by id once and call Haiku once, and the start
// pass made the only full read.
func TestHousekeepWakeEpicSummariesSummarizeTheStoredEpic(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	counted := &housekeepSummaryStore{Store: st}
	haiku := &housekeepHaiku{reply: json.RawMessage(`{"summary":"Housekeep summary."}`)}
	passes := make(chan struct{}, 16)
	summaries := NewEpicSummaries(EpicSummariesConfig{
		Store: counted, Haiku: haiku, NewTimer: unfiredTimer, Passed: func() { passes <- struct{}{} },
	})
	st.OnEpicStored(summaries.Wake)
	runHousekeep(t, summaries.Run)
	<-passes

	createSummaryEpic(t, st, summaryEpicA, summaryStamp)
	<-passes
	if got := counted.epicReads.Load(); got != 1 {
		t.Fatalf("by-id reads after the store stored the epic = %d, want 1", got)
	}
	if got := haiku.calls.Load(); got != 1 {
		t.Fatalf("Haiku calls = %d, want 1", got)
	}
	if got := counted.fullReads.Load(); got != 1 {
		t.Fatalf("full reads = %d, want 1: the start pass's", got)
	}
	if got := storedSummaryOf(t, st, summaryEpicA); got != "Housekeep summary." {
		t.Fatalf("summary after the store stored the epic = %q, want the call's", got)
	}
}

// TestHousekeepWakeEpicReportsReportTheEndedEpic checks that an epic the store ends is reported by the store's hook
// alone: the hook wakes the reports, which read the ending by id once and call Haiku once, and the start pass made the
// only full read.
func TestHousekeepWakeEpicReportsReportTheEndedEpic(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	counted := &housekeepReportStore{Store: st}
	haiku := &housekeepHaiku{reply: json.RawMessage(`{"why":"Housekeep why."}`)}
	passes := make(chan struct{}, 16)
	reports := NewEpicReports(EpicReportsConfig{
		Store: counted, Haiku: haiku, NewTimer: unfiredTimer, Passed: func() { passes <- struct{}{} },
	})
	st.OnEpicEnded(reports.Wake)
	runHousekeep(t, reports.Run)
	<-passes

	createReportEpic(t, st, reportEpicA, "accepted")
	if _, err := st.CloseEpic(
		t.Context(), reportEpicA, "shipped", "", store.CloserPlanner, store.Maker{By: store.MakerOrreryd},
		time.Unix(reportStamp+60, 0),
	); err != nil {
		t.Fatalf("CloseEpic: %v", err)
	}
	<-passes
	if got := counted.epicReads.Load(); got != 1 {
		t.Fatalf("by-id reads after the store ended the epic = %d, want 1", got)
	}
	if got := haiku.calls.Load(); got != 1 {
		t.Fatalf("Haiku calls = %d, want 1", got)
	}
	if got := counted.fullReads.Load(); got != 1 {
		t.Fatalf("full reads = %d, want 1: the start pass's", got)
	}
	if got := storedReportOf(t, st, reportEpicA).Phase; got != store.PhaseClosedShipped {
		t.Fatalf("report phase after the store ended the epic = %q, want %q", got, store.PhaseClosedShipped)
	}
}

// TestHousekeepDeadlineRefusedSummaryStoredWhenRetryFires checks that a summary the store refuses is kept and its retry
// is armed for housekeepRetry. A wake before the retry fires writes nothing. When the retry fires, the kept summary is
// written once, with no second call, and that write arms no further retry.
func TestHousekeepDeadlineRefusedSummaryStoredWhenRetryFires(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	createSummaryEpic(t, st, summaryEpicA, summaryStamp)
	counted := &housekeepSummaryStore{Store: st, housekeepCounts: housekeepCounts{refuseFirst: true}}
	haiku := &housekeepHaiku{reply: json.RawMessage(`{"summary":"Housekeep summary."}`)}
	clock := &retryClock{armed: make(chan armedRetry, 8)}
	passes := make(chan struct{}, 16)
	summaries := NewEpicSummaries(EpicSummariesConfig{
		Store: counted, Haiku: haiku, NewTimer: clock.newTimer, Passed: func() { passes <- struct{}{} },
	})
	runHousekeep(t, summaries.Run)
	<-passes
	retry := <-clock.armed
	if retry.d != housekeepRetry {
		t.Fatalf("retry armed for %v, want %v", retry.d, housekeepRetry)
	}
	if got := storedSummaryOf(t, st, summaryEpicA); got != "" {
		t.Fatalf("summary after a refused write = %q, want empty", got)
	}

	summaries.Wake(summaryEpicA)
	<-passes
	if got := counted.writes.Load(); got != 1 {
		t.Fatalf("writes before the retry fires = %d, want 1: the kept summary waits for the timer", got)
	}
	if got := haiku.calls.Load(); got != 1 {
		t.Fatalf("Haiku calls before the retry fires = %d, want 1", got)
	}

	retry.fire <- time.Time{}
	<-passes
	if got := counted.writes.Load(); got != 2 {
		t.Fatalf("writes after the retry fires = %d, want 2", got)
	}
	if got := storedSummaryOf(t, st, summaryEpicA); got != "Housekeep summary." {
		t.Fatalf("summary after the retry fires = %q, want the call's", got)
	}
	if got := haiku.calls.Load(); got != 1 {
		t.Fatalf("Haiku calls after the retry fires = %d, want still 1", got)
	}

	summaries.Wake(summaryEpicA)
	<-passes
	clock.noRetryArmed(t)
}

// TestHousekeepDeadlineRefusedReportStoredWhenRetryFires checks that a report the store refuses is kept, and that the
// retry is armed for housekeepRetry. A wake before the retry fires writes nothing. When the retry fires the kept report
// is written once, with no second call, and the write arms no further retry.
func TestHousekeepDeadlineRefusedReportStoredWhenRetryFires(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	createReportEpic(t, st, reportEpicA, "released")
	counted := &housekeepReportStore{Store: st, housekeepCounts: housekeepCounts{refuseFirst: true}}
	haiku := &housekeepHaiku{reply: json.RawMessage(`{"why":"Housekeep why."}`)}
	clock := &retryClock{armed: make(chan armedRetry, 8)}
	passes := make(chan struct{}, 16)
	reports := NewEpicReports(EpicReportsConfig{
		Store: counted, Haiku: haiku, NewTimer: clock.newTimer, Passed: func() { passes <- struct{}{} },
	})
	runHousekeep(t, reports.Run)
	<-passes
	retry := <-clock.armed
	if retry.d != housekeepRetry {
		t.Fatalf("retry armed for %v, want %v", retry.d, housekeepRetry)
	}
	if got := storedReportOf(t, st, reportEpicA).Phase; got != "" {
		t.Fatalf("report phase after a refused write = %q, want empty", got)
	}

	reports.Wake(reportEpicA)
	<-passes
	if got := counted.writes.Load(); got != 1 {
		t.Fatalf("writes before the retry fires = %d, want 1: the kept report waits for the timer", got)
	}
	if got := haiku.calls.Load(); got != 1 {
		t.Fatalf("Haiku calls before the retry fires = %d, want 1", got)
	}

	retry.fire <- time.Time{}
	<-passes
	if got := counted.writes.Load(); got != 2 {
		t.Fatalf("writes after the retry fires = %d, want 2", got)
	}
	if got := storedReportOf(t, st, reportEpicA).Phase; got != "released" {
		t.Fatalf("report phase after the retry fires = %q, want released", got)
	}
	if got := haiku.calls.Load(); got != 1 {
		t.Fatalf("Haiku calls after the retry fires = %d, want still 1", got)
	}

	reports.Wake(reportEpicA)
	<-passes
	clock.noRetryArmed(t)
}
