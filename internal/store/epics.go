package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrNoEpic reports that no epic has the id asked for.
var ErrNoEpic = errors.New("no epic")

// ErrEpicExists reports that an epic with the id is already stored.
var ErrEpicExists = errors.New("epic already stored")

// ErrDecisionConflict reports that a decision id was already recorded on the epic with other text.
var ErrDecisionConflict = errors.New("decision recorded with other text")

// EpicRow is one epic: the head of an accepted contract and its ten ordered sections. Sections keep the order the
// contract gave them. Times are stored at whole seconds.
type EpicRow struct {
	ID          string
	Repository  string
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

// EpicRequirement is one thing that will be true when the epic is done.
type EpicRequirement struct {
	ID       string
	Text     string
	Evidence string
	Kind     string
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
// for the decision: a seed decision's id, or the client id of an answer.
type EpicDecision struct {
	Seq  int64
	ID   string
	Text string
	Time time.Time
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
			ID: row.ID, Repository: row.Repository, Title: row.Title, Approved: row.Approved, Intent: row.Intent,
			Care: row.Care, Approach: row.Approach, ReleaseThen: row.ReleaseThen, Checks: row.Checks,
			Phase: row.Phase, Created: row.Created.Unix(), Changed: row.Changed.Unix(),
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
	if err := insertInOrder(row.Requirements, func(position int64, r EpicRequirement) error {
		return queries.InsertEpicRequirement(ctx, sqlcgen.InsertEpicRequirementParams{
			Epic: row.ID, Position: position, ID: r.ID, Text: r.Text, Evidence: r.Evidence, Kind: r.Kind,
		})
	}); err != nil {
		return fmt.Errorf("requirements: %w", err)
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
			Epic: row.ID, Seq: position, ClientID: d.ID, Text: d.Text, Time: row.Created.Unix(),
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
	row.Requirements = mapInOrder(requirements, func(r sqlcgen.EpicRequirement) EpicRequirement {
		return EpicRequirement{ID: r.ID, Text: r.Text, Evidence: r.Evidence, Kind: r.Kind}
	})

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

// Epics returns every stored epic ordered by id, with its head fields only: the sections and decisions are nil.
func (s *Store) Epics(ctx context.Context) ([]EpicRow, error) {
	heads, err := sqlcgen.New(s.db).ListEpics(ctx)
	if err != nil {
		return nil, fmt.Errorf("list epics: %w", err)
	}
	return mapInOrder(heads, epicHeadOf), nil
}

// AnswerEpic records text as decision id on epic in one write. A stored id with equal text returns the stored
// decision and writes nothing; a stored id with other text is ErrDecisionConflict; otherwise the decision is
// appended as the next seq, dated at, and the epic's changed moves to at. An unknown epic is ErrNoEpic.
func (s *Store) AnswerEpic(ctx context.Context, epic, id, text string, at time.Time) (EpicDecision, error) {
	var answer EpicDecision
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		stored, err := queries.CountEpic(ctx, epic)
		if err != nil {
			return fmt.Errorf("count epic %s: %w", epic, err)
		}
		if stored == 0 {
			return fmt.Errorf("epic %s: %w", epic, ErrNoEpic)
		}
		previous, err := queries.GetEpicDecisionByClientID(ctx, sqlcgen.GetEpicDecisionByClientIDParams{
			Epic: epic, ClientID: id,
		})
		if err == nil {
			if previous.Text != text {
				return fmt.Errorf("decision %s on epic %s: %w", id, epic, ErrDecisionConflict)
			}
			answer = epicDecisionOf(previous)
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read decision %s on epic %s: %w", id, epic, err)
		}
		maxSeq, err := queries.MaxEpicDecisionSeq(ctx, epic)
		if err != nil {
			return fmt.Errorf("read decision count of epic %s: %w", epic, err)
		}
		decision := EpicDecision{Seq: maxSeq + 1, ID: id, Text: text, Time: time.Unix(at.Unix(), 0)}
		if err = queries.InsertEpicDecision(ctx, sqlcgen.InsertEpicDecisionParams{
			Epic: epic, Seq: decision.Seq, ClientID: id, Text: text, Time: at.Unix(),
		}); err != nil {
			return fmt.Errorf("insert decision %s on epic %s: %w", id, epic, err)
		}
		if err = queries.UpdateEpicChanged(ctx, sqlcgen.UpdateEpicChangedParams{
			Changed: at.Unix(), ID: epic,
		}); err != nil {
			return fmt.Errorf("move changed of epic %s: %w", epic, err)
		}
		answer = decision
		return nil
	})
	if err != nil {
		return EpicDecision{}, err
	}
	return answer, nil
}

// epicHeadOf returns the head fields of a stored epic, with its sections and decisions nil.
func epicHeadOf(head sqlcgen.Epic) EpicRow {
	return EpicRow{
		ID:          head.ID,
		Repository:  head.Repository,
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
		Seq:  decision.Seq,
		ID:   decision.ClientID,
		Text: decision.Text,
		Time: time.Unix(decision.Time, 0),
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
