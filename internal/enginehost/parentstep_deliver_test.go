package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// passRounds is a CallerPass that counts the rounds of the pass it wraps, each counted once the pass has returned. A test
// waits on the count to know that a round has handled the wake it sent.
type passRounds struct {
	pass   CallerPass
	rounds atomic.Int64
}

// Pass runs the wrapped pass and counts the round.
func (p *passRounds) Pass(ctx context.Context) error {
	err := p.pass.Pass(ctx)
	p.rounds.Add(1)
	return err
}

// neverTimer is a CallerEventsConfig NewTimer whose timer never fires, so no retry runs in a test.
func neverTimer(time.Duration) (<-chan time.Time, func() bool) {
	return make(chan time.Time), func() bool { return true }
}

// passingReturn is the return check of a parent whose done return has no failure, so the return ends the step without a
// judge call.
func passingReturn(json.RawMessage) []string { return nil }

// deliverRig is a parent rig wired as orreryd wires a step's end: a CallerSender over the rig's store whose DeliverStep is
// the rig's Steps.DeliverChildEnd, a StepCallerEvents over the store that sends through it, and a CallerEvents that runs
// that pass on its start and on each wake, whose Wake is the Steps' Stored hook. pass counts the rounds of that pass.
type deliverRig struct {
	parentRig
	db   *store.Store
	pass *passRounds
}

// newDeliverRig returns a parent rig wired as deliverRig describes. Its CallerEvents runs until the test ends, and the
// round it runs on its start has finished when newDeliverRig returns. A test takes the time of the real step starts and
// stops it makes, each of which runs git and starts or stops a holder and a fake claude process, as the TestParentStepOpen
// tests do. A test waits only on stored rows, the fake claude's files, or the run's own state, and never on a real clock.
func newDeliverRig(t *testing.T) deliverRig {
	t.Helper()
	rig := newParentRig(t)
	db, ok := rig.spy.StepStore.(*store.Store)
	if !ok {
		t.Fatalf("step store is %T, want *store.Store", rig.spy.StepStore)
	}
	sender := NewCallerSender(CallerSenderConfig{
		Deliver:     rig.manager.Deliver,
		DeliverStep: rig.steps.DeliverChildEnd,
		WakePane: func(context.Context, string, string, string) (bool, error) {
			t.Error("a pane caller was woken in a step test")
			return false, nil
		},
		Store: db,
		Now:   fixedClock,
	})
	pass := &passRounds{pass: NewStepCallerEvents(StepCallerEventsConfig{Store: db, Sender: sender})}
	events := NewCallerEvents(CallerEventsConfig{Passes: []CallerPass{pass}, NewTimer: neverTimer})
	rig.steps.stored = events.Wake
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		events.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	waittest.Until(t, nil, "the caller-event start round", func() bool { return pass.rounds.Load() > 0 })
	return deliverRig{parentRig: rig, db: db, pass: pass}
}

// childEvent returns the event of the end of the step child: the PromptID its end is delivered under.
func childEvent(child string) string {
	return stepEventPrefix + child
}

// setNext writes the next file of the fake claude of the step id. The fake takes it in place of the text of the next
// prompt it reads, and removes it.
func (r deliverRig) setNext(t *testing.T, id, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.worktrees, id, "fakeclaude.next"), []byte(text), 0o600); err != nil {
		t.Fatalf("write fakeclaude.next of step %s: %v", id, err)
	}
}

// nextTaken reports whether the fake claude of the step id has removed its next file, which it does once it reads the
// prompt the file replaces.
func (r deliverRig) nextTaken(id string) bool {
	_, err := os.Stat(filepath.Join(r.worktrees, id, "fakeclaude.next"))
	return errors.Is(err, os.ErrNotExist)
}

// holdTurn removes the release file of the step id, so the turn its fake claude holds for a release waits again.
func (r deliverRig) holdTurn(t *testing.T, id string) {
	t.Helper()
	if err := os.Remove(filepath.Join(r.worktrees, id, "fakeclaude.release")); err != nil {
		t.Fatalf("remove fakeclaude.release of step %s: %v", id, err)
	}
}

// marked reports whether the end of child is marked delivered to parent.
func (r deliverRig) marked(t *testing.T, child, parent string) bool {
	t.Helper()
	ok, err := r.db.CallerEventMarked(t.Context(), childEvent(child), parent)
	if err != nil {
		t.Fatalf("CallerEventMarked of %s for %s: %v", childEvent(child), parent, err)
	}
	return ok
}

// waitMarked waits until the end of child is marked delivered to parent.
func (r deliverRig) waitMarked(t *testing.T, child, parent string) {
	t.Helper()
	waittest.Until(t, nil, "the end of step "+child+" to be marked delivered to "+parent, func() bool {
		return r.marked(t, child, parent)
	})
}

