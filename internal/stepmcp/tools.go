// Package stepmcp exposes the orrery step, epic and evidence commands as MCP tools.
package stepmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/jsonschema-go/jsonschema"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/rpcretry"
)

// Option configures NewServer.
type Option func(*serverOptions)

// serverOptions is what the options of NewServer set. A nil crons leaves the cron tools to answer each call with a tool
// error, a nil designs leaves the design tools to answer each call with a tool error, and a nil stage leaves the stage
// tools to answer each call with a tool error.
type serverOptions struct {
	crons   enginev1connect.CronsClient
	designs enginev1connect.DesignsClient
	stage   enginev1connect.StageClient
}

// WithCrons returns the option that makes the cron tools call crons.
func WithCrons(crons enginev1connect.CronsClient) Option {
	return func(opts *serverOptions) {
		opts.crons = crons
	}
}

// WithDesigns returns the option that makes the design tools call designs.
func WithDesigns(designs enginev1connect.DesignsClient) Option {
	return func(opts *serverOptions) {
		opts.designs = designs
	}
}

// WithStage returns the option that makes the stage tools call stage.
func WithStage(stage enginev1connect.StageClient) Option {
	return func(opts *serverOptions) {
		opts.stage = stage
	}
}

// NewServer returns the MCP server "orrery" whose step tools call steps, whose epic tools call epics, whose contract
// tools call contracts, whose evidence tools call evidence, whose cron tools call the crons that opts set, whose
// design tools call the designs that opts set and whose stage tools call the stage that opts set.
func NewServer(
	steps enginev1connect.StepsClient,
	epics enginev1connect.EpicsClient,
	contracts enginev1connect.ContractsClient,
	evidence enginev1connect.EvidenceClient,
	opts ...Option,
) (*Server, error) {
	var set serverOptions
	for _, opt := range opts {
		opt(&set)
	}
	return newServer(steps, epics, contracts, evidence, set.crons, set.designs, set.stage, rpcretry.SystemClock{})
}

// newServer returns the MCP server "orrery" whose step tools call steps and time their retries on clock, whose epic
// tools call epics, whose contract tools call contracts, whose evidence tools call evidence, whose cron tools call
// crons, whose design tools call designs, whose stage tools call stage, and whose record tools call stage and
// contracts. A tool name that two tools share is an error.
func newServer(
	steps enginev1connect.StepsClient,
	epics enginev1connect.EpicsClient,
	contracts enginev1connect.ContractsClient,
	evidence enginev1connect.EvidenceClient,
	crons enginev1connect.CronsClient,
	designs enginev1connect.DesignsClient,
	stage enginev1connect.StageClient,
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
	contractList, err := contractTools(contracts)
	if err != nil {
		return nil, fmt.Errorf("new orrery server: %w", err)
	}
	evidenceList, err := evidenceTools(evidence)
	if err != nil {
		return nil, fmt.Errorf("new orrery server: %w", err)
	}
	cronList, err := cronTools(crons)
	if err != nil {
		return nil, fmt.Errorf("new orrery server: %w", err)
	}
	designList, err := designTools(designs)
	if err != nil {
		return nil, fmt.Errorf("new orrery server: %w", err)
	}
	stageList, err := stageTools(stage)
	if err != nil {
		return nil, fmt.Errorf("new orrery server: %w", err)
	}
	recordList, err := recordTools(stage, contracts)
	if err != nil {
		return nil, fmt.Errorf("new orrery server: %w", err)
	}
	tools := slices.Concat(stepList, epicList, contractList, evidenceList, cronList, designList, stageList, recordList)
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
	resultInput, err := argumentSchema[stepResultArgs](nil)
	if err != nil {
		return nil, err
	}
	checkInput, err := argumentSchema[stepCheckArgs](nil)
	if err != nil {
		return nil, err
	}
	listInput, err := argumentSchema[stepListArgs](nil)
	if err != nil {
		return nil, err
	}
	return []Tool{
		{
			Name: "step_start",
			Description: "Start a step: an agent working in a workspace. Returns the step id and next. " +
				"The step's end arrives in this conversation as an orrery-event of kind step_end.",
			InputSchema: startInput,
			Handler:     startHandler(steps, clock),
		},
		{
			Name: "step_status",
			Description: "Report a step's current state by id. It returns the state only: " +
				"no return and no check output.",
			InputSchema: idInput,
			Handler:     statusHandler(steps, clock),
		},
		{
			Name: "step_result",
			Description: "Read one part of a step's return by id. With only id it returns the outline: each top-level " +
				"field with its type and size, no values. With field it returns that field's value, a string as text " +
				"and anything else as compact JSON; with field and item, one element of that array field, by its id " +
				"property or 0-based index. A value is cut at limit runes (default and most 4000) and the result names " +
				"the offset to continue from.",
			InputSchema: resultInput,
			Handler:     stepResultHandler(steps, clock),
		},
		{
			Name: "step_check",
			Description: "Read one check of a step by id and check name: its round, whether it passed, its detail " +
				"(at most 4000 runes), and a slice of its gate output from the log orrery kept, or from the stored " +
				"tails when none was kept. The slice is the last lines lines (default 40, at most 80, each cut at " +
				"150 runes) before line before, keeping only lines containing match when given; the result names " +
				"the before to continue from. round defaults to the check's last round. An unknown check is an " +
				"error naming the step's checks.",
			InputSchema: checkInput,
			Handler:     stepCheckHandler(steps, clock),
		},
		{
			Name: "step_list",
			Description: "List the steps this session started, one line each: id, agent, epic, task, end (running " +
				"while the step runs) and status. It lists each running step and the 20 most recently ended, oldest " +
				"first, only one epic's when epic is given. The text is at most 4000 bytes; when steps are cut, the " +
				"earliest go and a first line counts them. Read one step with step_status, step_result or step_check.",
			InputSchema: listInput,
			Handler:     stepListHandler(steps, clock),
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
	}, nil
}

// startedNext is the next of step_start's result: what the caller does with the started step. Its end arrives as an
// event, so the caller does not poll.
const startedNext = "The step runs in the background. Its end arrives in this conversation as an orrery-event of " +
	"kind step_end, so do not poll for it."

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
	Purpose    string          `json:"purpose,omitempty"`
}

