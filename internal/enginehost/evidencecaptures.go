package enginehost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/redact"
	"github.com/joshsymonds/orrery/internal/store"
)

// ErrCaptureRefused reports a bad request to capture evidence. Capture wraps it with the reason.
var ErrCaptureRefused = errors.New("evidence capture is refused")

// ErrCaptureNotStep reports that the caller of a capture is not a running step: it has no step, or its step has ended.
// Capture wraps it with ErrCaptureRefused, so the Evidence service answers that refusal apart from the others.
var ErrCaptureNotStep = errors.New("caller is not a running step")

// errCapturesClosed is what Capture returns once Close has been called.
var errCapturesClosed = errors.New("capture runner is closed")

// captureDeliverRetry is how long a capture's end waits before it is delivered again to a step that is busy or running its
// gate.
const captureDeliverRetry = 5 * time.Second

// captureBlockLines is how many of the newest lines of each log the block that tells a step of a capture's end shows.
const captureBlockLines = 200

// captureEventPrefix heads the PromptID of a capture's end: the prefix and the capture id.
const captureEventPrefix = "capture:"

// The roles a step that asks for a capture holds: a gatherer's capture, or any other step's.
const (
	captureRoleGatherer = "evidence gatherer"
	captureRoleStep     = "step"
)

// CaptureArgs is what a capture runs: the argv of its command, at the commit it runs at, under the project runner it
// names. Briefs are the ids of the evidence runs it answers, and Files are the repository files it keeps.
type CaptureArgs struct {
	Commit string
	Argv   []string
	Runner string
	Briefs []string
	Files  []string
}

// CaptureStore is the store a capture records captures in and reads its briefs with. *store.Store satisfies it.
type CaptureStore interface {
	AddCapture(ctx context.Context, capture store.Capture) error
	SetCaptureGrant(ctx context.Context, id, grant, holder string) error
	RecordCaptureEnd(ctx context.Context, id string, end store.EvidenceCapture) error
	MarkCaptureDelivered(ctx context.Context, id string) error
	Capture(ctx context.Context, id string) (store.Capture, error)
	RunningCaptures(ctx context.Context) ([]store.Capture, error)
	UndeliveredCaptures(ctx context.Context) ([]store.Capture, error)
	CapturesAt(ctx context.Context, repository, commit string) ([]store.Capture, error)
	AttachCapture(ctx context.Context, capture string, runs []string, at time.Time) ([]store.EvidenceRun, error)
	Epic(ctx context.Context, id string) (store.EpicRow, error)
	EvidenceRun(ctx context.Context, id string) (store.EvidenceRun, error)
	EvidenceGathererOf(ctx context.Context, step string) (store.EvidenceGatherer, bool, error)
	EpicHost(ctx context.Context, epic string) (string, error)
}

// CaptureBriefs settles the briefs an ended capture was started for. *Evidence satisfies it.
type CaptureBriefs interface {
	Settle(ctx context.Context, capture store.Capture) error
}

// CaptureSteps reads the status of the step that asks for a capture. *Steps satisfies it.
type CaptureSteps interface {
	Status(ctx context.Context, id string) (store.StepRow, error)
}

// CapturesConfig configures Captures. Store records the captures, Steps reads the step that asks for one, Briefs settles
// the briefs of an ended capture, and Deliver then tells the asking step that it ended. A nil Briefs settles none. Pools, Environ, GateStateDir and GateScope run each command as a
// capture does, and StateDir holds the capture worktrees. Hosts resolves the host each epic is stored on to the Host its
// captures' git, worktrees and artifact reads run on, and a nil Hosts is hostexec.LocalHosts. NewTimer arms a timer that sends on its channel once, after a
// duration, and returns the timer's stop; a nil NewTimer wraps time.NewTimer. A nil Now is time.Now, and a nil Logger
// discards.
type CapturesConfig struct {
	Store        CaptureStore
	Steps        CaptureSteps
	Briefs       CaptureBriefs
	Deliver      func(context.Context, DeliverRequest) error
	Hosts        hostexec.Hosts
	Pools        PoolCommands
	Environ      []string
	StateDir     string
	GateStateDir string
	GateScope    string
	Now          func() time.Time
	Logger       *slog.Logger
	NewTimer     func(time.Duration) (<-chan time.Time, func() bool)
}

