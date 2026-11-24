package stage

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// Approach stores an approach a design's stage weighs, with the caller's session, and returns it as stored. The design
// must be a stored design or a contract that reads as a design of one, and the name is required. The approach is logged
// as approach recorded, with its seq and its name as the words.
func (s *Service) Approach(
	ctx context.Context,
	req *connect.Request[v1.ApproachRequest],
) (*connect.Response[v1.ApproachResponse], error) {
	design, name := req.Msg.GetDesign(), req.Msg.GetName()
	if err := requireField("design", design); err != nil {
		return nil, err
	}
	if err := requireField("name", name); err != nil {
		return nil, err
	}
	if _, err := s.readDesign(ctx, design); err != nil {
		return nil, err
	}
	session, err := s.callerSession(ctx, callerOf(req.Header()))
	if err != nil {
		return nil, err
	}
	if err = s.requireWorkingConversation(ctx, session, design); err != nil {
		return nil, err
	}
	stored, err := s.cfg.Store.RecordApproach(ctx, store.DesignApproach{
		Design: design, Name: name, Shape: req.Msg.GetShape(), Cost: req.Msg.GetCost(), Wins: req.Msg.GetWins(),
		Loses: req.Msg.GetLoses(), Reconsider: req.Msg.GetReconsider(), Research: req.Msg.GetResearch(),
		Recommended: req.Msg.GetRecommended(), Reason: req.Msg.GetReason(), Session: session,
		At: s.cfg.Now().Unix(),
	})
	if err != nil {
		return nil, internalError(fmt.Errorf("store approach of design %s: %w", design, err))
	}
	err = s.logCall(ctx, design, loggedCall{
		Session: session, What: "approach recorded", IDs: []string{approachID(stored.Seq)}, Words: name,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.ApproachResponse{Approach: stageApproachOf(stored)}), nil
}

// Choose stores the approach the owner chose for a design, with the maker the request names, and returns the choice as
// stored. The design must exist, and the approach must be one of the design's approaches: an approach the design lacks
// is not found. Then the maker is checked, as checkMaker checks it. The choice is logged as approach chosen, with the
// approach's seq as its id.
func (s *Service) Choose(
	ctx context.Context,
	req *connect.Request[v1.ChooseRequest],
) (*connect.Response[v1.ChooseResponse], error) {
	design, seq, words := req.Msg.GetDesign(), req.Msg.GetApproach(), req.Msg.GetWords()
	if _, err := s.readDesign(ctx, design); err != nil {
		return nil, err
	}
	if err := s.checkApproachOf(ctx, design, seq); err != nil {
		return nil, err
	}
	maker, session, err := s.checkMaker(ctx, req.Header(), req.Msg.GetMaker())
	if err != nil {
		return nil, err
	}
	if err = s.requireWorkingConversation(ctx, session, design); err != nil {
		return nil, err
	}
	stored, err := s.cfg.Store.ChooseApproach(ctx, store.ApproachChoice{
		Design: design, Approach: seq, Words: words, Maker: maker.Kind, Quote: maker.Quote,
		MakerReason: maker.Reason, Delegation: maker.Delegation, Session: session, At: s.cfg.Now().Unix(),
	})
	if err != nil {
		return nil, internalError(fmt.Errorf("store choice of design %s: %w", design, err))
	}
	err = s.logCall(ctx, design, loggedCall{
		Maker: maker, Session: session, What: "approach chosen", IDs: []string{approachID(seq)}, Words: words,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.ChooseResponse{Choice: stageChoiceOf(stored)}), nil
}

// checkApproachOf returns the refusal of choosing approach seq of design when the design has no approach numbered seq,
// or nil when it has one. A failed read is internal.
func (s *Service) checkApproachOf(ctx context.Context, design string, seq int64) error {
	approaches, err := s.cfg.Store.DesignApproaches(ctx, design)
	if err != nil {
		return internalError(fmt.Errorf("list approaches of design %s: %w", design, err))
	}
	for _, approach := range approaches {
		if approach.Seq == seq {
			return nil
		}
	}
	return refuse(connect.CodeNotFound, fmt.Sprintf("approach: %d is not an approach of design %s", seq, design))
}

// readApproaches returns the approaches of the design as the store holds them, oldest first, and its choice, which is
// nil until a choice is stored. A failed read is internal.
func (s *Service) readApproaches(ctx context.Context, design string) ([]*v1.StageApproach, *v1.StageChoice, error) {
	approaches, err := s.cfg.Store.DesignApproaches(ctx, design)
	if err != nil {
		return nil, nil, internalError(fmt.Errorf("list approaches of design %s: %w", design, err))
	}
	messages := make([]*v1.StageApproach, 0, len(approaches))
	for _, approach := range approaches {
		messages = append(messages, stageApproachOf(approach))
	}
	choice, err := s.cfg.Store.DesignApproachChoice(ctx, design)
	if errors.Is(err, store.ErrNoApproachChoice) {
		return messages, nil, nil
	}
	if err != nil {
		return nil, nil, internalError(fmt.Errorf("read choice of design %s: %w", design, err))
	}
	return messages, stageChoiceOf(choice), nil
}

// approachID returns the id a log line gives an approach numbered seq within its design.
func approachID(seq int64) string {
	return strconv.FormatInt(seq, 10)
}

// stageApproachOf returns the StageApproach of approach as the store holds it.
func stageApproachOf(approach store.DesignApproach) *v1.StageApproach {
	return &v1.StageApproach{
		Seq: approach.Seq, Name: approach.Name, Shape: approach.Shape, Cost: approach.Cost, Wins: approach.Wins,
		Loses: approach.Loses, Reconsider: approach.Reconsider, Research: approach.Research,
		Recommended: approach.Recommended, Reason: approach.Reason, Session: approach.Session, At: approach.At,
	}
}

// stageChoiceOf returns the StageChoice of choice as the store holds it.
func stageChoiceOf(choice store.ApproachChoice) *v1.StageChoice {
	return &v1.StageChoice{
		Approach: choice.Approach, Words: choice.Words,
		Maker: &v1.StageMaker{
			Kind: choice.Maker, Quote: choice.Quote, Reason: choice.MakerReason, Delegation: choice.Delegation,
		},
		Session: choice.Session, At: choice.At,
	}
}
