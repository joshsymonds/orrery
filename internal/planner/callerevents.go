package planner

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// EpicCallerStore is the store an EpicCallerEvents reads the epics, their steerers and the marks of their events
// through. *store.Store satisfies it.
type EpicCallerStore interface {
	Attention(ctx context.Context) ([]store.EpicAttention, error)
	EpicSteerer(ctx context.Context, epic string) (store.CallerSession, bool, error)
	CallerEventMarked(ctx context.Context, event, session string) (bool, error)
}

// EpicCallerEventsConfig configures an EpicCallerEvents. Store lists the epics and their marks, and Sender sends their
// events.
type EpicCallerEventsConfig struct {
	Store  EpicCallerStore
	Sender *enginehost.CallerSender
}

// EpicCallerEvents tells the session that steers each epic, once, when the epic parks, asks a question, releases or
// ends.
type EpicCallerEvents struct {
	store  EpicCallerStore
	sender *enginehost.CallerSender
}

// NewEpicCallerEvents returns an EpicCallerEvents for cfg.
func NewEpicCallerEvents(cfg EpicCallerEventsConfig) *EpicCallerEvents {
	return &EpicCallerEvents{store: cfg.Store, sender: cfg.Sender}
}

// Pass sends the events of each epic that Store.Attention lists to its steerer. An epic with no steerer sends nothing.
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

// attend sends the events of one epic to its steerer: each open question in seq order, then the end of its phase. It
// returns the errors of the steerer read and of the sends, joined. An epic whose steerer has no sessions row sends
// nothing.
func (e *EpicCallerEvents) attend(ctx context.Context, entry store.EpicAttention) error {
	steerer, ok, err := e.store.EpicSteerer(ctx, entry.Epic)
	if err != nil {
		return fmt.Errorf("read steerer of epic %s: %w", entry.Epic, err)
	}
	if !ok {
		return nil
	}
	errs := make([]error, 0, len(entry.Open)+1)
	for _, question := range entry.Open {
		kind, body := questionEventOf(entry, question)
		event := store.EpicQuestionEvent(entry.Epic, question.Seq)
		errs = append(errs, e.send(ctx, entry.Epic, steerer, event, kind, body))
	}
	if kind, sends := store.EpicPhaseEventKind(entry.Phase); sends {
		event := store.EpicPhaseEvent(entry.Epic, entry.Phase)
		errs = append(errs, e.send(ctx, entry.Epic, steerer, event, kind, phaseEventBody(entry)))
	}
	return errors.Join(errs...)
}

// send sends the event with id event, of kind and body, to steerer, unless the steerer's mark shows it was sent. The
// CallerSender marks the event once it is finished, and holds it while the steerer is busy or stopped, so a later pass
// sends it. It returns the error of the mark read or of the send.
func (e *EpicCallerEvents) send(
	ctx context.Context, epic string, steerer store.CallerSession, event, kind, body string,
) error {
	marked, err := e.store.CallerEventMarked(ctx, event, steerer.ID)
	if err != nil {
		return fmt.Errorf("read caller event mark of %s: %w", event, err)
	}
	if marked {
		return nil
	}
	text := store.CallerEventBlock(kind, event, nil, body)
	if _, err = e.sender.Send(ctx, steerer, event, text); err != nil {
		return fmt.Errorf("send epic event %s of epic %s to caller %s: %w", event, epic, steerer.ID, err)
	}
	return nil
}

// The byte budget of a question or park event body, the rune limits of the texts it cuts, and the ellipsis a cut text
// ends with.
const (
	eventBodyBytes    = 1500
	questionTextRunes = 600
	choiceTextRunes   = 160
	recommendWhyRunes = 200
	parkedLineRunes   = 300
	cutEllipsis       = "…"
	epicReportLine    = "epic_get with part report reads its report."
)

// eventLayout lays out a question or park event body. Its lines are the lines lead returns, then the choice lines in
// order, then the lines tail returns, then last. text is the body's uncut cut text, the question's text or a park's
// parked line, cut to runes runes first; lead and tail take the cut text as it stands for a render.
type eventLayout struct {
	lead, tail func(cut string) []string
	text       string
	runes      int
	choices    []string
	last       string
}

// questionEventOf returns the kind and body of the event of open question of entry. A park question's body is the
// park's body, and any other question's body is its head, its choices and its recommendation. Both are bounded (see
// boundedEventBody), and both say that epic_get with part question reads the question whole and that epic_answer
// answers it.
func questionEventOf(entry store.EpicAttention, question store.EpicQuestion) (string, string) {
	last := fmt.Sprintf("epic_get with part question and question Q%d reads it whole; epic_answer answers it.",
		question.Seq)
	if question.Label == store.ParkLabel {
		return store.CallerKindPark, boundedEventBody(parkLayout(entry, question, last))
	}
	return store.CallerKindQuestion, boundedEventBody(questionLayout(entry, question, last))
}

