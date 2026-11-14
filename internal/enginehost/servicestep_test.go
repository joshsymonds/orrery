package enginehost

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/store"
)

// stepIDs is a StepCommands whose Status treats a listed id as a step and every other id as ErrNoStep. A non-nil
// statusErr fails every Status call instead. The engine service calls only Status, so the other StepCommands methods
// are left unimplemented by the embedded nil interface.
type stepIDs struct {
	StepCommands
	mu        sync.Mutex
	steps     map[string]bool
	statusErr error
}

func (f *stepIDs) Status(_ context.Context, id string) (store.StepRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statusErr != nil {
		return store.StepRow{}, f.statusErr
	}
	if f.steps[id] {
		return store.StepRow{ID: id}, nil
	}
	return store.StepRow{}, fmt.Errorf("step %s: %w", id, store.ErrNoStep)
}

func TestEngineRefusesStepIDsWithoutReachingCommands(t *testing.T) {
	t.Parallel()
	engine := &fakeCommands{}
	path, stop := startServerWithSteps(t, engine, &stepIDs{steps: map[string]bool{"s1": true}})
	errs := callAll(t, newClient(t, path))
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	// Start and Seen are not refused: callAll gives Start no id and Seen the step id s1.
	for _, i := range []int{0, 5} {
		if errs[i] != nil {
			t.Fatalf("RPC %d on a step id returned %v, want success", i, errs[i])
		}
	}
	for _, i := range []int{1, 2, 3, 4} {
		err := errs[i]
		if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
			t.Fatalf("RPC %d code = %v, want %v (error %v)", i, got, connect.CodeFailedPrecondition, err)
		}
		var connectErr *connect.Error
		if !errors.As(err, &connectErr) {
			t.Fatalf("RPC %d error %T is not a connect error", i, err)
		}
		msg := connectErr.Message()
		if !strings.Contains(msg, "s1") || !strings.Contains(msg, "Steps") {
			t.Fatalf("RPC %d message = %q, want the id s1 and a pointer to the Steps service", i, msg)
		}
	}

	want := []call{
		{method: "Start", dir: "/work", prompt: "hello"},
		{method: "Seen", id: "s1"},
	}
	if got := engine.recorded(); !slices.Equal(got, want) {
		t.Fatalf("commands received %+v, want %+v", got, want)
	}
}

func TestEngineForwardsNonStepIDsAsBefore(t *testing.T) {
	t.Parallel()
	engine := &fakeCommands{startID: "0123456789abcdef0123456789abcdef"}
	path, stop := startServerWithSteps(t, engine, &stepIDs{})
	for i, err := range callAll(t, newClient(t, path)) {
		if err != nil {
			t.Fatalf("RPC %d on a non-step id returned %v, want success", i, err)
		}
	}
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	want := []call{
		{method: "Start", dir: "/work", prompt: "hello"},
		{method: "Say", id: "s1", text: "more"},
		{method: "Answer", id: "s1", requestID: "r1", answer: "allow"},
		{method: "Interrupt", id: "s1"},
		{method: "Stop", id: "s1"},
		{method: "Seen", id: "s1"},
	}
	if got := engine.recorded(); !slices.Equal(got, want) {
		t.Fatalf("commands received %+v, want %+v", got, want)
	}
}

func TestEngineStatusFailureIsInternalAndReachesNoCommand(t *testing.T) {
	t.Parallel()
	engine := &fakeCommands{}
	path, stop := startServerWithSteps(t, engine, &stepIDs{statusErr: errors.New("disk on fire")})
	errs := callAll(t, newClient(t, path))
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	for _, i := range []int{1, 2, 3, 4} {
		err := errs[i]
		if got := connect.CodeOf(err); got != connect.CodeInternal {
			t.Fatalf("RPC %d code = %v, want %v (error %v)", i, got, connect.CodeInternal, err)
		}
		var connectErr *connect.Error
		if !errors.As(err, &connectErr) {
			t.Fatalf("RPC %d error %T is not a connect error", i, err)
		}
		if got := connectErr.Message(); got != "disk on fire" {
			t.Fatalf("RPC %d message = %q, want %q", i, got, "disk on fire")
		}
	}

	want := []call{
		{method: "Start", dir: "/work", prompt: "hello"},
		{method: "Seen", id: "s1"},
	}
	if got := engine.recorded(); !slices.Equal(got, want) {
		t.Fatalf("commands received %+v, want %+v", got, want)
	}
}
