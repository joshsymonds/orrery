package enginehost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/store"
)

// errNodeChecksClosed is what Run returns once Close has been called.
var errNodeChecksClosed = errors.New("node checks runner is closed")

// NodeCheckStore is the store NodeChecks records its checks in. *store.Store satisfies it. OpenNodeChecks lists the
// checks a restart takes up.
type NodeCheckStore interface {
	Epic(ctx context.Context, id string) (store.EpicRow, error)
	AddNodeCheck(ctx context.Context, check store.NodeCheck, at time.Time) error
	NodeCheck(ctx context.Context, id string) (store.NodeCheck, error)
	OpenNodeChecks(ctx context.Context) ([]store.NodeCheck, error)
	SetNodeCheckGrant(ctx context.Context, id, grant, holder string) error
	EndNodeCheck(ctx context.Context, id string, end store.NodeCheckEnd, at time.Time) error
}

// NodeChecksConfig configures a NodeChecks. Store records the checks, and Pools, Environ, GateStateDir and GateScope run each
// check's command as an evidence capture runs its gate. StateDir holds the check worktrees. A nil Now is time.Now, and a
// nil Logger discards.
type NodeChecksConfig struct {
	Store        NodeCheckStore
	Pools        PoolCommands
	Environ      []string
	StateDir     string
	GateStateDir string
	GateScope    string
	Now          func() time.Time
	Logger       *slog.Logger
}

// NodeChecks runs gate and command checks. Each check is stored once under its id, then driven in the background by a
// drive that Run starts. cancels holds the cancel of each drive still under way, by check id, and Close cancels them all.
type NodeChecks struct {
	store        NodeCheckStore
	pools        PoolCommands
	environ      []string
	worktrees    string
	gateStateDir string
	gateScope    string
	now          func() time.Time
	logger       *slog.Logger

	mu      sync.Mutex
	closed  bool
	cancels map[string]context.CancelCauseFunc
	drives  sync.WaitGroup
}

// NewNodeChecks returns a NodeChecks for cfg. Its check worktrees are kept under StateDir/node-check-worktrees.
func NewNodeChecks(cfg NodeChecksConfig) *NodeChecks {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &NodeChecks{
		store:        cfg.Store,
		pools:        cfg.Pools,
		environ:      cfg.Environ,
		worktrees:    filepath.Join(cfg.StateDir, "node-check-worktrees"),
		gateStateDir: cfg.GateStateDir,
		gateScope:    cfg.GateScope,
		now:          now,
		logger:       logger,
		cancels:      map[string]context.CancelCauseFunc{},
	}
}

// Run stores check, unless its id is stored already, and drives it in the background. It returns the stored check. A repeat
// under the same id returns the stored check and runs nothing. A check whose NotRun is set is stored ended and runs nothing.
// Run refuses an id that is not 32 lowercase hex digits, before anything is stored, since the id names the worktree.
func (n *NodeChecks) Run(ctx context.Context, check store.NodeCheck) (store.NodeCheck, error) {
	if err := checkID(check.ID); err != nil {
		return store.NodeCheck{}, fmt.Errorf("node check %q: %w", check.ID, err)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return store.NodeCheck{}, errNodeChecksClosed
	}
	stored, err := n.store.NodeCheck(ctx, check.ID)
	switch {
	case err == nil:
		return stored, nil
	case !errors.Is(err, store.ErrNoNodeCheck):
		return store.NodeCheck{}, fmt.Errorf("read node check %s: %w", check.ID, err)
	}
	if check.NotRun != "" {
		return n.add(ctx, check)
	}
	row, err := n.store.Epic(ctx, check.Epic)
	if err != nil {
		return store.NodeCheck{}, fmt.Errorf("node check %s of epic %s: %w", check.ID, check.Epic, err)
	}
	stored, err = n.add(ctx, check)
	if err != nil {
		return store.NodeCheck{}, err
	}
	n.start(ctx, row.Repository, stored)
	return stored, nil
}

