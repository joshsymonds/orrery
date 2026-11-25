package store

import (
	"context"
	"reflect"
	"testing"
)

// requireIntegrateRounds fails unless EpicFlow reads want as the integrate rounds of integrateEpic, in every field.
func requireIntegrateRounds(t *testing.T, s *Store, want []IntegrateRound) {
	t.Helper()
	flow, err := s.EpicFlow(context.Background(), integrateEpic)
	if err != nil {
		t.Fatalf("EpicFlow of epic %s: %v", integrateEpic, err)
	}
	if !reflect.DeepEqual(flow.Rounds, want) {
		t.Errorf("integrate rounds of epic %s = %+v, want %+v", integrateEpic, flow.Rounds, want)
	}
}

// TestIntegrateRoundRecordedAndRead checks that a recorded round is read back with the second it was recorded at, that
// rounds recorded out of order are read in path, attempt, task and round order, and that a repeat of a recorded round
// writes nothing.
func TestIntegrateRoundRecordedAndRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	const path = "plan/S1/integrate"
	early := IntegrateRound{
		Path: path, Attempt: 1, Task: 1, Round: 1, Step: "step-1", Verdict: "merged", Head: "head-1",
		Explanation: "merged cleanly",
	}
	late := IntegrateRound{
		Path: path, Attempt: 1, Task: 2, Round: 1, Step: "step-2", Verdict: "clash", Head: "head-2",
		Explanation: "both edit main.go",
	}

	if err := s.RecordIntegrateRound(ctx, integrateEpic, late, integrateStartedAt()); err != nil {
		t.Fatalf("RecordIntegrateRound of task 2: %v", err)
	}
	if err := s.RecordIntegrateRound(ctx, integrateEpic, early, integrateEndedAt()); err != nil {
		t.Fatalf("RecordIntegrateRound of task 1: %v", err)
	}
	want := []IntegrateRound{
		{
			Path: path, Attempt: 1, Task: 1, Round: 1, Step: "step-1", Verdict: "merged", Head: "head-1",
			Explanation: "merged cleanly", Created: integrateEndedAt(),
		},
		{
			Path: path, Attempt: 1, Task: 2, Round: 1, Step: "step-2", Verdict: "clash", Head: "head-2",
			Explanation: "both edit main.go", Created: integrateStartedAt(),
		},
	}
	requireIntegrateRounds(t, s, want)

	repeat := early
	repeat.Verdict, repeat.Explanation = "ended", "a second account"
	if err := s.RecordIntegrateRound(ctx, integrateEpic, repeat, integrateParkedAt()); err != nil {
		t.Fatalf("repeat RecordIntegrateRound: %v", err)
	}
	requireIntegrateRounds(t, s, want)
}

// TestIntegrateRoundComposeMovesRun checks that composing a round sets its outcome to composed and the integrate run's
// revision and result, and that a round already decided, or a run that has ended, keeps its revision and result.
func TestIntegrateRoundComposeMovesRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	const path = "plan/S1/integrate"
	startIntegrateRuns(t, s, NodeRun{Path: path, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1})
	clash := IntegrateRound{
		Path: path, Attempt: 1, Task: 3, Round: 1, Step: "step-3", Verdict: "clash", Head: "head-3",
		Explanation: "both edit g.go",
	}
	if err := s.RecordIntegrateRound(ctx, integrateEpic, clash, integrateStartedAt()); err != nil {
		t.Fatalf("RecordIntegrateRound: %v", err)
	}

	if err := s.ComposeIntegrateRound(ctx, integrateEpic, path, 1, 3, 1, "rev-3", `{"composed":[3]}`,
		integrateEndedAt()); err != nil {
		t.Fatalf("ComposeIntegrateRound: %v", err)
	}
	composed := clash
	composed.Outcome = "composed"
	composed.Created = integrateStartedAt()
	requireIntegrateRounds(t, s, []IntegrateRound{composed})
	running := NodeRun{
		Path: path, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1, Revision: "rev-3",
		Result: `{"composed":[3]}`, State: NodeRunning, Started: integrateStartedAt(),
	}
	requireIntegrateRun(t, s, path, running)

	if err := s.ComposeIntegrateRound(ctx, integrateEpic, path, 1, 3, 1, "rev-other", "other",
		integrateParkedAt()); err != nil {
		t.Fatalf("repeat ComposeIntegrateRound: %v", err)
	}
	requireIntegrateRun(t, s, path, running)

	later := IntegrateRound{
		Path: path, Attempt: 1, Task: 4, Round: 1, Step: "step-4", Verdict: "clash", Head: "head-4",
		Explanation: "both edit h.go",
	}
	if err := s.RecordIntegrateRound(ctx, integrateEpic, later, integrateStartedAt()); err != nil {
		t.Fatalf("RecordIntegrateRound of task 4: %v", err)
	}
	ends := []NodeEnd{{Path: path, Outcome: "passed"}}
	if err := s.EndNodes(ctx, integrateEpic, ends, integrateEndedAt()); err != nil {
		t.Fatalf("EndNodes: %v", err)
	}
	if err := s.ComposeIntegrateRound(ctx, integrateEpic, path, 1, 4, 1, "rev-4", "result-4",
		integrateParkedAt()); err != nil {
		t.Fatalf("ComposeIntegrateRound of an ended run: %v", err)
	}
	requireIntegrateRounds(t, s, []IntegrateRound{composed, {
		Path: path, Attempt: 1, Task: 4, Round: 1, Step: "step-4", Verdict: "clash", Head: "head-4",
		Explanation: "both edit h.go", Created: integrateStartedAt(),
	}})
	ended := running
	ended.State = NodeEnded
	ended.Outcome = "passed"
	ended.Ended = integrateEndedAt()
	requireIntegrateRun(t, s, path, ended)
}

