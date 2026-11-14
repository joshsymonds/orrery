package stepmcp

import (
	"context"
	"encoding/json"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/mcp"
)

const (
	epicCreateSchema = `{"type":"object","properties":{` +
		`"contract":{"type":"object","description":"the accepted contract in protobuf JSON field names: ` +
		`id, repository, title, approved, intent, failures, care, additions, requirements, premises, ` +
		`exclusions, approach, alternatives, releaseSteps, releaseThen, checks"},` +
		`"decisions":{"type":"array","items":{"type":"object","properties":{` +
		`"id":{"type":"string"},"text":{"type":"string"}},` +
		`"required":["id","text"],"additionalProperties":false}}` +
		`},"required":["contract"],"additionalProperties":false}`

	epicListSchema = `{"type":"object","properties":{},"required":[],"additionalProperties":false}`

	epicAnswerSchema = `{"type":"object","properties":{` +
		`"epicId":{"type":"string"},"id":{"type":"string"},"text":{"type":"string"}},` +
		`"required":["epicId","id","text"],"additionalProperties":false}`
)

// epicTools returns the epic tools, each calling epics.
func epicTools(epics enginev1connect.EpicsClient) []mcp.Tool {
	return []mcp.Tool{
		{
			Name: "epic_create",
			Description: "Create an epic from an accepted contract. decisions seeds the epic's recorded " +
				"decisions. Returns the stored epic. A refusal names the field that failed.",
			InputSchema: json.RawMessage(epicCreateSchema),
			Handler:     epicCreateHandler(epics),
		},
		{
			Name: "epic_get",
			Description: "Return a stored epic by id: its contract, phase, timestamps, decisions, whether it is " +
				"paused and why it is parked, and its planner turns, tasks and questions.",
			InputSchema: json.RawMessage(idSchema),
			Handler:     epicGetHandler(epics),
		},
		{
			Name:        "epic_list",
			Description: "List the stored epics as summaries: id, repository, title, phase, created and changed.",
			InputSchema: json.RawMessage(epicListSchema),
			Handler:     epicListHandler(epics),
		},
		{
			Name: "epic_answer",
			Description: "Record a decision on an epic. A decision id already recorded with the same text " +
				"returns the stored decision; other text under that id is refused.",
			InputSchema: json.RawMessage(epicAnswerSchema),
			Handler:     epicAnswerHandler(epics),
		},
	}
}

// decodeProto decodes a tool's arguments into msg with protojson, refusing unknown keys.
func decodeProto(raw json.RawMessage, msg proto.Message) error {
	if err := protojson.Unmarshal(raw, msg); err != nil {
		return fmt.Errorf("decode arguments: %w", err)
	}
	return nil
}

// protoResult returns msg as the protobuf JSON text of a single text content block. Unpopulated fields are emitted,
// so an empty repeated field reads as an empty array.
func protoResult(msg proto.Message) (mcp.ToolResult, error) {
	text, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(msg)
	if err != nil {
		return mcp.ToolResult{}, fmt.Errorf("encode result: %w", err)
	}
	return textResult(json.RawMessage(text))
}

func epicCreateHandler(epics enginev1connect.EpicsClient) mcp.ToolHandler {
	return func(ctx context.Context, call mcp.Call) (mcp.ToolResult, error) {
		var req v1.CreateEpicRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return mcp.ToolResult{}, err
		}
		created, err := epics.CreateEpic(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(created.Msg.GetEpic())
	}
}

func epicGetHandler(epics enginev1connect.EpicsClient) mcp.ToolHandler {
	return func(ctx context.Context, call mcp.Call) (mcp.ToolResult, error) {
		var req v1.GetEpicRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return mcp.ToolResult{}, err
		}
		got, err := epics.GetEpic(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(got.Msg.GetEpic())
	}
}

func epicListHandler(epics enginev1connect.EpicsClient) mcp.ToolHandler {
	return func(ctx context.Context, call mcp.Call) (mcp.ToolResult, error) {
		var req v1.ListEpicsRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return mcp.ToolResult{}, err
		}
		listed, err := epics.ListEpics(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(listed.Msg)
	}
}

func epicAnswerHandler(epics enginev1connect.EpicsClient) mcp.ToolHandler {
	return func(ctx context.Context, call mcp.Call) (mcp.ToolResult, error) {
		var req v1.AnswerEpicRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return mcp.ToolResult{}, err
		}
		answered, err := epics.AnswerEpic(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(answered.Msg.GetDecision())
	}
}
