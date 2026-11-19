package main

import (
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/exectest"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// Synthetic values only. The epic, its host and the summary and the why the fake claude answers are invented.
const (
	housekeepWakeHost  = "housekeep-host"
	housekeepWakeEpic  = "orrery-8d9e0f1/housekeep-epic"
	housekeepWakeStamp = 1_700_000_000
	// housekeepFakeScript is the fake claude. It reads and discards its stdin, then prints the one JSON result line a
	// Haiku call reads. Its structured output holds the summary and the why, each writer decodes the field it asks for.
	housekeepFakeScript = `#!/bin/sh
cat > /dev/null
echo '{"type":"result","subtype":"success","is_error":false,"structured_output":{"summary":"Housekeep summary.","why":"Housekeep why."}}'
`
)

// housekeepClaude writes the fake claude into a directory of its own and returns its path.
func housekeepClaude(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	exectest.WriteFile(t, path, []byte(housekeepFakeScript), 0o700)
	return path
}

// startHousekeepHost starts a --sessions host over st whose claude is claude, and stops it at cleanup. No session is
// started, so claude runs only for the epic summaries and the epic reports.
func startHousekeepHost(t *testing.T, st *store.Store, getenv func(string) string, claude string) {
	t.Helper()
	opts := options{
		socket:       filepath.Join(t.TempDir(), "orreryd.sock"),
		claude:       claude,
		sessionShell: eventWakeShell(t),
		sessionCap:   defaultSessionCap,
		stepCap:      defaultStepCap,
	}
	host, err := startSessions(
		t.Context(), opts, housekeepWakeHost, st, newRecordingRouter(), nil, slog.New(slog.DiscardHandler), getenv,
	)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}
	t.Cleanup(func() {
		if err := host.stop(); err != nil {
			t.Errorf("stop sessions: %v", err)
		}
	})
}

// TestHousekeepWakeSessionsHostSummarizesAndReportsAnEpic checks that a --sessions host summarizes an epic the store
// stores and reports the epic the store closes, without any call to Wake: the store's hooks wake the summaries and the
// reports. The waits poll the store, since the summary and the report are written by the host's own goroutines.
func TestHousekeepWakeSessionsHostSummarizesAndReportsAnEpic(t *testing.T) {
	t.Parallel()
	st, getenv := openLimitStore(t)
	startHousekeepHost(t, st, getenv, housekeepClaude(t))

	createFlowWiringEpic(t, st, housekeepWakeEpic, time.Unix(housekeepWakeStamp, 0))
	waittest.Until(t, nil, "the stored epic has its summary", func() bool {
		row, err := st.Epic(t.Context(), housekeepWakeEpic)
		return err == nil && row.Summary != ""
	})
	row, err := st.Epic(t.Context(), housekeepWakeEpic)
	if err != nil {
		t.Fatalf("read epic after its summary: %v", err)
	}
	if row.Summary != "Housekeep summary." {
		t.Fatalf("summary = %q, want the fake's", row.Summary)
	}

	if _, err := st.CloseEpic(
		t.Context(), housekeepWakeEpic, "shipped", "", store.CloserPlanner, time.Unix(housekeepWakeStamp+60, 0),
	); err != nil {
		t.Fatalf("CloseEpic: %v", err)
	}
	waittest.Until(t, nil, "the closed epic has its report", func() bool {
		row, err := st.Epic(t.Context(), housekeepWakeEpic)
		return err == nil && row.Report.Why != ""
	})
	row, err = st.Epic(t.Context(), housekeepWakeEpic)
	if err != nil {
		t.Fatalf("read epic after its report: %v", err)
	}
	if row.Report.Phase != store.PhaseClosedShipped || row.Report.Why != "Housekeep why." {
		t.Fatalf("report = %+v, want phase %q and the fake's why", row.Report, store.PhaseClosedShipped)
	}
}
