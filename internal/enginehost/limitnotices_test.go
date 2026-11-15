package enginehost

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
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

// tellCall is one TellLimit call a fakeTold received.
type tellCall struct {
	limit string
	steps []string
}

// fakeTold is a LimitTold that answers reads from marks. Its first failReads reads fail. Each write fails with writeErr
// when it is set, and is recorded either way. Marks are not changed by writes, since a check reads them only at start.
type fakeTold struct {
	marks     map[string][]string
	failReads int
	writeErr  error
	tells     []tellCall
	forgets   []string
}

func (f *fakeTold) ToldLimits(context.Context) (map[string][]string, error) {
	if f.failReads > 0 {
		f.failReads--
		return nil, errors.New("store is closed")
	}
	return f.marks, nil
}

func (f *fakeTold) TellLimit(_ context.Context, limit string, steps []string) error {
	f.tells = append(f.tells, tellCall{limit: limit, steps: steps})
	return f.writeErr
}

func (f *fakeTold) ForgetLimit(_ context.Context, limit string) error {
	f.forgets = append(f.forgets, limit)
	return f.writeErr
}

// runLimitNotices runs LimitNotices over told and reads. The first read is the one at start, and each later read is one
// tick. It returns once every tick has been taken and processed, with what the router was told and what was logged.
func runLimitNotices(t *testing.T, told *fakeTold, reads ...limitRead) (*fakeRouter, string) {
	t.Helper()
	router := &fakeRouter{}
	var logs bytes.Buffer
	ticks := make(chan time.Time)
	limits := NewLimitNotices(LimitNoticesConfig{
		Steps:  &scriptedLimits{reads: reads},
		Told:   told,
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

// checkTells fails the test unless told received exactly the TellLimit calls want, in order.
func checkTells(t *testing.T, told *fakeTold, want ...tellCall) {
	t.Helper()
	if !slices.EqualFunc(told.tells, want, func(a, b tellCall) bool {
		return a.limit == b.limit && slices.Equal(a.steps, b.steps)
	}) {
		t.Fatalf("tells = %+v, want %+v", told.tells, want)
	}
}

func TestLimitNoticesNamesEveryStepOfOneLimitInOneNotice(t *testing.T) {
	t.Parallel()
	router, _ := runLimitNotices(t, &fakeTold{},
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
	told := &fakeTold{}
	router, _ := runLimitNotices(t, told,
		limitRead{},
		limitRead{rows: []store.StepRow{usageRow("a"), usageRow("b")}},
		limitRead{rows: []store.StepRow{usageRow("a"), usageRow("b"), usageRow("c")}},
	)

	if len(router.notices) != 2 {
		t.Fatalf("got %d notices, want 2: %+v", len(router.notices), router.notices)
	}
	checkLimitNotice(t, router.notices[1], resetKey(),
		"3 steps held by the usage limit, reset 2026-11-14T17:00:00Z: a, b, c")
	checkTells(t, told,
		tellCall{limit: resetKey().Limit, steps: []string{"a", "b"}},
		tellCall{limit: resetKey().Limit, steps: []string{"a", "b", "c"}},
	)
}

func TestLimitNoticesSecondResetIsSecondNotice(t *testing.T) {
	t.Parallel()
	router, _ := runLimitNotices(t, &fakeTold{},
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
	router, _ := runLimitNotices(t, &fakeTold{},
		limitRead{},
		limitRead{rows: []store.StepRow{unknown}},
	)

	if len(router.notices) != 1 {
		t.Fatalf("got %d notices, want 1: %+v", len(router.notices), router.notices)
	}
	checkLimitNotice(t, router.notices[0], notice.Key{Limit: "usage@0"},
		"1 step held by the usage limit, reset unknown: a")
}

func TestLimitNoticesTellsStepsLimitedAtStartOnceAndMarksThem(t *testing.T) {
	t.Parallel()
	steps := []store.StepRow{usageRow("a"), usageRow("b")}
	told := &fakeTold{}
	router, _ := runLimitNotices(t, told,
		limitRead{rows: steps},
		limitRead{rows: steps},
		limitRead{rows: steps},
	)

	if len(router.notices) != 1 {
		t.Fatalf("got %d notices, want 1 for steps unmarked at start: %+v", len(router.notices), router.notices)
	}
	checkLimitNotice(t, router.notices[0], resetKey(),
		"2 steps held by the usage limit, reset 2026-11-14T17:00:00Z: a, b")
	checkTells(t, told, tellCall{limit: resetKey().Limit, steps: []string{"a", "b"}})
	if len(router.seens) != 0 {
		t.Fatalf("got seen reports %+v, want none", router.seens)
	}
}

func TestLimitNoticesSendsNothingForStepsMarkedBeforeStart(t *testing.T) {
	t.Parallel()
	steps := []store.StepRow{usageRow("a"), usageRow("b")}
	told := &fakeTold{marks: map[string][]string{resetKey().Limit: {"a", "b"}}}
	router, _ := runLimitNotices(t, told,
		limitRead{rows: steps},
		limitRead{rows: steps},
		limitRead{rows: steps},
	)

	if len(router.notices) != 0 || len(router.seens) != 0 {
		t.Fatalf("notices %+v seens %+v, want none for steps marked as told", router.notices, router.seens)
	}
	checkTells(t, told)
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
			told := &fakeTold{marks: map[string][]string{resetKey().Limit: {"a", "b"}}}
			router, _ := runLimitNotices(t, told,
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
	told := &fakeTold{marks: map[string][]string{resetKey().Limit: {"a", "b"}}}
	router, _ := runLimitNotices(t, told,
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
	if !slices.Equal(told.forgets, []string{resetKey().Limit}) {
		t.Fatalf("forgot %v, want the emptied limit %q", told.forgets, resetKey().Limit)
	}
	checkTells(t, told, tellCall{limit: otherResetKey().Limit, steps: []string{"c"}})
}

func TestLimitNoticesSeesAMarkedGroupThatEmptiesOnceAndForgetsIt(t *testing.T) {
	t.Parallel()
	told := &fakeTold{marks: map[string][]string{resetKey().Limit: {"a"}}}
	router, _ := runLimitNotices(t, told,
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
	if !slices.Equal(told.forgets, []string{resetKey().Limit}) {
		t.Fatalf("forgot %v, want %q once", told.forgets, resetKey().Limit)
	}
}

func TestLimitNoticesFailedReadIsLoggedAndSkipped(t *testing.T) {
	t.Parallel()
	told := &fakeTold{marks: map[string][]string{resetKey().Limit: {"a", "b"}}}
	router, logs := runLimitNotices(t, told,
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

func TestLimitNoticesFailedMarkReadReportsNothingUntilALaterRead(t *testing.T) {
	t.Parallel()
	// The start check makes no step read while the marks are unread, so the one tick's read is the first step read, and
	// it holds a and b. Had the start check reported, a and b would be told at start, and the tick's a, b, c would be a
	// second notice.
	told := &fakeTold{failReads: 1}
	router, logs := runLimitNotices(t, told,
		limitRead{rows: []store.StepRow{usageRow("a"), usageRow("b")}},
		limitRead{rows: []store.StepRow{usageRow("a"), usageRow("b"), usageRow("c")}},
	)

	if !strings.Contains(logs, "store is closed") {
		t.Fatalf("log = %q, want the failed mark read logged", logs)
	}
	if len(router.notices) != 1 {
		t.Fatalf("got %d notices, want 1: %+v", len(router.notices), router.notices)
	}
	checkLimitNotice(t, router.notices[0], resetKey(),
		"2 steps held by the usage limit, reset 2026-11-14T17:00:00Z: a, b")
}

func TestLimitNoticesLogsAFailedMarkWriteAndStillTells(t *testing.T) {
	t.Parallel()
	told := &fakeTold{writeErr: errors.New("store is read-only")}
	router, logs := runLimitNotices(t, told,
		limitRead{},
		limitRead{rows: []store.StepRow{usageRow("a"), usageRow("b")}},
	)

	if len(router.notices) != 1 {
		t.Fatalf("got %d notices, want 1: %+v", len(router.notices), router.notices)
	}
	if !strings.Contains(logs, "store is read-only") {
		t.Fatalf("log = %q, want the failed mark write logged", logs)
	}
}

func TestLimitNoticesLogsAFailedForgetWrite(t *testing.T) {
	t.Parallel()
	told := &fakeTold{
		marks:    map[string][]string{resetKey().Limit: {"a"}},
		writeErr: errors.New("store is read-only"),
	}
	router, logs := runLimitNotices(t, told,
		limitRead{rows: []store.StepRow{usageRow("a")}},
		limitRead{},
	)

	want := seenReport{key: resetKey(), seen: limitNow().Unix()}
	if len(router.seens) != 1 || router.seens[0] != want {
		t.Fatalf("seen reports = %+v, want exactly [%+v]", router.seens, want)
	}
	if !strings.Contains(logs, "store is read-only") {
		t.Fatalf("log = %q, want the failed forget write logged", logs)
	}
}

func TestRestoreLimitNoticeRebuildsTheNotice(t *testing.T) {
	t.Parallel()
	unknown := store.StepRow{ID: "c", LoopLimited: true, LoopLimitKind: "usage"}
	cases := []struct {
		name string
		rows []store.StepRow
		key  notice.Key
		body string
	}{
		{
			name: "known reset",
			rows: []store.StepRow{usageRow("a"), usageRow("b")},
			key:  resetKey(),
			body: "2 steps held by the usage limit, reset 2026-11-14T17:00:00Z: a, b",
		},
		{
			name: "unknown reset",
			rows: []store.StepRow{unknown},
			key:  notice.Key{Limit: "usage@0"},
			body: "1 step held by the usage limit, reset unknown: c",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			router, _ := runLimitNotices(t, &fakeTold{}, limitRead{}, limitRead{rows: tc.rows})
			if len(router.notices) != 1 {
				t.Fatalf("got %d notices, want 1: %+v", len(router.notices), router.notices)
			}
			told := router.notices[0]
			checkLimitNotice(t, told, tc.key, tc.body)
			if told.Source.Kind != LimitSource {
				t.Fatalf("source kind = %q, want %q", told.Source.Kind, LimitSource)
			}

			restored, err := RestoreLimitNotice(t.Context(), told.Source.Data, limitNow().Unix())
			if err != nil {
				t.Fatalf("restore limit notice: %v", err)
			}
			checkLimitNotice(t, restored, tc.key, tc.body)
		})
	}
}
