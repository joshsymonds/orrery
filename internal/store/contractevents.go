package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ContractEvent is a caller event about a change of a contract's state, as ContractEvents lists it: an accept, a set
// aside or a reopen that someone other than the contract's conversation made, owed to that conversation. State is
// ContractAccepted, ContractSetAside, or ContractProposed for a reopen. Session is the sessions row id of who made the
// change, "" for the owner. Title is the title of the version the change entered at.
type ContractEvent struct {
	ID, Contract, Title string
	Recipient           CallerSession
	Seq                 int64
	State               string
	Version             int64
	Session, Words      string
}

// ContractStateEvent returns the id of the event of state seq of contract, contract:<contract>:<seq>.
func ContractStateEvent(contract string, seq int64) string {
	return fmt.Sprintf("contract:%s:%d", contract, seq)
}

// ContractEvents returns the contract events owed: one for each contract_states row whose session is not the writer of
// the contract's version 1, whose writer has a sessions row, and whose id no caller_events mark holds under that row,
// with Recipient filled from that row. They are ordered by the change's time, then contract id, then seq.
func (s *Store) ContractEvents(ctx context.Context) ([]ContractEvent, error) {
	queries := sqlcgen.New(s.db)
	rows, err := queries.ListContractChangeEvents(ctx)
	if err != nil {
		return nil, fmt.Errorf("list contract changes: %w", err)
	}
	events := make([]ContractEvent, 0, len(rows))
	for _, row := range rows {
		events = append(events, ContractEvent{
			ID:        ContractStateEvent(row.Contract, row.Seq),
			Contract:  row.Contract,
			Title:     row.Title,
			Recipient: CallerSession{ID: row.Conversation},
			Seq:       row.Seq,
			State:     row.State,
			Version:   row.Version,
			Session:   row.Session,
			Words:     row.Words,
		})
	}
	return owedContractEvents(ctx, queries, events)
}

// owedContractEvents keeps the events whose recipient has a sessions row and no mark under it, filling each kept
// recipient from its row.
func owedContractEvents(
	ctx context.Context, queries *sqlcgen.Queries, events []ContractEvent,
) ([]ContractEvent, error) {
	var out []ContractEvent
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
