// Package compose composes a batch of accepted task commits into one candidate commit: the batch base with each task's
// revision merged in, pinned create-only under refs/orrery/, and an epic branch moved only from the commit it is
// expected at.
package compose

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// pinPrefix is the namespace a candidate is pinned under, and branchPrefix the namespace Advance may move.
const (
	pinPrefix    = "refs/orrery/"
	branchPrefix = "refs/heads/epic/"
)

// Task is one accepted task of a batch: the commit it was built on and the commit that holds its work.
type Task struct {
	Seq      int64
	Base     string
	Revision string
}

// Composed is a task that became a commit of the candidate.
type Composed struct {
	Seq    int64
	Commit string
}

// LeftOut is a task that did not compose, and why. Conflicts holds the paths a task's merge conflicted at, and is empty
// for a task left out for any other reason.
type LeftOut struct {
	Seq       int64
	Reason    string
	Conflicts []string
}

// Result is what Compose built. Candidate is the last composed commit, or Base when no task composed.
type Result struct {
	Base      string
	Candidate string
	Composed  []Composed
	LeftOut   []LeftOut
}

// Compose composes tasks, in order, on base, and pins the candidate at ref when any task composed. Nothing reads or
// writes the repository's index or working tree: merges are written with merge-tree and commit-tree, without a
// checkout. Its git runs in repo through host.
func Compose(
	ctx context.Context, host hostexec.Host, repo, base string, tasks []Task, ref string,
) (Result, error) {
	batchBase, err := resolveCommit(ctx, host, repo, base)
	if err != nil {
		return Result{}, fmt.Errorf("resolve batch base: %w", err)
	}
	result, err := composeTasks(ctx, host, repo, batchBase, tasks)
	if err != nil {
		return Result{}, err
	}
	if len(result.Composed) > 0 {
		if pinErr := pinCandidate(ctx, host, repo, ref, result.Candidate); pinErr != nil {
			return Result{}, pinErr
		}
	}
	return result, nil
}

// composeTasks walks tasks in order, each merged into the candidate so far, which starts at batchBase. A task built on
// a commit that is neither batchBase nor an ancestor of it is left out. A task whose revision the candidate already
// holds composes as the candidate itself. Every other task is merged in with a merge commit whose first parent is the
// candidate, and a task whose merge conflicts is left out whole, with the conflicted paths.
func composeTasks(
	ctx context.Context, host hostexec.Host, repo, batchBase string, tasks []Task,
) (Result, error) {
	result := Result{
		Base:      batchBase,
		Candidate: batchBase,
		Composed:  make([]Composed, 0, len(tasks)),
		LeftOut:   make([]LeftOut, 0, len(tasks)),
	}
	for _, task := range tasks {
		builds, err := buildsOn(ctx, host, repo, batchBase, task.Base)
		if err != nil {
			return Result{}, fmt.Errorf("base of T%d: %w", task.Seq, err)
		}
		if !builds {
			result.LeftOut = append(result.LeftOut, LeftOut{
				Seq:    task.Seq,
				Reason: fmt.Sprintf("T%d was built on %s, not on the batch base %s", task.Seq, task.Base, batchBase),
			})
			continue
		}
		revision, err := resolveCommit(ctx, host, repo, task.Revision)
		if err != nil {
			return Result{}, fmt.Errorf("resolve T%d revision: %w", task.Seq, err)
		}
		held, err := isAncestor(ctx, host, repo, revision, result.Candidate)
		if err != nil {
			return Result{}, fmt.Errorf("check whether the candidate holds T%d: %w", task.Seq, err)
		}
		if held {
			result.Composed = append(result.Composed, Composed{Seq: task.Seq, Commit: result.Candidate})
			continue
		}
		tree, conflicts, clean, err := mergedTree(ctx, host, repo, result.Candidate, revision)
		if err != nil {
			return Result{}, fmt.Errorf("merge T%d: %w", task.Seq, err)
		}
		if !clean {
			reason := fmt.Sprintf("T%d conflicts with the candidate at %s", task.Seq, strings.Join(conflicts, ", "))
			result.LeftOut = append(result.LeftOut, LeftOut{Seq: task.Seq, Reason: reason, Conflicts: conflicts})
			continue
		}
		message := fmt.Appendf(nil, "Merge T%d %s\n", task.Seq, revision)
		commit, err := mergeCommit(ctx, host, repo, tree, result.Candidate, revision, revision, message)
		if err != nil {
			return Result{}, fmt.Errorf("compose T%d: %w", task.Seq, err)
		}
		result.Composed = append(result.Composed, Composed{Seq: task.Seq, Commit: commit})
		result.Candidate = commit
	}
	return result, nil
}

