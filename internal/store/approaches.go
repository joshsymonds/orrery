package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrNoApproachChoice reports that a design has no choice of an approach stored.
var ErrNoApproachChoice = errors.New("no approach choice")

// RecordApproach stores approach on its design, numbered as the design's next approach: counted from 1 within the
// design, one more than the highest number the design already holds. approach.Seq is ignored. Research is stored as a
// JSON array. Rows only append, so an approach once stored is never changed. It returns the approach as stored, with
// its Seq, and Recommended as the approach was recorded.
func (s *Store) RecordApproach(ctx context.Context, approach DesignApproach) (DesignApproach, error) {
	research, err := encodeList(approach.Research)
	if err != nil {
		return DesignApproach{}, fmt.Errorf("encode research of an approach of design %s: %w", approach.Design, err)
	}
	var seq int64
	err = s.write(ctx, func(queries *sqlcgen.Queries) error {
		rows, listErr := queries.ListDesignApproaches(ctx, approach.Design)
		if listErr != nil {
			return fmt.Errorf("number an approach of design %s: %w", approach.Design, listErr)
		}
		seq = nextApproachSeq(rows)
		if insertErr := queries.InsertDesignApproach(ctx, sqlcgen.InsertDesignApproachParams{
			Design: approach.Design, Seq: seq, Name: approach.Name, Shape: approach.Shape, Cost: approach.Cost,
			Wins: approach.Wins, Loses: approach.Loses, Reconsider: approach.Reconsider, Research: research,
			Recommended: boolInt(approach.Recommended), Reason: approach.Reason, Session: approach.Session,
			At: approach.At,
		}); insertErr != nil {
			return fmt.Errorf("insert approach %d of design %s: %w", seq, approach.Design, insertErr)
		}
		return nil
	})
	if err != nil {
		return DesignApproach{}, err
	}
	approach.Seq = seq
	return approach, nil
}

// ChooseApproach stores choice on its design, numbered as the design's next choice: counted from 1 within the design,
// one more than the highest number the design already holds. choice.Seq is ignored. Rows only append, so an earlier
// choice stays stored after a later one, and the newest is the design's choice. It returns the choice as stored, with
// its Seq. Whether choice.Approach is an approach of the design is the caller's to check.
func (s *Store) ChooseApproach(ctx context.Context, choice ApproachChoice) (ApproachChoice, error) {
	var seq int64
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		rows, listErr := queries.ListDesignApproachChoices(ctx, choice.Design)
		if listErr != nil {
			return fmt.Errorf("number a choice of design %s: %w", choice.Design, listErr)
		}
		seq = nextChoiceSeq(rows)
		if insertErr := queries.InsertDesignApproachChoice(ctx, sqlcgen.InsertDesignApproachChoiceParams{
			Design: choice.Design, Seq: seq, Approach: choice.Approach, Words: choice.Words, Maker: choice.Maker,
			Quote: choice.Quote, MakerReason: choice.MakerReason, Delegation: choice.Delegation,
			Session: choice.Session, At: choice.At,
		}); insertErr != nil {
			return fmt.Errorf("insert choice %d of design %s: %w", seq, choice.Design, insertErr)
		}
		return nil
	})
	if err != nil {
		return ApproachChoice{}, err
	}
	choice.Seq = seq
	return choice, nil
}

// DesignApproaches returns the approaches stored against the design, oldest first, which is by seq. A design with no
// approaches has none. The design's recommendation is the newest approach recorded as recommended, so only that one
// reads as Recommended, and the others read as not recommended whatever they were recorded as.
func (s *Store) DesignApproaches(ctx context.Context, design string) ([]DesignApproach, error) {
	return readDesignApproaches(ctx, sqlcgen.New(s.read), design)
}

