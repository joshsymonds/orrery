package enginehost

import (
	"context"
	"encoding/json"
	"errors"
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

// LimitEpicReader reads the stored epic with an id, so a limit notice can name the epic by its contract title. It returns
// an error matching store.ErrNoEpic for an id no epic has. *store.Store satisfies it.
type LimitEpicReader interface {
	Epic(ctx context.Context, id string) (store.EpicRow, error)
}

// LimitTold holds the marks of the usage limits told: for each limit, the sorted names its last notice gave. A limit with
// no marked name is absent from ToldLimits. *store.Store satisfies it.
type LimitTold interface {
	ToldLimits(ctx context.Context) (map[string][]string, error)
	TellLimit(ctx context.Context, limit string, names []string) error
	ForgetLimit(ctx context.Context, limit string) error
}

// LimitNoticesConfig configures LimitNotices. Steps reads the limited steps, Epics reads the epic each of them is held
// under, Told holds the marks of the limits told, Route receives the notices, Now is the clock, Tick is the channel each
// check waits on, and Logger logs a failed read or write. A nil Logger discards.
type LimitNoticesConfig struct {
	Steps  LimitedStepReader
	Epics  LimitEpicReader
	Told   LimitTold
	Route  Router
	Now    func() time.Time
	Tick   <-chan time.Time
	Logger *slog.Logger
}

// LimitSource is the source kind of a limit notice. Its data is the limitData of the group it tells.
const LimitSource = "limit"

// limitNoEpicName is the name a held step with no epic is given.
const limitNoEpicName = "a step outside any epic"

// limitData is what a limit notice stores to be rebuilt: its group's kind, reset and the names its body gives.
type limitData struct {
	Kind   string    `json:"kind"`
	Resets time.Time `json:"resets"`
	Names  []string  `json:"names"`
}

// RestoreLimitNotice rebuilds the notice that a stored row of LimitSource describes, from the limitData in data. It is
// the route.Rebuild for LimitSource, and it returns the notice the check builds for the same group. It ignores at, the
// row's recorded time: a limit notice never ends by it. A stored notice with no names is one no limit holds, so it is
// not restored.
func RestoreLimitNotice(_ context.Context, data []byte, _ int64) (route.Notice, error) {
	var stored limitData
	if err := json.Unmarshal(data, &stored); err != nil {
		return route.Notice{}, fmt.Errorf("%s notice: %w", LimitSource, err)
	}
	if len(stored.Names) == 0 {
		return route.Notice{}, fmt.Errorf("%s notice: no held name", LimitSource)
	}
	return limitNotice(&limitGroup{
		key:    limitKeyOf(stored.Kind, stored.Resets),
		kind:   stored.Kind,
		resets: stored.Resets,
		names:  stored.Names,
	}), nil
}

// LimitNotices tells the owner once per usage limit which epics are waiting on it. It keeps a mark per limit of the names
// its last notice gave, in the store, so a restart tells neither a limit told before nor one twice.
type LimitNotices struct {
	cfg LimitNoticesConfig
	// known holds, for each limit with a mark, the names the mark gives, sorted. It is read from the store once and kept
	// as each check changes it.
	known map[notice.Key][]string
	// read is set once the marks have been read. Until then a check reads them and reports nothing.
	read bool
	// unstored holds each change to a mark that the store refused, by limit. Each check writes them again before it
	// reads the steps, and a change the store takes leaves the map. known already holds the change.
	unstored map[notice.Key]markChange
}

// markChange is one write to a limit's mark: forget removes the mark, and otherwise the mark gives names.
type markChange struct {
	names  []string
	forget bool
}

// NewLimitNotices returns a LimitNotices for cfg.
func NewLimitNotices(cfg LimitNoticesConfig) *LimitNotices {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &LimitNotices{
		cfg: cfg, known: make(map[notice.Key][]string), unstored: make(map[notice.Key]markChange),
	}
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

// limitGroup is the open limited steps that share one kind and reset. names holds the name each of them is held under,
// once per name and sorted: the contract title of its epic, its label when no stored epic has it, or limitNoEpicName.
type limitGroup struct {
	key    notice.Key
	kind   string
	resets time.Time
	names  []string
}

// check reads the marks until they are read, writes the mark changes the store refused before, then reads the limited
// steps and the epics they are held under, and reports the changes since the last check. A failed read is logged, and
// the check changes nothing else.
func (l *LimitNotices) check(ctx context.Context) {
	if !l.read && !l.readMarks(ctx) {
		return
	}
	markCtx := context.WithoutCancel(ctx)
	for _, key := range sortedLimitKeys(l.unstored) {
		l.storeMark(markCtx, key, l.unstored[key])
	}
	rows, err := l.cfg.Steps.LimitedSteps(ctx)
	if err != nil {
		l.cfg.Logger.ErrorContext(ctx, "read limited steps", "error", err)
		return
	}
	groups, err := l.limitGroups(ctx, rows)
	if err != nil {
		l.cfg.Logger.ErrorContext(ctx, "read epics of limited steps", "error", err)
		return
	}
	l.update(ctx, groups)
}

// readMarks reads the marks of the limits told into known, and reports whether it did. A failed read is logged.
func (l *LimitNotices) readMarks(ctx context.Context) bool {
	marks, err := l.cfg.Told.ToldLimits(ctx)
	if err != nil {
		l.cfg.Logger.ErrorContext(ctx, "read told limits", "error", err)
		return false
	}
	for limit, names := range marks {
		l.known[notice.Key{Limit: limit}] = slices.Sorted(slices.Values(names))
	}
	l.read = true
	return true
}

// limitGroups groups rows by kind and reset, and names each group's steps by the epic they are held under. It returns an
// error when an epic read fails other than by finding no epic.
func (l *LimitNotices) limitGroups(ctx context.Context, rows []store.StepRow) (map[notice.Key]*limitGroup, error) {
	groups := make(map[notice.Key]*limitGroup)
	titles := make(map[string]string)
	for _, row := range rows {
		key := limitKeyOf(row.LoopLimitKind, row.LoopLimitResets)
		group, ok := groups[key]
		if !ok {
			group = &limitGroup{key: key, kind: row.LoopLimitKind, resets: row.LoopLimitResets}
			groups[key] = group
		}
		name, err := l.heldName(ctx, row.Epic, titles)
		if err != nil {
			return nil, err
		}
		group.names = append(group.names, name)
	}
	for _, group := range groups {
		slices.Sort(group.names)
		group.names = slices.Compact(group.names)
	}
	return groups, nil
}

// heldName is the name a step held under epic is given: limitNoEpicName for a step with no epic, else the contract title
// of the epic stored under that id. The label stands in for the title when no epic has the id or the stored title is
// blank. titles holds the names this check has already read, by label.
func (l *LimitNotices) heldName(ctx context.Context, epic string, titles map[string]string) (string, error) {
	if epic == "" {
		return limitNoEpicName, nil
	}
	if name, ok := titles[epic]; ok {
		return name, nil
	}
	name := epic
	row, err := l.cfg.Epics.Epic(ctx, epic)
	switch {
	case err == nil:
		if row.Title != "" {
			name = row.Title
		}
	case !errors.Is(err, store.ErrNoEpic):
		return "", fmt.Errorf("read epic %s: %w", epic, err)
	}
	titles[epic] = name
	return name, nil
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

// update brings the marks to groups. A group whose names differ from its mark is told as one notice, and marked with its
// names. A marked limit with no group has emptied: it is seen, and its mark is forgotten. The Router takes a notice
// whatever ctx's cancellation, so the mark writes ignore it too: a stop landing after a notice still marks it, and the
// next start does not tell it again. The mark is kept in memory either way, so the check does not tell or see the
// limit again, and a write the store refuses is logged and written again at each later check until it is stored.
func (l *LimitNotices) update(ctx context.Context, groups map[notice.Key]*limitGroup) {
	markCtx := context.WithoutCancel(ctx)
	for _, key := range sortedLimitKeys(groups) {
		group := groups[key]
		if slices.Equal(l.known[key], group.names) {
			continue
		}
		l.cfg.Route.Notify(ctx, limitRouted(group))
		l.known[key] = group.names
		l.storeMark(markCtx, key, markChange{names: group.names})
	}
	now := l.cfg.Now().Unix()
	for _, key := range sortedLimitKeys(l.known) {
		if _, open := groups[key]; open {
			continue
		}
		delete(l.known, key)
		l.cfg.Route.Seen(ctx, key, now)
		l.storeMark(markCtx, key, markChange{forget: true})
	}
}

// storeMark writes change to key's mark. A write the store refuses is logged and kept in unstored, replacing any
// earlier change of the same limit, and a stored write leaves unstored.
func (l *LimitNotices) storeMark(ctx context.Context, key notice.Key, change markChange) {
	var err error
	if change.forget {
		if err = l.cfg.Told.ForgetLimit(ctx, key.Limit); err != nil {
			l.cfg.Logger.ErrorContext(ctx, "forget limit told", "limit", key.Limit, "error", err)
		}
	} else if err = l.cfg.Told.TellLimit(ctx, key.Limit, change.names); err != nil {
		l.cfg.Logger.ErrorContext(ctx, "mark limit told", "limit", key.Limit, "error", err)
	}
	if err != nil {
		l.unstored[key] = change
		return
	}
	delete(l.unstored, key)
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
	if data, err := json.Marshal(limitData{Kind: group.kind, Resets: group.resets, Names: group.names}); err == nil {
		n.Source = route.Source{Kind: LimitSource, Data: data}
	}
	return n
}

// limitBody names the group's held epics, its kind and its reset in RFC 3339 UTC or "unknown". It names no step.
func limitBody(group *limitGroup) string {
	reset := "unknown"
	if !group.resets.IsZero() {
		reset = group.resets.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("Held by the %s limit, reset %s: %s", group.kind, reset, strings.Join(group.names, ", "))
}
