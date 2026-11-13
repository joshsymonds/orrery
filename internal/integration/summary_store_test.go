package integration_test

import (
	"context"
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

// summaryStoreDeadline bounds how long a summary and label take to reach the pane's row once tmux holds them. It is
// well under the tidy interval, so only the summarizer's own sync can meet it.
const summaryStoreDeadline = 2 * time.Second

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

// waitForPaneID polls until the fixture's pane has an @orrery_id, failing the test after waitLimit. orreryd's first
// tidy sets the id just before it releases the mirror lock.
func (f *storeFixture) waitForPaneID(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), waitLimit)
	defer cancel()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for readOption(t, f.client, f.pane, protocol.OptionID) == "" {
		select {
		case <-ctx.Done():
			t.Fatalf("pane %s had no %s within %v", f.pane, protocol.OptionID, waitLimit)
		case <-ticker.C:
		}
	}
}

// waitForSummaryRow polls the store row for pane until it holds summary and label, failing the test after
// summaryStoreDeadline. It returns the row it matched.
func (f *storeFixture) waitForSummaryRow(t *testing.T, db *sql.DB, pane, summary, label string) sqlcgen.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), summaryStoreDeadline)
	defer cancel()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		got, _, ok := f.paneRow(t, db, pane)
		if ok && got.Summary == summary && got.Label == label {
			return got
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pane %s row never held summary %q and label %q within %s: row %+v (found %t)",
				pane, summary, label, summaryStoreDeadline, got, ok)
		case <-ticker.C:
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

	// No hook follows the summarizer's writes, so the row can only reach the summary and label through the sync
	// that the summarizer triggers after its run writes them.
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
