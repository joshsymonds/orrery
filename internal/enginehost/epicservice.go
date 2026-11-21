package enginehost

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/evidence"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// epicUnavailableMessage is the message every epic RPC is refused with when there is no epic store.
const epicUnavailableMessage = "epic store unavailable"

// epicPhaseAccepted is the phase of an epic the Epics service creates.
const epicPhaseAccepted = "accepted"

// steeringUnavailableMessage is the message a create that needs a steering session started is refused with when there
// are no steering sessions.
const steeringUnavailableMessage = "steering sessions unavailable"

// EpicCommands is the set of epic operations the Epics service answers with. The store satisfies it.
type EpicCommands interface {
	CreateContractEpic(ctx context.Context, row store.EpicRow, version int64) error
	Contract(ctx context.Context, id string) (store.ContractRecord, error)
	Epic(ctx context.Context, id string) (store.EpicRow, error)
	EpicFlow(ctx context.Context, id string) (store.EpicFlow, error)
	EpicHistory(ctx context.Context, id string) (store.EpicHistory, error)
	MoveEpic(ctx context.Context, epic, to string, maker store.Maker, at time.Time) (store.EpicAction, error)
	RecordEpicAction(ctx context.Context, epic, kind string, maker store.Maker, at time.Time) error
	SteerTarget(ctx context.Context, id string) (store.SteerTarget, bool, error)
	Epics(ctx context.Context) ([]store.EpicRow, error)
	EpicListings(ctx context.Context) ([]store.EpicRow, error)
	CallerMaker(ctx context.Context, caller store.EpicCaller) (store.Maker, error)
	CallerSession(ctx context.Context, caller store.EpicCaller) (string, bool, error)
	AnswerEpic(
		ctx context.Context, epic, id, text string, maker store.Maker, at time.Time,
	) (store.EpicDecision, error)
	AnswerQuestion(
		ctx context.Context, epic, id, text string, question int64, choice string, maker store.Maker,
		at time.Time,
	) (store.EpicDecision, error)
	OpenQuestions(ctx context.Context, epic string) ([]store.EpicQuestion, error)
	Follows(ctx context.Context) ([]store.LabelFollow, error)
	PauseEpic(ctx context.Context, epic string, maker store.Maker, at time.Time) error
	ResumeEpic(ctx context.Context, epic string, maker store.Maker, at time.Time) error
	StopEpic(ctx context.Context, epic string, maker store.Maker, at time.Time) (string, error)
	RetryEpic(ctx context.Context, epic string, maker store.Maker, at time.Time) error
	RetryNode(ctx context.Context, epic, node string, maker store.Maker, at time.Time) error
	KickEpic(ctx context.Context, epic, text string, maker store.Maker, at time.Time) error
	ReleaseEpic(ctx context.Context, epic string, undoNone []int64, maker store.Maker, at time.Time) error
	CloseEpic(
		ctx context.Context, epic, kind, by, closer string, maker store.Maker, at time.Time,
	) (string, error)
	AmendEvidence(
		ctx context.Context,
		epic string,
		amend store.Amendment,
		maker store.Maker,
		at time.Time,
	) (store.EvidenceAmendment, error)
	SeeEpic(ctx context.Context, epic string, at time.Time) error
	ArchiveRow(ctx context.Context, kind, id string, at time.Time) error
	UnarchiveRow(ctx context.Context, kind, id string, at time.Time) error
}

// SteeringSessions starts and stops the steering sessions of epics, and reads the directory of a tmux pane. *EpicSteering
// satisfies it. CreateEpic starts the steering session of an epic that no conversation creates, and stops it when the
// create then stores nothing.
type SteeringSessions interface {
	Start(ctx context.Context, row store.EpicRow) (string, error)
	Stop(ctx context.Context, id string) error
	PaneDir(ctx context.Context, server, pane string) (string, error)
}

// epicService implements the Epics RPCs over epics. getenv reads the environment a contract's gates resolve in; nil
// reads the process environment. steps is the step store StopEpic stops the turn's step through; it is nil when Serve
// has no step engine, and StopEpic then stops no step. steering starts the steering session of an epic that no
// conversation creates; it is nil when Serve has no steering, and a create that needs a start is then refused.
type epicService struct {
	epics    EpicCommands
	steps    StepCommands
	steering SteeringSessions
	getenv   func(string) string
}

// ready returns the refusal every epic RPC gets when there is no epic store.
func (s *epicService) ready() error {
	if s.epics == nil {
		return refuseEpic(connect.CodeUnavailable, epicUnavailableMessage)
	}
	return nil
}

// callerMaker returns who made the call whose headers are header, as the store resolves its caller. Headers that
// name no caller make the owner the maker.
func (s *epicService) callerMaker(ctx context.Context, header http.Header) (store.Maker, error) {
	caller, _ := epicCallerOf(header)
	maker, err := s.epics.CallerMaker(ctx, caller)
	if err != nil {
		return store.Maker{}, epicStoreError(err)
	}
	return maker, nil
}

// controlMaker returns who makes a changing call of epic whose headers are header. A conversation that does not steer
// epic is refused with FailedPrecondition, naming the steerer and epic_steer, before the call writes anything. The owner
// and a step pass, and so does a conversation of an epic that is not stored, which its handler answers NotFound.
func (s *epicService) controlMaker(ctx context.Context, epic string, header http.Header) (store.Maker, error) {
	maker, err := s.callerMaker(ctx, header)
	if err != nil || maker.By != store.MakerConversation {
		return maker, err
	}
	row, err := s.epics.Epic(ctx, epic)
	switch {
	case errors.Is(err, store.ErrNoEpic):
		return maker, nil
	case err != nil:
		return store.Maker{}, epicStoreError(err)
	case row.Steerer == maker.Session:
		return maker, nil
	case row.Steerer == "":
		return store.Maker{}, refuseEpic(connect.CodeFailedPrecondition, fmt.Sprintf(
			"session %s does not steer epic %s: no conversation steers it; epic_steer makes this conversation the steerer",
			maker.Session,
			epic,
		))
	}
	return store.Maker{}, refuseEpic(connect.CodeFailedPrecondition, fmt.Sprintf(
		"session %s does not steer epic %s: session %s steers it; epic_steer makes this conversation the steerer",
		maker.Session, epic, row.Steerer,
	))
}