// Captures runs the commands that CaptureEvidence asks for, records each as a capture, and tells the step that asked for it
// when it ends. Each capture is driven in the background, under a context that Close cancels. cancels holds the cancel of
// each drive still under way, by capture id.
type Captures struct {
	store    CaptureStore
	steps    CaptureSteps
	briefs   CaptureBriefs
	deliver  func(context.Context, DeliverRequest) error
	captures evidenceCapture
	hosts    hostexec.Hosts
	newTimer func(time.Duration) (<-chan time.Time, func() bool)
	now      func() time.Time
	logger   *slog.Logger

	mu      sync.Mutex
	closed  bool
	cancels map[string]context.CancelCauseFunc
	drives  sync.WaitGroup
}

// NewCaptures returns a Captures for cfg. Its blobs are kept under StateDir/evidence/blobs, the directory an Evidence keeps its
// blobs in, and its capture worktrees under StateDir/evidence-worktrees.
func NewCaptures(cfg CapturesConfig) *Captures {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	newTimer := cfg.NewTimer
	if newTimer == nil {
		newTimer = func(d time.Duration) (<-chan time.Time, func() bool) {
			timer := time.NewTimer(d)
			return timer.C, timer.Stop
		}
	}
	hosts := cfg.Hosts
	if hosts == nil {
		hosts = hostexec.LocalHosts()
	}
	return &Captures{
		store:   cfg.Store,
		steps:   cfg.Steps,
		briefs:  cfg.Briefs,
		deliver: cfg.Deliver,
		captures: evidenceCapture{
			pools:        cfg.Pools,
			environ:      cfg.Environ,
			worktrees:    filepath.Join(cfg.StateDir, "evidence-worktrees"),
			gateStateDir: cfg.GateStateDir,
			gateScope:    cfg.GateScope,
			blobs:        evidenceBlobs{dir: filepath.Join(cfg.StateDir, "evidence", "blobs")},
		},
		hosts:    hosts,
		newTimer: newTimer,
		now:      now,
		logger:   logger,
		cancels:  map[string]context.CancelCauseFunc{},
	}
}

// Capture runs the command in args for the caller session, which names the step asking for it, and records it as a capture. A
// request that is refused matches ErrCaptureRefused and records nothing. It returns the capture as stored.
func (c *Captures) Capture(ctx context.Context, caller string, args CaptureArgs) (store.Capture, error) {
	if err := c.requireRunningStep(ctx, caller); err != nil {
		return store.Capture{}, err
	}
	if len(args.Argv) == 0 {
		return store.Capture{}, captureRefused("argv: empty")
	}
	if len(args.Briefs) == 0 {
		return store.Capture{}, captureRefused("briefs: none named")
	}
	epic, limit, err := c.briefsOf(ctx, args.Briefs)
	if err != nil {
		return store.Capture{}, err
	}
	row, host, err := c.epicOn(ctx, epic)
	if err != nil {
		return store.Capture{}, fmt.Errorf("capture evidence: %w", err)
	}
	runner := args.Runner
	if runner == "" {
		runner = localRunnerName
	}
	prefix, found, err := runnerPrefix(ctx, host, row, runner)
	if err != nil {
		return store.Capture{}, fmt.Errorf("capture evidence: %w", err)
	}
	if !found {
		return store.Capture{}, captureRefused(
			fmt.Sprintf("runner %q is not named by the project at %s", runner, row.Base),
		)
	}
	commit, err := stepGitResolve(ctx, host, row.Repository, args.Commit)
	if err != nil {
		return store.Capture{}, fmt.Errorf("%w: commit %q does not resolve: %w", ErrCaptureRefused, args.Commit, err)
	}
	role, err := c.roleOf(ctx, caller)
	if err != nil {
		return store.Capture{}, err
	}
	id, err := newSessionID()
	if err != nil {
		return store.Capture{}, err
	}
	if err = c.store.AddCapture(ctx, store.Capture{
		ID: id, Epic: epic, Repository: row.Repository, Step: caller, Role: role, Runner: runner, Commit: commit,
		Argv: args.Argv, Files: args.Files, Briefs: args.Briefs, Limit: limit, Created: c.now(),
	}); err != nil {
		return store.Capture{}, fmt.Errorf("add capture %s: %w", id, err)
	}
	stored, err := c.store.Capture(ctx, id)
	if err != nil {
		return store.Capture{}, fmt.Errorf("read capture %s: %w", id, err)
	}
	item := captureItem(stored, prefix)
	if err = c.begin(ctx, id, func(driveCtx context.Context) {
		c.runFresh(driveCtx, host, row.Repository, stored, item)
	}); err != nil {
		return store.Capture{}, fmt.Errorf("capture %s: %w", id, err)
	}
	return stored, nil
}

