package store

import (
	"context"
	"fmt"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// NodeOutputMax is the most bytes of a node run's output the store keeps.
const NodeOutputMax = 4096

// workNamespace starts the parts every node work id hashes, so the ids are this package's and not NodeRunID's.
const workNamespace = "orrery-workflow-work"

// LostAtRestart is the text a lost node check's NotRun and a lost evidence capture's NotCaptured carry: the gate that
// ran it cannot be readopted at a restart.
const LostAtRestart = "lost at a restart"

// NodeWorkID returns the id of part of the work of the node run runID at attempt, such as the check or step the work
// records under that id. It is the first 32 hex digits of the lowercase hex sha256 of the work namespace, runID, attempt
// and part joined with NUL, so the same inputs give the same id.
func NodeWorkID(runID string, attempt int64, part string) string {
	return nodeHash(workNamespace, runID, strconv.FormatInt(attempt, 10), part)
}

// PrepareNode stores revision and refs as the revision and refs of the running run at path of epic at attempt, in one
// write. A run that already has a revision, a run at another attempt and a run that is not running change nothing, so
// a repeat is a no-op.
func (s *Store) PrepareNode(ctx context.Context, epic, path string, attempt int64, revision, refs string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.PrepareEpicNodeRun(ctx, sqlcgen.PrepareEpicNodeRunParams{
			Revision: revision, Refs: refs, Epic: epic, Path: path, Attempt: attempt,
		}); err != nil {
			return fmt.Errorf("prepare node run %s of epic %s: %w", path, epic, err)
		}
		return nil
	})
}

// RecordNodeResult stores result as the result of the running run at path of epic at attempt, in one write. A run that
// already has a result, a run at another attempt and a run that is not running change nothing, so a repeat is a no-op.
func (s *Store) RecordNodeResult(ctx context.Context, epic, path string, attempt int64, result string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.RecordEpicNodeResult(ctx, sqlcgen.RecordEpicNodeResultParams{
			Result: result, Epic: epic, Path: path, Attempt: attempt,
		}); err != nil {
			return fmt.Errorf("record result of node run %s of epic %s: %w", path, epic, err)
		}
		return nil
	})
}

// CheckNodeRefs records, in one write, that the refs of the running run at path of epic at attempt were checked. A
// non-empty park parks the run and epic with park as one tell, and each of tells is then added as a tell. A run whose
// refs were checked already, a run at another attempt and a run that is not running change nothing, so a repeat adds
// no tell.
func (s *Store) CheckNodeRefs(
	ctx context.Context, epic, path string, attempt int64, park string, tells []string, at time.Time,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		checked, err := queries.CheckEpicNodeRefs(ctx, sqlcgen.CheckEpicNodeRefsParams{
			Epic: epic, Path: path, Attempt: attempt,
		})
		if err != nil {
			return fmt.Errorf("check refs of node run %s of epic %s: %w", path, epic, err)
		}
		if checked == 0 {
			return nil
		}
		if park == "" {
			return insertTells(ctx, queries, epic, tells, at)
		}
		return parkCheckedRun(ctx, queries, epic, path, attempt, park, tells, at)
	})
}

// parkCheckedRun parks the running run at path of epic at attempt, parks epic with park and adds park as a tell, then
// adds each of tells, in the caller's write.
func parkCheckedRun(
	ctx context.Context, queries *sqlcgen.Queries, epic, path string, attempt int64, park string, tells []string,
	at time.Time,
) error {
	// A running run has no outcome or output, so parking it with neither leaves them as they are.
	if _, err := queries.ParkEpicNodeRun(ctx, sqlcgen.ParkEpicNodeRunParams{
		Epic: epic, Path: path, Attempt: attempt,
	}); err != nil {
		return fmt.Errorf("park node run %s of epic %s: %w", path, epic, err)
	}
	if err := parkWithTell(ctx, queries, epic, capTurnText(park), at); err != nil {
		return err
	}
	return insertTells(ctx, queries, epic, tells, at)
}

