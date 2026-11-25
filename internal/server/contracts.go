package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/joshsymonds/orrery/internal/contracttext"
	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/rpcretry"
	"github.com/joshsymonds/orrery/internal/settings"
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

// Get answers the contract of the id List gave it, with every version oldest first, the steps that read each version,
// the state history, the row writes with their makers, the work of each requirement (contractWork), the refusals of its
// writes, oldest first, what is left before it is accepted, the rows the owner was shown and the length of the body the
// page draws (setLength). An id no contract holds answers not_found.
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
	refusals, err := c.refusalMessages(ctx, record.ContractListing.ID)
	if err != nil {
		return nil, err
	}
	response := &orreryv1.ContractsGetResponse{
		Contract: contractListingMessage(record.ContractListing),
		Versions: versions,
		States:   states,
		Work:     work,
		Refusals: refusals,
	}
	err = c.setProvenance(ctx, record.ContractListing.ID, response)
	if err != nil {
		return nil, err
	}
	working, hasWorking, err := c.setWorking(ctx, record.ContractListing.ID, response)
	if err != nil {
		return nil, err
	}
	// A settings file that does not read leaves the length unset, and the rest of the contract is answered as it is.
	var lengths *settings.Lengths
	if read, settingsErr := settings.ContractLengths(os.Getenv); settingsErr == nil {
		lengths = &read
	}
	err = setLength(record, working, hasWorking, lengths, response)
	if err != nil {
		return nil, err
	}
	err = c.setLeft(ctx, record.ContractListing.ID, response)
	if err != nil {
		return nil, err
	}
	err = c.setShown(ctx, record.ContractListing.ID, response)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}

// setLeft sets what is left before the contract id is accepted on response, in the order the reader gives. A failed
// read answers as contractReadError does.
func (c contractsServer) setLeft(ctx context.Context, id string, response *orreryv1.ContractsGetResponse) error {
	left, err := c.reader.ContractLeft(ctx, id)
	if err != nil {
		return contractReadError(err, "read contract")
	}
	response.Left = make([]*orreryv1.ContractLeft, 0, len(left))
	for _, item := range left {
		response.Left = append(response.Left, &orreryv1.ContractLeft{Kind: item.Kind, Ref: item.Ref, Text: item.Text})
	}
	return nil
}

// setShown sets the rows of the contract versions the owner was shown on response, oldest first. A failed read answers
// as contractReadError does.
func (c contractsServer) setShown(ctx context.Context, id string, response *orreryv1.ContractsGetResponse) error {
	marks, err := c.reader.ContractShown(ctx, id)
	if err != nil {
		return contractReadError(err, "read contract")
	}
	response.Shown = make([]*orreryv1.ContractShown, 0, len(marks))
	for _, mark := range marks {
		response.Shown = append(response.Shown, &orreryv1.ContractShown{
			Version: mark.Version, Row: mark.Row, Time: mark.At,
		})
	}
	return nil
}

// setProvenance sets the steps that read each of response's versions on that version, oldest first, and the row writes
// of the contract id on response, oldest first, each with its maker. A failed read answers as contractReadError does.
func (c contractsServer) setProvenance(
	ctx context.Context,
	id string,
	response *orreryv1.ContractsGetResponse,
) error {
	reads, err := c.reader.ContractStepReads(ctx, id)
	if err != nil {
		return contractReadError(err, "read contract")
	}
	writes, err := c.reader.ContractRowWrites(ctx, id)
	if err != nil {
		return contractReadError(err, "read contract")
	}
	readBy := make(map[int64][]string)
	for _, read := range reads {
		readBy[read.Version] = append(readBy[read.Version], read.Step)
	}
	for _, version := range response.GetVersions() {
		version.ReadBy = readBy[version.GetVersion()]
	}
	response.Rows = make([]*orreryv1.ContractRowWrite, 0, len(writes))
	for _, write := range writes {
		response.Rows = append(response.Rows, contractRowWriteMessage(write))
	}
	return nil
}

// contractRowWriteMessage converts a row write to its ContractRowWrite. Its maker is the RecordMaker the write stored,
// with the session that stored it; a write the writing session made on its own account has kind "".
func contractRowWriteMessage(write store.ContractRowWrite) *orreryv1.ContractRowWrite {
	return &orreryv1.ContractRowWrite{
		Seq: write.Seq, Version: write.Version, Row: write.Row, Action: write.Action, Depends: write.Depends,
		Words: write.Words,
		Maker: &orreryv1.RecordMaker{
			Kind: write.Maker, Quote: write.Quote, Reason: write.MakerReason, Delegation: write.Delegation,
			Session: write.Session,
		},
		Time: write.At,
	}
}

// refusalMessages returns the refusals of the contract id as ContractRefusal messages, oldest first. A failed read answers
// as contractReadError does.
func (c contractsServer) refusalMessages(ctx context.Context, id string) ([]*orreryv1.ContractRefusal, error) {
	refusals, err := c.reader.ContractRefusals(ctx, id)
	if err != nil {
		return nil, contractReadError(err, "read contract")
	}
	messages := make([]*orreryv1.ContractRefusal, 0, len(refusals))
	for _, refusal := range refusals {
		messages = append(messages, contractRefusalMessage(refusal))
	}
	return messages, nil
}

