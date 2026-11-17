package build

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
)

// briefEpic is an epic with three Requirements, one exclusion, one epic decision, a turn with two decisions followed by
// a turn with none, one enabled task-stage reviewer, one disabled one and one epic-stage one.
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
		Reviewers: []reviewers.Reviewer{
			{
				Name: "floor", Enabled: true, Stage: []string{reviewers.StageTask},
				Must: []string{"tests use fakes"}, MustNot: []string{"a real sleep"},
			},
			{Name: "off", Stage: []string{reviewers.StageTask}, Must: []string{"never shown"}},
			{Name: "whole", Enabled: true, Stage: []string{reviewers.StageEpic}, Must: []string{"never shown"}},
		},
	}
}

// briefTask is task T4 at base b4se with a capture whose last word needs quoting, two files, one test change, and
// Requirements R3 and R1 in that order, plus R9, which the epic does not have.
func briefTask() store.EpicTask {
	return store.EpicTask{
		Seq:          4,
		Title:        "Render brief",
		Deliverable:  "brief.go",
		Files:        []string{"internal/build/brief.go", "internal/build/brief_test.go"},
		TestChanges:  []string{"internal/build/brief_test.go"},
		Requirements: []string{"R3", "R9", "R1"},
		Gate:         "check",
		Evidence: evidence.Item{
			Capture:    []string{"scripts/in-devenv", "-c", "go test -run Brief ./internal/build/"},
			Conclusion: "the brief renders",
		},
		Build: store.TaskBuild{Base: "b4se"},
	}
}

// briefGate is the command gate check, whose last word needs quoting.
func briefGate() gates.Gate {
	return gates.Gate{Name: "check", Argv: []string{"scripts/in-devenv", "-c", "just remote just check"}}
}

// briefRules is the line every brief gives about files beyond the list.
const briefRules = "Do not edit any other file. When the work needs a file that is not in this list, stop and return " +
	"needs_context with each such path in needs_files. orreryd gives you every path no other task owns and sends you " +
	"back to this same worktree with your work kept.\n"

// briefGuardAndGate are the guard line and the gate line for briefGate.
const briefGuardAndGate = "Outside those test changes, orreryd's guard refuses a commit that deletes or renames a guarded " +
	"test file, removes a test present at the base, lowers a test's assertions, adds a skip, or changes a file a " +
	"capture runs.\n" +
	"Gate: check. After you return done, orreryd runs `scripts/in-devenv -c 'just remote just check'` on your commit. " +
	"Do not run the gate yourself. When it fails, orreryd sends you its output: fix the cause, amend your commit, and " +
	"return done again.\n"

// briefClose is the closing of a brief for a task at base.
func briefClose(base string) string {
	return "This worktree keeps any earlier work on this task. Read `git status` and `git log --oneline " + base +
		"..HEAD` first, and continue from what is there.\n" +
		"Finish every command you start before you return: run nothing in the background and wait on no monitor. " +
		"Ending your turn is your return.\n" +
		"Commit your work, then return status done with commit set to the full id of HEAD and a short summary. " +
		"Return needs_context or blocked, with the reason in summary, when you cannot. When the work needs files " +
		"beyond the owned list, return needs_context and name each one in needs_files."
}