// blocksOf returns how many prompts of the step parent carry the end block of child. Each prompt that carries the block
// counts once, so a block sent twice counts twice.
func (r deliverRig) blocksOf(t *testing.T, parent, child string) int {
	t.Helper()
	prompts, err := r.db.HolderPrompts(t.Context(), parent)
	if err != nil {
		t.Fatalf("read prompts of step %s: %v", parent, err)
	}
	tag := `id="` + childEvent(child) + `"`
	n := 0
	for _, prompt := range prompts {
		if strings.Contains(prompt.Text, tag) {
			n++
		}
	}
	return n
}

// promptSent reports whether a prompt of the step parent carries the PromptID of the end of child.
func (r deliverRig) promptSent(t *testing.T, parent, child string) bool {
	t.Helper()
	sent, err := r.db.EnginePromptSent(t.Context(), parent, childEvent(child))
	if err != nil {
		t.Fatalf("read prompt %s of step %s: %v", childEvent(child), parent, err)
	}
	return sent
}

// requireBlockOnce fails the test unless the end of child is one prompt of parent, with the end's event as its PromptID.
func (r deliverRig) requireBlockOnce(t *testing.T, parent, child string) {
	t.Helper()
	if n := r.blocksOf(t, parent, child); n != 1 {
		t.Fatalf("prompts of step %s carry the end of step %s %d times, want once", parent, child, n)
	}
	if !r.promptSent(t, parent, child) {
		t.Fatalf("no prompt of step %s has %s as its PromptID", parent, childEvent(child))
	}
}

// requireNoBlock fails the test if any prompt of parent carries the end of child, or has its event as its PromptID.
func (r deliverRig) requireNoBlock(t *testing.T, parent, child string) {
	t.Helper()
	if n := r.blocksOf(t, parent, child); n != 0 {
		t.Fatalf("prompts of step %s carry the end of step %s %d times, want none", parent, child, n)
	}
	if r.promptSent(t, parent, child) {
		t.Fatalf("a prompt of step %s has %s as its PromptID, want none", parent, childEvent(child))
	}
}

// waitEnded waits until the step id has ended, and returns its row.
func (r deliverRig) waitEnded(t *testing.T, id string) store.StepRow {
	t.Helper()
	var row store.StepRow
	waitUntil(t, engineOf(r.manager, r.steps), "step "+id+" to end", func() bool {
		row = stepRowOf(t, r.steps, id)
		return row.End != ""
	})
	return row
}

// stopChild stops the child step, so its end is stored and published, and fails the test on the error of the stop.
func (r deliverRig) stopChild(t *testing.T, child string) {
	t.Helper()
	if err := r.steps.Stop(t.Context(), child); err != nil {
		t.Fatalf("Stop of step %s: %v", child, err)
	}
}

// heldTurnEnded reports whether the loop of run has handled the end of its held turn.
func heldTurnEnded(t *testing.T, run *stepRun) bool {
	t.Helper()
	var ended bool
	if err := run.do(t.Context(), func(context.Context) error {
		ended = run.heldEnded
		return nil
	}); err != nil {
		t.Fatalf("read the held turn of step %s: %v", run.id, err)
	}
	return ended
}

// TestParentStepDeliverPastIdleLimit covers a parent whose turn ended not done while its child runs, past the idle limit.
// The child's end reaches the open parent as one prompt with the child's event as its PromptID, and the parent's next done
// return ends it done, with the event marked.
func TestParentStepDeliverPastIdleLimit(t *testing.T) {
	t.Parallel()
	rig := newDeliverRig(t)
	repo := stepGitTestRepo(t)
	parent := rig.startParent(t, repo, "TRICKLEHELD one", passingReturn)
	child := rig.startChild(t, repo, parent)
	rig.releaseStep(t, parent)
	settleAwaited(t, engineOf(rig.manager, rig.steps), rig.spy, stepsRunOf(t, rig.steps, parent), parent)
	rig.clock.advance(defaultIdleAfter + time.Minute)
	rig.manager.checkIdle(t.Context())
	if !rig.manager.StepLive(parent) {
		t.Fatalf("session of step %s stopped by an idle check past the idle limit, want it kept", parent)
	}
	requireUnreturned(t, rig.steps, parent)

	rig.setNext(t, parent, "READDONE")
	rig.stopChild(t, child)
	rig.waitMarked(t, child, parent)
	row := rig.waitEnded(t, parent)
	if row.End != stepLoopEndDone || row.Status != stepDefaultDone {
		t.Fatalf("end = %q status %q, want done %q", row.End, row.Status, stepDefaultDone)
	}
	rig.requireBlockOnce(t, parent, child)
}

