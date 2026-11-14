package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrNotClaimable reports that an epic has no turn to claim: it is paused or parked, it has a running turn, or it has
// no pending trigger.
var ErrNotClaimable = errors.New("epic has no turn to claim")

// ErrTurnNotRunning reports that a turn is not running.
var ErrTurnNotRunning = errors.New("turn is not running")

// EpicPlanState is the planning state of one epic with pending triggers. Parked is the park reason, empty when the
// epic is not parked. Running is true when the epic has a turn in state running.
type EpicPlanState struct {
	Epic    string
	Paused  bool
	Parked  string
	Running bool
	Pending int
}

// EpicTurn is one planner turn on an epic. Times are stored at whole seconds; Ended is zero while the turn runs.
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
}

// EpicTask is one task an epic's planner turn planned: its title, its deliverable, the files it touches, the
// Requirements it covers, its check and its status. Turn is the seq of the turn that planned it.
type EpicTask struct {
	Seq          int64
	Turn         int64
	Title        string
	Deliverable  string
	Files        []string
	Requirements []string
	Check        string
	Status       string
}

// EpicQuestion is one question a planner turn asked, with its state. Turn is the seq of the turn that asked it.
type EpicQuestion struct {
	Seq   int64
	Turn  int64
	Text  string
	State string
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

// EpicPlan is the valid return of one planner turn: its decisions, its tasks, its outline, its questions and its
// outcome.
type EpicPlan struct {
	Decisions []string
	Tasks     []EpicTaskInput
	Outline   string
	Questions []string
	Outcome   string
}

// EpicTaskInput is one task of a planner return: its title, its deliverable, the files it touches, the Requirements
// it covers and its check.
type EpicTaskInput struct {
	Title        string
	Deliverable  string
	Files        []string
	Requirements []string
	Check        string
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
			Parked:  state.Parked,
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
// A task carries its files and Requirements in position order.
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
	pathsOf := make(map[int64][]string)
	for _, file := range files {
		pathsOf[file.Task] = append(pathsOf[file.Task], file.Path)
	}
	requirementsOf := make(map[int64][]string)
	for _, requirement := range requirements {
		requirementsOf[requirement.Task] = append(requirementsOf[requirement.Task], requirement.Requirement)
	}
	row.Tasks = mapInOrder(tasks, func(task sqlcgen.EpicTask) EpicTask {
		return EpicTask{
			Seq: task.Seq, Turn: task.Turn, Title: task.Title, Deliverable: task.Deliverable,
			Files: pathsOf[task.Seq], Requirements: requirementsOf[task.Seq], Check: task.TaskCheck,
			Status: task.Status,
		}
	})

	questions, err := queries.ListEpicQuestions(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list questions: %w", err)
	}
	row.Questions = mapInOrder(questions, func(question sqlcgen.EpicQuestion) EpicQuestion {
		return EpicQuestion{Seq: question.Seq, Turn: question.Turn, Text: question.Text, State: question.State}
	})
	return nil
}

// turnOf returns the stored turn with its decisions in position order. Ended is zero while the turn runs.
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
	}
	if stored.Ended != 0 {
		turn.Ended = time.Unix(stored.Ended, 0)
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

// checkClaimable returns ErrNoEpic for an unknown epic, and ErrNotClaimable when the epic is paused or parked, has a
// running turn, or has no pending trigger.
func checkClaimable(ctx context.Context, queries *sqlcgen.Queries, epic string) error {
	head, err := queries.GetEpic(ctx, epic)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("epic %s: %w", epic, ErrNoEpic)
	}
	if err != nil {
		return fmt.Errorf("get epic %s: %w", epic, err)
	}
	if head.Paused != 0 || head.Parked != "" {
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
// their files and Requirements, its outline, its questions and its outcome. The turn succeeds ended at at, and every
// trigger the turn claimed is consumed. A turn that is not running, or does not exist, is ErrTurnNotRunning and
// writes nothing.
func (s *Store) CompleteTurn(ctx context.Context, epic string, turn int64, plan EpicPlan, at time.Time) error {
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
		if err = insertTasks(ctx, queries, epic, turn, plan.Tasks); err != nil {
			return fmt.Errorf("tasks of turn %d of epic %s: %w", turn, epic, err)
		}
		if err = insertQuestions(ctx, queries, epic, turn, plan.Questions); err != nil {
			return fmt.Errorf("questions of turn %d of epic %s: %w", turn, epic, err)
		}
		if err = queries.ConsumeEpicTriggers(ctx, sqlcgen.ConsumeEpicTriggersParams{
			Epic: epic, Turn: turn,
		}); err != nil {
			return fmt.Errorf("consume triggers of turn %d of epic %s: %w", turn, epic, err)
		}
		return nil
	})
}

