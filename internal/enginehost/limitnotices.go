package enginehost

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// LimitedStepReader reads the open step rows whose loop is limited. *store.Store satisfies it.
type LimitedStepReader interface {
	LimitedSteps(ctx context.Context) ([]store.StepRow, error)
}

// LimitTold holds the marks of the usage limits told: for each limit, the sorted ids of the steps its last notice
// named. A limit with no marked step is absent from ToldLimits. *store.Store satisfies it.
type LimitTold interface {
	ToldLimits(ctx context.Context) (map[string][]string, error)
	TellLimit(ctx context.Context, limit string, steps []string) error
	ForgetLimit(ctx context.Context, limit string) error
}

// LimitNoticesConfig configures LimitNotices. Steps reads the limited steps, Told holds the marks of the limits told,
// Route receives the notices, Now is the clock, Tick is the channel each check waits on, and Logger logs a failed read
// or write. A nil Logger discards.
type LimitNoticesConfig struct {
	Steps  LimitedStepReader
	Told   LimitTold
	Route  Router
	Now    func() time.Time
	Tick   <-chan time.Time
	Logger *slog.Logger
}

// LimitSource is the source kind of a limit notice. Its data is the limitData of the group it tells.
const LimitSource = "limit"

// limitData is what a limit notice stores to be rebuilt: its group's kind, reset and step ids in row order.
type limitData struct {
	Kind   string    `json:"kind"`
	Resets time.Time `json:"resets"`
	IDs    []string  `json:"ids"`
}

// RestoreLimitNotice rebuilds the notice that a stored row of LimitSource describes, from the limitData in data. It is
// the route.Rebuild for LimitSource, and it returns the notice the check builds for the same group. It ignores at, the
// row's recorded time: a limit notice never ends by it.
func RestoreLimitNotice(_ context.Context, data []byte, _ int64) (route.Notice, error) {
	var stored limitData
	if err := json.Unmarshal(data, &stored); err != nil {
		return route.Notice{}, fmt.Errorf("%s notice: %w", LimitSource, err)
	}
	return limitNotice(&limitGroup{
		key:    limitKeyOf(stored.Kind, stored.Resets),
		kind:   stored.Kind,
		resets: stored.Resets,
		ids:    stored.IDs,
	}), nil
}

// LimitNotices tells the owner once per usage limit which steps are waiting on it. It keeps a mark per limit of the
// step ids it last told, in the store, so a restart tells neither a limit told before nor one twice.
type LimitNotices struct {
	cfg LimitNoticesConfig
	// known holds, for each limit with a mark, the ids the mark names, sorted. It is read from the store once and kept
	// as each check changes it.
	known map[notice.Key][]string
	// read is set once the marks have been read. Until then a check reads them and reports nothing.
	read bool
}

// NewLimitNotices returns a LimitNotices for cfg.
func NewLimitNotices(cfg LimitNoticesConfig) *LimitNotices {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &LimitNotices{cfg: cfg, known: make(map[notice.Key][]string)}
}

// Run checks the limited steps at start and at each tick until ctx ends. The check at start reads the marks first, so
// a limit told before a restart is not told again. A failed read of the marks is retried at the next check.
func (l *LimitNotices) Run(ctx context.Context) {
	l.check(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-l.cfg.Tick:
			l.check(ctx)
		}
	}
}

// limitState is the state of a limit notice, on the desk and the phone.
const limitState = "limited"

// limitTitle is the desk title and the push project of a limit notice.
const limitTitle = "usage limit"

// limitGroup is the open limited steps that share one kind and reset, in the order the store returned them.
type limitGroup struct {
	key    notice.Key
	kind   string
	resets time.Time
	ids    []string
}

// check reads the marks until they are read, then the limited steps, and reports the changes since the last check. A
// failed read is logged, and the check changes nothing.
func (l *LimitNotices) check(ctx context.Context) {
	if !l.read && !l.readMarks(ctx) {
		return
	}
	rows, err := l.cfg.Steps.LimitedSteps(ctx)
	if err != nil {
		l.cfg.Logger.ErrorContext(ctx, "read limited steps", "error", err)
		return
	}
	l.update(ctx, limitGroups(rows))
}

// readMarks reads the marks of the limits told into known, and reports whether it did. A failed read is logged.
func (l *LimitNotices) readMarks(ctx context.Context) bool {
	marks, err := l.cfg.Told.ToldLimits(ctx)
	if err != nil {
		l.cfg.Logger.ErrorContext(ctx, "read told limits", "error", err)
		return false
	}
	for limit, ids := range marks {
		l.known[notice.Key{Limit: limit}] = slices.Sorted(slices.Values(ids))
	}
	l.read = true
	return true
}

