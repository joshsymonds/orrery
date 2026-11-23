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
