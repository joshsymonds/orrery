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
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/stepmcp"
)

// epicKickForm names the form of `orrery epic kick`.
const epicKickForm = "kick ID TEXT"

// epicRetryForm names the form of `orrery epic retry`.
const epicRetryForm = "retry ID [NODE]"

// epicReleaseForm names the form of `orrery epic release`.
const epicReleaseForm = "release ID [--undo-none N]..."

// epicCloseForm names the form of `orrery epic close`.
const epicCloseForm = "close ID --as superseded|shipped|abandoned [--by EPIC]"

// epicAmendForm names the form of `orrery epic amend`.
const epicAmendForm = "amend ID R<n> --item JSON [--reason TEXT]"

// epicLineageForm names the form of `orrery epic lineage`.
const epicLineageForm = "lineage [--epic ID] [--escalated]"

// epicForms names every form of `orrery epic`, for the usage error of a missing or unknown verb.
const epicForms = "create FILE|-, get ID, list, follow, answer EPIC --id ID [--question QN] [--choice C] [TEXT], " +
	"pause ID, resume ID, stop ID, " + epicKickForm + ", " + epicRetryForm + ", " + epicReleaseForm + ", " +
	epicCloseForm + ", " + epicAmendForm + ", " + epicLineageForm

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

// epicKickArgs is the arguments of epic_kick: the epic's id and the owner's text.
type epicKickArgs struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// epicReleaseArgs is the arguments of epic_release. UndoNone is omitted when the form names no position.
type epicReleaseArgs struct {
	ID       string `json:"id"`
	UndoNone []int  `json:"undoNone,omitempty"`
}

// epicCloseArgs is the arguments of epic_close. By is omitted when the form names none.
type epicCloseArgs struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	By   string `json:"by,omitempty"`
}

// epicAmendArgs is the arguments of epic_amend: the epic's id, the Requirement, the evidence item as the JSON the form
// gave, and the reason, omitted when the form names none.
type epicAmendArgs struct {
	ID          string          `json:"id"`
	Requirement string          `json:"requirement"`
	Item        json.RawMessage `json:"item"`
	Reason      string          `json:"reason,omitempty"`
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
// with a newline. A refusal returns an error carrying the tool's text and prints nothing. For answer, the rest of the
// acknowledgement's warning line is also printed on stderr. A wrong form returns a usage error naming the form it
// wants.
// lineage reads the Epics client directly instead of calling an epic tool.
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
		return epicNoArgs(ctx, epics, verb, "epic_list", map[string]string{}, rest, stdout)
	case "follow":
		return epicNoArgs(ctx, epics, verb, "epic_follow", struct{}{}, rest, stdout)
	case "get", "pause", "resume", "stop":
		if len(rest) != 1 {
			return epicForm(verb + " ID")
		}
		return epicInvoke(ctx, epics, "epic_"+verb, epicIDArgs{ID: rest[0]}, stdout)
	case "kick":
		return epicKick(ctx, epics, rest, stdout)
	case "retry":
		return epicRetry(ctx, epics, rest, stdout)
	case "release":
		return epicRelease(ctx, epics, rest, stdout)
	case "close":
		return epicClose(ctx, epics, rest, stdout)
	case "amend":
		return epicAmend(ctx, epics, rest, stdout)
	case "lineage":
		return epicLineage(ctx, epics, rest, stdout)
	default:
		return epicForm(epicForms)
	}
}

// epicNoArgs implements a form of verb that takes no arguments: it calls tool with value, or returns the usage error of
// verb when rest is not empty.
func epicNoArgs(
	ctx context.Context, epics enginev1connect.EpicsClient, verb, tool string, value any, rest []string,
	stdout io.Writer,
) error {
	if len(rest) != 0 {
		return epicForm(verb)
	}
	return epicInvoke(ctx, epics, tool, value, stdout)
}

// epicKick implements `kick ID TEXT`. TEXT is the owner's words, which the planner reads at the epic's next plan run.
func epicKick(ctx context.Context, epics enginev1connect.EpicsClient, rest []string, stdout io.Writer) error {
	if len(rest) != 2 {
		return epicForm(epicKickForm)
	}
	return epicInvoke(ctx, epics, "epic_kick", epicKickArgs{ID: rest[0], Text: rest[1]}, stdout)
}

