package enginehost

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"connectrpc.com/connect"

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
	id, err := s.steps.Start(ctx, StepStart{
		Agent:     req.Msg.GetAgent(),
		Prompt:    req.Msg.GetPrompt(),
		Schema:    req.Msg.GetSchema(),
		Workspace: req.Msg.GetWorkspace(),
		Revision:  req.Msg.GetRevision(),
		ReadOnly:  req.Msg.GetReadOnly(),
		Owned:     req.Msg.GetOwnedPaths(),
		Gate:      req.Msg.GetGate(),
		GateLimit: time.Duration(req.Msg.GetGateLimitSeconds()) * time.Second,
		DoneValue: req.Msg.GetDoneValue(),
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
	step, err := stepMessage(row)
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
	ended, rows, err := s.steps.Wait(ctx, req.Msg.GetIds(), timeout)
	if err != nil {
		return nil, stepCommandError(err)
	}
	resp := &v1.WaitStepsResponse{Steps: make([]*v1.Step, 0, len(rows))}
	if ended != nil {
		if resp.Ended, err = stepMessage(*ended); err != nil {
			return nil, stepCommandError(err)
		}
	}
	for _, row := range rows {
		var step *v1.Step
		if step, err = stepMessage(row); err != nil {
			return nil, stepCommandError(err)
		}
		resp.Steps = append(resp.Steps, step)
	}
	return connect.NewResponse(resp), nil
}

// stepMessage returns the Step message of row. Ended is zero while the step runs. A check round outside uint32 is an
// error, since the reply cannot carry it.
func stepMessage(row store.StepRow) (*v1.Step, error) {
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
		})
	}
	step := &v1.Step{
		Id:        row.ID,
		Agent:     row.Agent,
		Workspace: row.Workspace,
		Revision:  row.Revision,
		ReadOnly:  row.ReadOnly,
		Worktree:  row.Worktree,
		End:       row.End,
		Status:    row.Status,
		Result:    row.Result,
		Checks:    checks,
		Reason:    row.Reason,
		Started:   row.Started.Unix(),
	}
	if !row.Ended.IsZero() {
		step.Ended = row.Ended.Unix()
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
	case errors.Is(err, ErrEmptyPrompt), errors.Is(err, ErrBadSchema), errors.Is(err, ErrBadAgent),
		errors.Is(err, ErrBadDir), errors.Is(err, ErrBadOwned), errors.Is(err, ErrBadGate),
		errors.Is(err, ErrNotGit), errors.Is(err, ErrMainWorktree):
		code = connect.CodeInvalidArgument
	}
	return errors.Join(connect.NewError(code, err))
}
