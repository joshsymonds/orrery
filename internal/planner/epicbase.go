package planner

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// defaultFetchLimit is how long a fetch of origin's main may take before its turn keeps its epic's base, when Config
// names no positive FetchLimit.
const defaultFetchLimit = time.Minute

// notAncestorStatus is the exit status of `git merge-base --is-ancestor` when the first commit is not an ancestor of
// the second.
const notAncestorStatus = 1

// moveEpicBase moves the base of epic to origin's main before the turn claimed for it starts its step. An epic that has
// a task of any status, or a branch, keeps its base. Otherwise origin's main is fetched under the fetch limit: a fetch
// that fails or passes the limit keeps the base and tells the epic why, a main at the base leaves it as it is, and a
// main that does not contain the base keeps the base and tells the epic so. Otherwise the base and stored base move to
// origin's main in one write. A nil OriginMain disables the move. An error here is logged, and the turn goes on.
func (r *Runner) moveEpicBase(ctx context.Context, epic string) {
	if r.originMain == nil {
		return
	}
	row, err := r.store.Epic(ctx, epic)
	if err != nil {
		r.logger.ErrorContext(ctx, "read epic before its base moves", "epic", epic, "err", err)
		return
	}
	if len(row.Tasks) > 0 || row.Branch != "" {
		return
	}
	base := row.Base
	commit, err := r.boundedOriginMain(ctx, row.Repository)
	if err != nil {
		r.tellEpic(ctx, epic, fmt.Sprintf("base stays at %s: %v", base, err))
		return
	}
	if commit == base {
		return
	}
	ancestor, err := isAncestorCommit(ctx, row.Repository, base, commit)
	if err != nil {
		r.logger.ErrorContext(ctx, "compare base of epic with origin's main",
			"epic", epic, "base", base, "commit", commit, "err", err)
		return
	}
	if !ancestor {
		r.tellEpic(ctx, epic, fmt.Sprintf("base stays at %s: origin's main %s does not contain the base %s",
			base, commit, base))
		return
	}
	moved, err := r.store.MoveEpicBase(ctx, epic, base, commit, r.now())
	if err != nil {
		r.logger.ErrorContext(ctx, "move base of epic to origin's main",
			"epic", epic, "base", base, "commit", commit, "err", err)
		return
	}
	if !moved {
		r.logger.InfoContext(ctx, "base of epic did not move: it changed since it was read",
			"epic", epic, "base", base, "commit", commit)
	}
}

// boundedOriginMain returns the commit origin's main names in repository, as OriginMain returns it under a context that
// the fetch limit bounds. A fetch that passes the bound returns an error saying so, whether or not OriginMain returned
// one; any other failure returns an error that names it.
func (r *Runner) boundedOriginMain(ctx context.Context, repository string) (string, error) {
	bound, cancel := context.WithTimeout(ctx, r.fetchLimit)
	defer cancel()
	commit, err := r.originMain(bound, repository)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(bound.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("the fetch of origin's main passed its time bound of %s", r.fetchLimit)
	}
	if err != nil {
		return "", fmt.Errorf("the fetch of origin's main failed: %w", err)
	}
	return commit, nil
}

// isAncestorCommit reports whether base is an ancestor of commit, or commit itself, in repository. A failure of git
// other than its not-an-ancestor status is an error.
func isAncestorCommit(ctx context.Context, repository, base, commit string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repository, "merge-base", "--is-ancestor", base, commit)
	_, err := cmd.Output()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == notAncestorStatus {
		return false, nil
	}
	return false, fmt.Errorf("check that %s is an ancestor of %s: %w", base, commit, err)
}

// tellEpic adds text to epic as a tell. A tell the store refuses is logged.
func (r *Runner) tellEpic(ctx context.Context, epic, text string) {
	if err := r.store.TellEpic(ctx, epic, text, r.now()); err != nil {
		r.logger.ErrorContext(ctx, "tell epic its base stays", "epic", epic, "err", err)
	}
}
