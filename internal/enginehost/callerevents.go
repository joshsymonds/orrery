package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/store"
)

// stepEndKind is the kind of the event a step's end sends its caller.
const stepEndKind = "step_end"

// stepEventPrefix heads the event id of a step's end: the prefix and the step id. The store's CallerSteps lists the
// steps whose end has no mark under this id.
const stepEventPrefix = "step:"

// stepSummaryCap is the most runes of a step's summary that its end's event carries.
const stepSummaryCap = 500

// stepFieldCap is the most runes a string field of a structured result may have and still be told in its step's summary.
const stepFieldCap = 120

// stepEndHint is a step end's last line. It tells the caller where the full result is read, so the event carries only
// what the caller needs to act and the caller asks for the rest.
const stepEndHint = "step_status gives the full result."

// callerEventRetry is how long a CallerEvents waits before it runs a round again after a round failed, when its Retry
// is not positive.
const callerEventRetry = 5 * time.Second

// CallerEventBlock returns the block that carries an event of kind, with id, to its caller: an <orrery-event> opening
// tag, the body on a line of its own, and the closing tag.
func CallerEventBlock(kind, id, body string) string {
	return fmt.Sprintf("<orrery-event kind=\"%s\" id=\"%s\">\n%s\n</orrery-event>", kind, id, body)
}

// CallerEventStore is the store a CallerSender marks the events it has finished in. *store.Store satisfies it.
type CallerEventStore interface {
	MarkCallerEvent(ctx context.Context, event, session string, at time.Time) error
}

// CallerSenderConfig configures a CallerSender. Deliver sends a headless caller its text as a prompt (see
// Manager.Deliver). DeliverStep sends a step caller the end of a step it started as a prompt (see Steps.DeliverChildEnd),
// and a nil DeliverStep leaves a step caller with nothing sent. WakePane sends a pane caller its text as keys, and reports
// whether the pane was idle to take them. Store records the events finished. A nil Now means time.Now, and a nil Logger
// discards.
type CallerSenderConfig struct {
	Deliver     func(context.Context, DeliverRequest) error
	DeliverStep func(context.Context, DeliverRequest) error
	WakePane    func(ctx context.Context, server, pane, text string) (bool, error)
	Store       CallerEventStore
	Now         func() time.Time
	Logger      *slog.Logger
}

// CallerSender sends an event to the session that called for it, and marks the event finished once it is done. Both
// the step-end pass and the epic pass send through it.
type CallerSender struct {
	deliver     func(context.Context, DeliverRequest) error
	deliverStep func(context.Context, DeliverRequest) error
	wakePane    func(ctx context.Context, server, pane, text string) (bool, error)
	store       CallerEventStore
	now         func() time.Time
	logger      *slog.Logger
}

// NewCallerSender returns a CallerSender for cfg.
func NewCallerSender(cfg CallerSenderConfig) *CallerSender {
	sender := &CallerSender{
		deliver: cfg.Deliver, deliverStep: cfg.DeliverStep, wakePane: cfg.WakePane, store: cfg.Store, now: cfg.Now,
		logger: cfg.Logger,
	}
	if sender.now == nil {
		sender.now = time.Now
	}
	if sender.logger == nil {
		sender.logger = slog.New(slog.DiscardHandler)
	}
	return sender
}

// Send sends text, the block of event, to caller, and reports whether the event is finished: it has been marked. A
// step caller receives nothing, and its event is marked; the end of a step reaches its step caller through SendStepEnd
// instead. A headless caller receives text as a prompt whose PromptID is event. A caller that is busy, stopped, or quiet
// since the owner interrupted it holds the event, with no error and no mark, until a later send. A pane caller receives
// text as keys, which WakePane holds while the pane is not idle, and the event is marked after the keys are
// sent. Any other error is returned with no mark.
func (s *CallerSender) Send(ctx context.Context, caller store.CallerSession, event, text string) (bool, error) {
	switch caller.Kind {
	case store.KindStep:
		return s.mark(ctx, caller.ID, event)
	case store.KindEngine:
		err := s.deliver(ctx, DeliverRequest{ID: caller.ID, Text: text, PromptID: event})
		if errors.Is(err, ErrSessionBusy) || errors.Is(err, ErrCallerStopped) || errors.Is(err, ErrCallerQuiet) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("deliver %s to session %s: %w", event, caller.ID, err)
		}
		return s.mark(ctx, caller.ID, event)
	case "":
		// A pane session has kind "".
		return s.wake(ctx, caller, event, text)
	default:
		return false, fmt.Errorf("send %s to session %s: unknown kind %q", event, caller.ID, caller.Kind)
	}
}

