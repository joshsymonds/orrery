package interpret

import (
	"context"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
)

// featureReadSecurity is the name of the default epic-stage reviewer the reviewer tests start.
const featureReadSecurity = "security"

// featureReadCommits are the commits of the FeatureRead repository: base B on main, candidate C on top of B on the epic's
// branch, and unrelated U, an orphan commit the checkout has checked out, so HEAD is none of the other two.
type featureReadCommits struct {
	repo, base, candidate, unrelated string
}

// featureReadRepo builds the FeatureRead repository: commit B on main, commit C on top of B on the epic's branch, and the
// unrelated commit U on an orphan branch, which is checked out. main stays at B and the epic's branch at C.
func featureReadRepo(t *testing.T) featureReadCommits {
	t.Helper()
	repo := t.TempDir()
	nodeGit(t, repo, "init", "-q", "-b", "main")
	nodeWrite(t, repo, "README", "base\n")
	nodeGit(t, repo, "add", "README")
	nodeGit(t, repo, "commit", "-q", "-m", "base")
	base := nodeGit(t, repo, "rev-parse", "HEAD")

	nodeGit(t, repo, "checkout", "-q", "-b", nodeBranchName)
	nodeWrite(t, repo, "feature.txt", "candidate\n")
	nodeGit(t, repo, "add", "feature.txt")
	nodeGit(t, repo, "commit", "-q", "-m", "candidate")
	candidate := nodeGit(t, repo, "rev-parse", "HEAD")

	nodeGit(t, repo, "checkout", "-q", "main")
	nodeGit(t, repo, "checkout", "-q", "--orphan", "unrelated")
	nodeGit(t, repo, "rm", "-q", "-f", "README")
	nodeWrite(t, repo, "unrelated.txt", "unrelated\n")
	nodeGit(t, repo, "add", "unrelated.txt")
	nodeGit(t, repo, "commit", "-q", "-m", "unrelated")
	unrelated := nodeGit(t, repo, "rev-parse", "HEAD")

	return featureReadCommits{repo: repo, base: base, candidate: candidate, unrelated: unrelated}
}

