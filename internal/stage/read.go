package stage

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/testrunner"
)

// runningStatus is the status of a stage step that has not ended.
const runningStatus = "running"

// GetStage returns the stage of the design: the steps its design_steps rows record, oldest first, each with its step
// row's status and report. A design no step names has no steps.
func (s *Service) GetStage(
	ctx context.Context,
	req *connect.Request[v1.GetStageRequest],
) (*connect.Response[v1.GetStageResponse], error) {
	design := req.Msg.GetDesign()
	rows, err := s.cfg.Store.DesignSteps(ctx, design)
	if err != nil {
		return nil, internalError(err)
	}
	steps := make([]*v1.StageStep, 0, len(rows))
	for _, row := range rows {
		step, readErr := s.readStageStep(ctx, row)
		if readErr != nil {
			return nil, internalError(readErr)
		}
		steps = append(steps, step)
	}
	return connect.NewResponse(&v1.GetStageResponse{
		Stage: &v1.DesignStage{Design: design, Steps: steps},
	}), nil
}

// readStageStep returns the StageStep of row, the design step row, read with its step row from the store.
func (s *Service) readStageStep(ctx context.Context, row store.DesignStep) (*v1.StageStep, error) {
	step, err := s.cfg.Store.Step(ctx, row.Step)
	if err != nil {
		return nil, fmt.Errorf("read step %s of design %s: %w", row.Step, row.Design, err)
	}
	return stageStepOf(row, step), nil
}

// stageStepOf returns the StageStep of the design step row and its step row. The step is running while it has not
// ended, and its status is then its own status. Its report is empty until it returns.
func stageStepOf(row store.DesignStep, step store.StepRow) *v1.StageStep {
	status := runningStatus
	if step.End != "" {
		status = step.Status
	}
	return &v1.StageStep{
		Step:       row.Step,
		Kind:       row.Kind,
		Subject:    row.Subject,
		Repository: row.Repository,
		Revision:   row.Revision,
		Delta:      row.Delta,
		Created:    row.Created.Unix(),
		Status:     status,
		Report:     reportOf(row.Kind, step.Result),
	}
}

// reportOf returns the report of a step of kind whose kept return is result: the return verbatim for research, and the
// output testrunner renders for reproduce. A step that holds no return has an empty report.
func reportOf(kind, result string) string {
	if result == "" {
		return ""
	}
	if kind == store.StepReproduce {
		return testrunner.Output(result)
	}
	return result
}

// internalError returns err as the internal error a Stage RPC answers with when the store fails.
func internalError(err error) error {
	return errors.Join(connect.NewError(connect.CodeInternal, err))
}
