package enginehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/redact"
	"github.com/joshsymonds/orrery/internal/store"
)

// ErrEvidenceRefused reports a bad request to run an evidence item. Run wraps it with the reason.
var ErrEvidenceRefused = errors.New("evidence run is refused")

// ErrNoEvidenceArtifact reports that a run has no file of the name asked for: no log, a name it does not list, or an
// artifact it did not keep.
var ErrNoEvidenceArtifact = errors.New("no evidence artifact")

// errEvidenceClosed is what Run returns once Close has been called.
var errEvidenceClosed = errors.New("evidence runner is closed")

// evidenceJudgeTries is how many judge attempts a run makes before it records no verdict. An attempt whose judge does not start
// counts too. A judge that ends with no verdict
// is followed by another on the same kept output.
const evidenceJudgeTries = 3

// evidenceSubscribeRetry is how long a judge's wait waits after a Subscribe that failed before it subscribes to the judge again.
const evidenceSubscribeRetry = 5 * time.Second

// evidencePauseRecheck is how long a judge waits before it reads its epic again while the epic is paused.
const evidencePauseRecheck = 5 * time.Second

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
	Step(ctx context.Context, id string) (store.StepRow, error)
}

// EvidenceSteps starts judge steps and subscribes to their ends and holds. *Steps satisfies it.
type EvidenceSteps interface {
	Start(ctx context.Context, start StepStart) (string, error)
	Subscribe(ctx context.Context, ids []string) (*StepSubscription, error)
}

// EvidenceConfig configures an Evidence. Store records the runs, Steps runs the judges, and Pools, Environ, GateStateDir,
// GateScope run each captured item's gate as a capture does. StateDir holds the blobs and the capture worktrees.
// NewTimer arms a timer that sends on its channel once, after a duration, and returns the timer's stop; a nil NewTimer wraps
// time.NewTimer. A nil Now is time.Now, and a nil Logger discards. After returns the channel a failed store write of a run
// waits on before it is tried again (see retryStore), and that a judge of a paused epic waits on before it reads the epic
// again, and nil is time.After.
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
	NewTimer     func(time.Duration) (<-chan time.Time, func() bool)
	After        func(time.Duration) <-chan time.Time
}

// Evidence runs evidence items. Each run is stored, captured or read, judged, and recorded once, by a drive that Run starts
// in the background. cancels holds the cancel of each drive still under way, by run id, and Close cancels them all.
type Evidence struct {
	store    EvidenceStore
	steps    EvidenceSteps
	captures evidenceCapture
	blobs    evidenceBlobs
	now      func() time.Time
	newTimer func(time.Duration) (<-chan time.Time, func() bool)
	logger   *slog.Logger
	after    func(time.Duration) <-chan time.Time

	mu      sync.Mutex
	closed  bool
	cancels map[string]context.CancelCauseFunc
	drives  sync.WaitGroup
	// calls counts the calls under way that can start or end work (see callsInFlight).
	calls callsInFlight
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
	newTimer := cfg.NewTimer
	if newTimer == nil {
		newTimer = func(d time.Duration) (<-chan time.Time, func() bool) {
			timer := time.NewTimer(d)
			return timer.C, timer.Stop
		}
	}
	after := cfg.After
	if after == nil {
		after = time.After
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
		blobs:    blobs,
		now:      now,
		newTimer: newTimer,
		logger:   logger,
		after:    after,
		cancels:  map[string]context.CancelCauseFunc{},
	}
}

