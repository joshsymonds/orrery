package interpret

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
)

// earlierReturnMax bounds how much of the return of the step that gave a release reviewer's earlier verdict the release
// integrator's prompt gives. The whole return stays on the step's row.
const earlierReturnMax = 1200

// earlierReview is the verdict a release reviewer gave a candidate of this release that origin's main then moved past,
// as the release integrator of the candidate prepared again decides on it: the reviewer, the position its review of the
// new candidate takes (store.ReleaseReviewPosition at the new generation), the passed review run of the generation
// before, the candidate the reviewer last read itself, which is that run's head or, for a review carried to it, the
// candidate the carried verdict was given on, and the end and structured return of the step that gave the verdict, ""
// when its row is not held.
type earlierReview struct {
	reviewer string
	position int64
	verdict  store.ReleaseRun
	reviewed string
	end, ret string
}

// earlierReviews returns the earlier verdict of each enabled release reviewer, in merged order, that the round of the
// release integrator of run's candidate decides on, from flow, the epic's rows, and result, what the release's prepare
// recorded. It returns none, so every reviewer reviews the new candidate in full, unless the candidate was prepared
// again only because origin's main moved: the run's generation replaces a candidate (store.ReplacedCandidate), the
// merge is of origin's commit into a tip that lacks it, so no migration renumber changes the epic's files, and git's
// merge of the two conflicts in no path, since a conflicted path is one the epic's own change touched. A reviewer is
// listed only when its review of the replaced candidate passed (earlierReviewOf), so only a verdict that held is ever
// carried, and the candidate it last read was made from result's tip (kinds.CandidateTip), so the epic's own change is
// the one it read. The rest of the floor, the paths the merge changes outside git's merge, is the round's own and is
// read once it has returned (epicChangeMoved).
//
// The earlier review is looked up at run's attempt and the generation before its own. A candidate is marked stale only
// as the first release step starts or ends, and that step starts only once every enabled reviewer held the candidate at
// the attempt and generation it runs under: a candidate a new attempt reuses is reviewed again at that attempt before
// its first step. So the candidate a generation replaces was always reviewed at the same attempt, one generation
// before, and no earlier attempt needs to be read.
func earlierReviews(flow store.EpicFlow, run store.NodeRun, result kinds.ReleaseResult) []earlierReview {
	generation := store.ReleaseGeneration(flow.Releases, run.Attempt)
	replaced, ok := store.ReplacedCandidate(flow.Releases, run.Attempt, generation)
	if !ok || result.Origin == "" || !result.Behind || len(result.Conflicts) > 0 {
		return nil
	}
	enabled := reviewers.ForStage(flow.Epic.Reviewers, reviewers.StageRelease)
	count := int64(len(enabled))
	var out []earlierReview
	for i, reviewer := range enabled {
		index := int64(i + 1)
		earlier, held := earlierReviewOf(flow, run, replaced, generation, count, index, reviewer.Name)
		if !held {
			continue
		}
		if tip, made := kinds.CandidateTip(flow.Releases, earlier.reviewed); !made || tip != result.Tip {
			continue
		}
		earlier.position = store.ReleaseReviewPosition(generation, count, index)
		out = append(out, earlier)
	}
	return out
}