// questionLayout lays out the body of a question that is not a park: the epic line that asks it, its head with its text
// cut, its choices, the other line and its recommendation.
func questionLayout(entry store.EpicAttention, question store.EpicQuestion, last string) eventLayout {
	return eventLayout{
		lead: func(cut string) []string {
			head := question
			head.Text = cut
			return []string{epicHeading(entry) + " asks a question.", noticeHead(head)}
		},
		tail:    func(string) []string { return answerLines(question) },
		text:    question.Text,
		runes:   questionTextRunes,
		choices: choiceLines(question.Choices),
		last:    last,
	}
}

// parkLayout lays out the body of a park, whose question is question. A park question that declares choices leads with
// its head, its choices, the other line and its recommendation, then the parked line. One that declares none leads with
// the parked line, then its head, then the other line and its recommendation.
func parkLayout(entry store.EpicAttention, question store.EpicQuestion, last string) eventLayout {
	layout := eventLayout{
		text:    parkedPrefix + entry.Parked,
		runes:   parkedLineRunes,
		choices: choiceLines(question.Choices),
		last:    last,
	}
	parked := epicHeading(entry) + " is parked."
	if len(question.Choices) == 0 {
		layout.lead = func(cut string) []string { return []string{parked, cut, adviceHead(question)} }
		layout.tail = func(string) []string { return answerLines(question) }
		return layout
	}
	layout.lead = func(string) []string { return []string{parked, adviceHead(question)} }
	layout.tail = func(cut string) []string { return append(answerLines(question), cut) }
	return layout
}

// boundedEventBody returns the body of l within eventBodyBytes. Its text is cut to its rune limit, and its choices
// are kept in order while the body fits, the rest collapsing into one line. When the body is still over with no
// choice kept, its text is cut further, on a rune boundary, to fit. The epic line and the last line are never cut.
func boundedEventBody(layout eventLayout) string {
	text := cutRunes(layout.text, layout.runes)
	kept := 0
	for kept < len(layout.choices) && len(layout.render(text, kept+1)) <= eventBodyBytes {
		kept++
	}
	body := layout.render(text, kept)
	if over := len(body) - eventBodyBytes; over > 0 {
		body = layout.render(cutBytes(layout.text, len(text)-over), 0)
	}
	return body
}

// render returns the body of l with its text as cut, and with its first kept choices, then one line that counts the
// rest when any is left out.
func (l eventLayout) render(cut string, kept int) string {
	lines := l.lead(cut)
	lines = append(lines, l.choices[:kept]...)
	if rest := len(l.choices) - kept; rest > 0 {
		lines = append(lines, fmt.Sprintf("  and %d more choices", rest))
	}
	lines = append(lines, l.tail(cut)...)
	return strings.Join(append(lines, l.last), "\n")
}

// epicHeading returns the heading that names entry's epic and its title.
func epicHeading(entry store.EpicAttention) string {
	return fmt.Sprintf("Epic %s (%s)", entry.Epic, entry.Title)
}

// choiceLines returns the line of each declared choice, as choiceLine writes it, with the choice's text cut to
// choiceTextRunes.
func choiceLines(choices []store.EpicChoice) []string {
	lines := make([]string, 0, len(choices))
	for _, choice := range choices {
		choice.Does = cutRunes(choice.Does, choiceTextRunes)
		lines = append(lines, choiceLine(choice))
	}
	return lines
}

// answerLines returns the other line, which orrery offers on every question, then the recommendation when question
// recommends one, with its why cut to recommendWhyRunes.
func answerLines(question store.EpicQuestion) []string {
	lines := []string{fmt.Sprintf("  %s: answer in your own words", store.ChoiceOther)}
	if question.Recommended != "" {
		lines = append(lines, fmt.Sprintf("  recommended: %s (%s): %s",
			question.Recommended, question.Confidence, cutRunes(question.Why, recommendWhyRunes)))
	}
	return lines
}

// cutRunes returns text cut to its first runes runes and an ellipsis, or text itself when it has runes runes or fewer.
func cutRunes(text string, runes int) string {
	count := 0
	for i := range text {
		if count == runes {
			return text[:i] + cutEllipsis
		}
		count++
	}
	return text
}

// cutBytes returns text when it takes at most limit bytes. Otherwise it returns the longest run of whole runes of text
// that, with an ellipsis, takes at most limit bytes.
func cutBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	keep := limit - len(cutEllipsis)
	end := 0
	for i := range text {
		if i > keep {
			break
		}
		end = i
	}
	return text[:end] + cutEllipsis
}

// phaseEventBody returns the body of the event of entry's phase: the epic line with its phase, then the line that says
// epic_get with part report reads its report.
func phaseEventBody(entry store.EpicAttention) string {
	return fmt.Sprintf("%s is %s.\n%s", epicHeading(entry), entry.Phase, epicReportLine)
}
