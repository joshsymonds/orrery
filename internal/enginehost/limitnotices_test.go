package enginehost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

// limitTitles returns the contract titles of the epics the limit tests hold steps under, by label. usageRow holds the
// step with id under the label epic-<id>, so the steps a and b are held under Alpha and Beta.
func limitTitles() map[string]string {
	return map[string]string{"epic-a": "Alpha", "epic-b": "Beta", "epic-c": "Gamma", "epic-d": "Delta"}
}

// usageRow is an open step row limited by usage until limitReset, held under the epic epic-<id>.
func usageRow(id string) store.StepRow {
	return store.StepRow{
		ID: id, Epic: "epic-" + id, LoopLimited: true, LoopLimitKind: "usage", LoopLimitResets: limitReset(),
	}
}

// laterUsageRow is an open step row limited by usage until limitOtherReset, held under the epic epic-<id>.
func laterUsageRow(id string) store.StepRow {
	return store.StepRow{
		ID: id, Epic: "epic-" + id, LoopLimited: true, LoopLimitKind: "usage", LoopLimitResets: limitOtherReset(),
	}
}

// limitRead is one answer to a read of the limited steps: its rows, or the error it failed with.
type limitRead struct {
	rows []store.StepRow
	err  error
}

// scriptedLimits is a LimitedStepReader that answers each read with the next of its reads. Once they run out, it
// repeats the last. Each read sends on reading first, so a test waits for the check that reads to have begun.
type scriptedLimits struct {
	reads   []limitRead
	next    int
	reading chan struct{}
}

func (s *scriptedLimits) LimitedSteps(context.Context) ([]store.StepRow, error) {
	s.reading <- struct{}{}
	read := s.reads[min(s.next, len(s.reads)-1)]
	s.next++
	return read.rows, read.err
}

// unfiredTimer is the NewTimer of the tests that never fire a retry: each timer it arms never sends.
func unfiredTimer(time.Duration) (<-chan time.Time, func() bool) {
	return make(chan time.Time), func() bool { return true }
}

// fakeEpics is a LimitEpicReader over titles: a label it does not hold is ErrNoEpic, and a held one is an epic whose
// contract title is its title. Its first failReads reads fail.
type fakeEpics struct {
	titles    map[string]string
	failReads int
}

func (f *fakeEpics) Epic(_ context.Context, id string) (store.EpicRow, error) {
	if f.failReads > 0 {
		f.failReads--
		return store.EpicRow{}, errors.New("store is closed")
	}
	title, ok := f.titles[id]
	if !ok {
		return store.EpicRow{}, fmt.Errorf("epic %s: %w", id, store.ErrNoEpic)
	}
	return store.EpicRow{ID: id, Title: title}, nil
}

// tellCall is one TellLimit call a fakeTold received.
type tellCall struct {
	limit string
	names []string
}

// fakeTold is a LimitTold that answers reads from marks. Its first failReads reads fail. Each write fails with writeErr
// when it is set, and each of its first failWrites writes fails as a busy store refuses it. A write is recorded either
// way. Marks are not changed by writes, since a check reads them only at start.
type fakeTold struct {
	marks      map[string][]string
	failReads  int
	failWrites int
	writeErr   error
	tells      []tellCall
	forgets    []string
}

// writeResult is the error of the write fakeTold is making: a refusal while failWrites lasts, else writeErr.
func (f *fakeTold) writeResult() error {
	if f.failWrites > 0 {
		f.failWrites--
		return errors.New("database is locked")
	}
	return f.writeErr
}

func (f *fakeTold) ToldLimits(context.Context) (map[string][]string, error) {
	if f.failReads > 0 {
		f.failReads--
		return nil, errors.New("store is closed")
	}
	return f.marks, nil
}

// TellLimit records the mark. Like the store, it refuses a write on an ended context, which then records nothing.
func (f *fakeTold) TellLimit(ctx context.Context, limit string, names []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.tells = append(f.tells, tellCall{limit: limit, names: names})
	return f.writeResult()
}

// ForgetLimit records the forget. Like the store, it refuses a write on an ended context, which then records nothing.
func (f *fakeTold) ForgetLimit(ctx context.Context, limit string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.forgets = append(f.forgets, limit)
	return f.writeResult()
}

// stoppingRouter is a fakeRouter that ends the LimitNotices context once it is told a notice or a seen report, as a
// stop of orreryd landing right after the router took it.
type stoppingRouter struct {
	fakeRouter
	stop context.CancelFunc
}

