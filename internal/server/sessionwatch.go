package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/conversation"
	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// originRunes is the most runes a session's origin keeps.
const originRunes = 300

// WatchConversation answers Sessions.WatchConversation with the stream of one headless session or one labeled step.
// The limit is checked as Panes.WatchConversation checks it. The stream sends its first delta at once, then a delta for
// each poll that finds new main-thread items, a changed state, a changed unfinished set or a changed queued set, and a
// keepalive once keepalive has passed without a delta. A step session is admitted once, by StepStatus, before its first
// delta. The stream ends with the error the session read gives, when serve shuts down, or when a send stays blocked
// past the send deadline. Its send and end-of-stream writes are bounded as Events.Watch's are (see endWithin).
func (s sessionsServer) WatchConversation(
	ctx context.Context,
	req *connect.Request[orreryv1.SessionsWatchConversationRequest],
	stream *connect.ServerStream[orreryv1.SessionsWatchConversationResponse],
) error {
	if req.Msg.GetLimit() > conversationLimitMax {
		return listError(connect.CodeInvalidArgument, badQueryMessage)
	}
	limit := int(req.Msg.GetLimit())
	if limit == 0 {
		limit = conversationLimitDefault
	}
	watch := &sessionWatch{
		reader: s.reader,
		home:   s.home,
		steps:  s.steps,
		now:    s.now,
		id:     req.Msg.GetId(),
		limit:  limit,
		after:  req.Msg.GetAfter(),
	}
	return endWithin(ctx, s.runWatch(ctx, watch, stream), s.sendTimeout, s.shutdown)
}

// runWatch sends the deltas of watch on stream until the client leaves, the session's read fails, serve shuts down,
// or a send stays blocked past the send deadline. It steps watch every poll, and sends a keepalive once keepalive has
// passed since the last message.
func (s sessionsServer) runWatch(
	ctx context.Context,
	watch *sessionWatch,
	stream *connect.ServerStream[orreryv1.SessionsWatchConversationResponse],
) error {
	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()
	lastSent := time.Now()
	for {
		if stopping(s.shutdown) {
			return shutdownError()
		}
		msg, changed, err := watch.step(ctx)
		if err != nil {
			return err
		}
		if changed || time.Since(lastSent) >= s.keepalive {
			if sendErr := sendWithin(ctx, s.sendTimeout, s.shutdown, stream, msg); sendErr != nil {
				return fmt.Errorf("send session delta: %w", sendErr)
			}
			lastSent = time.Now()
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("watch conversation: %w", ctx.Err())
		case <-s.shutdown:
			return shutdownError()
		case <-ticker.C:
		}
	}
}

// sessionWatch is one WatchConversation call: the session it watches and the position its stream has reached.
type sessionWatch struct {
	reader *store.Reader
	// home is the home directory the session's Claude transcript is found under (see transcriptPath).
	home string
	// steps is asked about a step session. stepAdmitted is set once StepStatus has found the step labeled.
	steps        StepClient
	stepAdmitted bool
	id           string
	limit        int
	// after is the seq the client holds. The first read answers with the items above it, or with a reset when it is 0.
	after int64
	// seq is the seq of the newest session event the stream has read.
	seq int64
	// origin is the text of the session's first prompt, cut to originRunes, and haveOrigin reports whether the
	// session has had one.
	origin     string
	haveOrigin bool
	// transcript is the Claude transcript the context figure is read from, empty while the session has none, and
	// transcriptSeq is the offset just past the last complete line the stream has read from it. figure is the context
	// figure of that transcript.
	transcript    string
	transcriptSeq int64
	figure        conversation.Context
	// request is the newest unrefused request the session has raised.
	request *orreryv1.OpenRequest
	// interruptSeq is the seq of the newest engine_interrupt the stream has read, and interruptTurning is whether that
	// record says a turn ran. turnEndSeq is the seq of the newest engine_turn_ended, and ownerPromptSeq the seq of the
	// newest engine_prompt whose sender is the owner. leaveSeq is the seq of the newest engine_changed whose fields set
	// state to a value other than working. A seq of 0 means the stream has read no such event.
	interruptSeq     int64
	interruptTurning bool
	turnEndSeq       int64
	ownerPromptSeq   int64
	leaveSeq         int64
	// now is the clock the background window is read against. tasks holds each background task the stream has folded, by
	// task id, and taskOrder holds the same tasks in the order the stream first saw them (see foldTask).
	now       func() time.Time
	tasks     map[string]*backgroundTask
	taskOrder []*backgroundTask
	// sent is the state the stream last sent, or nil before its first delta. sentUnfinished and sentQueued are the
	// unfinished and queued sets it last sent.
	sent           *orreryv1.ConversationState
	sentUnfinished []*orreryv1.EngineText
	sentQueued     []*orreryv1.QueuedMessage
	// prompts is the prompts of the session with a lifecycle state, in send order, as the step's read found them, and
	// withheld holds the ids of the owner prompts whose newest state is canceled or discarded (see readPrompts).
	prompts  []store.PromptState
	withheld map[string]bool
}

