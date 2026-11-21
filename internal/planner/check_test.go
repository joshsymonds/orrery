package planner

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// epicRequirements returns the Requirement ids of the epic the tests plan against.
func epicRequirements() []string {
	return []string{"R1", "R2"}
}

// validTask is a task that passes every check against epicRequirements() when it names no owned file.
func validTask(title string, files ...string) Task {
	return Task{
		Title:        title,
		Deliverable:  "a deliverable for " + title,
		Files:        files,
		Requirements: []string{"R1"},
		Gate:         "check",
		Evidence: evidence.Item{
			Kind:       evidence.KindCapture,
			Capture:    []string{"scripts/check"},
			Limit:      60,
			Conclusion: "a conclusion for " + title,
			Judge:      "a judge",
		},
	}
}

// checkLines asserts that Check returned exactly want, in order, and nil when want is empty.
func checkLines(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Check() = %q, want %q", got, want)
	}
}

// checkPlan runs Check on ret against epicRequirements() and owned, with the gates and judges of testResolver.
func checkPlan(ret Return, owned []string) []string {
	return checkTasks(ret, epicTasksOwning(owned))
}

// checkTasks runs Check on ret against epicRequirements() and tasks, with the gates and judges of testResolver.
func checkTasks(ret Return, tasks []store.EpicTask) []string {
	var resolve testResolver
	gate := func(name string) error { return resolve.Gate(context.Background(), store.EpicRow{}, name) }
	judge := func(name string) error { return resolve.Judge(store.EpicRow{}, name) }
	return Check(ret, epicRequirements(), tasks, gate, judge)
}

// epicTasksOwning returns the epic's tasks for a test that names owned files: one open task, T1, that owns them.
func epicTasksOwning(owned []string) []store.EpicTask {
	if len(owned) == 0 {
		return nil
	}
	return []store.EpicTask{{Seq: 1, Status: taskOpen, Files: owned}}
}

func TestCheckRefusesUncleanOrNonRelativeFile(t *testing.T) {
	t.Parallel()
	const reason = "not a normalized repository-relative file path"
	cases := []struct {
		name string
		file string
	}{
		{"empty", ""},
		{"leading white space", " internal/x.go"},
		{"trailing white space", "internal/x.go "},
		{"leading slash", "/internal/x.go"},
		{"backslash", `internal\x.go`},
		{"double slash", "internal//x.go"},
		{"trailing slash", "internal/"},
		{"parent step inside the path", "internal/../x.go"},
		{"dot", "."},
		{"dot-dot", ".."},
		{"leading dot component", "./x.go"},
		{"leading dot-dot component", "../x.go"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ret := Return{Tasks: []Task{validTask("T", tc.file)}}
			checkLines(t, checkPlan(ret, nil), `tasks[0] "T" files[0]: `+reason)
		})
	}
}

func TestCheckRefusesFileInsideGitDirectory(t *testing.T) {
	t.Parallel()
	const reason = "inside a .git directory"
	cases := []struct {
		name string
		file string
	}{
		{"a hook", ".git/hooks/pre-commit"},
		{"the config", ".git/config"},
		{"the directory itself", ".git"},
		{"a nested repository's hook", "vendor/lib/.git/hooks/post-checkout"},
		{"another letter case", ".Git/hooks/pre-push"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ret := Return{Tasks: []Task{validTask("T", tc.file)}}
			checkLines(t, checkPlan(ret, nil), `tasks[0] "T" files[0]: `+reason)
		})
	}
}

func TestCheckAcceptsGitNamedFilesOutsideGitDirectory(t *testing.T) {
	t.Parallel()
	ret := Return{Tasks: []Task{validTask("T", ".gitignore", "docs/.github/x.md", "internal/git/run.go")}}
	checkLines(t, checkPlan(ret, nil))
}

func TestCheckRefusesFileNamedTwice(t *testing.T) {
	t.Parallel()
	const reason = "named twice in the plan"
	cases := []struct {
		name string
		ret  Return
		want string
	}{
		{
			name: "across tasks",
			ret: Return{Tasks: []Task{
				validTask("A", "internal/a.go"),
				validTask("B", "internal/a.go"),
			}},
			want: `tasks[1] "B" files[0]: ` + reason,
		},
		{
			name: "within one task",
			ret:  Return{Tasks: []Task{validTask("T", "internal/a.go", "internal/a.go")}},
			want: `tasks[0] "T" files[1]: ` + reason,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkLines(t, checkPlan(tc.ret, nil), tc.want)
		})
	}
}