// insertTasks stores tasks as the next task seqs of epic after its highest, each with its files and Requirements in
// order.
func insertTasks(ctx context.Context, queries *sqlcgen.Queries, epic string, turn int64, tasks []EpicTaskInput) error {
	highest, err := queries.MaxEpicTaskSeq(ctx, epic)
	if err != nil {
		return fmt.Errorf("read task count of epic %s: %w", epic, err)
	}
	return insertInOrder(tasks, func(taskPosition int64, task EpicTaskInput) error {
		seq := highest + taskPosition
		if err = queries.InsertEpicTask(ctx, sqlcgen.InsertEpicTaskParams{
			Epic: epic, Seq: seq, Turn: turn, Title: task.Title, Deliverable: task.Deliverable, TaskCheck: task.Check,
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
		return nil
	})
}

// insertQuestions stores questions as the next question seqs of epic after its highest, each open.
func insertQuestions(ctx context.Context, queries *sqlcgen.Queries, epic string, turn int64, questions []string) error {
	highest, err := queries.MaxEpicQuestionSeq(ctx, epic)
	if err != nil {
		return fmt.Errorf("read question count of epic %s: %w", epic, err)
	}
	return insertInOrder(questions, func(position int64, text string) error {
		return queries.InsertEpicQuestion(ctx, sqlcgen.InsertEpicQuestionParams{
			Epic: epic, Seq: highest + position, Turn: turn, Text: text,
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

// parkedAfterFailures opens the park reason of an epic whose three latest turns that are not free all failed.
const parkedAfterFailures = "3 planner turns failed in a row: "

// TurnFree is how a running turn ends without a return: its Reason, whether it Pauses the epic, and the Park reason
// it parks the epic with when not empty.
type TurnFree struct {
	Reason string
	Pause  bool
	Park   string
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
		return setParked(ctx, queries, epic, parkedAfterFailures+stored)
	})
}

// FreeTurn ends the running turn seq of epic as free with free.Reason, cut to TurnReasonMax bytes, and returns its
// triggers to pending with turn 0 in one write. Pause pauses the epic, and a non-empty Park parks it with that reason.
// A free turn never counts toward the three failed turns that park an epic. A turn that is not running, or does not
// exist, is ErrTurnNotRunning and writes nothing.
func (s *Store) FreeTurn(ctx context.Context, epic string, turn int64, free TurnFree, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := endTurn(ctx, queries, epic, turn, "free", capTurnText(free.Reason), at); err != nil {
			return err
		}
		if free.Pause {
			if err := queries.PauseEpic(ctx, epic); err != nil {
				return fmt.Errorf("pause epic %s: %w", epic, err)
			}
		}
		if free.Park == "" {
			return nil
		}
		return setParked(ctx, queries, epic, free.Park)
	})
}

// ParkEpic parks epic with reason, cut to TurnReasonMax bytes, in one write. An unknown epic is ErrNoEpic.
func (s *Store) ParkEpic(ctx context.Context, epic string, reason string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		return setParked(ctx, queries, epic, reason)
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

// setParked sets the park reason of epic to reason, cut to TurnReasonMax bytes. An unknown epic is ErrNoEpic.
func setParked(ctx context.Context, queries *sqlcgen.Queries, epic, reason string) error {
	rows, err := queries.SetEpicParked(ctx, sqlcgen.SetEpicParkedParams{Parked: capTurnText(reason), ID: epic})
	if err != nil {
		return fmt.Errorf("park epic %s: %w", epic, err)
	}
	if rows == 0 {
		return fmt.Errorf("epic %s: %w", epic, ErrNoEpic)
	}
	return nil
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
