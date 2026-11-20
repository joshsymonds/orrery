package enginehost

import (
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// closeSteps returns a stopRecorder over the running steps the close tests list: step-1 and step-2 run, and step-3 ended.
// Its List answers the rows, and its Stop records each id it is asked to stop.
func closeSteps() *stopRecorder {
	return &stopRecorder{StepCommands: &resumeSteps{rows: []store.StepRow{
		{ID: "step-1", Epic: epicServiceID},
		{ID: "step-2", Epic: epicServiceID},
		{ID: "step-3", Epic: epicServiceID, End: "done"},
	}}}
}

// TestCloseEpicRPCStopsTheTurnStepAndRunningStepsAndReadsTheEpicBack closes a parked epic as shipped while its turn runs.
// The reply is the epic read back, closed as shipped, and the turn's step and the other running step are stopped, each
// once, while the ended step is left alone.
func TestCloseEpicRPCStopsTheTurnStepAndRunningStepsAndReadsTheEpicBack(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, _ := openPlannerStore(t)
	steps := closeSteps()
	client := newEpicsClient(t, startControlServer(t, st, steps))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	turn, err := st.ClaimTurn(ctx, epicServiceID, time.Unix(turnStarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = st.SetTurnStep(ctx, epicServiceID, turn.Seq, "step-1"); err != nil {
		t.Fatalf("SetTurnStep: %v", err)
	}

	closed, err := client.CloseEpic(ctx, connect.NewRequest(&v1.CloseEpicRequest{
		Id: epicServiceID, Kind: "shipped",
	}))
	if err != nil {
		t.Fatalf("CloseEpic: %v", err)
	}
	if got := closed.Msg.GetEpic().GetPhase(); got != store.PhaseClosedShipped {
		t.Fatalf("phase in the CloseEpic reply = %q, want %q", got, store.PhaseClosedShipped)
	}
	if got := getEpicOK(t, client); !proto.Equal(got, closed.Msg.GetEpic()) {
		t.Fatalf("GetEpic = %v, want the closed epic %v", got, closed.Msg.GetEpic())
	}
	if got := steps.stopped(); !slices.Equal(got, []string{"step-1", "step-2"}) {
		t.Fatalf("steps stopped = %v, want [step-1 step-2]", got)
	}
}

// TestCloseEpicRPCTakesThePlannerCloserFromTheTurnsStep closes two epics. A close from the session of an epic's running
// turn is the planner's, and the close the owner makes from no session is the owner's.
func TestCloseEpicRPCTakesThePlannerCloserFromTheTurnsStep(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, _ := openPlannerStore(t)
	const other = "synthetic-repo-0a1b2c3/epic-other"
	client := newEpicsClient(t, startControlServer(t, st, closeSteps()))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), other))
	turn, err := st.ClaimTurn(ctx, epicServiceID, time.Unix(turnStarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = st.SetTurnStep(ctx, epicServiceID, turn.Seq, "step-1"); err != nil {
		t.Fatalf("SetTurnStep: %v", err)
	}

	planner := connect.NewRequest(&v1.CloseEpicRequest{Id: epicServiceID, Kind: "abandoned"})
	planner.Header().Set(protocol.HeaderSession, "step-1")
	if _, err = client.CloseEpic(ctx, planner); err != nil {
		t.Fatalf("CloseEpic from the turn's step: %v", err)
	}
	if _, err = client.CloseEpic(
		ctx,
		connect.NewRequest(&v1.CloseEpicRequest{Id: other, Kind: "abandoned"}),
	); err != nil {
		t.Fatalf("CloseEpic from no session: %v", err)
	}
	for id, want := range map[string]string{epicServiceID: store.CloserPlanner, other: store.CloserOwner} {
		row, err := st.Epic(ctx, id)
		if err != nil {
			t.Fatalf("Epic %s: %v", id, err)
		}
		if row.ClosedBy != want {
			t.Errorf("closed by of %s = %q, want %q", id, row.ClosedBy, want)
		}
	}
}

// TestCloseEpicRPCRefusesSupersededWithoutByAndStopsNothing closes an epic whose turn runs as superseded without naming
// the epic that replaces it. The refusal is FailedPrecondition with the store's text, and nothing is stopped or closed.
func TestCloseEpicRPCRefusesSupersededWithoutByAndStopsNothing(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, _ := openPlannerStore(t)
	steps := closeSteps()
	client := newEpicsClient(t, startControlServer(t, st, steps))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	turn, err := st.ClaimTurn(ctx, epicServiceID, time.Unix(turnStarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = st.SetTurnStep(ctx, epicServiceID, turn.Seq, "step-1"); err != nil {
		t.Fatalf("SetTurnStep: %v", err)
	}

	_, err = client.CloseEpic(ctx, connect.NewRequest(&v1.CloseEpicRequest{Id: epicServiceID, Kind: "superseded"}))
	wantConnectError(t, err, connect.CodeFailedPrecondition,
		"epic "+epicServiceID+" is superseded and names no epic that replaces it")
	if got := steps.stopped(); len(got) != 0 {
		t.Fatalf("steps stopped = %v, want none", got)
	}
	if got := getEpicOK(t, client).GetPhase(); got != epicPhaseAccepted {
		t.Fatalf("phase after the refused CloseEpic = %q, want %q", got, epicPhaseAccepted)
	}
}
