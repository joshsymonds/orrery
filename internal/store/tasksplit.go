package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// minSplitDescendants is the fewest descendants a split may give a task.
const minSplitDescendants = 2

// splitTask splits the task action names into the descendants action holds, in the caller's write, as CarryOutAction
// describes. The task must be open, on ApproachRung, with no change of approach standing, and it must not be a
// descendant of a split. The split has at least two descendants, each with files, no file in two descendants or twice
// in one, and the descendants' files together exactly the task's current files. Each Requirement the task claims is
// claimed by exactly one descendant, and no descendant claims a Requirement the task does not. Any other split is a
// RefusalError and writes nothing.
//
// The write sets the task's status to TaskSplit and removes its claim rows, so its claims move to its descendants. It
// inserts each descendant as an open ordinary task with the next seqs, planned on the action's turn, with its title,
// deliverable, gate, evidence item, files, test changes, claims and contributions as given, and with the task as its
// SplitFrom. A descendant has no depends_on and no build, and no build child run, as the task reopenDesign inserts has
// none; it starts on rung 1 like any fresh task, and starting it is not done here. A task whose depends_on names the
// task depends on the descendants instead, in the task's place, in seq order, with no entry repeated. It returns the ids
// of the task's steps with no end, for the runner to stop.
func splitTask(
	ctx context.Context, queries *sqlcgen.Queries, action PlannerAction, _ time.Time,
) ([]string, error) {
	if err := checkSplit(ctx, queries, action); err != nil {
		return nil, err
	}
	if err := writeSplit(ctx, queries, action); err != nil {
		return nil, err
	}
	return openTaskSteps(ctx, queries, action.Epic, action.Task)
}

// writeSplit writes the split of action in the caller's write, once checkSplit allows it: the task is ended as split, its
// claim rows are removed, its descendants are inserted, and the tasks that depend on it are rewired to them.
func writeSplit(ctx context.Context, queries *sqlcgen.Queries, action PlannerAction) error {
	if err := setTaskStatus(ctx, queries, action.Epic, action.Task, TaskSplit); err != nil {
		return err
	}
	if err := queries.DeleteEpicTaskRequirements(ctx, sqlcgen.DeleteEpicTaskRequirementsParams{
		Epic: action.Epic, Task: action.Task,
	}); err != nil {
		return fmt.Errorf("remove claims of task %d of epic %s: %w", action.Task, action.Epic, err)
	}
	seqs, err := insertDescendants(ctx, queries, action)
	if err != nil {
		return err
	}
	return rewireDependents(ctx, queries, action.Epic, action.Task, seqs)
}

// insertDescendants inserts each descendant of action as the next task seqs of its epic, in order, in the caller's write,
// and returns those seqs.
func insertDescendants(ctx context.Context, queries *sqlcgen.Queries, action PlannerAction) ([]int64, error) {
	highest, err := queries.MaxEpicTaskSeq(ctx, action.Epic)
	if err != nil {
		return nil, fmt.Errorf("read task count of epic %s: %w", action.Epic, err)
	}
	seqs := make([]int64, len(action.Split))
	err = insertInOrder(action.Split, func(position int64, descendant SplitTask) error {
		seqs[position-1] = highest + position
		return insertDescendant(ctx, queries, action, seqs[position-1], descendant)
	})
	return seqs, err
}

