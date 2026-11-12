package server

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeEffortState writes a synthetic efforts/<effort>/state.json under dir.
func writeEffortState(t *testing.T, dir, effort, body string) {
	t.Helper()
	effortDir := filepath.Join(dir, "efforts", effort)
	if err := os.MkdirAll(effortDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(effortDir, "state.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadTasksOrdersByEffortNumberThenFileOrder(t *testing.T) {
	dir := t.TempDir()
	writeEffortState(t, dir, "10", `{"tasks":[{"slug":"ten-a","subject":"Ten A","status":"done"}]}`)
	writeEffortState(t, dir, "2", `{"tasks":[
		{"slug":"two-b","subject":"Two B","status":"pending"},
		{"slug":"two-a","subject":"Two A","status":"gated"}
	]}`)
	writeEffortState(t, dir, "1", `{"tasks":[{"slug":"one-a","subject":"One A","status":"dispatched"}]}`)

	got, problems, err := readTasks(dir)
	if err != nil {
		t.Fatalf("readTasks: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
	want := []taskRecord{
		{Effort: 1, Slug: "one-a", Subject: "One A", Status: "dispatched"},
		{Effort: 2, Slug: "two-b", Subject: "Two B", Status: "pending"},
		{Effort: 2, Slug: "two-a", Subject: "Two A", Status: "gated"},
		{Effort: 10, Slug: "ten-a", Subject: "Ten A", Status: "done"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tasks = %+v, want %+v", got, want)
	}
}

func TestReadTasksKeepsStatusAsWritten(t *testing.T) {
	dir := t.TempDir()
	writeEffortState(t, dir, "1", `{"tasks":[
		{"slug":"a","subject":"A","status":"Pending "},
		{"slug":"b","subject":"B","status":{ "phase": "gated", "round": 2 }},
		{"slug":"c","subject":"C","status":3},
		{"slug":"d","subject":"D"}
	]}`)

	got, problems, err := readTasks(dir)
	if err != nil {
		t.Fatalf("readTasks: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
	wantStatus := []string{"Pending ", `{"phase":"gated","round":2}`, "3", ""}
	if len(got) != len(wantStatus) {
		t.Fatalf("got %d tasks, want %d: %+v", len(got), len(wantStatus), got)
	}
	for i, task := range got {
		if task.Status != wantStatus[i] {
			t.Errorf("task %d status = %q, want %q", i, task.Status, wantStatus[i])
		}
	}
}

func TestReadTasksSkipsNonEffortEntries(t *testing.T) {
	dir := t.TempDir()
	writeEffortState(t, dir, "1", `{"tasks":[{"slug":"a","subject":"A","status":"done"}]}`)
	efforts := filepath.Join(dir, "efforts")
	if err := os.WriteFile(filepath.Join(efforts, "x.log"), []byte("log"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory named notes holding a state.json, a directory whose name is not all digits, and an effort
	// directory without state.json are not efforts.
	writeEffortState(t, dir, "notes", `{"tasks":[{"slug":"hidden","subject":"H","status":"done"}]}`)
	writeEffortState(t, dir, "x1", `{"tasks":[{"slug":"hidden2","subject":"H","status":"done"}]}`)
	if err := os.MkdirAll(filepath.Join(efforts, "3"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, problems, err := readTasks(dir)
	if err != nil {
		t.Fatalf("readTasks: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
	want := []taskRecord{{Effort: 1, Slug: "a", Subject: "A", Status: "done"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tasks = %+v, want %+v", got, want)
	}
}

func TestReadTasksReportsUnreadableEffortsAndKeepsOthers(t *testing.T) {
	dir := t.TempDir()
	writeEffortState(t, dir, "1", `{"tasks":[{"slug":"one","subject":"One","status":"done"}]}`)
	writeEffortState(t, dir, "2", `{"tasks":[`)
	writeEffortState(t, dir, "3", `{"tasks":"pending"}`)
	writeEffortState(t, dir, "4", `{"tasks":[{"slug":"four","subject":"Four","status":"gap"}]}`)

	got, problems, err := readTasks(dir)
	if err != nil {
		t.Fatalf("readTasks: %v", err)
	}
	wantProblems := []string{
		"efforts/2/state.json: does not parse",
		"efforts/3/state.json: does not parse",
	}
	if !reflect.DeepEqual(problems, wantProblems) {
		t.Fatalf("problems = %v, want %v", problems, wantProblems)
	}
	want := []taskRecord{
		{Effort: 1, Slug: "one", Subject: "One", Status: "done"},
		{Effort: 4, Slug: "four", Subject: "Four", Status: "gap"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tasks = %+v, want %+v", got, want)
	}
}

func TestReadTasksWithoutEffortsDirectory(t *testing.T) {
	for _, dir := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		got, problems, err := readTasks(dir)
		if err != nil {
			t.Fatalf("readTasks(%s): %v", dir, err)
		}
		if len(got) != 0 || len(problems) != 0 {
			t.Fatalf("readTasks(%s) = %+v, %v; want nothing", dir, got, problems)
		}
	}
}
