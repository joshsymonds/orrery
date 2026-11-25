package interpret

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/compose"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// passAmendments drives each open evidence amendment of the epics a pass read, before their flows are decided: it starts
// the work the amendment's state waits on, under the ids the amendment names, and then settles the amendment by that
// work. Both are repeatable, so a restart that finds an amendment checking or capturing starts nothing twice: a run or
// check stored under its id is that work, and a settle whose work has not ended writes nothing. An amendment of an epic
// the pass did not read waits for a later pass. A failure is logged, and the next pass drives the amendment again.
func (r *Runner) passAmendments(ctx context.Context, epics []store.EpicRow) {
	amendments, err := r.store.OpenAmendments(ctx)
	if err != nil {
		r.logger.ErrorContext(ctx, "list open evidence amendments", "err", err)
		return
	}
	read := make(map[string]store.EpicRow, len(epics))
	for _, epic := range epics {
		read[epic.ID] = epic
	}
	for _, amended := range amendments {
		if ctx.Err() != nil {
			return
		}
		epic, ok := read[amended.Epic]
		if !ok {
			continue
		}
		if err = r.driveAmendment(ctx, epic, amended); err != nil {
			r.logger.ErrorContext(ctx, "drive evidence amendment", "epic", amended.Epic, "amendment", amended.Seq,
				"err", err)
		}
	}
}

