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

// mainRef is main, whose move parks when the step made it, headRef names the move of the primary checkout's HEAD, and
// refs under refsOrreryPrefix belong to the orchestrator and never appear in a snapshot. Neither do refs under
// taskBranchesPrefix, the task branches the implementers commit to: those commits are the implementers' work, not ref
// moves.
const (
	mainRef            = "refs/heads/main"
	headRef            = "HEAD"
	refsOrreryPrefix   = "refs/orrery/"
	taskBranchesPrefix = "refs/heads/orrery/"
	// symbolicRefDetached is the exit status of `git symbolic-ref -q HEAD` when HEAD is detached.
	symbolicRefDetached = 1
)

// RefMove is one change Compare found between two snapshots. From is "" for a ref that was added and To is "" for
// one that was removed. Parks marks a move that stops the step when the step made it (see ScopeMoves): a move of
// refs/heads/main, of the branch the primary checkout had checked out before, or of HEAD.
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
// changed, in ref name order, then one HEAD move when the primary checkout's branch or commit changed. A move of
// refs/heads/main, of the branch the primary checkout had checked out before, or of HEAD is marked Parks; whether it
// parks is ScopeMoves' to decide, by who made it.
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

// ScopeMoves returns the moves of those Compare found that the step made, in their order. Another worktree or the
// primary checkout moving a ref is no concern of the epic's, so a move is kept only when it is the step's: a move of one
// of the epic's own refs, its epic branch or a ref under refs/orrery/<epic slug>/, or a move whose new target holds one
// of the commits the implementer made between base and head. That holds for main, for the primary checkout's branch and
// for HEAD as for any other ref; a kept move of one of those keeps its Parks. Every other move is dropped, and so is a
// move that removes its ref, which has no target to hold a commit. Those commits are read only when a move needs them.
// A check node makes no commits, so it passes "" for base and head, and only the epic's own refs are kept for it.
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
		keep := isEpicRef(move.Ref, epic)
		if judgedByCommits(move, epic) {
			holds, err := containsAny(ctx, repo, moveTarget(move), steps)
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

// judgedByCommits reports whether ScopeMoves keeps move by the commits its new target holds: a move that is not the
// epic's own and that has a new target.
func judgedByCommits(move RefMove, epic string) bool {
	return moveTarget(move) != "" && !isEpicRef(move.Ref, epic)
}

// moveTarget returns the commit a move moves its ref to: the primary checkout's commit after a move of HEAD, and the new
// target of any other ref. It is "" for a ref the move removes.
func moveTarget(move RefMove) string {
	if move.Ref == headRef {
		_, commit, _ := strings.Cut(move.To, " ")
		return commit
	}
	return move.To
}

// isEpicRef reports whether ref is the branch of epic or one of its refs under refs/orrery/<epic slug>/.
func isEpicRef(ref, epic string) bool {
	return ref == BranchRef(epic) || strings.HasPrefix(ref, refsOrreryPrefix+Slug(epic)+"/")
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
