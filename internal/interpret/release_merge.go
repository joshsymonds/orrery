package interpret

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// releaseRemote is the remote a release reads origin's default branch from.
const releaseRemote = "origin"

// remoteKillGrace bounds how long git that reads origin is waited for once its context has ended and it was killed, so
// a transport it started that outlives it cannot hold its output open.
const remoteKillGrace = 2 * time.Second

// The exit statuses that answer a question rather than fail. merge-base --is-ancestor exits notAncestor for a commit
// that is not an ancestor, merge-tree --write-tree exits mergeConflict when the merge conflicts, with the conflicted
// paths on its stdout.
const (
	notAncestor   = 1
	mergeConflict = 1
)

// prepareRelease carries out the prepare of a release run. A run with no Result works out its candidate, records the
// Result, and then prepares the candidate as its revision. A run whose Result is set prepares the candidate that Result
// names and reads nothing from git again. A Result that has an error prepares nothing: the release kind stops the run
// at that error. A Result that names origin's commit prepares nothing either, whether git's merge of it conflicts or
// not, or the tip already holds it and has a migration collision: the release integrator makes the candidate.
func (r *Runner) prepareRelease(ctx context.Context, epic store.EpicRow, act Action) error {
	result, err := kinds.ParseReleaseResult(act.Run.Result)
	if err != nil {
		return fmt.Errorf("read result of node %s: %w", act.Path, err)
	}
	if act.Run.Result == "" {
		decided, decideErr := r.decideRelease(ctx, epic)
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
	if result.Error != "" || result.Origin != "" {
		return nil
	}
	if err = r.store.PrepareNode(ctx, epic.ID, act.Path, act.Run.Attempt, result.Candidate, ""); err != nil {
		return fmt.Errorf("prepare node %s: %w", act.Path, err)
	}
	return nil
}

// decideRelease works out the Result of a release run's prepare. The candidate is the epic's branch tip when origin's
// commit is an ancestor of it. Otherwise the Result has no candidate: it names origin's commit, the branch of origin it
// was fetched from and the paths git's merge of origin's commit into the tip conflicts in, none for a clean merge, and
// the release integrator makes the merge. A merge that conflicts in no path git names is an error. Either way the
// Result names the goose migrations origin's project file declares and each new migration of the tip whose version
// origin's main uses for another file, with the renumber that settles it (releaseMigrations). A tip that holds
// origin's commit and has such a collision has no candidate either: the Result names origin's commit and its branch,
// and the release integrator makes the renumber. Nothing is checked out and nothing is committed, and the only ref
// written is the release's own ref for origin.
func (r *Runner) decideRelease(ctx context.Context, epic store.EpicRow) (kinds.ReleaseResult, error) {
	host, err := r.hostOf(epic)
	if err != nil {
		return kinds.ReleaseResult{}, err
	}
	tip, ok, err := build.BranchTip(ctx, host, epic.Repository, epic.ID)
	if err != nil {
		return kinds.ReleaseResult{}, fmt.Errorf("branch of epic %s: %w", epic.ID, err)
	}
	if !ok {
		return kinds.ReleaseResult{}, fmt.Errorf("epic %s has no branch", epic.ID)
	}
	branch, origin, err := fetchOrigin(ctx, host, epic, releaseRef(epic, "origin"))
	if err != nil {
		return kinds.ReleaseResult{}, err
	}
	dir, collisions, err := releaseMigrations(ctx, host, epic.Repository, tip, origin)
	if err != nil {
		return kinds.ReleaseResult{}, err
	}
	ancestor, err := isAncestor(ctx, host, epic.Repository, origin, tip)
	if err != nil {
		return kinds.ReleaseResult{}, err
	}
	if ancestor && len(collisions) == 0 {
		return kinds.ReleaseResult{Candidate: tip, Tip: tip, Migrations: dir}, nil
	}
	if ancestor {
		return kinds.ReleaseResult{
			Tip: tip, Origin: origin, Branch: branch, Migrations: dir, Collisions: collisions,
		}, nil
	}
	out, conflicted, err := mergeTree(ctx, host, epic.Repository, tip, origin)
	if err != nil {
		return kinds.ReleaseResult{}, err
	}
	_, paths := mergedTree(out)
	if conflicted && len(paths) == 0 {
		return kinds.ReleaseResult{}, errors.New(conflictMessage(tip, origin, nil))
	}
	return kinds.ReleaseResult{
		Behind: true, Tip: tip, Origin: origin, Branch: branch, Conflicts: paths, Migrations: dir,
		Collisions: collisions,
	}, nil
}

// releaseMigrations returns the directory of goose migrations the project file at origin's commit declares, read from
// the commit (enginehost.ReadProjectMigrations), and the lines that name each new migration of tip whose version
// origin's main uses for another file, with the renumber of the tip's new migrations that settles them
// (migrations.Plan.Lines). origin's declaration is read, so an epic whose base predates it is still checked against the
// main it releases to. A project that declares none gives "" and no line, and so does a tip with no collision.
func releaseMigrations(ctx context.Context, host hostexec.Host, repo, tip, origin string) (string, []string, error) {
	dir, err := enginehost.ReadProjectMigrations(ctx, host, repo, origin)
	if err != nil {
		return "", nil, fmt.Errorf("read the migrations origin's commit %s declares: %w", origin, err)
	}
	if dir == "" {
		return "", nil, nil
	}
	plan, err := checkMigrations(ctx, host, repo, dir, tip, origin, "")
	if err != nil {
		return "", nil, err
	}
	return dir, plan.Lines(sideCandidate), nil
}

// fetchOrigin fetches origin's default branch into ref of epic's repository on host, and returns the branch and the
// commit the ref names. The default branch is the one origin's HEAD names (lsRemoteHead). A release fetches into its
// origin ref.
func fetchOrigin(ctx context.Context, host hostexec.Host, epic store.EpicRow, ref string) (string, string, error) {
	branch, _, err := lsRemoteHead(ctx, host, epic.Repository)
	if err != nil {
		return "", "", err
	}
	commit, err := fetchBranch(ctx, host, epic, branch, ref)
	if err != nil {
		return "", "", err
	}
	return branch, commit, nil
}

// lsRemoteHead reads origin of repo on host with git ls-remote --symref origin HEAD (remoteOutput), and returns origin's
// default branch, the one its HEAD names, and the commit its HEAD names. It fetches nothing.
func lsRemoteHead(ctx context.Context, host hostexec.Host, repo string) (string, string, error) {
	listing, err := remoteOutput(ctx, host, repo, "ls-remote", "--symref", releaseRemote, "HEAD")
	if err != nil {
		return "", "", err
	}
	branch, err := defaultBranch(listing)
	if err != nil {
		return "", "", err
	}
	commit, err := headCommit(listing)
	if err != nil {
		return "", "", err
	}
	return branch, commit, nil
}

// fetchBranch fetches branch of origin into ref of epic's repository on host (remoteOutput), and returns the commit the
// ref names.
func fetchBranch(ctx context.Context, host hostexec.Host, epic store.EpicRow, branch, ref string) (string, error) {
	fetchSpec := "+" + branch + ":" + ref
	if _, err := remoteOutput(
		ctx,
		host,
		epic.Repository,
		"fetch",
		"--no-tags",
		"--no-write-fetch-head",
		"--refmap=",
		releaseRemote,
		fetchSpec,
	); err != nil {
		return "", err
	}
	commit, err := gitOutput(ctx, host, epic.Repository, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(commit), nil
}

// remoteOutput runs git in dir on host as gitOutput does, for a command that reads origin. The run is a Remote one: git
// never prompts for a username or password, so it fails where it would wait on a prompt, and it runs in a process group
// of its own, which ctx's end kills whole, so the transport git started is stopped with it. It is waited for at most
// remoteKillGrace after that.
func remoteOutput(ctx context.Context, host hostexec.Host, dir string, args ...string) (string, error) {
	out, err := host.Git(ctx, hostexec.GitCall{Dir: dir, Args: args, Remote: true, WaitDelay: remoteKillGrace})
	if err != nil {
		return "", fmt.Errorf("%w", err)
	}
	return string(out), nil
}

// candidateContainsOrigin reports whether origin's current main is in candidate, which is to say an ancestor of it or the
// commit itself. It fetches origin first, as a prepare does, so the answer is for origin as it is now.
func candidateContainsOrigin(
	ctx context.Context,
	host hostexec.Host,
	epic store.EpicRow,
	candidate string,
) (bool, error) {
	_, origin, err := fetchOrigin(ctx, host, epic, releaseRef(epic, "origin"))
	if err != nil {
		return false, err
	}
	return isAncestor(ctx, host, epic.Repository, origin, candidate)
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

// headCommit returns the commit that origin's HEAD names, read from the output of ls-remote --symref: the line that
// names HEAD and is not the line of its symref.
func headCommit(listing string) (string, error) {
	for _, line := range strings.Split(listing, "\n") {
		commit, name, found := strings.Cut(line, "\t")
		if found && name == "HEAD" && !strings.HasPrefix(commit, "ref: ") && commit != "" {
			return commit, nil
		}
	}
	return "", fmt.Errorf("git ls-remote --symref %s HEAD names no commit", releaseRemote)
}

// isAncestor reports whether ancestor is an ancestor of descendant in repo on host. A commit that is not one is an
// answer, not a failure.
func isAncestor(ctx context.Context, host hostexec.Host, repo, ancestor, descendant string) (bool, error) {
	_, err := gitOutput(ctx, host, repo, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	var gitErr *hostexec.GitError
	if errors.As(err, &gitErr) && gitErr.ExitCode == notAncestor {
		return false, nil
	}
	return false, err
}

// mergeMessage is the message of the merge of origin's commit into the release tip tip.
func mergeMessage(tip, origin string) string {
	return fmt.Sprintf("Merge origin's commit %s into release tip %s", origin, tip)
}

// renumberMessage is the message of the commit that renumbers the migrations of the release tip tip, which already holds
// origin's commit origin, after origin's.
func renumberMessage(tip, origin string) string {
	return fmt.Sprintf("Renumber the migrations of release tip %s after origin's commit %s", tip, origin)
}

// runPin returns the ref the merge a release run's release integrator makes is pinned under: the run's id and attempt
// under epic's release namespace.
func runPin(epic store.EpicRow, run store.NodeRun) string {
	return releaseRef(epic, fmt.Sprintf("%s-%d", run.ID, run.Attempt))
}

// mergeTree runs merge-tree --write-tree of tip and origin in repo on host, without a checkout, naming only the
// conflicted paths, and returns what git printed on stdout and whether the merge conflicts (runMergeTree).
func mergeTree(ctx context.Context, host hostexec.Host, repo, tip, origin string) (string, bool, error) {
	return runMergeTree(ctx, host, repo, "--name-only", tip, origin)
}

// runMergeTree runs merge-tree --write-tree --no-messages with args in repo on host, without a checkout, and returns
// what git printed on stdout and whether the merge conflicts, which git says with the exit status mergeConflict. A
// conflicting merge's stdout is returned with no error.
func runMergeTree(ctx context.Context, host hostexec.Host, repo string, args ...string) (string, bool, error) {
	args = append([]string{"merge-tree", "--write-tree", "--no-messages"}, args...)
	out, err := host.Git(ctx, hostexec.GitCall{Dir: repo, Args: args})
	var gitErr *hostexec.GitError
	conflicted := errors.As(err, &gitErr) && gitErr.ExitCode == mergeConflict
	if err != nil && !conflicted {
		return "", false, fmt.Errorf("%w", err)
	}
	return string(out), conflicted, nil
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