// ParkNode parks the running run at path of epic at attempt with outcome and output, parks epic with reason and adds
// reason as a tell, in one write. Output is cut to NodeOutputMax bytes and reason to TurnReasonMax. A run at another
// attempt and a run that is not running change nothing and add no tell, so a repeat is a no-op.
func (s *Store) ParkNode(
	ctx context.Context, epic, path string, attempt int64, outcome, output, reason string, at time.Time,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		_, err := parkNodeRun(ctx, queries, epic, path, attempt, outcome, output, reason, at)
		return err
	})
}

// parkNodeRun parks the running run at path of epic at attempt with outcome and output, and parks epic with reason and
// adds reason as a tell, in the caller's write. It reports whether a running run was parked, which is meaningless when
// err is not nil.
func parkNodeRun(
	ctx context.Context, queries *sqlcgen.Queries, epic, path string, attempt int64, outcome, output, reason string,
	at time.Time,
) (bool, error) {
	parked, err := queries.ParkEpicNodeRun(ctx, sqlcgen.ParkEpicNodeRunParams{
		Outcome: outcome, Output: capNodeOutput(output), Epic: epic, Path: path, Attempt: attempt,
	})
	if err != nil {
		return false, fmt.Errorf("park node run %s of epic %s: %w", path, epic, err)
	}
	if parked == 0 {
		return false, nil
	}
	return true, parkWithTell(ctx, queries, epic, capTurnText(reason), at)
}

// EndNodesWaking ends each running node run of epic at the path of ends with its outcome and output, and adds trigger
// pending, in one write. A trigger whose id is stored in any state is not added, so a repeat changes nothing.
func (s *Store) EndNodesWaking(
	ctx context.Context,
	epic string,
	ends []NodeEnd,
	trigger NodeTrigger,
	at time.Time,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := endNodeRuns(ctx, queries, epic, ends, at); err != nil {
			return err
		}
		return insertNodeTrigger(ctx, queries, epic, trigger, at)
	})
}

// RetryNode turns the parked run of node in epic running at its next attempt, with its outcome, output, revision, refs,
// refs checked, result and end cleared, and unparks epic, in one write. The park ends as RetryEpic ends it. A node with
// no parked run is a RefusalError, and so is an epic parked for good, which changes nothing.
func (s *Store) RetryNode(ctx context.Context, epic, node string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		return retryNode(ctx, queries, epic, node)
	})
}

// retryNode turns the parked run of node in epic running at its next attempt and unparks epic, in the caller's write,
// as RetryNode does. A node with no parked run is a RefusalError, and so is an epic parked for good.
func retryNode(ctx context.Context, queries *sqlcgen.Queries, epic, node string) error {
	retried, err := queries.RetryEpicNode(ctx, sqlcgen.RetryEpicNodeParams{Epic: epic, Node: node})
	if err != nil {
		return fmt.Errorf("retry node %s of epic %s: %w", node, epic, err)
	}
	if retried == 0 {
		return RefusalError(fmt.Sprintf("node %s of epic %s is not parked", node, epic))
	}
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return err
	}
	if head.Phase == phaseMetAtBase {
		return metAtBaseRefusal(epic, head.Parked)
	}
	highest, err := queries.MaxEpicTurnSeq(ctx, epic)
	if err != nil {
		return fmt.Errorf("read turn count of epic %s: %w", epic, err)
	}
	if err = queries.RetryEpic(ctx, sqlcgen.RetryEpicParams{Retried: highest, ID: epic}); err != nil {
		return fmt.Errorf("unpark epic %s: %w", epic, err)
	}
	return nil
}

// capNodeOutput returns output cut to NodeOutputMax bytes at a UTF-8 rune start.
func capNodeOutput(output string) string {
	if len(output) <= NodeOutputMax {
		return output
	}
	cut := NodeOutputMax
	for cut > 0 && !utf8.RuneStart(output[cut]) {
		cut--
	}
	return output[:cut]
}