// TestParentStepDeliverSecondChildWhileTurnRuns covers a parent with two children whose turn runs after the first child's
// end was delivered. The second child is started under that turn, so the parent and one child run at
// a time. The second child's end is sent while the turn runs, and both ends are among the parent's prompts before its done
// return, which comes after the last end, ends it.
func TestParentStepDeliverSecondChildWhileTurnRuns(t *testing.T) {
	t.Parallel()
	rig := newDeliverRig(t)
	repo := stepGitTestRepo(t)
	parent := rig.startParent(t, repo, "TRICKLEHELD one", passingReturn)
	first := rig.startChild(t, repo, parent)
	rig.releaseStep(t, parent)
	settleAwaited(t, engineOf(rig.manager, rig.steps), rig.spy, stepsRunOf(t, rig.steps, parent), parent)
	requireUnreturned(t, rig.steps, parent)

	// The turn the first end starts holds until the test releases it, so the second end arrives while it runs.
	rig.holdTurn(t, parent)
	rig.setNext(t, parent, "TRICKLEHELD two")
	rig.stopChild(t, first)
	rig.waitMarked(t, first, parent)
	waittest.Until(t, nil, "the fake claude to take the first end's prompt", func() bool {
		return rig.nextTaken(parent)
	})
	rig.setNext(t, parent, "READDONE")
	second := rig.startChild(t, repo, parent)
	rig.stopChild(t, second)
	rig.waitMarked(t, second, parent)
	if row := stepRowOf(t, rig.steps, parent); row.End != "" {
		t.Fatalf("step %s ended %q with its held turn still running, want it open until released", parent, row.End)
	}
	rig.releaseStep(t, parent)

	row := rig.waitEnded(t, parent)
	if row.End != stepLoopEndDone || row.Status != stepDefaultDone {
		t.Fatalf("end = %q status %q, want done %q", row.End, row.Status, stepDefaultDone)
	}
	rig.requireBlockOnce(t, parent, first)
	rig.requireBlockOnce(t, parent, second)
}

// TestParentStepDeliverDoneReturnEndsFirst covers a parent that returns done while its child runs. It ends at once, and the
// child's later end is marked with nothing sent to the parent.
func TestParentStepDeliverDoneReturnEndsFirst(t *testing.T) {
	t.Parallel()
	rig := newDeliverRig(t)
	repo := stepGitTestRepo(t)
	parent := rig.startParent(t, repo, "READDONE one", passingReturn)
	child := rig.startChild(t, repo, parent)
	rig.releaseStep(t, parent)
	row := rig.waitEnded(t, parent)
	if row.End != stepLoopEndDone || row.Status != stepDefaultDone {
		t.Fatalf("end = %q status %q, want done %q", row.End, row.Status, stepDefaultDone)
	}
	if running := stepRowOf(t, rig.steps, child); running.End != "" {
		t.Fatalf("child %s ended %q with its parent, want it still running", child, running.End)
	}

	rig.stopChild(t, child)
	rig.waitMarked(t, child, parent)
	rig.requireNoBlock(t, parent, child)
}

// TestParentStepDeliverHeldUntilNudge covers a held parent. Its child's end is held and unmarked while the parent is held,
// and the owner's nudge resumes it, which delivers the end once.
func TestParentStepDeliverHeldUntilNudge(t *testing.T) {
	t.Parallel()
	rig := newDeliverRig(t)
	repo := stepGitTestRepo(t)
	parent := rig.startParent(t, repo, "HANG one", passingReturn)
	child := rig.startChild(t, repo, parent)
	run := stepsRunOf(t, rig.steps, parent)
	waittest.Until(t, nil, "the turn of step "+parent+" to run", run.watch.turnRunning)
	if err := rig.steps.Hold(t.Context(), parent); err != nil {
		t.Fatalf("Hold of step %s: %v", parent, err)
	}

	before := rig.pass.rounds.Load()
	rig.stopChild(t, child)
	waittest.Until(t, nil, "the caller-event round that holds the end of step "+child, func() bool {
		return rig.pass.rounds.Load() > before
	})
	if !rig.steps.Held(parent) {
		t.Fatalf("step %s is not held, want it held", parent)
	}
	if rig.marked(t, child, parent) {
		t.Fatalf("end of step %s marked delivered to held step %s", child, parent)
	}
	rig.requireNoBlock(t, parent, child)

	waittest.Until(t, nil, "the held turn of step "+parent+" to end", func() bool {
		return heldTurnEnded(t, run)
	})
	if err := rig.steps.Nudge(t.Context(), parent, "READDONE owner"); err != nil {
		t.Fatalf("Nudge of held step %s: %v", parent, err)
	}
	waittest.Until(t, nil, "the owner's turn of step "+parent+" to run", run.watch.turnRunning)
	rig.setNext(t, parent, "READDONE")
	rig.waitMarked(t, child, parent)
	rig.releaseStep(t, parent)

	row := rig.waitEnded(t, parent)
	if row.End != stepLoopEndDone || row.Status != stepDefaultDone {
		t.Fatalf("end = %q status %q, want done %q", row.End, row.Status, stepDefaultDone)
	}
	rig.requireBlockOnce(t, parent, child)
}
