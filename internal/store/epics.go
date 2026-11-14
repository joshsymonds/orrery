package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrNoEpic reports that no epic has the id asked for.
var ErrNoEpic = errors.New("no epic")

// ErrEpicExists reports that an epic with the id is already stored.
var ErrEpicExists = errors.New("epic already stored")

// ErrDecisionConflict reports that a decision id was already recorded on the epic with other text.
var ErrDecisionConflict = errors.New("decision recorded with other text")

// ErrNoQuestion reports that the epic has no question with the seq asked for.
var ErrNoQuestion = errors.New("no question")

// ErrQuestionClosed reports that the question asked for is no longer open.
var ErrQuestionClosed = errors.New("question closed")

// EpicRow is one epic: the head of an accepted contract and its ten ordered sections. Sections keep the order the
// contract gave them. Times are stored at whole seconds.
type EpicRow struct {
	ID          string
	Repository  string
	Base        string
	Title       string
	Approved    string
	Intent      string
	Care        string
	Approach    string
	ReleaseThen string
	Checks      string
	Phase       string
	Created     time.Time
	Changed     time.Time
	Paused      bool
	Parked      string

	Failures     []EpicFailure
	Additions    []EpicAddition
	Requirements []EpicRequirement
	Gates        []EpicGate
	DoneGates    []string
	ReviewGates  []string
	Premises     []EpicPremise
	Exclusions   []string
	Alternatives []EpicAlternative
	ReleaseSteps []EpicReleaseStep
	Decisions    []EpicDecision
	Turns        []EpicTurn
	Tasks        []EpicTask
	Questions    []EpicQuestion
}

// EpicFailure is one thing that could go wrong, with its level of care and its response.
type EpicFailure struct {
	ID       string
	Text     string
	Rating   string
	Response string
}

// EpicAddition is one thing the contract adds that was not asked for.
type EpicAddition struct {
	Location string
	What     string
	Why      string
	Cost     string
}

// EpicRequirement is one thing that will be true when the epic is done, and the evidence item that shows it. The
// item is the zero Item when the Requirement names none.
type EpicRequirement struct {
	ID       string
	Text     string
	Evidence string
	Kind     string
	Item     evidence.Item
}

// EpicGate is one named gate of an epic: a command it runs, or an agent it asks, and what each must and must not do.
// Needs are the pools the gate draws on while it runs, and Limit is its time limit in seconds.
type EpicGate struct {
	Name    string
	Argv    []string
	Limit   int64
	Needs   []EpicGateNeed
	Agent   string
	Must    []string
	MustNot []string
}

// EpicGateNeed is one pool a gate draws Count slots from.
type EpicGateNeed struct {
	Pool  string `json:"pool"`
	Count int64  `json:"count"`
}

// EpicPremise is one assumption the contract rests on.
type EpicPremise struct {
	ID      string
	Text    string
	IfWrong string
}

// EpicAlternative is one approach the contract considered and did not take, and why.
type EpicAlternative struct {
	Alternative string
	WhyNot      string
	Reconsider  string
}

// EpicReleaseStep is one thing that leaves the machine, and how to undo it.
type EpicReleaseStep struct {
	Step   string
	Target string
	Undo   string
}

// EpicDecision is one recorded answer on an epic. Seq counts from 1 in recording order, and ID is the caller's id
// for the decision: a seed decision's id, or the client id of an answer. Question is the seq of the question the
// decision answers, and 0 for a note.
type EpicDecision struct {
	Seq      int64
	ID       string
	Text     string
	Time     time.Time
	Question int64
}

