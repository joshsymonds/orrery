package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The states a brief run waits and gathers in before a capture is attached to it. A waiting run has no gatherer and no
// capture yet; a gathering run is held by the gatherer step that took it, and goes back to waiting when that gatherer
// ends without attaching a capture.
const (
	RunWaiting   = "waiting"
	RunGathering = "gathering"
)

// ErrNoCapture reports that no capture has the id asked for.
var ErrNoCapture = errors.New("no capture")

// ErrGathererRunning reports that the epic already has a running gatherer, so another cannot start until it ends.
var ErrGathererRunning = errors.New("epic has a running gatherer")

// Capture is one command orreryd ran for evidence: the command, the repository files it keeps, the evidence runs it
// answers, and what it left. Argv and Files keep their order. Briefs holds the ids of the runs, which the store returns
// in id order. Limit is the
// time limit in seconds. State is running until the capture ends, and Grant and Holder name the pool grant and holder it
// runs under. Artifacts are the files it kept, in order, and Delivered is set once the step that asked for it has been told
// it ended.
// Times are stored at whole seconds, and Ended is zero while the capture runs.
type Capture struct {
	ID          string
	Epic        string
	Repository  string
	Step        string
	Role        string
	Runner      string
	Commit      string
	Argv        []string
	Files       []string
	Briefs      []string
	Limit       int64
	State       string
	Grant       string
	Holder      string
	Exit        int
	TimedOut    bool
	NotCaptured string
	LogHash     string
	LogBytes    int64
	StderrHash  string
	StderrBytes int64
	Artifacts   []EvidenceArtifact
	Delivered   bool
	Created     time.Time
	Ended       time.Time
}

// EvidenceGatherer is one gatherer step of an epic, numbered by Seq in the order the epic started them. Ended is zero
// while the gatherer runs, and an epic has at most one running gatherer.
type EvidenceGatherer struct {
	Epic    string
	Seq     int64
	Step    string
	Started time.Time
	Ended   time.Time
}

// unsupportedError returns the error a stub method returns: it wraps errors.ErrUnsupported and names method.
func unsupportedError(method string) error {
	return fmt.Errorf("%s: %w", method, errors.ErrUnsupported)
}

// unsupported returns the zero value of T with the error unsupportedError gives for method. A stub that returns a value
// returns it, until a later task gives the method a body.
func unsupported[T any](method string) (T, error) {
	var zero T
	return zero, unsupportedError(method)
}

// AddCapture stores c as a running capture in one write, with its files and briefs in the order given.
func (s *Store) AddCapture(ctx context.Context, capture Capture) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		argv, err := encodeStrings(capture.Argv)
		if err != nil {
			return fmt.Errorf("argv of capture %s: %w", capture.ID, err)
		}
		files, err := encodeStrings(capture.Files)
		if err != nil {
			return fmt.Errorf("files of capture %s: %w", capture.ID, err)
		}
		if err = queries.InsertEvidenceCapture(ctx, sqlcgen.InsertEvidenceCaptureParams{
			ID: capture.ID, Epic: capture.Epic, Repository: capture.Repository, Step: capture.Step,
			Role: capture.Role, Runner: capture.Runner, CommitSha: capture.Commit, Argv: argv, Files: files,
			LimitSeconds: capture.Limit, State: captureRunning, Created: capture.Created.Unix(),
		}); err != nil {
			return fmt.Errorf("insert capture %s: %w", capture.ID, err)
		}
		for _, run := range capture.Briefs {
			if err = queries.InsertEvidenceCaptureBrief(ctx, sqlcgen.InsertEvidenceCaptureBriefParams{
				Capture: capture.ID, Run: run,
			}); err != nil {
				return fmt.Errorf("brief %s of capture %s: %w", run, capture.ID, err)
			}
		}
		return nil
	})
}

// SetCaptureGrant records the pool grant and holder that a running capture runs under. A capture in any other state is
// ErrEvidenceState, and an unknown capture is ErrNoCapture.
func (s *Store) SetCaptureGrant(ctx context.Context, id, grant, holder string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := requireCaptureState(ctx, queries, id, captureRunning); err != nil {
			return err
		}
		if err := queries.SetEvidenceCaptureGrant(ctx, sqlcgen.SetEvidenceCaptureGrantParams{
			GrantID: grant, Holder: holder, ID: id,
		}); err != nil {
			return fmt.Errorf("set grant of capture %s: %w", id, err)
		}
		return nil
	})
}

