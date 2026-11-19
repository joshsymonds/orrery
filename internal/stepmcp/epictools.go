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

// epicGetArgs is the arguments of epic_get, described for its input schema. Without part, the call returns the epic's
// one-screen summary. part names the part to read; task and question name the one item a single-item part reads. offset
// and limit page the part, counting items for a list part and runes for a text part or an item. They are pointers, so
// that a key given with a zero value is told from a key left out.
type epicGetArgs struct {
	ID       string `json:"id"                 jsonschema:"the id of the stored epic"`
	Part     string `json:"part,omitempty"     jsonschema:"the part of the epic to read: tasks, turns, decisions, questions or history, one line per item; contract, workflow or report, its text; or task or question, one item. Omit it for the one-screen summary"`
	Task     string `json:"task,omitempty"     jsonschema:"the id of the task to read, such as T3: required with part task, and refused with any other part"`
	Question string `json:"question,omitempty" jsonschema:"the id of the question to read, such as Q2: required with part question, and refused with any other part"`
	Offset   *int   `json:"offset,omitempty"   jsonschema:"the first item of a list part, or the first rune of a text part or an item, to read: 0 when omitted; needs a part"`
	Limit    *int   `json:"limit,omitempty"    jsonschema:"the most items of a list part, or runes of a text part or an item, to read: 50 items or 8000 runes when omitted or 0; needs a part"`
}

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

// epicKickArgs is the arguments of epic_kick, described for its input schema. Text is the owner's words, which the
// planner reads at the epic's next plan run.
type epicKickArgs struct {
	ID   string `json:"id"`
	Text string `json:"text" jsonschema:"the owner's words, which the planner reads at the epic's next plan run"`
}

// epicReleaseArgs is the arguments of epic_release, described for its input schema. UndoNone names the 1-based positions
// of the undo-none release steps to run again; omitted, none runs again.
type epicReleaseArgs struct {
	ID       string `json:"id"`
	UndoNone []int  `json:"undoNone,omitempty" jsonschema:"the 1-based positions of the undo-none release steps to run again; omit to name none"`
}

// epicCloseArgs is the arguments of epic_close, described for its input schema. By names the epic that replaces the
// closed one, which a superseded close requires.
type epicCloseArgs struct {
	ID   string `json:"id"`
	Kind string `json:"kind"         jsonschema:"the close kind: superseded, shipped or abandoned"`
	By   string `json:"by,omitempty" jsonschema:"the epic that replaces this one: required for superseded"`
}

// epicAmendArgs is the arguments of epic_amend, described for its input schema as epicCreateArgs is. Item is the evidence
// item the Requirement should hold, in protobuf JSON field names.
type epicAmendArgs struct {
	ID          string          `json:"id"`
	Requirement string          `json:"requirement"      jsonschema:"the id of the Requirement whose evidence item to amend, such as R2"`
	Item        json.RawMessage `json:"item"             jsonschema:"the evidence item the Requirement should hold in place of its own, in protobuf JSON field names: capture, limit, artifacts, supplied, conclusion, judge, kind and tests"`
	Reason      string          `json:"reason,omitempty" jsonschema:"why the item is amended, recorded with the amendment"`
}

