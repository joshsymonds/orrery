package integration_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// TestOrrerydWebPushHonorsStopBackgroundTasks drives one orreryd with a session per pane, each ending its turn as its
// background work leaves it. A subagent or workflow still running keeps the session's registry entry busy, so its pane
// reads working and sends no Web Push. A shell task, a monitor task, or none leaves the entry idle once the turn ends,
// so each pushes one done notification for its pane.
func TestOrrerydWebPushHonorsStopBackgroundTasks(t *testing.T) {
	t.Parallel()
	client, name := newServer(t)
	cases := []struct {
		name   string
		notify bool
	}{
		{name: "subagent"},
		{name: "workflow"},
		{name: "shell", notify: true},
		{name: "monitor", notify: true},
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
	daemon, _ := startPushDaemon(t, name, dir, stateHome, endpoint)

	notifying := 0
	for i, c := range cases {
		session := startClaudeSession(t, client, dir, panes[i], dir)
		session.submitPrompt(t)
		waitForOption(t, daemon, client, panes[i], protocol.OptionState, string(protocol.StateWorking))
		if c.notify {
			session.finishTurn(t, "Done.")
			notifying++
		}
	}

	// A pane reads done once its entry's idle status is applied, and reads working otherwise. A Stop that notifies is
	// judged after its judge delay, so each notifying push is awaited before orreryd stops. orreryd awaits every
	// notification before it exits, so once it has exited, a push still queued is one a quiet pane sent.
	for i, c := range cases {
		state := protocol.StateWorking
		if c.notify {
			state = protocol.StateDone
		}
		waitForOption(t, daemon, client, panes[i], protocol.OptionState, string(state))
	}
	pushed := make(map[string][]string)
	for range notifying {
		note := nextNote(t, endpoint)
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