// Resume takes up the captures the store holds as running after a restart, and the ended ones not yet delivered. A running
// capture with a stored grant readopts its gate, and one with no grant runs from the start. Each is driven in the background
// under a context that Close cancels and waits for. It returns the errors of the captures it could not take up, joined.
func (c *Captures) Resume(ctx context.Context) error {
	running, err := c.store.RunningCaptures(ctx)
	if err != nil {
		return fmt.Errorf("resume captures: %w", err)
	}
	var errs []error
	for _, capture := range running {
		if err = c.resumeRunning(ctx, capture); err != nil {
			errs = append(errs, fmt.Errorf("resume capture %s: %w", capture.ID, err))
		}
	}
	ended, err := c.store.UndeliveredCaptures(ctx)
	if err != nil {
		return errors.Join(append(errs, fmt.Errorf("resume captures: %w", err))...)
	}
	for _, capture := range ended {
		if err = c.begin(
			ctx,
			capture.ID,
			func(driveCtx context.Context) { c.deliverEnd(driveCtx, capture) },
		); err != nil {
			errs = append(errs, fmt.Errorf("deliver capture %s: %w", capture.ID, err))
		}
	}
	return errors.Join(errs...)
}

// Close cancels every drive and waits for each to return. Under a gate scope each drive is canceled with errGateDetach, which
// leaves its capture's gate running under its holder for a restarted orreryd to re-adopt, and records nothing for it. Without
// one the cancel kills the gate. Capture returns an error from then on.
func (c *Captures) Close() {
	c.mu.Lock()
	c.closed = true
	var cause error
	if c.captures.gateScope != "" {
		cause = errGateDetach
	}
	for _, cancel := range c.cancels {
		cancel(cause)
	}
	c.mu.Unlock()
	c.drives.Wait()
}

// captureRefused returns the refusal of a capture request for reason, which matches ErrCaptureRefused.
func captureRefused(reason string) error {
	return fmt.Errorf("%w: %s", ErrCaptureRefused, reason)
}

// requireRunningStep returns the refusal of a capture whose caller is not a running step: one with no step row, or one whose
// step has ended. A read that fails is returned wrapped.
func (c *Captures) requireRunningStep(ctx context.Context, caller string) error {
	row, err := c.steps.Status(ctx, caller)
	switch {
	case errors.Is(err, store.ErrNoStep):
		return fmt.Errorf("%w: %w: %s has no step", ErrCaptureRefused, ErrCaptureNotStep, caller)
	case err != nil:
		return fmt.Errorf("capture evidence: %w", err)
	case row.End != "":
		return fmt.Errorf("%w: %w: step %s has ended", ErrCaptureRefused, ErrCaptureNotStep, caller)
	}
	return nil
}

// briefsOf returns the epic the briefs belong to and the largest limit of their items. A brief that names no stored run, or a
// run that is not a captured run waiting or gathering, is refused, and so are briefs of more than one epic.
func (c *Captures) briefsOf(ctx context.Context, briefs []string) (string, int64, error) {
	var epic string
	var limit int64
	for _, id := range briefs {
		run, err := c.store.EvidenceRun(ctx, id)
		if errors.Is(err, store.ErrNoEvidenceRun) {
			return "", 0, captureRefused(fmt.Sprintf("brief %s is no stored run", id))
		}
		if err != nil {
			return "", 0, fmt.Errorf("capture evidence: brief %s: %w", id, err)
		}
		if run.Source != evidence.SourceCaptured || (run.State != store.RunWaiting && run.State != store.RunGathering) {
			return "", 0, captureRefused(fmt.Sprintf(
				"brief %s is %s and %s, not a captured run waiting or gathering", id, run.Source, run.State,
			))
		}
		if epic == "" {
			epic = run.Epic
		}
		if run.Epic != epic {
			return "", 0, captureRefused(fmt.Sprintf("brief %s belongs to epic %s, not %s", id, run.Epic, epic))
		}
		limit = max(limit, run.Spec.Limit)
	}
	return epic, limit, nil
}

