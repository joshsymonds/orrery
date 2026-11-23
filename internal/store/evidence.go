package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrNoEvidenceRun reports that no evidence run has the id asked for.
var ErrNoEvidenceRun = errors.New("no evidence run")

// ErrEvidenceState reports that a write does not fit the state its evidence run is in.
var ErrEvidenceState = errors.New("evidence run is in another state")

// NotCapturedEnvironment is the NotCaptured of a capture that exited EnvironmentExit. Its gate ran, but nothing it left is
// judged, and the kinds that read its exit run it again.
const NotCapturedEnvironment = "exited 75 for its environment"

// The states an evidence run moves through in order: capturing, then judging, then ended. A brief run may wait and gather
// before it is attached and judged (see RunWaiting and RunGathering).
const (
	runCapturing = "capturing"
	runJudging   = "judging"
	runEnded     = "ended"
)

// EvidenceRun is one run of an evidence item: a capture or supplied files that show a Requirement or task holds at
// one revision in one phase. Spec is the item as the run was added, Grant and Holder name the pool grant and holder
// the capture ran under, and Artifacts keep the order the capture gave them. Capture is the id of the capture attached
// to the run, Gatherer the gatherer step holding its brief, and Gatherers the count of gatherers that ended leaving it
// unattached. Times are stored at whole seconds, and Ended is zero until the run ends.
type EvidenceRun struct {
	ID          string
	Epic        string
	Item        string
	Revision    string
	Phase       string
	Source      string
	State       string
	Spec        evidence.Item
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
	JudgeStep   string
	Judges      int
	Verdict     string
	JudgeReason string
	Passed      bool
	Capture     string
	Gatherer    string
	Gatherers   int
	Created     time.Time
	Ended       time.Time
}

// EvidenceArtifact is one artifact a run left: its path, whether it was kept, missing or over the cap, and for a kept
// one its hash, blob and size.
type EvidenceArtifact struct {
	Path  string
	State string
	Hash  string
	Blob  string
	Size  int64
}

// EvidenceCapture is what a run's capture leaves: the exit and timeout of the capture, the reason it captured nothing
// when it did not, the hash and size of its log, its artifacts, and when it finished.
type EvidenceCapture struct {
	Exit        int
	TimedOut    bool
	NotCaptured string
	LogHash     string
	LogBytes    int64
	StderrHash  string
	StderrBytes int64
	Artifacts   []EvidenceArtifact
	At          time.Time
}

// EvidenceVerdict is the judge's conclusion on a run: its verdict, the judge's reason, whether the run passed, and
// when the verdict was given.
type EvidenceVerdict struct {
	Verdict string
	Reason  string
	Passed  bool
	At      time.Time
}

// AddEvidenceRun stores run as one write, in run.State or in state capturing when run.State is empty. Spec is stored as
// JSON and Created at whole seconds. The grant, capture and verdict fields keep their zero values until later writes set
// them. An id already stored is refused by the primary key and changes nothing.
func (s *Store) AddEvidenceRun(ctx context.Context, run EvidenceRun) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		spec, err := encodeItem(run.Spec)
		if err != nil {
			return fmt.Errorf("spec of evidence run %s: %w", run.ID, err)
		}
		state := run.State
		if state == "" {
			state = runCapturing
		}
		if err = queries.InsertEvidenceRun(ctx, sqlcgen.InsertEvidenceRunParams{
			ID: run.ID, Epic: run.Epic, Item: run.Item, Revision: run.Revision, Phase: run.Phase, Source: run.Source,
			State: state, Spec: spec, Created: run.Created.Unix(),
		}); err != nil {
			return fmt.Errorf("insert evidence run %s: %w", run.ID, err)
		}
		return nil
	})
}

// SetEvidenceGrant records the pool grant and holder that a capturing run runs under, in one write. A run in any
// other state is ErrEvidenceState, and an unknown run is ErrNoEvidenceRun.
func (s *Store) SetEvidenceGrant(ctx context.Context, id, grant, holder string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := requireRunState(ctx, queries, id, runCapturing); err != nil {
			return err
		}
		if err := queries.SetEvidenceRunGrant(ctx, sqlcgen.SetEvidenceRunGrantParams{
			GrantID: grant, Holder: holder, ID: id,
		}); err != nil {
			return fmt.Errorf("set grant of evidence run %s: %w", id, err)
		}
		return nil
	})
}

