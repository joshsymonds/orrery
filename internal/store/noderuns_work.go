package store

import (
	"context"
	"fmt"
	"slices"
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

// CheckNodeRefs records, in one write, that the refs of the running run at path of epic at attempt were checked, and
// adds each of tells as a tell. A run whose refs were checked already, a run at another attempt and a run that is not
// running change nothing, so a repeat adds no tell.
func (s *Store) CheckNodeRefs(
	ctx context.Context, epic, path string, attempt int64, tells []string, at time.Time,
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
		return insertTells(ctx, queries, epic, tells, at)
	})
}

// ParkNode parks the running run at path of epic at attempt with outcome and output, and parks epic with reason as class,
// in one write. A question class adds reason as a tell, and a planner class wakes the planner. Output is cut to
// NodeOutputMax bytes and reason to TurnReasonMax. A run at another attempt and a run that is not running change nothing
// and add no tell, so a repeat is a no-op.
func (s *Store) ParkNode(
	ctx context.Context, epic, path string, attempt int64, outcome, output string, class ParkClass, reason string,
	at time.Time,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		return parkNodeRun(ctx, queries, epic, path, attempt, outcome, output, class, reason, at)
	})
}

// parkNodeRun parks the running run at path of epic at attempt with outcome and output, and parks epic with reason as
// class, in the caller's write. When the park opens a park question, as a question class does and a planner park at the
// bound of its chain does, the question names the run.
func parkNodeRun(
	ctx context.Context, queries *sqlcgen.Queries, epic, path string, attempt int64, outcome, output string,
	class ParkClass, reason string, at time.Time,
) error {
	parked, err := parkRunningRun(ctx, queries, epic, path, attempt, outcome, output)
	if err != nil || !parked {
		return err
	}
	question, err := parkEpicQuestion(ctx, queries, epic, class, path, reason, at)
	if err != nil || question == 0 {
		return err
	}
	return insertParkQuestionRun(ctx, queries, epic, question, path)
}

// insertParkQuestionRun stores that the park question with seq question of epic names the run at path, in the caller's
// write.
func insertParkQuestionRun(
	ctx context.Context, queries *sqlcgen.Queries, epic string, question int64, path string,
) error {
	if err := queries.InsertEpicParkQuestionRun(ctx, sqlcgen.InsertEpicParkQuestionRunParams{
		Epic: epic, Question: question, Path: path,
	}); err != nil {
		return fmt.Errorf("store the run of park question %d of epic %s: %w", question, epic, err)
	}
	return nil
}

// parkRunningRun parks the running run at path of epic at attempt with outcome and output, in the caller's write, and
// reports whether it parked one. A run at another attempt and a run that is not running are not parked.
func parkRunningRun(
	ctx context.Context, queries *sqlcgen.Queries, epic, path string, attempt int64, outcome, output string,
) (bool, error) {
	parked, err := queries.ParkEpicNodeRun(ctx, sqlcgen.ParkEpicNodeRunParams{
		Outcome: outcome, Output: capNodeOutput(output), Epic: epic, Path: path, Attempt: attempt,
	})
	if err != nil {
		return false, fmt.Errorf("park node run %s of epic %s: %w", path, epic, err)
	}
	return parked != 0, nil
}

// rerunParkedRun turns the parked run at path of epic running at its next attempt, in the caller's write, with its work
// cleared as retryParkedNode clears it. It reports whether it reran a run: a run that is not parked, and a loop, which
// RetryEpic grants a round instead, change nothing.
func rerunParkedRun(ctx context.Context, queries *sqlcgen.Queries, epic, path string) (bool, error) {
	rerun, err := queries.RetryEpicNodeRun(ctx, sqlcgen.RetryEpicNodeRunParams{Epic: epic, Path: path})
	if err != nil {
		return false, fmt.Errorf("rerun node run %s of epic %s: %w", path, epic, err)
	}
	return rerun != 0, nil
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
// nodeFailedOutcome and nodePassedOutcome are the outcomes of a node run that ended failed and passed.
const (
	integrateKind     = "integrate"
	nodeFailedOutcome = "failed"
	nodePassedOutcome = "passed"
)

// RetryNode reruns node of epic under its next attempt and unparks epic, in one write. node names either a run, by the
// path a park reason gives it, or a node, by its id. A parked run whose path is node turns running on its own.
// Otherwise each parked run of node turns running with its outcome, output, revision, refs, refs checked, result and
// end cleared. When node has no parked run and its latest integrate run ended failed, that run turns running with its
// outcome, output, refs checked and end cleared, keeping its revision and result, so its work decides again on the same
// candidate. The latest integrate run is the one in the newest settle walk of the plan (latestIntegrateRun). The park
// ends as RetryEpic ends it, and each open park question of epic is answered retry. A node with nothing to rerun is a
// RefusalError, and so is an epic parked for good, which changes nothing. So is the release node, which only
// ReleaseEpic resumes: retried here, it would run while the phase stays stopped and wait for good. The write appends one
// retry action made by maker at at.
func (s *Store) RetryNode(ctx context.Context, epic, node string, maker Maker, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := rerunNodeIn(ctx, queries, epic, node); err != nil {
			return err
		}
		_, err := appendEpicAction(ctx, queries, epic, ActionRetry, maker, "", "", at)
		return err
	})
}

