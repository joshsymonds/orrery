package enginehost

import (
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// startLabelled starts a writing step in a fresh linked worktree of repo with the given labels and returns its id.
func startLabelled(t *testing.T, f *stepStartFixture, repo, epic, task string) string {
	t.Helper()
	start := stepStartWriting(stepWorktreeTestAddLinked(t, repo))
	start.Epic = epic
	start.Task = task
	id, err := f.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start epic %q task %q: %v", epic, task, err)
	}
	return id
}

// labeledIDs returns the ids of rows, sorted, so a list can be compared as a set.
func labeledIDs(rows []store.StepRow) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	slices.Sort(ids)
	return ids
}

// assertOldestFirst fails the test unless rows are ordered by start time, then id.
func assertOldestFirst(t *testing.T, rows []store.StepRow) {
	t.Helper()
	for i := 1; i < len(rows); i++ {
		prev, cur := rows[i-1], rows[i]
		if cur.Started.Before(prev.Started) || (cur.Started.Equal(prev.Started) && cur.ID < prev.ID) {
			t.Fatalf("rows %d and %d are out of oldest-first order: %s then %s", i-1, i, prev.ID, cur.ID)
		}
	}
}

func TestStepsStartStoresLabelsAndStatusAndListReadThem(t *testing.T) {
	f := newStepStartFixture(t)
	repo := stepGitTestRepo(t)

	id := startLabelled(t, f, repo, "repo/epic-one", "task-a")

	row, err := f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if row.Epic != "repo/epic-one" || row.Task != "task-a" {
		t.Fatalf("Status labels = epic %q task %q, want epic %q task %q", row.Epic, row.Task, "repo/epic-one", "task-a")
	}

	rows, err := f.steps.List(t.Context(), "repo/epic-one", "task-a")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got, want := labeledIDs(rows), []string{id}; !slices.Equal(got, want) {
		t.Fatalf("List ids = %v, want %v", got, want)
	}
	if rows[0].Epic != "repo/epic-one" || rows[0].Task != "task-a" {
		t.Fatalf("List labels = epic %q task %q, want epic %q task %q",
			rows[0].Epic, rows[0].Task, "repo/epic-one", "task-a")
	}
}

func TestStepsListLeavesOutUnlabeledAndNarrowsByLabel(t *testing.T) {
	f := newStepStartFixture(t)
	repo := stepGitTestRepo(t)

	stepA := startLabelled(t, f, repo, "repo/epic-one", "task-a")
	unlabeled := startLabelled(t, f, repo, "", "")
	taskOnly := startLabelled(t, f, repo, "", "task-a")
	stepC := startLabelled(t, f, repo, "repo/epic-one", "task-b")
	stepD := startLabelled(t, f, repo, "repo/epic-two", "task-a")

	cases := []struct {
		name string
		epic string
		task string
		want []string
	}{
		{name: "no filter lists every labeled step only", want: []string{stepA, stepC, stepD}},
		{name: "epic narrows", epic: "repo/epic-one", want: []string{stepA, stepC}},
		{name: "epic and task narrow", epic: "repo/epic-one", task: "task-b", want: []string{stepC}},
		{name: "task alone narrows labeled steps", task: "task-a", want: []string{stepA, stepD}},
		{name: "no match is empty", epic: "repo/epic-two", task: "task-b", want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := f.steps.List(t.Context(), tc.epic, tc.task)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			want := slices.Clone(tc.want)
			slices.Sort(want)
			if got := labeledIDs(rows); !slices.Equal(got, want) {
				t.Fatalf("List(%q, %q) ids = %v, want %v", tc.epic, tc.task, got, want)
			}
			assertOldestFirst(t, rows)
		})
	}

	all, err := f.steps.List(t.Context(), "", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, row := range all {
		if row.ID == unlabeled || row.ID == taskOnly {
			t.Fatalf("List returned unlabeled step %s with epic %q task %q", row.ID, row.Epic, row.Task)
		}
	}
}