// RecordCaptureEnd stores what a running capture left in one write, with its files in the order given, and ends it at
// end.At. A capture in any other state is ErrEvidenceState and changes nothing.
func (s *Store) RecordCaptureEnd(ctx context.Context, id string, end EvidenceCapture) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := requireCaptureState(ctx, queries, id, captureRunning); err != nil {
			return err
		}
		if err := queries.RecordEvidenceCaptureEnd(ctx, sqlcgen.RecordEvidenceCaptureEndParams{
			Exit: int64(end.Exit), TimedOut: boolInt(end.TimedOut), NotCaptured: end.NotCaptured, LogHash: end.LogHash,
			LogBytes: end.LogBytes, StderrHash: end.StderrHash, StderrBytes: end.StderrBytes, Ended: end.At.Unix(),
			ID: id,
		}); err != nil {
			return fmt.Errorf("record end of capture %s: %w", id, err)
		}
		if err := insertInOrder(end.Artifacts, func(position int64, a EvidenceArtifact) error {
			return queries.InsertEvidenceCaptureFile(ctx, sqlcgen.InsertEvidenceCaptureFileParams{
				Capture: id, Position: position, Path: a.Path, State: a.State, Hash: a.Hash, Blob: a.Blob, Size: a.Size,
			})
		}); err != nil {
			return fmt.Errorf("files of capture %s: %w", id, err)
		}
		return nil
	})
}

// MarkCaptureDelivered records that the step that asked for an ended capture has been told it ended, so UndeliveredCaptures
// no longer returns it. An unknown capture is ErrNoCapture.
func (s *Store) MarkCaptureDelivered(ctx context.Context, id string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if _, err := captureState(ctx, queries, id); err != nil {
			return err
		}
		if err := queries.MarkEvidenceCaptureDelivered(ctx, id); err != nil {
			return fmt.Errorf("mark capture %s delivered: %w", id, err)
		}
		return nil
	})
}

// Capture returns the stored capture with id, its files and briefs loaded. It returns an error matching ErrNoCapture
// when no capture has that id.
func (s *Store) Capture(ctx context.Context, id string) (Capture, error) {
	queries := sqlcgen.New(s.read)
	stored, err := queries.GetEvidenceCapture(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Capture{}, fmt.Errorf("capture %s: %w", id, ErrNoCapture)
	}
	if err != nil {
		return Capture{}, fmt.Errorf("get capture %s: %w", id, err)
	}
	return captureOf(ctx, queries, stored)
}

// RunningCaptures returns the captures that are running, ordered by created, then id.
func (s *Store) RunningCaptures(ctx context.Context) ([]Capture, error) {
	queries := sqlcgen.New(s.read)
	stored, err := queries.ListRunningEvidenceCaptures(ctx)
	if err != nil {
		return nil, fmt.Errorf("list running captures: %w", err)
	}
	return capturesOf(ctx, queries, stored)
}

// UndeliveredCaptures returns the ended captures whose asking step has not been told they ended, ordered by end, then id.
func (s *Store) UndeliveredCaptures(ctx context.Context) ([]Capture, error) {
	queries := sqlcgen.New(s.read)
	stored, err := queries.ListUndeliveredEvidenceCaptures(ctx)
	if err != nil {
		return nil, fmt.Errorf("list undelivered captures: %w", err)
	}
	return capturesOf(ctx, queries, stored)
}

// The states a capture is in: running until RecordCaptureEnd ends it, and ended after.
const (
	captureRunning = "running"
	captureEnded   = "ended"
)

// notCapturedKilled is the NotCaptured of a capture that timed out, and of a run attached to one. It is the reason the
// capture's end gives it.
const notCapturedKilled = "killed at its limit"

// artifactMissing is the state of an artifact a run names that its capture kept no file of.
const artifactMissing = "missing"

