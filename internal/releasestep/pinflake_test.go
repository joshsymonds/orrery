package releasestep

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// flakeURL is the url a test's pin-flake step sets the orrery input to.
const flakeURL = "github:joshsymonds/orrery/" + TagSlot

// flakeText is a flake.nix whose orrery input names tag, in the form of the owner's flake: the url line inside inputs,
// beside a follows line of the same input.
func flakeText(tag string) string {
	return "{\n  inputs = {\n    nixpkgs.url = \"github:NixOS/nixpkgs/nixos-unstable\";\n" +
		"    orrery.url = \"github:joshsymonds/orrery/" + tag + "\";\n" +
		"    orrery.inputs.nixpkgs.follows = \"nixpkgs\";\n  };\n  outputs = _: { };\n}\n"
}

// lockText is a flake.lock that locks the orrery input of a flake at rev, the commit of tag.
func lockText(tag, rev string) string {
	return fmt.Sprintf(`{
  "nodes": {
    "nixpkgs": {"locked": {"rev": "0000000000000000000000000000000000000000", "type": "github"}},
    "orrery": {
      "inputs": {"nixpkgs": ["nixpkgs"]},
      "locked": {"owner": "joshsymonds", "repo": "orrery", "rev": %q, "type": "github"},
      "original": {"owner": "joshsymonds", "ref": %q, "repo": "orrery", "type": "github"}
    },
    "root": {"inputs": {"nixpkgs": "nixpkgs", "orrery": "orrery"}}
  },
  "root": "root",
  "version": 7
}
`, rev, tag)
}

// withFlake gives the world a flake repository: a bare origin, and the owner's checkout of it on main at
// <home>/nix-config, whose flake pins the orrery input to tag at rev.
func (w *world) withFlake(tag, rev string) {
	w.t.Helper()
	w.flakeOrigin = w.t.TempDir()
	gitIn(w.t, w.flakeOrigin, "init", "-q", "--bare", "-b", "main")
	w.flake = filepath.Join(w.t.TempDir(), "nix-config")
	if err := os.Mkdir(w.flake, 0o750); err != nil {
		w.t.Fatalf("make the flake checkout: %v", err)
	}
	gitIn(w.t, w.flake, "init", "-q", "-b", "main")
	for _, pair := range [][2]string{
		{"user.name", "Test"}, {"user.email", "test@example.invalid"}, {"commit.gpgsign", "false"},
	} {
		gitIn(w.t, w.flake, "config", pair[0], pair[1])
	}
	gitIn(w.t, w.flake, "remote", "add", Remote, w.flakeOrigin)
	writeFile(w.t, w.flake, flakeNix, flakeText(tag))
	writeFile(w.t, w.flake, flakeLock, lockText(tag, rev))
	gitIn(w.t, w.flake, "add", flakeNix, flakeLock)
	gitIn(w.t, w.flake, "commit", "-q", "-m", "flake")
	gitIn(w.t, w.flake, "push", "-q", "-u", Remote, "main")
}

// flakeMain returns the commit the flake origin's main names.
func (w *world) flakeMain() string {
	w.t.Helper()
	return gitIn(w.t, w.flakeOrigin, "rev-parse", "refs/heads/main")
}

// pinParams are the parameters of a pin-flake step on the world's flake, named from the home directory.
func (w *world) pinParams() Params {
	return Params{Branch: "main", Prefix: "v", Flake: "~/" + filepath.Base(w.flake), Input: "orrery", URL: flakeURL}
}

// fakeNixUpdate makes the machine's `nix flake update orrery` lock the orrery input at the commit origin's tag peels
// to, the tag the flake.nix in its directory names. before runs first on each call, with the call's number from 1.
func (w *world) fakeNixUpdate(before func(n int)) {
	var calls atomic.Int32
	w.machine.nix = func(dir string, argv []string) error {
		if !slices.Equal(argv, []string{"nix", "flake", "update", "orrery"}) {
			return fmt.Errorf("%w: unexpected %q", errFake, argv)
		}
		if before != nil {
			before(int(calls.Add(1)))
		}
		text, err := os.ReadFile(filepath.Join(dir, flakeNix))
		if err != nil {
			return err
		}
		url, err := inputURL(string(text), "orrery")
		if err != nil {
			return err
		}
		tag := url[strings.LastIndex(url, "/")+1:]
		rev, err := gitTry(w.origin, "rev-parse", "refs/tags/"+tag+"^{commit}")
		if err != nil {
			return fmt.Errorf("%w: %s", err, rev)
		}
		return os.WriteFile(filepath.Join(dir, flakeLock), []byte(lockText(tag, rev)), 0o600)
	}
}

// TestPinFlakeNewerPinHoldsAndChangesNothing: origin's main of the flake already pins a tag newer than the candidate's
// that contains it, locked at that tag's commit, so the step runs nothing and pushes nothing.
func TestPinFlakeNewerPinHoldsAndChangesNothing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	candidate := w.commit("change.txt")
	later := w.commit("later.txt")
	w.pushMain()
	w.originTag("v0.1.1", candidate)
	w.originTag("v0.1.2", later)
	w.withFlake("v0.1.2", later)
	before := w.flakeMain()

	res := w.runStep(KindPinFlake, w.pinParams(), candidate)
	wantCode(t, res, exitHeld)
	wantOut(t, "stdout", res.stdout, `sets orrery.url to "github:joshsymonds/orrery/v0.1.2"`,
		"tag v0.1.2 peels to "+later+", which contains candidate "+candidate, "locks orrery at "+later,
		"nothing was done")
	if got := w.flakeMain(); got != before {
		t.Errorf("flake origin main = %s, want it left at %s", got, before)
	}
	if runs := w.machine.runs(); len(runs) != 0 {
		t.Errorf("the step ran %v, want nothing", runs)
	}
}