// earlierReviewOf returns the earlier verdict of the release reviewer name, at index among the enabled release
// reviewers, on replaced, the candidate that run's candidate at generation replaces: its passed review of replaced at
// run's attempt and the generation before. It reports false when no such review passed, and when the review that passed
// is not one whose step ended done: a step that ended any other way, such as at an engine restart, held the candidate
// without a verdict of its own, so it is reviewed again rather than carried. A review carried to replaced was given on
// an earlier candidate by a step that ended done, so the generations before are walked back to the review that was not
// carried, and the step that gave the verdict is the one that review ran under (store.ReleaseReviewWorkID).
func earlierReviewOf(
	flow store.EpicFlow, run store.NodeRun, replaced string, generation, enabled, index int64, name string,
) (earlierReview, bool) {
	verdict, ok := reviewAt(flow.Releases, run.Attempt, store.ReleaseReviewPosition(generation-1, enabled, index), name)
	if !ok || !verdict.Passed || verdict.Head != replaced ||
		(verdict.End != releaseDone && verdict.End != store.ReleaseCarriedEnd) {
		return earlierReview{}, false
	}
	earlier := earlierReview{reviewer: name, verdict: verdict, reviewed: verdict.Head}
	if verdict.End == store.ReleaseCarriedEnd {
		earlier.reviewed = verdict.Start
	}
	for given := generation - 1; given >= 0; given-- {
		at, found := reviewAt(flow.Releases, run.Attempt, store.ReleaseReviewPosition(given, enabled, index), name)
		if !found {
			break
		}
		if at.End == store.ReleaseCarriedEnd {
			continue
		}
		step, err := stepIn(flow, store.ReleaseReviewWorkID(run.ID, run.Attempt, given, name))
		if err == nil {
			earlier.end, earlier.ret = step.End, step.Result
		}
		break
	}
	return earlier, true
}

// reviewAt returns the review release run of the reviewer name at attempt and position among runs, and whether there
// is one.
func reviewAt(runs []store.ReleaseRun, attempt, position int64, name string) (store.ReleaseRun, bool) {
	i := slices.IndexFunc(runs, func(run store.ReleaseRun) bool {
		return run.Part == store.ReleasePartReview && run.Attempt == attempt && run.Position == position &&
			run.Step == name
	})
	if i < 0 {
		return store.ReleaseRun{}, false
	}
	return runs[i], true
}

// reviewerNames returns the reviewers of earlier, in order.
func reviewerNames(earlier []earlierReview) []string {
	names := make([]string, 0, len(earlier))
	for _, review := range earlier {
		names = append(names, review.reviewer)
	}
	return names
}

// held returns what the earlier verdict says, as the release integrator and a reviewer that reviews again read it:
// `held candidate <c>`, then, for a verdict carried to the replaced candidate, the candidate it was carried to, and
// otherwise how its review ended, with the reason it recorded when it recorded one. A carried verdict's own reason holds
// the verdict it was carried from, so it is left out, and the text does not grow with each generation that carries it.
func (e earlierReview) held() string {
	line := "held candidate " + e.reviewed
	switch {
	case e.verdict.End == store.ReleaseCarriedEnd:
		return fmt.Sprintf("%s; that verdict was carried to candidate %s", line, e.verdict.Head)
	case e.verdict.Reason != "":
		return fmt.Sprintf("%s; its review ended %s: %s", line, e.verdict.End, e.verdict.Reason)
	}
	return fmt.Sprintf("%s; its review ended %s", line, e.verdict.End)
}

// earlierReviewLines returns the Earlier reviews section of a release integrator's prompt: each earlier verdict as `-
// <reviewer> held candidate <c>; ...` (earlierReview.held), then the structured return of the step that gave it, cut to
// earlierReturnMax bytes, when its row is held.
func earlierReviewLines(earlier []earlierReview) []string {
	lines := make([]string, 0, 2*len(earlier))
	for _, review := range earlier {
		lines = append(lines, fmt.Sprintf("- %s %s", review.reviewer, review.held()))
		if ret := strings.TrimSpace(review.ret); ret != "" {
			lines = append(lines, fmt.Sprintf("  its step ended %s and returned: %s", review.end,
				cutText(ret, earlierReturnMax)))
		}
	}
	return lines
}

