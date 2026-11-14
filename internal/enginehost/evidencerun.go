package enginehost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// ErrEvidenceRefused reports a bad request to run an evidence item. Run wraps it with the reason.
var ErrEvidenceRefused = errors.New("evidence run is refused")

// ErrNoEvidenceArtifact reports that a run has no file of the name asked for: no log, a name it does not list, or an
// artifact it did not keep.
var ErrNoEvidenceArtifact = errors.New("no evidence artifact")

// errEvidenceClosed is what Run returns once Close has been called.
var errEvidenceClosed = errors.New("evidence runner is closed")

// evidenceRunTaskPattern matches the name of a planned task's evidence item, T<n>.
var evidenceRunTaskPattern = regexp.MustCompile(`^T[1-9][0-9]*$`)

// evidenceJudgeTries is how many judge steps a run starts before it records no verdict. A judge that ends with no verdict
// is followed by another on the same kept output.
const evidenceJudgeTries = 3

// evidenceRunAwait is how long each wait for a judge step lasts before the step is waited for again.
const evidenceRunAwait = time.Minute

// EvidenceStore is the store an evidence runner reads epics and records runs in. *store.Store satisfies it.
type EvidenceStore interface {
	Epic(ctx context.Context, id string) (store.EpicRow, error)
	AddEvidenceRun(ctx context.Context, run store.EvidenceRun) error
	SetEvidenceGrant(ctx context.Context, id, grant, holder string) error
	RecordEvidenceCapture(ctx context.Context, id string, capture store.EvidenceCapture) error
	SetEvidenceJudge(ctx context.Context, id, step string) error
	RecordEvidenceVerdict(ctx context.Context, id string, verdict store.EvidenceVerdict) error
	EvidenceRun(ctx context.Context, id string) (store.EvidenceRun, error)
	EvidenceRuns(ctx context.Context, epic string) ([]store.EvidenceRun, error)
	OpenEvidenceRuns(ctx context.Context) ([]store.EvidenceRun, error)
	LabelledSteps(ctx context.Context, epic, task string) ([]store.StepRow, error)
}

// EvidenceSteps starts judge steps and waits for them to end. *Steps satisfies it.
type EvidenceSteps interface {
	Start(ctx context.Context, start StepStart) (string, error)
	Wait(ctx context.Context, ids []string, timeout time.Duration) (*store.StepRow, []store.StepRow, error)
}

// EvidenceConfig configures an Evidence. Store records the runs, Steps runs the judges, and Pools, Environ, GateStateDir and
// GateScope run each captured item's gate as a capture does. StateDir holds the blobs and the capture worktrees. A nil
// Now is time.Now, and a nil Logger discards.
type EvidenceConfig struct {
	Store        EvidenceStore
	Steps        EvidenceSteps
	Pools        PoolCommands
	Environ      []string
	StateDir     string
	GateStateDir string
	GateScope    string
	Now          func() time.Time
	Logger       *slog.Logger
}

// Evidence runs evidence items. Each run is stored, captured or read, judged, and recorded once, by a drive that Run starts
// in the background. cancels holds the cancel of each drive still under way, by run id, and Close cancels them all.
type Evidence struct {
	store    EvidenceStore
	steps    EvidenceSteps
	captures evidenceCapture
	blobs    evidenceBlobs
	now      func() time.Time
	logger   *slog.Logger

	mu      sync.Mutex
	closed  bool
	cancels map[string]context.CancelCauseFunc
	drives  sync.WaitGroup
}

// NewEvidence returns an Evidence for cfg. Its blobs are kept under StateDir/evidence/blobs, and its capture worktrees under
// StateDir/evidence-worktrees.
func NewEvidence(cfg EvidenceConfig) *Evidence {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	blobs := evidenceBlobs{dir: filepath.Join(cfg.StateDir, "evidence", "blobs")}
	return &Evidence{
		store: cfg.Store,
		steps: cfg.Steps,
		captures: evidenceCapture{
			pools:        cfg.Pools,
			environ:      cfg.Environ,
			worktrees:    filepath.Join(cfg.StateDir, "evidence-worktrees"),
			gateStateDir: cfg.GateStateDir,
			gateScope:    cfg.GateScope,
			blobs:        blobs,
		},
		blobs:   blobs,
		now:     now,
		logger:  logger,
		cancels: map[string]context.CancelCauseFunc{},
	}
}

