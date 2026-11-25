package store

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// TestGateTriageRecordedAndRead checks that a recorded triage is read back by GateTriages and EpicFlow with the second
// it was recorded at, in the order they were recorded, and that a repeat for a check already triaged writes nothing.
func TestGateTriageRecordedAndRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	const path = "plan/S1/integrate"
	environment := GateTriage{
		Check: "check-2", Path: path, Step: "step-2", Verdict: "environment",
		Evidence: "load 139.20 121.48 131.33, nproc 32", Reason: "the host was loaded",
	}
	change := GateTriage{
		Check: "check-1", Path: path, Step: "step-1", Verdict: "change", Citation: "--- FAIL: TestX",
		Reason: "the batch broke TestX",
	}
	if err := s.RecordGateTriage(ctx, integrateEpic, environment, integrateStartedAt()); err != nil {
		t.Fatalf("RecordGateTriage of check-2: %v", err)
	}
	if err := s.RecordGateTriage(ctx, integrateEpic, change, integrateEndedAt()); err != nil {
		t.Fatalf("RecordGateTriage of check-1: %v", err)
	}
	environment.Created, change.Created = integrateStartedAt(), integrateEndedAt()
	want := []GateTriage{environment, change}
	requireGateTriages(t, s, want)

	repeat := environment
	repeat.Verdict, repeat.Reason = "change", "a second account"
	if err := s.RecordGateTriage(ctx, integrateEpic, repeat, integrateParkedAt()); err != nil {
		t.Fatalf("repeat RecordGateTriage: %v", err)
	}
	requireGateTriages(t, s, want)
}

// TestGateTriageRefusesAnUnknownVerdict checks that the store holds a triage only with a verdict of environment, flake
// or change.
func TestGateTriageRefusesAnUnknownVerdict(t *testing.T) {
	t.Parallel()
	s := openIntegrateStore(t)
	err := s.RecordGateTriage(context.Background(), integrateEpic, GateTriage{
		Check: "check-1", Path: "plan/S1/integrate", Step: "step-1", Verdict: "maybe",
	}, integrateStartedAt())
	if err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Errorf("RecordGateTriage with verdict maybe = %v, want the CHECK constraint to refuse it", err)
	}
}

// requireGateTriages fails unless GateTriages and EpicFlow both read want as the gate triages of integrateEpic.
func requireGateTriages(t *testing.T, s *Store, want []GateTriage) {
	t.Helper()
	ctx := context.Background()
	got, err := s.GateTriages(ctx, integrateEpic)
	if err != nil {
		t.Fatalf("GateTriages: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GateTriages = %+v, want %+v", got, want)
	}
	flow, err := s.EpicFlow(ctx, integrateEpic)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	if !reflect.DeepEqual(flow.Triages, want) {
		t.Errorf("EpicFlow triages = %+v, want %+v", flow.Triages, want)
	}
}