// step reads the session once. It returns the message for the read, and true when the message is a delta: the first
// message, a read with new items, a changed state, or a changed unfinished or queued set. Otherwise the message carries
// neither items nor state, and the caller sends it only as a keepalive. A reset carries the seq of its oldest item.
func (w *sessionWatch) step(ctx context.Context) (*orreryv1.SessionsWatchConversationResponse, bool, error) {
	first := w.sent == nil
	items, oldest, history, err := w.read(ctx, first)
	if err != nil {
		return nil, false, err
	}
	w.readFigure(history)
	pending, err := w.pendingEvents(ctx, history)
	if err != nil {
		return nil, false, err
	}
	putOff, err := w.requestPutOff(ctx, history)
	if err != nil {
		return nil, false, err
	}
	state, err := w.state(history, pending, putOff)
	if err != nil {
		return nil, false, err
	}
	unfinished, err := unfinishedTexts(history)
	if err != nil {
		return nil, false, err
	}
	queued := w.queued(history)
	stateChanged := first || !proto.Equal(state, w.sent)
	unfinishedChanged := !slices.EqualFunc(unfinished, w.sentUnfinished, func(a, b *orreryv1.EngineText) bool {
		return proto.Equal(a, b)
	})
	queuedChanged := !slices.EqualFunc(queued, w.sentQueued, func(a, b *orreryv1.QueuedMessage) bool {
		return proto.Equal(a, b)
	})
	changed := stateChanged || unfinishedChanged || queuedChanged || len(items) > 0
	msg := &orreryv1.SessionsWatchConversationResponse{
		Transcript: w.id,
		Seq:        w.seq,
		Reset_:     first && w.after == 0,
		Items:      items,
		Unfinished: unfinished,
		Queued:     queued,
	}
	msg.Oldest = oldest
	if stateChanged {
		msg.State = state
	}
	if changed {
		w.sent = state
		w.sentUnfinished = unfinished
		w.sentQueued = queued
	}
	return msg, changed, nil
}

// readPrompts reads the prompts of the session with a lifecycle state, as store.Reader.PromptStates reads them. It sets
// prompts to them and withheld to the ids of the owner prompts whose newest state is canceled or discarded. A prompt
// with no id is not withheld, since no item can be matched to it by id. A failed read is answered as a failed session
// read is.
func (w *sessionWatch) readPrompts(ctx context.Context) error {
	prompts, err := w.reader.PromptStates(ctx, w.id)
	if err != nil {
		return sessionReadError(err)
	}
	w.prompts = prompts
	w.withheld = make(map[string]bool)
	for _, prompt := range prompts {
		if prompt.Sender != store.PromptSenderOwner || prompt.PromptID == "" {
			continue
		}
		if prompt.State == store.PromptCancelled || prompt.State == store.PromptDiscarded {
			w.withheld[prompt.PromptID] = true
		}
	}
	return nil
}

