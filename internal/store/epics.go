package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/reviewers"
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

// ErrNoChoice reports that the question does not offer the choice an answer names: neither one of its declared choices
// nor other.
var ErrNoChoice = errors.New("question offers no such choice")

// ErrOtherNeedsText reports an answer that needs text and has none: other with text that is blank, or correct to a
// gaps question with text that is blank, since the text is the correction the planner is given.
var ErrOtherNeedsText = errors.New("other answer needs text")

// QuestionReplacedError reports an answer to question Seq, which the question By replaced.
type QuestionReplacedError struct{ Seq, By int64 }

func (e *QuestionReplacedError) Error() string {
	return fmt.Sprintf("question %d was replaced by question %d", e.Seq, e.By)
}

// EpicRow is one epic: the head of an accepted contract and its ten ordered sections. Sections keep the order the
// contract gave them. Times are stored at whole seconds. Seen is the time of the epic's last SeeEpic mark, zero when
// none is stored. Synced is the commit of origin's main that the stored base last merged in a sync, "" while no sync
// has passed, and OriginRead the epic's last read of origin's main.
type EpicRow struct {
	ID          string
	Repository  string
	Base        string
	StoredBase  string
	Candidate   string
	Synced      string
	OriginRead  OriginRead
	Branch      string
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
	PhaseAt     time.Time
	Seen        time.Time
	// Revision counts the store writes that wrote the epic's row or a row of its parts, its steps aside, its creation
	// among them (see epicMoves), so a reader can tell that the epic changed in any part by comparing it alone.
	Revision int64
	Paused   bool
	// Parked is the park reason, empty when the epic is not parked, and ParkClass is the class of that park, which says
	// who answers it.
	Parked    string
	ParkClass ParkClass
	// ClosedBy is the closer the epic was closed by, the owner or the planner, and "" while it is not closed.
	// SupersededBy is the epic that replaces an epic closed as superseded, and "" otherwise.
	ClosedBy     string
	SupersededBy string
	Summary      string
	Report       EpicReport
	Workflow     EpicWorkflow

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
	Reviewers    []reviewers.Reviewer
	// Amendments are the amendments of the evidence items of the epic's Requirements, in seq order.
	Amendments []EvidenceAmendment
}

// EpicWorkflow is the workflow an epic pins: its name, the canonical text it was loaded as, the sha256 of that text,
// and its nodes in preorder. A zero EpicWorkflow is an epic with no pinned workflow.
type EpicWorkflow struct {
	Name   string
	Text   string
	SHA256 string
	Nodes  []EpicWorkflowNode
}

