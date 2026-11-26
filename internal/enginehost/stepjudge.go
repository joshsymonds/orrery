package enginehost

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/haikuprompt"
)

// stepJudgeVerdictMet and stepJudgeVerdictUnmet are the two verdicts the judge may return.
const (
	stepJudgeVerdictMet   = "met"
	stepJudgeVerdictUnmet = "unmet"
)

// stepJudgeSchema is the JSON schema the judge's reply must match: a verdict of met or unmet and a one-line reason.
const stepJudgeSchema = `{
	"type": "object",
	"properties": {
		"verdict": {"type": "string", "enum": ["met", "unmet"]},
		"reason": {"type": "string"}
	},
	"required": ["verdict", "reason"],
	"additionalProperties": false
}`

// stepJudgeSectionBytes is the most a kept head or tail of a section holds.
const stepJudgeSectionBytes = 8 * 1024

// stepJudgeInstruction closes the judge's input, after its tagged parts. The mechanical checks have already passed, so the judge answers unmet
// only when the return itself shows the task is not finished or reports a failure. It applies to every kind of step.
const stepJudgeInstruction = "The step's mechanical checks already passed. " +
	"Answer unmet only when the return says part of the task is not done or reports a failure. " +
	"Give a one-line reason, naming what is missing when unmet. " +
	"A return that omits proof, output or counts is not unmet. " +
	"Never ask for a change the task does not name. " +
	"A tagged part with a line naming bytes left out lost only its middle, which does not mean the return was cut off."

// errStepJudgeVerdict reports a judge reply whose verdict is neither met nor unmet.
var errStepJudgeVerdict = errors.New("enginehost: judge verdict is not met or unmet")

// errStepJudgeReason reports a judge reply with no reason after trimming.
var errStepJudgeReason = errors.New("enginehost: judge reason is empty")

// stepJudgeInput builds the judge's stdin from the step's first prompt, its return, and, when non-empty, the diff stat
// and the gate's stdout and stderr tails, each in its own tag, laid out by haikuprompt.Input. The prompt and the return keep both ends of a long
// text, as stepJudgeEnds describes. The diff stat keeps its head and each gate tail keeps its last bytes, since the
// failure a judge needs sits at the end of the output. The diff stat and each gate tail are omitted when blank.
func stepJudgeInput(prompt, result, diffStat, gateStdout, gateStderr string) string {
	parts := []haikuprompt.Part{
		{Tag: "task", Text: stepJudgeEnds(prompt)},
		{Tag: "return", Text: stepJudgeEnds(result)},
	}
	if strings.TrimSpace(diffStat) != "" {
		parts = append(parts, haikuprompt.Part{Tag: "diff_stat", Text: stepJudgeHead(diffStat)})
	}
	if strings.TrimSpace(gateStdout) != "" {
		parts = append(parts, haikuprompt.Part{Tag: "gate_stdout_tail", Text: stepJudgeTail(gateStdout)})
	}
	if strings.TrimSpace(gateStderr) != "" {
		parts = append(parts, haikuprompt.Part{Tag: "gate_stderr_tail", Text: stepJudgeTail(gateStderr)})
	}
	return haikuprompt.Input(stepJudgeInstruction, parts...)
}

// stepJudgeHead keeps the first stepJudgeSectionBytes of text, backing off to a rune boundary.
func stepJudgeHead(text string) string {
	if len(text) <= stepJudgeSectionBytes {
		return text
	}
	n := stepJudgeSectionBytes
	for n > 0 && !utf8.RuneStart(text[n]) {
		n--
	}
	return text[:n]
}

// stepJudgeTail keeps the last stepJudgeSectionBytes of text, advancing to a rune boundary.
func stepJudgeTail(text string) string {
	if len(text) <= stepJudgeSectionBytes {
		return text
	}
	n := len(text) - stepJudgeSectionBytes
	for n < len(text) && !utf8.RuneStart(text[n]) {
		n++
	}
	return text[n:]
}

// stepJudgeEnds keeps the head and the tail of text longer than twice stepJudgeSectionBytes, with a line between them
// that counts the bytes left out. Text of up to twice stepJudgeSectionBytes is returned whole. The head and the tail
// never overlap, since each is at most stepJudgeSectionBytes long and the text is longer than both together.
func stepJudgeEnds(text string) string {
	if len(text) <= 2*stepJudgeSectionBytes {
		return text
	}
	head := stepJudgeHead(text)
	tail := stepJudgeTail(text)
	return head + fmt.Sprintf("\n[%d bytes left out]\n", len(text)-len(head)-len(tail)) + tail
}

// parseStepJudge reads the judge's reply. It returns met for a met verdict and unmet for an unmet one, with the reason
// trimmed. It fails when the reply is not JSON, its verdict is neither met nor unmet, or its reason is blank.
func parseStepJudge(raw json.RawMessage) (met bool, reason string, err error) {
	var reply struct {
		Verdict string `json:"verdict"`
		Reason  string `json:"reason"`
	}
	err = json.Unmarshal(raw, &reply)
	if err != nil {
		return false, "", fmt.Errorf("enginehost: judge reply is not JSON: %w", err)
	}
	switch reply.Verdict {
	case stepJudgeVerdictMet:
		met = true
	case stepJudgeVerdictUnmet:
		met = false
	default:
		return false, "", fmt.Errorf("%w: %q", errStepJudgeVerdict, reply.Verdict)
	}
	reason = strings.TrimSpace(reply.Reason)
	if reason == "" {
		return false, "", errStepJudgeReason
	}
	return met, reason, nil
}
