package planner

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// dependent returns validTask(title, file) with its depends_on naming names.
func dependent(title, file string, names ...string) Task {
	task := validTask(title, file)
	task.DependsOn = names
	return task
}

// epicTask returns an epic task numbered seq, with its status and the files it owns.
func epicTask(seq int64, status string, files ...string) store.EpicTask {
	return store.EpicTask{Seq: seq, Status: status, Files: files}
}

// TestParseDependsOn checks that Parse reads a task's depends_on, a JSON array of strings written as a string: an
// array is read, blank text and an empty array read as none, and text that is not such an array is left unread with
// its error.
func TestParseDependsOn(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		text      string
		want      []string
		wantError bool
	}{
		{name: "an array of strings", text: `["T1","Build the store"]`, want: []string{"T1", "Build the store"}},
		{name: "blank", text: "  "},
		{name: "an empty array", text: "[]"},
		{name: "malformed", text: `["T1"`, wantError: true},
		{name: "not an array", text: `"T1"`, wantError: true},
		{name: "null", text: "null", wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			task := parseTaskKey(t, "depends_on", tc.text)
			if !reflect.DeepEqual(task.DependsOn, tc.want) {
				t.Errorf("DependsOn = %q, want %q", task.DependsOn, tc.want)
			}
			if got := task.DependsOnError != ""; got != tc.wantError {
				t.Errorf("DependsOnError = %q, want an error: %v", task.DependsOnError, tc.wantError)
			}
		})
	}
}

// TestParseRefusesDependsOnThatIsNotAString checks that a depends_on written as an array, not as a string, is a Parse
// error for the whole return, as an unresolvable or a test_changes of the wrong type is.
func TestParseRefusesDependsOnThatIsNotAString(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"tasks":[{"title":"B","depends_on":["T1"]}]}`)
	if _, err := Parse(raw); err == nil {
		t.Errorf("Parse(%s) error = nil, want a refusal of a depends_on that is not a string", raw)
	}
}

// TestCheckDependsOnAccepts checks that a T<n> naming an open or a done epic task, and the exact title of a task of the
// same return, give no refusal.
func TestCheckDependsOnAccepts(t *testing.T) {
	t.Parallel()
	tasks := []store.EpicTask{
		epicTask(1, taskOpen, "internal/store/epics.go"),
		epicTask(2, "done", "internal/kinds/kinds.go"),
	}
	ret := Return{Tasks: []Task{
		dependent("A", "internal/a.go", "T1", "T2"),
		dependent("B", "internal/b.go", "A"),
	}}
	checkLines(t, checkTasks(ret, tasks))
}

// TestCheckDependsOnRefuses checks that each depends_on entry Check cannot resolve gives its line: a T<n> naming no
// epic task, a name that is neither a T<n> nor the title of a task of the return, a title two tasks of the return
// share, a T<n> of a failed task or of a gap, and an entry that is part of a cycle of depends_on, a self-reference
// included.
func TestCheckDependsOnRefuses(t *testing.T) {
	t.Parallel()
	tasks := []store.EpicTask{
		epicTask(1, taskOpen),
		epicTask(2, taskFailed),
		epicTask(3, store.TaskGap),
	}
	cases := []struct {
		name string
		ret  Return
		want []string
	}{
		{
			name: "an unknown T<n>",
			ret:  Return{Tasks: []Task{dependent("B", "internal/b.go", "T9")}},
			want: []string{`tasks[0] "B" depends_on[0]: names no task of the epic`},
		},
		{
			name: "an unknown title",
			ret:  Return{Tasks: []Task{dependent("B", "internal/b.go", "Nowhere")}},
			want: []string{
				`tasks[0] "B" depends_on[0]: names neither a T<n> nor the exact title of a task of the return`,
			},
		},
		{
			name: "a shared title",
			ret: Return{Tasks: []Task{
				validTask("A", "internal/a.go"),
				validTask("A", "internal/c.go"),
				dependent("B", "internal/b.go", "A"),
			}},
			want: []string{`tasks[2] "B" depends_on[0]: names a title two tasks of the return share`},
		},
		{
			name: "a failed task",
			ret:  Return{Tasks: []Task{dependent("B", "internal/b.go", "T2")}},
			want: []string{`tasks[0] "B" depends_on[0]: names a failed task`},
		},
		{
			name: "a gap task",
			ret:  Return{Tasks: []Task{dependent("B", "internal/b.go", "T3")}},
			want: []string{`tasks[0] "B" depends_on[0]: names a gap task`},
		},
		{
			name: "a two-task cycle",
			ret: Return{Tasks: []Task{
				dependent("A", "internal/a.go", "B"),
				dependent("B", "internal/b.go", "A"),
			}},
			want: []string{
				`tasks[0] "A" depends_on[0]: is part of a cycle of depends_on`,
				`tasks[1] "B" depends_on[0]: is part of a cycle of depends_on`,
			},
		},
		{
			name: "a self-reference",
			ret:  Return{Tasks: []Task{dependent("A", "internal/a.go", "A")}},
			want: []string{`tasks[0] "A" depends_on[0]: is part of a cycle of depends_on`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkLines(t, checkTasks(tc.ret, tasks), tc.want...)
		})
	}
}

// TestCheckDependsOnSharedFiles checks that a file two tasks of the return name, or that an open epic task owns, passes
// when depends_on orders the tasks or names the open task by its T<n>, or reaches it through a return task.
func TestCheckDependsOnSharedFiles(t *testing.T) {
	t.Parallel()
	tasks := []store.EpicTask{epicTask(1, taskOpen, "internal/store/epics.go")}
	cases := []struct {
		name string
		ret  Return
	}{
		{
			name: "a dependent names its dependency's file",
			ret: Return{Tasks: []Task{
				validTask("A", "internal/a.go"),
				dependent("B", "internal/a.go", "A"),
			}},
		},
		{
			name: "a dependent names the file of an open task it names by T<n>",
			ret:  Return{Tasks: []Task{dependent("B", "internal/store/epics.go", "T1")}},
		},
		{
			name: "a dependent reaches an open task through a return task",
			ret: Return{Tasks: []Task{
				dependent("A", "internal/a.go", "T1"),
				dependent("B", "internal/store/epics.go", "A"),
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkLines(t, checkTasks(tc.ret, tasks))
		})
	}
}

// TestCheckDependsOnRefusesSplitTask checks that a T<n> of a split task gives the split refusal, while a T<n> of an open
// task in the same return passes.
func TestCheckDependsOnRefusesSplitTask(t *testing.T) {
	t.Parallel()
	tasks := []store.EpicTask{
		epicTask(1, taskOpen),
		epicTask(2, store.TaskSplit),
	}
	ret := Return{Tasks: []Task{dependent("B", "internal/b.go", "T1", "T2")}}
	checkLines(t, checkTasks(ret, tasks), `tasks[0] "B" depends_on[1]: names a split task; name its descendants`)
}
