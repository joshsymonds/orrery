package stepmcp

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// The caps of the epic summary. The title, park reason and labels are cut to their rune caps, and the whole summary to
// summaryBytesCap bytes.
const (
	summaryTitleCap = 200
	summaryParkCap  = 300
	summaryLabelCap = 40
	summaryBytesCap = 4000
	questionOpen    = "open"
)

// epicSummary returns the one-screen text summary of epic, which epic_get answers in place of the stored epic. It names
// the epic, its phase, whether it is paused or why it is parked, its open questions, its task counts by status, and each
// part with its item count and byte size. It lists every open question while the summary fits in summaryBytesCap bytes,
// and counts the rest on one last line; a summary that is still longer is cut to summaryBytesCap bytes.
func epicSummary(epic *v1.StoredEpic) (string, error) {
	contract := epic.GetContract()
	head := []string{
		fmt.Sprintf("epic %s: %s", contract.GetId(), capRunes(contract.GetTitle(), summaryTitleCap)),
		"phase: " + epic.GetPhase(),
		"paused: " + yesNo(epic.GetPaused()),
	}
	if parked := epic.GetParked(); parked != "" {
		first, _, _ := strings.Cut(parked, "\n")
		head = append(head, "parked: "+capRunes(first, summaryParkCap))
	}
	parts, err := partLines(epic)
	if err != nil {
		return "", err
	}
	tail := append([]string{taskLine(epic.GetTasks()), "parts:"}, parts...)
	// The question lines sit between the head and the tail, so the head's last line and the tail's first are each joined
	// to them by a newline: the two newlines of len("\n\n") bytes come out of the room.
	room := summaryBytesCap - len(strings.Join(head, "\n")) - len(strings.Join(tail, "\n")) - len("\n\n")
	lines := slices.Concat(head, openQuestionLines(epic.GetQuestions(), room), tail)
	return capBytes(strings.Join(lines, "\n"), summaryBytesCap), nil
}

// openQuestionLines returns the open-questions count, then a line per open question, in order, for as many questions as
// fit in room bytes. The count and the lines are joined by newlines, and the bytes of a question's line count with the
// line counting the questions left out after it, which is written last when any are left out. A question's line is its
// id and its label, cut to summaryLabelCap. The count is always written, and so is the line counting the questions left
// out, even when room is too small for them; epicSummary then cuts the summary to summaryBytesCap.
func openQuestionLines(questions []*v1.EpicQuestion, room int) []string {
	open := make([]*v1.EpicQuestion, 0, len(questions))
	for _, question := range questions {
		if question.GetState() == questionOpen {
			open = append(open, question)
		}
	}
	count := fmt.Sprintf("open questions: %d", len(open))
	lines := []string{count}
	size := len(count)
	for i, question := range open {
		line := "  " + question.GetId()
		if label := question.GetLabel(); label != "" {
			line += " " + capRunes(label, summaryLabelCap)
		}
		next := size + 1 + len(line)
		if left := len(open) - i - 1; left > 0 {
			next += 1 + len(moreLine(left))
		}
		if next > room {
			break
		}
		lines = append(lines, line)
		size += 1 + len(line)
	}
	if left := len(open) - (len(lines) - 1); left > 0 {
		lines = append(lines, moreLine(left))
	}
	return lines
}

// moreLine returns the line counting the open questions left out of a summary: left of them.
func moreLine(left int) string {
	return fmt.Sprintf("  and %d more", left)
}

// taskLine returns the task count, then the count of each status in status-name order.
func taskLine(tasks []*v1.EpicTask) string {
	if len(tasks) == 0 {
		return "tasks: 0"
	}
	byStatus := map[string]int{}
	for _, task := range tasks {
		byStatus[task.GetStatus()]++
	}
	counts := make([]string, 0, len(byStatus))
	for _, status := range slices.Sorted(maps.Keys(byStatus)) {
		counts = append(counts, fmt.Sprintf("%s %d", status, byStatus[status]))
	}
	return fmt.Sprintf("tasks: %d (%s)", len(tasks), strings.Join(counts, ", "))
}

