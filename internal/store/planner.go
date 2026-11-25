package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrNotClaimable reports that an epic has no turn to claim: it is paused or parked, it has a running turn, or it has
// no pending trigger.
var ErrNotClaimable = errors.New("epic has no turn to claim")

// ErrTurnNotRunning reports that a turn is not running.
var ErrTurnNotRunning = errors.New("turn is not running")

// ErrBadDependency reports a task of a planner return whose dependency CompleteTurn refuses: an entry that names no
// task of the epic or of the return, a title two tasks of the return share, or the task itself. The error names the
// task's position and the entry, and the turn writes nothing.
var ErrBadDependency = errors.New("bad task dependency")

// TaskKindDesign is the kind of a design task, which designs an interface the epic's other tasks build against. A task
// of the empty kind is an ordinary task.
const TaskKindDesign = "design"

// TaskSplit is the status of a task a split action split into its descendants. The descendants stand in its place.
const TaskSplit = "split"

// EpicPlanState is the planning state of one epic with pending triggers. Parked is the park reason of a question park,
// empty when the epic is not parked or its park is a planner park, which the planner answers. PlannerPark is the park
// reason of that planner park, empty otherwise, so a decision records the park it was made under. Running is true when
// the epic has a turn in state running.
type EpicPlanState struct {
	Epic        string
	Paused      bool
	Parked      string
	PlannerPark string
	Running     bool
	Pending     int
}

// EpicTurn is one planner turn on an epic. Times are stored at whole seconds; Ended is zero while the turn runs.
// Told reports whether the owner was told about the turn. Limited reports whether a free turn was a usage limit, and
// Resets is the usage-limit reset time a free turn stored, zero when it stored none or when the limit named none.
type EpicTurn struct {
	Epic      string
	Seq       int64
	Step      string
	State     string
	Reason    string
	Seen      int64
	Outline   string
	Outcome   string
	Decisions []string
	Started   time.Time
	Ended     time.Time
	Told      bool
	Limited   bool
	Resets    time.Time
}

// EpicTask is one task an epic's planner turn planned: its title, its deliverable, the files it touches, the
// Requirements it covers, the Requirements it contributes to, the test files it changes in the order the planner
// gave them, its gate, its evidence item and its status. Turn is the seq of the turn that planned it.
// Build is its build state, Reviews its current generation's reviews in attempt then position order, and Red and Green the evidence runs
// its build names, nil when it names none or when the run is not current with Evidence. Batch is the path of the
// integrate run that claimed the task, "" while none has. NeedsFiles are the paths beyond its owned Files that its
// implementer named when it failed, in the order it named them. GateChanges are the gate-needed changes its
// implementers declared with a commit the guard passed, one per file in the order first declared. Notes are the notes
// its build holds, in the order they were recorded. Refused are the paths
// an orchestrator decision refused on the task, which no rule grant gives the task again until a decision adds them.
// Kind is the task's kind, "" for an ordinary task or TaskKindDesign. Revises is the seq of the design task the task
// revises, 0 for none. SplitFrom is the seq of the task this task was split from, 0 for none.
type EpicTask struct {
	Seq          int64
	Turn         int64
	Title        string
	Deliverable  string
	Files        []string
	Requirements []string
	Contributes  []string
	TestChanges  []string
	Gate         string
	Evidence     evidence.Item
	Status       string
	Build        TaskBuild
	Reviews      []TaskReview
	Red          *EvidenceRun
	Green        *EvidenceRun
	Batch        string
	NeedsFiles   []string
	GateChanges  []GateChange
	Notes        []string
	Refused      []string
	// Version is the number of the task's current version: its highest applied version, or 1 when none is applied. The
	// Deliverable, TestChanges, Gate and Evidence above are the current version's.
	Version int64
	// Versions are every version stored for the task, applied, checking or refused, in version order.
	Versions []TaskVersion
	// Rungs are the task's ladder rows in rung order, nil for a task that never climbed.
	Rungs []TaskRung
	// Generation is the task's reopen generation: 0 until its first send_back, and one more for each send_back after.
	// Reviews and Rungs are the current generation's alone; the rows of earlier generations stay stored under theirs.
	Generation int64
	// DependsOn are the seqs of the tasks the task depends on, in the order its planner named them, nil for none.
	DependsOn []int64
	Kind      string
	Revises   int64
	SplitFrom int64
}

// EpicQuestion is one question a planner turn asked, with its state. Turn is the seq of the turn that asked it. Label,
// Choices, Recommended, Why, Confidence and Replaces are the question's shape as the turn stored it. ReplacedBy is the
// seq of the question that replaced it, and Choice is the choice that answered it: ChoiceOther for words, "" while it
// is open or replaced. Escalated is when the question was escalated, zero while it is not; EscalationReason and
// EscalationMaker are what that escalation names, and EscalationTold is set once the owner has been told of it.
type EpicQuestion struct {
	Seq              int64
	Turn             int64
	Text             string
	State            string
	Label            string
	Choices          []EpicChoice
	Recommended      string
	Why              string
	Confidence       string
	Replaces         int64
	ReplacedBy       int64
	Choice           string
	Escalated        time.Time
	EscalationReason string
	EscalationMaker  Maker
	EscalationTold   bool
}

// EpicTrigger is one trigger of an epic: its cause, its state, the turn that claimed it (0 while pending) and when it
// was created.
type EpicTrigger struct {
	ID      string
	Cause   string
	State   string
	Turn    int64
	Created time.Time
}

// EpicPlan is the valid return of one planner turn: its decisions, its tasks, its outline, its questions, its outcome, its
// actions and the gaps it cannot resolve without the owner.
type EpicPlan struct {
	Decisions    []string
	Tasks        []EpicTaskInput
	Outline      string
	Questions    []EpicQuestionInput
	Outcome      string
	Actions      []EpicActionInput
	Unresolvable []GapDeclaration
}

// EpicTaskInput is one task of a planner return: its title, its deliverable, the files it touches, the Requirements
// it covers, the Requirements it contributes to, the test files it changes, its gate, its evidence item, its kind and
// the tasks it depends on. Kind is "" or TaskKindDesign; CompleteTurn stores it as given, and the store refuses any
// other kind.
type EpicTaskInput struct {
	Title        string
	Deliverable  string
	Files        []string
	Requirements []string
	Contributes  []string
	TestChanges  []string
	Gate         string
	Evidence     evidence.Item
	Kind         string
	// DependsOn names the tasks this task depends on, in order. An entry of the form T<n>, with n >= 1 and no leading
	// zero, names the task of the epic with seq n, which must already be stored. Any other entry names the task of the
	// return with that exact title, so a title is never T<n>. A title two tasks of the return share names neither task,
	// and an entry naming the task itself is refused. CompleteTurn returns ErrBadDependency for a refused entry.
	DependsOn []string
}

