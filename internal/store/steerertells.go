package store

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// SteererTell is a tell of an epic that its steerer has not been sent: the epic and its title, the tell's seq, its text,
// when it was stored, and the question it is about, 0 for none.
type SteererTell struct {
	Epic, Title string
	Seq         int64
	Text        string
	At          time.Time
	Question    int64
}

// UndeliveredTells returns the tells no steerer has been sent, in epic then seq order.
func (s *Store) UndeliveredTells(ctx context.Context) ([]SteererTell, error) {
	rows, err := sqlcgen.New(s.read).ListUndeliveredEpicTells(ctx)
	if err != nil {
		return nil, fmt.Errorf("list undelivered tells: %w", err)
	}
	var tells []SteererTell
	for _, row := range rows {
		tells = append(tells, SteererTell{
			Epic: row.Epic, Title: row.Title, Seq: row.Seq, Text: row.Text, At: time.Unix(row.At, 0),
			Question: row.Question,
		})
	}
	return tells, nil
}

// MarkTellDelivered records that the steerer of epic has been sent its tell seq, in one write. It sets only the
// delivered mark, so the told mark owner notices use is unchanged. A tell that does not exist is an error matching
// ErrNoTell.
func (s *Store) MarkTellDelivered(ctx context.Context, epic string, seq int64) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		rows, err := queries.MarkEpicTellSteererDelivered(ctx, sqlcgen.MarkEpicTellSteererDeliveredParams{
			Epic: epic, Seq: seq,
		})
		if err != nil {
			return fmt.Errorf("mark tell %d of epic %s delivered: %w", seq, epic, err)
		}
		if rows == 0 {
			return fmt.Errorf("tell %d of epic %s: %w", seq, epic, ErrNoTell)
		}
		return nil
	})
}

// insertPhaseTell adds text to epic as insertTell does, in a write that sets epic to phase. A phase whose event the
// steerer is sent, as EpicPhaseEventKind reports, already tells the steerer of the write, so the tell is inserted
// delivered. Any other phase leaves the tell undelivered, as insertTell does.
func insertPhaseTell(ctx context.Context, queries *sqlcgen.Queries, epic, phase, text string, at time.Time) error {
	if _, sends := EpicPhaseEventKind(phase); sends {
		return insertDeliveredTell(ctx, queries, epic, text, at)
	}
	return insertTell(ctx, queries, epic, text, at)
}

// insertDeliveredTell adds text to epic as its next tell, as insertTell does, already delivered to its steerer. The
// insert is marked a caller event, so the write calls the OnCallerEvent hook as any tell write does.
func insertDeliveredTell(ctx context.Context, queries *sqlcgen.Queries, epic, text string, at time.Time) error {
	highest, err := queries.MaxEpicTellSeq(ctx, epic)
	if err != nil {
		return fmt.Errorf("read tells of epic %s: %w", epic, err)
	}
	if err = queries.InsertDeliveredEpicTell(withEpicEvent(ctx), sqlcgen.InsertDeliveredEpicTellParams{
		Epic: epic, Seq: highest + 1, Text: text, At: at.Unix(),
	}); err != nil {
		return fmt.Errorf("insert tell of epic %s: %w", epic, err)
	}
	return nil
}
