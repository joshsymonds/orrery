package enginehost

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/joshsymonds/orrery/internal/contracttext"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/settings"
	"github.com/joshsymonds/orrery/internal/store"
)

// contractUnavailableMessage is the message every Contracts RPC is refused with when there is no contract store.
const contractUnavailableMessage = "contract store unavailable"

// The owner and session names a contract's state change is made by, as its by field names them.
const (
	contractByOwner   = "owner"
	contractBySession = "session"
)

// ContractStore is the set of contract operations the Contracts service answers with. *store.Store satisfies it.
type ContractStore interface {
	CallerSession(ctx context.Context, caller store.EpicCaller) (string, bool, error)
	ProposeContract(ctx context.Context, id string, v store.ContractVersion) error
	ProposeContractInDesign(ctx context.Context, designID, id string, v store.ContractVersion) error
	DraftContract(ctx context.Context, draft store.ContractDraft) error
	ContractDraft(ctx context.Context, id string) (store.ContractDraft, error)
	ReviseContract(ctx context.Context, id string, v store.ContractVersion) error
	AcceptContract(ctx context.Context, id string, c store.ContractChange) error
	SetAsideContract(ctx context.Context, id string, c store.ContractChange) error
	ReopenContract(ctx context.Context, id string, c store.ContractChange) error
	Contract(ctx context.Context, id string) (store.ContractRecord, error)
	ContractWorkingCopy(ctx context.Context, id string) (store.ContractWorkingCopy, error)
	ContractStepReads(ctx context.Context, id string) ([]store.StepRead, error)
	ContractRowWrites(ctx context.Context, id string) ([]store.ContractRowWrite, error)
	ContractRefusals(ctx context.Context, id string) ([]store.ContractRefusal, error)
	ContractLeft(ctx context.Context, id string) ([]store.ContractLeft, error)
	ContractShown(ctx context.Context, id string) ([]store.ContractShown, error)
	RefuseContract(ctx context.Context, id string, refusal store.ContractRefusal) error
	Contracts(ctx context.Context) ([]store.ContractListing, error)
	Designs(ctx context.Context) ([]store.DesignRecord, error)
	AppendDesignLog(ctx context.Context, line store.DesignLogLine) error
}

// Contracts is what orreryd's Contracts service answers with. Now is the clock every stored time is read from. Getenv
// is the environment a contract's checks resolve in; nil reads the process environment. OriginMain returns the commit
// origin's main names in a contract's repository, which an accept stores as the contract's base; nil fetches origin's
// main with git. Hosts resolves host names to the Hosts that run a contract's git, and Host names the controller's
// host: a contract's checks and its origin's fetch run on the Host that Hosts resolves Host to. A nil Hosts resolves
// every name to this machine. AcceptCheck is what an accept asks of the contract's id after it reads the contract and
// before it fetches origin's main: a refusal it returns stops the accept and stores nothing. A nil AcceptCheck checks
// nothing.
type Contracts struct {
	Store       ContractStore
	Now         func() time.Time
	Getenv      func(string) string
	OriginMain  func(ctx context.Context, repository string) (string, error)
	Hosts       hostexec.Hosts
	Host        string
	AcceptCheck func(ctx context.Context, contract string) error
}

// contractService implements the Contracts RPCs over contracts. A nil contracts answers the RPCs it serves as
// unavailable.
type contractService struct {
	enginev1connect.UnimplementedContractsHandler
	contracts *Contracts
}

// ready returns the refusal every Contracts RPC it serves gets when there is no contract store.
func (s *contractService) ready() error {
	if s.contracts == nil {
		return refuseEpic(connect.CodeUnavailable, contractUnavailableMessage)
	}
	return nil
}

