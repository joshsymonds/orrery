package interpret

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// newEpicBaseWorld returns a conflict world whose epic started from base, the commit main was at when its branch was
// made, and whose tip changes tipFile on top of it. origin's main has moved past the tip: it holds one commit, on top of
// base as well, that changes originFile. The epic is stored clean with the tag step as its one release step, and nothing
// has passed yet.
func newEpicBaseWorld(t *testing.T, tipFile, originFile string) conflictWorld {
	t.Helper()
	w := newReleaseWorld(t, &recordingEnding{})
	base := nodeGit(t, w.repo, "rev-parse", "main")
	remote := mergeRemote(t, w.repo)
	tip := nodeEpicBranch(t, w.repo, tipFile)
	origin := mergeOriginCommit(t, remote, originFile)
	w.createEpicOn(releaseFlow, base, runEpic, store.PhaseClean, tagStep())
	return conflictWorld{releaseWorld: w, tip: tip, origin: origin}
}

// TestEpicBaseReleaseMergesOriginIntoTipAndPinsIt: an epic whose origin's main has moved past its tip still has its
// release prepare merge origin's main into the tip. The merge has the tip and origin's commit as its two parents and holds
// origin's file, and it is pinned under the run's release ref and is the run's revision.
func TestEpicBaseReleaseMergesOriginIntoTipAndPinsIt(t *testing.T) {
	t.Parallel()
	w := newEpicBaseWorld(t, "epic.txt", "origin.txt")
	w.pass()

	run := runAt(t, w.s, releaseNodePath)
	got := decodeMerge(t, run.Result)
	if !got.Behind || got.Error != "" || got.Tip != w.tip || got.Candidate == "" {
		t.Fatalf("Result = %+v, want a merge behind tip %s", got, w.tip)
	}
	if run.Revision != got.Candidate {
		t.Errorf("revision = %q, want the candidate %q", run.Revision, got.Candidate)
	}
	parents := strings.Fields(nodeGit(t, w.repo, "rev-list", "--parents", "-n", "1", got.Candidate))
	if want := []string{got.Candidate, w.tip, w.origin}; !slices.Equal(parents, want) {
		t.Errorf("candidate and its parents = %q, want %q", parents, want)
	}
	if content := nodeGit(t, w.repo, "show", got.Candidate+":origin.txt"); content != "origin" {
		t.Errorf("origin.txt in the candidate = %q, want origin's content", content)
	}
	ref := fmt.Sprintf("refs/orrery/epic-runner/release/%s-%d", run.ID, run.Attempt)
	if pinned := nodeGit(t, w.repo, "rev-parse", ref); pinned != got.Candidate {
		t.Errorf("%s points at %q, want the candidate %q", ref, pinned, got.Candidate)
	}
}

// TestEpicBaseReleaseWalksDoneGatesAtMerge: the merge a release prepare pins is the commit its Done gate runs at, and
// nothing is recorded or started before that gate passes. Once it passes, the merge is recorded as the candidate and the
// first step starts at it.
func TestEpicBaseReleaseWalksDoneGatesAtMerge(t *testing.T) {
	t.Parallel()
	w := newEpicBaseWorld(t, "epic.txt", "origin.txt")
	w.pass()

	candidate := runAt(t, w.s, releaseNodePath).Revision
	if candidate == "" || candidate == w.tip || !containsCommit(t, w.repo, w.origin, candidate) {
		t.Fatalf("revision = %q after the prepare, want a merge that contains origin's main %s", candidate, w.origin)
	}
	gate := workID(t, w.s, releaseNodePath, "done 1")
	if got := w.fakes.checks.calls; len(got) != 1 || got[0].ID != gate || got[0].Revision != candidate {
		t.Fatalf("checks run = %+v, want the one Done gate run at the merge %s", got, candidate)
	}
	if got := w.fakes.steps.calls; len(got) != 0 {
		t.Fatalf("step starts = %+v before the gate ends, want none", got)
	}
	if got := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartCandidate); len(got) != 0 {
		t.Fatalf("candidate runs = %+v before the gate ends, want none", got)
	}

	w.endGate(0)
	w.pass()

	candidates := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartCandidate)
	if len(candidates) != 1 || candidates[0].Head != candidate || !candidates[0].Passed {
		t.Errorf("candidate runs = %+v, want the merge %s recorded as passed", candidates, candidate)
	}
	if got := w.fakes.steps.calls; len(got) != 1 || got[0].ID != w.stepID(1) || got[0].Revision != candidate {
		t.Errorf("step starts = %+v, want the first step started at the merge %s", got, candidate)
	}
}

// TestEpicBaseReleaseConflictGoesToIntegrator: origin's main has moved past the tip and changes the file the tip changes,
// so the merge conflicts. The prepare records the conflict with no candidate and runs no Done gate, and it starts the
// release integrator at the tip with origin's commit merging in.
func TestEpicBaseReleaseConflictGoesToIntegrator(t *testing.T) {
	t.Parallel()
	w := newEpicBaseWorld(t, "README", "README")
	w.pass()

	run := runAt(t, w.s, releaseNodePath)
	var got struct {
		mergeWire
		Origin    string   `json:"origin"`
		Conflicts []string `json:"conflicts"`
	}
	if err := json.Unmarshal([]byte(run.Result), &got); err != nil {
		t.Fatalf("Result %q: %v", run.Result, err)
	}
	if got.Error != "" || !got.Behind || got.Tip != w.tip || got.Origin != w.origin || got.Candidate != "" ||
		!slices.Equal(got.Conflicts, []string{"README"}) || run.Revision != "" {
		t.Errorf("Result = %+v with revision %q, want the conflict in README of %s into %s and no candidate",
			got, run.Revision, w.origin, w.tip)
	}
	if n := len(w.fakes.checks.calls); n != 0 {
		t.Errorf("Done gate checks = %d, want none for a merge that conflicts", n)
	}

	start := w.started(w.integratorID(1))
	if start.Schema != IntegratorSchema || start.Revision != w.tip || !start.Unowned ||
		start.Workspace != w.worktree() {
		t.Errorf("integrator start = %+v, want an unowned integrator at the tip %s in %s", start, w.tip, w.worktree())
	}
	if mergeHead := nodeGit(t, w.worktree(), "rev-parse", "MERGE_HEAD"); mergeHead != w.origin {
		t.Errorf("worktree MERGE_HEAD = %s, want origin's main %s", mergeHead, w.origin)
	}
}
