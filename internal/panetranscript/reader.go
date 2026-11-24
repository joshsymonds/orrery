// Package panetranscript reads the turn of a pane from its Claude transcript: the prompt that opened it, the tool call
// that runs now, the calls the newest assistant turn still waits on, its last text, whether its newest reply failed,
// whether it has ended, and the context usage.
package panetranscript

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/joshsymonds/orrery/internal/conversation"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// Turn is the turn of a pane as its transcript shows it at the last Read. A field is zero when the transcript does
// not show it yet.
type Turn struct {
	// TurnStart is the unix seconds of the newest main-chain prompt.
	TurnStart int64
	// TurnEnd is the unix seconds of the main-chain line that ended the newest turn, and 0 while a turn runs. A turn ends
	// at an assistant line whose stop_reason is set and is not tool_use, at an API error line, and at an interruption
	// line. A prompt, a tool result and any other assistant line leave a turn running.
	TurnEnd int64
	// Tool is the newest main-chain tool call that has no result yet, or nil when every call has one.
	Tool *Tool
	// Pending is the main-chain tool calls of the newest assistant turn that have no result yet, oldest first.
	Pending []Call
	// LastMessage is the text of the newest assistant turn's last text block.
	LastMessage string
	// Failed is set when the newest main-chain assistant line is an API error line, and APIError is that line's error.
	Failed   bool
	APIError string
	// Context is the context usage of the newest main-chain assistant line that carries usage.
	Context conversation.Context
}

// Tool is a main-chain tool call that has no result yet. Input is the one-line display text of its input, and Start
// is the unix seconds of its line.
type Tool struct {
	ID    string
	Name  string
	Input string
	Start int64
}

// Call is a main-chain tool call that has no result yet, with its whole input.
type Call struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// Reader reads the turn of one transcript. It remembers how far it has read and the state those lines leave, so each
// Read reads only the lines appended since the last one.
type Reader struct {
	path   string
	offset int64
	state  turnState
}

// NewReader returns a Reader of the transcript at path. Its first Read reads the whole file.
func NewReader(path string) *Reader {
	return &Reader{path: path}
}

// Read reads the complete lines appended since the last Read and returns the turn they leave. A final line without
// its newline is held back until it is complete. A transcript shorter than the offset of the last Read is read from
// its start. A missing transcript returns an error that wraps fs.ErrNotExist.
func (r *Reader) Read() (Turn, error) {
	file, err := os.Open(filepath.Clean(r.path))
	if err != nil {
		return Turn{}, fmt.Errorf("open transcript: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return Turn{}, fmt.Errorf("stat transcript: %w", err)
	}
	if info.Size() < r.offset {
		r.offset = 0
		r.state = turnState{}
	}
	err = r.scan(file)
	if err != nil {
		return Turn{}, err
	}
	return r.state.turn(), nil
}

// scan applies each complete line from the offset on, and moves the offset past it. The offset stays at a final line
// that has no newline yet.
func (r *Reader) scan(file *os.File) error {
	if _, err := file.Seek(r.offset, io.SeekStart); err != nil {
		return fmt.Errorf("seek transcript: %w", err)
	}
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read transcript: %w", err)
		}
		r.state.apply(line)
		r.offset += int64(len(line))
	}
}

// turnState is what the complete lines of a transcript leave for its turn. openCalls are the main-chain tool calls
// with no result yet, oldest first, and turnCalls are those of the newest assistant turn. lastText is the last text of
// that turn, failed, apiError and usage are taken from the newest main-chain assistant line, and turnEnd is Turn's.
type turnState struct {
	turnStart int64
	turnEnd   int64
	openCalls []call
	turnCalls []call
	lastText  string
	failed    bool
	apiError  string
	usage     conversation.Context
}

// call is a main-chain tool call of a transcript.
type call struct {
	id    string
	name  string
	input json.RawMessage
	start int64
}

