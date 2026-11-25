package stepmcp

import (
	"fmt"
	"strings"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// The caps of an acknowledgement. The id, the error's first line and the change line are cut to their rune caps, the
// warning to its rune cap, and the whole text to ackBytesCap bytes.
const (
	ackIDCap      = 200
	ackErrorCap   = 200
	ackChangeCap  = 300
	ackWarningCap = 300
	ackBytesCap   = 1000
)

// ackResult returns text as the one text block of a write's result.
func ackResult(text string) ToolResult {
	return ToolResult{Content: []Content{{Type: textContentType, Text: text}}}
}

// ackText returns the acknowledgement of a write: head, then change cut to ackChangeCap runes, then a line `warning: `
// for each warning that is not empty, its text cut to ackWarningCap runes, its newlines joined to spaces first. The text
// is cut to ackBytesCap bytes.
func ackText(head, change string, warnings ...string) string {
	lines := []string{head, capRunes(change, ackChangeCap)}
	for _, warning := range warnings {
		if warning != "" {
			lines = append(lines, "warning: "+capRunes(strings.ReplaceAll(warning, "\n", " "), ackWarningCap))
		}
	}
	return capBytes(strings.Join(lines, "\n"), ackBytesCap)
}

// ackHead returns the first line of an acknowledgement, `epic <id>: <status>`, with the id cut to ackIDCap runes.
func ackHead(id, status string) string {
	return fmt.Sprintf("epic %s: %s", capRunes(id, ackIDCap), status)
}

// epicAck returns the acknowledgement of a write whose response carries epic, with a warning line for each of warnings.
// The epic's id is its contract's id, or requestID when the response carries no epic, and its phase is the epic's phase.
func epicAck(epic *v1.StoredEpic, requestID, change string, warnings ...string) ToolResult {
	id := requestID
	if epic != nil {
		id = epic.GetContract().GetId()
	}
	return ackResult(ackText(ackHead(id, "phase "+epic.GetPhase()), change, warnings...))
}

// answerPhase returns the status of an answer's first line: the phase of the epic that its GetEpic read returned, or
// `phase not read (<err>)` when that read failed, with the error's first line cut to ackErrorCap runes. The answer is
// recorded either way.
func answerPhase(got *connect.Response[v1.GetEpicResponse], err error) string {
	if err != nil {
		first, _, _ := strings.Cut(err.Error(), "\n")
		return "phase not read (" + capRunes(first, ackErrorCap) + ")"
	}
	return "phase " + got.Msg.GetEpic().GetPhase()
}

// createChange returns what epic_create stored: the requirements of its contract and the decisions of the epic.
func createChange(epic *v1.StoredEpic) string {
	return fmt.Sprintf("created with %d requirements and %d decisions",
		len(epic.GetContract().GetRequirements()), len(epic.GetDecisions()))
}

// answerChange returns what epic_answer recorded: a decision that answers the question the request names, with the
// choice it gives, or a note when the request names no question.
func answerChange(req *v1.AnswerEpicRequest, decision *v1.EpicDecision) string {
	if req.GetQuestion() == "" {
		return "recorded note " + decision.GetId()
	}
	return fmt.Sprintf("recorded decision %s answering %s with %s",
		decision.GetId(), req.GetQuestion(), req.GetChoice())
}

// resumeChange returns what epic_resume resumed: the count of the limited steps it resumed and, when any, their ids.
func resumeChange(resumed []string) string {
	change := fmt.Sprintf("resumed %d limited steps", len(resumed))
	if len(resumed) > 0 {
		change += ": " + joinList(resumed)
	}
	return change
}

// stopChange returns what epic_stop did: it paused a running turn, or it stopped the release, so no step starts.
func stopChange(epic *v1.StoredEpic) string {
	if epic.GetPaused() {
		return "stopped its running turn and paused it"
	}
	return "stopped its release, so no further step starts"
}

// retryChange returns what epic_retry did: it reran the named node, or it ended the epic's park.
func retryChange(node string) string {
	if node != "" {
		return "reran node " + node
	}
	return "ended the park"
}

// releaseChange returns what epic_release started: the release, and the undo-none steps named to run again, if any.
func releaseChange(undoNone []int32) string {
	change := "release started"
	if len(undoNone) > 0 {
		change += "; undo-none steps " + joinList(undoNone) + " run again"
	}
	return change
}

// closeChange returns what epic_close closed the epic as, and the epic that replaces it, when it names one.
func closeChange(kind, by string) string {
	change := "closed as " + kind
	if by != "" {
		change += ", superseded by " + by
	}
	return change
}

// amendChange returns the amendment epic_amend stored: its sequence number, its Requirement and its state.
func amendChange(amendment *v1.EpicAmendment) string {
	return fmt.Sprintf("amendment A%d of %s is %s",
		amendment.GetSeq(), amendment.GetRequirement(), amendment.GetState())
}

// joinList returns items joined by ", ", each written with fmt.Sprint.
func joinList[T any](items []T) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, fmt.Sprint(item))
	}
	return strings.Join(parts, ", ")
}
