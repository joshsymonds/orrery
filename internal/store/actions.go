package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The kinds of planner action. Each is carried out by the control of the same name, with the action's fields as given.
const (
	ActionRetryNode     = "retry_node"
	ActionRetryEpic     = "retry_epic"
	ActionFailTask      = "fail_task"
	ActionResumeRelease = "resume_release"
	ActionCloseEpic     = "close_epic"
)

// The states a planner action leaves its pending state in, once: taken, or refused. The state is stored as text, so
// the write that carries an action out compares against pending in its query.
const (
	actionTaken   = "taken"
	actionRefused = "refused"
)

// errActionSettled reports that an action is no longer pending, so the write that found it so changes nothing.
var errActionSettled = errors.New("action is not pending")

// EpicActionInput is one action of a planner return: its kind, the node, task, close kind and replacing epic it names
// where its kind uses them, the undo-none positions it names, and the planner's reason.
type EpicActionInput struct {
	Kind     string
	Node     string
	Task     int64
	Close    string
	By       string
	UndoNone []int64
	Reason   string
}

// PlannerAction is a stored action of a planner turn: the epic and turn that returned it, its 1-based position in the
// return, and its fields.
type PlannerAction struct {
	Epic     string
	Turn     int64
	Position int64
	EpicActionInput
}

// PendingActions returns the pending actions of every epic, ordered by epic, then turn, then position.
func (s *Store) PendingActions(ctx context.Context) ([]PlannerAction, error) {
	rows, err := sqlcgen.New(s.db).ListPendingPlannerActions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pending planner actions: %w", err)
	}
	return convertInOrder(rows, plannerActionOf)
}

// plannerActionOf returns the action a stored row holds.
func plannerActionOf(row sqlcgen.PlannerAction) (PlannerAction, error) {
	var undo []int64
	if err := json.Unmarshal([]byte(row.UndoNone), &undo); err != nil {
		return PlannerAction{}, fmt.Errorf("undo-none positions of action %d of turn %d of epic %s: %w",
			row.Position, row.Turn, row.Epic, err)
	}
	return PlannerAction{
		Epic: row.Epic, Turn: row.Turn, Position: row.Position,
		EpicActionInput: EpicActionInput{
			Kind: row.Kind, Node: row.Node, Task: row.Task, Close: row.CloseKind, By: row.ByEpic, UndoNone: undo,
			Reason: row.Reason,
		},
	}, nil
}

// CarryOutAction carries out action, a pending planner action, and returns the ids of the steps the runner must stop.
// The control of its kind runs with the action's fields as given, in one write that also marks the action taken and
// appends an epic decision naming the action and holding its reason. retry_node is RetryNode, retry_epic is RetryEpic,
// fail_task is FailTask on the task's stored build with the reason as the failure, resume_release is the planner's
// resume of a stopped release, and close_epic is CloseEpic by the planner.
//
// A close returns the step of the turn it ended and each step of the epic with no end. A fail_task returns the open
// steps of the failed task. Every other action returns none.
//
// A RefusalError from the control writes nothing of the control, and an action on a closed epic is refused the same way.
// A second write then marks the action refused, appends a decision holding the refusal and the reason, and adds a pending
// trigger whose cause names the action and the refusal, so the planner takes another turn. A refused action returns no
// steps and no error. Any other error is returned with nothing written, so the action stays pending for the next pass.
// An action that is not pending changes nothing.
func (s *Store) CarryOutAction(ctx context.Context, action PlannerAction, at time.Time) ([]string, error) {
	var stop []string
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		var err error
		stop, err = takeAction(ctx, queries, action, at)
		return err
	})
	var refusal RefusalError
	switch {
	case err == nil:
		return stop, nil
	case errors.Is(err, errActionSettled):
		return nil, nil
	case errors.As(err, &refusal):
		return nil, s.refuse(ctx, action, refusal, at)
	default:
		return nil, err
	}
}

// refuse records the refusal of action in one write. An action that is no longer pending is left as it is.
func (s *Store) refuse(ctx context.Context, action PlannerAction, refusal RefusalError, at time.Time) error {
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		return refuseAction(ctx, queries, action, refusal, at)
	})
	if errors.Is(err, errActionSettled) {
		return nil
	}
	return err
}

