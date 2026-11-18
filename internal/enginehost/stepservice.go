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
	Wait(ctx context.Context, ids []string, timeout time.Duration) (*store.StepRow, []store.StepRow, error)
	List(ctx context.Context, epic, task string) ([]store.StepRow, error)
	Hold(ctx context.Context, id string) error
	Held(id string) bool
	Nudge(ctx context.Context, id, text string) error
	Resume(ctx context.Context, id string) error
}

// stepService implements the Steps RPCs over steps.
// stoppingMessage is why a WaitSteps still waiting when the session socket shuts down fails.
const stoppingMessage = "orreryd is stopping"

type stepService struct {
	steps StepCommands
	// stopping ends when Serve begins its shutdown. A WaitSteps still waiting then fails at once as unavailable, since
	// this orreryd's steps, the producers of every end it waits for, are stopping with it; its caller waits again on the
	// next orreryd, as it does for any unavailable orreryd.
	stopping <-chan struct{}
}

// isStopping reports whether Serve has begun its shutdown.
func (s *stepService) isStopping() bool {
	select {
	case <-s.stopping:
		return true
	default:
		return false
	}
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

func (s *stepService) WaitSteps(
	ctx context.Context,
	req *connect.Request[v1.WaitStepsRequest],
) (*connect.Response[v1.WaitStepsResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	timeout := time.Duration(req.Msg.GetTimeoutSeconds()) * time.Second
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-s.stopping:
			cancel()
		case <-waitCtx.Done():
		}
	}()
	ended, rows, err := s.steps.Wait(waitCtx, req.Msg.GetIds(), timeout)
	if err != nil && ctx.Err() == nil && s.isStopping() {
		return nil, errors.Join(connect.NewError(connect.CodeUnavailable, errors.New(stoppingMessage)))
	}
	if err != nil {
		return nil, stepCommandError(err)
	}
	resp := &v1.WaitStepsResponse{Steps: make([]*v1.Step, 0, len(rows))}
	if ended != nil {
		if resp.Ended, err = stepMessage(*ended, s.steps.Held(ended.ID)); err != nil {
			return nil, stepCommandError(err)
		}
	}
	for _, row := range rows {
		var step *v1.Step
		if step, err = stepMessage(row, s.steps.Held(row.ID)); err != nil {
			return nil, stepCommandError(err)
		}
		resp.Steps = append(resp.Steps, step)
	}
	return connect.NewResponse(resp), nil
}

func (s *stepService) ListSteps(
	ctx context.Context,
	req *connect.Request[v1.ListStepsRequest],
) (*connect.Response[v1.ListStepsResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	rows, err := s.steps.List(ctx, req.Msg.GetEpic(), req.Msg.GetTask())
	if err != nil {
		return nil, stepCommandError(err)
	}
	resp := &v1.ListStepsResponse{Steps: make([]*v1.Step, 0, len(rows))}
	for _, row := range rows {
		var step *v1.Step
		if step, err = stepMessage(row, s.steps.Held(row.ID)); err != nil {
			return nil, stepCommandError(err)
		}
		resp.Steps = append(resp.Steps, step)
	}
	return connect.NewResponse(resp), nil
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

// stepMessage returns the Step message of row, with held as its held flag. Ended is zero while the step runs. A check
// or verdict round outside uint32 is an error, since the reply cannot carry it.
func stepMessage(row store.StepRow, held bool) (*v1.Step, error) {
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
		Held:             held,
		Workspace:        row.Workspace,
		Revision:         row.Revision,
		ReadOnly:         row.ReadOnly,
		Worktree:         row.Worktree,
		End:              row.End,
		Status:           row.Status,
		Result:           row.Result,
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
		errors.Is(err, ErrNotGit), errors.Is(err, ErrMainWorktree), errors.Is(err, agents.ErrUnresolved):
		code = connect.CodeInvalidArgument
	}
	return errors.Join(connect.NewError(code, err))
}