// requireCaptureState returns an error matching ErrNoCapture when no capture has id, and one matching ErrEvidenceState when
// the capture is not in state want.
func requireCaptureState(ctx context.Context, queries *sqlcgen.Queries, id, want string) error {
	state, err := captureState(ctx, queries, id)
	if err != nil {
		return err
	}
	if state != want {
		return fmt.Errorf("capture %s is %s, not %s: %w", id, state, want, ErrEvidenceState)
	}
	return nil
}

// captureState returns the state of the capture with id, or an error matching ErrNoCapture when no capture has that id.
func captureState(ctx context.Context, queries *sqlcgen.Queries, id string) (string, error) {
	state, err := queries.GetEvidenceCaptureState(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("capture %s: %w", id, ErrNoCapture)
	}
	if err != nil {
		return "", fmt.Errorf("read state of capture %s: %w", id, err)
	}
	return state, nil
}

// capturesOf returns the captures of the stored rows in order, each with its files and briefs loaded.
func capturesOf(ctx context.Context, queries *sqlcgen.Queries, stored []sqlcgen.EvidenceCapture) ([]Capture, error) {
	return convertInOrder(stored, func(row sqlcgen.EvidenceCapture) (Capture, error) {
		return captureOf(ctx, queries, row)
	})
}

// captureOf returns the capture a stored row holds, with its files and briefs loaded.
func captureOf(ctx context.Context, queries *sqlcgen.Queries, stored sqlcgen.EvidenceCapture) (Capture, error) {
	files, err := queries.ListEvidenceCaptureFiles(ctx, stored.ID)
	if err != nil {
		return Capture{}, fmt.Errorf("list files of capture %s: %w", stored.ID, err)
	}
	briefs, err := queries.ListEvidenceCaptureBriefs(ctx, stored.ID)
	if err != nil {
		return Capture{}, fmt.Errorf("list briefs of capture %s: %w", stored.ID, err)
	}
	return captureWith(stored, files, briefs)
}

// captureWith returns the capture a stored row holds, with its files as the artifacts it left and its briefs as given.
func captureWith(
	stored sqlcgen.EvidenceCapture, files []sqlcgen.EvidenceCaptureFile, briefs []string,
) (Capture, error) {
	argv, err := decodeStrings(stored.Argv)
	if err != nil {
		return Capture{}, fmt.Errorf("argv of capture %s: %w", stored.ID, err)
	}
	paths, err := decodeStrings(stored.Files)
	if err != nil {
		return Capture{}, fmt.Errorf("files of capture %s: %w", stored.ID, err)
	}
	capture := Capture{
		ID: stored.ID, Epic: stored.Epic, Repository: stored.Repository, Step: stored.Step, Role: stored.Role,
		Runner: stored.Runner, Commit: stored.CommitSha, Argv: argv, Files: paths, Briefs: briefs,
		Limit: stored.LimitSeconds, State: stored.State, Grant: stored.GrantID, Holder: stored.Holder,
		Exit: int(stored.Exit), TimedOut: stored.TimedOut != 0, NotCaptured: stored.NotCaptured,
		LogHash: stored.LogHash, LogBytes: stored.LogBytes, StderrHash: stored.StderrHash,
		StderrBytes: stored.StderrBytes, Artifacts: mapInOrder(files, captureArtifactOf),
		Delivered: stored.Delivered != 0, Created: time.Unix(stored.Created, 0),
	}
	if stored.Ended != 0 {
		capture.Ended = time.Unix(stored.Ended, 0)
	}
	return capture, nil
}

// captureArtifactOf returns the artifact a stored file row holds.
func captureArtifactOf(file sqlcgen.EvidenceCaptureFile) EvidenceArtifact {
	return EvidenceArtifact{Path: file.Path, State: file.State, Hash: file.Hash, Blob: file.Blob, Size: file.Size}
}

// CapturesAt returns the captures of repository at commit, ordered by created, then id, each with its files and briefs
// loaded.
func (s *Store) CapturesAt(ctx context.Context, repository, commit string) ([]Capture, error) {
	queries := sqlcgen.New(s.read)
	stored, err := queries.ListEvidenceCapturesAt(ctx, sqlcgen.ListEvidenceCapturesAtParams{
		Repository: repository, CommitSha: commit,
	})
	if err != nil {
		return nil, fmt.Errorf("list captures at %s: %w", commit, err)
	}
	return capturesOf(ctx, queries, stored)
}

