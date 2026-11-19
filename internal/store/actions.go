package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The kinds of planner action. Each is carried out by the control of the same name, with the action's fields as given.
// amend_evidence is AmendEvidence by the planner, whose action stays pending until its amendment is settled.
const (
	ActionRetryNode      = "retry_node"
	ActionRetryEpic      = "retry_epic"
	ActionFailTask       = "fail_task"
	ActionResumeRelease  = "resume_release"
	ActionCloseEpic      = "close_epic"
	ActionAmendEvidence  = "amend_evidence"
	ActionChangeApproach = "change_approach"
	ActionSendBack       = "send_back"
)

// The states a planner action leaves its pending state in, once: taken, or refused. The state is stored as text, so
// the write that carries an action out compares against pending in its query.
const (
	actionTaken   = "taken"
	actionRefused = "refused"
)

// errActionSettled reports that an action is no longer pending, so the write that found it so changes nothing.
var errActionSettled = errors.New("action is not pending")

// EpicActionInput is one action of a planner return: its kind, the node, task, close kind, replacing epic, Requirement,
// evidence item and deliverable it names where its kind uses them, the undo-none positions it names, and the planner's
// reason.
type EpicActionInput struct {
	Kind        string
	Node        string
	Task        int64
	Close       string
	By          string
	Requirement string
	Item        evidence.Item
	Deliverable string
	Files       []string
	Remove      []string
	UndoNone    []int64
	Reason      string
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
	item, err := decodeItem(row.Item)
	if err != nil {
		return PlannerAction{}, fmt.Errorf("item of action %d of turn %d of epic %s: %w",
			row.Position, row.Turn, row.Epic, err)
	}
	files, err := decodeList[string](row.Files)
	if err != nil {
		return PlannerAction{}, fmt.Errorf("files of action %d of turn %d of epic %s: %w",
			row.Position, row.Turn, row.Epic, err)
	}
	remove, err := decodeList[string](row.RemoveFiles)
	if err != nil {
		return PlannerAction{}, fmt.Errorf("remove files of action %d of turn %d of epic %s: %w",
			row.Position, row.Turn, row.Epic, err)
	}
	return PlannerAction{
		Epic: row.Epic, Turn: row.Turn, Position: row.Position,
		EpicActionInput: EpicActionInput{
			Kind: row.Kind, Node: row.Node, Task: row.Task, Close: row.CloseKind, By: row.ByEpic,
			Requirement: row.Requirement, Item: item, Deliverable: row.Deliverable, Files: files, Remove: remove,
			UndoNone: undo, Reason: row.Reason,
		},
	}, nil
}

