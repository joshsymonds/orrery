package planner

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// baseStartEpic is the epic the base-start tests plan: an epic with no task and no branch, whose base is the first
// commit of its repository.
const baseStartEpic = "orrery-2b3c4d5/base-start"

// baseStartRepo is an invented repository for the base-start tests: a checkout whose origin is a bare repository. The
// checkout and origin's main are both at base, the commit the epic's base names.
type baseStartRepo struct {
	checkout string
	origin   string
	base     string
}

// newBaseStartRepo makes the bare origin and the checkout under t.TempDir(). The checkout is on main at base, its
// origin remote is the bare origin, and base is pushed to origin's main.
func newBaseStartRepo(t *testing.T) baseStartRepo {
	t.Helper()
	root := t.TempDir()
	repo := baseStartRepo{origin: filepath.Join(root, "origin.git"), checkout: filepath.Join(root, "checkout")}
	featureGit(t, root, "init", "-q", "--bare", "-b", "main", repo.origin)
	featureGit(t, root, "init", "-q", "-b", "main", repo.checkout)
	repo.base = featureCommit(t, repo.checkout, "epic.txt", "base\n", "base commit")
	featureGit(t, repo.checkout, "remote", "add", "origin", repo.origin)
	featureGit(t, repo.checkout, "push", "-q", "origin", "main")
	return repo
}

// advanceOrigin pushes a commit on top of base to origin's main, from a clone of origin, and returns the commit.
// origin's main is then ahead of the epic's base, and the checkout has not fetched it.
func advanceOrigin(t *testing.T, repo baseStartRepo) string {
	t.Helper()
	dir := t.TempDir()
	clone := filepath.Join(dir, "clone")
	featureGit(t, dir, "clone", "-q", repo.origin, clone)
	ahead := featureCommit(t, clone, "epic.txt", "ahead\n", "origin's main moves on")
	featureGit(t, clone, "push", "-q", "origin", "main")
	return ahead
}

// rewriteOrigin force-pushes a commit with no parent to origin's main, so origin's main does not contain the base, and
// returns the commit.
func rewriteOrigin(t *testing.T, repo baseStartRepo) string {
	t.Helper()
	dir := t.TempDir()
	unrelated := filepath.Join(dir, "unrelated")
	featureGit(t, dir, "init", "-q", "-b", "main", unrelated)
	rewritten := featureCommit(t, unrelated, "epic.txt", "rewritten\n", "origin's main rewritten")
	featureGit(t, unrelated, "push", "-q", "--force", repo.origin, "HEAD:main")
	return rewritten
}

// createBaseStartEpic stores baseStartEpic on the checkout of repo, with repo's base as its base and stored base, no
// task and no branch, and its created trigger pending.
func createBaseStartEpic(t *testing.T, s *store.Store, repo baseStartRepo) {
	t.Helper()
	if err := s.CreateEpic(context.Background(), store.EpicRow{
		ID: baseStartEpic, Repository: repo.checkout, Base: repo.base, StoredBase: repo.base,
		Title: "Start from origin's main", Approved: "approved text", Intent: "start from origin's main",
		Care: "serious", Approach: "one planner turn", ReleaseThen: "release it", Checks: "just check",
		Phase: "accepted", Created: claimedAt().Add(-time.Minute), Changed: claimedAt().Add(-time.Minute),
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: "a trigger is claimed", Evidence: "runner test", Kind: "test"},
			{ID: "R2", Text: "a pending trigger gets a turn", Evidence: "runner test", Kind: "test"},
		},
	}); err != nil {
		t.Fatalf("CreateEpic %s: %v", baseStartEpic, err)
	}
}

// runBaseStartTurn runs the planner turn of baseStartEpic, with origin's main read through originMain, and waits until
// the turn starts its step. It returns that start.
func runBaseStartTurn(
	t *testing.T,
	s *store.Store,
	originMain func(ctx context.Context, repository string) (string, error),
) enginehost.StepStart {
	t.Helper()
	steps := newFakeSteps()
	cfg := runnerConfig(s, steps, func() (string, error) { return runnerAgent, nil })
	cfg.OriginMain = originMain
	runRunner(t, cfg)
	waitFor(t, &steps.changeNotice, "a planner step to start", func() bool { return len(steps.started()) == 1 })
	return steps.started()[0]
}

// baseStartRow returns the stored baseStartEpic.
func baseStartRow(t *testing.T, s *store.Store) store.EpicRow {
	t.Helper()
	row, err := s.Epic(context.Background(), baseStartEpic)
	if err != nil {
		t.Fatalf("Epic %s: %v", baseStartEpic, err)
	}
	return row
}

// baseStartTells returns the texts of the tells baseStartEpic holds, in seq order.
func baseStartTells(t *testing.T, s *store.Store) []string {
	t.Helper()
	history, err := s.EpicHistory(context.Background(), baseStartEpic)
	if err != nil {
		t.Fatalf("EpicHistory %s: %v", baseStartEpic, err)
	}
	texts := make([]string, 0, len(history.Tells))
	for _, tell := range history.Tells {
		texts = append(texts, tell.Text)
	}
	return texts
}

