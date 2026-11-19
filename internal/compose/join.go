package compose

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// mergeConflict is the exit status merge-tree --write-tree gives a merge that conflicts.
const mergeConflict = 1

// Join returns a commit that holds both base and line and builds on base, and whether there is one: line when base is
// an ancestor of it or line itself, base when line is an ancestor of base, and otherwise the merge of base into line.
// The merge is written without a checkout, with line's author, committer and dates and a message that names both, so
// the same two commits always give the same merge, and it is pinned create-only under prefix followed by its own id.
// A merge that conflicts gives no commit and false.
func Join(ctx context.Context, repo, base, line, prefix string) (string, bool, error) {
	baseCommit, err := resolveCommit(ctx, repo, base)
	if err != nil {
		return "", false, fmt.Errorf("resolve join base: %w", err)
	}
	lineCommit, err := resolveCommit(ctx, repo, line)
	if err != nil {
		return "", false, fmt.Errorf("resolve join line: %w", err)
	}
	for _, pair := range [][2]string{{baseCommit, lineCommit}, {lineCommit, baseCommit}} {
		inside, ancestorErr := isAncestor(ctx, repo, pair[0], pair[1])
		if ancestorErr != nil {
			return "", false, ancestorErr
		}
		if inside {
			return pair[1], true, nil
		}
	}
	tree, clean, err := mergedTree(ctx, repo, lineCommit, baseCommit)
	if err != nil || !clean {
		return "", false, err
	}
	merge, err := mergeCommit(ctx, repo, tree, lineCommit, baseCommit)
	if err != nil {
		return "", false, err
	}
	if pinErr := pinCandidate(ctx, repo, prefix+merge, merge); pinErr != nil {
		return "", false, pinErr
	}
	return merge, true, nil
}

// mergedTree writes the tree of the merge of base into line with merge-tree, without a checkout, and returns it. It
// returns false, and no tree, when the merge conflicts.
func mergedTree(ctx context.Context, repo, line, base string) (string, bool, error) {
	out, err := runGit(ctx, repo, nil, nil, "merge-tree", "--write-tree", "--no-messages", "--name-only", line, base)
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		tree, _, _ := strings.Cut(string(out), "\n")
		return strings.TrimSpace(tree), true, nil
	case errors.As(err, &exitErr) && exitErr.ExitCode() == mergeConflict:
		return "", false, nil
	default:
		return "", false, fmt.Errorf("merge %s into %s: %w", base, line, err)
	}
}

// mergeCommit writes the commit of tree whose parents are line and then base, with line's author, committer and
// dates.
func mergeCommit(ctx context.Context, repo, tree, line, base string) (string, error) {
	raw, err := runGit(ctx, repo, nil, nil, "cat-file", "commit", line)
	if err != nil {
		return "", fmt.Errorf("read join line: %w", err)
	}
	identity, _, err := commitIdentity(raw)
	if err != nil {
		return "", err
	}
	message := fmt.Appendf(nil, "Merge %s into %s\n", base, line)
	commit, err := runGit(ctx, repo, identity, message, "commit-tree", tree, "-p", line, "-p", base)
	if err != nil {
		return "", fmt.Errorf("create merge commit: %w", err)
	}
	return strings.TrimSpace(string(commit)), nil
}