// withholds reports whether the stream leaves item out: the prompt item of an owner prompt whose newest state is
// canceled or discarded, matched by prompt id (see readPrompts).
func (w *sessionWatch) withholds(item *orreryv1.ConversationItem) bool {
	return item.GetKind() == "prompt" && item.GetSender() == store.PromptSenderOwner && w.withheld[item.GetPromptId()]
}

// queued returns the owner messages the session has not started, as the stream serves them (see queuedMessages). A step
// session serves none.
func (w *sessionWatch) queued(history store.EngineHistory) []*orreryv1.QueuedMessage {
	if history.Kind == store.KindStep {
		return nil
	}
	return queuedMessages(w.prompts)
}

// queuedMessages returns the QueuedMessage of each owner prompt the stream serves, in the order of prompts. A prompt
// whose newest state is queued is always served. A prompt whose newest state is canceled or discarded is served only
// while no owner prompt after it has a newest state of started or completed. A prompt whose newest state is started or
// completed is never served, and a caller prompt is never served.
func queuedMessages(prompts []store.PromptState) []*orreryv1.QueuedMessage {
	var served []*orreryv1.QueuedMessage
	// started is whether an owner prompt after the one being read has a newest state of started or completed. The
	// prompts are read newest first, so it is set before the older prompts are judged.
	started := false
	for _, prompt := range slices.Backward(prompts) {
		if prompt.Sender != store.PromptSenderOwner {
			continue
		}
		keep := false
		switch prompt.State {
		case store.PromptStarted, store.PromptCompleted:
			started = true
		case store.PromptQueued:
			keep = true
		case store.PromptCancelled, store.PromptDiscarded:
			keep = !started
		}
		if keep {
			served = append(served, &orreryv1.QueuedMessage{
				PromptId: prompt.PromptID, Text: prompt.Text, State: prompt.State,
			})
		}
	}
	slices.Reverse(served)
	return served
}

// The waits labels of a PendingEvent: speak when the owner's interrupt keeps the session quiet, and free otherwise.
const (
	waitsSpeak = "speak"
	waitsFree  = "free"
)

// pendingEvents returns the caller events the session is owed, as store.Reader.PendingCallerEvents reads them. It sets
// the waits label of each event (see waitsLabel). A step session is owed none, since caller events go to the sessions
// that start steps or create epics. A failed read is answered as a failed session read is.
func (w *sessionWatch) pendingEvents(
	ctx context.Context,
	history store.EngineHistory,
) ([]*orreryv1.PendingEvent, error) {
	if history.Kind == store.KindStep {
		return nil, nil
	}
	events, err := w.reader.PendingCallerEvents(ctx, w.id)
	if err != nil {
		return nil, sessionReadError(err)
	}
	waits := w.waitsLabel()
	pending := make([]*orreryv1.PendingEvent, 0, len(events))
	for _, event := range events {
		pending = append(pending, &orreryv1.PendingEvent{
			Id:      event.ID,
			Kind:    event.Kind,
			Step:    event.Step,
			Agent:   event.Agent,
			End:     event.End,
			Status:  event.Status,
			Reason:  event.Reason,
			Summary: event.Summary,
			Result:  event.Result,
			Epic:    event.Epic,
			Title:   event.Title,
			Waits:   waits,
		})
	}
	return pending, nil
}

// waitsLabel returns the waits label of the session's caller events: speak when its newest engine_interrupt is newer
// than its newest owner prompt, and free otherwise.
func (w *sessionWatch) waitsLabel() string {
	if w.interruptSeq > w.ownerPromptSeq {
		return waitsSpeak
	}
	return waitsFree
}

// interrupting reports whether the owner's Interrupt of the session is recorded and its turn still runs: the state is
// working, the newest engine_interrupt has turning set, and neither an engine_turn_ended nor an engine_changed that
// leaves working follows it. A session that left working after the interrupt does not read interrupting when it
// returns to working.
func (w *sessionWatch) interrupting(state string) bool {
	return state == string(protocol.StateWorking) && w.interruptTurning &&
		w.turnEndSeq < w.interruptSeq && w.leaveSeq < w.interruptSeq
}

