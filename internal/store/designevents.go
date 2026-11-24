package store

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// DesignEvent is a caller event about a line of a design's log, as DesignEvents lists it: a line the owner made through
// serve, owed to the design's working conversation. ID is DesignLineEvent of the line, and Recipient is the sessions row
// of the working conversation, filled from that row. Line is the line.
type DesignEvent struct {
	ID        string
	Recipient CallerSession
	Line      DesignLogLine
}

// DesignLineEvent returns the id of the event of the design's log line seq, design:<design>:<seq>.
func DesignLineEvent(design string, seq int64) string {
	return fmt.Sprintf("design:%s:%d", design, seq)
}

// designLineOwed reports whether a line of a design's log is owed to the design's working conversation as a caller event:
// the owner made it through serve, and it is neither an accept, a set aside nor a reopen. ContractEvents sends those
// three, from the contract's own states.
func designLineOwed(line DesignLogLine) bool {
	if line.Maker != LogMakerOwner || line.Session != "" {
		return false
	}
	switch line.What {
	case "accepted", "set aside", "reopened":
		return false
	default:
		return true
	}
}

// DesignEvents returns the design events owed: one for each line of each design's log that the owner made through serve
// (see designLineOwed), whose design's working conversation has a sessions row, and whose id no caller_events mark holds
// under that row. They are ordered by the line's time, then design, then seq.
func (s *Store) DesignEvents(ctx context.Context) ([]DesignEvent, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin design events read: %w", err)
	}
	events, err := designEventsIn(ctx, sqlcgen.New(tx))
	if err != nil {
		return nil, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end design events read: %w", err)
	}
	return events, nil
}

// designEventsIn returns the design events owed, as DesignEvents gives them, read with queries. Every design's lines are
// read, so a design whose working conversation has no sessions row contributes none.
func designEventsIn(ctx context.Context, queries *sqlcgen.Queries) ([]DesignEvent, error) {
	records, err := readDesigns(ctx, queries)
	if err != nil {
		return nil, err
	}
	var events []DesignEvent
	for _, record := range records {
		var owed []DesignEvent
		owed, err = owedDesignEvents(ctx, queries, record.ID)
		if err != nil {
			return nil, err
		}
		events = append(events, owed...)
	}
	slices.SortStableFunc(events, func(a, b DesignEvent) int {
		return cmp.Or(
			cmp.Compare(a.Line.At, b.Line.At),
			cmp.Compare(a.Line.Design, b.Line.Design),
			cmp.Compare(a.Line.Seq, b.Line.Seq),
		)
	})
	return events, nil
}

// owedDesignEvents returns the design events of design's log that are owed: its owed lines, when its working conversation
// has a sessions row, minus those a mark holds under that row. It returns none when the design has no owed line.
func owedDesignEvents(ctx context.Context, queries *sqlcgen.Queries, design string) ([]DesignEvent, error) {
	lines, err := readDesignLog(ctx, queries, design)
	if err != nil {
		return nil, err
	}
	var owed []DesignLogLine
	for _, line := range lines {
		if designLineOwed(line) {
			owed = append(owed, line)
		}
	}
	if len(owed) == 0 {
		return nil, nil
	}
	conversation, err := readDesignConversation(ctx, queries, design)
	if err != nil {
		return nil, err
	}
	row, err := queries.GetSession(ctx, conversation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read session %s: %w", conversation, err)
	}
	recipient := CallerSession{ID: row.ID, Kind: row.Kind, Server: row.Server, Pane: row.Pane, State: row.State}
	var events []DesignEvent
	for _, line := range owed {
		id := DesignLineEvent(design, line.Seq)
		var marked bool
		marked, err = callerEventMarkedIn(ctx, queries, id, row.ID)
		if err != nil {
			return nil, err
		}
		if !marked {
			events = append(events, DesignEvent{ID: id, Recipient: recipient, Line: line})
		}
	}
	return events, nil
}
