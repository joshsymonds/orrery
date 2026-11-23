package planner

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// splitEvidence returns the evidence item a split descendant of these tests holds: a capture item judged by the judge
// testResolver resolves, with the conclusion given.
func splitEvidence(conclusion string) evidence.Item {
	return evidence.Item{
		Kind: evidence.KindCapture, Capture: []string{"scripts/check"}, Limit: 60,
		Conclusion: conclusion, Judge: "a judge",
	}
}

// splitPart returns a descendant of title as the args of a split action write it: a JSON object that passes every check
// when it names file. It claims R1, contributes to R2, gates on check and judges its evidence item.
func splitPart(title, file string) map[string]any {
	return map[string]any{
		"title":       title,
		"deliverable": "a deliverable for " + title,
		"files":       []string{file},
		"claims":      []string{"R1"},
		"contributes": []string{"R2"},
		"gate":        "check",
		"evidence":    splitEvidence("a conclusion for " + title),
	}
}

// splitArgs returns the args of a split action of task that names parts as its tasks, written as JSON.
func splitArgs(t *testing.T, task int64, parts []map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"task": task, "tasks": parts})
	if err != nil {
		t.Fatalf("encode args: %v", err)
	}
	return string(encoded)
}

// parseSplit parses a planner return whose only action splits task into parts.
func parseSplit(t *testing.T, task int64, parts []map[string]any) Return {
	t.Helper()
	ret, err := Parse(actionPlan(store.ActionSplit, splitArgs(t, task, parts)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return ret
}

// TestTaskSplitActionReadsDescendants checks that the tasks key of a split action reads each descendant's fields, its
// test_changes key included, and that a return holding it passes the check.
func TestTaskSplitActionReadsDescendants(t *testing.T) {
	t.Parallel()
	a := splitPart("A", "internal/planner/a.go")
	a["test_changes"] = []string{"internal/planner/a.go"}
	ret := parseSplit(t, 2, []map[string]any{a, splitPart("B", "internal/planner/b.go")})
	got := ret.Actions[0]
	want := []Descendant{
		{
			Title: "A", Deliverable: "a deliverable for A", Files: []string{"internal/planner/a.go"},
			TestChanges: []string{"internal/planner/a.go"}, Claims: []string{"R1"}, Contributes: []string{"R2"},
			Gate: "check", Evidence: splitEvidence("a conclusion for A"),
		},
		{
			Title: "B", Deliverable: "a deliverable for B", Files: []string{"internal/planner/b.go"},
			Claims: []string{"R1"}, Contributes: []string{"R2"}, Gate: "check",
			Evidence: splitEvidence("a conclusion for B"),
		},
	}
	if got.Kind != store.ActionSplit || got.Task != 2 || got.ArgsError != "" || len(got.Unknown) != 0 ||
		!reflect.DeepEqual(got.Split, want) {
		t.Errorf("Parse split = %+v, want task 2 and descendants %+v read with no error", got, want)
	}
	checkLines(t, checkPlan(ret, nil))
}

// TestTaskSplitActionRefusesFewerThanTwo checks that a split names two or more descendants of a task above 0, and that
// each of the refusals for the count and the task is the one line.
func TestTaskSplitActionRefusesFewerThanTwo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		task  int64
		parts []map[string]any
		want  string
	}{
		{
			name: "no descendant", task: 2, parts: []map[string]any{},
			want: `actions[0] "split" tasks: fewer than two descendants`,
		},
		{
			name: "one descendant", task: 2, parts: []map[string]any{splitPart("A", "internal/planner/a.go")},
			want: `actions[0] "split" tasks: fewer than two descendants`,
		},
		{
			name: "task not above 0", task: 0,
			parts: []map[string]any{
				splitPart("A", "internal/planner/a.go"), splitPart("B", "internal/planner/b.go"),
			},
			want: `actions[0] "split" task: not above 0`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkLines(t, checkPlan(parseSplit(t, tc.task, tc.parts), nil), tc.want)
		})
	}
}

// TestTaskSplitActionRefusesSharedFile checks that a file named by two descendants is refused on the later one, and
// that the refusal is the only line.
func TestTaskSplitActionRefusesSharedFile(t *testing.T) {
	t.Parallel()
	ret := parseSplit(t, 2, []map[string]any{
		splitPart("A", "internal/planner/shared.go"),
		splitPart("B", "internal/planner/shared.go"),
	})
	checkLines(t, checkPlan(ret, nil), `actions[0] "split" tasks[1] "B" files[0]: named by an earlier descendant`)
}

