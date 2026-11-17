package build

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// briefEpic is an epic with three Requirements, one exclusion, one epic decision, and a turn with two decisions
// followed by a turn with none.
func briefEpic() store.EpicRow {
	return store.EpicRow{
		ID:    "repo/epic-x",
		Title: "Build the task",
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: "one"},
			{ID: "R2", Text: "two"},
			{ID: "R3", Text: "three"},
		},
		Exclusions: []string{"no network"},
		Decisions:  []store.EpicDecision{{Seq: 1, ID: "D1", Text: "use sqlite"}},
		Turns: []store.EpicTurn{
			{Seq: 1, Decisions: []string{"keep it simple", "split later"}},
			{Seq: 2},
		},
	}
}

// briefTask is task T4 with a capture, two files, and Requirements R3 and R1 in that order, plus R9, which the epic
// does not have.
func briefTask() store.EpicTask {
	return store.EpicTask{
		Seq:          4,
		Title:        "Render brief",
		Deliverable:  "brief.go",
		Files:        []string{"internal/build/brief.go", "internal/build/brief_test.go"},
		Requirements: []string{"R3", "R9", "R1"},
		Gate:         "check",
		Evidence: evidence.Item{
			Capture:    []string{"scripts/render", "--brief"},
			Conclusion: "the brief renders",
		},
	}
}

