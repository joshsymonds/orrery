package stepmcp

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// The parts epic_get reads. The list parts give one line per item; contract, workflow and report give their text; task,
// question, decision and turn give one item each.
const (
	partTasks     = "tasks"
	partTurns     = "turns"
	partDecisions = "decisions"
	partQuestions = "questions"
	partHistory   = "history"
	partContract  = "contract"
	partWorkflow  = "workflow"
	partReport    = "report"
	partTask      = "task"
	partQuestion  = "question"
	partDecision  = "decision"
	partTurn      = "turn"
)

// epicPartNames names the parts epic_get accepts, in the order its refusal of an unknown part lists them.
const epicPartNames = "tasks, turns, decisions, questions, history, contract, workflow, report, task, question, decision, turn"

// The limits of epic_get. A list part's limit counts items, and a text part's or an item's limit counts runes; an absent
// or zero limit is the default of the part's kind. itemTextCap is the rune cap of an item line's text.
const (
	listLimitDefault = 50
	textLimitDefault = 8000
	itemTextCap      = 120
)

// checkEpicGetArgs refuses the arguments of epic_get that no read can answer: a part that is not one of epicPartNames, a
// task, question, decision or turn given with a part other than its own, a task, question, decision or turn part without
// its item, a turn that is not a positive integer, offset or limit with no part, and a negative offset or limit. It runs
// before the epic is read.
func checkEpicGetArgs(args epicGetArgs) error {
	if args.Part != "" && !isEpicPart(args.Part) {
		return fmt.Errorf("part: unknown part %q, want one of %s", args.Part, epicPartNames)
	}
	if err := checkItemArgs(args); err != nil {
		return err
	}
	if args.Part == "" {
		if args.Offset != nil {
			return errors.New("offset needs a part")
		}
		if args.Limit != nil {
			return errors.New("limit needs a part")
		}
		return nil
	}
	if args.Offset != nil && *args.Offset < 0 {
		return errors.New("offset must not be negative")
	}
	if args.Limit != nil && *args.Limit < 0 {
		return errors.New("limit must not be negative")
	}
	return nil
}

// checkItemArgs refuses a task, question, decision or turn given with a part other than its own, the task, question,
// decision or turn part that lacks its item, and a turn that is not a positive integer.
func checkItemArgs(args epicGetArgs) error {
	if err := checkItemKeys(args); err != nil {
		return err
	}
	switch {
	case args.Part == partTask && args.Task == "":
		return errors.New("part task needs task, such as T3")
	case args.Part == partQuestion && args.Question == "":
		return errors.New("part question needs question, such as Q2")
	case args.Part == partDecision && args.Decision == "":
		return errors.New("part decision needs decision, such as DL3")
	case args.Part == partTurn && args.Turn == "":
		return errors.New("part turn needs turn, such as 3")
	}
	if args.Part == partTurn {
		if _, err := turnSeq(args.Turn); err != nil {
			return err
		}
	}
	return nil
}

// turnSeq returns the number turn names, which is a positive integer, and an error naming turn when it is not one.
func turnSeq(turn string) (int64, error) {
	seq, err := strconv.ParseInt(turn, 10, 64)
	if err != nil || seq < 1 {
		return 0, fmt.Errorf("turn %q is not a positive integer", turn)
	}
	return seq, nil
}

// checkItemKeys refuses a task, question, decision or turn given with a part other than its own.
func checkItemKeys(args epicGetArgs) error {
	if args.Task != "" && args.Part != partTask {
		return needsPart("task", partTask, args.Part)
	}
	if args.Question != "" && args.Part != partQuestion {
		return needsPart("question", partQuestion, args.Part)
	}
	if args.Decision != "" && args.Part != partDecision {
		return needsPart("decision", partDecision, args.Part)
	}
	if args.Turn != "" && args.Part != partTurn {
		return needsPart("turn", partTurn, args.Part)
	}
	return nil
}

// needsPart is the refusal of key, a task, question, decision or turn, given with a call whose part is part rather than
// want. An empty part means the call names no part.
func needsPart(key, want, part string) error {
	if part == "" {
		return fmt.Errorf("%s needs part %s", key, want)
	}
	return fmt.Errorf("%s needs part %s, not part %s", key, want, part)
}

// isEpicPart reports whether part is one of the parts epic_get reads.
func isEpicPart(part string) bool {
	switch part {
	case partTasks, partTurns, partDecisions, partQuestions, partHistory, partContract, partWorkflow, partReport,
		partTask, partQuestion, partDecision, partTurn:
		return true
	}
	return false
}

