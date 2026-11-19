package enginehost

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// Engine session states the protocol does not name: a session that ended cleanly or with an error.
const (
	stateStopped = "stopped"
	stateFailed  = "failed"
)

// rateLimitRejected is the rate-limit status that refuses the session's next turn. Only it is recorded.
const rateLimitRejected = "rejected"

// The statuses an engine task records. A task a message reports as completed is done, one reported as failed is failed,
// and any other terminal status is stopped. A task still running when the session exits is stopped too.
const (
	taskRunning = "running"
	taskDone    = "done"
	taskFailed  = "failed"
	taskStopped = "stopped"
)

// openText is the unfinished text of one streaming block: its kind and the text streamed so far.
type openText struct {
	kind string
	text string
}

// runningTask is what the recorder keeps of one background task that is running: the fields a message of the task may
// omit, so that a later message of the task still records them.
type runningTask struct {
	toolUseID    string
	description  string
	taskType     string
	subagentType string
}

// op returns the EngineTask that records the task taskID with status, lastTool and summary, and the task's own fields.
func (t runningTask) op(id, taskID, status, lastTool, summary string) store.EngineTask {
	return store.EngineTask{
		ID: id, TaskID: taskID, ToolUseID: t.toolUseID, Description: t.description,
		Type: taskKind(t.taskType, t.subagentType), SubagentType: t.subagentType,
		Status: status, LastTool: lastTool, Summary: summary,
	}
}

// taskKind is the type an engine task records: agent when its task type ends in agent or a subagent type is named, and
// shell otherwise.
func taskKind(taskType, subagentType string) string {
	if strings.HasSuffix(taskType, "agent") || subagentType != "" {
		return "agent"
	}
	return "shell"
}

// awaitedTaskType reports whether a background task of taskType runs a model: an agent, a workflow or a teammate. A
// step waits for such a task, since its end starts a turn of claude's own whose return is the one that counts. A shell
// task, which a Monitor's watch is too, is not waited for: a step whose turn returns while only shell tasks are listed has
// returned, and its stop ends them.
func awaitedTaskType(taskType string) bool {
	return strings.HasSuffix(taskType, "agent") || strings.HasSuffix(taskType, "workflow") ||
		strings.HasSuffix(taskType, "teammate")
}

// awaitsTask reports whether tasks lists a task that runs a model (see awaitedTaskType).
func awaitsTask(tasks []session.BackgroundTask) bool {
	return slices.ContainsFunc(tasks, func(task session.BackgroundTask) bool { return awaitedTaskType(task.TaskType) })
}

// taskEndStatus is the status an engine task records when its message reports the terminal status the wire gives.
func taskEndStatus(status string) string {
	switch status {
	case "completed":
		return taskDone
	case "failed":
		return taskFailed
	default:
		return taskStopped
	}
}

// recorder translates the events of one headless session into the store ops that record them. It keeps the
// unfinished text of each open block, the claude session it last recorded and the background tasks still running. Its
// running tasks are guarded, because a Stop and a Close may both record the session's stopped tasks once its pump has
// ended; the rest is used by the pump alone.
type recorder struct {
	id            string
	claudeSession string
	texts         map[textKey]openText
	tasks         map[string]runningTask
	mu            sync.Mutex // guards tasks
}

// newRecorder returns a recorder for the session id.
func newRecorder(id string) *recorder {
	return &recorder{id: id, texts: map[textKey]openText{}, tasks: map[string]runningTask{}}
}

// ops returns the store ops that record event, in the order they apply. A finished block and the removal of its
// unfinished row are returned together, so one ApplyEngine call stores both or neither.
func (r *recorder) ops(event session.Event) []store.EngineOp {
	switch ev := event.(type) {
	case session.Started:
		if ev.SessionID == r.claudeSession {
			return nil
		}
		r.claudeSession = ev.SessionID
		return []store.EngineOp{store.EngineSet{ID: r.id, Changes: store.EngineChanges{ClaudeSession: &ev.SessionID}}}
	case session.TextDelta:
		return r.delta(string(session.BlockText), ev.MessageID, ev.Index, ev.ParentToolUseID, ev.Text)
	case session.ThinkingDelta:
		return r.delta(string(session.BlockThinking), ev.MessageID, ev.Index, ev.ParentToolUseID, ev.Text)
	case session.Block:
		return r.block(ev)
	case session.Request:
		return r.request(ev)
	case session.RateLimit:
		return r.rateLimit(ev)
	case session.RequestCancelled:
		working := string(protocol.StateWorking)
		return []store.EngineOp{
			store.EngineRequestCancelled{ID: r.id, RequestID: ev.RequestID, ToolUseID: ev.ToolUseID},
			store.EngineSet{ID: r.id, Changes: store.EngineChanges{State: &working, Question: new("")}},
		}
	case session.TaskStarted:
		return r.taskStarted(ev)
	case session.TaskProgress:
		return r.taskProgress(ev)
	case session.TaskEnded:
		return r.taskEnded(ev)
	case session.Lifecycle:
		return r.promptState(ev)
	case session.TurnEnded:
		return r.turnEnd(ev, protocol.StateDone, false)
	case session.Exited:
		clear(r.texts)
		ops := r.stoppedTasks()
		return append(ops, store.EngineTextClear{ID: r.id}, store.EnginePromptsDiscard{ID: r.id},
			store.EngineSet{ID: r.id, Changes: exitChanges(ev)})
	default:
		return nil
	}
}

