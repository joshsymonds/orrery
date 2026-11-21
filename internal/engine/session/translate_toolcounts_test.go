package session

import (
	"encoding/json"
	"reflect"
	"testing"
)

// countsUserLine returns an SDK user line whose content is the content blocks JSON, and whose tool_use_result is
// toolUseResult.
func countsUserLine(content, toolUseResult string) string {
	return `{"type":"user","message":{"role":"user","content":[` + content + `]},"parent_tool_use_id":null,` +
		`"session_id":"session_1","tool_use_result":` + toolUseResult + `}`
}

func TestToolLineCountsSDKResultCarriesPatchCounts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		toolUseResult string
		added         int64
		removed       int64
	}{
		{
			name: "Edit",
			toolUseResult: `{"type":"update","filePath":"/x/a.go","structuredPatch":[{"oldStart":3,"oldLines":2,` +
				`"newStart":3,"newLines":3,"lines":[" keep","-old one","+new one","+new two"]}]}`,
			added:   2,
			removed: 1,
		},
		{
			name: "MultiEdit",
			toolUseResult: `{"filePath":"/x/b.go","edits":[],"structuredPatch":[` +
				`{"oldStart":1,"oldLines":1,"newStart":1,"newLines":1,"lines":["-a","+b"]},` +
				`{"oldStart":9,"oldLines":2,"newStart":9,"newLines":3,"lines":["-c","-d"," mid","+e","+f","+g"]}]}`,
			added:   4,
			removed: 3,
		},
		{
			name: "Write update",
			toolUseResult: `{"type":"update","filePath":"/x/c.go","content":"package c\n","structuredPatch":[` +
				`{"oldStart":1,"oldLines":1,"newStart":1,"newLines":1,"lines":["-package old","+package c"]}]}`,
			added:   1,
			removed: 1,
		},
		{
			name: "Write create",
			toolUseResult: `{"type":"create","filePath":"/x/d.go","content":"alpha\nbeta\ngamma\n",` +
				`"structuredPatch":[]}`,
			added:   3,
			removed: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr := newTranslator()
			line := countsUserLine(`{"type":"tool_result","tool_use_id":"toolu_1","content":"done"}`, tc.toolUseResult)
			got := requireOneEvent[Block](t, tr.translate([]byte(line)))
			want := Block{
				Kind: BlockToolResult, Role: "user", ToolUseID: "toolu_1",
				Content: json.RawMessage(`"done"`), Added: tc.added, Removed: tc.removed,
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Block = %#v, want %#v", got, want)
			}
		})
	}
}

func TestToolLineCountsErrorAndBashResultsCarryNone(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		line string
		want Block
	}{
		{
			name: "failed edit whose result still carries a patch",
			line: countsUserLine(`{"type":"tool_result","tool_use_id":"toolu_2","content":"edit failed",`+
				`"is_error":true}`,
				`{"type":"update","filePath":"/x/e.go","structuredPatch":[{"oldStart":1,"oldLines":1,`+
					`"newStart":1,"newLines":1,"lines":["-old","+new"]}]}`),
			want: Block{
				Kind: BlockToolResult, Role: "user", ToolUseID: "toolu_2",
				Content: json.RawMessage(`"edit failed"`), IsError: true,
			},
		},
		{
			name: "bash result",
			line: countsUserLine(`{"type":"tool_result","tool_use_id":"toolu_3","content":"listing"}`,
				`{"stdout":"a\nb\n","stderr":"","interrupted":false}`),
			want: Block{
				Kind: BlockToolResult, Role: "user", ToolUseID: "toolu_3",
				Content: json.RawMessage(`"listing"`),
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr := newTranslator()
			got := requireOneEvent[Block](t, tr.translate([]byte(tc.line)))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Block = %#v, want %#v", got, tc.want)
			}
		})
	}
}
