package integration_test

import (
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// summaryReleaseFile is the file beside gatedFakeClaudeScript whose creation lets its summary run reply.
const summaryReleaseFile = "release"

// gatedFakeClaudeScript is slowFakeClaudeScript with its pause replaced by a wait for summaryReleaseFile, so the test
// decides when the summary run writes its options and the summarizer syncs the pane.
const gatedFakeClaudeScript = `#!/bin/sh
D=$(dirname "$0")
mkdir -p "$D/calls"
R=$(mktemp -d "$D/calls/run.XXXXXX")
printf '%s\n' "$@" > "$R/args"
env | grep -E '^(ANTHROPIC_BASE_URL|ORRERY_SUMMARIZER)=' > "$R/env"
cat > "$R/stdin"
` + judgeRunScript + `until [ -e "$D/` + summaryReleaseFile + `" ]; do sleep 0.05; done
printf 'summary: example summary\nlabel: example label\n'
: > "$R/done"
`

// hookAndWait sends hook for the fixture's pane straight to orreryd's socket and returns once orreryd closes the
// connection. orreryd closes it only after the frame's store sync has finished, so no sync of that frame still holds
// the mirror lock. The wait has no deadline of its own: the sync's store write has no bound.
func (f *storeFixture) hookAndWait(t *testing.T, hook map[string]any) {
	t.Helper()
	payload, err := json.Marshal(hook)
	if err != nil {
		t.Fatalf("encode hook: %v", err)
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(t.Context(), "unix", f.socket)
	if err != nil {
		t.Fatalf("dial %s: %v", f.socket, err)
	}
	defer conn.Close()
	frame := protocol.HookFrame{Hook: payload, TmuxPane: f.pane, Cwd: f.project, AnthropicBaseURL: examplePatchbayURL}
	if err = protocol.WriteFrame(conn, frame); err != nil {
		t.Fatalf("write hook frame: %v", err)
	}
	if _, err = io.Copy(io.Discard, conn); err != nil {
		t.Fatalf("wait for orreryd to close the hook connection: %v", err)
	}
}

// waitForPaneID polls until the fixture's pane has an @orrery_id. orreryd's first tidy sets the id just before it
// releases the mirror lock. It sets no deadline of its own: the tidy follows a store write whose time load decides.
func (f *storeFixture) waitForPaneID(t *testing.T) {
	t.Helper()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for readOption(t, f.client, f.pane, protocol.OptionID) == "" {
		select {
		case <-t.Context().Done():
			t.Fatalf("pane %s never had an %s", f.pane, protocol.OptionID)
		case <-ticker.C:
			stopIfFailed(t)
		}
	}
}

// waitForSummaryRow polls the store row for pane until it holds summary and label, and returns the row it matched. It
// sets no deadline of its own: the summarizer's sync, or a later tidy when the contract lets that sync be dropped,
// writes the row after real store writes whose time load decides, and the test binary's timeout ends a wait that never
// ends. That the summarizer calls for the sync once it has written the options is
// TestSummarizerCallsOnWriteOnceTheOptionsAreWritten (internal/summary).
func (f *storeFixture) waitForSummaryRow(t *testing.T, db *sql.DB, pane, summary, label string) sqlcgen.Session {
	t.Helper()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		got, _, ok := f.paneRow(t, db, pane)
		if ok && got.Summary == summary && got.Label == label {
			return got
		}
		select {
		case <-t.Context().Done():
			t.Fatalf("pane %s row never held summary %q and label %q: row %+v (found %t)",
				pane, summary, label, got, ok)
		case <-ticker.C:
			stopIfFailed(t)
		}
	}
}

func TestSummaryReachesRowAndEvents(t *testing.T) {
	t.Parallel()
	f := newStoreFixture(t)
	if err := os.WriteFile(f.fakeClaude, []byte(gatedFakeClaudeScript), 0o700); err != nil {
		t.Fatalf("write gated fake claude: %v", err)
	}
	daemon := f.startOrreryd(t)
	// A sync waits at most 200 ms for a store still opening, or for the mirror lock, and is then dropped. So the
	// summarizer's sync must find the store open and the lock free: the hook follows the store-open line and the first
	// tidy, and the summary run replies only once the hook's own sync has finished.
	daemon.waitForStoreOpen(t)
	f.waitForPaneID(t)
	f.hookAndWait(t, f.userPromptHook())
	writeFile(t, filepath.Join(filepath.Dir(f.fakeClaude), summaryReleaseFile), "", 0o600)
	waitForOption(t, f.client, f.pane, protocol.OptionSummary, exampleSummary)
	db := openStoreForInspection(t, f.dbPath)

	// No hook follows the summarizer's writes, so the row reaches the summary and label through the sync the
	// summarizer calls for after its run writes them, or through a later tidy.
	row := f.waitForSummaryRow(t, db, f.pane, exampleSummary, exampleLabel)

	events, err := sqlcgen.New(db).ListEvents(t.Context())
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	// A session_changed event must carry the summary and one must carry the label; one event may carry both.
	if !hasChangedField(t, events, row.ID, "summary", exampleSummary) {
		t.Fatalf("store has no %s for session %s setting summary %q; events %+v",
			store.EventSessionChanged, row.ID, exampleSummary, events)
	}
	if !hasChangedField(t, events, row.ID, "label", exampleLabel) {
		t.Fatalf("store has no %s for session %s setting label %q; events %+v",
			store.EventSessionChanged, row.ID, exampleLabel, events)
	}

	wantExit(t, daemon.stop(t), 0)
}

// hasChangedField reports whether events holds a session_changed event for session id that sets field to value.
func hasChangedField(t *testing.T, events []sqlcgen.Event, id, field, value string) bool {
	t.Helper()
	for _, event := range events {
		if event.Kind != store.EventSessionChanged || event.SessionID != id {
			continue
		}
		var payload struct {
			Fields map[string]string `json:"fields"`
		}
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatalf("decode %s payload %q: %v", event.Kind, event.Payload, err)
		}
		if got, ok := payload.Fields[field]; ok && got == value {
			return true
		}
	}
	return false
}
