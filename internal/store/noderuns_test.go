package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// nodeEpic is the epic the node run tests write to, and nodeOther a second epic whose rows they must not touch.
const (
	nodeEpic  = "orrery-7a8b9c0/epic-nodes"
	nodeOther = "orrery-7a8b9c0/epic-other"
)

// nodeStartedAt is the second the node run tests start runs at, and nodeEndedAt the second they end them at.
func nodeStartedAt() time.Time {
	return time.Unix(1700001000, 0)
}

func nodeEndedAt() time.Time {
	return time.Unix(1700001100, 0)
}

// openNodeStore opens a migrated store file and returns the store with a raw handle to the same file, for the trigger
// rows the store API does not return whole.
func openNodeStore(t *testing.T) (*store.Store, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw handle: %v", err)
	}
	t.Cleanup(func() {
		if err := raw.Close(); err != nil {
			t.Errorf("close raw handle: %v", err)
		}
	})
	return s, raw
}

// epicTriggerRow is the stored row of a trigger of an epic, read from the table.
type epicTriggerRow struct {
	state   string
	turn    int64
	cause   string
	created int64
}

// triggerRowOf returns the stored row of trigger id of epic.
func triggerRowOf(t *testing.T, raw *sql.DB, epic, id string) epicTriggerRow {
	t.Helper()
	var row epicTriggerRow
	if err := raw.QueryRow(
		`SELECT state, turn, cause, created FROM epic_triggers WHERE epic = ? AND id = ?`, epic, id,
	).Scan(&row.state, &row.turn, &row.cause, &row.created); err != nil {
		t.Fatalf("read trigger %s of epic %s: %v", id, epic, err)
	}
	return row
}

// setTriggerState stores state and turn on trigger id of epic, as a turn that claims or consumes it does.
func setTriggerState(t *testing.T, raw *sql.DB, epic, id, state string, turn int64) {
	t.Helper()
	if _, err := raw.Exec(
		`UPDATE epic_triggers SET state = ?, turn = ? WHERE epic = ? AND id = ?`, state, turn, epic, id,
	); err != nil {
		t.Fatalf("set trigger %s of epic %s to %s: %v", id, epic, state, err)
	}
}

// pendingTriggersOf returns how many triggers of epic are stored pending, read from the table.
func pendingTriggersOf(t *testing.T, raw *sql.DB, epic string) int {
	t.Helper()
	var count int
	if err := raw.QueryRow(
		`SELECT COUNT(*) FROM epic_triggers WHERE epic = ? AND state = 'pending'`, epic,
	).Scan(&count); err != nil {
		t.Fatalf("count pending triggers of epic %s: %v", epic, err)
	}
	return count
}

// TestStartNodesStoresEveryFieldOrderedByPath checks that NodeRuns returns every run StartNodes stored with each field,
// running from the start second, ordered by path, and only the runs of the epic asked for.
func TestStartNodesStoresEveryFieldOrderedByPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openNodeStore(t)

	stored := []store.NodeRun{
		{Path: "plan/build/T3", ID: "id-task-3", Node: "build", Kind: "build", Task: 3},
		{Path: "plan", ID: "id-plan", Node: "plan", Kind: "plan", Mark: 4},
		{Path: "fix@2/build", ID: "id-fix-build", Node: "build", Kind: "build", Round: 2},
		{Path: "fix", ID: "id-fix", Node: "fix", Kind: "loop", LoopRound: 2, Granted: 1},
	}
	if err := s.StartNodes(ctx, nodeEpic, stored, nil, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes of epic %s: %v", nodeEpic, err)
	}
	other := []store.NodeRun{{Path: "plan", ID: "id-other-plan", Node: "plan", Kind: "plan"}}
	if err := s.StartNodes(ctx, nodeOther, other, nil, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes of epic %s: %v", nodeOther, err)
	}

	want := []store.NodeRun{
		{Path: "fix", ID: "id-fix", Node: "fix", Kind: "loop", LoopRound: 2, Granted: 1,
			State: store.NodeRunning, Started: nodeStartedAt()},
		{Path: "fix@2/build", ID: "id-fix-build", Node: "build", Kind: "build", Round: 2,
			State: store.NodeRunning, Started: nodeStartedAt()},
		{Path: "plan", ID: "id-plan", Node: "plan", Kind: "plan", Mark: 4,
			State: store.NodeRunning, Started: nodeStartedAt()},
		{Path: "plan/build/T3", ID: "id-task-3", Node: "build", Kind: "build", Task: 3,
			State: store.NodeRunning, Started: nodeStartedAt()},
	}
	got, err := s.NodeRuns(ctx, nodeEpic)
	if err != nil {
		t.Fatalf("NodeRuns of epic %s: %v", nodeEpic, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NodeRuns of epic %s = %+v, want %+v", nodeEpic, got, want)
	}

	gotOther, err := s.NodeRuns(ctx, nodeOther)
	if err != nil {
		t.Fatalf("NodeRuns of epic %s: %v", nodeOther, err)
	}
	wantOther := []store.NodeRun{
		{Path: "plan", ID: "id-other-plan", Node: "plan", Kind: "plan",
			State: store.NodeRunning, Started: nodeStartedAt()},
	}
	if !reflect.DeepEqual(gotOther, wantOther) {
		t.Errorf("NodeRuns of epic %s = %+v, want %+v", nodeOther, gotOther, wantOther)
	}
}

