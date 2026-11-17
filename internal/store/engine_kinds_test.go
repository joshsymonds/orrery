package store

import (
	"context"
	"testing"
)

func TestEngineEventKindsAppendInterruptAndTask(t *testing.T) {
	t.Parallel()
	s := openSyncStore(t)
	ops := []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "working"},
		EngineInterrupt{ID: "e1", Turning: true},
		EngineInterrupt{ID: "e1", Turning: false},
		EngineTask{ID: "e1", TaskID: "task-1", ToolUseID: "toolu-1", Description: "scan", Type: "agent",
			SubagentType: "explore", Status: "running", LastTool: "Grep", Summary: "searching"},
	}
	if err := s.ApplyEngine(context.Background(), ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	assertEngineEvents(t, s, engineNowAt(), []wantEngineEvent{
		{"e1", EventEngineAdded, map[string]any{"dir": "/a", "state": "working"}},
		{"e1", EventEngineInterrupt, map[string]any{"turning": true}},
		{"e1", EventEngineInterrupt, map[string]any{"turning": false}},
		{"e1", EventEngineTask, map[string]any{
			"taskId": "task-1", "toolUseId": "toolu-1", "description": "scan", "type": "agent",
			"subagentType": "explore", "status": "running", "lastTool": "Grep", "summary": "searching",
		}},
	})
}

func TestEngineEventKindsTurnEndStoresOwnerInterruptedOnlyWhenSet(t *testing.T) {
	t.Parallel()
	s := openSyncStore(t)
	ops := []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "working"},
		EngineTurnEnded{ID: "e1", Subtype: "success", TerminalReason: "done", Result: "ok", Origin: "user"},
		EngineTurnEnded{ID: "e1", Subtype: "success", TerminalReason: "done", Result: "ok", Origin: "user",
			OwnerInterrupted: true},
	}
	if err := s.ApplyEngine(context.Background(), ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	turnEnd := map[string]any{"subtype": "success", "isError": false, "terminalReason": "done",
		"result": "ok", "origin": "user"}
	interrupted := map[string]any{"subtype": "success", "isError": false, "terminalReason": "done",
		"result": "ok", "origin": "user", "ownerInterrupted": true}
	assertEngineEvents(t, s, engineNowAt(), []wantEngineEvent{
		{"e1", EventEngineAdded, map[string]any{"dir": "/a", "state": "working"}},
		{"e1", EventEngineTurnEnded, turnEnd},
		{"e1", EventEngineTurnEnded, interrupted},
	})
}