// CreateEpic stores row as one write. It seeds row.Decisions as seq 1..n dated at row.Created, ignoring their Seq
// and Time. An id already stored is ErrEpicExists and changes nothing.
func (s *Store) CreateEpic(ctx context.Context, row EpicRow) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		stored, err := queries.CountEpic(ctx, row.ID)
		if err != nil {
			return fmt.Errorf("count epic %s: %w", row.ID, err)
		}
		if stored > 0 {
			return fmt.Errorf("epic %s: %w", row.ID, ErrEpicExists)
		}
		if err = queries.InsertEpic(ctx, sqlcgen.InsertEpicParams{
			ID: row.ID, Repository: row.Repository, Base: row.Base, Title: row.Title, Approved: row.Approved,
			Intent: row.Intent, Care: row.Care, Approach: row.Approach, ReleaseThen: row.ReleaseThen,
			Checks: row.Checks, Phase: row.Phase, Created: row.Created.Unix(), Changed: row.Changed.Unix(),
		}); err != nil {
			return fmt.Errorf("insert epic %s: %w", row.ID, err)
		}
		if err = insertEpicSections(ctx, queries, row); err != nil {
			return fmt.Errorf("insert sections of epic %s: %w", row.ID, err)
		}
		if err = queries.InsertEpicTrigger(ctx, sqlcgen.InsertEpicTriggerParams{
			Epic: row.ID, ID: createdTriggerID, Cause: createdTriggerCause, State: "pending", Turn: 0,
			Created: row.Created.Unix(),
		}); err != nil {
			return fmt.Errorf("insert trigger of epic %s: %w", row.ID, err)
		}
		return nil
	})
}

// The trigger a created epic starts with: id `created` for cause `epic created`.
const (
	createdTriggerID    = "created"
	createdTriggerCause = "epic created"
)

// insertEpicSections stores the ordered sections of row and its seed decisions.
func insertEpicSections(ctx context.Context, queries *sqlcgen.Queries, row EpicRow) error {
	if err := insertInOrder(row.Failures, func(position int64, f EpicFailure) error {
		return queries.InsertEpicFailure(ctx, sqlcgen.InsertEpicFailureParams{
			Epic: row.ID, Position: position, ID: f.ID, Text: f.Text, Rating: f.Rating, Response: f.Response,
		})
	}); err != nil {
		return fmt.Errorf("failures: %w", err)
	}
	if err := insertInOrder(row.Additions, func(position int64, a EpicAddition) error {
		return queries.InsertEpicAddition(ctx, sqlcgen.InsertEpicAdditionParams{
			Epic: row.ID, Position: position, Location: a.Location, What: a.What, Why: a.Why, Cost: a.Cost,
		})
	}); err != nil {
		return fmt.Errorf("additions: %w", err)
	}
	if err := insertInOrder(row.Requirements, func(position int64, requirement EpicRequirement) error {
		item, err := encodeItem(requirement.Item)
		if err != nil {
			return err
		}
		return queries.InsertEpicRequirement(ctx, sqlcgen.InsertEpicRequirementParams{
			Epic: row.ID, Position: position, ID: requirement.ID, Text: requirement.Text,
			Evidence: requirement.Evidence, Kind: requirement.Kind, Item: item,
		})
	}); err != nil {
		return fmt.Errorf("requirements: %w", err)
	}
	if err := insertInOrder(row.Gates, func(position int64, g EpicGate) error {
		return insertGate(ctx, queries, row.ID, position, g)
	}); err != nil {
		return fmt.Errorf("gates: %w", err)
	}
	if err := insertGateList(ctx, queries, row.ID, "done", row.DoneGates); err != nil {
		return fmt.Errorf("done gates: %w", err)
	}
	if err := insertGateList(ctx, queries, row.ID, "review", row.ReviewGates); err != nil {
		return fmt.Errorf("review gates: %w", err)
	}
	if err := insertInOrder(row.Premises, func(position int64, p EpicPremise) error {
		return queries.InsertEpicPremise(ctx, sqlcgen.InsertEpicPremiseParams{
			Epic: row.ID, Position: position, ID: p.ID, Text: p.Text, IfWrong: p.IfWrong,
		})
	}); err != nil {
		return fmt.Errorf("premises: %w", err)
	}
	if err := insertInOrder(row.Exclusions, func(position int64, text string) error {
		return queries.InsertEpicExclusion(ctx, sqlcgen.InsertEpicExclusionParams{
			Epic: row.ID, Position: position, Text: text,
		})
	}); err != nil {
		return fmt.Errorf("exclusions: %w", err)
	}
	if err := insertInOrder(row.Alternatives, func(position int64, a EpicAlternative) error {
		return queries.InsertEpicAlternative(ctx, sqlcgen.InsertEpicAlternativeParams{
			Epic: row.ID, Position: position, Alternative: a.Alternative, WhyNot: a.WhyNot, Reconsider: a.Reconsider,
		})
	}); err != nil {
		return fmt.Errorf("alternatives: %w", err)
	}
	if err := insertInOrder(row.ReleaseSteps, func(position int64, r EpicReleaseStep) error {
		return queries.InsertEpicReleaseStep(ctx, sqlcgen.InsertEpicReleaseStepParams{
			Epic: row.ID, Position: position, Step: r.Step, Target: r.Target, Undo: r.Undo,
		})
	}); err != nil {
		return fmt.Errorf("release steps: %w", err)
	}
	if err := insertInOrder(row.Decisions, func(position int64, d EpicDecision) error {
		return queries.InsertEpicDecision(ctx, sqlcgen.InsertEpicDecisionParams{
			Epic: row.ID, Seq: position, ClientID: d.ID, Text: d.Text, Time: row.Created.Unix(), Question: 0,
		})
	}); err != nil {
		return fmt.Errorf("decisions: %w", err)
	}
	return nil
}

