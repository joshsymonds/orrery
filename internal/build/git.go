// Package build runs each planned task of an epic: red at its base, one implementer, green at its commit and the
// epic's review gates.
package build

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// originMainRef is the remote-tracking ref of origin's main branch, which a move of main or of HEAD is judged against.
const originMainRef = "refs/remotes/origin/main"

// notAncestor is the exit status of `git merge-base --is-ancestor` when the first commit is not an ancestor of the
// second.
const notAncestor = 1

// originMain returns the commit origin's main names in repo, and false with no error when repo has no origin main.
func originMain(ctx context.Context, repo string) (string, bool, error) {
	commit, err := runGit(ctx, repo, "rev-parse", "--verify", "--quiet", originMainRef+"^{commit}")
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == revParseMissing {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read origin main: %w", err)
	}
	return commit, true, nil
}

// isAncestor reports whether ancestor is reachable from descendant in repo. A commit is its own ancestor.
func isAncestor(ctx context.Context, repo, ancestor, descendant string) (bool, error) {
	_, err := runGit(ctx, repo, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == notAncestor {
		return false, nil
	}
	return false, fmt.Errorf("compare %s with %s: %w", ancestor, descendant, err)
}

// stepCommits returns the commits of an implementer's step in repo: those reachable from head that base, the commit the
// step started at, does not reach. It returns none when the step made no commit.
func stepCommits(ctx context.Context, repo, base, head string) ([]string, error) {
	if base == "" {
		return nil, errors.New("step has no base")
	}
	listing, err := runGit(ctx, repo, "rev-list", base+".."+head)
	if err != nil {
		return nil, fmt.Errorf("list step commits: %w", err)
	}
	if listing == "" {
		return nil, nil
	}
	return strings.Split(listing, "\n"), nil
}

// runGit runs git in dir and returns its trimmed stdout. On failure the error carries git's stderr. Like stepGit, it
// builds the argument vector itself: git runs with argv and no shell.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git")
	cmd.Args = append(cmd.Args, "-C", dir)
	cmd.Args = append(cmd.Args, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
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