func TestBriefRendersEverySectionInOrder(t *testing.T) {
	t.Parallel()
	red := store.EvidenceRun{Exit: 1, Verdict: "fails"}
	got := Brief(briefEpic(), briefTask(), briefGate(), red, []byte("first line\nlast line\n"), nil, "boom")
	want := "You implement task T4 of epic repo/epic-x (Build the task) in this worktree, whose task base is b4se.\n" +
		"Task: Render brief\n" +
		"Deliverable: brief.go\n" +
		"Change only these files:\n" +
		"- internal/build/brief.go\n" +
		"- internal/build/brief_test.go\n" +
		briefRules +
		"Test changes this task may make to the guarded tests:\n" +
		"- internal/build/brief_test.go\n" +
		briefGuardAndGate +
		"Evidence: the brief renders\n" +
		"Capture: scripts/in-devenv -c 'go test -run Brief ./internal/build/'\n" +
		"Requirements:\n" +
		"- R3: three\n" +
		"- R1: one\n" +
		"Exclusions:\n" +
		"- no network\n" +
		"Decisions:\n" +
		"- use sqlite\n" +
		"- keep it simple\n" +
		"- split later\n" +
		"After the gate passes, reviewers judge your commit by these rules:\n" +
		"- floor must: tests use fakes\n" +
		"- floor must not: a real sleep\n" +
		"Red at the base: exit 1, verdict fails\n" +
		"Red log (last 8 KiB):\n" +
		"```\n" +
		"first line\nlast line\n" +
		"\n```\n" +
		"From your previous try on this task: boom\n" +
		briefClose("b4se")
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
		Build:       store.TaskBuild{Base: "b4se"},
	}
	red := store.EvidenceRun{Exit: 0, Verdict: "holds"}
	got := Brief(epic, task, briefGate(), red, nil, nil, "")
	want := "You implement task T4 of epic repo/epic-x (Build the task) in this worktree, whose task base is b4se.\n" +
		"Task: Render brief\n" +
		"Deliverable: brief.go\n" +
		"Change only these files:\n" +
		"- none\n" +
		briefRules +
		"Test changes this task may make to the guarded tests:\n" +
		"- none\n" +
		briefGuardAndGate +
		"Evidence: the brief renders\n" +
		"Supplied: a/one.txt, b/two.txt\n" +
		"Requirements:\n" +
		"- none\n" +
		"Exclusions:\n" +
		"- none\n" +
		"Decisions:\n" +
		"- none\n" +
		"After the gate passes, reviewers judge your commit by these rules:\n" +
		"- none\n" +
		"Red at the base: exit 0, verdict holds\n" +
		"Red log (last 8 KiB):\n" +
		"```\n" +
		"\n```\n" +
		briefClose("b4se")
	if got != want {
		t.Fatalf("brief\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBriefShellWordsQuoteOnlyWhatAShellWouldSplit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"go", "test", "./internal/build/"}, "go test ./internal/build/"},
		{[]string{"scripts/in-devenv", "-c", "just check"}, "scripts/in-devenv -c 'just check'"},
		{[]string{"sh", "-c", "echo 'hi'"}, `sh -c 'echo '\''hi'\'''`},
		{[]string{"printf", ""}, "printf ''"},
		{[]string{"echo", "$HOME"}, "echo '$HOME'"},
	}
	for _, tc := range cases {
		if got := shellWords(tc.argv); got != tc.want {
			t.Errorf("shellWords(%q) = %s, want %s", tc.argv, got, tc.want)
		}
	}
}

func TestBriefCutsRedLogToItsLastEightKiB(t *testing.T) {
	t.Parallel()
	tail := strings.Repeat("b", 8192)
	log := []byte(strings.Repeat("a", 100) + tail)
	got := Brief(briefEpic(), briefTask(), briefGate(), store.EvidenceRun{}, log, nil, "")
	if want := "Red log (last 8 KiB):\n```\n" + tail + "\n```\n"; !strings.Contains(got, want) {
		t.Fatalf("red log not cut to its last 8192 bytes; brief:\n%s", got)
	}
}

func TestBriefPrintsRedStderrAfterRedLog(t *testing.T) {
	t.Parallel()
	red := store.EvidenceRun{Exit: 1, Verdict: "fails"}
	got := Brief(briefEpic(), briefTask(), briefGate(), red, []byte("first line\nlast line\n"), []byte("panic: boom\n"), "boom")
	want := "Red log (last 8 KiB):\n```\nfirst line\nlast line\n\n```\n" +
		"Red stderr (last 8 KiB):\n```\npanic: boom\n\n```\n" +
		"From your previous try on this task: boom\n"
	if !strings.Contains(got, want) {
		t.Fatalf("red stderr not printed after the red log; brief:\n%s", got)
	}
}

func TestBriefCutsRedStderrToItsLastEightKiB(t *testing.T) {
	t.Parallel()
	tail := strings.Repeat("b", 8192)
	stderr := []byte(strings.Repeat("a", 100) + tail)
	got := Brief(briefEpic(), briefTask(), briefGate(), store.EvidenceRun{}, nil, stderr, "")
	if want := "Red stderr (last 8 KiB):\n```\n" + tail + "\n```\n"; !strings.Contains(got, want) {
		t.Fatalf("red stderr not cut to its last 8192 bytes; brief:\n%s", got)
	}
}

func TestBriefWithoutRedStderrPrintsNoStderrBlock(t *testing.T) {
	t.Parallel()
	red := store.EvidenceRun{Exit: 1, Verdict: "fails"}
	got := Brief(briefEpic(), briefTask(), briefGate(), red, []byte("first line\n"), nil, "")
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