func (s *stoppingRouter) Notify(ctx context.Context, n route.Notice) {
	s.fakeRouter.Notify(ctx, n)
	s.stop()
}

func (s *stoppingRouter) Seen(ctx context.Context, key notice.Key, seen int64) {
	s.fakeRouter.Seen(ctx, key, seen)
	s.stop()
}

// runStoppedLimitNotices runs LimitNotices over told and one read of rows, with a router that stops it at its first
// notice or seen report. It returns what the router was told.
func runStoppedLimitNotices(t *testing.T, told *fakeTold, rows []store.StepRow) *stoppingRouter {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	router := &stoppingRouter{stop: cancel}
	NewLimitNotices(LimitNoticesConfig{
		Steps:    &scriptedLimits{reads: []limitRead{{rows: rows}}, reading: make(chan struct{}, 1)},
		Epics:    &fakeEpics{titles: limitTitles()},
		Told:     told,
		Route:    router,
		Now:      limitNow,
		NewTimer: unfiredTimer,
	}).Run(ctx)
	return router
}

// runLimitNotices runs LimitNotices over told, the epics of limitTitles, and reads. The first read is the one at start,
// and each later read is one wake. It returns once every wake has been taken and processed, with what the router was
// told and what was logged.
func runLimitNotices(t *testing.T, told *fakeTold, reads ...limitRead) (*fakeRouter, string) {
	t.Helper()
	return runLimitNoticesWith(t, told, &fakeEpics{titles: limitTitles()}, reads...)
}

// runLimitNoticesWith is runLimitNotices with epics as the reader of the epics the steps are held under.
func runLimitNoticesWith(t *testing.T, told *fakeTold, epics *fakeEpics, reads ...limitRead) (*fakeRouter, string) {
	t.Helper()
	router := &fakeRouter{}
	var logs bytes.Buffer
	steps := &scriptedLimits{reads: reads, reading: make(chan struct{}, len(reads))}
	// The check at start reads the steps unless the marks read fails, and each wake's check reads them. So the reads
	// that happen are the reads less the marks reads that fail, which are all at start.
	reading := len(reads) - told.failReads
	limits := NewLimitNotices(LimitNoticesConfig{
		Steps:    steps,
		Epics:    epics,
		Told:     told,
		Route:    router,
		Now:      limitNow,
		NewTimer: unfiredTimer,
		Logger:   slog.New(slog.NewTextHandler(&logs, nil)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		limits.Run(ctx)
	}()
	for range len(reads) - 1 {
		// Wake would absorb a wake that comes while one is pending, so the test sends on the wake channel, which waits
		// for the pending wake to be taken. Each wake is then taken by its own check.
		limits.wakes <- struct{}{}
	}
	// Each check that reads the steps has begun once its read is received. The last check began after every wake was
	// taken, so once the reads are in, no wake is pending and cancel ends Run after its last check.
	for range reading {
		<-steps.reading
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
		return a.limit == b.limit && slices.Equal(a.names, b.names)
	}) {
		t.Fatalf("tells = %+v, want %+v", told.tells, want)
	}
}

func TestLimitNoticesRenotifiesWhenAnEpicJoins(t *testing.T) {
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
		"Held by the usage limit, reset 2026-11-14T17:00:00Z: Alpha, Beta, Gamma")
	checkTells(t, told,
		tellCall{limit: resetKey().Limit, names: []string{"Alpha", "Beta"}},
		tellCall{limit: resetKey().Limit, names: []string{"Alpha", "Beta", "Gamma"}},
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
		"Held by the usage limit, reset 2026-11-14T17:00:00Z: Alpha, Beta")
	checkLimitNotice(t, router.notices[1], otherResetKey(),
		"Held by the usage limit, reset 2026-11-15T09:00:00Z: Delta")
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
		"Held by the usage limit, reset unknown: a step outside any epic")
}

func TestLimitNoticesTellsEpicsLimitedAtStartOnceAndMarksThem(t *testing.T) {
	t.Parallel()
	steps := []store.StepRow{usageRow("a"), usageRow("b")}
	told := &fakeTold{}
	router, _ := runLimitNotices(t, told,
		limitRead{rows: steps},
		limitRead{rows: steps},
		limitRead{rows: steps},
	)

	if len(router.notices) != 1 {
		t.Fatalf("got %d notices, want 1 for epics unmarked at start: %+v", len(router.notices), router.notices)
	}
	checkLimitNotice(t, router.notices[0], resetKey(),
		"Held by the usage limit, reset 2026-11-14T17:00:00Z: Alpha, Beta")
	checkTells(t, told, tellCall{limit: resetKey().Limit, names: []string{"Alpha", "Beta"}})
	if len(router.seens) != 0 {
		t.Fatalf("got seen reports %+v, want none", router.seens)
	}
}

