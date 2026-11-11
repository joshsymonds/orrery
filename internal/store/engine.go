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
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// KindEngine is the sessions.kind of a headless engine session. Pane sessions have kind "".
const KindEngine = "engine"

// Engine event kinds appended to the events table. Each payload is a JSON object fixed per kind.
const (
	EventEngineAdded            = "engine_added"
	EventEngineChanged          = "engine_changed"
	EventEnginePrompt           = "engine_prompt"
	EventEngineBlock            = "engine_block"
	EventEngineRequest          = "engine_request"
	EventEngineRequestCancelled = "engine_request_canceled"
	EventEngineTurnEnded        = "engine_turn_ended"
)

// EngineTextCap is the most bytes of block text, tool input and tool content the store keeps.
const EngineTextCap = 32 * 1024

// IsEngineKind reports whether an events.kind belongs to a headless engine session.
func IsEngineKind(kind string) bool {
	return strings.HasPrefix(kind, "engine_")
}

// EngineOp is one engine write. The interface is sealed: only this package defines ops.
type EngineOp interface {
	applyEngine(ctx context.Context, queries *sqlcgen.Queries, now time.Time) error
}

// EngineAdd inserts an engine session row and appends engine_added.
type EngineAdd struct {
	ID    string
	Dir   string
	State string
}

// EngineChanges holds the fields an EngineSet may change. A nil field is left as it is.
type EngineChanges struct {
	State         *string
	Seen          *string
	Question      *string
	ClaudeSession *string
	Reason        *string
}

// EngineSet updates the set fields of an engine session and appends one engine_changed naming them.
type EngineSet struct {
	ID      string
	Changes EngineChanges
}

// EnginePrompt appends engine_prompt for a prompt sent to a session. PromptID is the client-chosen id of the
// prompt, stored only when non-empty.
type EnginePrompt struct {
	ID       string
	Text     string
	PromptID string
}

// EnginePromptSent reports whether an engine_prompt event of the session carries promptID. It reads the writer's
// database, as EngineSession does.
func (s *Store) EnginePromptSent(ctx context.Context, sessionID, promptID string) (bool, error) {
	count, err := sqlcgen.New(s.db).CountEnginePrompts(ctx, sqlcgen.CountEnginePromptsParams{
		SessionID: sessionID, PromptID: promptID,
	})
	if err != nil {
		return false, fmt.Errorf("count engine prompt %s of session %s: %w", promptID, sessionID, err)
	}
	return count > 0, nil
}

// EngineBlock appends engine_block for a finished block.
type EngineBlock struct {
	ID              string
	Kind            string
	Role            string
	MessageID       string
	ParentToolUseID string
	Text            string
	ToolUseID       string
	Name            string
	Input           string
	Content         string
	IsError         bool
}

// EngineRequest appends engine_request for a permission or control request.
type EngineRequest struct {
	ID        string
	Kind      string
	RequestID string
	ToolUseID string
	ToolName  string
	Subtype   string
	Input     string
	Refused   bool
}

// EngineRequestCancelled appends the request-withdrawn event for a request the session took back.
type EngineRequestCancelled struct {
	ID        string
	RequestID string
	ToolUseID string
}

// EngineTurnEnded appends engine_turn_ended when a turn ends.
type EngineTurnEnded struct {
	ID             string
	Subtype        string
	IsError        bool
	TerminalReason string
	Result         string
	Origin         string
}

// EngineText upserts the unfinished row of one streaming block.
type EngineText struct {
	ID              string
	MessageID       string
	Index           int
	ParentToolUseID string
	Kind            string
	Text            string
}

// EngineTextDone deletes the unfinished row of one block once the block is stored.
type EngineTextDone struct {
	ID              string
	MessageID       string
	Index           int
	ParentToolUseID string
}

// EngineTextClear deletes every unfinished row of one session.
type EngineTextClear struct {
	ID string
}

type engineAddedPayload struct {
	Dir   string `json:"dir"`
	State string `json:"state"`
}

type engineChangedPayload struct {
	Fields map[string]string `json:"fields"`
}

type enginePromptPayload struct {
	Text     string `json:"text"`
	PromptID string `json:"promptId,omitempty"`
}