// epicGetText returns the text epic_get answers with for args, which checkEpicGetArgs has accepted: the one-screen
// summary of epic when args names no part, and else the text of that part.
func epicGetText(args epicGetArgs, epic *v1.StoredEpic) (string, error) {
	if args.Part == "" {
		return epicSummary(epic)
	}
	return epicPartText(args, epic)
}

// epicPartText returns the text of the part args names, paged as its kind pages. A task, question, decision or turn the
// epic lacks is an error.
func epicPartText(args epicGetArgs, epic *v1.StoredEpic) (string, error) {
	switch args.Part {
	case partTasks:
		return pageList(partTasks, mapItems(epic.GetTasks(), taskItem), args), nil
	case partTurns:
		return pageList(partTurns, mapItems(epic.GetTurns(), turnItem), args), nil
	case partDecisions:
		return pageList(partDecisions, mapItems(epic.GetDecisions(), decisionItem), args), nil
	case partQuestions:
		return pageList(partQuestions, mapItems(epic.GetQuestions(), questionItem), args), nil
	case partHistory:
		lines := slices.Concat(mapItems(epic.GetTriggers(), triggerItem), mapItems(epic.GetTells(), tellItem))
		return pageList(partHistory, lines, args), nil
	case partContract:
		text, err := contractText(epic.GetContract())
		if err != nil {
			return "", err
		}
		return pageText(partContract, text, args), nil
	case partWorkflow:
		return pageText(partWorkflow, workflowText(epic.GetWorkflow()), args), nil
	case partReport:
		text, err := reportText(epic.GetReport())
		if err != nil {
			return "", err
		}
		return pageText(partReport, text, args), nil
	default: // The part is task, question, decision or turn: checkEpicGetArgs accepts no other part here.
		return itemPartText(args, epic)
	}
}

// itemPartText returns the text of the one item args names, which is a task, decision or turn, or else a question. An
// item the epic lacks is an error.
func itemPartText(args epicGetArgs, epic *v1.StoredEpic) (string, error) {
	switch args.Part {
	case partTask:
		task, ok := itemNamed(epic.GetTasks(), args.Task)
		if !ok {
			return "", fmt.Errorf("task: no task %s in epic %s", args.Task, args.ID)
		}
		return pageItem(partTask, task, args)
	case partDecision:
		decision, ok := itemNamed(epic.GetDecisions(), args.Decision)
		if !ok {
			return "", fmt.Errorf("decision: no decision %s in epic %s", args.Decision, args.ID)
		}
		return pageItem(partDecision, decision, args)
	case partTurn:
		seq, err := turnSeq(args.Turn)
		if err != nil {
			return "", err
		}
		turn, ok := turnNamed(epic.GetTurns(), seq)
		if !ok {
			return "", fmt.Errorf("turn: no turn %d in epic %s", seq, args.ID)
		}
		return pageItem(partTurn, turn, args)
	default: // The part is question: checkEpicGetArgs accepts no other part here.
		question, ok := itemNamed(epic.GetQuestions(), args.Question)
		if !ok {
			return "", fmt.Errorf("question: no question %s in epic %s", args.Question, args.ID)
		}
		return pageItem(partQuestion, question, args)
	}
}

// itemNamed returns the item of items whose id is id, and whether there is one.
func itemNamed[Item interface{ GetId() string }](items []Item, id string) (Item, bool) {
	for _, item := range items {
		if item.GetId() == id {
			return item, true
		}
	}
	var none Item
	return none, false
}

// turnNamed returns the turn of turns whose seq is seq, and whether there is one.
func turnNamed(turns []*v1.EpicTurn, seq int64) (*v1.EpicTurn, bool) {
	i := slices.IndexFunc(turns, func(turn *v1.EpicTurn) bool { return turn.GetSeq() == seq })
	if i < 0 {
		return nil, false
	}
	return turns[i], true
}

// pageItem returns the protobuf JSON of item, paged as the text of part is.
func pageItem(part string, item proto.Message, args epicGetArgs) (string, error) {
	text, err := indentedJSON(item)
	if err != nil {
		return "", err
	}
	return pageText(part, text, args), nil
}

// page is the offset and limit of one read of a part: the first item or rune read, and how many.
type page struct {
	offset int
	limit  int
}

