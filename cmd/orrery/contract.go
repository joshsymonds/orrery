package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/stepmcp"
)

// contractForms names every form of `orrery contract`, for the usage error of a missing or unknown verb.
const contractForms = "list, get ID, propose ID FILE WORDS, revise ID FILE WORDS, accept ID [WORDS], " +
	"set-aside ID WORDS, reopen ID [WORDS]"

// contractIDArgs is the arguments of a contract tool that takes one id.
type contractIDArgs struct {
	ID string `json:"id"`
}

// contractFileArgs is the arguments of contract_propose and contract_revise: the id, the contract file and the words.
type contractFileArgs struct {
	ID    string `json:"id"`
	Path  string `json:"path"`
	Words string `json:"words"`
}

// contractWordsArgs is the arguments of a contract tool that takes an id and words. Words is omitted when the form
// names none.
type contractWordsArgs struct {
	ID    string `json:"id"`
	Words string `json:"words,omitempty"`
}

// contractCommand implements `orrery contract`. Each form turns its arguments into the arguments of the contract tool
// of the same verb and calls it through stepmcp.CallContractTool, as the MCP server does. It prints the tool's result
// text on stdout with a newline. A refusal returns an error carrying the tool's text and prints nothing. A wrong form
// returns a usage error naming the form it wants, and calls nothing.
func contractCommand(
	ctx context.Context, contracts enginev1connect.ContractsClient, args []string, stdout io.Writer,
) error {
	if len(args) == 0 {
		return contractForm(contractForms)
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case epicListVerb:
		if len(rest) != 0 {
			return contractForm("list")
		}
		return contractInvoke(ctx, contracts, "contract_list", struct{}{}, stdout)
	case "get":
		if len(rest) != 1 {
			return contractForm("get ID")
		}
		return contractInvoke(ctx, contracts, "contract_get", contractIDArgs{ID: rest[0]}, stdout)
	case "propose", "revise":
		if len(rest) != 3 {
			return contractForm(verb + " ID FILE WORDS")
		}
		return contractInvoke(ctx, contracts, "contract_"+verb, contractFileArgs{
			ID: rest[0], Path: rest[1], Words: rest[2],
		}, stdout)
	case "accept", "reopen":
		if len(rest) != 1 && len(rest) != 2 {
			return contractForm(verb + " ID [WORDS]")
		}
		return contractInvoke(ctx, contracts, "contract_"+verb, contractWordsArgs{
			ID: rest[0], Words: optionalArgument(rest, 1),
		}, stdout)
	case "set-aside":
		if len(rest) != 2 {
			return contractForm("set-aside ID WORDS")
		}
		return contractInvoke(ctx, contracts, "contract_set_aside", contractWordsArgs{
			ID: rest[0], Words: rest[1],
		}, stdout)
	default:
		return contractForm(contractForms)
	}
}

// optionalArgument returns args[i], or the empty string when args has no element i.
func optionalArgument(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

// contractForm returns the usage error for `orrery contract` given the wrong arguments; form names the form it takes.
func contractForm(form string) error {
	return usageError{msg: "contract: want " + form}
}

// contractInvoke marshals value with encoding/json, calls the contract tool tool with it, and prints the result.
func contractInvoke(
	ctx context.Context, contracts enginev1connect.ContractsClient, tool string, value any, stdout io.Writer,
) error {
	arguments, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("contract: encode arguments: %w", err)
	}
	text, err := contractCall(ctx, contracts, tool, arguments)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, text)
	return nil
}

// contractCall runs the contract tool tool with arguments through stepmcp.CallContractTool and returns its result text.
// A refusal returns an error carrying that text.
func contractCall(
	ctx context.Context, contracts enginev1connect.ContractsClient, tool string, arguments json.RawMessage,
) (string, error) {
	result, err := stepmcp.CallContractTool(ctx, contracts, tool, arguments)
	if err != nil {
		return "", fmt.Errorf("contract: %w", err)
	}
	text := epicText(result)
	if result.IsError {
		return "", errors.New(text)
	}
	return text, nil
}
