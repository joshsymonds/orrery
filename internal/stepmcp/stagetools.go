package stepmcp

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// stageUnavailableText is the text of the tool error every stage tool answers with when there is no Stage client.
const stageUnavailableText = "orrery stage tools have no orreryd connection"

// designResearchArgs is the arguments of design_research, described for its input schema.
type designResearchArgs struct {
	Design     string `json:"design"             jsonschema:"the id of the design or contract the step researches for"`
	Repository string `json:"repository"         jsonschema:"the absolute path of the git repository the scout reads"`
	Revision   string `json:"revision,omitempty" jsonschema:"the committed revision the scout reads at, empty for HEAD"`
	Question   string `json:"question"           jsonschema:"the question the scout answers"`
}

// designReproduceArgs is the arguments of design_reproduce, described for its input schema.
type designReproduceArgs struct {
	Design     string `json:"design"     jsonschema:"the id of the design or contract the output is stored on"`
	Repository string `json:"repository" jsonschema:"the absolute path of the git repository the command runs in"`
	Revision   string `json:"revision"   jsonschema:"the committed revision the command runs at"`
	Command    string `json:"command"    jsonschema:"the command to run"`
}

// designStageArgs is the arguments of design_stage, described for its input schema.
type designStageArgs struct {
	Design string `json:"design" jsonschema:"the id of the design or contract whose stage to read"`
}

// designAskArgs is the arguments of design_ask, described for its input schema.
type designAskArgs struct {
	Design         string   `json:"design"             jsonschema:"the design or contract the question is on"`
	Contract       string   `json:"contract,omitempty" jsonschema:"the contract the question concerns, or empty"`
	Text           string   `json:"text"               jsonschema:"the question's text"`
	Options        []string `json:"options"            jsonschema:"the options the owner may choose between"`
	Recommendation string   `json:"recommendation"     jsonschema:"the option the planner recommends"`
}

// designAnswerArgs is the arguments of design_answer, described for its input schema.
type designAnswerArgs struct {
	Design     string `json:"design"               jsonschema:"the design or contract the question is on"`
	Question   int64  `json:"question"             jsonschema:"the question id design_ask gave"`
	Maker      string `json:"maker"                jsonschema:"owner_serve, owner_conversation or conversation"`
	Quote      string `json:"quote,omitempty"      jsonschema:"the owner's words, for owner_conversation"`
	Reason     string `json:"reason,omitempty"     jsonschema:"why the conversation decides, for conversation"`
	Delegation string `json:"delegation,omitempty" jsonschema:"the owner's words it decides on, for conversation"`
	Answer     string `json:"answer,omitempty"     jsonschema:"the answer's words; empty with accept"`
	Accept     bool   `json:"accept,omitempty"     jsonschema:"true to answer with the recommendation"`
}

// designDisposeArgs is the arguments of design_dispose, described for its input schema. Reason is the item's reason
// and MakerReason the maker's, since the two are kept apart.
type designDisposeArgs struct {
	Design      string `json:"design"                jsonschema:"the design or contract the item belongs to"`
	Item        string `json:"item"                  jsonschema:"the id of the ledger item, as design_stage lists it"`
	Disposition string `json:"disposition"           jsonschema:"adopted, rejected, deferred or open before closure; revised_after_closure or accepted_as_risk after it"`
	Reason      string `json:"reason,omitempty"      jsonschema:"why the item is rejected, or why an open item closes after closure"`
	Boundary    string `json:"boundary,omitempty"    jsonschema:"the boundary the item is deferred to, for deferred"`
	Maker       string `json:"maker"                 jsonschema:"owner_serve, owner_conversation or conversation"`
	Quote       string `json:"quote,omitempty"       jsonschema:"the owner's words, for owner_conversation"`
	MakerReason string `json:"makerReason,omitempty" jsonschema:"why the conversation decides, for conversation"`
	Delegation  string `json:"delegation,omitempty"  jsonschema:"the owner's words it decides on, for conversation"`
}

