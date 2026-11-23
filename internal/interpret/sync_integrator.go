package interpret

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/planner"
	"github.com/joshsymonds/orrery/internal/roles"
	"github.com/joshsymonds/orrery/internal/store"
)

// syncRestartedEnd is the end of a step a restart ended before it returned: the end the engine's Reconcile stores on each
// open step row (enginehost's stepRestartedEnd).
const syncRestartedEnd = "engine_restarted"

// roundBlocked is the verdict of a sync integrator round whose step returned blocked, beside the verdicts of a task
// integrator round.
const roundBlocked = "blocked"

// syncStartItems is the number of items a start of a sync integrator round carries: its mode, its round and the head the
// round before made.
const syncStartItems = 3

// syncIntegratorRole is the role text of the sync's integrator: the writing step that finishes a merge of origin's commit
// into the stored base that conflicts, and commits it.
const syncIntegratorRole = "You are the sync integrator for one epic. origin's main has moved past the epic's stored " +
	"base, and the merge of origin's commit into the stored base conflicts. This worktree holds that merge in progress. " +
	"HEAD is the stored base, the stored base's side, which git calls ours and keeps at stage 2. MERGE_HEAD is origin's " +
	"commit, origin's side, which git calls theirs and keeps at stage 3. Each conflict shows both sides with the merge " +
	"base between them. The epic's contract and decisions, the merge, the conflicted paths, the changes both sides hold " +
	"and the checks that run after you follow this text.\n\n" +
	"Your job is to finish the merge so that what each side set out to do still holds, and to commit it. Nobody is " +
	"watching the turn and nobody answers a question during it. After you return done, orreryd checks the commit, runs " +
	"the epic's Done gates and evidence captures on it, and advances the epic to it when they pass. A resolution that " +
	"silently drops a change is worse than no resolution, because it passes for a merge and ships. So finish the merge " +
	"when you can show what each side meant, and stop and report when you cannot.\n\n" +
	"Read every conflict before you edit any. For each conflicted path, read the three versions with `git show :1:PATH`, " +
	"`:2:PATH` and `:3:PATH`, and the commits on each side that touched it with `git log --merge -p -- PATH`. Read first, " +
	"because one conflict's answer often rests on another's, and because a merge you cannot finish is best reported " +
	"before it is half edited.\n\n" +
	"Resolve each path by what the two sides did to it. When both sides added different things in one place, keep both, " +
	"in the order the file already follows. When both sides made the same change, it appears once; Same change lists " +
	"the pairs orreryd found. When one side changed code that the other side removed, renamed or rewrote, the removal, " +
	"rename or rewrite stands and the first side's change is carried into the new code. Keeping the old code beside the " +
	"new brings back what one side took out. Outside the conflicts, change only what one side's change needs in order " +
	"to work against the other's, and list each such change in other_changes with its reason. The merge adds no " +
	"behavior that neither side wrote, repairs nothing that is broken on one side alone, and reformats nothing.\n\n" +
	"Every test of either side stays, with its assertions as they are. A test of one side that fails against the other " +
	"side's change shows a real conflict: resolve it in the code or report it. Never change a test's assertions, skip " +
	"it or delete it to make the merge pass.\n\n" +
	"Stop, and return status blocked, when the two sides decide one behavior differently and neither the contract nor " +
	"the epic's decisions say which holds, when keeping both sides needs behavior that neither side wrote, or when a " +
	"resolution would remove behavior a Requirement names or bring in what Must Not Ship lists. In unresolved, give for " +
	"each such path what the stored base meant as candidate, what origin meant as origin, the decision that would settle " +
	"it, and side: merge, since the decision is how to resolve the merge itself. Never abort the merge, and never start " +
	"it again as a rebase or a cherry-pick.\n\n" +
	"Before you commit, check your work. `git ls-files -u` prints nothing, and no file holds a conflict marker. Read " +
	"`git diff AUTO_MERGE`, which shows exactly what you resolved, and run the build and the tests for the packages the " +
	"merge touched. Start no command that outlives your turn: no background job and no watcher of a log. A command longer " +
	"than the shell allows belongs to the Done gates, which orreryd runs on your commit after you return done. Do not run " +
	"the Done gates yourself.\n\n" +
	"Commit the merge once, with the message the prompt gives followed by one line for each path saying how it was " +
	"resolved. The commit has two parents, the stored base and origin's commit, in that order, and orreryd refuses any " +
	"other shape. Add no other commit. Do not push, tag or move any ref.\n\n" +
	"Return one object with these parts. status: done when the merge is committed, blocked when you stopped. commit: the " +
	"full id of HEAD, or \"\" when blocked. resolutions: one entry for each conflicted path, with path; took, which is " +
	"both, origin, candidate or regenerated, where candidate is the stored base's side; and kept, one or two sentences " +
	"saying what stayed from each side and, when took is origin or candidate, why the other side loses nothing. " +
	"other_changes: each change outside the conflicts, with path and change. checks: each command you ran, with command " +
	"and result. unresolved: each path you stopped on, with path, candidate, origin, decision and side. evidence: a short " +
	"account of the merge for someone who has not seen this worktree. Leave every unused list empty."

