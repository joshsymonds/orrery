package server_test

import (
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
)

// bgClock is the clock a background test's server reads. It stands at the unix second it was last set to, so a test
// moves it only when it means to.
type bgClock struct {
	at atomic.Int64
}

// newBgClock returns a clock standing at the unix second at.
func newBgClock(at int64) *bgClock {
	c := &bgClock{}
	c.at.Store(at)
	return c
}

// Now returns the time the clock stands at.
func (c *bgClock) Now() time.Time {
	return time.Unix(c.at.Load(), 0)
}

// set moves the clock to the unix second at.
func (c *bgClock) set(at int64) {
	c.at.Store(at)
}

// bgWatchClient serves the store at path through a server whose clock is clock, and returns a Sessions client for it.
// Its WatchConversation polls and sends keepalives as sessionsHandler's does.
func bgWatchClient(t *testing.T, path string, clock *bgClock) orreryv1connect.SessionsClient {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:                []string{allowedLogin},
		Tmux:                  newTmuxClient(t),
		Push:                  newPushStore(t),
		Sessions:              store.NewReader(path),
		ConversationPoll:      convPoll,
		ConversationKeepalive: convKeepalive,
		Now:                   clock.Now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return sessionsWatchClient(t, handler)
}

// bgApplyAt writes ops to the store in one write, stamped at the unix second at.
func bgApplyAt(t *testing.T, s *store.Store, at int64, ops ...store.EngineOp) {
	t.Helper()
	if err := s.ApplyEngine(t.Context(), ops, time.Unix(at, 0)); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
}

// bgAgentTask is the engine_task op of eventEngine's agent task, with the given status and last tool.
func bgAgentTask(status, lastTool string) store.EngineTask {
	return store.EngineTask{
		ID: eventEngine, TaskID: "task-agent", ToolUseID: "tool-agent", Description: "Survey the repo",
		Type: "agent", SubagentType: "Explore", Status: status, LastTool: lastTool,
	}
}

// bgShellTask is the engine_task op of eventEngine's shell task, with the given status and summary.
func bgShellTask(status, summary string) store.EngineTask {
	return store.EngineTask{
		ID: eventEngine, TaskID: "task-shell", ToolUseID: "tool-shell", Description: "Run the build",
		Type: "shell", Status: status, Summary: summary,
	}
}

// bgAgentRow is the background row of the agent task bgAgentTask writes, started at started and ended at ended, which
// is 0 while the task runs.
func bgAgentRow(status, activity string, started, ended int64) *orreryv1.BackgroundTask {
	return &orreryv1.BackgroundTask{
		Id: "task-agent", ToolUseId: "tool-agent", Description: "Survey the repo", Type: "agent",
		SubagentType: "Explore", Status: status, Activity: activity, Started: started, Ended: ended,
	}
}

// bgShellRow is the background row of the shell task bgShellTask writes, started at started and ended at ended, which
// is 0 while the task runs.
func bgShellRow(status, activity string, started, ended int64) *orreryv1.BackgroundTask {
	return &orreryv1.BackgroundTask{
		Id: "task-shell", ToolUseId: "tool-shell", Description: "Run the build", Type: "shell",
		Status: status, Activity: activity, Started: started, Ended: ended,
	}
}

// TestSessionWatchBackgroundTaskRowServesRunningAgentAndShell records a running agent task and a running shell task. The
// first delta serves both rows, oldest start first, with their description, type, subagent type, status and start time.
// Neither task makes an item.
func TestSessionWatchBackgroundTaskRowServesRunningAgentAndShell(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	at := int64(eventAt)
	bgApplyAt(t, s, at, bgAgentTask("running", ""))
	bgApplyAt(t, s, at+1, bgShellTask("running", ""))
	client := bgWatchClient(t, path, newBgClock(at+2))

	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        3,
		Reset_:     true,
		State: &orreryv1.ConversationState{
			Session: engineSession("idle"),
			Background: []*orreryv1.BackgroundTask{
				bgAgentRow("running", "", at, 0),
				bgShellRow("running", "", at+1, 0),
			},
		},
	})
}