// roleOf returns the role of the step caller: the gatherer's when the store names it the gatherer of its epic, and the step's
// otherwise.
func (c *Captures) roleOf(ctx context.Context, caller string) (string, error) {
	_, gatherer, err := c.store.EvidenceGathererOf(ctx, caller)
	if err != nil {
		return "", fmt.Errorf("capture evidence: gatherer of step %s: %w", caller, err)
	}
	if gatherer {
		return captureRoleGatherer, nil
	}
	return captureRoleStep, nil
}

// epicOn returns the stored row of epic and the Host it lives on, which its captures' git, worktrees and artifact reads
// run on.
func (c *Captures) epicOn(ctx context.Context, epic string) (store.EpicRow, hostexec.Host, error) {
	row, err := c.store.Epic(ctx, epic)
	if err != nil {
		return store.EpicRow{}, nil, fmt.Errorf("epic %s: %w", epic, err)
	}
	host, err := hostOfEpic(ctx, c.store, c.hosts, epic)
	if err != nil {
		return store.EpicRow{}, nil, err
	}
	return row, host, nil
}

// runnerPrefix returns the argv a capture on runner runs before its own argv. The local runner has none. Any other runner is
// the one the project of row names at its base, read through host, and found is false when that project names no runner of
// the name.
func runnerPrefix(ctx context.Context, host hostexec.Host, row store.EpicRow, runner string) ([]string, bool, error) {
	if runner == localRunnerName {
		return nil, true, nil
	}
	runners, err := ReadProjectRunners(ctx, host, row.Repository, row.Base)
	if err != nil {
		return nil, false, fmt.Errorf("runners of epic %s: %w", row.ID, err)
	}
	for _, named := range runners {
		if named.Name == runner {
			return named.Argv, true, nil
		}
	}
	return nil, false, nil
}

// captureItem returns the item capture runs: the runner's argv prefix followed by the capture's argv, with its limit and the
// files it keeps.
func captureItem(capture store.Capture, prefix []string) evidence.Item {
	return evidence.Item{Capture: slices.Concat(prefix, capture.Argv), Limit: capture.Limit, Artifacts: capture.Files}
}

// resumeRunning takes up a running capture on the host its epic lives on. One with a grant readopts its gate, and one with
// none clears its worktree and runs from the start. Either is driven in the background, as Capture's run is.
func (c *Captures) resumeRunning(ctx context.Context, capture store.Capture) error {
	row, host, err := c.epicOn(ctx, capture.Epic)
	if err != nil {
		return err
	}
	prefix, found, err := runnerPrefix(ctx, host, row, capture.Runner)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("runner %s is no longer named by the project at %s", capture.Runner, row.Base)
	}
	item := captureItem(capture, prefix)
	return c.begin(ctx, capture.ID, func(driveCtx context.Context) {
		if capture.Grant != "" {
			end, readoptErr := c.captures.on(host).readopt(
				driveCtx,
				row.Repository,
				capture.Commit,
				capture.ID,
				item,
				capture.Grant,
			)
			c.settle(driveCtx, capture, end, readoptErr, true)
			return
		}
		worktree := filepath.Join(c.captures.worktrees, capture.ID)
		if clearErr := clearUnlaunchedWorktree(
			context.WithoutCancel(driveCtx),
			host,
			row.Repository,
			worktree,
		); clearErr != nil {
			c.logger.WarnContext(
				driveCtx,
				"clear worktree of unlaunched capture",
				"capture",
				capture.ID,
				"err",
				clearErr,
			)
			return
		}
		c.runFresh(driveCtx, host, row.Repository, capture, item)
	})
}

