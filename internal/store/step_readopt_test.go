package store

import (
	"context"
	"reflect"
	"testing"
)

// addStepSession adds step s1 and the step session its events belong to, the way the engine adds a step.
func addStepSession(t *testing.T, s *Store) {
	t.Helper()
	if err := s.ApplyEngine(context.Background(), []EngineOp{
		stepAddOf("s1"),
		EngineAdd{ID: "s1", Dir: "/ws/s1", State: "idle", Kind: KindStep},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine add step s1: %v", err)
	}
}

// newestSeqOf returns the largest seq of an event of kind in session id, or 0 when the session has none.
func newestSeqOf(t *testing.T, s *Store, id, kind string) int64 {
	t.Helper()
	var seq int64
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT COALESCE(MAX(seq), 0) FROM events WHERE session_id = ? AND kind = ?`, id, kind,
	).Scan(&seq); err != nil {
		t.Fatalf("newest %s seq of session %s: %v", kind, id, err)
	}
	return seq
}

// loopStateOf returns the loop columns of row as the StepLoop that sets them.
func loopStateOf(row StepRow) StepLoop {
	return StepLoop{
		ID: row.ID, Round: row.LoopRound, Continues: row.LoopContinues, Nudge: row.LoopNudge,
		OwnerNudge: row.LoopOwnerNudge, Nudged: row.LoopNudged, Held: row.LoopHeld, JudgeSent: row.LoopJudgeSent,
	}
}

// gateStateOf returns the gate columns of row as the StepGate that sets them.
func gateStateOf(row StepRow) StepGate {
	return StepGate{ID: row.ID, Grant: row.GateGrant, Holder: row.GateHolder, Round: row.GateRound}
}

func TestStepLoopRoundTripsEveryLoopColumn(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	addStepSession(t, s)

	set := StepLoop{
		ID: "s1", Round: 2, Continues: 1, Nudge: "look again", OwnerNudge: "owner says hi",
		Nudged: true, Held: true, JudgeSent: true,
	}
	if err := s.ApplyEngine(ctx, []EngineOp{set}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine loop: %v", err)
	}
	got, err := s.Step(ctx, "s1")
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if want := set; !reflect.DeepEqual(loopStateOf(got), want) {
		t.Fatalf("loop state: got %+v, want %+v", loopStateOf(got), want)
	}

	// StepLoop sets every loop column, so an op with zero values clears the ones it set before.
	if err := s.ApplyEngine(ctx, []EngineOp{StepLoop{ID: "s1"}}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine cleared loop: %v", err)
	}
	got, err = s.Step(ctx, "s1")
	if err != nil {
		t.Fatalf("Step after clear: %v", err)
	}
	if want := (StepLoop{ID: "s1"}); !reflect.DeepEqual(loopStateOf(got), want) {
		t.Fatalf("cleared loop state: got %+v, want %+v", loopStateOf(got), want)
	}
}

func TestStepGateRoundTripsAndEmptyHolderClears(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	addStepSession(t, s)

	set := StepGate{ID: "s1", Grant: "g1", Holder: "s1", Round: 3}
	if err := s.ApplyEngine(ctx, []EngineOp{set}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine gate: %v", err)
	}
	got, err := s.Step(ctx, "s1")
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if want := set; !reflect.DeepEqual(gateStateOf(got), want) {
		t.Fatalf("gate state: got %+v, want %+v", gateStateOf(got), want)
	}

	// An empty holder clears the gate, whatever grant and round came with it.
	if err := s.ApplyEngine(ctx, []EngineOp{StepGate{ID: "s1", Grant: "g2", Round: 4}}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine cleared gate: %v", err)
	}
	got, err = s.Step(ctx, "s1")
	if err != nil {
		t.Fatalf("Step after clear: %v", err)
	}
	if want := (StepGate{ID: "s1"}); !reflect.DeepEqual(gateStateOf(got), want) {
		t.Fatalf("cleared gate state: got %+v, want %+v", gateStateOf(got), want)
	}
}

func TestStepReturnSetsLoopSeqToItsSessionNewestEventAndClearsGate(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	addStepSession(t, s)
	if err := s.ApplyEngine(ctx, []EngineOp{
		EnginePrompt{ID: "s1", Text: "go"},
		EngineTurnEnded{ID: "s1", Subtype: "success"},
		EngineAdd{ID: "other", Dir: "/other", State: "idle"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine session events: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		StepGate{ID: "s1", Grant: "g1", Holder: "s1", Round: 1},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine gate: %v", err)
	}

	if err := s.ApplyEngine(ctx, []EngineOp{StepReturn{ID: "s1", Result: "done"}}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine return: %v", err)
	}
	got, err := s.Step(ctx, "s1")
	if err != nil {
		t.Fatalf("Step: %v", err)
	}

	// The other session's event is newer than s1's turn end, so a store that took the newest event of every
	// session would not match the turn end.
	turnEnd := newestSeqOf(t, s, "s1", EventEngineTurnEnded)
	if other := newestSeqOf(t, s, "other", EventEngineAdded); other <= turnEnd {
		t.Fatalf("other session event seq %d, want newer than the turn end %d", other, turnEnd)
	}
	if got.LoopSeq != turnEnd {
		t.Fatalf("LoopSeq: got %d, want the step session's newest seq %d", got.LoopSeq, turnEnd)
	}
	if want := (StepGate{ID: "s1"}); !reflect.DeepEqual(gateStateOf(got), want) {
		t.Fatalf("gate after return: got %+v, want %+v", gateStateOf(got), want)
	}
}

func TestStepTurnReportsLastTurnEndAfterLastPrompt(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	addStepSession(t, s)
	output := "the turn said this"
	if err := s.ApplyEngine(ctx, []EngineOp{
		EnginePrompt{ID: "s1", Text: "go"},
		EngineTurnEnded{ID: "s1", Subtype: "success"},
		EngineConsumed{ID: "s1", Consumed: 3, Output: &output},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	turn, ok, err := s.StepTurn(ctx, "s1")
	if err != nil {
		t.Fatalf("StepTurn: %v", err)
	}
	want := StepTurnEnd{Seq: newestSeqOf(t, s, "s1", EventEngineTurnEnded), Output: output}
	if !ok || !reflect.DeepEqual(turn, want) {
		t.Fatalf("StepTurn: got %+v ok %v, want %+v ok true", turn, ok, want)
	}
}

func TestStepTurnReportsErrorTurnEnd(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	addStepSession(t, s)
	if err := s.ApplyEngine(ctx, []EngineOp{
		EnginePrompt{ID: "s1", Text: "go"},
		EngineTurnEnded{ID: "s1", Subtype: "error_during_execution", IsError: true},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	turn, ok, err := s.StepTurn(ctx, "s1")
	if err != nil {
		t.Fatalf("StepTurn: %v", err)
	}
	if !ok || !turn.IsError {
		t.Fatalf("StepTurn: got %+v ok %v, want an error turn end", turn, ok)
	}
}

func TestStepTurnReturnedOnlyForTurnEndsAtOrBeforeTheLatestReturn(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	addStepSession(t, s)
	first := "first output"
	if err := s.ApplyEngine(ctx, []EngineOp{
		EnginePrompt{ID: "s1", Text: "go"},
		EngineTurnEnded{ID: "s1", Subtype: "success"},
		EngineConsumed{ID: "s1", Consumed: 2, Output: &first},
		StepReturn{ID: "s1", Result: "returned"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine first turn and return: %v", err)
	}
	turn, ok, err := s.StepTurn(ctx, "s1")
	if err != nil {
		t.Fatalf("StepTurn after return: %v", err)
	}
	if want := (StepTurnEnd{Seq: newestSeqOf(t, s, "s1", EventEngineTurnEnded), Output: first, Returned: true}); !ok ||
		!reflect.DeepEqual(turn, want) {
		t.Fatalf("StepTurn after return: got %+v ok %v, want %+v ok true", turn, ok, want)
	}

	second := "second output"
	if err := s.ApplyEngine(ctx, []EngineOp{
		EnginePrompt{ID: "s1", Text: "again"},
		EngineTurnEnded{ID: "s1", Subtype: "success"},
		EngineConsumed{ID: "s1", Consumed: 4, Output: &second},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine second turn: %v", err)
	}
	turn, ok, err = s.StepTurn(ctx, "s1")
	if err != nil {
		t.Fatalf("StepTurn after second turn: %v", err)
	}
	if want := (StepTurnEnd{Seq: newestSeqOf(t, s, "s1", EventEngineTurnEnded), Output: second}); !ok ||
		!reflect.DeepEqual(turn, want) {
		t.Fatalf("StepTurn after second turn: got %+v ok %v, want %+v ok true", turn, ok, want)
	}
}

func TestStepTurnNoneAfterLaterPrompt(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	addStepSession(t, s)
	if err := s.ApplyEngine(ctx, []EngineOp{
		EnginePrompt{ID: "s1", Text: "go"},
		EngineTurnEnded{ID: "s1", Subtype: "success"},
		EnginePrompt{ID: "s1", Text: "one more"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	if turn, ok, err := s.StepTurn(ctx, "s1"); err != nil || ok {
		t.Fatalf("StepTurn after a later prompt: got %+v ok %v err %v, want none", turn, ok, err)
	}
}

func TestStepTurnNoneWithoutTurnEnd(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	addStepSession(t, s)
	if err := s.ApplyEngine(ctx, []EngineOp{EnginePrompt{ID: "s1", Text: "go"}}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	if turn, ok, err := s.StepTurn(ctx, "s1"); err != nil || ok {
		t.Fatalf("StepTurn with no turn end: got %+v ok %v err %v, want none", turn, ok, err)
	}
}

func TestStepTurnNoneWhileBackground(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	addStepSession(t, s)
	if err := s.ApplyEngine(ctx, []EngineOp{
		EnginePrompt{ID: "s1", Text: "go"},
		EngineTurnEnded{ID: "s1", Subtype: "success"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	background := true
	if err := s.ApplyEngine(ctx, []EngineOp{EngineConsumed{ID: "s1", Consumed: 2, Background: &background}},
		engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine background on: %v", err)
	}
	if turn, ok, err := s.StepTurn(ctx, "s1"); err != nil || ok {
		t.Fatalf("StepTurn while background: got %+v ok %v err %v, want none", turn, ok, err)
	}

	background = false
	if err := s.ApplyEngine(ctx, []EngineOp{EngineConsumed{ID: "s1", Consumed: 2, Background: &background}},
		engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine background off: %v", err)
	}
	turn, ok, err := s.StepTurn(ctx, "s1")
	if err != nil {
		t.Fatalf("StepTurn after background off: %v", err)
	}
	want := StepTurnEnd{Seq: newestSeqOf(t, s, "s1", EventEngineTurnEnded)}
	if !ok || !reflect.DeepEqual(turn, want) {
		t.Fatalf("StepTurn after background off: got %+v ok %v, want %+v ok true", turn, ok, want)
	}
}

func TestAddGrantStoresHolder(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	row := grantRowOf("g1", engineNowAt(), GrantMember{Pool: "build", Member: "m1"})
	row.Holder = "s1"
	if err := s.AddGrant(ctx, row); err != nil {
		t.Fatalf("AddGrant: %v", err)
	}
	got, err := s.Grants(ctx)
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if want := []GrantRow{row}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Grants: got %+v, want %+v", got, want)
	}
}
