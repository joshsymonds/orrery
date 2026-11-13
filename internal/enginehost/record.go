package enginehost

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// Engine session states the protocol does not name: a session that ended cleanly or with an error.
const (
	stateStopped = "stopped"
	stateFailed  = "failed"
)

// openText is the unfinished text of one streaming block: its kind and the text streamed so far.
type openText struct {
	kind string
	text string
}

// recorder translates the events of one headless session into the store ops that record them. It keeps the
// unfinished text of each open block and the claude session it last recorded. It is not safe for concurrent use.
type recorder struct {
	id            string
	claudeSession string
	texts         map[textKey]openText
}

// newRecorder returns a recorder for the session id.
func newRecorder(id string) *recorder {
	return &recorder{id: id, texts: map[textKey]openText{}}
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
	case session.RequestCancelled:
		working := string(protocol.StateWorking)
		return []store.EngineOp{
			store.EngineRequestCancelled{ID: r.id, RequestID: ev.RequestID, ToolUseID: ev.ToolUseID},
			store.EngineSet{ID: r.id, Changes: store.EngineChanges{State: &working, Question: new("")}},
		}
	case session.TurnEnded:
		clear(r.texts)
		done := string(protocol.StateDone)
		return []store.EngineOp{
			store.EngineTurnEnded{ID: r.id, Subtype: ev.Subtype, IsError: ev.IsError, TerminalReason: ev.TerminalReason,
				Result: ev.Result, Origin: ev.Origin},
			store.EngineTextClear{ID: r.id},
			store.EngineSet{ID: r.id, Changes: store.EngineChanges{State: &done, Question: new("")}},
		}
	case session.Exited:
		clear(r.texts)
		return []store.EngineOp{store.EngineTextClear{ID: r.id}, store.EngineSet{ID: r.id, Changes: exitChanges(ev)}}
	default:
		return nil
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
// turn's structured output, and a line holding BackgroundTasks stores whether its last report lists a task.
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
			background := len(ev.Tasks) > 0
			consumed.Background = &background
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