// Run runs the item named item of epic at revision, in phase red or green. item is a Requirement's R<n>, whose item must
// be set, or a planned task's T<n>, whose evidence must be set. Run refuses a bad request with an error matching
// ErrEvidenceRefused, and an epic that does not exist with the store's error. It stores the run in state capturing with its
// item, revision and phase before anything runs, then drives it in the background and returns the stored run.
func (e *Evidence) Run(ctx context.Context, epic, item, revision, phase string) (store.EvidenceRun, error) {
	row, err := e.store.Epic(ctx, epic)
	if err != nil {
		return store.EvidenceRun{}, fmt.Errorf("evidence run of epic %s: %w", epic, err)
	}
	spec, err := evidenceRunItemOf(row, item)
	if err != nil {
		return store.EvidenceRun{}, err
	}
	if phase != evidence.PhaseRed && phase != evidence.PhaseGreen {
		return store.EvidenceRun{}, fmt.Errorf("%w: phase %q is not red or green", ErrEvidenceRefused, phase)
	}
	commit, err := stepGitResolve(ctx, row.Repository, revision)
	if err != nil {
		return store.EvidenceRun{}, fmt.Errorf(
			"%w: revision %q does not resolve: %w", ErrEvidenceRefused, revision, err,
		)
	}
	id, err := newSessionID()
	if err != nil {
		return store.EvidenceRun{}, err
	}
	run := store.EvidenceRun{
		ID: id, Epic: epic, Item: item, Revision: commit, Phase: phase, Source: evidenceRunSource(spec),
		Spec: spec, Created: e.now(),
	}
	if err = e.store.AddEvidenceRun(ctx, run); err != nil {
		return store.EvidenceRun{}, fmt.Errorf("add evidence run %s: %w", id, err)
	}
	stored, err := e.store.EvidenceRun(ctx, id)
	if err != nil {
		return store.EvidenceRun{}, fmt.Errorf("read evidence run %s: %w", id, err)
	}
	err = e.start(ctx, row.Repository, stored)
	if err != nil {
		return store.EvidenceRun{}, err
	}
	return stored, nil
}

// Runs returns the runs of epic in the order the store gives them.
func (e *Evidence) Runs(ctx context.Context, epic string) ([]store.EvidenceRun, error) {
	runs, err := e.store.EvidenceRuns(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("runs of epic %s: %w", epic, err)
	}
	return runs, nil
}

// Artifact returns the bytes of a file of run and the hash they are stored under. The name "" is the run's log, and any
// other name is the kept artifact with that path. A run that does not exist returns an error matching
// store.ErrNoEvidenceRun. A file the run does not have, or has but did not keep, returns an error matching
// ErrNoEvidenceArtifact.
func (e *Evidence) Artifact(ctx context.Context, run, name string) ([]byte, string, error) {
	stored, err := e.store.EvidenceRun(ctx, run)
	if err != nil {
		return nil, "", fmt.Errorf("artifact of evidence run %s: %w", run, err)
	}
	blob, hash, err := evidenceRunFile(stored, name)
	if err != nil {
		return nil, "", err
	}
	data, err := e.blobs.read(blob)
	if err != nil {
		return nil, "", fmt.Errorf("artifact of evidence run %s: %w", run, err)
	}
	return data, hash, nil
}

// Close cancels every drive and waits for each to return. Under a gate scope each drive is canceled with errGateDetach,
// which leaves its capture's gate running under its holder for a restarted orreryd to re-adopt. Without one the cancel
// kills the gate. A drive that is canceled records nothing more, so its run stays as it is for a restart to resume. Run
// returns an error from then on.
func (e *Evidence) Close() {
	e.mu.Lock()
	e.closed = true
	var cause error
	if e.captures.gateScope != "" {
		cause = errGateDetach
	}
	for _, cancel := range e.cancels {
		cancel(cause)
	}
	e.mu.Unlock()
	e.drives.Wait()
}

// evidenceRunItemOf returns the evidence item the name names in row: a Requirement's item for R<n>, or a planned task's
// evidence for T<n>. Any other name is refused, and so is a Requirement or task with no item, such as a Requirement stored
// before O3d2.
func evidenceRunItemOf(row store.EpicRow, name string) (evidence.Item, error) {
	switch {
	case epicRequirementIDPattern.MatchString(name):
		return evidenceRunRequirementItem(row, name)
	case evidenceRunTaskPattern.MatchString(name):
		return evidenceRunTaskItem(row, name)
	}
	return evidence.Item{}, fmt.Errorf("%w: item %q is neither an R<n> nor a T<n>", ErrEvidenceRefused, name)
}

