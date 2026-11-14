package planner

import (
	"encoding/json"
	"reflect"
	"testing"
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
		Check:        "a check for " + title,
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
		{"empty check", func(task *Task) { task.Check = "" }, `tasks[0] "T" check: empty`},
		{"blank check", func(task *Task) { task.Check = "\n" }, `tasks[0] "T" check: empty`},
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
	task.Check = ""
	task.Files = nil
	checkLines(t, Check(Return{Tasks: []Task{task}}, epicRequirements(), nil),
		`tasks[0] "" title: empty`,
		`tasks[0] "" files: no files`,
		`tasks[0] "" check: empty`,
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
