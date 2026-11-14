package enginehost

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// limitNow is the time the LimitNotices clock reads in these tests.
func limitNow() time.Time {
	return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
}

// limitReset is a usage reset after limitNow, and limitOtherReset a second one, later still.
func limitReset() time.Time {
	return time.Date(2026, 11, 14, 17, 0, 0, 0, time.UTC)
}

func limitOtherReset() time.Time {
	return time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
}

// resetKey is the key of the usage limit that resets at limitReset, as LimitNotices names it: 1794675600 is limitReset
// in unix seconds. otherResetKey is the key of the one that resets at limitOtherReset, 1794733200.
func resetKey() notice.Key {
	return notice.Key{Limit: "usage@1794675600"}
}

func otherResetKey() notice.Key {
	return notice.Key{Limit: "usage@1794733200"}
}

// usageRow is an open step row limited by usage until limitReset.
func usageRow(id string) store.StepRow {
	return store.StepRow{ID: id, LoopLimited: true, LoopLimitKind: "usage", LoopLimitResets: limitReset()}
}

// laterUsageRow is an open step row limited by usage until limitOtherReset.
func laterUsageRow(id string) store.StepRow {
	return store.StepRow{ID: id, LoopLimited: true, LoopLimitKind: "usage", LoopLimitResets: limitOtherReset()}
}

// limitRead is one answer to a read of the limited steps: its rows, or the error it failed with.
type limitRead struct {
	rows []store.StepRow
	err  error
}

// scriptedLimits is a LimitedStepReader that answers each read with the next of its reads. Once they run out, it
// repeats the last.
type scriptedLimits struct {
	reads []limitRead
	next  int
}

func (s *scriptedLimits) LimitedSteps(context.Context) ([]store.StepRow, error) {
	read := s.reads[min(s.next, len(s.reads)-1)]
	s.next++
	return read.rows, read.err
}

// runLimitNotices runs LimitNotices over reads. The first read is the one at start, and each later read is one tick.
// It returns once every tick has been taken and processed, with what the router was told and what was logged.
func runLimitNotices(t *testing.T, reads ...limitRead) (*fakeRouter, string) {
	t.Helper()
	router := &fakeRouter{}
	var logs bytes.Buffer
	ticks := make(chan time.Time)
	limits := NewLimitNotices(LimitNoticesConfig{
		Steps:  &scriptedLimits{reads: reads},
		Route:  router,
		Now:    limitNow,
		Tick:   ticks,
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		limits.Run(ctx)
	}()
	for range len(reads) - 1 {
		ticks <- limitNow()
	}
	cancel()
	<-done
	return router, logs.String()
}

// checkLimitNotice fails the test unless n is keyed key, and its desk notice and push both carry body.
func checkLimitNotice(t *testing.T, n route.Notice, key notice.Key, body string) {
	t.Helper()
	if n.Key != key || n.State != "limited" {
		t.Fatalf("notice key %+v state %q, want key %+v state %q", n.Key, n.State, key, "limited")
	}
	desk, err := n.Desk(context.Background())
	if err != nil {
		t.Fatalf("desk notice: %v", err)
	}
	if want := (deskstream.Notice{State: "limited", Title: "usage limit", Body: body}); desk != want {
		t.Fatalf("desk notice = %+v, want %+v", desk, want)
	}
	note, err := n.Push(context.Background())
	if err != nil {
		t.Fatalf("push notification: %v", err)
	}
	want := push.Notification{Project: "usage limit", State: protocol.State("limited"), Summary: body}
	if note != want {
		t.Fatalf("push notification = %+v, want %+v", note, want)
	}
}

func TestLimitNoticesNamesEveryStepOfOneLimitInOneNotice(t *testing.T) {
	t.Parallel()
	router, _ := runLimitNotices(t,
		limitRead{},
		limitRead{rows: []store.StepRow{usageRow("a"), usageRow("b")}},
	)

	if len(router.notices) != 1 {
		t.Fatalf("got %d notices, want 1: %+v", len(router.notices), router.notices)
	}
	checkLimitNotice(t, router.notices[0], resetKey(),
		"2 steps held by the usage limit, reset 2026-11-14T17:00:00Z: a, b")
}

func TestLimitNoticesRenotifiesWhenAStepJoins(t *testing.T) {
	t.Parallel()
	router, _ := runLimitNotices(t,
		limitRead{},
		limitRead{rows: []store.StepRow{usageRow("a"), usageRow("b")}},
		limitRead{rows: []store.StepRow{usageRow("a"), usageRow("b"), usageRow("c")}},
	)

	if len(router.notices) != 2 {
		t.Fatalf("got %d notices, want 2: %+v", len(router.notices), router.notices)
	}
	checkLimitNotice(t, router.notices[1], resetKey(),
		"3 steps held by the usage limit, reset 2026-11-14T17:00:00Z: a, b, c")
}

func TestLimitNoticesSecondResetIsSecondNotice(t *testing.T) {
	t.Parallel()
	router, _ := runLimitNotices(t,
		limitRead{},
		limitRead{rows: []store.StepRow{usageRow("a"), usageRow("b")}},
		limitRead{rows: []store.StepRow{usageRow("a"), usageRow("b"), laterUsageRow("d")}},
	)

	if len(router.notices) != 2 {
		t.Fatalf("got %d notices, want 2 (the first reset is not re-notified): %+v",
			len(router.notices), router.notices)
	}
	checkLimitNotice(t, router.notices[0], resetKey(),
		"2 steps held by the usage limit, reset 2026-11-14T17:00:00Z: a, b")
	checkLimitNotice(t, router.notices[1], otherResetKey(),
		"1 step held by the usage limit, reset 2026-11-15T09:00:00Z: d")
}

func TestLimitNoticesNamesAnUnknownReset(t *testing.T) {
	t.Parallel()
	unknown := store.StepRow{ID: "a", LoopLimited: true, LoopLimitKind: "usage"}
	router, _ := runLimitNotices(t,
		limitRead{},
		limitRead{rows: []store.StepRow{unknown}},
	)

	if len(router.notices) != 1 {
		t.Fatalf("got %d notices, want 1: %+v", len(router.notices), router.notices)
	}
	checkLimitNotice(t, router.notices[0], notice.Key{Limit: "usage@0"},
		"1 step held by the usage limit, reset unknown: a")
}

func TestLimitNoticesStartedOverLimitedStepsSendsNothingUntilTheyChange(t *testing.T) {
	t.Parallel()
	steps := []store.StepRow{usageRow("a"), usageRow("b")}
	router, _ := runLimitNotices(t,
		limitRead{rows: steps},
		limitRead{rows: steps},
		limitRead{rows: steps},
	)

	if len(router.notices) != 0 || len(router.seens) != 0 {
		t.Fatalf("notices %+v seens %+v, want none for steps already limited", router.notices, router.seens)
	}
}

func TestLimitNoticesReportsAChangedMembershipOfTheSameSize(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		next []store.StepRow
		body string
	}{
		{
			name: "one step leaves",
			next: []store.StepRow{usageRow("a")},
			body: "1 step held by the usage limit, reset 2026-11-14T17:00:00Z: a",
		},
		{
			name: "one step leaves and another joins",
			next: []store.StepRow{usageRow("a"), usageRow("c")},
			body: "2 steps held by the usage limit, reset 2026-11-14T17:00:00Z: a, c",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			router, _ := runLimitNotices(t,
				limitRead{rows: []store.StepRow{usageRow("a"), usageRow("b")}},
				limitRead{rows: tc.next},
			)

			if len(router.notices) != 1 {
				t.Fatalf("got %d notices, want 1: %+v", len(router.notices), router.notices)
			}
			checkLimitNotice(t, router.notices[0], resetKey(), tc.body)
			if len(router.seens) != 0 {
				t.Fatalf("got seen reports %+v, want none", router.seens)
			}
		})
	}
}

