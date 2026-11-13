// Package stepmcp exposes the orrery step commands as MCP tools.
package stepmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/mcp"
)

// MaxWait is the longest a step_wait call holds open. A longer wait is clamped to it, so
// the call returns inside the time Claude Code allows a tool call and the caller asks again.
const MaxWait = 25 * time.Minute

const (
	startSchema = `{"type":"object","properties":{` +
		`"agent":{"type":"string"},` +
		`"prompt":{"type":"string"},` +
		`"workspace":{"type":"string"},` +
		`"schema":{"type":"object","description":"JSON schema of the step's return value"},` +
		`"revision":{"type":"string"},` +
		`"doneValue":{"type":"string"},` +
		`"readOnly":{"type":"boolean"},` +
		`"ownedPaths":{"type":"array","items":{"type":"string"}},` +
		`"gateName":{"type":"string"},` +
		`"gatesFile":{"type":"string"},` +
		`"epic":{"type":"string"},` +
		`"task":{"type":"string"}` +
		`},"required":["prompt","workspace"],"additionalProperties":false}`

	idSchema = `{"type":"object","properties":{"id":{"type":"string"}},` +
		`"required":["id"],"additionalProperties":false}`

	waitSchema = `{"type":"object","properties":{` +
		`"ids":{"type":"array","items":{"type":"string"},"minItems":1},` +
		`"timeoutSeconds":{"type":"integer","minimum":0}` +
		`},"required":["ids"],"additionalProperties":false}`
)

// NewServer returns the MCP server "orrery" whose step tools call steps.
func NewServer(steps enginev1connect.StepsClient) (*mcp.Server, error) {
	server, err := mcp.NewServer("orrery", "1",
		mcp.Tool{
			Name:        "step_start",
			Description: "Start a step: an agent working in a workspace. Returns the step id.",
			InputSchema: json.RawMessage(startSchema),
			Handler:     startHandler(steps),
		},
		mcp.Tool{
			Name:        "step_status",
			Description: "Report a step's current state by id.",
			InputSchema: json.RawMessage(idSchema),
			Handler:     statusHandler(steps),
		},
		mcp.Tool{
			Name:        "step_stop",
			Description: "Stop a running step by id.",
			InputSchema: json.RawMessage(idSchema),
			Handler:     stopHandler(steps),
		},
		mcp.Tool{
			Name: "step_wait",
			Description: "Wait until one of the given steps ends, or until timeoutSeconds passes " +
				"(at most 1500 seconds). Returns the ended step, or null with every step's state at timeout.",
			InputSchema: json.RawMessage(waitSchema),
			Handler:     waitHandler(steps),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("new orrery server: %w", err)
	}
	return server, nil
}

type startArgs struct {
	Agent      string          `json:"agent"`
	Prompt     string          `json:"prompt"`
	Workspace  string          `json:"workspace"`
	Schema     json.RawMessage `json:"schema"`
	Revision   string          `json:"revision"`
	DoneValue  string          `json:"doneValue"`
	ReadOnly   bool            `json:"readOnly"`
	OwnedPaths []string        `json:"ownedPaths"`
	GateName   string          `json:"gateName"`
	GatesFile  string          `json:"gatesFile"`
	Epic       string          `json:"epic"`
	Task       string          `json:"task"`
}

type idArgs struct {
	ID string `json:"id"`
}

type waitArgs struct {
	IDs            []string `json:"ids"`
	TimeoutSeconds *float64 `json:"timeoutSeconds"`
}

// decodeArguments decodes a tool's arguments, refusing unknown keys and mistyped values.
func decodeArguments(raw json.RawMessage, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return fmt.Errorf("decode arguments: %w", err)
	}
	return nil
}

// requireString reports an error naming field when value is empty.
func requireString(field, value string) error {
	if value == "" {
		return errors.New(field + " is required")
	}
	return nil
}

// schemaText returns the step schema argument as its JSON text. An absent schema is the empty text, which the daemon
// judges for the start. A present schema must be a JSON object.
func schemaText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return "", errors.New("schema must be a JSON object")
	}
	return string(raw), nil
}

// toolFailure is the tool error for a failed daemon call. Its text is the error's own text,
// which for a Connect error reads as its code and message.
func toolFailure(err error) mcp.ToolResult {
	return mcp.ToolResult{Content: []mcp.Content{{Type: "text", Text: err.Error()}}, IsError: true}
}

func startHandler(steps enginev1connect.StepsClient) mcp.ToolHandler {
	return func(ctx context.Context, call mcp.Call) (mcp.ToolResult, error) {
		var args startArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return mcp.ToolResult{}, err
		}
		if err := requireString("prompt", args.Prompt); err != nil {
			return mcp.ToolResult{}, err
		}
		if err := requireString("workspace", args.Workspace); err != nil {
			return mcp.ToolResult{}, err
		}
		schema, err := schemaText(args.Schema)
		if err != nil {
			return mcp.ToolResult{}, err
		}
		started, err := steps.StartStep(ctx, connect.NewRequest(&v1.StartStepRequest{
			Agent:      args.Agent,
			Prompt:     args.Prompt,
			Schema:     schema,
			Workspace:  args.Workspace,
			Revision:   args.Revision,
			ReadOnly:   args.ReadOnly,
			OwnedPaths: args.OwnedPaths,
			GateName:   args.GateName,
			GatesFile:  args.GatesFile,
			DoneValue:  args.DoneValue,
			Epic:       args.Epic,
			Task:       args.Task,
		}))
		if err != nil {
			return toolFailure(err), nil
		}
		return textResult(map[string]string{"id": started.Msg.GetId()})
	}
}