// Run runs the item named item of epic at revision, in phase red or green. item is a Requirement's R<n>, whose item must
// be set. Run refuses a bad request with an error matching ErrEvidenceRefused, and an epic that does not exist with the
// store's error. It stores the run in state capturing with its item, revision and phase before anything runs, then drives
// it in the background and returns the stored run.
func (e *Evidence) Run(ctx context.Context, epic, item, revision, phase string) (store.EvidenceRun, error) {
	defer e.calls.enter()()
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

// Artifact returns the bytes of a file of run and the hash they are stored under. The name "" is the run's stdout log,
// and any other name is the kept artifact with that path. A log is returned redacted, under the hash of the redacted
// bytes, and an artifact as it was stored. A run that does not exist returns an error matching store.ErrNoEvidenceRun.
// A file the run does not have, or has but did not keep, returns an error matching ErrNoEvidenceArtifact.
func (e *Evidence) Artifact(ctx context.Context, run, name string) ([]byte, string, error) {
	stored, err := e.store.EvidenceRun(ctx, run)
	if err != nil {
		return nil, "", fmt.Errorf("artifact of evidence run %s: %w", run, err)
	}
	if name == "" {
		return e.readLog(stored, stored.LogHash)
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

// StderrLog returns the stderr log of run, read as Artifact reads a log, and the hash of the bytes returned. A run that
// does not exist returns an error matching store.ErrNoEvidenceRun, and a run with no stderr log one matching
// ErrNoEvidenceArtifact.
func (e *Evidence) StderrLog(ctx context.Context, run string) ([]byte, string, error) {
	stored, err := e.store.EvidenceRun(ctx, run)
	if err != nil {
		return nil, "", fmt.Errorf("stderr log of evidence run %s: %w", run, err)
	}
	return e.readLog(stored, stored.StderrHash)
}

// readLog returns the log of run stored under hash, redacted, and the sha256 of the bytes returned. Every read redacts,
// so a log stored before redaction is redacted when shown. A log at least as long as the capture tail filled its
// capture's tail, so it was cut at its start, and its first line is dropped with redact.CutTail. A run with no log
// under hash returns an error matching ErrNoEvidenceArtifact.
func (e *Evidence) readLog(run store.EvidenceRun, hash string) ([]byte, string, error) {
	if hash == "" {
		return nil, "", fmt.Errorf("log of evidence run %s: %w", run.ID, ErrNoEvidenceArtifact)
	}
	raw, err := e.blobs.read(hash + ".log")
	if err != nil {
		return nil, "", fmt.Errorf("log of evidence run %s: %w", run.ID, err)
	}
	var redacted string
	if len(raw) >= e.captures.tail() {
		redacted = redact.CutTail(string(raw))
	} else {
		redacted = redact.Text(string(raw))
	}
	data := []byte(redacted)
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
}

// Close cancels every drive and waits for each to return. Under a gate scope each drive is canceled with errGateDetach,
// which leaves its capture's gate running under its holder for a restarted orreryd to re-adopt. Without one the cancel
// kills the gate. A capture that has ended is recorded even so, but a canceled drive starts no judge, so its run is left
// judging with no judge step for a restart's Resume to give the verdict. A capture cut short before its gate launched is
// recorded as nothing, so its run stays capturing for a restart to run it. Run returns an error from then on.
func (e *Evidence) Close() {
	defer e.calls.enter()()
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

// evidenceRunItemOf returns the evidence item the name names in row: a Requirement's item for R<n>. Any other name is
// refused, and so is a Requirement with no item, such as a Requirement stored before O3d2.
func evidenceRunItemOf(row store.EpicRow, name string) (evidence.Item, error) {
	if !epicRequirementIDPattern.MatchString(name) {
		return evidence.Item{}, fmt.Errorf("%w: item %q is not an R<n>", ErrEvidenceRefused, name)
	}
	return evidenceRunRequirementItem(row, name)
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

// evidenceRunSource returns the source a run of spec has: captured when the item names a capture, else supplied.
func evidenceRunSource(spec evidence.Item) string {
	if len(spec.Capture) > 0 {
		return evidence.SourceCaptured
	}
	return evidence.SourceSupplied
}

// evidenceRunFile returns the blob name and hash of the kept artifact of run named name. It returns an error matching
// ErrNoEvidenceArtifact when run lists no artifact of that name, or lists one it did not keep.
func evidenceRunFile(run store.EvidenceRun, name string) (string, string, error) {
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

// drive runs run to its verdict. A captured run's capture, or a supplied run's reading of its files, is recorded (see
// recordCapture), even when ctx has ended, and a run whose capture captured nothing ends there. A drive whose ctx has ended
// then stops before judging, and a Resume gives the judge. Otherwise its judge gives the verdict. A capture whose gate
// detaches records nothing, so the next restart re-adopts it, and so does one cut short before its gate launched or a
// supplied read cut short.
func (e *Evidence) drive(ctx context.Context, repo string, run store.EvidenceRun) {
	capture, err := e.capture(ctx, repo, run)
	if err != nil {
		e.logger.InfoContext(ctx, "evidence capture left for a restart", "run", run.ID, "err", err)
		return
	}
	if !e.recordCapture(ctx, run.ID, capture) {
		return
	}
	if capture.NotCaptured != "" || ctx.Err() != nil {
		return
	}
	e.judgeRun(ctx, repo, evidenceRunCaptured(run, capture))
}

// recordCapture stores capture as the capture of the run with id, retrying a write the store refuses until it lands (see
// retryStore), since nothing drives a run the store keeps capturing. Only the end of ctx, which Close cancels, stops the
// retries: the run is then left capturing for a restart's Resume, and recordCapture reports false.
func (e *Evidence) recordCapture(ctx context.Context, id string, capture store.EvidenceCapture) bool {
	err := e.retry(ctx, "evidence capture", id,
		func(storeCtx context.Context) error { return e.store.RecordEvidenceCapture(storeCtx, id, capture) })
	if err != nil {
		e.logger.ErrorContext(ctx, "evidence capture unstored at close, left for a restart", "run", id, "err", err)
		return false
	}
	return true
}

// recordJudge stores step as the judge of the run with id, retrying a write the store refuses until it lands or ctx ends
// (see retryStore). It returns the error of the last try once ctx has ended.
func (e *Evidence) recordJudge(ctx context.Context, id, step string) error {
	err := e.retry(ctx, "evidence judge", id,
		func(storeCtx context.Context) error { return e.store.SetEvidenceJudge(storeCtx, id, step) })
	if err != nil {
		return fmt.Errorf("record judge of evidence run %s: %w", id, err)
	}
	return nil
}

// capture captures run at repo's revision, or reads its supplied files. A captured run's grant and holder are recorded when
// its grant holds, before its gate launches. It returns an error when the gate detaches, and when ctx ends before the capture
// has ended: before its gate launched, or before its supplied files were read. Nothing ran then, so nothing is recorded.
func (e *Evidence) capture(ctx context.Context, repo string, run store.EvidenceRun) (store.EvidenceCapture, error) {
	if run.Source == evidence.SourceSupplied {
		capture := readSupplied(ctx, repo, run.Revision, run.Spec.Supplied, e.blobs)
		// A read that ctx cut short has not ended, so a restart reads the files again rather than recording the cut.
		if capture.NotCaptured != "" && ctx.Err() != nil {
			return store.EvidenceCapture{}, fmt.Errorf("read evidence run %s: %w", run.ID, ctx.Err())
		}
		return capture, nil
	}
	launched := false
	started := func(startCtx context.Context, grant, holder string) error {
		// A refused record is tried again until it lands, since a gate whose grant is unrecorded does not launch. Each try
		// runs on startCtx, so a cut ends the record unmade and the run is left for a restart.
		if err := e.retry(startCtx, "evidence grant", run.ID,
			func(context.Context) error { return e.store.SetEvidenceGrant(startCtx, run.ID, grant, holder) },
		); err != nil {
			return fmt.Errorf("record grant of evidence run %s: %w", run.ID, err)
		}
		launched = true
		return nil
	}
	capture, err := e.captures.run(ctx, repo, run.Revision, run.ID, run.Spec, started)
	// A capture that ctx cut short before its gate launched has not ended, so a restart runs it rather than recording the cut.
	if err == nil && !launched && ctx.Err() != nil {
		return store.EvidenceCapture{}, fmt.Errorf("capture evidence run %s: %w", run.ID, ctx.Err())
	}
	return capture, err
}

// evidenceRunCaptured returns run with the exit, timeout, logs and artifacts its capture left, which the judge reads.
func evidenceRunCaptured(run store.EvidenceRun, capture store.EvidenceCapture) store.EvidenceRun {
	run.Exit = capture.Exit
	run.TimedOut = capture.TimedOut
	run.LogHash = capture.LogHash
	run.LogBytes = capture.LogBytes
	run.StderrHash = capture.StderrHash
	run.StderrBytes = capture.StderrBytes
	run.Artifacts = capture.Artifacts
	return run
}

// judgeRun gives run its verdict. A judge that ends with no verdict is followed by another, up to evidenceJudgeTries attempts
// in all, and the last attempt's end then stands as the reason. An attempt whose judge does not start is one too. A judge that
// ends with no verdict while its epic is paused spends no attempt, and the next judge starts once the epic is resumed (see
// judgeOnce). The verdict is recorded once.
func (e *Evidence) judgeRun(ctx context.Context, repo string, run store.EvidenceRun) {
	judges, tries := run.Judges, 1
	for {
		started, verdict, reason, err := e.judgeOnce(ctx, repo, run, judges+1)
		if err != nil {
			e.logger.WarnContext(ctx, "evidence judge stopped", "run", run.ID, "err", err)
			return
		}
		if started {
			judges++
		}
		if verdict == "" && started {
			paused, pauseErr := e.epicPaused(ctx, run.Epic)
			if pauseErr != nil {
				e.logger.WarnContext(ctx, "evidence judge stopped", "run", run.ID, "err", pauseErr)
				return
			}
			if paused {
				continue
			}
		}
		if verdict != "" || tries == evidenceJudgeTries {
			e.recordVerdict(ctx, run, verdict, reason)
			return
		}
		tries++
	}
}

// epicPaused reports whether epic is paused in the store.
func (e *Evidence) epicPaused(ctx context.Context, epic string) (bool, error) {
	row, err := e.store.Epic(ctx, epic)
	if err != nil {
		return false, fmt.Errorf("epic %s: %w", epic, err)
	}
	return row.Paused, nil
}

// awaitResume returns once epic is not paused. While it is, it reads the epic again each evidencePauseRecheck, so a judge that
// waits here starts once the epic is resumed. It returns an error when the epic cannot be read, or when ctx ends first.
func (e *Evidence) awaitResume(ctx context.Context, epic string) error {
	for {
		paused, err := e.epicPaused(ctx, epic)
		if err != nil || !paused {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for epic %s to resume: %w", epic, ctx.Err())
		case <-e.after(evidencePauseRecheck):
		}
	}
}

// judgeOnce starts judge number judge on run's kept output and waits for it to end. A judge does not start while run's epic is
// paused, so it waits for the epic to be resumed first (see awaitResume). It returns whether the judge started and was recorded
// as run's judge, and the verdict its end gives, or the reason it gives none. A judge that does not start gives no verdict, and
// the reason is why. An error is returned only when the step cannot be recorded or waited for.
func (e *Evidence) judgeOnce(
	ctx context.Context, repo string, run store.EvidenceRun, judge int,
) (bool, string, string, error) {
	if err := ctx.Err(); err != nil {
		return false, "", "", fmt.Errorf("judge evidence run %s: %w", run.ID, err)
	}
	if err := e.awaitResume(ctx, run.Epic); err != nil {
		return false, "", "", fmt.Errorf("judge evidence run %s: %w", run.ID, err)
	}
	step, refusal := e.startJudge(ctx, repo, run, judge)
	if refusal != "" {
		return false, "", refusal, nil
	}
	if err := ctx.Err(); err != nil {
		return false, "", "", fmt.Errorf("judge evidence run %s: %w", run.ID, err)
	}
	if err := e.recordJudge(ctx, run.ID, step); err != nil {
		return false, "", "", err
	}
	row, err := e.awaitJudge(ctx, step)
	if err != nil {
		return true, "", "", err
	}
	verdict, reason := judgeVerdict(row)
	return true, verdict, reason, nil
}

// startJudge starts judge number judge of run at repo under the id evidenceJudgeID derives for it. It returns the step's id, or
// the reason the step did not start: its prompt could not be built, its task could not be read, or Start refused it. A run of a
// task (T<seq>) reads the task from its epic, and its prompt opens with the lines judgeSpecLines gives for the task, then a blank
// line; a green run that is not a task's reads its epic's runs and its prompt ends with the lines judgeCarryLines gives, after
// a blank line; a red run that is not a task's reads nothing and its prompt is judgePrompt's alone. Start returns the id of a
// step whose StartStep failed after its row was written, with that row ended failed, so such a judge returns its id and its end
// gives the reason; the start's error is that end's reason.
func (e *Evidence) startJudge(ctx context.Context, repo string, run store.EvidenceRun, judge int) (string, string) {
	prompt, err := judgePrompt(run, e.blobs)
	if err != nil {
		return "", err.Error()
	}
	lines, err := e.judgeTaskLines(ctx, run)
	if err != nil {
		return "", err.Error()
	}
	if len(lines) > 0 {
		prompt = strings.Join(lines, "\n") + "\n\n" + prompt
	}
	carry, err := e.judgeCarry(ctx, run)
	if err != nil {
		return "", err.Error()
	}
	if len(carry) > 0 {
		prompt += "\n\n" + strings.Join(carry, "\n")
	}
	start := judgeStart(run, repo, prompt)
	start.ID = evidenceJudgeID(run.ID, judge)
	step, err := e.steps.Start(ctx, start)
	if err != nil && step == "" {
		return "", err.Error()
	}
	return step, ""
}

// judgeTaskLines returns the lines judgeSpecLines gives for run when run is a task's run, which its Item names as T<seq>, and
// nil for any other run, which reads nothing. It returns an error when the epic cannot be read, or the epic has no task run names.
func (e *Evidence) judgeTaskLines(ctx context.Context, run store.EvidenceRun) ([]string, error) {
	if !strings.HasPrefix(run.Item, "T") {
		return nil, nil
	}
	row, err := e.store.Epic(ctx, run.Epic)
	if err != nil {
		return nil, fmt.Errorf("epic %s of evidence run %s: %w", run.Epic, run.ID, err)
	}
	for _, task := range row.Tasks {
		if fmt.Sprintf("T%d", task.Seq) == run.Item {
			return judgeSpecLines(run, task), nil
		}
	}
	return nil, fmt.Errorf("task %s of evidence run %s: epic %s has no such task", run.Item, run.ID, run.Epic)
}

// judgeWait is the state of one await of a judge step: its subscription, and the one retry timer armed while a Subscribe has
// failed.
type judgeWait struct {
	id     string
	sub    *StepSubscription
	retry  <-chan time.Time
	disarm func() bool
}

// awaitJudge waits until the judge step with id ends, and returns its row. It subscribes to the step's ends and holds, and never
// polls. A row that only holds the step is ignored, since the judge reads only its verdict. A Subscribe that fails with an error
// matching store.ErrNoStep is returned, as nothing will end the step. Any other failed Subscribe is logged and arms one retry timer
// of evidenceSubscribeRetry, and when it fires the step is subscribed to again. It returns an error when ctx ends first. The
// subscription is closed, and an armed timer stopped, when it returns.
func (e *Evidence) awaitJudge(ctx context.Context, id string) (store.StepRow, error) {
	wait := &judgeWait{id: id}
	defer wait.close()
	if err := e.subscribeJudge(ctx, wait); err != nil {
		return store.StepRow{}, err
	}
	for {
		select {
		case <-ctx.Done():
			return store.StepRow{}, fmt.Errorf("wait for judge step %s: %w", id, ctx.Err())
		case <-wait.ready():
			if row, ok := judgeEndRow(wait.sub.Take()); ok {
				return row, nil
			}
		case <-wait.retry:
			wait.retry, wait.disarm = nil, nil
			if err := e.subscribeJudge(ctx, wait); err != nil {
				return store.StepRow{}, err
			}
		}
	}
}

// subscribeJudge subscribes wait to its judge step. A Subscribe that fails with an error matching store.ErrNoStep is returned
// wrapped, as nothing will end the step. Any other failure is logged, and arms the retry timer of wait.
func (e *Evidence) subscribeJudge(ctx context.Context, wait *judgeWait) error {
	sub, err := e.steps.Subscribe(ctx, []string{wait.id})
	if errors.Is(err, store.ErrNoStep) {
		return fmt.Errorf("wait for judge step %s: %w", wait.id, err)
	}
	if err != nil {
		e.logger.ErrorContext(ctx, "subscribe to judge step", "step", wait.id, "err", err)
		wait.retry, wait.disarm = e.newTimer(evidenceSubscribeRetry)
		return nil
	}
	wait.sub = sub
	return nil
}

// ready returns the channel that signals the pending rows of wait. It is nil while wait has no subscription, which blocks a
// select on it.
func (wait *judgeWait) ready() <-chan struct{} {
	if wait.sub == nil {
		return nil
	}
	return wait.sub.Ready()
}

// close stops the armed retry timer of wait, if one is armed, and closes its subscription, if it has one.
func (wait *judgeWait) close() {
	if wait.disarm != nil {
		wait.disarm()
	}
	if wait.sub != nil {
		wait.sub.Close()
	}
}

// judgeEndRow returns the first row in rows that ends its step, and whether there is one. A row that only holds its step is not.
func judgeEndRow(rows []store.StepRow) (store.StepRow, bool) {
	for _, row := range rows {
		if row.End != "" {
			return row, true
		}
	}
	return store.StepRow{}, false
}

// recordVerdict records verdict and reason for run, and whether run passes its phase, unless ctx has ended. A write the
// store refuses is retried until it lands or ctx ends (see retryStore), and a run left judging then gets its verdict from a
// restart's Resume, which waits on its ended judge.
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
	err := e.retry(ctx, "evidence verdict", run.ID,
		func(storeCtx context.Context) error {
			return e.store.RecordEvidenceVerdict(storeCtx, run.ID, verdictRecord)
		})
	if err != nil {
		e.logger.ErrorContext(ctx, "evidence verdict unstored at close, left for a restart", "run", run.ID, "err", err)
	}
}
