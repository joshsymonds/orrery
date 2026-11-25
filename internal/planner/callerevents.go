package planner

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// EpicCallerStore is the store an EpicCallerEvents reads the epics, their steerers and the marks of their events
// through. *store.Store satisfies it.
type EpicCallerStore interface {
	ActionEvents(ctx context.Context) ([]store.ActionEvent, error)
	ContractEvents(ctx context.Context) ([]store.ContractEvent, error)
	DesignEvents(ctx context.Context) ([]store.DesignEvent, error)
	Attention(ctx context.Context) ([]store.EpicAttention, error)
	Epic(ctx context.Context, id string) (store.EpicRow, error)
	EpicSteerer(ctx context.Context, epic string) (store.CallerSession, bool, error)
	CallerEventMarked(ctx context.Context, event, session string) (bool, error)
	UndeliveredTells(ctx context.Context) ([]store.SteererTell, error)
	MarkTellDelivered(ctx context.Context, epic string, seq int64) error
}

// EpicCallerEventsConfig configures an EpicCallerEvents. Store lists the epics, their actions and their marks, and Sender
// sends their events.
type EpicCallerEventsConfig struct {
	Store  EpicCallerStore
	Sender *enginehost.CallerSender
}

// EpicCallerEvents tells the session that steers each epic, once, of each move and owner action on it, and when the epic
// parks, asks a question, releases or ends. It tells the conversation that made each contract, once, of each change
// another made to the contract's state: an accept, a set aside or a reopen. It tells the working conversation of each
// design, once, of each line of the design's log that the owner made through serve. It tells the session that steers
// each epic, once, of each tell the epic makes that the steerer has not been sent, in seq order.
type EpicCallerEvents struct {
	store  EpicCallerStore
	sender *enginehost.CallerSender
}

// NewEpicCallerEvents returns an EpicCallerEvents for cfg.
func NewEpicCallerEvents(cfg EpicCallerEventsConfig) *EpicCallerEvents {
	return &EpicCallerEvents{store: cfg.Store, sender: cfg.Sender}
}

// Pass sends the action events that Store.ActionEvents lists, each to its recipient, then the contract events that
// Store.ContractEvents lists, each to the conversation that made its contract, then the design events that
// Store.DesignEvents lists, each to its design's working conversation, then the events of each epic that Store.Attention
// lists to its steerer, then the tells that Store.UndeliveredTells lists to the steerer of their epic (see sendTells).
// An epic with no steerer sends nothing. It returns the errors of the reads and sends that failed, joined. A failure
// does not stop the pass: it goes on to the next event.
func (e *EpicCallerEvents) Pass(ctx context.Context) error {
	var errs []error
	actions, err := e.store.ActionEvents(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("read action events for caller events: %w", err))
	}
	for _, event := range actions {
		errs = append(errs, e.sendAction(ctx, event))
	}
	contracts, err := e.store.ContractEvents(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("read contract events for caller events: %w", err))
	}
	for _, event := range contracts {
		errs = append(errs, e.sendContract(ctx, event))
	}
	designs, err := e.store.DesignEvents(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("read design events for caller events: %w", err))
	}
	for _, event := range designs {
		errs = append(errs, e.sendDesign(ctx, event))
	}
	entries, err := e.store.Attention(ctx)
	if err != nil {
		return errors.Join(append(errs, fmt.Errorf("read attention for caller events: %w", err))...)
	}
	for _, entry := range entries {
		errs = append(errs, e.attend(ctx, entry))
	}
	tells, err := e.store.UndeliveredTells(ctx)
	if err != nil {
		return errors.Join(append(errs, fmt.Errorf("read undelivered tells for caller events: %w", err))...)
	}
	epics, byEpic := groupTells(tells)
	for _, epic := range epics {
		errs = append(errs, e.sendTells(ctx, epic, byEpic[epic]))
	}
	return errors.Join(errs...)
}

// sendAction sends one action event to its recipient. ActionEvents lists only the events no mark holds under their
// recipient, and the CallerSender marks each one it finishes, so a sent event is not sent again.
func (e *EpicCallerEvents) sendAction(ctx context.Context, event store.ActionEvent) error {
	body, err := e.actionBody(ctx, event)
	if err != nil {
		return fmt.Errorf("build action event %s of epic %s: %w", event.ID, event.Epic, err)
	}
	text := store.CallerEventBlock(event.Kind, event.ID, nil, body)
	if _, err = e.sender.Send(ctx, event.Recipient, event.ID, text); err != nil {
		return fmt.Errorf("send action event %s of epic %s to session %s: %w",
			event.ID, event.Epic, event.Recipient.ID, err)
	}
	return nil
}

// sendContract sends one contract event to its recipient, the conversation that made the contract. ContractEvents lists
// only the events no mark holds under their recipient, and the CallerSender marks each one it finishes, so a sent event
// is not sent again.
func (e *EpicCallerEvents) sendContract(ctx context.Context, event store.ContractEvent) error {
	text := store.CallerEventBlock(store.CallerKindAction, event.ID, nil, contractBody(event))
	if _, err := e.sender.Send(ctx, event.Recipient, event.ID, text); err != nil {
		return fmt.Errorf("send contract event %s of contract %s to session %s: %w",
			event.ID, event.Contract, event.Recipient.ID, err)
	}
	return nil
}

