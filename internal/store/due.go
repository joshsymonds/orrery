package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// dueNamespace starts the parts every due row id hashes, so the ids are this package's.
const dueNamespace = "orrery-due"

// dueKindStep is the kind of the due row a step's usage limit stores.
const dueKindStep = "step"

// DueResume is a due row that is still due: its kind, its target and the reset it waits for.
type DueResume struct {
	ID     string
	Kind   string
	Target string
	Resets time.Time
}

// DueResumeClosed marks the due row ID done. It appends no event.
type DueResumeClosed struct {
	ID string
}

func (op DueResumeClosed) applyEngine(ctx context.Context, queries *sqlcgen.Queries, _ time.Time) error {
	if err := queries.CloseDueResume(ctx, op.ID); err != nil {
		return fmt.Errorf("close due resume %s: %w", op.ID, err)
	}
	return nil
}

// StepDueResumesClosed marks every due row of step ID done. It appends no event.
type StepDueResumesClosed struct {
	ID string
}

func (op StepDueResumesClosed) applyEngine(ctx context.Context, queries *sqlcgen.Queries, _ time.Time) error {
	closed := sqlcgen.CloseDueResumesOfParams{Kind: dueKindStep, Target: op.ID}
	if err := queries.CloseDueResumesOf(ctx, closed); err != nil {
		return fmt.Errorf("close due resumes of step %s: %w", op.ID, err)
	}
	return nil
}

// DueResumes returns the due rows that are still due, oldest reset first.
func (s *Store) DueResumes(ctx context.Context) ([]DueResume, error) {
	rows, err := sqlcgen.New(s.db).ListDueResumes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list due resumes: %w", err)
	}
	out := make([]DueResume, 0, len(rows))
	for _, row := range rows {
		out = append(out, DueResume{ID: row.ID, Kind: row.Kind, Target: row.Target, Resets: time.Unix(row.Resets, 0)})
	}
	return out, nil
}

// insertDueResume stores the due row of kind and target for the reset at the unix second resets. It stores nothing
// when a row of that kind and target already has a reset at or after resets, so a reset is resumed once, whatever
// state its row is in.
func insertDueResume(ctx context.Context, queries *sqlcgen.Queries, kind, target string, resets int64) error {
	if err := queries.InsertDueResume(ctx, sqlcgen.InsertDueResumeParams{
		ID: dueID(kind, target, resets), Kind: kind, Target: target, Resets: resets,
	}); err != nil {
		return fmt.Errorf("insert due resume of %s %s: %w", kind, target, err)
	}
	return nil
}

// dueID returns the id of the due row of kind and target for the reset at the unix second resets. It is the lowercase
// hex sha256 of the namespace, kind, target and reset joined with NUL.
func dueID(kind, target string, resets int64) string {
	parts := []string{dueNamespace, kind, target, strconv.FormatInt(resets, 10)}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}
