package enginehost

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// stopRecorder is a StepCommands that records each Stop. Its other methods come from the nil embedded StepCommands, so
// a call to one panics and fails the test: an epic control only stops steps.
type stopRecorder struct {
	StepCommands

	mu    sync.Mutex
	stops []string
}

// Stop records id.
func (r *stopRecorder) Stop(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stops = append(r.stops, id)
	return nil
}

// stopped returns a copy of the ids Stop was called with, in order.
func (r *stopRecorder) stopped() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.stops)
}

// startControlServer serves epics with steps on a sessions socket in a temporary directory and returns the socket's
// path. The server stops at cleanup.
func startControlServer(t *testing.T, epics EpicCommands, steps StepCommands) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.sock")
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, nil, steps, epics, nil) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve after cleanup: %v", err)
		}
	})
	return path
}

func TestEpicPauseStoresPauseAndRefusesSecond(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	client := newEpicsClient(t, startEpicServer(t, epicTestStore(t)))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	paused, err := client.PauseEpic(ctx, connect.NewRequest(&v1.PauseEpicRequest{Id: epicServiceID}))
	if err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}
	if !paused.Msg.GetEpic().GetPaused() {
		t.Fatalf("paused = false in the PauseEpic reply, want true")
	}
	if got := getEpicOK(t, client); !proto.Equal(got, paused.Msg.GetEpic()) {
		t.Fatalf("GetEpic = %v, want the paused epic %v", got, paused.Msg.GetEpic())
	}

	_, err = client.PauseEpic(ctx, connect.NewRequest(&v1.PauseEpicRequest{Id: epicServiceID}))
	wantConnectError(t, err, connect.CodeFailedPrecondition, "epic "+epicServiceID+" is already paused")
	if got := getEpicOK(t, client); !proto.Equal(got, paused.Msg.GetEpic()) {
		t.Fatalf("GetEpic after the refused PauseEpic = %v, want unchanged %v", got, paused.Msg.GetEpic())
	}
}

