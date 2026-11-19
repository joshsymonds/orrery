package enginehost

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/store"
)

// Resume takes up every running check the store holds, as a restarted orreryd does. A check with a stored grant
// readopts its gate under that grant, and one with no grant runs from the start. Each check is driven in the background
// under a context that Close cancels and waits for, as Run's drives are, and a check this NodeChecks already drives is
// left alone. It returns the errors of the checks it could not take up, joined.
func (n *NodeChecks) Resume(ctx context.Context) error {
	defer n.calls.enter()()
	open, err := n.store.OpenNodeChecks(ctx)
	if err != nil {
		return fmt.Errorf("resume node checks: %w", err)
	}
	var errs []error
	for _, check := range open {
		if err = n.readoptStart(ctx, check); err != nil {
			errs = append(errs, fmt.Errorf("resume node check %s: %w", check.ID, err))
		}
	}
	return errors.Join(errs...)
}

// readoptStart drives check in the background at its epic's repository, as Run starts a new check, under a context that
// Close cancels. The check is read again under mu, so one that a drive ended after Resume listed it is left alone, and
// the drive starts from the stored row. It returns an error when the epic or the check cannot be read or the runner is
// closed.
func (n *NodeChecks) readoptStart(ctx context.Context, check store.NodeCheck) error {
	row, err := n.store.Epic(ctx, check.Epic)
	if err != nil {
		return fmt.Errorf("epic %s: %w", check.Epic, err)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return errNodeChecksClosed
	}
	if _, driven := n.cancels[check.ID]; driven {
		return nil
	}
	current, err := n.store.NodeCheck(ctx, check.ID)
	if err != nil {
		return fmt.Errorf("read node check: %w", err)
	}
	if current.State != store.NodeRunning {
		return nil
	}
	driveCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	n.cancels[check.ID] = cancel
	n.drives.Go(func() {
		n.readoptDrive(driveCtx, row.Repository, current)
		n.finish(check.ID)
	})
	return nil
}

// readoptDrive takes up check at repo. A check with a stored grant readopts its gate, and any other check runs from the
// start.
func (n *NodeChecks) readoptDrive(ctx context.Context, repo string, check store.NodeCheck) {
	if check.Grant == "" {
		n.readoptUnlaunched(ctx, repo, check)
		return
	}
	n.readoptGranted(ctx, repo, check)
}

// readoptUnlaunched runs check from the start when no grant is stored, so its gate never launched. A worktree a restart
// found at its path is what a start stopped before the grant left, so it is removed before the check runs again.
func (n *NodeChecks) readoptUnlaunched(ctx context.Context, repo string, check store.NodeCheck) {
	worktree := filepath.Join(n.worktrees, check.ID)
	if err := removeStepWorktree(context.WithoutCancel(ctx), repo, worktree); err != nil {
		n.logger.WarnContext(ctx, "clear worktree of unlaunched node check", "check", check.ID, "err", err)
		return
	}
	n.drive(ctx, repo, check)
}

// readoptGranted readopts the gate of check under its stored grant, and records its end as drive does (see record). A
// gate that detaches again records nothing, so the next restart readopts it.
func (n *NodeChecks) readoptGranted(ctx context.Context, repo string, check store.NodeCheck) {
	end, err := n.readoptEnd(ctx, repo, check)
	if err != nil {
		n.logger.InfoContext(ctx, "node check left for a restart", "check", check.ID, "err", err)
		return
	}
	n.record(ctx, check.ID, end)
}

// readoptEnd readopts the gate of check under its stored grant and returns how the check ended. A gate that cannot be
// readopted ends lost. It returns an error matching errGateDetach when the gate detaches again, and an error when ctx
// ends before the gate is touched, so the check is left running with its holder for a restart.
func (n *NodeChecks) readoptEnd(ctx context.Context, repo string, check store.NodeCheck) (store.NodeCheckEnd, error) {
	worktree := filepath.Join(n.worktrees, check.ID)
	req, err := n.readoptRequest(ctx, repo, worktree, check)
	if err != nil {
		if ctx.Err() != nil {
			return store.NodeCheckEnd{}, fmt.Errorf("readopt node check %s: %w", check.ID, err)
		}
		return n.readoptLost(ctx, repo, worktree, check, err), nil
	}
	result, err := readoptGrantedGate(ctx, n.pools, req, n.gate(worktree, check, nil), check.Grant)
	switch {
	case errors.Is(err, errGateDetach):
		// The gate keeps running under its holder in worktree, so neither is removed: a restart readopts it again.
		return store.NodeCheckEnd{}, err
	case err != nil:
		return n.readoptLost(ctx, repo, worktree, check, err), nil
	}
	return n.end(ctx, repo, worktree, &result, ""), nil
}

// readoptRequest re-adds check's worktree when a restart removed it, and returns the grant request the check queues in
// it.
func (n *NodeChecks) readoptRequest(
	ctx context.Context, repo, worktree string, check store.NodeCheck,
) (grants.Request, error) {
	if err := captureEnsureWorktree(ctx, repo, check.Revision, worktree); err != nil {
		return grants.Request{}, err
	}
	return n.request(ctx, worktree, check)
}

// readoptLost ends check, whose gate cannot be readopted because of cause, as lost at a restart. Nothing reads its
// holder again, so the holder is stopped and cleared before the worktree is removed. A failure to clear it is kept as
// not finished.
func (n *NodeChecks) readoptLost(
	ctx context.Context, repo, worktree string, check store.NodeCheck, cause error,
) store.NodeCheckEnd {
	n.logger.WarnContext(ctx, "node check lost at a restart", "check", check.ID, "err", cause)
	clearErr := gateClear(ctx, check.Holder)
	ended := n.end(ctx, repo, worktree, nil, store.LostAtRestart)
	if clearErr != nil {
		ended.NotRun += "; " + captureNotFinished(clearErr)
	}
	return ended
}
