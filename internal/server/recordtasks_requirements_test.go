package server

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestEpicsGetRecordTaskGivesItsRequirementIDs(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "repoa", "record")
	writeHead(t, root, "repoa", "record", `{"outcome":"shipped"}`, epicsNow())
	writeEffortState(t, dir, "1", `{"tasks":[
		{"slug":"one-a","subject":"One A","status":"done","requirement":"R1, R4"},
		{"slug":"one-b","subject":"One B","status":"done"}
	]}`)
	client := epicStoreServe(t, root, &epicsFake{}, &epicStoreFake{})

	tasks := epicsGet(t, client, "repoa/record").GetTasks()
	if len(tasks) != 2 || tasks[0].GetSlug() != "one-a" || tasks[1].GetSlug() != "one-b" {
		t.Fatalf("tasks = %v, want one-a then one-b", tasks)
	}
	if got := tasks[0].GetRequirements(); !reflect.DeepEqual(got, []string{"R1", "R4"}) {
		t.Errorf("one-a requirements = %q, want [R1 R4]", got)
	}
	if got := tasks[1].GetRequirements(); len(got) != 0 {
		t.Errorf("one-b requirements = %q, want none for a task with no requirement", got)
	}
}

func TestReadTasksSplitsRequirementText(t *testing.T) {
	dir := t.TempDir()
	writeEffortState(t, dir, "1", `{"tasks":[
		{"slug":"a","requirement":"R1, R4"},
		{"slug":"b","requirement":" R2 ,, R3 ,"},
		{"slug":"c","requirement":""},
		{"slug":"d","requirement":" , "},
		{"slug":"e","requirement":7},
		{"slug":"f","requirement":["R5"]},
		{"slug":"g","requirement":null},
		{"slug":"h"}
	]}`)

	got, problems, err := readTasks(dir, newHeadCache())
	if err != nil {
		t.Fatalf("readTasks: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
	want := [][]string{{"R1", "R4"}, {"R2", "R3"}, nil, nil, nil, nil, nil, nil}
	if len(got) != len(want) {
		t.Fatalf("got %d tasks, want %d: %+v", len(got), len(want), got)
	}
	for i, task := range got {
		if len(task.Requirements) == 0 && len(want[i]) == 0 {
			continue
		}
		if !reflect.DeepEqual(task.Requirements, want[i]) {
			t.Errorf("task %d requirements = %q, want %q", i, task.Requirements, want[i])
		}
	}
}
