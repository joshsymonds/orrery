package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

const (
	// engineStreamSessions is how many sessions stream at the same time.
	engineStreamSessions = 4
	// engineStreamPrompt is the prompt that makes the fake claude stream engineStreamBlocks finished text blocks.
	engineStreamPrompt = "TRICKLE"
	// engineStreamBlocks is how many finished text blocks one stream prompt streams.
	engineStreamBlocks = 60
	// engineStreamText starts the text of each finished block the stream prompt streams.
	engineStreamText = "fake trickle"
	// panePulses is how many pane hooks the test sends while the sessions stream. They alternate working and done.
	panePulses = 40
	// pulseSession is the Claude Code session id every pane hook carries.
	pulseSession = "pulse-session"
)

// TestEngineFourSessionsKeepPaneSyncs proves that four sessions streaming at once never cost a pane sync. While the
// sessions stream, the test sends a run of pane hooks whose states alternate. Each hook's state must reach the pane's
// store row and the event log in order, every session's pane event must fall within that session's blocks in the
// event log, every session must store all of its blocks, and orreryd must log no dropped mirror sync.
func TestEngineFourSessionsKeepPaneSyncs(t *testing.T) {
	t.Parallel()
	f := newEngineFixture(t)
	daemon := f.startOrreryd(t, true)

	ids := make([]string, engineStreamSessions)
	dirs := make([]string, engineStreamSessions)
	for i := range engineStreamSessions {
		dirs[i] = f.sessionDir(t, fmt.Sprintf("stream-%d", i))
		ids[i] = f.start(t, dirs[i], engineStreamPrompt)
	}

	client, err := tmuxctl.New(tmuxctl.Server{Name: f.server})
	if err != nil {
		t.Fatalf("New(Name=%q): %v", f.server, err)
	}
	pane := fixturePane(t, client)
	pulseDir := f.sessionDir(t, "pulse")
	transcript := filepath.Join(pulseDir, "transcript.jsonl")
	writeFile(t, transcript, exampleTranscript, 0o600)
	q := sqlcgen.New(openStoreForInspection(t, f.dbPath))

	want := make([]string, 0, panePulses)
	for i := range panePulses {
		state, event := protocol.StateWorking, "UserPromptSubmit"
		if i%2 == 1 {
			state, event = protocol.StateDone, "Stop"
		}
		hook := map[string]any{"hook_event_name": event, "session_id": pulseSession, "transcript_path": transcript}
		sendHook(t, f.socket, pane, pulseDir, hook)
		waitForPaneState(t, q, client, pane, state)
		want = append(want, string(state))
	}

	for _, id := range ids {
		f.waitForHistory(t, id, hasStreamBlocks)
	}

	events, err := q.ListEvents(t.Context())
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	paneID := readOption(t, client, pane, protocol.OptionID)
	if got := eventStates(t, events, paneID); !slices.Equal(got, want) {
		t.Fatalf("pane %s event log states = %v, want %v", paneID, got, want)
	}
	for _, id := range ids {
		if !paneEventWithinBlocks(events, id, paneID) {
			t.Errorf("no pane session_changed event falls between the first and last engine_block of session %s", id)
		}
	}

	for i, id := range ids {
		f.stopSession(t, id, dirs[i])
	}

	out := daemon.stop(t)
	wantExit(t, out, 0)
	if strings.Contains(out.stderr, mirrorDroppedLog) {
		t.Fatalf("orreryd dropped a mirror sync; stderr %q", out.stderr)
	}
}

// fixturePane returns the ID of the pane the engine fixture's startPane made on client's server. The fixture discards
// that ID, so the test reads it from the session the fixture created.
func fixturePane(t *testing.T, client *tmuxctl.Client) string {
	t.Helper()
	out, err := client.Run(t.Context(), "list-panes", "-t", "s1", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	panes := strings.Fields(out)
	if len(panes) != 1 {
		t.Fatalf("session s1 has %d panes, want 1: %q", len(panes), out)
	}
	return panes[0]
}

// waitForPaneState polls until the store row of pane holds state, failing the test after waitLimit. The pane has no
// row until orreryd mints its id, so a missing id or row is polled again.
func waitForPaneState(t *testing.T, q *sqlcgen.Queries, client *tmuxctl.Client, pane string, state protocol.State) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), waitLimit)
	defer cancel()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		id := readOption(t, client, pane, protocol.OptionID)
		if id != "" {
			row, err := q.GetSession(ctx, id)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("read row %s: %v", id, err)
			}
			if err == nil && row.State == string(state) {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pane %s row never reached state %q (id %q)", pane, state, id)
		case <-ticker.C:
		}
	}
}

// eventStates returns, in order, the state each session_changed event for session id records.
func eventStates(t *testing.T, events []sqlcgen.Event, id string) []string {
	t.Helper()
	var states []string
	for _, event := range events {
		if event.SessionID != id || event.Kind != store.EventSessionChanged {
			continue
		}
		var payload struct {
			Fields map[string]string `json:"fields"`
		}
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatalf("decode %s payload %q: %v", event.Kind, event.Payload, err)
		}
		if state, ok := payload.Fields["state"]; ok {
			states = append(states, state)
		}
	}
	return states
}

// paneEventWithinBlocks reports whether a session_changed event of pane paneID falls between the first and the last
// engine_block event of session id in the event log. The log is in seq order, so the seqs compare directly.
func paneEventWithinBlocks(events []sqlcgen.Event, id, paneID string) bool {
	var first, last int64
	found := false
	for _, event := range events {
		if event.SessionID != id || event.Kind != store.EventEngineBlock {
			continue
		}
		if !found {
			first = event.Seq
			found = true
		}
		last = event.Seq
	}
	if !found {
		return false
	}
	for _, event := range events {
		between := event.Seq > first && event.Seq < last
		if event.SessionID == paneID && event.Kind == store.EventSessionChanged && between {
			return true
		}
	}
	return false
}

// hasStreamBlocks reports whether history holds every block of a session's first stream prompt. Each session's first
// prompt is prompt 1 for its fake claude, so the blocks read "<engineStreamText> 1.k".
func hasStreamBlocks(history store.EngineHistory) bool {
	for k := 1; k <= engineStreamBlocks; k++ {
		if !hasBlock(history, fmt.Sprintf("%s 1.%d", engineStreamText, k)) {
			return false
		}
	}
	return true
}