// PlanStates returns one entry per epic with at least one pending trigger, ordered by epic id.
func (s *Store) PlanStates(ctx context.Context) ([]EpicPlanState, error) {
	rows, err := sqlcgen.New(s.read).ListEpicPlanStates(ctx)
	if err != nil {
		return nil, fmt.Errorf("list plan states: %w", err)
	}
	return mapInOrder(rows, func(state sqlcgen.ListEpicPlanStatesRow) EpicPlanState {
		return EpicPlanState{
			Epic:        state.ID,
			Paused:      state.Paused != 0,
			Parked:      questionPark(state.Parked, state.ParkClass),
			PlannerPark: plannerPark(state.Parked, state.ParkClass),
			Running:     state.Running != 0,
			Pending:     int(state.Pending),
		}
	}), nil
}

// RunningTurns returns every running turn of every epic, ordered by epic then seq, each with its decisions.
func (s *Store) RunningTurns(ctx context.Context) ([]EpicTurn, error) {
	queries := sqlcgen.New(s.read)
	rows, err := queries.ListRunningEpicTurns(ctx)
	if err != nil {
		return nil, fmt.Errorf("list running turns: %w", err)
	}
	var turns []EpicTurn
	for _, row := range rows {
		var turn EpicTurn
		if turn, err = turnOf(ctx, queries, row); err != nil {
			return nil, fmt.Errorf("turn %d of epic %s: %w", row.Seq, row.Epic, err)
		}
		turns = append(turns, turn)
	}
	return turns, nil
}

// TurnTriggers returns the triggers of epic whose turn is seq, ordered by id. A turn with none is nil.
func (s *Store) TurnTriggers(ctx context.Context, epic string, turn int64) ([]EpicTrigger, error) {
	rows, err := sqlcgen.New(s.read).ListEpicTurnTriggers(ctx, sqlcgen.ListEpicTurnTriggersParams{
		Epic: epic, Turn: turn,
	})
	if err != nil {
		return nil, fmt.Errorf("list triggers of turn %d of epic %s: %w", turn, epic, err)
	}
	return mapInOrder(rows, triggerOf), nil
}

// loadPlanSections fills the turns, tasks and questions of row from the rows stored under row.ID, each in seq order.
// A task carries its files, Requirements and contributions in position order.
func loadPlanSections(ctx context.Context, queries *sqlcgen.Queries, row *EpicRow) error {
	turns, err := queries.ListEpicTurns(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list turns: %w", err)
	}
	for _, stored := range turns {
		var turn EpicTurn
		if turn, err = turnOf(ctx, queries, stored); err != nil {
			return fmt.Errorf("turn %d: %w", stored.Seq, err)
		}
		row.Turns = append(row.Turns, turn)
	}

	tasks, err := queries.ListEpicTasks(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list tasks: %w", err)
	}
	files, err := queries.ListEpicTaskFiles(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list task files: %w", err)
	}
	requirements, err := queries.ListEpicTaskRequirements(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list task requirements: %w", err)
	}
	contributes, err := queries.ListEpicTaskContributes(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list task contributes: %w", err)
	}
	testChanges, err := queries.ListEpicTaskTestChanges(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list task test changes: %w", err)
	}
	reviewsOf, err := storedTaskReviews(ctx, queries, row.ID)
	if err != nil {
		return err
	}
	pathsOf := groupByTask(files, func(file sqlcgen.EpicTaskFile) int64 { return file.Task },
		func(file sqlcgen.EpicTaskFile) string { return file.Path })
	requirementsOf := groupByTask(requirements, func(requirement sqlcgen.EpicTaskRequirement) int64 {
		return requirement.Task
	}, func(requirement sqlcgen.EpicTaskRequirement) string { return requirement.Requirement })
	contributesOf := groupByTask(contributes, func(contribute sqlcgen.EpicTaskContribute) int64 {
		return contribute.Task
	}, func(contribute sqlcgen.EpicTaskContribute) string { return contribute.Requirement })
	testChangesOf := groupByTask(testChanges, func(change sqlcgen.EpicTaskTestChange) int64 { return change.Task },
		func(change sqlcgen.EpicTaskTestChange) string { return change.Path })
	versions, err := queries.ListEpicTaskVersions(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list task versions: %w", err)
	}
	versionsOf, err := taskVersionsOf(versions)
	if err != nil {
		return fmt.Errorf("task versions: %w", err)
	}
	rungsOf, refusedOf, err := taskLadderOf(ctx, queries, row.ID)
	if err != nil {
		return err
	}
	row.Tasks, err = convertInOrder(tasks, func(task sqlcgen.EpicTask) (EpicTask, error) {
		epicTask, taskErr := taskOf(ctx, queries, task, pathsOf[task.Seq], requirementsOf[task.Seq],
			contributesOf[task.Seq], testChangesOf[task.Seq], refusedOf[task.Seq], reviewsOf[task.Seq],
			versionsOf[task.Seq])
		epicTask.Rungs = rungsOf[task.Seq]
		return epicTask, taskErr
	})
	if err != nil {
		return fmt.Errorf("tasks: %w", err)
	}

	return loadQuestions(ctx, queries, row)
}

// storedTaskReviews returns the reviews of each task of epic, keyed by task seq, from the rows stored under epic: each
// review with its findings and test runs, as taskReviewsOf gives them.
func storedTaskReviews(ctx context.Context, queries *sqlcgen.Queries, epic string) (map[int64][]TaskReview, error) {
	reviews, err := queries.ListEpicTaskReviews(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list task reviews: %w", err)
	}
	findings, err := queries.ListEpicTaskFindings(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list task findings: %w", err)
	}
	testRuns, err := queries.ListEpicTaskTestRuns(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list task test runs: %w", err)
	}
	return taskReviewsOf(reviews, findings, testRuns), nil
}

// taskLadderOf returns the rungs each task of epic holds, and the files orchestrator decisions refused on each, keyed
// by task.
func taskLadderOf(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
) (map[int64][]TaskRung, map[int64][]string, error) {
	rungs, err := queries.ListEpicTaskRungs(ctx, epic)
	if err != nil {
		return nil, nil, fmt.Errorf("list task rungs: %w", err)
	}
	refused, err := queries.ListEpicTaskRefused(ctx, epic)
	if err != nil {
		return nil, nil, fmt.Errorf("list task refused files: %w", err)
	}
	refusedOf := groupByTask(refused, func(file sqlcgen.EpicTaskRefused) int64 { return file.Task },
		func(file sqlcgen.EpicTaskRefused) string { return file.Path })
	return taskRungsOf(rungs), refusedOf, nil
}

