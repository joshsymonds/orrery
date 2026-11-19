package planner

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/store"
)

// lineLimit is how many failed tasks a task's line holds when the planner may plan no further try of it: a task that
// failed, then the task that replaced it, which failed too. The next try goes to the owner instead.
const lineLimit = 2

// SpentLine is a line of failed tasks a turn may not try again: the failed task whose trigger the turn claimed, and the
// failed tasks it replaced, followed back. Failed holds their seqs, oldest first, so its last element is the task the
// trigger names.
type SpentLine struct {
	Failed []int64
}

// Task returns the seq of the failed task whose trigger the turn claimed: the line's newest.
func (l SpentLine) Task() int64 {
	return l.Failed[len(l.Failed)-1]
}

// labels returns the line's tasks as T<seq>, oldest first, joined with commas.
func (l SpentLine) labels() string {
	names := make([]string, len(l.Failed))
	for i, seq := range l.Failed {
		names[i] = "T" + strconv.FormatInt(seq, 10)
	}
	return strings.Join(names, ", ")
}

// triggerReader reads the triggers a planner turn claimed. Every Store satisfies it.
type triggerReader interface {
	TurnTriggers(ctx context.Context, epic string, turn int64) ([]store.EpicTrigger, error)
}

// failedSeq returns the seq of the task a trigger id of the form T<seq>-failed names, and whether id has that form.
func failedSeq(id string) (int64, bool) {
	digits, found := strings.CutPrefix(id, "T")
	if !found {
		return 0, false
	}
	digits, found = strings.CutSuffix(digits, "-failed")
	if !found {
		return 0, false
	}
	seq, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || seq < 1 || strconv.FormatInt(seq, 10) != digits {
		return 0, false
	}
	return seq, true
}

// spentLines returns the lines of failed tasks the turn that claimed triggers may not try again, in trigger order. Each
// trigger T<F>-failed names a failed task F, and F's line is F followed back through the tasks it replaced. Task N
// replaced task E when the turn that planned N claimed the trigger T<E>-failed, E was failed and planned before N, and
// the two share a claimed or contributed Requirement or a file; of several such E, N replaced the lowest seq. A line of
// lineLimit failed tasks or more is spent. A line ends at a task planned by a turn that claimed no such trigger, so the
// line of a task planned after the owner answered starts again at that task. epic's tasks are read as the turn sees
// them, and triggers reads the triggers each earlier turn claimed.
func spentLines(
	ctx context.Context, triggers triggerReader, epic store.EpicRow, claimed []store.EpicTrigger,
) ([]SpentLine, error) {
	var spent []SpentLine
	for _, trigger := range claimed {
		seq, ok := failedSeq(trigger.ID)
		if !ok {
			continue
		}
		failed, err := lineOf(ctx, triggers, epic, seq)
		if err != nil {
			return nil, err
		}
		if len(failed) >= lineLimit {
			spent = append(spent, SpentLine{Failed: failed})
		}
	}
	return spent, nil
}

// lineOf returns the line of failed task seq of epic, oldest first: seq, then each task it replaced, followed back as
// spentLines gives. A task epic does not hold as failed has no line.
func lineOf(ctx context.Context, triggers triggerReader, epic store.EpicRow, seq int64) ([]int64, error) {
	var line []int64
	for {
		task, ok := failedTask(epic.Tasks, seq)
		if !ok {
			break
		}
		line = append(line, task.Seq)
		claimed, err := triggers.TurnTriggers(ctx, epic.ID, task.Turn)
		if err != nil {
			return nil, fmt.Errorf("read triggers of turn %d of epic %s: %w", task.Turn, epic.ID, err)
		}
		replaced, found := replacedBy(epic.Tasks, task, claimed)
		if !found {
			break
		}
		seq = replaced
	}
	slices.Reverse(line)
	return line, nil
}

// failedTask returns the task seq of tasks when it is failed.
func failedTask(tasks []store.EpicTask, seq int64) (store.EpicTask, bool) {
	i := slices.IndexFunc(tasks, func(task store.EpicTask) bool { return task.Seq == seq })
	if i < 0 || tasks[i].Status != taskFailed {
		return store.EpicTask{}, false
	}
	return tasks[i], true
}

