package store_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// noRoleFile is the park a planner turn frees with when the planner role does not read.
const noRoleFile = "no role file /home/user/.config/orrery/roles.toml"

// verifyFailed is the node park of the failed verify gate the retry tests start from.
const verifyFailed = "workflow node verify failed: exit 1"

// turnEndedAt is the second the retry tests end their turns at.
func turnEndedAt() time.Time {
	return time.Unix(1700001400, 0)
}

// parkVerifyNode starts the verify gate of workEpic and parks it failed as a node park, which adds the pending trigger
// that wakes the planner.
func parkVerifyNode(t *testing.T, s *store.Store) {
	t.Helper()
	ctx := context.Background()
	if err := s.StartNodes(ctx, workEpic, []store.NodeRun{
		{Path: "verify", ID: "id-verify", Node: "verify", Kind: "gate", Attempt: 1},
	}, nil, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if err := s.ParkNode(
		ctx, workEpic, "verify", 1, "failed", "exit 1", store.ParkClassNode, verifyFailed, loopParkedAt(),
	); err != nil {
		t.Fatalf("ParkNode: %v", err)
	}
}

// claimWorkTurn claims a planner turn of workEpic and returns its seq.
func claimWorkTurn(t *testing.T, s *store.Store) int64 {
	t.Helper()
	turn, err := s.ClaimTurn(context.Background(), workEpic, nodeEndedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	return turn.Seq
}

// epicParkOf returns the park reason, park class and retried seq stored for epic.
func epicParkOf(t *testing.T, raw *sql.DB, epic string) (string, string, int64) {
	t.Helper()
	var parked, class string
	var retried int64
	if err := raw.QueryRow(
		`SELECT parked, park_class, retried FROM epics WHERE id = ?`, epic,
	).Scan(&parked, &class, &retried); err != nil {
		t.Fatalf("read park of epic %s: %v", epic, err)
	}
	return parked, class, retried
}

// openParkQuestions returns the seq and text of each open park question of epic.
func openParkQuestions(t *testing.T, raw *sql.DB, epic string) [][]any {
	t.Helper()
	return rawRows(t, raw,
		`SELECT seq, text FROM epic_questions WHERE epic = ? AND label = ? AND state = 'open' ORDER BY seq`,
		epic, store.ParkLabel)
}

// turnRows returns the seq, state and reason of each turn of epic.
func turnRows(t *testing.T, raw *sql.DB, epic string) [][]any {
	t.Helper()
	return rawRows(t, raw, `SELECT seq, state, reason FROM epic_turns WHERE epic = ? ORDER BY seq`, epic)
}

// TestFreeTurnParkAfterARetrySinceTheClaimLeavesTheEpicUnparked checks that a turn claimed on a node-parked epic, whose
// owner retries the epic while the turn runs, ends free without its park: the retry prevails, so the epic stays
// unparked with no park question, and the retried node stays rerun. Without the retry the same free parks the epic.
// The turn's park is decided under the node park, so a retry that lands between that decision and the claim voids
// it the same way.
func TestFreeTurnParkAfterARetrySinceTheClaimLeavesTheEpicUnparked(t *testing.T) {
	t.Parallel()
	retryNode := func(ctx context.Context, s *store.Store) error { return s.RetryNode(ctx, workEpic, "verify") }
	retryEpic := func(ctx context.Context, s *store.Store) error { return s.RetryEpic(ctx, workEpic) }
	rerun := []store.NodeRun{{Path: "verify", ID: "id-verify", Node: "verify", Kind: "gate", Attempt: 2,
		State: store.NodeRunning, Started: nodeStartedAt()}}
	stillParked := []store.NodeRun{{Path: "verify", ID: "id-verify", Node: "verify", Kind: "gate", Attempt: 1,
		Outcome: "failed", Output: "exit 1", State: store.NodeParked, Started: nodeStartedAt()}}
	decided := verifyFailed
	retries := []struct {
		name    string
		retry   func(context.Context, *store.Store) error
		before  bool
		decided *string
		runs    []store.NodeRun
		retried int64
	}{
		{name: "RetryNode after the claim", retry: retryNode, runs: rerun, retried: 1},
		{name: "RetryEpic after the claim", retry: retryEpic, runs: stillParked, retried: 1},
		{name: "RetryNode after the claim, decided under the node park", retry: retryNode, decided: &decided,
			runs: rerun, retried: 1},
		{name: "RetryNode before the claim, decided under the node park", retry: retryNode, before: true,
			decided: &decided, runs: rerun, retried: 0},
		{name: "RetryEpic before the claim, decided under the node park", retry: retryEpic, before: true,
			decided: &decided, runs: stillParked, retried: 0},
	}
	for _, tc := range retries {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s, raw := openWorkStore(t)
			parkVerifyNode(t, s)
			if tc.before {
				if err := tc.retry(ctx, s); err != nil {
					t.Fatalf("retry: %v", err)
				}
			}
			seq := claimWorkTurn(t, s)
			if !tc.before {
				if err := tc.retry(ctx, s); err != nil {
					t.Fatalf("retry: %v", err)
				}
			}
			if err := s.FreeTurn(ctx, workEpic, seq, store.TurnFree{
				Reason: noRoleFile, Park: noRoleFile, DecidedUnder: tc.decided,
			}, turnEndedAt()); err != nil {
				t.Fatalf("FreeTurn: %v", err)
			}

			parked, class, retried := epicParkOf(t, raw, workEpic)
			if parked != "" || class != "" {
				t.Errorf("park after the retry then FreeTurn = %q class %q, want unparked: the retry prevails",
					parked, class)
			}
			if retried != tc.retried {
				t.Errorf("retried = %d, want %d", retried, tc.retried)
			}
			if open := openParkQuestions(t, raw, workEpic); len(open) != 0 {
				t.Errorf("open park questions = %v, want none", open)
			}
			want := [][]any{{seq, "free", noRoleFile}}
			if got := turnRows(t, raw, workEpic); !reflect.DeepEqual(got, want) {
				t.Errorf("turns = %v, want %v", got, want)
			}
			requireNodeRuns(t, s, workEpic, tc.runs)
			if pending := pendingTriggerCount(t, raw, workEpic); pending == 0 {
				t.Errorf("pending triggers after the free = 0, want the claimed triggers pending again")
			}
		})
	}

	t.Run("no retry, decided under the node park", func(t *testing.T) {
		t.Parallel()
		s, raw := openWorkStore(t)
		parkVerifyNode(t, s)
		seq := claimWorkTurn(t, s)
		if err := s.FreeTurn(context.Background(), workEpic, seq, store.TurnFree{
			Reason: noRoleFile, Park: noRoleFile, DecidedUnder: &decided,
		}, turnEndedAt()); err != nil {
			t.Fatalf("FreeTurn: %v", err)
		}
		parked, class, _ := epicParkOf(t, raw, workEpic)
		if parked != noRoleFile || class != string(store.ParkClassOrchestrator) {
			t.Errorf("park after FreeTurn = %q class %q, want %q class %q", parked, class, noRoleFile,
				store.ParkClassOrchestrator)
		}
		if open := openParkQuestions(t, raw, workEpic); len(open) != 1 {
			t.Errorf("open park questions = %v, want the orchestrator park's one", open)
		}
	})
}

