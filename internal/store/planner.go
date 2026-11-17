package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrNotClaimable reports that an epic has no turn to claim: it is paused or parked, it has a running turn, or it has
// no pending trigger.
var ErrNotClaimable = errors.New("epic has no turn to claim")

// ErrTurnNotRunning reports that a turn is not running.
var ErrTurnNotRunning = errors.New("turn is not running")

// EpicPlanState is the planning state of one epic with pending triggers. Parked is the park reason of an owner park,
// empty when the epic is not parked or its park is a planner park, which the planner answers. Running is true when the
// epic has a turn in state running.
type EpicPlanState struct {
	Epic    string
	Paused  bool
	Parked  string
	Running bool
	Pending int
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
// Build is its build state, Reviews its reviews in attempt then position order, and Red and Green the evidence runs
// its build names, nil when it names none or when the run is not current with Evidence. Batch is the path of the
// integrate run that claimed the task, "" while none has.
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
}

// EpicQuestion is one question a planner turn asked, with its state. Turn is the seq of the turn that asked it. Label,
// Choices, Recommended, Why, Confidence and Replaces are the question's shape as the turn stored it. ReplacedBy is the
// seq of the question that replaced it, and Choice is the choice that answered it: ChoiceOther for words, "" while it
// is open or replaced.
type EpicQuestion struct {
	Seq         int64
	Turn        int64
	Text        string
	State       string
	Label       string
	Choices     []EpicChoice
	Recommended string
	Why         string
	Confidence  string
	Replaces    int64
	ReplacedBy  int64
	Choice      string
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

// EpicPlan is the valid return of one planner turn: its decisions, its tasks, its outline, its questions, its outcome and
// its actions.
type EpicPlan struct {
	Decisions []string
	Tasks     []EpicTaskInput
	Outline   string
	Questions []EpicQuestionInput
	Outcome   string
	Actions   []EpicActionInput
}

// EpicTaskInput is one task of a planner return: its title, its deliverable, the files it touches, the Requirements
// it covers, the Requirements it contributes to, the test files it changes, its gate and its evidence item.
type EpicTaskInput struct {
	Title        string
	Deliverable  string
	Files        []string
	Requirements []string
	Contributes  []string
	TestChanges  []string
	Gate         string
	Evidence     evidence.Item
}

// PlanStates returns one entry per epic with at least one pending trigger, ordered by epic id.
func (s *Store) PlanStates(ctx context.Context) ([]EpicPlanState, error) {
	rows, err := sqlcgen.New(s.db).ListEpicPlanStates(ctx)
	if err != nil {
		return nil, fmt.Errorf("list plan states: %w", err)
	}
	return mapInOrder(rows, func(state sqlcgen.ListEpicPlanStatesRow) EpicPlanState {
		return EpicPlanState{
			Epic:    state.ID,
			Paused:  state.Paused != 0,
			Parked:  ownedPark(state.Parked, state.ParkClass),
			Running: state.Running != 0,
			Pending: int(state.Pending),
		}
	}), nil
}

// RunningTurns returns every running turn of every epic, ordered by epic then seq, each with its decisions.
func (s *Store) RunningTurns(ctx context.Context) ([]EpicTurn, error) {
	queries := sqlcgen.New(s.db)
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
	rows, err := sqlcgen.New(s.db).ListEpicTurnTriggers(ctx, sqlcgen.ListEpicTurnTriggersParams{
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
	reviews, err := queries.ListEpicTaskReviews(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list task reviews: %w", err)
	}
	findings, err := queries.ListEpicTaskFindings(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list task findings: %w", err)
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
	reviewsOf := taskReviewsOf(reviews, findings)
	row.Tasks, err = convertInOrder(tasks, func(task sqlcgen.EpicTask) (EpicTask, error) {
		return taskOf(ctx, queries, task, pathsOf[task.Seq], requirementsOf[task.Seq], contributesOf[task.Seq],
			testChangesOf[task.Seq], reviewsOf[task.Seq])
	})
	if err != nil {
		return fmt.Errorf("tasks: %w", err)
	}

	return loadQuestions(ctx, queries, row)
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
// its evidence item decoded, its build state, and the evidence runs its build names as red and green, each kept only
// while its spec is current with the evidence item.
func taskOf(
	ctx context.Context,
	queries *sqlcgen.Queries,
	stored sqlcgen.EpicTask,
	files, requirements, contributes, testChanges []string,
	reviews []TaskReview,
) (EpicTask, error) {
	item, err := decodeItem(stored.Evidence)
	if err != nil {
		return EpicTask{}, fmt.Errorf("task %d evidence: %w", stored.Seq, err)
	}
	task := EpicTask{
		Seq: stored.Seq, Turn: stored.Turn, Title: stored.Title, Deliverable: stored.Deliverable,
		Files: files, Requirements: requirements, Contributes: contributes, TestChanges: testChanges,
		Gate: stored.Gate, Evidence: item,
		Status: stored.Status, Build: buildOf(stored), Reviews: reviews, Batch: stored.Batch,
	}
	if task.Build.RedRun != "" {
		run, runErr := taskRunOf(ctx, queries, task.Build.RedRun)
		if runErr != nil {
			return EpicTask{}, fmt.Errorf("task %d red run: %w", stored.Seq, runErr)
		}
		if evidence.Current(run.Spec, item) {
			task.Red = &run
		}
	}
	if task.Build.GreenRun != "" {
		run, runErr := taskRunOf(ctx, queries, task.Build.GreenRun)
		if runErr != nil {
			return EpicTask{}, fmt.Errorf("task %d green run: %w", stored.Seq, runErr)
		}
		if evidence.Current(run.Spec, item) {
			task.Green = &run
		}
	}
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
	return EpicQuestion{
		Seq: question.Seq, Turn: question.Turn, Text: question.Text, State: question.State,
		Label: question.Label, Choices: choices, Recommended: question.Recommended, Why: question.Why,
		Confidence: question.Confidence, Replaces: question.Replaces, ReplacedBy: question.ReplacedBy,
		Choice: question.Choice,
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

// checkClaimable returns ErrNoEpic for an unknown epic, and ErrNotClaimable when the epic is closed, paused or owner
// parked, has a running turn, or has no pending trigger. A planner park leaves the epic claimable.
func checkClaimable(ctx context.Context, queries *sqlcgen.Queries, epic string) error {
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return err
	}
	if IsClosedPhase(head.Phase) {
		return fmt.Errorf("epic %s is closed: %w", epic, ErrNotClaimable)
	}
	if head.Paused != 0 || ownedPark(head.Parked, head.ParkClass) != "" {
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
// their files and Requirements, its outline, its questions, its outcome and its actions, which are stored pending. The
// turn succeeds ended at at, and every trigger the turn claimed is consumed. When epic has a running plan run whose pinned workflow holds a build node
// under the plan, each task also gets a running build child run under that node in the same write. A turn that is not
// running, or does not exist, is ErrTurnNotRunning and writes nothing. A question of the plan that breaks a rule of
// QuestionRefusals, or replaces a question epic does not hold, is ErrBadQuestion, and the call writes nothing.
func (s *Store) CompleteTurn(ctx context.Context, epic string, turn int64, plan EpicPlan, at time.Time) error {
	if err := checkQuestions(plan.Questions); err != nil {
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

// insertTasks stores tasks as the next task seqs of epic after its highest, each with its files, Requirements and
// contributions in order. When fan has a build node, each task also gets a running build child run under the plan run
// at fan.plan, with an id from its turn and its 1-based position in the turn, started at at.
func insertTasks(ctx context.Context, queries *sqlcgen.Queries, epic string, turn int64, tasks []EpicTaskInput,
	fan taskFanOut, at time.Time,
) error {
	highest, err := queries.MaxEpicTaskSeq(ctx, epic)
	if err != nil {
		return fmt.Errorf("read task count of epic %s: %w", epic, err)
	}
	return insertInOrder(tasks, func(taskPosition int64, task EpicTaskInput) error {
		seq := highest + taskPosition
		item, encodeErr := encodeItem(task.Evidence)
		if encodeErr != nil {
			return fmt.Errorf("evidence of task %d of epic %s: %w", seq, epic, encodeErr)
		}
		if err = queries.InsertEpicTask(ctx, sqlcgen.InsertEpicTaskParams{
			Epic: epic, Seq: seq, Turn: turn, Title: task.Title, Deliverable: task.Deliverable,
			Gate: task.Gate, Evidence: item,
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
		return queries.InsertEpicQuestion(ctx, sqlcgen.InsertEpicQuestionParams{
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

// failuresToPark is how many of an epic's latest turns that are not free must all fail to park the epic.
const failuresToPark = 3

// parkTriggerPrefix starts the id of each pending trigger a planner park adds.
const parkTriggerPrefix = "park:"

// parkedAfterFailures opens the park reason of an epic whose three latest turns that are not free all failed.
const parkedAfterFailures = "3 planner turns failed in a row: "

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
// not zero.
type TurnFree struct {
	Reason string
	Pause  bool
	Park   string
	Limit  bool
	Resets time.Time
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

// FailTurn ends the running turn seq of epic as failed with reason, cut to TurnReasonMax bytes, in one write, and
// returns the triggers it claimed to pending with turn 0. When the epic's three latest turns that are not free are all
// failed, the same write parks the epic with parkedAfterFailures followed by the stored reason. A turn that is not
// running, or does not exist, is ErrTurnNotRunning and writes nothing.
func (s *Store) FailTurn(ctx context.Context, epic string, turn int64, reason string, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		stored := capTurnText(reason)
		if err := endTurn(ctx, queries, epic, turn, "failed", stored, at); err != nil {
			return err
		}
		recent, err := queries.ListRecentEpicTurnStates(ctx, epic)
		if err != nil {
			return fmt.Errorf("read recent turns of epic %s: %w", epic, err)
		}
		if !failedInARow(recent) {
			return nil
		}
		_, err = setParked(ctx, queries, epic, ParkClassOrchestrator, parkedAfterFailures+stored)
		return err
	})
}

// FreeTurn ends the running turn seq of epic as free with free.Reason, cut to TurnReasonMax bytes, and returns its
// triggers to pending with turn 0 in one write. The turn stores storedResetsOf(free): a non-zero free.Resets as its
// reset time, and limitWithoutReset for a Limit that names none. Pause pauses the epic as pauseFreed records, and a
// non-empty Park parks it with that reason. A free turn never counts toward the three failed turns that park an epic.
// A turn that is not running, or does not exist, is ErrTurnNotRunning and writes nothing.
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
		_, err := setParked(ctx, queries, epic, ParkClassOrchestrator, free.Park)
		return err
	})
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

// failedInARow reports whether states, the states of the latest turns that are not free, newest first, are three
// failed turns.
func failedInARow(states []string) bool {
	if len(states) != failuresToPark {
		return false
	}
	for _, state := range states {
		if state != "failed" {
			return false
		}
	}
	return true
}

// setParked parks epic with reason, cut to TurnReasonMax bytes, as class, in the caller's write. An owner class also
// opens the park question of epic for it. The question is final when the epic's phase is met_at_base, so a park of an
// epic the owner ended declares no choice. setParked returns the seq of the question, and 0 for a planner class, which
// opens none. An unknown epic is ErrNoEpic.
func setParked(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	class ParkClass,
	reason string,
) (int64, error) {
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return 0, err
	}
	park := capTurnText(reason)
	if !class.OwnerHeld() {
		return 0, setPark(ctx, queries, epic, park, class)
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

// parkEpicAs parks epic with reason as class, in the caller's write. An owner class opens the park question and adds reason
// as a tell that carries it. A planner class opens no question and adds no tell: it adds the pending trigger that wakes
// the planner, which carries reason as its cause.
func parkEpicAs(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	class ParkClass,
	reason string,
	at time.Time,
) error {
	park := capTurnText(reason)
	question, err := setParked(ctx, queries, epic, class, park)
	if err != nil {
		return err
	}
	if !class.OwnerHeld() {
		return insertParkTrigger(ctx, queries, epic, park, at)
	}
	return insertQuestionTell(ctx, queries, epic, park, question, at)
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
	if len(text) <= TurnReasonMax {
		return text
	}
	cut := TurnReasonMax
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}