// backgroundWindow is how many seconds an ended background task stays in the state after its end, either side of the
// clock.
const backgroundWindow = 60

// backgroundTask is one background task as the stream has folded its engine_task events (see foldTask). ended is 0
// while the task runs.
type backgroundTask struct {
	id           string
	toolUseID    string
	description  string
	taskType     string
	subagentType string
	status       string
	lastTool     string
	summary      string
	started      int64
	ended        int64
}

// foldTask folds one engine_task event into the background task its task id names, creating the task at its first
// event. The task's started time is that event's time, and its ended time is the time of the first event whose status
// is not running. Its status, tool use id, type and subagent type come from each event, and its description, last tool
// and summary keep the newest non-empty value of each.
func (w *sessionWatch) foldTask(event store.EngineEvent) error {
	body, err := sessionTask(event)
	if err != nil {
		return err
	}
	if w.tasks == nil {
		w.tasks = make(map[string]*backgroundTask)
	}
	task, ok := w.tasks[body.TaskId]
	if !ok {
		task = &backgroundTask{id: body.TaskId, started: event.At}
		w.tasks[body.TaskId] = task
		w.taskOrder = append(w.taskOrder, task)
	}
	if task.ended == 0 && body.Status != taskRunning {
		task.ended = event.At
	}
	task.toolUseID = body.ToolUseId
	task.taskType = body.Type
	task.subagentType = body.SubagentType
	task.status = body.Status
	if body.Description != "" {
		task.description = body.Description
	}
	if body.LastTool != "" {
		task.lastTool = body.LastTool
	}
	if body.Summary != "" {
		task.summary = body.Summary
	}
	return nil
}

// shows reports whether the state carries the task at now: a running task always, and an ended task while its end is
// within backgroundWindow seconds of now.
func (t *backgroundTask) shows(now int64) bool {
	if t.ended == 0 {
		return true
	}
	age := now - t.ended
	return age >= -backgroundWindow && age <= backgroundWindow
}

// row returns the task as a BackgroundTask. Its activity is the newest non-empty last tool, or else the newest non-empty
// summary.
func (t *backgroundTask) row() *orreryv1.BackgroundTask {
	activity := t.summary
	if t.lastTool != "" {
		activity = t.lastTool
	}
	return &orreryv1.BackgroundTask{
		Id:           t.id,
		ToolUseId:    t.toolUseID,
		Description:  t.description,
		Type:         t.taskType,
		SubagentType: t.subagentType,
		Status:       t.status,
		Activity:     activity,
		Started:      t.started,
		Ended:        t.ended,
	}
}

// backgroundRows returns the background tasks the state carries at now, oldest start first: each task that shows at now
// (see shows).
func (w *sessionWatch) backgroundRows(now int64) []*orreryv1.BackgroundTask {
	shown := make([]*backgroundTask, 0, len(w.taskOrder))
	for _, task := range w.taskOrder {
		if task.shows(now) {
			shown = append(shown, task)
		}
	}
	slices.SortStableFunc(shown, func(a, b *backgroundTask) int { return cmp.Compare(a.started, b.started) })
	rows := make([]*orreryv1.BackgroundTask, 0, len(shown))
	for _, task := range shown {
		rows = append(rows, task.row())
	}
	return rows
}

// keptItem is a main-thread item a read keeps, with the seq of the event it came from.
type keptItem struct {
	seq  int64
	item *orreryv1.ConversationItem
}