// ProposeContract stores the contract as version 1 in state proposed, at the clock's time, by the sessions row the
// caller resolves to. The id must be the body's. The body's checks run first, then the decisions, then the caller.
// The reply is the stored contract read back.
func (s *contractService) ProposeContract(
	ctx context.Context,
	req *connect.Request[v1.ProposeContractRequest],
) (*connect.Response[v1.ProposeContractResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	id := req.Msg.GetId()
	contract := req.Msg.GetContract()
	if err := s.checkContract(ctx, id, contract, req.Msg.GetDecisions()); err != nil {
		return nil, s.refuseProposal(ctx, req.Header(), id, req.Msg.GetDesign(), err)
	}
	warnings, err := s.contractLengthWarnings(contract, req.Msg.GetDecisions())
	if err != nil {
		return nil, err
	}
	writer, err := s.contractWriter(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	version, err := contractVersionOf(
		id, contract, req.Msg.GetDecisions(), req.Msg.GetWords(), writer, s.contracts.Now().Unix())
	if err != nil {
		return nil, err
	}
	design, err := s.proposeDesign(ctx, id, req.Msg.GetDesign())
	if err != nil {
		return nil, err
	}
	err = s.storeProposal(ctx, id, design, version)
	if err != nil {
		return nil, err
	}
	err = s.logContract(ctx, id, "proposed", writer, req.Msg.GetWords())
	if err != nil {
		return nil, err
	}
	stored, err := s.read(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.ProposeContractResponse{Contract: stored, Warnings: warnings}), nil
}

// storeProposal stores version as the new contract id, in design when design is not "", and answers a refusal of the
// store as a propose of the contract is answered: a held id is already held, a design no one holds is not found, and any
// other error is internal.
func (s *contractService) storeProposal(ctx context.Context, id, design string, version store.ContractVersion) error {
	var err error
	if design == "" {
		err = s.contracts.Store.ProposeContract(ctx, id, version)
	} else {
		err = s.contracts.Store.ProposeContractInDesign(ctx, design, id, version)
	}
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrContractExists):
		return refuseEpic(connect.CodeAlreadyExists, fmt.Sprintf("id: %s is already held by a contract or an epic", id))
	case errors.Is(err, store.ErrNoDesign):
		return refuseEpic(connect.CodeNotFound, fmt.Sprintf("design: %s not found", design))
	default:
		return epicStoreError(err)
	}
}

// refuseProposal answers the check refusal err of a propose of the contract id that names design, and keeps the refusal
// as refuseWrite does, for version 1, the version a propose makes. A propose that names no design joins the design of the
// draft of id, when a draft holds it.
func (s *contractService) refuseProposal(ctx context.Context, header http.Header, id, design string, err error) error {
	if !isCheckRefusal(err) {
		return err
	}
	if design == "" {
		draft, draftErr := s.contracts.Store.ContractDraft(ctx, id)
		if draftErr != nil && !errors.Is(draftErr, store.ErrNoDraft) {
			return epicStoreError(draftErr)
		}
		design = draft.Design
	}
	return s.refuseWrite(ctx, header, id, 1, design, err)
}

// refuseRevision answers the check refusal err of a revise of the contract id, and keeps the refusal as refuseWrite does,
// for the version after the newest. A contract no one holds has no version to keep the refusal under, so its refusal is
// answered unstored.
func (s *contractService) refuseRevision(ctx context.Context, header http.Header, id string, err error) error {
	if !isCheckRefusal(err) {
		return err
	}
	record, readErr := s.contracts.Store.Contract(ctx, id)
	if errors.Is(readErr, store.ErrNoContract) {
		return err
	}
	if readErr != nil {
		return epicStoreError(readErr)
	}
	return s.refuseWrite(ctx, header, id, record.Version+1, "", err)
}