// sendDesign sends one design event to its recipient, the design's working conversation. DesignEvents lists only the
// events no mark holds under their recipient, and the CallerSender marks each one it finishes, so a sent event is not
// sent again. The block's design attribute names the design.
func (e *EpicCallerEvents) sendDesign(ctx context.Context, event store.DesignEvent) error {
	attrs := []store.EventAttr{{Name: "design", Value: event.Line.Design}}
	text := store.CallerEventBlock(store.CallerKindDesign, event.ID, attrs, designBody(event.Line))
	if _, err := e.sender.Send(ctx, event.Recipient, event.ID, text); err != nil {
		return fmt.Errorf("send design event %s of design %s to session %s: %w",
			event.ID, event.Line.Design, event.Recipient.ID, err)
	}
	return nil
}

// designBody returns the body of a design event. Its first line names the design, what the owner did, and the ids the
// line touched when it touched any. Its words follow, cut to decisionTextRunes, when the line gave any.
func designBody(line store.DesignLogLine) string {
	first := fmt.Sprintf("Design %s: the owner %s", line.Design, line.What)
	if len(line.IDs) > 0 {
		first += ", ids " + strings.Join(line.IDs, ", ")
	}
	lines := []string{first + "."}
	if line.Words != "" {
		lines = append(lines, "words: "+cutRunes(line.Words, decisionTextRunes))
	}
	return strings.Join(lines, "\n")
}

// contractBody returns the body of a contract event. Its first line names the contract, its title, who made the change
// and what it did at its version. Its words follow, cut to decisionTextRunes, when the change gave any. An accept's last
// line says that epic_create makes its epic.
func contractBody(event store.ContractEvent) string {
	who := "the owner"
	if event.Session != "" {
		who = "session " + event.Session
	}
	lines := []string{fmt.Sprintf("Contract %s (%s): %s %s at version %d.",
		event.Contract, event.Title, who, contractChangePhrase(event.State), event.Version)}
	if event.Words != "" {
		lines = append(lines, "words: "+cutRunes(event.Words, decisionTextRunes))
	}
	if event.State == store.ContractAccepted {
		lines = append(lines, "epic_create with its id and a base makes its epic.")
	}
	return strings.Join(lines, "\n")
}

// contractChangePhrase returns what a change to a contract's state did, after its maker: accepted it, set it aside, or
// reopened it. ContractEvents lists no other change than these three, and a reopen enters the proposed state.
func contractChangePhrase(state string) string {
	switch state {
	case store.ContractAccepted:
		return "accepted it"
	case store.ContractSetAside:
		return "set it aside"
	default:
		return "reopened it"
	}
}

// actionBody returns the body of an action event. The session that takes an epic is sent the steering prompt of the
// epic's stored row, so its first line is the steering instruction; every other event's body is actionLine.
func (e *EpicCallerEvents) actionBody(ctx context.Context, event store.ActionEvent) (string, error) {
	if !event.Taken {
		return actionLine(event), nil
	}
	row, err := e.store.Epic(ctx, event.Epic)
	if err != nil {
		return "", fmt.Errorf("read epic %s: %w", event.Epic, err)
	}
	lines, err := SteeringLines(row)
	if err != nil {
		return "", err
	}
	return enginehost.SteeringPrompt(event.Epic, lines), nil
}

// actionLine returns the body of an action event that is not a taker's steering prompt. A move names the epic, the session
// it went to and who moved it. Any other action names the epic and what the owner did to it, and a decision adds its text
// cut to decisionTextRunes.
func actionLine(event store.ActionEvent) string {
	if event.Kind == store.CallerKindMoved {
		return fmt.Sprintf("Epic %s (%s) moved to session %s, by %s.",
			event.Epic, event.Title, event.To, moverOf(event.Maker))
	}
	line := fmt.Sprintf("Epic %s (%s): the owner %s.", event.Epic, event.Title, actionPhrase(event))
	switch event.Action {
	case "answer", "kick":
		return line + "\ntext: " + cutRunes(event.Text, decisionTextRunes)
	}
	return line
}

// actionPhrase returns what the owner's action did to the epic, after "the owner": the verb of an epic action, or for a
// decision, the question it answered, a note, or a kick.
func actionPhrase(event store.ActionEvent) string {
	switch event.Action {
	case store.ActionPause:
		return "paused the epic"
	case store.ActionResume:
		return "resumed the epic"
	case store.ActionStop:
		return "stopped the epic"
	case store.ActionRetry:
		return "retried the epic"
	case store.ActionRelease:
		return "released the epic"
	case store.ActionClose:
		return "closed the epic"
	case store.ActionAmend:
		return "amended the epic's evidence"
	case "kick":
		return "kicked the epic"
	case "answer":
		if event.Question > 0 {
			return fmt.Sprintf("answered Q%d", event.Question)
		}
		return "added a note"
	}
	return "made " + event.Action
}

