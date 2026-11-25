package interpret

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// crashKind is the test-only kind every node of the crash flow has, plan nodes included.
const crashKind = "step"

// crashDoneRound is the round whose review reports done, the round a granted retry runs.
const crashDoneRound = 3

// crashPasses is how many passes one incarnation makes before it stops, a bound no run of the crash flow reaches.
const crashPasses = 8

// errCrashKilled is what a write returns once its incarnation is killed. The write is refused and nothing is stored.
var errCrashKilled = errors.New("run killed: write refused")

// crashStep is the test-only kind. Each of its nodes ends on its first decision. Review reports done in round
// crashDoneRound, and no other node reports an outcome.
type crashStep struct{}

// Decide ends the node run with the outcome its node reports in its round.
func (crashStep) Decide(node kinds.Node, _ kinds.Rows) kinds.Action {
	var outcome string
	if node.Def.ID == "review" && node.Run.Round == crashDoneRound {
		outcome = "done"
	}
	return kinds.Action{Kind: kinds.ActEnd, Outcome: outcome}
}

// crashFlow is the workflow the crash runs: a loop of work then review, at most two rounds, until review reports done,
// parking when its rounds are spent; then a step; then a plan, and a second plan that starts once a plan run exists and
// so writes a trigger.
func crashFlow() workflow.Workflow {
	refine := loopNode(workflow.Loop{Max: 2, Until: workflow.Outcome{Node: "review", Name: "done"}, Spent: "park"},
		kindNode("work", crashKind), kindNode("review", crashKind),
	)
	return workflow.Workflow{Name: "crash", Nodes: []workflow.Node{
		refine,
		kindNode("tail", crashKind),
		kindNode("plan", kindPlan),
		kindNode("replan", kindPlan),
	}}
}

// crashGate is one incarnation's write budget. It performs its first limit writes and refuses every write after them. A
// negative limit performs every write.
type crashGate struct {
	limit, count int
	dead         bool
}

// crashWorld is one run's store, and the log of its writes. Each incarnation builds fresh components over the store.
// Every write the interpreter makes, and the operator's retry, is an action of the world, performed through act in order.
type crashWorld struct {
	t      *testing.T
	store  *store.Store
	dbPath string

	mu     sync.Mutex
	gate   *crashGate
	labels []string
}

// crashTell is one tell of the epic as the store holds it.
type crashTell struct {
	seq  int64
	text string
	told bool
}

// crashTrigger is one trigger of the epic as the store holds it.
type crashTrigger struct {
	id, cause, state string
}

// crashEnd is what a run leaves behind: its writes in order, and the node runs, parked reason, tells and triggers of
// the epic.
type crashEnd struct {
	labels   []string
	runs     []store.NodeRun
	parked   string
	tells    []crashTell
	triggers []crashTrigger
}