// read scans the session's events in pages of eventPageSize and returns the main-thread items the delta carries, the
// seq of the oldest of them when the delta is a reset (0 otherwise), and the row and unfinished text of the last page.
// The first read scans from seq 0, so it sees the whole history: the origin and the newest request. It keeps the newest
// limit items when the client asked for a reset, and the items above its seq otherwise. Later reads scan from the seq
// the stream has reached and keep every item they read. Each read also reads the prompt states (see readPrompts), and
// it keeps no item that withholds reports.
func (w *sessionWatch) read(
	ctx context.Context,
	first bool,
) ([]*orreryv1.ConversationItem, int64, store.EngineHistory, error) {
	if w.reader == nil {
		return nil, 0, store.EngineHistory{}, listError(connect.CodeUnavailable, "store unavailable")
	}
	if err := w.readPrompts(ctx); err != nil {
		return nil, 0, store.EngineHistory{}, err
	}
	reset := first && w.after == 0
	after := w.seq
	if first {
		after = 0
	}
	var kept []keptItem
	for {
		page, err := w.reader.EngineHistory(ctx, w.id, after, eventPageSize)
		if err != nil {
			return nil, 0, store.EngineHistory{}, sessionReadError(err)
		}
		if admitErr := w.admit(ctx, page.Kind); admitErr != nil {
			return nil, 0, store.EngineHistory{}, admitErr
		}
		for _, event := range page.Events {
			kept, err = w.collect(kept, event, first, reset)
			if err != nil {
				return nil, 0, store.EngineHistory{}, err
			}
		}
		if n := len(page.Events); n > 0 {
			after = page.Events[n-1].Seq
			w.seq = after
		}
		if len(page.Events) < eventPageSize {
			items, oldest := keptItems(kept, reset)
			return items, oldest, page, nil
		}
	}
}

// keptItems returns the items of kept in order, and the seq of the oldest of them when reset is set, or 0 when kept is
// empty or reset is not set.
func keptItems(kept []keptItem, reset bool) ([]*orreryv1.ConversationItem, int64) {
	items := make([]*orreryv1.ConversationItem, 0, len(kept))
	for _, k := range kept {
		items = append(items, k.item)
	}
	if !reset || len(kept) == 0 {
		return items, 0
	}
	return items, kept[0].seq
}

// admit returns nil for an engine session and for a step session StepStatus admits. Any other kind is not found.
func (w *sessionWatch) admit(ctx context.Context, kind string) error {
	switch kind {
	case store.KindEngine:
		return nil
	case store.KindStep:
		return w.checkStep(ctx)
	default:
		return sessionNotFound()
	}
}

// checkStep asks StepStatus about the step session, once per stream, and admits it when the row carries an epic label.
// A nil client, a step that is not found and a step with no label answer not_found. Any other StepStatus error answers
// unavailable, and a refused check leaves the stream with nothing sent.
func (w *sessionWatch) checkStep(ctx context.Context) error {
	if w.stepAdmitted {
		return nil
	}
	if w.steps == nil {
		return sessionNotFound()
	}
	resp, err := w.steps.StepStatus(ctx, connect.NewRequest(&enginev1.StepStatusRequest{Id: w.id}))
	var connectErr *connect.Error
	switch {
	case err != nil && errors.As(err, &connectErr) && connectErr.Code() == connect.CodeNotFound:
		return sessionNotFound()
	case err != nil:
		return listError(connect.CodeUnavailable, stepsUnavailable)
	case resp.Msg.GetStep().GetEpic() == "":
		return sessionNotFound()
	}
	w.stepAdmitted = true
	return nil
}

// collect tracks one event and appends its main-thread item to kept when the delta carries it. first and reset are
// read's flags: a first read skips the items at or below the seq the client holds, and a reset keeps the newest limit.
// An item that withholds reports is tracked but not kept, so it takes no place in the limit.
func (w *sessionWatch) collect(
	kept []keptItem,
	event store.EngineEvent,
	first, reset bool,
) ([]keptItem, error) {
	item, ok, err := sessionItem(event)
	if err == nil {
		err = w.track(event, item, ok)
	}
	if err != nil {
		return nil, listError(connect.CodeInternal, "read sessions")
	}
	if !ok || (first && event.Seq <= w.after) || w.withholds(item) {
		return kept, nil
	}
	kept = append(kept, keptItem{seq: event.Seq, item: item})
	if reset && len(kept) > w.limit {
		kept = kept[1:]
	}
	return kept, nil
}

