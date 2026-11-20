package enginehost

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/waittest"
)

// engine is the work a test's wait can see end: the Managers, Steps, Evidence runners, node checks and bare step runs
// in scope at the wait. A wait that names them fails at once when all their work has finished and what it waits for
// has not happened, since nothing in them is left to make it happen. An engine with no parts never finishes.
type engine struct {
	managers []*Manager
	steps    []*Steps
	evidence []*Evidence
	checks   []*NodeChecks
	passes   []*DuePass
	runs     []*stepRun
}

// engineOf returns the engine of parts. A nil part is skipped, as a part not built yet has no work.
func engineOf(parts ...any) engine {
	var e engine
	for _, part := range parts {
		switch p := part.(type) {
		case nil:
		case *Manager:
			if p != nil {
				e.managers = append(e.managers, p)
			}
		case *Steps:
			if p != nil {
				e.steps = append(e.steps, p)
			}
		case *Evidence:
			if p != nil {
				e.evidence = append(e.evidence, p)
			}
		case *NodeChecks:
			if p != nil {
				e.checks = append(e.checks, p)
			}
		case *DuePass:
			if p != nil {
				e.passes = append(e.passes, p)
			}
		case *stepRun:
			if p != nil {
				e.runs = append(e.runs, p)
			}
		default:
			panic(fmt.Sprintf("engineOf: %T is no engine part", part))
		}
	}
	return e
}

// finished reports whether the engine has parts and none of them has work left.
func (e engine) finished() bool {
	if len(e.managers)+len(e.steps)+len(e.evidence)+len(e.checks)+len(e.passes)+len(e.runs) == 0 {
		return false
	}
	for _, m := range e.managers {
		if !m.workFinished() {
			return false
		}
	}
	for _, s := range e.steps {
		if !s.workFinished() {
			return false
		}
	}
	for _, ev := range e.evidence {
		if !ev.workFinished() {
			return false
		}
	}
	for _, n := range e.checks {
		if !n.workFinished() {
			return false
		}
	}
	for _, d := range e.passes {
		if !d.workFinished() {
			return false
		}
	}
	for _, r := range e.runs {
		if !r.workFinished() {
			return false
		}
	}
	return true
}

// until polls ok until it holds. It has no deadline of its own, and fails at once, naming what, when the engine's work
// has finished first.
func (e engine) until(t *testing.T, what string, ok func() bool) {
	t.Helper()
	waittest.UntilIdle(t, e.finished, what, ok)
}

// underWay reports whether a call c counts is under way.
func (c *callsInFlight) underWay() bool {
	return c.n.Load() > 0
}

// workFinished reports whether m has no work left: no call that can start or end work is under way, no session's pump
// runs, no retired session is still stopping, no keyed Start or Say is under way, and its writer has stored or dropped
// every op put to it.
func (m *Manager) workFinished() bool {
	if m.calls.underWay() {
		return false
	}
	m.mu.Lock()
	busy := len(m.sessions) > 0 || len(m.stopping) > 0
	m.mu.Unlock()
	if busy {
		return false
	}
	m.attemptMu.Lock()
	defer m.attemptMu.Unlock()
	for _, running := range m.attempts {
		select {
		case <-running.done:
		default:
			return false
		}
	}
	return m.writer.workFinished()
}

// workFinished reports whether s has no work left: no call that can start or end work is under way, no step's loop
// runs, no re-adopted run awaits its session, and its writer has stored or dropped every op put to it. A loop leaves
// loops only once it has returned, after the step's last work: its end stored, its session stopped and its worktree
// removed.
func (s *Steps) workFinished() bool {
	if s.calls.underWay() {
		return false
	}
	s.mu.Lock()
	busy := len(s.loops) > 0 || len(s.starting) > 0 || len(s.adopted) > 0
	s.mu.Unlock()
	return !busy && s.writer.workFinished()
}

// workFinished reports whether e has no call and no drive under way.
func (e *Evidence) workFinished() bool {
	if e.calls.underWay() {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.cancels) == 0
}

// workFinished reports whether n has no call and no drive under way.
func (n *NodeChecks) workFinished() bool {
	if n.calls.underWay() {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.cancels) == 0
}

// workFinished reports whether d has no pass under way. A pass counts from its start to its return, so one that has read
// its rows and not yet acted on them is under way.
func (d *DuePass) workFinished() bool {
	return !d.calls.underWay()
}

// workFinished reports whether r's run has returned and its writer has stored or dropped every op put to it.
func (r *stepRun) workFinished() bool {
	select {
	case <-r.runDone:
		return r.writer.workFinished()
	default:
		return false
	}
}

// workFinished reports whether w has stored or dropped every op put to it: its queue is empty and no flush is applying a
// batch. A nil writer has no work.
func (w *Writer) workFinished() bool {
	if w == nil {
		return true
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.queue) == 0 && !w.inFlight
}

