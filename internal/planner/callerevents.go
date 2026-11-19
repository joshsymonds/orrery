package planner

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// EpicCallerStore is the store an EpicCallerEvents reads the epics, their creators and the marks of their events
// through. *store.Store satisfies it.
type EpicCallerStore interface {
	Attention(ctx context.Context) ([]store.EpicAttention, error)
	EpicCreator(ctx context.Context, epic string) (store.CallerSession, bool, error)
	CallerEventMarked(ctx context.Context, event, session string) (bool, error)
}

// EpicCallerEventsConfig configures an EpicCallerEvents. Store lists the epics and their marks, and Sender sends their
// events.
type EpicCallerEventsConfig struct {
	Store  EpicCallerStore
	Sender *enginehost.CallerSender
}

// EpicCallerEvents tells the session that created each epic, once, when the epic parks, asks a question, releases or
// ends.
type EpicCallerEvents struct {
	store  EpicCallerStore
	sender *enginehost.CallerSender
}

// NewEpicCallerEvents returns an EpicCallerEvents for cfg.
func NewEpicCallerEvents(cfg EpicCallerEventsConfig) *EpicCallerEvents {
	return &EpicCallerEvents{store: cfg.Store, sender: cfg.Sender}
}

// Pass sends the events of each epic that Store.Attention lists to its creator. An epic with no creator sends nothing.
// It returns the read error of the epics, or else the errors of the reads and sends that failed, joined. A failure does
// not stop the pass: it goes on to the next event.
func (e *EpicCallerEvents) Pass(ctx context.Context) error {
	entries, err := e.store.Attention(ctx)
	if err != nil {
		return fmt.Errorf("read attention for caller events: %w", err)
	}
	errs := make([]error, 0, len(entries))
	for _, entry := range entries {
		errs = append(errs, e.attend(ctx, entry))
	}
	return errors.Join(errs...)
}

// attend sends the events of one epic to its creator: each open question in seq order, then the end of its phase. It
// returns the errors of the creator read and of the sends, joined.
func (e *EpicCallerEvents) attend(ctx context.Context, entry store.EpicAttention) error {
	creator, ok, err := e.store.EpicCreator(ctx, entry.Epic)
	if err != nil {
		return fmt.Errorf("read creator of epic %s: %w", entry.Epic, err)
	}
	if !ok {
		return nil
	}
	errs := make([]error, 0, len(entry.Open)+1)
	for _, question := range entry.Open {
		kind, body := questionEventOf(entry, question)
		event := store.EpicQuestionEvent(entry.Epic, question.Seq)
		errs = append(errs, e.send(ctx, entry.Epic, creator, event, kind, body))
	}
	if kind, sends := store.EpicPhaseEventKind(entry.Phase); sends {
		event := store.EpicPhaseEvent(entry.Epic, entry.Phase)
		errs = append(errs, e.send(ctx, entry.Epic, creator, event, kind, phaseEventBody(entry)))
	}
	return errors.Join(errs...)
}

// send sends the event with id event, of kind and body, to creator, unless the creator's mark shows it was sent. The
// CallerSender marks the event once it is finished, and holds it while the creator is busy or stopped, so a later pass
// sends it. It returns the error of the mark read or of the send.
func (e *EpicCallerEvents) send(
	ctx context.Context, epic string, creator store.CallerSession, event, kind, body string,
) error {
	marked, err := e.store.CallerEventMarked(ctx, event, creator.ID)
	if err != nil {
		return fmt.Errorf("read caller event mark of %s: %w", event, err)
	}
	if marked {
		return nil
	}
	text := enginehost.CallerEventBlock(kind, event, body)
	if _, err = e.sender.Send(ctx, creator, event, text); err != nil {
		return fmt.Errorf("send epic event %s of epic %s to caller %s: %w", event, epic, creator.ID, err)
	}
	return nil
}

// questionEventOf returns the kind and body of the event of open question of entry. A park question's body is the
// park's body, and any other question's body is its head and advice. Both say that epic_answer answers it.
func questionEventOf(entry store.EpicAttention, question store.EpicQuestion) (string, string) {
	heading := fmt.Sprintf("Epic %s (%s)", entry.Epic, entry.Title)
	if question.Label == store.ParkLabel {
		body := fmt.Sprintf("%s is parked.\n%s\nepic_answer answers it.",
			heading, parkBody(entry.Parked, &question))
		return store.CallerKindPark, body
	}
	lines := append([]string{heading + " asks a question.", noticeHead(question)}, adviceLines(question)...)
	lines = append(lines, "epic_answer answers it.")
	return store.CallerKindQuestion, strings.Join(lines, "\n")
}

// phaseEventBody returns the body of the event of entry's phase. It names the epic, its title and its phase, and says
// that epic_get gives its report.
func phaseEventBody(entry store.EpicAttention) string {
	return fmt.Sprintf("Epic %s (%s) is %s. epic_get gives its report.", entry.Epic, entry.Title, entry.Phase)
}
