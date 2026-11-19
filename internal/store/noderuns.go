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

// Node run states. A run is running from its start until it ends, parked when its loop spent its rounds.
const (
	NodeRunning = "running"
	NodeEnded   = "ended"
	NodeParked  = "parked"
)

// nodeNamespace starts the parts every node run id hashes, so the ids are this package's.
const nodeNamespace = "orrery-workflow"

// nodeIDLength is the number of hex digits of a node run id.
const nodeIDLength = 32

// NodeRun is one instance of a workflow node on an epic. Path is the instance path, ID the id NodeRunID derives, and
// Node the workflow node's id. Kind is the node's kind, or loop for a loop. Task is a build child's task seq and zero
// otherwise. Round is the innermost enclosing loop's round and zero outside a loop. LoopRound is a loop's current round
// from 1, Granted the rounds a retry granted a loop, and Mark a plan's highest turn seq when it started. Started is the
// second the run started and Ended the second it ended, zero while it runs. Attempt is the attempt the run runs at, which
// a retry moves on by one. Revision is the revision the run's work ran at, Refs the refs it was prepared with, and
// RefsChecked whether those refs were compared. Result is the work's recorded result and Output the output the run ended
// or parked with, cut to NodeOutputMax bytes.
type NodeRun struct {
	Path, ID, Node, Kind string
	Task, Round          int64
	State, Outcome       string
	LoopRound, Granted   int64
	Mark                 int64
	Started, Ended       time.Time
	Attempt              int64
	Revision, Refs       string
	RefsChecked          bool
	Result, Output       string
}

// NodeEnd ends the running node run at Path with Outcome and Output.
type NodeEnd struct {
	Path, Outcome, Output string
}

// NodeTrigger is a trigger, identified by ID, that asks the planner for a turn because of Cause.
type NodeTrigger struct {
	ID, Cause string
}

// NodeRunID returns the id of a node run of epic at path in round, for cause. It is the first 32 hex digits of the
// lowercase hex sha256 of the namespace, epic, path, round and cause joined with NUL. The same inputs give the same id,
// so a run started again under its id is that run.
func NodeRunID(epic, path string, round int64, cause string) string {
	return nodeHash(nodeNamespace, epic, path, strconv.FormatInt(round, 10), cause)
}

// nodeHash returns the first nodeIDLength hex digits of the lowercase hex sha256 of namespace and parts joined with NUL.
func nodeHash(namespace string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(append([]string{namespace}, parts...), "\x00")))
	return hex.EncodeToString(sum[:])[:nodeIDLength]
}

// NodeRuns returns the node runs of epic ordered by path, each with every field it stores.
func (s *Store) NodeRuns(ctx context.Context, epic string) ([]NodeRun, error) {
	return readNodeRuns(ctx, sqlcgen.New(s.db), epic)
}

// OpenNodeRunStates returns, keyed by epic, the node runs of every epic that is not in a closed phase (IsClosedPhase),
// each epic's ordered by path, in one query. Each run holds only its Path, Kind, State, Outcome and Attempt: the
// fields that say where an epic's workflow stands without its work, which the interpreter reads for every epic on
// every pass. An epic with no node run has no entry.
func (s *Store) OpenNodeRunStates(ctx context.Context) (map[string][]NodeRun, error) {
	rows, err := sqlcgen.New(s.db).ListOpenEpicNodeRunStates(ctx)
	if err != nil {
		return nil, fmt.Errorf("list node run states: %w", err)
	}
	states := make(map[string][]NodeRun)
	for _, row := range rows {
		states[row.Epic] = append(states[row.Epic], NodeRun{
			Path: row.Path, Kind: row.Kind, State: row.State, Outcome: row.Outcome, Attempt: row.Attempt,
		})
	}
	return states, nil
}

// readNodeRuns returns the node runs of epic as queries read them, ordered by path.
func readNodeRuns(ctx context.Context, queries *sqlcgen.Queries, epic string) ([]NodeRun, error) {
	stored, err := queries.ListEpicNodeRuns(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list node runs of epic %s: %w", epic, err)
	}
	return mapInOrder(stored, nodeRunOf), nil
}

