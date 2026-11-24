package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/rpcretry"
	"github.com/joshsymonds/orrery/internal/store"
)

// contractNotFound is the fixed message for a Get of an id no contract holds.
const contractNotFound = "contract not found"

// contractStoreUnavailable is the fixed message for a contract read the store cannot answer.
const contractStoreUnavailable = "contract store unavailable"

// contractCommandsUnavailable is the fixed message for a contract command when serve has no contract command client.
const contractCommandsUnavailable = "contract command service unavailable"

// contractStageUnavailable is the fixed message for a contract's keep, strike or shown when serve has no stage client.
const contractStageUnavailable = "contract stage service unavailable"

// The by names a state change of a contract carries: the owner, or a session.
const (
	contractByOwner   = "owner"
	contractBySession = "session"
)

// contractsServer answers orrery.v1.Contracts. List and Get read the store reader. A nil reader answers them
// unavailable. Get reads the work of an accepted contract's requirements from store and evidence, as Epics.Get reads
// its epic: a nil store holds no epic, and a nil evidence attaches no runs. Accept, SetAside and Reopen forward to
// commands with no caller headers, so orreryd records the owner as the one who made each change. A nil commands answers
// them unavailable. Keep and Strike forward to stage with no caller headers, as the owner's writes, and answer the
// contract read from the reader; Shown forwards to stage and answers the rows orreryd marked. A nil stage answers them
// unavailable. Each forwarded call runs under timeout and is retried only while rpcretry.NeverConnected accepts its
// error, as the epic commands are.
type contractsServer struct {
	reader   *store.Reader
	store    EpicStoreClient
	evidence EvidenceClient
	commands enginev1connect.ContractsClient
	stage    enginev1connect.StageClient
	timeout  time.Duration
	clock    rpcretry.Clock
}

// List answers the listing of every contract, created ascending and then by id, with no versions or states.
func (c contractsServer) List(
	ctx context.Context,
	_ *connect.Request[orreryv1.ContractsListRequest],
) (*connect.Response[orreryv1.ContractsListResponse], error) {
	if c.reader == nil {
		return nil, listError(connect.CodeUnavailable, contractStoreUnavailable)
	}
	listings, err := c.reader.Contracts(ctx)
	if err != nil {
		return nil, contractReadError(err, "read contracts")
	}
	contracts := make([]*orreryv1.Contract, 0, len(listings))
	for _, listing := range listings {
		contracts = append(contracts, contractListingMessage(listing))
	}
	return connect.NewResponse(&orreryv1.ContractsListResponse{Contracts: contracts}), nil
}

// Get answers the contract of the id List gave it, with every version oldest first, the state history and the work of
// each requirement (contractWork). An id no contract holds answers not_found.
func (c contractsServer) Get(
	ctx context.Context,
	req *connect.Request[orreryv1.ContractsGetRequest],
) (*connect.Response[orreryv1.ContractsGetResponse], error) {
	if c.reader == nil {
		return nil, listError(connect.CodeUnavailable, contractStoreUnavailable)
	}
	record, err := c.reader.Contract(ctx, req.Msg.GetId())
	if errors.Is(err, store.ErrNoContract) {
		return nil, listError(connect.CodeNotFound, contractNotFound)
	}
	if err != nil {
		return nil, contractReadError(err, "read contract")
	}
	versions := make([]*orreryv1.ContractVersion, 0, len(record.Versions))
	for _, version := range record.Versions {
		message, decodeErr := contractVersionMessage(version)
		if decodeErr != nil {
			return nil, contractReadError(decodeErr, "read contract")
		}
		versions = append(versions, message)
	}
	states := make([]*orreryv1.ContractStateChange, 0, len(record.States))
	for _, state := range record.States {
		states = append(states, contractStateMessage(state))
	}
	work, err := c.contractWork(ctx, record.ContractListing, versions)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.ContractsGetResponse{
		Contract: contractListingMessage(record.ContractListing),
		Versions: versions,
		States:   states,
		Work:     work,
	}), nil
}