func TestCheckRefusesFileOwnedByUnfinishedTask(t *testing.T) {
	t.Parallel()
	ret := Return{Tasks: []Task{validTask("T", "internal/store/epics.go")}}
	owned := []string{"internal/store/epics.go"}
	checkLines(t, checkPlan(ret, owned),
		`tasks[0] "T" files[0]: owned by an unfinished task`)
}

func TestCheckRefusesUnknownRequirement(t *testing.T) {
	t.Parallel()
	task := validTask("T", "internal/a.go")
	task.Requirements = []string{"R1", "R9"}
	checkLines(t, checkPlan(Return{Tasks: []Task{task}}, nil),
		`tasks[0] "T" requirements[1]: not a Requirement of the epic`)
}

func TestCheckRefusesUnknownGate(t *testing.T) {
	t.Parallel()
	task := validTask("T", "internal/a.go")
	task.Gate = "missing"
	checkLines(t, checkPlan(Return{Tasks: []Task{task}}, nil),
		`tasks[0] "T" gate: resolve gate: unknown gate: "missing"`)
}

func TestCheckRefusesAgentGate(t *testing.T) {
	t.Parallel()
	task := validTask("T", "internal/a.go")
	task.Gate = "review"
	checkLines(t, checkPlan(Return{Tasks: []Task{task}}, nil),
		`tasks[0] "T" gate: gate "review" is an agent gate, not a command gate`)
}

func TestCheckRefusesUnknownJudge(t *testing.T) {
	t.Parallel()
	task := validTask("T", "internal/a.go")
	task.Evidence.Judge = "nobody"
	checkLines(t, checkPlan(Return{Tasks: []Task{task}}, nil),
		`tasks[0] "T" evidence.judge: resolve judge: agent name does not resolve: "nobody"`)
}

func TestCheckGivesBlankJudgeOnlyItsFormRefusal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		judge string
	}{
		{"empty judge", ""},
		{"blank judge", "  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			task := validTask("T", "internal/a.go")
			task.Evidence.Judge = tc.judge
			checkLines(t, checkPlan(Return{Tasks: []Task{task}}, nil),
				`tasks[0] "T" evidence.judge: empty or blank`)
		})
	}
}

func TestCheckResolvesGateAndJudgeAfterFormRefusals(t *testing.T) {
	t.Parallel()
	task := validTask("T", "internal/a.go")
	task.Evidence.Limit = 0
	task.Gate = "missing"
	task.Evidence.Judge = "nobody"
	checkLines(t, checkPlan(Return{Tasks: []Task{task}}, nil),
		`tasks[0] "T" evidence.limit: 0 is outside 1 to 9223372036 seconds`,
		`tasks[0] "T" gate: resolve gate: unknown gate: "missing"`,
		`tasks[0] "T" evidence.judge: resolve judge: agent name does not resolve: "nobody"`)
}

func TestCheckRefusesEmptyTaskText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*Task)
		want   string
	}{
		{"empty title", func(task *Task) { task.Title = "" }, `tasks[0] "" title: empty`},
		{"blank title", func(task *Task) { task.Title = "   " }, `tasks[0] "   " title: empty`},
		{"empty deliverable", func(task *Task) { task.Deliverable = "" }, `tasks[0] "T" deliverable: empty`},
		{"blank deliverable", func(task *Task) { task.Deliverable = " \t" }, `tasks[0] "T" deliverable: empty`},
		{"empty gate", func(task *Task) { task.Gate = "" }, `tasks[0] "T" gate: empty`},
		{"blank gate", func(task *Task) { task.Gate = "\n" }, `tasks[0] "T" gate: empty`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			task := validTask("T", "internal/a.go")
			tc.mutate(&task)
			checkLines(t, checkPlan(Return{Tasks: []Task{task}}, nil), tc.want)
		})
	}
}

