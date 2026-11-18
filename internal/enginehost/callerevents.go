package enginehost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// stepEndKind is the kind of the event a step's end sends its caller.
const stepEndKind = "step_end"

// stepEventPrefix heads the event id of a step's end: the prefix and the step id. The store's CallerSteps lists the
// steps whose end has no mark under this id.
const stepEventPrefix = "step:"

// callerEventTick is how often a StepCallerEvents passes when its Tick is not positive.
const callerEventTick = 5 * time.Second

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
// Manager.Deliver). WakePane sends a pane caller its text as keys, and reports whether the pane was idle to take them.
// Store records the events finished. A nil Now means time.Now, and a nil Logger discards.
type CallerSenderConfig struct {
	Deliver  func(context.Context, DeliverRequest) error
	WakePane func(ctx context.Context, server, pane, text string) (bool, error)
	Store    CallerEventStore
	Now      func() time.Time
	Logger   *slog.Logger
}

// CallerSender sends an event to the session that called for it, and marks the event finished once it is done. Both
// the step-end pass and the epic pass send through it.
type CallerSender struct {
	deliver  func(context.Context, DeliverRequest) error
	wakePane func(ctx context.Context, server, pane, text string) (bool, error)
	store    CallerEventStore
	now      func() time.Time
	logger   *slog.Logger
}

// NewCallerSender returns a CallerSender for cfg.
func NewCallerSender(cfg CallerSenderConfig) *CallerSender {
	sender := &CallerSender{
		deliver: cfg.Deliver, wakePane: cfg.WakePane, store: cfg.Store, now: cfg.Now, logger: cfg.Logger,
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
// step caller receives nothing, and its event is marked. A headless caller receives text as a prompt whose PromptID is
// event. A caller that is busy, stopped, or quiet since the owner interrupted it holds the event, with no error and no
// mark, until a later send. A pane caller
// receives text as keys, which WakePane holds while the pane is not idle, and the event is marked after the keys are
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

// StepCallerEventsConfig configures a StepCallerEvents. A Tick that is not positive means five seconds. Ticks, when not
// nil, is where Run takes its ticks in place of a ticker every Tick. A nil Logger discards.
type StepCallerEventsConfig struct {
	Store  StepCallerStore
	Sender *CallerSender
	Tick   time.Duration
	Ticks  <-chan time.Time
	Logger *slog.Logger
}

// StepCallerEvents tells the caller of each step that has ended, once, that the step has ended.
type StepCallerEvents struct {
	store  StepCallerStore
	sender *CallerSender
	tick   time.Duration
	ticks  <-chan time.Time
	logger *slog.Logger
}

// NewStepCallerEvents returns a StepCallerEvents for cfg.
func NewStepCallerEvents(cfg StepCallerEventsConfig) *StepCallerEvents {
	events := &StepCallerEvents{
		store: cfg.Store, sender: cfg.Sender, tick: cfg.Tick, ticks: cfg.Ticks, logger: cfg.Logger,
	}
	if events.tick <= 0 {
		events.tick = callerEventTick
	}
	if events.logger == nil {
		events.logger = slog.New(slog.DiscardHandler)
	}
	return events
}

// Run passes at once, then once per tick, until ctx ends.
func (e *StepCallerEvents) Run(ctx context.Context) {
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

// Pass sends the end of each listed step to its caller, as Run does on each tick. A send that fails is logged, and the
// pass goes on to the next step.
func (e *StepCallerEvents) Pass(ctx context.Context) {
	steps, err := e.store.CallerSteps(ctx)
	if err != nil {
		e.logger.ErrorContext(ctx, "list caller steps", "err", err)
		return
	}
	for _, step := range steps {
		e.send(ctx, step)
	}
}

// send sends the end of step to its caller as a step_end event.
func (e *StepCallerEvents) send(ctx context.Context, step store.CallerStep) {
	event := stepEventPrefix + step.Step.ID
	text := CallerEventBlock(stepEndKind, event, stepEndBody(step.Step))
	if _, err := e.sender.Send(ctx, step.Caller, event, text); err != nil {
		e.logger.ErrorContext(
			ctx, "send step end", "step", step.Step.ID, "caller", step.Caller.ID, "err", err,
		)
	}
}

// stepEndBody returns the body of the event of step's end: the step, its end, status and reason, its agent, the epic
// that holds it when one does, and the step's whole result.
func stepEndBody(step store.StepRow) string {
	lines := []string{
		fmt.Sprintf("Step %s ended: end %s, status %s, reason %s.", step.ID, step.End, step.Status, step.Reason),
		"Agent: " + step.Agent,
	}
	if step.Epic != "" {
		lines = append(lines, "Epic: "+step.Epic)
	}
	return strings.Join(append(lines, "Summary: "+step.Result), "\n")
}
