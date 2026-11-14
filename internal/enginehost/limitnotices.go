package enginehost

import (
	"context"
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

// LimitNoticesConfig configures LimitNotices. Steps reads the limited steps, Route receives the notices, Now is the
// clock, Tick is the channel each check waits on, and Logger logs a failed read. A nil Logger discards.
type LimitNoticesConfig struct {
	Steps  LimitedStepReader
	Route  Router
	Now    func() time.Time
	Tick   <-chan time.Time
	Logger *slog.Logger
}

// LimitNotices tells the owner once per usage limit which steps are waiting on it.
type LimitNotices struct {
	cfg LimitNoticesConfig
	// known holds, for each limit with held steps, the ids of those steps as the last report left them, sorted.
	known map[notice.Key][]string
	// remembered is set once the limited steps have been read. Until then a check reads them without reporting.
	remembered bool
}

// NewLimitNotices returns a LimitNotices for cfg.
func NewLimitNotices(cfg LimitNoticesConfig) *LimitNotices {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &LimitNotices{cfg: cfg, known: make(map[notice.Key][]string)}
}

// Run checks the limited steps at start and at each tick until ctx ends. The check at start only remembers the groups
// it reads, so steps already limited when Run starts are not reported. A start read that fails leaves nothing
// remembered, so the first check that reads remembers instead.
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

// check reads the limited steps and reports the changes since the last check. A failed read is logged, and the check
// changes nothing.
func (l *LimitNotices) check(ctx context.Context) {
	rows, err := l.cfg.Steps.LimitedSteps(ctx)
	if err != nil {
		l.cfg.Logger.ErrorContext(ctx, "read limited steps", "error", err)
		return
	}
	l.update(ctx, limitGroups(rows), l.remembered)
	l.remembered = true
}

// limitGroups groups rows by kind and reset. Each group's ids are in row order.
func limitGroups(rows []store.StepRow) map[notice.Key]*limitGroup {
	groups := make(map[notice.Key]*limitGroup)
	for _, row := range rows {
		key := notice.Key{Limit: fmt.Sprintf("%s@%d", row.LoopLimitKind, resetUnix(row.LoopLimitResets))}
		group, ok := groups[key]
		if !ok {
			group = &limitGroup{key: key, kind: row.LoopLimitKind, resets: row.LoopLimitResets}
			groups[key] = group
		}
		group.ids = append(group.ids, row.ID)
	}
	return groups
}

// resetUnix is the reset in unix seconds, or 0 when the reset is unknown.
func resetUnix(resets time.Time) int64 {
	if resets.IsZero() {
		return 0
	}
	return resets.Unix()
}

// update brings known to groups. A group whose step ids differ from the ones known is new, grown or shrunk, and is
// routed as one notice when report is set. A known group that is gone has emptied, and is seen when report is set.
// Either way known then holds groups' ids, so the next update compares against them.
func (l *LimitNotices) update(ctx context.Context, groups map[notice.Key]*limitGroup, report bool) {
	for _, key := range sortedLimitKeys(groups) {
		group := groups[key]
		ids := slices.Sorted(slices.Values(group.ids))
		if slices.Equal(l.known[key], ids) {
			continue
		}
		l.known[key] = ids
		if report {
			l.cfg.Route.Notify(ctx, limitNotice(group))
		}
	}
	now := l.cfg.Now().Unix()
	for _, key := range sortedLimitKeys(l.known) {
		if _, open := groups[key]; open {
			continue
		}
		delete(l.known, key)
		if report {
			l.cfg.Route.Seen(ctx, key, now)
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
