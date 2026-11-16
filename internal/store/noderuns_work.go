package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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
		return parkNodeRun(ctx, queries, epic, path, attempt, outcome, output, reason, at)
	})
}

// parkNodeRun parks the running run at path of epic at attempt with outcome and output, and parks epic with reason and
// adds reason as a tell, in the caller's write.
func parkNodeRun(
	ctx context.Context, queries *sqlcgen.Queries, epic, path string, attempt int64, outcome, output, reason string,
	at time.Time,
) error {
	parked, err := queries.ParkEpicNodeRun(ctx, sqlcgen.ParkEpicNodeRunParams{
		Outcome: outcome, Output: capNodeOutput(output), Epic: epic, Path: path, Attempt: attempt,
	})
	if err != nil {
		return fmt.Errorf("park node run %s of epic %s: %w", path, epic, err)
	}
	if parked == 0 {
		return nil
	}
	return parkWithTell(ctx, queries, epic, capTurnText(reason), at)
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

// integrateKind is the kind of an integrate node, whose node runs batch accepted tasks onto the candidate, and
// nodeFailedOutcome is the outcome of a node run that ended failed.
const (
	integrateKind     = "integrate"
	nodeFailedOutcome = "failed"
)

// RetryNode reruns node of epic under its next attempt and unparks epic, in one write. Each parked run of node turns
// running with its outcome, output, revision, refs, refs checked, result and end cleared. When node has no parked run
// and its latest integrate run ended failed, that run turns running with its outcome, output, refs checked and end
// cleared, keeping its revision and result, so its work decides again on the same candidate. The latest integrate run
// is the one in the newest settle walk of the plan (latestIntegrateRun). The park ends as RetryEpic ends it, and each
// open park question of epic is answered retry. A node with nothing to rerun is a RefusalError, and so is an epic
// parked for good, which changes nothing. So is the release node, which only ReleaseEpic resumes: retried here, it would
// run while the phase stays stopped and wait for good.
func (s *Store) RetryNode(ctx context.Context, epic, node string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		runs, err := readNodeRuns(ctx, queries, epic)
		if err != nil {
			return err
		}
		for _, run := range runs {
			if run.Node == node && run.Kind == releaseKind {
				return RefusalError(fmt.Sprintf(
					"node %s of epic %s is the release; resume it with orrery epic release %s", node, epic, epic))
			}
		}
		parked, err := retryParkedNode(ctx, queries, epic, node)
		if err != nil || parked {
			return err
		}
		return retryFailedIntegrate(ctx, queries, epic, node, runs)
	})
}

// retryNode turns the parked run of node in epic running at its next attempt, answers each open park question of epic
// retry and unparks epic, in the caller's write, as RetryNode does. A node with no parked run is a RefusalError, and so
// is an epic parked for good.
func retryNode(ctx context.Context, queries *sqlcgen.Queries, epic, node string) error {
	parked, err := retryParkedNode(ctx, queries, epic, node)
	if err != nil {
		return err
	}
	if !parked {
		return notParkedRefusal(epic, node)
	}
	return nil
}

// retryParkedNode turns each parked run of node in epic running at its next attempt and unparks epic, in the caller's
// write. It reports false, and changes nothing, when no run of node is parked.
func retryParkedNode(ctx context.Context, queries *sqlcgen.Queries, epic, node string) (bool, error) {
	retried, err := queries.RetryEpicNode(ctx, sqlcgen.RetryEpicNodeParams{Epic: epic, Node: node})
	if err != nil {
		return false, fmt.Errorf("retry node %s of epic %s: %w", node, epic, err)
	}
	if retried == 0 {
		return false, nil
	}
	return true, unparkRetried(ctx, queries, epic)
}

// retryFailedIntegrate reruns the latest integrate run of node in epic when it ended failed, and unparks epic, in the
// caller's write. Any other run of node is a RefusalError, so an integrate run that is running or passed, or an earlier
// failed run that a later run of node superseded, changes nothing.
func retryFailedIntegrate(ctx context.Context, queries *sqlcgen.Queries, epic, node string, runs []NodeRun) error {
	run, ok := latestIntegrateRun(runs, node)
	if !ok || run.State != NodeEnded || run.Outcome != nodeFailedOutcome {
		return notParkedRefusal(epic, node)
	}
	rerun, err := queries.RetryFailedEpicIntegrateRun(ctx, sqlcgen.RetryFailedEpicIntegrateRunParams{
		Epic: epic, Path: run.Path,
	})
	if err != nil {
		return fmt.Errorf("retry integrate run %s of epic %s: %w", run.Path, epic, err)
	}
	if rerun == 0 {
		return notParkedRefusal(epic, node)
	}
	return unparkRetried(ctx, queries, epic)
}

// latestIntegrateRun returns the integrate run of node that sits in the newest settle walk, and whether there is one.
// A settle walk is numbered by the seq of the task it settles, and a later task has a higher seq, so the run with the
// highest seq is the latest.
func latestIntegrateRun(runs []NodeRun, node string) (NodeRun, bool) {
	var latest NodeRun
	var newest uint64
	found := false
	for _, run := range runs {
		if run.Kind != integrateKind || run.Node != node {
			continue
		}
		seq, ok := settleSeq(run.Path)
		if ok && (!found || seq > newest) {
			latest, newest, found = run, seq, true
		}
	}
	return latest, found
}

// settleSeq returns the seq of the settle walk that holds the node run at path: the N of the S<N> segment just above the
// run's own node. It returns false when path sits in no settle walk.
func settleSeq(path string) (uint64, bool) {
	i := strings.LastIndex(path, "/")
	if i < 0 {
		return 0, false
	}
	parent := path[:i]
	walk, found := strings.CutPrefix(parent[strings.LastIndex(parent, "/")+1:], "S")
	if !found {
		return 0, false
	}
	seq, err := strconv.ParseUint(walk, 10, 64)
	return seq, err == nil
}

// unparkRetried answers each open park question of epic retry and unparks epic, in the caller's write. An epic in phase
// met_at_base is parked for good, so it is a RefusalError and the write changes nothing.
func unparkRetried(ctx context.Context, queries *sqlcgen.Queries, epic string) error {
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return err
	}
	if head.Phase == phaseMetAtBase {
		return metAtBaseRefusal(epic, head.Parked)
	}
	err = closeParkQuestions(ctx, queries, epic)
	if err != nil {
		return err
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

// notParkedRefusal is the refusal of a retry of node in epic that has no run to rerun.
func notParkedRefusal(epic, node string) RefusalError {
	return RefusalError(fmt.Sprintf("node %s of epic %s is not parked", node, epic))
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