// AttachCapture attaches the ended capture to each of runs, in one write, and returns the runs as stored, in the order
// given. Each run that is waiting or gathering takes the capture's id, its exit, timeout and logs, and the artifacts its
// item names: the capture's file of each path, or a missing file when the capture kept none. A run whose capture timed out
// or exited EnvironmentExit ends not captured, and a run whose capture has another reason ends with that reason (see
// attachNotCaptured). Any other run moves to judging. A capture that is not ended is ErrEvidenceState, and one that does
// not exist ErrNoCapture. A run that names no stored run is ErrNoEvidenceRun, and one that is not waiting or gathering
// ErrEvidenceState. Any refusal changes nothing. at is when the capture was attached, and the time each run it ends not
// captured ends.
func (s *Store) AttachCapture(ctx context.Context, capture string, runs []string, at time.Time) ([]EvidenceRun, error) {
	var attached []EvidenceRun
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := requireCaptureState(ctx, queries, capture, captureEnded); err != nil {
			return err
		}
		stored, err := queries.GetEvidenceCapture(ctx, capture)
		if err != nil {
			return fmt.Errorf("get capture %s: %w", capture, err)
		}
		ended, err := captureOf(ctx, queries, stored)
		if err != nil {
			return err
		}
		attached = make([]EvidenceRun, 0, len(runs))
		for _, id := range runs {
			run, attachErr := attachRun(ctx, queries, ended, id, at)
			if attachErr != nil {
				return attachErr
			}
			attached = append(attached, run)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return attached, nil
}

// attachRun attaches the ended capture to the run with id, as AttachCapture describes, and returns the run as stored. A run
// that is not waiting or gathering is ErrEvidenceState, and an unknown run is ErrNoEvidenceRun.
func attachRun(
	ctx context.Context, queries *sqlcgen.Queries, capture Capture, id string, at time.Time,
) (EvidenceRun, error) {
	state, err := queries.GetEvidenceRunState(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return EvidenceRun{}, fmt.Errorf("evidence run %s: %w", id, ErrNoEvidenceRun)
	}
	if err != nil {
		return EvidenceRun{}, fmt.Errorf("read state of evidence run %s: %w", id, err)
	}
	if state != RunWaiting && state != RunGathering {
		return EvidenceRun{}, fmt.Errorf(
			"evidence run %s is %s, not waiting or gathering: %w",
			id,
			state,
			ErrEvidenceState,
		)
	}
	row, err := queries.GetEvidenceRun(ctx, id)
	if err != nil {
		return EvidenceRun{}, fmt.Errorf("get evidence run %s: %w", id, err)
	}
	spec, err := decodeItem(row.Spec)
	if err != nil {
		return EvidenceRun{}, fmt.Errorf("spec of evidence run %s: %w", id, err)
	}
	taken, err := queries.AttachEvidenceRunCapture(
		ctx,
		sqlcgen.AttachEvidenceRunCaptureParams{Capture: capture.ID, ID: id},
	)
	if err != nil {
		return EvidenceRun{}, fmt.Errorf("attach capture %s to evidence run %s: %w", capture.ID, id, err)
	}
	if taken != 1 {
		return EvidenceRun{}, fmt.Errorf("attach capture %s to evidence run %s: %w", capture.ID, id, ErrEvidenceState)
	}
	notCaptured := attachNotCaptured(capture)
	next, ended := runJudging, int64(0)
	if notCaptured != "" {
		next, ended = runEnded, at.Unix()
	}
	if err = queries.RecordEvidenceRunCapture(ctx, sqlcgen.RecordEvidenceRunCaptureParams{
		Exit: int64(capture.Exit), TimedOut: boolInt(capture.TimedOut), NotCaptured: notCaptured,
		LogHash: capture.LogHash, LogBytes: capture.LogBytes, StderrHash: capture.StderrHash,
		StderrBytes: capture.StderrBytes, State: next, Ended: ended, ID: id,
	}); err != nil {
		return EvidenceRun{}, fmt.Errorf("record capture of evidence run %s: %w", id, err)
	}
	if err = insertInOrder(
		attachArtifacts(capture.Artifacts, spec.Artifacts),
		func(position int64, a EvidenceArtifact) error {
			return queries.InsertEvidenceArtifact(ctx, sqlcgen.InsertEvidenceArtifactParams{
				Run: id, Position: position, Path: a.Path, State: a.State, Hash: a.Hash, Blob: a.Blob, Size: a.Size,
			})
		},
	); err != nil {
		return EvidenceRun{}, fmt.Errorf("artifacts of evidence run %s: %w", id, err)
	}
	row, err = queries.GetEvidenceRun(ctx, id)
	if err != nil {
		return EvidenceRun{}, fmt.Errorf("read evidence run %s: %w", id, err)
	}
	return evidenceRunOf(ctx, queries, row)
}

// attachNotCaptured returns the reason a run attached to capture captured nothing, as evidenceCapture.end gives it: a capture
// that timed out was killed at its limit, and one that exited EnvironmentExit failed in its environment. Any other capture
// keeps the reason it has, which is empty when it captured.
func attachNotCaptured(capture Capture) string {
	switch {
	case capture.TimedOut:
		return notCapturedKilled
	case capture.Exit == EnvironmentExit:
		return NotCapturedEnvironment
	}
	return capture.NotCaptured
}

// attachArtifacts returns the artifacts of a run whose item names paths, in the order given: the capture's file of each
// path, or a missing file when the capture kept none of that path.
func attachArtifacts(files []EvidenceArtifact, paths []string) []EvidenceArtifact {
	artifacts := make([]EvidenceArtifact, 0, len(paths))
	for _, path := range paths {
		i := slices.IndexFunc(files, func(file EvidenceArtifact) bool { return file.Path == path })
		if i < 0 {
			artifacts = append(artifacts, EvidenceArtifact{Path: path, State: artifactMissing})
			continue
		}
		artifacts = append(artifacts, files[i])
	}
	return artifacts
}

// OpenBriefs returns the runs of epic that are waiting or gathering, ordered by created, then id, each with its
// artifacts. It reads the runs in one query and the artifacts of the epic in another.
func (s *Store) OpenBriefs(ctx context.Context, epic string) ([]EvidenceRun, error) {
	queries := sqlcgen.New(s.read)
	stored, err := queries.ListOpenEvidenceBriefs(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list open briefs of epic %s: %w", epic, err)
	}
	artifacts, err := queries.ListEpicEvidenceArtifacts(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list evidence artifacts of epic %s: %w", epic, err)
	}
	return evidenceRunsOf(stored, artifacts)
}

// EpicsWithWaitingBriefs returns the ids of the epics that hold a waiting run, ordered by id.
func (s *Store) EpicsWithWaitingBriefs(ctx context.Context) ([]string, error) {
	epics, err := sqlcgen.New(s.read).ListEpicsWithWaitingBriefs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list epics with waiting briefs: %w", err)
	}
	return epics, nil
}