// evidenceRunRequirementItem returns the item of the Requirement named name in row.
func evidenceRunRequirementItem(row store.EpicRow, name string) (evidence.Item, error) {
	for _, requirement := range row.Requirements {
		if requirement.ID != name {
			continue
		}
		if reflect.ValueOf(requirement.Item).IsZero() {
			return evidence.Item{}, fmt.Errorf("%w: requirement %s has no evidence item", ErrEvidenceRefused, name)
		}
		return requirement.Item, nil
	}
	return evidence.Item{}, fmt.Errorf("%w: no requirement %s", ErrEvidenceRefused, name)
}

// evidenceRunTaskItem returns the evidence of the planned task named name in row, whose seq is the number after its T.
func evidenceRunTaskItem(row store.EpicRow, name string) (evidence.Item, error) {
	seq, err := strconv.ParseInt(name[1:], 10, 64)
	if err != nil {
		return evidence.Item{}, fmt.Errorf("%w: item %q names no task", ErrEvidenceRefused, name)
	}
	for _, task := range row.Tasks {
		if task.Seq != seq {
			continue
		}
		if reflect.ValueOf(task.Evidence).IsZero() {
			return evidence.Item{}, fmt.Errorf("%w: task %s has no evidence item", ErrEvidenceRefused, name)
		}
		return task.Evidence, nil
	}
	return evidence.Item{}, fmt.Errorf("%w: no task %s", ErrEvidenceRefused, name)
}

// evidenceRunSource returns the source a run of spec has: captured when the item names a capture, else supplied.
func evidenceRunSource(spec evidence.Item) string {
	if len(spec.Capture) > 0 {
		return evidence.SourceCaptured
	}
	return evidence.SourceSupplied
}

// evidenceRunFile returns the blob name and hash of the file of run that name asks for: the log for the name "", else the
// kept artifact with that path. It returns an error matching ErrNoEvidenceArtifact when run has no such file.
func evidenceRunFile(run store.EvidenceRun, name string) (string, string, error) {
	if name == "" {
		if run.LogHash == "" {
			return "", "", fmt.Errorf("log of evidence run %s: %w", run.ID, ErrNoEvidenceArtifact)
		}
		return run.LogHash + ".log", run.LogHash, nil
	}
	for _, artifact := range run.Artifacts {
		if artifact.Path != name {
			continue
		}
		if artifact.State != artifactKept {
			return "", "", fmt.Errorf(
				"artifact %s of evidence run %s is %s: %w", name, run.ID, artifact.State, ErrNoEvidenceArtifact,
			)
		}
		return artifact.Blob, artifact.Hash, nil
	}
	return "", "", fmt.Errorf("artifact %s of evidence run %s: %w", name, run.ID, ErrNoEvidenceArtifact)
}

// start drives run at repo in the background, under a context that Close cancels, with errGateDetach under a gate scope, and
// that finish cancels with nil. It returns an error when the runner is closed, and the run then stays as it was stored for a
// restart to resume.
func (e *Evidence) start(ctx context.Context, repo string, run store.EvidenceRun) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errEvidenceClosed
	}
	driveCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	e.cancels[run.ID] = cancel
	e.drives.Go(func() {
		e.drive(driveCtx, repo, run)
		e.finish(run.ID)
	})
	return nil
}

// finish cancels the drive of run id with nil and forgets it, once the drive has returned.
func (e *Evidence) finish(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if cancel, ok := e.cancels[id]; ok {
		cancel(nil)
		delete(e.cancels, id)
	}
}

// drive runs run to its verdict. A captured run's capture, or a supplied run's reading of its files, is recorded, and a run
// whose capture captured nothing ends there. Otherwise its judge gives the verdict. A capture whose gate detaches records
// nothing, so the next restart re-adopts it. Each record is written only while ctx has not ended.
func (e *Evidence) drive(ctx context.Context, repo string, run store.EvidenceRun) {
	capture, err := e.capture(ctx, repo, run)
	if err != nil {
		e.logger.InfoContext(ctx, "evidence capture detached from its gate", "run", run.ID, "err", err)
		return
	}
	if ctx.Err() != nil {
		return
	}
	if err = e.store.RecordEvidenceCapture(ctx, run.ID, capture); err != nil {
		e.logger.ErrorContext(ctx, "record evidence capture", "run", run.ID, "err", err)
		return
	}
	if capture.NotCaptured != "" {
		return
	}
	e.judgeRun(ctx, repo, evidenceRunCaptured(run, capture))
}