// loadQuestions fills the questions of row from the rows stored under row.ID, in seq order.
func loadQuestions(ctx context.Context, queries *sqlcgen.Queries, row *EpicRow) error {
	questions, err := queries.ListEpicQuestions(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list questions: %w", err)
	}
	row.Questions, err = convertInOrder(questions, questionOf)
	if err != nil {
		return fmt.Errorf("questions: %w", err)
	}
	return nil
}

// groupByTask returns the value of each row keyed by the task the row belongs to, each task's values in row order.
func groupByTask[R any](rows []R, task func(R) int64, value func(R) string) map[int64][]string {
	out := make(map[int64][]string)
	for _, row := range rows {
		out[task(row)] = append(out[task(row)], value(row))
	}
	return out
}

// taskOf returns the task a stored row holds, with the files, Requirements, contributions and reviews stored under it,
// its build state, and the evidence runs its build names as red and green. Its Deliverable, TestChanges, Gate and
// Evidence are the stored row's, overlaid with each applied version in version order, so they are the current version's
// and Version is that version's number. Versions lists every stored version, and a checking or refused one is never
// overlaid. Each run is kept only while its spec is current with the task's evidence item.
func taskOf(
	ctx context.Context,
	queries *sqlcgen.Queries,
	stored sqlcgen.EpicTask,
	files, requirements, contributes, testChanges, refused []string,
	reviews []TaskReview,
	versions []TaskVersion,
) (EpicTask, error) {
	item, err := decodeItem(stored.Evidence)
	if err != nil {
		return EpicTask{}, fmt.Errorf("task %d evidence: %w", stored.Seq, err)
	}
	needsFiles, err := decodeList[string](stored.NeedsFiles)
	if err != nil {
		return EpicTask{}, fmt.Errorf("task %d needs files: %w", stored.Seq, err)
	}
	gateChanges, err := decodeList[GateChange](stored.GateChanges)
	if err != nil {
		return EpicTask{}, fmt.Errorf("task %d gate changes: %w", stored.Seq, err)
	}
	dependsOn, err := decodeSeqs(stored.DependsOn)
	if err != nil {
		return EpicTask{}, fmt.Errorf("task %d depends on: %w", stored.Seq, err)
	}
	notes, err := decodeList[string](stored.Notes)
	if err != nil {
		return EpicTask{}, fmt.Errorf("task %d notes: %w", stored.Seq, err)
	}
	task := EpicTask{
		Seq: stored.Seq, Turn: stored.Turn, Title: stored.Title, Deliverable: stored.Deliverable,
		Files: files, Requirements: requirements, Contributes: contributes, TestChanges: testChanges,
		Gate: stored.Gate, Evidence: item,
		Status: stored.Status, Build: buildOf(stored), Reviews: reviews, Batch: stored.Batch,
		NeedsFiles: needsFiles, GateChanges: gateChanges, Notes: notes,
		Refused: refused, Version: 1, Versions: versions, Generation: stored.Generation, DependsOn: dependsOn,
		Kind: stored.Kind, Revises: stored.Revises, SplitFrom: stored.SplitFrom,
	}
	overlayVersions(&task)
	if task.Build.RedRun != "" {
		run, runErr := taskRunOf(ctx, queries, task.Build.RedRun)
		if runErr != nil {
			return EpicTask{}, fmt.Errorf("task %d red run: %w", stored.Seq, runErr)
		}
		if evidence.Current(run.Spec, task.Evidence) {
			task.Red = &run
		}
	}
	if task.Build.GreenRun != "" {
		run, runErr := taskRunOf(ctx, queries, task.Build.GreenRun)
		if runErr != nil {
			return EpicTask{}, fmt.Errorf("task %d green run: %w", stored.Seq, runErr)
		}
		if evidence.Current(run.Spec, task.Evidence) {
			task.Green = &run
		}
	}
	return task, nil
}

// overlayVersions sets the Deliverable, TestChanges, Gate and Evidence of task to those of each applied version in its
// Versions, in version order, and its Version to the number of the last one. A checking or refused version is not
// overlaid.
func overlayVersions(task *EpicTask) {
	for _, version := range task.Versions {
		if version.State != VersionApplied {
			continue
		}
		task.Version = version.Version
		if version.Deliverable != nil {
			task.Deliverable = *version.Deliverable
		}
		if version.TestChanges != nil {
			task.TestChanges = *version.TestChanges
		}
		if version.Gate != nil {
			task.Gate = *version.Gate
		}
		if version.Evidence != nil {
			task.Evidence = *version.Evidence
		}
	}
}

// currentSpecOf returns the spec of stored, a task row of epic, as its current version leaves it, in the caller's write:
// its Files as the task owns them, and its Gate, TestChanges and Evidence as overlayVersions sets them from the
// applied versions, as taskOf reads them.
func currentSpecOf(
	ctx context.Context, queries *sqlcgen.Queries, epic string, stored sqlcgen.EpicTask,
) (EpicTask, error) {
	item, err := decodeItem(stored.Evidence)
	if err != nil {
		return EpicTask{}, fmt.Errorf("task %d evidence: %w", stored.Seq, err)
	}
	files, err := ownedFiles(ctx, queries, epic, stored.Seq)
	if err != nil {
		return EpicTask{}, err
	}
	changes, err := queries.ListEpicTaskTestChanges(ctx, epic)
	if err != nil {
		return EpicTask{}, fmt.Errorf("list task test changes: %w", err)
	}
	rows, err := queries.ListEpicTaskVersions(ctx, epic)
	if err != nil {
		return EpicTask{}, fmt.Errorf("list task versions: %w", err)
	}
	versions, err := taskVersionsOf(rows)
	if err != nil {
		return EpicTask{}, fmt.Errorf("task versions: %w", err)
	}
	testChanges := groupByTask(changes, func(change sqlcgen.EpicTaskTestChange) int64 { return change.Task },
		func(change sqlcgen.EpicTaskTestChange) string { return change.Path })
	task := EpicTask{
		Files: files, TestChanges: testChanges[stored.Seq], Gate: stored.Gate, Evidence: item,
		Versions: versions[stored.Seq],
	}
	overlayVersions(&task)
	return task, nil
}

