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

// briefStanding is the standing rules block every brief gives, before the Deliverable.
const briefStanding = "Standing rules a commit is rejected for:\n" +
	"- changing anything the Deliverable does not ask for, including tidying, reformatting, or fixing code or tests " +
	"that are already broken; this overrides any standing instruction to fix what you find\n" +
	"- editing, renaming, or deleting an existing test the Deliverable does not name\n" +
	"- a test that repeats an existing assertion or passes with the change removed\n" +
	"- a real timer or clock in a test\n" +
	"- an Evidence clause left unproved\n" +
	"- dead code, an unhandled error, a skip, or a suppressed check\n"

// briefGuardAndGate are the guard line and the gate line for briefGate.
const briefGuardAndGate = "Outside those test changes, orreryd's guard refuses a commit that deletes or renames a guarded " +
	"test file, removes a test present at the base, lowers a test's assertions, adds a skip, or changes a file a " +
	"capture runs.\n" +
	"Gate: check. After you return done, orreryd runs `scripts/in-devenv -c 'just remote just check'` on your commit. " +
	"Do not run the gate yourself. When it fails, orreryd sends you its output: fix the cause, commit the fix on top, " +
	"and return done again.\n"

// briefClose is the closing of a brief for a task at base.
func briefClose(base string) string {
	return "This worktree keeps any earlier work on this task. Read `git status` and `git log --oneline " + base +
		"..HEAD` first, and continue from what is there.\n" +
		"Finish every command you start before you return: run nothing in the background and wait on no monitor. " +
		"Ending your turn is your return.\n" +
		"Run the Capture, when Evidence names one, and wait for it to finish before committing.\n" +
		"Commit your work on top of the start commit " + base + ", never amend or rewrite a commit, then return status " +
		"done with commit set to the full id of HEAD and a short summary.\n" +
		"needs_context means the work needs files or facts you do not have, named in needs_files or in summary. " +
		"blocked means the task cannot be done as written. Return one of them, with the reason in summary, when you " +
		"cannot finish.\n" +
		"A commit that changes a file outside the owned files is sent back: take that file out of the commit, or " +
		"return needs_context naming it. When the work needs files beyond the owned list, return needs_context and " +
		"name each one in needs_files."
}

