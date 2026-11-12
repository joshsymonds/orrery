package integration_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// Recorded Claude Code 2.1.321 background_tasks values for a Stop hook, as wanted_test.go records them.
const (
	quietSubagentTasks = `[{"id":"a1","type":"subagent","status":"running","description":"search the tree"}]`
	quietWorkflowTasks = `[{"id":"w1","type":"workflow","status":"running","description":"release check"}]`
	quietShellTasks    = `[{"id":"b1","type":"shell","status":"running","description":"make test","command":"make test"}]`
	quietMonitorTasks  = `[{"id":"m1","type":"monitor","status":"running","description":"watch the log"}]`
)

// stopHookInput is a Claude Code 2.1.321 Stop hook input whose transcript_path is transcript. background is the raw
// JSON for the background_tasks member, or "" to omit the member.
func stopHookInput(t *testing.T, transcript, background string) map[string]any {
	t.Helper()
	hook := map[string]any{
		"session_id":             "3f2c9a1e-0000-4000-8000-000000000001",
		"transcript_path":        transcript,
		"cwd":                    "/home/dev/work",
		"hook_event_name":        "Stop",
		"stop_hook_active":       false,
		"last_assistant_message": "Done.",
	}
	if background != "" {
		var tasks any
		if err := json.Unmarshal([]byte(background), &tasks); err != nil {
			t.Fatalf("decode background_tasks: %v", err)
		}
		hook["background_tasks"] = tasks
	}
	return hook
}

// TestOrrerydWebPushHonorsStopBackgroundTasks drives one orreryd with a Stop per pane. A Stop whose background_tasks
// holds a subagent or workflow sends no Web Push, and a shell task, a monitor task, or no background_tasks member
// each push one done notification for its pane.
func TestOrrerydWebPushHonorsStopBackgroundTasks(t *testing.T) {
	client, name := newServer(t)
	cases := []struct {
		name       string
		background string
		notify     bool
	}{
		{name: "subagent", background: quietSubagentTasks},
		{name: "workflow", background: quietWorkflowTasks},
		{name: "shell", background: quietShellTasks, notify: true},
		{name: "monitor", background: quietMonitorTasks, notify: true},
		{name: "no field", notify: true},
	}
	panes := make([]string, len(cases))
	panes[0] = startPane(t, client, "sleep 300")
	for i := 1; i < len(cases); i++ {
		panes[i] = splitPane(t, client, panes[0], "sleep 300")
	}

	dir := t.TempDir()
	stateHome := filepath.Join(dir, "state")
	endpoint := newPushEndpoint(t)
	registerPush(t, stateHome, endpoint)
	daemon, socket := startPushDaemon(t, name, dir, stateHome, endpoint)
	transcript := filepath.Join(dir, "transcript.jsonl")
	writeFile(t, transcript, exampleTranscript, 0o600)

	notifying := 0
	for i, c := range cases {
		sendHook(t, socket, panes[i], dir, stopHookInput(t, transcript, c.background))
		if c.notify {
			notifying++
		}
	}

	// A pane reads done once orreryd has applied its Stop. A Stop that notifies is judged after route.JudgeDelay, so
	// each notifying push is awaited before orreryd stops. orreryd awaits every notification before it exits, so once
	// it has exited, a push still queued is one a quiet Stop sent.
	for _, pane := range panes {
		waitForOption(t, client, pane, protocol.OptionState, string(protocol.StateDone))
	}
	pushed := make(map[string][]string)
	for range notifying {
		note := nextNote(t, endpoint, judgedLimit)
		pushed[note["pane"]] = append(pushed[note["pane"]], note["state"])
	}
	wantExit(t, daemon.stop(t), 0)
	if extra := len(endpoint.requests); extra != 0 {
		t.Fatalf("%d push requests beyond the %d notifying Stops", extra, notifying)
	}
	for i, c := range cases {
		states := pushed[panes[i]]
		if !c.notify {
			if len(states) != 0 {
				t.Fatalf("%s Stop pushed %q, want no Web Push", c.name, states)
			}
			continue
		}
		if !slices.Equal(states, []string{string(protocol.StateDone)}) {
			t.Fatalf("%s Stop pushed %q, want one %s notification", c.name, states, protocol.StateDone)
		}
	}
}