func TestEpicResumeClearsPauseAndKeepsPark(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	resume := connect.NewRequest(&v1.ResumeEpicRequest{Id: epicServiceID})

	_, err := client.ResumeEpic(ctx, resume)
	wantConnectError(t, err, connect.CodeFailedPrecondition, "epic "+epicServiceID+" is not paused")

	turn, err := st.ClaimTurn(ctx, epicServiceID, time.Unix(turnStarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	free := store.TurnFree{Park: "needs a human"}
	if err = st.FreeTurn(ctx, epicServiceID, turn.Seq, free, time.Unix(turnFreed, 0)); err != nil {
		t.Fatalf("FreeTurn: %v", err)
	}
	_, err = client.ResumeEpic(ctx, resume)
	wantConnectError(t, err, connect.CodeFailedPrecondition,
		"epic "+epicServiceID+" is parked, not paused; retry ends a park")

	if _, err = client.PauseEpic(ctx, connect.NewRequest(&v1.PauseEpicRequest{Id: epicServiceID})); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}
	resumed, err := client.ResumeEpic(ctx, resume)
	if err != nil {
		t.Fatalf("ResumeEpic: %v", err)
	}
	if resumed.Msg.GetEpic().GetPaused() {
		t.Fatalf("paused = true in the ResumeEpic reply, want false")
	}
	if resumed.Msg.GetEpic().GetParked() != "needs a human" {
		t.Fatalf("parked = %q after ResumeEpic, want the park kept", resumed.Msg.GetEpic().GetParked())
	}
	if got := getEpicOK(t, client); !proto.Equal(got, resumed.Msg.GetEpic()) {
		t.Fatalf("GetEpic = %v, want the resumed epic %v", got, resumed.Msg.GetEpic())
	}
}

func TestEpicStopPausesRunningTurnAndStopsItsStep(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, _ := openPlannerStore(t)
	steps := &stopRecorder{}
	client := newEpicsClient(t, startControlServer(t, st, steps))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	turn, err := st.ClaimTurn(ctx, epicServiceID, time.Unix(turnStarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = st.SetTurnStep(ctx, epicServiceID, turn.Seq, "step-1"); err != nil {
		t.Fatalf("SetTurnStep: %v", err)
	}

	stopped, err := client.StopEpic(ctx, connect.NewRequest(&v1.StopEpicRequest{Id: epicServiceID}))
	if err != nil {
		t.Fatalf("StopEpic: %v", err)
	}
	if !stopped.Msg.GetEpic().GetPaused() {
		t.Fatalf("paused = false in the StopEpic reply, want true")
	}
	if got := getEpicOK(t, client); !proto.Equal(got, stopped.Msg.GetEpic()) {
		t.Fatalf("GetEpic = %v, want the stopped epic %v", got, stopped.Msg.GetEpic())
	}
	if got := steps.stopped(); !slices.Equal(got, []string{"step-1"}) {
		t.Fatalf("steps stopped = %v, want [step-1]", got)
	}
	flagged, err := st.TurnStopped(ctx, epicServiceID, turn.Seq)
	if err != nil {
		t.Fatalf("TurnStopped: %v", err)
	}
	if !flagged {
		t.Fatalf("turn %d is not flagged to stop", turn.Seq)
	}
}

func TestEpicStopWithoutRunningTurnRefusesAndStopsNothing(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, _ := openPlannerStore(t)
	steps := &stopRecorder{}
	client := newEpicsClient(t, startControlServer(t, st, steps))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	_, err := client.StopEpic(ctx, connect.NewRequest(&v1.StopEpicRequest{Id: epicServiceID}))
	wantConnectError(t, err, connect.CodeFailedPrecondition, "epic "+epicServiceID+" has no running turn")
	if got := steps.stopped(); len(got) != 0 {
		t.Fatalf("steps stopped = %v, want none", got)
	}
	if getEpicOK(t, client).GetPaused() {
		t.Fatalf("paused = true after the refused StopEpic, want false")
	}
}

func TestEpicRetryEndsParkAndRefusesUnparked(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	_, err := client.RetryEpic(ctx, connect.NewRequest(&v1.RetryEpicRequest{Id: epicServiceID}))
	wantConnectError(t, err, connect.CodeFailedPrecondition, "epic "+epicServiceID+" is not parked")

	turn, err := st.ClaimTurn(ctx, epicServiceID, time.Unix(turnStarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	free := store.TurnFree{Park: "needs a human"}
	if err = st.FreeTurn(ctx, epicServiceID, turn.Seq, free, time.Unix(turnFreed, 0)); err != nil {
		t.Fatalf("FreeTurn: %v", err)
	}
	retried, err := client.RetryEpic(ctx, connect.NewRequest(&v1.RetryEpicRequest{Id: epicServiceID}))
	if err != nil {
		t.Fatalf("RetryEpic: %v", err)
	}
	if retried.Msg.GetEpic().GetParked() != "" {
		t.Fatalf("parked = %q in the RetryEpic reply, want empty", retried.Msg.GetEpic().GetParked())
	}
	if got := getEpicOK(t, client); !proto.Equal(got, retried.Msg.GetEpic()) {
		t.Fatalf("GetEpic = %v, want the retried epic %v", got, retried.Msg.GetEpic())
	}
}

func TestEpicControlsRefuseEmptyAndUnknownID(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	client := newEpicsClient(t, startEpicServer(t, epicTestStore(t)))
	controls := []struct {
		name string
		call func(id string) error
	}{
		{name: "PauseEpic", call: func(id string) error {
			_, err := client.PauseEpic(ctx, connect.NewRequest(&v1.PauseEpicRequest{Id: id}))
			return err
		}},
		{name: "ResumeEpic", call: func(id string) error {
			_, err := client.ResumeEpic(ctx, connect.NewRequest(&v1.ResumeEpicRequest{Id: id}))
			return err
		}},
		{name: "StopEpic", call: func(id string) error {
			_, err := client.StopEpic(ctx, connect.NewRequest(&v1.StopEpicRequest{Id: id}))
			return err
		}},
		{name: "RetryEpic", call: func(id string) error {
			_, err := client.RetryEpic(ctx, connect.NewRequest(&v1.RetryEpicRequest{Id: id}))
			return err
		}},
	}
	for _, control := range controls {
		t.Run(control.name, func(t *testing.T) {
			t.Parallel()
			wantConnectError(t, control.call(""), connect.CodeInvalidArgument, "id: empty")
			wantConnectError(t, control.call(epicServiceID), connect.CodeNotFound, "id: no epic "+epicServiceID)
		})
	}
}
