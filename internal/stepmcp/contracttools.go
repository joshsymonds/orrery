package stepmcp

import (
	"context"
	"encoding/json"
	"fmt"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// contractProposeArgs is the arguments of contract_propose, described for its input schema. Path names the contract file
// that holds the contract body and its decisions.
type contractProposeArgs struct {
	ID     string `json:"id"               jsonschema:"the id the new contract takes: the id of the contract in the file"`
	Path   string `json:"path"             jsonschema:"the contract file: one JSON object holding the contract in protobuf JSON field names, plus an optional decisions array of decisions in protobuf JSON field names"`
	Words  string `json:"words"            jsonschema:"the words the writer gives with the version"`
	Design string `json:"design,omitempty" jsonschema:"the id of the design the contract joins, as design_create made it; empty joins no design, so the contract reads as a design of one"`
}

// contractDraftArgs is the arguments of contract_draft, described for its input schema as contractProposeArgs is. Path
// names the contract file that holds the contract body and its decisions, read as contract_propose reads it.
type contractDraftArgs struct {
	ID     string `json:"id"               jsonschema:"the id the draft takes, the id the contract takes when it is proposed"`
	Path   string `json:"path"             jsonschema:"the contract file: one JSON object holding the contract in protobuf JSON field names, plus an optional decisions array of decisions in protobuf JSON field names"`
	Words  string `json:"words"            jsonschema:"the words the writer gives with the draft"`
	Design string `json:"design,omitempty" jsonschema:"the id of the design the draft joins, as design_create made it; empty joins no design"`
}

// contractReviseArgs is the arguments of contract_revise, described for its input schema as contractProposeArgs is.
type contractReviseArgs struct {
	ID    string `json:"id"    jsonschema:"the id of the stored contract to revise"`
	Path  string `json:"path"  jsonschema:"the contract file the new version is read from, in the shape contract_propose reads"`
	Words string `json:"words" jsonschema:"the words the writer gives with the version"`
}

// contractAcceptArgs is the arguments of contract_accept, described for its input schema.
type contractAcceptArgs struct {
	ID    string `json:"id"              jsonschema:"the id of the stored contract to accept"`
	Words string `json:"words,omitempty" jsonschema:"the words the owner accepts the contract with"`
}

// contractSetAsideArgs is the arguments of contract_set_aside, described for its input schema.
type contractSetAsideArgs struct {
	ID    string `json:"id"    jsonschema:"the id of the stored contract to set aside"`
	Words string `json:"words" jsonschema:"the words the contract is set aside with"`
}

// contractReopenArgs is the arguments of contract_reopen, described for its input schema.
type contractReopenArgs struct {
	ID    string `json:"id"              jsonschema:"the id of the stored contract to reopen"`
	Words string `json:"words,omitempty" jsonschema:"the words the contract is reopened with"`
}

// contractGetArgs is the arguments of contract_get, described for its input schema.
type contractGetArgs struct {
	ID string `json:"id" jsonschema:"the id of the stored contract to read"`
}

// contractListArgs is the arguments of contract_list, which takes none.
type contractListArgs struct{}

// contractTools returns the contract tools, each calling contracts: the writes, then the reads.
func contractTools(contracts enginev1connect.ContractsClient) ([]Tool, error) {
	propose, err := argumentSchema[contractProposeArgs](nil)
	if err != nil {
		return nil, err
	}
	draft, err := argumentSchema[contractDraftArgs](nil)
	if err != nil {
		return nil, err
	}
	revise, err := argumentSchema[contractReviseArgs](nil)
	if err != nil {
		return nil, err
	}
	accept, err := argumentSchema[contractAcceptArgs](nil)
	if err != nil {
		return nil, err
	}
	setAside, err := argumentSchema[contractSetAsideArgs](nil)
	if err != nil {
		return nil, err
	}
	reopen, err := argumentSchema[contractReopenArgs](nil)
	if err != nil {
		return nil, err
	}
	get, err := argumentSchema[contractGetArgs](nil)
	if err != nil {
		return nil, err
	}
	list, err := argumentSchema[contractListArgs](nil)
	if err != nil {
		return nil, err
	}
	return []Tool{
		{
			Name: "contract_propose",
			Description: "Propose a contract: store the contract in the file at path as version 1 in state proposed, " +
				"under id, with the words and the file's decisions. The file is one JSON object holding the contract " +
				"in protobuf JSON field names, plus an optional decisions array. Returns the stored contract with its " +
				"versions and states. A file that does not read is refused, naming the path.",
			InputSchema: propose,
			Handler:     contractProposeHandler(contracts),
		},
		{
			Name: "contract_draft",
			Description: "Draft a contract before it is proposed: store the contract in the file at path as a draft " +
				"under id, with the words and the file's decisions, in the design named by design when it is given. " +
				"The file has the shape contract_propose reads. A draft has no versions and is not a contract until " +
				"contract_propose proposes it under the same id. Returns the stored draft. A file that does not read " +
				"is refused, naming the path.",
			InputSchema: draft,
			Handler:     contractDraftHandler(contracts),
		},
		{
			Name: "contract_revise",
			Description: "Revise a stored contract by id: append the contract in the file at path as a new version, " +
				"with the words and the file's decisions. The file has the shape contract_propose reads. Returns the " +
				"stored contract with its versions and states.",
			InputSchema: revise,
			Handler:     contractReviseHandler(contracts),
		},
		{
			Name: "contract_accept",
			Description: "Accept a stored contract by id, with the owner's words. Returns the stored contract with its " +
				"versions and states.",
			InputSchema: accept,
			Handler:     contractAcceptHandler(contracts),
		},
		{
			Name: "contract_set_aside",
			Description: "Set a stored contract aside by id, with words. Returns the stored contract with its " +
				"versions and states.",
			InputSchema: setAside,
			Handler:     contractSetAsideHandler(contracts),
		},
		{
			Name: "contract_reopen",
			Description: "Reopen a stored contract by id, moving it back to state proposed, with the words. Returns the " +
				"stored contract with its versions and states.",
			InputSchema: reopen,
			Handler:     contractReopenHandler(contracts),
		},
		{
			Name:        "contract_get",
			Description: "Return a stored contract by id, with its versions and state changes, as protobuf JSON.",
			InputSchema: get,
			Handler:     contractGetHandler(contracts),
		},
		{
			Name: "contract_list",
			Description: "List the stored contracts as summaries, without their versions or state changes, as protobuf " +
				"JSON.",
			InputSchema: list,
			Handler:     contractListHandler(contracts),
		},
	}, nil
}

// CallContractTool runs the contract tool named name with args, as the MCP server runs it, and returns its result. A name
// that is not a contract tool is an error.
func CallContractTool(
	ctx context.Context, contracts enginev1connect.ContractsClient, name string, args json.RawMessage,
) (ToolResult, error) {
	tools, err := contractTools(contracts)
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
	return ToolResult{}, fmt.Errorf("unknown contract tool %q", name)
}

func contractProposeHandler(contracts enginev1connect.ContractsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args contractProposeArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		contract, decisions, err := readContractFile(args.Path)
		if err != nil {
			return toolFailure(err), nil
		}
		proposed, err := contracts.ProposeContract(ctx, connect.NewRequest(&v1.ProposeContractRequest{
			Id: args.ID, Contract: contract, Decisions: decisions, Words: args.Words, Design: args.Design,
		}))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(proposed.Msg)
	}
}