// CarryOutAction carries out action, a pending planner action, and returns the ids of the steps the runner must stop.
// The control of its kind runs with the action's fields as given, in one write that also marks the action taken and
// appends an epic decision naming the action and holding its reason. retry_node is RetryNode, retry_epic is RetryEpic,
// fail_task climbs the task one rung off its stored build with the reason as the cause, as failPlannedTask does,
// resume_release is the planner's resume of a stopped release, close_epic is CloseEpic by the planner, change_approach
// stores the task's change-of-approach version from the action's deliverable and evidence item, as changeApproach does,
// and send_back reopens the task at implement on rung 1 with the reason as its failure, as sendBack does.
//
// A close returns the step of the turn it ended and each step of the epic with no end. A fail_task and a send_back
// return the open steps of the task they move. Every other action returns none.
//
// amend_evidence is not carried out in this write. Its amendment starts checking, as AmendEvidence starts one by the
// planner, and the action stays pending until SettleAmendment takes or refuses it with its amendment; while it is pending
// with its amendment stored, carrying it out again changes nothing. A refusal of the amendment at its start is recorded
// as any refusal is.
//
// A RefusalError from the control writes nothing of the control, and an action on a closed epic is refused the same way.
// A second write then marks the action refused, appends a decision holding the refusal and the reason, and adds a pending
// trigger whose cause names the action and the refusal, so the planner takes another turn. A refused action returns no
// steps and no error. Any other error is returned with nothing written, so the action stays pending for the next pass.
// An action that is not pending changes nothing.
func (s *Store) CarryOutAction(ctx context.Context, action PlannerAction, at time.Time) ([]string, error) {
	if action.Kind == ActionAmendEvidence {
		return nil, s.startPlannedAmendment(ctx, action, at)
	}
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
// The action is marked taken in the same write, so an action that is not pending is errActionSettled. An action on a
// closed epic is a RefusalError. So is a retry_node or retry_epic action on an epic whose park the owner holds: only the
// owner retries such a park, so the park, its class and its question stay as they are.
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
	retries := action.Kind == ActionRetryNode || action.Kind == ActionRetryEpic
	if retries && head.Parked != "" && ParkClass(head.ParkClass).OwnerHeld() {
		return nil, RefusalError(fmt.Sprintf(
			"epic %s holds the owner's %s park; only the owner retries it", action.Epic, head.ParkClass))
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

// sendBack sends the task action names back to implement in the caller's write, as CarryOutAction describes. The task may
// be open, done or ended as a gap, from any rung. It is refused while an integrate run that claims it or composes it has
// not ended, running or parked, and on an epic in an ended phase, and writes nothing then. Otherwise the task is reopened: its status is open
// and its build is a fresh build on implement at attempt 1, built on the stored base, with the action's reason as its
// failure (sentBackBuild). Its batch claim is cleared, so the next batch claims it once it is done again. The epic's
// candidate is cleared too, unless the task's claiming batch passed with the task in it: then the stored base holds the
// task's commit, and the candidate holds no commit of the task that the base lacks. Otherwise the candidate may hold the
// task's commit, and a batch composes the work that remains from the stored base. The task's
// ladder restarts at rung 1 moved by send_back (restartLadder), and a version of source send_back records the reopen
// (insertSendBackVersion). It returns the ids of the task's steps with no end, as failPlannedTask does, so the runner
// stops a unit still running on the task.
func sendBack(ctx context.Context, queries *sqlcgen.Queries, action PlannerAction, at time.Time) ([]string, error) {
	task, err := queries.GetEpicTask(ctx, sqlcgen.GetEpicTaskParams{Epic: action.Epic, Seq: action.Task})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, RefusalError(fmt.Sprintf("task %d of epic %s does not exist", action.Task, action.Epic))
	}
	if err != nil {
		return nil, fmt.Errorf("read task %d of epic %s: %w", action.Task, action.Epic, err)
	}
	head, err := epicHead(ctx, queries, action.Epic)
	if err != nil {
		return nil, err
	}
	if endedPhase(head.Phase) {
		return nil, RefusalError(fmt.Sprintf("epic %s is %s, an ended phase", action.Epic, head.Phase))
	}
	runs, err := readNodeRuns(ctx, queries, action.Epic)
	if err != nil {
		return nil, err
	}
	if path := runningBatchOf(runs, task); path != "" {
		return nil, RefusalError(fmt.Sprintf(
			"task %d of epic %s is in integrate run %s, which has not ended", action.Task, action.Epic, path))
	}
	inBase := claimedPassed(runs, task)
	from := buildOf(task)
	cause := capTurnText(action.Reason)
	err = setTaskStatus(ctx, queries, action.Epic, action.Task, taskOpen)
	if err != nil {
		return nil, err
	}
	err = moveTask(ctx, queries, action.Epic, action.Task, from, sentBackBuild(from, head.StoredBase, cause))
	if err != nil {
		return nil, err
	}
	err = queries.ClearEpicTaskBatch(ctx, sqlcgen.ClearEpicTaskBatchParams{Epic: action.Epic, Seq: action.Task})
	if err != nil {
		return nil, fmt.Errorf("clear batch of task %d of epic %s: %w", action.Task, action.Epic, err)
	}
	if !inBase && head.Candidate != "" {
		err = queries.SetEpicCandidate(ctx, sqlcgen.SetEpicCandidateParams{Candidate: "", ID: action.Epic})
		if err != nil {
			return nil, fmt.Errorf("clear candidate of epic %s: %w", action.Epic, err)
		}
	}
	err = restartLadder(ctx, queries, action.Epic, action.Task, cause, at)
	if err != nil {
		return nil, err
	}
	err = insertSendBackVersion(ctx, queries, action.Epic, action.Task, actionDecisionID(action), action.Reason, at)
	if err != nil {
		return nil, err
	}
	return openTaskSteps(ctx, queries, action.Epic, action.Task)
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
	case ActionChangeApproach:
		return nil, changeApproach(ctx, queries, action, at)
	case ActionSendBack:
		return sendBack(ctx, queries, action, at)
	}
	return nil, RefusalError(fmt.Sprintf("%q is not a planner action", action.Kind))
}

