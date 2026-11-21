package planner

import (
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// summaryTestItem is an evidence item of the summary tests: a capture that runs scripts/check, with conclusion.
func summaryTestItem(conclusion string) evidence.Item {
	return evidence.Item{Capture: []string{"scripts/check"}, Limit: 60, Conclusion: conclusion, Judge: "a judge"}
}

// summaryTestTrigger returns the one trigger with id that a turn claimed.
func summaryTestTrigger(id string) []store.EpicTrigger {
	return []store.EpicTrigger{{ID: id, Cause: "a cause of " + id, State: "claimed", Turn: 2}}
}

// requireSummaryLines asserts that got equals want, line for line.
func requireSummaryLines(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("lines are\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestSummaryTaskDoneTaskIsOneLine: a done task no trigger names is one line of its number, title and status, and the
// same task named by a trigger renders in full, as taskLines renders it.
func TestSummaryTaskDoneTaskIsOneLine(t *testing.T) {
	t.Parallel()
	done := store.EpicTask{
		Seq: 1, Turn: 1, Title: "finished task", Deliverable: "the finished deliverable", Status: "done",
		Files: []string{"internal/c/done.go"}, Requirements: []string{"R1"}, Contributes: []string{"R2"},
		Gate: "check", Evidence: summaryTestItem("the finished task holds"),
		Rungs: []store.TaskRung{{Rung: 1, Kind: store.RungAttempt}},
	}
	got, err := summaryTaskLines([]store.EpicTask{done}, nil)
	if err != nil {
		t.Fatalf("summaryTaskLines: %v", err)
	}
	requireSummaryLines(t, got, []string{"- T1 [done] finished task"})

	named, err := summaryTaskLines([]store.EpicTask{done}, summaryTestTrigger("T1-failed"))
	if err != nil {
		t.Fatalf("summaryTaskLines named: %v", err)
	}
	full, err := taskLines([]store.EpicTask{done})
	if err != nil {
		t.Fatalf("taskLines: %v", err)
	}
	requireSummaryLines(t, named, full)
}

// TestSummaryTaskOpenTaskKeepsFilesAndIdsButNoDeliverableOrEvidence: an open task no trigger names keeps its files,
// its claimed Requirement ids and its contributed ids, and shows neither its deliverable nor its evidence, nor its
// test changes, gate or guard refusal.
func TestSummaryTaskOpenTaskKeepsFilesAndIdsButNoDeliverableOrEvidence(t *testing.T) {
	t.Parallel()
	open := store.EpicTask{
		Seq: 1, Turn: 1, Title: "first task", Deliverable: "the first deliverable", Status: "open",
		Files: []string{"internal/a/one.go"}, Requirements: []string{"R1", "R4"}, Contributes: []string{"R5"},
		TestChanges: []string{"one_test.go"}, Gate: "check", Evidence: summaryTestItem("the first task holds"),
		Build: store.TaskBuild{Guard: "one_test.go: TestOld: test present at the base removed"},
	}
	got, err := summaryTaskLines([]store.EpicTask{open}, nil)
	if err != nil {
		t.Fatalf("summaryTaskLines: %v", err)
	}
	requireSummaryLines(t, got, []string{
		"- T1 [open] first task",
		"  files: internal/a/one.go",
		"  requirements: R1, R4",
		"  contributes: R5",
	})
}

// TestSummaryTaskFailedAndGapTaskShowsOnlyTheFirstLineOfItsFailure: a failed or gap task no trigger names keeps its
// rungs, files, requirements and contributes, then its failure's first line and the files it may need, and not the
// rest of a failure that runs over several lines.
func TestSummaryTaskFailedAndGapTaskShowsOnlyTheFirstLineOfItsFailure(t *testing.T) {
	t.Parallel()
	const failure = "checks_failed: owned: changed paths outside owned: internal/b/x.go\n" +
		"the second line names internal/b/y.go\nthe third line is a long log"
	for _, status := range []string{"failed", "gap"} {
		task := store.EpicTask{
			Seq: 2, Turn: 1, Title: "second task", Deliverable: "the second deliverable", Status: status,
			Files: []string{"internal/b/two.go"}, Requirements: []string{"R2"}, Contributes: []string{"R3"},
			Gate: "check", Evidence: summaryTestItem("the second task holds"),
			Rungs: []store.TaskRung{{
				Rung: 2, Kind: store.RungAttempt, MovedBy: store.MovedByUnit, Cause: "the unit check failed",
			}},
			Build: store.TaskBuild{Failure: failure}, NeedsFiles: []string{"internal/b/z.go"},
		}
		got, err := summaryTaskLines([]store.EpicTask{task}, nil)
		if err != nil {
			t.Fatalf("summaryTaskLines of a %s task: %v", status, err)
		}
		requireSummaryLines(t, got, []string{
			"- T2 [" + status + "] second task",
			"  rung 2 attempt moved by unit: the unit check failed",
			"  files: internal/b/two.go",
			"  requirements: R2",
			"  contributes: R3",
			"  failure: checks_failed: owned: changed paths outside owned: internal/b/x.go",
			"  files the next task may need: internal/b/z.go",
		})
	}
}

// TestSummaryTaskTriggerNamingATaskRendersItInFull: a trigger whose id has the token T2 among its ':' and '-' parts
// renders task 2 in full, and task 1, which no trigger names, stays a summary. An id with no token T2 names nothing.
func TestSummaryTaskTriggerNamingATaskRendersItInFull(t *testing.T) {
	t.Parallel()
	tasks := []store.EpicTask{
		{
			Seq: 1, Turn: 1, Title: "first task", Deliverable: "the first deliverable", Status: "open",
			Files: []string{"internal/a/one.go"}, Requirements: []string{"R1"}, Contributes: []string{"R4"},
			Gate: "check", Evidence: summaryTestItem("the first task holds"),
		},
		{
			Seq: 2, Turn: 1, Title: "second task", Deliverable: "the second deliverable", Status: "failed",
			Files: []string{"internal/a/two.go"}, Requirements: []string{"R2"}, Contributes: []string{"R3"},
			Gate: "check", Evidence: summaryTestItem("the second task holds"),
			Build: store.TaskBuild{Failure: "checks_failed: gate: lint failed"},
		},
	}
	firstSummary := []string{
		"- T1 [open] first task", "  files: internal/a/one.go", "  requirements: R1", "  contributes: R4",
	}
	secondSummary := []string{
		"- T2 [failed] second task", "  files: internal/a/two.go", "  requirements: R2", "  contributes: R3",
		"  failure: checks_failed: gate: lint failed", "  files the next task may need: None.",
	}
	secondFull, err := taskLines(tasks[1:])
	if err != nil {
		t.Fatalf("taskLines: %v", err)
	}
	for _, id := range []string{"T2-failed", "T2-approach", "T2-gap", "T2-gap-2", "gap:task:T2"} {
		got, err := summaryTaskLines(tasks, summaryTestTrigger(id))
		if err != nil {
			t.Fatalf("summaryTaskLines named by %s: %v", id, err)
		}
		requireSummaryLines(t, got, slices.Concat(firstSummary, secondFull))
	}
	for _, id := range []string{"T20-failed", "T02-failed", "T2x-failed", "created"} {
		got, err := summaryTaskLines(tasks, summaryTestTrigger(id))
		if err != nil {
			t.Fatalf("summaryTaskLines named by %s: %v", id, err)
		}
		requireSummaryLines(t, got, slices.Concat(firstSummary, secondSummary))
	}
}

// TestSummaryTaskNoUntriggeredTaskRendersItsDeliverableOrEvidence: with no trigger, no task of any status renders its
// deliverable, evidence, test changes, gate, guard refusal or gate-needed changes.
func TestSummaryTaskNoUntriggeredTaskRendersItsDeliverableOrEvidence(t *testing.T) {
	t.Parallel()
	statuses := []string{"open", "done", "failed", "gap"}
	tasks := make([]store.EpicTask, 0, len(statuses))
	for i, status := range statuses {
		tasks = append(tasks, store.EpicTask{
			Seq: int64(i + 1), Turn: 1, Title: "task " + status, Deliverable: "the deliverable of " + status,
			Status: status, Files: []string{"internal/d/" + status + ".go"}, Requirements: []string{"R1"},
			TestChanges: []string{"x_test.go"}, Gate: "check",
			Evidence:    summaryTestItem("the " + status + " task holds"),
			GateChanges: []store.GateChange{{Path: "internal/d/g.go", Reason: "a race"}},
			Build:       store.TaskBuild{Guard: "x_test.go: TestOld: test present at the base removed"},
		})
	}
	got, err := summaryTaskLines(tasks, nil)
	if err != nil {
		t.Fatalf("summaryTaskLines: %v", err)
	}
	for _, line := range got {
		for _, banned := range []string{
			"  deliverable: ", "  evidence: ", "  test_changes: ", "  gate: ", "  guard refused: ",
			"  gate-needed changes declared: ",
		} {
			if strings.HasPrefix(line, banned) {
				t.Errorf("untriggered task renders %q in its summary line %q", banned, line)
			}
		}
	}
	inOrder(t, strings.Join(got, "\n"),
		"- T1 [open] task open", "- T2 [done] task done", "- T3 [failed] task failed", "- T4 [gap] task gap")
}

// TestSummaryTaskPromptTasksSectionSummarizesEachTask: the Tasks section of the prompt is the summary, a done task a
// line, an open task its files and Requirement ids, with no deliverable, before the Unclaimed requirements section.
func TestSummaryTaskPromptTasksSectionSummarizesEachTask(t *testing.T) {
	t.Parallel()
	epic := syntheticEpic()
	epic.Tasks = append(epic.Tasks, store.EpicTask{
		Seq: 2, Turn: 1, Title: "a done task", Deliverable: "a done deliverable", Status: "done",
		Files: []string{"internal/planner/done.go"}, Requirements: []string{"R1"},
	})
	prompt := promptOf(t, epic, 2, syntheticTriggers())
	want := "\n## Tasks\n" +
		"- T1 [open] a task\n" +
		"  files: internal/planner/prompt.go\n" +
		"  requirements: R1\n" +
		"  contributes: R2\n" +
		"- T2 [done] a done task\n" +
		"\n## Unclaimed requirements\n"
	if !strings.Contains(prompt, want) {
		t.Fatalf("prompt lacks the summary Tasks section %q; prompt is:\n%s", want, prompt)
	}
}
