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
		GateLimit: 90 * time.Second, ModelLimit: 60 * time.Minute,
	}
}

func TestStepVerdictJSONNames(t *testing.T) {
	raw, err := json.Marshal(StepVerdict{
		At: 1700000000, Kind: "monitor", Trip: "silent", Round: 0, Verdict: "looping",
		Detail: "editing", Error: "", Action: "nudge",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"at":1700000000,"kind":"monitor","trip":"silent","round":0,"verdict":"looping",` +
		`"detail":"editing","error":"","action":"nudge"}`
	if got := string(raw); got != want {
		t.Fatalf("StepVerdict JSON: got %s, want %s", got, want)
	}
}

func TestStepWatchAppendsVerdictsInOrderAndKeepsDoing(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := engineNowAt()
	if err := s.ApplyEngine(ctx, []EngineOp{stepAddOf("s1")}, t0); err != nil {
		t.Fatalf("ApplyEngine add: %v", err)
	}
	fresh, err := s.Step(ctx, "s1")
	if err != nil {
		t.Fatalf("Step after add: %v", err)
	}
	if fresh.Doing != "" || len(fresh.Verdicts) != 0 || fresh.ModelLimit != 60*time.Minute {
		t.Fatalf("new step: got Doing %q Verdicts %+v ModelLimit %v, want empty, none and 60m",
			fresh.Doing, fresh.Verdicts, fresh.ModelLimit)
	}

	t1 := t0.Add(time.Minute)
	first := StepVerdict{Kind: "monitor", Trip: "silent", Verdict: "looping", Detail: "editing", Action: "nudge"}
	if err := s.ApplyEngine(
		ctx,
		[]EngineOp{StepWatch{ID: "s1", Verdict: first, Doing: "running tests"}},
		t1,
	); err != nil {
		t.Fatalf("ApplyEngine first watch: %v", err)
	}

	t2 := t1.Add(time.Minute)
	second := StepVerdict{Kind: "judge", Round: 1, Verdict: "", Error: "timeout", Action: "continue"}
	if err := s.ApplyEngine(ctx, []EngineOp{StepWatch{ID: "s1", Verdict: second}}, t2); err != nil {
		t.Fatalf("ApplyEngine second watch: %v", err)
	}

	got, err := s.Step(ctx, "s1")
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	first.At = t1.Unix()
	second.At = t2.Unix()
	if want := []StepVerdict{first, second}; !reflect.DeepEqual(got.Verdicts, want) {
		t.Fatalf("Verdicts: got %+v, want %+v", got.Verdicts, want)
	}
	if got.Doing != "running tests" {
		t.Fatalf("Doing: got %q, want running tests kept by the op with no Doing", got.Doing)
	}
	if got.ModelLimit != 60*time.Minute || got.Result != "" || got.End != "" {
		t.Fatalf("other fields changed: ModelLimit %v Result %q End %q", got.ModelLimit, got.Result, got.End)
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
		GateLimit: 90 * time.Second, ModelLimit: 60 * time.Minute, Started: now, Checks: []StepCheck{},
		Verdicts: []StepVerdict{},
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
	if err := s.ApplyEngine(
		ctx,
		[]EngineOp{StepWatch{ID: "missing", Verdict: StepVerdict{Kind: "monitor"}, Doing: "x"}},
		engineNowAt(),
	); !errors.Is(
		err,
		ErrNoStep,
	) {
		t.Fatalf("StepWatch of unknown id: got %v, want ErrNoStep", err)
	}
}

func TestStepOpsAppendNoEvent(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	ops := []EngineOp{
		stepAddOf("s1"),
		StepReturn{ID: "s1", Result: "r", Checks: []StepCheck{{Round: 1, Name: "gate", OK: true}}},
		StepWatch{ID: "s1", Verdict: StepVerdict{Kind: "monitor", Verdict: "progressing", Action: "none"}, Doing: "x"},
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

func labelledStepOf(id, epic, task string) StepAdd {
	add := stepAddOf(id)
	add.Epic = epic
	add.Task = task
	return add
}

func TestStepAddStoresLabelsAndLabelledStepsReadsThem(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	now := engineNowAt()
	if err := s.ApplyEngine(ctx, []EngineOp{labelledStepOf("s1", "orrery-epic", "store-labels")}, now); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	got, err := s.Step(ctx, "s1")
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if got.Epic != "orrery-epic" || got.Task != "store-labels" {
		t.Fatalf("Step labels: got epic %q task %q, want orrery-epic and store-labels", got.Epic, got.Task)
	}

	labeled, err := s.LabelledSteps(ctx, "", "")
	if err != nil {
		t.Fatalf("LabelledSteps: %v", err)
	}
	if len(labeled) != 1 || labeled[0].ID != "s1" || labeled[0].Epic != "orrery-epic" ||
		labeled[0].Task != "store-labels" || !reflect.DeepEqual(labeled[0].Owned, []string{"a.go", "b.go"}) {
		t.Fatalf("LabelledSteps: got %+v, want the labeled s1 row with its labels and owned paths", labeled)
	}
}

func TestLabelledStepsLeavesOutUnlabelledRows(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	now := engineNowAt()
	if err := s.ApplyEngine(ctx, []EngineOp{stepAddOf("plain"), labelledStepOf("tagged", "e", "t")}, now); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	plain, err := s.Step(ctx, "plain")
	if err != nil {
		t.Fatalf("Step of unlabelled row: %v", err)
	}
	if plain.Epic != "" || plain.Task != "" {
		t.Fatalf("unlabelled row: got epic %q task %q, want empty and empty", plain.Epic, plain.Task)
	}

	labeled, err := s.LabelledSteps(ctx, "", "")
	if err != nil {
		t.Fatalf("LabelledSteps: %v", err)
	}
	if got := stepIDsOf(labeled); !reflect.DeepEqual(got, []string{"tagged"}) {
		t.Fatalf("LabelledSteps unfiltered: got %v, want [tagged]", got)
	}
}

func TestLabelledStepsFiltersByEpicAndTask(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	now := engineNowAt()
	if err := s.ApplyEngine(ctx, []EngineOp{
		labelledStepOf("s1", "E1", "T1"),
		labelledStepOf("s2", "E1", "T2"),
		labelledStepOf("s3", "E2", "T1"),
		stepAddOf("s4"),
	}, now); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	cases := []struct {
		name        string
		epic, task  string
		wantStepIDs []string
	}{
		{name: "epic only", epic: "E1", wantStepIDs: []string{"s1", "s2"}},
		{name: "task only", task: "T1", wantStepIDs: []string{"s1", "s3"}},
		{name: "epic and task", epic: "E1", task: "T1", wantStepIDs: []string{"s1"}},
		{name: "unknown epic", epic: "E9", wantStepIDs: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := s.LabelledSteps(ctx, tc.epic, tc.task)
			if err != nil {
				t.Fatalf("LabelledSteps(%q, %q): %v", tc.epic, tc.task, err)
			}
			if got := stepIDsOf(rows); !reflect.DeepEqual(got, tc.wantStepIDs) {
				t.Fatalf("LabelledSteps(%q, %q): got %v, want %v", tc.epic, tc.task, got, tc.wantStepIDs)
			}
		})
	}
}

func TestLabelledStepsOrdersByStartedThenID(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := engineNowAt()
	later := t0.Add(time.Minute)
	if err := s.ApplyEngine(ctx, []EngineOp{labelledStepOf("b", "e", "t")}, later); err != nil {
		t.Fatalf("ApplyEngine b: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{labelledStepOf("c", "e", "t")}, t0); err != nil {
		t.Fatalf("ApplyEngine c: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{labelledStepOf("a", "e", "t")}, t0); err != nil {
		t.Fatalf("ApplyEngine a: %v", err)
	}

	rows, err := s.LabelledSteps(ctx, "e", "t")
	if err != nil {
		t.Fatalf("LabelledSteps: %v", err)
	}
	if got, want := stepIDsOf(rows), []string{"a", "c", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("LabelledSteps order: got %v, want %v (started, then id)", got, want)
	}
}