// taskRunOf returns the evidence run with id. The id names a run that must be stored: its absence is an error.
func taskRunOf(ctx context.Context, queries *sqlcgen.Queries, id string) (EvidenceRun, error) {
	stored, err := queries.GetEvidenceRun(ctx, id)
	if err != nil {
		return EvidenceRun{}, fmt.Errorf("get evidence run %s: %w", id, err)
	}
	return evidenceRunOf(ctx, queries, stored)
}

// questionOf returns the question a stored row holds, its choices decoded from their JSON.
func questionOf(question sqlcgen.EpicQuestion) (EpicQuestion, error) {
	choices, err := choicesOf(question.Choices)
	if err != nil {
		return EpicQuestion{}, fmt.Errorf("choices of question %d: %w", question.Seq, err)
	}
	var escalated time.Time
	if question.Escalated != 0 {
		escalated = time.Unix(question.Escalated, 0)
	}
	return EpicQuestion{
		Seq: question.Seq, Turn: question.Turn, Text: question.Text, State: question.State,
		Label: question.Label, Choices: choices, Recommended: question.Recommended, Why: question.Why,
		Confidence: question.Confidence, Replaces: question.Replaces, ReplacedBy: question.ReplacedBy,
		Choice:    question.Choice,
		Escalated: escalated, EscalationReason: question.EscalationReason,
		EscalationMaker: Maker{By: question.EscalationMaker, Session: question.EscalationSession},
		EscalationTold:  question.EscalationTold != 0,
	}, nil
}

// turnOf returns the stored turn with its decisions in position order. Ended and Resets are zero while the turn runs
// or when it stored no reset, and Limited is set when it stored a reset or a limit with none.
func turnOf(ctx context.Context, queries *sqlcgen.Queries, stored sqlcgen.EpicTurn) (EpicTurn, error) {
	decisions, err := queries.ListEpicTurnDecisionsOf(ctx, sqlcgen.ListEpicTurnDecisionsOfParams{
		Epic: stored.Epic, Turn: stored.Seq,
	})
	if err != nil {
		return EpicTurn{}, fmt.Errorf("list decisions: %w", err)
	}
	turn := EpicTurn{
		Epic: stored.Epic, Seq: stored.Seq, Step: stored.Step, State: stored.State, Reason: stored.Reason,
		Seen: stored.Seen, Outline: stored.Outline, Outcome: stored.Outcome,
		Decisions: mapInOrder(decisions, func(decision sqlcgen.EpicTurnDecision) string { return decision.Text }),
		Started:   time.Unix(stored.Started, 0),
		Told:      stored.Told != 0,
	}
	if stored.Ended != 0 {
		turn.Ended = time.Unix(stored.Ended, 0)
	}
	if stored.Resets != 0 {
		turn.Limited = true
	}
	if stored.Resets > 0 {
		turn.Resets = time.Unix(stored.Resets, 0)
	}
	return turn, nil
}

// triggerOf returns the trigger a stored row holds.
func triggerOf(trigger sqlcgen.EpicTrigger) EpicTrigger {
	return EpicTrigger{
		ID: trigger.ID, Cause: trigger.Cause, State: trigger.State, Turn: trigger.Turn,
		Created: time.Unix(trigger.Created, 0),
	}
}

// ClaimTurn starts a running turn on epic in one write and claims every pending trigger of the epic for it. The turn's
// seq is the epic's highest turn seq plus one, and it sees the epic's highest decision seq. An unknown epic is
// ErrNoEpic. A paused or parked epic, one with a running turn, or one with no pending trigger is ErrNotClaimable and
// the write changes nothing.
func (s *Store) ClaimTurn(ctx context.Context, epic string, at time.Time) (EpicTurn, error) {
	var turn EpicTurn
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := checkClaimable(ctx, queries, epic); err != nil {
			return err
		}
		var err error
		turn, err = startTurn(ctx, queries, epic, at)
		return err
	})
	if err != nil {
		return EpicTurn{}, err
	}
	return turn, nil
}

// checkClaimable returns ErrNoEpic for an unknown epic, and ErrNotClaimable when the epic is closed, paused or question
// parked, has a running turn, or has no pending trigger. A planner park leaves the epic claimable.
func checkClaimable(ctx context.Context, queries *sqlcgen.Queries, epic string) error {
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return err
	}
	if IsClosedPhase(head.Phase) {
		return fmt.Errorf("epic %s is closed: %w", epic, ErrNotClaimable)
	}
	if head.Paused != 0 || questionPark(head.Parked, head.ParkClass) != "" {
		return fmt.Errorf("epic %s is paused or parked: %w", epic, ErrNotClaimable)
	}
	running, err := queries.CountRunningEpicTurns(ctx, epic)
	if err != nil {
		return fmt.Errorf("count running turns of epic %s: %w", epic, err)
	}
	if running > 0 {
		return fmt.Errorf("epic %s has a running turn: %w", epic, ErrNotClaimable)
	}
	pending, err := queries.CountPendingEpicTriggers(ctx, epic)
	if err != nil {
		return fmt.Errorf("count pending triggers of epic %s: %w", epic, err)
	}
	if pending == 0 {
		return fmt.Errorf("epic %s has no pending trigger: %w", epic, ErrNotClaimable)
	}
	return nil
}

// startTurn inserts the next running turn of epic, seen at the epic's highest decision seq, and claims the epic's
// pending triggers for it.
func startTurn(ctx context.Context, queries *sqlcgen.Queries, epic string, at time.Time) (EpicTurn, error) {
	highest, err := queries.MaxEpicTurnSeq(ctx, epic)
	if err != nil {
		return EpicTurn{}, fmt.Errorf("read turn count of epic %s: %w", epic, err)
	}
	seen, err := queries.MaxEpicDecisionSeq(ctx, epic)
	if err != nil {
		return EpicTurn{}, fmt.Errorf("read decision count of epic %s: %w", epic, err)
	}
	seq := highest + 1
	if err = queries.InsertEpicTurn(ctx, sqlcgen.InsertEpicTurnParams{
		Epic: epic, Seq: seq, Seen: seen, Started: at.Unix(),
	}); err != nil {
		return EpicTurn{}, fmt.Errorf("insert turn %d of epic %s: %w", seq, epic, err)
	}
	if err = queries.ClaimEpicTriggers(ctx, sqlcgen.ClaimEpicTriggersParams{Turn: seq, Epic: epic}); err != nil {
		return EpicTurn{}, fmt.Errorf("claim triggers of epic %s for turn %d: %w", epic, seq, err)
	}
	return EpicTurn{Epic: epic, Seq: seq, State: "running", Seen: seen, Started: time.Unix(at.Unix(), 0)}, nil
}

