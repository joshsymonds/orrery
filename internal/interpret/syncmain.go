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
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// outcomeSkipped is the outcome of a sync run that did not decide origin's commit: its prepare made no merge, a check
// it could not decide or work that did not finish stopped it, or its advance was not taken.
const outcomeSkipped = "skipped"

// The limits of the git a sync of origin's main runs against origin. The read of origin's main and the sync's prepare
// run on the interpreter's pass, so origin that stalls holds every epic's flow until git is stopped. syncReadLimit
// bounds each git ls-remote of origin, the read's and the prepare's, which moves no objects. syncFetchLimit bounds the
// prepare's fetch of origin's main, which moves the commits origin's main gained since the repository last fetched it,
// and is run only once a read found origin's main ahead of the stored base.
const (
	syncReadLimit  = 20 * time.Second
	syncFetchLimit = time.Minute
)

// readOrigin reads origin's main for epic's sync (lookAtOrigin) and records the read as the epic's last, with the
// stored base it was read for and at, in one write. A read that failed, git that ran past the read limit among it, is
// recorded failed and logged, so no sync starts, and origin's main is read again only once syncInterval has passed or
// the stored base has moved. A pass whose context has ended records nothing, since the daemon is stopping.
func (r *Runner) readOrigin(ctx context.Context, epic store.EpicRow, at time.Time) error {
	read := store.OriginRead{Base: epic.StoredBase, At: at}
	commit, state, err := r.lookAtOrigin(ctx, epic)
	switch {
	case err != nil && ctx.Err() != nil:
		return err
	case err != nil:
		read.State = originFailed
		r.logger.WarnContext(ctx, "origin's main was not read", "epic", epic.ID, "err", err)
	default:
		read.Commit, read.State = commit, state
	}
	if err = r.store.RecordOriginRead(ctx, epic.ID, read); err != nil {
		return fmt.Errorf("record read of origin's main: %w", err)
	}
	return nil
}

// lookAtOrigin returns origin's commit, the one origin's HEAD names, and what it is to epic. A repository with no
// remote origin is originNone. Otherwise origin is read with git ls-remote within the read limit (originHead), which
// fetches nothing, and the commit is placed by the repository's own commits (originState).
func (r *Runner) lookAtOrigin(ctx context.Context, epic store.EpicRow) (string, string, error) {
	host, err := r.hostOf(epic)
	if err != nil {
		return "", "", err
	}
	has, err := hasOrigin(ctx, host, epic.Repository)
	if err != nil {
		return "", "", err
	}
	if !has {
		return "", originNone, nil
	}
	_, commit, err := r.originHead(ctx, host, epic)
	if err != nil {
		return "", "", err
	}
	state, err := originState(ctx, host, epic, commit)
	if err != nil {
		return "", "", err
	}
	return commit, state, nil
}

// originState returns what origin's commit is to epic on host. A commit the repository does not have is originAhead,
// since the stored base holds only commits the repository has, and the sync's fetch brings it. A commit the stored base
// holds is originHeld. One that does not hold the epic's change base (changeBase) is originApart, since a sync of it
// would end skipped, and any other is originAhead.
func originState(ctx context.Context, host hostexec.Host, epic store.EpicRow, commit string) (string, error) {
	known, err := hasCommit(ctx, host, epic.Repository, commit)
	if err != nil {
		return "", err
	}
	if !known {
		return originAhead, nil
	}
	held, err := isAncestor(ctx, host, epic.Repository, commit, epic.StoredBase)
	if err != nil {
		return "", err
	}
	if held {
		return originHeld, nil
	}
	holds, err := isAncestor(ctx, host, epic.Repository, changeBase(epic), commit)
	if err != nil {
		return "", err
	}
	if !holds {
		return originApart, nil
	}
	return originAhead, nil
}

// hasCommit reports whether repo on host has commit. A commit it does not have is an answer, not a failure.
func hasCommit(ctx context.Context, host hostexec.Host, repo, commit string) (bool, error) {
	_, err := gitOutput(ctx, host, repo, "rev-parse", "--verify", "--quiet", "--end-of-options", commit+"^{commit}")
	if err == nil {
		return true, nil
	}
	var gitErr *hostexec.GitError
	if errors.As(err, &gitErr) && gitErr.ExitCode == noCommit {
		return false, nil
	}
	return false, err
}

