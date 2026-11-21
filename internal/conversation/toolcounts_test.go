package conversation

import (
	"reflect"
	"testing"
)

// countsCallLine returns an assistant line that calls tool with the JSON input, under the id.
func countsCallLine(id, tool, input string) string {
	return `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"` + id + `","name":"` + tool +
		`","input":` + input + `}]}}`
}

// countsResultLine returns a user line that answers the call id with content, and carries toolUseResult as its
// top-level toolUseResult.
func countsResultLine(id, content, toolUseResult string) string {
	return `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"` + id + `","content":"` +
		content + `"}]},"toolUseResult":` + toolUseResult + `}`
}

// The control edit adds one line and removes one. Each test adds it to its transcript, so the test also shows that
// counts are read, and a result that carries none is not a failure of the reader as a whole.
const (
	countsControlCall = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_ok",` +
		`"name":"Edit","input":{"file_path":"/x/ok.go"}}]}}`
	countsControlResult = `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_ok",` +
		`"content":"edited"}]},"toolUseResult":{"type":"update","filePath":"/x/ok.go","structuredPatch":[` +
		`{"oldStart":1,"oldLines":1,"newStart":1,"newLines":1,"lines":["-was","+now"]}]}}`
)

// countsControlItem is the result item of the control edit.
var countsControlItem = Item{Kind: kindResult, Text: "edited", ID: "toolu_ok", Added: 1, Removed: 1}

// writeCountsTranscript writes lines followed by the control edit to a new transcript and returns its path.
func writeCountsTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	return writeTranscript(t, append(lines, countsControlCall, countsControlResult)...)
}

// resultItems reads the transcript at path and returns its result items, oldest first.
func resultItems(t *testing.T, path string) []Item {
	t.Helper()
	var results []Item
	for _, item := range mustRead(t, path, 100).Items {
		if item.Kind == kindResult {
			results = append(results, item)
		}
	}
	return results
}

func TestReadToolLineCountsPatchResults(t *testing.T) {
	tests := []struct {
		name          string
		tool          string
		input         string
		toolUseResult string
		added         int64
		removed       int64
	}{
		{
			name:          "Edit",
			tool:          "Edit",
			input:         `{"file_path":"/x/a.go","old_string":"old one","new_string":"new one"}`,
			toolUseResult: `{"type":"update","filePath":"/x/a.go","structuredPatch":[{"oldStart":3,"oldLines":2,"newStart":3,"newLines":3,"lines":[" keep","-old one","+new one","+new two"]}]}`,
			added:         2,
			removed:       1,
		},
		{
			name:  "MultiEdit",
			tool:  "MultiEdit",
			input: `{"file_path":"/x/b.go","edits":[]}`,
			toolUseResult: `{"filePath":"/x/b.go","edits":[],"structuredPatch":[` +
				`{"oldStart":1,"oldLines":1,"newStart":1,"newLines":1,"lines":["-a","+b"]},` +
				`{"oldStart":9,"oldLines":2,"newStart":9,"newLines":3,"lines":["-c","-d"," mid","+e","+f","+g"]}]}`,
			added:   4,
			removed: 3,
		},
		{
			name:          "Write update",
			tool:          "Write",
			input:         `{"file_path":"/x/c.go","content":"package c\n"}`,
			toolUseResult: `{"type":"update","filePath":"/x/c.go","content":"package c\n","structuredPatch":[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":1,"lines":["-package old","+package c"]}]}`,
			added:         1,
			removed:       1,
		},
		{
			name:          "Write create",
			tool:          "Write",
			input:         `{"file_path":"/x/d.go","content":"alpha\nbeta\ngamma\n"}`,
			toolUseResult: `{"type":"create","filePath":"/x/d.go","content":"alpha\nbeta\ngamma\n","structuredPatch":[]}`,
			added:         3,
			removed:       0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeCountsTranscript(t,
				countsCallLine("toolu_1", tc.tool, tc.input),
				countsResultLine("toolu_1", "done", tc.toolUseResult),
			)
			want := []Item{
				{Kind: kindResult, Text: "done", ID: "toolu_1", Added: tc.added, Removed: tc.removed},
				countsControlItem,
			}
			if got := resultItems(t, path); !reflect.DeepEqual(got, want) {
				t.Fatalf("result items = %+v, want %+v", got, want)
			}
		})
	}
}

func TestReadToolLineCountsFailedEditCarriesNone(t *testing.T) {
	path := writeCountsTranscript(t,
		countsCallLine("toolu_f", "Edit", `{"file_path":"/x/a.go"}`),
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_f","is_error":true,`+
			`"content":"old_string not found"}]},"toolUseResult":"Error: old_string not found"}`,
	)
	want := []Item{
		{Kind: kindResult, Text: "old_string not found", ID: "toolu_f", Error: true},
		countsControlItem,
	}
	if got := resultItems(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("result items = %+v, want %+v", got, want)
	}
}

func TestReadToolLineCountsBashAndReadCarryNone(t *testing.T) {
	path := writeCountsTranscript(t,
		countsCallLine("toolu_b", "Bash", `{"command":"ls"}`),
		countsResultLine("toolu_b", "a.go", `{"stdout":"a.go","stderr":"","interrupted":false}`),
		countsCallLine("toolu_r", "Read", `{"file_path":"/x/a.go"}`),
		countsResultLine("toolu_r", "package a",
			`{"type":"text","file":{"filePath":"/x/a.go","content":"package a\n","numLines":1,"startLine":1,"totalLines":1}}`),
	)
	want := []Item{
		{Kind: kindResult, Text: "a.go", ID: "toolu_b"},
		{Kind: kindResult, Text: "package a", ID: "toolu_r"},
		countsControlItem,
	}
	if got := resultItems(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("result items = %+v, want %+v", got, want)
	}
}

func TestReadToolLineCountsSeveralResultsCarryNone(t *testing.T) {
	path := writeCountsTranscript(t,
		countsCallLine("toolu_a", "Edit", `{"file_path":"/x/a.go"}`),
		countsCallLine("toolu_c", "Bash", `{"command":"ls"}`),
		`{"type":"user","message":{"content":[`+
			`{"type":"tool_result","tool_use_id":"toolu_a","content":"first"},`+
			`{"type":"tool_result","tool_use_id":"toolu_c","content":"second"}]},`+
			`"toolUseResult":{"type":"update","filePath":"/x/a.go","structuredPatch":[{"oldStart":1,"oldLines":1,`+
			`"newStart":1,"newLines":1,"lines":["-old","+new"]}]}}`,
	)
	want := []Item{
		{Kind: kindResult, Text: "first", ID: "toolu_a"},
		{Kind: kindResult, Text: "second", ID: "toolu_c"},
		countsControlItem,
	}
	if got := resultItems(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("result items = %+v, want %+v", got, want)
	}
}

func TestReadToolLineCountsNoResultLineGivesNoResultItem(t *testing.T) {
	path := writeCountsTranscript(t,
		countsCallLine("toolu_n", "Edit", `{"file_path":"/x/n.go"}`),
	)
	want := []Item{countsControlItem}
	if got := resultItems(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("result items = %+v, want %+v", got, want)
	}
}
