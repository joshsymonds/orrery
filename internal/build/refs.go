package build

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// mainRef always parks a move, headRef names the move of the primary checkout's HEAD, and refs under refsOrreryPrefix
// belong to the orchestrator and never appear in a snapshot. Neither do refs under taskBranchesPrefix, the task
// branches the implementers commit to: those commits are the implementers' work, not ref moves.
const (
	mainRef            = "refs/heads/main"
	headRef            = "HEAD"
	refsOrreryPrefix   = "refs/orrery/"
	taskBranchesPrefix = "refs/heads/orrery/"
	// symbolicRefDetached is the exit status of `git symbolic-ref -q HEAD` when HEAD is detached.
	symbolicRefDetached = 1
)

// RefMove is one change Compare found between two snapshots. From is "" for a ref that was added and To is "" for
// one that was removed. Parks marks a move that stops the step: a move of refs/heads/main, of the branch the primary
// checkout had checked out before, or of HEAD. originTells clears Parks from the moves of main and HEAD it tells.
type RefMove struct {
	Ref, From, To string
	Parks         bool
}

// snapshotHead is the primary checkout's HEAD: the full name of its branch, empty when detached, and its commit.
type snapshotHead struct {
	Branch string `json:"branch"`
	Commit string `json:"commit"`
}

// snapshot is what Snapshot writes and Compare reads: the primary checkout's HEAD and every ref it kept.
type snapshot struct {
	Head snapshotHead      `json:"head"`
	Refs map[string]string `json:"refs"`
}

// Snapshot returns the JSON of repo's refs and of its primary checkout's HEAD, which is the parent of repo's common
// git directory. It leaves out refs under refs/orrery/, refs under refs/heads/orrery/ (the task branches), and each ref
// named in exclude. It writes nothing to the repository.
func Snapshot(ctx context.Context, repo string, exclude []string) (string, error) {
	common, err := runGit(ctx, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("find primary checkout: %w", err)
	}
	primary := filepath.Dir(common)
	branch, err := runGit(ctx, primary, "symbolic-ref", "-q", "HEAD")
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != symbolicRefDetached {
			return "", fmt.Errorf("read HEAD branch: %w", err)
		}
		branch = ""
	}
	commit, err := runGit(ctx, primary, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", fmt.Errorf("read HEAD commit: %w", err)
	}
	listing, err := runGit(ctx, repo, "for-each-ref", "--format=%(refname) %(objectname)")
	if err != nil {
		return "", fmt.Errorf("list refs: %w", err)
	}
	refs := make(map[string]string)
	for line := range strings.SplitSeq(listing, "\n") {
		if line == "" {
			continue
		}
		name, sha, found := strings.Cut(line, " ")
		if !found {
			return "", fmt.Errorf("unexpected for-each-ref line %q", line)
		}
		if strings.HasPrefix(name, refsOrreryPrefix) || strings.HasPrefix(name, taskBranchesPrefix) ||
			slices.Contains(exclude, name) {
			continue
		}
		refs[name] = sha
	}
	data, err := json.Marshal(snapshot{Head: snapshotHead{Branch: branch, Commit: commit}, Refs: refs})
	if err != nil {
		return "", fmt.Errorf("encode snapshot: %w", err)
	}
	return string(data), nil
}

// Compare returns the moves from the before snapshot to the after snapshot: one for each ref added, removed or
// changed, in ref name order, then one HEAD move when the primary checkout's branch or commit changed. A move parks
// when it moves refs/heads/main, the branch the primary checkout had checked out in before, or HEAD.
func Compare(before, after string) ([]RefMove, error) {
	prev, err := decodeSnapshot(before)
	if err != nil {
		return nil, fmt.Errorf("before: %w", err)
	}
	next, err := decodeSnapshot(after)
	if err != nil {
		return nil, fmt.Errorf("after: %w", err)
	}
	union := maps.Clone(prev.Refs)
	maps.Copy(union, next.Refs)
	var moves []RefMove
	for _, name := range slices.Sorted(maps.Keys(union)) {
		from, to := prev.Refs[name], next.Refs[name]
		if from == to {
			continue
		}
		parks := name == mainRef || (prev.Head.Branch != "" && name == prev.Head.Branch)
		moves = append(moves, RefMove{Ref: name, From: from, To: to, Parks: parks})
	}
	if prev.Head != next.Head {
		from, to := headLabel(prev.Head), headLabel(next.Head)
		moves = append(moves, RefMove{Ref: headRef, From: from, To: to, Parks: true})
	}
	return moves, nil
}

