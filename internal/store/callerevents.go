package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The kinds of the caller events, as the <orrery-event> block names them.
const (
	CallerKindStepEnd  = "step_end"
	CallerKindPark     = "park"
	CallerKindQuestion = "question"
	CallerKindRelease  = "release"
	CallerKindEnd      = "end"
)

// stepEventPrefix heads the id of a step's end event: the prefix and the step id. ListCallerSteps and
// ListPendingCallerSteps list the steps whose end has no mark under this id.
const stepEventPrefix = "step:"

// CallerSession is the sessions row of a caller: its id, kind, server, pane and state.
type CallerSession struct {
	ID     string
	Kind   string
	Server string
	Pane   string
	State  string
}

// CallerStep is an ended step together with the sessions row of the caller that started it.
type CallerStep struct {
	Step   StepRow
	Caller CallerSession
}

// CallerSteps returns the ended steps whose caller has a sessions row, and that have no caller_events mark for the
// event "step:" followed by the step id, under the caller's row id. It orders them by ended time, then id.
func (s *Store) CallerSteps(ctx context.Context) ([]CallerStep, error) {
	rows, err := sqlcgen.New(s.read).ListCallerSteps(ctx)
	if err != nil {
		return nil, fmt.Errorf("list caller steps: %w", err)
	}
	out := make([]CallerStep, 0, len(rows))
	for _, row := range rows {
		step, stepErr := toStepRow(row.Step)
		if stepErr != nil {
			return nil, stepErr
		}
		out = append(out, CallerStep{Step: step, Caller: CallerSession{
			ID: row.CallerID, Kind: row.CallerKind, Server: row.CallerServer, Pane: row.CallerPane,
			State: row.CallerState,
		}})
	}
	return out, nil
}

// MarkCallerEvent records that the event was delivered to the session, at at. A repeat of the mark is a no-op, and it
// keeps the time of the first. It records no mark while the session's newest engine_prompt carrying the event as its
// prompt id is an event prompt whose newest state is canceled or discarded: that prompt never reached Claude, so the
// event stays owed. An event no engine_prompt of the session carries is marked as before.
func (s *Store) MarkCallerEvent(ctx context.Context, event, session string, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.InsertCallerEvent(ctx, sqlcgen.InsertCallerEventParams{
			Event: event, Session: session, At: at.Unix(),
		}); err != nil {
			return fmt.Errorf("mark event %s delivered to session %s: %w", event, session, err)
		}
		return nil
	})
}

// CallerEventMarked reports whether MarkCallerEvent has recorded the event as delivered to the session.
func (s *Store) CallerEventMarked(ctx context.Context, event, session string) (bool, error) {
	return callerEventMarkedIn(ctx, sqlcgen.New(s.read), event, session)
}

// callerEventMarkedIn reports whether a caller_events row holds event under session, as read through queries.
func callerEventMarkedIn(ctx context.Context, queries *sqlcgen.Queries, event, session string) (bool, error) {
	count, err := queries.CountCallerEvent(ctx, sqlcgen.CountCallerEventParams{Event: event, Session: session})
	if err != nil {
		return false, fmt.Errorf("read event %s delivered to session %s: %w", event, session, err)
	}
	return count > 0, nil
}

// The epic event ids below are the ids that migration 00057_caller_events.sql seeds as marks, so the two must match:
// change them together.

// EpicQuestionEvent returns the id of the event of question seq of epic, which is a park or a question.
func EpicQuestionEvent(epic string, seq int64) string {
	return fmt.Sprintf("epic:%s:Q%d", epic, seq)
}

// EpicPhaseEvent returns the id of the event of epic's phase, which is its release or its end.
func EpicPhaseEvent(epic, phase string) string {
	return fmt.Sprintf("epic:%s:%s", epic, phase)
}

// EpicPhaseEventKind returns the kind of the event of an epic in phase: release for released and stopped, and end for
// the phases that follow a release. It returns false for a phase that sends none, clean among them.
func EpicPhaseEventKind(phase string) (string, bool) {
	switch phase {
	case "released", PhaseStopped:
		return CallerKindRelease, true
	case "met_at_base", "ended_with_gaps", "done":
		return CallerKindEnd, true
	default:
		return "", false
	}
}

// PendingEvent is a caller event still owed to a session, as PendingCallerEvents lists it. ID is the id the event is
// marked under once it is delivered, and Kind is the kind its block names.
//
// A step_end carries the step: Step is its id, Agent its agent, End and Status how it ended, Brief the lines of its
// step_end block's body joined by newlines, as StepEndParts gives them, its last line the More line, Epic the epic
// that holds it, empty when none does, and Facts the step's facts, as StepFactsOf gives them. An epic event carries
// Epic, the epic's id, and Title, the epic's title, and no facts.
type PendingEvent struct {
	ID     string
	Kind   string
	Step   string
	Agent  string
	End    string
	Status string
	Brief  string
	Epic   string
	Title  string
	Facts  StepFacts
}

