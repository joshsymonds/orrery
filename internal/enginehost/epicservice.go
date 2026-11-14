package enginehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/evidence"
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
}

// epicService implements the Epics RPCs over epics. getenv reads the environment a contract's gates resolve in; nil
// reads the process environment.
type epicService struct {
	epics  EpicCommands
	getenv func(string) string
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
	getenv := s.getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if err := checkEpic(ctx, req.Msg, getenv); err != nil {
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

// AnswerEpic records the answer on the epic, once. It refuses an empty epic_id, id or text first; the store then
// answers an unknown epic, or an id recorded with other text, as the refusals say.
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
	decision, err := s.epics.AnswerEpic(ctx, req.Msg.GetEpicId(), req.Msg.GetId(), req.Msg.GetText(), time.Now())
	switch {
	case errors.Is(err, store.ErrNoEpic):
		return nil, refuseEpic(connect.CodeNotFound, fmt.Sprintf("epic_id: no epic %s", req.Msg.GetEpicId()))
	case errors.Is(err, store.ErrDecisionConflict):
		return nil, refuseEpic(
			connect.CodeAlreadyExists,
			fmt.Sprintf("id: %s was recorded with other text", req.Msg.GetId()),
		)
	case err != nil:
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.AnswerEpicResponse{Decision: decisionMessage(decision)}), nil
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

// decisionMessage returns the EpicDecision message of d.
func decisionMessage(d store.EpicDecision) *v1.EpicDecision {
	return &v1.EpicDecision{Seq: d.Seq, Id: d.ID, Text: d.Text, Time: d.Time.Unix()}
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
		Gate:         task.Gate,
		Evidence:     evidenceItemMessage(task.Evidence),
		Status:       task.Status,
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
	}
}

// questionMessage returns the EpicQuestion message of question, its id Q<seq>.
func questionMessage(question store.EpicQuestion) *v1.EpicQuestion {
	return &v1.EpicQuestion{
		Id:    fmt.Sprintf("Q%d", question.Seq),
		Turn:  question.Turn,
		Text:  question.Text,
		State: question.State,
	}
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
