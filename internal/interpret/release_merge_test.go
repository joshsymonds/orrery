package interpret

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// mergeWire is the Result of a release run as the tests read it from the store. The tests decode it themselves, so they
// check the shape the release kind and the executor share without taking that shape from the code under test.
type mergeWire struct {
	Error     string `json:"error"`
	Behind    bool   `json:"behind"`
	Candidate string `json:"candidate"`
	Tip       string `json:"tip"`
	Origin    string `json:"origin"`
}

// mergeRepo makes a scratch repository on main, with the identity the release's merge commit is written under.
func mergeRepo(t *testing.T) string {
	t.Helper()
	repo, _ := nodeRepo(t)
	nodeGit(t, repo, "config", "user.name", "Test")
	nodeGit(t, repo, "config", "user.email", "test@example.invalid")
	return repo
}

// mergeRemote adds a bare origin to repo as its origin remote, pushes main to it, and returns the origin's path.
func mergeRemote(t *testing.T, repo string) string {
	t.Helper()
	origin := t.TempDir()
	nodeGit(t, origin, "init", "-q", "--bare", "-b", "main")
	nodeGit(t, repo, "remote", "add", "origin", origin)
	nodeGit(t, repo, "push", "-q", "origin", "main")
	return origin
}

// mergeOriginCommit commits file, holding one line, to origin's main from a clone of origin, so the scratch repository's
// own branches do not move, and returns the commit it made.
func mergeOriginCommit(t *testing.T, origin, file string) string {
	t.Helper()
	clone := t.TempDir()
	nodeGit(t, clone, "clone", "-q", origin, ".")
	nodeWrite(t, clone, file, "origin\n")
	nodeGit(t, clone, "add", file)
	nodeGit(t, clone, "commit", "-q", "-m", "origin "+file)
	nodeGit(t, clone, "push", "-q", "origin", "main")
	return nodeGit(t, clone, "rev-parse", "HEAD")
}

// newMergeWorld returns a release world with the test epic stored clean in its scratch repository, with the tag step as
// its one release step.
func newMergeWorld(t *testing.T) *releaseWorld {
	t.Helper()
	w := newReleaseWorld(t, &recordingEnding{})
	w.createEpic(runEpic, store.PhaseClean, tagStep())
	return w
}

// decodeMerge decodes raw, the Result of a release run, which must be a JSON object.
func decodeMerge(t *testing.T, raw string) mergeWire {
	t.Helper()
	var got mergeWire
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("Result %q is not a release result: %v", raw, err)
	}
	return got
}

// TestReleasePrepareNotBehindRecordsTipAsCandidate: origin's main is an ancestor of the epic's tip, so the candidate is
// the tip itself, recorded as the Result and prepared as the revision.
func TestReleasePrepareNotBehindRecordsTipAsCandidate(t *testing.T) {
	t.Parallel()
	w := newMergeWorld(t)
	repo := w.repo
	mergeRemote(t, repo)
	tip := nodeEpicBranch(t, repo, "epic.txt")
	w.pass()

	run := runAt(t, w.s, releaseNodePath)
	want := mergeWire{Tip: tip, Candidate: tip}
	if got := decodeMerge(t, run.Result); got != want {
		t.Errorf("Result = %+v, want %+v", got, want)
	}
	if run.Revision != tip {
		t.Errorf("revision = %q, want the tip %q", run.Revision, tip)
	}
}

