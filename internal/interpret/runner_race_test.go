package interpret

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// turnCommit is the store a test wraps to commit a planner turn's CompleteTurn right after the pass reads the epic and
// before it decides. The commit happens once, on the first read, which is the interleaving review I1 found.
type turnCommit struct {
	Store
	s    *store.Store
	turn int64
	plan store.EpicPlan
	err  error
	done bool
}

// EpicFlow reads the epic's rows through the wrapped store, then commits the turn.
func (c *turnCommit) EpicFlow(ctx context.Context, id string) (store.EpicFlow, error) {
	flow, err := c.Store.EpicFlow(ctx, id)
	c.commitOnce(ctx)
	return flow, err
}

// commitOnce completes the turn with the plan, unless it has been committed already.
func (c *turnCommit) commitOnce(ctx context.Context) {
	if c.done {
		return
	}
	c.done = true
	c.err = c.s.CompleteTurn(ctx, runEpic, c.turn, c.plan, runClock())
}

// TestTurnCommittedAfterEpicReadBuildsItsTasks: a planner turn that completes between the pass's read of the epic and
// its decision leaves its build children running while their tasks are open, and the next pass hands both tasks to the
// builder.
func TestTurnCommittedAfterEpicReadBuildsItsTasks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, builtinPin(t))
	newRunner(s, &fakePlanner{known: []string{runEpic}}, &fakeBuilder{}).Pass(ctx)
	turn, err := s.ClaimTurn(ctx, runEpic, runClock())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}

	hook := &turnCommit{Store: s, s: s, turn: turn.Seq, plan: runPlan("complete")}
	builder := &fakeBuilder{}
	runner := newRunner(hook, &fakePlanner{known: []string{runEpic}}, builder)
	runner.Pass(ctx)
	if hook.err != nil {
		t.Fatalf("CompleteTurn between the reads: %v", hook.err)
	}
	for _, seq := range []int64{1, 2} {
		path := fmt.Sprintf("plan/build/T%d", seq)
		if run := runAt(t, s, path); run.State != store.NodeRunning {
			t.Errorf("child %s = %+v, want running while its task is open", path, run)
		}
	}

	runner.Pass(ctx)
	if want := []taskPass{{epic: runEpic, seqs: []int64{1, 2}}}; !reflect.DeepEqual(builder.passes, want) {
		t.Errorf("PassTasks calls = %+v, want %+v", builder.passes, want)
	}
}
