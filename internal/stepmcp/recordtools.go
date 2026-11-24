package stepmcp

import (
	"context"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// designApproachArgs is the arguments of design_approach, described for its input schema.
type designApproachArgs struct {
	Design      string   `json:"design"                jsonschema:"the design or contract the approach is weighed on"`
	Name        string   `json:"name"                  jsonschema:"the approach's name"`
	Shape       string   `json:"shape"                 jsonschema:"the shape the approach takes"`
	Cost        string   `json:"cost"                  jsonschema:"what the approach costs"`
	Wins        string   `json:"wins"                  jsonschema:"what the approach wins"`
	Loses       string   `json:"loses"                 jsonschema:"what the approach loses"`
	Reconsider  string   `json:"reconsider"            jsonschema:"what would make the planner reconsider the approach"`
	Research    []string `json:"research,omitempty"    jsonschema:"the ids of the research steps the approach rests on"`
	Recommended bool     `json:"recommended,omitempty" jsonschema:"true when the planner recommends the approach"`
	Reason      string   `json:"reason,omitempty"      jsonschema:"why the planner recommends the approach or does not"`
}

// designChooseArgs is the arguments of design_choose, described for its input schema.
type designChooseArgs struct {
	Design     string `json:"design"               jsonschema:"the design or contract the choice is made for"`
	Approach   int64  `json:"approach"             jsonschema:"the seq of the chosen approach, as design_approach gave it"`
	Words      string `json:"words"                jsonschema:"the owner's words the choice is made with"`
	Maker      string `json:"maker"                jsonschema:"owner_serve, owner_conversation or conversation"`
	Quote      string `json:"quote,omitempty"      jsonschema:"the owner's words, for owner_conversation"`
	Reason     string `json:"reason,omitempty"     jsonschema:"why the conversation decides, for conversation"`
	Delegation string `json:"delegation,omitempty" jsonschema:"the owner's words it decides on, for conversation"`
}

// designDelegateArgs is the arguments of design_delegate, described for its input schema.
type designDelegateArgs struct {
	Design  string   `json:"design"  jsonschema:"the design or contract the delegation covers"`
	Kinds   []string `json:"kinds"   jsonschema:"the kinds of decision the conversation may make for the owner"`
	Message string   `json:"message" jsonschema:"the owner's words the delegation is given with"`
}

// contractRowArgs is the arguments of contract_row, described for its input schema.
type contractRowArgs struct {
	Contract   string `json:"contract"             jsonschema:"the id of the contract whose row is written"`
	Design     string `json:"design,omitempty"     jsonschema:"the design the contract joins, empty for none"`
	Row        string `json:"row"                  jsonschema:"the F, R or P row the write concerns"`
	Value      string `json:"value,omitempty"      jsonschema:"the row's value the write sets, empty with remove"`
	Remove     bool   `json:"remove,omitempty"     jsonschema:"true to remove the row rather than set it"`
	Depends    string `json:"depends,omitempty"    jsonschema:"the row the written row depends on"`
	Words      string `json:"words"                jsonschema:"the owner's words the write is made with"`
	Maker      string `json:"maker"                jsonschema:"owner_serve, owner_conversation or conversation"`
	Quote      string `json:"quote,omitempty"      jsonschema:"the owner's words, for owner_conversation"`
	Reason     string `json:"reason,omitempty"     jsonschema:"why the conversation decides, for conversation"`
	Delegation string `json:"delegation,omitempty" jsonschema:"the owner's words it decides on, for conversation"`
}

// contractKeepArgs is the arguments of contract_keep, described for its input schema.
type contractKeepArgs struct {
	Contract   string `json:"contract"             jsonschema:"the id of the contract the addition belongs to"`
	Addition   string `json:"addition"             jsonschema:"the addition the contract keeps"`
	Words      string `json:"words"                jsonschema:"the owner's words the addition is kept with"`
	Maker      string `json:"maker"                jsonschema:"owner_serve, owner_conversation or conversation"`
	Quote      string `json:"quote,omitempty"      jsonschema:"the owner's words, for owner_conversation"`
	Reason     string `json:"reason,omitempty"     jsonschema:"why the conversation decides, for conversation"`
	Delegation string `json:"delegation,omitempty" jsonschema:"the owner's words it decides on, for conversation"`
}

// contractStrikeArgs is the arguments of contract_strike, described for its input schema as contractKeepArgs is.
type contractStrikeArgs struct {
	Contract   string `json:"contract"             jsonschema:"the id of the contract the addition belongs to"`
	Addition   string `json:"addition"             jsonschema:"the addition the contract keeps, which is struck"`
	Words      string `json:"words"                jsonschema:"the owner's words the addition is struck with"`
	Maker      string `json:"maker"                jsonschema:"owner_serve, owner_conversation or conversation"`
	Quote      string `json:"quote,omitempty"      jsonschema:"the owner's words, for owner_conversation"`
	Reason     string `json:"reason,omitempty"     jsonschema:"why the conversation decides, for conversation"`
	Delegation string `json:"delegation,omitempty" jsonschema:"the owner's words it decides on, for conversation"`
}

// contractProposeWorkingArgs is the arguments of contract_propose_working, described for its input schema.
type contractProposeWorkingArgs struct {
	ID    string `json:"id"    jsonschema:"the id the new contract takes"`
	Words string `json:"words" jsonschema:"the words the writer gives with the proposal"`
}

// recordTools returns the tools that store a design's stage records: its approaches, its choice and its delegations,
// the rows a contract's stage writes, and the proposal of a design's working copy. The approach, choice, delegation and
// row tools call stage, and contract_propose_working calls contracts. A nil client answers each call with a tool error.
func recordTools(stage enginev1connect.StageClient, contracts enginev1connect.ContractsClient) ([]Tool, error) {
	approach, err := argumentSchema[designApproachArgs](nil)
	if err != nil {
		return nil, err
	}
	choose, err := argumentSchema[designChooseArgs](nil)
	if err != nil {
		return nil, err
	}
	delegate, err := argumentSchema[designDelegateArgs](nil)
	if err != nil {
		return nil, err
	}
	row, err := argumentSchema[contractRowArgs](nil)
	if err != nil {
		return nil, err
	}
	keep, err := argumentSchema[contractKeepArgs](nil)
	if err != nil {
		return nil, err
	}
	strike, err := argumentSchema[contractStrikeArgs](nil)
	if err != nil {
		return nil, err
	}
	proposeWorking, err := argumentSchema[contractProposeWorkingArgs](nil)
	if err != nil {
		return nil, err
	}
	return []Tool{
		{
			Name: "design_approach",
			Description: "Store an approach design's stage weighs: its shape, its cost, what it wins and loses, what would " +
				"make the planner reconsider it, the research steps it rests on, and whether the planner recommends it. " +
				"Returns the stored approach with its seq.",
			InputSchema: approach,
			Handler:     designApproachHandler(stage),
		},
		{
			Name: "design_choose",
			Description: "Store the approach the owner chose for design, by the seq design_approach gave it, with the " +
				"maker it came from: owner_serve for the owner through serve, owner_conversation for the owner in the " +
				"conversation with quote, or conversation for the owner with reason and delegation. Returns the choice.",
			InputSchema: choose,
			Handler:     designChooseHandler(stage),
		},
		{
			Name: "design_delegate",
			Description: "Store the owner's delegation to the conversation of the decisions of kinds on design, with the " +
				"owner's message. Returns the delegation.",
			InputSchema: delegate,
			Handler:     designDelegateHandler(stage),
		},
		{
			Name: "contract_row",
			Description: "Store a write to a row of contract: set its F, R or P row to value, or remove it with remove, " +
				"with depends, the words and the maker it came from: owner_serve for the owner through serve, " +
				"owner_conversation for the owner in the conversation with quote, or conversation for the owner with " +
				"reason and delegation. Returns the stored write.",
			InputSchema: row,
			Handler:     contractRowHandler(stage),
		},
		{
			Name: "contract_keep",
			Description: "Store an addition contract keeps, with the words and the maker it came from: owner_serve for " +
				"the owner through serve, owner_conversation for the owner in the conversation with quote, or " +
				"conversation for the owner with reason and delegation. Returns the stored write.",
			InputSchema: keep,
			Handler:     contractKeepHandler(stage),
		},
		{
			Name: "contract_strike",
			Description: "Strike an addition contract keeps, with the words and the maker it came from: owner_serve for " +
				"the owner through serve, owner_conversation for the owner in the conversation with quote, or " +
				"conversation for the owner with reason and delegation. Returns the stored write and the rows it removed.",
			InputSchema: strike,
			Handler:     contractStrikeHandler(stage),
		},
		{
			Name: "contract_propose_working",
			Description: "Propose the working copy of a contract a design holds as a new contract under id, with the " +
				"words. Returns the stored contract with its warnings.",
			InputSchema: proposeWorking,
			Handler:     contractProposeWorkingHandler(contracts),
		},
	}, nil
}

func designApproachHandler(stage enginev1connect.StageClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args designApproachArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if stage == nil {
			return stageUnavailable(), nil
		}
		approached, err := stage.Approach(ctx, connect.NewRequest(&v1.ApproachRequest{
			Design:      args.Design,
			Name:        args.Name,
			Shape:       args.Shape,
			Cost:        args.Cost,
			Wins:        args.Wins,
			Loses:       args.Loses,
			Reconsider:  args.Reconsider,
			Research:    args.Research,
			Recommended: args.Recommended,
			Reason:      args.Reason,
		}))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(approached.Msg)
	}
}

