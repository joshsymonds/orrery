package server

import (
	"encoding/json"
	"testing"
)

// inboxStatesListing returns the JSON object listPanes gives for a pane running command with the Claude session
// claudeSession, with invented values for the other fields.
func inboxStatesListing(t *testing.T, command, claudeSession string) map[string]json.RawMessage {
	t.Helper()
	values := map[string]any{
		"id":             "%1",
		"command":        command,
		claudeSessionKey: claudeSession,
		questionHashKey:  "synthetic-question-hash",
		newestReplyKey:   int64(0),
		lastTurnEndKey:   int64(0),
		orreryIDKey:      "synthetic-orrery-id",
	}
	fields := make(map[string]json.RawMessage, len(values))
	for key, value := range values {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("encode %s: %v", key, err)
		}
		fields[key] = data
	}
	return fields
}

// inboxStatesUnbound returns the Unbound of the pane Panes.List answers through paneMessages for a pane running
// command with the Claude session claudeSession.
func inboxStatesUnbound(t *testing.T, command, claudeSession string) bool {
	t.Helper()
	panes, err := paneMessages([]map[string]json.RawMessage{inboxStatesListing(t, command, claudeSession)})
	if err != nil {
		t.Fatalf("paneMessages: %v", err)
	}
	if len(panes) != 1 {
		t.Fatalf("paneMessages has %d panes, want 1", len(panes))
	}
	return panes[0].GetUnbound()
}

func TestInboxStatesUnboundClaudeWithoutSession(t *testing.T) {
	t.Parallel()

	if !inboxStatesUnbound(t, "claude", "") {
		t.Fatalf("Unbound = false, want true for a pane running claude with no Claude session")
	}
}

func TestInboxStatesUnboundClaudeWithSession(t *testing.T) {
	t.Parallel()

	if inboxStatesUnbound(t, "claude", "synthetic-claude-session") {
		t.Fatalf("Unbound = true, want false for a pane running claude with a bound Claude session")
	}
}

func TestInboxStatesUnboundOtherCommand(t *testing.T) {
	t.Parallel()

	if inboxStatesUnbound(t, "sleep", "") {
		t.Fatalf("Unbound = true, want false for a pane running another command with no Claude session")
	}
}
