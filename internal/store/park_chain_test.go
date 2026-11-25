package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// parkChainStart is the second the park chain tests store their first park at. Each later park is stored a minute after
// the one before it, so a pass between two parks is stored between their seconds.
func parkChainStart() time.Time {
	return time.Unix(1700002000, 0)
}

// parkChainAt returns the second the park chain tests store park n at, counted from 1.
func parkChainAt(n int) time.Time {
	return parkChainStart().Add(time.Duration(n-1) * time.Minute)
}

// parkChainReason is the reason the park chain tests store park n with.
func parkChainReason(n int) string {
	return "workflow node check failed: run " + string(rune('0'+n))
}

// parkChainNode parks claimEpic as a node park, whose subject is empty, with the reason of park n at its second, so each
// such park joins the chain of the one before it.
func parkChainNode(t *testing.T, s *Store, n int) {
	t.Helper()
	if err := s.ParkEpic(
		context.Background(),
		claimEpic,
		ParkClassNode,
		parkChainReason(n),
		parkChainAt(n),
	); err != nil {
		t.Fatalf("ParkEpic %d: %v", n, err)
	}
}

// retryEpicAction is the planner action that ends the park of claimEpic, for reason.
func retryEpicAction(reason string) EpicActionInput {
	return EpicActionInput{Kind: ActionRetryEpic, Reason: reason}
}

// answerPark completes the next planner turn of claimEpic with actions and carries them out.
func answerPark(t *testing.T, s *Store, actions ...EpicActionInput) {
	t.Helper()
	completePlannerTurn(t, s, EpicPlan{Actions: actions})
	carryOutPending(t, s)
}

// parkChainToBound parks claimEpic twice, each park answered by a retry_epic, then a third time, so the third park comes
// to its chain with two actions taken on it.
func parkChainToBound(t *testing.T, s *Store) {
	t.Helper()
	parkChainNode(t, s, 1)
	answerPark(t, s, retryEpicAction("the check runs again"))
	parkChainNode(t, s, 2)
	answerPark(t, s, retryEpicAction("the check runs once more"))
	parkChainNode(t, s, 3)
}

// boundChainOf returns the bound chain of claimEpic, failing the test on error.
func boundChainOf(t *testing.T, s *Store) BoundChain {
	t.Helper()
	chain, err := s.BoundChain(context.Background(), claimEpic)
	if err != nil {
		t.Fatalf("BoundChain: %v", err)
	}
	return chain
}

// requirePlannerPark fails unless claimEpic holds the node park of park n with no question open and its park trigger
// pending, the planner's to answer, and its bound chain is the zero BoundChain.
func requirePlannerPark(t *testing.T, s *Store, n int) {
	t.Helper()
	if epic := closeRow(t, s, claimEpic); epic.Parked != parkChainReason(n) || epic.ParkClass != ParkClassNode {
		t.Errorf("park of epic = %q %q, want %q %q", epic.ParkClass, epic.Parked, ParkClassNode, parkChainReason(n))
	}
	if got := questionsOf(t, s); len(got) != 0 {
		t.Errorf("questions = %+v, want none: the planner answers the park", got)
	}
	if want := []string{parkChainReason(n)}; !reflect.DeepEqual(pendingCauses(t, s), want) {
		t.Errorf("pending triggers = %v, want %v", pendingCauses(t, s), want)
	}
	if chain := boundChainOf(t, s); chain.Seq != 0 {
		t.Errorf("bound chain = %+v, want none: the park is not at the bound", chain)
	}
}

// requireOwnerPark fails unless claimEpic holds the reason of park n as retry_bound, its owner question has text, and no
// trigger is pending.
func requireOwnerPark(t *testing.T, s *Store, n int, text string) {
	t.Helper()
	if epic := closeRow(t, s, claimEpic); epic.Parked != parkChainReason(n) || epic.ParkClass != ParkClassRetryBound {
		t.Errorf("park of epic = %q %q, want %q %q", epic.ParkClass, epic.Parked, ParkClassRetryBound,
			parkChainReason(n))
	}
	questions := questionsOf(t, s)
	if len(questions) == 0 || questions[len(questions)-1].Text != text || questions[len(questions)-1].State != "open" {
		t.Errorf("questions = %+v, want an open last question %q", questions, text)
	}
	if n := len(pendingCauses(t, s)); n != 0 {
		t.Errorf("pending triggers = %d, want none: an owner park wakes no planner", n)
	}
}