// idArgs is the arguments of a tool that takes one id, the step tools and the epic tools alike.
type idArgs struct {
	ID string `json:"id"`
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
				Purpose:    args.Purpose,
			}))
			return errors.Join(callErr)
		})
		if err != nil {
			return toolFailure(err), nil
		}
		return textResult(map[string]string{"id": started.Msg.GetId(), "next": startedNext})
	}
}

// statusHandler returns the handler of step_status, which answers the step's state only: no return and no check output.
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

// reasonCap is the most runes of a step's reason that its step object carries. A longer first line is cut to its first
// reasonCap-1 runes and the ellipsis rune.
const reasonCap = 200

// stepJSON is a step as the tools report it: its state only, with no return, workspace, revision, read-only flag,
// worktree or check output.
type stepJSON struct {
	ID          string      `json:"id"`
	Agent       string      `json:"agent"`
	End         string      `json:"end"`
	Status      string      `json:"status"`
	Stage       string      `json:"stage"`
	Doing       string      `json:"doing"`
	Epic        string      `json:"epic"`
	Task        string      `json:"task"`
	Started     int64       `json:"started"`
	Ended       int64       `json:"ended"`
	Limited     bool        `json:"limited"`
	LimitKind   string      `json:"limitKind"`
	LimitResets int64       `json:"limitResets"`
	Reason      string      `json:"reason"`
	Checks      []checkJSON `json:"checks"`
}

// checkJSON is one check of a step: its round, its name and whether it passed. A check's output is not reported.
type checkJSON struct {
	Round uint32 `json:"round"`
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
}

// reasonLine returns the first line of reason, the text before its first newline, cut to reasonCap runes as stepJSON's
// reason is.
func reasonLine(reason string) string {
	line, _, _ := strings.Cut(reason, "\n")
	runes := []rune(line)
	if len(runes) <= reasonCap {
		return line
	}
	return string(runes[:reasonCap-1]) + "…"
}

func stepFromProto(step *v1.Step) stepJSON {
	checks := make([]checkJSON, 0, len(step.GetChecks()))
	for _, check := range step.GetChecks() {
		checks = append(checks, checkJSON{
			Round: check.GetRound(),
			Name:  check.GetName(),
			OK:    check.GetOk(),
		})
	}
	return stepJSON{
		ID:          step.GetId(),
		Agent:       step.GetAgent(),
		End:         step.GetEnd(),
		Status:      step.GetStatus(),
		Stage:       step.GetStage(),
		Doing:       step.GetDoing(),
		Epic:        step.GetEpic(),
		Task:        step.GetTask(),
		Started:     step.GetStarted(),
		Ended:       step.GetEnded(),
		Limited:     step.GetLimited(),
		LimitKind:   step.GetLimitKind(),
		LimitResets: step.GetLimitResets(),
		Reason:      reasonLine(step.GetReason()),
		Checks:      checks,
	}
}

// StepObject returns step as the state-only step object that step_status reports, with no trailing newline.
// orrery step status prints through it, so its output cannot drift from the tool's.
func StepObject(step *v1.Step) ([]byte, error) {
	text, err := json.Marshal(stepFromProto(step))
	if err != nil {
		return nil, fmt.Errorf("encode step: %w", err)
	}
	return text, nil
}

// textResult returns value as the JSON text of a single text content block.
func textResult(value any) (ToolResult, error) {
	text, err := json.Marshal(value)
	if err != nil {
		return ToolResult{}, fmt.Errorf("encode result: %w", err)
	}
	return ToolResult{Content: []Content{{Type: textContentType, Text: string(text)}}}, nil
}