// refuseWrite keeps the check refusal err of a write to the contract id that would make version, and answers err. It
// stores the refusal, with the caller's session, and then logs it as 'refused' in the design the contract is in, when
// refusalDesign finds one. An error that is not a check refusal is answered unchanged and stores nothing.
func (s *contractService) refuseWrite(
	ctx context.Context,
	header http.Header,
	id string,
	version int64,
	design string,
	err error,
) error {
	if !isCheckRefusal(err) {
		return err
	}
	session, _, sessionErr := s.callerSession(ctx, header)
	if sessionErr != nil {
		return sessionErr
	}
	text := refusalText(err)
	if storeErr := s.contracts.Store.RefuseContract(ctx, id, store.ContractRefusal{
		Version: version, Field: refusalField(text), Text: text, Session: session, At: s.contracts.Now().Unix(),
	}); storeErr != nil {
		return epicStoreError(storeErr)
	}
	logDesign, designErr := s.refusalDesign(ctx, id, design)
	if designErr != nil {
		return designErr
	}
	if logDesign == "" {
		return err
	}
	if logErr := s.logContractIn(ctx, logDesign, id, "refused", session, text); logErr != nil {
		return logErr
	}
	return err
}

// refusalDesign returns the design a refusal of a write to the contract id is logged in, or "" when it is logged in none.
// The design a design holds the contract in is that one. A contract no design holds is in the design named, when a design
// holds that id, and in none otherwise.
func (s *contractService) refusalDesign(ctx context.Context, id, named string) (string, error) {
	records, err := s.contracts.Store.Designs(ctx)
	if err != nil {
		return "", epicStoreError(err)
	}
	for _, record := range records {
		for _, contract := range record.Contracts {
			if contract.ID == id {
				return record.ID, nil
			}
		}
	}
	for _, record := range records {
		if named != "" && record.ID == named {
			return named, nil
		}
	}
	return "", nil
}

// isCheckRefusal reports whether err is a refusal the checks of a contract gave. Those are Connect errors with code
// InvalidArgument, and every other error the checks return is internal.
func isCheckRefusal(err error) bool {
	return connect.CodeOf(err) == connect.CodeInvalidArgument
}

// refusalText returns the whole message of the check refusal err.
func refusalText(err error) string {
	var coded *connect.Error
	if errors.As(err, &coded) {
		return coded.Message()
	}
	return err.Error()
}

// refusalField returns the field a refusal's text names: the text before its first ': ', or "" when the text has none.
func refusalField(text string) string {
	field, _, found := strings.Cut(text, ": ")
	if !found {
		return ""
	}
	return field
}

// controllerHost returns the Host that runs a contract's git before its epic exists: the controller's, which Hosts
// resolves Host to.
func (s *contractService) controllerHost() (hostexec.Host, error) {
	host, err := resolveHost(s.contracts.Hosts, s.contracts.Host)
	if err != nil {
		return nil, fmt.Errorf("controller host: %w", err)
	}
	return host, nil
}

// checkContract refuses a contract whose id is not id, whose body fails the checks of a contract, or whose decisions are
// malformed. Propose and revise both check their requests this way. The body's git runs on the controller's host.
func (s *contractService) checkContract(
	ctx context.Context,
	id string,
	contract *v1.EpicContract,
	decisions []*v1.ContractDecision,
) error {
	if id != contract.GetId() {
		return refuseEpic(connect.CodeInvalidArgument, "id: differs from the contract's id")
	}
	host, err := s.controllerHost()
	if err != nil {
		return epicStoreError(err)
	}
	if err = checkContractBody(ctx, host, contract, s.contractGetenv()); err != nil {
		return refuseEpic(connect.CodeInvalidArgument, err.Error())
	}
	if !contracttext.QualityBarLine(contract.GetChecks()) {
		return refuseEpic(connect.CodeInvalidArgument, "checks: the Quality Bar line is not verbatim")
	}
	if err = checkContractDecisions(decisions); err != nil {
		return refuseEpic(connect.CodeInvalidArgument, err.Error())
	}
	return nil
}

// contractGetenv returns the environment a contract's checks and settings resolve in: the Contracts' Getenv, or the
// process environment when it has none.
func (s *contractService) contractGetenv() func(string) string {
	if s.contracts.Getenv == nil {
		return os.Getenv
	}
	return s.contracts.Getenv
}

