package integration_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
	// engineStreamPrompt is the prompt that makes the fake claude stream engineStreamBlocks finished text blocks. It
	// holds the last block until fakeclaude.release exists in the session's directory, so each turn stays open until the
	// test has sent every pane hook.
	engineStreamPrompt = "TRICKLEHELD"
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
	}
	// Each stream advances a block per pane hook, so the sessions stream while the hooks arrive at any machine's pace.
	paceStreams(t, dirs, 1)
	for i := range engineStreamSessions {
		ids[i] = f.start(t, dirs[i], engineStreamPrompt)
	}
	// Every session's first block is stored before the first hook, and its last block only after the last hook, so
	// every pane event falls within every session's blocks whatever the machine's pace.
	for _, id := range ids {
		f.waitForBlock(t, id, engineStreamText+" 1.1")
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
	// Every pulse is the same session, so its row id, once the first pulse has minted it, is read from the store.
	var rowID string
	for i := range panePulses {
		state, event := protocol.StateWorking, "UserPromptSubmit"
		if i%2 == 1 {
			state, event = protocol.StateDone, "Stop"
		}
		hook := map[string]any{"hook_event_name": event, "session_id": pulseSession, "transcript_path": transcript}
		// The frame goes straight to orreryd's socket: the subject is the pane syncs under the sessions' load, not
		// the hook command, so no orrery process is spawned per pulse.
		paceStreams(t, dirs, i+2)
		sendHook(t, f.socket, pane, pulseDir, hook)
		rowID = waitForPaneState(t, q, client, pane, rowID, state)
		want = append(want, string(state))
	}

	paceStreams(t, dirs, engineStreamBlocks)
	for i, id := range ids {
		writeFile(t, filepath.Join(dirs[i], "fakeclaude.release"), "", 0o600)
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

// paceStreams lets the fake claude in each of dirs stream up to block k of its trickle: at each block it waits for
// fakeclaude.pace to reach it. The file is replaced whole, so the fake reads a number or no file.
func paceStreams(t *testing.T, dirs []string, k int) {
	t.Helper()
	for _, dir := range dirs {
		next := filepath.Join(dir, "fakeclaude.pace.next")
		writeFile(t, next, strconv.Itoa(k), 0o600)
		if err := os.Rename(next, filepath.Join(dir, "fakeclaude.pace")); err != nil {
			t.Fatalf("pace the stream in %s: %v", dir, err)
		}
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

// waitForPaneState polls until the store row of pane holds state, and returns the row's id. It has no deadline of its
// own. id is the row's id from an earlier wait, or "" when the caller has none. A known id is read from the store
// alone; the pane's id option, a tmux call, is read only while that row does not hold state, so a new id is followed.
// The pane has no row until orreryd mints its id, so a missing id or row is polled again.
func waitForPaneState(
	t *testing.T, q *sqlcgen.Queries, client *tmuxctl.Client, pane, id string, state protocol.State,
) string {
	t.Helper()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		if id != "" && rowHolds(t, q, id, state) {
			return id
		}
		if current := readOption(t, client, pane, protocol.OptionID); current != id {
			id = current
			continue
		}
		stopIfFailed(t)
		<-ticker.C
	}
}

// rowHolds reports whether the store row id exists and holds state.
func rowHolds(t *testing.T, q *sqlcgen.Queries, id string, state protocol.State) bool {
	t.Helper()
	row, err := q.GetSession(t.Context(), id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("read row %s: %v", id, err)
	}
	return err == nil && row.State == string(state)
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