// rerunNodeIn reruns node of epic in the caller's write, as RetryNode does. The parked run whose path is node is rerun
// alone when there is one. Otherwise node is a node id, and its parked runs, or its latest failed integrate run, are
// rerun.
func rerunNodeIn(ctx context.Context, queries *sqlcgen.Queries, epic, node string) error {
	runs, err := readNodeRuns(ctx, queries, epic)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run.Kind == releaseKind && (run.Node == node || run.Path == node) {
			return RefusalError(fmt.Sprintf(
				"node %s of epic %s is the release; resume it with orrery epic release %s", node, epic, epic))
		}
	}
	rerun, err := rerunParkedRun(ctx, queries, epic, node)
	if err != nil {
		return err
	}
	if rerun {
		return unparkRetried(ctx, queries, epic)
	}
	parked, err := retryParkedNode(ctx, queries, epic, node)
	if err != nil || parked {
		return err
	}
	return retryFailedIntegrate(ctx, queries, epic, node, runs)
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
	stored, err := queries.ListEpicIntegrateRunPaths(
		ctx,
		sqlcgen.ListEpicIntegrateRunPathsParams{Epic: epic, Node: node},
	)
	if err != nil {
		return fmt.Errorf("list integrate runs of node %s of epic %s: %w", node, epic, err)
	}
	run, ok := latestIntegrateRun(runs, stored)
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

// latestIntegrateRun returns the run among runs that sits in the newest settle or late walk, and whether there is one.
// stored is the paths of the node's integrate runs in the order the store wrote them, which is the order they started,
// since a run is written when it starts. A walk starts only once the walks before it have ended, so the newest is the
// run written last, whatever the seq of its walk: a late walk, which integrates a task that waited on others, starts
// after the settle walk it follows. A run that ended passed with no revision moved nothing, an empty batch, so it does
// not count: a failed run whose candidate never reached the branch stays the latest behind it. A running run does count.
func latestIntegrateRun(runs []NodeRun, stored []string) (NodeRun, bool) {
	for i := len(stored) - 1; i >= 0; i-- {
		if _, ok := settleSeq(stored[i]); !ok {
			continue
		}
		if j := slices.IndexFunc(runs, func(run NodeRun) bool { return run.Path == stored[i] }); j >= 0 &&
			!emptyPassedBatch(runs[j]) {
			return runs[j], true
		}
	}
	return NodeRun{}, false
}

// emptyPassedBatch reports whether run is an integrate run that ended passed with no revision, a batch that had nothing
// to integrate.
func emptyPassedBatch(run NodeRun) bool {
	return run.State == NodeEnded && run.Outcome == nodePassedOutcome && run.Revision == ""
}

// settleSeq returns the seq of the settle walk or late walk that holds the node run at path: the N of the S<N>, L<N> or
// L<N>g<G> segment just above the run's own node, the last the late walk of a task at reopen generation G. It returns
// false when path sits in no such walk.
func settleSeq(path string) (uint64, bool) {
	i := strings.LastIndex(path, "/")
	if i < 0 {
		return 0, false
	}
	parent := path[:i]
	segment := parent[strings.LastIndex(parent, "/")+1:]
	walk, found := strings.CutPrefix(segment, "S")
	if !found {
		walk, found = strings.CutPrefix(segment, "L")
		if late, generation, reopened := strings.Cut(walk, "g"); found && reopened {
			if _, err := strconv.ParseUint(generation, 10, 64); err != nil {
				return 0, false
			}
			walk = late
		}
	}
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

// RebuildTask turns the build run at path of epic, which ended at attempt, running at its next attempt with its outcome,
// output, revision, refs, refs checked, result and end cleared, in one write, so its task builds again. A send_back that
// reopens a done task leaves its build run ended, and a plan starts no second run at a stored path. A run at another
// attempt, a run that is not ended and a run of another kind change nothing, so a repeat is a no-op.
func (s *Store) RebuildTask(ctx context.Context, epic, path string, attempt int64) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if _, err := queries.RebuildEpicTaskRun(ctx, sqlcgen.RebuildEpicTaskRunParams{
			Epic: epic, Path: path, Attempt: attempt,
		}); err != nil {
			return fmt.Errorf("rebuild node run %s of epic %s: %w", path, epic, err)
		}
		return nil
	})
}