func TestCheckRefusesEvidenceForm(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		evidence evidence.Item
		want     []string
	}{
		{
			name:     "no capture and no supplied files",
			evidence: evidence.Item{Kind: evidence.KindCapture, Conclusion: "c", Judge: "a judge"},
			want: []string{
				`tasks[0] "T" evidence.capture: none, name a capture or supplied files`,
				`tasks[0] "T" evidence.kind: capture without a capture`,
			},
		},
		{
			name: "blank conclusion",
			evidence: evidence.Item{
				Kind: evidence.KindCapture, Capture: []string{"scripts/check"}, Limit: 60,
				Conclusion: " ", Judge: "a judge",
			},
			want: []string{`tasks[0] "T" evidence.conclusion: empty or blank`},
		},
		{
			name: "blank judge",
			evidence: evidence.Item{
				Kind: evidence.KindCapture, Capture: []string{"scripts/check"}, Limit: 60, Conclusion: "c",
			},
			want: []string{`tasks[0] "T" evidence.judge: empty or blank`},
		},
		{
			name:     "no kind",
			evidence: evidence.Item{Capture: []string{"scripts/check"}, Limit: 60, Conclusion: "c", Judge: "a judge"},
			want:     []string{`tasks[0] "T" evidence.kind: none, name test, capture or supplied`},
		},
		{
			name: "capture program without a slash",
			evidence: evidence.Item{
				Kind: evidence.KindCapture, Capture: []string{"check"}, Limit: 60, Conclusion: "c", Judge: "a judge",
			},
			want: []string{`tasks[0] "T" evidence.capture[0]: not a path with a / in it`},
		},
		{
			name: "limit outside the range beside a capture",
			evidence: evidence.Item{
				Kind: evidence.KindCapture, Capture: []string{"scripts/check"}, Conclusion: "c",
				Judge: "a judge",
			},
			want: []string{`tasks[0] "T" evidence.limit: 0 is outside 1 to 9223372036 seconds`},
		},
		{
			name: "artifact path outside the repository",
			evidence: evidence.Item{
				Kind:    evidence.KindCapture,
				Capture: []string{"scripts/check"}, Limit: 60, Artifacts: []string{"../out"},
				Conclusion: "c", Judge: "a judge",
			},
			want: []string{`tasks[0] "T" evidence.artifacts[0]: not a normalized repository-relative file path`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			task := validTask("T", "internal/a.go")
			task.Evidence = tc.evidence
			checkLines(t, checkPlan(Return{Tasks: []Task{task}}, nil), tc.want...)
		})
	}
}

func TestCheckRefusesTaskWithNoFiles(t *testing.T) {
	t.Parallel()
	task := validTask("T")
	task.Files = nil
	checkLines(t, checkPlan(Return{Tasks: []Task{task}}, nil),
		`tasks[0] "T" files: no files`)
}

func TestCheckRefusesTestChangesOutsideFiles(t *testing.T) {
	t.Parallel()
	task := validTask("T", "internal/a.go", "internal/a_test.go")
	task.TestChanges = []string{"internal/a_test.go", "internal/b_test.go"}
	checkLines(t, checkPlan(Return{Tasks: []Task{task}}, nil),
		`tasks[0] "T" test_changes[1]: not one of the task's files`)
}

func TestCheckAcceptsTestChangesNamingOwnFiles(t *testing.T) {
	t.Parallel()
	task := validTask("T", "internal/a.go", "internal/a_test.go")
	task.TestChanges = []string{"internal/a_test.go"}
	checkLines(t, checkPlan(Return{Tasks: []Task{task}}, nil))
}

func TestCheckRefusesEachBadFieldOnce(t *testing.T) {
	t.Parallel()
	task := validTask("T")
	task.Title = ""
	task.Gate = ""
	task.Files = nil
	checkLines(t, checkPlan(Return{Tasks: []Task{task}}, nil),
		`tasks[0] "" title: empty`,
		`tasks[0] "" files: no files`,
		`tasks[0] "" gate: empty`,
	)
}

func TestCheckWritesTitleBetweenQuotesAsGiven(t *testing.T) {
	t.Parallel()
	task := validTask(`Add "x" flag`, "internal/a.go")
	task.Deliverable = ""
	checkLines(t, checkPlan(Return{Tasks: []Task{task}}, nil),
		`tasks[0] "Add "x" flag" deliverable: empty`)
}

func TestCheckPassesValidPlanWithNewFiles(t *testing.T) {
	t.Parallel()
	ret := Return{Tasks: []Task{
		validTask("A", "internal/planner/plan.go", "internal/planner/check.go"),
		validTask("B", "internal/planner/brand_new.go"),
	}}
	ret.Tasks[1].Requirements = []string{"R1", "R2"}
	checkLines(t, checkPlan(ret, []string{"internal/store/epics.go"}))
}

// validQuestion is a question that passes every rule of store.QuestionRefusals under label.
func validQuestion(label string) Question {
	return Question{
		Label: label,
		Text:  "a text for " + label,
		Choices: []Choice{
			{ID: "first", Does: "a first choice"},
			{ID: "second", Does: "a second choice"},
		},
		Recommended: "first",
		Why:         "a why for " + label,
		Catastrophe: "irreversible-step",
		Confidence:  "low",
		Replaces:    "",
	}
}

