package enginehost

import (
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// callerReadOwner is the session that starts the step the read tests end, and callerReadOther is a second session.
// callerReadStep is the id of that step.
const (
	callerReadOwner = "session-1"
	callerReadOther = "session-2"
	callerReadStep  = "step-1"
)

// storeCallerReadStep stores the sessions of the read tests and a running callerReadStep started by callerReadOwner.
func storeCallerReadStep(t *testing.T, s *store.Store) {
	t.Helper()
	at := time.Unix(1700003000, 0)
	sessions := []store.EngineOp{
		store.EngineAdd{ID: callerReadOwner, Dir: "/work", State: "idle"},
		store.EngineAdd{ID: callerReadOther, Dir: "/work", State: "idle"},
	}
	if err := s.ApplyEngine(t.Context(), sessions, at); err != nil {
		t.Fatalf("ApplyEngine add sessions: %v", err)
	}
	start := store.StepAdd{
		ID:     callerReadStep,
		Agent:  "reviewer",
		Prompt: "look",
		Caller: store.EpicCaller{Session: callerReadOwner},
	}
	if err := s.ApplyEngine(t.Context(), []store.EngineOp{start}, at); err != nil {
		t.Fatalf("ApplyEngine add step %s: %v", callerReadStep, err)
	}
}

// endCallerReadStep ends callerReadStep a minute after the start that storeCallerReadStep stored.
func endCallerReadStep(t *testing.T, s *store.Store) {
	t.Helper()
	end := store.StepEnd{ID: callerReadStep, End: "done", Status: "passed", Reason: "gate green"}
	if err := s.ApplyEngine(t.Context(), []store.EngineOp{end}, time.Unix(1700003060, 0)); err != nil {
		t.Fatalf("ApplyEngine end step %s: %v", callerReadStep, err)
	}
}

// callerReadListed reports whether CallerSteps still lists callerReadStep, so its end would be sent to its caller.
func callerReadListed(t *testing.T, s *store.Store) bool {
	t.Helper()
	steps, err := s.CallerSteps(t.Context())
	if err != nil {
		t.Fatalf("CallerSteps: %v", err)
	}
	return slices.ContainsFunc(steps, func(step store.CallerStep) bool { return step.Step.ID == callerReadStep })
}

// TestCallerEventReadStepStatusMarksEndedStepReadForItsCaller calls StepStatus over the Steps service with each case's
// caller header, and checks whether the step is still listed by CallerSteps afterwards.
func TestCallerEventReadStepStatusMarksEndedStepReadForItsCaller(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// ended is whether the step has ended before the call. A step that has not ended ends after it.
		ended bool
		// session is the Orrery-Session header of the call, empty for a call with no caller headers.
		session string
		// listed is whether CallerSteps still lists the step after the call.
		listed bool
	}{
		{name: "own caller on ended step", ended: true, session: callerReadOwner, listed: false},
		{name: "other session on ended step", ended: true, session: callerReadOther, listed: true},
		{name: "no caller header on ended step", ended: true, listed: true},
		{name: "own caller on running step", ended: false, session: callerReadOwner, listed: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newStepStartFixture(t)
			storeCallerReadStep(t, f.store)
			if tc.ended {
				endCallerReadStep(t, f.store)
			}
			path, _ := startServerWithSteps(t, nil, f.steps)
			client := newStepClient(t, path)

			req := connect.NewRequest(&v1.StepStatusRequest{Id: callerReadStep})
			if tc.session != "" {
				req.Header().Set(protocol.HeaderSession, tc.session)
			}
			if _, err := client.StepStatus(t.Context(), req); err != nil {
				t.Fatalf("StepStatus: %v", err)
			}
			if !tc.ended {
				// CallerSteps lists only ended steps, so a step that ran through the call ends after it.
				endCallerReadStep(t, f.store)
			}
			if got := callerReadListed(t, f.store); got != tc.listed {
				t.Fatalf("CallerSteps lists the step = %v, want %v", got, tc.listed)
			}
		})
	}
}

// TestCallerEventReadWaitStepsMarksEndedStepReadForItsCaller calls WaitSteps over the Steps service with each case's
// caller header, and checks whether the step is still listed by CallerSteps afterwards. A timeout of zero returns at
// once, so a running step is answered with no end.
func TestCallerEventReadWaitStepsMarksEndedStepReadForItsCaller(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// ended is whether the step has ended before the call. A step that has not ended ends after it.
		ended bool
		// session is the Orrery-Session header of the call, empty for a call with no caller headers.
		session string
		// listed is whether CallerSteps still lists the step after the call.
		listed bool
	}{
		{name: "own caller on ended step", ended: true, session: callerReadOwner, listed: false},
		{name: "other session on ended step", ended: true, session: callerReadOther, listed: true},
		{name: "no caller header on ended step", ended: true, listed: true},
		{name: "own caller on running step", ended: false, session: callerReadOwner, listed: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newStepStartFixture(t)
			storeCallerReadStep(t, f.store)
			if tc.ended {
				endCallerReadStep(t, f.store)
			}
			path, _ := startServerWithSteps(t, nil, f.steps)
			client := newStepClient(t, path)

			req := connect.NewRequest(&v1.WaitStepsRequest{Ids: []string{callerReadStep}})
			if tc.session != "" {
				req.Header().Set(protocol.HeaderSession, tc.session)
			}
			resp, err := client.WaitSteps(t.Context(), req)
			if err != nil {
				t.Fatalf("WaitSteps: %v", err)
			}
			if got := resp.Msg.GetEnded() != nil; got != tc.ended {
				t.Fatalf("WaitSteps answered an end = %v, want %v", got, tc.ended)
			}
			if !tc.ended {
				// CallerSteps lists only ended steps, so a step that ran through the call ends after it.
				endCallerReadStep(t, f.store)
			}
			if got := callerReadListed(t, f.store); got != tc.listed {
				t.Fatalf("CallerSteps lists the step = %v, want %v", got, tc.listed)
			}
		})
	}
}
