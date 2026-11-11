package session

import "encoding/json"

// Event is one thing a session reports to its host. It is sealed: only the event types of this
// package implement it.
type Event interface {
	isEvent()
}

// Started reports that a session initialized: the session id claude assigned and the permission mode
// the session runs in.
type Started struct {
	SessionID      string
	PermissionMode string
}

func (Started) isEvent() {}

// TextDelta is one streamed piece of assistant text. MessageID names the message it belongs to, Index
// the content block within that message, and ParentToolUseID the tool call of the subagent that streams
// it, empty for the main conversation.
type TextDelta struct {
	MessageID       string
	Index           int
	ParentToolUseID string
	Text            string
}

func (TextDelta) isEvent() {}

// ThinkingDelta is one streamed piece of thinking, keyed the same way as TextDelta.
type ThinkingDelta struct {
	MessageID       string
	Index           int
	ParentToolUseID string
	Text            string
}

func (ThinkingDelta) isEvent() {}

// ToolCallStarted reports that a tool call's content block began: its tool use id and tool name. It
// is keyed the same way as TextDelta.
type ToolCallStarted struct {
	MessageID       string
	Index           int
	ParentToolUseID string
	ToolUseID       string
	Name            string
}

func (ToolCallStarted) isEvent() {}

// BlockKind names the kind of a finished content block.
type BlockKind string

// The kinds of finished content block.
const (
	BlockText       BlockKind = "text"
	BlockThinking   BlockKind = "thinking"
	BlockToolCall   BlockKind = "tool_call"
	BlockToolResult BlockKind = "tool_result"
)

// Block is one finished content block. Role is the side that wrote it, "assistant" or "user". A tool
// call carries its ToolUseID, Name and Input; a tool result carries its ToolUseID, Content and IsError.
// A thinking block carries its text and never its signature.
type Block struct {
	Kind            BlockKind
	Role            string
	MessageID       string
	ParentToolUseID string
	Text            string
	ToolUseID       string
	Name            string
	Input           json.RawMessage
	Content         json.RawMessage
	IsError         bool
}

func (Block) isEvent() {}

// RequestKind names the kind of a request the session makes of its host.
type RequestKind string

// The kinds of request a session makes of its host.
const (
	RequestQuestion   RequestKind = "question"
	RequestPermission RequestKind = "permission"
	RequestOther      RequestKind = "other"
)

// Request is a control request the session waits on its host to answer. Refused reports a request the
// package cannot answer: the session declines it itself, so the host must not answer it.
type Request struct {
	Kind      RequestKind
	RequestID string
	ToolUseID string
	ToolName  string
	Subtype   string
	Input     json.RawMessage
	Refused   bool
}

func (Request) isEvent() {}

// RequestCancelled reports that the session withdrew an open request before the host answered it.
type RequestCancelled struct {
	RequestID string
	ToolUseID string
}

func (RequestCancelled) isEvent() {}

// NoticeKind names the kind of a notice the session reports without a turn.
type NoticeKind string

// The kinds of notice a session reports.
const (
	NoticeCompaction    NoticeKind = "compaction"
	NoticeAPIRetry      NoticeKind = "api_retry"
	NoticeAuthStatus    NoticeKind = "auth_status"
	NoticeInformational NoticeKind = "informational"
	NoticeHookFailed    NoticeKind = "hook_failed"
)

// Notice is a message the session reports about itself: its text, and the line it came from.
type Notice struct {
	Kind NoticeKind
	Text string
	Line []byte
}

func (Notice) isEvent() {}

// BackgroundTask is one task the session runs in the background.
type BackgroundTask struct {
	TaskID      string
	TaskType    string
	Description string
}

// BackgroundTasks reports the background tasks the session runs now, replacing any earlier report.
type BackgroundTasks struct {
	Tasks []BackgroundTask
}

func (BackgroundTasks) isEvent() {}

// TurnEnded reports that a turn finished: its result subtype, whether it failed, the terminal reason
// claude gave, the result text of a successful turn, the structured return of a successful turn (nil
// when the turn gave none) and the kind of origin that ended it.
type TurnEnded struct {
	Subtype          string
	IsError          bool
	TerminalReason   string
	Result           string
	StructuredOutput json.RawMessage
	Origin           string
}

func (TurnEnded) isEvent() {}

// Unknown reports a line the translator could not decode or that does not fit the declared
// messages. Line is a copy of the line and Err says why it was not translated.
type Unknown struct {
	Line []byte
	Err  error
}

func (Unknown) isEvent() {}

// Exited reports that the session process ended: its exit error, the error that ended its output
// stream, and the stderr it wrote.
type Exited struct {
	ExitErr   error
	StreamErr error
	Stderr    string
}

func (Exited) isEvent() {}