// transcriptLine holds the fields of one transcript line that the reader uses. Error is raw, so that a line whose
// error is not a string still decodes.
type transcriptLine struct {
	Type              string          `json:"type"`
	Timestamp         string          `json:"timestamp"`
	IsMeta            bool            `json:"isMeta"`
	IsSidechain       bool            `json:"isSidechain"`
	IsAPIErrorMessage bool            `json:"isApiErrorMessage"`
	Error             json.RawMessage `json:"error"`
	Message           struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// block is one block of a message content array.
type block map[string]json.RawMessage

// apply adds one complete transcript line to the state. A line that does not decode, and a sidechain or meta line,
// changes nothing.
func (s *turnState) apply(raw []byte) {
	var entry transcriptLine
	if json.Unmarshal(raw, &entry) != nil || entry.IsSidechain || entry.IsMeta {
		return
	}
	ts := unixSeconds(entry.Timestamp)
	switch entry.Type {
	case "user":
		s.applyUser(entry, ts)
	case "assistant":
		s.applyAssistant(raw, entry, ts)
	}
}

// applyUser resolves the calls that a user line answers, and starts a new turn at a prompt line. An interruption line
// ends the turn, and any other user line leaves it running.
func (s *turnState) applyUser(entry transcriptLine, ts int64) {
	for _, b := range blocksOf(entry.Message.Content) {
		if stringField(b, "type") == "tool_result" {
			s.resolve(stringField(b, "tool_use_id"))
		}
	}
	if isPrompt(entry.Message.Content) {
		s.turnStart = ts
		s.turnCalls = nil
		s.lastText = ""
	}
	s.turnEnd = 0
	if isInterruption(entry.Message.Content) {
		s.turnEnd = ts
	}
}

// applyAssistant records the failure of an assistant line, whether it ends the turn, and its usage, and adds its text
// and tool calls to the turn.
func (s *turnState) applyAssistant(raw []byte, entry transcriptLine, ts int64) {
	s.failed = entry.IsAPIErrorMessage
	s.apiError = stringValue(entry.Error)
	s.turnEnd = 0
	if stop := stopReason(raw); entry.IsAPIErrorMessage || stop != "" && stop != stopToolUse {
		s.turnEnd = ts
	}
	if usage, ok := conversation.AssistantContext(raw); ok {
		s.usage = usage
	}
	for _, b := range blocksOf(entry.Message.Content) {
		switch stringField(b, "type") {
		case "text":
			s.lastText = stringField(b, "text")
		case "tool_use":
			c := call{id: stringField(b, "id"), name: stringField(b, "name"), input: b["input"], start: ts}
			s.openCalls = append(s.openCalls, c)
			s.turnCalls = append(s.turnCalls, c)
		}
	}
}

// resolve drops the call with the given id from the open calls and the turn's calls, because a result answers it.
func (s *turnState) resolve(id string) {
	s.openCalls = slices.DeleteFunc(s.openCalls, func(c call) bool { return c.id == id })
	s.turnCalls = slices.DeleteFunc(s.turnCalls, func(c call) bool { return c.id == id })
}

// turn returns the Turn that the state leaves. Pending is a new slice, so the caller may keep it.
func (s *turnState) turn() Turn {
	result := Turn{
		TurnStart:   s.turnStart,
		TurnEnd:     s.turnEnd,
		LastMessage: s.lastText,
		Failed:      s.failed,
		APIError:    s.apiError,
		Context:     s.usage,
	}
	if n := len(s.openCalls); n > 0 {
		newest := s.openCalls[n-1]
		result.Tool = &Tool{
			ID:    newest.id,
			Name:  newest.name,
			Input: protocol.ToolInputText(newest.input),
			Start: newest.start,
		}
	}
	result.Pending = make([]Call, 0, len(s.turnCalls))
	for _, c := range s.turnCalls {
		result.Pending = append(result.Pending, Call{ID: c.id, Name: c.name, Input: c.input})
	}
	return result
}

// stopToolUse is the stop_reason of an assistant message that ends to call a tool, which leaves its turn running.
const stopToolUse = "tool_use"

// stopReason returns the stop_reason of the message of the transcript line raw, or "" when it has none or it is not a
// string.
func stopReason(raw []byte) string {
	var line struct {
		Message block `json:"message"`
	}
	if json.Unmarshal(raw, &line) != nil {
		return ""
	}
	return stringField(line.Message, "stop_reason")
}

// Interruption texts: Claude Code writes one as a user line's only text block when the person interrupts a turn.
const (
	interruptedText     = "[Request interrupted by user]"
	interruptedToolText = "[Request interrupted by user for tool use]"
)

// isInterruption reports whether content is one text block that holds a whole interruption text.
func isInterruption(content json.RawMessage) bool {
	blocks := blocksOf(content)
	if len(blocks) != 1 || stringField(blocks[0], "type") != "text" {
		return false
	}
	text := stringField(blocks[0], "text")
	return text == interruptedText || text == interruptedToolText
}

// isPrompt reports whether content is a prompt: a string, or an array with a block that is not a tool result.
func isPrompt(content json.RawMessage) bool {
	var blocks []block
	if err := json.Unmarshal(content, &blocks); err != nil {
		var text string
		return json.Unmarshal(content, &text) == nil
	}
	for _, b := range blocks {
		if stringField(b, "type") != "tool_result" {
			return true
		}
	}
	return false
}

// blocksOf returns the blocks of a content array, or none when content is not an array.
func blocksOf(content json.RawMessage) []block {
	var blocks []block
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	return blocks
}

// stringField returns the string value of key in b, or "" when the key is absent or not a string.
func stringField(b block, key string) string {
	return stringValue(b[key])
}

// stringValue returns the string that raw holds, or "" when raw holds another value.
func stringValue(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

// unixSeconds returns the unix time of an RFC 3339 timestamp, or 0 when it is absent or unparseable.
func unixSeconds(stamp string) int64 {
	parsed, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return 0
	}
	return parsed.Unix()
}