// driveAmendment starts the work of amended and settles it. A checking amendment runs the red run of its new item at
// the epic's base. A capturing one runs the green run of its new item at the stored base it names, and for a test item
// its reproduction (see amendmentRepro). Work the evidence runner refuses for good stops the amendment with the refusal
// (see store.Store.StopAmendment). Otherwise the store settles the amendment by its work (settleAmendment), and an
// amendment the store defers while an evidence, integrate or sync run of its epic is under way is settled again on a
// later pass.
func (r *Runner) driveAmendment(ctx context.Context, epic store.EpicRow, amended store.EvidenceAmendment) error {
	if r.evidence == nil {
		return missingService("evidence")
	}
	id, revision, phase := amended.RedRun, amended.Base, evidence.PhaseRed
	if amended.State == store.AmendmentCapturing {
		id, revision, phase = amended.GreenRun, amended.StoredBase, evidence.PhaseGreen
	}
	_, err := r.evidence.RunAmendment(ctx, id, epic.ID, amended.Requirement, amended.Item, revision, phase)
	if errors.Is(err, enginehost.ErrEvidenceRefused) {
		if stopErr := r.store.StopAmendment(ctx, epic.ID, amended.Seq, err.Error(), r.now()); stopErr != nil {
			return fmt.Errorf("stop amendment %d: %w", amended.Seq, stopErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("run %s of amendment %d: %w", phase, amended.Seq, err)
	}
	if amended.State == store.AmendmentCapturing && amended.ReproCheck != "" {
		if reproErr := r.amendmentRepro(ctx, epic, amended); reproErr != nil {
			return reproErr
		}
	}
	return r.settleAmendment(ctx, epic, amended)
}

// settleAmendment settles amended by its work (store.Store.SettleAmendment). While the store waits for the ruling of
// the judges of its reproduction (store.ErrAmendmentJudging), the judge is carried on (amendmentJudge), and the
// amendment is settled again whenever a ruling is recorded, at most once for each judge a reproduction gets, so a
// ruling that settles the reproduction applies the amendment in the same pass. A settle the store defers waits for a
// later pass.
func (r *Runner) settleAmendment(ctx context.Context, epic store.EpicRow, amended store.EvidenceAmendment) error {
	for range store.ReproJudges + 1 {
		err := r.store.SettleAmendment(ctx, epic.ID, amended.Seq, r.now())
		switch {
		case err == nil, errors.Is(err, store.ErrAmendmentDeferred):
			return nil
		case !errors.Is(err, store.ErrAmendmentJudging):
			return fmt.Errorf("settle amendment %d: %w", amended.Seq, err)
		}
		ruled, err := r.amendmentJudge(ctx, epic, amended)
		if err != nil || !ruled {
			return err
		}
	}
	return nil
}

// amendmentJudge carries on the judge of the reproduction of amended, a capturing amendment whose reproduction ran and
// exited 0 without timing out and whose rulings do not settle it yet. The judge numbered by how many rulings the
// reproduction has decides, under its derived id (store.AmendmentJudgeID), labeled with the amendment
// (amendmentLabel): one with no step starts (judgeReproduction), one whose step runs waits, and one whose step has ended
// has its ruling recorded (recordJudgeRuling). It reports whether it recorded a ruling.
func (r *Runner) amendmentJudge(
	ctx context.Context,
	epic store.EpicRow,
	amended store.EvidenceAmendment,
) (bool, error) {
	rulings, err := r.store.ReproRulings(ctx, epic.ID)
	if err != nil {
		return false, fmt.Errorf("read the rulings of amendment %d: %w", amended.Seq, err)
	}
	ruling := store.ReproRuling{
		Check: amended.ReproCheck, Judge: int64(len(store.CheckRulings(rulings, amended.ReproCheck))),
	}
	id := store.AmendmentJudgeID(amended, ruling.Judge)
	labeled, err := r.store.LabelledSteps(ctx, epic.ID, amendmentLabel(amended))
	if err != nil {
		return false, fmt.Errorf("read the judge steps of amendment %d: %w", amended.Seq, err)
	}
	if i := slices.IndexFunc(labeled, func(row store.StepRow) bool { return row.ID == id }); i >= 0 {
		if labeled[i].End == "" {
			return false, nil
		}
		return true, r.recordJudgeRuling(ctx, epic, ruling, labeled[i], r.now())
	}
	judged, err := r.amendmentJudgment(ctx, epic, amended)
	if err != nil {
		return false, err
	}
	return r.judgeReproduction(ctx, epic, judged, ruling, id, r.now())
}

// amendmentJudgment returns what the judge of amended's reproduction rules on: the reproduction the amendment ran at the
// epic's base with the test files of the stored base it names, for the item it asks for; the epic's tasks that claim or
// contribute to its Requirement and that an integrate run that ended passed composed, since the stored base holds
// their change; and the Requirement's red at the epic's base. Its step is labeled with the amendment. A reproduction
// that is not stored is an error.
func (r *Runner) amendmentJudgment(
	ctx context.Context, epic store.EpicRow, amended store.EvidenceAmendment,
) (reproJudgment, error) {
	flow, err := r.store.EpicFlow(ctx, epic.ID)
	if err != nil {
		return reproJudgment{}, fmt.Errorf("read epic %s: %w", epic.ID, err)
	}
	i := slices.IndexFunc(flow.Checks, func(check store.NodeCheck) bool { return check.ID == amended.ReproCheck })
	if i < 0 {
		return reproJudgment{}, fmt.Errorf("reproduction %s of amendment %d is not stored", amended.ReproCheck,
			amended.Seq)
	}
	tasks := claimingTasks(flow.Epic.Tasks, amended.Requirement, func(task store.EpicTask) bool {
		return slices.ContainsFunc(flow.Runs, func(run store.NodeRun) bool {
			return run.Kind == kindIntegrate && run.Path == task.Batch && run.Outcome == outcomePassed &&
				composedBy(run, task.Seq)
		})
	})
	return reproJudgment{
		requirement: store.EpicRequirement{ID: amended.Requirement, Item: amended.Item}, tasks: tasks,
		red: baseRed(flow.Evidence, amended.Requirement, amended.Base), repro: flow.Checks[i], base: amended.Base,
		commit: amended.StoredBase, label: amendmentLabel(amended),
	}, nil
}

// amendmentRepro starts the reproduction of a capturing amendment's test item, once: the epic's base with the item's test
// files taken from the stored base the amendment names, pinned under the repro ref of its check, where the item's capture
// runs as a check under the check id the amendment names, as an integrate run reproduces a test item at its candidate (see
// Runner.repro). A check stored under that id already is that reproduction, so nothing more starts.
func (r *Runner) amendmentRepro(ctx context.Context, epic store.EpicRow, amended store.EvidenceAmendment) error {
	if r.checks == nil {
		return missingService("checks")
	}
	_, err := r.store.NodeCheck(ctx, amended.ReproCheck)
	if err == nil {
		return nil
	}
	if !errors.Is(err, store.ErrNoNodeCheck) {
		return fmt.Errorf("read reproduction of amendment %d: %w", amended.Seq, err)
	}
	ref := fmt.Sprintf("refs/orrery/%s/repro/%s", build.Slug(epic.ID), amended.ReproCheck)
	host, err := r.hostOf(epic)
	if err != nil {
		return fmt.Errorf("make reproduction of amendment %d: %w", amended.Seq, err)
	}
	overlay, err := compose.Overlay(
		ctx, host, epic.Repository, amended.Base, amended.StoredBase, amended.Item.Tests, ref,
	)
	if err != nil {
		return fmt.Errorf("make reproduction of amendment %d: %w", amended.Seq, err)
	}
	check := store.NodeCheck{
		ID: amended.ReproCheck, Epic: epic.ID, Path: amendmentLabel(amended), Revision: overlay,
		Argv: amended.Item.Capture, Limit: amended.Item.Limit,
	}
	if _, err = r.checks.Run(ctx, check); err != nil {
		return fmt.Errorf("run reproduction of amendment %d: %w", amended.Seq, err)
	}
	return nil
}