// changeApproach stores the change-of-approach version of the task action names: its deliverable is the action's, its
// evidence item is the action's when the item sets any field, which makes the version checking, and its files are the
// action's Files added and Remove removed. A task that does not exist, a task that is not open, a task that is not on
// ApproachRung, a task whose change of approach stands (ApproachStands), and a file the version refuses
// (refuseApproachFiles) are each a RefusalError, and writes nothing.
func changeApproach(ctx context.Context, queries *sqlcgen.Queries, action PlannerAction, at time.Time) error {
	task, err := queries.GetEpicTask(ctx, sqlcgen.GetEpicTaskParams{Epic: action.Epic, Seq: action.Task})
	if errors.Is(err, sql.ErrNoRows) {
		return RefusalError(fmt.Sprintf("task %d of epic %s does not exist", action.Task, action.Epic))
	}
	if err != nil {
		return fmt.Errorf("read task %d of epic %s: %w", action.Task, action.Epic, err)
	}
	if task.Status != "open" {
		return RefusalError(fmt.Sprintf("task %d of epic %s is not open", action.Task, action.Epic))
	}
	if rung := buildOf(task).Attempt; rung != ApproachRung {
		return RefusalError(fmt.Sprintf("task %d of epic %s is on rung %d, not rung %d",
			action.Task, action.Epic, rung, ApproachRung))
	}
	rows, err := queries.ListEpicTaskVersions(ctx, action.Epic)
	if err != nil {
		return fmt.Errorf("list versions of epic %s: %w", action.Epic, err)
	}
	versions, err := taskVersionsOf(rows)
	if err != nil {
		return fmt.Errorf("list versions of epic %s: %w", action.Epic, err)
	}
	if ApproachStands(versions[action.Task]) {
		return RefusalError(fmt.Sprintf("task %d of epic %s already has a change of approach",
			action.Task, action.Epic))
	}
	err = refuseApproachFiles(ctx, queries, action)
	if err != nil {
		return err
	}
	in := TaskVersionInput{
		Source: SourceApproach, Reason: action.Reason, Deliverable: &action.Deliverable,
		Files: action.Files, Remove: action.Remove,
	}
	if !reflect.ValueOf(action.Item).IsZero() {
		in.Evidence = &action.Item
	}
	return appendTaskVersion(ctx, queries, action.Epic, action.Task, actionDecisionID(action), in, at)
}

// refuseApproachFiles returns a RefusalError for the files of a change of approach that the task of action cannot have:
// a file it adds that it already owns, a file it adds that another open task owns, or one an accepted task owns that the
// epic has not integrated, which the holder names; a file it removes that it does not own; a file both added and
// removed, or named twice; and a change that would leave the task no files.
func refuseApproachFiles(ctx context.Context, queries *sqlcgen.Queries, action PlannerAction) error {
	owned, err := ownedFiles(ctx, queries, action.Epic, action.Task)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, file := range slices.Concat(action.Files, action.Remove) {
		if seen[file] {
			return RefusalError(fmt.Sprintf("file %s is named twice", file))
		}
		seen[file] = true
	}
	for _, file := range action.Remove {
		if !slices.Contains(owned, file) {
			return RefusalError(fmt.Sprintf("%s is not one of task %d's files", file, action.Task))
		}
	}
	for _, file := range action.Files {
		if slices.Contains(owned, file) {
			return RefusalError(fmt.Sprintf("%s is already one of task %d's files", file, action.Task))
		}
	}
	err = refuseHeldFiles(ctx, queries, action.Epic, action.Task, action.Files)
	if err != nil {
		return err
	}
	if len(action.Files)+len(action.Remove) > 0 && len(withoutPaths(owned, action.Remove))+len(action.Files) == 0 {
		return RefusalError(fmt.Sprintf("task %d of epic %s would be left no files", action.Task, action.Epic))
	}
	return nil
}

// refuseHeldFiles returns a RefusalError naming the holder of each of files that another task of the epic holds: an
// open task that owns it, or an accepted task that owns it and has not been integrated. An accepted task has not been
// integrated while no integrate batch has claimed it, or while the batch that claimed it has not ended.
func refuseHeldFiles(ctx context.Context, queries *sqlcgen.Queries, epic string, seq int64, files []string) error {
	if len(files) == 0 {
		return nil
	}
	tasks, err := queries.ListEpicTasks(ctx, epic)
	if err != nil {
		return fmt.Errorf("read tasks of epic %s: %w", epic, err)
	}
	rows, err := queries.ListEpicTaskFiles(ctx, epic)
	if err != nil {
		return fmt.Errorf("read task files of epic %s: %w", epic, err)
	}
	runs, err := readNodeRuns(ctx, queries, epic)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Task == seq || !slices.Contains(files, row.Path) {
			continue
		}
		i := slices.IndexFunc(tasks, func(task sqlcgen.EpicTask) bool { return task.Seq == row.Task })
		if i < 0 {
			continue
		}
		task := tasks[i]
		switch {
		case task.Status == taskOpen:
			return RefusalError(fmt.Sprintf("%s is owned by open T%d", row.Path, row.Task))
		case task.Status == taskDone && !batchEnded(task.Batch, runs):
			return RefusalError(fmt.Sprintf("%s is owned by accepted T%d, not yet integrated", row.Path, row.Task))
		}
	}
	return nil
}

