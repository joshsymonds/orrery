package build

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// errGitRefused is the error a Git that refuses every write returns.
var errGitRefused = errors.New("git refused the write")

// refusingGit refuses every write a Runner makes to a repository.
type refusingGit struct{}

func (refusingGit) CreateBranch(context.Context, hostexec.Host, string, string, string) error {
	return errGitRefused
}

func (refusingGit) Pin(context.Context, hostexec.Host, string, string, string) error {
	return errGitRefused
}

func (refusingGit) AddTaskWorktree(context.Context, hostexec.Host, string, string, int64, string, string) error {
	return errGitRefused
}

func (refusingGit) MoveTaskWorktree(context.Context, hostexec.Host, string, string, string, int64, string) error {
	return errGitRefused
}

func (refusingGit) KeepWork(context.Context, hostexec.Host, string, string, string) (string, error) {
	return "", errGitRefused
}

func (refusingGit) RemoveWorktrees(context.Context, hostexec.Host, string, string) error {
	return errGitRefused
}

// recordingGit records each branch create it is asked for, then makes it through the package's functions.
type recordingGit struct {
	packageGit
	writes []string
}

func (g *recordingGit) CreateBranch(ctx context.Context, host hostexec.Host, repo, epic, base string) error {
	g.writes = append(g.writes, "create "+epic+" at "+base)
	return g.packageGit.CreateBranch(ctx, host, repo, epic, base)
}

// gitRunner returns a Runner over the fixture's store and fakes that makes its repository writes through git.
func (f *passFixture) gitRunner(git Git) *Runner {
	return New(Config{
		Store: f.store, Steps: f.steps, Evidence: f.evidence, Getenv: f.getenv, StateDir: f.stateDir, Now: passClock,
		Git: git,
	})
}

// TestCreateRefusedByGitParksEpicAndCreatesNoBranch covers R4: a Git that refuses the branch create parks the epic as a
// failed create does, and the epic's branch is absent from the repository.
func TestCreateRefusedByGitParksEpicAndCreatesNoBranch(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	f.gitRunner(refusingGit{}).pass(f.ctx)
	const reason = "epic/epic exists: git refused the write"
	epic := f.epic()
	if epic.Parked != reason || epic.Branch != branchCreating {
		t.Errorf("epic Parked = %q, Branch = %q, want parked %q with branch creating", epic.Parked, epic.Branch, reason)
	}
	if _, found, err := BranchTip(f.ctx, hostexec.NewLocal(), f.repo, passEpic); err != nil || found {
		t.Errorf("BranchTip found = %v, err = %v, want no branch", found, err)
	}
}

// TestCreateRecordedByGitIsMadeThroughIt covers R4: the branch create of an epic with none goes through the Git the
// Runner was given, once, at the epic's base.
func TestCreateRecordedByGitIsMadeThroughIt(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	git := &recordingGit{}
	f.gitRunner(git).pass(f.ctx)
	if want := []string{"create " + passEpic + " at " + f.base}; !slices.Equal(git.writes, want) {
		t.Errorf("recorded writes = %v, want %v", git.writes, want)
	}
	if got := f.epic().Branch; got != branchCreated {
		t.Errorf("epic Branch = %q, want %q", got, branchCreated)
	}
}