type engineBlockPayload struct {
	Kind            string `json:"kind"`
	Role            string `json:"role"`
	MessageID       string `json:"messageId"`
	ParentToolUseID string `json:"parentToolUseId"`
	Text            string `json:"text"`
	ToolUseID       string `json:"toolUseId"`
	Name            string `json:"name"`
	Input           string `json:"input"`
	Content         string `json:"content"`
	IsError         bool   `json:"isError"`
	Truncated       bool   `json:"truncated,omitempty"`
}

type engineRequestPayload struct {
	Kind      string `json:"kind"`
	RequestID string `json:"requestId"`
	ToolUseID string `json:"toolUseId"`
	ToolName  string `json:"toolName"`
	Subtype   string `json:"subtype"`
	Input     string `json:"input"`
	Refused   bool   `json:"refused"`
}

type engineRequestCancelledPayload struct {
	RequestID string `json:"requestId"`
	ToolUseID string `json:"toolUseId"`
}

type engineTurnEndedPayload struct {
	Subtype        string `json:"subtype"`
	IsError        bool   `json:"isError"`
	TerminalReason string `json:"terminalReason"`
	Result         string `json:"result"`
	Origin         string `json:"origin"`
}

// EngineOpError reports that the op at Index in an ApplyEngine batch failed. The whole batch was rolled back. It
// unwraps to the op's error.
type EngineOpError struct {
	Index int
	Err   error
}

func (e *EngineOpError) Error() string {
	return fmt.Sprintf("engine op %d: %v", e.Index, e.Err)
}

func (e *EngineOpError) Unwrap() error {
	return e.Err
}

// ApplyEngine applies ops in order inside one write transaction. A failed op, or a commit that fails, rolls the
// whole batch back, so no session, event or unfinished-text row from the batch is kept. The error is an
// *EngineOpError naming the failed op's index.
func (s *Store) ApplyEngine(ctx context.Context, ops []EngineOp, now time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		for i, op := range ops {
			if err := op.applyEngine(ctx, queries, now); err != nil {
				return &EngineOpError{Index: i, Err: err}
			}
		}
		return nil
	})
}

func (op EngineAdd) applyEngine(ctx context.Context, queries *sqlcgen.Queries, now time.Time) error {
	if err := queries.InsertEngineSession(ctx, sqlcgen.InsertEngineSessionParams{
		ID: op.ID, Dir: op.Dir, State: op.State, Changed: engineStamp(now),
	}); err != nil {
		return fmt.Errorf("insert engine session %s: %w", op.ID, err)
	}
	return appendEnginePayload(ctx, queries, op.ID, EventEngineAdded,
		engineAddedPayload{Dir: op.Dir, State: op.State}, now)
}

func (op EngineSet) applyEngine(ctx context.Context, queries *sqlcgen.Queries, now time.Time) error {
	row, err := queries.GetEngineSession(ctx, op.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("engine session %s: %w", op.ID, ErrNoEngineSession)
	}
	if err != nil {
		return fmt.Errorf("read engine session %s: %w", op.ID, err)
	}
	params := sqlcgen.UpdateEngineSessionParams{
		State: row.State, Changed: row.Changed, Seen: row.Seen, Question: row.Question,
		ClaudeSession: row.ClaudeSession, Reason: row.Reason, ID: op.ID,
	}
	fields := map[string]string{}
	set := func(name string, value *string, target *string) {
		if value != nil {
			*target = *value
			fields[name] = *value
		}
	}
	if op.Changes.State != nil {
		params.Changed = engineStamp(now)
		fields["changed"] = params.Changed
	}
	set("state", op.Changes.State, &params.State)
	set("seen", op.Changes.Seen, &params.Seen)
	set("question", op.Changes.Question, &params.Question)
	set("claudeSession", op.Changes.ClaudeSession, &params.ClaudeSession)
	set("reason", op.Changes.Reason, &params.Reason)
	if len(fields) == 0 {
		return nil
	}

	affected, err := queries.UpdateEngineSession(ctx, params)
	if err != nil {
		return fmt.Errorf("update engine session %s: %w", op.ID, err)
	}
	if affected != 1 {
		return fmt.Errorf("update engine session %s: %d rows affected, want 1", op.ID, affected)
	}
	return appendEnginePayload(ctx, queries, op.ID, EventEngineChanged, engineChangedPayload{Fields: fields}, now)
}

