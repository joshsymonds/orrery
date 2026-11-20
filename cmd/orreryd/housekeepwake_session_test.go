package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/exectest"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// Synthetic values only. The session, its dir and the title and headline the fake claude answers are invented.
const (
	housekeepSessionID  = "housekeep-session"
	housekeepSessionDir = "/work/housekeep-session"
	// housekeepSessionScript is the fake claude of the session test. It reads and discards its stdin, then prints the one
	// JSON result line a Haiku call reads. Its structured output holds the title and the headline, each writer decodes the
	// field it asks for.
	housekeepSessionScript = `#!/bin/sh
cat > /dev/null
echo '{"type":"result","subtype":"success","is_error":false,"structured_output":{"title":"Housekeep title.","headline":"Housekeep headline."}}'
`
)

// housekeepSessionRow returns the headless row of the test session as the store's reader reads it at path. It returns
// false when the read fails or the session is not listed.
func housekeepSessionRow(ctx context.Context, path string) (store.HeadlessRow, bool) {
	rows, err := store.NewReader(path).HeadlessSessions(ctx, 0)
	if err != nil {
		return store.HeadlessRow{}, false
	}
	for _, row := range rows {
		if row.ID == housekeepSessionID {
			return row, true
		}
	}
	return store.HeadlessRow{}, false
}

// TestHousekeepWakeSessionsHostTitlesAndHeadlinesASession checks that a --sessions host titles and headlines a headless
// session the store records, without any call to Wake: the store's hooks wake the titles on the first prompt and the
// headlines on the turn end. The waits poll the store, since the title and the headline are written by the host's own
// goroutines.
func TestHousekeepWakeSessionsHostTitlesAndHeadlinesASession(t *testing.T) {
	t.Parallel()
	st, getenv := openLimitStore(t)
	claude := filepath.Join(t.TempDir(), "claude")
	exectest.WriteFile(t, claude, []byte(housekeepSessionScript), 0o700)
	startHousekeepHost(t, st, getenv, claude)

	if err := st.ApplyEngine(t.Context(), []store.EngineOp{
		store.EngineAdd{ID: housekeepSessionID, Dir: housekeepSessionDir, State: "idle"},
		store.EnginePrompt{ID: housekeepSessionID, Text: "Plan the housekeeping wake."},
		store.EngineTurnEnded{
			ID: housekeepSessionID, Subtype: "success", TerminalReason: "completed", Result: "Planned.", Origin: "user",
		},
	}, time.Unix(housekeepWakeStamp, 0)); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	path, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store path: %v", err)
	}
	var row store.HeadlessRow
	waittest.Until(t, nil, "the session has its title and headline", func() bool {
		var ok bool
		row, ok = housekeepSessionRow(t.Context(), path)
		return ok && row.Title != "" && row.Headline != ""
	})
	if row.Title != "Housekeep title." || row.Headline != "Housekeep headline." {
		t.Fatalf("title and headline = %q and %q, want the fake's", row.Title, row.Headline)
	}
}
