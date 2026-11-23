package enginehost

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// leftoverTask is the far end of a fake claude's LEFTOVER task: a FIFO the task holds open for writing for as long as it
// runs. opened closes once the task has opened it, so the task is running, and gone closes once the read reaches EOF, so
// every writer, the task among them, has ended. A failed open or read is sent on failed instead.
type leftoverTask struct {
	path   string
	opened chan struct{}
	gone   chan struct{}
	failed chan error
}

// newLeftoverTask makes the FIFO of a LEFTOVER task in a directory of its own and starts the read that reports the task's
// start and end. The read's open waits for the task to open the FIFO, so nothing is timed. At cleanup the FIFO is opened
// for writing once, which ends a read still waiting in its open when a failed test never started the task.
func newLeftoverTask(t *testing.T) *leftoverTask {
	t.Helper()
	path := filepath.Join(t.TempDir(), "leftover")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("make the leftover task's FIFO: %v", err)
	}
	task := &leftoverTask{
		path: path, opened: make(chan struct{}), gone: make(chan struct{}), failed: make(chan error, 1),
	}
	go task.read()
	t.Cleanup(func() {
		if f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = f.Close()
		}
	})
	return task
}

// read opens the FIFO, which returns once the task has opened it for writing, then reads to EOF.
func (l *leftoverTask) read() {
	f, err := os.Open(l.path)
	if err != nil {
		l.failed <- err
		return
	}
	close(l.opened)
	_, err = io.Copy(io.Discard, f)
	_ = f.Close()
	if err != nil {
		l.failed <- err
		return
	}
	close(l.gone)
}

// prompt returns the fake claude prompt mode, LEFTOVER or LEFTOVERHOLD, that starts this task.
func (l *leftoverTask) prompt(mode string) string {
	return mode + ":" + l.path + ":"
}

// awaitStarted waits until the task has opened the FIFO.
func (l *leftoverTask) awaitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-l.opened:
	case err := <-l.failed:
		t.Fatalf("open the leftover task's FIFO: %v", err)
	}
}

// awaitGone waits until the task has ended.
func (l *leftoverTask) awaitGone(t *testing.T) {
	t.Helper()
	select {
	case <-l.gone:
	case err := <-l.failed:
		t.Fatalf("read the leftover task's FIFO: %v", err)
	}
}

// running reports whether the task still runs: it has opened the FIFO and the read has not reached EOF.
func (l *leftoverTask) running() bool {
	select {
	case <-l.gone:
		return false
	default:
	}
	select {
	case <-l.opened:
		return true
	default:
		return false
	}
}

// signalObserver is a stepObserver that also signals: handed closes at the first hand-off, and called at the first
// finished main-conversation tool call block the session records.
type signalObserver struct {
	*stepObserver
	handed     chan struct{}
	called     chan struct{}
	handedOnce sync.Once
	calledOnce sync.Once
}

// newSignalObserver returns a signalObserver with nothing received.
func newSignalObserver() *signalObserver {
	return &signalObserver{
		stepObserver: &stepObserver{}, handed: make(chan struct{}), called: make(chan struct{}),
	}
}

func (o *signalObserver) Event(ev session.Event) {
	o.stepObserver.Event(ev)
	if block, ok := ev.(session.Block); ok && block.Kind == session.BlockToolCall && block.ParentToolUseID == "" {
		o.calledOnce.Do(func() { close(o.called) })
	}
}

func (o *signalObserver) Return(turn session.TurnEnded) {
	o.stepObserver.Return(turn)
	o.handedOnce.Do(func() { close(o.handed) })
}

// awaitStepEnd waits for the stored end of the step id through a subscription to it, and returns the ended row.
func awaitStepEnd(t *testing.T, steps *Steps, id string) store.StepRow {
	t.Helper()
	sub, err := steps.Subscribe(t.Context(), []string{id})
	if err != nil {
		t.Fatalf("Subscribe to step %s: %v", id, err)
	}
	defer sub.Close()
	for {
		<-sub.Ready()
		for _, row := range sub.Take() {
			if row.End != "" {
				return row
			}
		}
	}
}

// TestStepTurnWithOnlyAShellTaskListedIsHanded covers a turn that ends while a background shell task is listed and no
// background agent is: the turn end is handed to the observer at once, since the step does not wait on a shell task.
func TestStepTurnWithOnlyAShellTaskListedIsHanded(t *testing.T) {
	t.Parallel()
	m, _, _ := newHandOffManager(t, &fakeSink{})
	obs := newSignalObserver()
	if err := m.StartStep(t.Context(), StepSession{
		ID: stepA, Dir: t.TempDir(), Prompt: "BACKGROUND", Agent: "reviewer", Observer: obs,
	}); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	<-obs.handed
	if n := obs.returned(); n != 1 {
		t.Fatalf("turn ends handed to the observer with a shell task listed = %d, want 1", n)
	}
}

// TestStepReturnUnderALeftoverWatchEndsReturnedAndStopsIt plays the release step that ended its turn with its result
// while a watch it started, a Monitor's tail -f, still ran: claude keeps such a session open, and the step must not wait
// on it. The step ends returned with the return's status and value, and its stop ends the watch, which runs in a session
// of its own, so only the stop of the holder's scope reaches it.
func TestStepReturnUnderALeftoverWatchEndsReturnedAndStopsIt(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	task := newLeftoverTask(t)
	id := startLimitProbe(t, rig, repo, revision, task.prompt("LEFTOVER"))
	task.awaitStarted(t)

	row := awaitStepEnd(t, rig.steps, id)
	if row.End != stepLoopEndReturned || row.Status != "blocked" {
		t.Fatalf("end = %q status %q, want returned blocked", row.End, row.Status)
	}
	var ret struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(row.Result), &ret); err != nil {
		t.Fatalf("decode stored return %q: %v", row.Result, err)
	}
	if ret.Summary != "fake blocked" {
		t.Fatalf("stored return = %q, want the blocked return the turn ended with", row.Result)
	}
	task.awaitGone(t)
}

// TestStepMidTurnUnderALeftoverWatchIsNotHanded covers a step whose turn is still running, in a foreground tool call,
// while a watch it started runs: nothing is handed to the step's loop, so the step stays open and the watch keeps running
// until the step is stopped.
func TestStepMidTurnUnderALeftoverWatchIsNotHanded(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	task := newLeftoverTask(t)
	obs := newSignalObserver()
	if err := rig.manager.StartStep(t.Context(), StepSession{
		ID: stepA, Dir: t.TempDir(), Prompt: task.prompt("LEFTOVERHOLD"), Agent: "reviewer", Observer: obs,
	}); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	task.awaitStarted(t)
	<-obs.called
	if n := obs.returned(); n != 0 {
		t.Fatalf("turn ends handed to the observer mid-turn = %d, want 0", n)
	}
	if !task.running() {
		t.Fatal("the watch ended while the step's turn still runs")
	}

	if err := rig.manager.Stop(t.Context(), stepA); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	task.awaitGone(t)
	if n := obs.returned(); n != 0 {
		t.Fatalf("turn ends handed to the observer by the stop = %d, want 0", n)
	}
}