// DesignApproaches returns the approaches of the design as Store.DesignApproaches does, read through the Reader.
func (r *Reader) DesignApproaches(ctx context.Context, design string) ([]DesignApproach, error) {
	var approaches []DesignApproach
	err := r.readEngine(ctx, func(queries *sqlcgen.Queries) error {
		var readErr error
		approaches, readErr = readDesignApproaches(ctx, queries, design)
		return readErr
	})
	return approaches, err
}

// DesignApproachChoice returns the choice of an approach the design holds: its newest choice. It returns an error that
// matches ErrNoApproachChoice when the design has none.
func (s *Store) DesignApproachChoice(ctx context.Context, design string) (ApproachChoice, error) {
	return readApproachChoice(ctx, sqlcgen.New(s.read), design)
}

// DesignApproachChoice returns the choice of an approach the design holds as Store.DesignApproachChoice does, read
// through the Reader.
func (r *Reader) DesignApproachChoice(ctx context.Context, design string) (ApproachChoice, error) {
	var choice ApproachChoice
	err := r.readEngine(ctx, func(queries *sqlcgen.Queries) error {
		var readErr error
		choice, readErr = readApproachChoice(ctx, queries, design)
		return readErr
	})
	return choice, err
}

// readDesignApproaches returns the approaches of the design, oldest first, read with queries. The newest approach
// recorded as recommended is the only one that reads as Recommended.
func readDesignApproaches(ctx context.Context, queries *sqlcgen.Queries, design string) ([]DesignApproach, error) {
	rows, err := queries.ListDesignApproaches(ctx, design)
	if err != nil {
		return nil, fmt.Errorf("list approaches of design %s: %w", design, err)
	}
	recommended := -1
	for i, row := range rows {
		if row.Recommended != 0 {
			recommended = i
		}
	}
	approaches := make([]DesignApproach, 0, len(rows))
	for i, row := range rows {
		research, decodeErr := decodeList[string](row.Research)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode research of approach %d of design %s: %w", row.Seq, design, decodeErr)
		}
		approaches = append(approaches, DesignApproach{
			Design: row.Design, Seq: row.Seq, Name: row.Name, Shape: row.Shape, Cost: row.Cost, Wins: row.Wins,
			Loses: row.Loses, Reconsider: row.Reconsider, Research: research, Recommended: i == recommended,
			Reason: row.Reason, Session: row.Session, At: row.At,
		})
	}
	return approaches, nil
}

// readApproachChoice returns the newest choice of the design, read with queries. It returns an error that matches
// ErrNoApproachChoice when the design has none.
func readApproachChoice(ctx context.Context, queries *sqlcgen.Queries, design string) (ApproachChoice, error) {
	rows, err := queries.ListDesignApproachChoices(ctx, design)
	if err != nil {
		return ApproachChoice{}, fmt.Errorf("list choices of design %s: %w", design, err)
	}
	if len(rows) == 0 {
		return ApproachChoice{}, fmt.Errorf("choice of design %s: %w", design, ErrNoApproachChoice)
	}
	row := rows[len(rows)-1]
	return ApproachChoice{
		Design: row.Design, Seq: row.Seq, Approach: row.Approach, Words: row.Words, Maker: row.Maker,
		Quote: row.Quote, MakerReason: row.MakerReason, Delegation: row.Delegation, Session: row.Session, At: row.At,
	}, nil
}

// nextApproachSeq returns the seq of the next approach after rows: one more than the highest seq rows hold, or 1 for
// none.
func nextApproachSeq(rows []sqlcgen.DesignApproach) int64 {
	next := int64(1)
	for _, row := range rows {
		if row.Seq >= next {
			next = row.Seq + 1
		}
	}
	return next
}

// nextChoiceSeq returns the seq of the next choice after rows: one more than the highest seq rows hold, or 1 for none.
func nextChoiceSeq(rows []sqlcgen.DesignApproachChoice) int64 {
	next := int64(1)
	for _, row := range rows {
		if row.Seq >= next {
			next = row.Seq + 1
		}
	}
	return next
}
