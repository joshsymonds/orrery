package releasestep

import (
	"slices"
	"testing"
)

// testUnits are the units a test's update step checks.
func testUnits() []string {
	return []string{"orreryd", "orrery-serve"}
}

// updateWorld is a world with a flake whose owner's checkout is on main, and a candidate on origin's main.
func updateWorld(t *testing.T) (*world, string) {
	t.Helper()
	w := newWorld(t)
	candidate := w.commit("change.txt")
	w.pushMain()
	w.originTag("v0.1.0", candidate)
	w.withFlake("v0.1.0", candidate)
	return w, candidate
}

// updateStepParams are the parameters of an update step on the world's flake that runs `update` and checks both units.
func (w *world) updateStepParams() Params {
	return Params{Branch: "main", Flake: w.flake, Units: testUnits(), Run: []string{"update", "--quiet"}}
}

// runBuild makes both units run the build of commit, with pids from first and store path hash.
func (w *world) runBuild(first int, hash, commit string) {
	for i, name := range testUnits() {
		w.machine.runUnit(name, first+i, hash, commit)
	}
}

// fakeUpdate makes the machine's update command switch both units to the build of commit, and returns a counter of the
// times it ran.
func (w *world) fakeUpdate(commit string) *[]call {
	var ran []call
	w.machine.update = func(dir string, argv []string) error {
		ran = append(ran, call{dir: dir, argv: slices.Clone(argv)})
		if commit != "" {
			w.runBuild(300, "newbuild", commit)
		}
		return nil
	}
	return &ran
}

// TestUpdateUnitsRunNewerBuildHoldsAndRunsNothing: both units run a build of a commit that contains the candidate, one
// the repository has to fetch from origin first, so the step neither moves the checkout nor runs the update command.
func TestUpdateUnitsRunNewerBuildHoldsAndRunsNothing(t *testing.T) {
	t.Parallel()
	w, candidate := updateWorld(t)
	newer := w.originCommit("newer.txt")
	w.runBuild(100, "aaaa", newer)
	cloneCommit(t, w.flakeOrigin, "host.nix")
	head := gitIn(t, w.flake, "rev-parse", "HEAD")
	ran := w.fakeUpdate("")

	res := w.runStep(KindUpdate, w.updateStepParams(), candidate)
	wantCode(t, res, exitHeld)
	wantOut(t, "stdout", res.stdout,
		"unit orreryd is active, pid 100, exe /nix/store/aaaa-orrery-0.0.0/bin/orreryd, built from "+newer+
			", which contains candidate "+candidate,
		"unit orrery-serve is active, pid 101", "nothing was done")
	if len(*ran) != 0 {
		t.Errorf("the update command ran %v, want not at all", *ran)
	}
	if got := gitIn(t, w.flake, "rev-parse", "HEAD"); got != head {
		t.Errorf("the checkout's HEAD = %s, want it left at %s", got, head)
	}
}

// TestUpdateFastForwardsCheckoutAndRunsUpdate: the units run an older build and the owner's checkout is behind origin's
// main, so the step fast-forwards the checkout, runs the update command in it, and finds both units run the candidate.
func TestUpdateFastForwardsCheckoutAndRunsUpdate(t *testing.T) {
	t.Parallel()
	w, candidate := updateWorld(t)
	w.runBuild(100, "oldbuild", w.base)
	ahead := cloneCommit(t, w.flakeOrigin, "host.nix")
	ran := w.fakeUpdate(candidate)

	res := w.runStep(KindUpdate, w.updateStepParams(), candidate)
	wantCode(t, res, exitHeld)
	if got := gitIn(t, w.flake, "rev-parse", "HEAD"); got != ahead {
		t.Errorf("the checkout's HEAD = %s, want origin's main %s", got, ahead)
	}
	if len(*ran) != 1 || (*ran)[0].dir != w.flake || !slices.Equal((*ran)[0].argv, []string{"update", "--quiet"}) {
		t.Errorf("the update command ran %v, want `update --quiet` once in %s", *ran, w.flake)
	}
	wantOut(t, "stdout", res.stdout, "fast-forwarded "+w.flake+" main", "ran update --quiet in "+w.flake,
		"unit orreryd is active, pid 300, exe /nix/store/newbuild-orrery-0.0.0/bin/orreryd, built from "+candidate,
		"unit orrery-serve is active, pid 301, exe /nix/store/newbuild-orrery-0.0.0/bin/orrery-serve")
}

// TestUpdateDivergedCheckoutFailsWithoutRunning: the owner's checkout has a commit origin's main lacks and origin's main
// one the checkout lacks, so the step fails, saying the checkout must be rebased, and runs nothing.
func TestUpdateDivergedCheckoutFailsWithoutRunning(t *testing.T) {
	t.Parallel()
	w, candidate := updateWorld(t)
	w.runBuild(100, "oldbuild", w.base)
	cloneCommit(t, w.flakeOrigin, "host.nix")
	local := commitFile(t, w.flake, "local.nix", "local\n")
	ran := w.fakeUpdate(candidate)

	res := w.runStep(KindUpdate, w.updateStepParams(), candidate)
	wantCode(t, res, exitFailed)
	wantOut(t, "stderr", res.stderr, "has diverged from origin main", "must be rebased onto origin main first")
	if len(*ran) != 0 {
		t.Errorf("the update command ran %v, want not at all", *ran)
	}
	if got := gitIn(t, w.flake, "rev-parse", "HEAD"); got != local {
		t.Errorf("the checkout's HEAD = %s, want it left at %s", got, local)
	}
}

// TestUpdateUnitOnOlderBuildAfterRunFails: the update command runs, and a unit still runs a build of a commit that
// does not contain the candidate, so the step fails naming it.
func TestUpdateUnitOnOlderBuildAfterRunFails(t *testing.T) {
	t.Parallel()
	w, candidate := updateWorld(t)
	w.runBuild(100, "oldbuild", w.base)
	ran := w.fakeUpdate("")

	res := w.runStep(KindUpdate, w.updateStepParams(), candidate)
	wantCode(t, res, exitFailed)
	if len(*ran) != 1 {
		t.Errorf("the update command ran %v, want once", *ran)
	}
	wantOut(t, "stderr", res.stderr, "the postcondition does not hold after the step",
		"unit orreryd: build commit "+w.base+" does not contain candidate "+candidate)
	wantOut(
		t,
		"stdout",
		res.stdout,
		"unit orreryd is active, pid 100, exe /nix/store/oldbuild-orrery-0.0.0/bin/orreryd",
	)
}

// TestUpdateInactiveUnitOrBuildWithNoCommitDoesNotHold: a unit that is not active, and one whose build records no
// commit, do not meet the postcondition, and the update command that does not change them leaves the step failed.
func TestUpdateInactiveUnitOrBuildWithNoCommitDoesNotHold(t *testing.T) {
	t.Parallel()
	w, candidate := updateWorld(t)
	w.machine.runUnit("orreryd", 100, "dirty", "")
	w.fakeUpdate("")

	res := w.runStep(KindUpdate, w.updateStepParams(), candidate)
	wantCode(t, res, exitFailed)
	wantOut(t, "stderr", res.stderr, `share/orrery/commit holds "", not a full commit id`,
		"unit orrery-serve is not active with a main process")
}
