package enginehost

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// epicUnavailableMessage is the message every epic RPC is refused with when there is no epic store.
const epicUnavailableMessage = "epic store unavailable"

// epicPhaseAccepted is the phase of an epic the Epics service creates.
const epicPhaseAccepted = "accepted"

// EpicCommands is the set of epic operations the Epics service answers with. The store satisfies it.
type EpicCommands interface {
	CreateEpic(ctx context.Context, row store.EpicRow) error
	Epic(ctx context.Context, id string) (store.EpicRow, error)
	Epics(ctx context.Context) ([]store.EpicRow, error)
	AnswerEpic(ctx context.Context, epic, id, text string, at time.Time) (store.EpicDecision, error)
	AnswerQuestion(
		ctx context.Context, epic, id, text string, question int64, at time.Time,
	) (store.EpicDecision, error)
	OpenQuestions(ctx context.Context, epic string) ([]store.EpicQuestion, error)
	PauseEpic(ctx context.Context, epic string) error
	ResumeEpic(ctx context.Context, epic string) error
	StopEpic(ctx context.Context, epic string) (string, error)
	RetryEpic(ctx context.Context, epic string) error
}

// epicService implements the Epics RPCs over epics. steps is the step store StopEpic stops the turn's step through;
// it is nil when Serve has no step engine, and StopEpic then stops no step.
type epicService struct {
	epics EpicCommands
	steps StepCommands
}

// ready returns the refusal every epic RPC gets when there is no epic store.
func (s *epicService) ready() error {
	if s.epics == nil {
		return refuseEpic(connect.CodeUnavailable, epicUnavailableMessage)
	}
	return nil
}

// CreateEpic checks the contract with checkEpic, then stores it accepted, with its seed decisions, and returns the
// stored epic read back.
func (s *epicService) CreateEpic(
	ctx context.Context,
	req *connect.Request[v1.CreateEpicRequest],
) (*connect.Response[v1.CreateEpicResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := checkEpic(ctx, req.Msg); err != nil {
		return nil, refuseEpic(connect.CodeInvalidArgument, err.Error())
	}
	row := epicRowOf(req.Msg, time.Now())
	if err := s.epics.CreateEpic(ctx, row); err != nil {
		if errors.Is(err, store.ErrEpicExists) {
			return nil, refuseEpic(connect.CodeAlreadyExists, fmt.Sprintf("id: %s is already in the database", row.ID))
		}
		return nil, epicStoreError(err)
	}
	stored, err := s.epics.Epic(ctx, row.ID)
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.CreateEpicResponse{Epic: storedEpicMessage(stored)}), nil
}

func (s *epicService) GetEpic(
	ctx context.Context,
	req *connect.Request[v1.GetEpicRequest],
) (*connect.Response[v1.GetEpicResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	row, err := s.epics.Epic(ctx, req.Msg.GetId())
	if errors.Is(err, store.ErrNoEpic) {
		return nil, refuseEpic(connect.CodeNotFound, fmt.Sprintf("id: no epic %s", req.Msg.GetId()))
	}
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.GetEpicResponse{Epic: storedEpicMessage(row)}), nil
}

func (s *epicService) ListEpics(
	ctx context.Context,
	_ *connect.Request[v1.ListEpicsRequest],
) (*connect.Response[v1.ListEpicsResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	rows, err := s.epics.Epics(ctx)
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.ListEpicsResponse{Epics: epicMap(rows, summaryMessage)}), nil
}

// AnswerEpic records the answer on the epic, once. It refuses an empty epic_id, id or text first. An answer with no
// question is a note, and it warns, when questions are open, naming them. An answer with a question is tied to it,
// which closes it; a question that is not Q<n> with a positive n is refused as unknown. The store then answers an
// unknown epic, an unknown or closed question, or an id recorded with other text, as the refusals say.
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
	if req.Msg.GetQuestion() == "" {
		return s.answerNote(ctx, req.Msg)
	}
	seq, ok := questionSeq(req.Msg.GetQuestion())
	if !ok {
		return nil, questionNotFound(req.Msg)
	}
	decision, err := s.epics.AnswerQuestion(
		ctx, req.Msg.GetEpicId(), req.Msg.GetId(), req.Msg.GetText(), seq, time.Now(),
	)
	if err != nil {
		return nil, answerError(req.Msg, err)
	}
	return connect.NewResponse(&v1.AnswerEpicResponse{Decision: decisionMessage(decision)}), nil
}

