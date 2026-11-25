package releasestep

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// tagParams are the parameters of a tag step of v tags.
func tagParams() Params {
	return Params{Prefix: "v"}
}

// wantSignedTag fails the test unless origin holds name as a signed annotated tag object that peels to commit, with
// message, whose signature the repository verifies.
func wantSignedTag(t *testing.T, w *world, name, commit, message string) {
	t.Helper()
	ref := "refs/tags/" + name
	if got := gitIn(t, w.origin, "cat-file", "-t", ref); got != "tag" {
		t.Fatalf("origin's %s is a %s object, want an annotated tag", name, got)
	}
	if got := gitIn(t, w.origin, "rev-parse", ref+"^{commit}"); got != commit {
		t.Errorf("origin's %s peels to %s, want %s", name, got, commit)
	}
	object := gitIn(t, w.origin, "cat-file", "-p", ref)
	wantOut(t, "tag object", object, "tag "+name, message, "-----BEGIN SSH SIGNATURE-----")
	gitIn(t, w.repo, "fetch", "-q", Remote, ref+":"+ref)
	gitIn(t, w.repo, "verify-tag", name)
}

// TestTagNewerTagContainsCandidateHoldsAndTagsNothing: a newer tag on origin contains the candidate, so the step names
// it and makes no tag, and a tag at the candidate itself is preferred over a newer one.
func TestTagNewerTagContainsCandidateHoldsAndTagsNothing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	candidate := w.commit("change.txt")
	later := w.commit("later.txt")
	w.pushMain()
	w.originTag("v0.1.0", w.base)
	w.originTag("v0.1.1", later)
	tags := w.originTags()

	res := w.runStep(KindTag, tagParams(), candidate)
	wantCode(t, res, exitHeld)
	wantOut(t, "stdout", res.stdout, "tag v0.1.1 on origin peels to "+later,
		"tag v0.1.1 contains candidate "+candidate, "nothing was done")
	if got := w.originTags(); got != tags {
		t.Errorf("origin's tags = %s, want them left as %s", got, tags)
	}
}

// TestTagPrefersTagAtCandidateOverNewerOne: origin has a tag at the candidate and a newer one that contains it, so the
// step names the one at the candidate.
func TestTagPrefersTagAtCandidateOverNewerOne(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	candidate := w.commit("change.txt")
	later := w.commit("later.txt")
	w.pushMain()
	w.originTag("v0.1.1", candidate)
	w.originTag("v0.1.2", later)

	res := w.runStep(KindTag, tagParams(), candidate)
	wantCode(t, res, exitHeld)
	wantOut(t, "stdout", res.stdout, "tag v0.1.1 on origin peels to "+candidate, "tag v0.1.1 is candidate "+candidate)
}

// TestTagPushesSignedTagAtNextPatch: no tag on origin contains the candidate, so the step pushes a signed annotated tag
// at it, named the next patch above origin's newest, with the change's title in its message.
func TestTagPushesSignedTagAtNextPatch(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.originTag("v0.1.9", w.base)
	w.originTag("v0.1.10", w.base)
	w.originTag("other-1", w.base)
	candidate := w.commit("change.txt")
	w.pushMain()

	res := w.runStep(KindTag, tagParams(), candidate)
	wantCode(t, res, exitHeld)
	wantSignedTag(t, w, "v0.1.11", candidate, "v0.1.11: the change")
	wantOut(t, "stdout", res.stdout, "pushed signed annotated tag v0.1.11 at "+candidate,
		"tag v0.1.11 is candidate "+candidate)
}

// TestTagSkipsNameTakenLocally: the next name above origin's newest tag is already a tag in the repository, so the step
// takes the one above it.
func TestTagSkipsNameTakenLocally(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.originTag("v0.1.0", w.base)
	gitIn(t, w.repo, "tag", "v0.1.1", w.base)
	candidate := w.commit("change.txt")
	w.pushMain()

	res := w.runStep(KindTag, tagParams(), candidate)
	wantCode(t, res, exitHeld)
	wantSignedTag(t, w, "v0.1.2", candidate, "v0.1.2: the change")
	if got := gitIn(t, w.repo, "rev-parse", "refs/tags/v0.1.1"); got != w.base {
		t.Errorf("the repository's v0.1.1 = %s, want it left at %s", got, w.base)
	}
	if strings.Contains(w.originTags(), "refs/tags/v0.1.1 ") {
		t.Errorf("origin has v0.1.1, which the step never pushes:\n%s", w.originTags())
	}
}

// TestTagNameTakenOnOriginMeanwhileTakesNextName: while the step pushes its tag, origin is given that name by someone
// else, so origin refuses the push, and the step takes the next free name and leaves the other tag alone.
func TestTagNameTakenOnOriginMeanwhileTakesNextName(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.originTag("v0.1.0", w.base)
	candidate := w.commit("change.txt")
	w.pushMain()
	marker := filepath.Join(t.TempDir(), "taken")
	hook := fmt.Sprintf(
		"#!/bin/sh\nif [ ! -e %q ]; then\n  touch %q\n  git --git-dir=%q update-ref refs/tags/v0.1.1 %s\n"+
			"fi\n",
		marker,
		marker,
		w.origin,
		w.base,
	)
	writeFile(t, w.repo, filepath.Join(".git", "hooks", "pre-push"), hook)
	makeExecutable(t, filepath.Join(w.repo, ".git", "hooks", "pre-push"))

	res := w.runStep(KindTag, tagParams(), candidate)
	wantCode(t, res, exitHeld)
	wantOut(t, "stdout", res.stdout, "origin took v0.1.1 while this step ran")
	wantSignedTag(t, w, "v0.1.2", candidate, "v0.1.2: the change")
	if got := gitIn(t, w.origin, "rev-parse", "refs/tags/v0.1.1"); got != w.base {
		t.Errorf("origin's v0.1.1 = %s, want the other release's %s left alone", got, w.base)
	}
}