// epicInputs holds the input schema of each epic tool, by the arguments it takes.
type epicInputs struct {
	create, id, get, list, answer, follow, retry, kick, release, close, amend json.RawMessage
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
	if in.get, err = argumentSchema[epicGetArgs](nil); err != nil {
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
	if in.kick, err = argumentSchema[epicKickArgs](nil); err != nil {
		return epicInputs{}, err
	}
	if in.release, err = argumentSchema[epicReleaseArgs](nil); err != nil {
		return epicInputs{}, err
	}
	if in.close, err = argumentSchema[epicCloseArgs](nil); err != nil {
		return epicInputs{}, err
	}
	if in.amend, err = argumentSchema[epicAmendArgs](nil); err != nil {
		return epicInputs{}, err
	}
	return in, nil
}

// epicTools returns the epic tools, each calling epics: the record tools, then the control tools.
func epicTools(epics enginev1connect.EpicsClient) ([]Tool, error) {
	in, err := epicToolInputs()
	if err != nil {
		return nil, err
	}
	return append(epicRecordTools(epics, in), epicControlTools(epics, in)...), nil
}

// epicRecordTools returns the epic tools that create, read and answer epics, each calling epics with its input schema
// from in.
func epicRecordTools(epics enginev1connect.EpicsClient, in epicInputs) []Tool {
	return []Tool{
		{
			Name: "epic_create",
			Description: "Create an epic from an accepted contract. decisions seeds the epic's recorded " +
				"decisions. workflow names the workflow the epic pins, epic by default. Returns a short " +
				"acknowledgement, not the stored epic: the epic id, its phase and one line naming what changed. " +
				"Lint findings come back as warnings: a warning line for each, naming its field and reason, and the " +
				"epic is still stored. A refusal names the field that failed.",
			InputSchema: in.create,
			Handler:     epicCreateHandler(epics),
		},
		{
			Name: "epic_get",
			Description: "Return a stored epic by id. Without part, it returns the one-screen text summary: its id, " +
				"title and phase, whether it is paused or why it is parked, its open questions, its task counts by " +
				"status, and each part with its item count and byte size. With part, it returns that part, one of " +
				"tasks, turns, decisions, questions, history, contract, workflow, report, task and question. " +
				"tasks, turns, decisions, questions and history give one line per item, 50 from offset by default, " +
				"and an offset <n> continues line when items remain past them. task, with task such as T3, and " +
				"question, with question such as Q2, return that item whole as protobuf JSON. contract, workflow, " +
				"report, task and question return limit runes from offset, 8000 by default, and name the offset " +
				"that continues when text remains past them.",
			InputSchema: in.get,
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
				"text under that id is refused. Returns a short acknowledgement, not the stored epic: the epic id, " +
				"its phase and one line naming what changed. A note's warning is the acknowledgement's warning line.",
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
	}
}

// epicControlTools returns the epic tools that pause, resume, stop, retry, kick, release, close and amend epics, each
// calling epics with its input schema from in.
func epicControlTools(epics enginev1connect.EpicsClient, in epicInputs) []Tool {
	return []Tool{
		{
			Name: "epic_pause",
			Description: "Pause an epic by id, so the planner starts no turn for it and no limit reset resumes it. " +
				"Returns a short acknowledgement, not the stored epic: the epic id, its phase and one line naming " +
				"what changed. An epic the owner paused, or paused with no recorded source, is refused; " +
				"an epic a usage limit paused is accepted and stays paused past the reset.",
			InputSchema: in.id,
			Handler:     epicPauseHandler(epics),
		},
		{
			Name: "epic_resume",
			Description: "Clear the pause of an epic by id, if it is paused, and resume its limited task steps. " +
				"Returns a short acknowledgement, not the stored epic: the epic id, its phase and one line naming " +
				"what changed. An epic that is not paused and holds no limited task step is refused. A park " +
				"stays; epic_retry ends a park.",
			InputSchema: in.id,
			Handler:     epicResumeHandler(epics),
		},
		{
			Name: "epic_stop",
			Description: "Pause an epic by id and stop the step of its running turn. Returns a short " +
				"acknowledgement, not the stored epic: the epic id, its phase and one line naming what changed. " +
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
				"Returns a short acknowledgement, not the stored epic: the epic id, its phase and one line naming " +
				"what changed. Without node, an epic that is not parked is refused; with node, a node " +
				"that is not parked is refused.",
			InputSchema: in.retry,
			Handler:     epicRetryHandler(epics),
		},
		{
			Name: "epic_kick",
			Description: "Kick an epic by id: record text as the owner's decision and wake the epic's planner with it " +
				"at the epic's next plan run. Refused for a paused epic, for a parked epic (epic_retry ends the park " +
				"first), and for an epic in an ended phase, which takes no kick. Returns a short acknowledgement, " +
				"not the stored epic: the epic id, its phase and one line naming what changed.",
			InputSchema: in.kick,
			Handler:     epicKickHandler(epics),
		},
		{
			Name: "epic_release",
			Description: "Release an epic by id. A stopped release resumes at its first unfinished step, and undoNone names the 1-based positions of undo-none release steps to run again. " +
				"An epic that ended with gaps is released on the owner's word, logged as the owner's decision, so undoNone must be empty for it. " +
				"Returns a short acknowledgement, not the stored epic: the epic id, its phase and one line naming " +
				"what changed. Refused unless the release is stopped or the epic ended with gaps, and each position is undo-none.",
			InputSchema: in.release,
			Handler:     epicReleaseHandler(epics),
		},
		{
			Name: "epic_close",
			Description: "Close an epic by id as superseded, shipped by hand or abandoned, which ends it. kind names the " +
				"close; superseded requires by, the epic that replaces it. The close answers the epic's open questions, " +
				"ends its running turn, node runs and steps, and frees its release slot. Returns a short " +
				"acknowledgement, not the stored epic: the epic id, its phase and one line naming what changed. " +
				"Refused for a superseded close without by.",
			InputSchema: in.close,
			Handler:     epicCloseHandler(epics),
		},
		{
			Name: "epic_amend",
			Description: "Amend the evidence item of a Requirement of an epic by id: requirement names it and item " +
				"is the whole item it should hold, which must pass the shape check a created epic's items pass. The " +
				"amendment starts checking: orreryd runs the new item's red at the epic's base and applies the item " +
				"only when that red is judged not to hold there; a red that holds refuses it and the old item stays. " +
				"The owner may change the conclusion. Returns a short acknowledgement, not the stored epic: the epic " +
				"id, its phase and one line naming what changed.",
			InputSchema: in.amend,
			Handler:     epicAmendHandler(epics),
		},
	}
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
		epic := created.Msg.GetEpic()
		return epicAck(epic, req.GetContract().GetId(), createChange(epic), created.Msg.GetWarnings()...), nil
	}
}

// epicGetHandler answers epic_get. The arguments are checked before the epic is read, which the call reads once, with its
// history: the summary counts the history, and the history part lists it. Without a part, the answer is the summary.
func epicGetHandler(epics enginev1connect.EpicsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args epicGetArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if err := checkEpicGetArgs(args); err != nil {
			return ToolResult{}, err
		}
		got, err := epics.GetEpic(ctx, connect.NewRequest(&v1.GetEpicRequest{Id: args.ID, History: true}))
		if err != nil {
			return toolFailure(err), nil
		}
		text, err := epicGetText(args, got.Msg.GetEpic())
		if err != nil {
			return ToolResult{}, err
		}
		return ToolResult{Content: []Content{{Type: textContentType, Text: text}}}, nil
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
		status := answerPhase(epics.GetEpic(ctx, connect.NewRequest(&v1.GetEpicRequest{Id: req.GetEpicId()})))
		change := answerChange(&req, answered.Msg.GetDecision())
		return ackResult(ackText(ackHead(req.GetEpicId(), status), change, answered.Msg.GetWarning())), nil
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
		return epicAck(paused.Msg.GetEpic(), req.GetId(), "paused"), nil
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
		change := resumeChange(resumed.Msg.GetResumed(), resumed.Msg.GetStillLimited())
		return epicAck(resumed.Msg.GetEpic(), req.GetId(), change), nil
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
		epic := stopped.Msg.GetEpic()
		return epicAck(epic, req.GetId(), stopChange(epic)), nil
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
		return epicAck(retried.Msg.GetEpic(), req.GetId(), retryChange(req.GetNode())), nil
	}
}

func epicKickHandler(epics enginev1connect.EpicsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var req v1.KickEpicRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return ToolResult{}, err
		}
		kicked, err := epics.KickEpic(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		change := "kicked; the planner reads the words at its next plan run"
		return epicAck(kicked.Msg.GetEpic(), req.GetId(), change), nil
	}
}

func epicCloseHandler(epics enginev1connect.EpicsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var req v1.CloseEpicRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return ToolResult{}, err
		}
		closed, err := epics.CloseEpic(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		change := closeChange(req.GetKind(), req.GetBy())
		return epicAck(closed.Msg.GetEpic(), req.GetId(), change), nil
	}
}

func epicAmendHandler(epics enginev1connect.EpicsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var req v1.AmendEvidenceRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return ToolResult{}, err
		}
		amended, err := epics.AmendEvidence(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		change := amendChange(amended.Msg.GetAmendment())
		return epicAck(amended.Msg.GetEpic(), req.GetId(), change), nil
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
		change := releaseChange(req.GetUndoNone())
		return epicAck(released.Msg.GetEpic(), req.GetId(), change), nil
	}
}