// PendingCallerEvents returns the caller events still owed to session, read in one read transaction. A step end is
// owed when the step has ended, session called it, it is unread, and no mark holds its id under session. An epic event
// is owed when session steers the epic, as EpicSteerer reads it, and no mark holds its id under session: each open
// question of the epic in seq order, then the epic's phase event when its phase sends one.
//
// The step ends come first, oldest end first and then by id. The epic events follow in epic id order. An epic event has
// no time of its own, so the epic events are not merged into the step ends by time.
//
// An action event is owed when session is its recipient or a move leaves or takes it, as ActionEvents lists it; those
// events come last, after the epic events, in the order ActionEvents gives them.
//
// It returns an error matching ErrUnavailable when the store file is missing or the database is busy.
func (r *Reader) PendingCallerEvents(ctx context.Context, session string) ([]PendingEvent, error) {
	db, err := sql.Open("sqlite", readOnlyDSN(r.path))
	if err != nil {
		return nil, fmt.Errorf("open store reader: %w", err)
	}
	r.reach(readOpened)
	events, err := pendingInDB(ctx, db, session, r.reach)
	if closeErr := db.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close store reader: %w", closeErr))
	}
	if err != nil {
		if unavailable(err) {
			return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return nil, err
	}
	return events, nil
}

// pendingInDB runs pendingInTx in one read transaction on db.
func pendingInDB(ctx context.Context, db *sql.DB, session string, reach func(readPoint)) ([]PendingEvent, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin store read: %w", err)
	}
	reach(readBegun)
	events, err := pendingInTx(ctx, tx, session)
	if err != nil {
		return nil, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end store read: %w", err)
	}
	return events, nil
}

// pendingInTx returns the caller events owed to session, as PendingCallerEvents orders them.
func pendingInTx(ctx context.Context, tx *sql.Tx, session string) ([]PendingEvent, error) {
	queries := sqlcgen.New(tx)
	rows, err := queries.ListPendingCallerSteps(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("list pending caller steps: %w", err)
	}
	events := make([]PendingEvent, 0, len(rows))
	for _, row := range rows {
		step, stepErr := toStepRow(row)
		if stepErr != nil {
			return nil, stepErr
		}
		_, lines := StepEndParts(step)
		events = append(events, PendingEvent{
			ID: stepEventPrefix + step.ID, Kind: CallerKindStepEnd, Step: step.ID, Agent: step.Agent,
			End: step.End, Status: step.Status, Brief: strings.Join(lines, "\n"), Epic: step.Epic,
			Facts: StepFactsOf(step),
		})
	}

	steered, err := queries.ListSteeredEpics(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("list epics steered by session %s: %w", session, err)
	}
	var epicEvents []PendingEvent
	for _, epic := range steered {
		owned, epicErr := epicEventsOf(ctx, queries, epic)
		if epicErr != nil {
			return nil, epicErr
		}
		epicEvents = append(epicEvents, owned...)
	}
	owed, err := unmarkedEvents(ctx, queries, session, epicEvents)
	if err != nil {
		return nil, err
	}
	events = append(events, owed...)
	actions, err := sessionActionEvents(ctx, queries, session)
	if err != nil {
		return nil, err
	}
	for _, action := range actions {
		events = append(events, PendingEvent{ID: action.ID, Kind: action.Kind, Epic: action.Epic, Title: action.Title})
	}
	return events, nil
}

// epicEventsOf returns the epic events of epic before the marks are read: its open questions in seq order, then its
// phase event when its phase sends one.
func epicEventsOf(ctx context.Context, queries *sqlcgen.Queries, epic sqlcgen.Epic) ([]PendingEvent, error) {
	questions, err := queries.ListOpenEpicQuestions(ctx, epic.ID)
	if err != nil {
		return nil, fmt.Errorf("list open questions of epic %s: %w", epic.ID, err)
	}
	events := make([]PendingEvent, 0, len(questions)+1)
	for _, question := range questions {
		kind := CallerKindQuestion
		if question.Label == ParkLabel {
			kind = CallerKindPark
		}
		events = append(events, PendingEvent{
			ID: EpicQuestionEvent(epic.ID, question.Seq), Kind: kind, Epic: epic.ID, Title: epic.Title,
		})
	}
	if kind, sends := EpicPhaseEventKind(epic.Phase); sends {
		events = append(events, PendingEvent{
			ID: EpicPhaseEvent(epic.ID, epic.Phase), Kind: kind, Epic: epic.ID, Title: epic.Title,
		})
	}
	return events, nil
}

// unmarkedEvents returns the events that no caller_events mark holds under session, in their order.
func unmarkedEvents(
	ctx context.Context, queries *sqlcgen.Queries, session string, events []PendingEvent,
) ([]PendingEvent, error) {
	var out []PendingEvent
	for _, event := range events {
		marked, err := callerEventMarkedIn(ctx, queries, event.ID, session)
		if err != nil {
			return nil, err
		}
		if !marked {
			out = append(out, event)
		}
	}
	return out, nil
}
