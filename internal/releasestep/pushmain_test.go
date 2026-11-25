package releasestep

import (
	"path/filepath"
	"testing"
)

// mainParams are the parameters of a push-main step to origin's main.
func mainParams() Params {
	return Params{Branch: "main"}
}

// TestPushMainOriginPastCandidateHoldsAndPushesNothing: origin's main has moved past the candidate, which it holds, so
// the postcondition already holds and nothing is pushed: main is never moved back.
func TestPushMainOriginPastCandidateHoldsAndPushesNothing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	candidate := w.commit("change.txt")
	w.pushMain()
	past := w.originCommit("later.txt")

	res := w.runStep(KindPushMain, mainParams(), candidate)
	wantCode(t, res, exitHeld)
	wantOut(t, "stdout", res.stdout, "origin main is "+past, "candidate "+candidate+" is in origin main",
		"nothing was done")
	if got := w.originMain(); got != past {
		t.Errorf("origin main = %s, want it left at %s", got, past)
	}
}

// TestPushMainFastForwardsOriginToCandidate: origin's main is behind the candidate, so the step pushes the candidate
// and then finds origin's main holds it.
func TestPushMainFastForwardsOriginToCandidate(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	candidate := w.commit("change.txt")

	res := w.runStep(KindPushMain, mainParams(), candidate)
	wantCode(t, res, exitHeld)
	if got := w.originMain(); got != candidate {
		t.Fatalf("origin main = %s, want the candidate %s", got, candidate)
	}
	wantOut(t, "stdout", res.stdout, "pushed "+candidate+" to origin main, which was "+w.base,
		"origin main is "+candidate, "candidate "+candidate+" is in origin main")
}

// TestPushMainOriginMovedElsewhereFailsForReprepare: origin's main moved to a commit the candidate does not hold, so
// the step fails without pushing and says the candidate must be prepared again.
func TestPushMainOriginMovedElsewhereFailsForReprepare(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	candidate := w.commit("change.txt")
	moved := w.originCommit("other.txt")

	res := w.runStep(KindPushMain, mainParams(), candidate)
	wantCode(t, res, exitFailed)
	wantOut(t, "stderr", res.stderr, "origin main is "+moved, "must be prepared again")
	if got := w.originMain(); got != moved {
		t.Errorf("origin main = %s, want it left at %s", got, moved)
	}
}

// TestPushMainRefusedPushFailsWithGitsMessage: origin refuses the push, so the step fails with what git said.
func TestPushMainRefusedPushFailsWithGitsMessage(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	candidate := w.commit("change.txt")
	writeFile(t, w.origin, filepath.Join("hooks", "pre-receive"), "#!/bin/sh\necho 'main is frozen' >&2\nexit 1\n")
	makeExecutable(t, filepath.Join(w.origin, "hooks", "pre-receive"))

	res := w.runStep(KindPushMain, mainParams(), candidate)
	wantCode(t, res, exitFailed)
	wantOut(t, "stderr", res.stderr, "origin refused the push of "+candidate, "main is frozen")
	if got := w.originMain(); got != w.base {
		t.Errorf("origin main = %s, want it left at %s", got, w.base)
	}
}