func contractDraftHandler(contracts enginev1connect.ContractsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args contractDraftArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		contract, decisions, err := readContractFile(args.Path)
		if err != nil {
			return toolFailure(err), nil
		}
		drafted, err := contracts.DraftContract(ctx, connect.NewRequest(&v1.DraftContractRequest{
			Id: args.ID, Contract: contract, Decisions: decisions, Words: args.Words, Design: args.Design,
		}))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(drafted.Msg)
	}
}

func contractReviseHandler(contracts enginev1connect.ContractsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args contractReviseArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		contract, decisions, err := readContractFile(args.Path)
		if err != nil {
			return toolFailure(err), nil
		}
		revised, err := contracts.ReviseContract(ctx, connect.NewRequest(&v1.ReviseContractRequest{
			Id: args.ID, Contract: contract, Decisions: decisions, Words: args.Words,
		}))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(revised.Msg)
	}
}

func contractAcceptHandler(contracts enginev1connect.ContractsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var req v1.AcceptContractRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return ToolResult{}, err
		}
		accepted, err := contracts.AcceptContract(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(accepted.Msg)
	}
}

func contractSetAsideHandler(contracts enginev1connect.ContractsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var req v1.SetAsideContractRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return ToolResult{}, err
		}
		setAside, err := contracts.SetAsideContract(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(setAside.Msg)
	}
}

func contractReopenHandler(contracts enginev1connect.ContractsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var req v1.ReopenContractRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return ToolResult{}, err
		}
		reopened, err := contracts.ReopenContract(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(reopened.Msg)
	}
}

func contractGetHandler(contracts enginev1connect.ContractsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var req v1.GetContractRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return ToolResult{}, err
		}
		got, err := contracts.GetContract(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(got.Msg)
	}
}

func contractListHandler(contracts enginev1connect.ContractsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var req v1.ListContractsRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return ToolResult{}, err
		}
		listed, err := contracts.ListContracts(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(listed.Msg)
	}
}