// limitGroups groups rows by kind and reset. Each group's ids are in row order.
func limitGroups(rows []store.StepRow) map[notice.Key]*limitGroup {
	groups := make(map[notice.Key]*limitGroup)
	for _, row := range rows {
		key := limitKeyOf(row.LoopLimitKind, row.LoopLimitResets)
		group, ok := groups[key]
		if !ok {
			group = &limitGroup{key: key, kind: row.LoopLimitKind, resets: row.LoopLimitResets}
			groups[key] = group
		}
		group.ids = append(group.ids, row.ID)
	}
	return groups
}

// limitKeyOf is the key of the usage limit of kind that resets at resets.
func limitKeyOf(kind string, resets time.Time) notice.Key {
	return notice.Key{Limit: fmt.Sprintf("%s@%d", kind, resetUnix(resets))}
}

// resetUnix is the reset in unix seconds, or 0 when the reset is unknown.
func resetUnix(resets time.Time) int64 {
	if resets.IsZero() {
		return 0
	}
	return resets.Unix()
}

// update brings the marks to groups. A group whose step ids differ from its mark is told as one notice, and marked with
// its ids. A marked limit with no group has emptied: it is seen, and its mark is forgotten. A failed write of a mark is
// logged, and the mark is kept in memory either way, so the check does not tell or see the limit again.
func (l *LimitNotices) update(ctx context.Context, groups map[notice.Key]*limitGroup) {
	for _, key := range sortedLimitKeys(groups) {
		group := groups[key]
		ids := slices.Sorted(slices.Values(group.ids))
		if slices.Equal(l.known[key], ids) {
			continue
		}
		l.cfg.Route.Notify(ctx, limitRouted(group))
		l.known[key] = ids
		if err := l.cfg.Told.TellLimit(ctx, key.Limit, ids); err != nil {
			l.cfg.Logger.ErrorContext(ctx, "mark limit told", "limit", key.Limit, "error", err)
		}
	}
	now := l.cfg.Now().Unix()
	for _, key := range sortedLimitKeys(l.known) {
		if _, open := groups[key]; open {
			continue
		}
		delete(l.known, key)
		l.cfg.Route.Seen(ctx, key, now)
		if err := l.cfg.Told.ForgetLimit(ctx, key.Limit); err != nil {
			l.cfg.Logger.ErrorContext(ctx, "forget limit told", "limit", key.Limit, "error", err)
		}
	}
}

// sortedLimitKeys returns the keys of m in order of their limit names.
func sortedLimitKeys[V any](m map[notice.Key]V) []notice.Key {
	return slices.SortedFunc(maps.Keys(m), func(a, b notice.Key) int {
		return strings.Compare(a.Limit, b.Limit)
	})
}

// limitNotice is the notice that reports group.
func limitNotice(group *limitGroup) route.Notice {
	body := limitBody(group)
	return route.Notice{
		Key:   group.key,
		State: limitState,
		Desk: func(context.Context) (deskstream.Notice, error) {
			return deskstream.Notice{State: limitState, Title: limitTitle, Body: body}, nil
		},
		Push: func(context.Context) (push.Notification, error) {
			return push.Notification{
				Project: limitTitle,
				State:   protocol.State(limitState),
				Summary: body,
			}, nil
		},
	}
}

// limitRouted is limitNotice for group with the Source that restores it after a restart. The data is strings and a
// time, which marshal unless the reset lies outside years 0 to 9999; then the notice routes with no row to restore it
// from.
func limitRouted(group *limitGroup) route.Notice {
	n := limitNotice(group)
	if data, err := json.Marshal(limitData{Kind: group.kind, Resets: group.resets, IDs: group.ids}); err == nil {
		n.Source = route.Source{Kind: LimitSource, Data: data}
	}
	return n
}

// limitBody names the count of the group's steps, its kind, its reset in RFC 3339 UTC or "unknown", and its step ids.
func limitBody(group *limitGroup) string {
	count := fmt.Sprintf("%d steps", len(group.ids))
	if len(group.ids) == 1 {
		count = "1 step"
	}
	reset := "unknown"
	if !group.resets.IsZero() {
		reset = group.resets.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("%s held by the %s limit, reset %s: %s", count, group.kind, reset, strings.Join(group.ids, ", "))
}