// TestParkChainParkAtTheBoundWaitsOnThePlannersCall checks that a park that comes to a chain holding two taken actions
// opens no question and wakes the planner, and that its bound chain holds the park, both earlier parks, both actions, and
// what changed since the chain's previous park: here nothing.
func TestParkChainParkAtTheBoundWaitsOnThePlannersCall(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	base := closeRow(t, s, claimEpic).StoredBase
	parkChainToBound(t, s)

	if got := questionsOf(t, s); len(got) != 0 {
		t.Errorf("questions = %+v, want none: the planner calls a park at the bound", got)
	}
	if want := []string{parkChainReason(3)}; !reflect.DeepEqual(pendingCauses(t, s), want) {
		t.Errorf("pending triggers = %v, want %v", pendingCauses(t, s), want)
	}
	want := BoundChain{
		Seq: 3, Park: parkChainReason(3), Earlier: []string{parkChainReason(1), parkChainReason(2)},
		Actions: []string{
			"retry epic: the check runs again (taken)",
			"retry epic: the check runs once more (taken)",
		},
		Changes: []string{
			"stored base: unchanged at " + commitOrNone(base),
			"candidate: unchanged at none",
			"node runs passed since: none",
			"release steps passed since: none",
		},
	}
	if got := boundChainOf(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("bound chain = %+v, want %+v", got, want)
	}
}

// TestParkChainNewCallStartsAChainOfItsOwn checks that park_new on a park at the bound moves it to a chain of its own:
// the call and the action after it are both taken, the action ends the park, and the next park of the same class and
// subject joins the new chain, which holds one taken action, since the call is not counted, so the planner answers it.
func TestParkChainNewCallStartsAChainOfItsOwn(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	parkChainToBound(t, s)
	answerPark(t, s,
		EpicActionInput{Kind: ActionParkNew, Reason: "run 3 failed another way"},
		retryEpicAction("the check runs for the new failure"),
	)
	if epic := closeRow(t, s, claimEpic); epic.Parked != "" {
		t.Errorf("park after the new call and its retry = %q %q, want none", epic.ParkClass, epic.Parked)
	}
	if got := questionsOf(t, s); len(got) != 0 {
		t.Errorf("questions after the new call = %+v, want none", got)
	}

	parkChainNode(t, s, 4)
	requirePlannerPark(t, s, 4)
}

// TestParkChainRefusalsHaveTheirOwnBound checks that a refused action does not spend the bound of the actions taken: a
// park that comes to a chain holding one taken and one refused action is the planner's to answer, where a chain holding
// two actions of any outcome sent it to the owner before.
func TestParkChainRefusalsHaveTheirOwnBound(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	parkChainNode(t, s, 1)
	answerPark(t, s, EpicActionInput{Kind: ActionRetryNode, Node: "other", Reason: "a node that is not parked"})
	if epic := closeRow(t, s, claimEpic); epic.ParkClass != ParkClassNode {
		t.Fatalf("park class after one refusal = %q, want %q", epic.ParkClass, ParkClassNode)
	}
	answerPark(t, s, retryEpicAction("the check runs again"))

	parkChainNode(t, s, 2)
	requirePlannerPark(t, s, 2)
}

// TestParkChainActionBeforeTheCallSendsTheParkToTheOwner checks that an action other than a call, carried out on a park
// at the bound before the planner called it, is refused, and its refusal sends the park to the owner: its question lists
// the refusal, and no trigger wakes the planner.
func TestParkChainActionBeforeTheCallSendsTheParkToTheOwner(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	parkChainToBound(t, s)
	answerPark(t, s, retryEpicAction("the check runs a third time"))

	text := parkChainReason(3) +
		"\nEarlier parks on this chain:" +
		"\n- " + parkChainReason(1) +
		"\n- " + parkChainReason(2) +
		"\nPlanner actions on this chain:" +
		"\n- retry epic: the check runs again (taken)" +
		"\n- retry epic: the check runs once more (taken)" +
		"\n- retry epic: the check runs a third time (refused: the park of epic " + claimEpic + " is at the bound " +
		"of its chain and waits on the planner's call: park_recurs, or park_new before the actions that answer the park)"
	requireOwnerPark(t, s, 3, text)
}