// TestReleasePrepareBehindPinsTwoParentMerge: origin's main has a commit the tip lacks, so the candidate is a merge of
// the tip and origin's commit that holds both sides. It is pinned under the run's release ref, and no ref outside
// refs/orrery moves: not a branch, origin's remote-tracking refs, HEAD or the checkout.
func TestReleasePrepareBehindPinsTwoParentMerge(t *testing.T) {
	t.Parallel()
	w := newMergeWorld(t)
	repo := w.repo
	origin := mergeRemote(t, repo)
	nodeGit(t, repo, "remote", "set-head", "origin", "-a")
	tip := nodeEpicBranch(t, repo, "epic.txt")
	originHead := mergeOriginCommit(t, origin, "origin.txt")
	head := nodeGit(t, repo, "symbolic-ref", "HEAD")
	refs := nodeGit(t, repo, "for-each-ref", "--exclude=refs/orrery")
	w.pass()

	run := runAt(t, w.s, releaseNodePath)
	got := decodeMerge(t, run.Result)
	if !got.Behind || got.Error != "" || got.Tip != tip || run.Revision == "" || got.Candidate != run.Revision {
		t.Fatalf("Result = %+v with revision %q, want a merge behind tip %q", got, run.Revision, tip)
	}
	ref := fmt.Sprintf("refs/orrery/epic-runner/release/%s-%d", run.ID, run.Attempt)
	if pinned := nodeGit(t, repo, "rev-parse", ref); pinned != run.Revision {
		t.Errorf("%s points at %q, want the candidate %q", ref, pinned, run.Revision)
	}
	parents := strings.Fields(nodeGit(t, repo, "rev-list", "--parents", "-n", "1", run.Revision))
	if want := []string{run.Revision, tip, originHead}; !reflect.DeepEqual(parents, want) {
		t.Errorf("merge commit and parents = %q, want %q", parents, want)
	}
	files := strings.Fields(nodeGit(t, repo, "ls-tree", "--name-only", run.Revision))
	for _, file := range []string{"README", "epic.txt", "origin.txt"} {
		if !slices.Contains(files, file) {
			t.Errorf("merge tree = %q, want it to hold %s from one side or the other", files, file)
		}
	}
	if after := nodeGit(t, repo, "symbolic-ref", "HEAD"); after != head {
		t.Errorf("HEAD = %q after the prepare, want %q", after, head)
	}
	if after := nodeGit(t, repo, "for-each-ref", "--exclude=refs/orrery"); after != refs {
		t.Errorf("refs outside refs/orrery after the prepare = %q, want %q", after, refs)
	}
	if status := nodeGit(t, repo, "status", "--porcelain"); status != "" {
		t.Errorf("checkout after the prepare = %q, want it clean", status)
	}
}

// TestReleasePrepareWithoutOriginRecordsError: a repository with no origin records an error that names the command that
// failed, and no revision.
func TestReleasePrepareWithoutOriginRecordsError(t *testing.T) {
	t.Parallel()
	w := newMergeWorld(t)
	nodeEpicBranch(t, w.repo, "epic.txt")
	w.pass()

	run := runAt(t, w.s, releaseNodePath)
	if got := decodeMerge(t, run.Result); !strings.Contains(got.Error, "ls-remote") {
		t.Errorf("Result error = %q, want it to name the failed ls-remote", got.Error)
	}
	if run.Revision != "" {
		t.Errorf("revision = %q, want none without an origin", run.Revision)
	}
}

// TestReleasePrepareRepeatChangesNothing: a prepare of a run whose Result is already set reads nothing from git again,
// so its Result, its revision and every ref stay as they were.
func TestReleasePrepareRepeatChangesNothing(t *testing.T) {
	t.Parallel()
	w := newMergeWorld(t)
	repo := w.repo
	origin := mergeRemote(t, repo)
	nodeEpicBranch(t, repo, "epic.txt")
	mergeOriginCommit(t, origin, "origin.txt")
	w.pass()
	first := runAt(t, w.s, releaseNodePath)
	refs := nodeGit(t, repo, "for-each-ref")

	act := Action{Kind: ActWork, Work: kinds.ActPrepare, NodeKind: kindRelease, Path: releaseNodePath, Run: first}
	if err := w.runner.work(w.t.Context(), epicOf(t, w.s), act, runClock()); err != nil {
		t.Fatalf("work: %v", err)
	}
	again := runAt(t, w.s, releaseNodePath)
	if again.Result != first.Result || again.Revision != first.Revision {
		t.Errorf("run after a repeat = result %q and revision %q, want %q and %q",
			again.Result, again.Revision, first.Result, first.Revision)
	}
	if after := nodeGit(t, repo, "for-each-ref"); after != refs {
		t.Errorf("refs after a repeat = %q, want %q", after, refs)
	}
}