// TestStartNodesRepeatChangesNothing checks that a second StartNodes under a stored path leaves the run and the
// trigger with that id as the first stored them, whatever fields, seconds and causes the repeat carries.
func TestStartNodesRepeatChangesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openNodeStore(t)

	first := store.NodeRun{Path: "plan", ID: "id-plan", Node: "plan", Kind: "plan", Mark: 0}
	trigger := store.NodeTrigger{ID: "workflow:id-plan", Cause: "workflow node plan started"}
	if err := s.StartNodes(ctx, nodeEpic, []store.NodeRun{first}, &trigger, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}

	repeat := store.NodeRun{Path: "plan", ID: "id-plan", Node: "other", Kind: "loop", LoopRound: 7, Mark: 9}
	again := store.NodeTrigger{ID: trigger.ID, Cause: "a different cause"}
	if err := s.StartNodes(ctx, nodeEpic, []store.NodeRun{repeat}, &again, nodeEndedAt()); err != nil {
		t.Fatalf("repeat StartNodes: %v", err)
	}

	want := []store.NodeRun{
		{Path: "plan", ID: "id-plan", Node: "plan", Kind: "plan", Mark: 0,
			State: store.NodeRunning, Started: nodeStartedAt()},
	}
	got, err := s.NodeRuns(ctx, nodeEpic)
	if err != nil {
		t.Fatalf("NodeRuns: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NodeRuns after a repeat = %+v, want %+v", got, want)
	}

	row := triggerRowOf(t, raw, nodeEpic, trigger.ID)
	if row.cause != trigger.Cause || row.state != "pending" || row.created != nodeStartedAt().Unix() {
		t.Errorf("trigger after a repeat = %+v, want pending, cause %q, created %d",
			row, trigger.Cause, nodeStartedAt().Unix())
	}
}

// TestStartNodesAddsTriggerOnceAndNotAfterItIsClaimed checks that a trigger is added pending once, that a repeat while
// it is pending adds nothing, and that a repeat after a turn claimed it adds nothing and leaves it claimed.
func TestStartNodesAddsTriggerOnceAndNotAfterItIsClaimed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openNodeStore(t)

	trigger := store.NodeTrigger{ID: "workflow:id-plan", Cause: "workflow node plan started"}
	if err := s.StartNodes(ctx, nodeEpic, nil, &trigger, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if got := pendingTriggersOf(t, raw, nodeEpic); got != 1 {
		t.Fatalf("pending triggers after the first start = %d, want 1", got)
	}
	row := triggerRowOf(t, raw, nodeEpic, trigger.ID)
	if row.state != "pending" || row.turn != 0 || row.cause != trigger.Cause || row.created != nodeStartedAt().Unix() {
		t.Errorf("trigger after the first start = %+v, want pending turn 0 cause %q created %d",
			row, trigger.Cause, nodeStartedAt().Unix())
	}

	if err := s.StartNodes(ctx, nodeEpic, nil, &trigger, nodeEndedAt()); err != nil {
		t.Fatalf("repeat StartNodes while pending: %v", err)
	}
	if got := pendingTriggersOf(t, raw, nodeEpic); got != 1 {
		t.Fatalf("pending triggers after a repeat while pending = %d, want 1", got)
	}

	setTriggerState(t, raw, nodeEpic, trigger.ID, "claimed", 5)
	if err := s.StartNodes(ctx, nodeEpic, nil, &trigger, nodeEndedAt()); err != nil {
		t.Fatalf("repeat StartNodes after a claim: %v", err)
	}
	if got := pendingTriggersOf(t, raw, nodeEpic); got != 0 {
		t.Fatalf("pending triggers after a repeat past a claim = %d, want 0", got)
	}
	row = triggerRowOf(t, raw, nodeEpic, trigger.ID)
	if row.state != "claimed" || row.turn != 5 {
		t.Errorf("trigger after a repeat past a claim = %+v, want claimed by turn 5", row)
	}
}

