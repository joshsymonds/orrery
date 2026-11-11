package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func stepAddOf(id string) StepAdd {
	return StepAdd{
		ID: id, Agent: "implementer", Prompt: "do it", Schema: `{"type":"object"}`,
		Workspace: "/ws/" + id, Revision: "abc123", Worktree: "/wt/" + id, DoneValue: "DONE",
		ReadOnly: true, Owned: []string{"a.go", "b.go"}, Gate: []string{"go test ./..."},
		GateLimit: 90 * time.Second,
	}
}

func TestStepCheckJSONNames(t *testing.T) {
	raw, err := json.Marshal(StepCheck{Round: 2, Name: "head", OK: true, Detail: "ok"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if got, want := string(raw), `{"round":2,"name":"head","ok":true,"detail":"ok"}`; got != want {
		t.Fatalf("StepCheck JSON: got %s, want %s", got, want)
	}
}

func TestStepAddStoresEveryFieldWithEndEmpty(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	now := engineNowAt()
	if err := s.ApplyEngine(ctx, []EngineOp{stepAddOf("s1")}, now); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	got, err := s.Step(ctx, "s1")
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	want := StepRow{
		ID: "s1", Agent: "implementer", Prompt: "do it", Schema: `{"type":"object"}`,
		Workspace: "/ws/s1", Revision: "abc123", Worktree: "/wt/s1", DoneValue: "DONE",
		ReadOnly: true, Owned: []string{"a.go", "b.go"}, Gate: []string{"go test ./..."},
		GateLimit: 90 * time.Second, Started: now, Checks: []StepCheck{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Step: got %+v, want %+v", got, want)
	}
}

func TestStepReturnKeepsLatestResultAndAllChecksInOrder(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	if err := s.ApplyEngine(ctx, []EngineOp{stepAddOf("s1")}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine add: %v", err)
	}

	first := []StepCheck{{Round: 1, Name: "head", OK: false, Detail: "moved"}}
	second := []StepCheck{{Round: 2, Name: "head", OK: true}, {Round: 2, Name: "owned", OK: true, Detail: "clean"}}
	if err := s.ApplyEngine(
		ctx,
		[]EngineOp{StepReturn{ID: "s1", Result: "first", Checks: first}},
		engineNowAt(),
	); err != nil {
		t.Fatalf("ApplyEngine first return: %v", err)
	}
	if err := s.ApplyEngine(
		ctx,
		[]EngineOp{StepReturn{ID: "s1", Result: "second", Checks: second}},
		engineNowAt(),
	); err != nil {
		t.Fatalf("ApplyEngine second return: %v", err)
	}

	got, err := s.Step(ctx, "s1")
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if got.Result != "second" {
		t.Fatalf("Result: got %q, want second", got.Result)
	}
	if want := append(append([]StepCheck{}, first...), second...); !reflect.DeepEqual(got.Checks, want) {
		t.Fatalf("Checks: got %+v, want %+v", got.Checks, want)
	}
	if !got.Ended.IsZero() || got.End != "" {
		t.Fatalf("open step: got End %q Ended %v, want empty and zero", got.End, got.Ended)
	}
}

func TestStepEndSetsFieldsAndOpenStepsOmitsIt(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := engineNowAt()
	if err := s.ApplyEngine(ctx, []EngineOp{stepAddOf("s1"), stepAddOf("s2")}, t0); err != nil {
		t.Fatalf("ApplyEngine add: %v", err)
	}

	open, err := s.OpenSteps(ctx)
	if err != nil {
		t.Fatalf("OpenSteps before end: %v", err)
	}
	if got := stepIDsOf(open); !reflect.DeepEqual(got, []string{"s1", "s2"}) {
		t.Fatalf("open before end: got %v, want [s1 s2]", got)
	}

	t1 := t0.Add(time.Minute)
	if err := s.ApplyEngine(
		ctx,
		[]EngineOp{StepEnd{ID: "s1", End: "done", Status: "passed", Reason: "gate green"}},
		t1,
	); err != nil {
		t.Fatalf("ApplyEngine end: %v", err)
	}

	got, err := s.Step(ctx, "s1")
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if got.End != "done" || got.Status != "passed" || got.Reason != "gate green" || !got.Ended.Equal(t1) {
		t.Fatalf("ended step: got End %q Status %q Reason %q Ended %v, want done passed gate green %v",
			got.End, got.Status, got.Reason, got.Ended, t1)
	}

	open, err = s.OpenSteps(ctx)
	if err != nil {
		t.Fatalf("OpenSteps after end: %v", err)
	}
	if got := stepIDsOf(open); !reflect.DeepEqual(got, []string{"s2"}) {
		t.Fatalf("open after end: got %v, want [s2]", got)
	}
}

func TestStepUnknownIDMatchesErrNoStep(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	if _, err := s.Step(ctx, "missing"); !errors.Is(err, ErrNoStep) {
		t.Fatalf("Step of unknown id: got %v, want ErrNoStep", err)
	}
	if err := s.ApplyEngine(
		ctx,
		[]EngineOp{StepReturn{ID: "missing", Result: "x"}},
		engineNowAt(),
	); !errors.Is(
		err,
		ErrNoStep,
	) {
		t.Fatalf("StepReturn of unknown id: got %v, want ErrNoStep", err)
	}
	if err := s.ApplyEngine(
		ctx,
		[]EngineOp{StepEnd{ID: "missing", End: "done"}},
		engineNowAt(),
	); !errors.Is(
		err,
		ErrNoStep,
	) {
		t.Fatalf("StepEnd of unknown id: got %v, want ErrNoStep", err)
	}
}

func TestStepOpsAppendNoEvent(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	ops := []EngineOp{
		stepAddOf("s1"),
		StepReturn{ID: "s1", Result: "r", Checks: []StepCheck{{Round: 1, Name: "gate", OK: true}}},
		StepEnd{ID: "s1", End: "done", Status: "passed"},
	}
	if err := s.ApplyEngine(ctx, ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	if events := eventsOf(t, s); len(events) != 0 {
		t.Fatalf("events after step ops: got %d, want 0", len(events))
	}
}

func TestStepRowSurvivesEventRetentionPrune(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := engineNowAt()
	ops := []EngineOp{
		stepAddOf("s1"),
		StepReturn{ID: "s1", Result: "kept", Checks: []StepCheck{{Round: 1, Name: "gate", OK: true, Detail: "green"}}},
		StepEnd{ID: "s1", End: "done", Status: "passed", Reason: "gate green"},
	}
	if err := s.ApplyEngine(ctx, ops, t0); err != nil {
		t.Fatalf("ApplyEngine step ops: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{EngineAdd{ID: "eng-a", Dir: "/a", State: "idle"}}, t0); err != nil {
		t.Fatalf("ApplyEngine eng-a: %v", err)
	}

	later := t0.Add(EventRetention + time.Hour)
	if err := s.ApplyEngine(ctx, []EngineOp{EngineAdd{ID: "eng-b", Dir: "/b", State: "idle"}}, later); err != nil {
		t.Fatalf("ApplyEngine eng-b: %v", err)
	}
	if err := s.SyncServer(ctx, "-L a", nil, later); err != nil {
		t.Fatalf("SyncServer past retention: %v", err)
	}
	if events := eventsOf(t, s); len(events) != 1 {
		t.Fatalf("events after prune: got %d, want 1 so the prune ran", len(events))
	}

	got, err := s.Step(ctx, "s1")
	if err != nil {
		t.Fatalf("Step after prune: %v", err)
	}
	if got.Result != "kept" || got.Status != "passed" || len(got.Checks) != 1 || got.Checks[0].Detail != "green" {
		t.Fatalf("step after prune: got Result %q Status %q Checks %+v, want kept, passed and one green check",
			got.Result, got.Status, got.Checks)
	}
}

func TestStepAndEngineAddInOneBatchBothLand(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	ops := []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		stepAddOf("s1"),
	}
	if err := s.ApplyEngine(ctx, ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	if got, want := engineIDsOf(t, s), []string{"e1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("engine sessions: got %v, want %v", got, want)
	}
	if _, err := s.Step(ctx, "s1"); err != nil {
		t.Fatalf("Step s1 from the same batch: %v", err)
	}
}

func stepIDsOf(rows []StepRow) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}
