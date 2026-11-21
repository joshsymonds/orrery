package planner

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// parseTaskKey parses a return of one task, titled B, whose key holds value, a JSON value of its own, and returns that
// task. A Go string value is written as a JSON string.
func parseTaskKey(t *testing.T, key string, value any) Task {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"tasks": []map[string]any{{"title": "B", key: value}}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	ret, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(%s) error = %v", raw, err)
	}
	return ret.Tasks[0]
}

// TestParseEvidenceString checks that a task's evidence string holding a whole evidence object reads the same Item as
// the object form. A string that is not such an object, or that holds a key an evidence item lacks, leaves the evidence
// empty and sets EvidenceError. A blank string reads as an empty item with no error.
func TestParseEvidenceString(t *testing.T) {
	t.Parallel()
	item := validTask("B", "internal/a.go").Evidence
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	objectForm := parseTaskKey(t, "evidence", item)
	stringForm := parseTaskKey(t, "evidence", string(encoded))
	if !reflect.DeepEqual(stringForm.Evidence, objectForm.Evidence) || stringForm.EvidenceError != "" {
		t.Errorf("evidence string read as %+v (error %q), want the object form's %+v",
			stringForm.Evidence, stringForm.EvidenceError, objectForm.Evidence)
	}
	cases := []struct {
		name      string
		value     string
		wantError bool
	}{
		{name: "not JSON", value: "not json", wantError: true},
		{name: "a key an evidence item lacks", value: `{"kind":"capture","bogus":true}`, wantError: true},
		{name: "blank", value: "  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			task := parseTaskKey(t, "evidence", tc.value)
			if !reflect.DeepEqual(task.Evidence, evidence.Item{}) {
				t.Errorf("Evidence = %+v, want an empty item", task.Evidence)
			}
			if got := task.EvidenceError != ""; got != tc.wantError {
				t.Errorf("EvidenceError = %q, want an error: %v", task.EvidenceError, tc.wantError)
			}
		})
	}
}

// TestCheckEvidenceString checks that a task whose evidence string did not read is refused on its evidence line, which
// gives the reason the string did not read.
func TestCheckEvidenceString(t *testing.T) {
	t.Parallel()
	task := parseTaskKey(t, "evidence", "not json")
	if task.EvidenceError == "" {
		t.Fatalf("EvidenceError is empty, want the reason the evidence string did not read")
	}
	want := `tasks[0] "B" evidence: not a JSON object: ` + task.EvidenceError
	if got := checkPlan(Return{Tasks: []Task{task}}, nil); !slices.Contains(got, want) {
		t.Errorf("Check() = %q, want it to give %q", got, want)
	}
}