// insertInOrder calls insert for each item with its position, counted from 1, and stops at the first error.
func insertInOrder[T any](items []T, insert func(position int64, item T) error) error {
	for i, item := range items {
		if err := insert(int64(i+1), item); err != nil {
			return fmt.Errorf("position %d: %w", i+1, err)
		}
	}
	return nil
}

// insertGate stores g as gate position of epic, its argv, needs, must and must not as JSON arrays.
func insertGate(ctx context.Context, queries *sqlcgen.Queries, epic string, position int64, gate EpicGate) error {
	argv, err := encodeList(gate.Argv)
	if err != nil {
		return fmt.Errorf("argv: %w", err)
	}
	needs, err := encodeList(gate.Needs)
	if err != nil {
		return fmt.Errorf("needs: %w", err)
	}
	must, err := encodeList(gate.Must)
	if err != nil {
		return fmt.Errorf("must: %w", err)
	}
	mustNot, err := encodeList(gate.MustNot)
	if err != nil {
		return fmt.Errorf("must not: %w", err)
	}
	if err = queries.InsertEpicGate(ctx, sqlcgen.InsertEpicGateParams{
		Epic: epic, Position: position, Name: gate.Name, Argv: argv, LimitSeconds: gate.Limit, Needs: needs,
		Agent: gate.Agent, Must: must, MustNot: mustNot,
	}); err != nil {
		return fmt.Errorf("insert gate %s: %w", gate.Name, err)
	}
	return nil
}

// insertGateList stores gates as the named list of epic, in order from position 1.
func insertGateList(ctx context.Context, queries *sqlcgen.Queries, epic, list string, gates []string) error {
	return insertInOrder(gates, func(position int64, gate string) error {
		return queries.InsertEpicGateList(ctx, sqlcgen.InsertEpicGateListParams{
			Epic: epic, List: list, Position: position, Gate: gate,
		})
	})
}

// encodeItem returns the stored form of item: its JSON, or the empty string for the zero Item.
func encodeItem(item evidence.Item) (string, error) {
	if reflect.ValueOf(item).IsZero() {
		return "", nil
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return "", fmt.Errorf("encode evidence item: %w", err)
	}
	return string(raw), nil
}

// encodeList returns items as a JSON array, and no items as [].
func encodeList[T any](items []T) (string, error) {
	if len(items) == 0 {
		return "[]", nil
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return "", fmt.Errorf("encode list: %w", err)
	}
	return string(raw), nil
}

