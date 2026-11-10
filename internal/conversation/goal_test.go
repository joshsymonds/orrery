package conversation

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// goalAttachmentLine wraps an attachment value in a transcript attachment line.
func goalAttachmentLine(attachment string) string {
	return `{"type":"attachment","attachment":` + attachment + `,"timestamp":"2026-11-10T09:00:00Z"}`
}

// setGoal returns the record written when a goal is set.
func setGoal(condition string) string {
	return goalAttachmentLine(`{"type":"goal_status","condition":"` + condition + `","met":false,"sentinel":true}`)
}

// checkGoal returns the record written when the goal is checked and not yet met.
func checkGoal(condition string) string {
	return goalAttachmentLine(`{"type":"goal_status","condition":"` + condition + `","met":false,"reason":"not yet"}`)
}

// metGoal returns the record written when the goal is met.
func metGoal(condition string) string {
	return goalAttachmentLine(`{"type":"goal_status","condition":"` + condition +
		`","met":true,"reason":"done","iterations":3,"durationMs":1000,"tokens":500}`)
}

// clearedGoal returns the record written when the goal is cleared.
func clearedGoal(condition string) string {
	return goalAttachmentLine(`{"type":"goal_status","condition":"` + condition + `","met":true,"sentinel":true}`)
}

// userLine is a transcript line that holds no goal.
const userLine = `{"type":"user","message":{"content":"synthetic prompt"}}`

// assertGoal fails the test unless goals reports want for path.
func assertGoal(t *testing.T, goals *Goals, path, want string) {
	t.Helper()
	got, err := goals.Goal(path)
	if err != nil {
		t.Fatalf("Goal: %v", err)
	}
	if got != want {
		t.Fatalf("Goal = %q, want %q", got, want)
	}
}

// writeFile replaces the file at path with data.
func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

// appendText appends data to the file at path.
func appendText(t *testing.T, path, data string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := file.WriteString(data); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close after append: %v", err)
	}
}

// overwriteInPlace replaces the bytes old in the file at path with replacement, at the same offset.
func overwriteInPlace(t *testing.T, path, old, replacement string) {
	t.Helper()
	if len(old) != len(replacement) {
		t.Fatalf("overwrite lengths differ: %d and %d", len(old), len(replacement))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	at := bytes.Index(data, []byte(old))
	if at < 0 {
		t.Fatalf("%q not found in file", old)
	}
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open for overwrite: %v", err)
	}
	if _, err := file.WriteAt([]byte(replacement), int64(at)); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close after overwrite: %v", err)
	}
}

func TestGoalFollowsNewestGoalRecord(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  string
	}{
		{name: "set", lines: []string{setGoal("ship the parser")}, want: "ship the parser"},
		{
			name:  "checked",
			lines: []string{setGoal("ship the parser"), checkGoal("ship the parser")},
			want:  "ship the parser",
		},
		{name: "met", lines: []string{setGoal("ship the parser"), metGoal("ship the parser")}, want: ""},
		{name: "cleared", lines: []string{setGoal("ship the parser"), clearedGoal("ship the parser")}, want: ""},
		{
			name:  "set again after met",
			lines: []string{setGoal("first"), metGoal("first"), setGoal("second")},
			want:  "second",
		},
		{
			name:  "set again after cleared",
			lines: []string{setGoal("first"), clearedGoal("first"), setGoal("second")},
			want:  "second",
		},
		{
			name:  "resumed",
			lines: []string{setGoal("first"), checkGoal("first"), setGoal("first")},
			want:  "first",
		},
		{name: "no goal", lines: []string{userLine}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTranscript(t, tt.lines...)
			assertGoal(t, NewGoals(), path, tt.want)
		})
	}
}

func TestGoalCapsConditionAt300Runes(t *testing.T) {
	condition := strings.Repeat("é", 350)
	path := writeTranscript(t, setGoal(condition))
	want := strings.Repeat("é", 300)
	got, err := NewGoals().Goal(path)
	if err != nil {
		t.Fatalf("Goal: %v", err)
	}
	if got != want {
		t.Fatalf("Goal has %d runes, want %d runes of the condition", utf8.RuneCountInString(got), 300)
	}
}

func TestGoalIgnoresNonGoalLinesMentioningGoalStatus(t *testing.T) {
	path := writeTranscript(t,
		setGoal("active"),
		`{"type":"user","message":{"content":"the goal_status field is {\"type\":\"goal_status\",`+
			`\"condition\":\"other\",\"met\":true}"}}`,
		goalAttachmentLine(`{"type":"queued_command","prompt":"goal_status {\"type\":\"goal_status\",`+
			`\"condition\":\"other\",\"met\":true}"}`),
		`{"type":"user","attachment":{"type":"goal_status","condition":"other","met":true}}`,
	)
	assertGoal(t, NewGoals(), path, "active")
}

func TestGoalFindsRecordEightMiBBeforeEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	filler := `{"type":"user","message":{"content":"` + strings.Repeat("x", 64<<10) + `"}}` + "\n"
	var data strings.Builder
	data.WriteString(setGoal("far goal") + "\n")
	for range 136 {
		data.WriteString(filler)
	}
	writeFile(t, path, data.String())
	assertGoal(t, NewGoals(), path, "far goal")
}

func TestGoalReadsOnlyAppendedBytesAfterFirstCall(t *testing.T) {
	path := writeTranscript(t, setGoal("first goal"))
	goals := NewGoals()
	assertGoal(t, goals, path, "first goal")

	overwriteInPlace(t, path, "first goal", "XXXXXXXXXX")
	appendText(t, path, userLine+"\n")
	assertGoal(t, goals, path, "first goal")
	assertGoal(t, NewGoals(), path, "XXXXXXXXXX")

	appendText(t, path, setGoal("second goal")+"\n")
	assertGoal(t, goals, path, "second goal")
}

func TestGoalCountsTrailingLineOnceComplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	writeFile(t, path, setGoal("ship it"))
	goals := NewGoals()
	assertGoal(t, goals, path, "")

	appendText(t, path, "\n")
	assertGoal(t, goals, path, "ship it")

	met := metGoal("ship it")
	half := len(met) / 2
	appendText(t, path, met[:half])
	assertGoal(t, goals, path, "ship it")

	appendText(t, path, met[half:]+"\n")
	assertGoal(t, goals, path, "")
}

func TestGoalRescansShrunkFile(t *testing.T) {
	path := writeTranscript(t, setGoal("a much longer first goal"))
	goals := NewGoals()
	assertGoal(t, goals, path, "a much longer first goal")

	writeFile(t, path, setGoal("B")+"\n")
	assertGoal(t, goals, path, "B")
}

func TestGoalRescansReplacedFile(t *testing.T) {
	path := writeTranscript(t, setGoal("first goal"))
	goals := NewGoals()
	assertGoal(t, goals, path, "first goal")

	replacement := filepath.Join(filepath.Dir(path), "replacement.jsonl")
	writeFile(t, replacement, setGoal("replacement goal")+"\n")
	if err := os.Rename(replacement, path); err != nil {
		t.Fatalf("rename replacement: %v", err)
	}
	assertGoal(t, goals, path, "replacement goal")
}

func TestGoalMissingFileWrapsErrNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.jsonl")
	got, err := NewGoals().Goal(path)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Goal error = %v, want one wrapping fs.ErrNotExist", err)
	}
	if got != "" {
		t.Fatalf("Goal = %q on error, want empty", got)
	}
}
