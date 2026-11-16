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
)

// epicCreateArgs is the arguments of epic_create, described for its input schema. The handler decodes the arguments
// with protojson, which refuses an unknown key, so the struct is never decoded into.
type epicCreateArgs struct {
	Contract  json.RawMessage    `json:"contract"            jsonschema:"the accepted contract in protobuf JSON field names: id, repository, title, approved, intent, failures, care, additions, requirements, premises, exclusions, approach, alternatives, releaseSteps, releaseThen, checks, base, gates, doneGates, reviewGates. A requirement carries item, its evidence: capture, limit, artifacts, supplied, conclusion and judge."`
	Decisions []epicDecisionArgs `json:"decisions,omitempty"`
	Workflow  string             `json:"workflow,omitempty"  jsonschema:"the workflow the epic pins, read at the contract's base: a repository file .orrery/workflows/<name>.yaml, else the built-in of that name; omit it to pin epic"`
}

// epicDecisionArgs is one decision of epic_create's decisions.
type epicDecisionArgs struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// epicListArgs is the arguments of epic_list, which takes none.
type epicListArgs struct{}

// epicAnswerArgs is the arguments of epic_answer, described for its input schema as epicCreateArgs is.
type epicAnswerArgs struct {
	EpicID   string `json:"epicId"`
	ID       string `json:"id"`
	Text     string `json:"text,omitempty"     jsonschema:"the owner's words: required with choice other, optional with a declared choice"`
	Question string `json:"question,omitempty" jsonschema:"the Q<n> the answer names; omit for a note"`
	Choice   string `json:"choice,omitempty"   jsonschema:"the choice the answer gives the question: a declared choice's id, or other; omit for a note"`
}

// epicFollowArgs is the arguments of epic_follow, which takes none.
type epicFollowArgs struct{}

// epicRetryArgs is the arguments of epic_retry, described for its input schema. Node names the workflow node whose
// parked run reruns; omitted, the epic's park ends instead.
type epicRetryArgs struct {
	ID   string `json:"id"`
	Node string `json:"node,omitempty" jsonschema:"the workflow node id whose parked run reruns under its next attempt; omit to end the epic's park"`
}

// epicTools returns the epic tools, each calling epics.
func epicTools(epics enginev1connect.EpicsClient) ([]Tool, error) {
	createInput, err := argumentSchema[epicCreateArgs](nil)
	if err != nil {
		return nil, err
	}
	idInput, err := argumentSchema[idArgs](nil)
	if err != nil {
		return nil, err
	}
	listInput, err := argumentSchema[epicListArgs](nil)
	if err != nil {
		return nil, err
	}
	answerInput, err := argumentSchema[epicAnswerArgs](nil)
	if err != nil {
		return nil, err
	}
	followInput, err := argumentSchema[epicFollowArgs](nil)
	if err != nil {
		return nil, err
	}
	retryInput, err := argumentSchema[epicRetryArgs](nil)
	if err != nil {
		return nil, err
	}
	return []Tool{
		{
			Name: "epic_create",
			Description: "Create an epic from an accepted contract. decisions seeds the epic's recorded " +
				"decisions. workflow names the workflow the epic pins, epic by default. Returns the stored epic. " +
				"A refusal names the field that failed.",
			InputSchema: createInput,
			Handler:     epicCreateHandler(epics),
		},
		{
			Name: "epic_get",
			Description: "Return a stored epic by id: its contract, the workflow it pins with its name, text and " +
				"sha256, phase, timestamps, decisions, whether it is paused and why it is parked, and its planner " +
				"turns, tasks and questions.",
			InputSchema: idInput,
			Handler:     epicGetHandler(epics),
		},
		{
			Name:        "epic_list",
			Description: "List the stored epics as summaries: id, repository, title, phase, created and changed.",
			InputSchema: listInput,
			Handler:     epicListHandler(epics),
		},
		{
			Name: "epic_answer",
			Description: "Record a decision on an epic. question names the Q<n> the decision answers, and choice " +
				"names the choice it gives: a declared choice's id, or other, which needs text. A decision with " +
				"no question is a note, which takes no choice and returns a warning naming the questions still " +
				"open. A decision id already recorded with the same text returns the stored decision; other " +
				"text under that id is refused. Returns the decision and the warning.",
			InputSchema: answerInput,
			Handler:     epicAnswerHandler(epics),
		},
		{
			Name: "epic_follow",
			Description: "Return how often the owner took the advice of each question label, across all epics: " +
				"per label, the answered questions that had a recommendation and how many were answered with it.",
			InputSchema: followInput,
			Handler:     epicFollowHandler(epics),
		},
		{
			Name: "epic_pause",
			Description: "Pause an epic by id, so the planner starts no turn for it and no limit reset resumes it. " +
				"Returns the stored epic. An epic the owner paused, or paused with no recorded source, is refused; " +
				"an epic a usage limit paused is accepted and stays paused past the reset.",
			InputSchema: idInput,
			Handler:     epicPauseHandler(epics),
		},
		{
			Name: "epic_resume",
			Description: "Clear the pause of an epic by id, if it is paused, and resume its limited task steps. " +
				"Returns the stored epic, the ids of the steps resumed, and the ids of the steps a full step cap " +
				"left limited. An epic that is not paused and holds no limited task step is refused. A park " +
				"stays; epic_retry ends a park.",
			InputSchema: idInput,
			Handler:     epicResumeHandler(epics),
		},
		{
			Name: "epic_stop",
			Description: "Pause an epic by id and stop the step of its running turn. Returns the stored epic. " +
				"An epic with no running turn is refused, and nothing is stopped.",
			InputSchema: idInput,
			Handler:     epicStopHandler(epics),
		},
		{
			Name: "epic_retry",
			Description: "End the park of an epic by id, so the planner starts its turns again. With node, rerun " +
				"the parked workflow node of that id under its next attempt instead, which also ends the park. " +
				"Returns the stored epic. Without node, an epic that is not parked is refused; with node, a node " +
				"that is not parked is refused.",
			InputSchema: retryInput,
			Handler:     epicRetryHandler(epics),
		},
	}, nil
}

