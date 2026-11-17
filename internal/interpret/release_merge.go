package interpret

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// releaseRemote is the remote a release reads origin's default branch from.
const releaseRemote = "origin"

// remoteKillGrace bounds how long git that reads origin is waited for once its context has ended and it was killed, so
// a transport it started that outlives it cannot hold its output open.
const remoteKillGrace = 2 * time.Second

// The exit statuses that answer a question rather than fail. merge-base --is-ancestor exits notAncestor for a commit
// that is not an ancestor, and merge-tree --write-tree exits mergeConflict when the merge conflicts, with the conflicted
// paths on its stdout.
const (
	notAncestor   = 1
	mergeConflict = 1
)

// prepareRelease carries out the prepare of a release run. A run with no Result works out its candidate, records the
// Result, and then prepares the candidate as its revision. A run whose Result is set prepares the candidate that Result
// names and reads nothing from git again. A Result that has an error prepares nothing: the release kind stops the run
// at that error. A Result whose merge conflicts prepares nothing either: the release integrator makes its candidate.
func (r *Runner) prepareRelease(ctx context.Context, epic store.EpicRow, act Action) error {
	result, err := kinds.ParseReleaseResult(act.Run.Result)
	if err != nil {
		return fmt.Errorf("read result of node %s: %w", act.Path, err)
	}
	if act.Run.Result == "" {
		decided, decideErr := decideRelease(ctx, epic, act.Run)
		if decideErr != nil {
			decided = kinds.ReleaseResult{Error: decideErr.Error()}
		}
		encoded, marshalErr := json.Marshal(decided)
		if marshalErr != nil {
			return fmt.Errorf("encode result of node %s: %w", act.Path, marshalErr)
		}
		if err = r.store.RecordNodeResult(ctx, epic.ID, act.Path, act.Run.Attempt, string(encoded)); err != nil {
			return fmt.Errorf("record result of node %s: %w", act.Path, err)
		}
		result = decided
	}
	if result.Error != "" || len(result.Conflicts) > 0 {
		return nil
	}
	if err = r.store.PrepareNode(ctx, epic.ID, act.Path, act.Run.Attempt, result.Candidate, ""); err != nil {
		return fmt.Errorf("prepare node %s: %w", act.Path, err)
	}
	return nil
}

// decideRelease works out the Result of a release run's prepare. The candidate is the epic's branch tip, or, when
// origin's commit is not an ancestor of the tip, the merge of origin's commit into the tip. A merge that conflicts has
// no candidate: its Result names origin's commit, the branch of origin it was fetched from and the conflicted paths. A
// merge that conflicts in no path git names is an error. Nothing is checked out, and the only ref written is the
// release's own ref for origin and the run's pin.
func decideRelease(ctx context.Context, epic store.EpicRow, run store.NodeRun) (kinds.ReleaseResult, error) {
	tip, ok, err := build.BranchTip(ctx, epic.Repository, epic.ID)
	if err != nil {
		return kinds.ReleaseResult{}, fmt.Errorf("branch of epic %s: %w", epic.ID, err)
	}
	if !ok {
		return kinds.ReleaseResult{}, fmt.Errorf("epic %s has no branch", epic.ID)
	}
	branch, origin, err := fetchOrigin(ctx, epic, releaseRef(epic, "origin"))
	if err != nil {
		return kinds.ReleaseResult{}, err
	}
	ancestor, err := isAncestor(ctx, epic.Repository, origin, tip)
	if err != nil {
		return kinds.ReleaseResult{}, err
	}
	if ancestor {
		return kinds.ReleaseResult{Candidate: tip, Tip: tip}, nil
	}
	out, conflicted, err := mergeTree(ctx, epic.Repository, tip, origin)
	if err != nil {
		return kinds.ReleaseResult{}, err
	}
	tree, paths := mergedTree(out)
	if conflicted {
		if len(paths) == 0 {
			return kinds.ReleaseResult{}, errors.New(conflictMessage(tip, origin, nil))
		}
		return kinds.ReleaseResult{Behind: true, Tip: tip, Origin: origin, Branch: branch, Conflicts: paths}, nil
	}
	merged, err := mergeOrigin(ctx, epic, run, tip, origin, tree)
	if err != nil {
		return kinds.ReleaseResult{}, err
	}
	return kinds.ReleaseResult{Behind: true, Candidate: merged, Tip: tip}, nil
}

// fetchOrigin fetches origin's default branch into ref of epic's repository, and returns the branch and the commit the
// ref names. The default branch is the one origin's HEAD names. A release fetches into its origin ref, and a sync of
// origin's main into its own. It reads origin with remoteOutput.
func fetchOrigin(ctx context.Context, epic store.EpicRow, ref string) (string, string, error) {
	listing, err := remoteOutput(ctx, epic.Repository, "ls-remote", "--symref", releaseRemote, "HEAD")
	if err != nil {
		return "", "", err
	}
	branch, err := defaultBranch(listing)
	if err != nil {
		return "", "", err
	}
	fetchSpec := "+" + branch + ":" + ref
	if _, err = remoteOutput(
		ctx, epic.Repository, "fetch", "--no-tags", "--no-write-fetch-head", "--refmap=", releaseRemote, fetchSpec,
	); err != nil {
		return "", "", err
	}
	commit, err := gitOutput(ctx, epic.Repository, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", "", err
	}
	return branch, strings.TrimSpace(commit), nil
}