// SendStepEnd sends text, the block of the end of a step with event, to caller, and reports whether the event is
// finished. A step caller with a DeliverStep receives text as a prompt of the step it started, whose PromptID is event
// (see DeliverStep): the event is marked once the prompt is sent, and also when the step has ended or is no step, where
// nothing is sent. A step that is busy, running its gate, held or limited holds the event, with no error and no mark,
// until a later send. Any other error is returned with no mark. Every other caller, and a step caller when DeliverStep is
// nil, goes to Send as it does.
func (s *CallerSender) SendStepEnd(ctx context.Context, caller store.CallerSession, event, text string) (bool, error) {
	if caller.Kind != store.KindStep || s.deliverStep == nil {
		return s.Send(ctx, caller, event, text)
	}
	err := s.deliverStep(ctx, DeliverRequest{ID: caller.ID, Text: text, PromptID: event})
	switch {
	case err == nil:
		return s.mark(ctx, caller.ID, event)
	case errors.Is(err, ErrStepNotRunning), errors.Is(err, store.ErrNoStep):
		return s.mark(ctx, caller.ID, event)
	case errors.Is(err, ErrSessionBusy), errors.Is(err, ErrStepGating):
		return false, nil
	default:
		return false, fmt.Errorf("deliver step end %s to step %s: %w", event, caller.ID, err)
	}
}

// wake sends text to the pane of caller and marks event once the keys are sent. A pane that is not idle holds the
// event. An error that comes with the keys sent is logged, as the keys were sent and the event is finished.
func (s *CallerSender) wake(ctx context.Context, caller store.CallerSession, event, text string) (bool, error) {
	sent, err := s.wakePane(ctx, caller.Server, caller.Pane, text)
	if !sent {
		if err != nil {
			return false, fmt.Errorf("wake pane %s of server %q for %s: %w", caller.Pane, caller.Server, event, err)
		}
		return false, nil
	}
	if err != nil {
		s.logger.ErrorContext(
			ctx, "wake pane after its keys were sent", "event", event, "session", caller.ID, "err", err,
		)
	}
	return s.mark(ctx, caller.ID, event)
}

// mark records event as delivered to session.
func (s *CallerSender) mark(ctx context.Context, session, event string) (bool, error) {
	if err := s.store.MarkCallerEvent(ctx, event, session, s.now()); err != nil {
		return false, fmt.Errorf("mark %s delivered to session %s: %w", event, session, err)
	}
	return true, nil
}

// StepCallerStore is the store a StepCallerEvents reads the ended steps of their callers through. *store.Store
// satisfies it.
type StepCallerStore interface {
	CallerSteps(ctx context.Context) ([]store.CallerStep, error)
}

// StepCallerEventsConfig configures a StepCallerEvents. Store lists the ended steps, and Sender sends each step's end.
type StepCallerEventsConfig struct {
	Store  StepCallerStore
	Sender *CallerSender
}

// StepCallerEvents tells the caller of each step that has ended, once, that the step has ended. A step caller is told
// through the step it started, while that step is open (see CallerSender.SendStepEnd).
type StepCallerEvents struct {
	store  StepCallerStore
	sender *CallerSender
}

// NewStepCallerEvents returns a StepCallerEvents for cfg.
func NewStepCallerEvents(cfg StepCallerEventsConfig) *StepCallerEvents {
	return &StepCallerEvents{store: cfg.Store, sender: cfg.Sender}
}

// Pass sends the end of each listed step to its caller. It returns the read error of the steps, or else the errors of
// the sends that failed, joined. A send that holds the end is no error. A failed send does not stop the pass: it goes on
// to the next step.
func (e *StepCallerEvents) Pass(ctx context.Context) error {
	steps, err := e.store.CallerSteps(ctx)
	if err != nil {
		return fmt.Errorf("read caller steps: %w", err)
	}
	errs := make([]error, 0, len(steps))
	for _, step := range steps {
		errs = append(errs, e.send(ctx, step))
	}
	return errors.Join(errs...)
}

// send sends the end of step to its caller as a step_end event through SendStepEnd, and returns the error of the send.
func (e *StepCallerEvents) send(ctx context.Context, step store.CallerStep) error {
	event := stepEventPrefix + step.Step.ID
	text := CallerEventBlock(stepEndKind, event, stepEndBody(step.Step))
	if _, err := e.sender.SendStepEnd(ctx, step.Caller, event, text); err != nil {
		return fmt.Errorf("send step end of %s to caller %s: %w", step.Step.ID, step.Caller.ID, err)
	}
	return nil
}

// stepEndBody returns the body of the event of step's end: the step, its end, status and reason, its agent, the epic
// that holds it when one does, the summary of its result (stepSummary), and where its full result is read. A result can
// run to tens of kilobytes, which the caller never needs in its context to act on the end.
func stepEndBody(step store.StepRow) string {
	lines := []string{
		fmt.Sprintf("Step %s ended: end %s, status %s, reason %s.", step.ID, step.End, step.Status, step.Reason),
		"Agent: " + step.Agent,
	}
	if step.Epic != "" {
		lines = append(lines, "Epic: "+step.Epic)
	}
	return strings.Join(append(lines, "Summary: "+stepSummary(step.Result), stepEndHint), "\n")
}

// stepSummary returns what a step's end tells its caller of the step's result. A result that is one JSON object is told
// by its short top-level fields (shortFields); any other result, and an object with no short field, by its start. Either
// is cut to stepSummaryCap runes.
func stepSummary(result string) string {
	if fields := shortFields(result); fields != "" {
		return capped(fields, stepSummaryCap)
	}
	return capped(result, stepSummaryCap)
}

