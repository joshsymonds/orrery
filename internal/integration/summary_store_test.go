package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// summaryStoreDeadline bounds how long a summary and label take to reach the pane's row once tmux holds them. It is
// well under the tidy interval, so only the summarizer's own sync can meet it.
const summaryStoreDeadline = 2 * time.Second

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
	f := newStoreFixture(t)
	if err := os.WriteFile(f.fakeClaude, []byte(slowFakeClaudeScript), 0o700); err != nil {
		t.Fatalf("write slow fake claude: %v", err)
	}
	daemon := f.startOrreryd(t)

	f.hook(t, f.userPromptHook())
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