// TestSessionWatchBackgroundTaskRowProgressUpdatesActivity records a running agent task, then its progress with a last
// tool. The delta that follows serves that tool as the row's activity, and it makes no item.
func TestSessionWatchBackgroundTaskRowProgressUpdatesActivity(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	at := int64(eventAt)
	bgApplyAt(t, s, at, bgAgentTask("running", ""))
	client := bgWatchClient(t, path, newBgClock(at+5))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	stream.next(t)

	bgApplyAt(t, s, at+3, bgAgentTask("running", "Grep"))
	wantStreamMessage(t, stream.nextDelta(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        3,
		State: &orreryv1.ConversationState{
			Session:    engineSession("idle"),
			Background: []*orreryv1.BackgroundTask{bgAgentRow("running", "Grep", at, 0)},
		},
	})
}

// TestSessionWatchBackgroundTaskRowShellProgressAndEndServeNoItem records a running shell task, then its progress with a
// summary, then its end. Each delta carries the row as it stands and no item.
func TestSessionWatchBackgroundTaskRowShellProgressAndEndServeNoItem(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	at := int64(eventAt)
	bgApplyAt(t, s, at, bgShellTask("running", ""))
	client := bgWatchClient(t, path, newBgClock(at+5))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	stream.next(t)

	bgApplyAt(t, s, at+3, bgShellTask("running", "Compiling"))
	wantStreamMessage(t, stream.nextDelta(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        3,
		State: &orreryv1.ConversationState{
			Session:    engineSession("idle"),
			Background: []*orreryv1.BackgroundTask{bgShellRow("running", "Compiling", at, 0)},
		},
	})

	bgApplyAt(t, s, at+4, bgShellTask("done", ""))
	wantStreamMessage(t, stream.nextDelta(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        4,
		State: &orreryv1.ConversationState{
			Session:    engineSession("idle"),
			Background: []*orreryv1.BackgroundTask{bgShellRow("done", "Compiling", at, at+4)},
		},
	})
}

// TestSessionWatchBackgroundTaskRowEndedServedWithinWindowThenDropped records a shell task that ends ten seconds after it
// starts. While the clock stands 60 seconds past the end, the first delta serves the ended row. Once the clock moves on
// to 61 seconds past the end, the next delta carries no background task.
func TestSessionWatchBackgroundTaskRowEndedServedWithinWindowThenDropped(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	at := int64(eventAt)
	end := at + 10
	bgApplyAt(t, s, at, bgShellTask("running", ""))
	bgApplyAt(t, s, end, bgShellTask("done", ""))
	clock := newBgClock(end + 60)
	stream := openSessionStream(t, bgWatchClient(t, path, clock), sessionReq(eventEngine, 0, 0))

	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        3,
		Reset_:     true,
		State: &orreryv1.ConversationState{
			Session:    engineSession("idle"),
			Background: []*orreryv1.BackgroundTask{bgShellRow("done", "", at, end)},
		},
	})

	clock.set(end + 61)
	wantStreamMessage(t, stream.nextDelta(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        3,
		State:      &orreryv1.ConversationState{Session: engineSession("idle")},
	})
}

// TestSessionWatchBackgroundTaskRowAgentEndServesFinishedLine records an agent task that runs with a last tool, then ends
// done. The first delta carries one agent_finished item, stamped with the end's time and naming the agent's description,
// tool use id, subagent type and status. The row keeps the last tool as its activity.
func TestSessionWatchBackgroundTaskRowAgentEndServesFinishedLine(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	at := int64(eventAt)
	end := at + 10
	bgApplyAt(t, s, at, bgAgentTask("running", "Grep"))
	bgApplyAt(t, s, end, bgAgentTask("done", ""))
	client := bgWatchClient(t, path, newBgClock(end+5))

	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        3,
		Reset_:     true,
		Oldest:     3,
		Items: []*orreryv1.ConversationItem{{
			Kind:   "agent_finished",
			Time:   end,
			Text:   "Survey the repo",
			Id:     "tool-agent",
			Tool:   "Explore",
			Notice: "done",
		}},
		State: &orreryv1.ConversationState{
			Session:    engineSession("idle"),
			Background: []*orreryv1.BackgroundTask{bgAgentRow("done", "Grep", at, end)},
		},
	})
}
