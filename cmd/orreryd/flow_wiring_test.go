package main

import (
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic values only. The epics and their texts are invented, and no role file exists, so a planner turn can only
// park its epic.
const (
	flowWiringEpic    = "orrery-7c8d9e0/flow-completed"
	flowWiringControl = "orrery-7c8d9e0/flow-control"
	flowWiringRepo    = "/work/synthetic-repo"
	flowWiringHost    = "synthetic-host"
)

// TestSessionsHostLeavesCompletedEpicUnparked checks that a --sessions orreryd runs every epic through the interpreter.
// The synthetic epic's plan run has ended complete and its trigger is pending, so the interpreter gives its plan no
// turn: the epic stays unparked with no turn claimed. The control epic has no plan run, so the interpreter starts one
// and passes the planner for it, and the planner parks it for the missing role file. The planner is passed only after
// every epic is decided, so the control's park shows the pass covered the synthetic epic too. The host then stops
// without error.
func TestSessionsHostLeavesCompletedEpicUnparked(t *testing.T) {
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
	createFlowWiringEpic(t, st, flowWiringEpic, created)
	createFlowWiringEpic(t, st, flowWiringControl, created)
	endFlowWiringPlan(t, st, flowWiringEpic, created)
	read, err := st.EpicFlow(ctx, flowWiringEpic)
	if err != nil {
		t.Fatalf("read epic flow: %v", err)
	}
	if read.Pending != 1 {
		t.Fatalf("pending triggers = %d, want 1: the synthetic epic's trigger is pending", read.Pending)
	}

	opts := options{
		socket:     filepath.Join(t.TempDir(), "orreryd.sock"),
		sessionCap: defaultSessionCap,
		stepCap:    defaultStepCap,
	}
	host, err := startSessions(
		ctx, opts, flowWiringHost, st, &recordingRouter{}, nil, slog.New(slog.DiscardHandler), getenv,
	)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}

	waitParked(t, st, host, flowWiringControl)
	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}

	row, err := st.Epic(ctx, flowWiringEpic)
	if err != nil {
		t.Fatalf("read epic: %v", err)
	}
	if row.Parked != "" {
		t.Errorf("parked = %q, want the epic unparked: its plan run is complete", row.Parked)
	}
	if len(row.Turns) != 0 {
		t.Errorf("turns = %d, want 0: a complete plan run claims no planner turn", len(row.Turns))
	}
}

// createFlowWiringEpic creates the synthetic accepted epic id with one requirement, as the planner wiring test does.
func createFlowWiringEpic(t *testing.T, st *store.Store, id string, created time.Time) {
	t.Helper()
	if err := st.CreateEpic(t.Context(), store.EpicRow{
		ID:          id,
		Repository:  flowWiringRepo,
		Title:       "Run the flow",
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
			{ID: "R1", Text: "the flow runs", Evidence: "wiring test", Kind: "test"},
		},
	}); err != nil {
		t.Fatalf("create epic %s: %v", id, err)
	}
}

// endFlowWiringPlan starts the plan run of epic and ends it complete, as a plan whose turns finished leaves it.
func endFlowWiringPlan(t *testing.T, st *store.Store, epic string, at time.Time) {
	t.Helper()
	ctx := t.Context()
	run := store.NodeRun{
		Path: "plan", ID: store.NodeRunID(epic, "plan", 0, "start"), Node: "plan", Kind: "plan", Started: at,
	}
	if err := st.StartNodes(ctx, epic, []store.NodeRun{run}, nil, at); err != nil {
		t.Fatalf("start plan run of %s: %v", epic, err)
	}
	if err := st.EndNodes(ctx, epic, []store.NodeEnd{{Path: "plan", Outcome: "complete"}}, at); err != nil {
		t.Fatalf("end plan run of %s: %v", epic, err)
	}
	runs, err := st.NodeRuns(ctx, epic)
	if err != nil {
		t.Fatalf("read node runs of %s: %v", epic, err)
	}
	if len(runs) != 1 || runs[0].State != store.NodeEnded || runs[0].Outcome != "complete" {
		t.Fatalf("node runs of %s = %+v, want one plan run ended complete", epic, runs)
	}
}
