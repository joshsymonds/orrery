package enginehost

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/agents"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepsUnavailableMessage is the message every step RPC is refused with when there is no step store.
const stepsUnavailableMessage = "step store unavailable"

// StepCommands is the set of step operations the Steps service answers with. Steps satisfies it.
type StepCommands interface {
	Start(ctx context.Context, start StepStart) (string, error)
	Stop(ctx context.Context, id string) error
	Status(ctx context.Context, id string) (store.StepRow, error)
	List(ctx context.Context, epic, task string) ([]store.StepRow, error)
	Listings(ctx context.Context, epic, task string) ([]store.StepListing, error)
	RunningListings(ctx context.Context, epic, task string) ([]store.StepListing, map[string]int, error)
	Hold(ctx context.Context, id string) error
	Held(id string) bool
	Nudge(ctx context.Context, id, text string) error
	Resume(ctx context.Context, id string) error
}

// stepService implements the Steps RPCs over steps.
type stepService struct {
	steps StepCommands
}

// ready returns the refusal every step RPC gets when there is no step store.
func (s *stepService) ready() error {
	if s.steps == nil {
		return errors.Join(connect.NewError(connect.CodeUnavailable, errors.New(stepsUnavailableMessage)))
	}
	return nil
}

func (s *stepService) StartStep(
	ctx context.Context,
	req *connect.Request[v1.StartStepRequest],
) (*connect.Response[v1.StartStepResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if len(req.Msg.GetGate()) > 0 || req.Msg.GetGateLimitSeconds() != 0 {
		return nil, stepCommandError(fmt.Errorf(
			"%w: a step names its gate with gate_name and gates_file, not gate or gate_limit_seconds", ErrBadGate))
	}
	// A step's end goes back to its caller, so a call that names no caller is refused before anything is started.
	caller, ok := epicCallerOf(req.Header())
	if !ok {
		return nil, stepCommandError(fmt.Errorf("%w: the call carries no caller headers", ErrNoCaller))
	}
	id, err := s.steps.Start(ctx, StepStart{
		Agent:      req.Msg.GetAgent(),
		Prompt:     req.Msg.GetPrompt(),
		Schema:     req.Msg.GetSchema(),
		Workspace:  req.Msg.GetWorkspace(),
		Revision:   req.Msg.GetRevision(),
		ReadOnly:   req.Msg.GetReadOnly(),
		Owned:      req.Msg.GetOwnedPaths(),
		GateName:   req.Msg.GetGateName(),
		GatesFile:  req.Msg.GetGatesFile(),
		ModelLimit: time.Duration(req.Msg.GetModelLimitSeconds()) * time.Second,
		DoneValue:  req.Msg.GetDoneValue(),
		Epic:       req.Msg.GetEpic(),
		Task:       req.Msg.GetTask(),
		Purpose:    req.Msg.GetPurpose(),
		Caller:     caller,
	})
	if err != nil {
		return nil, stepCommandError(err)
	}
	return connect.NewResponse(&v1.StartStepResponse{Id: id}), nil
}

func (s *stepService) StopStep(
	ctx context.Context,
	req *connect.Request[v1.StopStepRequest],
) (*connect.Response[v1.StopStepResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.steps.Stop(ctx, req.Msg.GetId()); err != nil {
		return nil, stepCommandError(err)
	}
	return connect.NewResponse(&v1.StopStepResponse{}), nil
}

func (s *stepService) StepStatus(
	ctx context.Context,
	req *connect.Request[v1.StepStatusRequest],
) (*connect.Response[v1.StepStatusResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	row, err := s.steps.Status(ctx, req.Msg.GetId())
	if err != nil {
		return nil, stepCommandError(err)
	}
	step, err := stepMessage(row, s.steps.Held(row.ID))
	if err != nil {
		return nil, stepCommandError(err)
	}
	return connect.NewResponse(&v1.StepStatusResponse{Step: step}), nil
}

// ReadStepResult answers one part of a step's result: its outline, one field's value, or one element of an array field.
func (s *stepService) ReadStepResult(
	ctx context.Context,
	req *connect.Request[v1.ReadStepResultRequest],
) (*connect.Response[v1.ReadStepResultResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	row, err := s.steps.Status(ctx, req.Msg.GetId())
	if err != nil {
		return nil, stepCommandError(err)
	}
	msg := req.Msg
	resp, err := readStepResult(row.Result, msg.GetField(), msg.GetItem(), msg.GetOffset(), msg.GetLimit())
	if err != nil {
		return nil, stepCommandError(err)
	}
	return connect.NewResponse(resp), nil
}

// ReadStepCheck answers one check of a step's latest round, or of the round the request names: its detail, and a slice of
// its kept output.
func (s *stepService) ReadStepCheck(
	ctx context.Context,
	req *connect.Request[v1.ReadStepCheckRequest],
) (*connect.Response[v1.ReadStepCheckResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	row, err := s.steps.Status(ctx, req.Msg.GetId())
	if err != nil {
		return nil, stepCommandError(err)
	}
	var logs keptGateLogs
	if kept, ok := s.steps.(keptGateLogs); ok {
		logs = kept
	}
	resp, err := readStepCheck(row, req.Msg, logs)
	if err != nil {
		return nil, stepCommandError(err)
	}
	return connect.NewResponse(resp), nil
}

// ListSteps answers the listings of the labeled steps the request narrows to, each with no result. A request for the
// running steps reads only those, and the count of each epic's ended steps. A caller request answers the steps the
// call's caller session started instead, labeled or not.
func (s *stepService) ListSteps(
	ctx context.Context,
	req *connect.Request[v1.ListStepsRequest],
) (*connect.Response[v1.ListStepsResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if req.Msg.GetCaller() {
		return s.listCallerSteps(ctx, req)
	}
	var listings []store.StepListing
	var ended map[string]int
	var err error
	if req.Msg.GetRunning() {
		listings, ended, err = s.steps.RunningListings(ctx, req.Msg.GetEpic(), req.Msg.GetTask())
	} else {
		listings, err = s.steps.Listings(ctx, req.Msg.GetEpic(), req.Msg.GetTask())
	}
	if err != nil {
		return nil, stepCommandError(err)
	}
	steps, err := s.listedSteps(listings)
	if err != nil {
		return nil, stepCommandError(err)
	}
	resp := &v1.ListStepsResponse{Steps: steps}
	if req.Msg.GetRunning() {
		if resp.Ended, err = endedCounts(ended); err != nil {
			return nil, stepCommandError(err)
		}
	}
	return connect.NewResponse(resp), nil
}

// listCallerSteps answers a caller listing: the steps the call's caller session started, narrowed to the request's
// epic, each with no result and no ended counts. It cannot be combined with running or task, and the call must carry
// caller headers that name the session.
func (s *stepService) listCallerSteps(
	ctx context.Context,
	req *connect.Request[v1.ListStepsRequest],
) (*connect.Response[v1.ListStepsResponse], error) {
	if req.Msg.GetRunning() || req.Msg.GetTask() != "" {
		return nil, errors.Join(connect.NewError(
			connect.CodeInvalidArgument, errors.New("caller cannot be combined with running or task")))
	}
	caller, ok := epicCallerOf(req.Header())
	if !ok {
		return nil, stepCommandError(fmt.Errorf("%w: the call carries no caller headers", ErrNoCaller))
	}
	callers, ok := s.steps.(callerListings)
	if !ok {
		return nil, errors.Join(connect.NewError(
			connect.CodeUnimplemented, errors.New("step store cannot list a caller's steps")))
	}
	listings, err := callers.CallerListings(ctx, caller, req.Msg.GetEpic())
	if err != nil {
		return nil, stepCommandError(err)
	}
	steps, err := s.listedSteps(listings)
	if err != nil {
		return nil, stepCommandError(err)
	}
	return connect.NewResponse(&v1.ListStepsResponse{Steps: steps}), nil
}

// listedSteps returns the Step message of each listing, with the held flag its id has and no result.
func (s *stepService) listedSteps(listings []store.StepListing) ([]*v1.Step, error) {
	steps := make([]*v1.Step, 0, len(listings))
	for _, listing := range listings {
		step, err := listingMessage(listing, s.steps.Held(listing.ID))
		if err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, nil
}

// endedCounts returns ended, the count of each epic's ended steps, as the reply carries it. A count outside int32 is an
// error, since the reply cannot carry it.
func endedCounts(ended map[string]int) (map[string]int32, error) {
	out := make(map[string]int32, len(ended))
	for epic, count := range ended {
		if count < 0 || count > math.MaxInt32 {
			return nil, fmt.Errorf("epic %s ended step count %d is outside int32", epic, count)
		}
		out[epic] = int32(count)
	}
	return out, nil
}

func (s *stepService) HoldStep(
	ctx context.Context,
	req *connect.Request[v1.HoldStepRequest],
) (*connect.Response[v1.HoldStepResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.steps.Hold(ctx, req.Msg.GetId()); err != nil {
		return nil, stepCommandError(err)
	}
	return connect.NewResponse(&v1.HoldStepResponse{}), nil
}

func (s *stepService) NudgeStep(
	ctx context.Context,
	req *connect.Request[v1.NudgeStepRequest],
) (*connect.Response[v1.NudgeStepResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.steps.Nudge(ctx, req.Msg.GetId(), req.Msg.GetText()); err != nil {
		return nil, stepCommandError(err)
	}
	return connect.NewResponse(&v1.NudgeStepResponse{}), nil
}

func (s *stepService) ResumeStep(
	ctx context.Context,
	req *connect.Request[v1.ResumeStepRequest],
) (*connect.Response[v1.ResumeStepResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.steps.Resume(ctx, req.Msg.GetId()); err != nil {
		return nil, stepCommandError(err)
	}
	return connect.NewResponse(&v1.ResumeStepResponse{}), nil
}

// stepMessage returns the Step message of row, with held as its held flag: the message of its listing, with its result.
func stepMessage(row store.StepRow, held bool) (*v1.Step, error) {
	step, err := listingMessage(row.Listing(), held)
	if err != nil {
		return nil, err
	}
	step.Result = row.Result
	return step, nil
}

// listingMessage returns the Step message of row, a step's listing, with held as its held flag and no result. Ended is
// zero while the step runs. A check or verdict round outside uint32 is an error, since the reply cannot carry it.
func listingMessage(row store.StepListing, held bool) (*v1.Step, error) {
	checks := make([]*v1.StepCheck, 0, len(row.Checks))
	for _, check := range row.Checks {
		if int64(check.Round) < 0 || int64(check.Round) > math.MaxUint32 {
			return nil, fmt.Errorf("step %s check round %d is outside uint32", row.ID, check.Round)
		}
		checks = append(checks, &v1.StepCheck{
			Round:  uint32(check.Round),
			Name:   check.Name,
			Ok:     check.OK,
			Detail: check.Detail,
			Stdout: check.Stdout,
			Stderr: check.Stderr,
			Log:    check.Log,
		})
	}
	verdicts := make([]*v1.StepVerdict, 0, len(row.Verdicts))
	for _, verdict := range row.Verdicts {
		if int64(verdict.Round) < 0 || int64(verdict.Round) > math.MaxUint32 {
			return nil, fmt.Errorf("step %s verdict round %d is outside uint32", row.ID, verdict.Round)
		}
		verdicts = append(verdicts, &v1.StepVerdict{
			Round:   uint32(verdict.Round),
			Kind:    verdict.Kind,
			Verdict: verdict.Verdict,
			Detail:  verdict.Detail,
		})
	}
	step := &v1.Step{
		Id:               row.ID,
		Agent:            row.Agent,
		Epic:             row.Epic,
		Task:             row.Task,
		Purpose:          row.Purpose,
		Held:             held,
		Workspace:        row.Workspace,
		Revision:         row.Revision,
		ReadOnly:         row.ReadOnly,
		Worktree:         row.Worktree,
		End:              row.End,
		Status:           row.Status,
		Checks:           checks,
		Doing:            row.Doing,
		Reason:           row.Reason,
		Started:          row.Started.Unix(),
		Limited:          row.LoopLimited,
		LimitKind:        row.LoopLimitKind,
		Stage:            row.LoopStage,
		GateName:         row.GateName,
		GateLimitSeconds: int64(row.GateLimit / time.Second),
		Verdicts:         verdicts,
		Caller:           row.Caller,
		Summary:          row.Summary,
		RedEvidence:      row.RedEvidence,
		GreenEvidence:    row.GreenEvidence,
	}
	if !row.Ended.IsZero() {
		step.Ended = row.Ended.Unix()
	}
	if !row.LoopLimitResets.IsZero() {
		step.LimitResets = row.LoopLimitResets.Unix()
	}
	if !row.LoopStageFrom.IsZero() {
		step.StageStarted = row.LoopStageFrom.Unix()
	}
	return step, nil
}

// stepCommandError maps a StepCommands error to its Connect code. The error's text is the message.
func stepCommandError(err error) error {
	code := connect.CodeInternal
	switch {
	case errors.Is(err, ErrCapReached):
		code = connect.CodeResourceExhausted
	case errors.Is(err, store.ErrNoStep):
		code = connect.CodeNotFound
	case errors.Is(err, ErrStepNoTurn), errors.Is(err, ErrStepHeld), errors.Is(err, ErrStepGating),
		errors.Is(err, ErrStepNotRunning), errors.Is(err, ErrStepNotLimited), errors.Is(err, ErrNoCaller):
		code = connect.CodeFailedPrecondition
	case errors.Is(err, ErrEmptyPrompt), errors.Is(err, ErrBadSchema), errors.Is(err, ErrBadAgent),
		errors.Is(err, ErrBadDir), errors.Is(err, ErrBadOwned), errors.Is(err, ErrBadGate),
		errors.Is(err, ErrBadResultRead), errors.Is(err, ErrBadCheckRead), errors.Is(err, ErrNotGit),
		errors.Is(err, ErrMainWorktree),
		errors.Is(err, agents.ErrUnresolved):
		code = connect.CodeInvalidArgument
	}
	return errors.Join(connect.NewError(code, err))
}