// syncIntegrator carries out one ActIntegrator action of a sync run, as the first of its items says: SyncRoundStart
// starts round Items[1] (startSyncRound), and SyncRoundRecord records it once its step ended (recordSyncRound).
func (r *Runner) syncIntegrator(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	if len(act.IDs) == 0 || len(act.Items) < 2 {
		return fmt.Errorf("sync integrator action of node %s names no step or round", act.Path)
	}
	round, err := strconv.ParseInt(act.Items[1], 10, 64)
	if err != nil {
		return fmt.Errorf("sync integrator round %q of node %s is not a round number: %w", act.Items[1], act.Path, err)
	}
	switch act.Items[0] {
	case kinds.SyncRoundStart:
		return r.startSyncRound(ctx, epic, act, round, at)
	case kinds.SyncRoundRecord:
		return r.recordSyncRound(ctx, epic, act, round, at)
	default:
		return fmt.Errorf("sync integrator action of node %s has no mode %q", act.Path, act.Items[0])
	}
}

// startSyncRound starts round of the sync's integrator act names, as an unowned writing step in the sync run's integrator
// worktree. Round 1 adds the worktree afresh, detached at the stored base, and starts the merge of origin's commit there
// with merge.conflictStyle zdiff3, leaving its conflicts in place (startMerge). A later round checks that HEAD is the
// merge commit the round before made, which act's third item names (keptMerge). The step is labeled with the epic and the
// sync run's path, carries the integrator's return check (integratorCheck), and its prompt names act's output as the last
// round's failure. A role, worktree, merge, prompt or check that fails, or a start refused for good, records the round
// failed at once, and the sync ends failed on its next decision.
func (r *Runner) startSyncRound(
	ctx context.Context, epic store.EpicRow, act Action, round int64, at time.Time,
) error {
	if len(act.Items) < syncStartItems {
		return fmt.Errorf("sync integrator round %d of node %s names no head", round, act.Path)
	}
	result, err := kinds.ParseSyncResult(act.Run.Result)
	if err != nil {
		return fmt.Errorf("read result of node %s: %w", act.Path, err)
	}
	id, head := act.IDs[0], act.Items[2]
	fail := func(cause error) error { return r.failSyncRound(ctx, epic, act, round, cause, at) }
	agent, err := roles.AgentOr(r.getenv, roleIntegrator, roleRelease)
	if err != nil {
		return fail(err)
	}
	dir, err := r.integratorDir(act.Run)
	if err != nil {
		return fail(err)
	}
	merge := syncMerge(epic, result, filepath.Join(dir, releaseWorktree))
	if head == "" {
		err = startMerge(ctx, merge, dir)
	} else {
		err = keptMerge(ctx, merge, head)
	}
	if err != nil {
		return fail(err)
	}
	prompt, err := r.syncIntegratorPrompt(ctx, epic, merge, result.Branch, head, act.Output)
	if err != nil {
		return fail(err)
	}
	check, err := integratorCheck(ctx, merge)
	if err != nil {
		return fail(err)
	}
	_, err = r.steps.Start(ctx, enginehost.StepStart{
		ID: id, Agent: agent, Prompt: prompt, Schema: IntegratorSchema, Workspace: merge.worktree,
		Revision: merge.tip, Unowned: true, DoneValue: releaseDone, Epic: epic.ID, Task: act.Path,
		ReturnCheck: check,
	})
	if err == nil {
		return nil
	}
	if !refusedForGood(err) {
		return fmt.Errorf("start sync integrator %s: %w", id, err)
	}
	return fail(err)
}

