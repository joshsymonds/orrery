package store

import (
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The kinds a rung of a task's ladder can be. A rung is the task build's Attempt number: rungs 1 and 2 are RungAttempt,
// 3 is RungRescue, 4 is RungOrchestrator, 5 and 6 are RungApproach, and 7 is RungGap.
const (
	RungAttempt      = "attempt"
	RungRescue       = "rescue"
	RungOrchestrator = "orchestrator"
	RungApproach     = "approach"
	RungGap          = "gap"
)

// The values of a rung's moved_by: the kind of failure that moved the task onto the rung. Rung 1 has none.
const (
	MovedByUnit         = "unit"
	MovedByFinding      = "finding"
	MovedByFailDecision = "fail_decision"
	MovedByCannotMeet   = "cannot_meet"
	MovedByFailTask     = "fail_task"
)

// The outcomes a rung can end with. RungGapped is the outcome 'gap', the same word as the kind RungGap.
const (
	RungFailed   = "failed"
	RungAccepted = "accepted"
	RungGapped   = "gap"
)

// TaskGap is the status of a task that ended as a gap.
const TaskGap = "gap"

// TaskRung is one rung of a task's ladder: its number, its kind, the failure that moved the task onto it, and when the
// rung was written. Outcome and Reason are the rung's outcome, and Ended when it was written. Outcome and Reason are
// empty and Ended is zero while the rung has no outcome. The failure fields are empty for rung 1.
type TaskRung struct {
	Rung      int64
	Kind      string
	MovedBy   string
	Cause     string
	Gate      string
	Assertion string
	Step      string
	Created   time.Time
	Outcome   string
	Reason    string
	Ended     time.Time
}

// taskRungsOf returns the rungs each task holds, keyed by task, each task's in the rung order of rows. A task with no
// rung has no entry.
func taskRungsOf(rows []sqlcgen.ListEpicTaskRungsRow) map[int64][]TaskRung {
	out := map[int64][]TaskRung{}
	for _, row := range rows {
		out[row.Task] = append(out[row.Task], taskRungOf(row))
	}
	return out
}

// taskRungOf returns the rung a stored row holds, with its outcome when it has one.
func taskRungOf(row sqlcgen.ListEpicTaskRungsRow) TaskRung {
	rung := TaskRung{
		Rung: row.Rung, Kind: row.Kind, MovedBy: row.MovedBy, Cause: row.Cause, Gate: row.Gate,
		Assertion: row.Assertion, Step: row.Step, Created: time.Unix(row.Created, 0),
		Outcome: row.Outcome.String, Reason: row.Reason.String,
	}
	if row.Ended.Valid {
		rung.Ended = time.Unix(row.Ended.Int64, 0)
	}
	return rung
}
