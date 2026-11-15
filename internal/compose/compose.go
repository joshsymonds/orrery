// Package compose composes a batch of accepted task commits into one candidate commit (O3d4 E4): one commit per task
// on the batch base, pinned create-only under refs/orrery/, and an epic branch moved only from the commit it is
// expected at.
package compose

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// LeftOut is a task that did not compose, and why.
type LeftOut struct {
	Seq    int64
	Reason string
}

// Result is what Compose built. Candidate is the last composed commit, or Base when no task composed.
type Result struct {
	Base      string
	Candidate string
	Composed  []Composed
	LeftOut   []LeftOut
}

// owner is a path that an earlier composed task of the batch changed.
type owner struct {
	seq  int64
	path string
}

// Compose composes tasks, in order, on base, and pins the candidate at ref when any task composed. Nothing reads or
// writes the repository's index or working tree: each commit is staged in a temporary index outside the repository.
func Compose(ctx context.Context, repo, base string, tasks []Task, ref string) (result Result, err error) {
	batchBase, err := resolveCommit(ctx, repo, base)
	if err != nil {
		return Result{}, fmt.Errorf("resolve batch base: %w", err)
	}
	scratch, err := os.MkdirTemp("", "orrery-compose-")
	if err != nil {
		return Result{}, fmt.Errorf("create temporary index directory: %w", err)
	}
	defer func() {
		if removeErr := os.RemoveAll(scratch); removeErr != nil && err == nil {
			result, err = Result{}, fmt.Errorf("remove temporary index directory: %w", removeErr)
		}
	}()
	result, err = composeTasks(ctx, repo, batchBase, filepath.Join(scratch, "index"), tasks)
	if err != nil {
		return Result{}, err
	}
	if len(result.Composed) > 0 {
		if pinErr := pinCandidate(ctx, repo, ref, result.Candidate); pinErr != nil {
			return Result{}, pinErr
		}
	}
	return result, nil
}

// composeTasks walks tasks in order. A task built on another base is left out, as is one whose changed paths overlap a
// path an earlier composed task changed. Every other task becomes a commit on the candidate so far.
func composeTasks(ctx context.Context, repo, batchBase, index string, tasks []Task) (Result, error) {
	result := Result{
		Base:      batchBase,
		Candidate: batchBase,
		Composed:  make([]Composed, 0, len(tasks)),
		LeftOut:   make([]LeftOut, 0, len(tasks)),
	}
	owners := make([]owner, 0)
	for _, task := range tasks {
		if task.Base != batchBase {
			result.LeftOut = append(result.LeftOut, LeftOut{
				Seq:    task.Seq,
				Reason: fmt.Sprintf("T%d was built on %s, not on the batch base %s", task.Seq, task.Base, batchBase),
			})
			continue
		}
		revision, err := resolveCommit(ctx, repo, task.Revision)
		if err != nil {
			return Result{}, fmt.Errorf("resolve T%d revision: %w", task.Seq, err)
		}
		paths, err := changedPaths(ctx, repo, batchBase, revision)
		if err != nil {
			return Result{}, fmt.Errorf("changed paths of T%d: %w", task.Seq, err)
		}
		if reason, overlapped := overlapReason(task.Seq, paths, owners); overlapped {
			result.LeftOut = append(result.LeftOut, LeftOut{Seq: task.Seq, Reason: reason})
			continue
		}
		commit, err := composeTask(ctx, repo, index, result.Candidate, revision, paths)
		if err != nil {
			return Result{}, fmt.Errorf("compose T%d: %w", task.Seq, err)
		}
		result.Composed = append(result.Composed, Composed{Seq: task.Seq, Commit: commit})
		result.Candidate = commit
		for _, path := range paths {
			owners = append(owners, owner{seq: task.Seq, path: path})
		}
	}
	return result, nil
}