func TestBriefRendersEverySectionInOrder(t *testing.T) {
	t.Parallel()
	red := store.EvidenceRun{Exit: 1, Verdict: "fails"}
	got := Brief(briefEpic(), briefTask(), briefGate(), red, []byte("first line\nlast line\n"), nil, "boom")
	want := "You implement task T4 of epic repo/epic-x (Build the task) in this worktree, whose task base is b4se.\n" +
		"Task: Render brief\n" +
		briefStanding +
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
		briefStanding +
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
	got := Brief(
		briefEpic(),
		briefTask(),
		briefGate(),
		red,
		[]byte("first line\nlast line\n"),
		[]byte("panic: boom\n"),
		"boom",
	)
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
	got := ReviewPrompt(briefEpic(), briefTask(), "floor", "base1", "rev1", store.StepCheck{})
	want := "Task T4 of epic repo/epic-x (Build the task): Render brief\n" +
		"Deliverable: brief.go\n" +
		"Files:\n" +
		"- internal/build/brief.go\n" +
		"- internal/build/brief_test.go\n" +
		"Test changes:\n" +
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
	got := ReviewPrompt(epic, task, "floor", "base1", "rev1", store.StepCheck{})
	want := "Task T4 of epic repo/epic-x (Build the task): Render brief\n" +
		"Deliverable: brief.go\n" +
		"Files:\n" +
		"- none\n" +
		"Test changes:\n" +
		"- none\n" +
		"Requirements:\n" +
		"- none\n" +
		"The change under review is base1..rev1; this worktree is checked out at rev1. Inspect it with `git diff base1 rev1`."
	if got != want {
		t.Fatalf("review prompt\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestReviewPromptEndsTestParsimonyWithTheGatesTestCost: the test_parsimony reviewer's prompt ends with the Test cost
// section, holding the budget lines of the gate check's stdout and stderr in order, or "no budget lines" when the check
// holds none or the task has no gate check.
func TestReviewPromptEndsTestParsimonyWithTheGatesTestCost(t *testing.T) {
	t.Parallel()
	gateOutput := store.StepCheck{
		Name: "gate", OK: true, Detail: "gate exited 0",
		Stdout: "check-changed: base 212dc29, 3 changed files\n" +
			"ok  \tgithub.com/joshsymonds/orrery/internal/store\t41.2s\n" +
			"test-budget: internal/store 212 cpu-s > 160 (+33%)\n" +
			"test-budget: internal/new 12 cpu-s, no baseline\n" +
			"test-budget: 2 packages checked, none over twice its baseline\n" +
			"check-changed: Android untouched; android-test skipped\n" +
			"check-changed: OK after 97s of its 240s budget\n",
		Stderr: "golangci-lint: 0 issues\n",
	}
	exceeded := store.StepCheck{
		Name: "gate", Detail: "gate exited 124",
		Stderr: "check-changed: exceeded 240s budget (Go tests was running)\n",
	}
	cases := []struct {
		name string
		gate store.StepCheck
		want []string
	}{
		{
			name: "passed gate",
			gate: gateOutput,
			want: []string{
				"- test-budget: internal/store 212 cpu-s > 160 (+33%)",
				"- test-budget: internal/new 12 cpu-s, no baseline",
				"- test-budget: 2 packages checked, none over twice its baseline",
				"- check-changed: OK after 97s of its 240s budget",
			},
		},
		{
			name: "exceeded budget on stderr",
			gate: exceeded,
			want: []string{"- check-changed: exceeded 240s budget (Go tests was running)"},
		},
		{
			name: "output without budget lines",
			gate: store.StepCheck{Name: "gate", OK: true, Stdout: "all good\n"},
			want: []string{"- no budget lines"},
		},
		{name: "no gate check", want: []string{"- no budget lines"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ReviewPrompt(briefEpic(), briefTask(), reviewers.TestParsimony, "base1", "rev1", tc.gate)
			plain := ReviewPrompt(briefEpic(), briefTask(), "floor", "base1", "rev1", tc.gate)
			want := strings.Join(append([]string{plain, reviewers.TestCostHeading}, tc.want...), "\n")
			if got != want {
				t.Fatalf("review prompt\n got:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

// TestReviewPromptGivesNoOtherReviewerTheTestCost: a reviewer other than test_parsimony gets no Test cost section, even
// when the gate check holds budget lines.
func TestReviewPromptGivesNoOtherReviewerTheTestCost(t *testing.T) {
	t.Parallel()
	gate := store.StepCheck{Name: "gate", OK: true, Stdout: "test-budget: internal/store 212 cpu-s > 160 (+33%)\n"}
	got := ReviewPrompt(briefEpic(), briefTask(), "floor", "base1", "rev1", gate)
	if strings.Contains(got, "Test cost") || strings.Contains(got, "test-budget") {
		t.Fatalf("floor review prompt carries the Test cost section:\n%s", got)
	}
}

// fixtureBrief is the brief of briefTask under briefEpic, with no red stderr and no previous try.
func fixtureBrief() string {
	return Brief(briefEpic(), briefTask(), briefGate(), store.EvidenceRun{}, nil, nil, "")
}

// assertBriefHas fails the test for each of texts the brief does not contain.
func assertBriefHas(t *testing.T, got string, texts ...string) {
	t.Helper()
	for _, text := range texts {
		if !strings.Contains(got, text) {
			t.Errorf("brief lacks %q; brief:\n%s", text, got)
		}
	}
}

// TestBriefCarriesTaskAndStandingRules: the brief carries the task and the rules a commit is rejected for, and no
// decision the epic or its turns hold.
func TestBriefCarriesTaskAndStandingRules(t *testing.T) {
	t.Parallel()
	t.Run("a brief for a task whose epic and turns hold decisions lists none of them", func(t *testing.T) {
		t.Parallel()
		epic := briefEpic()
		if len(epic.Decisions) == 0 || len(epic.Turns[0].Decisions) == 0 {
			t.Fatal("the fixture holds no decisions to test against")
		}
		got := Brief(epic, briefTask(), briefGate(), store.EvidenceRun{}, nil, nil, "")
		for _, text := range []string{"use sqlite", "keep it simple", "split later", "Decisions:"} {
			if strings.Contains(got, text) {
				t.Errorf("brief carries %q; brief:\n%s", text, got)
			}
		}
	})
	t.Run("the standing rules come before the Deliverable", func(t *testing.T) {
		t.Parallel()
		got := fixtureBrief()
		rules, deliverable := strings.Index(got, briefStanding), strings.Index(got, "Deliverable: ")
		if rules < 0 || deliverable < 0 || rules > deliverable {
			t.Fatalf("standing rules at %d, Deliverable at %d; brief:\n%s", rules, deliverable, got)
		}
	})
	t.Run("the standing rules name tidying and already-broken code as rejected", func(t *testing.T) {
		t.Parallel()
		assertBriefHas(t, fixtureBrief(),
			"- changing anything the Deliverable does not ask for, including tidying, reformatting, or fixing code or "+
				"tests that are already broken; this overrides any standing instruction to fix what you find\n")
	})
	t.Run("the brief says to run the Capture before committing", func(t *testing.T) {
		t.Parallel()
		got := fixtureBrief()
		assertBriefHas(t, got, "Run the Capture, when Evidence names one, and wait for it to finish before committing.")
		run := strings.Index(got, "Run the Capture")
		commit := strings.Index(got, "Commit your work")
		if run < 0 || commit < 0 || run > commit {
			t.Fatalf("Capture run at %d, commit instruction at %d; brief:\n%s", run, commit, got)
		}
	})
	t.Run("the brief says to commit on top and never amend", func(t *testing.T) {
		t.Parallel()
		assertBriefHas(t, fixtureBrief(),
			"Commit your work on top of the start commit b4se, never amend or rewrite a commit,",
			"commit the fix on top, and return done again.",
		)
	})
	t.Run("the brief defines needs_context and blocked", func(t *testing.T) {
		t.Parallel()
		assertBriefHas(t, fixtureBrief(),
			"needs_context means the work needs files or facts you do not have, named in needs_files or in summary.",
			"blocked means the task cannot be done as written.",
		)
	})
	const takenOut = "the brief says files outside the owned files may be taken out or returned as needs_context"
	t.Run(takenOut, func(t *testing.T) {
		t.Parallel()
		assertBriefHas(t, fixtureBrief(),
			"A commit that changes a file outside the owned files is sent back: take that file out of the commit, or "+
				"return needs_context naming it.",
		)
	})
}

// TestStepSendBackReviewPromptCarriesTestChanges: the review a send-back's reviewers judge by names the task's test
// changes, after its files.
func TestStepSendBackReviewPromptCarriesTestChanges(t *testing.T) {
	t.Parallel()
	t.Run("ReviewPrompt carries the task's Test changes", func(t *testing.T) {
		t.Parallel()
		task := briefTask()
		task.TestChanges = []string{"internal/build/brief_test.go", "internal/build/floor_test.go"}
		got := ReviewPrompt(briefEpic(), task, "floor", "base1", "rev1", store.StepCheck{})
		want := "Files:\n" +
			"- internal/build/brief.go\n" +
			"- internal/build/brief_test.go\n" +
			"Test changes:\n" +
			"- internal/build/brief_test.go\n" +
			"- internal/build/floor_test.go\n" +
			"Requirements:\n"
		if !strings.Contains(got, want) {
			t.Fatalf("review prompt lacks the Test changes after Files; review prompt:\n%s", got)
		}
	})
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
