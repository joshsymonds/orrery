package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/rpcretry"
	"github.com/joshsymonds/orrery/internal/store"
)

// designNotFound is the fixed message for a Get of an id no design holds.
const designNotFound = "design not found"

// designStoreUnavailable is the fixed message for a design read the store cannot answer.
const designStoreUnavailable = "design store unavailable"

// designStageUnavailable is the fixed message for a design answer when serve has no stage command client.
const designStageUnavailable = "design stage service unavailable"

// designsServer answers orrery.v1.Designs. List and Get read the store reader. A nil reader answers them unavailable.
// Answer forwards to stage with no caller headers, so orreryd records the owner as the maker, and answers the answer
// orreryd stored. Continue forwards to stage with no caller headers, and answers the design read from the reader with
// the session orreryd names. A nil stage answers Answer and Continue unavailable. Each forwarded call runs under
// timeout and is retried only while rpcretry.NeverConnected accepts its error, as the contract commands are.
type designsServer struct {
	orreryv1connect.UnimplementedDesignsHandler
	reader  *store.Reader
	stage   enginev1connect.StageClient
	timeout time.Duration
	clock   rpcretry.Clock
}

// Answer forwards the owner's answer to a question of a design to orreryd, as the owner through serve, and answers the
// answer orreryd stored.
func (d designsServer) Answer(
	ctx context.Context,
	req *connect.Request[orreryv1.DesignsAnswerRequest],
) (*connect.Response[orreryv1.DesignsAnswerResponse], error) {
	design, question, answer, accept := req.Msg.GetDesign(), req.Msg.GetQuestion(), req.Msg.GetAnswer(),
		req.Msg.GetAccept()
	var reply *connect.Response[enginev1.StageAnswerResponse]
	err := d.forwardStage(ctx, func(ctx context.Context, stage enginev1connect.StageClient) error {
		var err error
		reply, err = stage.Answer(ctx, connect.NewRequest(&enginev1.StageAnswerRequest{
			Design: design, Question: question, Answer: answer, Accept: accept,
			Maker: &enginev1.StageMaker{Kind: store.StageMakerOwnerServe},
		}))
		return wrapEngine("answer", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.DesignsAnswerResponse{
		Answer: reply.Msg.GetQuestion().GetAnswer(),
	}), nil
}

// Continue forwards the continuation of a design's stage to orreryd, and answers the design read from the reader with
// the session orreryd names as continuing the stage.
func (d designsServer) Continue(
	ctx context.Context,
	req *connect.Request[orreryv1.DesignsContinueRequest],
) (*connect.Response[orreryv1.DesignsContinueResponse], error) {
	id := req.Msg.GetId()
	var reply *connect.Response[enginev1.ContinueDesignResponse]
	err := d.forwardStage(ctx, func(ctx context.Context, stage enginev1connect.StageClient) error {
		var err error
		reply, err = stage.ContinueDesign(ctx, connect.NewRequest(&enginev1.ContinueDesignRequest{Design: id}))
		return wrapEngine("continue", err)
	})
	if err != nil {
		return nil, err
	}
	if d.reader == nil {
		return nil, listError(connect.CodeUnavailable, designStoreUnavailable)
	}
	record, err := d.reader.Design(ctx, id)
	if errors.Is(err, store.ErrNoDesign) {
		return nil, listError(connect.CodeNotFound, designNotFound)
	}
	if err != nil {
		return nil, designReadError(err, "read design")
	}
	return connect.NewResponse(&orreryv1.DesignsContinueResponse{
		Design:  designMessage(record),
		Session: reply.Msg.GetSession(),
	}), nil
}

// forwardStage calls the stage client under the server's timeout and retry rules. A nil stage client answers
// unavailable, and an engine error answers as the control forwards do.
func (d designsServer) forwardStage(
	ctx context.Context,
	call func(context.Context, enginev1connect.StageClient) error,
) error {
	if d.stage == nil {
		return listError(connect.CodeUnavailable, designStageUnavailable)
	}
	err := retryUnder(ctx, d.clock, d.timeout, rpcretry.NeverConnected, func(ctx context.Context) error {
		return call(ctx, d.stage)
	})
	if err != nil {
		return translateError(err)
	}
	return nil
}

// List answers every design the store reader gives, in the order it gives them.
func (d designsServer) List(
	ctx context.Context,
	_ *connect.Request[orreryv1.DesignsListRequest],
) (*connect.Response[orreryv1.DesignsListResponse], error) {
	if d.reader == nil {
		return nil, listError(connect.CodeUnavailable, designStoreUnavailable)
	}
	records, err := d.reader.Designs(ctx)
	if err != nil {
		return nil, designReadError(err, "read designs")
	}
	designs := make([]*orreryv1.Design, 0, len(records))
	for _, record := range records {
		designs = append(designs, designMessage(record))
	}
	return connect.NewResponse(&orreryv1.DesignsListResponse{Designs: designs}), nil
}

// Get answers the design of the id List gave it, with the approaches the design's stage weighs, the choice of one
// unset until one is stored, the design's log, and its delegations with the decisions made under each, each oldest
// first. An id no design holds answers not_found.
func (d designsServer) Get(
	ctx context.Context,
	req *connect.Request[orreryv1.DesignsGetRequest],
) (*connect.Response[orreryv1.DesignsGetResponse], error) {
	if d.reader == nil {
		return nil, listError(connect.CodeUnavailable, designStoreUnavailable)
	}
	record, err := d.reader.Design(ctx, req.Msg.GetId())
	if errors.Is(err, store.ErrNoDesign) {
		return nil, listError(connect.CodeNotFound, designNotFound)
	}
	if err != nil {
		return nil, designReadError(err, "read design")
	}
	approaches, err := d.reader.DesignApproaches(ctx, req.Msg.GetId())
	if err != nil {
		return nil, designReadError(err, "read design approaches")
	}
	var choice *orreryv1.DesignChoice
	stored, err := d.reader.DesignApproachChoice(ctx, req.Msg.GetId())
	switch {
	case err == nil:
		choice = designChoiceMessage(stored)
	case !errors.Is(err, store.ErrNoApproachChoice):
		return nil, designReadError(err, "read design choice")
	}
	lines, err := d.reader.DesignLog(ctx, req.Msg.GetId())
	if err != nil {
		return nil, designReadError(err, "read design log")
	}
	delegations, err := d.reader.DesignDelegations(ctx, req.Msg.GetId())
	if err != nil {
		return nil, designReadError(err, "read design delegations")
	}
	return connect.NewResponse(&orreryv1.DesignsGetResponse{
		Design:      designMessage(record),
		Approaches:  designApproachMessages(approaches),
		Choice:      choice,
		Log:         designLogMessages(lines),
		Delegations: designDelegationMessages(delegations),
	}), nil
}

// designApproachMessages converts each approach a design's stage weighs to its DesignApproach, in the order given.
func designApproachMessages(approaches []store.DesignApproach) []*orreryv1.DesignApproach {
	messages := make([]*orreryv1.DesignApproach, 0, len(approaches))
	for _, approach := range approaches {
		messages = append(messages, &orreryv1.DesignApproach{
			Seq:         approach.Seq,
			Name:        approach.Name,
			Shape:       approach.Shape,
			Cost:        approach.Cost,
			Wins:        approach.Wins,
			Loses:       approach.Loses,
			Reconsider:  approach.Reconsider,
			Research:    approach.Research,
			Recommended: approach.Recommended,
			Reason:      approach.Reason,
			Time:        approach.At,
		})
	}
	return messages
}

// designChoiceMessage converts the choice of an approach a design holds to its DesignChoice, with its maker as a
// RecordMaker.
func designChoiceMessage(choice store.ApproachChoice) *orreryv1.DesignChoice {
	return &orreryv1.DesignChoice{
		Approach: choice.Approach,
		Words:    choice.Words,
		Maker: &orreryv1.RecordMaker{
			Kind:       choice.Maker,
			Quote:      choice.Quote,
			Reason:     choice.MakerReason,
			Delegation: choice.Delegation,
			Session:    choice.Session,
		},
		Time: choice.At,
	}
}

// designLogMessages converts each line of a design's log to its DesignLogLine, in the order given.
func designLogMessages(lines []store.DesignLogLine) []*orreryv1.DesignLogLine {
	messages := make([]*orreryv1.DesignLogLine, 0, len(lines))
	for _, line := range lines {
		messages = append(messages, &orreryv1.DesignLogLine{
			Seq:     line.Seq,
			Time:    line.At,
			Maker:   &orreryv1.RecordMaker{Kind: line.Maker},
			Session: line.Session,
			What:    line.What,
			Ids:     line.IDs,
			Words:   line.Words,
		})
	}
	return messages
}

// designDelegationMessages converts each delegation of a design to its DesignDelegation, with the decisions made under
// it, in the order given.
func designDelegationMessages(delegations []store.DelegationWithDecisions) []*orreryv1.DesignDelegation {
	messages := make([]*orreryv1.DesignDelegation, 0, len(delegations))
	for _, delegation := range delegations {
		decisions := make([]*orreryv1.DesignDecision, 0, len(delegation.Decisions))
		for _, decision := range delegation.Decisions {
			decisions = append(decisions, &orreryv1.DesignDecision{
				Kind:   decision.Kind,
				Ref:    decision.Ref,
				Text:   decision.Text,
				Reason: decision.Reason,
				Time:   decision.At,
			})
		}
		messages = append(messages, &orreryv1.DesignDelegation{
			Seq:       delegation.Seq,
			Kinds:     delegation.Kinds,
			Message:   delegation.Message,
			Session:   delegation.Session,
			Time:      delegation.At,
			Decisions: decisions,
		})
	}
	return messages
}

// designReadError returns the error a design read answers with for err: unavailable when the store is, and internal
// with message otherwise.
func designReadError(err error, message string) error {
	if errors.Is(err, store.ErrUnavailable) {
		return listError(connect.CodeUnavailable, designStoreUnavailable)
	}
	return listError(connect.CodeInternal, message)
}

// designMessage converts a design record to its Design, copying every field, with its contracts in position order.
func designMessage(record store.DesignRecord) *orreryv1.Design {
	contracts := make([]*orreryv1.DesignContract, 0, len(record.Contracts))
	for _, contract := range record.Contracts {
		contracts = append(contracts, designContractMessage(contract))
	}
	return &orreryv1.Design{
		Id:           record.ID,
		Title:        record.Title,
		Conversation: record.Conversation,
		Created:      record.Created.Unix(),
		OfOne:        record.OfOne,
		State:        record.State,
		Contracts:    contracts,
	}
}

// readListDesigns returns every design the store reader gives, in the order it gives them, for a list to carry. A nil
// reader gives none. It fails as the list's other store reads fail: unavailable when the store is, and internal
// otherwise.
func readListDesigns(ctx context.Context, reader *store.Reader) ([]store.DesignRecord, error) {
	if reader == nil {
		return []store.DesignRecord{}, nil
	}
	records, err := reader.Designs(ctx)
	if errors.Is(err, store.ErrUnavailable) {
		return nil, listError(connect.CodeUnavailable, "store unavailable")
	}
	if err != nil {
		return nil, listError(connect.CodeInternal, "read designs")
	}
	return records, nil
}

// designMessages converts each design record to its Design, in the order given.
func designMessages(records []store.DesignRecord) []*orreryv1.Design {
	designs := make([]*orreryv1.Design, 0, len(records))
	for _, record := range records {
		designs = append(designs, designMessage(record))
	}
	return designs
}

// conversationDesigns returns the designs whose conversation is conversation, in the order of designs.
func conversationDesigns(designs []*orreryv1.Design, conversation string) []*orreryv1.Design {
	var own []*orreryv1.Design
	for _, design := range designs {
		if design.GetConversation() == conversation {
			own = append(own, design)
		}
	}
	return own
}

// designContractMessage converts a contract of a design to its DesignContract, copying every field.
func designContractMessage(contract store.DesignContract) *orreryv1.DesignContract {
	return &orreryv1.DesignContract{
		Id:         contract.ID,
		Title:      contract.Title,
		Repository: contract.Repository,
		State:      contract.State,
		Position:   contract.Position,
		After:      contract.After,
		Epic:       contract.Epic,
		EpicPhase:  contract.EpicPhase,
	}
}

// newDesignsHandler returns the path and handler of the Designs service, reading designs from reader and forwarding
// answers to stage, both as designsServer does. A zero timeout is defaultControlTimeout.
func newDesignsHandler(
	reader *store.Reader,
	stage enginev1connect.StageClient,
	timeout time.Duration,
	clock rpcretry.Clock,
	opts ...connect.HandlerOption,
) (string, http.Handler) {
	if timeout == 0 {
		timeout = defaultControlTimeout
	}
	return orreryv1connect.NewDesignsHandler(
		designsServer{reader: reader, stage: stage, timeout: timeout, clock: clock},
		opts...,
	)
}