// rateLimit returns the store op that records a rate limit that refuses the session's next turn, and nothing for any
// other status.
func (r *recorder) rateLimit(ev session.RateLimit) []store.EngineOp {
	if ev.Status != rateLimitRejected {
		return nil
	}
	return []store.EngineOp{
		store.EngineRateLimited{ID: r.id, Status: ev.Status, Kind: ev.Kind, ResetsAt: ev.ResetsAt},
	}
}

// taskStarted returns the store op that records a task that started running, and adds the task to the running ones.
func (r *recorder) taskStarted(ev session.TaskStarted) []store.EngineOp {
	task := runningTask{toolUseID: ev.ToolUseID, description: ev.Description, taskType: ev.TaskType,
		subagentType: ev.SubagentType}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks[ev.TaskID] = task
	return []store.EngineOp{task.op(r.id, ev.TaskID, taskRunning, "", "")}
}

// taskProgress returns the store op that records the progress of a running task. The fields the progress gives update
// the task's fields, and those it omits keep the values the task had.
func (r *recorder) taskProgress(ev session.TaskProgress) []store.EngineOp {
	r.mu.Lock()
	defer r.mu.Unlock()
	task := r.tasks[ev.TaskID]
	if ev.ToolUseID != "" {
		task.toolUseID = ev.ToolUseID
	}
	if ev.Description != "" {
		task.description = ev.Description
	}
	if ev.SubagentType != "" {
		task.subagentType = ev.SubagentType
	}
	r.tasks[ev.TaskID] = task
	return []store.EngineOp{task.op(r.id, ev.TaskID, taskRunning, ev.LastTool, ev.Summary)}
}

// taskEnded returns the store op that records a task that reached a terminal status, and removes the task from the
// running ones.
func (r *recorder) taskEnded(ev session.TaskEnded) []store.EngineOp {
	r.mu.Lock()
	defer r.mu.Unlock()
	task := r.tasks[ev.TaskID]
	delete(r.tasks, ev.TaskID)
	if ev.ToolUseID != "" {
		task.toolUseID = ev.ToolUseID
	}
	return []store.EngineOp{task.op(r.id, ev.TaskID, taskEndStatus(ev.Status), "", ev.Summary)}
}

// promptState returns the store op that records the state claude reports for the prompt whose uuid is the write id, or
// nil when the report carries no uuid or names a state the store does not keep.
func (r *recorder) promptState(ev session.Lifecycle) []store.EngineOp {
	if ev.CommandUUID == "" {
		return nil
	}
	state, ok := promptStateOf(ev.State)
	if !ok {
		return nil
	}
	return []store.EngineOp{store.EnginePromptState{ID: r.id, WriteID: ev.CommandUUID, State: state}}
}

// promptStateOf returns the store prompt state a lifecycle state records as, and whether it has one. Claude's cancelled
// is the store's canceled.
func promptStateOf(state session.LifecycleState) (string, bool) {
	switch state {
	case session.LifecycleQueued:
		return store.PromptQueued, true
	case session.LifecycleStarted:
		return store.PromptStarted, true
	case session.LifecycleCompleted:
		return store.PromptCompleted, true
	case session.LifecycleCancelled:
		return store.PromptCancelled, true
	case session.LifecycleDiscarded:
		return store.PromptDiscarded, true
	default:
		return "", false
	}
}

// stoppedTasks returns the store ops that record each task still running as stopped, in task id order, and forgets
// those tasks.
func (r *recorder) stoppedTasks() []store.EngineOp {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := slices.Sorted(maps.Keys(r.tasks))
	ops := make([]store.EngineOp, 0, len(ids))
	for _, id := range ids {
		ops = append(ops, r.tasks[id].op(r.id, id, taskStopped, "", ""))
	}
	clear(r.tasks)
	return ops
}

// followedTurnEnd returns the store ops that record ended, a turn's end that another turn follows: its turn end and the
// removal of its unfinished text, as for any turn's end, with the session left working, since claude runs the next
// turn at once. ownerInterrupted is whether the owner's interrupt caused it.
func (r *recorder) followedTurnEnd(ended session.TurnEnded, ownerInterrupted bool) []store.EngineOp {
	return r.turnEnd(ended, protocol.StateWorking, ownerInterrupted)
}