// TestTaskSplitActionRefusesBadDescendant checks that each way a descendant breaks a rule gives one line, which names
// the descendant by index and title, when its other descendant is well formed.
func TestTaskSplitActionRefusesBadDescendant(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(part map[string]any)
		want   string
	}{
		{
			name:   "empty title",
			mutate: func(part map[string]any) { part["title"] = "" },
			want:   `actions[0] "split" tasks[1] "" title: empty`,
		},
		{
			name:   "empty deliverable",
			mutate: func(part map[string]any) { part["deliverable"] = " " },
			want:   `actions[0] "split" tasks[1] "B" deliverable: empty`,
		},
		{
			name:   "no files",
			mutate: func(part map[string]any) { part["files"] = []string{} },
			want:   `actions[0] "split" tasks[1] "B" files: no files`,
		},
		{
			name:   "repeated title",
			mutate: func(part map[string]any) { part["title"] = "A" },
			want:   `actions[0] "split" tasks[1] "A" title: repeats the title of an earlier descendant`,
		},
		{
			name:   "file not normalized",
			mutate: func(part map[string]any) { part["files"] = []string{"/internal/planner/b.go"} },
			want:   `actions[0] "split" tasks[1] "B" files[0]: not a normalized repository-relative file path`,
		},
		{
			name: "test change not its own file",
			mutate: func(part map[string]any) {
				part["test_changes"] = []string{"internal/planner/other_test.go"}
			},
			want: `actions[0] "split" tasks[1] "B" test_changes[0]: not one of the task's files`,
		},
		{
			name:   "claim not a Requirement",
			mutate: func(part map[string]any) { part["claims"] = []string{"R9"} },
			want:   `actions[0] "split" tasks[1] "B" claims[0]: not a Requirement of the epic`,
		},
		{
			name:   "contribute not a Requirement",
			mutate: func(part map[string]any) { part["contributes"] = []string{"R9"} },
			want:   `actions[0] "split" tasks[1] "B" contributes[0]: not a Requirement of the epic`,
		},
		{
			name:   "agent gate",
			mutate: func(part map[string]any) { part["gate"] = "review" },
			want:   `actions[0] "split" tasks[1] "B" gate: gate "review" is an agent gate, not a command gate`,
		},
		{
			name:   "empty gate",
			mutate: func(part map[string]any) { part["gate"] = "" },
			want:   `actions[0] "split" tasks[1] "B" gate: empty`,
		},
		{
			name:   "unknown gate",
			mutate: func(part map[string]any) { part["gate"] = "missing" },
			want:   `actions[0] "split" tasks[1] "B" gate: resolve gate:`,
		},
		{
			name: "evidence conclusion blank",
			mutate: func(part map[string]any) {
				item := splitEvidence("a conclusion for B")
				item.Conclusion = ""
				part["evidence"] = item
			},
			want: `actions[0] "split" tasks[1] "B" item.conclusion: empty or blank`,
		},
		{
			name: "evidence judge unresolved",
			mutate: func(part map[string]any) {
				item := splitEvidence("a conclusion for B")
				item.Judge = "nobody"
				part["evidence"] = item
			},
			want: `actions[0] "split" tasks[1] "B" item.judge: resolve judge:`,
		},
		{
			name:   "key no descendant has",
			mutate: func(part map[string]any) { part["bogus"] = true },
			want:   `actions[0] "split" args: tasks: descendant 1: json: unknown field "bogus"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			part := splitPart("B", "internal/planner/b.go")
			tc.mutate(part)
			parts := []map[string]any{splitPart("A", "internal/planner/a.go"), part}
			lines := checkPlan(parseSplit(t, 2, parts), nil)
			if len(lines) != 1 || !strings.HasPrefix(lines[0], tc.want) {
				t.Errorf("Check = %q, want one line starting %q", lines, tc.want)
			}
		})
	}
}

// TestTaskSplitActionStoresDescendants checks that a checked return's split reaches the stored pending action with its
// descendants, each read back with its test changes, claims, contributes, gate and evidence item.
func TestTaskSplitActionStoresDescendants(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn, err := s.ClaimTurn(ctx, runnerEpicID, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	a := splitPart("A", "internal/planner/a.go")
	a["test_changes"] = []string{"internal/planner/a.go"}
	ret := parseSplit(t, 2, []map[string]any{a, splitPart("B", "internal/planner/b.go")})
	checkLines(t, checkPlan(ret, nil))
	if err = s.CompleteTurn(ctx, runnerEpicID, turn.Seq, planOf(ret), claimedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	pending, err := s.PendingActions(ctx)
	if err != nil {
		t.Fatalf("PendingActions: %v", err)
	}
	want := []store.SplitTask{
		{
			Title: "A", Deliverable: "a deliverable for A", Files: []string{"internal/planner/a.go"},
			TestChanges: []string{"internal/planner/a.go"}, Claims: []string{"R1"}, Contributes: []string{"R2"},
			Gate: "check", Evidence: splitEvidence("a conclusion for A"),
		},
		{
			Title: "B", Deliverable: "a deliverable for B", Files: []string{"internal/planner/b.go"},
			Claims: []string{"R1"}, Contributes: []string{"R2"}, Gate: "check",
			Evidence: splitEvidence("a conclusion for B"),
		},
	}
	if len(pending) != 1 || pending[0].Kind != store.ActionSplit || pending[0].Task != 2 ||
		!reflect.DeepEqual(pending[0].Split, want) {
		t.Errorf("pending actions = %+v, want one split of T2 with descendants %+v", pending, want)
	}
}

// TestTaskSplitPromptNamesSplitTasks checks that the Tasks section names a split task's status, and that a descendant
// names the task it was split from, in both the summary and the full task lines.
func TestTaskSplitPromptNamesSplitTasks(t *testing.T) {
	t.Parallel()
	tasks := []store.EpicTask{
		{Seq: 1, Title: "Big task", Status: store.TaskSplit},
		{Seq: 2, Title: "Part A", Status: taskOpen, SplitFrom: 1},
		{Seq: 3, Title: "Part B", Status: taskDone, SplitFrom: 1},
	}
	summary, err := summaryTaskLines(tasks, nil)
	if err != nil {
		t.Fatalf("summaryTaskLines: %v", err)
	}
	full, err := taskLines(tasks)
	if err != nil {
		t.Fatalf("taskLines: %v", err)
	}
	want := []string{
		"- T1 [split] Big task", "- T2 [open] Part A (split from T1)", "- T3 [done] Part B (split from T1)",
	}
	for _, lines := range [][]string{summary, full} {
		for _, line := range want {
			if !slices.Contains(lines, line) {
				t.Errorf("Tasks lines %q lack %q", lines, line)
			}
		}
	}
}
