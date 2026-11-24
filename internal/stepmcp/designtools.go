package stepmcp

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// designUnavailableText is the text of the tool error every design tool answers with when there is no Designs client.
const designUnavailableText = "orrery design tools have no orreryd connection"

// designCreateArgs is the arguments of design_create, described for its input schema.
type designCreateArgs struct {
	ID    string `json:"id"    jsonschema:"the id the new design takes: no contract or design may hold it"`
	Title string `json:"title" jsonschema:"the design's title, on one line"`
}

// designGetArgs is the arguments of design_get, described for its input schema.
type designGetArgs struct {
	ID string `json:"id" jsonschema:"the id of the design to read, or the id of a contract to read the design of one it is in"`
}

// designListArgs is the arguments of design_list, which takes none.
type designListArgs struct{}

// designOrderArgs is the arguments of design_order, described for its input schema.
type designOrderArgs struct {
	Design   string `json:"design"   jsonschema:"the id of the design both contracts belong to"`
	Contract string `json:"contract" jsonschema:"the id of the contract that starts after the other"`
	After    string `json:"after"    jsonschema:"the id of the contract that the contract starts after"`
}

// designTools returns the design tools, each calling designs. A nil designs answers each call with a tool error.
func designTools(designs enginev1connect.DesignsClient) ([]Tool, error) {
	create, err := argumentSchema[designCreateArgs](nil)
	if err != nil {
		return nil, err
	}
	get, err := argumentSchema[designGetArgs](nil)
	if err != nil {
		return nil, err
	}
	list, err := argumentSchema[designListArgs](nil)
	if err != nil {
		return nil, err
	}
	order, err := argumentSchema[designOrderArgs](nil)
	if err != nil {
		return nil, err
	}
	return []Tool{
		{
			Name: "design_create",
			Description: "Create a design under id with title. A design holds the contracts one conversation works, " +
				"in the order they start, and starts with none: contract_propose adds one with its design argument. " +
				"Returns the design with its contracts and state.",
			InputSchema: create,
			Handler:     designCreateHandler(designs),
		},
		{
			Name: "design_get",
			Description: "Return a design by id, with its contracts, the contracts each starts after and its state. " +
				"The id of a contract returns the design of one that holds it, a contract proposed with no design.",
			InputSchema: get,
			Handler:     designGetHandler(designs),
		},
		{
			Name: "design_list",
			Description: "List every design, with its contracts, sorted by id. A contract proposed with no design " +
				"is listed as a design of one.",
			InputSchema: list,
			Handler:     designListHandler(designs),
		},
		{
			Name: "design_order",
			Description: "Record that a contract of a design starts after another contract of the same design, given " +
				"as contract and after. The edge takes effect when the contract's epic is created, and no tool removes " +
				"an edge. Returns the design with the edge.",
			InputSchema: order,
			Handler:     designOrderHandler(designs),
		},
	}, nil
}

// designUnavailable is the tool error a design tool answers with when there is no Designs client.
func designUnavailable() ToolResult {
	return toolFailure(errors.New(designUnavailableText))
}

func designCreateHandler(designs enginev1connect.DesignsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args designCreateArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if designs == nil {
			return designUnavailable(), nil
		}
		created, err := designs.CreateDesign(ctx, connect.NewRequest(&v1.CreateDesignRequest{
			Id: args.ID, Title: args.Title,
		}))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(created.Msg)
	}
}

func designGetHandler(designs enginev1connect.DesignsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args designGetArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if designs == nil {
			return designUnavailable(), nil
		}
		got, err := designs.GetDesign(ctx, connect.NewRequest(&v1.GetDesignRequest{Id: args.ID}))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(got.Msg)
	}
}

func designListHandler(designs enginev1connect.DesignsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args designListArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if designs == nil {
			return designUnavailable(), nil
		}
		listed, err := designs.ListDesigns(ctx, connect.NewRequest(&v1.ListDesignsRequest{}))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(listed.Msg)
	}
}

func designOrderHandler(designs enginev1connect.DesignsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args designOrderArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if designs == nil {
			return designUnavailable(), nil
		}
		ordered, err := designs.OrderDesign(ctx, connect.NewRequest(&v1.OrderDesignRequest{
			Design: args.Design, Contract: args.Contract, After: []string{args.After},
		}))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(ordered.Msg)
	}
}