// takeAction carries out action in the caller's write, as CarryOutAction describes, and returns the steps to stop. The
// epic head is read before the control runs, so the park the action answers is the one the epic held when it was taken.
// The action is marked taken in the same write, so an action that is not pending is errActionSettled, and an action on a
// closed epic is a RefusalError.
func takeAction(ctx context.Context, queries *sqlcgen.Queries, action PlannerAction, at time.Time) ([]string, error) {
	head, err := epicHead(ctx, queries, action.Epic)
	if err != nil {
		return nil, err
	}
	park, err := actionPark(ctx, queries, action, head)
	if err != nil {
		return nil, err
	}
	err = setActionState(ctx, queries, action, actionTaken, "", park)
	if err != nil {
		return nil, err
	}
	if IsClosedPhase(head.Phase) {
		return nil, RefusalError(fmt.Sprintf("epic %s is closed", action.Epic))
	}
	stop, err := carryOutAction(ctx, queries, action, at)
	if err != nil {
		return nil, err
	}
	text := fmt.Sprintf("planner %s: %s", actionSummary(action), action.Reason)
	if _, err = appendDecision(ctx, queries, action.Epic, actionDecisionID(action), text, 0, at); err != nil {
		return nil, err
	}
	return stop, nil
}

// carryOutAction runs the control of action's kind in the caller's write, with the action's fields as given, and returns
// the steps the runner must stop.
func carryOutAction(
	ctx context.Context, queries *sqlcgen.Queries, action PlannerAction, at time.Time,
) ([]string, error) {
	switch action.Kind {
	case ActionRetryNode:
		return nil, rerunNodeIn(ctx, queries, action.Epic, action.Node)
	case ActionRetryEpic:
		return nil, retryEpicIn(ctx, queries, action.Epic)
	case ActionFailTask:
		return failPlannedTask(ctx, queries, action, at)
	case ActionResumeRelease:
		return nil, resumeReleaseByPlanner(ctx, queries, action.Epic, action.UndoNone, at)
	case ActionCloseEpic:
		return closePlannedEpic(ctx, queries, action, at)
	}
	return nil, RefusalError(fmt.Sprintf("%q is not a planner action", action.Kind))
}

// failPlannedTask fails the task action names, as FailTask does from the build the task stores, with the planner's reason
// as its failure. It returns the ids of the task's steps with no end. A task that does not exist, or is not open, is a
// RefusalError.
func failPlannedTask(
	ctx context.Context, queries *sqlcgen.Queries, action PlannerAction, at time.Time,
) ([]string, error) {
	task, err := queries.GetEpicTask(ctx, sqlcgen.GetEpicTaskParams{Epic: action.Epic, Seq: action.Task})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, RefusalError(fmt.Sprintf("task %d of epic %s does not exist", action.Task, action.Epic))
	}
	if err != nil {
		return nil, fmt.Errorf("read task %d of epic %s: %w", action.Task, action.Epic, err)
	}
	err = failTaskIn(ctx, queries, action.Epic, action.Task, buildOf(task), action.Reason, at)
	if errors.Is(err, ErrTaskMoved) {
		return nil, RefusalError(fmt.Sprintf("task %d of epic %s is not open", action.Task, action.Epic))
	}
	if err != nil {
		return nil, err
	}
	return openTaskSteps(ctx, queries, action.Epic, action.Task)
}

// closePlannedEpic closes the epic of action as CloseEpic closes it for the planner. It returns the step of the turn the
// close ended, then each other step of the epic with no end.
func closePlannedEpic(
	ctx context.Context, queries *sqlcgen.Queries, action PlannerAction, at time.Time,
) ([]string, error) {
	turnStep, err := closeEpicIn(ctx, queries, action.Epic, action.Close, action.By, CloserPlanner, at)
	if err != nil {
		return nil, err
	}
	open, err := openEpicSteps(ctx, queries, action.Epic)
	if err != nil {
		return nil, err
	}
	var stop []string
	if turnStep != "" {
		stop = append(stop, turnStep)
	}
	for _, id := range open {
		if id != turnStep {
			stop = append(stop, id)
		}
	}
	return stop, nil
}

// openEpicSteps returns the ids of the steps labeled with epic that have no end, in start order.
func openEpicSteps(ctx context.Context, queries *sqlcgen.Queries, epic string) ([]string, error) {
	ids, err := queries.ListOpenEpicSteps(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list open steps of epic %s: %w", epic, err)
	}
	return ids, nil
}

