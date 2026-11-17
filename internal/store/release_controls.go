package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// releaseUndoNone is the undo of a release step that has none, as the contract writes it in any case and with any
// surrounding space. Once such a step has failed, the owner must name it to run it again.
const releaseUndoNone = "none"

// phaseEndedWithGaps is the phase an epic ends in when its review leaves a gap, and releaseEndedWithGapsID is the id of
// the owner decision that releases such an epic.
const (
	phaseEndedWithGaps     = "ended_with_gaps"
	releaseEndedWithGapsID = "release-ended-with-gaps"
)

// isUndoNone reports whether undo is releaseUndoNone, trimmed and in any case, as the release kind reads it.
func isUndoNone(undo string) bool {
	return strings.EqualFold(strings.TrimSpace(undo), releaseUndoNone)
}

// ReleaseEpic releases epic in one write, and returns an error matching ErrNoEpic for an id no epic has. When epic ended
// with gaps it records the owner decision that releases it and moves it to clean, so its release run takes the release
// slot of its repository and no tell is added; it is a RefusalError when undoNone is not empty, as such an epic has no
// release run to name a step of. Otherwise it resumes the stopped release of epic at its first unfinished release step.
// That is a RefusalError when epic is not stopped, when epic has no parked release node run, when another epic holds the
// release slot of its repository, when a position in undoNone is not an undo-none release step, and when the first
// unfinished step is undo-none, has failed and is not in undoNone. Otherwise it takes the release slot when it is free,
// stores a named release run for each distinct position in undoNone for the release node run's next attempt, records one
// owner decision per such position, moves the release node run to that attempt and unparks epic as RetryNode does, moves
// epic from stopped to releasing, and adds one tell that says where the release resumed.
func (s *Store) ReleaseEpic(ctx context.Context, epic string, undoNone []int64, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		return resumeRelease(ctx, queries, epic, undoNone, at)
	})
}

// releaseResume is the state a stopped release resumes from: its parked release node run, its release steps, the
// distinct positions the owner named in position order, and its first unfinished step, or 0 when every step has passed.
type releaseResume struct {
	parked     NodeRun
	steps      []EpicReleaseStep
	named      []int64
	unfinished int64
}

// resumeRelease releases epic in the caller's write, as ReleaseEpic does.
func resumeRelease(
	ctx context.Context, queries *sqlcgen.Queries, epic string, undoNone []int64, at time.Time,
) error {
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return err
	}
	if head.Phase == phaseEndedWithGaps {
		return releaseEndedWithGaps(ctx, queries, head.ID, undoNone, at)
	}
	return resumeStoppedRelease(ctx, queries, head, undoNone, at)
}

// resumeReleaseByPlanner resumes the stopped release of epic for a planner's resume_release action, in the caller's
// write. It runs only for an epic in phase stopped that already holds its repository's release slot: it never takes a
// free slot, so a release the owner stopped, which freed the slot, stays with the owner. It refuses any undo-none step
// named, since only the owner names one. Otherwise it resumes the release as ReleaseEpic does.
func resumeReleaseByPlanner(
	ctx context.Context, queries *sqlcgen.Queries, epic string, undoNone []int64, at time.Time,
) error {
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return err
	}
	if head.Phase != PhaseStopped {
		return RefusalError(fmt.Sprintf("epic %s is not stopped", epic))
	}
	if len(undoNone) > 0 {
		return RefusalError(
			fmt.Sprintf("epic %s: a planner resume names no undo-none step; only the owner names one", epic))
	}
	holder, err := releaseSlotHolder(ctx, queries, head.Repository)
	if err != nil {
		return err
	}
	if holder != epic {
		return RefusalError(fmt.Sprintf("epic %s does not hold the release slot of %s", epic, head.Repository))
	}
	return resumeStoppedRelease(ctx, queries, head, undoNone, at)
}

// resumeStoppedRelease resumes the stopped release of head in the caller's write, as ReleaseEpic does for an epic that is
// not ended with gaps: it takes the release slot, stores the named releases, moves the release node run to its next
// attempt and unparks epic, and moves epic to releasing with the tell that says where it resumed.
func resumeStoppedRelease(
	ctx context.Context, queries *sqlcgen.Queries, head sqlcgen.Epic, undoNone []int64, at time.Time,
) error {
	epic := head.ID
	resume, err := checkReleaseResume(ctx, queries, head, undoNone)
	if err != nil {
		return err
	}
	err = queries.InsertEpicReleaseSlot(ctx, sqlcgen.InsertEpicReleaseSlotParams{
		Repository: head.Repository, Epic: epic,
	})
	if err != nil {
		return fmt.Errorf("take release slot of epic %s: %w", epic, err)
	}
	err = recordNamedReleases(ctx, queries, epic, resume, at)
	if err != nil {
		return err
	}
	err = retryNode(ctx, queries, epic, resume.parked.Node)
	if err != nil {
		return err
	}
	err = queries.ResumeEpicRelease(ctx, sqlcgen.ResumeEpicReleaseParams{PhaseAt: at.Unix(), ID: epic})
	if err != nil {
		return fmt.Errorf("move epic %s to releasing: %w", epic, err)
	}
	return insertTell(ctx, queries, epic, resumedReleaseText(resume.steps, resume.unfinished), at)
}

