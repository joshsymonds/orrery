package compose

import (
	"maps"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// TestOverlayTakesListedPathsFromSource checks that the overlay is the base's tree with each listed path taken from the
// source: a listed path the source changed takes its new content, a listed path it deleted is removed, a listed path it
// added is added, and an unlisted path keeps the base's content, whatever the source did to it.
func TestOverlayTakesListedPathsFromSource(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := composeRepo(t)
	base := composeRevision(t, dir, "", "base\n", map[string]string{"a": "one", "b": "bee", "c": "see"}, nil)
	source := composeRevision(t, dir, base, "source\n",
		map[string]string{"a": "changed", "n": "new", "x": "unlisted"}, []string{"b"})

	overlay, err := Overlay(ctx, hostexec.NewLocal(), dir, base, source, []string{"a", "b", "n"}, testRef)
	if err != nil {
		t.Fatalf("Overlay: %v", err)
	}
	want := map[string]string{"a": "changed", "c": "see", "n": "new"}
	if got := composeFiles(t, dir, overlay); !maps.Equal(got, want) {
		t.Fatalf("overlay files = %v, want %v", got, want)
	}
}

// TestOverlayIsOneCommitOnBaseWithSourceIdentity checks that the overlay's parent is the base, that the pin names it, and
// that it carries the source's author, committer, dates and message. The base is written with a different identity, so
// an overlay that took its identity from the base would fail the comparison.
func TestOverlayIsOneCommitOnBaseWithSourceIdentity(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := composeRepo(t)
	written := composeRevision(t, dir, "", "base\n", map[string]string{"a": "one"}, nil)
	otherIdentity := []string{
		"GIT_AUTHOR_NAME=Mallory",
		"GIT_AUTHOR_EMAIL=mallory@example.invalid",
		"GIT_AUTHOR_DATE=1600000000 +0000",
		"GIT_COMMITTER_NAME=Mallory",
		"GIT_COMMITTER_EMAIL=mallory@example.invalid",
		"GIT_COMMITTER_DATE=1600000050 +0000",
	}
	tree := composeGit(t, dir, nil, "", "rev-parse", written+"^{tree}")
	base := composeGit(t, dir, otherIdentity, "base\n", "commit-tree", tree)
	source := composeRevision(t, dir, base, "source\n", map[string]string{"a": "changed"}, nil)

	overlay, err := Overlay(ctx, hostexec.NewLocal(), dir, base, source, []string{"a"}, testRef)
	if err != nil {
		t.Fatalf("Overlay: %v", err)
	}
	if got := composeParents(t, dir, overlay); !slices.Equal(got, []string{base}) {
		t.Fatalf("overlay parents = %v, want the base %s", got, base)
	}
	if got := composeGit(t, dir, nil, "", "rev-parse", testRef); got != overlay {
		t.Fatalf("pin %s = %s, want the overlay %s", testRef, got, overlay)
	}
	if got, want := composeMeta(t, dir, overlay), composeMeta(t, dir, source); got != want {
		t.Fatalf("overlay meta = %q, want the source's %q", got, want)
	}
}

// TestOverlayRepeatGivesSameCommit checks that the same inputs give the same commit, and that a second call passes
// against the pin the first call made.
func TestOverlayRepeatGivesSameCommit(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := composeRepo(t)
	base := composeRevision(t, dir, "", "base\n", map[string]string{"a": "one", "b": "bee"}, nil)
	source := composeRevision(t, dir, base, "source\n", map[string]string{"a": "changed"}, []string{"b"})

	first, err := Overlay(ctx, hostexec.NewLocal(), dir, base, source, []string{"a", "b"}, testRef)
	if err != nil {
		t.Fatalf("Overlay: %v", err)
	}
	second, err := Overlay(ctx, hostexec.NewLocal(), dir, base, source, []string{"a", "b"}, testRef)
	if err != nil {
		t.Fatalf("second Overlay refused by the pin: %v", err)
	}
	if second != first {
		t.Fatalf("second Overlay = %s, want the first %s", second, first)
	}
}

// TestOverlayPinRefusesAnotherCommit checks that a ref already at another commit is an error, and that the refused
// call leaves the ref where it was.
func TestOverlayPinRefusesAnotherCommit(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := composeRepo(t)
	base := composeRevision(t, dir, "", "base\n", map[string]string{"a": "one"}, nil)
	source := composeRevision(t, dir, base, "source\n", map[string]string{"a": "changed"}, nil)
	other := composeRevision(t, dir, base, "other\n", map[string]string{"z": "zed"}, nil)
	composeGit(t, dir, nil, "", "update-ref", testRef, other)

	if _, err := Overlay(ctx, hostexec.NewLocal(), dir, base, source, []string{"a"}, testRef); err == nil {
		t.Fatal("Overlay moved a pin that points at another commit")
	}
	if got := composeGit(t, dir, nil, "", "rev-parse", testRef); got != other {
		t.Fatalf("pin = %s after a refused Overlay, want %s", got, other)
	}
}