// TestReleasePrepareResultSetPreparesRecordedCandidate: a run whose Result is recorded and whose revision is not, as a
// crash between the two writes leaves it, is prepared at the candidate its Result names. Origin has moved past the tip
// since the Result was recorded, so a prepare that read git again would pin a merge that contains origin's main. The
// epic is paused for the first pass, so the run starts but does not prepare.
//
// This test used to assert that the recorded candidate stayed and that no ref moved. Its assertions changed because
// the candidate is now checked before the first step starts, whether an attempt recorded it or an earlier one did: the
// tip is recorded, origin's main is found not in it, and the attempt prepares again. The verify run that marks the tip
// stale is the proof that the prepare took the recorded Result rather than reading git again.
func TestReleasePrepareResultSetPreparesRecordedCandidate(t *testing.T) {
	t.Parallel()
	w := newMergeWorld(t)
	origin := mergeRemote(t, w.repo)
	tip := nodeEpicBranch(t, w.repo, "epic.txt")
	if err := w.s.PauseEpic(
		w.t.Context(),
		runEpic,
		store.Maker{By: store.MakerOwner},
		time.Unix(1700000000, 0),
	); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}
	w.pass()
	run := runAt(t, w.s, releaseNodePath)
	if run.Result != "" || run.Revision != "" {
		t.Fatalf("run before the record = result %q and revision %q, want neither", run.Result, run.Revision)
	}

	encoded, err := json.Marshal(mergeWire{Tip: tip, Candidate: tip})
	if err != nil {
		t.Fatalf("encode Result: %v", err)
	}
	if err = w.s.RecordNodeResult(w.t.Context(), runEpic, releaseNodePath, run.Attempt, string(encoded)); err != nil {
		t.Fatalf("RecordNodeResult: %v", err)
	}
	moved := mergeOriginCommit(t, origin, "origin.txt")
	if err = w.s.ResumeEpic(
		w.t.Context(),
		runEpic,
		store.Maker{By: store.MakerOwner},
		time.Unix(1700000000, 0),
	); err != nil {
		t.Fatalf("ResumeEpic: %v", err)
	}
	w.pass()

	verify := w.releaseVerifyRuns()
	if len(verify) != 1 || verify[0].Head != tip || verify[0].Attempt != run.Attempt || verify[0].Passed {
		t.Errorf("verify runs = %+v, want the one mark of the recorded candidate %s at attempt %d",
			verify, tip, run.Attempt)
	}
	got := runAt(t, w.s, releaseNodePath)
	if got.Revision == tip || !containsCommit(t, w.repo, moved, got.Revision) {
		t.Errorf("revision = %q, want a new candidate that contains origin's main %s", got.Revision, moved)
	}
}

// TestMergedTreeReadsTreeAndConflictedPaths: merge-tree writes its tree first and then each conflicted path, and a merge
// that conflicts writes a tree too. mergedTree reads the tree and the paths, each once. The output is synthetic, so the
// test needs no conflicting repository.
func TestMergedTreeReadsTreeAndConflictedPaths(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		out   string
		paths []string
	}{
		{name: "clean merge", out: "tree\n"},
		{name: "conflict naming paths", out: "tree\nREADME\nd/f\n", paths: []string{"README", "d/f"}},
		{name: "a path named twice", out: "tree\nREADME\nREADME\n", paths: []string{"README"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tree, paths := mergedTree(tc.out)
			if tree != "tree" || !slices.Equal(paths, tc.paths) {
				t.Errorf("mergedTree = %q, %q, want %q, %q", tree, paths, "tree", tc.paths)
			}
		})
	}
}

// TestConflictMessageNamesConflictedPaths: the reason a conflicting merge gives names origin's commit, the tip and each
// conflicted path, and says only that the merge conflicts when git named no path.
func TestConflictMessageNamesConflictedPaths(t *testing.T) {
	t.Parallel()
	if got, want := conflictMessage("tip", "origin", []string{"README", "d/f"}),
		"merge of origin's commit origin into tip conflicts in README, d/f"; got != want {
		t.Errorf("conflictMessage = %q, want %q", got, want)
	}
	got, want := conflictMessage("tip", "origin", nil), "merge of origin's commit origin into tip conflicts"
	if got != want {
		t.Errorf("conflictMessage with no path = %q, want %q", got, want)
	}
}
