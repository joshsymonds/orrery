package enginehost

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// publicHostsSteps is a StepCommands whose listings are read from st, so each listed step carries the host its stored
// row holds. Its other methods are fakeStepCommands'.
type publicHostsSteps struct {
	*fakeStepCommands
	st *store.Store
}

func (s publicHostsSteps) Listings(ctx context.Context, epic, task string) ([]store.StepListing, error) {
	return s.st.StepListings(ctx, epic, task)
}

// TestPublicHostsEngineEpicAndStep covers the host an epic and its step carry from the store: an epic stored on host b,
// with one of its steps, answers host b from GetEpic, from ListEpics and from ListSteps.
func TestPublicHostsEngineEpicAndStep(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	created := time.Unix(1700000000, 0)
	if err := st.CreateEpic(t.Context(), store.EpicRow{
		ID: epicServiceID, Repository: "synthetic-repo-0a1b2c3", Host: "b", Created: created, Changed: created,
	}); err != nil {
		t.Fatalf("CreateEpic %s: %v", epicServiceID, err)
	}
	const stepID = "public-hosts-step"
	// The step names no host, so it takes its epic's stored host.
	add := store.StepAdd{
		ID: stepID, Agent: "implementer", Prompt: "do it", Epic: epicServiceID, Task: "public-hosts-task",
	}
	if err := st.ApplyEngine(t.Context(), []store.EngineOp{add}, fixedClock()); err != nil {
		t.Fatalf("ApplyEngine step %s: %v", stepID, err)
	}
	epics := &epicService{epics: st, steering: fixedSteering{}, host: "a"}
	steps := &stepService{steps: publicHostsSteps{fakeStepCommands: &fakeStepCommands{}, st: st}}

	got, err := epics.GetEpic(t.Context(), connect.NewRequest(&v1.GetEpicRequest{Id: epicServiceID}))
	if err != nil {
		t.Fatalf("GetEpic %s: %v", epicServiceID, err)
	}
	if host := got.Msg.GetEpic().GetHost(); host != "b" {
		t.Errorf("GetEpic host = %q, want b", host)
	}

	listed, err := epics.ListEpics(t.Context(), connect.NewRequest(&v1.ListEpicsRequest{}))
	if err != nil {
		t.Fatalf("ListEpics: %v", err)
	}
	listedHost, found := "", false
	for _, summary := range listed.Msg.GetEpics() {
		if summary.GetId() == epicServiceID {
			listedHost, found = summary.GetHost(), true
		}
	}
	if !found {
		t.Fatalf("ListEpics answered no epic %s", epicServiceID)
	}
	if listedHost != "b" {
		t.Errorf("ListEpics host of %s = %q, want b", epicServiceID, listedHost)
	}

	listedSteps, err := steps.ListSteps(t.Context(), connect.NewRequest(&v1.ListStepsRequest{Epic: epicServiceID}))
	if err != nil {
		t.Fatalf("ListSteps %s: %v", epicServiceID, err)
	}
	if n := len(listedSteps.Msg.GetSteps()); n != 1 {
		t.Fatalf("ListSteps answered %d steps, want 1", n)
	}
	answered := listedSteps.Msg.GetSteps()[0]
	if answered.GetId() != stepID || answered.GetHost() != "b" {
		t.Errorf("ListSteps answered step %s on host %q, want %s on host b",
			answered.GetId(), answered.GetHost(), stepID)
	}
}