// checkSplit returns a RefusalError when the split of action may not be carried out, and reads the task and its claims
// and files to decide it. It writes nothing.
func checkSplit(ctx context.Context, queries *sqlcgen.Queries, action PlannerAction) error {
	task, err := queries.GetEpicTask(ctx, sqlcgen.GetEpicTaskParams{Epic: action.Epic, Seq: action.Task})
	if errors.Is(err, sql.ErrNoRows) {
		return RefusalError(fmt.Sprintf("task %d of epic %s does not exist", action.Task, action.Epic))
	}
	if err != nil {
		return fmt.Errorf("read task %d of epic %s: %w", action.Task, action.Epic, err)
	}
	if task.Status != taskOpen {
		return RefusalError(fmt.Sprintf("task %d of epic %s is not open", action.Task, action.Epic))
	}
	if rung := buildOf(task).Attempt; rung != ApproachRung {
		return RefusalError(fmt.Sprintf("task %d of epic %s is on rung %d, not rung %d",
			action.Task, action.Epic, rung, ApproachRung))
	}
	rows, err := queries.ListEpicTaskVersions(ctx, action.Epic)
	if err != nil {
		return fmt.Errorf("list versions of epic %s: %w", action.Epic, err)
	}
	versions, err := taskVersionsOf(rows)
	if err != nil {
		return fmt.Errorf("list versions of epic %s: %w", action.Epic, err)
	}
	if ApproachStands(versions[action.Task]) {
		return RefusalError(fmt.Sprintf("task %d of epic %s already has a change of approach",
			action.Task, action.Epic))
	}
	if task.SplitFrom != 0 {
		return RefusalError(fmt.Sprintf("task %d of epic %s is a descendant of a split and may not split",
			action.Task, action.Epic))
	}
	if len(action.Split) < minSplitDescendants {
		return RefusalError(fmt.Sprintf("the split of task %d of epic %s has fewer than two descendants",
			action.Task, action.Epic))
	}
	owned, err := ownedFiles(ctx, queries, action.Epic, action.Task)
	if err != nil {
		return err
	}
	claims, err := claimsOf(ctx, queries, action.Epic, action.Task)
	if err != nil {
		return err
	}
	return checkSplitPartition(action, owned, claims)
}

// checkSplitPartition returns the RefusalError that checkSplitFiles or checkSplitClaims gives for action, given the files
// and the Requirements the task holds, or nil when the split partitions both.
func checkSplitPartition(action PlannerAction, owned, claims []string) error {
	if err := checkSplitFiles(action, owned); err != nil {
		return err
	}
	return checkSplitClaims(action, claims)
}

// checkSplitFiles returns a RefusalError unless each descendant of action has files, no file is named twice, and the
// descendants' files are exactly owned, the files task action names holds.
func checkSplitFiles(action PlannerAction, owned []string) error {
	var all []string
	owner := map[string]int{}
	for i, descendant := range action.Split {
		if len(descendant.Files) == 0 {
			return RefusalError(fmt.Sprintf("descendant %d of the split of task %d has no files", i+1, action.Task))
		}
		for _, file := range descendant.Files {
			j, taken := owner[file]
			switch {
			case taken && j == i:
				return RefusalError(fmt.Sprintf("descendant %d of the split of task %d names %s twice",
					i+1, action.Task, file))
			case taken:
				return RefusalError(fmt.Sprintf("file %s is in descendants %d and %d of the split of task %d",
					file, j+1, i+1, action.Task))
			}
			owner[file] = i
			all = append(all, file)
		}
	}
	if !sameFiles(all, owned) {
		return RefusalError(fmt.Sprintf("the files of the split of task %d are not exactly its files", action.Task))
	}
	return nil
}

// sameFiles reports whether a and b hold the same paths, in any order.
func sameFiles(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}

// checkSplitClaims returns a RefusalError unless each Requirement a descendant of action claims is one of claims, the
// Requirements the task claims, and each of claims is claimed by exactly one descendant entry.
func checkSplitClaims(action PlannerAction, claims []string) error {
	counts := map[string]int{}
	for i, descendant := range action.Split {
		for _, requirement := range descendant.Claims {
			if !slices.Contains(claims, requirement) {
				return RefusalError(fmt.Sprintf(
					"descendant %d of the split of task %d claims %s, which the task does not",
					i+1, action.Task, requirement))
			}
			counts[requirement]++
		}
	}
	for _, requirement := range claims {
		if counts[requirement] != 1 {
			return RefusalError(fmt.Sprintf(
				"requirement %s of task %d is claimed %d times by the split, not exactly once",
				requirement, action.Task, counts[requirement]))
		}
	}
	return nil
}

// claimsOf returns the Requirements task seq of epic claims, in position order.
func claimsOf(ctx context.Context, queries *sqlcgen.Queries, epic string, seq int64) ([]string, error) {
	rows, err := queries.ListEpicTaskRequirements(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("read task requirements of epic %s: %w", epic, err)
	}
	var claims []string
	for _, row := range rows {
		if row.Task == seq {
			claims = append(claims, row.Requirement)
		}
	}
	return claims, nil
}