// releaseEndedWithGaps releases epic, which ended with gaps, in the caller's write: it records the owner's decision and
// moves epic to clean, so its release run takes the release slot of its repository. It is a RefusalError when undoNone
// names any position, since epic has not started a release.
func releaseEndedWithGaps(
	ctx context.Context, queries *sqlcgen.Queries, epic string, undoNone []int64, at time.Time,
) error {
	if len(undoNone) > 0 {
		return RefusalError(fmt.Sprintf("epic %s has not started a release; name no undo-none step", epic))
	}
	text := fmt.Sprintf("owner released epic %s, which ended with gaps", epic)
	if _, err := appendDecision(ctx, queries, epic, releaseEndedWithGapsID, text, 0, at); err != nil {
		return err
	}
	return setEpicPhase(ctx, queries, epic, PhaseClean, at)
}

// checkReleaseResume returns the state the stopped release of head resumes from. It is a RefusalError when head is not
// stopped, when it has no parked release node run, when another epic holds its repository's release slot, when a
// position in undoNone is not an undo-none release step, and when its first unfinished step is undo-none, has failed and
// is not in undoNone.
func checkReleaseResume(
	ctx context.Context, queries *sqlcgen.Queries, head sqlcgen.Epic, undoNone []int64,
) (releaseResume, error) {
	epic := head.ID
	if head.Phase != PhaseStopped {
		return releaseResume{}, RefusalError(fmt.Sprintf("epic %s is not stopped", epic))
	}
	parked, err := parkedReleaseRun(ctx, queries, epic)
	if err != nil {
		return releaseResume{}, err
	}
	holder, err := releaseSlotHolder(ctx, queries, head.Repository)
	if err != nil {
		return releaseResume{}, err
	}
	if holder != "" && holder != epic {
		return releaseResume{}, RefusalError(
			fmt.Sprintf("epic %s holds the release slot of %s", holder, head.Repository))
	}
	steps, err := releaseStepsOf(ctx, queries, epic)
	if err != nil {
		return releaseResume{}, err
	}
	named, err := namedUndoNone(epic, steps, undoNone)
	if err != nil {
		return releaseResume{}, err
	}
	runs, err := readReleaseRuns(ctx, queries, epic)
	if err != nil {
		return releaseResume{}, err
	}
	unfinished := unfinishedReleaseStep(runs, len(steps))
	if unfinished > 0 && isUndoNone(steps[unfinished-1].Undo) &&
		releaseStepRun(runs, unfinished, false) && !slices.Contains(named, unfinished) {
		return releaseResume{}, RefusalError(fmt.Sprintf(
			"release step %d of epic %s is undo-none and failed; name it to run it again", unfinished, epic))
	}
	return releaseResume{parked: parked, steps: steps, named: named, unfinished: unfinished}, nil
}

// recordNamedReleases stores, in the caller's write, the named release run and the owner decision of each named
// position of resume, for the next attempt of its parked release node run.
func recordNamedReleases(
	ctx context.Context, queries *sqlcgen.Queries, epic string, resume releaseResume, at time.Time,
) error {
	attempt := resume.parked.Attempt + 1
	for _, position := range resume.named {
		step := resume.steps[position-1].Step
		if err := recordRelease(ctx, queries, epic, ReleaseRun{
			Part: ReleasePartNamed, Position: position, Attempt: attempt, Step: step,
			Reason: "owner named it", At: at,
		}); err != nil {
			return err
		}
		decision := fmt.Sprintf("owner named undo-none release step %d, %s, to run it again", position, step)
		id := fmt.Sprintf("release-undo-none-%d-%d", position, attempt)
		if _, err := appendDecision(ctx, queries, epic, id, decision, 0, at); err != nil {
			return err
		}
	}
	return nil
}