// CompleteTurn stores plan as the return of the running turn seq of epic in one write: its decisions, its tasks with
// their files and Requirements, its outline, its questions, its outcome, its actions, which are stored pending, and the
// gaps it cannot resolve, which are stored in order. The turn succeeds ended at at, and every trigger the turn claimed
// is consumed. When epic has a running plan run whose pinned workflow holds a build node
// under the plan, each task also gets a running build child run under that node in the same write. A turn that is not
// running, or does not exist, is ErrTurnNotRunning and writes nothing. A question of the plan that breaks a rule of
// QuestionRefusals, or replaces a question epic does not hold, is ErrBadQuestion, and the call writes nothing. A task
// whose DependsOn names no task of the epic or of the plan, a title two of its tasks share, or the task itself is
// ErrBadDependency, and the call writes nothing. A gap declaration whose recommendation is not release or correct also
// writes nothing.
func (s *Store) CompleteTurn(ctx context.Context, epic string, turn int64, plan EpicPlan, at time.Time) error {
	if err := checkQuestions(plan.Questions); err != nil {
		return err
	}
	if err := checkGapDeclarations(plan.Unresolvable); err != nil {
		return err
	}
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		rows, err := queries.CompleteEpicTurn(ctx, sqlcgen.CompleteEpicTurnParams{
			Outline: plan.Outline, Outcome: plan.Outcome, Ended: at.Unix(), Epic: epic, Seq: turn,
		})
		if err != nil {
			return fmt.Errorf("complete turn %d of epic %s: %w", turn, epic, err)
		}
		if rows == 0 {
			return fmt.Errorf("turn %d of epic %s: %w", turn, epic, ErrTurnNotRunning)
		}
		if err = insertInOrder(plan.Decisions, func(position int64, text string) error {
			return queries.InsertEpicTurnDecision(ctx, sqlcgen.InsertEpicTurnDecisionParams{
				Epic: epic, Turn: turn, Position: position, Text: text,
			})
		}); err != nil {
			return fmt.Errorf("decisions of turn %d of epic %s: %w", turn, epic, err)
		}
		fan, err := planFanOutOf(ctx, queries, epic)
		if err != nil {
			return fmt.Errorf("build node of turn %d of epic %s: %w", turn, epic, err)
		}
		if err = insertTasks(ctx, queries, epic, turn, plan.Tasks, fan, at); err != nil {
			return fmt.Errorf("tasks of turn %d of epic %s: %w", turn, epic, err)
		}
		if err = insertQuestions(ctx, queries, epic, turn, plan.Questions); err != nil {
			return fmt.Errorf("questions of turn %d of epic %s: %w", turn, epic, err)
		}
		if err = insertActions(ctx, queries, epic, turn, plan.Actions); err != nil {
			return fmt.Errorf("actions of turn %d of epic %s: %w", turn, epic, err)
		}
		if err = insertUnresolvable(ctx, queries, epic, turn, plan.Unresolvable); err != nil {
			return fmt.Errorf("unresolvable gaps of turn %d of epic %s: %w", turn, epic, err)
		}
		if err = queries.ConsumeEpicTriggers(ctx, sqlcgen.ConsumeEpicTriggersParams{
			Epic: epic, Turn: turn,
		}); err != nil {
			return fmt.Errorf("consume triggers of turn %d of epic %s: %w", turn, epic, err)
		}
		return nil
	})
}

// taskFanOut is the build node a turn's tasks fan out under: the path and Round of the running plan run, and the id of
// the build node the plan's workflow holds under it. The zero taskFanOut has no build node, and its turn stores no
// child runs.
type taskFanOut struct {
	plan, build string
	round       int64
}

// planFanOutOf returns the build node the tasks of a turn on epic fan out under. It is the first build node in
// position order whose parent is the node of the epic's running plan run, and the zero taskFanOut when the epic has no
// running plan run or the plan holds no build node.
func planFanOutOf(ctx context.Context, queries *sqlcgen.Queries, epic string) (taskFanOut, error) {
	runs, err := queries.ListEpicNodeRuns(ctx, epic)
	if err != nil {
		return taskFanOut{}, fmt.Errorf("list node runs of epic %s: %w", epic, err)
	}
	index := slices.IndexFunc(runs, func(run sqlcgen.EpicNodeRun) bool {
		return run.Kind == "plan" && run.Task == 0 && run.State == NodeRunning
	})
	if index < 0 {
		return taskFanOut{}, nil
	}
	plan := runs[index]
	nodes, err := queries.ListEpicWorkflowNodes(ctx, epic)
	if err != nil {
		return taskFanOut{}, fmt.Errorf("list workflow nodes of epic %s: %w", epic, err)
	}
	for _, node := range nodes {
		if node.Parent == plan.Node && node.Kind == "build" {
			return taskFanOut{plan: plan.Path, build: node.ID, round: plan.Round}, nil
		}
	}
	return taskFanOut{}, nil
}