// Epic returns the stored epic with id, every section in order. It returns an error matching ErrNoEpic when no epic
// has that id.
func (s *Store) Epic(ctx context.Context, id string) (EpicRow, error) {
	queries := sqlcgen.New(s.db)
	head, err := queries.GetEpic(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return EpicRow{}, fmt.Errorf("epic %s: %w", id, ErrNoEpic)
	}
	if err != nil {
		return EpicRow{}, fmt.Errorf("get epic %s: %w", id, err)
	}
	row := epicHeadOf(head)
	if err = loadEpicSections(ctx, queries, &row); err != nil {
		return EpicRow{}, fmt.Errorf("read sections of epic %s: %w", id, err)
	}
	return row, nil
}

// loadEpicSections fills the ordered sections, decisions and planner sections of row from the rows stored under row.ID.
func loadEpicSections(ctx context.Context, queries *sqlcgen.Queries, row *EpicRow) error {
	failures, err := queries.ListEpicFailures(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list failures: %w", err)
	}
	row.Failures = mapInOrder(failures, func(f sqlcgen.EpicFailure) EpicFailure {
		return EpicFailure{ID: f.ID, Text: f.Text, Rating: f.Rating, Response: f.Response}
	})

	additions, err := queries.ListEpicAdditions(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list additions: %w", err)
	}
	row.Additions = mapInOrder(additions, func(a sqlcgen.EpicAddition) EpicAddition {
		return EpicAddition{Location: a.Location, What: a.What, Why: a.Why, Cost: a.Cost}
	})

	requirements, err := queries.ListEpicRequirements(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list requirements: %w", err)
	}
	row.Requirements, err = convertInOrder(requirements, requirementOf)
	if err != nil {
		return fmt.Errorf("requirements: %w", err)
	}

	if err = loadGateSections(ctx, queries, row); err != nil {
		return fmt.Errorf("gate sections: %w", err)
	}

	premises, err := queries.ListEpicPremises(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list premises: %w", err)
	}
	row.Premises = mapInOrder(premises, func(p sqlcgen.EpicPremise) EpicPremise {
		return EpicPremise{ID: p.ID, Text: p.Text, IfWrong: p.IfWrong}
	})

	exclusions, err := queries.ListEpicExclusions(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list exclusions: %w", err)
	}
	row.Exclusions = mapInOrder(exclusions, func(e sqlcgen.EpicExclusion) string { return e.Text })

	alternatives, err := queries.ListEpicAlternatives(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list alternatives: %w", err)
	}
	row.Alternatives = mapInOrder(alternatives, func(a sqlcgen.EpicAlternative) EpicAlternative {
		return EpicAlternative{Alternative: a.Alternative, WhyNot: a.WhyNot, Reconsider: a.Reconsider}
	})

	releaseSteps, err := queries.ListEpicReleaseSteps(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list release steps: %w", err)
	}
	row.ReleaseSteps = mapInOrder(releaseSteps, func(r sqlcgen.EpicReleaseStep) EpicReleaseStep {
		return EpicReleaseStep{Step: r.Step, Target: r.Target, Undo: r.Undo}
	})

	decisions, err := queries.ListEpicDecisions(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list decisions: %w", err)
	}
	row.Decisions = mapInOrder(decisions, epicDecisionOf)

	if err = loadPlanSections(ctx, queries, row); err != nil {
		return fmt.Errorf("planner sections: %w", err)
	}
	return nil
}

// loadGateSections fills the gates, done gates and review gates of row from the rows stored under row.ID.
func loadGateSections(ctx context.Context, queries *sqlcgen.Queries, row *EpicRow) error {
	gates, err := queries.ListEpicGates(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list gates: %w", err)
	}
	row.Gates, err = convertInOrder(gates, gateOf)
	if err != nil {
		return fmt.Errorf("gates: %w", err)
	}

	doneGates, err := queries.ListEpicDoneGates(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list done gates: %w", err)
	}
	row.DoneGates = mapInOrder(doneGates, func(g sqlcgen.EpicGateList) string { return g.Gate })

	reviewGates, err := queries.ListEpicReviewGates(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list review gates: %w", err)
	}
	row.ReviewGates = mapInOrder(reviewGates, func(g sqlcgen.EpicGateList) string { return g.Gate })
	return nil
}

