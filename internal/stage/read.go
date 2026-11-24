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
// row's status and report, and the questions its design_questions rows record, oldest first, each with its answer once
// it has one. A steelman step carries its pass's verdict and whether the pass counts. It also returns the design's
// ledger, read as readLedger reads it, whether the ledger is frozen, and whether the discovery and closure passes
// counted and whether the closure was skipped. It also returns the design's log, oldest first, and its approaches,
// oldest first, with its choice of an approach, which is unset until one is stored. It also returns the design's
// delegations, oldest first, each with the decisions the conversation made under it. A design no step names has no
// steps, one no question names has no questions, one no line names has no log, and one no delegation names has none.
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
	questions, err := s.cfg.Store.DesignQuestions(ctx, design)
	if err != nil {
		return nil, internalError(err)
	}
	var asked []*v1.StageQuestion
	for _, question := range questions {
		asked = append(asked, stageQuestionOf(question))
	}
	ledger, frozen, err := s.readLedger(ctx, design, rows)
	if err != nil {
		return nil, internalError(err)
	}
	var items []*v1.StageLedgerItem
	for _, item := range ledger {
		items = append(items, stageLedgerItemOf(item))
	}
	discoveryCounted, closureCounted := countedPasses(steps)
	skipped, err := s.closureSkipped(ctx, design)
	if err != nil {
		return nil, internalError(err)
	}
	lines, err := s.cfg.Store.DesignLog(ctx, design)
	if err != nil {
		return nil, internalError(err)
	}
	var logged []*v1.StageLogLine
	for _, line := range lines {
		logged = append(logged, stageLogLineOf(line))
	}
	approaches, choice, err := s.readApproaches(ctx, design)
	if err != nil {
		return nil, err
	}
	delegations, err := s.cfg.Store.DesignDelegations(ctx, design)
	if err != nil {
		return nil, internalError(err)
	}
	var delegated []*v1.StageDelegation
	for _, delegation := range delegations {
		delegated = append(delegated, stageDelegationOf(delegation))
	}
	return connect.NewResponse(&v1.GetStageResponse{
		Stage: &v1.DesignStage{
			Design: design, Steps: steps, Questions: asked, Ledger: items, Frozen: frozen,
			ClosureSkipped: skipped, DiscoveryCounted: discoveryCounted, ClosureCounted: closureCounted,
			Log: logged, Approaches: approaches, Choice: choice, Delegations: delegated,
		},
	}), nil
}

// countedPasses reports whether a counted discovery step and whether a counted closure step are among steps.
func countedPasses(steps []*v1.StageStep) (bool, bool) {
	var discovery, closure bool
	for _, step := range steps {
		if !step.GetCounted() {
			continue
		}
		switch step.GetKind() {
		case store.StepDiscovery:
			discovery = true
		case store.StepClosure:
			closure = true
		}
	}
	return discovery, closure
}

// readStageStep returns the StageStep of row, the design step row, read with its step row from the store. A steelman
// step's verdict and whether it counts come from its kept return.
func (s *Service) readStageStep(ctx context.Context, row store.DesignStep) (*v1.StageStep, error) {
	step, err := s.cfg.Store.Step(ctx, row.Step)
	if err != nil {
		return nil, fmt.Errorf("read step %s of design %s: %w", row.Step, row.Design, err)
	}
	message := stageStepOf(row, step)
	if row.Kind == store.StepDiscovery || row.Kind == store.StepClosure {
		verdict, counted, passErr := passOf(step)
		if passErr != nil {
			return nil, fmt.Errorf("decode return of %s step %s of design %s: %w",
				row.Kind, row.Step, row.Design, passErr)
		}
		message.Verdict, message.Counted = verdict, counted
	}
	return message, nil
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