// originHead reads origin of epic's repository on host with git ls-remote (lsRemoteHead) for at most the runner's read
// limit, and returns origin's default branch and the commit its HEAD names. git still running at the limit is stopped,
// and the error says so (stoppedAt).
func (r *Runner) originHead(ctx context.Context, host hostexec.Host, epic store.EpicRow) (string, string, error) {
	reading, cancel := context.WithTimeout(ctx, r.readLimit)
	defer cancel()
	branch, commit, err := lsRemoteHead(reading, host, epic.Repository)
	if err != nil {
		return "", "", stoppedAt(reading, r.readLimit, err)
	}
	return branch, commit, nil
}

// fetchMain reads origin's default branch within the read limit (originHead) and fetches it into the sync's origin ref
// on host for at most the runner's fetch limit (fetchBranch), as a release fetches it, and returns the branch and the
// commit the ref names. git still running at a limit is stopped, and the error says so (stoppedAt).
func (r *Runner) fetchMain(ctx context.Context, host hostexec.Host, epic store.EpicRow) (string, string, error) {
	branch, _, err := r.originHead(ctx, host, epic)
	if err != nil {
		return "", "", err
	}
	fetching, cancel := context.WithTimeout(ctx, r.fetchLimit)
	defer cancel()
	commit, err := fetchBranch(fetching, host, epic, branch, syncRef(epic, "origin"))
	if err != nil {
		return "", "", stoppedAt(fetching, r.fetchLimit, err)
	}
	return branch, commit, nil
}

// stoppedAt returns err, the error of git run under ctx, which ends after limit. When ctx ended at its limit, the error
// says git did not finish within it.
func stoppedAt(ctx context.Context, limit time.Duration, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("git did not finish within %s: %w", limit, err)
	}
	return err
}

// prepareSync carries out the prepare of a sync run. It reads origin's main and merges origin's commit into the stored
// base without a checkout when the stored base does not hold that commit, the commit holds the epic's change base and
// no earlier sync tried it (mergeMain). The result is recorded, with the merge as the run's revision and the prepare's
// read of origin's main as the epic's last read (preparedRead), in one write, once. What kept the sync from reading or
// merging origin's main, git that ran past a limit among it, is recorded as the result's error and logged, so the run
// ends skipped and the epic goes on: a sync never parks. A pass whose context has ended records nothing, since the
// daemon is stopping and the next one prepares the run again.
func (r *Runner) prepareSync(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	result := kinds.SyncResult{Base: epic.StoredBase}
	if err := r.mergeMain(ctx, epic, act, &result); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("prepare sync %s: %w", act.Path, err)
		}
		result.Error = err.Error()
		r.logger.WarnContext(ctx, "sync of origin's main did not run", "epic", epic.ID, "path", act.Path, "err", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode result of node %s: %w", act.Path, err)
	}
	if err = r.store.PrepareSync(
		ctx, epic.ID, act.Path, act.Run.Attempt, result.Merge, string(encoded), preparedRead(epic, result, at),
	); err != nil {
		return fmt.Errorf("prepare sync %s: %w", act.Path, err)
	}
	return nil
}

// preparedRead returns the read of origin's main a sync's prepare made for epic at at, as result found it: originNone
// with no origin, originFailed on an error, originHeld for a commit the stored base holds, originApart for one that
// does not hold the change base, and originTaken for one the prepare took, to merge, to find conflicting or to find
// tried. The read the sync started from found that commit ahead of the stored base, and this one takes its place, so it
// starts no second sync while it stands.
func preparedRead(epic store.EpicRow, result kinds.SyncResult, at time.Time) store.OriginRead {
	read := store.OriginRead{Base: epic.StoredBase, Commit: result.Origin, At: at}
	switch {
	case result.NoOrigin:
		read.State = originNone
	case result.Error != "":
		read.State = originFailed
	case result.Contained:
		read.State = originHeld
	case result.ChangeBase != "":
		read.State = originApart
	default:
		read.State = originTaken
	}
	return read
}

// mergeMain fills result for the sync run of act. A repository with no remote origin is noted and read no further.
// Otherwise origin's default branch is fetched into the sync's origin ref (fetchMain). A commit the stored base holds
// is noted. So is a commit that does not hold the epic's change base (changeBase), with that change base, since the
// change from such a commit would hold commits of the change base beside the epic's own. So is a commit an earlier sync
// tried, which act's Items name with the path of that sync in its IDs. Any other commit is merged into the stored base
// with git merge-tree --write-tree: a merge that conflicts records its conflicted paths, and one that does not is
// committed and pinned (commitSync). It returns what kept it from reading or merging origin's main.
func (r *Runner) mergeMain(ctx context.Context, epic store.EpicRow, act Action, result *kinds.SyncResult) error {
	host, err := r.hostOf(epic)
	if err != nil {
		return err
	}
	has, err := hasOrigin(ctx, host, epic.Repository)
	if err != nil {
		return err
	}
	if !has {
		result.NoOrigin = true
		return nil
	}
	branch, origin, err := r.fetchMain(ctx, host, epic)
	if err != nil {
		return err
	}
	result.Branch, result.Origin = branch, origin
	contained, err := isAncestor(ctx, host, epic.Repository, origin, epic.StoredBase)
	if err != nil || contained {
		result.Contained = contained
		return err
	}
	from := changeBase(epic)
	held, err := isAncestor(ctx, host, epic.Repository, from, origin)
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
	out, conflicted, err := mergeTree(ctx, host, epic.Repository, epic.StoredBase, origin)
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
	result.Merge, err = commitSync(ctx, host, epic, origin, tree)
	return err
}

