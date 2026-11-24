package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrNoClosureSkip reports that a design has no closure skip recorded.
var ErrNoClosureSkip = errors.New("no closure skip")

// AddDesignDisposition appends disposition to its design's dispositions, numbered as the design's next disposition:
// counted from 1 within the design, one more than the highest number the design already holds. disposition.Seq is
// ignored. Rows only append, so an earlier disposition of an item stays stored after a later one.
func (s *Store) AddDesignDisposition(ctx context.Context, disposition DesignDisposition) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		next, err := queries.NextDesignDispositionSeq(ctx, disposition.Design)
		if err != nil {
			return fmt.Errorf("number a disposition of design %s: %w", disposition.Design, err)
		}
		if err = queries.InsertDesignDisposition(ctx, sqlcgen.InsertDesignDispositionParams{
			Design: disposition.Design, Seq: next, Item: disposition.Item, Disposition: disposition.Disposition,
			Reason: disposition.Reason, Boundary: disposition.Boundary, Maker: disposition.Maker.Kind,
			Quote: disposition.Maker.Quote, MakerReason: disposition.Maker.Reason,
			Delegation: disposition.Maker.Delegation, Session: disposition.Session, At: disposition.At.Unix(),
		}); err != nil {
			return fmt.Errorf("insert disposition %d of design %s: %w", next, disposition.Design, err)
		}
		return nil
	})
}

// DesignDispositions returns the dispositions stored against the design, oldest first, which is by seq. A design with
// no dispositions has none.
func (s *Store) DesignDispositions(ctx context.Context, design string) ([]DesignDisposition, error) {
	rows, err := sqlcgen.New(s.read).ListDesignDispositions(ctx, design)
	if err != nil {
		return nil, fmt.Errorf("list dispositions of design %s: %w", design, err)
	}
	dispositions := make([]DesignDisposition, 0, len(rows))
	for _, row := range rows {
		dispositions = append(dispositions, DesignDisposition{
			Design: row.Design, Seq: row.Seq, Item: row.Item, Disposition: row.Disposition, Reason: row.Reason,
			Boundary: row.Boundary,
			Maker: StageMaker{
				Kind: row.Maker, Quote: row.Quote, Reason: row.MakerReason, Delegation: row.Delegation,
			},
			Session: row.Session, At: time.Unix(row.At, 0),
		})
	}
	return dispositions, nil
}

// RecordClosureSkip stores skip as the record that its design's closure was skipped. Each design has at most one, so a
// second record is refused by the table's primary key, and nothing is stored.
func (s *Store) RecordClosureSkip(ctx context.Context, skip ClosureSkip) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.InsertDesignClosureSkip(ctx, sqlcgen.InsertDesignClosureSkipParams{
			Design: skip.Design, Reason: skip.Reason, Maker: skip.Maker.Kind, Quote: skip.Maker.Quote,
			MakerReason: skip.Maker.Reason, Delegation: skip.Maker.Delegation, Session: skip.Session,
			At: skip.At.Unix(),
		}); err != nil {
			return fmt.Errorf("record closure skip of design %s: %w", skip.Design, err)
		}
		return nil
	})
}

// ClosureSkip returns the closure skip recorded against the design. It returns an error that matches ErrNoClosureSkip
// when the design has none.
func (s *Store) ClosureSkip(ctx context.Context, design string) (ClosureSkip, error) {
	row, err := sqlcgen.New(s.read).GetDesignClosureSkip(ctx, design)
	if errors.Is(err, sql.ErrNoRows) {
		return ClosureSkip{}, fmt.Errorf("closure skip of design %s: %w", design, ErrNoClosureSkip)
	}
	if err != nil {
		return ClosureSkip{}, fmt.Errorf("read closure skip of design %s: %w", design, err)
	}
	return ClosureSkip{
		Design: row.Design, Reason: row.Reason,
		Maker:   StageMaker{Kind: row.Maker, Quote: row.Quote, Reason: row.MakerReason, Delegation: row.Delegation},
		Session: row.Session, At: time.Unix(row.At, 0),
	}, nil
}