// originSinceLines returns the Origin since the reviewed candidates section of a release integrator's prompt: for each
// candidate an earlier verdict of earlier was given on, once, origin's change since it, from the merge base of that
// candidate and origin, where the two meet, to origin, read in repo on host: a line that names the three commits, each
// path the change touches as `- <status> <path>` (changeLines), then its diff stat. It returns none when earlier is
// empty.
func originSinceLines(
	ctx context.Context, host hostexec.Host, repo string, earlier []earlierReview, origin string,
) ([]string, error) {
	var lines, seen []string
	for _, review := range earlier {
		if slices.Contains(seen, review.reviewed) {
			continue
		}
		seen = append(seen, review.reviewed)
		base, err := gitOutput(ctx, host, repo, "merge-base", review.reviewed, origin)
		if err != nil {
			return nil, fmt.Errorf(
				"merge base of candidate %s and origin's commit %s: %w",
				review.reviewed,
				origin,
				err,
			)
		}
		base = strings.TrimSpace(base)
		changes, err := nameStatus(ctx, host, repo, base, origin)
		if err != nil {
			return nil, err
		}
		stat, err := gitOutput(ctx, host, repo, "diff", "--stat", base, origin)
		if err != nil {
			return nil, fmt.Errorf("diff stat of origin's change since candidate %s: %w", review.reviewed, err)
		}
		lines = append(lines, fmt.Sprintf("Since candidate %s: origin's change from %s, where the two meet, to "+
			"origin's commit %s", review.reviewed, base, origin))
		lines = append(lines, changeLines(changes, base, origin)...)
		lines = append(lines, "Diff stat:")
		lines = append(lines, cappedLines(stat)...)
	}
	return lines, nil
}

// settleReviews returns what a passed round of the release integrator of run settles for the release reviewers whose
// earlier verdicts its prompt listed, earlier, once it made head, the new candidate, from merge with its done return
// ret: the release runs the round's write stores for them, and the lines the round's record gives of each. When the
// epic's own change may have moved in the merge (epicChangeMoved), it settles nothing, so every reviewer reviews head
// in full with the plain prompt, and one line says why. Otherwise each reviewer whose verdict ret says stands is
// carried to head: a review release run at its position, passed, with the end store.ReleaseCarriedEnd, the candidate
// it last read as its start, head as its head, and a reason that names the earlier review and the integrator's reason.
// The release kind then finds that reviewer held and starts no step for it. Each other reviewer, one ret says does not
// stand or gives no entry for, reviews head again: a rereview release run at its position, with the candidate it last
// read as its start, head as its head, its earlier verdict as its evidence and why it reviews again as its reason,
// which its start reads (startReleaseReview). Each run is stamped at, the round's own time.
func (r *Runner) settleReviews(
	ctx context.Context, epic store.EpicRow, run store.NodeRun, merge integratorMerge, ret integratorReturn,
	earlier []earlierReview, head string, at time.Time,
) ([]store.ReleaseRun, []string, error) {
	if len(earlier) == 0 {
		return nil, nil, nil
	}
	host, err := r.hostOf(epic)
	if err != nil {
		return nil, nil, err
	}
	moved, err := epicChangeMoved(ctx, host, merge, ret)
	if err != nil {
		return nil, nil, err
	}
	if moved != "" {
		return nil, []string{fmt.Sprintf("every release reviewer reviews candidate %s in full: %s", head, moved)}, nil
	}
	reviews := make([]store.ReleaseRun, 0, len(earlier))
	lines := make([]string, 0, len(earlier))
	for _, review := range earlier {
		settled := store.ReleaseRun{
			Position: review.position, Attempt: run.Attempt, Step: review.reviewer, Start: review.reviewed,
			Head: head, At: at,
		}
		i := slices.IndexFunc(ret.Reviews, func(said integratorReview) bool { return said.Reviewer == review.reviewer })
		if i >= 0 && ret.Reviews[i].Stands {
			settled.Part, settled.End, settled.Passed = store.ReleasePartReview, store.ReleaseCarriedEnd, true
			settled.Reason = fmt.Sprintf("carried from the review of release reviewer %s at attempt %d, position %d, "+
				"which %s; the release integrator said it stands: %s", review.reviewer, review.verdict.Attempt,
				review.verdict.Position, review.held(), ret.Reviews[i].Reason)
			lines = append(lines, fmt.Sprintf("release reviewer %s's verdict on candidate %s is carried to %s: %s",
				review.reviewer, review.reviewed, head, ret.Reviews[i].Reason))
			reviews = append(reviews, settled)
			continue
		}
		settled.Part, settled.Evidence = store.ReleasePartRereview, review.held()
		settled.Reason = "the release integrator's return gave no entry for it"
		if i >= 0 {
			settled.Reason = "the release integrator said its verdict does not stand: " + ret.Reviews[i].Reason
		}
		lines = append(lines, fmt.Sprintf("release reviewer %s reviews candidate %s again, with the change since "+
			"candidate %s: %s", review.reviewer, head, review.reviewed, settled.Reason))
		reviews = append(reviews, settled)
	}
	return reviews, lines, nil
}

