package store

import (
	"context"
	"testing"
)

// The integrate run a refs move parks in the tests of this file, and the reason the move parks it with.
const (
	parkRetryRunPath = "plan/S1/integrate"
	parkRetryRunPark = "workflow node plan/S1/integrate moved main (aaa to bbb)"
)

// refMoveRunParked starts the integrate run at parkRetryRunPath of a new claimEpic and parks it when its refs check finds
// a move, as the ref guard does. The park opens the epic's park question, which names the run.
func refMoveRunParked(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	createEpic(t, s, fullEpic(claimEpic))
	runs := []NodeRun{{Path: parkRetryRunPath, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1}}
	if err := s.StartNodes(ctx, claimEpic, runs, nil, claimedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if err := s.CheckNodeRefs(ctx, claimEpic, parkRetryRunPath, 1, parkRetryRunPark, nil, claimedAt()); err != nil {
		t.Fatalf("CheckNodeRefs: %v", err)
	}
}

// runAtPath returns the node run of claimEpic at path as the store reads it back. It fails the test when there is none.
func runAtPath(t *testing.T, s *Store, path string) NodeRun {
	t.Helper()
	runs, err := s.NodeRuns(context.Background(), claimEpic)
	if err != nil {
		t.Fatalf("NodeRuns: %v", err)
	}
	for _, run := range runs {
		if run.Path == path {
			return run
		}
	}
	t.Fatalf("no node run at %s", path)
	return NodeRun{}
}

// TestParkRetryRerunAnswerRetryRerunsTheParkedRunOfItsQuestion checks that answering retry to the park question of a refs
// move reruns the parked run the question names, at its next attempt, and ends the epic's park.
func TestParkRetryRerunAnswerRetryRerunsTheParkedRunOfItsQuestion(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	refMoveRunParked(t, s)

	if _, err := s.AnswerQuestion(
		context.Background(),
		claimEpic,
		"D3",
		"",
		1,
		ChoiceRetry,
		Maker{},
		claimedAt(),
	); err != nil {
		t.Fatalf("AnswerQuestion retry: %v", err)
	}
	run := runAtPath(t, s, parkRetryRunPath)
	if run.State != NodeRunning || run.Attempt != 2 || run.Outcome != "" || run.Output != "" {
		t.Errorf("run after retry = %+v, want running at attempt 2 with its outcome and output cleared", run)
	}
	if _, parked := epicFlags(t, s); parked != "" {
		t.Errorf("parked after the retry answer = %q, want empty", parked)
	}
}

// TestParkRetryRerunLeavesAnotherParkedRunOfTheSameNodeParked checks that the retry of a park question reruns only the run
// its question names, and leaves another parked run of the same node id, under another path, parked.
func TestParkRetryRerunLeavesAnotherParkedRunOfTheSameNodeParked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const other = "plan/S2/integrate"
	s := openEpicStore(t)
	refMoveRunParked(t, s)
	runs := []NodeRun{{Path: other, ID: "id-s2", Node: "integrate", Kind: "integrate", Attempt: 1}}
	if err := s.StartNodes(ctx, claimEpic, runs, nil, claimedAt()); err != nil {
		t.Fatalf("StartNodes of %s: %v", other, err)
	}
	const park = "workflow node plan/S2/integrate failed: boom"
	if err := s.ParkNode(ctx, claimEpic, other, 1, "failed", "boom", ParkClassNode, park, claimedAt()); err != nil {
		t.Fatalf("ParkNode of %s: %v", other, err)
	}

	if _, err := s.AnswerQuestion(ctx, claimEpic, "D3", "", 1, ChoiceRetry, Maker{}, claimedAt()); err != nil {
		t.Fatalf("AnswerQuestion retry: %v", err)
	}
	if run := runAtPath(t, s, parkRetryRunPath); run.State != NodeRunning || run.Attempt != 2 {
		t.Errorf("run %s after retry = %+v, want running at attempt 2", parkRetryRunPath, run)
	}
	if run := runAtPath(t, s, other); run.State != NodeParked || run.Attempt != 1 {
		t.Errorf("run %s after retry = %+v, want parked at attempt 1", other, run)
	}
}

// TestParkRetryRerunOtherAnswerRerunsNothing checks that answering a park question with a choice other than retry leaves
// the run it names parked, while the epic's park ends as it does for any answer to a park question.
func TestParkRetryRerunOtherAnswerRerunsNothing(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	refMoveRunParked(t, s)

	if _, err := s.AnswerQuestion(
		context.Background(), claimEpic, "D3", "Keep the run parked", 1, ChoiceOther, Maker{},
		claimedAt()); err != nil {
		t.Fatalf("AnswerQuestion other: %v", err)
	}
	if run := runAtPath(t, s, parkRetryRunPath); run.State != NodeParked || run.Attempt != 1 {
		t.Errorf("run after other = %+v, want parked at attempt 1", run)
	}
	if _, parked := epicFlags(t, s); parked != "" {
		t.Errorf("parked after the other answer = %q, want empty", parked)
	}
}

// TestParkRetryRerunRetryEpicRerunsTheParkedRunOfItsOpenQuestion checks that RetryEpic ending a park reruns the parked run
// its open park question names, at its next attempt, as it answers the question retry.
func TestParkRetryRerunRetryEpicRerunsTheParkedRunOfItsOpenQuestion(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	refMoveRunParked(t, s)

	if err := s.RetryEpic(context.Background(), claimEpic); err != nil {
		t.Fatalf("RetryEpic: %v", err)
	}
	if run := runAtPath(t, s, parkRetryRunPath); run.State != NodeRunning || run.Attempt != 2 {
		t.Errorf("run after RetryEpic = %+v, want running at attempt 2", run)
	}
	if _, parked := epicFlags(t, s); parked != "" {
		t.Errorf("parked after RetryEpic = %q, want empty", parked)
	}
}
