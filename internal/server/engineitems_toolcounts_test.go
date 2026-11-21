package server

import (
	"encoding/json"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestSessionItemToolLineCountsResultItem checks that a session's result item carries the lines its file edit added and
// removed, and that a failed result and a tool call with no result yet carry none.
func TestSessionItemToolLineCountsResultItem(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		payload map[string]any
		added   int64
		removed int64
	}{
		{
			name: "successful edit result carries its counts",
			payload: map[string]any{
				"kind": "tool_result", "role": "user", "toolUseId": "toolu_1", "content": "edited",
				"added": 4, "removed": 2,
			},
			added:   4,
			removed: 2,
		},
		{
			name: "failed result carries none",
			payload: map[string]any{
				"kind": "tool_result", "role": "user", "toolUseId": "toolu_2", "content": "old_string not found",
				"isError": true,
			},
		},
		{
			name: "tool call with no result yet carries none",
			payload: map[string]any{
				"kind": "tool_call", "role": "assistant", "toolUseId": "toolu_3", "name": "Edit",
				"input": `{"file_path":"/x/a.go"}`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			payload, err := json.Marshal(tt.payload)
			if err != nil {
				t.Fatalf("marshal block: %v", err)
			}
			got, ok, err := sessionItem(store.EngineEvent{
				Seq: 1, Kind: store.EventEngineBlock, At: 300, Payload: string(payload),
			})
			if err != nil || !ok {
				t.Fatalf("sessionItem ok = %v, err = %v, want an item", ok, err)
			}
			if added, removed := got.GetAdded(), got.GetRemoved(); added != tt.added || removed != tt.removed {
				t.Fatalf("item added, removed = %d, %d; want %d, %d", added, removed, tt.added, tt.removed)
			}
		})
	}
}