// CallEpicTool runs the epic tool named name with args, as the MCP server runs it, and returns its result. A name that
// is not an epic tool is an error.
func CallEpicTool(
	ctx context.Context, epics enginev1connect.EpicsClient, name string, args json.RawMessage,
) (ToolResult, error) {
	tools, err := epicTools(epics)
	if err != nil {
		return ToolResult{}, err
	}
	for _, tool := range tools {
		if tool.Name == name {
			result, callErr := tool.Handler(ctx, Call{Arguments: args})
			if callErr != nil {
				return ToolResult{}, fmt.Errorf("run %s: %w", name, callErr)
			}
			return result, nil
		}
	}
	return ToolResult{}, fmt.Errorf("unknown epic tool %q", name)
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
func protoResult(msg proto.Message) (ToolResult, error) {
	text, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(msg)
	if err != nil {
		return ToolResult{}, fmt.Errorf("encode result: %w", err)
	}
	return textResult(json.RawMessage(text))
}

func epicCreateHandler(epics enginev1connect.EpicsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var req v1.CreateEpicRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return ToolResult{}, err
		}
		created, err := epics.CreateEpic(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(created.Msg.GetEpic())
	}
}

func epicGetHandler(epics enginev1connect.EpicsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var req v1.GetEpicRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return ToolResult{}, err
		}
		got, err := epics.GetEpic(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(got.Msg.GetEpic())
	}
}

func epicListHandler(epics enginev1connect.EpicsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var req v1.ListEpicsRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return ToolResult{}, err
		}
		listed, err := epics.ListEpics(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(listed.Msg)
	}
}

func epicAnswerHandler(epics enginev1connect.EpicsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var req v1.AnswerEpicRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return ToolResult{}, err
		}
		answered, err := epics.AnswerEpic(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(answered.Msg)
	}
}

func epicFollowHandler(epics enginev1connect.EpicsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var req v1.FollowEpicsRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return ToolResult{}, err
		}
		followed, err := epics.FollowEpics(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(followed.Msg)
	}
}

func epicPauseHandler(epics enginev1connect.EpicsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var req v1.PauseEpicRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return ToolResult{}, err
		}
		paused, err := epics.PauseEpic(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(paused.Msg.GetEpic())
	}
}

func epicResumeHandler(epics enginev1connect.EpicsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var req v1.ResumeEpicRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return ToolResult{}, err
		}
		resumed, err := epics.ResumeEpic(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(resumed.Msg)
	}
}

func epicStopHandler(epics enginev1connect.EpicsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var req v1.StopEpicRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return ToolResult{}, err
		}
		stopped, err := epics.StopEpic(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(stopped.Msg.GetEpic())
	}
}

func epicRetryHandler(epics enginev1connect.EpicsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var req v1.RetryEpicRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return ToolResult{}, err
		}
		retried, err := epics.RetryEpic(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(retried.Msg.GetEpic())
	}
}