// Epics returns every stored epic ordered by id, with its head fields only: the sections and decisions are nil.
func (s *Store) Epics(ctx context.Context) ([]EpicRow, error) {
	heads, err := sqlcgen.New(s.db).ListEpics(ctx)
	if err != nil {
		return nil, fmt.Errorf("list epics: %w", err)
	}
	return mapInOrder(heads, epicHeadOf), nil
}

// AnswerEpic records text as decision id on epic in one write, as a note that answers no question. A stored id with
// equal text returns the stored decision and writes nothing; a stored id with other text is ErrDecisionConflict;
// otherwise the decision is appended as the next seq, dated at, and the epic's changed moves to at. An unknown epic
// is ErrNoEpic.
func (s *Store) AnswerEpic(ctx context.Context, epic, id, text string, at time.Time) (EpicDecision, error) {
	var answer EpicDecision
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := requireEpic(ctx, queries, epic); err != nil {
			return err
		}
		replay, found, err := replayAnswer(ctx, queries, epic, id, text)
		if err != nil {
			return err
		}
		if found {
			answer = replay
			return nil
		}
		answer, err = appendDecision(ctx, queries, epic, id, text, 0, at)
		return err
	})
	if err != nil {
		return EpicDecision{}, err
	}
	return answer, nil
}

// AnswerQuestion records text as decision id on epic, tied to question seq, in one write. An unknown epic is
// ErrNoEpic. A stored id with equal text returns the stored decision and writes nothing, even after its question
// closed; a stored id with other text is ErrDecisionConflict. A question the epic does not have is ErrNoQuestion and
// one that is not open is ErrQuestionClosed; both write nothing. Otherwise the decision is appended with its
// question, the question becomes answered, a pending trigger `answer:<id>` is added, and the epic's changed moves to
// at.
func (s *Store) AnswerQuestion(
	ctx context.Context,
	epic, id, text string,
	question int64,
	at time.Time,
) (EpicDecision, error) {
	var answer EpicDecision
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := requireEpic(ctx, queries, epic); err != nil {
			return err
		}
		replay, found, err := replayAnswer(ctx, queries, epic, id, text)
		if err != nil {
			return err
		}
		if found {
			answer = replay
			return nil
		}
		answer, err = recordQuestionAnswer(ctx, queries, epic, id, text, question, at)
		return err
	})
	if err != nil {
		return EpicDecision{}, err
	}
	return answer, nil
}

// recordQuestionAnswer answers the open question with seq question as decision id with text: the question becomes
// answered, a pending trigger `answer:<id>` is added, and the decision is appended tied to the question.
func recordQuestionAnswer(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic, id, text string,
	question int64,
	at time.Time,
) (EpicDecision, error) {
	if err := requireOpenQuestion(ctx, queries, epic, question); err != nil {
		return EpicDecision{}, err
	}
	if err := queries.SetEpicQuestionAnswered(ctx, sqlcgen.SetEpicQuestionAnsweredParams{
		Epic: epic, Seq: question,
	}); err != nil {
		return EpicDecision{}, fmt.Errorf("answer question %d of epic %s: %w", question, epic, err)
	}
	if err := queries.InsertEpicTrigger(ctx, sqlcgen.InsertEpicTriggerParams{
		Epic: epic, ID: "answer:" + id, Cause: fmt.Sprintf("answer %s to Q%d", id, question),
		State: "pending", Turn: 0, Created: at.Unix(),
	}); err != nil {
		return EpicDecision{}, fmt.Errorf("insert trigger of answer %s on epic %s: %w", id, epic, err)
	}
	return appendDecision(ctx, queries, epic, id, text, question, at)
}