// remoteOutput runs git in dir as gitOutput does, for a command that reads origin. git never prompts for a username or
// password (GIT_TERMINAL_PROMPT=0), so it fails where it would wait on a prompt. It runs in a process group of its own,
// which ctx's end kills whole, so the transport git started is stopped with it, and it is waited for at most
// remoteKillGrace after that.
func remoteOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := gitCommand(ctx, dir, args)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = remoteKillGrace
	return gitRun(cmd, args)
}

// candidateContainsOrigin reports whether origin's current main is in candidate, which is to say an ancestor of it or the
// commit itself. It fetches origin first, as a prepare does, so the answer is for origin as it is now.
func candidateContainsOrigin(ctx context.Context, epic store.EpicRow, candidate string) (bool, error) {
	_, origin, err := fetchOrigin(ctx, epic, releaseRef(epic, "origin"))
	if err != nil {
		return false, err
	}
	return isAncestor(ctx, epic.Repository, origin, candidate)
}

// defaultBranch returns the branch that origin's HEAD names, read from the output of ls-remote --symref.
func defaultBranch(listing string) (string, error) {
	for _, line := range strings.Split(listing, "\n") {
		if target, ok := strings.CutPrefix(line, "ref: "); ok {
			branch, _, _ := strings.Cut(target, "\t")
			return branch, nil
		}
	}
	return "", fmt.Errorf("git ls-remote --symref %s HEAD names no default branch", releaseRemote)
}

// isAncestor reports whether ancestor is an ancestor of descendant in repo. A commit that is not one is an answer, not a
// failure.
func isAncestor(ctx context.Context, repo, ancestor, descendant string) (bool, error) {
	_, err := gitOutput(ctx, repo, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == notAncestor {
		return false, nil
	}
	return false, err
}

// mergeOrigin commits tree, the tree merge-tree wrote for the merge of origin's commit into tip, as the merge of the two
// in epic's repository, without a checkout, and pins the merge under the run's release ref.
func mergeOrigin(ctx context.Context, epic store.EpicRow, run store.NodeRun, tip, origin, tree string) (string, error) {
	commit, err := gitOutput(ctx, epic.Repository, "commit-tree", tree, "-p", tip, "-p", origin, "-m",
		mergeMessage(tip, origin))
	if err != nil {
		return "", err
	}
	merged := strings.TrimSpace(commit)
	if pinErr := build.Pin(ctx, epic.Repository, runPin(epic, run), merged); pinErr != nil {
		return "", fmt.Errorf("pin release merge: %w", pinErr)
	}
	return merged, nil
}

// mergeMessage is the message of the merge of origin's commit into the release tip tip.
func mergeMessage(tip, origin string) string {
	return fmt.Sprintf("Merge origin's commit %s into release tip %s", origin, tip)
}

// runPin returns the ref the merge a release run's prepare or its release integrator makes is pinned under: the run's
// id and attempt under epic's release namespace.
func runPin(epic store.EpicRow, run store.NodeRun) string {
	return releaseRef(epic, fmt.Sprintf("%s-%d", run.ID, run.Attempt))
}

// mergeTree runs merge-tree --write-tree of tip and origin in repo, without a checkout, naming only the conflicted
// paths, and returns what git printed on stdout and whether the merge conflicts (runMergeTree).
func mergeTree(ctx context.Context, repo, tip, origin string) (string, bool, error) {
	return runMergeTree(ctx, repo, "--name-only", tip, origin)
}

// runMergeTree runs merge-tree --write-tree --no-messages with args in repo, without a checkout, and returns what git
// printed on stdout and whether the merge conflicts, which git says with the exit status mergeConflict.
func runMergeTree(ctx context.Context, repo string, args ...string) (string, bool, error) {
	args = append([]string{"merge-tree", "--write-tree", "--no-messages"}, args...)
	cmd := exec.CommandContext(ctx, "git")
	cmd.Args = append(cmd.Args, "-C", repo)
	cmd.Args = append(cmd.Args, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	conflicted := errors.As(err, &exit) && exit.ExitCode() == mergeConflict
	if err != nil && !conflicted {
		return "", false, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), conflicted, nil
}

// mergedTree returns the tree merge-tree wrote and the paths it names as conflicted, as its stdout out gives them. git
// writes the tree first and then each conflicted path, one to a line, and a path is named once however many of its
// stages conflict. A merge that conflicts writes a tree too, with the conflicted paths holding their conflict markers.
func mergedTree(out string) (string, []string) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var paths []string
	for _, path := range lines[1:] {
		if path != "" && !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	return lines[0], paths
}

// conflictMessage is the reason a merge of origin's commit into tip that conflicts gives: it names the conflicted paths,
// or says only that the merge conflicts when there are none to name.
func conflictMessage(tip, origin string, paths []string) string {
	message := fmt.Sprintf("merge of origin's commit %s into %s conflicts", origin, tip)
	if len(paths) > 0 {
		message += " in " + strings.Join(paths, ", ")
	}
	return message
}

// releaseRef returns the ref under epic's release namespace named name: the release's origin ref, or a run's pin.
func releaseRef(epic store.EpicRow, name string) string {
	return "refs/orrery/" + build.Slug(epic.ID) + "/release/" + name
}