// secondQuestion is a return whose first question is valid and whose second question is bad.
func secondQuestion(bad Question) Return {
	return Return{Questions: []Question{validQuestion("first"), bad}}
}

func TestCheckRefusesQuestionLabel(t *testing.T) {
	t.Parallel()
	bad := validQuestion("second")
	bad.Label = ""
	checkLines(t, checkPlan(secondQuestion(bad), nil), `questions[1] "" label: empty`)
}

func TestCheckRefusesQuestionText(t *testing.T) {
	t.Parallel()
	bad := validQuestion("second")
	bad.Text = ""
	checkLines(t, checkPlan(secondQuestion(bad), nil), `questions[1] "second" text: empty`)
}

func TestCheckRefusesQuestionChoices(t *testing.T) {
	t.Parallel()
	bad := validQuestion("second")
	bad.Choices = []Choice{{ID: "first", Does: "a first choice"}}
	bad.Recommended = "first"
	checkLines(t, checkPlan(secondQuestion(bad), nil), `questions[1] "second" choices: fewer than two choices`)
}

func TestCheckRefusesQuestionRecommendation(t *testing.T) {
	t.Parallel()
	bad := validQuestion("second")
	bad.Recommended = ""
	checkLines(t, checkPlan(secondQuestion(bad), nil),
		`questions[1] "second" recommended: names no declared choice`)
}

func TestCheckRefusesQuestionWhy(t *testing.T) {
	t.Parallel()
	bad := validQuestion("second")
	bad.Why = ""
	checkLines(t, checkPlan(secondQuestion(bad), nil), `questions[1] "second" why: empty`)
}

func TestCheckRefusesQuestionConfidence(t *testing.T) {
	t.Parallel()
	bad := validQuestion("second")
	bad.Confidence = ""
	checkLines(t, checkPlan(secondQuestion(bad), nil),
		`questions[1] "second" confidence: not low, medium or high`)
}

func TestCheckRefusesQuestionReplaces(t *testing.T) {
	t.Parallel()
	bad := validQuestion("second")
	bad.Replaces = "Q0"
	checkLines(t, checkPlan(secondQuestion(bad), nil),
		`questions[1] "second" replaces: not empty or Q<n> with n >= 1`)
}

func TestCheckNamesEachBadQuestionByPositionInRuleOrder(t *testing.T) {
	t.Parallel()
	first := validQuestion("")
	first.Why = ""
	second := validQuestion("second")
	second.Text = ""
	checkLines(t, checkPlan(Return{Questions: []Question{first, second}}, nil),
		`questions[0] "" label: empty`,
		`questions[0] "" why: empty`,
		`questions[1] "second" text: empty`,
	)
}

func TestCheckGivesTaskLinesBeforeQuestionLines(t *testing.T) {
	t.Parallel()
	task := validTask("T")
	task.Files = nil
	bad := validQuestion("second")
	bad.Why = ""
	checkLines(t, checkPlan(Return{Tasks: []Task{task}, Questions: []Question{validQuestion("first"), bad}}, nil),
		`tasks[0] "T" files: no files`,
		`questions[1] "second" why: empty`,
	)
}

func TestCheckPassesValidQuestions(t *testing.T) {
	t.Parallel()
	replacing := validQuestion("which-cache")
	replacing.Confidence = "high"
	replacing.Replaces = "Q1"
	ret := Return{
		Tasks:     []Task{validTask("T", "internal/a.go")},
		Questions: []Question{validQuestion("which-epic"), replacing},
	}
	checkLines(t, checkPlan(ret, nil))
}

func TestParseRoundTripsValidReturn(t *testing.T) {
	t.Parallel()
	want := Return{
		Status:    "done",
		Decisions: []string{"split the store work from the planner"},
		Tasks:     []Task{validTask("A", "internal/planner/plan.go")},
		Outline:   "one task",
		Questions: []Question{{
			Label: "which-epic", Text: "which epic?",
			Choices: []Choice{
				{ID: "first", Does: "Synthetic first choice."},
				{ID: "second", Does: "Synthetic second choice."},
			},
			Recommended: "first", Why: "Synthetic reason.", Confidence: "low", Replaces: "",
		}},
		Outcome: "work",
	}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(%s) error = %v", raw, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Parse(%s) = %+v, want %+v", raw, got, want)
	}
}

// schemaNode is the part of a JSON schema the schema test reads.
type schemaNode struct {
	Type       string                `json:"type"`
	Items      *schemaNode           `json:"items"`
	Properties map[string]schemaNode `json:"properties"`
	Required   []string              `json:"required"`
}