// runFresh runs capture at its commit in repo on host under a grant of the gate pool. The grant is recorded once it holds,
// and the capture is then settled (see settle).
func (c *Captures) runFresh(
	ctx context.Context, host hostexec.Host, repo string, capture store.Capture, item evidence.Item,
) {
	launched := false
	started := func(startCtx context.Context, grant, holder string) error {
		if err := c.retry(startCtx, "capture grant", capture.ID, func(storeCtx context.Context) error {
			return c.store.SetCaptureGrant(storeCtx, capture.ID, grant, holder)
		}); err != nil {
			return fmt.Errorf("record grant of capture %s: %w", capture.ID, err)
		}
		launched = true
		return nil
	}
	end, err := c.captures.on(host).run(ctx, repo, capture.Commit, capture.ID, item, started)
	c.settle(ctx, capture, end, err, launched)
}

// settle records the end a drive of capture ran to, and delivers it. A drive that returned an error, which is a gate that
// detached, records nothing, and neither does one cut short before its gate launched: a restart takes up both. launched is
// whether the capture's gate launched.
func (c *Captures) settle(
	ctx context.Context,
	capture store.Capture,
	end store.EvidenceCapture,
	err error,
	launched bool,
) {
	if err != nil {
		c.logger.InfoContext(ctx, "capture left for a restart", "capture", capture.ID, "err", err)
		return
	}
	if !launched && ctx.Err() != nil {
		c.logger.InfoContext(ctx, "capture left for a restart", "capture", capture.ID, "err", ctx.Err())
		return
	}
	if !c.recordEnd(ctx, capture.ID, end) {
		return
	}
	c.deliverEnd(ctx, endedCapture(capture, end))
}

// recordEnd stores end as the end of the capture with id, retrying a write the store refuses until it lands or ctx ends. It
// reports whether the end was stored; one that was not is left for a restart to take up.
func (c *Captures) recordEnd(ctx context.Context, id string, end store.EvidenceCapture) bool {
	err := c.retry(ctx, "capture end", id, func(storeCtx context.Context) error {
		return c.store.RecordCaptureEnd(storeCtx, id, end)
	})
	if err != nil {
		c.logger.ErrorContext(ctx, "capture end unstored at close, left for a restart", "capture", id, "err", err)
		return false
	}
	return true
}

// endedCapture returns capture with the exit, timeout, reason, logs, artifacts and end time that its run left.
func endedCapture(capture store.Capture, end store.EvidenceCapture) store.Capture {
	capture.Exit = end.Exit
	capture.TimedOut = end.TimedOut
	capture.NotCaptured = end.NotCaptured
	capture.LogHash = end.LogHash
	capture.LogBytes = end.LogBytes
	capture.StderrHash = end.StderrHash
	capture.StderrBytes = end.StderrBytes
	capture.Artifacts = end.Artifacts
	capture.Ended = end.At
	return capture
}

// deliverEnd settles the briefs of capture, then tells the step that asked for it that it ended, in one block, and marks the
// capture delivered once the block is delivered or the step has ended. Briefs that cannot be settled leave the capture
// undelivered, for a restart's Resume to settle and deliver. A step that is busy or running its gate is tried again every captureDeliverRetry, until ctx
// ends. Any other failure is logged, and the capture is left undelivered for a restart's Resume to deliver.
func (c *Captures) deliverEnd(ctx context.Context, capture store.Capture) {
	if c.briefs != nil {
		if err := c.briefs.Settle(ctx, capture); err != nil {
			c.logger.ErrorContext(
				ctx,
				"capture briefs unsettled, left for a restart",
				"capture",
				capture.ID,
				"err",
				err,
			)
			return
		}
	}
	text, err := c.block(capture)
	if err != nil {
		c.logger.ErrorContext(ctx, "capture end unread, left for a restart", "capture", capture.ID, "err", err)
		return
	}
	req := DeliverRequest{ID: capture.Step, Text: text, PromptID: captureEventPrefix + capture.ID}
	for {
		err = c.deliver(ctx, req)
		switch {
		case err == nil, errors.Is(err, ErrStepNotRunning), errors.Is(err, store.ErrNoStep):
			c.markDelivered(ctx, capture.ID)
			return
		case errors.Is(err, ErrSessionBusy), errors.Is(err, ErrStepGating):
			if !c.waitRetry(ctx) {
				return
			}
		default:
			if ctx.Err() == nil {
				c.logger.ErrorContext(
					ctx,
					"capture end undelivered, left for a restart",
					"capture",
					capture.ID,
					"err",
					err,
				)
			}
			return
		}
	}
}

