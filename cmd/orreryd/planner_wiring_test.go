package main

import (
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic values only. The epic, its repository and its texts are invented. No role file exists, so the planner's
// agent cannot be read and no planner step can start.
const (
	plannerWiringEpic  = "orrery-1a2b3c4/planner-wiring"
	plannerWiringRepo  = "/work/synthetic-repo"
	plannerWiringHost  = "synthetic-host"
	plannerWiringWait  = 10 * time.Second
	plannerWiringPoll  = 10 * time.Millisecond
	plannerWiringStamp = 1_700_000_000
)

// TestSessionsHostParksEpicWithoutRoleFile checks that a --sessions orreryd runs the planner over its store. The
// environment has no role file, so the planner's agent cannot be read, and the synthetic epic with a pending trigger
// parks with that reason before any turn is claimed. The host then stops without error.
func TestSessionsHostParksEpicWithoutRoleFile(t *testing.T) {
	ctx := t.Context()
	env := map[string]string{
		"XDG_STATE_HOME":  t.TempDir(),
		"XDG_CONFIG_HOME": t.TempDir(),
	}
	getenv := func(key string) string { return env[key] }
	path, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store path: %v", err)
	}
	st := openPaneStore(t, path)
	created := time.Unix(plannerWiringStamp, 0)
	if err := st.CreateEpic(ctx, store.EpicRow{
		ID:          plannerWiringEpic,
		Repository:  plannerWiringRepo,
		Title:       "Plan the wiring",
		Approved:    "approved text",
		Intent:      "plan the next tasks",
		Care:        "serious",
		Approach:    "one planner turn",
		ReleaseThen: "release it",
		Checks:      "just check",
		Phase:       "accepted",
		Created:     created,
		Changed:     created,
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: "the planner parks", Evidence: "wiring test", Kind: "test"},
		},
	}); err != nil {
		t.Fatalf("create epic: %v", err)
	}

	opts := options{
		socket:     filepath.Join(t.TempDir(), "orreryd.sock"),
		sessionCap: defaultSessionCap,
		stepCap:    defaultStepCap,
	}
	host, err := startSessions(
		ctx, opts, plannerWiringHost, st, &recordingRouter{}, slog.New(slog.DiscardHandler), getenv,
	)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}

	row := waitParked(t, st, plannerWiringEpic)
	if !strings.Contains(row.Parked, "no role file") {
		t.Errorf("parked = %q, want the reason %q", row.Parked, "no role file")
	}
	if len(row.Turns) != 0 {
		t.Errorf("turns = %d, want none: the epic parks before a turn is claimed", len(row.Turns))
	}

	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// waitParked reads epic from st until it is parked or plannerWiringWait has passed, and returns the last read.
func waitParked(t *testing.T, st *store.Store, epic string) store.EpicRow {
	t.Helper()
	deadline := time.Now().Add(plannerWiringWait)
	for {
		row, err := st.Epic(t.Context(), epic)
		if err != nil {
			t.Fatalf("read epic %s: %v", epic, err)
		}
		if row.Parked != "" || time.Now().After(deadline) {
			return row
		}
		time.Sleep(plannerWiringPoll)
	}
}
