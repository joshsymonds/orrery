package stage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/enginehost"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/testrunner"
)

// Research starts a scout step that answers the question about the repository at the revision, for the design, and
// stores the step on the design. The design must be a stored design or a contract that reads as a design of one. The
// step is started by the caller the request's headers name. A revision that is empty starts at HEAD, as Steps.Start
// does.
func (s *Service) Research(
	ctx context.Context,
	req *connect.Request[v1.ResearchRequest],
) (*connect.Response[v1.ResearchResponse], error) {
	design, repository, revision, question := req.Msg.GetDesign(), req.Msg.GetRepository(), req.Msg.GetRevision(),
		req.Msg.GetQuestion()
	if err := requireField("repository", repository); err != nil {
		return nil, err
	}
	if err := requireField("question", question); err != nil {
		return nil, err
	}
	if err := s.requireDesign(ctx, design); err != nil {
		return nil, err
	}
	start, err := ScoutStart(s.cfg.Getenv, repository, revision, question)
	if err != nil {
		return nil, errors.Join(connect.NewError(connect.CodeFailedPrecondition, err))
	}
	start.Caller = callerOf(req.Header())
	step, err := s.startStep(ctx, start, store.DesignStep{
		Design: design, Kind: store.StepResearch, Subject: question, Repository: repository, Revision: revision,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.ResearchResponse{Step: step}), nil
}

// Reproduce runs the command through a test runner step at the revision of the repository, for the design, and stores
// the step on the design, whose output the step's report carries once it returns. The design must be a stored design
// or a contract that reads as a design of one. The step is started by the caller the request's headers name.
func (s *Service) Reproduce(
	ctx context.Context,
	req *connect.Request[v1.ReproduceRequest],
) (*connect.Response[v1.ReproduceResponse], error) {
	design, repository, revision, command := req.Msg.GetDesign(), req.Msg.GetRepository(), req.Msg.GetRevision(),
		req.Msg.GetCommand()
	if err := requireField("repository", repository); err != nil {
		return nil, err
	}
	if err := requireField("revision", revision); err != nil {
		return nil, err
	}
	if err := requireField("command", command); err != nil {
		return nil, err
	}
	if err := s.requireDesign(ctx, design); err != nil {
		return nil, err
	}
	start, err := testrunner.Start(
		s.cfg.Getenv, repository, "", testrunner.Request{Command: command, Revision: revision}, "", "",
	)
	if err != nil {
		return nil, errors.Join(connect.NewError(connect.CodeFailedPrecondition, err))
	}
	start.Caller = callerOf(req.Header())
	step, err := s.startStep(ctx, start, store.DesignStep{
		Design: design, Kind: store.StepReproduce, Subject: command, Repository: repository, Revision: revision,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.ReproduceResponse{Step: step}), nil
}

// startStep starts the step start and, when it starts, stores row as the step's design step row, with the id the start
// returns and the time now. It returns the StageStep of the stored row. A start that fails stores no row and is a
// failed precondition.
func (s *Service) startStep(
	ctx context.Context, start enginehost.StepStart, row store.DesignStep,
) (*v1.StageStep, error) {
	id, err := s.cfg.Steps.Start(ctx, start)
	if err != nil {
		return nil, errors.Join(connect.NewError(connect.CodeFailedPrecondition, err))
	}
	row.Step, row.Created = id, s.cfg.Now()
	if err = s.cfg.Store.AddDesignStep(ctx, row); err != nil {
		return nil, internalError(fmt.Errorf("store step %s of design %s: %w", id, row.Design, err))
	}
	step, err := s.readStageStep(ctx, row)
	if err != nil {
		return nil, internalError(err)
	}
	return step, nil
}

// requireDesign returns an error that the Stage RPC answers as not found when no stored design or contract that reads
// as a design of one has the id, and an internal error when the store fails.
func (s *Service) requireDesign(ctx context.Context, design string) error {
	_, err := s.cfg.Store.Design(ctx, design)
	if errors.Is(err, store.ErrNoDesign) {
		return errors.Join(connect.NewError(connect.CodeNotFound, err))
	}
	if err != nil {
		return internalError(err)
	}
	return nil
}

// requireField returns an error that the Stage RPC answers as an invalid argument when value is blank, naming field.
func requireField(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.Join(connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s is required", field)))
	}
	return nil
}

// callerOf returns the caller the request's headers name. A request with neither caller header has the zero caller,
// an in-process start, which Steps.Start accepts.
func callerOf(header http.Header) store.EpicCaller {
	caller, _ := enginehost.CallerOf(header)
	return caller
}