// OpenQuestions returns the open questions of epic in seq order, nil when none is open.
func (s *Store) OpenQuestions(ctx context.Context, epic string) ([]EpicQuestion, error) {
	rows, err := sqlcgen.New(s.db).ListOpenEpicQuestions(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list open questions of epic %s: %w", epic, err)
	}
	return mapInOrder(rows, func(question sqlcgen.EpicQuestion) EpicQuestion {
		return EpicQuestion{Seq: question.Seq, Turn: question.Turn, Text: question.Text, State: question.State}
	}), nil
}

// requireEpic is ErrNoEpic when no epic has the id epic.
func requireEpic(ctx context.Context, queries *sqlcgen.Queries, epic string) error {
	stored, err := queries.CountEpic(ctx, epic)
	if err != nil {
		return fmt.Errorf("count epic %s: %w", epic, err)
	}
	if stored == 0 {
		return fmt.Errorf("epic %s: %w", epic, ErrNoEpic)
	}
	return nil
}

// replayAnswer is the client-id check both answer paths share. found is true when the epic already holds decision id
// with equal text, and then decision is that stored row; other text under id is ErrDecisionConflict; found is false
// when id is not stored.
func replayAnswer(ctx context.Context, queries *sqlcgen.Queries, epic, id, text string) (EpicDecision, bool, error) {
	previous, err := queries.GetEpicDecisionByClientID(ctx, sqlcgen.GetEpicDecisionByClientIDParams{
		Epic: epic, ClientID: id,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return EpicDecision{}, false, nil
	}
	if err != nil {
		return EpicDecision{}, false, fmt.Errorf("read decision %s on epic %s: %w", id, epic, err)
	}
	if previous.Text != text {
		return EpicDecision{}, false, fmt.Errorf("decision %s on epic %s: %w", id, epic, ErrDecisionConflict)
	}
	return epicDecisionOf(previous), true, nil
}

// requireOpenQuestion is ErrNoQuestion when epic has no question seq, and ErrQuestionClosed when that question is not
// open.
func requireOpenQuestion(ctx context.Context, queries *sqlcgen.Queries, epic string, question int64) error {
	stored, err := queries.GetEpicQuestion(ctx, sqlcgen.GetEpicQuestionParams{Epic: epic, Seq: question})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("question %d on epic %s: %w", question, epic, ErrNoQuestion)
	}
	if err != nil {
		return fmt.Errorf("read question %d on epic %s: %w", question, epic, err)
	}
	if stored.State != "open" {
		return fmt.Errorf("question %d on epic %s: %w", question, epic, ErrQuestionClosed)
	}
	return nil
}

// appendDecision appends text as decision id on epic, answering question (0 for a note), as the next seq dated at,
// and moves the epic's changed to at.
func appendDecision(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic, id, text string,
	question int64,
	at time.Time,
) (EpicDecision, error) {
	maxSeq, err := queries.MaxEpicDecisionSeq(ctx, epic)
	if err != nil {
		return EpicDecision{}, fmt.Errorf("read decision count of epic %s: %w", epic, err)
	}
	decision := EpicDecision{
		Seq: maxSeq + 1, ID: id, Text: text, Time: time.Unix(at.Unix(), 0), Question: question,
	}
	if err = queries.InsertEpicDecision(ctx, sqlcgen.InsertEpicDecisionParams{
		Epic: epic, Seq: decision.Seq, ClientID: id, Text: text, Time: at.Unix(), Question: question,
	}); err != nil {
		return EpicDecision{}, fmt.Errorf("insert decision %s on epic %s: %w", id, epic, err)
	}
	if err = queries.UpdateEpicChanged(ctx, sqlcgen.UpdateEpicChangedParams{
		Changed: at.Unix(), ID: epic,
	}); err != nil {
		return EpicDecision{}, fmt.Errorf("move changed of epic %s: %w", epic, err)
	}
	return decision, nil
}

