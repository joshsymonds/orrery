package interpret

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// releaseRemote is the remote a release reads origin's default branch from.
const releaseRemote = "origin"

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
// at that error.
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
	if result.Error != "" {
		return nil
	}
	if err = r.store.PrepareNode(ctx, epic.ID, act.Path, act.Run.Attempt, result.Candidate, ""); err != nil {
		return fmt.Errorf("prepare node %s: %w", act.Path, err)
	}
	return nil
}

// decideRelease works out the Result of a release run's prepare. The candidate is the epic's branch tip, or, when
// origin's commit is not an ancestor of the tip, the merge of origin's commit into the tip. Nothing is checked out, and
// the only ref written is the release's own ref for origin and the run's pin.
func decideRelease(ctx context.Context, epic store.EpicRow, run store.NodeRun) (kinds.ReleaseResult, error) {
	tip, ok, err := build.BranchTip(ctx, epic.Repository, epic.ID)
	if err != nil {
		return kinds.ReleaseResult{}, fmt.Errorf("branch of epic %s: %w", epic.ID, err)
	}
	if !ok {
		return kinds.ReleaseResult{}, fmt.Errorf("epic %s has no branch", epic.ID)
	}
	origin, err := fetchOrigin(ctx, epic)
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
	merged, err := mergeOrigin(ctx, epic, run, tip, origin)
	if err != nil {
		return kinds.ReleaseResult{}, err
	}
	return kinds.ReleaseResult{Behind: true, Candidate: merged, Tip: tip}, nil
}

// fetchOrigin fetches origin's default branch into the release's origin ref of epic's repository, and returns the commit
// the ref names. The default branch is the one origin's HEAD names.
func fetchOrigin(ctx context.Context, epic store.EpicRow) (string, error) {
	listing, err := gitOutput(ctx, epic.Repository, "ls-remote", "--symref", releaseRemote, "HEAD")
	if err != nil {
		return "", err
	}
	branch, err := defaultBranch(listing)
	if err != nil {
		return "", err
	}
	ref := releaseRef(epic, "origin")
	fetchSpec := "+" + branch + ":" + ref
	if _, err = gitOutput(
		ctx, epic.Repository, "fetch", "--no-tags", "--no-write-fetch-head", "--refmap=", releaseRemote, fetchSpec,
	); err != nil {
		return "", err
	}
	commit, err := gitOutput(ctx, epic.Repository, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(commit), nil
}

// candidateContainsOrigin reports whether origin's current main is in candidate, which is to say an ancestor of it or the
// commit itself. It fetches origin first, as a prepare does, so the answer is for origin as it is now.
func candidateContainsOrigin(ctx context.Context, epic store.EpicRow, candidate string) (bool, error) {
	origin, err := fetchOrigin(ctx, epic)
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

// mergeOrigin merges origin's commit into tip in epic's repository, without a checkout, and pins the merge under the
// run's release ref. A merge that conflicts is an error that names the conflicted paths.
func mergeOrigin(ctx context.Context, epic store.EpicRow, run store.NodeRun, tip, origin string) (string, error) {
	out, conflicted, err := mergeTree(ctx, epic.Repository, tip, origin)
	if err != nil {
		return "", err
	}
	tree, err := mergedTree(tip, origin, out, conflicted)
	if err != nil {
		return "", err
	}
	message := fmt.Sprintf("Merge origin's commit %s into release tip %s", origin, tip)
	commit, err := gitOutput(ctx, epic.Repository, "commit-tree", tree, "-p", tip, "-p", origin, "-m", message)
	if err != nil {
		return "", err
	}
	merged := strings.TrimSpace(commit)
	ref := releaseRef(epic, fmt.Sprintf("%s-%d", run.ID, run.Attempt))
	if pinErr := build.Pin(ctx, epic.Repository, ref, merged); pinErr != nil {
		return "", fmt.Errorf("pin release merge: %w", pinErr)
	}
	return merged, nil
}

// mergeTree runs merge-tree --write-tree of tip and origin in repo, without a checkout, and returns what git printed on
// stdout and whether the merge conflicts, which git says with the exit status mergeConflict.
func mergeTree(ctx context.Context, repo, tip, origin string) (string, bool, error) {
	args := []string{"merge-tree", "--write-tree", "--name-only", "--no-messages", tip, origin}
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

// mergedTree returns the tree merge-tree wrote, as its stdout out gives it, for the merge of origin into tip. git writes
// the tree first and then each conflicted path, one to a line. A merge that conflicts has no tree. Its error names the
// conflicted paths git listed, and says the merge conflicts when git listed none.
func mergedTree(tip, origin, out string, conflicted bool) (string, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if !conflicted {
		return lines[0], nil
	}
	message := fmt.Sprintf("merge of origin's commit %s into %s conflicts", origin, tip)
	if paths := lines[1:]; len(paths) > 0 {
		message += " in " + strings.Join(paths, ", ")
	}
	return "", errors.New(message)
}

// releaseRef returns the ref under epic's release namespace named name: the release's origin ref, or a run's pin.
func releaseRef(epic store.EpicRow, name string) string {
	return "refs/orrery/" + build.Slug(epic.ID) + "/release/" + name
}