// nodeRunOf returns the node run a stored row holds. Ended is zero while the run runs.
func nodeRunOf(run sqlcgen.EpicNodeRun) NodeRun {
	out := NodeRun{
		Path: run.Path, ID: run.ID, Node: run.Node, Kind: run.Kind, Task: run.Task, Round: run.Round,
		State: run.State, Outcome: run.Outcome, LoopRound: run.LoopRound, Granted: run.Granted, Mark: run.Mark,
		Started: time.Unix(run.Started, 0), Attempt: run.Attempt, Revision: run.Revision, Refs: run.Refs,
		RefsChecked: run.RefsChecked != 0, Result: run.Result, Output: run.Output,
	}
	if run.Ended != 0 {
		out.Ended = time.Unix(run.Ended, 0)
	}
	return out
}

// StartNodes stores each of runs as a running node run started at at, and trigger as a pending trigger created at at,
// in one write. The state, outcome and end of each run are set here: running, none and zero, and its attempt is the
// run's Attempt. A run whose path is stored already is left as it is, and a trigger whose id is stored in any state is
// not added, so a repeat changes nothing.
func (s *Store) StartNodes(ctx context.Context, epic string, runs []NodeRun, trigger *NodeTrigger, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := insertNodeRuns(ctx, queries, epic, runs, at); err != nil {
			return err
		}
		if trigger == nil {
			return nil
		}
		return insertNodeTrigger(ctx, queries, epic, *trigger, at)
	})
}

// insertNodeRuns stores each of runs as a running node run of epic started at at, in the caller's write, as StartNodes
// does. A run whose path is stored already is left as it is.
func insertNodeRuns(ctx context.Context, queries *sqlcgen.Queries, epic string, runs []NodeRun, at time.Time) error {
	for _, run := range runs {
		if err := queries.InsertEpicNodeRun(ctx, sqlcgen.InsertEpicNodeRunParams{
			Epic: epic, Path: run.Path, ID: run.ID, Node: run.Node, Kind: run.Kind, Task: run.Task,
			Round: run.Round, State: NodeRunning, LoopRound: run.LoopRound, Granted: run.Granted,
			Mark: run.Mark, Started: at.Unix(), Attempt: run.Attempt,
		}); err != nil {
			return fmt.Errorf("insert node run %s of epic %s: %w", run.Path, epic, err)
		}
	}
	return nil
}

// EndNodes ends each running node run of epic at the path of ends with its outcome and output, at the second at, in one
// write. A run that is not running is left as it is.
func (s *Store) EndNodes(ctx context.Context, epic string, ends []NodeEnd, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		return endNodeRuns(ctx, queries, epic, ends, at)
	})
}

// endNodeRuns ends each running node run of epic at the path of ends with its outcome and output, at the second at, in
// the caller's write. Output is cut to NodeOutputMax bytes.
func endNodeRuns(ctx context.Context, queries *sqlcgen.Queries, epic string, ends []NodeEnd, at time.Time) error {
	for _, end := range ends {
		if err := queries.EndEpicNodeRun(ctx, sqlcgen.EndEpicNodeRunParams{
			Outcome: end.Outcome, Ended: at.Unix(), Output: capNodeOutput(end.Output), Epic: epic, Path: end.Path,
		}); err != nil {
			return fmt.Errorf("end node run %s of epic %s: %w", end.Path, epic, err)
		}
	}
	return nil
}

// insertNodeTrigger adds trigger to epic pending, created at at, unless a trigger with its id is stored, in the caller's
// write.
func insertNodeTrigger(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	trigger NodeTrigger,
	at time.Time,
) error {
	if err := queries.InsertEpicTriggerOnce(ctx, sqlcgen.InsertEpicTriggerOnceParams{
		Epic: epic, ID: trigger.ID, Cause: trigger.Cause, State: triggerPending, Turn: 0, Created: at.Unix(),
	}); err != nil {
		return fmt.Errorf("insert trigger %s of epic %s: %w", trigger.ID, epic, err)
	}
	return nil
}
