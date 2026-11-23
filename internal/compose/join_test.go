package compose

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// joinPrefix is the namespace the tests pin a join's merge under.
const joinPrefix = "refs/orrery/test/join/"

// TestJoinKeepsALineThatHoldsTheBase: a line that builds on base, and base itself, are their own join, and nothing is
// pinned for them.
func TestJoinKeepsALineThatHoldsTheBase(t *testing.T) {
	t.Parallel()
	dir := composeRepo(t)
	base := composeBase(t, dir)
	line := composeRevision(t, dir, base, "line\n", map[string]string{"c": "sea"}, nil)
	for _, tc := range []struct{ name, line string }{{"descendant", line}, {"the base itself", base}} {
		got, ok, err := Join(context.Background(), hostexec.NewLocal(), dir, base, tc.line, joinPrefix)
		if err != nil || !ok || got != tc.line {
			t.Errorf("%s: Join = %q, %v, %v, want %q, true, nil", tc.name, got, ok, err, tc.line)
		}
	}
	if refs := composeGit(t, dir, nil, "", "for-each-ref", "refs/orrery/"); refs != "" {
		t.Errorf("refs under refs/orrery/ = %q, want none", refs)
	}
}

// TestJoinGivesTheBaseWhenItHoldsTheLine: a line that base already holds joins as base.
func TestJoinGivesTheBaseWhenItHoldsTheLine(t *testing.T) {
	t.Parallel()
	dir := composeRepo(t)
	line := composeBase(t, dir)
	base := composeRevision(t, dir, line, "base\n", map[string]string{"c": "sea"}, nil)
	got, ok, err := Join(context.Background(), hostexec.NewLocal(), dir, base, line, joinPrefix)
	if err != nil || !ok || got != base {
		t.Errorf("Join = %q, %v, %v, want %q, true, nil", got, ok, err, base)
	}
}

// TestJoinMergesTheBaseIntoAForkedLine: a line forked from an ancestor of base joins as the merge of base into it: its
// parents are the line and then base, its tree holds the changes of both, it carries the line's identity, and it is
// pinned under the prefix and its own id. Joining again gives the same commit.
func TestJoinMergesTheBaseIntoAForkedLine(t *testing.T) {
	t.Parallel()
	dir := composeRepo(t)
	fork := composeBase(t, dir)
	base := composeRevision(t, dir, fork, "base\n", map[string]string{"a": "won"}, nil)
	line := composeRevision(t, dir, fork, "line\n", map[string]string{"c": "sea"}, []string{"b"})
	ctx := context.Background()

	merge, ok, err := Join(ctx, hostexec.NewLocal(), dir, base, line, joinPrefix)
	if err != nil || !ok {
		t.Fatalf("Join = %q, %v, %v, want a merge", merge, ok, err)
	}
	if got := composeParents(t, dir, merge); !slices.Equal(got, []string{line, base}) {
		t.Errorf("merge parents = %v, want the line %s then the base %s", got, line, base)
	}
	if got, want := composeFiles(t, dir, merge), map[string]string{"a": "won", "c": "sea"}; !maps.Equal(got, want) {
		t.Errorf("merge files = %v, want %v", got, want)
	}
	want := composeMeta(t, dir, line)
	want = want[:len(want)-len("line")] + "Merge " + base + " into " + line
	if got := composeMeta(t, dir, merge); got != want {
		t.Errorf("merge meta = %q, want %q", got, want)
	}
	if got := composeGit(t, dir, nil, "", "rev-parse", joinPrefix+merge); got != merge {
		t.Errorf("pin %s = %s, want %s", joinPrefix+merge, got, merge)
	}
	again, ok, err := Join(ctx, hostexec.NewLocal(), dir, base, line, joinPrefix)
	if err != nil || !ok || again != merge {
		t.Errorf("second Join = %q, %v, %v, want the same merge %q", again, ok, err, merge)
	}
}

// TestJoinRefusesAMergeThatConflicts: a line and a base that change one path apart do not join, and nothing is pinned
// or moved.
func TestJoinRefusesAMergeThatConflicts(t *testing.T) {
	t.Parallel()
	dir := composeRepo(t)
	fork := composeBase(t, dir)
	base := composeRevision(t, dir, fork, "base\n", map[string]string{"a": "won"}, nil)
	line := composeRevision(t, dir, fork, "line\n", map[string]string{"a": "uno"}, nil)
	before := composeGit(t, dir, nil, "", "for-each-ref")

	got, ok, err := Join(context.Background(), hostexec.NewLocal(), dir, base, line, joinPrefix)
	if err != nil || ok || got != "" {
		t.Errorf("Join = %q, %v, %v, want no commit, false, nil", got, ok, err)
	}
	if after := composeGit(t, dir, nil, "", "for-each-ref"); !reflect.DeepEqual(after, before) {
		t.Errorf("refs = %q, want them unchanged as %q", after, before)
	}
}

// TestJoinRefusesAPrefixOutsideOrrery: a merge is pinned only under refs/orrery/.
func TestJoinRefusesAPrefixOutsideOrrery(t *testing.T) {
	t.Parallel()
	dir := composeRepo(t)
	fork := composeBase(t, dir)
	base := composeRevision(t, dir, fork, "base\n", map[string]string{"a": "won"}, nil)
	line := composeRevision(t, dir, fork, "line\n", map[string]string{"c": "sea"}, nil)
	if _, _, err := Join(context.Background(), hostexec.NewLocal(), dir, base, line, "refs/heads/join/"); err == nil {
		t.Error("Join with a prefix under refs/heads/ = nil error, want a refusal")
	}
}