func TestBriefRendersEverySectionInOrder(t *testing.T) {
	t.Parallel()
	red := store.EvidenceRun{Exit: 1, Verdict: "fails"}
	got := Brief(briefEpic(), briefTask(), red, []byte("first line\nlast line\n"), nil, "boom")
	want := "You implement task T4 of epic repo/epic-x (Build the task) in this worktree, checked out detached at the task's base.\n" +
		"Task: Render brief\n" +
		"Deliverable: brief.go\n" +
		"Change only these files:\n" +
		"- internal/build/brief.go\n" +
		"- internal/build/brief_test.go\n" +
		"Gate: check (orreryd runs it on your commit; it must pass)\n" +
		"Evidence: the brief renders\n" +
		"Capture: scripts/render --brief\n" +
		"Requirements:\n" +
		"- R3: three\n" +
		"- R1: one\n" +
		"Exclusions:\n" +
		"- no network\n" +
		"Decisions:\n" +
		"- use sqlite\n" +
		"- keep it simple\n" +
		"- split later\n" +
		"Red at the base: exit 1, verdict fails\n" +
		"Red log (last 8 KiB):\n" +
		"```\n" +
		"first line\nlast line\n" +
		"\n```\n" +
		"Your previous attempt failed: boom\n" +
		"Commit once, then return status done with commit set to the full id of HEAD and a short summary. Return needs_context or blocked, with the reason in summary, when you cannot. When the work needs files beyond the owned list, return needs_context and name each one in needs_files."
	if got != want {
		t.Fatalf("brief\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBriefEmptySectionsPrintNoneAndSuppliedEvidenceNamesFiles(t *testing.T) {
	t.Parallel()
	epic := store.EpicRow{ID: "repo/epic-x", Title: "Build the task"}
	task := store.EpicTask{
		Seq:         4,
		Title:       "Render brief",
		Deliverable: "brief.go",
		Gate:        "check",
		Evidence:    evidence.Item{Supplied: []string{"a/one.txt", "b/two.txt"}, Conclusion: "the brief renders"},
	}
	red := store.EvidenceRun{Exit: 0, Verdict: "holds"}
	got := Brief(epic, task, red, nil, nil, "")
	want := "You implement task T4 of epic repo/epic-x (Build the task) in this worktree, checked out detached at the task's base.\n" +
		"Task: Render brief\n" +
		"Deliverable: brief.go\n" +
		"Change only these files:\n" +
		"- none\n" +
		"Gate: check (orreryd runs it on your commit; it must pass)\n" +
		"Evidence: the brief renders\n" +
		"Supplied: a/one.txt, b/two.txt\n" +
		"Requirements:\n" +
		"- none\n" +
		"Exclusions:\n" +
		"- none\n" +
		"Decisions:\n" +
		"- none\n" +
		"Red at the base: exit 0, verdict holds\n" +
		"Red log (last 8 KiB):\n" +
		"```\n" +
		"\n```\n" +
		"Commit once, then return status done with commit set to the full id of HEAD and a short summary. Return needs_context or blocked, with the reason in summary, when you cannot. When the work needs files beyond the owned list, return needs_context and name each one in needs_files."
	if got != want {
		t.Fatalf("brief\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBriefCutsRedLogToItsLastEightKiB(t *testing.T) {
	t.Parallel()
	tail := strings.Repeat("b", 8192)
	log := []byte(strings.Repeat("a", 100) + tail)
	got := Brief(briefEpic(), briefTask(), store.EvidenceRun{}, log, nil, "")
	if want := "Red log (last 8 KiB):\n```\n" + tail + "\n```\n"; !strings.Contains(got, want) {
		t.Fatalf("red log not cut to its last 8192 bytes; brief:\n%s", got)
	}
}

func TestBriefPrintsRedStderrAfterRedLog(t *testing.T) {
	t.Parallel()
	red := store.EvidenceRun{Exit: 1, Verdict: "fails"}
	got := Brief(briefEpic(), briefTask(), red, []byte("first line\nlast line\n"), []byte("panic: boom\n"), "boom")
	want := "Red log (last 8 KiB):\n```\nfirst line\nlast line\n\n```\n" +
		"Red stderr (last 8 KiB):\n```\npanic: boom\n\n```\n" +
		"Your previous attempt failed: boom\n"
	if !strings.Contains(got, want) {
		t.Fatalf("red stderr not printed after the red log; brief:\n%s", got)
	}
}

func TestBriefCutsRedStderrToItsLastEightKiB(t *testing.T) {
	t.Parallel()
	tail := strings.Repeat("b", 8192)
	stderr := []byte(strings.Repeat("a", 100) + tail)
	got := Brief(briefEpic(), briefTask(), store.EvidenceRun{}, nil, stderr, "")
	if want := "Red stderr (last 8 KiB):\n```\n" + tail + "\n```\n"; !strings.Contains(got, want) {
		t.Fatalf("red stderr not cut to its last 8192 bytes; brief:\n%s", got)
	}
}

func TestBriefWithoutRedStderrPrintsNoStderrBlock(t *testing.T) {
	t.Parallel()
	red := store.EvidenceRun{Exit: 1, Verdict: "fails"}
	got := Brief(briefEpic(), briefTask(), red, []byte("first line\n"), nil, "")
	if strings.Contains(got, "Red stderr") {
		t.Fatalf("brief for a red run with no stderr log prints a stderr block:\n%s", got)
	}
}

func TestReviewPromptNamesRangeFilesAndRequirements(t *testing.T) {
	t.Parallel()
	got := ReviewPrompt(briefEpic(), briefTask(), "base1", "rev1")
	want := "Task T4 of epic repo/epic-x (Build the task): Render brief\n" +
		"Deliverable: brief.go\n" +
		"Files:\n" +
		"- internal/build/brief.go\n" +
		"- internal/build/brief_test.go\n" +
		"Requirements:\n" +
		"- R3: three\n" +
		"- R1: one\n" +
		"The change under review is base1..rev1; this worktree is checked out at rev1. Inspect it with `git diff base1 rev1`."
	if got != want {
		t.Fatalf("review prompt\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestReviewPromptEmptySectionsPrintNone(t *testing.T) {
	t.Parallel()
	epic := store.EpicRow{ID: "repo/epic-x", Title: "Build the task"}
	task := store.EpicTask{Seq: 4, Title: "Render brief", Deliverable: "brief.go"}
	got := ReviewPrompt(epic, task, "base1", "rev1")
	want := "Task T4 of epic repo/epic-x (Build the task): Render brief\n" +
		"Deliverable: brief.go\n" +
		"Files:\n" +
		"- none\n" +
		"Requirements:\n" +
		"- none\n" +
		"The change under review is base1..rev1; this worktree is checked out at rev1. Inspect it with `git diff base1 rev1`."
	if got != want {
		t.Fatalf("review prompt\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestImplementerSchemaRequiresStatusAndSummary(t *testing.T) {
	t.Parallel()
	var schema struct {
		Type       string   `json:"type"`
		Required   []string `json:"required"`
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(ImplementerSchema), &schema); err != nil {
		t.Fatalf("ImplementerSchema is not JSON: %v", err)
	}
	if schema.Type != "object" {
		t.Fatalf("type = %q, want object", schema.Type)
	}
	if want := []string{"status", "summary"}; !slices.Equal(schema.Required, want) {
		t.Fatalf("required = %v, want %v", schema.Required, want)
	}
	if want := []string{"done", "needs_context", "blocked"}; !slices.Equal(schema.Properties["status"].Enum, want) {
		t.Fatalf("status enum = %v, want %v", schema.Properties["status"].Enum, want)
	}
	if _, ok := schema.Properties["commit"]; !ok {
		t.Fatalf("properties lack commit: %v", schema.Properties)
	}
}
