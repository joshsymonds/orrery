package store

import (
	"context"
	"reflect"
	"testing"
)

// retryBoundCheck is the node the retry-bound tests park and retry.
const retryBoundCheck = "check"

// startCheckRun starts the check node of claimEpic at its first attempt.
func startCheckRun(t *testing.T, s *Store) {
	t.Helper()
	runs := []NodeRun{{Path: retryBoundCheck, ID: "id-check", Node: retryBoundCheck, Kind: "gate", Attempt: 1}}
	if err := s.StartNodes(context.Background(), claimEpic, runs, nil, claimedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
}

// parkCheckRun parks the running check node of claimEpic at attempt, as a node park with reason.
func parkCheckRun(t *testing.T, s *Store, attempt int64, reason string) {
	t.Helper()
	if err := s.ParkNode(context.Background(), claimEpic, retryBoundCheck, attempt, "failed", "no verdict",
		ParkClassNode, reason, completedAt()); err != nil {
		t.Fatalf("ParkNode: %v", err)
	}
}

// retryCheck is the action that reruns the check node, for reason.
func retryCheck(reason string) EpicActionInput {
	return EpicActionInput{Kind: ActionRetryNode, Node: retryBoundCheck, Reason: reason}
}

// retryBoundTask is a task that claims the Requirement R1, planned under title.
func retryBoundTask(title string) EpicTaskInput {
	return EpicTaskInput{Title: title, Deliverable: title + " works", Requirements: []string{"R1"}, Gate: "check"}
}

// pendingCauses returns the causes of the triggers of claimEpic that are pending, in trigger order.
func pendingCauses(t *testing.T, s *Store) []string {
	t.Helper()
	history, err := s.EpicHistory(context.Background(), claimEpic)
	if err != nil {
		t.Fatalf("EpicHistory: %v", err)
	}
	var causes []string
	for _, trigger := range history.Triggers {
		if trigger.State == triggerPending {
			causes = append(causes, trigger.Cause)
		}
	}
	return causes
}

// completePlannerTurn claims the next planner turn of claimEpic and completes it with plan.
func completePlannerTurn(t *testing.T, s *Store, plan EpicPlan) {
	t.Helper()
	turn := claimTurnOf(t, s)
	if err := s.CompleteTurn(context.Background(), claimEpic, turn.Seq, plan, completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
}

// carryOutPending carries out each pending action of claimEpic in position order.
func carryOutPending(t *testing.T, s *Store) {
	t.Helper()
	for _, action := range pendingActionsOf(t, s, claimEpic) {
		if _, err := s.CarryOutAction(context.Background(), action, closeAt()); err != nil {
			t.Fatalf("CarryOutAction of action %d of turn %d: %v", action.Position, action.Turn, err)
		}
	}
}

// parkTaskBuild parks task seq of claimEpic as a task park with reason, moving its build from the zero build.
func parkTaskBuild(t *testing.T, s *Store, seq int64, reason string) {
	t.Helper()
	to := TaskBuild{Stage: "red", Base: "base-rev", Held: "parked"}
	if err := s.ParkTask(context.Background(), claimEpic, seq, TaskBuild{}, to, ParkClassTask, reason,
		completedAt()); err != nil {
		t.Fatalf("ParkTask of task %d: %v", seq, err)
	}
}

// TestOrchestratorRetryBoundRepeatedParkAfterOneActionWakesThePlanner checks that a node park which returns after one
// planner action on its node wakes the planner again, even with a different reason: the park is stored as the node class,
// opens no question, adds no tell, and adds one pending park trigger whose cause is the new reason.
func TestOrchestratorRetryBoundRepeatedParkAfterOneActionWakesThePlanner(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	startCheckRun(t, s)
	parkCheckRun(t, s, 1, "no verdict at attempt 1")
	completePlannerTurn(t, s, EpicPlan{Actions: []EpicActionInput{retryCheck("the verdict file was missing")}})
	carryOutPending(t, s)

	const again = "no verdict at attempt 2, with a different reason"
	parkCheckRun(t, s, 2, again)

	if epic := closeRow(t, s, claimEpic); epic.Parked != again || epic.ParkClass != ParkClassNode {
		t.Errorf("park of epic = %q %q, want %q %q", epic.ParkClass, epic.Parked, ParkClassNode, again)
	}
	if got := questionsOf(t, s); len(got) != 0 {
		t.Errorf("questions after the repeated park = %+v, want none: a planner park opens none", got)
	}
	if got := moveTellsOf(context.Background(), t, s); len(got) != 0 {
		t.Errorf("tells after the repeated park = %q, want none", got)
	}
	if want := []string{again}; !reflect.DeepEqual(pendingCauses(t, s), want) {
		t.Errorf("pending park triggers after the repeated park = %v, want %v",
			pendingCauses(t, s), want)
	}
}

// TestOrchestratorRetryBoundThirdParkOpensTheOwnerQuestion checks that a third park of the node class on the same node,
// after a second action on it, is stored as retry_bound. Its owner question holds the park, then both earlier parks and
// both actions, and its tell carries the same text. It adds no park trigger.
func TestOrchestratorRetryBoundThirdParkOpensTheOwnerQuestion(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	startCheckRun(t, s)
	parkCheckRun(t, s, 1, "no verdict at attempt 1")
	completePlannerTurn(t, s, EpicPlan{Actions: []EpicActionInput{retryCheck("the verdict file was missing")}})
	carryOutPending(t, s)
	parkCheckRun(t, s, 2, "no verdict at attempt 2")
	completePlannerTurn(t, s, EpicPlan{Actions: []EpicActionInput{retryCheck("the verdict file is written now")}})
	carryOutPending(t, s)

	const third = "no verdict at attempt 3"
	parkCheckRun(t, s, 3, third)

	const text = third +
		"\nEarlier parks on this chain:" +
		"\n- no verdict at attempt 1" +
		"\n- no verdict at attempt 2" +
		"\nPlanner actions on this chain:" +
		"\n- retry node check: the verdict file was missing (taken)" +
		"\n- retry node check: the verdict file is written now (taken)"
	if epic := closeRow(t, s, claimEpic); epic.Parked != third || epic.ParkClass != ParkClassRetryBound {
		t.Errorf("park of epic = %q %q, want %q %q", epic.ParkClass, epic.Parked, ParkClassRetryBound, third)
	}
	if got, want := questionsOf(t, s), []EpicQuestion{parkQuestionOf(1, text)}; !reflect.DeepEqual(got, want) {
		t.Errorf("questions after the third park = %+v, want %+v", got, want)
	}
	if got, want := moveTellsOf(context.Background(), t, s), []string{text}; !reflect.DeepEqual(got, want) {
		t.Errorf("tells after the third park = %q, want %q", got, want)
	}
	if n := len(pendingCauses(t, s)); n != 0 {
		t.Errorf("pending triggers after the third park = %d, want none: a retry-bound park wakes no planner", n)
	}
}

// TestOrchestratorRetryBoundTwoRefusedActionsReachTheOwnerQuestion checks that two actions the engine refuses on one park
// count as two actions on it. The second refusal brings the chain to the bound, so the park is stored as retry_bound with
// its question listing both refusals, and the second refusal adds no trigger.
func TestOrchestratorRetryBoundTwoRefusedActionsReachTheOwnerQuestion(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	startCheckRun(t, s)
	const park = "no verdict at attempt 1"
	parkCheckRun(t, s, 1, park)
	const missing = "node other of epic " + claimEpic + " is not parked"
	refused := func(reason string) EpicActionInput {
		return EpicActionInput{Kind: ActionRetryNode, Node: "other", Reason: reason}
	}
	completePlannerTurn(t, s, EpicPlan{Actions: []EpicActionInput{refused("the verdict file was missing")}})
	carryOutPending(t, s)
	completePlannerTurn(t, s, EpicPlan{Actions: []EpicActionInput{refused("the verdict file is written now")}})
	carryOutPending(t, s)

	const text = park +
		"\nPlanner actions on this chain:" +
		"\n- retry node other: the verdict file was missing (refused: " + missing + ")" +
		"\n- retry node other: the verdict file is written now (refused: " + missing + ")"
	if epic := closeRow(t, s, claimEpic); epic.Parked != park || epic.ParkClass != ParkClassRetryBound {
		t.Errorf("park of epic = %q %q, want %q %q", epic.ParkClass, epic.Parked, ParkClassRetryBound, park)
	}
	if got, want := questionsOf(t, s), []EpicQuestion{parkQuestionOf(1, text)}; !reflect.DeepEqual(got, want) {
		t.Errorf("questions after the second refusal = %+v, want %+v", got, want)
	}
	if got, want := moveTellsOf(context.Background(), t, s), []string{text}; !reflect.DeepEqual(got, want) {
		t.Errorf("tells after the second refusal = %q, want %q", got, want)
	}
	if n := len(pendingCauses(t, s)); n != 0 {
		t.Errorf("pending triggers after the second refusal = %d, want none: the bound adds no refusal trigger", n)
	}
}

// TestOrchestratorRetryBoundReplacedTaskParkContinuesItsPredecessorCount checks that a task park on a task that replaces one
// the planner failed continues that task's count. Task 2 replaces task 1, so its park is counted with the fail of task 1
// and wakes the planner. Task 3 replaces task 2, so its park is counted with the fails of tasks 1 and 2 and reaches the
// owner question.
func TestOrchestratorRetryBoundReplacedTaskParkContinuesItsPredecessorCount(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	completePlannerTurn(t, s, EpicPlan{Tasks: []EpicTaskInput{retryBoundTask("write the store")}})
	parkTaskBuild(t, s, 1, "the build of T1 parked")
	completePlannerTurn(t, s, EpicPlan{Actions: []EpicActionInput{{
		Kind: ActionFailTask, Task: 1, Reason: "the build cannot reach the store",
	}}})
	carryOutPending(t, s)
	completePlannerTurn(t, s, EpicPlan{Tasks: []EpicTaskInput{retryBoundTask("write the store again")}})
	parkTaskBuild(t, s, 2, "the build of T2 parked")
	if got, want := pendingCauses(t, s), []string{"the build of T2 parked"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pending park triggers after the replacing park = %v, want %v", got, want)
	}
	completePlannerTurn(t, s, EpicPlan{Actions: []EpicActionInput{{
		Kind: ActionFailTask, Task: 2, Reason: "the build cannot reach the store again",
	}}})
	carryOutPending(t, s)
	completePlannerTurn(t, s, EpicPlan{Tasks: []EpicTaskInput{retryBoundTask("write the store a third time")}})
	const third = "the build of T3 parked"
	parkTaskBuild(t, s, 3, third)

	text := third +
		"\nEarlier parks on this chain:" +
		"\n- the build of T1 parked" +
		"\n- the build of T2 parked" +
		"\nPlanner actions on this chain:" +
		"\n- fail task 1: the build cannot reach the store (taken)" +
		"\n- fail task 2: the build cannot reach the store again (taken)"
	if epic := closeRow(t, s, claimEpic); epic.Parked != third || epic.ParkClass != ParkClassRetryBound {
		t.Errorf("park of epic = %q %q, want %q %q", epic.ParkClass, epic.Parked, ParkClassRetryBound, third)
	}
	if got, want := questionsOf(t, s), []EpicQuestion{parkQuestionOf(1, text)}; !reflect.DeepEqual(got, want) {
		t.Errorf("questions after the replacing park = %+v, want %+v", got, want)
	}
	if n := len(pendingCauses(t, s)); n != 0 {
		t.Errorf("pending triggers after the replacing park = %d, want none", n)
	}
}

// TestOrchestratorRetryBoundParkOfAnotherClassStartsItsOwnCount checks that a park of another class on the node that
// already holds two actions starts a count of its own: it wakes the planner, and opens no question.
func TestOrchestratorRetryBoundParkOfAnotherClassStartsItsOwnCount(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	startCheckRun(t, s)
	parkCheckRun(t, s, 1, "no verdict at attempt 1")
	completePlannerTurn(t, s, EpicPlan{Actions: []EpicActionInput{retryCheck("the verdict file was missing")}})
	carryOutPending(t, s)
	parkCheckRun(t, s, 2, "no verdict at attempt 2")
	completePlannerTurn(t, s, EpicPlan{Actions: []EpicActionInput{retryCheck("the verdict file is written now")}})
	carryOutPending(t, s)

	const loop = "the loop ran out of rounds"
	if err := s.ParkNode(context.Background(), claimEpic, retryBoundCheck, 3, "failed", "no verdict",
		ParkClassLoop, loop, completedAt()); err != nil {
		t.Fatalf("ParkNode of the loop class: %v", err)
	}

	if epic := closeRow(t, s, claimEpic); epic.Parked != loop || epic.ParkClass != ParkClassLoop {
		t.Errorf("park of epic = %q %q, want %q %q", epic.ParkClass, epic.Parked, ParkClassLoop, loop)
	}
	if got := questionsOf(t, s); len(got) != 0 {
		t.Errorf("questions after the loop park = %+v, want none", got)
	}
	if want := []string{loop}; !reflect.DeepEqual(pendingCauses(t, s), want) {
		t.Errorf("pending park triggers after the loop park = %v, want %v",
			pendingCauses(t, s), want)
	}
}