func TestLimitNoticesSeesAGroupThatEmptiesAndNotifiesANewOne(t *testing.T) {
	t.Parallel()
	router, _ := runLimitNotices(t,
		limitRead{rows: []store.StepRow{usageRow("a"), usageRow("b")}},
		limitRead{rows: []store.StepRow{laterUsageRow("c")}},
	)

	want := seenReport{key: resetKey(), seen: limitNow().Unix()}
	if len(router.seens) != 1 || router.seens[0] != want {
		t.Fatalf("seen reports = %+v, want exactly [%+v]", router.seens, want)
	}
	if len(router.notices) != 1 {
		t.Fatalf("got %d notices, want 1: %+v", len(router.notices), router.notices)
	}
	checkLimitNotice(t, router.notices[0], otherResetKey(),
		"1 step held by the usage limit, reset 2026-11-15T09:00:00Z: c")
}

func TestLimitNoticesSeesAGroupThatEmptiesOnceResumedOrStopped(t *testing.T) {
	t.Parallel()
	router, _ := runLimitNotices(t,
		limitRead{rows: []store.StepRow{usageRow("a")}},
		limitRead{},
		limitRead{},
	)

	if len(router.notices) != 0 {
		t.Fatalf("got notices %+v, want none", router.notices)
	}
	want := seenReport{key: resetKey(), seen: limitNow().Unix()}
	if len(router.seens) != 1 || router.seens[0] != want {
		t.Fatalf("seen reports = %+v, want exactly [%+v]", router.seens, want)
	}
}

func TestLimitNoticesFailedReadIsLoggedAndSkipped(t *testing.T) {
	t.Parallel()
	router, logs := runLimitNotices(t,
		limitRead{rows: []store.StepRow{usageRow("a"), usageRow("b")}},
		limitRead{err: errors.New("store is closed")},
		limitRead{rows: []store.StepRow{usageRow("a"), usageRow("b")}},
		limitRead{},
	)

	if !strings.Contains(logs, "store is closed") {
		t.Fatalf("log = %q, want the failed read logged", logs)
	}
	if len(router.notices) != 0 {
		t.Fatalf("got notices %+v, want none", router.notices)
	}
	want := seenReport{key: resetKey(), seen: limitNow().Unix()}
	if len(router.seens) != 1 || router.seens[0] != want {
		t.Fatalf("seen reports = %+v, want exactly [%+v]: a failed read must not empty a group", router.seens, want)
	}
}

func TestLimitNoticesStartReadThatFailsRemembersOnTheNextRead(t *testing.T) {
	t.Parallel()
	router, _ := runLimitNotices(t,
		limitRead{err: errors.New("store is closed")},
		limitRead{rows: []store.StepRow{usageRow("a"), usageRow("b")}},
		limitRead{rows: []store.StepRow{usageRow("a"), usageRow("b"), usageRow("c")}},
	)

	if len(router.notices) != 1 {
		t.Fatalf("got %d notices, want 1 (the first good read is remembered, not reported): %+v",
			len(router.notices), router.notices)
	}
	checkLimitNotice(t, router.notices[0], resetKey(),
		"3 steps held by the usage limit, reset 2026-11-14T17:00:00Z: a, b, c")
}
