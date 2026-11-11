package enginehost

import (
	"encoding/json"
	"errors"
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

// request returns the EngineRequest for a request, then, unless the session refused it, the waiting state with the
// question the host must answer.
func (r *recorder) request(ev session.Request) []store.EngineOp {
	req := store.EngineRequest{
		ID: r.id, Kind: string(ev.Kind), RequestID: ev.RequestID, ToolUseID: ev.ToolUseID, ToolName: ev.ToolName,
		Subtype: ev.Subtype, Input: string(ev.Input), Refused: ev.Refused,
	}
	if ev.Refused {
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

// exitChanges is the state an exited session takes: stopped when it exited cleanly, else failed with the errors that
// ended it and the last non-empty line of its stderr.
func exitChanges(ev session.Exited) store.EngineChanges {
	if ev.ExitErr == nil && ev.StreamErr == nil {
		stopped := stateStopped
		return store.EngineChanges{State: &stopped}
	}
	reason := errors.Join(ev.ExitErr, ev.StreamErr).Error()
	if line := lastStderrLine(ev.Stderr); line != "" {
		reason += ": " + line
	}
	failed := stateFailed
	return store.EngineChanges{State: &failed, Reason: &reason}
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
