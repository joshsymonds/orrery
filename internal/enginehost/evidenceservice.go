package enginehost

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// evidenceServiceUnavailableMessage is the message every evidence RPC is refused with when there is no evidence runner.
const evidenceServiceUnavailableMessage = "evidence unavailable"

// EvidenceCommands is the set of evidence operations the Evidence service answers with. *Evidence satisfies it.
type EvidenceCommands interface {
	Run(ctx context.Context, epic, item, revision, phase string) (store.EvidenceRun, error)
	Runs(ctx context.Context, epic string) ([]store.EvidenceRun, error)
	Artifact(ctx context.Context, run, name string) ([]byte, string, error)
	StderrLog(ctx context.Context, run string) ([]byte, string, error)
}

// CaptureCommands is the set of capture operations the Evidence service answers CaptureEvidence with. *Captures
// satisfies it.
type CaptureCommands interface {
	Capture(ctx context.Context, caller string, args CaptureArgs) (store.Capture, error)
}

// AttachCommands is the set of operations the Evidence service answers AttachEvidence with. *Evidence satisfies it.
type AttachCommands interface {
	Attach(ctx context.Context, caller, capture string, briefs []string) ([]store.EvidenceRun, error)
}

// evidenceService implements the Evidence RPCs over evidence, captures and attacher. A nil captures refuses
// CaptureEvidence as unavailable, and a nil attacher refuses AttachEvidence as unavailable.
type evidenceService struct {
	evidence EvidenceCommands
	captures CaptureCommands
	attacher AttachCommands
}

// ready returns the refusal every evidence RPC gets when there is no evidence runner.
func (s *evidenceService) ready() error {
	if s.evidence == nil {
		return evidenceServiceRefuse(connect.CodeUnavailable, evidenceServiceUnavailableMessage)
	}
	return nil
}