// turnOpened returns the store op that records a turn claude started by itself as running: the session goes working.
func (r *recorder) turnOpened() []store.EngineOp {
	working := string(protocol.StateWorking)
	return []store.EngineOp{store.EngineSet{ID: r.id, Changes: store.EngineChanges{State: &working}}}
}

// turnEnd returns the store ops that record ended with the session left in state and its question cleared.
// ownerInterrupted is whether the owner's interrupt caused the turn's end.
func (r *recorder) turnEnd(ended session.TurnEnded, state protocol.State, ownerInterrupted bool) []store.EngineOp {
	clear(r.texts)
	after := string(state)
	return []store.EngineOp{
		store.EngineTurnEnded{ID: r.id, Subtype: ended.Subtype, IsError: ended.IsError,
			TerminalReason: ended.TerminalReason, Result: ended.Result, Origin: ended.Origin, Answered: ended.Answered,
			OwnerInterrupted: ownerInterrupted},
		store.EngineTextClear{ID: r.id},
		store.EngineSet{ID: r.id, Changes: store.EngineChanges{State: &after, Question: new("")}},
	}
}

// delta grows the unfinished text of one block by text and returns its whole text so far.
func (r *recorder) delta(kind, messageID string, index int, parentToolUseID, text string) []store.EngineOp {
	key := textKey{messageID: messageID, index: index, parentToolUseID: parentToolUseID}
	open := r.texts[key]
	open.kind = kind
	open.text += text
	r.texts[key] = open
	return []store.EngineOp{store.EngineText{ID: r.id, MessageID: messageID, Index: index,
		ParentToolUseID: parentToolUseID, Kind: kind, Text: open.text}}
}

// block returns the EngineBlock for a finished block, then the EngineTextDone of the open text it closes, if any.
func (r *recorder) block(ev session.Block) []store.EngineOp {
	ops := []store.EngineOp{store.EngineBlock{
		ID: r.id, Kind: string(ev.Kind), Role: ev.Role, MessageID: ev.MessageID, ParentToolUseID: ev.ParentToolUseID,
		Text: ev.Text, ToolUseID: ev.ToolUseID, Name: ev.Name, Input: string(ev.Input), Content: string(ev.Content),
		IsError: ev.IsError,
	}}
	if key, ok := r.closeText(ev); ok {
		ops = append(ops, store.EngineTextDone{ID: r.id, MessageID: key.messageID, Index: key.index,
			ParentToolUseID: key.parentToolUseID})
	}
	return ops
}

// closeText removes the open text the finished block ev closes: the lowest-index open block of its message, parent
// and kind. It reports false when no open text matches, as when nothing streamed.
func (r *recorder) closeText(ev session.Block) (textKey, bool) {
	var found textKey
	matched := false
	for key, open := range r.texts {
		if key.messageID != ev.MessageID || key.parentToolUseID != ev.ParentToolUseID || open.kind != string(ev.Kind) {
			continue
		}
		if !matched || key.index < found.index {
			found = key
			matched = true
		}
	}
	if matched {
		delete(r.texts, found)
	}
	return found, matched
}

// request returns the EngineRequest for a request, then, unless the session refused it or the host answered it before
// the session was re-adopted, the waiting state with the question the host must answer.
func (r *recorder) request(ev session.Request) []store.EngineOp {
	req := store.EngineRequest{
		ID: r.id, Kind: string(ev.Kind), RequestID: ev.RequestID, ToolUseID: ev.ToolUseID, ToolName: ev.ToolName,
		Subtype: ev.Subtype, Input: string(ev.Input), Refused: ev.Refused,
	}
	if ev.Refused || ev.Answered {
		return []store.EngineOp{req}
	}
	waiting := string(protocol.StateWaiting)
	question := requestQuestion(ev)
	return []store.EngineOp{
		req,
		store.EngineSet{ID: r.id, Changes: store.EngineChanges{State: &waiting, Question: &question}},
	}
}

// requestQuestion is the question a request shows: the first question of an AskUserQuestion request, the tool name of
// a permission request, and empty otherwise. An AskUserQuestion input that does not decode, or has no questions,
// shows an empty question.
func requestQuestion(ev session.Request) string {
	switch ev.Kind {
	case session.RequestQuestion:
		var input struct {
			Questions []struct {
				Question string `json:"question"`
			} `json:"questions"`
		}
		if err := json.Unmarshal(ev.Input, &input); err != nil || len(input.Questions) == 0 {
			return ""
		}
		return input.Questions[0].Question
	case session.RequestPermission:
		return ev.ToolName
	default:
		return ""
	}
}