// replacedBy returns the lowest seq of a failed task that task replaced: one planned before task whose trigger
// T<seq>-failed is among claimed, the triggers of the turn that planned task, and that shares a claimed or contributed
// Requirement or a file with it.
func replacedBy(tasks []store.EpicTask, task store.EpicTask, claimed []store.EpicTrigger) (int64, bool) {
	var lowest int64
	for _, trigger := range claimed {
		seq, ok := failedSeq(trigger.ID)
		if !ok || seq >= task.Seq || (lowest != 0 && seq >= lowest) {
			continue
		}
		earlier, failed := failedTask(tasks, seq)
		if failed && overlaps(earlier.Requirements, earlier.Contributes, earlier.Files, task.Requirements,
			task.Contributes, task.Files) {
			lowest = seq
		}
	}
	return lowest, lowest != 0
}

// overlaps reports whether two tasks share a Requirement, either claimed or contributed, or a file: the first task's
// claimed, contributed and files against the second's.
func overlaps(claims, contributes, files, otherClaims, otherContributes, otherFiles []string) bool {
	requirements := slices.Concat(claims, contributes)
	for _, id := range slices.Concat(otherClaims, otherContributes) {
		if slices.Contains(requirements, id) {
			return true
		}
	}
	for _, file := range otherFiles {
		if slices.Contains(files, file) {
			return true
		}
	}
	return false
}

// spentLineText returns what the planner reads of a spent line: the tasks of the line, and that it plans no further try.
func spentLineText(line SpentLine) string {
	return fmt.Sprintf("- T%d failed after replacing a failed task, so its line has failed %d times (%s). Plan no "+
		"further try of it: ask the owner a catastrophe question, or amend_evidence when the failure is in a "+
		"Requirement's evidence item.", line.Task(), len(line.Failed), line.labels())
}

// checkSpentLines returns the refusals of a return planned while lines are spent. Each task of ret that shares a claimed
// or contributed Requirement or a file with a task of a spent line is refused, named by index and title, since it would
// be the line's next try. A return that asks no question, amends no evidence and closes no epic while a line is spent
// gets one line under questions, since the owner decides a spent line.
func checkSpentLines(ret Return, lines []SpentLine, tasks []store.EpicTask) []string {
	if len(lines) == 0 {
		return nil
	}
	var refusals []string
	for i, task := range ret.Tasks {
		for _, line := range lines {
			if !lineOverlaps(line, tasks, task) {
				continue
			}
			refusals = append(refusals, fmt.Sprintf(
				`tasks[%d] "%s": tries again the line of T%d, which has failed %d times (%s); plan no further try: `+
					`ask the owner a catastrophe question, or amend_evidence when the failure is in a Requirement's `+
					`evidence item`,
				i, task.Title, line.Task(), len(line.Failed), line.labels()))
			break
		}
	}
	if !decidesSpent(ret) {
		for _, line := range lines {
			refusals = append(refusals, fmt.Sprintf(
				"questions: the line of T%d has failed %d times (%s); ask the owner a catastrophe question about it, "+
					"or amend_evidence when the failure is in a Requirement's evidence item",
				line.Task(), len(line.Failed), line.labels()))
		}
	}
	return refusals
}

// lineOverlaps reports whether task shares a claimed or contributed Requirement or a file with a task of line.
func lineOverlaps(line SpentLine, tasks []store.EpicTask, task Task) bool {
	for _, seq := range line.Failed {
		failed, ok := failedTask(tasks, seq)
		if ok && overlaps(failed.Requirements, failed.Contributes, failed.Files, task.Requirements, task.Contributes,
			task.Files) {
			return true
		}
	}
	return false
}

// decidesSpent reports whether ret hands a spent line to the owner or settles it otherwise: it asks a question, or it
// amends a Requirement's evidence or closes the epic.
func decidesSpent(ret Return) bool {
	if len(ret.Questions) > 0 {
		return true
	}
	return slices.ContainsFunc(ret.Actions, func(action EngineAction) bool {
		return action.Kind == store.ActionAmendEvidence || action.Kind == store.ActionCloseEpic
	})
}