// RunEvidence refuses an empty epic, item, revision or phase before anything runs. It then runs the item at the revision
// for the phase and returns the run the runner stored.
func (s *evidenceService) RunEvidence(
	ctx context.Context,
	req *connect.Request[v1.RunEvidenceRequest],
) (*connect.Response[v1.RunEvidenceResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := evidenceServiceCheckRun(req.Msg); err != nil {
		return nil, err
	}
	run, err := s.evidence.Run(
		ctx, req.Msg.GetEpic(), req.Msg.GetItem(), req.Msg.GetRevision(), req.Msg.GetPhase(),
	)
	if err != nil {
		return nil, evidenceServiceRunError(req.Msg.GetEpic(), err)
	}
	msg, err := evidenceServiceRunMessage(run)
	if err != nil {
		return nil, evidenceServiceStoreError(err)
	}
	return connect.NewResponse(&v1.RunEvidenceResponse{Run: msg}), nil
}

// ListEvidence refuses an empty epic before anything is read. It returns the epic's runs in the order the runner gives.
func (s *evidenceService) ListEvidence(
	ctx context.Context,
	req *connect.Request[v1.ListEvidenceRequest],
) (*connect.Response[v1.ListEvidenceResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if req.Msg.GetEpic() == "" {
		return nil, evidenceServiceRefuse(connect.CodeInvalidArgument, "epic: empty")
	}
	stored, err := s.evidence.Runs(ctx, req.Msg.GetEpic())
	if err != nil {
		return nil, evidenceServiceStoreError(err)
	}
	runs, err := evidenceServiceRunMessages(stored)
	if err != nil {
		return nil, evidenceServiceStoreError(err)
	}
	return connect.NewResponse(&v1.ListEvidenceResponse{Runs: runs}), nil
}

// GetEvidenceArtifact refuses an empty run before anything is read, and a stderr request that names an artifact. It
// returns the bytes and hash of the run's artifact named name, of its log when name is empty, or of its stderr log when
// stderr is set.
func (s *evidenceService) GetEvidenceArtifact(
	ctx context.Context,
	req *connect.Request[v1.GetEvidenceArtifactRequest],
) (*connect.Response[v1.GetEvidenceArtifactResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	run, name, stderr := req.Msg.GetRun(), req.Msg.GetName(), req.Msg.GetStderr()
	if run == "" {
		return nil, evidenceServiceRefuse(connect.CodeInvalidArgument, "run: empty")
	}
	if stderr && name != "" {
		return nil, evidenceServiceRefuse(connect.CodeInvalidArgument, "name: a stderr log is named by its run alone")
	}
	var data []byte
	var hash string
	var err error
	if stderr {
		data, hash, err = s.evidence.StderrLog(ctx, run)
	} else {
		data, hash, err = s.evidence.Artifact(ctx, run, name)
	}
	switch {
	case errors.Is(err, store.ErrNoEvidenceRun):
		return nil, evidenceServiceRefuse(connect.CodeNotFound, fmt.Sprintf("run: no run %s", run))
	case errors.Is(err, ErrNoEvidenceArtifact) && stderr:
		return nil, evidenceServiceRefuse(connect.CodeNotFound, fmt.Sprintf("stderr: no stderr log of run %s", run))
	case errors.Is(err, ErrNoEvidenceArtifact):
		return nil, evidenceServiceRefuse(connect.CodeNotFound, fmt.Sprintf("name: no kept artifact %s", name))
	case err != nil:
		return nil, evidenceServiceStoreError(err)
	}
	return connect.NewResponse(&v1.GetEvidenceArtifactResponse{Data: data, Hash: hash}), nil
}

// CaptureEvidence refuses as unavailable when there is no capture service. Otherwise it runs the command for the caller
// session, the session in the Orrery-Session header, and returns the id of the capture it made. A caller that is not a
// running step is FailedPrecondition, and any other refused request is InvalidArgument, each with its reason.
func (s *evidenceService) CaptureEvidence(
	ctx context.Context,
	req *connect.Request[v1.CaptureEvidenceRequest],
) (*connect.Response[v1.CaptureEvidenceResponse], error) {
	if s.captures == nil {
		return nil, evidenceServiceRefuse(connect.CodeUnavailable, evidenceServiceUnavailableMessage)
	}
	capture, err := s.captures.Capture(ctx, req.Header().Get(protocol.HeaderSession), CaptureArgs{
		Commit: req.Msg.GetCommit(),
		Argv:   req.Msg.GetArgv(),
		Runner: req.Msg.GetRunner(),
		Briefs: req.Msg.GetBriefs(),
		Files:  req.Msg.GetFiles(),
	})
	switch {
	case errors.Is(err, ErrCaptureNotStep):
		return nil, evidenceServiceRefuse(connect.CodeFailedPrecondition, err.Error())
	case errors.Is(err, ErrCaptureRefused):
		return nil, evidenceServiceRefuse(connect.CodeInvalidArgument, err.Error())
	case err != nil:
		return nil, evidenceServiceStoreError(err)
	}
	return connect.NewResponse(&v1.CaptureEvidenceResponse{Capture: capture.ID}), nil
}

// AttachEvidence refuses as unavailable when there is no attacher. Otherwise it attaches the capture to the briefs for
// the caller session, the session in the Orrery-Session header, and returns the runs it attached it to. A refused
// request is FailedPrecondition with its reason.
func (s *evidenceService) AttachEvidence(
	ctx context.Context,
	req *connect.Request[v1.AttachEvidenceRequest],
) (*connect.Response[v1.AttachEvidenceResponse], error) {
	if s.attacher == nil {
		return nil, evidenceServiceRefuse(connect.CodeUnavailable, evidenceServiceUnavailableMessage)
	}
	runs, err := s.attacher.Attach(
		ctx, req.Header().Get(protocol.HeaderSession), req.Msg.GetCapture(), req.Msg.GetBriefs(),
	)
	switch {
	case errors.Is(err, ErrAttachRefused):
		return nil, evidenceServiceRefuse(connect.CodeFailedPrecondition, err.Error())
	case err != nil:
		return nil, evidenceServiceStoreError(err)
	}
	msgs, err := evidenceServiceRunMessages(runs)
	if err != nil {
		return nil, evidenceServiceStoreError(err)
	}
	return connect.NewResponse(&v1.AttachEvidenceResponse{Runs: msgs}), nil
}

// evidenceServiceCheckRun returns the refusal of a run request whose epic, item, revision or phase is empty, naming the
// first one that is.
func evidenceServiceCheckRun(req *v1.RunEvidenceRequest) error {
	switch {
	case req.GetEpic() == "":
		return evidenceServiceRefuse(connect.CodeInvalidArgument, "epic: empty")
	case req.GetItem() == "":
		return evidenceServiceRefuse(connect.CodeInvalidArgument, "item: empty")
	case req.GetRevision() == "":
		return evidenceServiceRefuse(connect.CodeInvalidArgument, "revision: empty")
	case req.GetPhase() == "":
		return evidenceServiceRefuse(connect.CodeInvalidArgument, "phase: empty")
	}
	return nil
}

// evidenceServiceRunError returns the refusal of a run of epic that failed: a bad request is InvalidArgument with its
// text, an epic that does not exist is NotFound, and any other failure is Internal.
func evidenceServiceRunError(epic string, err error) error {
	switch {
	case errors.Is(err, ErrEvidenceRefused):
		return evidenceServiceRefuse(connect.CodeInvalidArgument, err.Error())
	case errors.Is(err, store.ErrNoEpic):
		return evidenceServiceRefuse(connect.CodeNotFound, fmt.Sprintf("epic: no epic %s", epic))
	default:
		return evidenceServiceStoreError(err)
	}
}

// evidenceServiceRunMessages returns the EvidenceRun messages of runs, in order. It fails when a run's exit is outside the
// range of int32.
func evidenceServiceRunMessages(runs []store.EvidenceRun) ([]*v1.EvidenceRun, error) {
	out := make([]*v1.EvidenceRun, 0, len(runs))
	for _, run := range runs {
		msg, err := evidenceServiceRunMessage(run)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

// evidenceServiceRunMessage returns the EvidenceRun message of run, field for field: times in unix seconds, 0 for zero,
// and its artifacts in order. It fails when run's exit is outside the range of int32.
func evidenceServiceRunMessage(run store.EvidenceRun) (*v1.EvidenceRun, error) {
	exit, err := int32Of("exit", run.Exit)
	if err != nil {
		return nil, err
	}
	return &v1.EvidenceRun{
		Id:          run.ID,
		Epic:        run.Epic,
		Item:        run.Item,
		Revision:    run.Revision,
		Phase:       run.Phase,
		Source:      run.Source,
		State:       run.State,
		Exit:        exit,
		TimedOut:    run.TimedOut,
		NotCaptured: run.NotCaptured,
		LogHash:     run.LogHash,
		LogBytes:    run.LogBytes,
		StderrHash:  run.StderrHash,
		StderrBytes: run.StderrBytes,
		Artifacts:   epicMap(run.Artifacts, evidenceServiceArtifactMessage),
		JudgeStep:   run.JudgeStep,
		Verdict:     run.Verdict,
		JudgeReason: run.JudgeReason,
		Passed:      run.Passed,
		Created:     unixOrZero(run.Created),
		Ended:       unixOrZero(run.Ended),
	}, nil
}

// evidenceServiceArtifactMessage returns the EvidenceArtifact message of artifact: its path, state, hash and size.
func evidenceServiceArtifactMessage(artifact store.EvidenceArtifact) *v1.EvidenceArtifact {
	return &v1.EvidenceArtifact{
		Path:  artifact.Path,
		State: artifact.State,
		Hash:  artifact.Hash,
		Size:  artifact.Size,
	}
}

// evidenceServiceRefuse returns a Connect error with code whose message is message.
func evidenceServiceRefuse(code connect.Code, message string) error {
	return errors.Join(connect.NewError(code, errors.New(message)))
}

// evidenceServiceStoreError returns an internal Connect error for err. The error's text is the message.
func evidenceServiceStoreError(err error) error {
	return errors.Join(connect.NewError(connect.CodeInternal, err))
}
