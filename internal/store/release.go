package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The parts a release run records: the candidate the release starts from, a release step, the judge's verdict, and a
// named position the owner set.
const (
	ReleasePartCandidate = "candidate"
	ReleasePartStep      = "release"
	ReleasePartJudge     = "judge"
	ReleasePartNamed     = "named"
)

// The phases an epic's release moves through: clean until it takes the release slot, releasing while it holds the
// slot, and stopped when a release step stops it. Clean is also the phase an epic whose frozen review found it clean
// ends in, and EndEpic stores the epic's stored base as its review's clean revision then.
const (
	PhaseClean     = "clean"
	PhaseReleasing = "releasing"
	PhaseStopped   = "stopped"
)

// releaseStopOutcome is the outcome a stopped release's node run parks with.
const releaseStopOutcome = "failed"

// ReleaseRun is one record of an epic's release. Part is the ReleasePart it belongs to and Position numbers it within
// its part. Attempt is the attempt of the release node run it was recorded under. Step names the release step, Start,
// Head and End are the revisions it recorded, and Commits the commits it made. Evidence names the evidence it ran, and
// Passed and Reason say whether it passed and why not. At is the time it was recorded, stored at whole seconds.
type ReleaseRun struct {
	Part     string
	Position int64
	Attempt  int64
	Step     string
	Start    string
	Head     string
	End      string
	Commits  []string
	Evidence string
	Passed   bool
	Reason   string
	At       time.Time
}

// RecordRelease stores run as a release run of epic, in one write. A run with the same part, position and attempt as
// one stored already changes nothing, so a repeat is a no-op.
func (s *Store) RecordRelease(ctx context.Context, epic string, run ReleaseRun) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		return recordRelease(ctx, queries, epic, run)
	})
}

// recordRelease stores run as a release run of epic in the caller's write, as RecordRelease does.
func recordRelease(ctx context.Context, queries *sqlcgen.Queries, epic string, run ReleaseRun) error {
	commits, err := encodeList(run.Commits)
	if err != nil {
		return fmt.Errorf("encode commits of release run %s of epic %s: %w", run.Part, epic, err)
	}
	if err = queries.InsertEpicReleaseRun(ctx, sqlcgen.InsertEpicReleaseRunParams{
		Epic: epic, Part: run.Part, Position: run.Position, Attempt: run.Attempt, Step: run.Step,
		Start: run.Start, Head: run.Head, Finish: run.End, Commits: commits, Evidence: run.Evidence,
		Passed: run.Passed, Reason: run.Reason, At: run.At.Unix(),
	}); err != nil {
		return fmt.Errorf("insert release run %s of epic %s: %w", run.Part, epic, err)
	}
	return nil
}

// TakeReleaseSlot takes the release slot of epic's repository for epic, in one write, and returns the slot's holder
// after the write. An epic in clean, releasing or stopped takes the slot when it is free, or keeps it when it already
// holds it, and only a clean epic that holds it moves to releasing. When another epic holds the slot nothing changes
// and that epic is returned. An epic in any other phase takes nothing. It returns an error matching ErrNoEpic when no
// epic has the id.
func (s *Store) TakeReleaseSlot(ctx context.Context, epic string) (string, error) {
	var holder string
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		row, err := queries.GetEpic(ctx, epic)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("epic %s: %w", epic, ErrNoEpic)
		}
		if err != nil {
			return fmt.Errorf("get epic %s: %w", epic, err)
		}
		if takesReleaseSlot(row.Phase) {
			if err = queries.InsertEpicReleaseSlot(ctx, sqlcgen.InsertEpicReleaseSlotParams{
				Repository: row.Repository, Epic: epic,
			}); err != nil {
				return fmt.Errorf("take release slot of epic %s: %w", epic, err)
			}
		}
		if holder, err = releaseSlotHolder(ctx, queries, row.Repository); err != nil {
			return err
		}
		if holder == epic && row.Phase == PhaseClean {
			if err = queries.StartEpicRelease(ctx, epic); err != nil {
				return fmt.Errorf("start release of epic %s: %w", epic, err)
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return holder, nil
}

// takesReleaseSlot reports whether an epic in phase may take the release slot of its repository.
func takesReleaseSlot(phase string) bool {
	return phase == PhaseClean || phase == PhaseReleasing || phase == PhaseStopped
}

// releaseSlotHolder returns the epic that holds the release slot of repository, or "" when the slot is free.
func releaseSlotHolder(ctx context.Context, queries *sqlcgen.Queries, repository string) (string, error) {
	holder, err := queries.GetEpicReleaseSlotHolder(ctx, repository)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read release slot of %s: %w", repository, err)
	}
	return holder, nil
}

// FreeReleaseSlot frees the release slot epic holds, in one write. A slot another epic holds is left as it is.
func (s *Store) FreeReleaseSlot(ctx context.Context, epic string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.FreeEpicReleaseSlot(ctx, epic); err != nil {
			return fmt.Errorf("free release slot of epic %s: %w", epic, err)
		}
		return nil
	})
}

// StopRelease stops the release of epic at the running node run at path and attempt, in one write. The run parks with
// outcome failed and reason as its output, the epic parks with reason and adds reason as a tell, and the epic moves
// from releasing to stopped. A run at another attempt and a run that is not running change nothing and add no tell, so
// a repeat is a no-op.
func (s *Store) StopRelease(ctx context.Context, epic, path string, attempt int64, reason string, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		parked, err := queries.ParkEpicNodeRun(ctx, sqlcgen.ParkEpicNodeRunParams{
			Outcome: releaseStopOutcome, Output: capNodeOutput(reason), Epic: epic, Path: path, Attempt: attempt,
		})
		if err != nil {
			return fmt.Errorf("park node run %s of epic %s: %w", path, epic, err)
		}
		if parked == 0 {
			return nil
		}
		return stopEpicRelease(ctx, queries, epic, reason, at)
	})
}

// stopEpicRelease parks epic with reason and adds reason as a tell, then moves epic from releasing to stopped, in the
// caller's write.
func stopEpicRelease(ctx context.Context, queries *sqlcgen.Queries, epic, reason string, at time.Time) error {
	if err := parkWithTell(ctx, queries, epic, capTurnText(reason), at); err != nil {
		return err
	}
	if err := queries.StopEpicRelease(ctx, epic); err != nil {
		return fmt.Errorf("stop release of epic %s: %w", epic, err)
	}
	return nil
}

// readReleaseRuns returns the release runs of epic as queries read them, ordered by attempt, part and position.
func readReleaseRuns(ctx context.Context, queries *sqlcgen.Queries, epic string) ([]ReleaseRun, error) {
	stored, err := queries.ListEpicReleaseRuns(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list release runs of epic %s: %w", epic, err)
	}
	var runs []ReleaseRun
	for _, row := range stored {
		var commits []string
		if commits, err = decodeList[string](row.Commits); err != nil {
			return nil, fmt.Errorf("decode commits of release run %s of epic %s: %w", row.Part, epic, err)
		}
		runs = append(runs, ReleaseRun{
			Part: row.Part, Position: row.Position, Attempt: row.Attempt, Step: row.Step, Start: row.Start,
			Head: row.Head, End: row.Finish, Commits: commits, Evidence: row.Evidence, Passed: row.Passed,
			Reason: row.Reason, At: time.Unix(row.At, 0),
		})
	}
	return runs, nil
}