// recordSyncRound records round of the sync's integrator act names once its step's end is stored. A done return whose
// commit passes the engine's checks again passes the round: the commit is pinned under the sync's ref for origin's commit
// and recorded as the round's head. A blocked return fails it with the verdict blocked and its report (blockedReason). A
// step that ended engine_restarted skips it, and any other end fails it, with its failure named.
func (r *Runner) recordSyncRound(
	ctx context.Context, epic store.EpicRow, act Action, round int64, at time.Time,
) error {
	row, err := r.taskStep(ctx, epic.ID, act.Path, act.IDs[0])
	if err != nil {
		return err
	}
	result, err := kinds.ParseSyncResult(act.Run.Result)
	if err != nil {
		return fmt.Errorf("read result of node %s: %w", act.Path, err)
	}
	merge := syncMerge(epic, result, row.Workspace)
	var decodeReason string
	ret, err := parseIntegratorReturn([]byte(row.Result))
	if err != nil {
		decodeReason = err.Error()
	}
	record := store.IntegrateRound{
		Path: act.Path, Attempt: act.Run.Attempt, Task: 0, Round: round, Step: act.IDs[0], Verdict: kinds.RoundEnded,
		Outcome: outcomeFailed,
	}
	switch {
	case row.End == releaseDone && decodeReason == "":
		head, failures, checkErr := integratorCommit(ctx, merge, ret)
		if checkErr != nil {
			return checkErr
		}
		record.Explanation = ret.Evidence
		if len(failures) > 0 {
			record.Failure = "the sync integrator's commit was refused: " + strings.Join(failures, "; ")
			break
		}
		if err = build.Pin(ctx, epic.Repository, syncRef(epic, result.Origin), head); err != nil {
			return fmt.Errorf("pin sync integrator merge: %w", err)
		}
		record.Outcome, record.Verdict, record.Head = outcomePassed, kinds.RoundMerged, head
	case row.End == stepReturned && ret.Status == releaseBlocked && decodeReason == "":
		record.Verdict = roundBlocked
		record.Explanation = blockedReason(merge, ret)
	case row.End == syncRestartedEnd:
		record.Outcome = outcomeSkipped
	case row.End == releaseDone:
		record.Failure = decodeReason
	default:
		record.Failure = releaseEndReason("sync integrator", row)
	}
	if err = r.store.RecordIntegrateRound(ctx, epic.ID, record, at); err != nil {
		return fmt.Errorf("record round %d of sync %s: %w", round, act.Path, err)
	}
	return nil
}

// syncMerge returns the merge a sync's integrator round finishes in worktree: origin's commit into the stored base of
// result, which is the merge's tip, with the paths result conflicts in.
func syncMerge(epic store.EpicRow, result kinds.SyncResult, worktree string) integratorMerge {
	return integratorMerge{
		repo: epic.Repository, worktree: worktree, tip: result.Base, origin: result.Origin, conflicts: result.Conflicts,
		stored: true,
	}
}