func TestLimitNoticesSendsNothingForEpicsMarkedBeforeStart(t *testing.T) {
	t.Parallel()
	steps := []store.StepRow{usageRow("a"), usageRow("b")}
	told := &fakeTold{marks: map[string][]string{resetKey().Limit: {"Alpha", "Beta"}}}
	router, _ := runLimitNotices(t, told,
		limitRead{rows: steps},
		limitRead{rows: steps},
		limitRead{rows: steps},
	)

	if len(router.notices) != 0 || len(router.seens) != 0 {
		t.Fatalf("notices %+v seens %+v, want none for epics marked as told", router.notices, router.seens)
	}
	checkTells(t, told)
}

func TestLimitNoticesReportsAChangedSetOfEpics(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		next []store.StepRow
		body string
	}{
		{
			name: "one epic leaves",
			next: []store.StepRow{usageRow("a")},
			body: "Held by the usage limit, reset 2026-11-14T17:00:00Z: Alpha",
		},
		{
			name: "one epic leaves and another joins",
			next: []store.StepRow{usageRow("a"), usageRow("c")},
			body: "Held by the usage limit, reset 2026-11-14T17:00:00Z: Alpha, Gamma",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			told := &fakeTold{marks: map[string][]string{resetKey().Limit: {"Alpha", "Beta"}}}
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
	told := &fakeTold{marks: map[string][]string{resetKey().Limit: {"Alpha", "Beta"}}}
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
		"Held by the usage limit, reset 2026-11-15T09:00:00Z: Gamma")
	if !slices.Equal(told.forgets, []string{resetKey().Limit}) {
		t.Fatalf("forgot %v, want the emptied limit %q", told.forgets, resetKey().Limit)
	}
	checkTells(t, told, tellCall{limit: otherResetKey().Limit, names: []string{"Gamma"}})
}

// TestLimitNoticesTellsALimitAgainAfterAResume checks that an epic told on a limit, then resumed at its reset, and then
// limited again is told again: the resume empties the limit's group, which is seen and forgotten, so the next limit
// has no mark, whether it resets later or at the same time.
func TestLimitNoticesTellsALimitAgainAfterAResume(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		again store.StepRow
		key   notice.Key
	}{
		{"a later reset", laterUsageRow("a"), otherResetKey()},
		{"the same reset", usageRow("a"), resetKey()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			told := &fakeTold{}
			router, _ := runLimitNotices(t, told,
				limitRead{rows: []store.StepRow{usageRow("a")}},
				limitRead{},
				limitRead{rows: []store.StepRow{tc.again}},
			)

			if len(router.notices) != 2 {
				t.Fatalf("got %d notices, want 2: the limit before the resume and the one after it",
					len(router.notices))
			}
			if router.notices[0].Key != resetKey() || router.notices[1].Key != tc.key {
				t.Fatalf("notice keys = %+v, %+v, want %+v, %+v",
					router.notices[0].Key, router.notices[1].Key, resetKey(), tc.key)
			}
			want := seenReport{key: resetKey(), seen: limitNow().Unix()}
			if len(router.seens) != 1 || router.seens[0] != want {
				t.Fatalf("seen reports = %+v, want exactly [%+v]", router.seens, want)
			}
			if !slices.Equal(told.forgets, []string{resetKey().Limit}) {
				t.Fatalf("forgot %v, want the resumed limit %q once", told.forgets, resetKey().Limit)
			}
			checkTells(t, told,
				tellCall{limit: resetKey().Limit, names: []string{"Alpha"}},
				tellCall{limit: tc.key.Limit, names: []string{"Alpha"}},
			)
		})
	}
}

