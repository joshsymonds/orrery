package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// EpicSource is the source kind of an epic notice. Its data is the epicNotice JSON that Notify stored.
const EpicSource = "epic"

// turnSucceeded is the state of a turn that returned its plan.
const turnSucceeded = "succeeded"

// attentionNeed is what an epic needs from its owner. Its name is part of the id of the notice the need routes, and
// stored ids outlive the process, so a name does not change.
type attentionNeed string

const (
	needNone      attentionNeed = "none"
	needPark      attentionNeed = "park"
	needLimit     attentionNeed = "limit"
	needQuestions attentionNeed = "questions"
	needTell      attentionNeed = "tell"
)

// epicNotice is the Source data of an epic notice: the id of its cause, the epic, its title and the body the owner
// reads.
type epicNotice struct {
	ID    string `json:"id"`
	Epic  string `json:"epic"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// AttentionStore is the store an Attention reads what each epic needs through, the notice rows it has stored, and
// marks its turns and tells told in. *store.Store satisfies it.
type AttentionStore interface {
	Attention(ctx context.Context) ([]store.EpicAttention, error)
	Notices(ctx context.Context) ([]store.NoticeRow, error)
	MarkTold(ctx context.Context, epic string, seq int64) error
	MarkTellTold(ctx context.Context, epic string, seq int64) error
}

// Router routes notices to the owner. *route.Router satisfies it.
type Router interface {
	Notify(ctx context.Context, n route.Notice)
	Seen(ctx context.Context, key notice.Key, seen int64)
}

// AttentionConfig configures an Attention. A Tick that is not positive means five seconds, and a nil Now means
// time.Now. A nil Logger discards. Ticks, when not nil, is where Run takes its ticks in place of a ticker every Tick.
type AttentionConfig struct {
	Store  AttentionStore
	Router Router
	Tick   time.Duration
	Ticks  <-chan time.Time
	Now    func() time.Time
	Logger *slog.Logger
}

// Attention tells the owner, once per turn, when an epic needs them, and clears the notice of an epic that no longer
// does.
type Attention struct {
	store  AttentionStore
	router Router
	tick   time.Duration
	ticks  <-chan time.Time
	now    func() time.Time
	logger *slog.Logger
}

// NewAttention returns an Attention for cfg.
func NewAttention(cfg AttentionConfig) *Attention {
	attention := &Attention{
		store: cfg.Store, router: cfg.Router, tick: cfg.Tick, ticks: cfg.Ticks, now: cfg.Now, logger: cfg.Logger,
	}
	if attention.tick <= 0 {
		attention.tick = defaultTick
	}
	if attention.now == nil {
		attention.now = time.Now
	}
	if attention.logger == nil {
		attention.logger = slog.New(slog.DiscardHandler)
	}
	return attention
}

// Run passes at once, then once per tick, until ctx ends.
func (a *Attention) Run(ctx context.Context) {
	ticks := a.ticks
	if ticks == nil {
		ticker := time.NewTicker(a.tick)
		defer ticker.Stop()
		ticks = ticker.C
	}
	for {
		a.pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}

// Pass attends to each epic once, as Run does on each tick.
func (a *Attention) Pass(ctx context.Context) {
	a.pass(ctx)
}

// pass attends to each epic that has a turn. It reads the ids of the epic notices stored before it routes anything, so
// a notice routed before a restart is not routed again.
func (a *Attention) pass(ctx context.Context) {
	entries, err := a.store.Attention(ctx)
	if err != nil {
		a.logger.ErrorContext(ctx, "list attention", "err", err)
		return
	}
	stored, err := a.storedIDs(ctx)
	if err != nil {
		a.logger.ErrorContext(ctx, "read stored notices", "err", err)
		return
	}
	now := a.now()
	for _, entry := range entries {
		a.attend(ctx, entry, now, stored)
	}
}

// storedIDs returns the ids of the epic notices whose rows are stored now. A row whose data does not decode is logged
// and skipped, as the Router does not restore it either.
func (a *Attention) storedIDs(ctx context.Context) (map[string]bool, error) {
	rows, err := a.store.Notices(ctx)
	if err != nil {
		return nil, fmt.Errorf("list notices: %w", err)
	}
	ids := make(map[string]bool)
	for _, row := range rows {
		if row.SourceKind != EpicSource {
			continue
		}
		var stored epicNotice
		if err = json.Unmarshal(row.Source, &stored); err != nil {
			a.logger.ErrorContext(ctx, "read epic notice row", "row", row.ID, "err", err)
			continue
		}
		ids[stored.ID] = true
	}
	return ids, nil
}

// attend passes over one epic. An epic that needs nothing has its notice cleared. Each untold tell is then routed as
// the epic's notice and marked told. Last, the latest turn, when it has ended and is not told, tells the owner once, if
// the need is one the owner must answer, and is marked told. Each notice routed here carries the mark of its turn or
// tell, which its row's write applies. stored holds the ids of the epic notices stored at the start of the pass, and a
// notice whose id is there is not routed again.
func (a *Attention) attend(ctx context.Context, entry store.EpicAttention, now time.Time, stored map[string]bool) {
	need := needOf(entry)
	if need == needNone {
		a.router.Seen(ctx, notice.Key{Epic: entry.Epic}, now.Unix())
	}
	for _, tell := range entry.Tells {
		a.tell(ctx, entry, tell, stored)
	}
	if entry.Latest.Seq == 0 || entry.Latest.Told {
		return
	}
	if tellsOwner(need, entry) {
		id := turnNoticeID(entry.Epic, entry.Latest.Seq, need)
		if !stored[id] {
			a.notify(ctx, entry, id, bodyOf(need, entry), store.ToldMark{Epic: entry.Epic, Seq: entry.Latest.Seq})
		}
	}
	if err := a.store.MarkTold(ctx, entry.Epic, entry.Latest.Seq); err != nil {
		a.logger.ErrorContext(ctx, "mark turn told", "epic", entry.Epic, "turn", entry.Latest.Seq, "err", err)
	}
}

// tell routes tell of entry as the epic's notice, with the tell's text, unless a stored notice carries its id, and
// marks it told. The notice's row marks the tell told in the write that stores it, so a restart after the route finds
// the tell told whether or not the row is still stored. The mark after the route covers a tell whose row write failed,
// and a tell whose mark fails is routed again on the next pass only when its notice is not stored.
func (a *Attention) tell(ctx context.Context, entry store.EpicAttention, tell store.EpicTell, stored map[string]bool) {
	id := tellNoticeID(entry.Epic, tell.Seq)
	if !stored[id] {
		a.notify(ctx, entry, id, tell.Text, store.ToldMark{Epic: entry.Epic, Seq: tell.Seq, Tell: true})
	}
	if err := a.store.MarkTellTold(ctx, entry.Epic, tell.Seq); err != nil {
		a.logger.ErrorContext(ctx, "mark tell told", "epic", entry.Epic, "tell", tell.Seq, "err", err)
	}
}

// turnNoticeID is the id of the notice that need of epic's turn seq routes. It names the turn and the need, so a later
// turn, or a different need of the same turn, has its own notice.
func turnNoticeID(epic string, seq int64, need attentionNeed) string {
	return fmt.Sprintf("%s:turn:%d:%s", epic, seq, need)
}

// tellNoticeID is the id of the notice of epic's tell seq.
func tellNoticeID(epic string, seq int64) string {
	return fmt.Sprintf("%s:tell:%d", epic, seq)
}

// notify routes a notice of body for entry, keyed by its epic, with id in its source data. Its rows mark told.
func (a *Attention) notify(ctx context.Context, entry store.EpicAttention, id, body string, told store.ToldMark) {
	stored := epicNotice{ID: id, Epic: entry.Epic, Title: entry.Title, Body: body}
	incoming := attentionNotice(stored)
	incoming.Told = &told
	data, err := json.Marshal(stored)
	if err != nil {
		a.logger.ErrorContext(ctx, "marshal epic notice", "epic", entry.Epic, "err", err)
	} else {
		incoming.Source = route.Source{Kind: EpicSource, Data: data}
	}
	a.router.Notify(ctx, incoming)
}

// RestoreNotice rebuilds the notice an epic notice stored, from its Source data. It is the route.Rebuild for
// EpicSource, and it returns the notice Notify builds for the same epic. It ignores at, the row's recorded time: an
// epic notice never ends by it.
func RestoreNotice(_ context.Context, data []byte, _ int64) (route.Notice, error) {
	var stored epicNotice
	if err := json.Unmarshal(data, &stored); err != nil {
		return route.Notice{}, fmt.Errorf("%s notice: %w", EpicSource, err)
	}
	return attentionNotice(stored), nil
}

// attentionNotice is the waiting notice of stored, keyed by its epic, with the desk notice and push that show its body.
// Notify and RestoreNotice both build it.
func attentionNotice(stored epicNotice) route.Notice {
	return route.Notice{
		Key:   notice.Key{Epic: stored.Epic},
		State: string(protocol.StateWaiting),
		Desk: func(context.Context) (deskstream.Notice, error) {
			return deskstream.Notice{State: string(protocol.StateWaiting), Title: stored.Epic, Body: stored.Body}, nil
		},
		Push: func(context.Context) (push.Notification, error) {
			return push.Notification{Project: stored.Title, State: protocol.StateWaiting, Summary: stored.Body}, nil
		},
	}
}

// needOf returns what entry needs from its owner: a park first, then a usage limit that freed the epic, then open
// questions, then a latest tell that is newer than the latest ended turn, or than any turn when none has ended.
func needOf(entry store.EpicAttention) attentionNeed {
	switch {
	case entry.Parked != "":
		return needPark
	case entry.Paused && entry.Latest.State == turnFree && entry.Latest.Limited:
		return needLimit
	case len(entry.Open) > 0:
		return needQuestions
	case !entry.LatestTell.IsZero() && (entry.Latest.Seq == 0 || entry.LatestTell.After(entry.Latest.Ended)):
		return needTell
	default:
		return needNone
	}
}

// tellsOwner reports whether the latest turn of entry, whose need is need, gets a notice. A park and a limit always
// do. Open questions do when the turn succeeded and asked some.
func tellsOwner(need attentionNeed, entry store.EpicAttention) bool {
	switch need {
	case needPark, needLimit:
		return true
	case needQuestions:
		return entry.Latest.State == turnSucceeded && entry.LatestAsked > 0
	default:
		return false
	}
}

// bodyOf returns the body of the notice of need for entry: the park reason, the limit's reset time or that it names
// none, or the open questions, one Q<seq> line each.
func bodyOf(need attentionNeed, entry store.EpicAttention) string {
	switch need {
	case needPark:
		return "parked: " + entry.Parked
	case needLimit:
		if entry.Latest.Resets.IsZero() {
			return "usage limit, reset time unknown"
		}
		return "usage limit, resets " + entry.Latest.Resets.UTC().Format(time.RFC3339)
	default:
		lines := make([]string, 0, len(entry.Open))
		for _, question := range entry.Open {
			lines = append(lines, fmt.Sprintf("Q%d: %s", question.Seq, question.Text))
		}
		return strings.Join(lines, "\n")
	}
}