// StartEvidenceGatherer records step as the next gatherer of epic, started at at, in one write. It numbers the gatherer
// after the epic's last one. An epic with a running gatherer is ErrGathererRunning and records nothing. Every waiting
// run of the epic moves to gathering, held by step.
func (s *Store) StartEvidenceGatherer(ctx context.Context, epic, step string, at time.Time) (EvidenceGatherer, error) {
	var gatherer EvidenceGatherer
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		_, err := queries.GetRunningEvidenceGatherer(ctx, epic)
		if err == nil {
			return fmt.Errorf("start gatherer %s of epic %s: %w", step, epic, ErrGathererRunning)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read running gatherer of epic %s: %w", epic, err)
		}
		stored, err := queries.StartEvidenceGatherer(ctx, sqlcgen.StartEvidenceGathererParams{
			Epic: epic, Step: step, Started: at.Unix(),
		})
		if err != nil {
			return fmt.Errorf("insert gatherer %s of epic %s: %w", step, epic, err)
		}
		gatherer = gathererOf(stored)
		open, err := queries.ListOpenEvidenceBriefs(ctx, epic)
		if err != nil {
			return fmt.Errorf("list open briefs of epic %s: %w", epic, err)
		}
		for _, run := range open {
			if run.State != RunWaiting {
				continue
			}
			if takeErr := takeBrief(ctx, queries, run.ID, step); takeErr != nil {
				return takeErr
			}
		}
		return nil
	})
	if err != nil {
		return EvidenceGatherer{}, err
	}
	return gatherer, nil
}