// epicRetry implements `retry ID [NODE]`. With NODE, that node reruns: its parked runs, or its latest integrate run when
// that run ended failed. Without NODE the epic's park ends. An empty NODE is a usage error, since sending it would
// silently end the park.
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

// epicRelease implements `release ID [--undo-none N]...`. Each N is a 1-based position of an undo-none release step to
// run again; the positions are sent in the order given, and the service decides what they mean.
func epicRelease(ctx context.Context, epics enginev1connect.EpicsClient, rest []string, stdout io.Writer) error {
	if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
		return epicForm(epicReleaseForm)
	}
	flags := flag.NewFlagSet("epic release", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var undoNone []int
	flags.Func("undo-none", "the 1-based position of an undo-none release step to run again", func(value string) error {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			return fmt.Errorf("--undo-none %q is not a positive integer", value)
		}
		undoNone = append(undoNone, n)
		return nil
	})
	if err := flags.Parse(rest[1:]); err != nil || flags.NArg() != 0 {
		return epicForm(epicReleaseForm)
	}
	return epicInvoke(ctx, epics, "epic_release", epicReleaseArgs{ID: rest[0], UndoNone: undoNone}, stdout)
}

// epicClose implements `close ID --as KIND [--by EPIC]`. KIND is superseded, shipped or abandoned, and superseded needs
// --by to name the epic that replaces ID. A form that breaks either rule is a usage error, and calls nothing. Any other
// refusal of the close is the service's to give.
func epicClose(ctx context.Context, epics enginev1connect.EpicsClient, rest []string, stdout io.Writer) error {
	if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
		return epicForm(epicCloseForm)
	}
	flags := flag.NewFlagSet("epic close", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	kind := flags.String("as", "", "the close kind: superseded, shipped or abandoned")
	by := flags.String("by", "", "the epic that replaces this one, which superseded requires")
	if err := flags.Parse(rest[1:]); err != nil || flags.NArg() != 0 {
		return epicForm(epicCloseForm)
	}
	if !epicCloseKind(*kind) || (*kind == "superseded" && *by == "") {
		return epicForm(epicCloseForm)
	}
	return epicInvoke(ctx, epics, "epic_close", epicCloseArgs{ID: rest[0], Kind: *kind, By: *by}, stdout)
}

// epicAmend implements `amend ID R<n> --item JSON [--reason TEXT]`. JSON is the whole evidence item the Requirement
// should hold, in the field names the epic_amend tool takes. A form without both ids, without --item, or whose --item is
// not a JSON object is a usage error, and calls nothing. Any other refusal of the amendment is the service's to give.
func epicAmend(ctx context.Context, epics enginev1connect.EpicsClient, rest []string, stdout io.Writer) error {
	if len(rest) < 2 || strings.HasPrefix(rest[0], "-") || strings.HasPrefix(rest[1], "-") {
		return epicForm(epicAmendForm)
	}
	flags := flag.NewFlagSet("epic amend", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	item := flags.String("item", "", "the evidence item the Requirement should hold, as JSON")
	reason := flags.String("reason", "", "why the item is amended")
	if err := flags.Parse(rest[2:]); err != nil || flags.NArg() != 0 {
		return epicForm(epicAmendForm)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(*item), &object); err != nil || object == nil {
		return epicForm(epicAmendForm)
	}
	return epicInvoke(ctx, epics, "epic_amend", epicAmendArgs{
		ID: rest[0], Requirement: rest[1], Item: json.RawMessage(*item), Reason: *reason,
	}, stdout)
}

// epicCloseKind reports whether kind is one `orrery epic close --as` takes: superseded, shipped or abandoned.
func epicCloseKind(kind string) bool {
	switch kind {
	case "superseded", "shipped", "abandoned":
		return true
	}
	return false
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
	fmt.Fprintln(stdout, text)
	for line := range strings.SplitSeq(text, "\n") {
		if warning, ok := strings.CutPrefix(line, "warning: "); ok {
			fmt.Fprintln(stderr, warning)
		}
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
