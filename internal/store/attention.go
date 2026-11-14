package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// EpicAttention is what an epic needs from its owner: its park reason, whether it is paused, its open questions in seq
// order, and its latest ended turn with the number of questions that turn asked.
type EpicAttention struct {
	Epic        string
	Title       string
	Parked      string
	Paused      bool
	Open        []EpicQuestion
	Latest      EpicTurn
	LatestAsked int
}

// Attention returns one entry per epic with at least one turn, ordered by epic id. Latest is the epic's highest-seq
// turn that has ended, zero when none has, and LatestAsked counts the questions that turn asked.
func (s *Store) Attention(ctx context.Context) ([]EpicAttention, error) {
	queries := sqlcgen.New(s.db)
	heads, err := queries.ListEpicsWithTurns(ctx)
	if err != nil {
		return nil, fmt.Errorf("list epics with turns: %w", err)
	}
	var out []EpicAttention
	for _, head := range heads {
		var entry EpicAttention
		if entry, err = attentionOf(ctx, queries, head); err != nil {
			return nil, fmt.Errorf("attention of epic %s: %w", head.ID, err)
		}
		out = append(out, entry)
	}
	return out, nil
}

// attentionOf returns the attention of the epic head holds: its open questions in seq order, and its latest ended
// turn with the questions that turn asked.
func attentionOf(ctx context.Context, queries *sqlcgen.Queries, head sqlcgen.Epic) (EpicAttention, error) {
	open, err := queries.ListOpenEpicQuestions(ctx, head.ID)
	if err != nil {
		return EpicAttention{}, fmt.Errorf("list open questions: %w", err)
	}
	entry := EpicAttention{
		Epic: head.ID, Title: head.Title, Parked: head.Parked, Paused: head.Paused != 0,
		Open: mapInOrder(open, questionOf),
	}
	ended, err := queries.GetLatestEndedEpicTurn(ctx, head.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return entry, nil
	}
	if err != nil {
		return EpicAttention{}, fmt.Errorf("read latest ended turn: %w", err)
	}
	var latest EpicTurn
	if latest, err = turnOf(ctx, queries, ended); err != nil {
		return EpicAttention{}, fmt.Errorf("turn %d: %w", ended.Seq, err)
	}
	asked, err := queries.CountEpicQuestionsOfTurn(ctx, sqlcgen.CountEpicQuestionsOfTurnParams{
		Epic: head.ID, Turn: ended.Seq,
	})
	if err != nil {
		return EpicAttention{}, fmt.Errorf("count questions of turn %d: %w", ended.Seq, err)
	}
	entry.Latest = latest
	entry.LatestAsked = int(asked)
	return entry, nil
}

// MarkTold sets the told marker of turn seq of epic in one write. A turn that does not exist is an error matching
// ErrNoTurn.
func (s *Store) MarkTold(ctx context.Context, epic string, seq int64) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		rows, err := queries.MarkEpicTurnTold(ctx, sqlcgen.MarkEpicTurnToldParams{Epic: epic, Seq: seq})
		if err != nil {
			return fmt.Errorf("mark turn %d of epic %s told: %w", seq, epic, err)
		}
		if rows == 0 {
			return fmt.Errorf("turn %d of epic %s: %w", seq, epic, ErrNoTurn)
		}
		return nil
	})
}
