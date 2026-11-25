package enginehost

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// engineTaskRow is the payload of one engine_task event.
type engineTaskRow struct {
	TaskID       string `json:"taskId"`
	ToolUseID    string `json:"toolUseId"`
	Description  string `json:"description"`
	Type         string `json:"type"`
	SubagentType string `json:"subagentType"`
	Status       string `json:"status"`
	LastTool     string `json:"lastTool"`
	Summary      string `json:"summary"`
}

// engineTasks returns the payloads of the engine_task events of history, in seq order.
func engineTasks(t *testing.T, history store.EngineHistory) []engineTaskRow {
	t.Helper()
	var rows []engineTaskRow
	for _, event := range history.Events {
		if event.Kind != store.EventEngineTask {
			continue
		}
		var row engineTaskRow
		if err := json.Unmarshal([]byte(event.Payload), &row); err != nil {
			t.Fatalf("decode engine_task payload %q: %v", event.Payload, err)
		}
		rows = append(rows, row)
	}
	return rows
}

// TestBackgroundTaskRowRecordsEachMessageOfTheAgentAndTheShell runs TASKS: an agent and a shell task start, the agent
// reports progress with its last tool and then with a summary, and it ends as completed while the shell task still
// runs. Each message is recorded as an engine_task event that carries the fields the message gives and those it omits
// from the task's start.
func TestBackgroundTaskRowRecordsEachMessageOfTheAgentAndTheShell(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "TASKS"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the TASKS turn to end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 1 && lastState(t, h) == string(protocol.StateDone)
	})

	want := []engineTaskRow{
		{TaskID: "task_fake_agent", ToolUseID: "toolu_agent_1", Description: "fake agent", Type: "agent",
			SubagentType: "general-purpose", Status: "running"},
		{TaskID: "task_fake_shell", Description: "fake sleep", Type: "shell", Status: "running"},
		{TaskID: "task_fake_agent", ToolUseID: "toolu_agent_1", Description: "fake agent", Type: "agent",
			SubagentType: "general-purpose", Status: "running", LastTool: "Read"},
		{TaskID: "task_fake_agent", ToolUseID: "toolu_agent_1", Description: "fake agent", Type: "agent",
			SubagentType: "general-purpose", Status: "running", Summary: "fake reading the agent"},
		{TaskID: "task_fake_agent", ToolUseID: "toolu_agent_1", Description: "fake agent", Type: "agent",
			SubagentType: "general-purpose", Status: "done", Summary: "fake agent finished"},
	}
	if got := engineTasks(t, history); !slices.Equal(got, want) {
		t.Fatalf("engine_task events = %+v, want %+v", got, want)
	}
}

// stoppedRows returns the engine_task events of history whose status is stopped, in seq order.
func stoppedRows(t *testing.T, history store.EngineHistory) []engineTaskRow {
	t.Helper()
	var stopped []engineTaskRow
	for _, row := range engineTasks(t, history) {
		if row.Status == "stopped" {
			stopped = append(stopped, row)
		}
	}
	return stopped
}

// TestBackgroundTaskRowStoppedRecordsTheRunningShellAsStopped stops the session once its TASKS turn has ended, with the
// shell task still running. The stop ends the process, which delivers no exit, so the shell task is recorded as stopped
// by the stop, and the agent, which already ended, gets no stopped record.
func TestBackgroundTaskRowStoppedRecordsTheRunningShellAsStopped(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "TASKS"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the TASKS turn to end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 1 && lastState(t, h) == string(protocol.StateDone)
	})
	pid := fakePID(t, dir)

	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the stopped session", func(h store.EngineHistory) bool {
		return h.State == "stopped"
	})
	requireGone(t, pid)

	want := []engineTaskRow{{TaskID: "task_fake_shell", Description: "fake sleep", Type: "shell", Status: "stopped"}}
	if got := stoppedRows(t, history); !slices.Equal(got, want) {
		t.Fatalf("stopped engine_task events = %+v, want %+v", got, want)
	}
}

// TestBackgroundTaskRowClosedSessionRecordsTheRunningShellAsStopped closes the manager once the TASKS turn has ended,
// with the shell task still running. Close stops the session's process, which delivers no exit, so the close records
// the shell task as stopped, and the agent, which already ended, gets no stopped record.
func TestBackgroundTaskRowClosedSessionRecordsTheRunningShellAsStopped(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "TASKS"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the TASKS turn to end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 1 && lastState(t, h) == string(protocol.StateDone)
	})

	m.Close()
	want := []engineTaskRow{{TaskID: "task_fake_shell", Description: "fake sleep", Type: "shell", Status: "stopped"}}
	if got := stoppedRows(t, historyOf(t, path, id)); !slices.Equal(got, want) {
		t.Fatalf("stopped engine_task events = %+v, want %+v", got, want)
	}
}
