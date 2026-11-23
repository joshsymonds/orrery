package compose

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// mergeConflict is the exit status merge-tree --write-tree gives a merge that conflicts.
const mergeConflict = 1

// Join returns a commit that holds both base and line and builds on base, and whether there is one: line when base is
// an ancestor of it or line itself, base when line is an ancestor of base, and otherwise the merge of base into line.
// The merge is written without a checkout, with line's author, committer and dates and a message that names both, so
// the same two commits always give the same merge, and it is pinned create-only under prefix followed by its own id.
// A merge that conflicts gives no commit and false. Its git runs in repo through host.
func Join(
	ctx context.Context, host hostexec.Host, repo, base, line, prefix string,
) (string, bool, error) {
	baseCommit, err := resolveCommit(ctx, host, repo, base)
	if err != nil {
		return "", false, fmt.Errorf("resolve join base: %w", err)
	}
	lineCommit, err := resolveCommit(ctx, host, repo, line)
	if err != nil {
		return "", false, fmt.Errorf("resolve join line: %w", err)
	}
	for _, pair := range [][2]string{{baseCommit, lineCommit}, {lineCommit, baseCommit}} {
		inside, ancestorErr := isAncestor(ctx, host, repo, pair[0], pair[1])
		if ancestorErr != nil {
			return "", false, ancestorErr
		}
		if inside {
			return pair[1], true, nil
		}
	}
	tree, _, clean, err := mergedTree(ctx, host, repo, lineCommit, baseCommit)
	if err != nil || !clean {
		return "", false, err
	}
	message := fmt.Appendf(nil, "Merge %s into %s\n", baseCommit, lineCommit)
	merge, err := mergeCommit(ctx, host, repo, tree, lineCommit, baseCommit, lineCommit, message)
	if err != nil {
		return "", false, err
	}
	if pinErr := pinCandidate(ctx, host, repo, prefix+merge, merge); pinErr != nil {
		return "", false, pinErr
	}
	return merge, true, nil
}

// mergedTree writes the tree of the merge of base into line with merge-tree, without a checkout, and returns it with
// true. A merge that conflicts returns no tree, false, and the conflicted paths. The paths are read NUL-separated, so
// a name that git would otherwise quote comes back as it is.
func mergedTree(
	ctx context.Context, host hostexec.Host, repo, line, base string,
) (string, []string, bool, error) {
	args := []string{"merge-tree", "--write-tree", "--no-messages", "--name-only", "-z", line, base}
	out, err := runGit(ctx, host, repo, nil, nil, args...)
	var gitErr *hostexec.GitError
	switch {
	case err == nil:
		tree, _, _ := strings.Cut(string(out), "\x00")
		return tree, nil, true, nil
	case errors.As(err, &gitErr) && gitErr.ExitCode == mergeConflict:
		// The first record is the tree the conflicted merge wrote; the rest name the conflicted paths.
		_, paths, _ := strings.Cut(string(out), "\x00")
		return "", splitNUL([]byte(paths)), false, nil
	default:
		return "", nil, false, fmt.Errorf("merge %s into %s: %w", base, line, err)
	}
}

// mergeCommit writes the commit of tree whose parents are line and then base, with the author, committer and dates of
// from, which is line or base, and message.
func mergeCommit(
	ctx context.Context, host hostexec.Host, repo, tree, line, base, from string, message []byte,
) (string, error) {
	raw, err := runGit(ctx, host, repo, nil, nil, "cat-file", "commit", from)
	if err != nil {
		return "", fmt.Errorf("read merge identity: %w", err)
	}
	identity, _, err := commitIdentity(raw)
	if err != nil {
		return "", err
	}
	commit, err := runGit(
		ctx, host, repo, identity, message, "commit-tree", tree, "-p", line, "-p", base,
	)
	if err != nil {
		return "", fmt.Errorf("create merge commit: %w", err)
	}
	return strings.TrimSpace(string(commit)), nil
}