// waitRetry waits captureDeliverRetry before a delivery is tried again. It reports false when ctx ends first.
func (c *Captures) waitRetry(ctx context.Context) bool {
	ticks, stop := c.newTimer(captureDeliverRetry)
	select {
	case <-ctx.Done():
		stop()
		return false
	case <-ticks:
		return true
	}
}

// markDelivered records that the end of capture id was delivered, retrying a write the store refuses until it lands or ctx
// ends. A capture whose mark is not stored is delivered again at a restart.
func (c *Captures) markDelivered(ctx context.Context, id string) {
	err := c.retry(ctx, "capture delivered", id, func(storeCtx context.Context) error {
		return c.store.MarkCaptureDelivered(storeCtx, id)
	})
	if err != nil {
		c.logger.ErrorContext(ctx, "capture delivery unmarked at close, left for a restart", "capture", id, "err", err)
	}
}

// block returns the text that tells the step of capture its capture ended: the capture's id, its exit, whether it timed out,
// why it captured nothing, the newest captureBlockLines of each log, and the state of each file it named.
func (c *Captures) block(capture store.Capture) (string, error) {
	stdout, err := c.logLines(capture.LogHash)
	if err != nil {
		return "", fmt.Errorf("stdout of capture %s: %w", capture.ID, err)
	}
	stderr, err := c.logLines(capture.StderrHash)
	if err != nil {
		return "", fmt.Errorf("stderr of capture %s: %w", capture.ID, err)
	}
	reason := capture.NotCaptured
	if reason == "" {
		reason = epicReasonNone
	}
	lines := []string{
		"Evidence capture " + capture.ID + " ended.",
		fmt.Sprintf("exit: %d", capture.Exit),
		fmt.Sprintf("timed out: %t", capture.TimedOut),
		"not captured: " + reason,
		fmt.Sprintf("stdout, last %d lines:", captureBlockLines),
		stdout,
		fmt.Sprintf("stderr, last %d lines:", captureBlockLines),
		stderr,
		"files:",
	}
	for _, artifact := range capture.Artifacts {
		lines = append(lines, artifact.Path+": "+artifact.State)
	}
	return strings.Join(lines, "\n"), nil
}

// logLines returns the newest captureBlockLines lines of the log stored under hash, redacted as an evidence run's log is read: a
// log at least as long as the capture tail was cut at its start, so its first line is dropped. An empty hash is an empty log.
func (c *Captures) logLines(hash string) (string, error) {
	if hash == "" {
		return "", nil
	}
	raw, err := c.captures.blobs.read(hash + ".log")
	if err != nil {
		return "", err
	}
	redacted := redact.Text(string(raw))
	if len(raw) >= c.captures.tail() {
		redacted = redact.CutTail(string(raw))
	}
	return lastLines(redacted, captureBlockLines), nil
}

// lastLines returns the newest n lines of text, without its final newline. Text of n lines or fewer is returned whole.
func lastLines(text string, n int) string {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// retry runs write, the write what of capture id, as retryStore does.
func (c *Captures) retry(ctx context.Context, what, id string, write func(context.Context) error) error {
	return retryStore(ctx, c.after, c.logger, what, []any{"capture", id}, write)
}

// after returns the channel of a timer of d, which retryStore waits on between its tries. The timer is not stopped: its channel
// is read once, and a store retry waits a second at most.
func (c *Captures) after(d time.Duration) <-chan time.Time {
	ticks, _ := c.newTimer(d)
	return ticks
}

// begin drives the capture id in the background with drive, under a context that Close cancels and that ctx's values carry
// without its cancel. It returns an error when the runner is closed, and does nothing when id is already under way.
func (c *Captures) begin(ctx context.Context, id string, drive func(context.Context)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errCapturesClosed
	}
	if _, driven := c.cancels[id]; driven {
		return nil
	}
	driveCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	c.cancels[id] = cancel
	c.drives.Go(func() {
		drive(driveCtx)
		c.finish(id)
	})
	return nil
}

// finish cancels the drive of capture id with nil and forgets it, once the drive has returned.
func (c *Captures) finish(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cancel, ok := c.cancels[id]; ok {
		cancel(nil)
		delete(c.cancels, id)
	}
}

var _ CaptureCommands = (*Captures)(nil)