// TestParkChainCallOffTheBoundIsRefused checks that park_recurs and park_new on a park that is not at the bound of its
// chain are each refused, and each refusal wakes the planner again with the park still its own.
func TestParkChainCallOffTheBoundIsRefused(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{ActionParkRecurs, ActionParkNew} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			s := openEpicStore(t)
			createEpic(t, s, fullEpic(claimEpic))
			parkChainNode(t, s, 1)
			answerPark(t, s, EpicActionInput{Kind: kind, Reason: "a call on a park off the bound"})

			if epic := closeRow(t, s, claimEpic); epic.ParkClass != ParkClassNode || epic.Parked != parkChainReason(1) {
				t.Errorf("park after the refused call = %q %q, want %q %q", epic.ParkClass, epic.Parked,
					ParkClassNode, parkChainReason(1))
			}
			want := []string{"planner " + actionSummary(PlannerAction{EpicActionInput: EpicActionInput{Kind: kind}}) +
				" refused: epic " + claimEpic + " holds no park at the bound of its chain that waits on a call"}
			if got := pendingCauses(t, s); !reflect.DeepEqual(got, want) {
				t.Errorf("pending triggers after the refused call = %v, want %v", got, want)
			}
			if got := questionsOf(t, s); len(got) != 0 {
				t.Errorf("questions after the refused call = %+v, want none", got)
			}
		})
	}
}

// parkChainNewTwice brings claimEpic to four parks the planner answered with no progress, the third of them called new:
// parks 1 and 2 on one chain, then park 3 at its bound called new with a retry, then park 4 on park 3's chain with a
// retry.
func parkChainNewTwice(t *testing.T, s *Store) {
	t.Helper()
	parkChainToBound(t, s)
	answerPark(t, s,
		EpicActionInput{Kind: ActionParkNew, Reason: "run 3 failed another way"},
		retryEpicAction("the check runs for the new failure"),
	)
	parkChainNode(t, s, 4)
	answerPark(t, s, retryEpicAction("the check runs for run 4"))
}

// TestParkChainNoProgressStreakSendsTheNextParkToTheOwner checks that once the planner has answered four parks in a row
// with no progress since the first of them, the next park goes to the owner although the planner called the last bound
// park new. Its question names the streak, then its chain's earlier parks and actions.
func TestParkChainNoProgressStreakSendsTheNextParkToTheOwner(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	parkChainNewTwice(t, s)
	parkChainNode(t, s, 5)

	text := parkChainReason(5) +
		"\n" + streakNote(streakBound) +
		"\nEarlier parks on this chain:" +
		"\n- " + parkChainReason(3) +
		"\n- " + parkChainReason(4) +
		"\nPlanner actions on this chain:" +
		"\n- park new: run 3 failed another way (taken)" +
		"\n- retry epic: the check runs for the new failure (taken)" +
		"\n- retry epic: the check runs for run 4 (taken)"
	requireOwnerPark(t, s, 5, text)
}

