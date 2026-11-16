// Package stepmcp exposes the orrery step, epic and evidence commands as MCP tools.
package stepmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/google/jsonschema-go/jsonschema"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/rpcretry"
)

// MaxWait is the longest a step_wait call holds open. A longer wait is clamped to it, so
// the call returns inside the time Claude Code allows a tool call and the caller asks again.
const MaxWait = 25 * time.Minute

// NewServer returns the MCP server "orrery" whose step tools call steps, whose epic tools call epics and whose evidence
// tools call evidence.
func NewServer(
	steps enginev1connect.StepsClient,
	epics enginev1connect.EpicsClient,
	evidence enginev1connect.EvidenceClient,
) (*Server, error) {
	return newServer(steps, epics, evidence, rpcretry.SystemClock{})
}

// newServer returns the MCP server "orrery" whose step tools call steps and time their retries on clock, whose epic
// tools call epics and whose evidence tools call evidence. A tool name that two tools share is an error.
func newServer(
	steps enginev1connect.StepsClient,
	epics enginev1connect.EpicsClient,
	evidence enginev1connect.EvidenceClient,
	clock rpcretry.Clock,
) (*Server, error) {
	stepList, err := stepTools(steps, clock)
	if err != nil {
		return nil, fmt.Errorf("new orrery server: %w", err)
	}
	epicList, err := epicTools(epics)
	if err != nil {
		return nil, fmt.Errorf("new orrery server: %w", err)
	}
	evidenceList, err := evidenceTools(evidence)
	if err != nil {
		return nil, fmt.Errorf("new orrery server: %w", err)
	}
	tools := slices.Concat(stepList, epicList, evidenceList)
	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if names[tool.Name] {
			return nil, fmt.Errorf("new orrery server: duplicate tool name %q", tool.Name)
		}
		names[tool.Name] = true
	}
	return &Server{tools: tools}, nil
}

// stepTools returns the step tools, each calling steps and timing its retries on clock.
func stepTools(steps enginev1connect.StepsClient, clock rpcretry.Clock) ([]Tool, error) {
	startInput, err := argumentSchema[startArgs](nil)
	if err != nil {
		return nil, err
	}
	idInput, err := argumentSchema[idArgs](nil)
	if err != nil {
		return nil, err
	}
	waitInput, err := argumentSchema[waitArgs](func(root *jsonschema.Schema) {
		root.Properties["ids"].MinItems = new(1)
		root.Properties["timeoutSeconds"] = &jsonschema.Schema{Type: "integer", Minimum: new(0.0)}
	})
	if err != nil {
		return nil, err
	}
	return []Tool{
		{
			Name:        "step_start",
			Description: "Start a step: an agent working in a workspace. Returns the step id.",
			InputSchema: startInput,
			Handler:     startHandler(steps, clock),
		},
		{
			Name:        "step_status",
			Description: "Report a step's current state by id.",
			InputSchema: idInput,
			Handler:     statusHandler(steps, clock),
		},
		{
			Name:        "step_stop",
			Description: "Stop a running step by id.",
			InputSchema: idInput,
			Handler:     stopHandler(steps, clock),
		},
		{
			Name:        "step_resume",
			Description: "Resume a step a usage limit holds by id.",
			InputSchema: idInput,
			Handler:     resumeHandler(steps, clock),
		},
		{
			Name: "step_wait",
			Description: "Wait until one of the given steps ends, or until timeoutSeconds passes " +
				"(at most 1500 seconds). Returns the ended step, or null with every step's state at timeout.",
			InputSchema: waitInput,
			Handler:     waitHandler(steps, clock),
		},
	}, nil
}

// startArgs is the arguments of step_start. A step's workspace and prompt are required; the rest are optional.
type startArgs struct {
	Agent      string          `json:"agent,omitempty"`
	Prompt     string          `json:"prompt"`
	Workspace  string          `json:"workspace"`
	Schema     json.RawMessage `json:"schema,omitempty"     jsonschema:"JSON schema of the step's return value"`
	Revision   string          `json:"revision,omitempty"`
	DoneValue  string          `json:"doneValue,omitempty"`
	ReadOnly   bool            `json:"readOnly,omitempty"`
	OwnedPaths []string        `json:"ownedPaths,omitempty"`
	GateName   string          `json:"gateName,omitempty"`
	GatesFile  string          `json:"gatesFile,omitempty"`
	Epic       string          `json:"epic,omitempty"`
	Task       string          `json:"task,omitempty"`
}

// idArgs is the arguments of a tool that takes one id, the step tools and the epic tools alike.
type idArgs struct {
	ID string `json:"id"`
}