// Accept forwards the owner's acceptance of the named contract to orreryd, and answers the contract as orreryd stored it.
func (c contractsServer) Accept(
	ctx context.Context,
	req *connect.Request[orreryv1.ContractsAcceptRequest],
) (*connect.Response[orreryv1.ContractsAcceptResponse], error) {
	id, words := req.Msg.GetId(), req.Msg.GetWords()
	var reply *connect.Response[enginev1.AcceptContractResponse]
	err := c.forwardCommand(ctx, func(ctx context.Context, commands enginev1connect.ContractsClient) error {
		var err error
		reply, err = commands.AcceptContract(ctx, connect.NewRequest(&enginev1.AcceptContractRequest{
			Id: id, Words: words,
		}))
		return wrapEngine("accept", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.ContractsAcceptResponse{
		Contract: contractStoredMessage(reply.Msg.GetContract()),
	}), nil
}

// SetAside forwards the owner's set aside of the named contract to orreryd, and answers the contract as orreryd stored it.
func (c contractsServer) SetAside(
	ctx context.Context,
	req *connect.Request[orreryv1.ContractsSetAsideRequest],
) (*connect.Response[orreryv1.ContractsSetAsideResponse], error) {
	id, words := req.Msg.GetId(), req.Msg.GetWords()
	var reply *connect.Response[enginev1.SetAsideContractResponse]
	err := c.forwardCommand(ctx, func(ctx context.Context, commands enginev1connect.ContractsClient) error {
		var err error
		reply, err = commands.SetAsideContract(ctx, connect.NewRequest(&enginev1.SetAsideContractRequest{
			Id: id, Words: words,
		}))
		return wrapEngine("set aside", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.ContractsSetAsideResponse{
		Contract: contractStoredMessage(reply.Msg.GetContract()),
	}), nil
}

// Reopen forwards the owner's reopening of the named contract to orreryd, and answers the contract as orreryd stored it.
func (c contractsServer) Reopen(
	ctx context.Context,
	req *connect.Request[orreryv1.ContractsReopenRequest],
) (*connect.Response[orreryv1.ContractsReopenResponse], error) {
	id, words := req.Msg.GetId(), req.Msg.GetWords()
	var reply *connect.Response[enginev1.ReopenContractResponse]
	err := c.forwardCommand(ctx, func(ctx context.Context, commands enginev1connect.ContractsClient) error {
		var err error
		reply, err = commands.ReopenContract(ctx, connect.NewRequest(&enginev1.ReopenContractRequest{
			Id: id, Words: words,
		}))
		return wrapEngine("reopen", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.ContractsReopenResponse{
		Contract: contractStoredMessage(reply.Msg.GetContract()),
	}), nil
}

// Keep forwards the owner's addition to the named contract to orreryd as the owner's, and answers the contract as the
// reader holds it after the write.
func (c contractsServer) Keep(
	ctx context.Context,
	req *connect.Request[orreryv1.ContractsKeepRequest],
) (*connect.Response[orreryv1.ContractsKeepResponse], error) {
	id, addition, words := req.Msg.GetId(), req.Msg.GetAddition(), req.Msg.GetWords()
	err := c.forwardStage(ctx, func(ctx context.Context, stage enginev1connect.StageClient) error {
		_, err := stage.Keep(ctx, connect.NewRequest(&enginev1.KeepRequest{
			Contract: id, Addition: addition, Words: words,
			Maker: &enginev1.StageMaker{Kind: store.StageMakerOwnerServe},
		}))
		return wrapEngine("keep", err)
	})
	if err != nil {
		return nil, err
	}
	contract, err := c.readListing(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.ContractsKeepResponse{Contract: contract}), nil
}

// Strike forwards the owner's striking of an addition the named contract keeps to orreryd as the owner's, and answers
// the contract as the reader holds it after the write, with the rows the striking removed.
func (c contractsServer) Strike(
	ctx context.Context,
	req *connect.Request[orreryv1.ContractsStrikeRequest],
) (*connect.Response[orreryv1.ContractsStrikeResponse], error) {
	id, addition, words := req.Msg.GetId(), req.Msg.GetAddition(), req.Msg.GetWords()
	var reply *connect.Response[enginev1.StrikeResponse]
	err := c.forwardStage(ctx, func(ctx context.Context, stage enginev1connect.StageClient) error {
		var err error
		reply, err = stage.Strike(ctx, connect.NewRequest(&enginev1.StrikeRequest{
			Contract: id, Addition: addition, Words: words,
			Maker: &enginev1.StageMaker{Kind: store.StageMakerOwnerServe},
		}))
		return wrapEngine("strike", err)
	})
	if err != nil {
		return nil, err
	}
	contract, err := c.readListing(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.ContractsStrikeResponse{
		Contract: contract,
		Removed:  reply.Msg.GetRemoved(),
	}), nil
}

// Shown forwards the rows of a contract version the owner was shown to orreryd, and answers the rows orreryd marked.
func (c contractsServer) Shown(
	ctx context.Context,
	req *connect.Request[orreryv1.ContractsShownRequest],
) (*connect.Response[orreryv1.ContractsShownResponse], error) {
	id, version, rows := req.Msg.GetId(), req.Msg.GetVersion(), req.Msg.GetRows()
	var reply *connect.Response[enginev1.ShownResponse]
	err := c.forwardStage(ctx, func(ctx context.Context, stage enginev1connect.StageClient) error {
		var err error
		reply, err = stage.Shown(ctx, connect.NewRequest(&enginev1.ShownRequest{
			Contract: id, Version: version, Rows: rows,
		}))
		return wrapEngine("shown", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.ContractsShownResponse{Rows: reply.Msg.GetRows()}), nil
}

// forwardStage runs call against the stage client. A nil client answers unavailable. A call whose connection was never
// made is retried while rpcretry.NeverConnected accepts its error. Any other error answers as translateError does.
func (c contractsServer) forwardStage(
	ctx context.Context,
	call func(context.Context, enginev1connect.StageClient) error,
) error {
	if c.stage == nil {
		return listError(connect.CodeUnavailable, contractStageUnavailable)
	}
	err := retryUnder(ctx, c.clock, c.timeout, rpcretry.NeverConnected, func(ctx context.Context) error {
		return call(ctx, c.stage)
	})
	if err != nil {
		return translateError(err)
	}
	return nil
}

// readListing answers the listing of the contract of id as the reader holds it. A nil reader answers unavailable, and
// an id no contract holds answers not_found.
func (c contractsServer) readListing(ctx context.Context, id string) (*orreryv1.Contract, error) {
	if c.reader == nil {
		return nil, listError(connect.CodeUnavailable, contractStoreUnavailable)
	}
	record, err := c.reader.Contract(ctx, id)
	if errors.Is(err, store.ErrNoContract) {
		return nil, listError(connect.CodeNotFound, contractNotFound)
	}
	if err != nil {
		return nil, contractReadError(err, "read contract")
	}
	return contractListingMessage(record.ContractListing), nil
}

// forwardCommand runs call against the contract command client. A nil client answers unavailable. A call whose
// connection was never made is retried while rpcretry.NeverConnected accepts its error. Any other error answers as
// translateError does.
func (c contractsServer) forwardCommand(
	ctx context.Context,
	call func(context.Context, enginev1connect.ContractsClient) error,
) error {
	if c.commands == nil {
		return listError(connect.CodeUnavailable, contractCommandsUnavailable)
	}
	err := retryUnder(ctx, c.clock, c.timeout, rpcretry.NeverConnected, func(ctx context.Context) error {
		return call(ctx, c.commands)
	})
	if err != nil {
		return translateError(err)
	}
	return nil
}

// contractReadError returns the error a contract read answers with for err: unavailable when the store is, and internal
// with message otherwise.
func contractReadError(err error, message string) error {
	if errors.Is(err, store.ErrUnavailable) {
		return listError(connect.CodeUnavailable, contractStoreUnavailable)
	}
	return listError(connect.CodeInternal, message)
}

// contractListingMessage converts a contract's listing to its Contract.
func contractListingMessage(listing store.ContractListing) *orreryv1.Contract {
	return &orreryv1.Contract{
		Id:            listing.ID,
		Repository:    listing.Repository,
		Title:         listing.Title,
		Care:          listing.Care,
		State:         listing.State,
		Version:       listing.Version,
		OpenDecisions: listing.OpenDecisions,
		Conversation:  listing.Conversation,
		Created:       listing.Created,
		Changed:       listing.Changed,
	}
}

// contractStoredMessage converts the StoredContract orreryd answers a command with to the Contract of its listing.
func contractStoredMessage(stored *enginev1.StoredContract) *orreryv1.Contract {
	return &orreryv1.Contract{
		Id:            stored.GetId(),
		Repository:    stored.GetRepository(),
		Title:         stored.GetTitle(),
		Care:          stored.GetCare(),
		State:         stored.GetState(),
		Version:       stored.GetVersion(),
		OpenDecisions: stored.GetOpenDecisions(),
		Conversation:  stored.GetConversation(),
		Created:       stored.GetCreated(),
		Changed:       stored.GetChanged(),
	}
}

// contractVersionMessage converts a stored version to its ContractVersion. Its body is the EpicContract in protobuf JSON,
// whose sections and requirements the message carries.
func contractVersionMessage(version store.ContractVersion) (*orreryv1.ContractVersion, error) {
	var body enginev1.EpicContract
	if err := protojson.Unmarshal([]byte(version.Body), &body); err != nil {
		return nil, fmt.Errorf("decode body of version %d: %w", version.Version, err)
	}
	decisions := make([]*orreryv1.ContractDecision, 0, len(version.Decisions))
	for _, decision := range version.Decisions {
		decisions = append(decisions, contractDecisionMessage(decision))
	}
	return &orreryv1.ContractVersion{
		Version:         version.Version,
		Contract:        epicContractMessage(&body, 0),
		Requirements:    epicRequirementMessages(body.GetRequirements(), nil),
		Decisions:       decisions,
		Words:           version.Words,
		Writer:          version.Writer,
		Time:            version.At,
		ChangedRows:     version.ChangedRows,
		ChangedSections: version.ChangedSections,
	}, nil
}

// contractDecisionMessage converts a decision of a version to its ContractDecision.
func contractDecisionMessage(decision store.ContractDecision) *orreryv1.ContractDecision {
	return &orreryv1.ContractDecision{
		Id:             decision.ID,
		Question:       decision.Question,
		Options:        decision.Options,
		Recommendation: decision.Recommendation,
		Reason:         decision.Reason,
		Row:            decision.Row,
		Answer:         decision.Answer,
	}
}

// contractStateMessage converts a state change to its ContractStateChange. A change a session made is by that session,
// and a change the owner made is by the owner, with no session. Its maker is the one the change stored, or nil when it
// stored none.
func contractStateMessage(state store.ContractStateChange) *orreryv1.ContractStateChange {
	by := contractByOwner
	if state.Session != "" {
		by = contractBySession
	}
	return &orreryv1.ContractStateChange{
		State:   state.State,
		Version: state.Version,
		By:      by,
		Session: state.Session,
		Words:   state.Words,
		Time:    state.At,
		Maker:   recordMakerMessage(state),
	}
}

// recordMakerMessage converts the maker a state change stored to its RecordMaker, which names the session that made the
// change. It returns nil when the change stored no maker.
func recordMakerMessage(state store.ContractStateChange) *orreryv1.RecordMaker {
	if state.Maker.Kind == "" {
		return nil
	}
	return &orreryv1.RecordMaker{
		Kind:       state.Maker.Kind,
		Quote:      state.Maker.Quote,
		Reason:     state.Maker.Reason,
		Delegation: state.Maker.Delegation,
		Session:    state.Session,
	}
}

// newContractsHandler returns the path and handler of the Contracts service, reading contracts from reader, reading
// each accepted contract's work from epicStore and evidence, forwarding its commands to commands and its keeps, strikes
// and shown rows to stage, all as contractsServer does. A zero timeout is defaultControlTimeout. Each request reads at
// most maxControlBody bytes.
func newContractsHandler(
	reader *store.Reader,
	epicStore EpicStoreClient,
	evidence EvidenceClient,
	commands enginev1connect.ContractsClient,
	stage enginev1connect.StageClient,
	timeout time.Duration,
	clock rpcretry.Clock,
	opts ...connect.HandlerOption,
) (string, http.Handler) {
	if timeout == 0 {
		timeout = defaultControlTimeout
	}
	options := append([]connect.HandlerOption{connect.WithReadMaxBytes(maxControlBody)}, opts...)
	return orreryv1connect.NewContractsHandler(
		contractsServer{
			reader: reader, store: epicStore, evidence: evidence, commands: commands, stage: stage, timeout: timeout,
			clock: clock,
		},
		options...,
	)
}