// insertTasks stores tasks as the next task seqs of epic after its highest, each with its files, Requirements,
// contributions and dependencies in order. Each dependency is resolved before anything is written, so a refused one
// stores nothing. When fan has a build node, each task also gets a running build child run under the plan run
// at fan.plan, with an id from its turn and its 1-based position in the turn, started at at.
func insertTasks(ctx context.Context, queries *sqlcgen.Queries, epic string, turn int64, tasks []EpicTaskInput,
	fan taskFanOut, at time.Time,
) error {
	highest, err := queries.MaxEpicTaskSeq(ctx, epic)
	if err != nil {
		return fmt.Errorf("read task count of epic %s: %w", epic, err)
	}
	dependsOn, err := resolveDependencies(ctx, queries, epic, highest, tasks)
	if err != nil {
		return err
	}
	return insertInOrder(tasks, func(taskPosition int64, task EpicTaskInput) error {
		seq := highest + taskPosition
		item, encodeErr := encodeItem(task.Evidence)
		if encodeErr != nil {
			return fmt.Errorf("evidence of task %d of epic %s: %w", seq, epic, encodeErr)
		}
		if err = queries.InsertEpicTask(ctx, sqlcgen.InsertEpicTaskParams{
			Epic: epic, Seq: seq, Turn: turn, Title: task.Title, Deliverable: task.Deliverable,
			Gate: task.Gate, Evidence: item, DependsOn: encodeSeqs(dependsOn[taskPosition-1]),
			Kind: task.Kind, Revises: 0,
		}); err != nil {
			return fmt.Errorf("insert task %d of epic %s: %w", seq, epic, err)
		}
		if err = insertInOrder(task.Files, func(position int64, path string) error {
			return queries.InsertEpicTaskFile(ctx, sqlcgen.InsertEpicTaskFileParams{
				Epic: epic, Task: seq, Position: position, Path: path,
			})
		}); err != nil {
			return fmt.Errorf("files of task %d of epic %s: %w", seq, epic, err)
		}
		if err = insertInOrder(task.Requirements, func(position int64, requirement string) error {
			return queries.InsertEpicTaskRequirement(ctx, sqlcgen.InsertEpicTaskRequirementParams{
				Epic: epic, Task: seq, Position: position, Requirement: requirement,
			})
		}); err != nil {
			return fmt.Errorf("requirements of task %d of epic %s: %w", seq, epic, err)
		}
		if err = insertInOrder(task.Contributes, func(position int64, requirement string) error {
			return queries.InsertEpicTaskContribute(ctx, sqlcgen.InsertEpicTaskContributeParams{
				Epic: epic, Task: seq, Position: position, Requirement: requirement,
			})
		}); err != nil {
			return fmt.Errorf("contributes of task %d of epic %s: %w", seq, epic, err)
		}
		if err = insertInOrder(task.TestChanges, func(position int64, path string) error {
			return queries.InsertEpicTaskTestChange(ctx, sqlcgen.InsertEpicTaskTestChangeParams{
				Epic: epic, Task: seq, Position: position, Path: path,
			})
		}); err != nil {
			return fmt.Errorf("test changes of task %d of epic %s: %w", seq, epic, err)
		}
		if fan.build == "" {
			return nil
		}
		path := fmt.Sprintf("%s/%s/T%d", fan.plan, fan.build, seq)
		cause := fmt.Sprintf("turn %d task %d", turn, taskPosition)
		if err = queries.InsertEpicNodeRun(ctx, sqlcgen.InsertEpicNodeRunParams{
			Epic: epic, Path: path, ID: NodeRunID(epic, path, fan.round, cause), Node: fan.build, Kind: "build",
			Task: seq, Round: fan.round, State: NodeRunning, Started: at.Unix(),
		}); err != nil {
			return fmt.Errorf("build child run of task %d of epic %s: %w", seq, epic, err)
		}
		return nil
	})
}

// taskRef matches a dependency entry that names a task by seq: T<n> with n >= 1 and no leading zero.
var taskRef = regexp.MustCompile(`^T[1-9][0-9]*$`)

// resolveDependencies returns, for each task of tasks, the seqs of the tasks it depends on, in the order it names them.
// The tasks of the return take the seqs after highest in position order, as insertTasks gives them. The first entry
// resolveDependency refuses is returned as an ErrBadDependency error that names the task's position and the entry.
func resolveDependencies(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	highest int64,
	tasks []EpicTaskInput,
) ([][]int64, error) {
	resolved := make([][]int64, len(tasks))
	for i, task := range tasks {
		for _, entry := range task.DependsOn {
			seq, err := resolveDependency(ctx, queries, epic, highest, tasks, i, entry)
			if err != nil {
				return nil, fmt.Errorf("task at position %d depends on %q: %w", i+1, entry, err)
			}
			resolved[i] = append(resolved[i], seq)
		}
	}
	return resolved, nil
}

// resolveDependency returns the seq of the task entry names, from task i of tasks. An entry of the form T<n> names the
// task of epic with seq n, which must be stored already and must not be split: a split task never integrates, so a
// task that waits for it would wait forever. Any other entry names the one task of tasks with that exact title. An
// entry that names no such task, a split task, a title two tasks of tasks share, or the task itself is
// ErrBadDependency.
func resolveDependency(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	highest int64,
	tasks []EpicTaskInput,
	i int,
	entry string,
) (int64, error) {
	if taskRef.MatchString(entry) {
		seq, err := strconv.ParseInt(entry[1:], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%w: names no task of the epic", ErrBadDependency)
		}
		if seq == highest+int64(i)+1 {
			return 0, fmt.Errorf("%w: names the task itself", ErrBadDependency)
		}
		row, err := queries.GetEpicTask(ctx, sqlcgen.GetEpicTaskParams{Epic: epic, Seq: seq})
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return 0, fmt.Errorf("%w: names no task of the epic", ErrBadDependency)
		case err != nil:
			return 0, fmt.Errorf("read task %d of epic %s: %w", seq, epic, err)
		case row.Status == TaskSplit:
			return 0, fmt.Errorf("%w: names a split task", ErrBadDependency)
		}
		return seq, nil
	}
	var named []int
	for j, task := range tasks {
		if task.Title == entry {
			named = append(named, j)
		}
	}
	switch {
	case len(named) == 0:
		return 0, fmt.Errorf("%w: names no task of the return", ErrBadDependency)
	case len(named) > 1:
		return 0, fmt.Errorf("%w: names a title two tasks of the return share", ErrBadDependency)
	case named[0] == i:
		return 0, fmt.Errorf("%w: names the task itself", ErrBadDependency)
	}
	return highest + int64(named[0]) + 1, nil
}

// encodeSeqs returns seqs as decimals joined with commas in the order given, and the empty string for none: the
// encoding a task's depends_on holds, as after_tasks holds its build's.
func encodeSeqs(seqs []int64) string {
	parts := make([]string, len(seqs))
	for i, seq := range seqs {
		parts[i] = strconv.FormatInt(seq, 10)
	}
	return strings.Join(parts, ",")
}

// decodeSeqs returns the seqs encodeSeqs joined into encoded, nil for the empty string.
func decodeSeqs(encoded string) ([]int64, error) {
	if encoded == "" {
		return nil, nil
	}
	var seqs []int64
	for _, part := range strings.Split(encoded, ",") {
		seq, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("seq %q: %w", part, err)
		}
		seqs = append(seqs, seq)
	}
	return seqs, nil
}

// insertQuestions stores questions as the next question seqs of epic after its highest, each open, with its label,
// choices, recommendation and replacement, and closes each question a question replaces.
func insertQuestions(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	turn int64,
	questions []EpicQuestionInput,
) error {
	highest, err := queries.MaxEpicQuestionSeq(ctx, epic)
	if err != nil {
		return fmt.Errorf("read question count of epic %s: %w", epic, err)
	}
	return insertInOrder(questions, func(position int64, question EpicQuestionInput) error {
		seq := highest + position
		choices, encodeErr := json.Marshal(question.Choices)
		if encodeErr != nil {
			return fmt.Errorf("choices of question %d of epic %s: %w", seq, epic, encodeErr)
		}
		replaces, ok := replacesSeq(question.Replaces)
		if !ok {
			return fmt.Errorf("replaces of question %d of epic %s: %q is not Q<n>", seq, epic, question.Replaces)
		}
		if replaces != 0 {
			if replaceErr := replaceQuestion(ctx, queries, epic, replaces, seq); replaceErr != nil {
				return fmt.Errorf("question %q: %w", question.Label, replaceErr)
			}
		}
		return queries.InsertEpicQuestion(withEpicEvent(ctx), sqlcgen.InsertEpicQuestionParams{
			Epic: epic, Seq: seq, Turn: turn, Text: question.Text, Label: question.Label, Choices: string(choices),
			Recommended: question.Recommended, Why: question.Why, Confidence: question.Confidence, Replaces: replaces,
		})
	})
}

