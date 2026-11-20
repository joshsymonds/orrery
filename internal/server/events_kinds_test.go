package server_test

import (
	"path/filepath"
	"testing"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

func TestEngineEventKindsWatchServesTypedMembers(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s,
		store.EngineInterrupt{ID: eventEngine, Turning: true},
		store.EngineTask{
			ID: eventEngine, TaskID: "task-1", ToolUseID: "toolu-1", Description: "scan", Type: "agent",
			SubagentType: "explore", Status: "running", LastTool: "Grep", Summary: "searching",
		},
		store.EngineTurnEnded{
			ID: eventEngine, Subtype: "success", TerminalReason: "done", Result: "ok", Origin: "user",
			OwnerInterrupted: true,
		},
	)
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), startServer(t, path, nil))

	at := int64(eventAt)
	want := []*orreryv1.WatchResponse{
		{Seq: 1, SessionId: eventEngine, At: at, Event: &orreryv1.WatchResponse_EngineAdded{
			EngineAdded: &orreryv1.EngineAdded{Dir: "/a", State: "idle"},
		}},
		{Seq: 2, SessionId: eventEngine, At: at, Event: &orreryv1.WatchResponse_EngineInterrupt{
			EngineInterrupt: &orreryv1.EngineInterrupt{Turning: true},
		}},
		{Seq: 3, SessionId: eventEngine, At: at, Event: &orreryv1.WatchResponse_EngineTask{
			EngineTask: &orreryv1.EngineTask{
				TaskId: "task-1", ToolUseId: "toolu-1", Description: "scan", Type: "agent",
				SubagentType: "explore", Status: "running", LastTool: "Grep", Summary: "searching",
			},
		}},
		{Seq: 4, SessionId: eventEngine, At: at, Event: &orreryv1.WatchResponse_EngineTurnEnded{
			EngineTurnEnded: &orreryv1.EngineTurnEnded{
				Subtype: "success", TerminalReason: "done", Result: "ok", Origin: "user", OwnerInterrupted: true,
			},
		}},
	}
	w := openWatch(t, events, 0)
	for _, wantMsg := range want {
		wantMessage(t, w.next(t), wantMsg)
	}
}