func TestLimitNoticesSeesAMarkedGroupThatEmptiesOnceAndForgetsIt(t *testing.T) {
	t.Parallel()
	told := &fakeTold{marks: map[string][]string{resetKey().Limit: {"Alpha"}}}
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

// TestLimitNoticesMarksALimitWhenAStopFollowsItsNotice checks that a limit told just before a stop is still marked,
// so the next start does not tell it again.
func TestLimitNoticesMarksALimitWhenAStopFollowsItsNotice(t *testing.T) {
	t.Parallel()
	told := &fakeTold{}
	router := runStoppedLimitNotices(t, told, []store.StepRow{usageRow("a")})

	if len(router.notices) != 1 {
		t.Fatalf("got %d notices, want 1", len(router.notices))
	}
	checkTells(t, told, tellCall{limit: resetKey().Limit, names: []string{"Alpha"}})
}

// TestLimitNoticesForgetsALimitWhenAStopFollowsItsSeen checks that a marked limit seen just before a stop is still
// forgotten, so the next start does not see it again.
func TestLimitNoticesForgetsALimitWhenAStopFollowsItsSeen(t *testing.T) {
	t.Parallel()
	told := &fakeTold{marks: map[string][]string{resetKey().Limit: {"Alpha"}}}
	router := runStoppedLimitNotices(t, told, nil)

	if len(router.seens) != 1 {
		t.Fatalf("got %d seen reports, want 1", len(router.seens))
	}
	if !slices.Equal(told.forgets, []string{resetKey().Limit}) {
		t.Fatalf("forgot %v, want %q once", told.forgets, resetKey().Limit)
	}
}

func TestLimitNoticesFailedReadIsLoggedAndSkipped(t *testing.T) {
	t.Parallel()
	told := &fakeTold{marks: map[string][]string{resetKey().Limit: {"Alpha", "Beta"}}}
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
	// The start check makes no step read while the marks are unread, so the one wake's read is the first step read, and
	// it holds a and b. Had the start check reported, a and b would be told at start, and the wake's a, b, c would be a
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
		"Held by the usage limit, reset 2026-11-14T17:00:00Z: Alpha, Beta")
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

// TestLimitNoticesWritesARefusedMarkAtTheNextCheck checks that a mark the store refuses is written again at the next
// check, with no second notice, so a restart after that does not tell the limit again.
func TestLimitNoticesWritesARefusedMarkAtTheNextCheck(t *testing.T) {
	t.Parallel()
	told := &fakeTold{failWrites: 1}
	rows := []store.StepRow{usageRow("a"), usageRow("b")}
	router, logs := runLimitNotices(t, told, limitRead{rows: rows}, limitRead{rows: rows}, limitRead{rows: rows})

	if len(router.notices) != 1 {
		t.Fatalf("got %d notices, want 1: %+v", len(router.notices), router.notices)
	}
	mark := tellCall{limit: resetKey().Limit, names: []string{"Alpha", "Beta"}}
	checkTells(t, told, mark, mark)
	if !strings.Contains(logs, "database is locked") {
		t.Fatalf("log = %q, want the refused mark write logged", logs)
	}
}

// TestLimitNoticesWritesARefusedForgetAtTheNextCheck checks that a forget the store refuses is written again at the
// next check, with no second seen report.
func TestLimitNoticesWritesARefusedForgetAtTheNextCheck(t *testing.T) {
	t.Parallel()
	told := &fakeTold{marks: map[string][]string{resetKey().Limit: {"Alpha"}}, failWrites: 1}
	router, _ := runLimitNotices(t, told,
		limitRead{rows: []store.StepRow{usageRow("a")}},
		limitRead{},
		limitRead{},
		limitRead{},
	)

	if len(router.seens) != 1 {
		t.Fatalf("seen reports = %+v, want exactly one", router.seens)
	}
	if want := []string{resetKey().Limit, resetKey().Limit}; !slices.Equal(told.forgets, want) {
		t.Fatalf("forgets = %q, want %q: the refused forget and its stored retry", told.forgets, want)
	}
}

func TestLimitNoticesLogsAFailedForgetWrite(t *testing.T) {
	t.Parallel()
	told := &fakeTold{
		marks:    map[string][]string{resetKey().Limit: {"Alpha"}},
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
			body: "Held by the usage limit, reset 2026-11-14T17:00:00Z: Alpha, Beta",
		},
		{
			name: "unknown reset",
			rows: []store.StepRow{unknown},
			key:  notice.Key{Limit: "usage@0"},
			body: "Held by the usage limit, reset unknown: a step outside any epic",
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

// TestRestoreLimitNoticeRejectsANoticeNamingNothing checks that a stored limit notice with no names, as a notice stored
// before the names were kept holds, is not restored: no limit holds nothing, so such a body would name nothing.
func TestRestoreLimitNoticeRejectsANoticeNamingNothing(t *testing.T) {
	t.Parallel()
	data := []byte(`{"kind":"usage","resets":"2026-11-14T17:00:00Z","ids":["a"]}`)
	if _, err := RestoreLimitNotice(t.Context(), data, limitNow().Unix()); err == nil {
		t.Fatal("restore of a limit notice with no names succeeded, want an error")
	}
}