// moverOf names who made a move: the owner, the session of a conversation, or orreryd.
func moverOf(maker store.Maker) string {
	switch maker.By {
	case store.MakerOwner:
		return "the owner"
	case store.MakerConversation:
		return "session " + maker.Session
	}
	return store.MakerOrreryd
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

// stoppedSessionState is the state a stopped session's sessions row has, as internal/store/attention.go:18 names it.
const stoppedSessionState = "stopped"

// groupTells groups tells by epic. It returns the epics in the order their first tell comes in, and each epic's tells
// in the order they come in, which is seq order.
func groupTells(tells []store.SteererTell) ([]string, map[string][]store.SteererTell) {
	var epics []string
	byEpic := make(map[string][]store.SteererTell)
	for _, tell := range tells {
		if _, seen := byEpic[tell.Epic]; !seen {
			epics = append(epics, tell.Epic)
		}
		byEpic[tell.Epic] = append(byEpic[tell.Epic], tell)
	}
	return epics, byEpic
}

// sendTells sends the undelivered tells of epic, in seq order, to its steerer. It returns the error of the steerer read
// or of a send or mark that failed. An epic with no steerer, or whose steerer's sessions row is stopped, sends nothing,
// so its tells stay undelivered for a later steerer. A send that is held or fails ends the epic's tells for this pass,
// so no tell goes out after a later one.
func (e *EpicCallerEvents) sendTells(ctx context.Context, epic string, tells []store.SteererTell) error {
	steerer, ok, err := e.store.EpicSteerer(ctx, epic)
	if err != nil {
		return fmt.Errorf("read steerer of epic %s: %w", epic, err)
	}
	if !ok || steerer.State == stoppedSessionState {
		return nil
	}
	for _, tell := range tells {
		delivered, tellErr := e.deliverTell(ctx, epic, steerer, tell)
		if tellErr != nil || !delivered {
			return tellErr
		}
	}
	return nil
}

// deliverTell delivers tell of epic to steerer, and reports whether the tell is delivered. A tell with a question, and
// a silent move tell, is marked delivered and not sent: its question or park reaches the steerer as its own event. A
// tell whose event the steerer's mark already holds is marked without a send, which covers a crash between a send and
// its mark. Any other tell is sent, and is marked once the send finishes. It reports false when the send is held.
func (e *EpicCallerEvents) deliverTell(
	ctx context.Context, epic string, steerer store.CallerSession, tell store.SteererTell,
) (bool, error) {
	if tell.Question != 0 || store.SilentMoveTell(tell.Text, false) {
		return true, e.markTell(ctx, epic, tell.Seq)
	}
	event := store.EpicTellEvent(epic, tell.Seq)
	marked, err := e.store.CallerEventMarked(ctx, event, steerer.ID)
	if err != nil {
		return false, fmt.Errorf("read caller event mark of %s: %w", event, err)
	}
	if !marked {
		finished, sendErr := e.sender.Send(ctx, steerer, event, tellText(epic, tell))
		if sendErr != nil {
			return false, fmt.Errorf("send tell %d of epic %s to caller %s: %w", tell.Seq, epic, steerer.ID, sendErr)
		}
		if !finished {
			return false, nil
		}
	}
	return true, e.markTell(ctx, epic, tell.Seq)
}

// markTell records that the steerer of epic has been sent, or need not be sent, its tell seq.
func (e *EpicCallerEvents) markTell(ctx context.Context, epic string, seq int64) error {
	if err := e.store.MarkTellDelivered(ctx, epic, seq); err != nil {
		return fmt.Errorf("mark tell %d of epic %s delivered: %w", seq, epic, err)
	}
	return nil
}

// tellText returns the block of tell of epic: the tell's event, with the attributes epic and seq, and a body that names
// the epic and its title and carries the tell's text, cut to decisionTextRunes.
func tellText(epic string, tell store.SteererTell) string {
	attrs := []store.EventAttr{{Name: "epic", Value: epic}, {Name: "seq", Value: strconv.FormatInt(tell.Seq, 10)}}
	body := fmt.Sprintf("Epic %s (%s) tells: %s", epic, tell.Title, cutRunes(tell.Text, decisionTextRunes))
	return store.CallerEventBlock(store.CallerKindTell, store.EpicTellEvent(epic, tell.Seq), attrs, body)
}

// The byte budget of a question or park event body, the rune limits of the texts it cuts, and the ellipsis a cut text
// ends with.
const (
	eventBodyBytes    = 1500
	questionTextRunes = 600
	choiceTextRunes   = 160
	recommendWhyRunes = 200
	parkedLineRunes   = 300
	decisionTextRunes = 600
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
	last := fmt.Sprintf(
		"epic_get with part question and question Q%d reads it whole; epic_answer answers it, "+
			"and epic_escalate takes it to the owner.",
		question.Seq,
	)
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