// TestParkChainProgressEndsTheStreak checks that the epic's progress after its fourth park ends the no-progress streak:
// a node run that ended passed, a release step that passed, or a stored base that moved. The fifth park then comes to its
// chain at the bound, with two taken actions, and waits on the planner's call instead of going to the owner. The bound
// chain names what changed since the chain's previous park.
func TestParkChainProgressEndsTheStreak(t *testing.T) {
	t.Parallel()
	passedAt := parkChainAt(4).Add(30 * time.Second)
	cases := []struct {
		name     string
		progress func(t *testing.T, s *Store)
		changed  func(base string) []string
	}{
		{
			name: "node run passed",
			progress: func(t *testing.T, s *Store) {
				t.Helper()
				startCheckRun(t, s)
				if err := s.EndNodes(context.Background(), claimEpic, []NodeEnd{
					{Path: retryBoundCheck, Outcome: "passed"},
				}, passedAt); err != nil {
					t.Fatalf("EndNodes: %v", err)
				}
			},
			changed: func(base string) []string {
				return []string{
					"stored base: unchanged at " + commitOrNone(base), "candidate: unchanged at none",
					"node runs passed since: " + retryBoundCheck, "release steps passed since: none",
				}
			},
		},
		{
			name: "release step passed",
			progress: func(t *testing.T, s *Store) {
				t.Helper()
				if err := s.RecordRelease(context.Background(), claimEpic, ReleaseRun{
					Part: ReleasePartStep, Position: 2, Attempt: 3, Step: "push main", Passed: true, At: passedAt,
				}); err != nil {
					t.Fatalf("RecordRelease: %v", err)
				}
			},
			changed: func(base string) []string {
				return []string{
					"stored base: unchanged at " + commitOrNone(base), "candidate: unchanged at none",
					"node runs passed since: none", "release steps passed since: step 2 at attempt 3",
				}
			},
		},
		{
			name: "stored base moved",
			progress: func(t *testing.T, s *Store) {
				t.Helper()
				if err := s.fixtureQueries().MoveEpicStoredBase(context.Background(), sqlcgen.MoveEpicStoredBaseParams{
					StoredBase: "moved-base", ID: claimEpic,
				}); err != nil {
					t.Fatalf("MoveEpicStoredBase: %v", err)
				}
			},
			changed: func(base string) []string {
				return []string{
					"stored base: " + commitOrNone(base) + " then, moved-base now", "candidate: unchanged at none",
					"node runs passed since: none", "release steps passed since: none",
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := openEpicStore(t)
			createEpic(t, s, fullEpic(claimEpic))
			base := closeRow(t, s, claimEpic).StoredBase
			parkChainNewTwice(t, s)
			tc.progress(t, s)
			parkChainNode(t, s, 5)

			if got := questionsOf(t, s); len(got) != 0 {
				t.Errorf("questions after progress = %+v, want none: progress ends the streak", got)
			}
			chain := boundChainOf(t, s)
			if chain.Seq != 5 || chain.Park != parkChainReason(5) {
				t.Errorf("bound chain = %+v, want park 5 at the bound", chain)
			}
			if want := tc.changed(base); !reflect.DeepEqual(chain.Changes, want) {
				t.Errorf("changes since the chain's previous park = %q, want %q", chain.Changes, want)
			}
		})
	}
}

// TestParkChainOwnerParkEndsTheStreak checks that a park the owner was sent ends the no-progress streak: once the owner
// retries the fifth park, which the streak sent to the owner, the sixth park waits on the planner's call.
func TestParkChainOwnerParkEndsTheStreak(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	parkChainNewTwice(t, s)
	parkChainNode(t, s, 5)
	if err := s.RetryEpic(context.Background(), claimEpic, Maker{By: MakerOwner}, parkChainAt(5)); err != nil {
		t.Fatalf("RetryEpic: %v", err)
	}
	parkChainNode(t, s, 6)

	if chain := boundChainOf(t, s); chain.Seq != 6 {
		t.Errorf("bound chain after the owner's retry = %+v, want park 6 waiting on the planner's call", chain)
	}
	if epic := closeRow(t, s, claimEpic); epic.ParkClass != ParkClassNode || epic.Parked != parkChainReason(6) {
		t.Errorf("park after the owner's retry = %q %q, want %q %q", epic.ParkClass, epic.Parked, ParkClassNode,
			parkChainReason(6))
	}
}

// parkChainCallsDownAndUp is the walk's check of 00118_park_chain_calls.sql: a park row stored as the owner's, with its
// heads, keeps its row when the Down drops its state and heads, and its Up adds them back as a park the planner answered
// with no heads, so a row's state and heads do not survive a Down.
func parkChainCallsDownAndUp() migrationCheck {
	return migrationCheck{
		seed: func(t *testing.T, s *Store) {
			t.Helper()
			if _, err := s.db.DB.Exec(`INSERT INTO epic_parks
				(epic, seq, class, subject, chain, reason, created, state, stored_base, candidate)
				VALUES ('repo/epic', 1, 'node', 'check', 1, 'the check failed', 1700000000, 'owner', 'base', 'cand')`,
			); err != nil {
				t.Fatalf("insert park row: %v", err)
			}
		},
		down: func(t *testing.T, s *Store) {
			t.Helper()
			if hasAny(columnsOf(t, s, "epic_parks"), "state", "stored_base", "candidate") {
				t.Errorf("epic_parks columns after Down = %v, want no state or heads", columnsOf(t, s, "epic_parks"))
			}
			var reason string
			if err := s.db.DB.QueryRow(`SELECT reason FROM epic_parks WHERE seq = 1`).Scan(&reason); err != nil ||
				reason != "the check failed" {
				t.Errorf("park row after Down = %q (err %v), want its reason kept", reason, err)
			}
		},
		up: func(t *testing.T, s *Store) {
			t.Helper()
			var state, base, candidate string
			if err := s.db.DB.QueryRow(`SELECT state, stored_base, candidate FROM epic_parks WHERE seq = 1`).Scan(
				&state, &base, &candidate); err != nil {
				t.Fatalf("read park row after Up: %v", err)
			}
			if state != parkStatePlanner || base != "" || candidate != "" {
				t.Errorf("park row after Down and Up = state %q, heads %q %q, want %q with no heads", state, base,
					candidate, parkStatePlanner)
			}
		},
	}
}