// requireStartsAt asserts that the planner step a turn started, started, starts at revision want.
func requireStartsAt(t *testing.T, started enginehost.StepStart, want string) {
	t.Helper()
	if started.Revision != want {
		t.Errorf("planner step starts at revision %s, want %s", started.Revision, want)
	}
}

// requireBaseAt asserts that the base and the stored base of baseStartEpic both are want.
func requireBaseAt(t *testing.T, s *store.Store, want string) {
	t.Helper()
	row := baseStartRow(t, s)
	if row.Base != want || row.StoredBase != want {
		t.Errorf("base and stored base are %s and %s, want both %s", row.Base, row.StoredBase, want)
	}
}

// requireOneTell asserts that baseStartEpic holds exactly one tell, and that the tell contains each of parts.
func requireOneTell(t *testing.T, s *store.Store, parts ...string) {
	t.Helper()
	tells := baseStartTells(t, s)
	if len(tells) != 1 {
		t.Fatalf("epic holds tells %q, want exactly one", tells)
	}
	for _, part := range parts {
		if !strings.Contains(tells[0], part) {
			t.Errorf("tell %q does not contain %q", tells[0], part)
		}
	}
}

// TestEpicBaseStartMovesBaseToOriginMainBeforeTurn asserts that a planner turn of an epic with no task and no branch
// moves its base and stored base to origin's main before its step starts, and that one tell names both commits.
func TestEpicBaseStartMovesBaseToOriginMainBeforeTurn(t *testing.T) {
	t.Parallel()
	repo := newBaseStartRepo(t)
	ahead := advanceOrigin(t, repo)
	s := openRunnerStore(t)
	createBaseStartEpic(t, s, repo)

	started := runBaseStartTurn(t, s, enginehost.FetchOriginMain)

	requireStartsAt(t, started, ahead)
	requireBaseAt(t, s, ahead)
	requireOneTell(t, s, repo.base, ahead)
}

// TestEpicBaseStartKeepsBaseWhenOriginMainDoesNotContainIt asserts that a planner turn of an epic with no task and no
// branch keeps its base when origin's main does not contain it, and that one tell says origin's main does not contain
// the base.
func TestEpicBaseStartKeepsBaseWhenOriginMainDoesNotContainIt(t *testing.T) {
	t.Parallel()
	repo := newBaseStartRepo(t)
	rewritten := rewriteOrigin(t, repo)
	s := openRunnerStore(t)
	createBaseStartEpic(t, s, repo)

	started := runBaseStartTurn(t, s, enginehost.FetchOriginMain)

	requireStartsAt(t, started, repo.base)
	requireBaseAt(t, s, repo.base)
	requireOneTell(t, s, "origin's main "+rewritten+" does not contain the base "+repo.base)
}

// TestEpicBaseStartKeepsBaseWhenFetchFails asserts that a planner turn of an epic with no task and no branch keeps its
// base when the fetch of origin's main fails, and that one tell says the base stays and names the fetch error.
func TestEpicBaseStartKeepsBaseWhenFetchFails(t *testing.T) {
	t.Parallel()
	repo := newBaseStartRepo(t)
	advanceOrigin(t, repo)
	s := openRunnerStore(t)
	createBaseStartEpic(t, s, repo)
	refused := func(context.Context, string) (string, error) {
		return "", errors.New("origin refused the fetch")
	}

	started := runBaseStartTurn(t, s, refused)

	requireStartsAt(t, started, repo.base)
	requireBaseAt(t, s, repo.base)
	requireOneTell(t, s, "base stays at "+repo.base, "origin refused the fetch")
}

// TestEpicBaseStartKeepsBaseWhenFetchPassesItsBound asserts that a planner turn of an epic with no task and no branch
// keeps its base when the fetch of origin's main passes its time bound, and that one tell says the bound was passed.
// The fetch is handed a bounded context: a fetch that is not bounded fails the tell's text instead, so the tell can
// only say the bound was passed when the runner bounds the fetch.
func TestEpicBaseStartKeepsBaseWhenFetchPassesItsBound(t *testing.T) {
	t.Parallel()
	repo := newBaseStartRepo(t)
	advanceOrigin(t, repo)
	s := openRunnerStore(t)
	createBaseStartEpic(t, s, repo)
	passed := func(ctx context.Context, _ string) (string, error) {
		if _, bounded := ctx.Deadline(); !bounded {
			return "", errors.New("the fetch has no time bound")
		}
		return "", context.DeadlineExceeded
	}

	started := runBaseStartTurn(t, s, passed)

	requireStartsAt(t, started, repo.base)
	requireBaseAt(t, s, repo.base)
	requireOneTell(t, s, "base stays at "+repo.base, "passed its time bound")
}