// batchEnded reports whether the integrate batch at path has ended, so the task it claimed is composed or left out. A
// task no batch claimed has an empty path, and a batch whose run is not among runs has not ended.
func batchEnded(path string, runs []NodeRun) bool {
	if path == "" {
		return false
	}
	i := slices.IndexFunc(runs, func(run NodeRun) bool { return run.Path == path })
	return i >= 0 && runs[i].State == NodeEnded
}

// failPlannedTask climbs the task action names one rung off the build the task stores, with the planner's reason as the
// cause, as ClimbTask does: the rung it climbs to is the one after its own, and from LastRung it is GapRung, which ends
// the task as a gap. The climb moves by fail_task and records the build's step. It returns the ids of the task's steps
// with no end. A task that does not exist, or is not open, is a RefusalError, and so is a stored build that moved under
// the climb.
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
	if task.Status != taskOpen {
		return nil, RefusalError(fmt.Sprintf("task %d of epic %s is not open", action.Task, action.Epic))
	}
	from := buildOf(task)
	cause := capTurnText(action.Reason)
	to := ClimbedBuild(from, min(RungOf(from)+1, GapRung), cause)
	move := RungMove{MovedBy: MovedByFailTask, Cause: cause, Step: from.Step}
	err = checkClimb(action.Epic, action.Task, from, to, move)
	if err != nil {
		return nil, err
	}
	err = climbTask(ctx, queries, action.Epic, action.Task, from, to, move, nil, nil, at)
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
// park it answers, a decision holds the refusal and the planner's reason, and for amend_evidence the Requirement's item
// and the item the action asked for, and then either the held planner park is stored
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
	return refuseLinkedAction(ctx, queries, action, refusal, park, at)
}

// refuseLinkedAction records the refusal of action as refuseAction does, with park as the park the action answers.
func refuseLinkedAction(
	ctx context.Context, queries *sqlcgen.Queries, action PlannerAction, refusal RefusalError, park int64,
	at time.Time,
) error {
	head, err := epicHead(ctx, queries, action.Epic)
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
	if action.Kind == ActionAmendEvidence {
		old, readErr := amendedItem(ctx, queries, action)
		if readErr != nil {
			return readErr
		}
		text += "." + itemsText(old, action.Item)
	}
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

// actionSummary names action for a decision or a trigger by its kind and the node, task, close or Requirement it names.
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
	case ActionAmendEvidence:
		return "amend evidence of " + action.Requirement
	case ActionChangeApproach:
		return fmt.Sprintf("change approach of task %d", action.Task)
	case ActionSendBack:
		return fmt.Sprintf("send back task %d", action.Task)
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
		item, err := encodeItem(action.Item)
		if err != nil {
			return fmt.Errorf("item of action %d of turn %d of epic %s: %w", position, turn, epic, err)
		}
		files, err := encodeList(action.Files)
		if err != nil {
			return fmt.Errorf("files of action %d of turn %d of epic %s: %w", position, turn, epic, err)
		}
		remove, err := encodeList(action.Remove)
		if err != nil {
			return fmt.Errorf("remove files of action %d of turn %d of epic %s: %w", position, turn, epic, err)
		}
		return queries.InsertPlannerAction(ctx, sqlcgen.InsertPlannerActionParams{
			Epic: epic, Turn: turn, Position: position, Kind: action.Kind, Node: action.Node, Task: action.Task,
			CloseKind: action.Close, ByEpic: action.By, UndoNone: string(encoded), Reason: action.Reason,
			Requirement: action.Requirement, Item: item, Deliverable: action.Deliverable, Files: files,
			RemoveFiles: remove,
		})
	})
}

// amendedItem returns the item the Requirement an amend_evidence action names holds, as queries read it, or the zero item
// when the epic holds no such Requirement.
func amendedItem(ctx context.Context, queries *sqlcgen.Queries, action PlannerAction) (evidence.Item, error) {
	item, err := epicRequirement(ctx, queries, action.Epic, action.Requirement)
	var refusal RefusalError
	if errors.As(err, &refusal) {
		return evidence.Item{}, nil
	}
	if err != nil {
		return evidence.Item{}, err
	}
	return item.Item, nil
}