// An engine finishes only when it has parts and each has no work left: a running step run, a loop, a live session, an
// attempt under way and a queued op each keep it unfinished.
func TestEngineFinishesOnlyWhenEveryPartHasNoWork(t *testing.T) {
	t.Parallel()
	if engineOf(nil, (*Manager)(nil)).finished() {
		t.Fatal("an engine with no parts finished")
	}

	w := NewWriter(WriterConfig{})
	run := &stepRun{stepRunConfig: stepRunConfig{writer: w}, runDone: make(chan struct{})}
	if engineOf(run).finished() {
		t.Fatal("the engine of a running step run finished")
	}
	close(run.runDone)
	w.mu.Lock()
	w.queue = append(w.queue, entry{id: "a"})
	w.mu.Unlock()
	if engineOf(run).finished() {
		t.Fatal("the engine finished while its writer had an op queued")
	}
	w.mu.Lock()
	w.queue = nil
	w.mu.Unlock()
	if !engineOf(run).finished() {
		t.Fatal("the engine of a returned step run with an idle writer did not finish")
	}

	s := &Steps{writer: w, loops: map[string]context.CancelFunc{"a": func() {}}}
	if engineOf(s, run).finished() {
		t.Fatal("the engine finished while a step's loop ran")
	}
	delete(s.loops, "a")
	if !engineOf(s, run).finished() {
		t.Fatal("the engine of Steps with no loop did not finish")
	}

	m := &Manager{writer: w, sessions: map[string]*liveSession{"a": {}}}
	if engineOf(m).finished() {
		t.Fatal("the engine finished while a session's pump ran")
	}
	delete(m.sessions, "a")
	m.attempts = map[attemptKey]*attempt{{session: "a"}: {done: make(chan struct{})}}
	if engineOf(m).finished() {
		t.Fatal("the engine finished while a keyed Start was under way")
	}
	close(m.attempts[attemptKey{session: "a"}].done)
	if !engineOf(m).finished() {
		t.Fatal("the engine of a Manager with no session and no attempt under way did not finish")
	}
}

// A writer is not finished while a flush applies the batch it took out of its queue, though the queue is empty then.
func TestWriterIsUnfinishedWhileAFlushApplies(t *testing.T) {
	t.Parallel()
	s, _ := openStore(t)
	addSessions(t, s, "a")
	applier := &gatedApplier{store: s, entered: make(chan struct{}), release: make(chan struct{})}
	w := NewWriter(WriterConfig{Applier: applier})
	if err := w.Put(t.Context(), "a", prompt("a", "queued")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	flushed := make(chan struct{})
	go func() {
		defer close(flushed)
		w.flush(t.Context())
	}()
	receive(t, applier.entered)
	if w.workFinished() {
		t.Fatal("the writer finished while its flush applied the batch")
	}
	close(applier.release)
	receive(t, flushed)
	if !w.workFinished() {
		t.Fatal("the writer did not finish once its flush stored the batch")
	}
}

// pausedAutoResume is a DueSteps that blocks each AutoResume until release closes, after signaling entered, and then
// resumes through steps. While it blocks, the pass that called it has read its rows and not yet acted on them.
type pausedAutoResume struct {
	steps   *Steps
	entered chan struct{}
	release chan struct{}
}

func (p *pausedAutoResume) AutoResume(ctx context.Context, id string, resets time.Time) error {
	close(p.entered)
	<-p.release
	return p.steps.AutoResume(ctx, id, resets)
}

// A Steps.Start under way has not finished, though no loop runs yet: a start with a fresh id is in no map until its
// loop is, so only the call itself shows the work coming. The test pauses the start after its rows are stored and
// before its loop starts.
func TestEngineIsUnfinishedWhileAStartIsUnderWay(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	defer releaseOnce()
	f.sessions.onStart = func() {
		close(entered)
		<-release
	}
	repo := stepGitTestRepo(t)
	start := stepStartReadOnly(repo, stepGitTestRun(t, repo, "rev-parse", "HEAD"))
	started := make(chan error, 1)
	go func() {
		_, err := f.steps.Start(t.Context(), start)
		started <- err
	}()
	select {
	case <-entered:
	case err := <-started:
		t.Fatalf("Start returned %v before it stored the step's rows", err)
	}
	if engineOf(f.steps).finished() {
		t.Fatal("the engine finished while a Start was under way before its loop started")
	}
	releaseOnce()
	if err := receive(t, started); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if engineOf(f.steps).finished() {
		t.Fatal("the engine finished while the started step's loop ran")
	}
}

// A due pass that has read its rows and not yet acted on them has not finished, though the Steps it resumes through
// has no loop and no call under way. The test pauses the pass there.
func TestEngineIsUnfinishedWhileAPassIsBetweenReadingAndActing(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	at := time.Unix(duePassStamp, 0)
	addDueStep(t, f.store, "due-step", at.Add(-time.Hour))
	paused := &pausedAutoResume{steps: f.steps, entered: make(chan struct{}), release: make(chan struct{})}
	releaseOnce := sync.OnceFunc(func() { close(paused.release) })
	defer releaseOnce()
	pass := newDuePass(f.store, paused, &at)
	passed := make(chan struct{})
	go func() {
		defer close(passed)
		pass.Pass(t.Context())
	}()
	receive(t, paused.entered)
	if engineOf(f.steps, pass).finished() {
		t.Fatal("the engine finished while a pass had read its rows and not yet acted on them")
	}
	releaseOnce()
	receive(t, passed)
	if !engineOf(f.steps, pass).finished() {
		t.Fatal("the engine did not finish once the pass had returned with nothing left to run")
	}
}
