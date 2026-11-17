package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// moveWip is the ref the move tests keep a worktree's uncommitted work in.
const moveWip = "refs/orrery/epic/wip/T1"

// moveWorld is a repository with a committer identity, a base holding thing.go, an implement worktree at the base, and
// a new base on it that adds theirs.go.
type moveWorld struct {
	t                    *testing.T
	repo, path, base, to string
}

// newMoveWorld returns a move world.
func newMoveWorld(t *testing.T) moveWorld {
	t.Helper()
	setup := newStartSetup(t)
	repo := setup.epic.Repository
	identify(t, repo)
	base := commitOff(t, repo, setup.base, "thing.go", "package thing\n")
	path := filepath.Join(t.TempDir(), "implement")
	if err := AddWorktree(context.Background(), repo, path, base); err != nil {
		t.Fatal(err)
	}
	return moveWorld{
		t:    t,
		repo: repo,
		path: path,
		base: base,
		to:   commitOff(t, repo, base, "theirs.go", "package theirs\n"),
	}
}

// work leaves a commit of mine.go, then an uncommitted change to thing.go, a staged new file and an untracked one.
func (w moveWorld) work() {
	w.t.Helper()
	writeFile(w.t, filepath.Join(w.path, "mine.go"), "package mine\n")
	buildRun(w.t, w.path, "add", "mine.go")
	buildRun(w.t, w.path, "commit", "-q", "-m", "mine")
	writeFile(w.t, filepath.Join(w.path, "thing.go"), "package thing // changed\n")
	writeFile(w.t, filepath.Join(w.path, "staged.go"), "package staged\n")
	buildRun(w.t, w.path, "add", "staged.go")
	writeFile(w.t, filepath.Join(w.path, "draft.go"), "package draft\n")
}

// move moves the worktree onto to and fails the test on an error.
func (w moveWorld) move(to string) {
	w.t.Helper()
	if err := MoveWorktree(context.Background(), w.path, moveWip, to); err != nil {
		w.t.Fatalf("MoveWorktree: %v", err)
	}
}

// requireMoved fails the test unless the worktree holds work's commit once on onto, with the uncommitted work kept as
// uncommitted work, the move's ref gone and nothing on the stash.
func (w moveWorld) requireMoved(onto string) {
	w.t.Helper()
	if parent := buildRun(w.t, w.path, "rev-parse", "HEAD^"); parent != onto {
		w.t.Errorf("HEAD^ %s, want the commit replayed once on %s", parent, onto)
	}
	if subject := buildRun(w.t, w.path, "log", "-1", "--format=%s"); subject != "mine" {
		w.t.Errorf("HEAD subject %q, want mine", subject)
	}
	for name, want := range map[string]string{
		"thing.go": "package thing // changed\n", "staged.go": "package staged\n", "draft.go": "package draft\n",
		"mine.go": "package mine\n", "theirs.go": "package theirs\n",
	} {
		if data, err := os.ReadFile(filepath.Join(w.path, name)); err != nil || string(data) != want {
			w.t.Errorf("%s = %q, %v, want %q", name, data, err, want)
		}
	}
	status := buildRun(w.t, w.path, "status", "--porcelain")
	for _, want := range []string{"M thing.go", "?? draft.go", "?? staged.go"} {
		if !strings.Contains(status, want) {
			w.t.Errorf("status %q lacks %q: the work is not uncommitted", status, want)
		}
	}
	if got := buildRun(w.t, w.repo, "for-each-ref", moveWip); got != "" {
		w.t.Errorf("%s = %q after the move, want it deleted", moveWip, got)
	}
	if got := buildRun(w.t, w.repo, "stash", "list"); got != "" {
		w.t.Errorf("stash list = %q, want the move to leave the stash alone", got)
	}
}

func TestMoveWorktreeReplaysCommitsAndKeepsUncommittedWork(t *testing.T) {
	t.Parallel()
	w := newMoveWorld(t)
	w.work()
	w.move(w.to)
	w.requireMoved(w.to)
	w.move(w.to)
	w.requireMoved(w.to)
}

func TestMoveWorktreeOfUncommittedWorkOnlyLandsOnTheNewBase(t *testing.T) {
	t.Parallel()
	w := newMoveWorld(t)
	writeFile(t, filepath.Join(w.path, "thing.go"), "package thing // changed\n")
	writeFile(t, filepath.Join(w.path, "draft.go"), "package draft\n")
	w.move(w.to)
	if head := buildHead(t, w.path); head != w.to {
		t.Errorf("HEAD %s, want %s", head, w.to)
	}
	if status := buildRun(t, w.path, "status", "--porcelain"); status != "M thing.go\n?? draft.go" {
		t.Errorf("status %q, want thing.go changed and draft.go untracked", status)
	}
}

