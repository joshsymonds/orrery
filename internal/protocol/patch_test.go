package protocol_test

import (
	"encoding/json"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
)

func TestPatchLineCounts(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantAdded   int64
		wantRemoved int64
	}{
		{
			name: "edit patch with two hunks",
			input: `{"filePath":"/x/a.go","oldString":"old one","newString":"new one","originalFile":"x",` +
				`"structuredPatch":[` +
				`{"oldStart":1,"oldLines":3,"newStart":1,"newLines":3,"lines":[" package a","-old one","+new one"," end"]},` +
				`{"oldStart":10,"oldLines":2,"newStart":10,"newLines":3,"lines":[" x","-y","+z","+w"]}` +
				`],"userModified":false}`,
			wantAdded:   3,
			wantRemoved: 2,
		},
		{
			name: "MultiEdit-shaped patch",
			input: `{"filePath":"/x/b.go","edits":[{"old_string":"a","new_string":"c"},{"old_string":"d","new_string":"f"}],` +
				`"structuredPatch":[{"oldStart":5,"oldLines":4,"newStart":5,"newLines":4,"lines":["-a","-b","+c"," mid","-d","+e"]}]}`,
			wantAdded:   2,
			wantRemoved: 3,
		},
		{
			name:        "create content ending in newline",
			input:       `{"type":"create","filePath":"/x/c.go","content":"package c\n\nfunc C() {}\n","structuredPatch":[]}`,
			wantAdded:   3,
			wantRemoved: 0,
		},
		{
			name:        "create content without final newline",
			input:       `{"type":"create","filePath":"/x/c.go","content":"package c\n\nfunc C() {}","structuredPatch":[]}`,
			wantAdded:   3,
			wantRemoved: 0,
		},
		{
			name:        "empty create",
			input:       `{"type":"create","filePath":"/x/d.go","content":"","structuredPatch":[]}`,
			wantAdded:   0,
			wantRemoved: 0,
		},
		{
			name:        "string error result",
			input:       `"Error: old_string not found in file"`,
			wantAdded:   0,
			wantRemoved: 0,
		},
		{
			name:        "Bash result",
			input:       `{"stdout":"ok\n+1 line\n","stderr":"","interrupted":false,"isImage":false}`,
			wantAdded:   0,
			wantRemoved: 0,
		},
		{
			name:        "Read result",
			input:       `{"type":"text","file":{"filePath":"/x/e.go","content":"a\nb\n","numLines":2,"startLine":1,"totalLines":2}}`,
			wantAdded:   0,
			wantRemoved: 0,
		},
		{
			name:        "malformed JSON",
			input:       `{"type":"create","content":"a\n"`,
			wantAdded:   0,
			wantRemoved: 0,
		},
		{
			name:        "JSON null",
			input:       `null`,
			wantAdded:   0,
			wantRemoved: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			added, removed := protocol.PatchLineCounts(json.RawMessage(tt.input))
			if added != tt.wantAdded || removed != tt.wantRemoved {
				t.Fatalf("PatchLineCounts() = (%d, %d), want (%d, %d)", added, removed, tt.wantAdded, tt.wantRemoved)
			}
		})
	}
}