// capture captures run at repo's revision, or reads its supplied files. A captured run's grant and holder are recorded when
// its grant holds, before its gate launches. It returns an error only when the gate detaches.
func (e *Evidence) capture(ctx context.Context, repo string, run store.EvidenceRun) (store.EvidenceCapture, error) {
	if run.Source == evidence.SourceSupplied {
		return readSupplied(ctx, repo, run.Revision, run.Spec.Supplied, e.blobs), nil
	}
	started := func(startCtx context.Context, grant, holder string) error {
		if err := e.store.SetEvidenceGrant(startCtx, run.ID, grant, holder); err != nil {
			return fmt.Errorf("record grant of evidence run %s: %w", run.ID, err)
		}
		return nil
	}
	return e.captures.run(ctx, repo, run.Revision, run.ID, run.Spec, started)
}

// evidenceRunCaptured returns run with the exit, timeout, log and artifacts its capture left, which the judge reads.
func evidenceRunCaptured(run store.EvidenceRun, capture store.EvidenceCapture) store.EvidenceRun {
	run.Exit = capture.Exit
	run.TimedOut = capture.TimedOut
	run.LogHash = capture.LogHash
	run.LogBytes = capture.LogBytes
	run.Artifacts = capture.Artifacts
	return run
}

// judgeRun gives run its verdict. A judge that ends with no verdict is followed by another, up to evidenceJudgeTries judges,
// and the third's end then stands as the reason. The verdict is recorded once.
func (e *Evidence) judgeRun(ctx context.Context, repo string, run store.EvidenceRun) {
	for judges := 1; ; judges++ {
		verdict, reason, err := e.judgeOnce(ctx, repo, run)
		if err != nil {
			e.logger.WarnContext(ctx, "evidence judge stopped", "run", run.ID, "err", err)
			return
		}
		if verdict != "" || judges == evidenceJudgeTries {
			e.recordVerdict(ctx, run, verdict, reason)
			return
		}
	}
}

// judgeOnce starts one judge step on run's kept output and waits for it to end. It returns the verdict the end gives, or
// the reason it gives none. A judge that does not start gives no verdict, and the reason is why. An error is returned only
// when the step cannot be recorded or waited for.
func (e *Evidence) judgeOnce(ctx context.Context, repo string, run store.EvidenceRun) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", fmt.Errorf("judge evidence run %s: %w", run.ID, err)
	}
	step, refusal := e.startJudge(ctx, repo, run)
	if refusal != "" {
		return "", refusal, nil
	}
	if err := ctx.Err(); err != nil {
		return "", "", fmt.Errorf("judge evidence run %s: %w", run.ID, err)
	}
	if err := e.store.SetEvidenceJudge(ctx, run.ID, step); err != nil {
		return "", "", fmt.Errorf("record judge of evidence run %s: %w", run.ID, err)
	}
	row, err := e.awaitJudge(ctx, step)
	if err != nil {
		return "", "", err
	}
	verdict, reason := judgeVerdict(row)
	return verdict, reason, nil
}

// startJudge starts the judge step of run at repo. It returns the step's id, or the reason the step did not start: its
// prompt could not be built, or Start refused it.
func (e *Evidence) startJudge(ctx context.Context, repo string, run store.EvidenceRun) (string, string) {
	prompt, err := judgePrompt(run, e.blobs)
	if err != nil {
		return "", err.Error()
	}
	step, err := e.steps.Start(ctx, judgeStart(run, repo, prompt))
	if err != nil {
		return "", err.Error()
	}
	return step, ""
}

// awaitJudge waits until the judge step with id ends, and returns its row. It returns an error when the wait fails, as a
// canceled context does.
func (e *Evidence) awaitJudge(ctx context.Context, id string) (store.StepRow, error) {
	for {
		ended, _, err := e.steps.Wait(ctx, []string{id}, evidenceRunAwait)
		if err != nil {
			return store.StepRow{}, fmt.Errorf("wait for judge step %s: %w", id, err)
		}
		if ended != nil {
			return *ended, nil
		}
	}
}

// recordVerdict records verdict and reason for run, and whether run passes its phase, unless ctx has ended.
func (e *Evidence) recordVerdict(ctx context.Context, run store.EvidenceRun, verdict, reason string) {
	if ctx.Err() != nil {
		return
	}
	verdictRecord := store.EvidenceVerdict{
		Verdict: verdict,
		Reason:  reason,
		Passed:  evidence.Passed(run.Phase, run.NotCaptured, run.Exit, verdict),
		At:      e.now(),
	}
	if err := e.store.RecordEvidenceVerdict(ctx, run.ID, verdictRecord); err != nil {
		e.logger.ErrorContext(ctx, "record evidence verdict", "run", run.ID, "err", err)
	}
}