// hasOrigin reports whether repo on host has the remote a sync reads origin's main from.
func hasOrigin(ctx context.Context, host hostexec.Host, repo string) (bool, error) {
	remotes, err := gitOutput(ctx, host, repo, "remote")
	if err != nil {
		return false, err
	}
	return slices.Contains(strings.Split(remotes, "\n"), releaseRemote), nil
}

// commitSync commits tree, the tree merge-tree wrote for the merge of origin's commit into epic's stored base, as the
// merge of the two in epic's repository, the stored base its first parent and origin's commit its second, without a
// checkout. It pins the merge under the sync ref named for origin's commit on host, and returns it.
func commitSync(ctx context.Context, host hostexec.Host, epic store.EpicRow, origin, tree string) (string, error) {
	message := fmt.Sprintf("Merge origin's commit %s into stored base %s", origin, epic.StoredBase)
	commit, err := gitOutput(ctx, host, epic.Repository, "commit-tree", tree, "-p", epic.StoredBase, "-p", origin, "-m",
		message)
	if err != nil {
		return "", err
	}
	merged := strings.TrimSpace(commit)
	if err = build.Pin(ctx, host, epic.Repository, syncRef(epic, origin), merged); err != nil {
		return "", fmt.Errorf("pin sync merge: %w", err)
	}
	return merged, nil
}

// syncRef returns the ref under epic's sync namespace named name: the ref origin's main is fetched into, or the pin of
// the merge of an origin commit, which is named by that commit.
func syncRef(epic store.EpicRow, name string) string {
	return "refs/orrery/" + build.Slug(epic.ID) + "/sync/" + name
}

// syncWork carries out one work action of a sync run: its prepare, the start of a Done gate's check, its captures, its
// reproductions or its advance. A start, a capture or a reproduction that fails ends the run skipped with why
// (syncUnfinished), which ends the hold the run puts on the plan, so the epic is not held by work that fails on every
// pass. The prepare records its own failures as its result, and the advance ends the run itself (advanceSync).
func (r *Runner) syncWork(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	var err error
	switch act.Work {
	case kinds.ActPrepare:
		return r.prepareSync(ctx, epic, act, at)
	case kinds.ActAdvance:
		return r.advanceSync(ctx, epic, act, at)
	case kinds.ActStart:
		err = r.start(ctx, epic, act)
	case kinds.ActCapture:
		err = r.capture(ctx, epic, act)
	case kinds.ActRepro:
		err = r.repro(ctx, epic, act)
	default:
		return fmt.Errorf("work %s is not one the executor carries out for a sync", act.Work)
	}
	if err != nil {
		return r.syncUnfinished(ctx, epic, act, err, at)
	}
	return nil
}

// syncUnfinished ends the sync run of act skipped once its work failed with cause, and logs cause as a warning. Its
// output is the line of its merge and then not finished: <cause>. A skipped sync decided nothing of origin's commit, so
// a later quiet point may try that commit again, and it tells nothing, as a prepare that failed tells nothing. The end
// is the run's one end, written once, so the run is told at most once whatever its work did before. A pass whose
// context has ended leaves the run as it is, since the daemon is stopping and the next one carries the work out again.
func (r *Runner) syncUnfinished(ctx context.Context, epic store.EpicRow, act Action, cause error, at time.Time) error {
	if ctx.Err() != nil {
		return cause
	}
	r.logger.WarnContext(ctx, "sync of origin's main did not finish", "epic", epic.ID, "path", act.Path, "err", cause)
	output := "not finished: " + cause.Error()
	if result, err := kinds.ParseSyncResult(act.Run.Result); err == nil {
		output = result.MergeLine() + "\n" + output
	}
	end := store.SyncEnd{Path: act.Path, Attempt: act.Run.Attempt, Outcome: outcomeSkipped, Output: output}
	if err := r.store.EndSync(ctx, epic.ID, end, at); err != nil {
		return errors.Join(cause, fmt.Errorf("end sync %s: %w", act.Path, err))
	}
	return nil
}