func designChooseHandler(stage enginev1connect.StageClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args designChooseArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if stage == nil {
			return stageUnavailable(), nil
		}
		chosen, err := stage.Choose(ctx, connect.NewRequest(&v1.ChooseRequest{
			Design: args.Design, Approach: args.Approach, Words: args.Words,
			Maker: &v1.StageMaker{
				Kind: args.Maker, Quote: args.Quote, Reason: args.Reason, Delegation: args.Delegation,
			},
		}))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(chosen.Msg)
	}
}

func designDelegateHandler(stage enginev1connect.StageClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args designDelegateArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if stage == nil {
			return stageUnavailable(), nil
		}
		delegated, err := stage.Delegate(ctx, connect.NewRequest(&v1.DelegateRequest{
			Design: args.Design, Kinds: args.Kinds, Message: args.Message,
		}))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(delegated.Msg)
	}
}

func contractRowHandler(stage enginev1connect.StageClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args contractRowArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if stage == nil {
			return stageUnavailable(), nil
		}
		written, err := stage.WriteRow(ctx, connect.NewRequest(&v1.WriteRowRequest{
			Contract: args.Contract, Design: args.Design, Row: args.Row, Value: args.Value, Remove: args.Remove,
			Depends: args.Depends, Words: args.Words,
			Maker: &v1.StageMaker{
				Kind: args.Maker, Quote: args.Quote, Reason: args.Reason, Delegation: args.Delegation,
			},
		}))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(written.Msg)
	}
}