// stageTools returns the stage tools, each calling stage. A nil stage answers each call with a tool error.
func stageTools(stage enginev1connect.StageClient) ([]Tool, error) {
	research, err := argumentSchema[designResearchArgs](nil)
	if err != nil {
		return nil, err
	}
	reproduce, err := argumentSchema[designReproduceArgs](nil)
	if err != nil {
		return nil, err
	}
	read, err := argumentSchema[designStageArgs](nil)
	if err != nil {
		return nil, err
	}
	ask, err := argumentSchema[designAskArgs](nil)
	if err != nil {
		return nil, err
	}
	answer, err := argumentSchema[designAnswerArgs](nil)
	if err != nil {
		return nil, err
	}
	dispose, err := argumentSchema[designDisposeArgs](nil)
	if err != nil {
		return nil, err
	}
	return []Tool{
		{
			Name: "design_research",
			Description: "Start a read-only scout step that answers question about repository at revision, for design, " +
				"and store the step on the design. Returns the step; its report is in design_stage once the step returns.",
			InputSchema: research,
			Handler:     designResearchHandler(stage),
		},
		{
			Name: "design_reproduce",
			Description: "Run command in repository at revision through a read-only test runner step, and store the step " +
				"on design. Returns the step; its output is in design_stage once the step returns.",
			InputSchema: reproduce,
			Handler:     designReproduceHandler(stage),
		},
		{
			Name: "design_stage",
			Description: "Return the contract stage of a design: the research and reproduction steps it started, each " +
				"with its status and its report once it returns.",
			InputSchema: read,
			Handler:     designStageHandler(stage),
		},
		{
			Name: "design_ask",
			Description: "Store a question on design, about contract or about none, with its options and the option the " +
				"planner recommends. Returns the question, unanswered; design_answer answers it.",
			InputSchema: ask,
			Handler:     designAskHandler(stage),
		},
		{
			Name: "design_answer",
			Description: "Store an answer to a question of design, with the maker it came from: owner_serve for the owner " +
				"through serve, owner_conversation for the owner in the conversation with quote, or conversation for " +
				"the owner with reason and delegation. accept answers with the recommendation. Returns the question with " +
				"its answer.",
			InputSchema: answer,
			Handler:     designAnswerHandler(stage),
		},
		{
			Name: "design_dispose",
			Description: "Store a disposition of a ledger item of design, with the maker it came from: owner_serve for " +
				"the owner through serve, owner_conversation for the owner in the conversation with quote, or " +
				"conversation for the owner with makerReason and delegation. Before closure an item is adopted, " +
				"rejected with reason, deferred with boundary, or open; after closure an open item closes as " +
				"revised_after_closure or accepted_as_risk, with reason. Returns the item with its disposition.",
			InputSchema: dispose,
			Handler:     designDisposeHandler(stage),
		},
	}, nil
}

// stageUnavailable is the tool error a stage tool answers with when there is no Stage client.
func stageUnavailable() ToolResult {
	return toolFailure(errors.New(stageUnavailableText))
}

func designResearchHandler(stage enginev1connect.StageClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args designResearchArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if stage == nil {
			return stageUnavailable(), nil
		}
		researched, err := stage.Research(ctx, connect.NewRequest(&v1.ResearchRequest{
			Design: args.Design, Repository: args.Repository, Revision: args.Revision, Question: args.Question,
		}))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(researched.Msg)
	}
}

func designReproduceHandler(stage enginev1connect.StageClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args designReproduceArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if stage == nil {
			return stageUnavailable(), nil
		}
		reproduced, err := stage.Reproduce(ctx, connect.NewRequest(&v1.ReproduceRequest{
			Design: args.Design, Repository: args.Repository, Revision: args.Revision, Command: args.Command,
		}))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(reproduced.Msg)
	}
}

func designStageHandler(stage enginev1connect.StageClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args designStageArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if stage == nil {
			return stageUnavailable(), nil
		}
		got, err := stage.GetStage(ctx, connect.NewRequest(&v1.GetStageRequest{Design: args.Design}))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(got.Msg)
	}
}

func designAskHandler(stage enginev1connect.StageClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args designAskArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if stage == nil {
			return stageUnavailable(), nil
		}
		asked, err := stage.Ask(ctx, connect.NewRequest(&v1.AskRequest{
			Design: args.Design, Contract: args.Contract, Text: args.Text, Options: args.Options,
			Recommendation: args.Recommendation,
		}))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(asked.Msg)
	}
}

func designAnswerHandler(stage enginev1connect.StageClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args designAnswerArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if stage == nil {
			return stageUnavailable(), nil
		}
		answered, err := stage.Answer(ctx, connect.NewRequest(&v1.StageAnswerRequest{
			Design: args.Design, Question: args.Question, Answer: args.Answer, Accept: args.Accept,
			Maker: &v1.StageMaker{
				Kind: args.Maker, Quote: args.Quote, Reason: args.Reason, Delegation: args.Delegation,
			},
		}))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(answered.Msg)
	}
}

func designDisposeHandler(stage enginev1connect.StageClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args designDisposeArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if stage == nil {
			return stageUnavailable(), nil
		}
		disposed, err := stage.Dispose(ctx, connect.NewRequest(&v1.DisposeRequest{
			Design: args.Design, Item: args.Item, Disposition: args.Disposition, Reason: args.Reason,
			Boundary: args.Boundary, Maker: &v1.StageMaker{
				Kind: args.Maker, Quote: args.Quote, Reason: args.MakerReason, Delegation: args.Delegation,
			},
		}))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(disposed.Msg)
	}
}