// track records what an event sets for the stream: the origin, from the session's first prompt; the request, from each
// unrefused request; the seqs that interrupting and pending read, from each engine_interrupt, engine_turn_ended, owner
// prompt and engine_changed that leaves working; and the background task, from each engine_task (see foldTask). item
// and ok are sessionItem's result for event. It returns the decode error of a request, interrupt, state change or task.
func (w *sessionWatch) track(event store.EngineEvent, item *orreryv1.ConversationItem, ok bool) error {
	switch event.Kind {
	case store.EventEnginePrompt:
		if ok && !w.haveOrigin {
			w.origin = cutRunes(item.GetText(), originRunes)
			w.haveOrigin = true
		}
		if ok && item.GetSender() == store.PromptSenderOwner {
			w.ownerPromptSeq = event.Seq
		}
	case store.EventEngineTurnEnded:
		w.turnEndSeq = event.Seq
	case store.EventEngineChanged:
		return w.trackChange(event)
	case store.EventEngineInterrupt:
		turning, err := sessionInterruptTurning(event.Payload)
		if err != nil {
			return err
		}
		w.interruptSeq, w.interruptTurning = event.Seq, turning
	case store.EventEngineRequest:
		req, open, err := sessionRequest(event.Payload)
		if err != nil {
			return err
		}
		if open {
			w.request = req
		}
	case store.EventEngineTask:
		return w.foldTask(event)
	}
	return nil
}

// trackChange records the seq of an engine_changed whose fields set state to a value other than working, as leaveSeq.
// It returns the decode error of the payload.
func (w *sessionWatch) trackChange(event store.EngineEvent) error {
	changed, err := engineChanged(event.Payload)
	if err != nil {
		return err
	}
	if changed.State != nil && *changed.State != string(protocol.StateWorking) {
		w.leaveSeq = event.Seq
	}
	return nil
}

// requestPutOff reports whether the open request of the session is put off, as headlessPutOff judges it. The marks are
// read only while history waits on a request, so a session with no open request reads no marks and is not put off. A
// failed read is returned as readPutOffMarks answers it.
func (w *sessionWatch) requestPutOff(ctx context.Context, history store.EngineHistory) (bool, error) {
	if history.State != string(protocol.StateWaiting) || w.request == nil {
		return false, nil
	}
	marks, err := readPutOffMarks(ctx, w.reader)
	if err != nil {
		return false, err
	}
	return headlessPutOff(marks, w.id, w.request.GetId(), history.State), nil
}

// state returns the state of the session as history reads it: its origin, its headless session without the summary
// and newest reply, its context figure when its transcript is read, whether the owner's interrupt is running (see
// interrupting), the caller events it is owed (pending), the background tasks it carries at the clock's time (see
// backgroundRows), and the open request while the session waits on one. putOff is whether that request is put off, and
// the headless session and the request carry it. The request is a copy of w.request, so w.request is not changed.
func (w *sessionWatch) state(
	history store.EngineHistory,
	pending []*orreryv1.PendingEvent,
	putOff bool,
) (*orreryv1.ConversationState, error) {
	changed, err := parseStamp(history.Changed)
	if err != nil {
		return nil, listError(connect.CodeInternal, "read sessions")
	}
	seen, err := parseStamp(history.Seen)
	if err != nil {
		return nil, listError(connect.CodeInternal, "read sessions")
	}
	state := &orreryv1.ConversationState{
		Origin: w.origin,
		Session: &orreryv1.HeadlessSession{
			Id:       w.id,
			Dir:      history.Dir,
			Label:    sessionLabel(history.Dir, history.Title, history.FirstPrompt),
			State:    history.State,
			Changed:  changed,
			Seen:     seen,
			Question: history.Question,
			PutOff:   putOff,
		},
		Interrupting: w.interrupting(history.State),
		Pending:      pending,
		Background:   w.backgroundRows(w.now().Unix()),
	}
	if w.transcript != "" {
		state.Context = &orreryv1.ConversationContext{Tokens: w.figure.Tokens, Window: w.figure.Window}
	}
	if history.State == string(protocol.StateWaiting) && w.request != nil {
		request := proto.CloneOf(w.request)
		request.PutOff = putOff
		state.Request = request
	}
	return state, nil
}

