package planner

import (
	"slices"
	"testing"
)

// TestPlanOfCarriesDependsOn checks that planOf copies each task's depends_on into the plan it stores, in order: a T<n>,
// the exact title of another task of the return, and none, which the plan holds as nil or empty.
func TestPlanOfCarriesDependsOn(t *testing.T) {
	t.Parallel()
	ret := Return{Tasks: []Task{
		dependent("A", "internal/a.go", "T1"),
		dependent("B", "internal/b.go", "A"),
		validTask("C", "internal/c.go"),
	}}
	tasks := planOf(ret).Tasks
	if len(tasks) != 3 {
		t.Fatalf("planOf tasks = %+v, want three", tasks)
	}
	want := [][]string{{"T1"}, {"A"}, nil}
	for i, names := range want {
		if !slices.Equal(tasks[i].DependsOn, names) {
			t.Errorf("task %d DependsOn = %q, want %q", i+1, tasks[i].DependsOn, names)
		}
	}
}