// partLines returns the summary's line of each part of epic, in the order contract, workflow, tasks, turns, decisions,
// questions, history and report. A single-message part has one item when it is set, and a list part has one item per
// entry; history has the triggers and the tells.
func partLines(epic *v1.StoredEpic) ([]string, error) {
	contract, err := contractText(epic.GetContract())
	if err != nil {
		return nil, err
	}
	report, err := reportText(epic.GetReport())
	if err != nil {
		return nil, err
	}
	tasks, err := compactBytes(epic.GetTasks())
	if err != nil {
		return nil, err
	}
	turns, err := compactBytes(epic.GetTurns())
	if err != nil {
		return nil, err
	}
	decisions, err := compactBytes(epic.GetDecisions())
	if err != nil {
		return nil, err
	}
	questions, err := compactBytes(epic.GetQuestions())
	if err != nil {
		return nil, err
	}
	triggers, err := compactBytes(epic.GetTriggers())
	if err != nil {
		return nil, err
	}
	tells, err := compactBytes(epic.GetTells())
	if err != nil {
		return nil, err
	}
	return []string{
		partLine("contract", setItems(epic.GetContract() != nil), len(contract)),
		partLine("workflow", setItems(epic.GetWorkflow() != nil), len(workflowText(epic.GetWorkflow()))),
		partLine("tasks", len(epic.GetTasks()), tasks),
		partLine("turns", len(epic.GetTurns()), turns),
		partLine("decisions", len(epic.GetDecisions()), decisions),
		partLine("questions", len(epic.GetQuestions()), questions),
		partLine("history", len(epic.GetTriggers())+len(epic.GetTells()), triggers+tells),
		partLine("report", setItems(epic.GetReport() != nil), len(report)),
	}, nil
}

// partLine returns the summary's line of one part: its item count and its byte size.
func partLine(name string, items, bytes int) string {
	return fmt.Sprintf("  %s: %d items, %d bytes", name, items, bytes)
}

// setItems is the item count of a single-message part: 1 when the message is set, 0 when it is unset.
func setItems(set bool) int {
	if set {
		return 1
	}
	return 0
}

// contractText is the text of the contract part: the contract as indented protobuf JSON, empty when it is unset.
func contractText(contract *v1.EpicContract) (string, error) {
	if contract == nil {
		return "", nil
	}
	return indentedJSON(contract)
}

// workflowText is the text of the workflow part: the workflow's canonical text, empty when it is unset.
func workflowText(workflow *v1.EpicWorkflow) string {
	return workflow.GetText()
}

// reportText is the text of the report part: the report as indented protobuf JSON, empty when it is unset.
func reportText(report *v1.EpicReport) (string, error) {
	if report == nil {
		return "", nil
	}
	return indentedJSON(report)
}

// indentedJSON returns msg as indented protobuf JSON, the text whose byte length a single-message part reports.
func indentedJSON(msg proto.Message) (string, error) {
	text, err := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("encode %s: %w", msg.ProtoReflect().Descriptor().FullName(), err)
	}
	return string(text), nil
}

// compactBytes returns the sum of the byte lengths of each item's compact protobuf JSON, the size of a list part.
func compactBytes[T proto.Message](items []T) (int, error) {
	total := 0
	for _, item := range items {
		text, err := protojson.Marshal(item)
		if err != nil {
			return 0, fmt.Errorf("encode %s: %w", item.ProtoReflect().Descriptor().FullName(), err)
		}
		total += len(text)
	}
	return total, nil
}

// capRunes returns text cut to limit runes, ending with an ellipsis, when it has more runes than that.
func capRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

// capBytes returns text cut on a rune boundary to at most limit bytes, its cut ending with an ellipsis, when it has more
// bytes than that.
func capBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit - len("…")
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}

// yesNo returns yes for true and no for false.
func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
