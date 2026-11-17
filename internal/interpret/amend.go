package interpret

import (
	"context"
	"errors"
	"fmt"

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

// driveAmendment starts the work of amended and settles it. A checking amendment runs the red run of its new item at the
// epic's base. A capturing one runs the green run of its new item at the stored base it names, and for a test item its
// reproduction (see amendmentRepro). Work the evidence runner refuses for good stops the amendment with the refusal (see
// store.Store.StopAmendment). Otherwise the store settles the amendment by its work, and an amendment the store defers
// while an evidence or integrate run of its epic is under way is settled again on a later pass.
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
	err = r.store.SettleAmendment(ctx, epic.ID, amended.Seq, r.now())
	if err != nil && !errors.Is(err, store.ErrAmendmentDeferred) {
		return fmt.Errorf("settle amendment %d: %w", amended.Seq, err)
	}
	return nil
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
	overlay, err := compose.Overlay(ctx, epic.Repository, amended.Base, amended.StoredBase, amended.Item.Tests, ref)
	if err != nil {
		return fmt.Errorf("make reproduction of amendment %d: %w", amended.Seq, err)
	}
	check := store.NodeCheck{
		ID: amended.ReproCheck, Epic: epic.ID, Path: fmt.Sprintf("amendment A%d", amended.Seq), Revision: overlay,
		Argv: amended.Item.Capture, Limit: amended.Item.Limit,
	}
	if _, err = r.checks.Run(ctx, check); err != nil {
		return fmt.Errorf("run reproduction of amendment %d: %w", amended.Seq, err)
	}
	return nil
}
