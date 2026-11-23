package compose

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// Overlay makes the reproduction commit and pins it at ref, create-only, returning its full id. The commit is base's tree
// with each of paths set to from's entry for it, and removed where from has none, on one commit whose parent is base,
// with from's author, committer, dates and message, so the same inputs give the same commit. Nothing reads or writes the
// repository's index or working tree: the commit is staged in a temporary index outside the repository. Its git runs in
// repo through host, and so does the making and removing of that temporary directory.
func Overlay(
	ctx context.Context, host hostexec.Host, repo, base, from string, paths []string, ref string,
) (commit string, err error) {
	baseCommit, err := resolveCommit(ctx, host, repo, base)
	if err != nil {
		return "", fmt.Errorf("resolve overlay base: %w", err)
	}
	fromCommit, err := resolveCommit(ctx, host, repo, from)
	if err != nil {
		return "", fmt.Errorf("resolve overlay source: %w", err)
	}
	scratch, err := host.MkdirTemp(ctx, "", "orrery-overlay-")
	if err != nil {
		return "", fmt.Errorf("create temporary index directory: %w", err)
	}
	defer func() {
		if removeErr := host.RemoveAll(ctx, scratch); removeErr != nil && err == nil {
			commit, err = "", fmt.Errorf("remove temporary index directory: %w", removeErr)
		}
	}()
	commit, err = composeTask(ctx, host, repo, filepath.Join(scratch, "index"), baseCommit, fromCommit, paths)
	if err != nil {
		return "", fmt.Errorf("make overlay commit: %w", err)
	}
	if pinErr := pinCandidate(ctx, host, repo, ref, commit); pinErr != nil {
		return "", pinErr
	}
	return commit, nil
}
