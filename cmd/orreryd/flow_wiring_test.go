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
	// The control epic runs its red and cost at a scratch repository's base, so it reaches its plan. The synthetic epic's
	// cost ends in its fixture, and it keeps the synthetic repository with no base, where its review waits as it did.
	repo, base := buildWiringRepo(t)
	createFlowWiringEpic(t, st, flowWiringEpic, flowWiringRepo, "", created)
	createFlowWiringEpic(t, st, flowWiringControl, repo, base, created)
	endFlowWiringPlan(t, st, flowWiringEpic, created)
	read, err := st.EpicFlow(ctx, flowWiringEpic)
	if err != nil {
		t.Fatalf("read epic flow: %v", err)
	}
	if read.Pending != 1 {
		t.Fatalf("pending triggers = %d, want 1: the synthetic epic's trigger is pending", read.Pending)
	}

	opts := options{
		socket: filepath.Join(t.TempDir(), "orreryd.sock"),
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

// createFlowWiringEpic creates the synthetic accepted epic id over repo at base, with one requirement, as the planner
// wiring test does.
func createFlowWiringEpic(t *testing.T, st *store.Store, id, repo, base string, created time.Time) {
	t.Helper()
	if err := st.CreateEpic(t.Context(), store.EpicRow{
		ID:          id,
		Repository:  repo,
		Base:        base,
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

// endFlowWiringPlan starts the plan run of epic and ends it complete, as a plan whose turns finished leaves it. The red
// and cost runs before it end passed first, since the plan starts only once they have.
func endFlowWiringPlan(t *testing.T, st *store.Store, epic string, at time.Time) {
	t.Helper()
	ctx := t.Context()
	nodes := []struct{ path, kind, outcome string }{
		{"red", "evidence", "passed"}, {"cost", "cost", "passed"}, {"plan", "plan", "complete"},
	}
	runs := make([]store.NodeRun, 0, len(nodes))
	ends := make([]store.NodeEnd, 0, len(nodes))
	for _, node := range nodes {
		runs = append(runs, store.NodeRun{
			Path: node.path, ID: store.NodeRunID(epic, node.path, 0, "start"), Node: node.path, Kind: node.kind,
			Started: at,
		})
		ends = append(ends, store.NodeEnd{Path: node.path, Outcome: node.outcome})
	}
	if err := st.StartNodes(ctx, epic, runs, nil, at); err != nil {
		t.Fatalf("start node runs of %s: %v", epic, err)
	}
	if err := st.EndNodes(ctx, epic, ends, at); err != nil {
		t.Fatalf("end node runs of %s: %v", epic, err)
	}
	read, err := st.NodeRuns(ctx, epic)
	if err != nil {
		t.Fatalf("read node runs of %s: %v", epic, err)
	}
	if len(read) != len(nodes) {
		t.Fatalf("node runs of %s = %+v, want %d runs", epic, read, len(nodes))
	}
	byPath := make(map[string]store.NodeRun, len(read))
	for _, run := range read {
		byPath[run.Path] = run
	}
	for _, node := range nodes {
		if run := byPath[node.path]; run.State != store.NodeEnded || run.Outcome != node.outcome {
			t.Fatalf("node run %s of %s = %+v, want ended %s", node.path, epic, run, node.outcome)
		}
	}
}
