package holder

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestReattachGateTakesOverALaunchedGateAndKeepsItsExitRecord(t *testing.T) {
	t.Parallel()

	root := launchRoot(t)
	child := launchGateChild(t, exitTailChild)
	first := mustLaunch(t, LaunchConfig{
		StateDir:  root,
		Session:   testSession,
		Child:     child,
		GateLimit: launchGateLimit,
	})
	dir := sessionDirOf(t, root)
	stopServingHolder(t, dir)
	if err := first.Detach(); err != nil {
		t.Fatalf("Detach the launching controller: %v", err)
	}

	p, err := ReattachGate(t.Context(), dir)
	if err != nil {
		t.Fatalf("ReattachGate: %v", err)
	}
	releaseGateChild(t, child)

	wantWait := fmt.Sprintf("exit status %d", childDiedCode)
	if waitErr := awaitResult(t, "Wait", startAsync(p.Wait)); waitErr == nil || waitErr.Error() != wantWait {
		t.Fatalf("Wait = %v, want %q from the exit record", waitErr, wantWait)
	}
	exit, ok := p.Exit()
	if !ok {
		t.Fatal("Exit reports no exit record after Wait returned")
	}
	if exit.Reason != ReasonExited || exit.Code != childDiedCode {
		t.Fatalf("Exit = reason %q code %d, want reason %q code %d",
			exit.Reason, exit.Code, ReasonExited, childDiedCode)
	}
	if got, want := p.Stderr(), "out\nerr\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ExitName)); statErr != nil {
		t.Fatalf("exit record after the holder ended: %v, want it kept until the caller clears it", statErr)
	}
	recorded, found, err := readGoneExit(dir)
	if err != nil || !found {
		t.Fatalf("read the exit record in %s: found %v, err %v", dir, found, err)
	}
	if recorded != exit {
		t.Fatalf("exit record in %s = %+v, want the Process's %+v", dir, recorded, exit)
	}
}

func TestReattachGateOfAHolderThatHasEndedIsGone(t *testing.T) {
	t.Parallel()

	root := launchRoot(t)
	// The child is stopped before it is released, so its script never runs.
	child := launchGateChild(t, "exit 0")
	first := mustLaunch(t, LaunchConfig{
		StateDir:  root,
		Session:   testSession,
		Child:     child,
		GateLimit: launchGateLimit,
	})
	dir := sessionDirOf(t, root)
	stopServingHolder(t, dir)
	if err := first.Detach(); err != nil {
		t.Fatalf("Detach the launching controller: %v", err)
	}
	if err := Stop(t.Context(), dir); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := ReattachGate(t.Context(), dir); !errors.Is(err, ErrGone) {
		t.Fatalf("ReattachGate of an ended holder = %v, want ErrGone", err)
	}
}
