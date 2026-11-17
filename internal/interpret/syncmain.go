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
	"github.com/joshsymonds/orrery/internal/compose"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// outcomeSkipped is the outcome of a sync run whose advance moved nothing, since its epic was no longer at the quiet
// point the sync started at.
const outcomeSkipped = "skipped"

// syncGitLimit is how long the git a sync's prepare runs may take, its fetch of origin's main above all. The prepare runs
// on the interpreter's pass, so a connection to origin that stalls holds every epic's flow until git is stopped.
const syncGitLimit = 3 * time.Minute

// prepareSync carries out the prepare of a sync run. It reads origin's main and merges origin's commit into the stored
// base without a checkout when the stored base does not hold that commit, the commit holds the epic's change base and no
// earlier sync tried it (mergeMain). Its git runs for at most the runner's sync limit, and git still running then is
// stopped. The result is
// recorded, with the merge as the run's revision, in one write, once. What kept the sync from reading or merging origin's
// main, git that ran past the limit among it, is recorded as the result's error and logged, so the run ends skipped and
// the epic goes on: a sync never parks.
func (r *Runner) prepareSync(ctx context.Context, epic store.EpicRow, act Action) error {
	result := kinds.SyncResult{Base: epic.StoredBase}
	if err := r.mergeMainWithin(ctx, epic, act, &result); err != nil {
		result.Error = err.Error()
		r.logger.WarnContext(ctx, "sync of origin's main did not run", "epic", epic.ID, "path", act.Path, "err", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode result of node %s: %w", act.Path, err)
	}
	if err = r.store.PrepareSync(ctx, epic.ID, act.Path, act.Run.Attempt, result.Merge, string(encoded)); err != nil {
		return fmt.Errorf("prepare sync %s: %w", act.Path, err)
	}
	return nil
}

// mergeMainWithin runs mergeMain for the sync run of act under a context that ends after the runner's sync limit, and
// returns its error. An error of git that the limit stopped says so, naming the limit.
func (r *Runner) mergeMainWithin(ctx context.Context, epic store.EpicRow, act Action, result *kinds.SyncResult) error {
	bounded, cancel := context.WithTimeout(ctx, r.syncLimit)
	defer cancel()
	err := mergeMain(bounded, epic, act, result)
	if err != nil && errors.Is(bounded.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("git did not finish within %s: %w", r.syncLimit, err)
	}
	return err
}

// mergeMain fills result for the sync run of act. A repository with no remote origin is noted and read no further.
// Otherwise origin's default branch is fetched into the sync's origin ref, as a release fetches it (fetchOrigin). A
// commit the stored base holds is noted. So is a commit that does not hold the epic's change base (changeBase), with
// that change base, since the change from such a commit would hold commits of the change base beside the epic's own. So
// is a commit an earlier sync tried, which act's Items name with the path of that sync in its IDs. Any other
// commit is merged into the stored base with git merge-tree --write-tree: a merge that conflicts records its conflicted
// paths, and one that does not is committed and pinned (commitSync). It returns what kept it from reading or merging
// origin's main.
func mergeMain(ctx context.Context, epic store.EpicRow, act Action, result *kinds.SyncResult) error {
	has, err := hasOrigin(ctx, epic.Repository)
	if err != nil {
		return err
	}
	if !has {
		result.NoOrigin = true
		return nil
	}
	branch, origin, err := fetchOrigin(ctx, epic, syncRef(epic, "origin"))
	if err != nil {
		return err
	}
	result.Branch, result.Origin = branch, origin
	contained, err := isAncestor(ctx, epic.Repository, origin, epic.StoredBase)
	if err != nil || contained {
		result.Contained = contained
		return err
	}
	from := changeBase(epic)
	held, err := isAncestor(ctx, epic.Repository, from, origin)
	if err != nil {
		return err
	}
	if !held {
		result.ChangeBase = from
		return nil
	}
	if i := slices.Index(act.Items, origin); i >= 0 {
		result.TriedBy = act.IDs[i]
		return nil
	}
	out, conflicted, err := mergeTree(ctx, epic.Repository, epic.StoredBase, origin)
	if err != nil {
		return err
	}
	tree, paths := mergedTree(out)
	if conflicted {
		if len(paths) == 0 {
			return errors.New(conflictMessage(epic.StoredBase, origin, nil))
		}
		result.Conflicts = paths
		return nil
	}
	result.Merge, err = commitSync(ctx, epic, origin, tree)
	return err
}

// hasOrigin reports whether repo has the remote a sync reads origin's main from.
func hasOrigin(ctx context.Context, repo string) (bool, error) {
	remotes, err := gitOutput(ctx, repo, "remote")
	if err != nil {
		return false, err
	}
	return slices.Contains(strings.Split(remotes, "\n"), releaseRemote), nil
}

// commitSync commits tree, the tree merge-tree wrote for the merge of origin's commit into epic's stored base, as the
// merge of the two in epic's repository, the stored base its first parent and origin's commit its second, without a
// checkout. It pins the merge under the sync ref named for origin's commit, and returns it.
func commitSync(ctx context.Context, epic store.EpicRow, origin, tree string) (string, error) {
	message := fmt.Sprintf("Merge origin's commit %s into stored base %s", origin, epic.StoredBase)
	commit, err := gitOutput(ctx, epic.Repository, "commit-tree", tree, "-p", epic.StoredBase, "-p", origin, "-m",
		message)
	if err != nil {
		return "", err
	}
	merged := strings.TrimSpace(commit)
	if err = build.Pin(ctx, epic.Repository, syncRef(epic, origin), merged); err != nil {
		return "", fmt.Errorf("pin sync merge: %w", err)
	}
	return merged, nil
}

// syncRef returns the ref under epic's sync namespace named name: the ref origin's main is fetched into, or the pin of
// the merge of an origin commit, which is named by that commit.
func syncRef(epic store.EpicRow, name string) string {
	return "refs/orrery/" + build.Slug(epic.ID) + "/sync/" + name
}

// advanceSync ends a sync run whose checks all passed at its merge (syncEnd), and writes the end. A passed end moves the
// stored base to the merge and records origin's commit as the epic's synced commit, in the end's write.
func (r *Runner) advanceSync(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	end, err := syncEnd(ctx, epic, act)
	if err != nil {
		return err
	}
	if err = r.store.EndSync(ctx, epic.ID, end, at); err != nil {
		return fmt.Errorf("end sync %s: %w", act.Path, err)
	}
	return nil
}

// syncEnd moves the epic's branch from the stored base to the merge of the sync run of act, with the same
// compare-and-swap, fast-forward-only advance an integrate run's batch makes, and returns the run's passed end. A branch
// already at the merge, with the stored base still the merge's first parent, is one a restart left after it moved the
// branch and before the end was written, so the end is passed with no move, whatever act's Reason: the end moves the
// stored base after the branch. Otherwise an act with a Reason, why the epic is no longer at the quiet point the sync
// started at, moves nothing, and its end is skipped, its output the merge's line and the Reason. A branch at another
// commit, one the merge does not build on, or one checked out in a worktree is not moved (advanceBlock): the end is
// failed, its output the merge's line and why, and it tells that output. The epic is not parked.
func syncEnd(ctx context.Context, epic store.EpicRow, act Action) (store.SyncEnd, error) {
	result, err := kinds.ParseSyncResult(act.Run.Result)
	if err != nil {
		return store.SyncEnd{}, fmt.Errorf("read result of node %s: %w", act.Path, err)
	}
	merge := act.Run.Revision
	passed := store.SyncEnd{
		Path: act.Path, Attempt: act.Run.Attempt, Outcome: outcomePassed, Output: act.Output, Synced: result.Origin,
	}
	tip, err := branchHead(ctx, epic)
	if err != nil {
		return store.SyncEnd{}, err
	}
	if tip == merge && epic.StoredBase == result.Base {
		return passed, nil
	}
	if act.Reason != "" {
		output := result.MergeLine() + "\nnot advanced: " + act.Reason
		return store.SyncEnd{Path: act.Path, Attempt: act.Run.Attempt, Outcome: outcomeSkipped, Output: output}, nil
	}
	reason, err := advanceBlock(ctx, epic, tip, merge)
	if err != nil {
		return store.SyncEnd{}, err
	}
	if reason != "" {
		output := result.MergeLine() + "\nnot advanced: " + reason
		return store.SyncEnd{
			Path: act.Path, Attempt: act.Run.Attempt, Outcome: outcomeFailed, Output: output,
			Tell: syncTell(act.Path, output),
		}, nil
	}
	if err = compose.Advance(ctx, epic.Repository, build.BranchRef(epic.ID), merge, epic.StoredBase); err != nil {
		return store.SyncEnd{}, fmt.Errorf("advance epic %s to %s: %w", epic.ID, merge, err)
	}
	return passed, nil
}

// changeBase returns the commit the epic's own change is read from: the commit of origin's main its stored base last
// merged in a sync, or the epic's base when no sync has passed. A sync merges origin's main into the stored base, so the
// change from the epic's base would hold origin's commits beside the epic's own. A sync merges only an origin commit that
// holds the change base before it (mergeMain), so the change from the change base is the epic's own and none of
// origin's.
func changeBase(epic store.EpicRow) string {
	if epic.Synced != "" {
		return epic.Synced
	}
	return epic.Base
}