// newCrashWorld returns a world over a new store that holds the test epic, with no pinned workflow text.
func newCrashWorld(t *testing.T) *crashWorld {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(context.Background(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	createRunEpic(t, s, store.EpicWorkflow{})
	return &crashWorld{t: t, store: s, dbPath: path, gate: &crashGate{limit: -1}}
}

// setGate starts an incarnation under a gate that performs limit writes.
func (w *crashWorld) setGate(limit int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.gate = &crashGate{limit: limit}
}

// killed reports whether the current incarnation's gate has refused a write.
func (w *crashWorld) killed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.gate.dead
}

// act performs op as the next write, labeled label, unless the incarnation is killed: the first write past the gate's
// limit is refused, and every write after it is refused too.
func (w *crashWorld) act(label string, op func() error) error {
	w.mu.Lock()
	g := w.gate
	if g.dead {
		w.mu.Unlock()
		return errCrashKilled
	}
	g.count++
	if g.limit >= 0 && g.count > g.limit {
		g.dead = true
		w.mu.Unlock()
		return errCrashKilled
	}
	w.labels = append(w.labels, label)
	w.mu.Unlock()
	if err := op(); err != nil {
		w.t.Errorf("write %q failed: %v", label, err)
		return err
	}
	return nil
}

// incarnation runs the crash flow over the world's store with a fresh runner, under a gate that performs limit writes.
// The operator retries the park once the runner has parked the epic. The incarnation returns when the run has ended or
// the gate refuses a write.
func (w *crashWorld) incarnation(limit int) {
	ctx := context.Background()
	w.setGate(limit)
	runner := newRunner(crashStore{Store: w.store, w: w}, &fakePlanner{}, &fakeBuilder{})
	runner.kinds = map[string]kinds.Kind{crashKind: crashStep{}, kindPlan: crashStep{}}
	for range crashPasses {
		runner.passFlow(ctx, store.EpicRow{ID: runEpic}, crashFlow())
		if w.killed() || w.finished(ctx) {
			return
		}
		if w.parked(ctx) && w.retry(ctx) != nil {
			return
		}
	}
	w.t.Errorf("the run did not end within %d passes", crashPasses)
}

// finished reports whether the run's last node has ended.
func (w *crashWorld) finished(ctx context.Context) bool {
	runs, err := w.store.NodeRuns(ctx, runEpic)
	if err != nil {
		w.t.Fatalf("NodeRuns: %v", err)
	}
	for _, run := range runs {
		if run.Path == "replan" {
			return run.State == store.NodeEnded
		}
	}
	return false
}

// parked reports whether the test epic is parked.
func (w *crashWorld) parked(ctx context.Context) bool {
	row, err := w.store.Epic(ctx, runEpic)
	if err != nil {
		w.t.Fatalf("Epic: %v", err)
	}
	return row.Parked != ""
}

// retry is the operator's retry of the park, one write of the world.
func (w *crashWorld) retry(ctx context.Context) error {
	return w.act("RetryEpic", func() error {
		return crashWrap(
			"retry epic",
			w.store.RetryEpic(ctx, runEpic, store.Maker{By: store.MakerOwner}, time.Unix(1700000000, 0)),
		)
	})
}

// end returns what the run has left behind so far.
func (w *crashWorld) end(t *testing.T) crashEnd {
	t.Helper()
	ctx := context.Background()
	runs, err := w.store.NodeRuns(ctx, runEpic)
	if err != nil {
		t.Fatalf("NodeRuns: %v", err)
	}
	row, err := w.store.Epic(ctx, runEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	w.mu.Lock()
	labels := slices.Clone(w.labels)
	w.mu.Unlock()
	return crashEnd{
		labels: labels, runs: runs, parked: row.Parked, tells: w.tells(t), triggers: w.triggers(t),
	}
}

// tells returns the tells of the test epic in seq order, read from the store's file.
func (w *crashWorld) tells(t *testing.T) []crashTell {
	t.Helper()
	var out []crashTell
	w.query(t, "SELECT seq, text, told FROM epic_tells WHERE epic = ? ORDER BY seq", func(rows *sql.Rows) error {
		var tell crashTell
		var told int64
		if err := rows.Scan(&tell.seq, &tell.text, &told); err != nil {
			return fmt.Errorf("scan tell: %w", err)
		}
		tell.told = told != 0
		out = append(out, tell)
		return nil
	})
	return out
}

// triggers returns the triggers of the test epic in id order, read from the store's file.
func (w *crashWorld) triggers(t *testing.T) []crashTrigger {
	t.Helper()
	var out []crashTrigger
	w.query(t, "SELECT id, cause, state FROM epic_triggers WHERE epic = ? ORDER BY id", func(rows *sql.Rows) error {
		var trigger crashTrigger
		if err := rows.Scan(&trigger.id, &trigger.cause, &trigger.state); err != nil {
			return fmt.Errorf("scan trigger: %w", err)
		}
		out = append(out, trigger)
		return nil
	})
	return out
}

// query runs text, which takes the test epic as its one argument, against the store's file, and calls scan on each row
// in order.
func (w *crashWorld) query(t *testing.T, text string, scan func(rows *sql.Rows) error) {
	t.Helper()
	db, err := sql.Open("sqlite3", w.dbPath)
	if err != nil {
		t.Fatalf("open store file: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close store file: %v", err)
		}
	}()
	rows, err := db.QueryContext(context.Background(), text, runEpic)
	if err != nil {
		t.Fatalf("query %q: %v", text, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close rows: %v", err)
		}
	}()
	for rows.Next() {
		if err := scan(rows); err != nil {
			t.Fatalf("read row of %q: %v", text, err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read rows of %q: %v", text, err)
	}
}

// crashWrap wraps err with what, or returns nil when err is nil.
func crashWrap(what string, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

// crashStore is the store the runner writes through. Each of its writes is one action of the world, and its reads go to
// the store as they are.
type crashStore struct {
	Store
	w *crashWorld
}

// StartNodes is one write of the world, labeled with the paths it starts and whether it writes a trigger.
func (s crashStore) StartNodes(
	ctx context.Context, epic string, runs []store.NodeRun, trigger *store.NodeTrigger, at time.Time,
) error {
	label := "start " + strings.Join(runPaths(runs), ",")
	if trigger != nil {
		label += " trigger"
	}
	return s.w.act(label, func() error {
		return crashWrap("start nodes", s.Store.StartNodes(ctx, epic, runs, trigger, at))
	})
}

// EndNodes is one write of the world, labeled with the paths it ends.
func (s crashStore) EndNodes(ctx context.Context, epic string, ends []store.NodeEnd, at time.Time) error {
	paths := make([]string, 0, len(ends))
	for _, end := range ends {
		paths = append(paths, end.Path)
	}
	return s.w.act("end "+strings.Join(paths, ","), func() error {
		return crashWrap("end nodes", s.Store.EndNodes(ctx, epic, ends, at))
	})
}

// NextRound is one write of the world, labeled with the loop and the round it moves past.
func (s crashStore) NextRound(ctx context.Context, epic, path string, from int64) error {
	return s.w.act(fmt.Sprintf("round %s from %d", path, from), func() error {
		return crashWrap("next round", s.Store.NextRound(ctx, epic, path, from))
	})
}

// RebuildTask is one write of the world, labeled with the build run and the attempt it ended at.
func (s crashStore) RebuildTask(ctx context.Context, epic, path string, attempt int64) error {
	return s.w.act(fmt.Sprintf("rebuild %s from %d", path, attempt), func() error {
		return crashWrap("rebuild task", s.Store.RebuildTask(ctx, epic, path, attempt))
	})
}

// ParkLoop is one write of the world, labeled with the loop and the round it parks in.
func (s crashStore) ParkLoop(ctx context.Context, epic, path string, round int64, reason string, at time.Time) error {
	return s.w.act(fmt.Sprintf("park %s at round %d", path, round), func() error {
		return crashWrap("park loop", s.Store.ParkLoop(ctx, epic, path, round, reason, at))
	})
}

// ParkEpic is one write of the world, labeled with the epic it parks.
func (s crashStore) ParkEpic(
	ctx context.Context,
	epic string,
	class store.ParkClass,
	reason string,
	at time.Time,
) error {
	return s.w.act("park epic "+epic, func() error {
		return crashWrap("park epic", s.Store.ParkEpic(ctx, epic, class, reason, at))
	})
}

// runPaths returns the paths of runs in order.
func runPaths(runs []store.NodeRun) []string {
	paths := make([]string, 0, len(runs))
	for _, run := range runs {
		paths = append(paths, run.Path)
	}
	return paths
}

// pathRun returns the node run at path among runs, and fails the test when there is none.
func pathRun(t *testing.T, runs []store.NodeRun, path string) store.NodeRun {
	t.Helper()
	for _, run := range runs {
		if run.Path == path {
			return run
		}
	}
	t.Fatalf("no node run at %s", path)
	return store.NodeRun{}
}

// crashUninterrupted runs the crash flow through to its end without a kill, checks that it reached each step the run is
// about, and returns what it left behind.
func crashUninterrupted(t *testing.T) crashEnd {
	t.Helper()
	w := newCrashWorld(t)
	w.incarnation(-1)
	got := w.end(t)
	wantOrder := []string{
		"start refine", "start refine@1/work", "end refine@1/work", "start refine@1/review", "end refine@1/review",
		"round refine from 1", "start refine@2/work", "end refine@2/work", "start refine@2/review",
		"end refine@2/review", "park refine at round 2", "RetryEpic", "round refine from 2",
		"start refine@3/work", "end refine@3/work", "start refine@3/review", "end refine@3/review", "end refine",
		"start tail", "end tail", "start plan", "end plan", "start replan trigger", "end replan",
	}
	if !slices.Equal(got.labels, wantOrder) {
		t.Errorf("writes = %q, want %q", got.labels, wantOrder)
	}
	loop := pathRun(t, got.runs, "refine")
	if loop.State != store.NodeEnded || loop.LoopRound != 3 || loop.Granted != 1 {
		t.Errorf("loop = %+v, want ended in round 3 with one granted round", loop)
	}
	if review := pathRun(t, got.runs, "refine@3/review"); review.Outcome != "done" {
		t.Errorf("review of round 3 outcome = %q, want done", review.Outcome)
	}
	if tail := pathRun(t, got.runs, "tail"); tail.State != store.NodeEnded {
		t.Errorf("tail = %+v, want ended", tail)
	}
	if replan := pathRun(t, got.runs, "replan"); replan.State != store.NodeEnded {
		t.Errorf("replan = %+v, want ended", replan)
	}
	// A loop park is a planner park: it adds no tell, and its trigger carries the park's reason.
	if len(got.tells) != 0 {
		t.Errorf("tells = %+v, want none: a loop park is the planner's", got.tells)
	}
	// The epic's own creation trigger is written by CreateEpic, before any pass; the loop's park trigger and the replan
	// trigger are the pass's.
	wantTriggers := []crashTrigger{
		{id: "created", cause: "epic created", state: "pending"},
		{id: "park:2", cause: "loop refine spent its 2 rounds without review.done", state: "pending"},
		{
			id:    "workflow:" + store.NodeRunID(runEpic, "replan", 0, "trigger"),
			cause: "workflow node replan started",
			state: "pending",
		},
	}
	if !reflect.DeepEqual(got.triggers, wantTriggers) {
		t.Errorf(
			"triggers = %+v, want the loop's park trigger and the replan trigger once: %+v",
			got.triggers,
			wantTriggers,
		)
	}
	if got.parked != "" {
		t.Errorf("epic parked = %q after the retry, want none", got.parked)
	}
	return got
}

// crashRunKilledAt runs the crash flow in a new world, killed after its first after writes, then restarts it with a
// fresh runner over the same store.
func crashRunKilledAt(t *testing.T, after int) crashEnd {
	t.Helper()
	w := newCrashWorld(t)
	w.incarnation(after)
	w.incarnation(-1)
	return w.end(t)
}

// crashCompare runs the crash flow killed after its first after writes, restarts it, and checks what it left behind
// against the run that was never killed: the same writes in the same order, each performed once, and the same node
// runs, parked reason, tells and triggers.
func crashCompare(t *testing.T, after int, want crashEnd) {
	t.Helper()
	got := crashRunKilledAt(t, after)
	if !slices.Equal(got.labels, want.labels) {
		t.Errorf("writes after a kill after write %d = %q, want %q", after, got.labels, want.labels)
	}
	performed := make(map[string]int)
	for _, label := range got.labels {
		performed[label]++
	}
	for label, count := range performed {
		if count != 1 {
			t.Errorf("write %q was performed %d times after a kill after write %d, want once", label, count, after)
		}
	}
	ids := make(map[string]bool)
	for _, run := range got.runs {
		if ids[run.ID] {
			t.Errorf("node run id %s is stored twice after a kill after write %d", run.ID, after)
		}
		ids[run.ID] = true
	}
	if !reflect.DeepEqual(got.runs, want.runs) {
		t.Errorf("node runs after a kill after write %d = %+v, want %+v", after, got.runs, want.runs)
	}
	if got.parked != want.parked {
		t.Errorf("epic parked = %q after a kill after write %d, want %q", got.parked, after, want.parked)
	}
	if !reflect.DeepEqual(got.tells, want.tells) {
		t.Errorf("tells = %+v after a kill after write %d, want %+v", got.tells, after, want.tells)
	}
	if !reflect.DeepEqual(got.triggers, want.triggers) {
		t.Errorf("triggers = %+v after a kill after write %d, want %+v", got.triggers, after, want.triggers)
	}
}

// TestCrashPointRestartsToTheUninterruptedRows kills the crash run after each write the interpreter makes, and the
// operator's retry, restarts it with a fresh runner over the same store, and checks each restart against the run that
// was never killed. Each kill point is a subtest.
func TestCrashPointRestartsToTheUninterruptedRows(t *testing.T) {
	t.Parallel()
	want := crashUninterrupted(t)
	t.Run("kill points", func(t *testing.T) {
		t.Parallel()
		for after := 1; after <= len(want.labels); after++ {
			t.Run(fmt.Sprintf("after write %02d", after), func(t *testing.T) {
				t.Parallel()
				crashCompare(t, after, want)
			})
		}
	})
}