// ScopeMoves returns the moves of those Compare found that a step is blamed for, in their order. A move that parks is
// kept, and so is one of the epic's own refs: its epic branch or a ref under refs/orrery/<epic slug>/. Any other move with
// a new target is kept only when that target holds one of the commits the implementer made between base and head. Those
// commits are read only when a move needs them. A check node makes no commits, so it passes "" for base and head.
func ScopeMoves(ctx context.Context, repo, epic string, moves []RefMove, base, head string) ([]RefMove, error) {
	var steps []string
	if head != "" && slices.ContainsFunc(moves, func(move RefMove) bool { return judgedByCommits(move, epic) }) {
		var err error
		if steps, err = stepCommits(ctx, repo, base, head); err != nil {
			return nil, fmt.Errorf("read implementer commits: %w", err)
		}
	}
	var kept []RefMove
	for _, move := range moves {
		keep := move.Parks || isEpicRef(move.Ref, epic)
		if judgedByCommits(move, epic) {
			holds, err := containsAny(ctx, repo, move.To, steps)
			if err != nil {
				return nil, fmt.Errorf("check ref %s against implementer commits: %w", move.Ref, err)
			}
			keep = holds
		}
		if keep {
			kept = append(kept, move)
		}
	}
	return kept, nil
}

// judgedByCommits reports whether ScopeMoves keeps move by the commits its new target holds: a move that neither parks
// nor is the epic's own, and that has a new target. A move that removes its ref has no target to hold a commit.
func judgedByCommits(move RefMove, epic string) bool {
	return !move.Parks && move.To != "" && !isEpicRef(move.Ref, epic)
}

// isEpicRef reports whether ref is the branch of epic or one of its refs under refs/orrery/<epic slug>/.
func isEpicRef(ref, epic string) bool {
	return ref == BranchRef(epic) || strings.HasPrefix(ref, refsOrreryPrefix+Slug(epic)+"/")
}

// originTells returns moves with each parking move that is a fast-forward onto origin's main unparked. Such a move is a
// move of refs/heads/main, or of HEAD within one branch, to a commit that origin reaches and that contains none of the
// step's commits. Every other move keeps its Parks.
func originTells(ctx context.Context, repo string, moves []RefMove, origin string, steps []string) ([]RefMove, error) {
	told := slices.Clone(moves)
	for i, move := range told {
		commit, ok := originTarget(move)
		if !move.Parks || !ok {
			continue
		}
		onOrigin, err := isAncestor(ctx, repo, commit, origin)
		if err != nil {
			return nil, err
		}
		if !onOrigin {
			continue
		}
		contains, err := containsAny(ctx, repo, commit, steps)
		if err != nil {
			return nil, err
		}
		if !contains {
			told[i].Parks = false
		}
	}
	return told, nil
}

// originTarget returns the commit a move of refs/heads/main or of HEAD moves to, and false for any other move. A move
// that creates or removes main moves to no commit, and a HEAD move that switches branch, or detaches, is not within one
// branch.
func originTarget(move RefMove) (string, bool) {
	switch move.Ref {
	case mainRef:
		return move.To, move.From != "" && move.To != ""
	case headRef:
		fromBranch, _, _ := strings.Cut(move.From, " ")
		toBranch, commit, _ := strings.Cut(move.To, " ")
		return commit, fromBranch == toBranch && commit != ""
	}
	return "", false
}

// containsAny reports whether commit contains any of commits in repo, that is, whether one of them is reachable from it.
func containsAny(ctx context.Context, repo, commit string, commits []string) (bool, error) {
	for _, c := range commits {
		ok, err := isAncestor(ctx, repo, c, commit)
		if err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

// decodeSnapshot reads a snapshot that Snapshot wrote.
func decodeSnapshot(data string) (snapshot, error) {
	var snap snapshot
	if err := json.Unmarshal([]byte(data), &snap); err != nil {
		return snapshot{}, fmt.Errorf("decode snapshot: %w", err)
	}
	return snap, nil
}

// headLabel is a HEAD's branch and commit, as a HEAD move reports them.
func headLabel(head snapshotHead) string {
	return head.Branch + " " + head.Commit
}