// requestChoices returns the option labels an open headless request offers and the id it is answered by: allow and
// deny for a permission request, and the labels of the only question of a question request that is not multiSelect.
// Any other event, a refused or answered request, and a request of another shape offers none, and so does a question
// with no options. The shapes mirror the ones the server answers by.
func requestChoices(event session.Event) (options []string, request string) {
	ev, ok := event.(session.Request)
	if !ok || ev.Refused || ev.Answered {
		return nil, ""
	}
	switch ev.Kind {
	case session.RequestPermission:
		return []string{"allow", "deny"}, ev.RequestID
	case session.RequestQuestion:
		options = singleChoiceLabels(ev.Input)
		if len(options) == 0 {
			return nil, ""
		}
		return options, ev.RequestID
	default:
		return nil, ""
	}
}

// singleChoiceLabels returns the option labels, in order, of an AskUserQuestion input that has exactly one question
// and that question is not multiSelect. Any other input, or one that does not decode, has none.
func singleChoiceLabels(input json.RawMessage) []string {
	var fields struct {
		Questions []struct {
			MultiSelect bool `json:"multiSelect"`
			Options     []struct {
				Label string `json:"label"`
			} `json:"options"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(input, &fields); err != nil || len(fields.Questions) != 1 ||
		fields.Questions[0].MultiSelect {
		return nil
	}
	var labels []string
	for _, option := range fields.Questions[0].Options {
		labels = append(labels, option.Label)
	}
	return labels
}

// eventNotice returns the state and body of the notice event sends the owner, and whether it sends one. reply is the
// text of the turn's last finished main-conversation text block, and interrupted reports that the turn was interrupted
// through Interrupt, so its end sends nothing.
func eventNotice(event session.Event, reply string, interrupted bool) (state, body string, ok bool) {
	switch ev := event.(type) {
	case session.Request:
		if ev.Refused || ev.Answered {
			return "", "", false
		}
		return string(protocol.StateWaiting), requestQuestion(ev), true
	case session.TurnEnded:
		if interrupted {
			return "", "", false
		}
		return string(protocol.StateDone), reply, true
	case session.Exited:
		reason, failed := exitFailure(ev)
		if !failed {
			return "", "", false
		}
		return stateFailed, reason, true
	default:
		return "", "", false
	}
}

// mainText returns the text of event when it is a finished assistant text block of the main conversation.
func mainText(event session.Event) (string, bool) {
	block, ok := event.(session.Block)
	if !ok || block.Kind != session.BlockText || block.Role != "assistant" || block.ParentToolUseID != "" {
		return "", false
	}
	return block.Text, true
}

// consumedOf returns the EngineConsumed that stores the offset of line for session id, and whether line holds a
// TurnEnded. The offset is the line's End. A line holding a TurnEnded also stores that End as the replay-from and the
// turn's structured output, and a line holding BackgroundTasks stores whether its last report lists a task and whether it
// lists one that runs a model (see awaitsTask).
func consumedOf(id string, line session.Line) (store.EngineConsumed, bool) {
	consumed := store.EngineConsumed{ID: id, Consumed: line.End}
	ended := false
	for _, event := range line.Events {
		switch ev := event.(type) {
		case session.TurnEnded:
			replayFrom := line.End
			output := string(ev.StructuredOutput)
			consumed.ReplayFrom = &replayFrom
			consumed.Output = &output
			ended = true
		case session.BackgroundTasks:
			background, awaited := len(ev.Tasks) > 0, awaitsTask(ev.Tasks)
			consumed.Background, consumed.Awaited = &background, &awaited
		}
	}
	return consumed, ended
}

// holdsExit reports whether line is the exit line the session sends when its process ends. It holds no stdout bytes,
// so it stores no consumed offset.
func holdsExit(line session.Line) bool {
	return slices.ContainsFunc(line.Events, func(event session.Event) bool {
		_, ok := event.(session.Exited)
		return ok
	})
}

// exitChanges is the state an exited session takes: stopped when it exited cleanly, else failed with the reason of
// exitFailure.
func exitChanges(ev session.Exited) store.EngineChanges {
	reason, failed := exitFailure(ev)
	if !failed {
		stopped := stateStopped
		return store.EngineChanges{State: &stopped}
	}
	state := stateFailed
	return store.EngineChanges{State: &state, Reason: &reason}
}

// exitFailure returns the reason a failed exit records: the errors that ended it and the last non-empty line of its
// stderr. It reports false for a clean exit.
func exitFailure(ev session.Exited) (string, bool) {
	if ev.ExitErr == nil && ev.StreamErr == nil {
		return "", false
	}
	reason := errors.Join(ev.ExitErr, ev.StreamErr).Error()
	if line := lastStderrLine(ev.Stderr); line != "" {
		reason += ": " + line
	}
	return reason, true
}

// lastStderrLine returns the last line of stderr that is not blank, trimmed, or "" when every line is blank.
func lastStderrLine(stderr string) string {
	lines := strings.Split(stderr, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}