// TestPinFlakePinsNewestTagOnOriginWithoutTouchingCheckout: the flake pins an older tag, so the step pins the newest
// tag that contains the candidate in a temporary worktree, commits only flake.nix and flake.lock, and pushes the
// commit to the flake origin's main. The owner's checkout and its worktrees are as they were.
func TestPinFlakePinsNewestTagOnOriginWithoutTouchingCheckout(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.originTag("v0.1.0", w.base)
	candidate := w.commit("change.txt")
	w.pushMain()
	w.originTag("v0.1.1", candidate)
	w.withFlake("v0.1.0", w.base)
	writeFile(t, w.flake, "notes.txt", "the owner's uncommitted work\n")
	head := gitIn(t, w.flake, "rev-parse", "HEAD")
	before := w.flakeMain()
	w.fakeNixUpdate(nil)

	res := w.runStep(KindPinFlake, w.pinParams(), candidate)
	wantCode(t, res, exitHeld)
	pin := w.flakeMain()
	if got := gitIn(t, w.flakeOrigin, "rev-parse", pin+"^"); got != before {
		t.Fatalf("the pin's parent = %s, want origin's main %s", got, before)
	}
	if got := gitIn(t, w.flakeOrigin, "log", "-1", "--format=%s", pin); got != "orrery: pin v0.1.1" {
		t.Errorf("the pin's message = %q, want %q", got, "orrery: pin v0.1.1")
	}
	if got := gitIn(t, w.flakeOrigin, "diff", "--name-only", before, pin); got != "flake.lock\nflake.nix" {
		t.Errorf("the pin changes %q, want only flake.lock and flake.nix", got)
	}
	if got := gitIn(t, w.flakeOrigin, "show", pin+":"+flakeNix); got != strings.TrimSpace(flakeText("v0.1.1")) {
		t.Errorf("the pinned flake.nix =\n%s\nwant only the url line changed", got)
	}
	wantOut(t, "stdout", res.stdout, "pushed "+pin+" (orrery: pin v0.1.1)", "locks orrery at "+candidate)
	if got := gitIn(t, w.flake, "rev-parse", "HEAD"); got != head {
		t.Errorf("the checkout's HEAD = %s, want it left at %s", got, head)
	}
	if got := gitIn(t, w.flake, "status", "--porcelain"); got != "?? notes.txt" {
		t.Errorf("the checkout's status = %q, want only the owner's notes.txt", got)
	}
	if got := gitIn(t, w.flake, "worktree", "list", "--porcelain"); strings.Count(got, "worktree ") != 1 {
		t.Errorf("the flake has worktrees left:\n%s", got)
	}
	runs := w.machine.runs()
	if len(runs) != 1 || runs[0].dir == w.flake {
		t.Errorf("nix ran %v, want once, in a temporary worktree", runs)
	}
	if _, err := os.Stat(runs[0].dir); !os.IsNotExist(err) {
		t.Errorf("the temporary worktree %s is still there: %v", runs[0].dir, err)
	}
}

// TestPinFlakeOriginMovedBeforePushPinsAgain: the flake origin's main moves while the pin is made, so origin refuses
// the push, and the step starts again from the fetch and pins on the moved main.
func TestPinFlakeOriginMovedBeforePushPinsAgain(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.originTag("v0.1.0", w.base)
	candidate := w.commit("change.txt")
	w.pushMain()
	w.originTag("v0.1.1", candidate)
	w.withFlake("v0.1.0", w.base)
	var moved string
	w.fakeNixUpdate(func(n int) {
		if n == 1 {
			moved = cloneCommit(t, w.flakeOrigin, "host.nix")
		}
	})

	res := w.runStep(KindPinFlake, w.pinParams(), candidate)
	wantCode(t, res, exitHeld)
	pin := w.flakeMain()
	if got := gitIn(t, w.flakeOrigin, "rev-parse", pin+"^"); got != moved {
		t.Fatalf("the pin's parent = %s, want the moved main %s", got, moved)
	}
	if got := gitIn(t, w.flakeOrigin, "log", "-1", "--format=%s", pin); got != "orrery: pin v0.1.1" {
		t.Errorf("the pin's message = %q", got)
	}
	wantOut(t, "stdout", res.stdout, "moved from", "to "+moved, "so the step starts again")
	if runs := w.machine.runs(); len(runs) != 2 {
		t.Errorf("nix ran %d times, want 2: %v", len(runs), runs)
	}
	if got := gitIn(t, w.flake, "worktree", "list", "--porcelain"); strings.Count(got, "worktree ") != 1 {
		t.Errorf("the flake has worktrees left:\n%s", got)
	}
}

// TestPinFlakeWithNoTagFailsTagFirst: no tag on origin contains the candidate, so the step fails and says the tag step
// comes first.
func TestPinFlakeWithNoTagFailsTagFirst(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.originTag("v0.1.0", w.base)
	candidate := w.commit("change.txt")
	w.pushMain()
	w.withFlake("v0.1.0", w.base)
	before := w.flakeMain()

	res := w.runStep(KindPinFlake, w.pinParams(), candidate)
	wantCode(t, res, exitFailed)
	wantOut(t, "stderr", res.stderr, "contains candidate "+candidate+": the tag step comes first")
	if got := w.flakeMain(); got != before {
		t.Errorf("flake origin main = %s, want it left at %s", got, before)
	}
}
