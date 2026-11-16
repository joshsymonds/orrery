package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrNoTell reports that the epic has no tell with the seq asked for.
var ErrNoTell = errors.New("no tell")

// EpicAttention is what an epic needs from its owner: its park reason, whether it is paused, its open questions in seq
// order, its untold tells in seq order, the time of its newest tell, and its latest ended turn with the number of
// questions that turn asked.
type EpicAttention struct {
	Epic        string
	Title       string
	Parked      string
	Paused      bool
	Open        []EpicQuestion
	Tells       []EpicTell
	LatestTell  time.Time
	Latest      EpicTurn
	LatestAsked int
}

// Attention returns one entry per epic with at least one turn, ordered by epic id. Latest is the epic's highest-seq
// turn that has ended, zero when none has, and LatestAsked counts the questions that turn asked.
func (s *Store) Attention(ctx context.Context) ([]EpicAttention, error) {
	return s.attention(ctx, func(string) {})
}

// attention is Attention with afterLatest called after each epic's latest-turn read. Tests use it to put a write
// between the reads.
func (s *Store) attention(ctx context.Context, afterLatest func(epic string)) ([]EpicAttention, error) {
	queries := sqlcgen.New(s.db)
	epics, err := queries.ListEpicsWithTurns(ctx)
	if err != nil {
		return nil, fmt.Errorf("list epics with turns: %w", err)
	}
	var out []EpicAttention
	for _, epic := range epics {
		var entry EpicAttention
		if entry, err = attentionOf(ctx, queries, epic.ID, afterLatest); err != nil {
			return nil, fmt.Errorf("attention of epic %s: %w", epic.ID, err)
		}
		out = append(out, entry)
	}
	return out, nil
}

// attentionOf returns the attention of epic: its head, its open questions in seq order, its untold tells in seq order
// with the time of its newest tell, and its latest ended turn with the questions that turn asked. The latest ended turn
// is read first, so every write that lands between the reads either leaves the turn as the latest or adds the questions
// it asked to Open; a turn that ends after the read is reported on a later pass.
func attentionOf(
	ctx context.Context,
	queries *sqlcgen.Queries,
	id string,
	afterLatest func(epic string),
) (EpicAttention, error) {
	ended, err := queries.GetLatestEndedEpicTurn(ctx, id)
	noEnded := errors.Is(err, sql.ErrNoRows)
	if err != nil && !noEnded {
		return EpicAttention{}, fmt.Errorf("read latest ended turn: %w", err)
	}
	afterLatest(id)
	head, err := queries.GetEpic(ctx, id)
	if err != nil {
		return EpicAttention{}, fmt.Errorf("read epic: %w", err)
	}
	open, err := queries.ListOpenEpicQuestions(ctx, id)
	if err != nil {
		return EpicAttention{}, fmt.Errorf("list open questions: %w", err)
	}
	questions, err := convertInOrder(open, questionOf)
	if err != nil {
		return EpicAttention{}, fmt.Errorf("open questions: %w", err)
	}
	tells, err := queries.ListEpicTells(ctx, id)
	if err != nil {
		return EpicAttention{}, fmt.Errorf("list tells: %w", err)
	}
	untold, err := untoldTellsOf(ctx, queries, id, tells)
	if err != nil {
		return EpicAttention{}, fmt.Errorf("untold tells: %w", err)
	}
	entry := EpicAttention{
		Epic: head.ID, Title: head.Title, Parked: head.Parked, Paused: head.Paused != 0,
		Open: questions, Tells: untold,
	}
	if len(tells) != 0 {
		entry.LatestTell = time.Unix(tells[len(tells)-1].At, 0)
	}
	if noEnded {
		return entry, nil
	}
	var latest EpicTurn
	if latest, err = turnOf(ctx, queries, ended); err != nil {
		return EpicAttention{}, fmt.Errorf("turn %d: %w", ended.Seq, err)
	}
	asked, err := queries.CountEpicQuestionsOfTurn(ctx, sqlcgen.CountEpicQuestionsOfTurnParams{
		Epic: id, Turn: ended.Seq,
	})
	if err != nil {
		return EpicAttention{}, fmt.Errorf("count questions of turn %d: %w", ended.Seq, err)
	}
	entry.Latest = latest
	entry.LatestAsked = int(asked)
	return entry, nil
}

// untoldTellsOf returns the tells of epic not yet told, in the seq order the rows come in. A tell that belongs to a
// question carries that question, read by its seq.
func untoldTellsOf(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	tells []sqlcgen.EpicTell,
) ([]EpicTell, error) {
	var out []EpicTell
	for _, tell := range tells {
		if tell.Told != 0 {
			continue
		}
		untold := EpicTell{Seq: tell.Seq, Text: tell.Text, At: time.Unix(tell.At, 0)}
		if tell.Question != 0 {
			stored, err := queries.GetEpicQuestion(ctx, sqlcgen.GetEpicQuestionParams{Epic: epic, Seq: tell.Question})
			if err != nil {
				return nil, fmt.Errorf("read question %d of tell %d: %w", tell.Question, tell.Seq, err)
			}
			question, err := questionOf(stored)
			if err != nil {
				return nil, fmt.Errorf("question %d of tell %d: %w", tell.Question, tell.Seq, err)
			}
			untold.Question = &question
		}
		out = append(out, untold)
	}
	return out, nil
}

// MarkTellTold sets the told marker of tell seq of epic in one write. A tell that does not exist is an error matching
// ErrNoTell.
func (s *Store) MarkTellTold(ctx context.Context, epic string, seq int64) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		rows, err := queries.MarkEpicTellTold(ctx, sqlcgen.MarkEpicTellToldParams{Epic: epic, Seq: seq})
		if err != nil {
			return fmt.Errorf("mark tell %d of epic %s told: %w", seq, epic, err)
		}
		if rows == 0 {
			return fmt.Errorf("tell %d of epic %s: %w", seq, epic, ErrNoTell)
		}
		return nil
	})
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