// contractLengthWarnings returns the length warning a contract with decisions gives, at the thresholds the settings
// file sets. Lines above the checks from strong_warn_lines up give one stronger warning, which tells the writer to
// cut or split; from warn_lines below strong_warn_lines they give one warning; below warn_lines they give none.
// Length never refuses. A settings file that does not read is the only refusal, and it is returned as the error.
func (s *contractService) contractLengthWarnings(
	contract *v1.EpicContract,
	decisions []*v1.ContractDecision,
) ([]string, error) {
	lengths, err := settings.ContractLengths(s.contractGetenv())
	if err != nil {
		return nil, epicStoreError(err)
	}
	lines := contracttext.LinesAboveChecks(contract, decisions)
	switch {
	case lines >= lengths.StrongWarnLines:
		return []string{fmt.Sprintf(
			"contract: %d lines above the checks, at or past the strong warning of %d lines: cut or split it",
			lines, lengths.StrongWarnLines,
		)}, nil
	case lines >= lengths.WarnLines:
		return []string{fmt.Sprintf(
			"contract: %d lines above the checks, at or past the warning of %d lines",
			lines, lengths.WarnLines,
		)}, nil
	default:
		return nil, nil
	}
}

// callerSession returns the sessions row the call's caller resolves to, and whether it resolves to one.
func (s *contractService) callerSession(ctx context.Context, header http.Header) (string, bool, error) {
	caller, _ := epicCallerOf(header)
	session, ok, err := s.contracts.Store.CallerSession(ctx, caller)
	if err != nil {
		return "", false, epicStoreError(err)
	}
	return session, ok, nil
}

// contractWriter returns the sessions row a contract version is written by. A caller that resolves to no session is
// refused, since only a session writes a version.
func (s *contractService) contractWriter(ctx context.Context, header http.Header) (string, error) {
	session, ok, err := s.callerSession(ctx, header)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", refuseEpic(connect.CodePermissionDenied, "caller: resolves to no session")
	}
	return session, nil
}

// changeSession returns the sessions row a state change is made by. A caller that resolves to no session is the owner,
// whose change has the empty session.
func (s *contractService) changeSession(ctx context.Context, header http.Header) (string, error) {
	session, _, err := s.callerSession(ctx, header)
	return session, err
}

// logContract appends the line of the design log that records what change to the contract id made, once the change is
// stored. The design is the one the contract is in now, which is its own id for a contract in no design. The line is
// made by session, or by the owner when session is "", with words. A failed read or append is answered as the store's
// other errors are.
func (s *contractService) logContract(ctx context.Context, id, what, session, words string) error {
	design, err := s.designOfContract(ctx, id)
	if err != nil {
		return err
	}
	return s.logContractIn(ctx, design, id, what, session, words)
}

// logContractIn appends the line of design's log that records what change to the contract id made, made by session or
// by the owner when session is "", with words. A failed append is answered as the store's other errors are.
func (s *contractService) logContractIn(ctx context.Context, design, id, what, session, words string) error {
	maker := store.LogMakerConversation
	if session == "" {
		maker = store.LogMakerOwner
	}
	line := store.DesignLogLine{
		Design: design, At: s.contracts.Now().Unix(), Maker: maker, Session: session, What: what,
		IDs: []string{id}, Words: words,
	}
	if err := s.contracts.Store.AppendDesignLog(ctx, line); err != nil {
		return epicStoreError(err)
	}
	return nil
}

// designOfContract returns the id of the design that holds the contract id: the design whose contracts include it. A
// contract in no design is a design of one, which Designs lists under the contract's own id, so that is the id returned
// when no listed design holds it.
func (s *contractService) designOfContract(ctx context.Context, id string) (string, error) {
	records, err := s.contracts.Store.Designs(ctx)
	if err != nil {
		return "", epicStoreError(err)
	}
	for _, record := range records {
		for _, contract := range record.Contracts {
			if contract.ID == id {
				return record.ID, nil
			}
		}
	}
	return id, nil
}

