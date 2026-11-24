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
// Get also forwards the read of the design's stage to stage, and answers the stage orreryd reports, or leaves the stage
// unset when stage is nil. Answer forwards to stage with no caller headers, so orreryd records the owner as the maker,
// and answers the answer orreryd stored. Continue forwards to stage with no caller headers, and answers the design
// read from the reader with the session orreryd names. A nil stage answers Answer and Continue unavailable. Each
// forwarded call runs under timeout and is retried only while rpcretry.NeverConnected accepts its error, as the
// contract commands are.
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
// unset until one is stored, the design's log, its delegations with the decisions made under each, each oldest first,
// and its stage's steps, questions, ledger and counts, which is unset when no stage client is set. An id no design
// holds answers not_found.
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
	var stage *orreryv1.DesignStage
	if d.stage != nil {
		stage, err = d.readDesignStage(ctx, req.Msg.GetId())
		if err != nil {
			return nil, err
		}
	}
	return connect.NewResponse(&orreryv1.DesignsGetResponse{
		Design:      designMessage(record),
		Approaches:  designApproachMessages(approaches),
		Choice:      choice,
		Log:         designLogMessages(lines),
		Delegations: designDelegationMessages(delegations),
		Stage:       stage,
	}), nil
}

// readDesignStage forwards the read of the stage of the design id to stage, under the rules forwardStage gives, and
// returns the stage as the design message carries it.
func (d designsServer) readDesignStage(ctx context.Context, id string) (*orreryv1.DesignStage, error) {
	var reply *connect.Response[enginev1.GetStageResponse]
	err := d.forwardStage(ctx, func(ctx context.Context, stage enginev1connect.StageClient) error {
		var err error
		reply, err = stage.GetStage(ctx, connect.NewRequest(&enginev1.GetStageRequest{Design: id}))
		return wrapEngine("get stage", err)
	})
	if err != nil {
		return nil, err
	}
	return designStageMessage(reply.Msg.GetStage()), nil
}

// designStageMessage converts the stage of a design to its DesignStage, with its steps, questions and ledger in the
// order given and its counts copied.
func designStageMessage(stage *enginev1.DesignStage) *orreryv1.DesignStage {
	return &orreryv1.DesignStage{
		Steps:            designStepMessages(stage.GetSteps()),
		Questions:        designQuestionMessages(stage.GetQuestions()),
		Ledger:           designLedgerItemMessages(stage.GetLedger()),
		Frozen:           stage.GetFrozen(),
		ClosureSkipped:   stage.GetClosureSkipped(),
		DiscoveryCounted: stage.GetDiscoveryCounted(),
		ClosureCounted:   stage.GetClosureCounted(),
	}
}

// designStepMessages converts each step a design's stage started to its DesignStep, in the order given.
func designStepMessages(steps []*enginev1.StageStep) []*orreryv1.DesignStep {
	messages := make([]*orreryv1.DesignStep, 0, len(steps))
	for _, step := range steps {
		messages = append(messages, &orreryv1.DesignStep{
			Step:    step.GetStep(),
			Kind:    step.GetKind(),
			Subject: step.GetSubject(),
			Created: step.GetCreated(),
			Status:  step.GetStatus(),
			Report:  step.GetReport(),
			Verdict: step.GetVerdict(),
			Counted: step.GetCounted(),
		})
	}
	return messages
}

// designQuestionMessages converts each question a design's stage asked to its DesignQuestion, in the order given.
func designQuestionMessages(questions []*enginev1.StageQuestion) []*orreryv1.DesignQuestion {
	messages := make([]*orreryv1.DesignQuestion, 0, len(questions))
	for _, question := range questions {
		messages = append(messages, &orreryv1.DesignQuestion{
			Id:             question.GetId(),
			Contract:       question.GetContract(),
			Text:           question.GetText(),
			Options:        question.GetOptions(),
			Recommendation: question.GetRecommendation(),
			Asked:          question.GetAsked(),
			Answer:         question.GetAnswer(),
			Accepted:       question.GetAccepted(),
			Maker:          designRecordMaker(question.GetMaker()),
			Answered:       question.GetAnswered(),
		})
	}
	return messages
}

// designLedgerItemMessages converts each item of a design's ledger to its DesignLedgerItem, in the order given.
func designLedgerItemMessages(items []*enginev1.StageLedgerItem) []*orreryv1.DesignLedgerItem {
	messages := make([]*orreryv1.DesignLedgerItem, 0, len(items))
	for _, item := range items {
		messages = append(messages, &orreryv1.DesignLedgerItem{
			Id:          item.GetId(),
			Contract:    item.GetContract(),
			Origin:      item.GetOrigin(),
			Impact:      item.GetImpact(),
			Evidence:    item.GetEvidence(),
			Change:      item.GetChange(),
			Disposition: item.GetDisposition(),
			Reason:      item.GetReason(),
			Boundary:    item.GetBoundary(),
			Maker:       designRecordMaker(item.GetMaker()),
			ClosureWord: item.GetClosureWord(),
			ClosureNote: item.GetClosureNote(),
		})
	}
	return messages
}

// designRecordMaker converts the maker of a stage record to the RecordMaker a design message carries, with the same
// kind, quote, reason and delegation. A maker that holds none of them, or no maker, is nil.
func designRecordMaker(maker *enginev1.StageMaker) *orreryv1.RecordMaker {
	if maker.GetKind() == "" && maker.GetQuote() == "" && maker.GetReason() == "" && maker.GetDelegation() == "" {
		return nil
	}
	return &orreryv1.RecordMaker{
		Kind:       maker.GetKind(),
		Quote:      maker.GetQuote(),
		Reason:     maker.GetReason(),
		Delegation: maker.GetDelegation(),
	}
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