// buildsOn reports whether a task built on base can compose on batchBase: base names batchBase itself, or a commit that
// is an ancestor of batchBase. A base that names no commit cannot.
func buildsOn(ctx context.Context, host hostexec.Host, repo, batchBase, base string) (bool, error) {
	if base == batchBase {
		return true, nil
	}
	commit, ok := commitOf(ctx, host, repo, base)
	if !ok {
		return false, nil
	}
	return isAncestor(ctx, host, repo, commit, batchBase)
}

// isAncestor reports whether ancestor is an ancestor of commit, or commit itself. It returns an error when git fails
// for another reason than the answer no.
func isAncestor(ctx context.Context, host hostexec.Host, repo, ancestor, commit string) (bool, error) {
	_, err := runGit(ctx, host, repo, nil, nil, "merge-base", "--is-ancestor", ancestor, commit)
	var gitErr *hostexec.GitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &gitErr) && gitErr.ExitCode == 1:
		return false, nil
	default:
		return false, fmt.Errorf("check that %s is an ancestor of %s: %w", ancestor, commit, err)
	}
}

// composeTask writes one commit on parent: the revision's changed paths staged over parent's tree in the temporary
// index, with the revision's author, committer and message.
func composeTask(
	ctx context.Context, host hostexec.Host, repo, index, parent, revision string, paths []string,
) (string, error) {
	raw, err := runGit(ctx, host, repo, nil, nil, "cat-file", "commit", revision)
	if err != nil {
		return "", fmt.Errorf("read revision: %w", err)
	}
	identity, message, err := commitIdentity(raw)
	if err != nil {
		return "", err
	}
	indexEnv := []string{"GIT_INDEX_FILE=" + index}
	if _, err = runGit(ctx, host, repo, indexEnv, nil, "read-tree", parent); err != nil {
		return "", fmt.Errorf("read parent tree: %w", err)
	}
	if stageErr := stagePaths(ctx, host, repo, indexEnv, revision, paths); stageErr != nil {
		return "", stageErr
	}
	tree, err := runGit(ctx, host, repo, indexEnv, nil, "write-tree")
	if err != nil {
		return "", fmt.Errorf("write composed tree: %w", err)
	}
	commit, err := runGit(
		ctx, host, repo, identity, message, "commit-tree", strings.TrimSpace(string(tree)), "-p", parent,
	)
	if err != nil {
		return "", fmt.Errorf("create commit: %w", err)
	}
	return strings.TrimSpace(string(commit)), nil
}

// stagePaths sets each of paths in the index to the revision's entry for it, and removes a path the revision lacks.
// Removals run first, so a file and a directory of the same name can trade places.
func stagePaths(
	ctx context.Context, host hostexec.Host, repo string, env []string, revision string, paths []string,
) error {
	if len(paths) == 0 {
		return nil
	}
	// The paths are literal: a path such as ":foo" must not be read as pathspec magic, or its entry would be missed and
	// the path removed.
	args := append([]string{"ls-tree", "-r", "-z", "--full-tree", revision, "--"}, paths...)
	listing, err := runGit(ctx, host, repo, []string{"GIT_LITERAL_PATHSPECS=1"}, nil, args...)
	if err != nil {
		return fmt.Errorf("read revision entries: %w", err)
	}
	// Each record is "<mode> <type> <object>\t<path>", the form update-index --index-info reads.
	entries := make(map[string]string)
	for _, record := range splitNUL(listing) {
		_, name, found := strings.Cut(record, "\t")
		if !found {
			return fmt.Errorf("invalid ls-tree record %q", record)
		}
		entries[name] = record
	}
	var removals, updates bytes.Buffer
	for _, path := range paths {
		entry, ok := entries[path]
		if !ok {
			fmt.Fprintf(&removals, "%s\x00", path)
			continue
		}
		fmt.Fprintf(&updates, "%s\x00", entry)
	}
	if removals.Len() > 0 {
		removeArgs := []string{"update-index", "-z", "--force-remove", "--stdin"}
		if _, err = runGit(ctx, host, repo, env, removals.Bytes(), removeArgs...); err != nil {
			return fmt.Errorf("remove paths from temporary index: %w", err)
		}
	}
	if updates.Len() > 0 {
		if _, err = runGit(
			ctx, host, repo, env, updates.Bytes(), "update-index", "-z", "--index-info",
		); err != nil {
			return fmt.Errorf("stage paths in temporary index: %w", err)
		}
	}
	return nil
}

// commitIdentity reads a commit object. It returns the environment that makes commit-tree write the same author and
// committer, names, emails and dates, and the message that follows the commit's headers.
func commitIdentity(raw []byte) ([]string, []byte, error) {
	headers, message, found := bytes.Cut(raw, []byte("\n\n"))
	if !found {
		return nil, nil, errors.New("commit object has no message")
	}
	var env []string
	for _, role := range []string{"AUTHOR", "COMMITTER"} {
		value, err := commitHeader(string(headers), strings.ToLower(role))
		if err != nil {
			return nil, nil, err
		}
		fields, err := identityEnv(role, value)
		if err != nil {
			return nil, nil, err
		}
		env = append(env, fields...)
	}
	return env, message, nil
}