// contractVersionOf returns the store version of the body of contract with its decisions, words, writer and time. Its
// version number and changed rows and sections are the caller's to set.
func contractVersionOf(
	id string,
	contract *v1.EpicContract,
	decisions []*v1.ContractDecision,
	words, writer string,
	at int64,
) (store.ContractVersion, error) {
	body, err := protojson.Marshal(contract)
	if err != nil {
		return store.ContractVersion{}, epicStoreError(fmt.Errorf("encode contract %s: %w", id, err))
	}
	return store.ContractVersion{
		Repository: contract.GetRepository(),
		Title:      contract.GetTitle(),
		Care:       contract.GetCare(),
		Body:       string(body),
		Decisions:  storeDecisions(decisions),
		Words:      words,
		Writer:     writer,
		At:         at,
	}, nil
}

// contractNotFound returns the refusal a contract no one holds gets.
func contractNotFound(id string) error {
	return refuseEpic(connect.CodeNotFound, fmt.Sprintf("contract %s not found", id))
}

// GetContract returns the contract with its versions, states, working copy, the steps that read each version, the
// row writes its stage recorded, its refusals, what is left before it is accepted and the rows the owner was shown.
func (s *contractService) GetContract(
	ctx context.Context,
	req *connect.Request[v1.GetContractRequest],
) (*connect.Response[v1.GetContractResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	stored, err := s.read(ctx, req.Msg.GetId())
	if err != nil {
		return nil, err
	}
	err = s.attachWorking(ctx, req.Msg.GetId(), stored)
	if err != nil {
		return nil, err
	}
	err = s.attachProvenance(ctx, req.Msg.GetId(), stored)
	if err != nil {
		return nil, err
	}
	err = s.attachRefusals(ctx, req.Msg.GetId(), stored)
	if err != nil {
		return nil, err
	}
	err = s.attachLeft(ctx, req.Msg.GetId(), stored)
	if err != nil {
		return nil, err
	}
	err = s.attachShown(ctx, req.Msg.GetId(), stored)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.GetContractResponse{Contract: stored}), nil
}

// attachLeft sets what is left before the contract id is accepted on stored, in the order the store gives. A failed
// read is answered as the store's other errors are.
func (s *contractService) attachLeft(ctx context.Context, id string, stored *v1.StoredContract) error {
	left, err := s.contracts.Store.ContractLeft(ctx, id)
	if err != nil {
		return epicStoreError(err)
	}
	stored.Left = epicMap(left, contractLeftMessage)
	return nil
}

// contractLeftMessage returns the ContractLeft message of left.
func contractLeftMessage(left store.ContractLeft) *v1.ContractLeft {
	return &v1.ContractLeft{Kind: left.Kind, Ref: left.Ref, Text: left.Text}
}

// attachShown sets the rows of the contract versions the owner was shown on stored, oldest first. A failed read is
// answered as the store's other errors are.
func (s *contractService) attachShown(ctx context.Context, id string, stored *v1.StoredContract) error {
	marks, err := s.contracts.Store.ContractShown(ctx, id)
	if err != nil {
		return epicStoreError(err)
	}
	stored.Shown = epicMap(marks, contractShownMessage)
	return nil
}

// contractShownMessage returns the ContractShown message of mark.
func contractShownMessage(mark store.ContractShown) *v1.ContractShown {
	return &v1.ContractShown{Version: mark.Version, Row: mark.Row, Time: mark.At}
}

// attachProvenance sets the steps that read each version of stored on that version, oldest first, and the row writes of
// the contract id on stored, oldest first, each with its maker. A failed read is answered as the store's other errors
// are.
func (s *contractService) attachProvenance(ctx context.Context, id string, stored *v1.StoredContract) error {
	reads, err := s.contracts.Store.ContractStepReads(ctx, id)
	if err != nil {
		return epicStoreError(err)
	}
	writes, err := s.contracts.Store.ContractRowWrites(ctx, id)
	if err != nil {
		return epicStoreError(err)
	}
	readBy := make(map[int64][]string)
	for _, read := range reads {
		readBy[read.Version] = append(readBy[read.Version], read.Step)
	}
	for _, version := range stored.GetVersions() {
		version.ReadBy = readBy[version.GetVersion()]
	}
	stored.Rows = epicMap(writes, stageRowWriteMessage)
	return nil
}