// TakeEvidenceBrief makes step the gatherer of the waiting run, which moves to gathering. A run that is not waiting is
// ErrEvidenceState, and an unknown run is ErrNoEvidenceRun.
func (s *Store) TakeEvidenceBrief(ctx context.Context, run, step string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := requireRunState(ctx, queries, run, RunWaiting); err != nil {
			return err
		}
		return takeBrief(ctx, queries, run, step)
	})
}

// takeBrief moves the waiting run id to gathering, held by step. The run must be waiting: a run that is not is
// ErrEvidenceState.
func takeBrief(ctx context.Context, queries *sqlcgen.Queries, id, step string) error {
	taken, err := queries.TakeEvidenceBrief(ctx, sqlcgen.TakeEvidenceBriefParams{Gatherer: step, ID: id})
	if err != nil {
		return fmt.Errorf("take brief %s: %w", id, err)
	}
	if taken != 1 {
		return fmt.Errorf("take brief %s: %w", id, ErrEvidenceState)
	}
	return nil
}

// EndEvidenceGatherer ends the running gatherer step of epic at at, in one write. Each brief step holds goes back to
// waiting with no gatherer, and counts one more gatherer that ended leaving it unattached. The briefs it released are
// returned as stored. leftReason is the reason the gatherer gives for ending without attaching a capture.
func (s *Store) EndEvidenceGatherer(
	_ context.Context, _, _, _ string, _ time.Time,
) ([]EvidenceRun, error) {
	return unsupported[[]EvidenceRun]("EndEvidenceGatherer")
}

// RunningEvidenceGatherer returns the running gatherer of epic, and whether the epic has one.
func (s *Store) RunningEvidenceGatherer(ctx context.Context, epic string) (EvidenceGatherer, bool, error) {
	stored, err := sqlcgen.New(s.read).GetRunningEvidenceGatherer(ctx, epic)
	if errors.Is(err, sql.ErrNoRows) {
		return EvidenceGatherer{}, false, nil
	}
	if err != nil {
		return EvidenceGatherer{}, false, fmt.Errorf("get running gatherer of epic %s: %w", epic, err)
	}
	return gathererOf(stored), true, nil
}

// RunningEvidenceGatherers returns the running gatherers of every epic, ordered by epic, then seq.
func (s *Store) RunningEvidenceGatherers(ctx context.Context) ([]EvidenceGatherer, error) {
	stored, err := sqlcgen.New(s.read).ListRunningEvidenceGatherers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list running gatherers: %w", err)
	}
	return mapInOrder(stored, gathererOf), nil
}

// EvidenceGathererOf returns the gatherer whose step is step, and whether one has that step.
func (s *Store) EvidenceGathererOf(ctx context.Context, step string) (EvidenceGatherer, bool, error) {
	stored, err := sqlcgen.New(s.read).GetEvidenceGathererOf(ctx, step)
	if errors.Is(err, sql.ErrNoRows) {
		return EvidenceGatherer{}, false, nil
	}
	if err != nil {
		return EvidenceGatherer{}, false, fmt.Errorf("get gatherer of step %s: %w", step, err)
	}
	return gathererOf(stored), true, nil
}

// gathererOf returns the gatherer a stored row holds. Ended is zero while the gatherer runs.
func gathererOf(stored sqlcgen.EvidenceGatherer) EvidenceGatherer {
	gatherer := EvidenceGatherer{
		Epic: stored.Epic, Seq: stored.Seq, Step: stored.Step, Started: time.Unix(stored.Started, 0),
	}
	if stored.Ended != 0 {
		gatherer.Ended = time.Unix(stored.Ended, 0)
	}
	return gatherer
}