// parkedReleaseRun returns the parked release node run of epic, or a RefusalError when it has none.
func parkedReleaseRun(ctx context.Context, queries *sqlcgen.Queries, epic string) (NodeRun, error) {
	runs, err := readNodeRuns(ctx, queries, epic)
	if err != nil {
		return NodeRun{}, err
	}
	for _, run := range runs {
		if run.Kind == releaseKind && run.State == NodeParked {
			return run, nil
		}
	}
	return NodeRun{}, RefusalError(fmt.Sprintf("epic %s has no parked release run", epic))
}

// releaseStepsOf returns the release steps of epic in position order, numbered from 1 by position.
func releaseStepsOf(ctx context.Context, queries *sqlcgen.Queries, epic string) ([]EpicReleaseStep, error) {
	stored, err := queries.ListEpicReleaseSteps(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list release steps of epic %s: %w", epic, err)
	}
	return mapInOrder(stored, func(r sqlcgen.EpicReleaseStep) EpicReleaseStep {
		return EpicReleaseStep{Step: r.Step, Target: r.Target, Undo: r.Undo}
	}), nil
}

// namedUndoNone returns the distinct positions in undoNone, in the order given, after checking that each is an undo-none
// release step of steps. A position that is not one is a RefusalError.
func namedUndoNone(epic string, steps []EpicReleaseStep, undoNone []int64) ([]int64, error) {
	var named []int64
	for _, position := range undoNone {
		if position < 1 || position > int64(len(steps)) || !isUndoNone(steps[position-1].Undo) {
			return nil, RefusalError(fmt.Sprintf(
				"position %d of epic %s is not an undo-none release step", position, epic))
		}
		if !slices.Contains(named, position) {
			named = append(named, position)
		}
	}
	return named, nil
}

// unfinishedReleaseStep returns the lowest position among the n release steps that has no passed release run in runs, or
// 0 when every step has passed.
func unfinishedReleaseStep(runs []ReleaseRun, n int) int64 {
	for position := int64(1); position <= int64(n); position++ {
		if !releaseStepRun(runs, position, true) {
			return position
		}
	}
	return 0
}

// releaseStepRun reports whether runs has a release run of the step at position whose Passed is passed.
func releaseStepRun(runs []ReleaseRun, position int64, passed bool) bool {
	for _, run := range runs {
		if run.Part == ReleasePartStep && run.Position == position && run.Passed == passed {
			return true
		}
	}
	return false
}

// resumedReleaseText says where a resumed release picks up: at its unfinished step, or at the judge when every step
// has passed.
func resumedReleaseText(steps []EpicReleaseStep, unfinished int64) string {
	if unfinished == 0 {
		return "release resumed at the judge"
	}
	return fmt.Sprintf("release resumed at step %d, %s", unfinished, steps[unfinished-1].Step)
}

// stopReleaseSlot is StopEpic's case for an epic with no running turn. An epic that holds its repository's release slot
// frees it in the caller's write and returns the id of the first release step of its release node runs that has no end,
// or "" when none does. Only the steps of each run's current attempt are read. An epic that holds no slot is a
// RefusalError.
func stopReleaseSlot(ctx context.Context, queries *sqlcgen.Queries, head sqlcgen.Epic) (string, error) {
	holder, err := releaseSlotHolder(ctx, queries, head.Repository)
	if err != nil {
		return "", err
	}
	if holder != head.ID {
		return "", RefusalError(fmt.Sprintf("epic %s has no running turn", head.ID))
	}
	open, err := openReleaseStep(ctx, queries, head.ID)
	if err != nil {
		return "", err
	}
	if err = queries.FreeEpicReleaseSlot(ctx, head.ID); err != nil {
		return "", fmt.Errorf("free release slot of epic %s: %w", head.ID, err)
	}
	return open, nil
}

// openReleaseStep returns the id of the first step, in the order EpicFlow reads them, of epic's release node runs that has
// no end, or "" when none does: a release step or the judge at the run's current attempt, or a round of the release
// integrator, which the step carries the run's path as its label for.
func openReleaseStep(ctx context.Context, queries *sqlcgen.Queries, epic string) (string, error) {
	runs, err := readNodeRuns(ctx, queries, epic)
	if err != nil {
		return "", err
	}
	var releases []NodeRun
	for _, run := range runs {
		if run.Kind == releaseKind {
			releases = append(releases, run)
		}
	}
	steps, err := releaseStepsOf(ctx, queries, epic)
	if err != nil {
		return "", err
	}
	rows, err := readRunSteps(ctx, queries, releases, len(steps))
	if err != nil {
		return "", err
	}
	integrators, err := readLabeledSteps(ctx, queries, epic, releases)
	if err != nil {
		return "", err
	}
	rows = append(rows, integrators...)
	for _, row := range rows {
		if row.End == "" {
			return row.ID, nil
		}
	}
	return "", nil
}