// pageOf returns the page args asks for, where an absent or zero limit is defaultLimit.
func pageOf(args epicGetArgs, defaultLimit int) page {
	paged := page{limit: defaultLimit}
	if args.Offset != nil {
		paged.offset = *args.Offset
	}
	if args.Limit != nil && *args.Limit != 0 {
		paged.limit = *args.Limit
	}
	return paged
}

// pageList returns the page of a list part: its header, which names the items the page holds and the offset it starts
// at, then the lines of those items. When items remain past the page, its last line names the offset that continues.
func pageList(part string, items []string, args epicGetArgs) string {
	paged := pageOf(args, listLimitDefault)
	start := min(paged.offset, len(items))
	end := start + min(paged.limit, len(items)-start)
	lines := make([]string, 0, end-start+2)
	lines = append(lines, fmt.Sprintf("%s: %d of %d from offset %d", part, end-start, len(items), paged.offset))
	lines = append(lines, items[start:end]...)
	if end < len(items) {
		lines = append(lines, fmt.Sprintf("offset %d continues", end))
	}
	return strings.Join(lines, "\n")
}

// pageText returns the page of a text part or an item: the runes of text from the offset, up to the limit. When text
// remains past them, a last line names the offset that continues. An empty text is none, and an offset at or past the
// end of a text is named as such.
func pageText(part, text string, args epicGetArgs) string {
	if text == "" {
		return part + ": none"
	}
	paged := pageOf(args, textLimitDefault)
	runes := []rune(text)
	total := len(runes)
	if paged.offset >= total {
		return fmt.Sprintf("%s: no text from offset %d of %d runes", part, paged.offset, total)
	}
	end := paged.offset + min(paged.limit, total-paged.offset)
	body := string(runes[paged.offset:end])
	if end < total {
		return fmt.Sprintf("%s\n… cut at rune %d of %d; offset %d continues", body, end, total, end)
	}
	return body
}

// mapItems returns the line of each item, in order.
func mapItems[Item any](items []Item, line func(Item) string) []string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, line(item))
	}
	return lines
}

// taskItem returns the line of a task: its id and status, then its title.
func taskItem(task *v1.EpicTask) string {
	return itemLine([]string{task.GetId(), task.GetStatus()}, task.GetTitle())
}

// turnItem returns the line of a turn: turn, its seq, its state and its outcome, then the first line of its outline, or
// else of its reason.
func turnItem(turn *v1.EpicTurn) string {
	text := firstLine(turn.GetOutline())
	if text == "" {
		text = firstLine(turn.GetReason())
	}
	return itemLine([]string{"turn", strconv.FormatInt(turn.GetSeq(), 10), turn.GetState(), turn.GetOutcome()}, text)
}

// decisionItem returns the line of a decision: its id, and answers and its question when it answers one, then its text.
func decisionItem(decision *v1.EpicDecision) string {
	fields := []string{decision.GetId()}
	if question := decision.GetQuestion(); question != "" {
		fields = append(fields, "answers", question)
	}
	return itemLine(fields, decision.GetText())
}

// questionItem returns the line of a question: its id, its state and its label, then its text.
func questionItem(question *v1.EpicQuestion) string {
	return itemLine([]string{question.GetId(), question.GetState(), question.GetLabel()}, question.GetText())
}

// triggerItem returns the line of a trigger: trigger, its id and its state, then its cause.
func triggerItem(trigger *v1.EpicTrigger) string {
	return itemLine([]string{"trigger", trigger.GetId(), trigger.GetState()}, trigger.GetCause())
}

// tellItem returns the line of a tell: tell, its seq and whether it was told, then its text.
func tellItem(tell *v1.EpicTell) string {
	told := "untold"
	if tell.GetTold() {
		told = "told"
	}
	return itemLine([]string{"tell", strconv.FormatInt(tell.GetSeq(), 10), told}, tell.GetText())
}

// itemLine returns the line of an item: its fields that are not empty, then ": " and the first line of text, cut to
// itemTextCap runes, when that line is not empty.
func itemLine(fields []string, text string) string {
	line := strings.Join(nonEmpty(fields), " ")
	if first := capRunes(firstLine(text), itemTextCap); first != "" {
		line += ": " + first
	}
	return line
}

// nonEmpty returns the fields that are not empty.
func nonEmpty(fields []string) []string {
	kept := make([]string, 0, len(fields))
	for _, field := range fields {
		if field != "" {
			kept = append(kept, field)
		}
	}
	return kept
}

// firstLine returns text up to its first newline.
func firstLine(text string) string {
	first, _, _ := strings.Cut(text, "\n")
	return first
}