// TestFailTurnAfterARetrySinceTheClaimDoesNotParkTheEpic checks that a third failed turn in a row parks no epic whose
// owner retried it while that turn ran: the turn is at or below the retried seq, so it does not count toward a park.
func TestFailTurnAfterARetrySinceTheClaimDoesNotParkTheEpic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openWorkStore(t)
	parkVerifyNode(t, s)
	for range 2 {
		seq := claimWorkTurn(t, s)
		if err := s.FailTurn(ctx, workEpic, seq, "boom", turnEndedAt()); err != nil {
			t.Fatalf("FailTurn of turn %d: %v", seq, err)
		}
	}
	seq := claimWorkTurn(t, s)
	if err := s.RetryNode(ctx, workEpic, "verify"); err != nil {
		t.Fatalf("RetryNode: %v", err)
	}
	if err := s.FailTurn(ctx, workEpic, seq, "boom", turnEndedAt()); err != nil {
		t.Fatalf("FailTurn of turn %d: %v", seq, err)
	}
	if parked, class, _ := epicParkOf(t, raw, workEpic); parked != "" || class != "" {
		t.Errorf("park after a retry then a third failure = %q class %q, want unparked", parked, class)
	}
	if open := openParkQuestions(t, raw, workEpic); len(open) != 0 {
		t.Errorf("open park questions = %v, want none", open)
	}
}

// pendingTriggerCount returns how many triggers of epic are pending.
func pendingTriggerCount(t *testing.T, raw *sql.DB, epic string) int64 {
	t.Helper()
	var pending int64
	if err := raw.QueryRow(
		`SELECT COUNT(*) FROM epic_triggers WHERE epic = ? AND state = 'pending'`, epic,
	).Scan(&pending); err != nil {
		t.Fatalf("count pending triggers of epic %s: %v", epic, err)
	}
	return pending
}