// TestEndNodesEndsOnlyRunningRunsOnce checks that EndNodes ends a running run with its outcome and end second, that an
// ended run keeps its first outcome and second, and that an epic's ends leave another epic's run at the same path.
func TestEndNodesEndsOnlyRunningRunsOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openNodeStore(t)

	runs := []store.NodeRun{
		{Path: "plan", ID: "id-plan", Node: "plan", Kind: "plan"},
		{Path: "fix", ID: "id-fix", Node: "fix", Kind: "loop", LoopRound: 1},
	}
	if err := s.StartNodes(ctx, nodeEpic, runs, nil, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes of epic %s: %v", nodeEpic, err)
	}
	other := []store.NodeRun{{Path: "plan", ID: "id-other-plan", Node: "plan", Kind: "plan"}}
	if err := s.StartNodes(ctx, nodeOther, other, nil, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes of epic %s: %v", nodeOther, err)
	}

	if err := s.EndNodes(
		ctx,
		nodeEpic,
		[]store.NodeEnd{{Path: "plan", Outcome: "complete"}},
		nodeEndedAt(),
	); err != nil {
		t.Fatalf("EndNodes: %v", err)
	}
	if err := s.EndNodes(ctx, nodeEpic, []store.NodeEnd{
		{Path: "plan", Outcome: "failed"},
		{Path: "fix", Outcome: "complete"},
	}, time.Unix(1700001200, 0)); err != nil {
		t.Fatalf("repeat EndNodes: %v", err)
	}

	want := []store.NodeRun{
		{Path: "fix", ID: "id-fix", Node: "fix", Kind: "loop", LoopRound: 1,
			State: store.NodeEnded, Outcome: "complete", Started: nodeStartedAt(), Ended: time.Unix(1700001200, 0)},
		{Path: "plan", ID: "id-plan", Node: "plan", Kind: "plan",
			State: store.NodeEnded, Outcome: "complete", Started: nodeStartedAt(), Ended: nodeEndedAt()},
	}
	got, err := s.NodeRuns(ctx, nodeEpic)
	if err != nil {
		t.Fatalf("NodeRuns of epic %s: %v", nodeEpic, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NodeRuns of epic %s after EndNodes = %+v, want %+v", nodeEpic, got, want)
	}

	gotOther, err := s.NodeRuns(ctx, nodeOther)
	if err != nil {
		t.Fatalf("NodeRuns of epic %s: %v", nodeOther, err)
	}
	wantOther := []store.NodeRun{
		{Path: "plan", ID: "id-other-plan", Node: "plan", Kind: "plan",
			State: store.NodeRunning, Started: nodeStartedAt()},
	}
	if !reflect.DeepEqual(gotOther, wantOther) {
		t.Errorf("NodeRuns of epic %s = %+v, want %+v", nodeOther, gotOther, wantOther)
	}
}

// TestNodeRunIDMatchesFormAndDiffers checks that NodeRunID is the first 32 hex digits of the sha256 of the namespace,
// epic, path, round and cause joined with NUL, and that changing any one of those parts changes the id. The expected
// value is computed outside the package with bash printf and sha256sum.
func TestNodeRunIDMatchesFormAndDiffers(t *testing.T) {
	t.Parallel()
	const (
		epic  = "orrery-7a8b9c0/epic-nodes"
		path  = "fix@2/build"
		round = int64(2)
		cause = "turn 4 task 2"
		want  = "41af9aa5612d811c7c36cc3a73fffc30"
	)
	if got := store.NodeRunID(epic, path, round, cause); got != want {
		t.Errorf("NodeRunID = %q, want %q", got, want)
	}

	differs := map[string]string{
		"epic":  store.NodeRunID("orrery-7a8b9c0/epic-other", path, round, cause),
		"path":  store.NodeRunID(epic, "fix@3/build", round, cause),
		"round": store.NodeRunID(epic, path, 3, cause),
		"cause": store.NodeRunID(epic, path, round, "turn 4 task 3"),
	}
	for part, got := range differs {
		if got == want {
			t.Errorf("NodeRunID with another %s = %q, the same as the base id", part, got)
		}
	}
}