// TestSwapIntegrateRunResult checks that a swap writes the new result only while the running run holds the result it
// names, and reports whether it wrote: a swap from a result the run no longer holds, at another attempt, or of a run
// that has ended writes nothing.
func TestSwapIntegrateRunResult(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	const (
		path    = "plan/S1/integrate"
		claimed = `{"composed":[]}`
		chosen  = `{"composed":[],"verification":{}}`
	)
	startIntegrateRuns(t, s, NodeRun{Path: path, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1})
	if err := s.ClaimBatch(ctx, integrateEpic, path, 1, "rev-1", "", claimed, nil); err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	swap := func(attempt int64, was, result string) bool {
		t.Helper()
		swapped, err := s.SwapIntegrateRunResult(ctx, integrateEpic, path, attempt, was, result, integrateEndedAt())
		if err != nil {
			t.Fatalf("SwapIntegrateRunResult: %v", err)
		}
		return swapped
	}
	if !swap(1, claimed, chosen) {
		t.Fatal("a swap from the held result wrote nothing")
	}
	if swap(1, claimed, "stale") || swap(2, chosen, "another attempt") {
		t.Error("a swap from a result the run does not hold, or at another attempt, wrote")
	}
	requireIntegrateRun(t, s, path, NodeRun{
		Path: path, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1, Revision: "rev-1", Result: chosen,
		State: NodeRunning, Started: integrateStartedAt(),
	})
	if err := s.EndNodes(
		ctx,
		integrateEpic,
		[]NodeEnd{{Path: path, Outcome: "passed"}},
		integrateEndedAt(),
	); err != nil {
		t.Fatalf("EndNodes: %v", err)
	}
	if swap(1, chosen, "after the end") {
		t.Error("a swap of an ended run wrote")
	}
}

// TestIntegrateRoundFailKeepsEarlierRounds checks that failing a round keeps the rounds before it as they were, and
// that a round already composed keeps its outcome when it is failed.
func TestIntegrateRoundFailKeepsEarlierRounds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	const path = "plan/S1/integrate"
	startIntegrateRuns(t, s, NodeRun{Path: path, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1})
	merged := IntegrateRound{
		Path: path, Attempt: 1, Task: 1, Round: 1, Step: "step-1", Verdict: "merged", Head: "head-1",
		Explanation: "merged cleanly",
	}
	first := IntegrateRound{
		Path: path, Attempt: 1, Task: 2, Round: 1, Step: "step-2", Verdict: "clash", Head: "head-2",
		Explanation: "both edit main.go",
	}
	second := IntegrateRound{
		Path: path, Attempt: 1, Task: 2, Round: 2, Step: "step-3", Verdict: "clash", Head: "head-3",
		Explanation: "still both edit main.go",
	}
	for _, round := range []IntegrateRound{merged, first, second} {
		if err := s.RecordIntegrateRound(ctx, integrateEpic, round, integrateStartedAt()); err != nil {
			t.Fatalf("RecordIntegrateRound of task %d round %d: %v", round.Task, round.Round, err)
		}
	}
	if err := s.ComposeIntegrateRound(ctx, integrateEpic, path, 1, 2, 1, "rev-2", "result-2",
		integrateEndedAt()); err != nil {
		t.Fatalf("ComposeIntegrateRound: %v", err)
	}

	if err := s.FailIntegrateRound(ctx, integrateEpic, path, 1, 2, 2, "gate red on head-3",
		integrateEndedAt()); err != nil {
		t.Fatalf("FailIntegrateRound: %v", err)
	}
	if err := s.FailIntegrateRound(ctx, integrateEpic, path, 1, 2, 1, "too late", integrateParkedAt()); err != nil {
		t.Fatalf("FailIntegrateRound of a composed round: %v", err)
	}

	merged.Created = integrateStartedAt()
	first.Outcome = "composed"
	first.Created = integrateStartedAt()
	second.Outcome = "failed"
	second.Failure = "gate red on head-3"
	second.Created = integrateStartedAt()
	requireIntegrateRounds(t, s, []IntegrateRound{merged, first, second})
}