// stageRowWriteMessage returns the StageRowWrite message of write, with the maker and the session the write stored.
func stageRowWriteMessage(write store.ContractRowWrite) *v1.StageRowWrite {
	return &v1.StageRowWrite{
		Seq: write.Seq, Version: write.Version, Row: write.Row, Action: write.Action, Depends: write.Depends,
		Words: write.Words,
		Maker: &v1.StageMaker{
			Kind: write.Maker, Quote: write.Quote, Reason: write.MakerReason, Delegation: write.Delegation,
		},
		Session: write.Session, At: write.At,
	}
}

// attachRefusals sets the refusals of the contract id on stored, oldest first. A failed read is answered as the store's
// other errors are.
func (s *contractService) attachRefusals(ctx context.Context, id string, stored *v1.StoredContract) error {
	refusals, err := s.contracts.Store.ContractRefusals(ctx, id)
	if err != nil {
		return epicStoreError(err)
	}
	stored.Refusals = epicMap(refusals, contractRefusalMessage)
	return nil
}

// contractRefusalMessage returns the ContractRefusal message of refusal.
func contractRefusalMessage(refusal store.ContractRefusal) *v1.ContractRefusal {
	return &v1.ContractRefusal{
		Seq:     refusal.Seq,
		Version: refusal.Version,
		Field:   refusal.Field,
		Text:    refusal.Text,
		Session: refusal.Session,
		Time:    refusal.At,
	}
}

// attachWorking sets the working copy of the contract id on stored, which a contract with none leaves unset. A failed read
// is answered as the store's other errors are.
func (s *contractService) attachWorking(ctx context.Context, id string, stored *v1.StoredContract) error {
	working, err := s.contracts.Store.ContractWorkingCopy(ctx, id)
	if errors.Is(err, store.ErrNoWorking) {
		return nil
	}
	if err != nil {
		return epicStoreError(err)
	}
	stored.Working, err = contractWorkingMessage(working)
	return err
}

// contractWorkingMessage returns the ContractWorking message of working, its body decoded from protobuf JSON.
func contractWorkingMessage(working store.ContractWorkingCopy) (*v1.ContractWorking, error) {
	var contract v1.EpicContract
	if err := protojson.Unmarshal([]byte(working.Body), &contract); err != nil {
		return nil, epicStoreError(fmt.Errorf("decode body of working copy of %s: %w", working.Contract, err))
	}
	return &v1.ContractWorking{
		Version:     working.Version,
		Contract:    &contract,
		Decisions:   epicMap(working.Decisions, contractDecisionMessage),
		Writer:      working.Writer,
		Updated:     working.Updated,
		ChangedRows: working.ChangedRows,
	}, nil
}

// ListContracts returns the listing of every contract, without versions or states.
func (s *contractService) ListContracts(
	ctx context.Context,
	_ *connect.Request[v1.ListContractsRequest],
) (*connect.Response[v1.ListContractsResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	listings, err := s.contracts.Store.Contracts(ctx)
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.ListContractsResponse{
		Contracts: epicMap(listings, storedListingOf),
	}), nil
}

// read returns the contract with id as the store holds it, with its versions and states. A contract no one holds is
// refused as not found.
func (s *contractService) read(ctx context.Context, id string) (*v1.StoredContract, error) {
	record, err := s.contracts.Store.Contract(ctx, id)
	if errors.Is(err, store.ErrNoContract) {
		return nil, contractNotFound(id)
	}
	if err != nil {
		return nil, epicStoreError(err)
	}
	stored := storedListingOf(record.ContractListing)
	for _, version := range record.Versions {
		message, convertErr := contractVersionMessage(version)
		if convertErr != nil {
			return nil, epicStoreError(
				fmt.Errorf("read version %d of contract %s: %w", version.Version, id, convertErr),
			)
		}
		stored.Versions = append(stored.Versions, message)
	}
	stored.States = epicMap(record.States, contractStateMessage)
	return stored, nil
}

