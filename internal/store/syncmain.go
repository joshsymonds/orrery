package store

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// syncPassed is the outcome of a sync run that advanced the epic's branch and stored base to its merge.
const syncPassed = "passed"

// SyncEnd is how a sync run of origin's main ends: the path and attempt of the run, its outcome and output, the commit
// of origin's main its merge holds when it passed, the merge the stored base moves to when it passed and that is not
// the run's revision, the text it tells, "" when it tells nothing, and the trigger it adds, nil when it adds none. A
// Trigger that is set is added pending by EndSync in its own write.
type SyncEnd struct {
	Path    string
	Attempt int64
	Outcome string
	Output  string
	Synced  string
	Merge   string
	Tell    string
	Trigger *NodeTrigger
}

// OriginRead is an epic's last read of origin's main, which a sync of origin's main starts from: the stored base it was
// read for, origin's commit it found, "" when it found none, what it found of that commit, as the interpreter names it,
// and when it was read, zero while origin's main was never read. Times are stored at whole seconds.
type OriginRead struct {
	Base   string
	Commit string
	State  string
	At     time.Time
}

// originReadOf returns the read of origin's main a stored epic holds, its time zero when none is stored.
func originReadOf(head sqlcgen.Epic) OriginRead {
	read := OriginRead{Base: head.OriginReadBase, Commit: head.OriginReadCommit, State: head.OriginReadState}
	if head.OriginReadAt != 0 {
		read.At = time.Unix(head.OriginReadAt, 0)
	}
	return read
}

// RecordOriginRead stores read as the last read of origin's main of epic, in place of the one it held, in one write.
func (s *Store) RecordOriginRead(ctx context.Context, epic string, read OriginRead) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		return setOriginRead(ctx, queries, epic, read)
	})
}

// setOriginRead stores read as the last read of origin's main of epic, in the caller's write.
func setOriginRead(ctx context.Context, queries *sqlcgen.Queries, epic string, read OriginRead) error {
	if err := queries.SetEpicOriginRead(ctx, sqlcgen.SetEpicOriginReadParams{
		Base: read.Base, Commit: read.Commit, State: read.State, At: read.At.Unix(), ID: epic,
	}); err != nil {
		return fmt.Errorf("record read of origin's main of epic %s: %w", epic, err)
	}
	return nil
}

// PrepareSync stores result as the result of the running sync run at path of epic at attempt, and revision as its
// revision, and stores read, the prepare's own read of origin's main, as the epic's last read, in one write. revision
// is the merge the sync made, "" when it made none. A run that already has a result, a run at another attempt and a run
// that is not running change nothing, the read among it, so the prepare is recorded once.
func (s *Store) PrepareSync(
	ctx context.Context, epic, path string, attempt int64, revision, result string, read OriginRead,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		prepared, err := queries.PrepareEpicSyncRun(ctx, sqlcgen.PrepareEpicSyncRunParams{
			Revision: revision, Result: result, Epic: epic, Path: path, Attempt: attempt,
		})
		if err != nil {
			return fmt.Errorf("prepare sync run %s of epic %s: %w", path, epic, err)
		}
		if prepared == 0 {
			return nil
		}
		return setOriginRead(ctx, queries, epic, read)
	})
}

// EndSync ends the running sync run at end.Path of epic at end.Attempt with its outcome and output, in one write. A
// passed run moves the stored base to end.Merge, or to the run's revision when end.Merge is "", clears the candidate
// and records end.Synced as the commit of origin's main the stored base merged. A Tell that is not empty is added as a
// tell, cut to TurnReasonMax bytes. A Trigger that is set is added pending. Output is cut to NodeOutputMax bytes. A run
// at another attempt and a run that is not running change nothing, so a repeat moves and tells nothing.
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

// RewakeSync stores, in one write, trigger as a pending trigger of epic at at and turn as a gap turn of epic, so that a
// sync whose gap is live wakes the planner again. A trigger id already stored writes nothing. A gap with GapTurnBound
// turns is an error wrapping ErrGapSpent, and writes nothing.
func (s *Store) RewakeSync(ctx context.Context, epic string, trigger NodeTrigger, turn GapTurn, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		stored, err := queries.CountEpicTrigger(ctx, sqlcgen.CountEpicTriggerParams{Epic: epic, ID: trigger.ID})
		if err != nil {
			return fmt.Errorf("count trigger %s of epic %s: %w", trigger.ID, epic, err)
		}
		if stored > 0 {
			return nil
		}
		err = insertGapTurn(ctx, queries, epic, turn.Gap, turn.Turn, turn.Reason, at)
		if err != nil {
			return err
		}
		if err = queries.InsertEpicTrigger(ctx, sqlcgen.InsertEpicTriggerParams{
			Epic: epic, ID: trigger.ID, Cause: trigger.Cause, State: triggerPending, Turn: 0, Created: at.Unix(),
		}); err != nil {
			return fmt.Errorf("insert trigger %s of epic %s: %w", trigger.ID, epic, err)
		}
		return nil
	})
}

// settleSync moves the stored base of epic for a sync run that passed (moveSyncedBase), adds end.Trigger pending when
// it is set, and adds end.Tell as a tell when it is not empty, in the caller's write.
func settleSync(ctx context.Context, queries *sqlcgen.Queries, epic string, end SyncEnd, at time.Time) error {
	if end.Outcome == syncPassed {
		if err := moveSyncedBase(ctx, queries, epic, end); err != nil {
			return err
		}
	}
	if end.Trigger != nil {
		if err := queries.InsertEpicTrigger(ctx, sqlcgen.InsertEpicTriggerParams{
			Epic: epic, ID: end.Trigger.ID, Cause: end.Trigger.Cause, State: triggerPending, Turn: 0,
			Created: at.Unix(),
		}); err != nil {
			return fmt.Errorf("insert trigger %s of epic %s: %w", end.Trigger.ID, epic, err)
		}
	}
	if end.Tell == "" {
		return nil
	}
	return insertTell(ctx, queries, epic, capTurnText(end.Tell), at)
}

// moveSyncedBase moves the stored base of epic to end.Merge, or to the revision of the sync run end names when
// end.Merge is "", clears the candidate and records the commit of origin's main the run merged, in the caller's write.
func moveSyncedBase(ctx context.Context, queries *sqlcgen.Queries, epic string, end SyncEnd) error {
	merge := end.Merge
	if merge == "" {
		run, err := queries.GetEpicNodeRun(ctx, sqlcgen.GetEpicNodeRunParams{Epic: epic, Path: end.Path})
		if err != nil {
			return fmt.Errorf("read sync run %s of epic %s: %w", end.Path, epic, err)
		}
		merge = run.Revision
	}
	if merge == "" {
		return fmt.Errorf("sync run %s of epic %s passed with no merge", end.Path, epic)
	}
	if err := queries.SyncEpicStoredBase(ctx, sqlcgen.SyncEpicStoredBaseParams{
		StoredBase: merge, Synced: end.Synced, ID: epic,
	}); err != nil {
		return fmt.Errorf("move stored base of epic %s to its sync: %w", epic, err)
	}
	return nil
}
