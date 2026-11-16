package compose

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Overlay makes the reproduction commit and pins it at ref, create-only, returning its full id. The commit is base's tree
// with each of paths set to from's entry for it, and removed where from has none, on one commit whose parent is base,
// with from's author, committer, dates and message, so the same inputs give the same commit. Nothing reads or writes the
// repository's index or working tree: the commit is staged in a temporary index outside the repository.
func Overlay(ctx context.Context, repo, base, from string, paths []string, ref string) (commit string, err error) {
	baseCommit, err := resolveCommit(ctx, repo, base)
	if err != nil {
		return "", fmt.Errorf("resolve overlay base: %w", err)
	}
	fromCommit, err := resolveCommit(ctx, repo, from)
	if err != nil {
		return "", fmt.Errorf("resolve overlay source: %w", err)
	}
	scratch, err := os.MkdirTemp("", "orrery-overlay-")
	if err != nil {
		return "", fmt.Errorf("create temporary index directory: %w", err)
	}
	defer func() {
		if removeErr := os.RemoveAll(scratch); removeErr != nil && err == nil {
			commit, err = "", fmt.Errorf("remove temporary index directory: %w", removeErr)
		}
	}()
	commit, err = composeTask(ctx, repo, filepath.Join(scratch, "index"), baseCommit, fromCommit, paths)
	if err != nil {
		return "", fmt.Errorf("make overlay commit: %w", err)
	}
	if pinErr := pinCandidate(ctx, repo, ref, commit); pinErr != nil {
		return "", pinErr
	}
	return commit, nil
}
