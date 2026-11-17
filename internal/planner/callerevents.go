package planner

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// The kinds of the events an epic sends its creator, as the <orrery-event> block names them.
const (
	epicKindPark     = "park"
	epicKindQuestion = "question"
	epicKindRelease  = "release"
	epicKindEnd      = "end"
)

// EpicCallerStore is the store an EpicCallerEvents reads the epics, their creators and the marks of their events
// through. *store.Store satisfies it.
type EpicCallerStore interface {
	Attention(ctx context.Context) ([]store.EpicAttention, error)
	EpicCreator(ctx context.Context, epic string) (store.CallerSession, bool, error)
	CallerEventMarked(ctx context.Context, event, session string) (bool, error)
}

// EpicCallerEventsConfig configures an EpicCallerEvents. A Tick that is not positive means defaultTick. Ticks, when not
// nil, is where Run takes its ticks in place of a ticker every Tick. A nil Logger discards.
type EpicCallerEventsConfig struct {
	Store  EpicCallerStore
	Sender *enginehost.CallerSender
	Tick   time.Duration
	Ticks  <-chan time.Time
	Logger *slog.Logger
}

// EpicCallerEvents tells the session that created each epic, once, when the epic parks, asks a question, releases or
// ends.
type EpicCallerEvents struct {
	store  EpicCallerStore
	sender *enginehost.CallerSender
	tick   time.Duration
	ticks  <-chan time.Time
	logger *slog.Logger
}

// NewEpicCallerEvents returns an EpicCallerEvents for cfg.
func NewEpicCallerEvents(cfg EpicCallerEventsConfig) *EpicCallerEvents {
	events := &EpicCallerEvents{
		store: cfg.Store, sender: cfg.Sender, tick: cfg.Tick, ticks: cfg.Ticks, logger: cfg.Logger,
	}
	if events.tick <= 0 {
		events.tick = defaultTick
	}
	if events.logger == nil {
		events.logger = slog.New(slog.DiscardHandler)
	}
	return events
}

// Run passes at once, then once per tick, until ctx ends.
func (e *EpicCallerEvents) Run(ctx context.Context) {
	ticks := e.ticks
	if ticks == nil {
		ticker := time.NewTicker(e.tick)
		defer ticker.Stop()
		ticks = ticker.C
	}
	for {
		e.Pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}

// Pass sends the events of each epic that Store.Attention lists to its creator, as Run does on each tick. An epic with
// no creator sends nothing. A send that fails is logged, and the pass goes on to the next event.
func (e *EpicCallerEvents) Pass(ctx context.Context) {
	entries, err := e.store.Attention(ctx)
	if err != nil {
		e.logger.ErrorContext(ctx, "list attention for caller events", "err", err)
		return
	}
	for _, entry := range entries {
		e.attend(ctx, entry)
	}
}

// attend sends the events of one epic to its creator: each open question in seq order, then the end of its phase.
func (e *EpicCallerEvents) attend(ctx context.Context, entry store.EpicAttention) {
	creator, ok, err := e.store.EpicCreator(ctx, entry.Epic)
	if err != nil {
		e.logger.ErrorContext(ctx, "read creator of epic", "epic", entry.Epic, "err", err)
		return
	}
	if !ok {
		return
	}
	for _, question := range entry.Open {
		kind, body := questionEventOf(entry, question)
		e.send(ctx, entry.Epic, creator, questionEvent(entry.Epic, question.Seq), kind, body)
	}
	if kind, sends := phaseEventKind(entry.Phase); sends {
		e.send(ctx, entry.Epic, creator, phaseEvent(entry.Epic, entry.Phase), kind, phaseEventBody(entry))
	}
}

// send sends the event with id event, of kind and body, to creator, unless the creator's mark shows it was sent. The
// CallerSender marks the event once it is finished, and holds it while the creator is busy or stopped, so a later pass
// sends it.
func (e *EpicCallerEvents) send(
	ctx context.Context, epic string, creator store.CallerSession, event, kind, body string,
) {
	marked, err := e.store.CallerEventMarked(ctx, event, creator.ID)
	if err != nil {
		e.logger.ErrorContext(ctx, "read caller event mark", "epic", epic, "event", event, "err", err)
		return
	}
	if marked {
		return
	}
	text := enginehost.CallerEventBlock(kind, event, body)
	if _, err = e.sender.Send(ctx, creator, event, text); err != nil {
		e.logger.ErrorContext(ctx, "send epic event", "epic", epic, "event", event, "caller", creator.ID, "err", err)
	}
}

// The event ids below are the ids that migration 00057_caller_events.sql seeds as marks, so the two must match: change
// them together.

// questionEvent returns the id of the event of question seq of epic, which is a park or a question.
func questionEvent(epic string, seq int64) string {
	return fmt.Sprintf("epic:%s:Q%d", epic, seq)
}

// phaseEvent returns the id of the event of epic's phase, which is its release or its end.
func phaseEvent(epic, phase string) string {
	return fmt.Sprintf("epic:%s:%s", epic, phase)
}

// questionEventOf returns the kind and body of the event of open question of entry. A park question's body is the
// park's body, and any other question's body is its head and advice. Both say that epic_answer answers it.
func questionEventOf(entry store.EpicAttention, question store.EpicQuestion) (string, string) {
	heading := fmt.Sprintf("Epic %s (%s)", entry.Epic, entry.Title)
	if question.Label == store.ParkLabel {
		body := fmt.Sprintf("%s is parked.\n%s\nepic_answer answers it.",
			heading, parkBody(entry.Parked, &question))
		return epicKindPark, body
	}
	lines := append([]string{heading + " asks a question.", noticeHead(question)}, adviceLines(question)...)
	lines = append(lines, "epic_answer answers it.")
	return epicKindQuestion, strings.Join(lines, "\n")
}

// phaseEventKind returns the kind of the event of an epic in phase: release for released and stopped, and end for the
// phases that follow a release. It returns false for a phase that sends none, clean among them.
func phaseEventKind(phase string) (string, bool) {
	switch phase {
	case "released", store.PhaseStopped:
		return epicKindRelease, true
	case "met_at_base", "ended_with_gaps", "done":
		return epicKindEnd, true
	default:
		return "", false
	}
}

// phaseEventBody returns the body of the event of entry's phase. It names the epic, its title and its phase, and says
// that epic_get gives its report.
func phaseEventBody(entry store.EpicAttention) string {
	return fmt.Sprintf("Epic %s (%s) is %s. epic_get gives its report.", entry.Epic, entry.Title, entry.Phase)
}