// shortFields returns the top-level fields of result as "key: value" joined by "; ", in the object's own order, when
// result is exactly one JSON object. Only a boolean, a number, or a non-empty string of at most stepFieldCap runes is
// told, with a string's line breaks made spaces; an object, an array, null, or a longer string is left for step_status.
// It returns "" when result is not one JSON object or has no such field.
func shortFields(result string) string {
	dec := json.NewDecoder(strings.NewReader(result))
	dec.UseNumber()
	if open, err := dec.Token(); err != nil || open != json.Delim('{') {
		return ""
	}
	var parts []string
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			return ""
		}
		key, ok := keyToken.(string)
		if !ok {
			return ""
		}
		var value any
		if err = dec.Decode(&value); err != nil {
			return ""
		}
		if text, told := shortValue(value); told {
			parts = append(parts, key+": "+text)
		}
	}
	if closing, err := dec.Token(); err != nil || closing != json.Delim('}') {
		return ""
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return ""
	}
	return strings.Join(parts, "; ")
}

// shortValue returns a field's value as shortFields tells it, and whether it is told at all.
func shortValue(value any) (string, bool) {
	switch field := value.(type) {
	case bool:
		return fmt.Sprint(field), true
	case json.Number:
		return field.String(), true
	case string:
		text := strings.Join(strings.Fields(field), " ")
		if text == "" || utf8.RuneCountInString(field) > stepFieldCap {
			return "", false
		}
		return text, true
	default:
		return "", false
	}
}

// capped returns the first n runes of s, or s whole when it has no more than n. A text cut short ends with "...".
func capped(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}

// CallerPass is one pass over the caller events. It sends what is due, and returns the read error or the joined send
// errors of the pass.
type CallerPass interface {
	Pass(ctx context.Context) error
}

// CallerEventsConfig configures a CallerEvents. Passes are the passes each round runs, in order. Retry is how long a
// retry waits after a failed round, and a Retry that is not positive means callerEventRetry. NewTimer arms a timer that
// sends on its channel once, after a duration, and returns the timer's stop; a nil NewTimer wraps time.NewTimer. A nil
// Logger discards.
type CallerEventsConfig struct {
	Passes   []CallerPass
	Retry    time.Duration
	NewTimer func(time.Duration) (<-chan time.Time, func() bool)
	Logger   *slog.Logger
}

// CallerEvents runs the caller-event passes on one goroutine: once when Run starts, once on each Wake, and once when a
// retry fires. There is no ticker, so a pass runs only when it is woken. A round with a failed pass arms one retry when
// none is armed; the retry, when it fires, runs one round. A round that fails again arms the next retry, and a clean
// round arms none. Only Run sends, so a caller is never sent to twice at once.
type CallerEvents struct {
	passes   []CallerPass
	retry    time.Duration
	newTimer func(time.Duration) (<-chan time.Time, func() bool)
	logger   *slog.Logger
	// wakes holds one pending wake, so a wake that comes while one is pending is absorbed.
	wakes chan struct{}
}

// NewCallerEvents returns the CallerEvents that cfg describes.
func NewCallerEvents(cfg CallerEventsConfig) *CallerEvents {
	events := &CallerEvents{
		passes: cfg.Passes, retry: cfg.Retry, newTimer: cfg.NewTimer, logger: cfg.Logger,
		wakes: make(chan struct{}, 1),
	}
	if events.retry <= 0 {
		events.retry = callerEventRetry
	}
	if events.newTimer == nil {
		events.newTimer = func(d time.Duration) (<-chan time.Time, func() bool) {
			timer := time.NewTimer(d)
			return timer.C, timer.Stop
		}
	}
	if events.logger == nil {
		events.logger = slog.New(slog.DiscardHandler)
	}
	return events
}

// Run runs every pass once at once, then once on each wake and each retry, until ctx ends. It stops the armed retry
// when it returns.
func (e *CallerEvents) Run(ctx context.Context) {
	var fire <-chan time.Time
	var stopRetry func() bool
	defer func() {
		if stopRetry != nil {
			stopRetry()
		}
	}()
	failed := e.round(ctx)
	for {
		if failed && fire == nil {
			fire, stopRetry = e.newTimer(e.retry)
		}
		select {
		case <-ctx.Done():
			return
		case <-e.wakes:
		case <-fire:
			fire = nil
		}
		failed = e.round(ctx)
	}
}

// round runs each pass once, logs the error of each pass that fails, and reports whether any failed.
func (e *CallerEvents) round(ctx context.Context) bool {
	failed := false
	for _, pass := range e.passes {
		if err := pass.Pass(ctx); err != nil {
			e.logger.ErrorContext(ctx, "caller event pass failed", "err", err)
			failed = true
		}
	}
	return failed
}

// Wake asks Run for a round, without blocking. A wake that comes while one is pending is absorbed by that one.
func (e *CallerEvents) Wake() {
	select {
	case e.wakes <- struct{}{}:
	default:
	}
}
