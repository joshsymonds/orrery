package enginehost

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// featureReadNow returns the fake time the feature's evidence runner reads.
func featureReadNow() time.Time {
	return time.Unix(1700000000, 0)
}

// featureReadRepo returns a scratch repository with commit B on main and commit C on top of B. It then commits U as a root
// commit with no history shared with B or C, and checks it out, so the checkout's HEAD is U. It returns the repository, B,
// C and U.
func featureReadRepo(t *testing.T) (repo, base, candidate, unrelated string) {
	t.Helper()
	repo = stepGitTestRepo(t)
	base = stepGitTestRun(t, repo, "rev-parse", "HEAD")
	candidate = stepGitTestCommit(t, repo, map[string]string{"README": "candidate\n"})
	stepGitTestRun(t, repo, "checkout", "-q", "--orphan", "unrelated")
	stepGitTestRun(t, repo, "rm", "-r", "-q", "-f", ".")
	stepGitTestRun(t, repo, "commit", "-q", "--allow-empty", "-m", "unrelated")
	unrelated = stepGitTestRun(t, repo, "rev-parse", "HEAD")
	return repo, base, candidate, unrelated
}

// featureReadGitExit runs git in dir with args and returns its exit code. It fails the test when git does not run.
func featureReadGitExit(t *testing.T, dir string, args ...string) int {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	err := cmd.Run()
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return exit.ExitCode()
}

// featureReadItem returns a supplied item that reads README at the revision its run names.
func featureReadItem() evidence.Item {
	return evidence.Item{Supplied: []string{"README"}, Conclusion: "the build passes", Judge: "reviewer"}
}

// featureReadStore returns a store holding epic evidenceRunEpic over repo with stored base base, which CreateEpic gives it
// with no candidate. Requirement R1 holds featureReadItem.
func featureReadStore(t *testing.T, repo, base string) *store.Store {
	t.Helper()
	st := epicTestStore(t)
	if err := st.CreateEpic(t.Context(), store.EpicRow{
		ID: evidenceRunEpic, Repository: repo, Base: base, Title: "Synthetic feature read epic",
		Intent: "Synthetic intent.", Care: "serious", Phase: "accepted",
		Created: featureReadNow(), Changed: featureReadNow(),
		Requirements: []store.EpicRequirement{{
			ID: "R1", Text: "Synthetic requirement.", Evidence: "Synthetic evidence.", Kind: "test",
			Item: featureReadItem(),
		}},
	}); err != nil {
		t.Fatalf("store CreateEpic: %v", err)
	}
	return st
}

// featureReadSetCandidate stores candidate as the candidate of the epic over base, as an integrate batch that failed over
// base leaves it. The batch's run claims candidate, which builds on base, and its end sets candidate with base as Over,
// so the stored base stays base.
func featureReadSetCandidate(t *testing.T, st *store.Store, base, candidate string) {
	t.Helper()
	const path = "plan/S1/integrate"
	ctx := t.Context()
	if err := st.StartNodes(ctx, evidenceRunEpic, []store.NodeRun{{
		Path: path, ID: "feature-read-integrate", Node: "integrate", Kind: "integrate", Attempt: 1,
	}}, nil, featureReadNow()); err != nil {
		t.Fatalf("StartNodes of epic %s: %v", evidenceRunEpic, err)
	}
	if err := st.ClaimBatch(ctx, evidenceRunEpic, path, 1, candidate, "refs", "composed", nil); err != nil {
		t.Fatalf("ClaimBatch of epic %s: %v", evidenceRunEpic, err)
	}
	if err := st.EndBatch(ctx, evidenceRunEpic, store.BatchEnd{
		Path: path, Attempt: 1, Outcome: "failed", Output: "red", Candidate: candidate, Over: base,
	}, nil, featureReadNow()); err != nil {
		t.Fatalf("EndBatch of epic %s: %v", evidenceRunEpic, err)
	}
}

// featureReadEvidence returns an Evidence over st and steps that reads the clock featureReadNow and whose timers fire at
// once, so no run waits on the wall clock. Close runs when the test ends.
func featureReadEvidence(t *testing.T, st *store.Store, steps EvidenceSteps) *Evidence {
	t.Helper()
	ev := NewEvidence(EvidenceConfig{
		Store:    st,
		Steps:    steps,
		StateDir: t.TempDir(),
		Now:      featureReadNow,
		Briefs:   st,
		After: func(time.Duration) <-chan time.Time {
			fired := make(chan time.Time, 1)
			fired <- featureReadNow()
			return fired
		},
	})
	t.Cleanup(ev.Close)
	return ev
}

// featureReadJudgeSteps is the EvidenceSteps of the judge tests. Its judges are those of evidenceRunSteps, and each Start
// also records the StepStart the judge was started with.
type featureReadJudgeSteps struct {
	*evidenceRunSteps
	mu     sync.Mutex
	starts []StepStart
}

// Start records start and starts the next scripted judge.
func (f *featureReadJudgeSteps) Start(ctx context.Context, start StepStart) (string, error) {
	f.mu.Lock()
	f.starts = append(f.starts, start)
	f.mu.Unlock()
	return f.evidenceRunSteps.Start(ctx, start)
}

