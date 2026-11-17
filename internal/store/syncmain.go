package store

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// syncPassed is the outcome of a sync run that advanced the epic's branch and stored base to its merge.
const syncPassed = "passed"

// SyncEnd is how a sync run of origin's main ends: the path and attempt of the run, its outcome and output, the commit of
// origin's main its merge holds when it passed, and the text it tells, "" when it tells nothing.
type SyncEnd struct {
	Path    string
	Attempt int64
	Outcome string
	Output  string
	Synced  string
	Tell    string
}

// PrepareSync stores result as the result of the running sync run at path of epic at attempt, and revision as its
// revision, in one write. revision is the merge the sync made, "" when it made none. A run that already has a result, a
// run at another attempt and a run that is not running change nothing, so the prepare is recorded once.
func (s *Store) PrepareSync(ctx context.Context, epic, path string, attempt int64, revision, result string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.PrepareEpicSyncRun(ctx, sqlcgen.PrepareEpicSyncRunParams{
			Revision: revision, Result: result, Epic: epic, Path: path, Attempt: attempt,
		}); err != nil {
			return fmt.Errorf("prepare sync run %s of epic %s: %w", path, epic, err)
		}
		return nil
	})
}

// EndSync ends the running sync run at end.Path of epic at end.Attempt with its outcome and output, in one write. A
// passed run moves the stored base to the run's revision, clears the candidate and records end.Synced as the commit of
// origin's main the stored base merged. A Tell that is not empty is added as a tell, cut to TurnReasonMax bytes. Output
// is cut to NodeOutputMax bytes. A run at another attempt and a run that is not running change nothing, so a repeat
// moves and tells nothing.
func (s *Store) EndSync(ctx context.Context, epic string, end SyncEnd, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		ended, err := queries.EndEpicNodeRunAttempt(ctx, sqlcgen.EndEpicNodeRunAttemptParams{
			Outcome: end.Outcome, Ended: at.Unix(), Output: capNodeOutput(end.Output), Epic: epic, Path: end.Path,
			Attempt: end.Attempt,
		})
		if err != nil {
			return fmt.Errorf("end sync run %s of epic %s: %w", end.Path, epic, err)
		}
		if ended == 0 {
			return nil
		}
		return settleSync(ctx, queries, epic, end, at)
	})
}

// settleSync moves the stored base of epic for a sync run that passed (moveSyncedBase) and adds end.Tell as a tell when
// it is not empty, in the caller's write.
func settleSync(ctx context.Context, queries *sqlcgen.Queries, epic string, end SyncEnd, at time.Time) error {
	if end.Outcome == syncPassed {
		if err := moveSyncedBase(ctx, queries, epic, end); err != nil {
			return err
		}
	}
	if end.Tell == "" {
		return nil
	}
	return insertTell(ctx, queries, epic, capTurnText(end.Tell), at)
}

// moveSyncedBase moves the stored base of epic to the revision of the sync run end names, clears the candidate and
// records the commit of origin's main the run merged, in the caller's write.
func moveSyncedBase(ctx context.Context, queries *sqlcgen.Queries, epic string, end SyncEnd) error {
	run, err := queries.GetEpicNodeRun(ctx, sqlcgen.GetEpicNodeRunParams{Epic: epic, Path: end.Path})
	if err != nil {
		return fmt.Errorf("read sync run %s of epic %s: %w", end.Path, epic, err)
	}
	if run.Revision == "" {
		return fmt.Errorf("sync run %s of epic %s passed with no merge", end.Path, epic)
	}
	if err = queries.SyncEpicStoredBase(ctx, sqlcgen.SyncEpicStoredBaseParams{
		StoredBase: run.Revision, Synced: end.Synced, ID: epic,
	}); err != nil {
		return fmt.Errorf("move stored base of epic %s to its sync: %w", epic, err)
	}
	return nil
}