// SetTurnStep records step as the step of the running turn seq of epic in one write. A turn that is not running, or
// does not exist, is ErrTurnNotRunning.
func (s *Store) SetTurnStep(ctx context.Context, epic string, turn int64, step string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		rows, err := queries.SetEpicTurnStep(ctx, sqlcgen.SetEpicTurnStepParams{Step: step, Epic: epic, Seq: turn})
		if err != nil {
			return fmt.Errorf("set step of turn %d of epic %s: %w", turn, epic, err)
		}
		if rows == 0 {
			return fmt.Errorf("turn %d of epic %s: %w", turn, epic, ErrTurnNotRunning)
		}
		return nil
	})
}

// TurnReasonMax is the most bytes a turn's reason and an epic's park reason keep.
const TurnReasonMax = 1024

// parkTriggerPrefix starts the id of each pending trigger a planner park adds.
const parkTriggerPrefix = "park:"

// limitWithoutReset is the epic_turns.resets of a usage limit that names no reset time. A reset time is never negative,
// so the stored value marks a limit, and it reads back with a zero Resets.
const limitWithoutReset = -1

// dueKindEpic is the kind of the due row a planner turn's usage limit stores for its epic.
const dueKindEpic = "epic"

// The pause sources an epic records: pauseOwner for a pause its owner makes, and pauseLimit for a pause a usage limit
// makes.
const (
	pauseOwner = "owner"
	pauseLimit = "limit"
)

// TurnFree is how a running turn ends without a return: its Reason, whether it Pauses the epic, the Park reason
// it parks the epic with when not empty, whether it is a usage Limit, and the usage-limit Resets time it stores when
// not zero. DecidedUnder, when not nil, is the park reason the epic held when Park was decided, before the turn was
// claimed, empty for an epic that was not parked: Park is then set only while the epic still holds that park.
type TurnFree struct {
	Reason       string
	Pause        bool
	Park         string
	Limit        bool
	Resets       time.Time
	DecidedUnder *string
}

// storedResetsOf returns the epic_turns.resets that free stores, and false when it stores none. A non-zero Resets is
// stored as its unix second, and a Limit with no Resets as limitWithoutReset.
func storedResetsOf(free TurnFree) (int64, bool) {
	switch {
	case !free.Resets.IsZero():
		return free.Resets.Unix(), true
	case free.Limit:
		return limitWithoutReset, true
	default:
		return 0, false
	}
}

// pauseFreed pauses epic for the turn free ends and records the source of the pause. A turn that is not a Limit
// records pauseOwner. A Limit records pauseLimit and, for a known reset, stores the epic's due row for that reset in
// the same write. A Limit over a pause its owner made, recorded as pauseOwner or with no source, leaves that pause and
// its source as they are and stores no row, since the owner's pause wins over a reset.
func pauseFreed(ctx context.Context, queries *sqlcgen.Queries, epic string, free TurnFree) error {
	if !free.Limit {
		return pauseEpicAs(ctx, queries, epic, pauseOwner)
	}
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return err
	}
	if head.Paused != 0 && head.PauseSource != pauseLimit {
		return nil
	}
	return pauseLimitedEpic(ctx, queries, epic, free.Resets)
}

// pauseLimitedEpic pauses epic with pauseLimit and, when resets is known, stores the epic's due row for that reset.
func pauseLimitedEpic(ctx context.Context, queries *sqlcgen.Queries, epic string, resets time.Time) error {
	if err := pauseEpicAs(ctx, queries, epic, pauseLimit); err != nil {
		return err
	}
	if resets.IsZero() {
		return nil
	}
	return insertDueResume(ctx, queries, dueKindEpic, epic, resets.Unix())
}

// pauseEpicAs pauses epic and records source as its pause source.
func pauseEpicAs(ctx context.Context, queries *sqlcgen.Queries, epic, source string) error {
	if err := queries.PauseEpic(ctx, epic); err != nil {
		return fmt.Errorf("pause epic %s: %w", epic, err)
	}
	if err := queries.SetEpicPauseSource(ctx, sqlcgen.SetEpicPauseSourceParams{
		PauseSource: source, ID: epic,
	}); err != nil {
		return fmt.Errorf("set pause source of epic %s: %w", epic, err)
	}
	return nil
}

// FailTurn ends the running turn seq of epic as failed with reason, cut to TurnReasonMax bytes, and returns the triggers
// it claimed to pending with turn 0, in one write. The same write tells the owner the failure as `planner turn <seq>
// failed: <reason>`. A failed turn parks nothing, so the epic's next turn is claimed as any turn is. A turn that is not
// running, or does not exist, is ErrTurnNotRunning and writes nothing.
func (s *Store) FailTurn(ctx context.Context, epic string, turn int64, reason string, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		stored := capTurnText(reason)
		if err := endTurn(ctx, queries, epic, turn, "failed", stored, at); err != nil {
			return err
		}
		return insertTell(ctx, queries, epic, fmt.Sprintf("planner turn %d failed: %s", turn, stored), at)
	})
}

// FreeTurn ends the running turn seq of epic as free with free.Reason, cut to TurnReasonMax bytes, and returns its
// triggers to pending with turn 0 in one write. The turn stores storedResetsOf(free): a non-zero free.Resets as its
// reset time, and limitWithoutReset for a Limit that names none. Pause pauses the epic as pauseFreed records, and a
// non-empty Park parks it with that reason, unless the park is void (parkVoided): the epic was retried after the turn
// was claimed, or no longer holds the park free.DecidedUnder names. The retry prevails over a park decided before it, so
// the turn ends free and the epic stays unparked. A turn that is not running, or does not exist, is ErrTurnNotRunning
// and writes nothing.
func (s *Store) FreeTurn(ctx context.Context, epic string, turn int64, free TurnFree, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := endTurn(ctx, queries, epic, turn, "free", capTurnText(free.Reason), at); err != nil {
			return err
		}
		if resets, stores := storedResetsOf(free); stores {
			if err := queries.SetEpicTurnResets(ctx, sqlcgen.SetEpicTurnResetsParams{
				Resets: resets, Epic: epic, Seq: turn,
			}); err != nil {
				return fmt.Errorf("set resets of turn %d of epic %s: %w", turn, epic, err)
			}
		}
		if free.Pause {
			if err := pauseFreed(ctx, queries, epic, free); err != nil {
				return err
			}
		}
		if free.Park == "" {
			return nil
		}
		void, err := parkVoided(ctx, queries, epic, turn, free.DecidedUnder)
		if err != nil || void {
			return err
		}
		_, err = setParked(ctx, queries, epic, ParkClassOrchestrator, "", free.Park, at)
		return err
	})
}