// add stores check, which a stored check with its id keeps as it is, and returns the stored check.
func (n *NodeChecks) add(ctx context.Context, check store.NodeCheck) (store.NodeCheck, error) {
	if err := n.store.AddNodeCheck(ctx, check, n.now()); err != nil {
		return store.NodeCheck{}, fmt.Errorf("add node check %s: %w", check.ID, err)
	}
	stored, err := n.store.NodeCheck(ctx, check.ID)
	if err != nil {
		return store.NodeCheck{}, fmt.Errorf("read node check %s: %w", check.ID, err)
	}
	return stored, nil
}

// start drives check at repo in the background, under a context that Close cancels, with errGateDetach under a gate scope,
// and that finish cancels with nil. The caller holds mu.
func (n *NodeChecks) start(ctx context.Context, repo string, check store.NodeCheck) {
	driveCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	n.cancels[check.ID] = cancel
	n.drives.Go(func() {
		n.drive(driveCtx, repo, check)
		n.finish(check.ID)
	})
}

// finish cancels the drive of check id with nil and forgets it, once the drive has returned.
func (n *NodeChecks) finish(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cancels[id](nil)
	delete(n.cancels, id)
}

// drive runs check to its end and records it, even when ctx has ended. A run that detaches records nothing, and neither does
// one cut short before its gate launched, so a restart runs the check again.
func (n *NodeChecks) drive(ctx context.Context, repo string, check store.NodeCheck) {
	end, err := n.run(ctx, repo, check)
	if err != nil {
		n.logger.InfoContext(ctx, "node check left for a restart", "check", check.ID, "err", err)
		return
	}
	if err = n.store.EndNodeCheck(context.WithoutCancel(ctx), check.ID, end, n.now()); err != nil {
		n.logger.ErrorContext(ctx, "end node check", "check", check.ID, "err", err)
	}
}

// run runs check at repo's revision in a detached worktree, under a grant of its needs, and returns how it ended. The grant
// and holder are recorded once the grant holds, before the gate launches. It returns an error when the gate detaches, and
// when ctx ends before the gate launched.
func (n *NodeChecks) run(ctx context.Context, repo string, check store.NodeCheck) (store.NodeCheckEnd, error) {
	worktree := filepath.Join(n.worktrees, check.ID)
	if err := addStepWorktree(ctx, repo, worktree, check.Revision); err != nil {
		return n.notStarted(ctx, repo, worktree, check.ID, err)
	}
	req, err := n.request(ctx, worktree, check)
	if err != nil {
		return n.notStarted(ctx, repo, worktree, check.ID, err)
	}
	// launched is set once the grant holds and is recorded. From then the gate may run, so a gate that ends with its context
	// did not finish, where one that never launched did not start.
	launched := false
	gate := n.gate(worktree, check, func(gateCtx context.Context, grant, holder string) error {
		if recordErr := n.store.SetNodeCheckGrant(gateCtx, check.ID, grant, holder); recordErr != nil {
			return fmt.Errorf("record grant of node check %s: %w", check.ID, recordErr)
		}
		launched = true
		return nil
	})
	result, err := runGrantedGate(ctx, n.pools, req, gate)
	if errors.Is(err, errGateDetach) {
		// The gate keeps running under its holder in worktree, so neither is removed: a restart re-adopts the gate.
		return store.NodeCheckEnd{}, err
	}
	if err != nil {
		if !launched && ctx.Err() != nil {
			// Nothing ran, so the worktree is removed and the check is left for a restart to run.
			cut := fmt.Errorf("run node check %s: %w", check.ID, ctx.Err())
			return store.NodeCheckEnd{}, errors.Join(
				cut, removeStepWorktree(context.WithoutCancel(ctx), repo, worktree),
			)
		}
		reason := captureNotStarted(err)
		if launched && ctx.Err() != nil {
			reason = captureNotFinished(err)
		}
		return n.end(ctx, repo, worktree, nil, reason), nil
	}
	return n.end(ctx, repo, worktree, &result, ""), nil
}

