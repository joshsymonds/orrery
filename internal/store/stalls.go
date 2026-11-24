package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// StallMark is the progress of an epic that a stall check compares between its passes: its task count, its node run
// count with their attempts, and its action count.
type StallMark struct {
	Tasks, Runs, Actions int64
}

// EpicStall is a stall the stall check found in an epic: its seq, when it was found, the StallMark the epic had then,
// and whether the epic parked for it.
type EpicStall struct {
	Seq    int64
	At     time.Time
	Mark   StallMark
	Parked bool
}

// StallMark returns the StallMark of epic as the store holds it now.
func (s *Store) StallMark(ctx context.Context, epic string) (StallMark, error) {
	counts, err := sqlcgen.New(s.read).CountEpicStallMark(ctx, epic)
	if err != nil {
		return StallMark{}, fmt.Errorf("count stall mark of epic %s: %w", epic, err)
	}
	return StallMark{Tasks: counts.Tasks, Runs: counts.Runs, Actions: counts.Actions}, nil
}

// LatestStall returns the stall with the highest seq of epic, and whether epic has any stall.
func (s *Store) LatestStall(ctx context.Context, epic string) (EpicStall, bool, error) {
	row, err := sqlcgen.New(s.read).LatestEpicStall(ctx, epic)
	if errors.Is(err, sql.ErrNoRows) {
		return EpicStall{}, false, nil
	}
	if err != nil {
		return EpicStall{}, false, fmt.Errorf("read latest stall of epic %s: %w", epic, err)
	}
	return stallOf(row), true, nil
}

// WakeStall records a stall row and adds the pending trigger `stall:<seq>` with cause, in one write.
func (s *Store) WakeStall(ctx context.Context, epic, cause string, mark StallMark, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		seq, err := insertStall(ctx, queries, epic, mark, false, at)
		if err != nil {
			return err
		}
		return insertNodeTrigger(ctx, queries, epic, NodeTrigger{ID: stallTriggerID(seq), Cause: cause}, at)
	})
}

// ParkStall records a parked stall row and parks the epic with ParkClassStall and reason, in one write.
func (s *Store) ParkStall(ctx context.Context, epic, reason string, mark StallMark, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if _, err := insertStall(ctx, queries, epic, mark, true, at); err != nil {
			return err
		}
		return parkEpicAs(ctx, queries, epic, ParkClassStall, "", reason, at)
	})
}

// stallTriggerID returns the id of the pending trigger that wakes the planner for the stall numbered seq.
func stallTriggerID(seq int64) string {
	return fmt.Sprintf("stall:%d", seq)
}

// insertStall adds a stall row to epic at the next seq, with mark and parked as given, in the caller's write, and
// returns that seq. The first stall of an epic is seq 1. An unknown epic is ErrNoEpic and writes nothing.
func insertStall(
	ctx context.Context, queries *sqlcgen.Queries, epic string, mark StallMark, parked bool, at time.Time,
) (int64, error) {
	if _, err := epicHead(ctx, queries, epic); err != nil {
		return 0, err
	}
	// On no stall yet, LatestEpicStall gives its zero row, so the next seq is 1.
	latest, err := queries.LatestEpicStall(ctx, epic)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("read latest stall of epic %s: %w", epic, err)
	}
	seq := latest.Seq + 1
	var parkedFlag int64
	if parked {
		parkedFlag = 1
	}
	err = queries.InsertEpicStall(ctx, sqlcgen.InsertEpicStallParams{
		Epic: epic, Seq: seq, At: at.Unix(), Tasks: mark.Tasks, Runs: mark.Runs, Actions: mark.Actions,
		Parked: parkedFlag,
	})
	if err != nil {
		return 0, fmt.Errorf("insert stall %d of epic %s: %w", seq, epic, err)
	}
	return seq, nil
}

// insertStallRetryTrigger adds to epic the pending trigger `stall-retry:<latest stall seq>` that wakes the planner when
// a stall park of epic is retried, with a cause saying so, in the caller's write. A stall park always has its stall
// row, since ParkStall writes both; with none, the id is numbered 0.
func insertStallRetryTrigger(ctx context.Context, queries *sqlcgen.Queries, epic string, at time.Time) error {
	latest, err := queries.LatestEpicStall(ctx, epic)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read latest stall of epic %s: %w", epic, err)
	}
	return insertNodeTrigger(ctx, queries, epic, NodeTrigger{
		ID:    fmt.Sprintf("stall-retry:%d", latest.Seq),
		Cause: "the stall park was retried, so the planner resumes the epic",
	}, at)
}

// stallOf returns the EpicStall row reads as.
func stallOf(row sqlcgen.EpicStall) EpicStall {
	return EpicStall{
		Seq:    row.Seq,
		At:     time.Unix(row.At, 0),
		Mark:   StallMark{Tasks: row.Tasks, Runs: row.Runs, Actions: row.Actions},
		Parked: row.Parked != 0,
	}
}
