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

// attentionNeed is what an epic needs from its owner.
type attentionNeed int

const (
	needNone attentionNeed = iota
	needPark
	needLimit
	needQuestions
)

// epicNotice is the Source data of an epic notice: the epic, its title and the body the owner reads.
type epicNotice struct {
	Epic  string `json:"epic"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// AttentionStore is the store an Attention reads what each epic needs through and marks its turns told in.
// *store.Store satisfies it.
type AttentionStore interface {
	Attention(ctx context.Context) ([]store.EpicAttention, error)
	MarkTold(ctx context.Context, epic string, seq int64) error
}

// Router routes notices to the owner. *route.Router satisfies it.
type Router interface {
	Notify(ctx context.Context, n route.Notice)
	Seen(ctx context.Context, key notice.Key, seen int64)
}

// AttentionConfig configures an Attention. A Tick that is not positive means five seconds, and a nil Now means
// time.Now. A nil Logger discards.
type AttentionConfig struct {
	Store  AttentionStore
	Router Router
	Tick   time.Duration
	Now    func() time.Time
	Logger *slog.Logger
}

// Attention tells the owner, once per turn, when an epic needs them, and clears the notice of an epic that no longer
// does.
type Attention struct {
	store  AttentionStore
	router Router
	tick   time.Duration
	now    func() time.Time
	logger *slog.Logger
}

// NewAttention returns an Attention for cfg.
func NewAttention(cfg AttentionConfig) *Attention {
	attention := &Attention{
		store: cfg.Store, router: cfg.Router, tick: cfg.Tick, now: cfg.Now, logger: cfg.Logger,
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
	ticker := time.NewTicker(a.tick)
	defer ticker.Stop()
	for {
		a.pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// pass attends to each epic that has a turn.
func (a *Attention) pass(ctx context.Context) {
	entries, err := a.store.Attention(ctx)
	if err != nil {
		a.logger.ErrorContext(ctx, "list attention", "err", err)
		return
	}
	now := a.now()
	for _, entry := range entries {
		a.attend(ctx, entry, now)
	}
}

// attend passes over one epic. An epic that needs nothing has its notice cleared. Otherwise the latest turn, when it
// has ended and is not told, tells the owner once, if the need is one the owner must answer, and is marked told.
func (a *Attention) attend(ctx context.Context, entry store.EpicAttention, now time.Time) {
	need := needOf(entry)
	if need == needNone {
		a.router.Seen(ctx, notice.Key{Epic: entry.Epic}, now.Unix())
	}
	if entry.Latest.Seq == 0 || entry.Latest.Told {
		return
	}
	if tellsOwner(need, entry) {
		a.notify(ctx, entry, need)
	}
	if err := a.store.MarkTold(ctx, entry.Epic, entry.Latest.Seq); err != nil {
		a.logger.ErrorContext(ctx, "mark turn told", "epic", entry.Epic, "turn", entry.Latest.Seq, "err", err)
	}
}

// notify routes the notice of need for entry. It runs before the turn is marked told, so a restart between the two
// repeats the notice rather than losing it.
func (a *Attention) notify(ctx context.Context, entry store.EpicAttention, need attentionNeed) {
	stored := epicNotice{Epic: entry.Epic, Title: entry.Title, Body: bodyOf(need, entry)}
	incoming := attentionNotice(stored)
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
// questions.
func needOf(entry store.EpicAttention) attentionNeed {
	switch {
	case entry.Parked != "":
		return needPark
	case entry.Paused && entry.Latest.State == turnFree && !entry.Latest.Resets.IsZero():
		return needLimit
	case len(entry.Open) > 0:
		return needQuestions
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

// bodyOf returns the body of the notice of need for entry: the park reason, the limit's reset time, or the open
// questions, one Q<seq> line each.
func bodyOf(need attentionNeed, entry store.EpicAttention) string {
	switch need {
	case needPark:
		return "parked: " + entry.Parked
	case needLimit:
		return "usage limit, resets " + entry.Latest.Resets.UTC().Format(time.RFC3339)
	default:
		lines := make([]string, 0, len(entry.Open))
		for _, question := range entry.Open {
			lines = append(lines, fmt.Sprintf("Q%d: %s", question.Seq, question.Text))
		}
		return strings.Join(lines, "\n")
	}
}
