package interpret

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestEpicBaseNoSyncOriginAheadStartsNoSync: origin's main moves ahead of an epic's stored base while the epic's batch
// is at its quiet point. No pass starts a node run of kind sync, the stored base moves to the batch's candidate only in
// the pass its integrate run passes, and it stays there while origin's commit waits. It moves again only when the next
// integrate run passes.
func TestEpicBaseNoSyncOriginAheadStartsNoSync(t *testing.T) {
	t.Parallel()
	var origin string
	w := newIntegrateWorld(t, madeBy(func(t *testing.T) (string, string) {
		t.Helper()
		repo, base := nodeRepo(t)
		origin = mergeRemote(t, repo)
		return repo, base
	}))
	// The identity a merge of origin's main would be committed under, so that a sync that started would merge it.
	w.git("config", "user.name", "Test")
	w.git("config", "user.email", "test@example.invalid")
	candidate := w.toCaptured()
	originHead := mergeOriginCommit(t, origin, "origin.txt")
	if got := epicOf(t, w.s).StoredBase; got != w.base {
		t.Fatalf("stored base = %s before the batch passes, want the base %s", got, w.base)
	}

	noSyncPass(t, w)
	if got := epicOf(t, w.s).StoredBase; got != candidate {
		t.Fatalf("stored base = %s after the batch passes, want its candidate %s", got, candidate)
	}
	for range 3 {
		noSyncPass(t, w)
	}
	if epic := epicOf(t, w.s); epic.StoredBase != candidate {
		t.Errorf("stored base %s at the quiet point, want %s: origin's commit %s was merged",
			epic.StoredBase, candidate, originHead)
	}

	one := w.commit(candidate, "t3.go", "one\n")
	two := w.commit(candidate, "t4.go", "two\n")
	w.planTurn()
	w.accept(3, candidate, one)
	w.accept(4, candidate, two)
	noSyncPass(t, w)
	w.endGate(integrateNextBatch, 0, "")
	noSyncPass(t, w)
	w.endCapture(integrateNextBatch)
	noSyncPass(t, w)
	next := w.candidate(integrateNextBatch)
	if got := epicOf(t, w.s).StoredBase; got != next {
		t.Errorf("stored base = %s after the next batch passes, want its candidate %s", got, next)
	}
}

// noSyncPass carries one pass out on w. It fails the test when the pass starts a node run of kind sync, and when it
// moves the stored base in a pass where no integrate run passed.
func noSyncPass(t *testing.T, w *integrateWorld) {
	t.Helper()
	before := epicOf(t, w.s).StoredBase
	passedBefore := passedIntegrates(t, w.s)
	w.pass()
	if after := epicOf(t, w.s).StoredBase; after != before && passedIntegrates(t, w.s) == passedBefore {
		t.Errorf("stored base moved from %s to %s in a pass no integrate run passed in", before, after)
	}
	for _, run := range runsOf(t, w.s) {
		if run.Kind == "sync" {
			t.Errorf("node run %s of kind sync started, want none", run.Path)
		}
	}
}

// passedIntegrates returns how many integrate runs of the test epic ended passed.
func passedIntegrates(t *testing.T, s *store.Store) int {
	t.Helper()
	passed := 0
	for _, run := range runsOf(t, s) {
		if run.Kind == kindIntegrate && run.State == store.NodeEnded && run.Outcome == outcomePassed {
			passed++
		}
	}
	return passed
}