// featureReadJudgeRun runs item R1 of the epic in st at revision in phase red, and waits for the run to end. It returns the
// run Run stored and the StepStart of the one judge that started.
func featureReadJudgeRun(t *testing.T, st *store.Store, revision string) (store.EvidenceRun, StepStart) {
	t.Helper()
	steps := &featureReadJudgeSteps{
		evidenceRunSteps: &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}},
	}
	ev := featureReadEvidence(t, st, steps)

	started, _ := evidenceRunOnce(t, ev, st, revision, evidence.PhaseRed)
	steps.mu.Lock()
	defer steps.mu.Unlock()
	if len(steps.starts) != 1 {
		t.Fatalf("judge steps started = %d, want one", len(steps.starts))
	}
	return started, steps.starts[0]
}

// TestFeatureReadReadOnlyStepStartsInADetachedWorktreeAtItsRevision starts a read-only step at commit C while the checkout
// of the repository has U checked out. While the step runs, its worktree is a directory other than the repository, is
// detached at C, and the checkout is still at U.
func TestFeatureReadReadOnlyStepStartsInADetachedWorktreeAtItsRevision(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	repo, _, candidate, unrelated := featureReadRepo(t)

	id, err := f.steps.Start(t.Context(), stepStartReadOnly(repo, candidate))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	row, err := f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if row.End != "" {
		t.Fatalf("step ended as %q before its checks, want it running", row.End)
	}
	wt := row.Worktree
	info, err := os.Stat(wt)
	if err != nil {
		t.Fatalf("stat worktree %q: %v", wt, err)
	}
	if !info.IsDir() || wt == repo {
		t.Errorf("worktree %q is dir %t and repository %t, want a directory other than the repository", wt,
			info.IsDir(), wt == repo)
	}
	if got := stepGitTestRun(t, wt, "rev-parse", "HEAD"); got != candidate {
		t.Errorf("worktree HEAD = %s, want the revision %s", got, candidate)
	}
	if code := featureReadGitExit(t, wt, "symbolic-ref", "-q", "HEAD"); code != 1 {
		t.Errorf("git symbolic-ref -q HEAD in the worktree exited %d, want 1 for a detached HEAD", code)
	}
	if got := stepGitTestRun(t, repo, "rev-parse", "HEAD"); got != unrelated {
		t.Errorf("repository HEAD = %s, want the unrelated commit %s it has checked out", got, unrelated)
	}
}

// TestFeatureReadEvidenceJudgeStartsAtTheCandidate runs an evidence item at the candidate the epic holds. The test sets the
// candidate C on the epic over its stored base B, reads the candidate back from the epic, and runs the item at that
// revision. The judge step starts read-only at C, not at the unrelated commit U the checkout has.
func TestFeatureReadEvidenceJudgeStartsAtTheCandidate(t *testing.T) {
	t.Parallel()
	repo, base, candidate, unrelated := featureReadRepo(t)
	st := featureReadStore(t, repo, base)
	featureReadSetCandidate(t, st, base, candidate)
	epic, err := st.Epic(t.Context(), evidenceRunEpic)
	if err != nil {
		t.Fatalf("Epic %s: %v", evidenceRunEpic, err)
	}
	if epic.Candidate != candidate || epic.StoredBase != base {
		t.Fatalf("epic candidate %q stored base %q, want candidate %q over stored base %q",
			epic.Candidate, epic.StoredBase, candidate, base)
	}

	started, judge := featureReadJudgeRun(t, st, epic.Candidate)
	if started.Revision != epic.Candidate {
		t.Fatalf("run revision = %s, want the epic's candidate %s", started.Revision, epic.Candidate)
	}
	if !judge.ReadOnly || judge.Revision != epic.Candidate || judge.Revision == unrelated {
		t.Errorf("judge = read-only %t revision %s, want read-only at the epic's candidate %s", judge.ReadOnly,
			judge.Revision, epic.Candidate)
	}
}

// TestFeatureReadEvidenceJudgeStartsAtTheStoredBase runs an evidence item at the epic's stored base B, when the epic holds
// no candidate. The test reads the epic back to check that it holds no candidate, then runs the item at its stored base. The
// judge step starts read-only at B, not at the unrelated commit U the checkout has.
func TestFeatureReadEvidenceJudgeStartsAtTheStoredBase(t *testing.T) {
	t.Parallel()
	repo, base, _, unrelated := featureReadRepo(t)
	st := featureReadStore(t, repo, base)
	epic, err := st.Epic(t.Context(), evidenceRunEpic)
	if err != nil {
		t.Fatalf("Epic %s: %v", evidenceRunEpic, err)
	}
	if epic.Candidate != "" || epic.StoredBase != base {
		t.Fatalf("epic candidate %q stored base %q, want no candidate over stored base %q",
			epic.Candidate, epic.StoredBase, base)
	}

	started, judge := featureReadJudgeRun(t, st, epic.StoredBase)
	if started.Revision != epic.StoredBase {
		t.Fatalf("run revision = %s, want the epic's stored base %s", started.Revision, epic.StoredBase)
	}
	if !judge.ReadOnly || judge.Revision != epic.StoredBase || judge.Revision == unrelated {
		t.Errorf("judge = read-only %t revision %s, want read-only at the epic's stored base %s", judge.ReadOnly,
			judge.Revision, epic.StoredBase)
	}
}