// readFigure sets the stream's context figure from the Claude transcript of the session history describes. The
// transcript is read whole when it is first found or changes, and then only what it has appended since, as
// conversationWatch follows a pane's. A session with no transcript, and a transcript that is missing or unreadable,
// has no figure: the error is not reported, and the next step looks for the transcript again.
func (w *sessionWatch) readFigure(history store.EngineHistory) {
	path := transcriptPath(w.home, history.Dir, history.ClaudeSession)
	if path == "" {
		w.dropFigure()
		return
	}
	if path == w.transcript {
		if follow, err := conversation.Since(path, w.transcriptSeq); err == nil {
			w.transcriptSeq = follow.End
			if follow.HasTokens {
				w.figure.Tokens = follow.Tokens
			}
			return
		}
	}
	conv, end, err := conversation.ReadEnd(path, 0)
	if err != nil {
		w.dropFigure()
		return
	}
	w.transcript, w.transcriptSeq, w.figure = path, end, conv.Context
}

// dropFigure clears the stream's transcript and its figure.
func (w *sessionWatch) dropFigure() {
	w.transcript, w.transcriptSeq, w.figure = "", 0, conversation.Context{}
}

// transcriptPath returns the path of the Claude transcript of the session in dir with the Claude session id, under
// home: <home>/.claude/projects/<dir with each rune that is not an ASCII letter or digit replaced by '-'>/<id>.jsonl.
// It returns "" when home or id is empty, or when id holds a path separator and so names no file in that directory.
func transcriptPath(home, dir, id string) string {
	if home == "" || id == "" || strings.ContainsRune(id, filepath.Separator) {
		return ""
	}
	project := strings.Map(func(r rune) rune {
		if isASCIIAlnum(r) {
			return r
		}
		return '-'
	}, dir)
	return filepath.Join(home, ".claude", "projects", project, id+".jsonl")
}

// isASCIIAlnum reports whether r is an ASCII letter or digit.
func isASCIIAlnum(r rune) bool {
	return ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9')
}

// unfinishedTexts returns the main-conversation unfinished set of history: the text so far of each unfinished block
// with no parent tool use.
func unfinishedTexts(history store.EngineHistory) ([]*orreryv1.EngineText, error) {
	texts := make([]*orreryv1.EngineText, 0, len(history.Unfinished))
	for _, block := range history.Unfinished {
		if block.ParentToolUseID != "" {
			continue
		}
		if block.BlockIndex < math.MinInt32 || block.BlockIndex > math.MaxInt32 {
			return nil, listError(connect.CodeInternal, "read sessions")
		}
		texts = append(texts, &orreryv1.EngineText{
			MessageId: block.MessageID,
			Index:     int32(block.BlockIndex),
			Kind:      block.Kind,
			Text:      block.Text,
		})
	}
	return texts, nil
}

// sessionReadError returns the Connect error of a failed session read. Store detail never reaches the client.
func sessionReadError(err error) error {
	switch {
	case errors.Is(err, store.ErrNoEngineSession):
		return sessionNotFound()
	case errors.Is(err, store.ErrUnavailable):
		return listError(connect.CodeUnavailable, "store unavailable")
	default:
		return listError(connect.CodeInternal, "read sessions")
	}
}

// sessionNotFound is the error for a session that is missing or is not a headless engine session.
func sessionNotFound() error {
	return listError(connect.CodeNotFound, "session not found")
}