// featureReadEpic stores the test epic on the repository of c, at its base B, which is also its stored base, with the
// merged reviewers reviewerList.
func featureReadEpic(t *testing.T, s *store.Store, c featureReadCommits, reviewerList []reviewers.Reviewer) {
	t.Helper()
	if err := s.CreateEpic(context.Background(), store.EpicRow{
		ID: runEpic, Repository: c.repo, Base: c.base, Title: "Read the epic", Phase: runPhase,
		Created: runClock(), Changed: runClock(), Reviewers: reviewerList,
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
}

// featureReadReviewers returns orrery's default reviewers, merged as an epic's reviewers are.
func featureReadReviewers(t *testing.T) []reviewers.Reviewer {
	t.Helper()
	merged, err := reviewers.Merge(reviewers.Defaults())
	if err != nil {
		t.Fatalf("Merge(Defaults()): %v", err)
	}
	return merged
}

// featureReadReviewerStart runs the epic-stage review of the test epic through the runner: its review run is prepared,
// which freezes the review at the epic's candidate, or at its stored base when it has none, and then started, which starts
// the security reviewer's step at the run's revision. It returns the step the runner started.
func featureReadReviewerStart(t *testing.T, s *store.Store) enginehost.StepStart {
	t.Helper()
	ctx := context.Background()
	run := reviewWorkStartRun(t, s, reviewNodeKind)
	steps := &fakeSteps{store: s}
	runner := reviewWorkRunner(s, steps, NewEpicReviewers(), reviewWorkGetenv(t))
	epic := epicOf(t, s)
	prepare := Action{Kind: ActWork, Work: kinds.ActPrepare, Path: run.Path, NodeKind: reviewNodeKind, Run: run}
	if err := runner.work(ctx, epic, prepare, runClock()); err != nil {
		t.Fatalf("prepare review: %v", err)
	}
	frozen := runAt(t, s, run.Path)
	id := workID(t, s, run.Path, "reviewer 1")
	start := Action{
		Kind: ActWork, Work: kinds.ActStart, Path: run.Path, NodeKind: reviewNodeKind, Run: frozen,
		IDs:   []string{id},
		Units: []kinds.Unit{{ID: id, Part: "reviewer 1", Reviewer: featureReadSecurity}},
	}
	if err := runner.work(ctx, epic, start, runClock()); err != nil {
		t.Fatalf("start review: %v", err)
	}
	if len(steps.calls) != 1 {
		t.Fatalf("step starts = %+v, want the reviewer's step started once", steps.calls)
	}
	return steps.calls[0]
}

// featureReadCheckStart asserts that start is read-only in the repository of c and that its revision, resolved with
// git rev-parse there, is want.
func featureReadCheckStart(t *testing.T, c featureReadCommits, start enginehost.StepStart, want string) {
	t.Helper()
	if !start.ReadOnly || start.Workspace != c.repo {
		t.Errorf("step start = %+v, want read-only in %s", start, c.repo)
	}
	if got := nodeGit(t, c.repo, "rev-parse", start.Revision); got != want {
		t.Errorf("step start revision %s resolves to %s, want %s (the checkout's HEAD is %s)",
			start.Revision, got, want, c.unrelated)
	}
}

// TestFeatureReadEpicReviewerStartsAtTheCandidate: with the epic's candidate C on its stored base B, an epic-stage
// reviewer's step is read-only and starts at C, not at B and not at the checkout's HEAD.
func TestFeatureReadEpicReviewerStartsAtTheCandidate(t *testing.T) {
	t.Parallel()
	c := featureReadRepo(t)
	s, path := openNodeStore(t)
	featureReadEpic(t, s, c, featureReadReviewers(t))
	execStoreFile(t, path, "UPDATE epics SET candidate = ? WHERE id = ?", c.candidate, runEpic)

	featureReadCheckStart(t, c, featureReadReviewerStart(t, s), c.candidate)
}

// TestFeatureReadEpicReviewerStartsAtTheStoredBase: with no candidate, an epic-stage reviewer's step is read-only and
// starts at the epic's stored base B, not at the checkout's HEAD.
func TestFeatureReadEpicReviewerStartsAtTheStoredBase(t *testing.T) {
	t.Parallel()
	c := featureReadRepo(t)
	s, _ := openNodeStore(t)
	featureReadEpic(t, s, c, featureReadReviewers(t))
	if candidate := epicOf(t, s).Candidate; candidate != "" {
		t.Fatalf("epic candidate = %q, want none", candidate)
	}

	featureReadCheckStart(t, c, featureReadReviewerStart(t, s), c.base)
}

// TestFeatureReadReleaseJudgeStartsAtTheCandidate: the release judge of an epic whose branch holds C on its stored base B
// starts read-only at C, the candidate the release prepared, not at the checkout's HEAD.
func TestFeatureReadReleaseJudgeStartsAtTheCandidate(t *testing.T) {
	t.Parallel()
	c := featureReadRepo(t)
	w := newReleaseWorldIn(t, &recordingEnding{}, c.repo)
	mergeRemote(t, c.repo)
	w.createEpicOn(releaseFlow, c.base, runEpic, store.PhaseClean, tagStep())
	w.pass()
	id := w.stepID(1)
	commit := commitIn(t, w.stepWorktree(id), "step1.txt")
	w.endStep(id, "done", "done", map[string]any{
		"status": "done", "commit": commit, "commits": []string{commit}, "evidence": "tagged main",
	})
	w.pass()

	if len(w.fakes.steps.calls) != 2 {
		t.Fatalf("step starts = %+v, want the judge started after the step", w.fakes.steps.calls)
	}
	judge := w.fakes.steps.calls[1]
	if judge.ID != w.judgeID() {
		t.Fatalf("judge start id = %s, want %s", judge.ID, w.judgeID())
	}
	featureReadCheckStart(t, c, judge, c.candidate)
}
