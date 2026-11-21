package enginehost

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// toolCountsPayload is the engine_block payload of a tool result, as far as the line-count test reads it.
type toolCountsPayload struct {
	Kind    string `json:"kind"`
	Added   int64  `json:"added"`
	Removed int64  `json:"removed"`
}

func TestRecordToolLineCountsPayload(t *testing.T) {
	t.Parallel()
	s, path := openStore(t)
	add := store.EngineAdd{ID: recorderID, Dir: t.TempDir(), State: "idle", Kind: store.KindEngine}
	ops := append([]store.EngineOp{add}, newRecorder(recorderID).ops(session.Block{
		Kind: session.BlockToolResult, Role: "user", ToolUseID: "toolu_1",
		Content: json.RawMessage(`"edited"`), Added: 2, Removed: 1,
	})...)
	if err := s.ApplyEngine(t.Context(), ops, fixedClock()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	var got []toolCountsPayload
	for _, event := range historyOf(t, path, recorderID).Events {
		if event.Kind != store.EventEngineBlock {
			continue
		}
		var payload toolCountsPayload
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatalf("decode engine_block payload %q: %v", event.Payload, err)
		}
		got = append(got, payload)
	}
	want := []toolCountsPayload{{Kind: "tool_result", Added: 2, Removed: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("engine_block payloads = %+v, want %+v", got, want)
	}
}
