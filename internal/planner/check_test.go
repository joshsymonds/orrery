package planner

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
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
			checkLines(t, Check(ret, epicRequirements(), nil), `tasks[0] "T" files[0]: `+reason)
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
			checkLines(t, Check(ret, epicRequirements(), nil), `tasks[0] "T" files[0]: `+reason)
		})
	}
}

func TestCheckAcceptsGitNamedFilesOutsideGitDirectory(t *testing.T) {
	t.Parallel()
	ret := Return{Tasks: []Task{validTask("T", ".gitignore", "docs/.github/x.md", "internal/git/run.go")}}
	checkLines(t, Check(ret, epicRequirements(), nil))
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
			checkLines(t, Check(tc.ret, epicRequirements(), nil), tc.want)
		})
	}
}

func TestCheckRefusesFileOwnedByUnfinishedTask(t *testing.T) {
	t.Parallel()
	ret := Return{Tasks: []Task{validTask("T", "internal/store/epics.go")}}
	owned := []string{"internal/store/epics.go"}
	checkLines(t, Check(ret, epicRequirements(), owned),
		`tasks[0] "T" files[0]: owned by an unfinished task`)
}

func TestCheckRefusesUnknownRequirement(t *testing.T) {
	t.Parallel()
	task := validTask("T", "internal/a.go")
	task.Requirements = []string{"R1", "R9"}
	checkLines(t, Check(Return{Tasks: []Task{task}}, epicRequirements(), nil),
		`tasks[0] "T" requirements[1]: not a Requirement of the epic`)
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
			checkLines(t, Check(Return{Tasks: []Task{task}}, epicRequirements(), nil), tc.want)
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
			evidence: evidence.Item{Conclusion: "c", Judge: "j"},
			want:     []string{`tasks[0] "T" evidence.capture: none, name a capture or supplied files`},
		},
		{
			name:     "blank conclusion",
			evidence: evidence.Item{Capture: []string{"scripts/check"}, Limit: 60, Conclusion: " ", Judge: "j"},
			want:     []string{`tasks[0] "T" evidence.conclusion: empty or blank`},
		},
		{
			name:     "blank judge",
			evidence: evidence.Item{Capture: []string{"scripts/check"}, Limit: 60, Conclusion: "c"},
			want:     []string{`tasks[0] "T" evidence.judge: empty or blank`},
		},
		{
			name:     "capture program without a slash",
			evidence: evidence.Item{Capture: []string{"check"}, Limit: 60, Conclusion: "c", Judge: "j"},
			want:     []string{`tasks[0] "T" evidence.capture[0]: not a path with a / in it`},
		},
		{
			name:     "limit outside the range beside a capture",
			evidence: evidence.Item{Capture: []string{"scripts/check"}, Conclusion: "c", Judge: "j"},
			want:     []string{`tasks[0] "T" evidence.limit: 0 is outside 1 to 9223372036 seconds`},
		},
		{
			name: "artifact path outside the repository",
			evidence: evidence.Item{
				Capture: []string{"scripts/check"}, Limit: 60, Artifacts: []string{"../out"},
				Conclusion: "c", Judge: "j",
			},
			want: []string{`tasks[0] "T" evidence.artifacts[0]: not a normalized repository-relative file path`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			task := validTask("T", "internal/a.go")
			task.Evidence = tc.evidence
			checkLines(t, Check(Return{Tasks: []Task{task}}, epicRequirements(), nil), tc.want...)
		})
	}
}

func TestCheckRefusesTaskWithNoFiles(t *testing.T) {
	t.Parallel()
	task := validTask("T")
	task.Files = nil
	checkLines(t, Check(Return{Tasks: []Task{task}}, epicRequirements(), nil),
		`tasks[0] "T" files: no files`)
}

func TestCheckRefusesEachBadFieldOnce(t *testing.T) {
	t.Parallel()
	task := validTask("T")
	task.Title = ""
	task.Gate = ""
	task.Files = nil
	checkLines(t, Check(Return{Tasks: []Task{task}}, epicRequirements(), nil),
		`tasks[0] "" title: empty`,
		`tasks[0] "" files: no files`,
		`tasks[0] "" gate: empty`,
	)
}

func TestCheckWritesTitleBetweenQuotesAsGiven(t *testing.T) {
	t.Parallel()
	task := validTask(`Add "x" flag`, "internal/a.go")
	task.Deliverable = ""
	checkLines(t, Check(Return{Tasks: []Task{task}}, epicRequirements(), nil),
		`tasks[0] "Add "x" flag" deliverable: empty`)
}

func TestCheckPassesValidPlanWithNewFiles(t *testing.T) {
	t.Parallel()
	ret := Return{Tasks: []Task{
		validTask("A", "internal/planner/plan.go", "internal/planner/check.go"),
		validTask("B", "internal/planner/brand_new.go"),
	}}
	ret.Tasks[1].Requirements = []string{"R1", "R2"}
	checkLines(t, Check(ret, epicRequirements(), []string{"internal/store/epics.go"}))
}

func TestParseRoundTripsValidReturn(t *testing.T) {
	t.Parallel()
	want := Return{
		Status:    "done",
		Decisions: []string{"split the store work from the planner"},
		Tasks:     []Task{validTask("A", "internal/planner/plan.go")},
		Outline:   "one task",
		Questions: []string{"which epic?"},
		Outcome:   "work",
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
	Type                 string                `json:"type"`
	Items                *schemaNode           `json:"items"`
	Properties           map[string]schemaNode `json:"properties"`
	Required             []string              `json:"required"`
	AdditionalProperties *bool                 `json:"additionalProperties"`
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
	wantTask := []string{"title", "deliverable", "files", "requirements", "gate", "evidence"}
	if !reflect.DeepEqual(task.Required, wantTask) {
		t.Errorf("task required = %q, want %q", task.Required, wantTask)
	}
	if _, ok := task.Properties["check"]; ok {
		t.Errorf("task still has a check property")
	}
	if got := task.Properties["gate"].Type; got != "string" {
		t.Errorf("gate type = %q, want string", got)
	}

	ev := task.Properties["evidence"]
	wantEvidence := []string{"capture", "limit", "artifacts", "supplied", "conclusion", "judge"}
	if !reflect.DeepEqual(ev.Required, wantEvidence) {
		t.Errorf("evidence required = %q, want %q", ev.Required, wantEvidence)
	}
	if len(ev.Properties) != len(wantEvidence) {
		t.Errorf("evidence has %d properties, want %d", len(ev.Properties), len(wantEvidence))
	}
	for _, name := range wantEvidence {
		if _, ok := ev.Properties[name]; !ok {
			t.Errorf("evidence lacks property %q", name)
		}
	}
	if ev.AdditionalProperties == nil || *ev.AdditionalProperties {
		t.Errorf("evidence additionalProperties = %v, want false", ev.AdditionalProperties)
	}
	if got := ev.Properties["limit"].Type; got != "integer" {
		t.Errorf("evidence limit type = %q, want integer", got)
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