func TestSchemaRequiresGateAndEvidenceOfEachTask(t *testing.T) {
	t.Parallel()
	var root schemaNode
	if err := json.Unmarshal([]byte(Schema), &root); err != nil {
		t.Fatalf("Schema is not JSON: %v", err)
	}
	task := root.Properties["tasks"].Items
	if task == nil {
		t.Fatalf("Schema has no tasks items")
	}
	wantTask := []string{
		"title", "deliverable", "files", "requirements", "contributes", "test_changes", "gate", "evidence",
		"depends_on",
	}
	if !reflect.DeepEqual(task.Required, wantTask) {
		t.Errorf("task required = %q, want %q", task.Required, wantTask)
	}
	for _, name := range []string{"contributes", "test_changes"} {
		list := task.Properties[name]
		if list.Type != "array" || list.Items == nil || list.Items.Type != "string" {
			t.Errorf("%s is not an array of strings: type %q", name, list.Type)
		}
	}
	if _, ok := task.Properties["check"]; ok {
		t.Errorf("task still has a check property")
	}
	if got := task.Properties["gate"].Type; got != "string" {
		t.Errorf("gate type = %q, want string", got)
	}

	for _, name := range []string{"evidence", "depends_on"} {
		if got := task.Properties[name].Type; got != "string" {
			t.Errorf("%s type = %q, want string", name, got)
		}
	}
}

func TestPlanOfCopiesTestChangesFromReturn(t *testing.T) {
	t.Parallel()
	raw := `{"tasks":[{"title":"A","files":["internal/a.go","internal/a_test.go"],"test_changes":["internal/a_test.go"]}]}`
	ret, err := Parse(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("Parse(%s) = %v", raw, err)
	}
	tasks := planOf(ret).Tasks
	if len(tasks) != 1 {
		t.Fatalf("planOf tasks = %+v, want one", tasks)
	}
	if want := []string{"internal/a_test.go"}; !reflect.DeepEqual(tasks[0].TestChanges, want) {
		t.Errorf("task 1 test_changes = %q, want %q", tasks[0].TestChanges, want)
	}
}

func TestParseRefusesInvalidJSON(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
	}{
		{"truncated object", `{"status":`},
		{"empty input", ``},
		{"array instead of object", `[]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Parse(json.RawMessage(tc.raw)); err == nil {
				t.Errorf("Parse(%q) error = nil, want an error", tc.raw)
			}
		})
	}
}

// testItemTask is validTask with a test item whose test is the task's first file.
func testItemTask(title string, files ...string) Task {
	task := validTask(title, files...)
	task.Evidence.Kind, task.Evidence.Tests = evidence.KindTest, files[:1]
	return task
}

// TestCheckRefusesATestItemOnATaskOfTestFilesOnly: a test item's reproduction runs its tests at the base without the
// rest of the commit, so a task whose files are all test files, as when it only adds tests for what the base does,
// gets a test item refused. A test item with a file that is not a test file, and a capture item of test files only,
// pass.
func TestCheckRefusesATestItemOnATaskOfTestFilesOnly(t *testing.T) {
	t.Parallel()
	const reason = "test, but every file of the task is a test file. A test item's green first runs its tests at " +
		"the base with the rest of the commit left out, and they must fail there; with no other file in the change " +
		"that run is the commit itself, so it passes as green does and the task can never be met. Give a task that " +
		"only adds, rewrites or fixes tests kind capture with no tests, and a conclusion its base output cannot show"
	t.Run("a test item on Go, web and Kotlin test files only is refused", func(t *testing.T) {
		t.Parallel()
		ret := Return{Tasks: []Task{testItemTask("tests only", "internal/enginehost/callerless_mcp_test.go",
			"web/src/app.test.tsx", "android/app/src/test/kotlin/WordsTest.kt")}}
		checkLines(t, checkPlan(ret, nil), `tasks[0] "tests only" evidence.kind: `+reason)
	})
	t.Run("a test item on a task that also changes code passes", func(t *testing.T) {
		t.Parallel()
		ret := Return{Tasks: []Task{testItemTask("tests and code", "internal/x_test.go", "internal/x.go")}}
		checkLines(t, checkPlan(ret, nil))
	})
	t.Run("a capture item on test files only passes", func(t *testing.T) {
		t.Parallel()
		ret := Return{Tasks: []Task{validTask("capture", "internal/x_test.go")}}
		checkLines(t, checkPlan(ret, nil))
	})
}