// parkVoided reports whether the park that frees turn seq turn of epic is void, in the caller's write. It is void when
// the epic was retried after the turn was claimed: a retry stores the epic's highest turn seq as its retried seq, and a
// claim numbers its turn after the highest, so a retried seq at or past turn is a retry that landed while the turn ran.
// When decided is not nil, it is also void once the epic no longer holds the park *decided it held when the park was
// decided, such as after a retry that landed between that decision and the claim.
func parkVoided(ctx context.Context, queries *sqlcgen.Queries, epic string, turn int64, decided *string) (bool, error) {
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return false, err
	}
	if head.Retried >= turn {
		return true, nil
	}
	return decided != nil && head.Parked != *decided, nil
}

// endTurn ends the running turn seq of epic as state with reason, ended at at, and returns the triggers the turn
// claimed to pending with turn 0. A turn that is not running, or does not exist, is ErrTurnNotRunning.
func endTurn(ctx context.Context, queries *sqlcgen.Queries, epic string, turn int64, state, reason string,
	at time.Time,
) error {
	rows, err := queries.EndEpicTurn(ctx, sqlcgen.EndEpicTurnParams{
		State: state, Reason: reason, Ended: at.Unix(), Epic: epic, Seq: turn,
	})
	if err != nil {
		return fmt.Errorf("end turn %d of epic %s: %w", turn, epic, err)
	}
	if rows == 0 {
		return fmt.Errorf("turn %d of epic %s: %w", turn, epic, ErrTurnNotRunning)
	}
	if err = queries.ReleaseEpicTriggers(ctx, sqlcgen.ReleaseEpicTriggersParams{Epic: epic, Turn: turn}); err != nil {
		return fmt.Errorf("release triggers of turn %d of epic %s: %w", turn, epic, err)
	}
	return nil
}

// setParked parks epic with reason, cut to TurnReasonMax bytes, as class of subject, in the caller's write. A question
// class opens the park question of epic for it, which is final when the epic's phase is met_at_base, so a park of an epic the
// owner ended declares no choice. A planner class is stored by parkPlanner. setParked returns the seq of the park question
// it opens, and 0 when the planner is woken instead. An unknown epic is ErrNoEpic.
func setParked(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	class ParkClass,
	subject string,
	reason string,
	at time.Time,
) (int64, error) {
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return 0, err
	}
	park := capTurnText(reason)
	if !class.OpensQuestion() {
		return parkPlanner(ctx, queries, epic, class, subject, park, at)
	}
	return openPark(ctx, queries, epic, park, class, parkQuestion(park, head.Phase == phaseMetAtBase))
}

// openPark parks epic with park as class and opens question as its park question, in the caller's write. It returns
// the seq of the question.
func openPark(
	ctx context.Context, queries *sqlcgen.Queries, epic, park string, class ParkClass, question EpicQuestionInput,
) (int64, error) {
	if err := setPark(ctx, queries, epic, park, class); err != nil {
		return 0, err
	}
	return insertParkQuestion(ctx, queries, epic, question)
}

// setPark stores park and class as the park reason and park class of epic, in the caller's write.
func setPark(ctx context.Context, queries *sqlcgen.Queries, epic, park string, class ParkClass) error {
	if _, err := queries.SetEpicParked(ctx, sqlcgen.SetEpicParkedParams{
		Parked: park, ParkClass: string(class), ID: epic,
	}); err != nil {
		return fmt.Errorf("park epic %s: %w", epic, err)
	}
	return nil
}

// parkEpicAs parks epic with reason as class of subject, in the caller's write. A question class opens the park question
// and adds reason as a tell that carries it, and the epic's steerer is told of the question. A planner class opens no
// question and adds no tell: it adds the pending trigger that wakes the planner, which carries reason as its cause. A
// planner park that parkPlanner sends to the owner is stored as retry_bound instead: parkPlanner has opened its question
// and added its tell, so no trigger is added.
func parkEpicAs(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	class ParkClass,
	subject string,
	reason string,
	at time.Time,
) error {
	_, err := parkEpicQuestion(ctx, queries, epic, class, subject, reason, at)
	return err
}

// parkEpicQuestion parks epic as parkEpicAs does, in the caller's write, and returns the seq of the park question it
// opens: a question class's question, or the retry_bound question of a planner park parkPlanner sends to the owner. It
// returns 0 when the park opens none, which wakes the planner instead.
func parkEpicQuestion(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	class ParkClass,
	subject string,
	reason string,
	at time.Time,
) (int64, error) {
	park := capTurnText(reason)
	question, err := setParked(ctx, queries, epic, class, subject, park, at)
	if err != nil {
		return 0, err
	}
	if class.OpensQuestion() {
		return question, insertQuestionTell(ctx, queries, epic, park, question, at)
	}
	if question == 0 {
		return 0, insertParkTrigger(ctx, queries, epic, park, at)
	}
	return question, nil
}

// insertParkTrigger adds to epic the pending trigger of a planner park with reason as its cause, in the caller's write.
// Its id is numbered one past the triggers epic has stored, and triggers are never removed, so each park has an id of its
// own.
func insertParkTrigger(ctx context.Context, queries *sqlcgen.Queries, epic, reason string, at time.Time) error {
	stored, err := queries.CountEpicTriggers(ctx, epic)
	if err != nil {
		return fmt.Errorf("count triggers of epic %s: %w", epic, err)
	}
	return insertNodeTrigger(ctx, queries, epic, NodeTrigger{
		ID: fmt.Sprintf("%s%d", parkTriggerPrefix, stored+1), Cause: reason,
	}, at)
}

// capTurnText cuts text to at most TurnReasonMax bytes at a UTF-8 rune start.
func capTurnText(text string) string {
	return capText(text, TurnReasonMax)
}
