package planner

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// claimRunnerTurn claims the runner epic's turn 1 and records step-1 as its step.
func claimRunnerTurn(t *testing.T, s *store.Store) store.EpicTurn {
	t.Helper()
	ctx := context.Background()
	turn, err := s.ClaimTurn(ctx, runnerEpicID, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.SetTurnStep(ctx, runnerEpicID, turn.Seq, "step-1"); err != nil {
		t.Fatalf("SetTurnStep: %v", err)
	}
	return turn
}

func TestReadoptCheckRestoresPlanCheckOfRunningTurnStep(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	claimRunnerTurn(t, s)

	check, err := ReadoptCheck(s, testResolver{})(context.Background(), store.StepRow{ID: "step-1"})
	if err != nil {
		t.Fatalf("ReadoptCheck: %v", err)
	}
	if check == nil {
		t.Fatal("ReadoptCheck of the running turn's step = nil, want the plan check")
	}
	want := []string{`tasks[0] "Build it" requirements[0]: not a Requirement of the epic`}
	if got := check(json.RawMessage(unknownRequirementPlan)); !reflect.DeepEqual(got, want) {
		t.Errorf("check of a plan naming R9 = %q, want %q", got, want)
	}
	if got := check(json.RawMessage(validPlan)); got != nil {
		t.Errorf("check of a valid plan = %q, want none", got)
	}
}

func TestReadoptCheckRefusesUnknownGate(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	claimRunnerTurn(t, s)

	check, err := ReadoptCheck(s, testResolver{})(context.Background(), store.StepRow{ID: "step-1"})
	if err != nil {
		t.Fatalf("ReadoptCheck: %v", err)
	}
	if check == nil {
		t.Fatal("ReadoptCheck of the running turn's step = nil, want the plan check")
	}
	want := []string{`tasks[0] "Build it" gate: resolve gate: unknown gate: "missing"`}
	if got := check(json.RawMessage(unknownGatePlan)); !reflect.DeepEqual(got, want) {
		t.Errorf("check of a plan naming gate missing = %q, want %q", got, want)
	}
}

func TestReadoptCheckIsNilForRowOfNoRunningTurn(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	claimRunnerTurn(t, s)

	check, err := ReadoptCheck(s, testResolver{})(context.Background(), store.StepRow{ID: "step-2"})
	if err != nil {
		t.Fatalf("ReadoptCheck of a row of no running turn: %v", err)
	}
	if check != nil {
		t.Errorf("ReadoptCheck of a row of no running turn is not nil")
	}
}

func TestReadoptCheckIsNilForStepOfFailedTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn := claimRunnerTurn(t, s)
	if err := s.FailTurn(ctx, runnerEpicID, turn.Seq, "step failed", claimedAt()); err != nil {
		t.Fatalf("FailTurn: %v", err)
	}

	check, err := ReadoptCheck(s, testResolver{})(ctx, store.StepRow{ID: "step-1"})
	if err != nil {
		t.Fatalf("ReadoptCheck of a failed turn's step: %v", err)
	}
	if check != nil {
		t.Errorf("ReadoptCheck of a failed turn's step is not nil")
	}
}

// readoptFailStore is a Store whose running turns and epic read fail with the errors set.
type readoptFailStore struct {
	Store
	turns    []store.EpicTurn
	turnsErr error
	epicErr  error
}

func (f readoptFailStore) RunningTurns(context.Context) ([]store.EpicTurn, error) {
	return f.turns, f.turnsErr
}

func (f readoptFailStore) Epic(context.Context, string) (store.EpicRow, error) {
	return store.EpicRow{}, f.epicErr
}

func TestReadoptCheckReturnsStoreErrors(t *testing.T) {
	t.Parallel()
	errTurns := errors.New("list running turns failed")
	errEpic := errors.New("read epic failed")
	running := []store.EpicTurn{{Epic: runnerEpicID, Seq: 1, Step: "step-1"}}
	cases := []struct {
		name  string
		store readoptFailStore
		want  error
	}{
		{name: "running turns", store: readoptFailStore{turnsErr: errTurns}, want: errTurns},
		{name: "epic", store: readoptFailStore{turns: running, epicErr: errEpic}, want: errEpic},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			check, err := ReadoptCheck(tc.store, testResolver{})(context.Background(), store.StepRow{ID: "step-1"})
			if !errors.Is(err, tc.want) {
				t.Errorf("ReadoptCheck error = %v, want %v", err, tc.want)
			}
			if check != nil {
				t.Errorf("ReadoptCheck with a store error returned a check, want nil")
			}
		})
	}
}
