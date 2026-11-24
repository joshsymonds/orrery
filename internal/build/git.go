// Package build runs each planned task of an epic: red at its base, one implementer, green at its commit and the
// epic's review gates.
package build

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// notAncestor is the exit status of `git merge-base --is-ancestor` when the first commit is not an ancestor of the
// second.
const notAncestor = 1

// isAncestor reports whether ancestor is reachable from descendant in repo on host. A commit is its own ancestor.
func isAncestor(ctx context.Context, host hostexec.Host, repo, ancestor, descendant string) (bool, error) {
	_, err := runGit(ctx, host, repo, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	var gitErr *hostexec.GitError
	if errors.As(err, &gitErr) && gitErr.ExitCode == notAncestor {
		return false, nil
	}
	return false, fmt.Errorf("compare %s with %s: %w", ancestor, descendant, err)
}

// stepCommits returns the commits of an implementer's step in repo on host: those reachable from head that base, the
// commit the step started at, does not reach. It returns none when the step made no commit.
func stepCommits(ctx context.Context, host hostexec.Host, repo, base, head string) ([]string, error) {
	if base == "" {
		return nil, errors.New("step has no base")
	}
	listing, err := runGit(ctx, host, repo, "rev-list", base+".."+head)
	if err != nil {
		return nil, fmt.Errorf("list step commits: %w", err)
	}
	if listing == "" {
		return nil, nil
	}
	return strings.Split(listing, "\n"), nil
}

// runGit runs git in dir on host and returns its trimmed stdout. A git that ran and failed returns a
// *hostexec.GitError, whose text carries git's stderr. Git runs with argv and no shell.
func runGit(ctx context.Context, host hostexec.Host, dir string, args ...string) (string, error) {
	return runGitEnv(ctx, host, dir, nil, args...)
}

// runGitEnv runs git in dir on host as runGit does, with env added to the environment it inherits.
func runGitEnv(ctx context.Context, host hostexec.Host, dir string, env []string, args ...string) (string, error) {
	out, err := host.Git(ctx, hostexec.GitCall{Dir: dir, Args: args, Env: env})
	if err != nil {
		return "", fmt.Errorf("run git in %s: %w", dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Slug returns the part of an epic id after its "/".
func Slug(epic string) string {
	_, slug, _ := strings.Cut(epic, "/")
	return slug
}

// BranchRef returns the full name of the branch an epic's work is committed to.
func BranchRef(epic string) string {
	return "refs/heads/epic/" + Slug(epic)
}