// commitHeader returns the value of the first header named name in headers.
func commitHeader(headers, name string) (string, error) {
	for line := range strings.SplitSeq(headers, "\n") {
		if value, ok := strings.CutPrefix(line, name+" "); ok {
			return value, nil
		}
	}
	return "", fmt.Errorf("commit has no %s header", name)
}

// identityEnv turns a header value, "Name <email> <raw date>", into the GIT_<role>_* variables that name it.
func identityEnv(role, value string) ([]string, error) {
	open := strings.IndexByte(value, '<')
	closing := strings.IndexByte(value, '>')
	if open < 0 || closing < open {
		return nil, fmt.Errorf("%s identity %q has no email", role, value)
	}
	return []string{
		"GIT_" + role + "_NAME=" + strings.TrimSpace(value[:open]),
		"GIT_" + role + "_EMAIL=" + value[open+1:closing],
		"GIT_" + role + "_DATE=" + strings.TrimSpace(value[closing+1:]),
	}, nil
}

// pinCandidate points ref at commit, create-only. A ref that already points at commit passes, and one that points at
// another commit is an error.
func pinCandidate(ctx context.Context, host hostexec.Host, repo, ref, commit string) error {
	if !strings.HasPrefix(ref, pinPrefix) {
		return fmt.Errorf("candidate ref %q is outside %s", ref, pinPrefix)
	}
	_, createErr := runGit(ctx, host, repo, nil, nil, "update-ref", ref, commit, "")
	if createErr == nil {
		return nil
	}
	existing, err := resolveCommit(ctx, host, repo, ref)
	if err != nil {
		return fmt.Errorf("pin candidate ref: %w", createErr)
	}
	if existing != commit {
		return fmt.Errorf("candidate ref %q points to %q, want %q", ref, existing, commit)
	}
	return nil
}

// Advance moves branchRef to to, and only when the branch is at from and to builds on from, so the move is a
// fast-forward that drops nothing the branch holds. The empty from means the branch must not exist. Only a branch under
// refs/heads/epic/ can move. Its git runs in repo through host.
func Advance(ctx context.Context, host hostexec.Host, repo, branchRef, to, from string) error {
	if !strings.HasPrefix(branchRef, branchPrefix) {
		return fmt.Errorf("branch %q is outside %s", branchRef, branchPrefix)
	}
	target, err := resolveCommit(ctx, host, repo, to)
	if err != nil {
		return fmt.Errorf("resolve advance target: %w", err)
	}
	expected := ""
	if from != "" {
		if expected, err = resolveCommit(ctx, host, repo, from); err != nil {
			return fmt.Errorf("resolve advance expected commit: %w", err)
		}
		if _, err = runGit(
			ctx, host, repo, nil, nil, "merge-base", "--is-ancestor", expected, target,
		); err != nil {
			return fmt.Errorf("advance branch: %s does not build on %s, so the move is not a fast-forward: %w",
				target, expected, err)
		}
	}
	if _, err = runGit(ctx, host, repo, nil, nil, "update-ref", branchRef, target, expected); err != nil {
		return fmt.Errorf("advance branch: %w", err)
	}
	return nil
}

// commitOf returns the full id of the commit that value names, and false when value names none.
func commitOf(ctx context.Context, host hostexec.Host, repo, value string) (string, bool) {
	commit, err := resolveCommit(ctx, host, repo, value)
	return commit, err == nil
}

// resolveCommit returns the full id of the commit that value names.
func resolveCommit(ctx context.Context, host hostexec.Host, repo, value string) (string, error) {
	out, err := runGit(ctx, host, repo, nil, nil, "rev-parse", "--verify", "--end-of-options", value+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// splitNUL returns the NUL-terminated records of out, without the empty ones.
func splitNUL(out []byte) []string {
	return strings.FieldsFunc(string(out), func(r rune) bool { return r == '\x00' })
}

// runGit runs git in repo through host with argv and no shell. env is added to its environment and stdin is fed to it.
// It returns git's stdout, which on failure is what git wrote before it failed, such as the conflicted paths merge-tree
// names when it exits 1, and an error that carries git's stderr when git failed. A git that exits non-zero fails with a
// *hostexec.GitError.
func runGit(
	ctx context.Context, host hostexec.Host, repo string, env []string, stdin []byte, args ...string,
) ([]byte, error) {
	out, err := host.Git(ctx, hostexec.GitCall{Dir: repo, Args: args, Env: env, Stdin: stdin})
	if err != nil {
		return out, fmt.Errorf("run git in %s: %w", repo, err)
	}
	return out, nil
}