// CreateEpic makes the epic of the accepted contract the request names by its id. The epic takes that id, and the body
// of the contract's accepted version with the request's base in place of the body's. A blank base takes the base the
// contract stored when it was accepted. It checks that contract with checkEpic, collects the lint's warnings, merges its
// reviewers, resolves its base to the full commit the base names, pins the workflow the request names at that commit,
// then stores it accepted, with its seed decisions and, when the base differs from the stored one, one more decision
// that names both, and returns the stored epic read back with the warnings. Its review list is the enabled task-stage
// reviewers, in merged order. A contract no one holds, one that is not accepted, a reviewer no layer can hold, and a
// workflow that does not load at the base are refused, and nothing is stored.
//
// The seed decisions are made by the caller. The steerer of the epic is the contract's conversation, the writer of its
// first version, whichever caller makes the create, when that conversation's sessions row is stored. Otherwise the id
// of an epic already stored is refused first, and then one steering session is started for the epic. A start that is
// refused, or a store that fails after the start, refuses the create, and the started session is stopped.
func (s *epicService) CreateEpic(
	ctx context.Context,
	req *connect.Request[v1.CreateEpicRequest],
) (*connect.Response[v1.CreateEpicResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	getenv := s.getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	contract, conversation, version, stored, err := s.acceptedContract(ctx, req.Msg.GetId(), req.Msg.GetBase())
	if err != nil {
		return nil, err
	}
	if err = checkEpic(ctx, contract, req.Msg.GetDecisions(), getenv); err != nil {
		return nil, refuseEpic(connect.CodeInvalidArgument, err.Error())
	}
	warnings, err := contractLintWarnings(ctx, contract)
	if err != nil {
		return nil, refuseEpic(connect.CodeInvalidArgument, err.Error())
	}
	merged, err := epicReviewers(ctx, contract, getenv)
	if err != nil {
		return nil, refuseEpic(connect.CodeInvalidArgument, err.Error())
	}
	base, err := stepGitResolve(ctx, contract.GetRepository(), contract.GetBase())
	if err != nil {
		return nil, epicStoreError(fmt.Errorf("resolve base: %w", err))
	}
	workflow, err := pinWorkflow(ctx, contract.GetRepository(), base, req.Msg.GetWorkflow())
	if err != nil {
		return nil, refuseEpic(connect.CodeInvalidArgument, "workflow: "+err.Error())
	}
	maker, err := s.callerMaker(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	row := epicRowOf(contract, req.Msg.GetDecisions(), base, workflow, time.Now())
	row.Reviewers = merged
	row.ReviewGates = taskReviewNames(merged)
	if decision, ok := contractBaseDecision(req.Msg.GetDecisions(), base, stored); ok {
		row.Decisions = append(row.Decisions, decision)
	}
	for i := range row.Decisions {
		row.Decisions[i].Maker = maker
	}
	steerer, started, err := s.steererOf(ctx, row, conversation)
	if err != nil {
		return nil, err
	}
	row.Steerer = steerer
	if err = s.epics.CreateContractEpic(ctx, row, version); err != nil {
		return nil, s.refuseCreate(ctx, row.ID, started, err)
	}
	message, err := s.storedEpic(ctx, row.ID)
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.CreateEpicResponse{Epic: message, Warnings: warnings}), nil
}

// acceptedContract returns the body of the accepted version of the contract id, with its base set to base, the
// contract's conversation, the writer of its version 1, the accepted version's number, and the base the accept stored.
// A blank base is replaced by the stored base first. An id no contract holds is refused as not found, and a contract
// whose newest state is not accepted is refused as a failed precondition that names the state.
func (s *epicService) acceptedContract(
	ctx context.Context, id, base string,
) (*v1.EpicContract, string, int64, string, error) {
	record, err := s.epics.Contract(ctx, id)
	switch {
	case errors.Is(err, store.ErrNoContract):
		return nil, "", 0, "", contractNotFound(id)
	case err != nil:
		return nil, "", 0, "", epicStoreError(err)
	case record.State != store.ContractAccepted:
		return nil, "", 0, "", refuseEpic(
			connect.CodeFailedPrecondition,
			fmt.Sprintf("state: %s, not accepted", record.State),
		)
	}
	newest := record.States[len(record.States)-1]
	if isBlank(base) {
		base = newest.Base
	}
	for _, version := range record.Versions {
		if version.Version != newest.Version {
			continue
		}
		message, convertErr := contractVersionMessage(version)
		if convertErr != nil {
			return nil, "", 0, "", epicStoreError(
				fmt.Errorf("read version %d of contract %s: %w", version.Version, id, convertErr),
			)
		}
		contract := message.GetContract()
		contract.Base = base
		return contract, record.Conversation, version.Version, newest.Base, nil
	}
	return nil, "", 0, "", epicStoreError(fmt.Errorf("contract %s has no version %d", id, newest.Version))
}

// contractBaseDecision returns the decision a create records after its seeds when base, the commit the create starts the
// epic at, differs from stored, the base the contract stored when it was accepted. The decision names both commits. Its
// maker is set by the create, as the seeds' are. Seed decision ids are the caller's, unique and otherwise unchecked, so
// the id is epic-base, or epic-base-n for the first n from 2 that no seed holds. It returns false when the contract
// stored no base or when base is the stored one.
func contractBaseDecision(seeds []*v1.EpicDecisionInput, base, stored string) (store.EpicDecision, bool) {
	if stored == "" || base == stored {
		return store.EpicDecision{}, false
	}
	taken := make(map[string]bool, len(seeds))
	for _, seed := range seeds {
		taken[seed.GetId()] = true
	}
	id := "epic-base"
	for n := 2; taken[id]; n++ {
		id = fmt.Sprintf("epic-base-%d", n)
	}
	return store.EpicDecision{
		ID: id,
		Text: fmt.Sprintf(
			"base %s named at create replaces the base %s the contract stored when it was accepted",
			base, stored,
		),
	}, true
}

// storedRefusal returns the refusal of a create whose id, id, is already stored.
func storedRefusal(id string) error {
	return refuseEpic(connect.CodeAlreadyExists, fmt.Sprintf("id: %s is already in the database", id))
}

// refuseStored returns the refusal of a create whose id is already stored, and nil when no epic has id.
func (s *epicService) refuseStored(ctx context.Context, id string) error {
	_, err := s.epics.Epic(ctx, id)
	switch {
	case err == nil:
		return storedRefusal(id)
	case errors.Is(err, store.ErrNoEpic):
		return nil
	default:
		return epicStoreError(err)
	}
}

// steererOf returns the steerer that a create of row stores, given conversation, the contract's conversation, and the
// id of the steering session the create started, which is "" when it started none. An id already stored is refused
// first. When conversation has a sessions row, it is the steerer. Otherwise one steering session is started for row,
// as it is for an epic a shell or the CLI makes.
func (s *epicService) steererOf(ctx context.Context, row store.EpicRow, conversation string) (string, string, error) {
	if err := s.refuseStored(ctx, row.ID); err != nil {
		return "", "", err
	}
	session, ok, err := s.epics.CallerSession(ctx, store.EpicCaller{Session: conversation})
	if err != nil {
		return "", "", epicStoreError(err)
	}
	if ok {
		return session, "", nil
	}
	started, err := s.startSteering(ctx, row)
	if err != nil {
		return "", "", err
	}
	return started, started, nil
}

// startSteering starts the steering session of row through the steering sessions and returns its id. With no steering
// sessions, or a start the session cap refuses or a session that fails to spawn, it returns the refusal and no id.
func (s *epicService) startSteering(ctx context.Context, row store.EpicRow) (string, error) {
	if s.steering == nil {
		return "", refuseEpic(connect.CodeUnavailable, steeringUnavailableMessage)
	}
	id, err := s.steering.Start(ctx, row)
	switch {
	case errors.Is(err, ErrCapReached):
		return "", refuseEpic(connect.CodeResourceExhausted, err.Error())
	case err != nil:
		return "", refuseEpic(connect.CodeUnavailable, err.Error())
	}
	return id, nil
}

// refuseCreate returns the refusal of a create of the epic id that the store refused with err. A contract the store
// finds no longer accepted, or no longer holding the version the create read, is refused as a failed precondition,
// state: no longer accepted. When the create started the steering session started, that session is stopped, even
// when ctx has ended, and a stop error joins the refusal.
func (s *epicService) refuseCreate(ctx context.Context, id, started string, err error) error {
	refusal := epicStoreError(err)
	switch {
	case errors.Is(err, store.ErrEpicExists):
		refusal = storedRefusal(id)
	case errors.Is(err, store.ErrContractState), errors.Is(err, store.ErrNoContract):
		refusal = refuseEpic(connect.CodeFailedPrecondition, "state: no longer accepted")
	}
	if started == "" {
		return refusal
	}
	if stopErr := s.steering.Stop(context.WithoutCancel(ctx), started); stopErr != nil {
		return errors.Join(refusal, fmt.Errorf("stop steering session %s: %w", started, stopErr))
	}
	return refusal
}

// GetEpic returns the stored epic with its workflow node runs. When req asks for history, it also returns the epic's
// triggers and tells.
func (s *epicService) GetEpic(
	ctx context.Context,
	req *connect.Request[v1.GetEpicRequest],
) (*connect.Response[v1.GetEpicResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	id := req.Msg.GetId()
	message, err := s.storedEpic(ctx, id)
	if err == nil && req.Msg.GetHistory() {
		err = s.fillHistory(ctx, id, message)
	}
	if errors.Is(err, store.ErrNoEpic) {
		return nil, refuseEpic(connect.CodeNotFound, fmt.Sprintf("id: no epic %s", id))
	}
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.GetEpicResponse{Epic: message}), nil
}

// fillHistory sets the triggers, tells and actions of message to the history the store reads for the epic with id. It
// returns an error matching store.ErrNoEpic when no epic has that id.
func (s *epicService) fillHistory(ctx context.Context, id string, message *v1.StoredEpic) error {
	history, err := s.epics.EpicHistory(ctx, id)
	if err != nil {
		return fmt.Errorf("read history of epic %s: %w", id, err)
	}
	message.Triggers = epicMap(history.Triggers, triggerMessage)
	message.Tells = epicMap(history.Tells, tellMessage)
	message.Actions = epicMap(history.Actions, actionMessage)
	return nil
}

// storedEpic returns the StoredEpic message of the epic with id with its workflow node runs, its review, its release
// runs, its release proof and the holder of its repository's release slot, all read in one read of the store, so every
// reply that carries the epic shows the same state. It returns an error matching store.ErrNoEpic when no epic has that
// id.
func (s *epicService) storedEpic(ctx context.Context, id string) (*v1.StoredEpic, error) {
	flow, err := s.epics.EpicFlow(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("read epic %s: %w", id, err)
	}
	message, err := storedEpicMessage(flow.Epic)
	if err != nil {
		return nil, err
	}
	requirementClaims(message, flow)
	message.Nodes, err = nodeMessages(flow)
	if err != nil {
		return nil, err
	}
	message.Ledger = reviewLedgerMessage(flow.Review)
	message.Frozen = flow.Review.Frozen
	message.Clean = flow.Review.Clean
	message.Releases = epicMap(flow.Releases, releaseRunMessage)
	message.ReleaseSlot = flow.Slot
	message.ReleaseProof = epicMap(store.ReleaseProofs(flow), releaseProofMessage)
	return message, nil
}

func (s *epicService) ListEpics(
	ctx context.Context,
	_ *connect.Request[v1.ListEpicsRequest],
) (*connect.Response[v1.ListEpicsResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	rows, err := s.epics.EpicListings(ctx)
	if err != nil {
		return nil, epicStoreError(err)
	}
	summaries := make([]*v1.EpicSummary, 0, len(rows))
	for _, row := range rows {
		summary, summaryErr := summaryMessage(row)
		if summaryErr != nil {
			return nil, epicStoreError(summaryErr)
		}
		summaries = append(summaries, summary)
	}
	return connect.NewResponse(&v1.ListEpicsResponse{Epics: summaries}), nil
}

// FollowEpics returns the follow count of every label across all epics, in label order.
func (s *epicService) FollowEpics(
	ctx context.Context,
	_ *connect.Request[v1.FollowEpicsRequest],
) (*connect.Response[v1.FollowEpicsResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	follows, err := s.epics.Follows(ctx)
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.FollowEpicsResponse{Follows: epicMap(follows, followMessage)}), nil
}

// AnswerEpic records the answer on the epic, once. It refuses an empty epic_id or id first, then a note that names a
// choice or has no text, and an answer to a question that names no choice. An answer with no question is a note, and
// it warns, when questions are open, naming them. An answer with a question is tied to it and to its choice, which
// closes it; a question that is not Q<n> with a positive n is refused as unknown. The store then answers an unknown
// epic, an unknown, replaced or closed question, a choice the question does not offer, other with blank text, or an id
// recorded with other text or another choice, as the refusals say.
func (s *epicService) AnswerEpic(
	ctx context.Context,
	req *connect.Request[v1.AnswerEpicRequest],
) (*connect.Response[v1.AnswerEpicResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := checkEpicAnswer(req.Msg); err != nil {
		return nil, err
	}
	maker, err := s.controlMaker(ctx, req.Msg.GetEpicId(), req.Header())
	if err != nil {
		return nil, err
	}
	if req.Msg.GetQuestion() == "" {
		return s.answerNote(ctx, req.Msg, maker)
	}
	seq, ok := questionSeq(req.Msg.GetQuestion())
	if !ok {
		return nil, questionNotFound(req.Msg)
	}
	decision, err := s.epics.AnswerQuestion(
		ctx, req.Msg.GetEpicId(), req.Msg.GetId(), req.Msg.GetText(), seq, req.Msg.GetChoice(), maker,
		time.Now(),
	)
	if err != nil {
		return nil, answerError(req.Msg, err)
	}
	return connect.NewResponse(&v1.AnswerEpicResponse{Decision: decisionMessage(decision)}), nil
}

// answerNote records the note req is, made by maker, and returns it with the warning naming the open questions when
// any are open.
func (s *epicService) answerNote(
	ctx context.Context,
	req *v1.AnswerEpicRequest,
	maker store.Maker,
) (*connect.Response[v1.AnswerEpicResponse], error) {
	decision, err := s.epics.AnswerEpic(ctx, req.GetEpicId(), req.GetId(), req.GetText(), maker, time.Now())
	if err != nil {
		return nil, answerError(req, err)
	}
	open, err := s.epics.OpenQuestions(ctx, req.GetEpicId())
	if err != nil {
		return nil, epicStoreError(err)
	}
	reply := &v1.AnswerEpicResponse{Decision: decisionMessage(decision)}
	if len(open) > 0 {
		reply.Warning = noteWarning(open)
	}
	return connect.NewResponse(reply), nil
}

// answerError maps an error from recording the answer req names to its Connect error: an unknown epic is NotFound on
// epic_id, an id recorded with other text or another choice is AlreadyExists, an unknown question is NotFound on
// question, a replaced or closed question is FailedPrecondition, a choice the question does not offer is
// InvalidArgument on choice, other with blank text is InvalidArgument on text, and any other error goes through
// epicStoreError.
func answerError(req *v1.AnswerEpicRequest, err error) error {
	var replaced *store.QuestionReplacedError
	switch {
	case errors.Is(err, store.ErrNoEpic):
		return refuseEpic(connect.CodeNotFound, fmt.Sprintf("epic_id: no epic %s", req.GetEpicId()))
	case errors.Is(err, store.ErrDecisionConflict):
		return refuseEpic(connect.CodeAlreadyExists, fmt.Sprintf("id: %s was recorded with other text", req.GetId()))
	case errors.Is(err, store.ErrNoQuestion):
		return questionNotFound(req)
	case errors.As(err, &replaced):
		return refuseEpic(connect.CodeFailedPrecondition, fmt.Sprintf(
			"question: %s was replaced by %s", questionID(replaced.Seq), questionID(replaced.By),
		))
	case errors.Is(err, store.ErrQuestionClosed):
		return refuseEpic(connect.CodeFailedPrecondition, fmt.Sprintf("question: %s is closed", req.GetQuestion()))
	case errors.Is(err, store.ErrNoChoice):
		return refuseEpic(connect.CodeInvalidArgument, fmt.Sprintf(
			"choice: %s offers no choice %s", req.GetQuestion(), req.GetChoice(),
		))
	case errors.Is(err, store.ErrOtherNeedsText):
		return refuseEpic(connect.CodeInvalidArgument, "text: empty")
	default:
		return epicStoreError(err)
	}
}

// questionNotFound is the refusal of a question req names that its epic does not hold.
func questionNotFound(req *v1.AnswerEpicRequest) error {
	return refuseEpic(
		connect.CodeNotFound,
		fmt.Sprintf("question: no question %s on epic %s", req.GetQuestion(), req.GetEpicId()),
	)
}

// questionSeq returns n of the question Q<n> for a positive n, and false for any other text.
func questionSeq(question string) (int64, bool) {
	digits, ok := strings.CutPrefix(question, "Q")
	if !ok || digits == "" || digits[0] < '0' || digits[0] > '9' {
		return 0, false
	}
	seq, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || seq < 1 {
		return 0, false
	}
	return seq, true
}

// noteWarning returns the warning a note gets while questions are open: it names them in seq order.
func noteWarning(open []store.EpicQuestion) string {
	ids := epicMap(open, func(question store.EpicQuestion) string { return questionID(question.Seq) })
	return "recorded as a note: it answers no question, and " + strings.Join(ids, ", ") + " are open"
}

// checkEpicAnswer returns the refusal of an answer whose epic_id or id is empty, or whose choice does not fit its
// question: a note names no choice and needs text, and an answer to a question names one. The text of a note is empty
// after trimming white space. The store judges the choice against the question and other's text.
func checkEpicAnswer(req *v1.AnswerEpicRequest) error {
	switch {
	case req.GetEpicId() == "":
		return refuseEpic(connect.CodeInvalidArgument, "epic_id: empty")
	case req.GetId() == "":
		return refuseEpic(connect.CodeInvalidArgument, "id: empty")
	case req.GetQuestion() == "" && req.GetChoice() != "":
		return refuseEpic(connect.CodeInvalidArgument, "choice: a note answers no question")
	case req.GetQuestion() == "" && isBlank(req.GetText()):
		return refuseEpic(connect.CodeInvalidArgument, "text: empty")
	case req.GetQuestion() != "" && req.GetChoice() == "":
		return refuseEpic(connect.CodeInvalidArgument, "choice: empty")
	}
	return nil
}

// PauseEpic pauses the epic as its owner's, so the planner starts no turn for it and no reset resumes it, stops each
// running step of the epic, and returns the epic read back. The pause is written before the steps are stopped, so a
// held unit is not started again in between. An epic the owner paused, or paused with no recorded source, is refused
// and stops nothing; an epic a usage limit paused is taken over by the owner.
func (s *epicService) PauseEpic(
	ctx context.Context,
	req *connect.Request[v1.PauseEpicRequest],
) (*connect.Response[v1.PauseEpicResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	id := req.Msg.GetId()
	if id == "" {
		return nil, refuseEpic(connect.CodeInvalidArgument, "id: empty")
	}
	maker, err := s.controlMaker(ctx, id, req.Header())
	if err != nil {
		return nil, err
	}
	if err = s.epics.PauseEpic(ctx, id, maker, time.Now()); err != nil {
		return nil, epicControlError(id, err)
	}
	err = s.stopRunningSteps(ctx, id)
	if err != nil {
		return nil, err
	}
	message, err := s.storedEpic(ctx, id)
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.PauseEpicResponse{Epic: message}), nil
}

// ResumeEpic clears the pause of the epic when it is paused, resumes each limited step of the epic that has a task, and
// returns the epic read back with the steps resumed and the steps a full step cap left limited. An epic that is not
// paused and holds no such step is refused.
func (s *epicService) ResumeEpic(
	ctx context.Context,
	req *connect.Request[v1.ResumeEpicRequest],
) (*connect.Response[v1.ResumeEpicResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	id := req.Msg.GetId()
	if id == "" {
		return nil, refuseEpic(connect.CodeInvalidArgument, "id: empty")
	}
	head, err := s.epics.Epic(ctx, id)
	if errors.Is(err, store.ErrNoEpic) {
		return nil, refuseEpic(connect.CodeNotFound, fmt.Sprintf("id: no epic %s", id))
	}
	if err != nil {
		return nil, epicStoreError(err)
	}
	limited, err := s.limitedTaskSteps(ctx, id)
	if err != nil {
		return nil, err
	}
	maker, err := s.controlMaker(ctx, id, req.Header())
	if err != nil {
		return nil, err
	}
	if err = s.recordResume(ctx, id, head.Paused, len(limited) > 0, maker); err != nil {
		return nil, epicControlError(id, err)
	}
	var resumed, stillLimited []string
	for _, row := range limited {
		err = s.steps.Resume(ctx, row.ID)
		switch {
		case err == nil:
			resumed = append(resumed, row.ID)
		case errors.Is(err, ErrCapReached):
			stillLimited = append(stillLimited, row.ID)
		case errors.Is(err, ErrStepNotLimited), errors.Is(err, ErrStepNotRunning):
			// The step was resumed or ended after the list, so it is neither resumed nor still limited.
		default:
			return nil, epicStoreError(err)
		}
	}
	message, err := s.storedEpic(ctx, id)
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.ResumeEpicResponse{
		Epic:         message,
		Resumed:      resumed,
		StillLimited: stillLimited,
	}), nil
}

// recordResume writes the resume a ResumeEpic call makes on epic. A paused epic, or one with no limited step, goes
// through the store's ResumeEpic, which clears the pause or refuses an epic that is neither paused nor limited. An
// unpaused epic that holds limited steps has no pause to clear, so its resume is one resume action.
func (s *epicService) recordResume(ctx context.Context, epic string, paused, limited bool, maker store.Maker) error {
	if paused || !limited {
		if err := s.epics.ResumeEpic(ctx, epic, maker, time.Now()); err != nil {
			return fmt.Errorf("resume epic %s: %w", epic, err)
		}
		return nil
	}
	if err := s.epics.RecordEpicAction(ctx, epic, store.ActionResume, maker, time.Now()); err != nil {
		return fmt.Errorf("record resume of epic %s: %w", epic, err)
	}
	return nil
}

// stopRunningSteps stops each step of epic that is running, and returns the stops that failed, each named by its step,
// joined. A step a usage limit holds has no process running, so it is not stopped: it stays held until the epic is
// resumed, as ResumeEpic resumes it. Every other open step is tried, so one that will not stop does not keep the others
// running. Without a step store there are none.
func (s *epicService) stopRunningSteps(ctx context.Context, epic string) error {
	if s.steps == nil {
		return nil
	}
	rows, err := s.steps.List(ctx, epic, "")
	if err != nil {
		return epicStoreError(err)
	}
	var failed []error
	for _, row := range rows {
		if row.End != "" || row.LoopLimited {
			continue
		}
		if err = s.steps.Stop(ctx, row.ID); err != nil {
			failed = append(failed, fmt.Errorf("stop step %s: %w", row.ID, err))
		}
	}
	if len(failed) > 0 {
		return epicStoreError(errors.Join(failed...))
	}
	return nil
}

// limitedTaskSteps returns the open steps of epic that a usage limit holds and that have a task, oldest first. A
// planner turn's step has no task, so it is not among them. Without a step store there are none.
func (s *epicService) limitedTaskSteps(ctx context.Context, epic string) ([]store.StepRow, error) {
	if s.steps == nil {
		return nil, nil
	}
	rows, err := s.steps.List(ctx, epic, "")
	if err != nil {
		return nil, epicStoreError(err)
	}
	return slices.DeleteFunc(rows, func(row store.StepRow) bool {
		return row.End != "" || !row.LoopLimited || row.Task == ""
	}), nil
}

// StopEpic pauses the epic, flags its running turn to stop, stops the turn's step when the turn has one, and returns
// the epic read back. An epic with no running turn is refused, and stops nothing.
func (s *epicService) StopEpic(
	ctx context.Context,
	req *connect.Request[v1.StopEpicRequest],
) (*connect.Response[v1.StopEpicResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	id := req.Msg.GetId()
	if id == "" {
		return nil, refuseEpic(connect.CodeInvalidArgument, "id: empty")
	}
	maker, err := s.controlMaker(ctx, id, req.Header())
	if err != nil {
		return nil, err
	}
	step, err := s.epics.StopEpic(ctx, id, maker, time.Now())
	if err != nil {
		return nil, epicControlError(id, err)
	}
	if step != "" && s.steps != nil {
		if err = s.steps.Stop(ctx, step); err != nil {
			return nil, epicStoreError(err)
		}
	}
	message, err := s.storedEpic(ctx, id)
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.StopEpicResponse{Epic: message}), nil
}

// RetryEpic ends the park of the epic and returns the epic read back. An epic that is not parked is refused. When the
// request names a node, the node reruns under its next attempt instead, and the epic is unparked with it: its parked
// runs, or its latest integrate run when that run ended failed (store.Store.RetryNode).
func (s *epicService) RetryEpic(
	ctx context.Context,
	req *connect.Request[v1.RetryEpicRequest],
) (*connect.Response[v1.RetryEpicResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	id := req.Msg.GetId()
	if id == "" {
		return nil, refuseEpic(connect.CodeInvalidArgument, "id: empty")
	}
	maker, err := s.controlMaker(ctx, id, req.Header())
	if err != nil {
		return nil, err
	}
	if err = s.retryPark(ctx, id, req.Msg.GetNode(), maker); err != nil {
		return nil, epicControlError(id, err)
	}
	message, err := s.storedEpic(ctx, id)
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.RetryEpicResponse{Epic: message}), nil
}

// KickEpic records the owner's text on the epic as a decision and wakes its planner with the text at the epic's next
// plan run, and returns the epic read back. An empty id, and text that is blank, are refused before the store is asked.
// The store refuses an unknown epic, a paused or parked epic, and an ended one, as the other controls do.
func (s *epicService) KickEpic(
	ctx context.Context,
	req *connect.Request[v1.KickEpicRequest],
) (*connect.Response[v1.KickEpicResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	id := req.Msg.GetId()
	if id == "" {
		return nil, refuseEpic(connect.CodeInvalidArgument, "id: empty")
	}
	if isBlank(req.Msg.GetText()) {
		return nil, refuseEpic(connect.CodeInvalidArgument, "text: empty")
	}
	maker, err := s.controlMaker(ctx, id, req.Header())
	if err != nil {
		return nil, err
	}
	if err = s.epics.KickEpic(ctx, id, req.Msg.GetText(), maker, time.Now()); err != nil {
		return nil, epicControlError(id, err)
	}
	message, err := s.storedEpic(ctx, id)
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.KickEpicResponse{Epic: message}), nil
}

// ReleaseEpic releases the epic. It resumes the stopped release at its first unfinished step, running again the
// undo-none release steps undo_none names. An epic that ended with gaps is released on the owner's word instead, and
// the store refuses a non-empty undo_none for it. It returns the epic read back. An empty id, and a position in
// undo_none below 1, are refused before the store is asked.
func (s *epicService) ReleaseEpic(
	ctx context.Context,
	req *connect.Request[v1.ReleaseEpicRequest],
) (*connect.Response[v1.ReleaseEpicResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	id := req.Msg.GetId()
	if id == "" {
		return nil, refuseEpic(connect.CodeInvalidArgument, "id: empty")
	}
	undoNone := make([]int64, 0, len(req.Msg.GetUndoNone()))
	for _, position := range req.Msg.GetUndoNone() {
		if position < 1 {
			return nil, refuseEpic(
				connect.CodeInvalidArgument, fmt.Sprintf("undo_none: position %d is below 1", position),
			)
		}
		undoNone = append(undoNone, int64(position))
	}
	maker, err := s.controlMaker(ctx, id, req.Header())
	if err != nil {
		return nil, err
	}
	if err = s.epics.ReleaseEpic(ctx, id, undoNone, maker, time.Now()); err != nil {
		return nil, epicControlError(id, err)
	}
	message, err := s.storedEpic(ctx, id)
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.ReleaseEpicResponse{Epic: message}), nil
}

// CloseEpic closes the epic as the kind its request names, superseded, shipped or abandoned, and returns the epic read
// back. superseded names by, the epic that replaces it. The closer is the planner when the call comes from the step of
// the epic's running turn, which is the session the planner's own calls run in, and the owner otherwise. After the close
// is stored, the turn's step and each running step of the epic are stopped. An empty id is refused, an unknown epic is
// NotFound, and a close the store refuses is FailedPrecondition with its text, and stops nothing.
func (s *epicService) CloseEpic(
	ctx context.Context,
	req *connect.Request[v1.CloseEpicRequest],
) (*connect.Response[v1.CloseEpicResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	id := req.Msg.GetId()
	if id == "" {
		return nil, refuseEpic(connect.CodeInvalidArgument, "id: empty")
	}
	closer, err := s.closerOf(ctx, id, req.Header())
	if err != nil {
		return nil, err
	}
	maker, err := s.controlMaker(ctx, id, req.Header())
	if err != nil {
		return nil, err
	}
	step, err := s.epics.CloseEpic(ctx, id, req.Msg.GetKind(), req.Msg.GetBy(), closer, maker, time.Now())
	if err != nil {
		return nil, epicControlError(id, err)
	}
	err = s.stopClosedSteps(ctx, id, step)
	if err != nil {
		return nil, err
	}
	message, err := s.storedEpic(ctx, id)
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.CloseEpicResponse{Epic: message}), nil
}

// closerOf returns who closes epic for a call whose headers are header: the planner when the session the call runs in is
// the step of epic's running turn, and the owner otherwise. An unknown epic is NotFound.
func (s *epicService) closerOf(ctx context.Context, epic string, header http.Header) (string, error) {
	session := header.Get(protocol.HeaderSession)
	if session == "" {
		return store.CloserOwner, nil
	}
	row, err := s.epics.Epic(ctx, epic)
	if errors.Is(err, store.ErrNoEpic) {
		return "", refuseEpic(connect.CodeNotFound, fmt.Sprintf("id: no epic %s", epic))
	}
	if err != nil {
		return "", epicStoreError(err)
	}
	for _, turn := range row.Turns {
		if turn.State == "running" && turn.Step == session {
			return store.CloserPlanner, nil
		}
	}
	return store.CloserOwner, nil
}

// stopClosedSteps stops turnStep, the step of the turn a close ended, and each running step of epic, each once. A step
// that has ended stops as a no-op, so a step listed as running that ends first is not an error.
func (s *epicService) stopClosedSteps(ctx context.Context, epic, turnStep string) error {
	if s.steps == nil {
		return nil
	}
	rows, err := s.steps.List(ctx, epic, "")
	if err != nil {
		return epicStoreError(err)
	}
	var stop []string
	if turnStep != "" {
		stop = append(stop, turnStep)
	}
	for _, row := range rows {
		if row.End == "" && !slices.Contains(stop, row.ID) {
			stop = append(stop, row.ID)
		}
	}
	for _, id := range stop {
		if err = s.steps.Stop(ctx, id); err != nil {
			return epicStoreError(err)
		}
	}
	return nil
}

// SeeEpic stores the time of this call as the seen mark of the epic, replacing the earlier mark, and answers an empty
// reply. An empty id is refused, and an unknown epic is NotFound, as PauseEpic refuses them.
func (s *epicService) SeeEpic(
	ctx context.Context,
	req *connect.Request[v1.SeeEpicRequest],
) (*connect.Response[v1.SeeEpicResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	id := req.Msg.GetId()
	if id == "" {
		return nil, refuseEpic(connect.CodeInvalidArgument, "id: empty")
	}
	if err := s.epics.SeeEpic(ctx, id, time.Now()); err != nil {
		return nil, epicControlError(id, err)
	}
	return connect.NewResponse(&v1.SeeEpicResponse{}), nil
}

// ArchiveRow stores the archive mark of the list row of kind and id at the time of this call, and answers an empty
// reply. A kind other than epic, session or headless, and an empty id, are refused as the store refuses them.
func (s *epicService) ArchiveRow(
	ctx context.Context,
	req *connect.Request[v1.ArchiveRowRequest],
) (*connect.Response[v1.ArchiveRowResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	kind := req.Msg.GetKind()
	if err := s.epics.ArchiveRow(ctx, kind, req.Msg.GetId(), time.Now()); err != nil {
		return nil, rowError(kind, err)
	}
	return connect.NewResponse(&v1.ArchiveRowResponse{}), nil
}

// UnarchiveRow stores the unarchive time of the list row of kind and id at the time of this call, which clears its
// archive mark, and answers an empty reply. The refusals are those of ArchiveRow.
func (s *epicService) UnarchiveRow(
	ctx context.Context,
	req *connect.Request[v1.UnarchiveRowRequest],
) (*connect.Response[v1.UnarchiveRowResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	kind := req.Msg.GetKind()
	if err := s.epics.UnarchiveRow(ctx, kind, req.Msg.GetId(), time.Now()); err != nil {
		return nil, rowError(kind, err)
	}
	return connect.NewResponse(&v1.UnarchiveRowResponse{}), nil
}

// rowError maps an error from archiving or unarchiving a list row of kind to its Connect error: a kind the store does
// not hold is InvalidArgument on kind, an empty id is InvalidArgument on id, and any other error goes through
// epicStoreError.
func rowError(kind string, err error) error {
	switch {
	case errors.Is(err, store.ErrBadRowKind):
		return refuseEpic(connect.CodeInvalidArgument, fmt.Sprintf("kind: %q is not epic, session or headless", kind))
	case errors.Is(err, store.ErrEmptyRowID):
		return refuseEpic(connect.CodeInvalidArgument, "id: empty")
	default:
		return epicStoreError(err)
	}
}

// retryPark ends the park of epic when node is empty, and otherwise reruns node: its parked runs, or its latest integrate
// run when that run ended failed. maker made the call, and the store records the retry as made at the time now. The
// store refuses a node with nothing to rerun, which a node of an unknown epic also is, so the epic is read first to
// make an unknown epic ErrNoEpic.
func (s *epicService) retryPark(ctx context.Context, epic, node string, maker store.Maker) error {
	if node == "" {
		if err := s.epics.RetryEpic(ctx, epic, maker, time.Now()); err != nil {
			return fmt.Errorf("retry epic %s: %w", epic, err)
		}
		return nil
	}
	if _, err := s.epics.Epic(ctx, epic); err != nil {
		return fmt.Errorf("read epic %s: %w", epic, err)
	}
	if err := s.epics.RetryNode(ctx, epic, node, maker, time.Now()); err != nil {
		return fmt.Errorf("retry node %s of epic %s: %w", node, epic, err)
	}
	return nil
}

// epicControlError maps an error from an epic control on id to its Connect error: an unknown epic is NotFound, a
// refusal is FailedPrecondition with its text, and any other error goes through epicStoreError.
func epicControlError(id string, err error) error {
	var refusal store.RefusalError
	switch {
	case errors.Is(err, store.ErrNoEpic):
		return refuseEpic(connect.CodeNotFound, fmt.Sprintf("id: no epic %s", id))
	case errors.As(err, &refusal):
		return refuseEpic(connect.CodeFailedPrecondition, refusal.Error())
	default:
		return epicStoreError(err)
	}
}

// refuseEpic returns a Connect error with code whose message is message.
func refuseEpic(code connect.Code, message string) error {
	return errors.Join(connect.NewError(code, errors.New(message)))
}

// epicStoreError maps an error from the epic store to an internal Connect error. The error's text is the message.
func epicStoreError(err error) error {
	return errors.Join(connect.NewError(connect.CodeInternal, err))
}

// taskReviewNames returns the names of the reviewers that run at the task stage, in the order of rs: the per-task
// review list a task's reviews run through.
func taskReviewNames(rs []reviewers.Reviewer) []string {
	return epicMap(reviewers.ForStage(rs, reviewers.StageTask), func(rev reviewers.Reviewer) string { return rev.Name })
}

// epicRowOf returns the store row of contract, based at base, pinned to workflow and accepted at now, with decisions as
// its seeds. Its reviewers and review list are the caller's to set.
func epicRowOf(
	contract *v1.EpicContract,
	decisions []*v1.EpicDecisionInput,
	base string,
	workflow store.EpicWorkflow,
	now time.Time,
) store.EpicRow {
	return store.EpicRow{
		ID:          contract.GetId(),
		Repository:  contract.GetRepository(),
		Base:        base,
		Workflow:    workflow,
		Title:       contract.GetTitle(),
		Approved:    contract.GetApproved(),
		Intent:      contract.GetIntent(),
		Care:        contract.GetCare(),
		Approach:    contract.GetApproach(),
		ReleaseThen: contract.GetReleaseThen(),
		Checks:      contract.GetChecks(),
		Phase:       epicPhaseAccepted,
		Created:     now,
		Changed:     now,
		Failures: epicMap(contract.GetFailures(), func(f *v1.EpicFailure) store.EpicFailure {
			return store.EpicFailure{ID: f.GetId(), Text: f.GetText(), Rating: f.GetRating(), Response: f.GetResponse()}
		}),
		Additions: epicMap(contract.GetAdditions(), func(a *v1.EpicAddition) store.EpicAddition {
			return store.EpicAddition{Location: a.GetLocation(), What: a.GetWhat(), Why: a.GetWhy(), Cost: a.GetCost()}
		}),
		Requirements: epicMap(contract.GetRequirements(), func(r *v1.EpicRequirement) store.EpicRequirement {
			return store.EpicRequirement{
				ID: r.GetId(), Text: r.GetText(), Evidence: r.GetEvidence(), Kind: r.GetKind(),
				Item: evidenceItemOf(r.GetItem()),
			}
		}),
		Gates:     epicMap(contract.GetGates(), epicGateOf),
		DoneGates: contract.GetDoneGates(),
		Premises: epicMap(contract.GetPremises(), func(p *v1.EpicPremise) store.EpicPremise {
			return store.EpicPremise{ID: p.GetId(), Text: p.GetText(), IfWrong: p.GetIfWrong()}
		}),
		Exclusions: contract.GetExclusions(),
		Alternatives: epicMap(contract.GetAlternatives(), func(a *v1.EpicAlternative) store.EpicAlternative {
			return store.EpicAlternative{
				Alternative: a.GetAlternative(), WhyNot: a.GetWhyNot(), Reconsider: a.GetReconsider(),
			}
		}),
		ReleaseSteps: epicMap(contract.GetReleaseSteps(), func(s *v1.EpicReleaseStep) store.EpicReleaseStep {
			return store.EpicReleaseStep{Step: s.GetStep(), Target: s.GetTarget(), Undo: s.GetUndo()}
		}),
		Decisions: epicMap(decisions, func(d *v1.EpicDecisionInput) store.EpicDecision {
			return store.EpicDecision{ID: d.GetId(), Text: d.GetText()}
		}),
	}
}

// epicGateOf returns the store form of gate: its argv, its limit in seconds, its needs in order, and its agent with the
// must and must not it asks.
func epicGateOf(gate *v1.EpicGate) store.EpicGate {
	return store.EpicGate{
		Name:  gate.GetName(),
		Argv:  gate.GetArgv(),
		Limit: gate.GetLimit(),
		Needs: epicMap(gate.GetNeeds(), func(n *v1.EpicGateNeed) store.EpicGateNeed {
			return store.EpicGateNeed{Pool: n.GetPool(), Count: n.GetCount()}
		}),
		Agent:   gate.GetAgent(),
		Must:    gate.GetMust(),
		MustNot: gate.GetMustNot(),
	}
}

// storedEpicMessage returns the StoredEpic message of row: its contract, summary, phase, times, decisions in order, its
// merged reviewers in order, its steerer, its paused state and pause source, its parked state with its turns, tasks and
// questions in order, its branch state, its pinned workflow, its phase time, its seen mark, its report, its evidence
// amendments in order and the epic its close named as its replacement, as stored, with no kind filter. It fails when a
// task's messages fail.
func storedEpicMessage(row store.EpicRow) (*v1.StoredEpic, error) {
	tasks, err := taskMessages(row.Tasks)
	if err != nil {
		return nil, err
	}
	return &v1.StoredEpic{
		Contract:     contractMessage(row),
		Summary:      row.Summary,
		Phase:        row.Phase,
		Created:      row.Created.Unix(),
		Changed:      row.Changed.Unix(),
		PhaseAt:      row.PhaseAt.Unix(),
		Seen:         unixOrZero(row.Seen),
		Revision:     row.Revision,
		Decisions:    epicMap(row.Decisions, decisionMessage),
		Reviewers:    epicMap(row.Reviewers, epicReviewerMessage),
		Steerer:      row.Steerer,
		Paused:       row.Paused,
		PauseSource:  row.PauseSource,
		Parked:       row.Parked,
		ParkClass:    string(row.ParkClass),
		Turns:        epicMap(row.Turns, turnMessage),
		Tasks:        tasks,
		Questions:    epicMap(row.Questions, questionMessage),
		Branch:       row.Branch,
		Workflow:     workflowMessage(row.Workflow),
		Report:       reportMessage(row.Report),
		Amendments:   epicMap(row.Amendments, amendmentMessage),
		SupersededBy: row.SupersededBy,
	}, nil
}

// reportMessage returns the EpicReport message of report: its outcome the phase it reports, its shipped and not shipped
// Requirement ids in order, and why. It is nil while report has no phase, which is the state before a report is stored.
func reportMessage(report store.EpicReport) *v1.EpicReport {
	if report.Phase == "" {
		return nil
	}
	return &v1.EpicReport{
		Outcome: report.Phase, Shipped: report.Shipped, NotShipped: report.NotShipped, Why: report.Why,
	}
}

// workflowMessage returns the EpicWorkflow message of w: its name, its canonical text and its sha256.
func workflowMessage(w store.EpicWorkflow) *v1.EpicWorkflow {
	return &v1.EpicWorkflow{Name: w.Name, Text: w.Text, Sha256: w.SHA256}
}

// triggerMessage returns the EpicTrigger message of trigger: its id, cause, state, claiming turn and creation time.
func triggerMessage(trigger store.EpicTrigger) *v1.EpicTrigger {
	return &v1.EpicTrigger{
		Id: trigger.ID, Cause: trigger.Cause, State: trigger.State, Turn: trigger.Turn, Created: trigger.Created.Unix(),
	}
}

// tellMessage returns the EpicTell message of tell: its seq, text, when it was written, and whether it was told.
func tellMessage(tell store.EpicTell) *v1.EpicTell {
	return &v1.EpicTell{Seq: tell.Seq, Text: tell.Text, At: tell.At.Unix(), Told: tell.Told}
}

// actionMessage returns the EpicAction message of action: its seq, kind, when it was taken, who made it, the session that
// made it, and the sessions a move took the epic from and to.
func actionMessage(action store.EpicAction) *v1.EpicAction {
	return &v1.EpicAction{
		Seq: action.Seq, Kind: action.Kind, Time: action.At.Unix(), By: action.Maker.By,
		Session: action.Maker.Session, From: action.From, To: action.To,
	}
}

// reviewLedgerMessage returns the EpicLedger message of review: its verdicts and its ledger findings by position, and
// its correction rounds by round. A review that has not started has an empty ledger.
func reviewLedgerMessage(review store.Review) *v1.EpicLedger {
	return &v1.EpicLedger{
		Verdicts: epicMap(review.Verdicts, reviewVerdictMessage),
		Findings: epicMap(review.Ledger, reviewFindingMessage),
		Rounds:   epicMap(review.Rounds, reviewRoundMessage),
	}
}

// reviewVerdictMessage returns the EpicReviewVerdict message of v.
func reviewVerdictMessage(v store.ReviewVerdict) *v1.EpicReviewVerdict {
	return &v1.EpicReviewVerdict{
		Position: v.Position, Reviewer: v.Reviewer, Step: v.Step, Verdict: v.Verdict, Reason: v.Reason,
	}
}

// reviewFindingMessage returns the EpicLedgerFinding message of f, with its closures by round.
func reviewFindingMessage(f store.LedgerFinding) *v1.EpicLedgerFinding {
	return &v1.EpicLedgerFinding{
		Id: f.ID, Position: f.Position, Reviewer: f.Reviewer, Finding: f.Finding, Claim: f.Claim, Citation: f.Citation,
		Location: f.Location, Evidence: f.Evidence, VerifyBy: f.VerifyBy, Step: f.Step, Result: f.Result,
		Unverified: f.Unverified, Reason: f.Reason, Closures: epicMap(f.Closures, reviewClosureMessage),
	}
}

// reviewClosureMessage returns the EpicClosure message of c.
func reviewClosureMessage(c store.Closure) *v1.EpicClosure {
	return &v1.EpicClosure{Round: c.Round, Step: c.Step, Result: c.Result, Reason: c.Reason}
}

// reviewRoundMessage returns the EpicReviewRound message of r, with its correction tasks in seq order.
func reviewRoundMessage(r store.ReviewRound) *v1.EpicReviewRound {
	return &v1.EpicReviewRound{Round: r.Round, Revision: r.Revision, Tasks: r.Tasks, Outcome: r.Outcome}
}

// The kinds whose node runs the nodes view reads rows beneath, and the parts a node run's work ids are derived from.
// They are the names the kinds and the store record, so a row is found under the id its run wrote it.
const (
	kindPlan     = "plan"
	kindBuild    = "build"
	kindGate     = "gate"
	kindCost     = "cost"
	kindCommand  = "command"
	kindEvidence = "evidence"
	kindAgent    = "agent"
	checkPart    = "check"
	stepPart     = "step"
)

// nodeMessages returns the EpicNode messages of the node runs of flow, ordered by the position of each run's node in the
// preorder of the workflow the epic runs, then by path. A run whose node that workflow does not hold is still shown,
// after every node it holds, so the read never hides a stored run.
func nodeMessages(flow store.EpicFlow) ([]*v1.EpicNode, error) {
	positions, err := workflowPositions(flow.Epic.Workflow)
	if err != nil {
		return nil, err
	}
	position := func(node string) int {
		if at, ok := positions[node]; ok {
			return at
		}
		return len(positions)
	}
	runs := slices.Clone(flow.Runs)
	slices.SortFunc(runs, func(a, b store.NodeRun) int {
		return cmp.Or(cmp.Compare(position(a.Node), position(b.Node)), strings.Compare(a.Path, b.Path))
	})
	out := make([]*v1.EpicNode, 0, len(runs))
	var message *v1.EpicNode
	for _, run := range runs {
		message, err = epicNodeMessage(flow, run)
		if err != nil {
			return nil, err
		}
		out = append(out, message)
	}
	return out, nil
}

// workflowPositions returns the position of each node id in the preorder of the workflow an epic runs: the workflow it
// pinned, or the built-in epic when it pins no text, as the interpreter runs it.
func workflowPositions(pin store.EpicWorkflow) (map[string]int, error) {
	nodes := pin.Nodes
	if pin.Text == "" {
		// No repository file is read, so the built-in epic is the workflow that loads.
		built, err := workflow.Load(defaultWorkflowName, func(string) ([]byte, bool, error) { return nil, false, nil })
		if err != nil {
			return nil, fmt.Errorf("workflow %s: %w", defaultWorkflowName, err)
		}
		nodes = epicWorkflowOf(built).Nodes
	}
	positions := make(map[string]int, len(nodes))
	for i, node := range nodes {
		positions[node.ID] = i
	}
	return positions, nil
}

// epicNodeMessage returns the EpicNode message of run. The rows beneath it are read from flow: a plan's turns and tasks
// above its mark, a build's task, an evidence node's runs of its attempt, a gate's or command's check and an agent's
// step, each only when the store holds it.
func epicNodeMessage(flow store.EpicFlow, run store.NodeRun) (*v1.EpicNode, error) {
	message := &v1.EpicNode{
		Path: run.Path, Node: run.Node, Kind: run.Kind, State: run.State, Outcome: run.Outcome,
		Round: run.Round, LoopRound: run.LoopRound, Attempt: run.Attempt, Output: run.Output, Revision: run.Revision,
	}
	switch run.Kind {
	case kindPlan:
		message.Turns, message.Tasks = planRows(flow.Epic, run.Mark)
	case kindBuild:
		message.Tasks = []int64{run.Task}
	case kindEvidence:
		message.Runs = evidenceRunIDs(flow, run)
	case kindGate, kindCost, kindCommand:
		check, found := storedCheckOf(flow, run)
		if !found {
			break
		}
		msg, err := nodeCheckMessage(check)
		if err != nil {
			return nil, err
		}
		message.Check = msg
	case kindAgent:
		id := store.NodeWorkID(run.ID, run.Attempt, stepPart)
		if slices.ContainsFunc(flow.Steps, func(step store.StepRow) bool { return step.ID == id }) {
			message.Step = id
		}
	}
	return message, nil
}

// planRows returns the turn seqs above mark and the task seqs of those turns, each in order. A task belongs to the turn
// it was planned under.
func planRows(epic store.EpicRow, mark int64) ([]int64, []int64) {
	var turns, tasks []int64
	for _, turn := range epic.Turns {
		if turn.Seq > mark {
			turns = append(turns, turn.Seq)
		}
	}
	for _, task := range epic.Tasks {
		if task.Turn > mark {
			tasks = append(tasks, task.Seq)
		}
	}
	return turns, tasks
}

// evidenceRunIDs returns, for run's attempt, the id of the evidence run that decides each Requirement, in the contract's
// order: the first run, or the rerun a store.EnvironmentExit started, as kinds.EvidenceRunID walks them. A Requirement
// with no stored run at that attempt is left out.
func evidenceRunIDs(flow store.EpicFlow, run store.NodeRun) []string {
	var ids []string
	for _, requirement := range flow.Epic.Requirements {
		id := kinds.EvidenceRunID(run, flow.Evidence, requirement.ID)
		if slices.ContainsFunc(flow.Evidence, func(stored store.EvidenceRun) bool { return stored.ID == id }) {
			ids = append(ids, id)
		}
	}
	return ids
}

// storedCheckOf returns the check stored under run's attempt and whether one is stored.
func storedCheckOf(flow store.EpicFlow, run store.NodeRun) (store.NodeCheck, bool) {
	id := store.NodeWorkID(run.ID, run.Attempt, checkPart)
	i := slices.IndexFunc(flow.Checks, func(check store.NodeCheck) bool { return check.ID == id })
	if i < 0 {
		return store.NodeCheck{}, false
	}
	return flow.Checks[i], true
}

// nodeCheckMessage returns the EpicNodeCheck message of check: its state, how it ended, and its output.
func nodeCheckMessage(check store.NodeCheck) (*v1.EpicNodeCheck, error) {
	exit, err := int32Of("exit", check.Exit)
	if err != nil {
		return nil, fmt.Errorf("check %s: %w", check.ID, err)
	}
	return &v1.EpicNodeCheck{
		State: check.State, Exit: exit, TimedOut: check.TimedOut, NotRun: check.NotRun,
		Stdout: check.Stdout, Stderr: check.Stderr,
	}, nil
}

// contractMessage returns the EpicContract message of row, every section in order. Its review gates and reviewers are
// left empty: the stored epic carries its reviewers, and its review list is derived from them.
func contractMessage(row store.EpicRow) *v1.EpicContract {
	return &v1.EpicContract{
		Id:         row.ID,
		Repository: row.Repository,
		Base:       row.Base,
		Title:      row.Title,
		Approved:   row.Approved,
		Intent:     row.Intent,
		Failures: epicMap(row.Failures, func(f store.EpicFailure) *v1.EpicFailure {
			return &v1.EpicFailure{Id: f.ID, Text: f.Text, Rating: f.Rating, Response: f.Response}
		}),
		Care: row.Care,
		Additions: epicMap(row.Additions, func(a store.EpicAddition) *v1.EpicAddition {
			return &v1.EpicAddition{Location: a.Location, What: a.What, Why: a.Why, Cost: a.Cost}
		}),
		Requirements: epicMap(row.Requirements, func(r store.EpicRequirement) *v1.EpicRequirement {
			return &v1.EpicRequirement{
				Id: r.ID, Text: r.Text, Evidence: r.Evidence, Kind: r.Kind, Item: requirementItemMessage(r.Item),
			}
		}),
		Gates:     epicMap(row.Gates, epicGateMessage),
		DoneGates: row.DoneGates,
		Premises: epicMap(row.Premises, func(p store.EpicPremise) *v1.EpicPremise {
			return &v1.EpicPremise{Id: p.ID, Text: p.Text, IfWrong: p.IfWrong}
		}),
		Exclusions: row.Exclusions,
		Approach:   row.Approach,
		Alternatives: epicMap(row.Alternatives, func(a store.EpicAlternative) *v1.EpicAlternative {
			return &v1.EpicAlternative{Alternative: a.Alternative, WhyNot: a.WhyNot, Reconsider: a.Reconsider}
		}),
		ReleaseSteps: epicMap(row.ReleaseSteps, func(s store.EpicReleaseStep) *v1.EpicReleaseStep {
			return &v1.EpicReleaseStep{Step: s.Step, Target: s.Target, Undo: s.Undo}
		}),
		ReleaseThen: row.ReleaseThen,
		Checks:      row.Checks,
	}
}

// decisionMessage returns the EpicDecision message of d, its question as Q<n> when it answers one, empty for a note.
func decisionMessage(d store.EpicDecision) *v1.EpicDecision {
	return &v1.EpicDecision{
		Seq: d.Seq, Id: d.ID, Text: d.Text, Time: d.Time.Unix(), Question: questionRef(d.Question),
		By: d.Maker.By, Session: d.Maker.Session,
	}
}

// turnMessage returns the EpicTurn message of turn. Ended is 0 while the turn runs.
func turnMessage(turn store.EpicTurn) *v1.EpicTurn {
	return &v1.EpicTurn{
		Seq:       turn.Seq,
		Step:      turn.Step,
		State:     turn.State,
		Reason:    turn.Reason,
		Outline:   turn.Outline,
		Outcome:   turn.Outcome,
		Decisions: turn.Decisions,
		Started:   turn.Started.Unix(),
		Ended:     unixOrZero(turn.Ended),
	}
}

// releaseRunMessage returns the EpicReleaseRun message of run, its time in unix seconds.
func releaseRunMessage(run store.ReleaseRun) *v1.EpicReleaseRun {
	return &v1.EpicReleaseRun{
		Part: run.Part, Position: run.Position, Attempt: run.Attempt, Step: run.Step, Start: run.Start, Head: run.Head,
		End: run.End, Commits: run.Commits, Evidence: run.Evidence, Passed: run.Passed, Reason: run.Reason,
		At: run.At.Unix(),
	}
}

// releaseProofMessage returns the EpicReleaseProof message of proof, its time in unix seconds, or 0 when it is the zero
// time.
func releaseProofMessage(proof store.ReleaseProof) *v1.EpicReleaseProof {
	return &v1.EpicReleaseProof{
		Position: proof.Position, Step: proof.Step, Target: proof.Target, Undo: proof.Undo, State: proof.State,
		Evidence: proof.Evidence, Reason: proof.Reason, Attempt: proof.Attempt, At: unixOrZero(proof.At),
	}
}

// taskMessages returns the EpicTask messages of tasks, in order. It fails when a task's messages fail.
func taskMessages(tasks []store.EpicTask) ([]*v1.EpicTask, error) {
	out := make([]*v1.EpicTask, 0, len(tasks))
	for _, task := range tasks {
		msg, err := taskMessage(task)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

// taskMessage returns the EpicTask message of task, its id T<seq>: its status, its build, its reviews in order, the
// evidence runs its build names as red and green, its current version and the source that wrote it, the versions
// stored after the planner's, and its ladder rungs in rung order. It fails when one of those runs fails to convert.
func taskMessage(task store.EpicTask) (*v1.EpicTask, error) {
	message := &v1.EpicTask{
		Id:           fmt.Sprintf("T%d", task.Seq),
		Turn:         task.Turn,
		Title:        task.Title,
		Deliverable:  task.Deliverable,
		Files:        task.Files,
		Requirements: task.Requirements,
		Contributes:  task.Contributes,
		TestChanges:  task.TestChanges,
		Gate:         task.Gate,
		Evidence:     evidenceItemMessage(task.Evidence),
		Status:       task.Status,
		Stage:        task.Build.Stage,
		Attempt:      task.Build.Attempt,
		Step:         task.Build.Step,
		Base:         task.Build.Base,
		Revision:     task.Build.Revision,
		Failure:      task.Build.Failure,
		Guard:        task.Build.Guard,
		Review:       task.Build.Review,
		Held:         task.Build.Held,
		Reviews:      epicMap(task.Reviews, taskReviewMessage),
		Version:      task.Version,
		Versions:     epicMap(task.Versions, taskVersionMessage),
		Source:       task.Source(),
		Rungs:        epicMap(task.Rungs, taskRungMessage),
	}
	if task.Red != nil {
		red, err := evidenceServiceRunMessage(*task.Red)
		if err != nil {
			return nil, fmt.Errorf("task %d red run: %w", task.Seq, err)
		}
		message.Red = red
	}
	if task.Green != nil {
		green, err := evidenceServiceRunMessage(*task.Green)
		if err != nil {
			return nil, fmt.Errorf("task %d green run: %w", task.Seq, err)
		}
		message.Green = green
	}
	return message, nil
}

// taskVersionMessage returns the EpicTaskVersion message of version: its source, decision, step, reason, state and
// red run, its refusal, the fields it changes in order and the values it sets, and its times. A field it does not
// change is empty.
func taskVersionMessage(version store.TaskVersion) *v1.EpicTaskVersion {
	message := &v1.EpicTaskVersion{
		Version:  version.Version,
		Source:   version.Source,
		Decision: version.Decision,
		Step:     version.Step,
		Reason:   version.Reason,
		State:    version.State,
		Changed:  version.Changed(),
		RedRun:   version.RedRun,
		Refusal:  version.Refusal,
		Created:  version.Created.Unix(),
		Settled:  unixOrZero(version.Settled),
	}
	if version.Deliverable != nil {
		message.Deliverable = *version.Deliverable
	}
	if version.Files != nil {
		message.Files = *version.Files
	}
	if version.TestChanges != nil {
		message.TestChanges = *version.TestChanges
	}
	if version.Gate != nil {
		message.Gate = *version.Gate
	}
	if version.Evidence != nil {
		message.Evidence = evidenceItemMessage(*version.Evidence)
	}
	return message
}

// taskRungMessage returns the EpicTaskRung message of rung: its number, kind, the failure that moved the task onto it,
// the review gate and claim of that finding, its step, when it was written, and its outcome, reason and end time.
func taskRungMessage(rung store.TaskRung) *v1.EpicTaskRung {
	return &v1.EpicTaskRung{
		Rung:      rung.Rung,
		Kind:      rung.Kind,
		MovedBy:   rung.MovedBy,
		Cause:     rung.Cause,
		Gate:      rung.Gate,
		Assertion: rung.Assertion,
		Step:      rung.Step,
		Created:   rung.Created.Unix(),
		Outcome:   rung.Outcome,
		Reason:    rung.Reason,
		Ended:     unixOrZero(rung.Ended),
	}
}

// taskReviewMessage returns the EpicTaskReview message of review: its attempt, gate, step, verdict, findings in order,
// and the reason an undetermined review gave.
func taskReviewMessage(review store.TaskReview) *v1.EpicTaskReview {
	return &v1.EpicTaskReview{
		Attempt:  review.Attempt,
		Gate:     review.Gate,
		Step:     review.Step,
		Verdict:  review.Verdict,
		Findings: epicMap(review.Findings, taskFindingMessage),
		Reason:   review.Reason,
	}
}

// taskFindingMessage returns the EpicTaskFinding message of finding: its id, claim, location, verification, and whether
// it was settled as confirmed without one.
func taskFindingMessage(finding store.TaskFinding) *v1.EpicTaskFinding {
	return &v1.EpicTaskFinding{
		Id:         finding.ID,
		Claim:      finding.Claim,
		Location:   finding.Location,
		Step:       finding.Step,
		Result:     finding.Result,
		Reason:     finding.Reason,
		Unverified: finding.Unverified,
	}
}

// evidenceItemMessage returns the EvidenceItem message of item.
func evidenceItemMessage(item evidence.Item) *v1.EvidenceItem {
	return &v1.EvidenceItem{
		Capture:    item.Capture,
		Limit:      item.Limit,
		Artifacts:  item.Artifacts,
		Supplied:   item.Supplied,
		Conclusion: item.Conclusion,
		Judge:      item.Judge,
		Kind:       string(item.Kind),
		Tests:      item.Tests,
	}
}

// requirementItemMessage returns the EvidenceItem message of a requirement's item. The zero Item, which an epic stored
// before O3d2 keeps, has no message.
func requirementItemMessage(item evidence.Item) *v1.EvidenceItem {
	if reflect.ValueOf(item).IsZero() {
		return nil
	}
	return evidenceItemMessage(item)
}

// epicGateMessage returns the EpicGate message of gate: its argv, its limit in seconds, its needs in order, and its
// agent with the must and must not it asks.
func epicGateMessage(gate store.EpicGate) *v1.EpicGate {
	return &v1.EpicGate{
		Name:  gate.Name,
		Argv:  gate.Argv,
		Limit: gate.Limit,
		Needs: epicMap(gate.Needs, func(n store.EpicGateNeed) *v1.EpicGateNeed {
			return &v1.EpicGateNeed{Pool: n.Pool, Count: n.Count}
		}),
		Agent:   gate.Agent,
		Must:    gate.Must,
		MustNot: gate.MustNot,
	}
}

// questionMessage returns the EpicQuestion message of question: its id Q<seq>, its label, its declared choices, its
// recommendation, which is unset when it recommends none, the questions it replaces and replaced, and its answer.
func questionMessage(question store.EpicQuestion) *v1.EpicQuestion {
	return &v1.EpicQuestion{
		Id:             questionID(question.Seq),
		Turn:           question.Turn,
		Text:           question.Text,
		State:          question.State,
		Label:          question.Label,
		Choices:        epicMap(question.Choices, choiceMessage),
		Recommendation: recommendationMessage(question),
		Replaces:       questionRef(question.Replaces),
		ReplacedBy:     questionRef(question.ReplacedBy),
		Choice:         question.Choice,
	}
}

// choiceMessage returns the EpicQuestionChoice message of choice.
func choiceMessage(choice store.EpicChoice) *v1.EpicQuestionChoice {
	return &v1.EpicQuestionChoice{Id: choice.ID, Does: choice.Does}
}

// recommendationMessage returns the EpicRecommendation message of question, or nil when question recommends none.
func recommendationMessage(question store.EpicQuestion) *v1.EpicRecommendation {
	if question.Recommended == "" {
		return nil
	}
	return &v1.EpicRecommendation{
		Choice: question.Recommended, Why: question.Why, Confidence: question.Confidence,
	}
}

// followMessage returns the EpicFollow message of follow.
func followMessage(follow store.LabelFollow) *v1.EpicFollow {
	return &v1.EpicFollow{Label: follow.Label, Answered: follow.Answered, Followed: follow.Followed}
}

// questionRef returns the id Q<seq> of the question with seq, or empty when seq is 0, which names no question.
func questionRef(seq int64) string {
	if seq == 0 {
		return ""
	}
	return questionID(seq)
}

// questionID returns the id of the question with seq, Q<seq>.
func questionID(seq int64) string {
	return fmt.Sprintf("Q%d", seq)
}

// unixOrZero returns t in unix seconds, or 0 when t is the zero time.
func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// summaryMessage returns the EpicSummary message of row: its head fields, its phase time, its seen mark, its revision,
// its steerer, its paused and parked state and its open question count, with no sections. It fails when the open
// question count is outside the range of int32.
func summaryMessage(row store.EpicRow) (*v1.EpicSummary, error) {
	if row.OpenQuestions < 0 || row.OpenQuestions > math.MaxInt32 {
		return nil, fmt.Errorf("open questions %d of epic %s is outside the range of int32", row.OpenQuestions, row.ID)
	}
	open := int32(row.OpenQuestions)
	return &v1.EpicSummary{
		Id:            row.ID,
		Repository:    row.Repository,
		Title:         row.Title,
		Phase:         row.Phase,
		PhaseAt:       row.PhaseAt.Unix(),
		Seen:          unixOrZero(row.Seen),
		Created:       row.Created.Unix(),
		Changed:       row.Changed.Unix(),
		Revision:      row.Revision,
		Steerer:       row.Steerer,
		Paused:        row.Paused,
		PauseSource:   row.PauseSource,
		Parked:        row.Parked,
		OpenQuestions: open,
	}, nil
}

// epicMap converts items in order.
func epicMap[S, T any](items []S, convert func(S) T) []T {
	out := make([]T, 0, len(items))
	for _, item := range items {
		out = append(out, convert(item))
	}
	return out
}
