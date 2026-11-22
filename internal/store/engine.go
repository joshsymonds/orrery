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

// KindStep is the sessions.kind of a step session. It is an engine-style row that the Reader never lists.
const KindStep = "step"

// Engine event kinds appended to the events table. Each payload is a JSON object fixed per kind.
const (
	EventEngineAdded            = "engine_added"
	EventEngineChanged          = "engine_changed"
	EventEnginePrompt           = "engine_prompt"
	EventEngineBlock            = "engine_block"
	EventEngineRequest          = "engine_request"
	EventEngineRequestCancelled = "engine_request_canceled"
	EventEngineTurnEnded        = "engine_turn_ended"
	EventEngineInterrupt        = "engine_interrupt"
	EventEngineTask             = "engine_task"
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

// EngineAdd inserts a session row of Kind and appends engine_added. Kind is KindEngine or KindStep; empty is KindEngine.
type EngineAdd struct {
	ID    string
	Dir   string
	State string
	Kind  string
}

// EngineChanges holds the fields an EngineSet may change. A nil field is left as it is, except Reason: a State with a nil
// Reason clears a reason the row has, since the reason belongs to the state change that set it.
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

// Senders of an engine prompt, stored as its sender.
const (
	PromptSenderBrief  = "brief"
	PromptSenderCron   = "cron"
	PromptSenderEngine = "engine"
	PromptSenderEvent  = "event"
	PromptSenderOwner  = "owner"
)

// EnginePrompt appends engine_prompt for a prompt sent to a session. PromptID is the client-chosen id of the
// prompt, stored only when non-empty. Sender and Round are stored only when non-empty and non-zero. Client is the
// X-Orrery-Client of the request that sent the prompt, stored only when non-empty. WriteID is the prompt's write id,
// stored in the event's write_id column and left out of the payload.
type EnginePrompt struct {
	ID       string
	Text     string
	PromptID string
	Sender   string
	Round    int
	Client   string
	WriteID  string
}

// EngineLaunch starts tracking an engine session's consumed offset from zero. It sets consumed and replay_from to
// 0, turns background and awaited off, and sets prompts_from to the newest event seq, so HolderPrompts lists only the prompts
// sent after the launch. It appends no event.
type EngineLaunch struct {
	ID string
}

// EngineConsumed sets the consumed offset of an engine session. ReplayFrom, Output, Background and Awaited are each
// stored only when non-nil, and the other columns are left as they are. Background is whether the session's last
// background-task report lists a task, and Awaited whether it lists one that runs a model. It appends no event.
type EngineConsumed struct {
	ID         string
	Consumed   int64
	ReplayFrom *int64
	Output     *string
	Background *bool
	Awaited    *bool
}

// EnginePromptFailed records that a prompt failed to send. The marker keeps the first reason for its session and
// prompt id, and appends no event.
type EnginePromptFailed struct {
	ID       string
	PromptID string
	Reason   string
}

// EnginePromptSent reports whether an engine_prompt event of the session carries promptID. An event prompt whose
// newest state is canceled or discarded does not count, so a caller event Claude cancelled or discarded before it
// started reads as not sent and can be sent again under the same prompt id. Prompts of other senders, and prompts
// with no sender, count whatever their state. It reads the writer's database, as EngineSession does.
func (s *Store) EnginePromptSent(ctx context.Context, sessionID, promptID string) (bool, error) {
	count, err := sqlcgen.New(s.read).CountEnginePrompts(ctx, sqlcgen.CountEnginePromptsParams{
		SessionID: sessionID, PromptID: promptID,
	})
	if err != nil {
		return false, fmt.Errorf("count engine prompt %s of session %s: %w", promptID, sessionID, err)
	}
	return count > 0, nil
}

// EnginePromptFailure reports the reason a prompt failed to send, and whether a failure marker exists for it. It
// reads the writer's database, as EnginePromptSent does.
func (s *Store) EnginePromptFailure(ctx context.Context, sessionID, promptID string) (string, bool, error) {
	reason, err := sqlcgen.New(s.read).GetEnginePromptFailure(ctx, sqlcgen.GetEnginePromptFailureParams{
		SessionID: sessionID, PromptID: promptID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read failure of engine prompt %s of session %s: %w", promptID, sessionID, err)
	}
	return reason, true, nil
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
	Added           int64
	Removed         int64
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

// EngineTurnEnded appends engine_turn_ended when a turn ends. Answered lists the uuids of the user messages the turn
// answered, in the order its result listed them. OwnerInterrupted is whether the owner's Interrupt ended the turn,
// stored as ownerInterrupted only when true.
type EngineTurnEnded struct {
	ID               string
	Subtype          string
	IsError          bool
	TerminalReason   string
	Result           string
	Origin           string
	Answered         []string
	OwnerInterrupted bool
}

// EngineInterrupt appends engine_interrupt when the owner's Interrupt of a session is recorded. Turning is whether a
// turn ran when it was recorded.
type EngineInterrupt struct {
	ID      string
	Turning bool
}

// EngineTask appends engine_task for one background task a session reported.
type EngineTask struct {
	ID           string
	TaskID       string
	ToolUseID    string
	Description  string
	Type         string
	SubagentType string
	Status       string
	LastTool     string
	Summary      string
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
	Sender   string `json:"sender,omitempty"`
	Round    int    `json:"round,omitempty"`
	Client   string `json:"client,omitempty"`
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
	Added           int64  `json:"added,omitempty"`
	Removed         int64  `json:"removed,omitempty"`
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
	Subtype          string `json:"subtype"`
	IsError          bool   `json:"isError"`
	TerminalReason   string `json:"terminalReason"`
	Result           string `json:"result"`
	Origin           string `json:"origin"`
	OwnerInterrupted bool   `json:"ownerInterrupted,omitempty"`
}

type engineInterruptPayload struct {
	Turning bool `json:"turning"`
}

type engineTaskPayload struct {
	TaskID       string `json:"taskId"`
	ToolUseID    string `json:"toolUseId"`
	Description  string `json:"description"`
	Type         string `json:"type"`
	SubagentType string `json:"subagentType"`
	Status       string `json:"status"`
	LastTool     string `json:"lastTool"`
	Summary      string `json:"summary"`
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
	kind := op.Kind
	if kind == "" {
		kind = KindEngine
	}
	if err := queries.InsertEngineSession(ctx, sqlcgen.InsertEngineSessionParams{
		ID: op.ID, Kind: kind, Dir: op.Dir, State: op.State, Changed: engineStamp(now),
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
	// A row's reason is the reason of the state change that set it: a failed or an interrupted state carries one. A later
	// state change that carries none, such as working after a resume, clears it.
	if op.Changes.State != nil && op.Changes.Reason == nil && row.Reason != "" {
		empty := ""
		set("reason", &empty, &params.Reason)
	}
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

// isEngineSession reports whether session is an engine session, a row of kind engine. A session with no row is not one.
func isEngineSession(ctx context.Context, queries *sqlcgen.Queries, session string) (bool, error) {
	row, err := queries.GetSession(ctx, session)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read session %s: %w", session, err)
	}
	return row.Kind == KindEngine, nil
}

// firstEnginePrompt reports whether session is an engine session with no engine_prompt event yet, so a prompt appended
// now is its first.
func firstEnginePrompt(ctx context.Context, queries *sqlcgen.Queries, session string) (bool, error) {
	engine, err := isEngineSession(ctx, queries, session)
	if err != nil {
		return false, err
	}
	if !engine {
		return false, nil
	}
	prompts, err := queries.CountSessionPrompts(ctx, session)
	if err != nil {
		return false, fmt.Errorf("count prompts of session %s: %w", session, err)
	}
	return prompts == 0, nil
}

func (op EnginePrompt) applyEngine(ctx context.Context, queries *sqlcgen.Queries, now time.Time) error {
	payload, err := json.Marshal(enginePromptPayload{
		Text: op.Text, PromptID: op.PromptID, Sender: op.Sender, Round: op.Round, Client: op.Client,
	})
	if err != nil {
		return fmt.Errorf("encode %s payload: %w", EventEnginePrompt, err)
	}
	first, err := firstEnginePrompt(ctx, queries, op.ID)
	if err != nil {
		return err
	}
	statement := ctx
	if first {
		statement = withHousekeepMark(ctx, hookFirstPrompt, op.ID)
	}
	if err = queries.InsertEnginePromptEvent(statement, sqlcgen.InsertEnginePromptEventParams{
		SessionID: op.ID, At: now.Unix(), Payload: string(payload), WriteID: op.WriteID,
	}); err != nil {
		return fmt.Errorf("append %s for session %s: %w", EventEnginePrompt, op.ID, err)
	}
	// first_prompt_text is set only while it is empty, so a later prompt leaves it, and so does the prune of the event
	// this prompt appended. An empty first prompt leaves it empty, and the next prompt fills it.
	if err = queries.SetSessionFirstPrompt(ctx, sqlcgen.SetSessionFirstPromptParams{
		Text: op.Text, ID: op.ID,
	}); err != nil {
		return fmt.Errorf("set first prompt of session %s: %w", op.ID, err)
	}
	return nil
}

func (op EngineLaunch) applyEngine(ctx context.Context, queries *sqlcgen.Queries, _ time.Time) error {
	promptsFrom, err := queries.NewestEventSeq(ctx)
	if err != nil {
		return fmt.Errorf("read newest event seq: %w", err)
	}
	affected, err := queries.UpdateEngineLaunch(ctx, sqlcgen.UpdateEngineLaunchParams{
		PromptsFrom: promptsFrom, ID: op.ID,
	})
	if err != nil {
		return fmt.Errorf("launch engine session %s: %w", op.ID, err)
	}
	return engineRowAffected(op.ID, affected)
}

func (op EngineConsumed) applyEngine(ctx context.Context, queries *sqlcgen.Queries, _ time.Time) error {
	params := sqlcgen.UpdateEngineConsumedParams{
		Consumed: sql.NullInt64{Int64: op.Consumed, Valid: true}, ID: op.ID,
	}
	if op.ReplayFrom != nil {
		params.ReplayFrom = sql.NullInt64{Int64: *op.ReplayFrom, Valid: true}
	}
	if op.Output != nil {
		params.TurnOutput = sql.NullString{String: *op.Output, Valid: true}
	}
	if op.Background != nil {
		params.Background = sql.NullBool{Bool: *op.Background, Valid: true}
	}
	if op.Awaited != nil {
		params.Awaited = sql.NullBool{Bool: *op.Awaited, Valid: true}
	}
	affected, err := queries.UpdateEngineConsumed(ctx, params)
	if err != nil {
		return fmt.Errorf("update consumed of engine session %s: %w", op.ID, err)
	}
	return engineRowAffected(op.ID, affected)
}

// engineRowAffected reports the outcome of an engine session UPDATE that matched affected rows. No row means the
// id names no engine or step session.
func engineRowAffected(id string, affected int64) error {
	switch affected {
	case 1:
		return nil
	case 0:
		return fmt.Errorf("engine session %s: %w", id, ErrNoEngineSession)
	default:
		return fmt.Errorf("update engine session %s: %d rows affected, want 1", id, affected)
	}
}

func (op EnginePromptFailed) applyEngine(ctx context.Context, queries *sqlcgen.Queries, _ time.Time) error {
	if err := queries.InsertEnginePromptFailed(ctx, sqlcgen.InsertEnginePromptFailedParams{
		SessionID: op.ID, PromptID: op.PromptID, Reason: op.Reason,
	}); err != nil {
		return fmt.Errorf("insert failure of engine prompt %s of session %s: %w", op.PromptID, op.ID, err)
	}
	return nil
}

func (op EngineBlock) applyEngine(ctx context.Context, queries *sqlcgen.Queries, now time.Time) error {
	text, cutText := capEngineText(op.Text)
	input, cutInput := capEngineText(op.Input)
	content, cutContent := capEngineText(op.Content)
	if err := appendEnginePayload(ctx, queries, op.ID, EventEngineBlock, engineBlockPayload{
		Kind: op.Kind, Role: op.Role, MessageID: op.MessageID, ParentToolUseID: op.ParentToolUseID,
		Text: text, ToolUseID: op.ToolUseID, Name: op.Name, Input: input, Content: content,
		IsError: op.IsError, Truncated: cutText || cutInput || cutContent, Added: op.Added, Removed: op.Removed,
	}, now); err != nil {
		return err
	}
	if op.Kind != "text" || op.Role != "assistant" || op.ParentToolUseID != "" {
		return nil
	}
	// latest_reply is the text of the newest main-conversation reply block. A later such block replaces it, which is
	// the listing's ORDER BY at DESC, seq DESC whenever at does not move backwards: blocks are written in seq order.
	if err := queries.SetSessionLatestReply(ctx, sqlcgen.SetSessionLatestReplyParams{
		LatestReply: text, At: now.Unix(), ID: op.ID,
	}); err != nil {
		return fmt.Errorf("set latest reply of session %s: %w", op.ID, err)
	}
	return nil
}

func (op EngineRequest) applyEngine(ctx context.Context, queries *sqlcgen.Queries, now time.Time) error {
	if err := appendEnginePayload(ctx, queries, op.ID, EventEngineRequest, engineRequestPayload{
		Kind: op.Kind, RequestID: op.RequestID, ToolUseID: op.ToolUseID, ToolName: op.ToolName,
		Subtype: op.Subtype, Input: op.Input, Refused: op.Refused,
	}, now); err != nil {
		return err
	}
	if op.Refused {
		return nil
	}
	if err := queries.SetSessionLatestRequest(ctx, sqlcgen.SetSessionLatestRequestParams{
		RequestID: op.RequestID, At: now.Unix(), ID: op.ID,
	}); err != nil {
		return fmt.Errorf("set latest request of session %s: %w", op.ID, err)
	}
	return nil
}

func (op EngineRequestCancelled) applyEngine(ctx context.Context, queries *sqlcgen.Queries, now time.Time) error {
	return appendEnginePayload(ctx, queries, op.ID, EventEngineRequestCancelled,
		engineRequestCancelledPayload{RequestID: op.RequestID, ToolUseID: op.ToolUseID}, now)
}

func (op EngineTurnEnded) applyEngine(ctx context.Context, queries *sqlcgen.Queries, now time.Time) error {
	payload, err := encodeEnginePayload(EventEngineTurnEnded, engineTurnEndedPayload{
		Subtype: op.Subtype, IsError: op.IsError, TerminalReason: op.TerminalReason,
		Result: op.Result, Origin: op.Origin, OwnerInterrupted: op.OwnerInterrupted,
	})
	if err != nil {
		return err
	}
	answered := op.Answered
	if answered == nil {
		answered = []string{}
	}
	list, err := json.Marshal(answered)
	if err != nil {
		return fmt.Errorf("encode %s answered of session %s: %w", EventEngineTurnEnded, op.ID, err)
	}
	engine, err := isEngineSession(ctx, queries, op.ID)
	if err != nil {
		return err
	}
	statement := ctx
	if engine {
		statement = withHousekeepMark(ctx, hookTurnEnded, op.ID)
	}
	if err = queries.InsertEngineTurnEndedEvent(statement, sqlcgen.InsertEngineTurnEndedEventParams{
		SessionID: op.ID, At: now.Unix(), Payload: payload, Answered: sql.NullString{String: string(list), Valid: true},
	}); err != nil {
		return fmt.Errorf("append %s for session %s: %w", EventEngineTurnEnded, op.ID, err)
	}
	return nil
}

func (op EngineInterrupt) applyEngine(ctx context.Context, queries *sqlcgen.Queries, now time.Time) error {
	return appendEnginePayload(ctx, queries, op.ID, EventEngineInterrupt,
		engineInterruptPayload{Turning: op.Turning}, now)
}

func (op EngineTask) applyEngine(ctx context.Context, queries *sqlcgen.Queries, now time.Time) error {
	return appendEnginePayload(ctx, queries, op.ID, EventEngineTask, engineTaskPayload{
		TaskID: op.TaskID, ToolUseID: op.ToolUseID, Description: op.Description, Type: op.Type,
		SubagentType: op.SubagentType, Status: op.Status, LastTool: op.LastTool, Summary: op.Summary,
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

// encodeEnginePayload encodes value as the JSON object payload of an engine event of kind.
func encodeEnginePayload(kind string, value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode %s payload: %w", kind, err)
	}
	return string(payload), nil
}

// appendEnginePayload appends one engine event whose payload is value encoded as a JSON object.
func appendEnginePayload(ctx context.Context, queries *sqlcgen.Queries, sessionID, kind string, value any,
	now time.Time,
) error {
	payload, err := encodeEnginePayload(kind, value)
	if err != nil {
		return err
	}
	return appendEvent(ctx, queries, sessionID, kind, payload, now)
}
