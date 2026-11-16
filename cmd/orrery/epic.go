package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/stepmcp"
)

// epicRetryForm names the form of `orrery epic retry`.
const epicRetryForm = "retry ID [NODE]"

// epicForms names every form of `orrery epic`, for the usage error of a missing or unknown verb.
const epicForms = "create FILE|-, get ID, list, follow, answer EPIC --id ID [--question QN] [--choice C] [TEXT], " +
	"pause ID, resume ID, stop ID, " + epicRetryForm

// epicAnswerForm names the form of `orrery epic answer`.
const epicAnswerForm = "answer EPIC --id ID [--question QN] [--choice C] [TEXT]"

// epicListVerb is the verb of `orrery epic list`. The tmux list command spells it the same way, so it is named here.
const epicListVerb = "list"

// epicIDArgs is the arguments of an epic tool that takes one id.
type epicIDArgs struct {
	ID string `json:"id"`
}

// epicRetryArgs is the arguments of epic_retry. Node is omitted when the form names none, which ends the epic's park.
type epicRetryArgs struct {
	ID   string `json:"id"`
	Node string `json:"node,omitempty"`
}

// epicAnswerArgs is the arguments of epic_answer. Question and Choice are omitted for a note.
type epicAnswerArgs struct {
	EpicID   string `json:"epicId"`
	ID       string `json:"id"`
	Question string `json:"question,omitempty"`
	Choice   string `json:"choice,omitempty"`
	Text     string `json:"text"`
}

// epicCommand implements `orrery epic`. Each form turns its arguments into the arguments of the epic tool epic_<verb>
// and calls that tool through stepmcp.CallEpicTool, as the MCP server does. It prints the tool's result text on stdout
// with a newline. A refusal returns an error carrying the tool's text and prints nothing. For answer, a non-empty
// warning in the result is also printed on stderr. A wrong form returns a usage error naming the form it wants.
func epicCommand(
	ctx context.Context, epics enginev1connect.EpicsClient, args []string, stdin io.Reader, stdout, stderr io.Writer,
) error {
	if len(args) == 0 {
		return epicForm(epicForms)
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "create":
		return epicCreate(ctx, epics, rest, stdin, stdout)
	case "answer":
		return epicAnswer(ctx, epics, rest, stdout, stderr)
	case epicListVerb:
		if len(rest) != 0 {
			return epicForm(verb)
		}
		return epicInvoke(ctx, epics, "epic_list", map[string]string{}, stdout)
	case "follow":
		if len(rest) != 0 {
			return epicForm(verb)
		}
		return epicInvoke(ctx, epics, "epic_follow", struct{}{}, stdout)
	case "get", "pause", "resume", "stop":
		if len(rest) != 1 {
			return epicForm(verb + " ID")
		}
		return epicInvoke(ctx, epics, "epic_"+verb, epicIDArgs{ID: rest[0]}, stdout)
	case "retry":
		return epicRetry(ctx, epics, rest, stdout)
	default:
		return epicForm(epicForms)
	}
}

// epicRetry implements `retry ID [NODE]`. With NODE, the parked run of that node reruns; without it, the epic's park
// ends. An empty NODE is a usage error, since sending it would silently end the park.
func epicRetry(ctx context.Context, epics enginev1connect.EpicsClient, rest []string, stdout io.Writer) error {
	switch {
	case len(rest) == 1:
		return epicInvoke(ctx, epics, "epic_retry", epicRetryArgs{ID: rest[0]}, stdout)
	case len(rest) == 2 && rest[1] != "":
		return epicInvoke(ctx, epics, "epic_retry", epicRetryArgs{ID: rest[0], Node: rest[1]}, stdout)
	default:
		return epicForm(epicRetryForm)
	}
}

// epicForm returns the usage error for `orrery epic` given the wrong arguments; form names the form it takes.
func epicForm(form string) error {
	return usageError{msg: "epic: want " + form}
}