// TestMoveWorktreeFinishesAMoveCutAtEachStep: a move cut after each of its steps, and one cut in the middle of its
// replay, is finished by the next MoveWorktree, with the work as a whole move leaves it.
func TestMoveWorktreeFinishesAMoveCutAtEachStep(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cuts := map[string]func(w moveWorld){
		"after the snapshot": func(w moveWorld) {
			if _, err := snapshotWorktree(ctx, w.path, moveWip); err != nil {
				t.Fatal(err)
			}
		},
		"after entering the snapshot": func(w moveWorld) {
			snapshot, err := snapshotWorktree(ctx, w.path, moveWip)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = runGit(ctx, w.path, "update-ref", "--no-deref", "HEAD", snapshot); err != nil {
				t.Fatal(err)
			}
		},
		"in the middle of the replay": func(w moveWorld) {
			snapshot, err := snapshotWorktree(ctx, w.path, moveWip)
			if err != nil {
				t.Fatal(err)
			}
			if err = enterSnapshot(ctx, w.path, snapshot); err != nil {
				t.Fatal(err)
			}
			if _, err = runGit(ctx, w.path, "rebase", "--empty=keep", "--exec", "false", "--onto", w.to,
				w.base); err == nil {
				t.Fatal("the stopped rebase ran to its end")
			}
		},
		"after the replay": func(w moveWorld) {
			snapshot, err := snapshotWorktree(ctx, w.path, moveWip)
			if err != nil {
				t.Fatal(err)
			}
			if err = enterSnapshot(ctx, w.path, snapshot); err != nil {
				t.Fatal(err)
			}
			if err = replaySnapshot(ctx, w.path, snapshot, w.to); err != nil {
				t.Fatal(err)
			}
		},
		"after the unwrap": func(w moveWorld) {
			snapshot, err := snapshotWorktree(ctx, w.path, moveWip)
			if err != nil {
				t.Fatal(err)
			}
			if err = enterSnapshot(ctx, w.path, snapshot); err != nil {
				t.Fatal(err)
			}
			if err = replaySnapshot(ctx, w.path, snapshot, w.to); err != nil {
				t.Fatal(err)
			}
			if _, err = runGit(ctx, w.path, "reset", "-q", "HEAD^"); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, cut := range cuts {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newMoveWorld(t)
			w.t = t
			w.work()
			cut(w)
			w.move(w.to)
			w.requireMoved(w.to)
		})
	}
}

// TestMoveWorktreeAfterAnUnstoredMoveReplaysOnlyItsOwnCommits: a move that finished onto one base, whose build was never
// stored, then moves onto a later base, replays the worktree's own commit once, not the commits of the earlier base.
func TestMoveWorktreeAfterAnUnstoredMoveReplaysOnlyItsOwnCommits(t *testing.T) {
	t.Parallel()
	w := newMoveWorld(t)
	w.work()
	w.move(w.to)
	later := commitOff(t, w.repo, w.to, "later.go", "package later\n")
	w.move(later)
	w.requireMoved(later)
	if count := buildRun(t, w.path, "rev-list", "--count", later+"..HEAD"); count != "1" {
		t.Errorf("commits on the later base = %s, want the worktree's one", count)
	}
}

func TestMoveWorktreeRefusesAConflictingReplayAndKeepsTheWork(t *testing.T) {
	t.Parallel()
	w := newMoveWorld(t)
	writeFile(t, filepath.Join(w.path, "theirs.go"), "package mine\n")
	buildRun(t, w.path, "add", "theirs.go")
	buildRun(t, w.path, "commit", "-q", "-m", "mine")
	writeFile(t, filepath.Join(w.path, "draft.go"), "package draft\n")
	err := MoveWorktree(context.Background(), w.path, moveWip, w.to)
	if err == nil || !strings.Contains(err.Error(), "move commits") {
		t.Fatalf("MoveWorktree = %v, want the refused replay", err)
	}
	if data, readErr := os.ReadFile(
		filepath.Join(w.path, "theirs.go"),
	); readErr != nil ||
		string(data) != "package mine\n" {
		t.Errorf("theirs.go = %q, %v, want the worktree's own version after the abort", data, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(w.path, "draft.go")); statErr != nil {
		t.Errorf("draft.go after the refusal: %v, want it kept", statErr)
	}
	if got := buildRun(t, w.repo, "for-each-ref", "--format=%(objectname)", moveWip); got == "" {
		t.Error("the move's ref is gone after a refused replay, want it kept as the snapshot of the work")
	}
}

func TestMoveWorktreeOfAMissingPathMovesNothing(t *testing.T) {
	t.Parallel()
	if err := MoveWorktree(context.Background(), filepath.Join(t.TempDir(), "gone"), moveWip, "b"); err != nil {
		t.Errorf("MoveWorktree of a missing path = %v, want nil", err)
	}
}
