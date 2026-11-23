package stage

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// Ask stores a question on a design, about the contract the request names or about none, with its options and the
// option the planner recommends, and returns the question as stored. The design must be a stored design or a contract
// that reads as a design of one. The text and at least one option are required, and the recommendation must be one of
// the options. A contract that is empty names none; any other must be a contract of the design. The question is
// unanswered until Answer answers it.
func (s *Service) Ask(
	ctx context.Context,
	req *connect.Request[v1.AskRequest],
) (*connect.Response[v1.AskResponse], error) {
	design, contract, text, options, recommendation := req.Msg.GetDesign(), req.Msg.GetContract(),
		req.Msg.GetText(), req.Msg.GetOptions(), req.Msg.GetRecommendation()
	if err := requireField("text", text); err != nil {
		return nil, err
	}
	if len(options) == 0 {
		return nil, refuse(connect.CodeInvalidArgument, "options: at least one option is required")
	}
	for _, option := range options {
		if err := requireField("option", option); err != nil {
			return nil, err
		}
	}
	if !slices.Contains(options, recommendation) {
		return nil, refuse(connect.CodeInvalidArgument, "recommendation: is not one of the options")
	}
	record, err := s.readDesign(ctx, design)
	if err != nil {
		return nil, err
	}
	if contract != "" && !designHolds(record, contract) {
		return nil, refuse(connect.CodeInvalidArgument,
			fmt.Sprintf("contract: %s is not a contract of design %s", contract, design))
	}
	asker, _, err := s.cfg.Store.CallerSession(ctx, callerOf(req.Header()))
	if err != nil {
		return nil, internalError(fmt.Errorf("resolve caller: %w", err))
	}
	stored, err := s.cfg.Store.AskDesignQuestion(ctx, store.DesignQuestion{
		Design: design, Contract: contract, Text: text, Options: options, Recommendation: recommendation,
		Asker: asker, Asked: s.cfg.Now(),
	})
	if err != nil {
		return nil, internalError(fmt.Errorf("store question of design %s: %w", design, err))
	}
	if err = s.logCall(ctx, design, loggedCall{
		Session: asker, What: "asked", IDs: []string{questionID(stored.Seq)}, Words: text,
	}); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.AskResponse{Question: stageQuestionOf(stored)}), nil
}

// Answer stores an answer to a question of a design, with the maker the request names, and returns the question as
// stored. The maker is checked first, as checkMaker checks it. With accept the answer is the question's recommendation,
// and the request's answer must then be empty; otherwise the answer's words are required. A question no design holds
// is not found, and a question already answered is a failed precondition.
func (s *Service) Answer(
	ctx context.Context,
	req *connect.Request[v1.StageAnswerRequest],
) (*connect.Response[v1.StageAnswerResponse], error) {
	design, seq, answer, accept := req.Msg.GetDesign(), req.Msg.GetQuestion(), req.Msg.GetAnswer(), req.Msg.GetAccept()
	maker, answerer, err := s.checkMaker(ctx, req.Header(), req.Msg.GetMaker())
	if err != nil {
		return nil, err
	}
	question, err := s.cfg.Store.DesignQuestion(ctx, design, seq)
	if errors.Is(err, store.ErrNoQuestion) {
		return nil, errors.Join(connect.NewError(connect.CodeNotFound, err))
	}
	if err != nil {
		return nil, internalError(fmt.Errorf("read question %d of design %s: %w", seq, design, err))
	}
	if accept {
		if answer != "" {
			return nil, refuse(connect.CodeInvalidArgument, "answer: must be empty when accept is set")
		}
		answer = question.Recommendation
	} else if strings.TrimSpace(answer) == "" {
		return nil, refuse(connect.CodeInvalidArgument, "answer: is required")
	}
	stored, err := s.cfg.Store.AnswerDesignQuestion(ctx, store.DesignQuestion{
		Design: design, Seq: seq, Answer: answer, Accepted: accept, Maker: maker, Answerer: answerer,
		Answered: s.cfg.Now(),
	})
	switch {
	case errors.Is(err, store.ErrQuestionAnswered):
		return nil, refuse(connect.CodeFailedPrecondition, err.Error())
	case errors.Is(err, store.ErrNoQuestion):
		return nil, errors.Join(connect.NewError(connect.CodeNotFound, err))
	case err != nil:
		return nil, internalError(fmt.Errorf("store answer to question %d of design %s: %w", seq, design, err))
	}
	if err = s.logCall(ctx, design, loggedCall{
		Maker: maker, Session: answerer, What: "answered", IDs: []string{questionID(seq)}, Words: stored.Answer,
	}); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.StageAnswerResponse{Question: stageQuestionOf(stored)}), nil
}

// readDesign returns the record of the design the id names, which is a stored design or a contract that reads as a
// design of one. An id no design holds is not found.
func (s *Service) readDesign(ctx context.Context, design string) (store.DesignRecord, error) {
	record, err := s.cfg.Store.Design(ctx, design)
	if errors.Is(err, store.ErrNoDesign) {
		return store.DesignRecord{}, errors.Join(connect.NewError(connect.CodeNotFound, err))
	}
	if err != nil {
		return store.DesignRecord{}, internalError(err)
	}
	return record, nil
}

// designHolds reports whether record has a contract with the id.
func designHolds(record store.DesignRecord, contract string) bool {
	for _, held := range record.Contracts {
		if held.ID == contract {
			return true
		}
	}
	return false
}

// stageQuestionOf returns the StageQuestion of question as the store holds it. Its answer, accepted flag, maker and
// answered time are set once it is answered, and empty or zero while it is open.
func stageQuestionOf(question store.DesignQuestion) *v1.StageQuestion {
	message := &v1.StageQuestion{
		Id: question.Seq, Contract: question.Contract, Text: question.Text, Options: question.Options,
		Recommendation: question.Recommendation, Asked: question.Asked.Unix(),
	}
	if question.Answered.IsZero() {
		return message
	}
	message.Answer = question.Answer
	message.Accepted = question.Accepted
	message.Maker = &v1.StageMaker{
		Kind: question.Maker.Kind, Quote: question.Maker.Quote, Reason: question.Maker.Reason,
		Delegation: question.Maker.Delegation,
	}
	message.Answered = question.Answered.Unix()
	return message
}