// openTaskSteps returns the ids of the steps of task seq of epic that have no end, in start order. A step carries its
// task's label as its task, the label the build gives each step it starts for the task.
func openTaskSteps(ctx context.Context, queries *sqlcgen.Queries, epic string, seq int64) ([]string, error) {
	ids, err := queries.ListOpenEpicTaskSteps(ctx, sqlcgen.ListOpenEpicTaskStepsParams{
		Epic: epic, Task: "T" + strconv.FormatInt(seq, 10),
	})
	if err != nil {
		return nil, fmt.Errorf("list open steps of task %d of epic %s: %w", seq, epic, err)
	}
	return ids, nil
}

// refuseAction records the refusal of action in the caller's write: the action is marked refused with the refusal and the
// park it answers, a decision holds the refusal and the planner's reason, and then either the held planner park is stored
// as retry_bound, when its chain is at the bound, or a pending trigger whose cause names the action and the refusal is
// added.
func refuseAction(
	ctx context.Context, queries *sqlcgen.Queries, action PlannerAction, refusal RefusalError, at time.Time,
) error {
	head, err := epicHead(ctx, queries, action.Epic)
	if err != nil {
		return err
	}
	park, err := actionPark(ctx, queries, action, head)
	if err != nil {
		return err
	}
	err = setActionState(ctx, queries, action, actionRefused, string(refusal), park)
	if err != nil {
		return err
	}
	summary := actionSummary(action)
	id := actionDecisionID(action) + "-refused"
	text := fmt.Sprintf("planner %s refused: %s. Its reason: %s", summary, refusal, action.Reason)
	if _, err = appendDecision(ctx, queries, action.Epic, id, text, 0, at); err != nil {
		return err
	}
	bound, err := boundHeldPark(ctx, queries, action.Epic, head, at)
	if err != nil || bound {
		return err
	}
	if err = queries.InsertEpicTrigger(ctx, sqlcgen.InsertEpicTriggerParams{
		Epic: action.Epic, ID: id, Cause: fmt.Sprintf("planner %s refused: %s", summary, refusal),
		State: triggerPending, Turn: 0, Created: at.Unix(),
	}); err != nil {
		return fmt.Errorf("insert refusal trigger of action %d of turn %d of epic %s: %w",
			action.Position, action.Turn, action.Epic, err)
	}
	return nil
}

// setActionState stores state, refusal and park on action in the caller's write, when the action is still pending. An
// action that is not pending is errActionSettled.
func setActionState(
	ctx context.Context, queries *sqlcgen.Queries, action PlannerAction, state, refusal string, park int64,
) error {
	rows, err := queries.SetPlannerActionState(ctx, sqlcgen.SetPlannerActionStateParams{
		State: state, Refusal: refusal, Park: park, Epic: action.Epic, Turn: action.Turn, Position: action.Position,
	})
	if err != nil {
		return fmt.Errorf("set state of action %d of turn %d of epic %s: %w",
			action.Position, action.Turn, action.Epic, err)
	}
	if rows == 0 {
		return errActionSettled
	}
	return nil
}

// actionDecisionID is the client id of the decision that records action being taken.
func actionDecisionID(action PlannerAction) string {
	return fmt.Sprintf("action-%d-%d", action.Turn, action.Position)
}

// actionSummary names action for a decision or a trigger by its kind and the node, task or close it names.
func actionSummary(action PlannerAction) string {
	switch action.Kind {
	case ActionRetryNode:
		return "retry node " + action.Node
	case ActionFailTask:
		return fmt.Sprintf("fail task %d", action.Task)
	case ActionCloseEpic:
		if action.By == "" {
			return "close epic as " + action.Close
		}
		return fmt.Sprintf("close epic as %s by %s", action.Close, action.By)
	}
	return strings.ReplaceAll(action.Kind, "_", " ")
}

// insertActions stores actions as the pending actions of turn of epic, each at its 1-based position in the return, in the
// caller's write.
func insertActions(
	ctx context.Context, queries *sqlcgen.Queries, epic string, turn int64, actions []EpicActionInput,
) error {
	return insertInOrder(actions, func(position int64, action EpicActionInput) error {
		undo := action.UndoNone
		if undo == nil {
			undo = []int64{}
		}
		encoded, err := json.Marshal(undo)
		if err != nil {
			return fmt.Errorf("undo-none positions of action %d of turn %d of epic %s: %w", position, turn, epic, err)
		}
		return queries.InsertPlannerAction(ctx, sqlcgen.InsertPlannerActionParams{
			Epic: epic, Turn: turn, Position: position, Kind: action.Kind, Node: action.Node, Task: action.Task,
			CloseKind: action.Close, ByEpic: action.By, UndoNone: string(encoded), Reason: action.Reason,
		})
	})
}