// answerNote records the note req is, and returns it with the warning naming the open questions when any are open.
func (s *epicService) answerNote(
	ctx context.Context,
	req *v1.AnswerEpicRequest,
) (*connect.Response[v1.AnswerEpicResponse], error) {
	decision, err := s.epics.AnswerEpic(ctx, req.GetEpicId(), req.GetId(), req.GetText(), time.Now())
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
// epic_id, an id recorded with other text is AlreadyExists, an unknown question is NotFound on question, a closed
// question is FailedPrecondition, and any other error goes through epicStoreError.
func answerError(req *v1.AnswerEpicRequest, err error) error {
	switch {
	case errors.Is(err, store.ErrNoEpic):
		return refuseEpic(connect.CodeNotFound, fmt.Sprintf("epic_id: no epic %s", req.GetEpicId()))
	case errors.Is(err, store.ErrDecisionConflict):
		return refuseEpic(connect.CodeAlreadyExists, fmt.Sprintf("id: %s was recorded with other text", req.GetId()))
	case errors.Is(err, store.ErrNoQuestion):
		return questionNotFound(req)
	case errors.Is(err, store.ErrQuestionClosed):
		return refuseEpic(connect.CodeFailedPrecondition, fmt.Sprintf("question: %s is closed", req.GetQuestion()))
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

// checkEpicAnswer returns the refusal of an answer whose epic_id, id or text is empty, naming the first one that is.
// The text is empty after trimming white space.
func checkEpicAnswer(req *v1.AnswerEpicRequest) error {
	switch {
	case req.GetEpicId() == "":
		return refuseEpic(connect.CodeInvalidArgument, "epic_id: empty")
	case req.GetId() == "":
		return refuseEpic(connect.CodeInvalidArgument, "id: empty")
	case isBlank(req.GetText()):
		return refuseEpic(connect.CodeInvalidArgument, "text: empty")
	}
	return nil
}

// PauseEpic pauses the epic, so the planner starts no turn for it, and returns the epic read back. An epic already
// paused is refused.
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
	if err := s.epics.PauseEpic(ctx, id); err != nil {
		return nil, epicControlError(id, err)
	}
	stored, err := s.epics.Epic(ctx, id)
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.PauseEpicResponse{Epic: storedEpicMessage(stored)}), nil
}

// ResumeEpic clears the pause of the epic and returns the epic read back. An epic that is not paused is refused.
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
	if err := s.epics.ResumeEpic(ctx, id); err != nil {
		return nil, epicControlError(id, err)
	}
	stored, err := s.epics.Epic(ctx, id)
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.ResumeEpicResponse{Epic: storedEpicMessage(stored)}), nil
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
	step, err := s.epics.StopEpic(ctx, id)
	if err != nil {
		return nil, epicControlError(id, err)
	}
	if step != "" && s.steps != nil {
		if err = s.steps.Stop(ctx, step); err != nil {
			return nil, epicStoreError(err)
		}
	}
	stored, err := s.epics.Epic(ctx, id)
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.StopEpicResponse{Epic: storedEpicMessage(stored)}), nil
}

// RetryEpic ends the park of the epic and returns the epic read back. An epic that is not parked is refused.
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
	if err := s.epics.RetryEpic(ctx, id); err != nil {
		return nil, epicControlError(id, err)
	}
	stored, err := s.epics.Epic(ctx, id)
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.RetryEpicResponse{Epic: storedEpicMessage(stored)}), nil
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

// epicRowOf returns the store row of the contract req names, accepted at now, with req's decisions as its seeds.
func epicRowOf(req *v1.CreateEpicRequest, now time.Time) store.EpicRow {
	contract := req.GetContract()
	return store.EpicRow{
		ID:          contract.GetId(),
		Repository:  contract.GetRepository(),
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
			return store.EpicRequirement{ID: r.GetId(), Text: r.GetText(), Evidence: r.GetEvidence(), Kind: r.GetKind()}
		}),
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
		Decisions: epicMap(req.GetDecisions(), func(d *v1.EpicDecisionInput) store.EpicDecision {
			return store.EpicDecision{ID: d.GetId(), Text: d.GetText()}
		}),
	}
}

// storedEpicMessage returns the StoredEpic message of row: its contract, phase, times, decisions in order, and its
// paused and parked state with its turns, tasks and questions in order.
func storedEpicMessage(row store.EpicRow) *v1.StoredEpic {
	return &v1.StoredEpic{
		Contract:  contractMessage(row),
		Phase:     row.Phase,
		Created:   row.Created.Unix(),
		Changed:   row.Changed.Unix(),
		Decisions: epicMap(row.Decisions, decisionMessage),
		Paused:    row.Paused,
		Parked:    row.Parked,
		Turns:     epicMap(row.Turns, turnMessage),
		Tasks:     epicMap(row.Tasks, taskMessage),
		Questions: epicMap(row.Questions, questionMessage),
	}
}

// contractMessage returns the EpicContract message of row, every section in order.
func contractMessage(row store.EpicRow) *v1.EpicContract {
	return &v1.EpicContract{
		Id:         row.ID,
		Repository: row.Repository,
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
			return &v1.EpicRequirement{Id: r.ID, Text: r.Text, Evidence: r.Evidence, Kind: r.Kind}
		}),
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
	message := &v1.EpicDecision{Seq: d.Seq, Id: d.ID, Text: d.Text, Time: d.Time.Unix()}
	if d.Question != 0 {
		message.Question = questionID(d.Question)
	}
	return message
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

// taskMessage returns the EpicTask message of task, its id T<seq>.
func taskMessage(task store.EpicTask) *v1.EpicTask {
	return &v1.EpicTask{
		Id:           fmt.Sprintf("T%d", task.Seq),
		Turn:         task.Turn,
		Title:        task.Title,
		Deliverable:  task.Deliverable,
		Files:        task.Files,
		Requirements: task.Requirements,
		Check:        task.Check,
		Status:       task.Status,
	}
}

// questionMessage returns the EpicQuestion message of question, its id Q<seq>.
func questionMessage(question store.EpicQuestion) *v1.EpicQuestion {
	return &v1.EpicQuestion{
		Id:    questionID(question.Seq),
		Turn:  question.Turn,
		Text:  question.Text,
		State: question.State,
	}
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

// summaryMessage returns the EpicSummary message of row: its head fields, with no sections.
func summaryMessage(row store.EpicRow) *v1.EpicSummary {
	return &v1.EpicSummary{
		Id:         row.ID,
		Repository: row.Repository,
		Title:      row.Title,
		Phase:      row.Phase,
		Created:    row.Created.Unix(),
		Changed:    row.Changed.Unix(),
	}
}

// epicMap converts items in order.
func epicMap[S, T any](items []S, convert func(S) T) []T {
	out := make([]T, 0, len(items))
	for _, item := range items {
		out = append(out, convert(item))
	}
	return out
}
