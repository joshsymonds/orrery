package store

import (
	"context"
	"errors"
	"fmt"
	"time"
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
// answers, and what it left. Argv, Files and Briefs keep their order, Briefs holding the ids of the runs. Limit is the
// time limit in seconds. State is running until the capture ends, and Grant and Holder name the pool grant and holder it
// runs under. Artifacts are the files it kept, in order, and Delivered is set once its briefs have been told it ended.
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
func (s *Store) AddCapture(_ context.Context, _ Capture) error {
	return unsupportedError("AddCapture")
}

// SetCaptureGrant records the pool grant and holder that a running capture runs under. A capture in any other state is
// ErrEvidenceState, and an unknown capture is ErrNoCapture.
func (s *Store) SetCaptureGrant(_ context.Context, _, _, _ string) error {
	return unsupportedError("SetCaptureGrant")
}

// RecordCaptureEnd stores what a running capture left in one write, with its files in the order given, and ends it at
// end.At. A capture in any other state is ErrEvidenceState and changes nothing.
func (s *Store) RecordCaptureEnd(_ context.Context, _ string, _ EvidenceCapture) error {
	return unsupportedError("RecordCaptureEnd")
}

// MarkCaptureDelivered records that the briefs of an ended capture have been told it ended, so UndeliveredCaptures no
// longer returns it. An unknown capture is ErrNoCapture.
func (s *Store) MarkCaptureDelivered(_ context.Context, _ string) error {
	return unsupportedError("MarkCaptureDelivered")
}

// Capture returns the stored capture with id, its files and briefs loaded. It returns an error matching ErrNoCapture
// when no capture has that id.
func (s *Store) Capture(_ context.Context, _ string) (Capture, error) {
	return unsupported[Capture]("Capture")
}

// RunningCaptures returns the captures that are running, ordered by created, then id.
func (s *Store) RunningCaptures(_ context.Context) ([]Capture, error) {
	return unsupported[[]Capture]("RunningCaptures")
}

// UndeliveredCaptures returns the ended captures whose briefs have not been told they ended, ordered by end, then id.
func (s *Store) UndeliveredCaptures(_ context.Context) ([]Capture, error) {
	return unsupported[[]Capture]("UndeliveredCaptures")
}

// CapturesAt returns the captures of repository at commit, ordered by created, then id.
func (s *Store) CapturesAt(_ context.Context, _, _ string) ([]Capture, error) {
	return unsupported[[]Capture]("CapturesAt")
}

// AttachCapture attaches capture to each of runs that is waiting or gathering, in one write. Each attached run takes the
// capture and moves to judging, and the runs are returned as stored. A run that is in another state, or that names no
// stored run, is refused and changes nothing. at is when the capture was attached.
func (s *Store) AttachCapture(_ context.Context, _ string, _ []string, _ time.Time) ([]EvidenceRun, error) {
	return unsupported[[]EvidenceRun]("AttachCapture")
}

// OpenBriefs returns the runs of epic that are waiting or gathering, ordered by created, then id, each with its
// artifacts.
func (s *Store) OpenBriefs(_ context.Context, _ string) ([]EvidenceRun, error) {
	return unsupported[[]EvidenceRun]("OpenBriefs")
}

// EpicsWithWaitingBriefs returns the ids of the epics that hold a waiting run, ordered by id.
func (s *Store) EpicsWithWaitingBriefs(_ context.Context) ([]string, error) {
	return unsupported[[]string]("EpicsWithWaitingBriefs")
}

// StartEvidenceGatherer records step as the next gatherer of epic, started at at, in one write. It numbers the gatherer
// after the epic's last one. An epic with a running gatherer is ErrGathererRunning and records nothing.
func (s *Store) StartEvidenceGatherer(_ context.Context, _, _ string, _ time.Time) (EvidenceGatherer, error) {
	return unsupported[EvidenceGatherer]("StartEvidenceGatherer")
}

// TakeEvidenceBrief makes step the gatherer of the waiting run, which moves to gathering. A run that is not waiting is
// ErrEvidenceState, and an unknown run is ErrNoEvidenceRun.
func (s *Store) TakeEvidenceBrief(_ context.Context, _, _ string) error {
	return unsupportedError("TakeEvidenceBrief")
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
func (s *Store) RunningEvidenceGatherer(_ context.Context, _ string) (EvidenceGatherer, bool, error) {
	gatherer, err := unsupported[EvidenceGatherer]("RunningEvidenceGatherer")
	return gatherer, false, err
}

// RunningEvidenceGatherers returns the running gatherers of every epic, ordered by epic, then seq.
func (s *Store) RunningEvidenceGatherers(_ context.Context) ([]EvidenceGatherer, error) {
	return unsupported[[]EvidenceGatherer]("RunningEvidenceGatherers")
}

// EvidenceGathererOf returns the gatherer whose step is step, and whether one has that step.
func (s *Store) EvidenceGathererOf(_ context.Context, _ string) (EvidenceGatherer, bool, error) {
	gatherer, err := unsupported[EvidenceGatherer]("EvidenceGathererOf")
	return gatherer, false, err
}