func (op EnginePrompt) applyEngine(ctx context.Context, queries *sqlcgen.Queries, now time.Time) error {
	return appendEnginePayload(ctx, queries, op.ID, EventEnginePrompt,
		enginePromptPayload{Text: op.Text, PromptID: op.PromptID}, now)
}

func (op EngineBlock) applyEngine(ctx context.Context, queries *sqlcgen.Queries, now time.Time) error {
	text, cutText := capEngineText(op.Text)
	input, cutInput := capEngineText(op.Input)
	content, cutContent := capEngineText(op.Content)
	return appendEnginePayload(ctx, queries, op.ID, EventEngineBlock, engineBlockPayload{
		Kind: op.Kind, Role: op.Role, MessageID: op.MessageID, ParentToolUseID: op.ParentToolUseID,
		Text: text, ToolUseID: op.ToolUseID, Name: op.Name, Input: input, Content: content,
		IsError: op.IsError, Truncated: cutText || cutInput || cutContent,
	}, now)
}

func (op EngineRequest) applyEngine(ctx context.Context, queries *sqlcgen.Queries, now time.Time) error {
	return appendEnginePayload(ctx, queries, op.ID, EventEngineRequest, engineRequestPayload{
		Kind: op.Kind, RequestID: op.RequestID, ToolUseID: op.ToolUseID, ToolName: op.ToolName,
		Subtype: op.Subtype, Input: op.Input, Refused: op.Refused,
	}, now)
}

func (op EngineRequestCancelled) applyEngine(ctx context.Context, queries *sqlcgen.Queries, now time.Time) error {
	return appendEnginePayload(ctx, queries, op.ID, EventEngineRequestCancelled,
		engineRequestCancelledPayload{RequestID: op.RequestID, ToolUseID: op.ToolUseID}, now)
}

func (op EngineTurnEnded) applyEngine(ctx context.Context, queries *sqlcgen.Queries, now time.Time) error {
	return appendEnginePayload(ctx, queries, op.ID, EventEngineTurnEnded, engineTurnEndedPayload{
		Subtype: op.Subtype, IsError: op.IsError, TerminalReason: op.TerminalReason,
		Result: op.Result, Origin: op.Origin,
	}, now)
}

func (op EngineText) applyEngine(ctx context.Context, queries *sqlcgen.Queries, _ time.Time) error {
	text, _ := capEngineText(op.Text)
	if err := queries.UpsertEngineText(ctx, sqlcgen.UpsertEngineTextParams{
		SessionID: op.ID, MessageID: op.MessageID, BlockIndex: int64(op.Index),
		ParentToolUseID: op.ParentToolUseID, Kind: op.Kind, Text: text,
	}); err != nil {
		return fmt.Errorf("upsert unfinished text of session %s: %w", op.ID, err)
	}
	return nil
}

func (op EngineTextDone) applyEngine(ctx context.Context, queries *sqlcgen.Queries, _ time.Time) error {
	if err := queries.DeleteEngineText(ctx, sqlcgen.DeleteEngineTextParams{
		SessionID: op.ID, MessageID: op.MessageID, BlockIndex: int64(op.Index),
		ParentToolUseID: op.ParentToolUseID,
	}); err != nil {
		return fmt.Errorf("delete unfinished text of session %s: %w", op.ID, err)
	}
	return nil
}

func (op EngineTextClear) applyEngine(ctx context.Context, queries *sqlcgen.Queries, _ time.Time) error {
	if err := queries.ClearEngineText(ctx, op.ID); err != nil {
		return fmt.Errorf("clear unfinished text of session %s: %w", op.ID, err)
	}
	return nil
}

// engineStamp is the changed column value for a moment: its unix seconds.
func engineStamp(now time.Time) string {
	return strconv.FormatInt(now.Unix(), 10)
}

// capEngineText cuts text to at most EngineTextCap bytes at a UTF-8 rune start and reports whether it cut.
func capEngineText(text string) (string, bool) {
	if len(text) <= EngineTextCap {
		return text, false
	}
	cut := EngineTextCap
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut], true
}

// appendEnginePayload appends one engine event whose payload is value encoded as a JSON object.
func appendEnginePayload(ctx context.Context, queries *sqlcgen.Queries, sessionID, kind string, value any,
	now time.Time,
) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s payload: %w", kind, err)
	}
	return appendEvent(ctx, queries, sessionID, kind, string(payload), now)
}
