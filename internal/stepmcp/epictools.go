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
	Contract  json.RawMessage    `json:"contract"            jsonschema:"the accepted contract in protobuf JSON field names: id, repository, title, approved, intent, failures, care, additions, requirements, premises, exclusions, approach, alternatives, releaseSteps, releaseThen, checks, base, gates, doneGates, reviewers. A requirement carries item, its evidence: capture, limit, artifacts, supplied, conclusion, judge, kind and tests. Each entry of reviewers carries name, enabled, stage, must, mustNot, prompt, agent, model, tools, effort, limit and verify."`
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

// epicReleaseArgs is the arguments of epic_release, described for its input schema. UndoNone names the 1-based positions
// of the undo-none release steps to run again; omitted, none runs again.
type epicReleaseArgs struct {
	ID       string `json:"id"`
	UndoNone []int  `json:"undoNone,omitempty" jsonschema:"the 1-based positions of the undo-none release steps to run again; omit to name none"`
}

// epicInputs holds the input schema of each epic tool, by the arguments it takes.
type epicInputs struct {
	create, id, list, answer, follow, retry, release json.RawMessage
}

// epicToolInputs returns the input schemas of the epic tools.
func epicToolInputs() (epicInputs, error) {
	var in epicInputs
	var err error
	if in.create, err = argumentSchema[epicCreateArgs](nil); err != nil {
		return epicInputs{}, err
	}
	if in.id, err = argumentSchema[idArgs](nil); err != nil {
		return epicInputs{}, err
	}
	if in.list, err = argumentSchema[epicListArgs](nil); err != nil {
		return epicInputs{}, err
	}
	if in.answer, err = argumentSchema[epicAnswerArgs](nil); err != nil {
		return epicInputs{}, err
	}
	if in.follow, err = argumentSchema[epicFollowArgs](nil); err != nil {
		return epicInputs{}, err
	}
	if in.retry, err = argumentSchema[epicRetryArgs](nil); err != nil {
		return epicInputs{}, err
	}
	if in.release, err = argumentSchema[epicReleaseArgs](nil); err != nil {
		return epicInputs{}, err
	}
	return in, nil
}

// epicTools returns the epic tools, each calling epics.
func epicTools(epics enginev1connect.EpicsClient) ([]Tool, error) {
	in, err := epicToolInputs()
	if err != nil {
		return nil, err
	}
	return []Tool{
		{
			Name: "epic_create",
			Description: "Create an epic from an accepted contract. decisions seeds the epic's recorded " +
				"decisions. workflow names the workflow the epic pins, epic by default. Returns the stored epic. " +
				"A refusal names the field that failed.",
			InputSchema: in.create,
			Handler:     epicCreateHandler(epics),
		},
		{
			Name: "epic_get",
			Description: "Return a stored epic by id: its contract, the workflow it pins with its name, text and " +
				"sha256, phase, timestamps, decisions, whether it is paused and why it is parked, and its planner " +
				"turns, tasks and questions.",
			InputSchema: in.id,
			Handler:     epicGetHandler(epics),
		},
		{
			Name:        "epic_list",
			Description: "List the stored epics as summaries: id, repository, title, phase, created and changed.",
			InputSchema: in.list,
			Handler:     epicListHandler(epics),
		},
		{
			Name: "epic_answer",
			Description: "Record a decision on an epic. question names the Q<n> the decision answers, and choice " +
				"names the choice it gives: a declared choice's id, or other, which needs text. A decision with " +
				"no question is a note, which takes no choice and returns a warning naming the questions still " +
				"open. A decision id already recorded with the same text returns the stored decision; other " +
				"text under that id is refused. Returns the decision and the warning.",
			InputSchema: in.answer,
			Handler:     epicAnswerHandler(epics),
		},
		{
			Name: "epic_follow",
			Description: "Return how often the owner took the advice of each question label, across all epics: " +
				"per label, the answered questions that had a recommendation and how many were answered with it.",
			InputSchema: in.follow,
			Handler:     epicFollowHandler(epics),
		},
		{
			Name: "epic_pause",
			Description: "Pause an epic by id, so the planner starts no turn for it and no limit reset resumes it. " +
				"Returns the stored epic. An epic the owner paused, or paused with no recorded source, is refused; " +
				"an epic a usage limit paused is accepted and stays paused past the reset.",
			InputSchema: in.id,
			Handler:     epicPauseHandler(epics),
		},
		{
			Name: "epic_resume",
			Description: "Clear the pause of an epic by id, if it is paused, and resume its limited task steps. " +
				"Returns the stored epic, the ids of the steps resumed, and the ids of the steps a full step cap " +
				"left limited. An epic that is not paused and holds no limited task step is refused. A park " +
				"stays; epic_retry ends a park.",
			InputSchema: in.id,
			Handler:     epicResumeHandler(epics),
		},
		{
			Name: "epic_stop",
			Description: "Pause an epic by id and stop the step of its running turn. Returns the stored epic. " +
				"An epic with no running turn is refused, and nothing is stopped, unless it holds its repository's " +
				"release slot: then the slot is freed, its open release step is stopped if one runs, and the epic " +
				"is not paused. epic_release resumes a stopped release.",
			InputSchema: in.id,
			Handler:     epicStopHandler(epics),
		},
		{
			Name: "epic_retry",
			Description: "End the park of an epic by id, so the planner starts its turns again. With node, rerun " +
				"the parked workflow node of that id under its next attempt instead, which also ends the park. " +
				"Returns the stored epic. Without node, an epic that is not parked is refused; with node, a node " +
				"that is not parked is refused.",
			InputSchema: in.retry,
			Handler:     epicRetryHandler(epics),
		},
		{
			Name: "epic_release",
			Description: "Release an epic by id. A stopped release resumes at its first unfinished step, and undoNone names the 1-based positions of undo-none release steps to run again. " +
				"An epic that ended with gaps is released on the owner's word, logged as the owner's decision, so undoNone must be empty for it. " +
				"Returns the stored epic. Refused unless the release is stopped or the epic ended with gaps, and each position is undo-none.",
			InputSchema: in.release,
			Handler:     epicReleaseHandler(epics),
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

func epicReleaseHandler(epics enginev1connect.EpicsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var req v1.ReleaseEpicRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return ToolResult{}, err
		}
		released, err := epics.ReleaseEpic(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(released.Msg.GetEpic())
	}
}