func statusHandler(steps enginev1connect.StepsClient) mcp.ToolHandler {
	return func(ctx context.Context, call mcp.Call) (mcp.ToolResult, error) {
		var args idArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return mcp.ToolResult{}, err
		}
		if err := requireString("id", args.ID); err != nil {
			return mcp.ToolResult{}, err
		}
		status, err := steps.StepStatus(ctx, connect.NewRequest(&v1.StepStatusRequest{Id: args.ID}))
		if err != nil {
			return toolFailure(err), nil
		}
		return textResult(stepFromProto(status.Msg.GetStep()))
	}
}

func stopHandler(steps enginev1connect.StepsClient) mcp.ToolHandler {
	return func(ctx context.Context, call mcp.Call) (mcp.ToolResult, error) {
		var args idArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return mcp.ToolResult{}, err
		}
		if err := requireString("id", args.ID); err != nil {
			return mcp.ToolResult{}, err
		}
		if _, err := steps.StopStep(ctx, connect.NewRequest(&v1.StopStepRequest{Id: args.ID})); err != nil {
			return toolFailure(err), nil
		}
		return textResult(map[string]any{"id": args.ID, "stopped": true})
	}
}

// waitSeconds returns the requested wait in whole seconds, clamped to MaxWait. An omitted wait is MaxWait.
func waitSeconds(requested *float64) (uint32, error) {
	limit := uint32(MaxWait / time.Second)
	if requested == nil {
		return limit, nil
	}
	seconds := *requested
	if seconds < 0 || seconds != math.Trunc(seconds) {
		return 0, errors.New("timeoutSeconds must be a whole number of seconds, 0 or more")
	}
	if seconds >= float64(limit) {
		return limit, nil
	}
	return uint32(seconds), nil
}

// waitResult is the step_wait reply. Ended is null when no step ended before the timeout.
type waitResult struct {
	Ended *stepJSON  `json:"ended"`
	Steps []stepJSON `json:"steps"`
}

func waitHandler(steps enginev1connect.StepsClient) mcp.ToolHandler {
	return func(ctx context.Context, call mcp.Call) (mcp.ToolResult, error) {
		var args waitArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return mcp.ToolResult{}, err
		}
		if len(args.IDs) == 0 {
			return mcp.ToolResult{}, errors.New("ids is required")
		}
		if slices.Contains(args.IDs, "") {
			return mcp.ToolResult{}, errors.New("ids must not contain an empty id")
		}
		timeout, err := waitSeconds(args.TimeoutSeconds)
		if err != nil {
			return mcp.ToolResult{}, err
		}
		waited, err := steps.WaitSteps(ctx, connect.NewRequest(&v1.WaitStepsRequest{
			Ids:            args.IDs,
			TimeoutSeconds: timeout,
		}))
		if err != nil {
			return toolFailure(err), nil
		}
		result := waitResult{Steps: make([]stepJSON, 0, len(waited.Msg.GetSteps()))}
		if ended := waited.Msg.GetEnded(); ended != nil {
			step := stepFromProto(ended)
			result.Ended = &step
		}
		for _, step := range waited.Msg.GetSteps() {
			result.Steps = append(result.Steps, stepFromProto(step))
		}
		return textResult(result)
	}
}

// stepJSON is a step as the tools report it.
type stepJSON struct {
	ID        string      `json:"id"`
	Agent     string      `json:"agent"`
	Workspace string      `json:"workspace"`
	Revision  string      `json:"revision"`
	ReadOnly  bool        `json:"readOnly"`
	Worktree  string      `json:"worktree"`
	End       string      `json:"end"`
	Status    string      `json:"status"`
	Result    string      `json:"result"`
	Checks    []checkJSON `json:"checks"`
	Reason    string      `json:"reason"`
	Started   int64       `json:"started"`
	Ended     int64       `json:"ended"`
}

// checkJSON is one gate check of a step.
type checkJSON struct {
	Round  uint32 `json:"round"`
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func stepFromProto(step *v1.Step) stepJSON {
	checks := make([]checkJSON, 0, len(step.GetChecks()))
	for _, check := range step.GetChecks() {
		checks = append(checks, checkJSON{
			Round:  check.GetRound(),
			Name:   check.GetName(),
			OK:     check.GetOk(),
			Detail: check.GetDetail(),
		})
	}
	return stepJSON{
		ID:        step.GetId(),
		Agent:     step.GetAgent(),
		Workspace: step.GetWorkspace(),
		Revision:  step.GetRevision(),
		ReadOnly:  step.GetReadOnly(),
		Worktree:  step.GetWorktree(),
		End:       step.GetEnd(),
		Status:    step.GetStatus(),
		Result:    step.GetResult(),
		Checks:    checks,
		Reason:    step.GetReason(),
		Started:   step.GetStarted(),
		Ended:     step.GetEnded(),
	}
}

// textResult returns value as the JSON text of a single text content block.
func textResult(value any) (mcp.ToolResult, error) {
	text, err := json.Marshal(value)
	if err != nil {
		return mcp.ToolResult{}, fmt.Errorf("encode result: %w", err)
	}
	return mcp.ToolResult{Content: []mcp.Content{{Type: "text", Text: string(text)}}}, nil
}