// syncIntegratorPrompt renders the prompt of a round of the sync's integrator that finishes merge, whose origin commit
// was fetched from origin's branch: the role text, then the sections Contract, Decisions, Merge, Conflicted paths, Same
// change, Checks after you and Previous attempt, each under a `## ` heading and None. when it has no lines
// (planner.WriteSection). head is the merge commit the round before made, "" for the first round, and previous the failure
// that commit met.
func (r *Runner) syncIntegratorPrompt(
	ctx context.Context, epic store.EpicRow, merge integratorMerge, branch, head, previous string,
) (string, error) {
	base, err := gitOutput(ctx, merge.repo, "merge-base", merge.tip, merge.origin)
	if err != nil {
		return "", err
	}
	base = strings.TrimSpace(base)
	contract, err := integratorContract(epic)
	if err != nil {
		return "", err
	}
	conflicted, err := conflictedLines(ctx, merge, base)
	if err != nil {
		return "", err
	}
	same, err := sameChangeLines(ctx, merge.repo, base, merge)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	fmt.Fprintln(&out, strings.TrimRight(syncIntegratorRole, "\n"))
	planner.WriteSection(&out, "Contract", contract)
	planner.WriteSection(&out, "Decisions", planner.DecisionLines(epic, math.MaxInt64))
	planner.WriteSection(&out, "Merge", syncMergeLines(merge, branch, base, head))
	planner.WriteSection(&out, "Conflicted paths", conflicted)
	planner.WriteSection(&out, "Same change", same)
	planner.WriteSection(&out, "Checks after you", r.checkLines(ctx, epic))
	planner.WriteSection(&out, "Previous attempt", previousLines(previous))
	return out.String(), nil
}

// syncMergeLines returns the Merge section of a sync's integrator prompt: the worktree, the stored base and origin's
// commit with the names git gives each side, the branch of origin the commit was fetched from, the merge base, and the
// message the merge commit carries. A round after the first also names head, the merge commit the round before made.
func syncMergeLines(merge integratorMerge, branch, base, head string) []string {
	lines := []string{
		"Worktree: " + merge.worktree,
		"Stored base: " + merge.tip + ", the stored base's side: HEAD when the merge began, ours, stage 2",
		fmt.Sprintf("Origin's commit: %s from origin's %s, origin's side: MERGE_HEAD, theirs, stage 3",
			merge.origin, branch),
		"Merge base: " + base,
		"Commit message: " + syncMergeMessage(merge.origin, merge.tip),
	}
	if head != "" {
		lines = append(lines, fmt.Sprintf(
			"Committed: an earlier round committed this merge as %s, which is HEAD now. Amend it; its parents stay the "+
				"stored base and origin's commit.",
			head,
		))
	}
	return lines
}

// failSyncRound records round of the sync run of act as failed, with the conflict line of its result and cause, the
// reason the round's step did not start. The next pass ends the sync failed and told, with that failure. The round
// never merged, so its verdict is ended, as a task integrator round whose step ended is: the store has no verdict for a
// round that did not start, and rejects an empty one.
func (r *Runner) failSyncRound(
	ctx context.Context, epic store.EpicRow, act Action, round int64, cause error, at time.Time,
) error {
	result, err := kinds.ParseSyncResult(act.Run.Result)
	if err != nil {
		return fmt.Errorf("read result of node %s: %w", act.Path, err)
	}
	record := store.IntegrateRound{
		Path: act.Path, Attempt: act.Run.Attempt, Task: 0, Round: round, Step: act.IDs[0], Verdict: kinds.RoundEnded,
		Outcome: outcomeFailed, Failure: result.ConflictLine() + ": " + cause.Error(),
	}
	if err = r.store.RecordIntegrateRound(ctx, epic.ID, record, at); err != nil {
		return fmt.Errorf("record round %d of sync %s: %w", round, act.Path, err)
	}
	return nil
}