// notStarted ends the check with id as not started for err, before its gate launched. When ctx has ended, nothing ran and
// err may be the cut itself, so the worktree is removed, nothing is recorded, and the check is left for a restart to run.
func (n *NodeChecks) notStarted(
	ctx context.Context, repo, worktree, id string, err error,
) (store.NodeCheckEnd, error) {
	if ctx.Err() != nil {
		cut := fmt.Errorf("run node check %s: %w", id, ctx.Err())
		return store.NodeCheckEnd{}, errors.Join(cut, removeStepWorktree(context.WithoutCancel(ctx), repo, worktree))
	}
	return n.end(ctx, repo, worktree, nil, captureNotStarted(err)), nil
}

// request returns the grant request check queues in worktree: the pools its needs name, or one member of the default gate
// pool when it names none.
func (n *NodeChecks) request(ctx context.Context, worktree string, check store.NodeCheck) (grants.Request, error) {
	needs := []grants.Need{{Pool: gates.DefaultPool, Count: 1}}
	if len(check.Needs) > 0 {
		needs = make([]grants.Need, 0, len(check.Needs))
		for _, need := range check.Needs {
			needs = append(needs, grants.Need{Pool: need.Pool, Count: int(need.Count)})
		}
	}
	return stepGrant(ctx, worktree, "node-check-"+check.ID, check.Argv, needs, stepGetenv(n.environ))
}

// gate returns the gate that runs check in worktree. Its limit is the check's limit in seconds, and it keeps the newest
// store.NodeOutputMax bytes of each of its stdout and stderr, which is all the store keeps.
func (n *NodeChecks) gate(
	worktree string, check store.NodeCheck, started func(ctx context.Context, grant, holder string) error,
) gateRun {
	return gateRun{
		Argv:     check.Argv,
		Dir:      worktree,
		Limit:    time.Duration(check.Limit) * time.Second,
		Environ:  n.environ,
		Name:     "node-check-" + check.ID,
		StateDir: n.gateStateDir,
		Scope:    n.gateScope,
		Started:  started,
		Tail:     store.NodeOutputMax,
	}
}

// end removes worktree and clears holder on every path, and returns how the check ended: the outcome of result when its gate
// ran to one, else reason as the reason it did not run. A failure to clean up is added to NotRun, so the check is never
// passed on a clean-up that failed.
func (n *NodeChecks) end(
	ctx context.Context, repo, worktree string, result *gateResult, reason string,
) store.NodeCheckEnd {
	ended := store.NodeCheckEnd{NotRun: reason}
	holder := ""
	if result != nil {
		holder = result.Holder
		ended.Exit = result.Exit
		ended.TimedOut = result.TimedOut
		ended.Stdout = result.Stdout
		ended.Stderr = result.Stderr
	}
	cleanErr := errors.Join(
		removeStepWorktree(context.WithoutCancel(ctx), repo, worktree),
		gateClear(ctx, holder),
	)
	if cleanErr != nil {
		finished := captureNotFinished(cleanErr)
		if ended.NotRun != "" {
			finished = ended.NotRun + "; " + finished
		}
		ended.NotRun = finished
	}
	return ended
}

// Close cancels every drive and waits for each to return. Under a gate scope each drive is canceled with errGateDetach,
// which leaves its check's gate running under its holder for a restarted orreryd to re-adopt. Without one the cancel kills
// the gate. Run returns an error from then on.
func (n *NodeChecks) Close() {
	n.mu.Lock()
	n.closed = true
	var cause error
	if n.gateScope != "" {
		cause = errGateDetach
	}
	for _, cancel := range n.cancels {
		cancel(cause)
	}
	n.mu.Unlock()
	n.drives.Wait()
}