// epicCreate implements `create FILE|-`. The JSON of the file, or of stdin for -, is the arguments of epic_create.
func epicCreate(
	ctx context.Context, epics enginev1connect.EpicsClient, rest []string, stdin io.Reader, stdout io.Writer,
) error {
	if len(rest) != 1 {
		return epicForm("create FILE|-")
	}
	arguments, err := epicReadInput(rest[0], stdin)
	if err != nil {
		return err
	}
	return epicPrint(ctx, epics, "epic_create", arguments, stdout)
}

// epicReadInput returns the contents of the file at path, or of stdin when path is "-".
func epicReadInput(path string, stdin io.Reader) (json.RawMessage, error) {
	if path == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("epic create: read stdin: %w", err)
		}
		return data, nil
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("epic create: %w", err)
	}
	return data, nil
}

// epicAnswer implements `answer EPIC --id ID [--question QN] [--choice C] [TEXT]`. TEXT may be left out only when
// --choice is given; the service refuses the answers that need text it does not get.
func epicAnswer(
	ctx context.Context, epics enginev1connect.EpicsClient, rest []string, stdout, stderr io.Writer,
) error {
	if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
		return epicForm(epicAnswerForm)
	}
	flags := flag.NewFlagSet("epic answer", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	id := flags.String("id", "", "the decision's id")
	question := flags.String("question", "", "the Q<n> the decision answers; omit it for a note")
	choice := flags.String("choice", "", "the choice the decision gives the question: a declared choice's id, or other")
	if err := flags.Parse(rest[1:]); err != nil {
		return epicForm(epicAnswerForm)
	}
	given := make(map[string]bool)
	flags.Visit(func(f *flag.Flag) { given[f.Name] = true })
	if !given["id"] || flags.NArg() > 1 || (flags.NArg() == 0 && !given["choice"]) {
		return epicForm(epicAnswerForm)
	}
	arguments, err := json.Marshal(epicAnswerArgs{
		EpicID:   rest[0],
		ID:       *id,
		Question: *question,
		Choice:   *choice,
		Text:     flags.Arg(0),
	})
	if err != nil {
		return fmt.Errorf("epic: encode arguments: %w", err)
	}
	text, err := epicCall(ctx, epics, "epic_answer", arguments)
	if err != nil {
		return err
	}
	var answered v1.AnswerEpicResponse
	if err = protojson.Unmarshal([]byte(text), &answered); err != nil {
		return fmt.Errorf("epic answer: read result: %w", err)
	}
	fmt.Fprintln(stdout, text)
	if answered.GetWarning() != "" {
		fmt.Fprintln(stderr, answered.GetWarning())
	}
	return nil
}

// epicInvoke marshals value with encoding/json, calls the epic tool tool with it, and prints the result.
func epicInvoke(
	ctx context.Context, epics enginev1connect.EpicsClient, tool string, value any, stdout io.Writer,
) error {
	arguments, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("epic: encode arguments: %w", err)
	}
	return epicPrint(ctx, epics, tool, arguments, stdout)
}

// epicPrint calls the epic tool tool with arguments and prints its result text on stdout with a newline.
func epicPrint(
	ctx context.Context, epics enginev1connect.EpicsClient, tool string, arguments json.RawMessage, stdout io.Writer,
) error {
	text, err := epicCall(ctx, epics, tool, arguments)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, text)
	return nil
}

// epicCall runs the epic tool tool with arguments through stepmcp.CallEpicTool and returns its result text. A refusal
// returns an error carrying that text.
func epicCall(
	ctx context.Context, epics enginev1connect.EpicsClient, tool string, arguments json.RawMessage,
) (string, error) {
	result, err := stepmcp.CallEpicTool(ctx, epics, tool, arguments)
	if err != nil {
		return "", fmt.Errorf("epic: %w", err)
	}
	text := epicText(result)
	if result.IsError {
		return "", errors.New(text)
	}
	return text, nil
}

// epicText returns the text of each content block of result, joined by newlines.
func epicText(result stepmcp.ToolResult) string {
	texts := make([]string, 0, len(result.Content))
	for _, block := range result.Content {
		texts = append(texts, block.Text)
	}
	return strings.Join(texts, "\n")
}