func contractKeepHandler(stage enginev1connect.StageClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args contractKeepArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if stage == nil {
			return stageUnavailable(), nil
		}
		kept, err := stage.Keep(ctx, connect.NewRequest(&v1.KeepRequest{
			Contract: args.Contract, Addition: args.Addition, Words: args.Words,
			Maker: &v1.StageMaker{
				Kind: args.Maker, Quote: args.Quote, Reason: args.Reason, Delegation: args.Delegation,
			},
		}))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(kept.Msg)
	}
}

func contractStrikeHandler(stage enginev1connect.StageClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args contractStrikeArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if stage == nil {
			return stageUnavailable(), nil
		}
		struck, err := stage.Strike(ctx, connect.NewRequest(&v1.StrikeRequest{
			Contract: args.Contract, Addition: args.Addition, Words: args.Words,
			Maker: &v1.StageMaker{
				Kind: args.Maker, Quote: args.Quote, Reason: args.Reason, Delegation: args.Delegation,
			},
		}))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(struck.Msg)
	}
}

func contractProposeWorkingHandler(contracts enginev1connect.ContractsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args contractProposeWorkingArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if contracts == nil {
			return stageUnavailable(), nil
		}
		proposed, err := contracts.ProposeWorking(ctx, connect.NewRequest(&v1.ProposeWorkingRequest{
			Id: args.ID, Words: args.Words,
		}))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(proposed.Msg)
	}
}
