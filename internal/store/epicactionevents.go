package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ownerKickPrefix heads the client id of a decision the owner kicked an epic with, as the owner's kick names it.
const ownerKickPrefix = "owner-kick:"

// ActionEvent is a caller event about an epic action or decision, as ActionEvents lists it. Kind is moved for a move,
// which its From session hears of with Taken false and its To session with Taken true, and action for any other
// action, and for a decision. Action is the action's kind, or for a decision "kick" or "answer". Question and Text
// are a decision's question and text.
type ActionEvent struct {
	ID, Kind, Epic, Title string
	Recipient             CallerSession
	Action                string
	Maker                 Maker
	From, To              string
	Taken                 bool
	Question              int64
	Text                  string
}

// ActionEvents returns the action events owed to every session: each one whose recipient has a sessions row and that
// no caller_events mark holds under the recipient. The events of the actions come first, a move's and the others' in
// the order of their time, then those of the decisions, in the order of theirs.
func (s *Store) ActionEvents(ctx context.Context) ([]ActionEvent, error) {
	events, err := allActionEvents(ctx, sqlcgen.New(s.read))
	if err != nil {
		return nil, fmt.Errorf("list action events: %w", err)
	}
	return events, nil
}

// allActionEvents returns the action events owed to every session, as ActionEvents lists them.
func allActionEvents(ctx context.Context, queries *sqlcgen.Queries) ([]ActionEvent, error) {
	actions, err := queries.ListAddressedEpicActions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list addressed epic actions: %w", err)
	}
	decisions, err := queries.ListAddressedEpicDecisions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list addressed epic decisions: %w", err)
	}
	var events []ActionEvent
	for _, row := range actions {
		events = append(events, actionEventsOfRow(row)...)
	}
	for _, row := range decisions {
		events = append(events, decisionEventOf(row))
	}
	return owedActionEvents(ctx, queries, events)
}

// sessionActionEvents returns the action events owed to session, in the order allActionEvents gives them. A move
// that session leaves or takes is listed once for session; the other rows the session query returns are filtered to
// session alone.
func sessionActionEvents(ctx context.Context, queries *sqlcgen.Queries, session string) ([]ActionEvent, error) {
	actions, err := queries.ListSessionEpicActions(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("list epic actions of session %s: %w", session, err)
	}
	decisions, err := queries.ListSessionEpicDecisions(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("list epic decisions of session %s: %w", session, err)
	}
	var events []ActionEvent
	for _, row := range actions {
		// A session query row has the same fields as an addressed one, so it converts to that row type.
		for _, event := range actionEventsOfRow(sqlcgen.ListAddressedEpicActionsRow(row)) {
			if event.Recipient.ID == session {
				events = append(events, event)
			}
		}
	}
	for _, row := range decisions {
		events = append(events, decisionEventOf(sqlcgen.ListAddressedEpicDecisionsRow(row)))
	}
	return owedActionEvents(ctx, queries, events)
}

// actionEventsOfRow returns the events of an epic action row. A move gives its From session a moved event with Taken
// false, skipped when From is "", and its To session one with Taken true. Any other action gives its recipient one
// action event.
func actionEventsOfRow(row sqlcgen.ListAddressedEpicActionsRow) []ActionEvent {
	id := EpicActionEvent(row.Epic, row.Seq)
	maker := Maker{By: row.Maker, Session: row.MakerSession}
	if row.Kind != ActionMove {
		return []ActionEvent{
			{
				ID:        id,
				Kind:      CallerKindAction,
				Epic:      row.Epic,
				Title:     row.Title,
				Recipient: CallerSession{ID: row.Recipient},
				Action:    row.Kind,
				Maker:     maker,
			},
		}
	}
	move := ActionEvent{
		ID: id, Kind: CallerKindMoved, Epic: row.Epic, Title: row.Title, Action: row.Kind, Maker: maker,
		From: row.FromSession, To: row.ToSession,
	}
	var events []ActionEvent
	if row.FromSession != "" {
		leave := move
		leave.Recipient = CallerSession{ID: row.FromSession}
		events = append(events, leave)
	}
	take := move
	take.Recipient, take.Taken = CallerSession{ID: row.ToSession}, true
	return append(events, take)
}

// decisionEventOf returns the action event of a decision addressed to a session: kick when its client id is an
// owner kick, and answer otherwise.
func decisionEventOf(row sqlcgen.ListAddressedEpicDecisionsRow) ActionEvent {
	action := "answer"
	if strings.HasPrefix(row.ClientID, ownerKickPrefix) {
		action = "kick"
	}
	return ActionEvent{
		ID:    EpicDecisionEvent(row.Epic, row.Seq),
		Kind:  CallerKindAction,
		Epic:  row.Epic,
		Title: row.Title,
		Recipient: CallerSession{
			ID: row.Recipient,
		},
		Action:   action,
		Maker:    Maker{By: row.Maker, Session: row.MakerSession},
		Question: row.Question,
		Text:     row.Text,
	}
}

// owedActionEvents keeps the events whose recipient has a sessions row and no mark under it, filling each kept
// recipient from its row.
func owedActionEvents(ctx context.Context, queries *sqlcgen.Queries, events []ActionEvent) ([]ActionEvent, error) {
	var out []ActionEvent
	for _, event := range events {
		row, err := queries.GetSession(ctx, event.Recipient.ID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read session %s: %w", event.Recipient.ID, err)
		}
		marked, err := callerEventMarkedIn(ctx, queries, event.ID, row.ID)
		if err != nil {
			return nil, err
		}
		if marked {
			continue
		}
		event.Recipient = CallerSession{
			ID:     row.ID,
			Kind:   row.Kind,
			Server: row.Server,
			Pane:   row.Pane,
			State:  row.State,
		}
		out = append(out, event)
	}
	return out, nil
}