// contractRefusalMessage converts a refusal of a contract's write to its ContractRefusal.
func contractRefusalMessage(refusal store.ContractRefusal) *orreryv1.ContractRefusal {
	return &orreryv1.ContractRefusal{
		Seq:     refusal.Seq,
		Version: refusal.Version,
		Field:   refusal.Field,
		Text:    refusal.Text,
		Session: refusal.Session,
		Time:    refusal.At,
	}
}

// setWorking sets the working copy of the contract id on response, which a contract with none leaves unset. It returns
// the stored working copy, and whether the contract has one. A failed read answers as contractReadError does.
func (c contractsServer) setWorking(
	ctx context.Context,
	id string,
	response *orreryv1.ContractsGetResponse,
) (store.ContractWorkingCopy, bool, error) {
	working, err := c.reader.ContractWorkingCopy(ctx, id)
	if errors.Is(err, store.ErrNoWorking) {
		return store.ContractWorkingCopy{}, false, nil
	}
	if err != nil {
		return store.ContractWorkingCopy{}, false, contractReadError(err, "read contract")
	}
	response.Working, err = contractWorkingMessage(working)
	if err != nil {
		return store.ContractWorkingCopy{}, false, contractReadError(err, "read contract")
	}
	return working, true, nil
}

// setLength sets the length of the body the page draws on response: the working copy's when the contract has one, and
// else the newest version's. A contract with neither draws no body, and its length is unset. The length is the lines
// above the checks of that body, against lengths, the thresholds the settings file sets. A nil lengths, for a settings
// file that does not read, leaves the length unset: length never refuses a read. A body that does not decode answers as
// contractReadError does.
func setLength(
	record store.ContractRecord,
	working store.ContractWorkingCopy,
	hasWorking bool,
	lengths *settings.Lengths,
	response *orreryv1.ContractsGetResponse,
) error {
	body, decisions, ok := drawnBody(record, working, hasWorking)
	if !ok || lengths == nil {
		return nil
	}
	var contract enginev1.EpicContract
	if err := protojson.Unmarshal([]byte(body), &contract); err != nil {
		return contractReadError(fmt.Errorf("decode body: %w", err), "read contract")
	}
	engineDecisions := make([]*enginev1.ContractDecision, 0, len(decisions))
	for _, decision := range decisions {
		engineDecisions = append(engineDecisions, engineDecisionMessage(decision))
	}
	response.Length = &orreryv1.ContractLength{
		Lines:  int64(contracttext.LinesAboveChecks(&contract, engineDecisions)),
		Warn:   int64(lengths.WarnLines),
		Strong: int64(lengths.StrongWarnLines),
	}
	return nil
}

// drawnBody returns the stored body and decisions the page draws for a contract: the working copy's when hasWorking,
// and else the newest version's. It returns false when the contract has neither.
func drawnBody(
	record store.ContractRecord,
	working store.ContractWorkingCopy,
	hasWorking bool,
) (string, []store.ContractDecision, bool) {
	if hasWorking {
		return working.Body, working.Decisions, true
	}
	if len(record.Versions) == 0 {
		return "", nil, false
	}
	newest := record.Versions[len(record.Versions)-1]
	return newest.Body, newest.Decisions, true
}

// engineDecisionMessage converts a decision of a stored body to the engine's ContractDecision, which the length counts.
func engineDecisionMessage(decision store.ContractDecision) *enginev1.ContractDecision {
	return &enginev1.ContractDecision{
		Id:             decision.ID,
		Question:       decision.Question,
		Options:        decision.Options,
		Recommendation: decision.Recommendation,
		Reason:         decision.Reason,
		Row:            decision.Row,
		Answer:         decision.Answer,
	}
}

// contractWorkingMessage converts a working copy to its ContractWorking. Its body is the EpicContract in protobuf JSON,
// whose sections and requirements the message carries.
func contractWorkingMessage(working store.ContractWorkingCopy) (*orreryv1.ContractWorking, error) {
	var body enginev1.EpicContract
	if err := protojson.Unmarshal([]byte(working.Body), &body); err != nil {
		return nil, fmt.Errorf("decode body of working copy of %s: %w", working.Contract, err)
	}
	decisions := make([]*orreryv1.ContractDecision, 0, len(working.Decisions))
	for _, decision := range working.Decisions {
		decisions = append(decisions, contractDecisionMessage(decision))
	}
	return &orreryv1.ContractWorking{
		Version:      working.Version,
		Contract:     epicContractMessage(&body, 0),
		Requirements: epicRequirementMessages(body.GetRequirements(), nil),
		Decisions:    decisions,
		Writer:       working.Writer,
		Updated:      working.Updated,
		ChangedRows:  working.ChangedRows,
	}, nil
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