// EpicWorkflowNode is one node of a pinned workflow. Parent is the id of the node that encloses it, "" at the top
// level, and Kind is the kind name, or `loop` for a loop node.
type EpicWorkflowNode struct {
	ID     string
	Parent string
	Kind   string
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
// item is the zero Item when the Requirement names none. DroppedBy is the seq of the owner's decision that dropped the
// Requirement as already met at the epic's base, and 0 while it is not dropped.
type EpicRequirement struct {
	ID        string
	Text      string
	Evidence  string
	Kind      string
	Item      evidence.Item
	DroppedBy int64
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
			ID: row.ID, Repository: row.Repository, Base: row.Base, StoredBase: row.Base, Title: row.Title,
			Approved: row.Approved, Intent: row.Intent, Care: row.Care, Approach: row.Approach,
			ReleaseThen: row.ReleaseThen, Checks: row.Checks, Phase: row.Phase, Created: row.Created.Unix(),
			Changed: row.Changed.Unix(), Workflow: row.Workflow.Name, WorkflowText: row.Workflow.Text,
			WorkflowSha256: row.Workflow.SHA256, PhaseAt: row.Created.Unix(),
		}); err != nil {
			return fmt.Errorf("insert epic %s: %w", row.ID, err)
		}
		if err = insertEpicSections(ctx, queries, row); err != nil {
			return fmt.Errorf("insert sections of epic %s: %w", row.ID, err)
		}
		if err = insertWorkflowNodes(ctx, queries, row); err != nil {
			return fmt.Errorf("insert workflow nodes of epic %s: %w", row.ID, err)
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
	if err := insertInOrder(row.Reviewers, func(position int64, rev reviewers.Reviewer) error {
		return insertReviewer(ctx, queries, row.ID, position, rev)
	}); err != nil {
		return fmt.Errorf("reviewers: %w", err)
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

// insertWorkflowNodes stores the nodes of row's pinned workflow in preorder, positions counted from 0.
func insertWorkflowNodes(ctx context.Context, queries *sqlcgen.Queries, row EpicRow) error {
	for i, node := range row.Workflow.Nodes {
		if err := queries.InsertEpicWorkflowNode(ctx, sqlcgen.InsertEpicWorkflowNodeParams{
			Epic: row.ID, Position: int64(i), ID: node.ID, Parent: node.Parent, Kind: node.Kind,
		}); err != nil {
			return fmt.Errorf("node %s: %w", node.ID, err)
		}
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

// insertReviewer stores rev as reviewer position of epic, its lists as JSON arrays and its flags as 0 or 1.
func insertReviewer(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	position int64,
	rev reviewers.Reviewer,
) error {
	stage, err := encodeStrings(rev.Stage)
	if err != nil {
		return fmt.Errorf("stage: %w", err)
	}
	must, err := encodeStrings(rev.Must)
	if err != nil {
		return fmt.Errorf("must: %w", err)
	}
	mustNot, err := encodeStrings(rev.MustNot)
	if err != nil {
		return fmt.Errorf("must not: %w", err)
	}
	tools, err := encodeStrings(rev.Tools)
	if err != nil {
		return fmt.Errorf("tools: %w", err)
	}
	if err = queries.InsertEpicReviewer(ctx, sqlcgen.InsertEpicReviewerParams{
		Epic: epic, Position: position, Name: rev.Name, Enabled: boolInt(rev.Enabled), Stage: stage, Must: must,
		MustNot: mustNot, Prompt: rev.Prompt, Agent: rev.Agent, Model: rev.Model, Tools: tools, Effort: rev.Effort,
		LimitSeconds: rev.Limit, Verify: boolInt(rev.Verify),
	}); err != nil {
		return fmt.Errorf("insert reviewer %s: %w", rev.Name, err)
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

// encodeStrings returns items as JSON, a nil list as null and an empty list as []. The two stay apart because an
// empty tools list runs a reviewer with no tools, and a nil one leaves the tools to the agent.
func encodeStrings(items []string) (string, error) {
	raw, err := json.Marshal(items)
	if err != nil {
		return "", fmt.Errorf("encode strings: %w", err)
	}
	return string(raw), nil
}

// Epic returns the stored epic with id, every section in order. It returns an error matching ErrNoEpic when no epic
// has that id.
func (s *Store) Epic(ctx context.Context, id string) (EpicRow, error) {
	return readEpic(ctx, sqlcgen.New(s.db), id)
}

// readEpic returns the stored epic with id as queries read it, every section in order. It returns an error matching
// ErrNoEpic when no epic has that id.
func readEpic(ctx context.Context, queries *sqlcgen.Queries, id string) (EpicRow, error) {
	head, err := queries.GetEpic(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return EpicRow{}, fmt.Errorf("epic %s: %w", id, ErrNoEpic)
	}
	if err != nil {
		return EpicRow{}, fmt.Errorf("get epic %s: %w", id, err)
	}
	return readEpicOf(ctx, queries, head)
}

// readEpicOf returns the stored epic whose head is head, every section in order, its pinned workflow's nodes, its seen
// mark, its evidence amendments and its revision.
func readEpicOf(ctx context.Context, queries *sqlcgen.Queries, head sqlcgen.Epic) (EpicRow, error) {
	row := epicHeadOf(head)
	report, err := epicReportOf(head)
	if err != nil {
		return EpicRow{}, fmt.Errorf("read report of epic %s: %w", row.ID, err)
	}
	row.Report = report
	if err = loadEpicSections(ctx, queries, &row); err != nil {
		return EpicRow{}, fmt.Errorf("read sections of epic %s: %w", row.ID, err)
	}
	if err = loadEpicWorkflowNodes(ctx, queries, &row); err != nil {
		return EpicRow{}, fmt.Errorf("read workflow of epic %s: %w", row.ID, err)
	}
	if err = loadEpicSeen(ctx, queries, &row); err != nil {
		return EpicRow{}, fmt.Errorf("read seen of epic %s: %w", row.ID, err)
	}
	if err = loadAmendments(ctx, queries, &row); err != nil {
		return EpicRow{}, fmt.Errorf("read amendments of epic %s: %w", row.ID, err)
	}
	if row.Revision, err = epicRevisionOf(ctx, queries, row.ID); err != nil {
		return EpicRow{}, err
	}
	return row, nil
}

// epicRevisionOf returns the revision of epic: its epic_revisions row's, or 0 when no store write has moved it.
func epicRevisionOf(ctx context.Context, queries *sqlcgen.Queries, epic string) (int64, error) {
	revision, err := queries.GetEpicRevision(ctx, epic)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read revision of epic %s: %w", epic, err)
	}
	return revision, nil
}

// loadEpicWorkflowNodes fills the nodes of the workflow of row from the rows stored under row.ID, in position order.
func loadEpicWorkflowNodes(ctx context.Context, queries *sqlcgen.Queries, row *EpicRow) error {
	nodes, err := queries.ListEpicWorkflowNodes(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list workflow nodes: %w", err)
	}
	row.Workflow.Nodes = mapInOrder(nodes, epicWorkflowNodeOf)
	return nil
}

// epicWorkflowNodeOf returns the workflow node a stored row holds.
func epicWorkflowNodeOf(n sqlcgen.EpicWorkflowNode) EpicWorkflowNode {
	return EpicWorkflowNode{ID: n.ID, Parent: n.Parent, Kind: n.Kind}
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

	if err = loadEpicReviewers(ctx, queries, row); err != nil {
		return fmt.Errorf("reviewers: %w", err)
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

// loadEpicReviewers fills the reviewers of row from the rows stored under row.ID, in position order.
func loadEpicReviewers(ctx context.Context, queries *sqlcgen.Queries, row *EpicRow) error {
	stored, err := queries.ListEpicReviewers(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list reviewers: %w", err)
	}
	row.Reviewers, err = convertInOrder(stored, reviewerOf)
	if err != nil {
		return fmt.Errorf("convert: %w", err)
	}
	return nil
}

// Epics returns every stored epic ordered by id, with its head fields, seen mark and pinned workflow only: the sections
// and decisions are nil. The heads, every epic's workflow nodes and every seen mark are read in one read transaction,
// three queries whatever the number of epics.
func (s *Store) Epics(ctx context.Context) ([]EpicRow, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin epics read: %w", err)
	}
	rows, err := readEpics(ctx, sqlcgen.New(tx))
	if err != nil {
		return nil, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end epics read: %w", err)
	}
	return rows, nil
}

// readEpics returns every stored epic as Epics does, read with queries.
func readEpics(ctx context.Context, queries *sqlcgen.Queries) ([]EpicRow, error) {
	heads, err := queries.ListEpics(ctx)
	if err != nil {
		return nil, fmt.Errorf("list epics: %w", err)
	}
	nodes, err := queries.ListAllEpicWorkflowNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workflow nodes: %w", err)
	}
	seen, err := queries.ListEpicSeen(ctx)
	if err != nil {
		return nil, fmt.Errorf("list seen marks: %w", err)
	}
	byEpic := make(map[string][]EpicWorkflowNode, len(heads))
	for _, n := range nodes {
		byEpic[n.Epic] = append(byEpic[n.Epic], epicWorkflowNodeOf(n))
	}
	seenAt := make(map[string]int64, len(seen))
	for _, mark := range seen {
		seenAt[mark.Epic] = mark.At
	}
	rows := mapInOrder(heads, func(head sqlcgen.ListEpicsRow) EpicRow { return epicHeadOf(head.Epic) })
	for i := range rows {
		rows[i].Workflow.Nodes = byEpic[rows[i].ID]
		rows[i].Revision = heads[i].Revision
		if at, ok := seenAt[rows[i].ID]; ok {
			rows[i].Seen = time.Unix(at, 0)
		}
	}
	return rows, nil
}

// EpicListings returns every stored epic ordered by id, with only the fields a listing of epics reads: its id,
// repository, title, phase, phase time, created and changed times, revision and seen mark. Every other field is zero,
// and no workflow node is read. The rows come from one query.
func (s *Store) EpicListings(ctx context.Context) ([]EpicRow, error) {
	return readEpicListings(ctx, sqlcgen.New(s.db))
}

// readEpicListings returns every stored epic as EpicListings does, read with queries.
func readEpicListings(ctx context.Context, queries *sqlcgen.Queries) ([]EpicRow, error) {
	listed, err := queries.ListEpicListings(ctx)
	if err != nil {
		return nil, fmt.Errorf("list epic listings: %w", err)
	}
	return mapInOrder(listed, epicListingOf), nil
}

// epicListingOf returns the epic a listing row holds, with the fields EpicListings reads and no others.
func epicListingOf(listed sqlcgen.ListEpicListingsRow) EpicRow {
	row := EpicRow{
		ID:         listed.ID,
		Repository: listed.Repository,
		Title:      listed.Title,
		Phase:      listed.Phase,
		Created:    time.Unix(listed.Created, 0),
		Changed:    time.Unix(listed.Changed, 0),
		PhaseAt:    time.Unix(listed.PhaseAt, 0),
		Revision:   listed.Revision,
	}
	if listed.SeenAt.Valid {
		row.Seen = time.Unix(listed.SeenAt.Int64, 0)
	}
	return row
}

// UnsummarizedEpics returns every stored epic whose summary is empty, ordered by id, each read as Epic reads it. A
// stored epic with a summary is not returned.
func (s *Store) UnsummarizedEpics(ctx context.Context) ([]EpicRow, error) {
	queries := sqlcgen.New(s.db)
	heads, err := queries.ListUnsummarizedEpics(ctx)
	if err != nil {
		return nil, fmt.Errorf("list unsummarized epics: %w", err)
	}
	var rows []EpicRow
	for _, head := range heads {
		var row EpicRow
		if row, err = readEpicOf(ctx, queries, head); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// SetEpicSummary stores summary as the summary of epic when the stored summary is empty, and leaves a stored summary
// as it is, so a summary is written once. It does not move the epic's changed. An unknown epic is ErrNoEpic.
func (s *Store) SetEpicSummary(ctx context.Context, id, summary string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := requireEpic(ctx, queries, id); err != nil {
			return err
		}
		if err := queries.SetEpicSummary(ctx, sqlcgen.SetEpicSummaryParams{Summary: summary, ID: id}); err != nil {
			return fmt.Errorf("set summary of epic %s: %w", id, err)
		}
		return nil
	})
}

// AnswerEpic records text as decision id on epic in one write, as a note that answers no question. A stored id with
// equal text returns the stored decision and writes nothing; a stored id with other text, or one that answered a
// question, is ErrDecisionConflict; otherwise the decision is appended as the next seq, dated at, and the epic's
// changed moves to at. An unknown epic is ErrNoEpic.
func (s *Store) AnswerEpic(ctx context.Context, epic, id, text string, at time.Time) (EpicDecision, error) {
	var answer EpicDecision
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := requireEpic(ctx, queries, epic); err != nil {
			return err
		}
		replay, found, err := replayAnswer(ctx, queries, epic, id, text, "")
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

// AnswerQuestion records text as decision id on epic, answering question seq with choice, in one write. choice is one
// the question offers: one of its declared choices, or ChoiceOther, which needs text that is not blank; a declared
// choice keeps text as given, which may be empty. An unknown epic is ErrNoEpic. A stored id with equal text and choice
// returns the stored decision and writes nothing, even after its question closed; a stored id with other text or
// another choice is ErrDecisionConflict. A question the epic does not have is ErrNoQuestion, a replaced one is a
// *QuestionReplacedError and an answered one is ErrQuestionClosed. A choice the question does not offer is ErrNoChoice,
// other with blank text is ErrOtherNeedsText, and so is correct with blank text to a gaps question. Each refusal
// writes nothing. Otherwise the question becomes answered with choice, a pending trigger `answer:<id>` is added, the
// decision is appended with its question, and the epic's changed moves to at. An answer to a park question that is not
// final also ends the park, as RetryEpic does; a final park offers only other and stays parked. An answer to a gaps
// question carries out its choice in the same write, as answerGapsQuestion gives.
func (s *Store) AnswerQuestion(
	ctx context.Context,
	epic, id, text string,
	question int64,
	choice string,
	at time.Time,
) (EpicDecision, error) {
	var answer EpicDecision
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := requireEpic(ctx, queries, epic); err != nil {
			return err
		}
		replay, found, err := replayAnswer(ctx, queries, epic, id, text, choice)
		if err != nil {
			return err
		}
		if found {
			answer = replay
			return nil
		}
		answer, err = recordQuestionAnswer(ctx, queries, epic, id, text, question, choice, at)
		return err
	})
	if err != nil {
		return EpicDecision{}, err
	}
	return answer, nil
}

// recordQuestionAnswer answers the open question with seq question as decision id with text and choice: the question
// becomes answered with choice, a pending trigger `answer:<id>` is added, and the decision is appended tied to the
// question. An answer to a park question for a red run judged holding at base carries out its choice, as
// answerMetAtBase gives. An answered park question that is not final ends the park in the same write. An answer to a
// gaps question carries out its choice in the same write, after the decision is appended, as answerGapsQuestion gives.
func recordQuestionAnswer(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic, id, text string,
	question int64,
	choice string,
	at time.Time,
) (EpicDecision, error) {
	answered, err := requireAnswerable(ctx, queries, epic, text, question, choice)
	if err != nil {
		return EpicDecision{}, err
	}
	if err = queries.SetEpicQuestionAnswered(ctx, sqlcgen.SetEpicQuestionAnsweredParams{
		Choice: choice, Epic: epic, Seq: question,
	}); err != nil {
		return EpicDecision{}, fmt.Errorf("answer question %d of epic %s: %w", question, epic, err)
	}
	if err = queries.InsertEpicTrigger(ctx, sqlcgen.InsertEpicTriggerParams{
		Epic: epic, ID: "answer:" + id, Cause: fmt.Sprintf("answer %s to Q%d", id, question),
		State: "pending", Turn: 0, Created: at.Unix(),
	}); err != nil {
		return EpicDecision{}, fmt.Errorf("insert trigger of answer %s on epic %s: %w", id, epic, err)
	}
	decision, err := appendDecision(ctx, queries, epic, id, text, question, at)
	if err != nil {
		return EpicDecision{}, err
	}
	err = answerMetAtBase(ctx, queries, epic, question, choice, decision.Seq, at)
	if err != nil {
		return EpicDecision{}, err
	}
	err = endParkByAnswer(ctx, queries, epic, answered, choice)
	if err != nil {
		return EpicDecision{}, err
	}
	err = answerGapsQuestion(ctx, queries, epic, id, text, answered, choice, at)
	if err != nil {
		return EpicDecision{}, err
	}
	return decision, nil
}

// answerMetAtBase carries out choice, answered to the park question with seq question as decision seq, in the caller's
// write, when the question is the park of an evidence run that judged Requirements holding at base. retry reruns the
// parked run at its next attempt. drop marks each Requirement the park found met as dropped by the decision, then reruns
// the run. end sets the phase of epic to met_at_base and leaves the park in place, so the epic stays parked. Any other
// question, and any other choice, changes nothing here.
func answerMetAtBase(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	question int64,
	choice string,
	decision int64,
	at time.Time,
) error {
	met, err := queries.GetEpicMetAtBase(ctx, sqlcgen.GetEpicMetAtBaseParams{Epic: epic, Question: question})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read park question %d of epic %s: %w", question, epic, err)
	}
	switch choice {
	case ChoiceRetry:
		_, err = rerunParkedRun(ctx, queries, epic, met.Path)
		return err
	case ChoiceDrop:
		return dropMetAtBase(ctx, queries, epic, met, decision)
	case ChoiceEnd:
		return setEpicPhase(ctx, queries, epic, phaseMetAtBase, at)
	default:
		return nil
	}
}

// dropMetAtBase marks each Requirement that met names as dropped by the decision with seq decision, then reruns the
// parked run met names at its next attempt, in the caller's write.
func dropMetAtBase(
	ctx context.Context, queries *sqlcgen.Queries, epic string, met sqlcgen.EpicMetAtBase, decision int64,
) error {
	ids, err := decodeList[string](met.Requirements)
	if err != nil {
		return fmt.Errorf("requirements of park question %d of epic %s: %w", met.Question, epic, err)
	}
	for _, id := range ids {
		if err = queries.DropEpicRequirement(ctx, sqlcgen.DropEpicRequirementParams{
			Decision: decision, Epic: epic, ID: id,
		}); err != nil {
			return fmt.Errorf("drop requirement %s of epic %s: %w", id, epic, err)
		}
	}
	_, err = rerunParkedRun(ctx, queries, epic, met.Path)
	return err
}

// endParkByAnswer ends the park of epic in the caller's write when answered, the question an answer has just closed, is
// a park question and epic is not met_at_base. A retry answer first reruns the parked node run that the question names,
// when it names one, and an answer of any other choice reruns nothing. An epic in met_at_base is left parked, as an end
// leaves it.
func endParkByAnswer(
	ctx context.Context, queries *sqlcgen.Queries, epic string, answered sqlcgen.EpicQuestion, choice string,
) error {
	if answered.Turn != 0 || answered.Label != ParkLabel {
		return nil
	}
	if choice == ChoiceRetry {
		if err := rerunParkQuestionRun(ctx, queries, epic, answered.Seq); err != nil {
			return err
		}
	}
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return err
	}
	if head.Phase == phaseMetAtBase {
		return nil
	}
	return endPark(ctx, queries, epic)
}

// OpenQuestions returns the open questions of epic in seq order, nil when none is open.
func (s *Store) OpenQuestions(ctx context.Context, epic string) ([]EpicQuestion, error) {
	rows, err := sqlcgen.New(s.db).ListOpenEpicQuestions(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list open questions of epic %s: %w", epic, err)
	}
	questions, err := convertInOrder(rows, questionOf)
	if err != nil {
		return nil, fmt.Errorf("open questions of epic %s: %w", epic, err)
	}
	return questions, nil
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
// with equal text answered with equal choice, and then decision is that stored row; other text or another choice under
// id is ErrDecisionConflict; found is false when id is not stored. A note answers with choice "".
func replayAnswer(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic, id, text, choice string,
) (EpicDecision, bool, error) {
	previous, err := queries.GetEpicDecisionByClientID(ctx, sqlcgen.GetEpicDecisionByClientIDParams{
		Epic: epic, ClientID: id,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return EpicDecision{}, false, nil
	}
	if err != nil {
		return EpicDecision{}, false, fmt.Errorf("read decision %s on epic %s: %w", id, epic, err)
	}
	stored, err := decisionChoice(ctx, queries, epic, previous)
	if err != nil {
		return EpicDecision{}, false, err
	}
	if previous.Text != text || stored != choice {
		return EpicDecision{}, false, fmt.Errorf("decision %s on epic %s: %w", id, epic, ErrDecisionConflict)
	}
	return epicDecisionOf(previous), true, nil
}

// decisionChoice returns the choice that answered the stored decision previous: "" for a note, and otherwise the choice
// its question records.
func decisionChoice(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	previous sqlcgen.EpicDecision,
) (string, error) {
	if previous.Question == 0 {
		return "", nil
	}
	question, err := queries.GetEpicQuestion(ctx, sqlcgen.GetEpicQuestionParams{Epic: epic, Seq: previous.Question})
	if err != nil {
		return "", fmt.Errorf("read question %d on epic %s: %w", previous.Question, epic, err)
	}
	return question.Choice, nil
}

// requireAnswerable is the refusal of an answer naming choice and text to question seq of epic: ErrNoQuestion when epic
// has no question seq, a *QuestionReplacedError when a later question replaced it, ErrQuestionClosed when it is
// answered, ErrNoChoice when it does not offer choice, and ErrOtherNeedsText when choice needs text it lacks (see
// choiceNeedsText). When the answer is allowed it returns the open question as stored.
func requireAnswerable(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic, text string,
	question int64,
	choice string,
) (sqlcgen.EpicQuestion, error) {
	stored, err := queries.GetEpicQuestion(ctx, sqlcgen.GetEpicQuestionParams{Epic: epic, Seq: question})
	if errors.Is(err, sql.ErrNoRows) {
		return sqlcgen.EpicQuestion{}, fmt.Errorf("question %d on epic %s: %w", question, epic, ErrNoQuestion)
	}
	if err != nil {
		return sqlcgen.EpicQuestion{}, fmt.Errorf("read question %d on epic %s: %w", question, epic, err)
	}
	if stored.State == QuestionReplaced {
		return sqlcgen.EpicQuestion{}, &QuestionReplacedError{Seq: question, By: stored.ReplacedBy}
	}
	if stored.State != QuestionOpen {
		return sqlcgen.EpicQuestion{}, fmt.Errorf("question %d on epic %s: %w", question, epic, ErrQuestionClosed)
	}
	offered, err := offers(stored, choice)
	if err != nil {
		return sqlcgen.EpicQuestion{}, err
	}
	if !offered {
		return sqlcgen.EpicQuestion{}, fmt.Errorf("question %d on epic %s: %w", question, epic, ErrNoChoice)
	}
	if choiceNeedsText(stored, choice) && strings.TrimSpace(text) == "" {
		return sqlcgen.EpicQuestion{}, fmt.Errorf("question %d on epic %s: %w", question, epic, ErrOtherNeedsText)
	}
	return stored, nil
}

// choiceNeedsText reports whether an answer naming choice to question must carry text: other always, and correct to a
// gaps question, whose text is the correction the planner is given.
func choiceNeedsText(question sqlcgen.EpicQuestion, choice string) bool {
	return choice == ChoiceOther || (choice == ChoiceCorrect && isGapsQuestion(question))
}

// offers reports whether question offers choice: other, which every question offers, or one of its declared choices.
func offers(question sqlcgen.EpicQuestion, choice string) (bool, error) {
	if choice == ChoiceOther {
		return true, nil
	}
	declared, err := choicesOf(question.Choices)
	if err != nil {
		return false, fmt.Errorf("choices of question %d on epic %s: %w", question.Seq, question.Epic, err)
	}
	return slices.ContainsFunc(declared, func(offered EpicChoice) bool { return offered.ID == choice }), nil
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
		ID:           head.ID,
		Repository:   head.Repository,
		Base:         head.Base,
		StoredBase:   head.StoredBase,
		Candidate:    head.Candidate,
		Synced:       head.Synced,
		OriginRead:   originReadOf(head),
		Branch:       head.Branch,
		Title:        head.Title,
		Approved:     head.Approved,
		Intent:       head.Intent,
		Care:         head.Care,
		Approach:     head.Approach,
		ReleaseThen:  head.ReleaseThen,
		Checks:       head.Checks,
		Phase:        head.Phase,
		Created:      time.Unix(head.Created, 0),
		Changed:      time.Unix(head.Changed, 0),
		PhaseAt:      time.Unix(head.PhaseAt, 0),
		Paused:       head.Paused != 0,
		Parked:       head.Parked,
		ParkClass:    ParkClass(head.ParkClass),
		ClosedBy:     head.ClosedBy,
		SupersededBy: head.SupersededBy,
		Summary:      head.Summary,
		Workflow: EpicWorkflow{
			Name: head.Workflow, Text: head.WorkflowText, SHA256: head.WorkflowSha256,
		},
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
		DroppedBy: stored.DroppedBy,
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

// reviewerOf returns the reviewer a stored row holds, its lists decoded and its flags read back as booleans.
func reviewerOf(stored sqlcgen.EpicReviewer) (reviewers.Reviewer, error) {
	stage, err := decodeStrings(stored.Stage)
	if err != nil {
		return reviewers.Reviewer{}, fmt.Errorf("reviewer %s stage: %w", stored.Name, err)
	}
	must, err := decodeStrings(stored.Must)
	if err != nil {
		return reviewers.Reviewer{}, fmt.Errorf("reviewer %s must: %w", stored.Name, err)
	}
	mustNot, err := decodeStrings(stored.MustNot)
	if err != nil {
		return reviewers.Reviewer{}, fmt.Errorf("reviewer %s must not: %w", stored.Name, err)
	}
	tools, err := decodeStrings(stored.Tools)
	if err != nil {
		return reviewers.Reviewer{}, fmt.Errorf("reviewer %s tools: %w", stored.Name, err)
	}
	return reviewers.Reviewer{
		Name: stored.Name, Enabled: stored.Enabled != 0, Stage: stage, Must: must, MustNot: mustNot,
		Prompt: stored.Prompt, Agent: stored.Agent, Model: stored.Model, Tools: tools, Effort: stored.Effort,
		Limit: stored.LimitSeconds, Verify: stored.Verify != 0,
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
