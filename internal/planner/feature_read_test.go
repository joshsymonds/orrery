package planner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

const featureEpicID = "orrery-5e6f7a8/feature-read"

// candidateStore is the Store a feature-read test runs the Runner over: the real store, with each epic read carrying
// candidate as its candidate.
type candidateStore struct {
	*store.Store
	candidate string
}

// Epic returns the stored epic with its candidate set to s.candidate.
func (s candidateStore) Epic(ctx context.Context, id string) (store.EpicRow, error) {
	row, err := s.Store.Epic(ctx, id)
	if err != nil {
		return row, fmt.Errorf("read epic %s: %w", id, err)
	}
	row.Candidate = s.candidate
	return row, nil
}

// featureRepo is a temporary repository with commit B, commit C on top of B, and the unrelated commit U checked out.
type featureRepo struct {
	dir       string
	base      string
	candidate string
	unrelated string
}

// featureGit runs git in dir with the test identity and returns its trimmed output. It fails the test on error.
func featureGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "user.name=Test",
		"-c", "user.email=test@example.invalid",
		"-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// featureCommit writes content to the repository-relative path rel, stages it and commits it as message, and returns
// the commit's id.
func featureCommit(t *testing.T, dir, rel, content, message string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	featureGit(t, dir, "add", rel)
	featureGit(t, dir, "commit", "-q", "-m", message)
	return featureGit(t, dir, "rev-parse", "HEAD")
}

// newFeatureRepo makes the repository under t.TempDir(): commit B, commit C on top of it, and an unrelated commit U
// with no parent, which is what the checkout is on.
func newFeatureRepo(t *testing.T) featureRepo {
	t.Helper()
	dir := t.TempDir()
	featureGit(t, dir, "init", "-q", "-b", "main")
	base := featureCommit(t, dir, "base.txt", "base\n", "B")
	candidate := featureCommit(t, dir, "candidate.txt", "candidate\n", "C")
	featureGit(t, dir, "checkout", "-q", "--orphan", "unrelated")
	featureGit(t, dir, "rm", "-r", "-q", "-f", ".")
	unrelated := featureCommit(t, dir, "unrelated.txt", "unrelated\n", "U")
	return featureRepo{dir: dir, base: base, candidate: candidate, unrelated: unrelated}
}

// createFeatureEpic stores featureEpicID over repo with stored base base.
func createFeatureEpic(t *testing.T, s *store.Store, repo, base string) {
	t.Helper()
	if err := s.CreateEpic(t.Context(), store.EpicRow{
		ID: featureEpicID, Repository: repo, Base: base, Title: "Read at the candidate", Approved: "approved text",
		Intent: "plan the next tasks", Care: "serious", Approach: "one planner turn", ReleaseThen: "release it",
		Checks: "just check", Phase: "accepted", Created: claimedAt().Add(-time.Minute),
		Changed: claimedAt().Add(-time.Minute),
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: "a read-only step reads the candidate", Evidence: "runner test", Kind: "test"},
		},
	}); err != nil {
		t.Fatalf("CreateEpic %s: %v", featureEpicID, err)
	}
}

// assertStartedAt checks that start is a read-only step whose revision is want in repo.
func assertStartedAt(t *testing.T, start enginehost.StepStart, repo featureRepo, want string) {
	t.Helper()
	if !start.ReadOnly {
		t.Errorf("step ReadOnly = false, want true")
	}
	if start.Revision == "" {
		t.Fatalf("step Revision is empty, want %s", want)
	}
	if got := featureGit(t, repo.dir, "rev-parse", start.Revision); got != want {
		t.Errorf("step Revision %s resolves to %s, want %s (the unrelated commit is %s)",
			start.Revision, got, want, repo.unrelated)
	}
}

func TestFeatureReadPlannerStepStartsAtTheCandidate(t *testing.T) {
	t.Parallel()
	repo := newFeatureRepo(t)
	s := openRunnerStore(t)
	createFeatureEpic(t, s, repo.dir, repo.base)
	steps := newFakeSteps()
	steps.watchStore(s)
	var reads atomic.Int64
	cfg := runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads))
	cfg.Store = candidateStore{Store: s, candidate: repo.candidate}
	runRunner(t, cfg)

	waitFor(t, &steps.changeNotice, "a planner step to start", func() bool { return len(steps.started()) == 1 })
	assertStartedAt(t, steps.started()[0], repo, repo.candidate)
}

func TestFeatureReadPlannerStepStartsAtTheStoredBase(t *testing.T) {
	t.Parallel()
	repo := newFeatureRepo(t)
	s := openRunnerStore(t)
	createFeatureEpic(t, s, repo.dir, repo.base)
	steps := newFakeSteps()
	steps.watchStore(s)
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	waitFor(t, &steps.changeNotice, "a planner step to start", func() bool { return len(steps.started()) == 1 })
	assertStartedAt(t, steps.started()[0], repo, repo.base)
}