// checkContractDecisions refuses a decision with an empty id, a repeated id, or an empty question. Propose and revise
// check their decisions as written, as CreateEpic checks its seed decisions.
func checkContractDecisions(decisions []*v1.ContractDecision) error {
	seen := make(map[string]bool, len(decisions))
	for i, decision := range decisions {
		id := decision.GetId()
		if id == "" {
			return &epicFieldError{field: epicRowField("decisions", i, "id"), reason: epicReasonEmpty}
		}
		if seen[id] {
			return &epicFieldError{field: epicRowField("decisions", i, "id"), reason: epicReasonDuplicate}
		}
		seen[id] = true
		if decision.GetQuestion() == "" {
			return &epicFieldError{field: epicRowField("decisions", i, "question"), reason: epicReasonEmpty}
		}
	}
	return nil
}

// storedListingOf returns the StoredContract of a listing, with neither versions nor states.
func storedListingOf(listing store.ContractListing) *v1.StoredContract {
	return &v1.StoredContract{
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

// contractVersionMessage returns the ContractVersion message of version, its body decoded from protobuf JSON.
func contractVersionMessage(version store.ContractVersion) (*v1.ContractVersion, error) {
	var contract v1.EpicContract
	if err := protojson.Unmarshal([]byte(version.Body), &contract); err != nil {
		return nil, fmt.Errorf("decode body: %w", err)
	}
	return &v1.ContractVersion{
		Version:         version.Version,
		Contract:        &contract,
		Decisions:       epicMap(version.Decisions, contractDecisionMessage),
		Words:           version.Words,
		Writer:          version.Writer,
		Time:            version.At,
		ChangedRows:     version.ChangedRows,
		ChangedSections: version.ChangedSections,
	}, nil
}

// contractDecisionMessage returns the ContractDecision message of decision.
func contractDecisionMessage(decision store.ContractDecision) *v1.ContractDecision {
	return &v1.ContractDecision{
		Id:             decision.ID,
		Question:       decision.Question,
		Options:        decision.Options,
		Recommendation: decision.Recommendation,
		Reason:         decision.Reason,
		Row:            decision.Row,
		Answer:         decision.Answer,
	}
}

// storeDecisions returns the store decisions of the decision messages decisions.
func storeDecisions(decisions []*v1.ContractDecision) []store.ContractDecision {
	return epicMap(decisions, func(decision *v1.ContractDecision) store.ContractDecision {
		return store.ContractDecision{
			ID:             decision.GetId(),
			Question:       decision.GetQuestion(),
			Options:        decision.GetOptions(),
			Recommendation: decision.GetRecommendation(),
			Reason:         decision.GetReason(),
			Row:            decision.GetRow(),
			Answer:         decision.GetAnswer(),
		}
	})
}

// contractStateMessage returns the ContractStateChange message of state. A change its owner made names the owner, and
// the maker the change stored is its maker, or nil when it stored none.
func contractStateMessage(state store.ContractStateChange) *v1.ContractStateChange {
	by := contractByOwner
	if state.Session != "" {
		by = contractBySession
	}
	return &v1.ContractStateChange{
		State:   state.State,
		Version: state.Version,
		By:      by,
		Session: state.Session,
		Words:   state.Words,
		Time:    state.At,
		Maker:   stageMakerMessage(state.Maker),
	}
}

// stageMakerMessage returns the StageMaker message of maker, or nil when maker is the zero StageMaker.
func stageMakerMessage(maker store.StageMaker) *v1.StageMaker {
	if maker.Kind == "" {
		return nil
	}
	return &v1.StageMaker{Kind: maker.Kind, Quote: maker.Quote, Reason: maker.Reason, Delegation: maker.Delegation}
}