// changedPaths returns the paths that differ between from and to.
func changedPaths(ctx context.Context, repo, from, to string) ([]string, error) {
	out, err := runGit(ctx, repo, nil, nil, "diff", "--name-only", "-z", "--no-renames", from, to)
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// overlapReason returns the reason task seq is left out when one of its paths is, or lies under, or holds, a path an
// earlier composed task changed. The path reported is the task's own.
func overlapReason(seq int64, paths []string, owners []owner) (string, bool) {
	for _, path := range paths {
		for _, earlier := range owners {
			if pathsOverlap(path, earlier.path) {
				return fmt.Sprintf("T%d overlaps T%d at %s", seq, earlier.seq, path), true
			}
		}
	}
	return "", false
}

// pathsOverlap reports whether two repository paths are equal, or one is a directory that holds the other.
func pathsOverlap(first, second string) bool {
	return first == second || strings.HasPrefix(first, second+"/") || strings.HasPrefix(second, first+"/")
}

// composeTask writes one commit on parent: the revision's changed paths staged over parent's tree in the temporary
// index, with the revision's author, committer and message.
func composeTask(ctx context.Context, repo, index, parent, revision string, paths []string) (string, error) {
	raw, err := runGit(ctx, repo, nil, nil, "cat-file", "commit", revision)
	if err != nil {
		return "", fmt.Errorf("read revision: %w", err)
	}
	identity, message, err := commitIdentity(raw)
	if err != nil {
		return "", err
	}
	indexEnv := []string{"GIT_INDEX_FILE=" + index}
	if _, err = runGit(ctx, repo, indexEnv, nil, "read-tree", parent); err != nil {
		return "", fmt.Errorf("read parent tree: %w", err)
	}
	if stageErr := stagePaths(ctx, repo, indexEnv, revision, paths); stageErr != nil {
		return "", stageErr
	}
	tree, err := runGit(ctx, repo, indexEnv, nil, "write-tree")
	if err != nil {
		return "", fmt.Errorf("write composed tree: %w", err)
	}
	commit, err := runGit(ctx, repo, identity, message, "commit-tree", strings.TrimSpace(string(tree)), "-p", parent)
	if err != nil {
		return "", fmt.Errorf("create commit: %w", err)
	}
	return strings.TrimSpace(string(commit)), nil
}

// stagePaths sets each of paths in the index to the revision's entry for it, and removes a path the revision lacks.
// Removals run first, so a file and a directory of the same name can trade places.
func stagePaths(ctx context.Context, repo string, env []string, revision string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	// The paths are literal: a path such as ":foo" must not be read as pathspec magic, or its entry would be missed and
	// the path removed.
	args := append([]string{"ls-tree", "-r", "-z", "--full-tree", revision, "--"}, paths...)
	listing, err := runGit(ctx, repo, []string{"GIT_LITERAL_PATHSPECS=1"}, nil, args...)
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
		if _, err = runGit(ctx, repo, env, removals.Bytes(), removeArgs...); err != nil {
			return fmt.Errorf("remove paths from temporary index: %w", err)
		}
	}
	if updates.Len() > 0 {
		if _, err = runGit(ctx, repo, env, updates.Bytes(), "update-index", "-z", "--index-info"); err != nil {
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
func pinCandidate(ctx context.Context, repo, ref, commit string) error {
	if !strings.HasPrefix(ref, pinPrefix) {
		return fmt.Errorf("candidate ref %q is outside %s", ref, pinPrefix)
	}
	_, createErr := runGit(ctx, repo, nil, nil, "update-ref", ref, commit, "")
	if createErr == nil {
		return nil
	}
	existing, err := resolveCommit(ctx, repo, ref)
	if err != nil {
		return fmt.Errorf("pin candidate ref: %w", createErr)
	}
	if existing != commit {
		return fmt.Errorf("candidate ref %q points to %q, want %q", ref, existing, commit)
	}
	return nil
}

// Advance moves branchRef to to, and only when the branch is at from. The empty from means the branch must not exist.
// Only a branch under refs/heads/epic/ can move.
func Advance(ctx context.Context, repo, branchRef, to, from string) error {
	if !strings.HasPrefix(branchRef, branchPrefix) {
		return fmt.Errorf("branch %q is outside %s", branchRef, branchPrefix)
	}
	target, err := resolveCommit(ctx, repo, to)
	if err != nil {
		return fmt.Errorf("resolve advance target: %w", err)
	}
	expected := ""
	if from != "" {
		if expected, err = resolveCommit(ctx, repo, from); err != nil {
			return fmt.Errorf("resolve advance expected commit: %w", err)
		}
	}
	if _, err = runGit(ctx, repo, nil, nil, "update-ref", branchRef, target, expected); err != nil {
		return fmt.Errorf("advance branch: %w", err)
	}
	return nil
}

// resolveCommit returns the full id of the commit that value names.
func resolveCommit(ctx context.Context, repo, value string) (string, error) {
	out, err := runGit(ctx, repo, nil, nil, "rev-parse", "--verify", "--end-of-options", value+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// splitNUL returns the NUL-terminated records of out, without the empty ones.
func splitNUL(out []byte) []string {
	return strings.FieldsFunc(string(out), func(r rune) bool { return r == '\x00' })
}

// runGit runs git in repo with argv and no shell. env is added to its environment and stdin is fed to it. It returns
// git's stdout, and on failure an error that carries git's stderr.
func runGit(ctx context.Context, repo string, env []string, stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git")
	cmd.Args = append(cmd.Args, "-C", repo)
	cmd.Args = append(cmd.Args, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