// epicHeadOf returns the head fields of a stored epic, with its sections and decisions nil.
func epicHeadOf(head sqlcgen.Epic) EpicRow {
	return EpicRow{
		ID:          head.ID,
		Repository:  head.Repository,
		Base:        head.Base,
		Title:       head.Title,
		Approved:    head.Approved,
		Intent:      head.Intent,
		Care:        head.Care,
		Approach:    head.Approach,
		ReleaseThen: head.ReleaseThen,
		Checks:      head.Checks,
		Phase:       head.Phase,
		Created:     time.Unix(head.Created, 0),
		Changed:     time.Unix(head.Changed, 0),
		Paused:      head.Paused != 0,
		Parked:      head.Parked,
	}
}

// epicDecisionOf returns the decision a stored row holds, its client id as the decision id.
func epicDecisionOf(decision sqlcgen.EpicDecision) EpicDecision {
	return EpicDecision{
		Seq:      decision.Seq,
		ID:       decision.ClientID,
		Text:     decision.Text,
		Time:     time.Unix(decision.Time, 0),
		Question: decision.Question,
	}
}

// mapInOrder converts items in order. No items map to nil, so an empty section reads back nil.
func mapInOrder[S, T any](items []S, convert func(S) T) []T {
	if len(items) == 0 {
		return nil
	}
	out := make([]T, 0, len(items))
	for _, item := range items {
		out = append(out, convert(item))
	}
	return out
}

// convertInOrder converts items in order, as mapInOrder does, and stops at the first conversion error.
func convertInOrder[S, T any](items []S, convert func(S) (T, error)) ([]T, error) {
	if len(items) == 0 {
		return nil, nil
	}
	out := make([]T, 0, len(items))
	for _, item := range items {
		converted, err := convert(item)
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
	}
	return out, nil
}

// requirementOf returns the Requirement a stored row holds, its item decoded.
func requirementOf(stored sqlcgen.EpicRequirement) (EpicRequirement, error) {
	item, err := decodeItem(stored.Item)
	if err != nil {
		return EpicRequirement{}, fmt.Errorf("requirement %s item: %w", stored.ID, err)
	}
	return EpicRequirement{
		ID: stored.ID, Text: stored.Text, Evidence: stored.Evidence, Kind: stored.Kind, Item: item,
	}, nil
}

// gateOf returns the gate a stored row holds, its JSON arrays decoded.
func gateOf(stored sqlcgen.EpicGate) (EpicGate, error) {
	argv, err := decodeList[string](stored.Argv)
	if err != nil {
		return EpicGate{}, fmt.Errorf("gate %s argv: %w", stored.Name, err)
	}
	needs, err := decodeList[EpicGateNeed](stored.Needs)
	if err != nil {
		return EpicGate{}, fmt.Errorf("gate %s needs: %w", stored.Name, err)
	}
	must, err := decodeList[string](stored.Must)
	if err != nil {
		return EpicGate{}, fmt.Errorf("gate %s must: %w", stored.Name, err)
	}
	mustNot, err := decodeList[string](stored.MustNot)
	if err != nil {
		return EpicGate{}, fmt.Errorf("gate %s must not: %w", stored.Name, err)
	}
	return EpicGate{
		Name: stored.Name, Argv: argv, Limit: stored.LimitSeconds, Needs: needs, Agent: stored.Agent,
		Must: must, MustNot: mustNot,
	}, nil
}

// decodeItem reads an item stored by encodeItem, where the empty string is the zero Item.
func decodeItem(stored string) (evidence.Item, error) {
	var item evidence.Item
	if stored == "" {
		return item, nil
	}
	if err := json.Unmarshal([]byte(stored), &item); err != nil {
		return evidence.Item{}, fmt.Errorf("decode evidence item: %w", err)
	}
	return item, nil
}

// decodeList reads a JSON array stored by encodeList. An empty array reads back nil, as an empty section does.
func decodeList[T any](stored string) ([]T, error) {
	var items []T
	if err := json.Unmarshal([]byte(stored), &items); err != nil {
		return nil, fmt.Errorf("decode list: %w", err)
	}
	if len(items) == 0 {
		return nil, nil
	}
	return items, nil
}