// advanceSync ends a sync run whose checks all passed at its merge (syncEnd), and writes the end. A passed end moves
// the stored base to the merge and records origin's commit as the epic's synced commit, in the end's write. An advance
// whose git or read failed has not moved the branch, so it ends the run skipped (syncUnfinished). An end whose write
// failed is left to the next pass, which reads a branch it moved at the merge and so ends the run passed.
func (r *Runner) advanceSync(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	end, err := r.syncEnd(ctx, epic, act)
	if err != nil {
		return r.syncUnfinished(ctx, epic, act, err, at)
	}
	if err = r.store.EndSync(ctx, epic.ID, end, at); err != nil {
		return fmt.Errorf("end sync %s: %w", act.Path, err)
	}
	return nil
}

// syncEnd moves the epic's branch from the stored base to the merge of the sync run of act, with the same
// compare-and-swap, fast-forward-only advance an integrate run's batch makes, and returns the run's passed end. A
// branch already at the merge, with the stored base still the merge's first parent, is one a restart left after it
// moved the branch and before the end was written, so the end is passed with no move, whatever act's Reason: the end
// moves the stored base after the branch. Otherwise the branch is not moved, and the end is skipped, its output the
// merge's line and then not advanced: <why>, when act has a Reason, why the epic was no longer at the quiet point the
// sync started at when the pass decided, or when the branch may not move (advanceBlock): it is at another commit, the
// merge does not build on it, or it is checked out in a worktree, which is told. Just before the move the epic is read
// again, and an epic no longer at that quiet point (syncHalt), paused or parked since the pass read it among it, is not
// moved either. The epic is not parked.
func (r *Runner) syncEnd(ctx context.Context, epic store.EpicRow, act Action) (store.SyncEnd, error) {
	result, err := kinds.ParseSyncResult(act.Run.Result)
	if err != nil {
		return store.SyncEnd{}, fmt.Errorf("read result of node %s: %w", act.Path, err)
	}
	merge := act.Run.Revision
	passed := store.SyncEnd{
		Path: act.Path, Attempt: act.Run.Attempt, Outcome: outcomePassed, Output: act.Output, Synced: result.Origin,
	}
	tip, err := r.branchHead(ctx, epic)
	if err != nil {
		return store.SyncEnd{}, err
	}
	if tip == merge && epic.StoredBase == result.Base {
		return passed, nil
	}
	if act.Reason != "" {
		return notAdvanced(act, result, act.Reason, false), nil
	}
	host, err := r.hostOf(epic)
	if err != nil {
		return store.SyncEnd{}, err
	}
	block, err := advanceBlock(ctx, host, epic, tip, merge)
	if err != nil {
		return store.SyncEnd{}, err
	}
	if block != "" {
		return notAdvanced(act, result, block, true), nil
	}
	moved, err := r.syncHalt(ctx, epic.ID, result)
	if err != nil {
		return store.SyncEnd{}, err
	}
	if moved != "" {
		return notAdvanced(act, result, moved, false), nil
	}
	if err = compose.Advance(ctx, host, epic.Repository, build.BranchRef(epic.ID), merge, epic.StoredBase); err != nil {
		return store.SyncEnd{}, fmt.Errorf("advance epic %s to %s: %w", epic.ID, merge, err)
	}
	return passed, nil
}

// notAdvanced returns the skipped end of the sync run of act whose advance did not move the branch for why: its output
// is the line of result's merge and then not advanced: <why>, and it tells that output when told is set.
func notAdvanced(act Action, result kinds.SyncResult, why string, told bool) store.SyncEnd {
	output := result.MergeLine() + "\nnot advanced: " + why
	end := store.SyncEnd{Path: act.Path, Attempt: act.Run.Attempt, Outcome: outcomeSkipped, Output: output}
	if told {
		end.Tell = syncTell(act.Path, outcomeSkipped, output)
	}
	return end
}

// syncHalt reads the rows of the epic with id as they are now and returns why the epic is no longer at the quiet point
// the sync whose prepare found result started at (kinds.SyncMoved), "" while it is.
func (r *Runner) syncHalt(ctx context.Context, id string, result kinds.SyncResult) (string, error) {
	read, err := r.store.EpicFlow(ctx, id)
	if err != nil {
		return "", fmt.Errorf("read epic %s: %w", id, err)
	}
	return kinds.SyncMoved(flowRows(read), result), nil
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
