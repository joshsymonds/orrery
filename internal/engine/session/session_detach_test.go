package session

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// errStop is what the detachable fake's Stop returns, and errDetach what its Detach returns, so a test
// can tell which of the two a later call returned.
var (
	errStop   = errors.New("stop failed")
	errDetach = errors.New("detach failed")
)

// detachableProcess is a session process that can be left running. Its Detach records the call and
// leaves the process running, as a holder does. Its Stop records the call and then ends the fake
// process.
type detachableProcess struct {
	*fakeProcess
	detaches atomic.Int32
	stops    atomic.Int32
}

func newDetachableProcess() *detachableProcess {
	return &detachableProcess{fakeProcess: newFakeProcess()}
}

func (p *detachableProcess) Stop() error {
	p.stops.Add(1)
	p.exit(nil, "")
	return errStop
}

func (p *detachableProcess) Detach() error {
	p.detaches.Add(1)
	return errDetach
}

// startDetachable starts a session on process and ends it when the test ends. The cleanup does not
// check the error of that end: each test asserts the errors it expects.
func startDetachable(t *testing.T, process Process) *Session {
	t.Helper()
	session, err := New(process)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = stopWithin(t, session) })
	return session
}

// detachWithin calls Detach and fails the test unless it returns within testGuard.
func detachWithin(t *testing.T, session *Session) error {
	t.Helper()
	result := make(chan error, 1)
	go func() { result <- session.Detach() }()
	select {
	case err := <-result:
		return err
	case <-time.After(testGuard):
		t.Fatalf("Detach did not return within %v", testGuard)
	}
	return nil
}

func TestDetachLeavesDetachableProcessRunning(t *testing.T) {
	t.Parallel()
	process := newDetachableProcess()
	session := startDetachable(t, process)

	err := detachWithin(t, session)
	if got := process.detaches.Load(); got != 1 {
		t.Errorf("Detach called the process's Detach %d times, want once", got)
	}
	if got := process.stops.Load(); got != 0 {
		t.Errorf("Detach called the process's Stop %d times, want never", got)
	}
	if process.exited() {
		t.Errorf("Detach stopped the process, want it left running")
	}
	if !errors.Is(err, errDetach) {
		t.Errorf("Detach = %v, want the process's Detach error %v", err, errDetach)
	}
	requireClosed(t, session)
}

// TestLaterCallsOfDetachAndStopReturnTheFirstError covers the shared once: after Detach, Stop and a
// second Detach return Detach's error and call neither the process's Detach nor its Stop again.
func TestLaterCallsOfDetachAndStopReturnTheFirstError(t *testing.T) {
	t.Parallel()
	process := newDetachableProcess()
	session := startDetachable(t, process)
	if err := detachWithin(t, session); !errors.Is(err, errDetach) {
		t.Fatalf("first Detach = %v, want %v", err, errDetach)
	}

	if err := stopWithin(t, session); !errors.Is(err, errDetach) {
		t.Errorf("Stop after Detach = %v, want Detach's error %v", err, errDetach)
	}
	if err := detachWithin(t, session); !errors.Is(err, errDetach) {
		t.Errorf("second Detach = %v, want Detach's error %v", err, errDetach)
	}
	if got := process.detaches.Load(); got != 1 {
		t.Errorf("process Detach called %d times, want once", got)
	}
	if got := process.stops.Load(); got != 0 {
		t.Errorf("process Stop called %d times, want never", got)
	}
}

func TestDetachStopsPlainProcess(t *testing.T) {
	t.Parallel()
	process := newFakeProcess()
	session := startDetachable(t, process)

	if err := detachWithin(t, session); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	if !process.exited() {
		t.Errorf("Detach left a process without a Detach method running, want it stopped as Stop does")
	}
	requireClosed(t, session)
}

// TestStopThenDetachCallsStopOnly covers the other order of the shared once: Detach returns Stop's
// error and does not call the process's Detach.
func TestStopThenDetachCallsStopOnly(t *testing.T) {
	t.Parallel()
	process := newDetachableProcess()
	session := startDetachable(t, process)
	if err := stopWithin(t, session); !errors.Is(err, errStop) {
		t.Fatalf("Stop = %v, want the process's Stop error %v", err, errStop)
	}

	if err := detachWithin(t, session); !errors.Is(err, errStop) {
		t.Errorf("Detach after Stop = %v, want Stop's error %v", err, errStop)
	}
	if got := process.detaches.Load(); got != 0 {
		t.Errorf("Detach after Stop called the process's Detach %d times, want never", got)
	}
	if got := process.stops.Load(); got != 1 {
		t.Errorf("process Stop called %d times, want once", got)
	}
	requireClosed(t, session)
}