// RecordEvidenceCapture stores the capture of a capturing run in one write: its exit, timeout, reason for capturing
// nothing, log, and artifacts in the order given. The run moves to judging, or to ended at capture.At when the capture
// captured nothing. A run in any other state is ErrEvidenceState and changes nothing.
func (s *Store) RecordEvidenceCapture(ctx context.Context, id string, capture EvidenceCapture) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := requireRunState(ctx, queries, id, runCapturing); err != nil {
			return err
		}
		state, ended := runJudging, int64(0)
		if capture.NotCaptured != "" {
			state, ended = runEnded, capture.At.Unix()
		}
		if err := queries.RecordEvidenceRunCapture(ctx, sqlcgen.RecordEvidenceRunCaptureParams{
			Exit: int64(capture.Exit), TimedOut: boolInt(capture.TimedOut), NotCaptured: capture.NotCaptured,
			LogHash: capture.LogHash, LogBytes: capture.LogBytes, StderrHash: capture.StderrHash,
			StderrBytes: capture.StderrBytes, State: state, Ended: ended, ID: id,
		}); err != nil {
			return fmt.Errorf("record capture of evidence run %s: %w", id, err)
		}
		if err := insertInOrder(capture.Artifacts, func(position int64, a EvidenceArtifact) error {
			return queries.InsertEvidenceArtifact(ctx, sqlcgen.InsertEvidenceArtifactParams{
				Run: id, Position: position, Path: a.Path, State: a.State, Hash: a.Hash, Blob: a.Blob, Size: a.Size,
			})
		}); err != nil {
			return fmt.Errorf("artifacts of evidence run %s: %w", id, err)
		}
		return nil
	})
}

// SetEvidenceJudge records step as the judge step of a judging run and counts one more judge on it, in one write. A
// second call replaces the step and counts again, so a rejudge keeps the latest step. A run in any other state is
// ErrEvidenceState.
func (s *Store) SetEvidenceJudge(ctx context.Context, id, step string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := requireRunState(ctx, queries, id, runJudging); err != nil {
			return err
		}
		if err := queries.SetEvidenceRunJudge(ctx, sqlcgen.SetEvidenceRunJudgeParams{
			JudgeStep: step, ID: id,
		}); err != nil {
			return fmt.Errorf("set judge of evidence run %s: %w", id, err)
		}
		return nil
	})
}

// RecordEvidenceVerdict ends a judging run with verdict in one write. A run in any other state is ErrEvidenceState and
// changes nothing.
func (s *Store) RecordEvidenceVerdict(ctx context.Context, id string, verdict EvidenceVerdict) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := requireRunState(ctx, queries, id, runJudging); err != nil {
			return err
		}
		if err := queries.RecordEvidenceRunVerdict(ctx, sqlcgen.RecordEvidenceRunVerdictParams{
			Verdict: verdict.Verdict, JudgeReason: verdict.Reason, Passed: boolInt(verdict.Passed),
			Ended: verdict.At.Unix(), ID: id,
		}); err != nil {
			return fmt.Errorf("record verdict of evidence run %s: %w", id, err)
		}
		return nil
	})
}

// EvidenceRun returns the stored run with id, its artifacts in order. It returns an error matching ErrNoEvidenceRun
// when no run has that id.
func (s *Store) EvidenceRun(ctx context.Context, id string) (EvidenceRun, error) {
	queries := sqlcgen.New(s.read)
	stored, err := queries.GetEvidenceRun(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return EvidenceRun{}, fmt.Errorf("evidence run %s: %w", id, ErrNoEvidenceRun)
	}
	if err != nil {
		return EvidenceRun{}, fmt.Errorf("get evidence run %s: %w", id, err)
	}
	return evidenceRunOf(ctx, queries, stored)
}

// EvidenceRuns returns the runs of epic ordered by created, then id, each with its artifacts. It reads the runs in one
// query and the artifacts of all of them in another, however many runs the epic has.
func (s *Store) EvidenceRuns(ctx context.Context, epic string) ([]EvidenceRun, error) {
	return readEvidenceRuns(ctx, sqlcgen.New(s.read), epic)
}