// waitArgs is the arguments of step_wait. TimeoutSeconds is a pointer so that an omitted wait differs from a zero one.
type waitArgs struct {
	IDs            []string `json:"ids"`
	TimeoutSeconds *float64 `json:"timeoutSeconds,omitempty"`
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

// argumentSchema returns the input schema of the argument struct T as JSON text, inferred from its fields. Inference
// makes each slice and pointer nullable and reads a json.RawMessage as an array of bytes, so argumentSchema types them
// as the tools have always listed them. adjust, when not nil, first sets the properties inference cannot type.
func argumentSchema[T any](adjust func(root *jsonschema.Schema)) (json.RawMessage, error) {
	root, err := jsonschema.For[T](&jsonschema.ForOptions{
		TypeSchemas: map[reflect.Type]*jsonschema.Schema{
			reflect.TypeFor[json.RawMessage](): {Type: "object"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("infer input schema: %w", err)
	}
	if adjust != nil {
		adjust(root)
	}
	nonNullable(root)
	text, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("encode input schema: %w", err)
	}
	// Inference omits an empty required list and an empty properties object, which every listed schema carries.
	var object map[string]any
	if err = json.Unmarshal(text, &object); err != nil {
		return nil, fmt.Errorf("encode input schema: %w", err)
	}
	if _, ok := object["properties"]; !ok {
		object["properties"] = map[string]any{}
	}
	if _, ok := object["required"]; !ok {
		object["required"] = []string{}
	}
	out, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("encode input schema: %w", err)
	}
	return out, nil
}

// nonNullable sets the type of schema, and of each schema under its properties and items, to the type it lists besides
// null.
func nonNullable(schema *jsonschema.Schema) {
	if len(schema.Types) > 0 {
		for _, typ := range schema.Types {
			if typ != "null" {
				schema.Type = typ
			}
		}
		schema.Types = nil
	}
	for _, property := range schema.Properties {
		nonNullable(property)
	}
	if schema.Items != nil {
		nonNullable(schema.Items)
	}
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
func toolFailure(err error) ToolResult {
	return ToolResult{Content: []Content{{Type: textContentType, Text: err.Error()}}, IsError: true}
}

func startHandler(steps enginev1connect.StepsClient, clock rpcretry.Clock) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args startArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if err := requireString("prompt", args.Prompt); err != nil {
			return ToolResult{}, err
		}
		if err := requireString("workspace", args.Workspace); err != nil {
			return ToolResult{}, err
		}
		schema, err := schemaText(args.Schema)
		if err != nil {
			return ToolResult{}, err
		}
		// A start is retried only when no connection was made, so a start cut mid-call never runs twice.
		var started *connect.Response[v1.StartStepResponse]
		err = rpcretry.Do(ctx, clock, rpcretry.NeverConnected, func(callCtx context.Context) error {
			var callErr error
			started, callErr = steps.StartStep(callCtx, connect.NewRequest(&v1.StartStepRequest{
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
			return errors.Join(callErr)
		})
		if err != nil {
			return toolFailure(err), nil
		}
		return textResult(map[string]string{"id": started.Msg.GetId()})
	}
}

func statusHandler(steps enginev1connect.StepsClient, clock rpcretry.Clock) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args idArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if err := requireString("id", args.ID); err != nil {
			return ToolResult{}, err
		}
		var status *connect.Response[v1.StepStatusResponse]
		err := rpcretry.Do(ctx, clock, rpcretry.Unavailable, func(callCtx context.Context) error {
			var callErr error
			status, callErr = steps.StepStatus(callCtx, connect.NewRequest(&v1.StepStatusRequest{Id: args.ID}))
			return errors.Join(callErr)
		})
		if err != nil {
			return toolFailure(err), nil
		}
		return textResult(stepFromProto(status.Msg.GetStep()))
	}
}

// idCallHandler returns the handler of a tool that takes a step id and makes one call for it. The call is retried on
// Unavailable, and a call that succeeds answers the id with outcome set to true.
func idCallHandler(clock rpcretry.Clock, outcome string, call func(context.Context, string) error) ToolHandler {
	return func(ctx context.Context, tool Call) (ToolResult, error) {
		var args idArgs
		if err := decodeArguments(tool.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if err := requireString("id", args.ID); err != nil {
			return ToolResult{}, err
		}
		err := rpcretry.Do(ctx, clock, rpcretry.Unavailable, func(callCtx context.Context) error {
			return errors.Join(call(callCtx, args.ID))
		})
		if err != nil {
			return toolFailure(err), nil
		}
		return textResult(map[string]any{"id": args.ID, outcome: true})
	}
}

func stopHandler(steps enginev1connect.StepsClient, clock rpcretry.Clock) ToolHandler {
	return idCallHandler(clock, "stopped", func(ctx context.Context, id string) error {
		_, err := steps.StopStep(ctx, connect.NewRequest(&v1.StopStepRequest{Id: id}))
		return errors.Join(err)
	})
}

func resumeHandler(steps enginev1connect.StepsClient, clock rpcretry.Clock) ToolHandler {
	return idCallHandler(clock, "resumed", func(ctx context.Context, id string) error {
		_, err := steps.ResumeStep(ctx, connect.NewRequest(&v1.ResumeStepRequest{Id: id}))
		return errors.Join(err)
	})
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

// waitSteps waits on ids for timeout seconds from now. When orreryd is unavailable mid-wait, it waits again for the
// time the request has left, and gives up only at the request's deadline. Each request asks for that time rounded up
// to whole seconds, so a retry never ends before the deadline.
func waitSteps(
	ctx context.Context,
	steps enginev1connect.StepsClient,
	clock rpcretry.Clock,
	ids []string,
	timeout uint32,
) (*connect.Response[v1.WaitStepsResponse], error) {
	deadline := clock.Now().Add(time.Duration(timeout) * time.Second)
	retryable := func(err error) bool {
		return rpcretry.Unavailable(err) && clock.Now().Before(deadline)
	}
	var waited *connect.Response[v1.WaitStepsResponse]
	call := func(callCtx context.Context) error {
		var callErr error
		waited, callErr = steps.WaitSteps(callCtx, connect.NewRequest(&v1.WaitStepsRequest{
			Ids:            ids,
			TimeoutSeconds: roundUpSeconds(deadline.Sub(clock.Now())),
		}))
		return errors.Join(callErr)
	}
	for {
		// Do stops after its own window, so a wait that outlasts the window runs Do again until its deadline.
		err := rpcretry.Do(ctx, clock, retryable, call)
		if err == nil || !retryable(err) {
			return waited, errors.Join(err)
		}
	}
}

// roundUpSeconds returns d in whole seconds, rounded up, and 0 when d has passed.
func roundUpSeconds(d time.Duration) uint32 {
	if d <= 0 {
		return 0
	}
	return uint32(math.Ceil(d.Seconds()))
}

func waitHandler(steps enginev1connect.StepsClient, clock rpcretry.Clock) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args waitArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if len(args.IDs) == 0 {
			return ToolResult{}, errors.New("ids is required")
		}
		if slices.Contains(args.IDs, "") {
			return ToolResult{}, errors.New("ids must not contain an empty id")
		}
		timeout, err := waitSeconds(args.TimeoutSeconds)
		if err != nil {
			return ToolResult{}, err
		}
		waited, err := waitSteps(ctx, steps, clock, args.IDs, timeout)
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
	ID          string      `json:"id"`
	Agent       string      `json:"agent"`
	Workspace   string      `json:"workspace"`
	Revision    string      `json:"revision"`
	ReadOnly    bool        `json:"readOnly"`
	Worktree    string      `json:"worktree"`
	End         string      `json:"end"`
	Status      string      `json:"status"`
	Result      string      `json:"result"`
	Checks      []checkJSON `json:"checks"`
	Reason      string      `json:"reason"`
	Started     int64       `json:"started"`
	Ended       int64       `json:"ended"`
	Limited     bool        `json:"limited"`
	LimitKind   string      `json:"limitKind"`
	LimitResets int64       `json:"limitResets"`
}

// checkJSON is one check of a step. A gate check carries the redacted tails of its stdout and stderr, and a failed gate
// check the file its output was kept in.
type checkJSON struct {
	Round  uint32 `json:"round"`
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
	Log    string `json:"log,omitempty"`
}

func stepFromProto(step *v1.Step) stepJSON {
	checks := make([]checkJSON, 0, len(step.GetChecks()))
	for _, check := range step.GetChecks() {
		checks = append(checks, checkJSON{
			Round:  check.GetRound(),
			Name:   check.GetName(),
			OK:     check.GetOk(),
			Detail: check.GetDetail(),
			Stdout: check.GetStdout(),
			Stderr: check.GetStderr(),
			Log:    check.GetLog(),
		})
	}
	return stepJSON{
		ID:          step.GetId(),
		Agent:       step.GetAgent(),
		Workspace:   step.GetWorkspace(),
		Revision:    step.GetRevision(),
		ReadOnly:    step.GetReadOnly(),
		Worktree:    step.GetWorktree(),
		End:         step.GetEnd(),
		Status:      step.GetStatus(),
		Result:      step.GetResult(),
		Checks:      checks,
		Reason:      step.GetReason(),
		Started:     step.GetStarted(),
		Ended:       step.GetEnded(),
		Limited:     step.GetLimited(),
		LimitKind:   step.GetLimitKind(),
		LimitResets: step.GetLimitResets(),
	}
}

// textResult returns value as the JSON text of a single text content block.
func textResult(value any) (ToolResult, error) {
	text, err := json.Marshal(value)
	if err != nil {
		return ToolResult{}, fmt.Errorf("encode result: %w", err)
	}
	return ToolResult{Content: []Content{{Type: textContentType, Text: string(text)}}}, nil
}
