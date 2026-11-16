package store

import (
	"context"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// LabelFollow is one label's count of how often the owner took the advice: Answered counts its answered questions
// that had a recommendation, and Followed counts those answered with the recommended choice.
type LabelFollow struct {
	Label    string
	Answered int64
	Followed int64
}

// Follows returns the follow counts of every label across all epics, in label order. A label appears when at least
// one of its answered questions had a recommendation. Notes, open and replaced questions, and questions with no label
// or no recommendation are never counted.
func (s *Store) Follows(ctx context.Context) ([]LabelFollow, error) {
	rows, err := sqlcgen.New(s.db).ListEpicQuestionFollows(ctx)
	if err != nil {
		return nil, fmt.Errorf("list question follows: %w", err)
	}
	return mapInOrder(rows, func(row sqlcgen.ListEpicQuestionFollowsRow) LabelFollow {
		return LabelFollow{Label: row.Label, Answered: row.Answered, Followed: row.Followed}
	}), nil
}