// OpenEvidenceRuns returns every run that is capturing or judging, ordered by created, then id, each with its
// artifacts. It reads the runs in one query and the artifacts of all of them in another.
func (s *Store) OpenEvidenceRuns(ctx context.Context) ([]EvidenceRun, error) {
	queries := sqlcgen.New(s.read)
	stored, err := queries.ListOpenEvidenceRuns(ctx)
	if err != nil {
		return nil, fmt.Errorf("list open evidence runs: %w", err)
	}
	artifacts, err := queries.ListOpenEvidenceArtifacts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list artifacts of open evidence runs: %w", err)
	}
	return evidenceRunsOf(stored, artifacts)
}

// requireRunState returns ErrNoEvidenceRun when no run has id, and ErrEvidenceState when the run is not in state want.
func requireRunState(ctx context.Context, queries *sqlcgen.Queries, id, want string) error {
	state, err := queries.GetEvidenceRunState(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("evidence run %s: %w", id, ErrNoEvidenceRun)
	}
	if err != nil {
		return fmt.Errorf("read state of evidence run %s: %w", id, err)
	}
	if state != want {
		return fmt.Errorf("evidence run %s is %s, not %s: %w", id, state, want, ErrEvidenceState)
	}
	return nil
}

// evidenceRunsOf converts stored rows in order, each with the rows of artifacts whose run is its id, in the order
// artifacts gives them. artifacts holds each run's rows in position order; a row of a run stored does not hold is left
// out.
func evidenceRunsOf(stored []sqlcgen.EvidenceRun, artifacts []sqlcgen.EvidenceArtifact) ([]EvidenceRun, error) {
	byRun := make(map[string][]sqlcgen.EvidenceArtifact)
	for _, artifact := range artifacts {
		byRun[artifact.Run] = append(byRun[artifact.Run], artifact)
	}
	return convertInOrder(stored, func(row sqlcgen.EvidenceRun) (EvidenceRun, error) {
		return evidenceRunWith(row, byRun[row.ID])
	})
}

// evidenceRunOf returns the run a stored row holds, its spec decoded and its artifacts loaded in position order.
func evidenceRunOf(ctx context.Context, queries *sqlcgen.Queries, stored sqlcgen.EvidenceRun) (EvidenceRun, error) {
	artifacts, err := queries.ListEvidenceArtifacts(ctx, stored.ID)
	if err != nil {
		return EvidenceRun{}, fmt.Errorf("list artifacts of evidence run %s: %w", stored.ID, err)
	}
	return evidenceRunWith(stored, artifacts)
}

// evidenceRunWith returns the run a stored row holds, its spec decoded, with artifacts, the rows of its artifacts in
// position order.
func evidenceRunWith(stored sqlcgen.EvidenceRun, artifacts []sqlcgen.EvidenceArtifact) (EvidenceRun, error) {
	spec, err := decodeItem(stored.Spec)
	if err != nil {
		return EvidenceRun{}, fmt.Errorf("spec of evidence run %s: %w", stored.ID, err)
	}
	run := EvidenceRun{
		ID: stored.ID, Epic: stored.Epic, Item: stored.Item, Revision: stored.Revision, Phase: stored.Phase,
		Source: stored.Source, State: stored.State, Spec: spec, Grant: stored.GrantID, Holder: stored.Holder,
		Exit: int(stored.Exit), TimedOut: stored.TimedOut != 0, NotCaptured: stored.NotCaptured,
		LogHash: stored.LogHash, LogBytes: stored.LogBytes, StderrHash: stored.StderrHash,
		StderrBytes: stored.StderrBytes,
		Artifacts:   mapInOrder(artifacts, evidenceArtifactOf),
		JudgeStep:   stored.JudgeStep, Judges: int(stored.Judges), Verdict: stored.Verdict,
		JudgeReason: stored.JudgeReason, Passed: stored.Passed != 0, Capture: stored.Capture,
		Gatherer: stored.Gatherer, Gatherers: int(stored.Gatherers), Created: time.Unix(stored.Created, 0),
	}
	if stored.Ended != 0 {
		run.Ended = time.Unix(stored.Ended, 0)
	}
	return run, nil
}

// evidenceArtifactOf returns the artifact a stored row holds.
func evidenceArtifactOf(a sqlcgen.EvidenceArtifact) EvidenceArtifact {
	return EvidenceArtifact{Path: a.Path, State: a.State, Hash: a.Hash, Blob: a.Blob, Size: a.Size}
}

// boolInt returns the stored form of a flag: 1 for true, 0 for false.
func boolInt(flag bool) int64 {
	if flag {
		return 1
	}
	return 0
}