// epicChangeMoved returns why the epic's own change may have moved in the round of the release integrator that made
// merge with its done return ret, read on host, and "" when it is unchanged: the round renumbered the tip's migrations,
// which changes the epic's files; git's merge conflicted, and a conflicted path is one the epic's own change touched;
// or a path the merge changes outside git's merge (ret's other_changes) is one the epic's own change, from the merge
// base of the tip and origin's commit to the tip, touched. With earlierReviews' own floor, a tip that is the one the
// reviewed candidate was made from, this is the floor that decides whether any earlier verdict may be carried.
func epicChangeMoved(
	ctx context.Context, host hostexec.Host, merge integratorMerge, ret integratorReturn,
) (string, error) {
	switch {
	case merge.held:
		return "the round renumbered the release tip's migrations, which changes the epic's own files", nil
	case len(merge.conflicts) > 0:
		return "git's merge conflicts in " + listPaths(merge.conflicts) + ", which the epic's own change touched", nil
	}
	base, err := gitOutput(ctx, host, merge.repo, "merge-base", merge.tip, merge.origin)
	if err != nil {
		return "", fmt.Errorf("merge base of release tip %s and origin's commit %s: %w", merge.tip, merge.origin, err)
	}
	touched, err := changedPaths(ctx, host, merge.repo, strings.TrimSpace(base), merge.tip)
	if err != nil {
		return "", err
	}
	var hit []string
	for _, change := range ret.OtherChanges {
		if slices.Contains(touched, change.Path) && !slices.Contains(hit, change.Path) {
			hit = append(hit, change.Path)
		}
	}
	if len(hit) > 0 {
		return "the merge changes " + listPaths(hit) + " outside git's merge, which the epic's own change touched", nil
	}
	return "", nil
}

// rereviewOf returns the rereview release run of review, the review of a release reviewer that has not run, as runs
// hold it: the one at review's attempt and position for its reviewer, which the release integrator's round recorded
// when it found that reviewer's earlier verdict may not stand. It reports false when there is none, and the reviewer
// reviews its candidate in full with the plain prompt.
func rereviewOf(runs []store.ReleaseRun, review store.ReleaseRun) (store.ReleaseRun, bool) {
	i := slices.IndexFunc(runs, func(run store.ReleaseRun) bool {
		return run.Part == store.ReleasePartRereview && run.Attempt == review.Attempt &&
			run.Position == review.Position && run.Step == review.Step
	})
	if i < 0 {
		return store.ReleaseRun{}, false
	}
	return runs[i], true
}

// rereviewText returns what a release reviewer that reviews again, as rereview records it, reads after its release
// review prompt: its earlier verdict, why the release integrator did not carry it, and the change from the candidate it
// last read to the candidate it reviews now, which it reads closely beside the whole change.
func rereviewText(rereview store.ReleaseRun) string {
	return fmt.Sprintf(
		"You reviewed an earlier candidate of this release: you %s. Origin's main has moved since, and the release "+
			"merged it again into the same change of the epic's, which made this candidate, %s. Your verdict was not "+
			"carried over: %s. Review the whole change as above, and read closely the change from the candidate you "+
			"reviewed to this one, which is origin's change since then as the merge lands it: `git diff %s %s`.",
		rereview.Evidence, rereview.Head, rereview.Reason, rereview.Start, rereview.Head)
}
