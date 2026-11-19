package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// GapTurnBound is the most planner turns a gap is given: RecordGapTurn refuses a gap that has already had this many.
const GapTurnBound = 2

// ErrGapSpent reports that a gap has had GapTurnBound planner turns, so RecordGapTurn gives it no more.
var ErrGapSpent = errors.New("gap has spent its planner turns")

// GapDeclaration is one gap a planner turn declared it cannot resolve without the owner: the gap's id, the reason the
// planner cannot resolve it, and what the planner recommends the owner do, release or correct.
type GapDeclaration struct {
	Gap, Reason, Recommended string
}

// ReviewRerun is a rerun of an epic's review that a rerun_review action carried out: the reviewer whose verdict the review
// runs again for, the action's turn and 1-based position in the turn's return, and when the action was carried out.
type ReviewRerun struct {
	Reviewer       string
	Turn, Position int64
	Created        time.Time
}

// GapTurn is a planner turn a gap was given: the gap, the turn, the reason, and when it was recorded.
type GapTurn struct {
	Gap     string
	Turn    int64
	Reason  string
	Created time.Time
}

// checkGapDeclarations returns an error naming the first declaration whose recommendation is not release or correct.
func checkGapDeclarations(gaps []GapDeclaration) error {
	for _, gap := range gaps {
		if gap.Recommended != "release" && gap.Recommended != "correct" {
			return fmt.Errorf("gap %s: recommended %q is not release or correct", gap.Gap, gap.Recommended)
		}
	}
	return nil
}

// insertUnresolvable stores gaps as the gaps turn of epic declared it cannot resolve, at their 1-based positions, in the
// caller's write.
func insertUnresolvable(
	ctx context.Context, queries *sqlcgen.Queries, epic string, turn int64, gaps []GapDeclaration,
) error {
	return insertInOrder(gaps, func(position int64, gap GapDeclaration) error {
		return queries.InsertPlannerUnresolvable(ctx, sqlcgen.InsertPlannerUnresolvableParams{
			Epic: epic, Turn: turn, Position: position, Gap: gap.Gap, Reason: gap.Reason, Recommended: gap.Recommended,
		})
	})
}

// UnresolvableGaps returns the gaps the epic's highest-seq succeeded turn declared it cannot resolve without the owner, in
// the order they were declared, or nil when that turn declared none.
func (s *Store) UnresolvableGaps(ctx context.Context, epic string) ([]GapDeclaration, error) {
	rows, err := sqlcgen.New(s.db).ListUnresolvableOfLastTurn(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list unresolvable gaps of epic %s: %w", epic, err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return mapInOrder(rows, func(row sqlcgen.PlannerUnresolvable) GapDeclaration {
		return GapDeclaration{Gap: row.Gap, Reason: row.Reason, Recommended: row.Recommended}
	}), nil
}

// rerunReview records, in the caller's write, that the epic's review runs again for the action's reviewer. An action that
// names no reviewer, or a reviewer the epic's stored review holds no verdict by, is a RefusalError and writes nothing.
func rerunReview(ctx context.Context, queries *sqlcgen.Queries, action PlannerAction, at time.Time) error {
	if action.Reviewer == "" {
		return RefusalError(fmt.Sprintf("rerun review of epic %s names no reviewer", action.Epic))
	}
	verdicts, err := queries.ListEpicReviewVerdicts(ctx, action.Epic)
	if err != nil {
		return fmt.Errorf("list verdicts of epic %s: %w", action.Epic, err)
	}
	if !slices.ContainsFunc(verdicts, func(verdict sqlcgen.EpicReviewVerdict) bool {
		return verdict.Reviewer == action.Reviewer
	}) {
		return RefusalError(fmt.Sprintf("epic %s's review holds no verdict by %s", action.Epic, action.Reviewer))
	}
	if err = queries.InsertEpicReviewRerun(ctx, sqlcgen.InsertEpicReviewRerunParams{
		Epic: action.Epic, Reviewer: action.Reviewer, Turn: action.Turn, Position: action.Position, Created: at.Unix(),
	}); err != nil {
		return fmt.Errorf("insert rerun of reviewer %s of epic %s: %w", action.Reviewer, action.Epic, err)
	}
	return nil
}

// ReviewReruns returns the reruns of the epic's review that its rerun_review actions carried out, in turn then position
// order.
func (s *Store) ReviewReruns(ctx context.Context, epic string) ([]ReviewRerun, error) {
	rows, err := sqlcgen.New(s.db).ListEpicReviewReruns(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list review reruns of epic %s: %w", epic, err)
	}
	return mapInOrder(rows, func(row sqlcgen.EpicReviewRerun) ReviewRerun {
		return ReviewRerun{
			Reviewer: row.Reviewer, Turn: row.Turn, Position: row.Position, Created: time.Unix(row.Created, 0),
		}
	}), nil
}

// RecordGapTurn records in one write that gap of epic was given to planner turn turn, for reason, at at. Recording a gap
// and turn that are already recorded changes nothing and returns nil. A gap that already has GapTurnBound turns, and no
// record of turn, is an error wrapping ErrGapSpent, and writes nothing.
func (s *Store) RecordGapTurn(ctx context.Context, epic, gap string, turn int64, reason string, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		given, err := queries.ListEpicGapTurnsOf(ctx, sqlcgen.ListEpicGapTurnsOfParams{Epic: epic, Gap: gap})
		if err != nil {
			return fmt.Errorf("list turns of gap %s of epic %s: %w", gap, epic, err)
		}
		if slices.ContainsFunc(given, func(row sqlcgen.EpicGapTurn) bool { return row.Turn == turn }) {
			return nil
		}
		if len(given) >= GapTurnBound {
			return fmt.Errorf("gap %s of epic %s has %d turns: %w", gap, epic, len(given), ErrGapSpent)
		}
		if err = queries.InsertEpicGapTurn(ctx, sqlcgen.InsertEpicGapTurnParams{
			Epic: epic, Gap: gap, Turn: turn, Reason: reason, Created: at.Unix(),
		}); err != nil {
			return fmt.Errorf("insert turn %d of gap %s of epic %s: %w", turn, gap, epic, err)
		}
		return nil
	})
}

// GapTurns returns the planner turns each gap of the epic was given, in gap then turn order.
func (s *Store) GapTurns(ctx context.Context, epic string) ([]GapTurn, error) {
	rows, err := sqlcgen.New(s.db).ListEpicGapTurns(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list gap turns of epic %s: %w", epic, err)
	}
	return mapInOrder(rows, func(row sqlcgen.EpicGapTurn) GapTurn {
		return GapTurn{Gap: row.Gap, Turn: row.Turn, Reason: row.Reason, Created: time.Unix(row.Created, 0)}
	}), nil
}