// insertDescendant stores descendant as the open task seq of the epic of action, planned on action's turn and split
// from action's task, with its files, claims, contributions and test changes, in the caller's write.
func insertDescendant(
	ctx context.Context, queries *sqlcgen.Queries, action PlannerAction, seq int64, descendant SplitTask,
) error {
	epic := action.Epic
	item, err := encodeItem(descendant.Evidence)
	if err != nil {
		return fmt.Errorf("evidence of task %d of epic %s: %w", seq, epic, err)
	}
	if err = queries.InsertEpicTask(ctx, sqlcgen.InsertEpicTaskParams{
		Epic: epic, Seq: seq, Turn: action.Turn, Title: descendant.Title, Deliverable: descendant.Deliverable,
		Gate: descendant.Gate, Evidence: item,
	}); err != nil {
		return fmt.Errorf("insert task %d of epic %s: %w", seq, epic, err)
	}
	if err = queries.SetEpicTaskSplitFrom(ctx, sqlcgen.SetEpicTaskSplitFromParams{
		SplitFrom: action.Task, Epic: epic, Seq: seq,
	}); err != nil {
		return fmt.Errorf("split from of task %d of epic %s: %w", seq, epic, err)
	}
	if err = appendTaskFiles(ctx, queries, epic, seq, descendant.Files); err != nil {
		return fmt.Errorf("files of task %d of epic %s: %w", seq, epic, err)
	}
	if err = insertInOrder(descendant.Claims, func(position int64, requirement string) error {
		return queries.InsertEpicTaskRequirement(ctx, sqlcgen.InsertEpicTaskRequirementParams{
			Epic: epic, Task: seq, Position: position, Requirement: requirement,
		})
	}); err != nil {
		return fmt.Errorf("requirements of task %d of epic %s: %w", seq, epic, err)
	}
	if err = insertInOrder(descendant.Contributes, func(position int64, requirement string) error {
		return queries.InsertEpicTaskContribute(ctx, sqlcgen.InsertEpicTaskContributeParams{
			Epic: epic, Task: seq, Position: position, Requirement: requirement,
		})
	}); err != nil {
		return fmt.Errorf("contributes of task %d of epic %s: %w", seq, epic, err)
	}
	if err = insertInOrder(descendant.TestChanges, func(position int64, path string) error {
		return queries.InsertEpicTaskTestChange(ctx, sqlcgen.InsertEpicTaskTestChangeParams{
			Epic: epic, Task: seq, Position: position, Path: path,
		})
	}); err != nil {
		return fmt.Errorf("test changes of task %d of epic %s: %w", seq, epic, err)
	}
	return nil
}

// rewireDependents makes each task of epic whose depends_on names task depend on seqs in its place, in the caller's
// write. The entry for task is replaced in place by seqs, and an entry the task already holds is kept once.
func rewireDependents(ctx context.Context, queries *sqlcgen.Queries, epic string, task int64, seqs []int64) error {
	tasks, err := queries.ListEpicTasks(ctx, epic)
	if err != nil {
		return fmt.Errorf("read tasks of epic %s: %w", epic, err)
	}
	for _, row := range tasks {
		dependsOn, decodeErr := decodeSeqs(row.DependsOn)
		if decodeErr != nil {
			return fmt.Errorf("depends on of task %d of epic %s: %w", row.Seq, epic, decodeErr)
		}
		if !slices.Contains(dependsOn, task) {
			continue
		}
		var rewired []int64
		for _, seq := range dependsOn {
			if seq != task {
				rewired = appendOnce(rewired, seq)
				continue
			}
			for _, descendant := range seqs {
				rewired = appendOnce(rewired, descendant)
			}
		}
		if err = queries.SetEpicTaskDependsOn(ctx, sqlcgen.SetEpicTaskDependsOnParams{
			DependsOn: encodeSeqs(rewired), Epic: epic, Seq: row.Seq,
		}); err != nil {
			return fmt.Errorf("set depends on of task %d of epic %s: %w", row.Seq, epic, err)
		}
	}
	return nil
}

// appendOnce returns seqs with seq added at the end, unless seqs already holds it.
func appendOnce(seqs []int64, seq int64) []int64 {
	if slices.Contains(seqs, seq) {
		return seqs
	}
	return append(seqs, seq)
}
