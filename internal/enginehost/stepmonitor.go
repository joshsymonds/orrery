package enginehost

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// stepMonitorSchema is the JSON schema the monitor's reply must match: one of the watch's four verdicts and a one-line
// account of what the step is doing now.
const stepMonitorSchema = `{
	"type": "object",
	"properties": {
		"verdict": {"type": "string", "enum": ["progressing", "long_command", "looping", "wedged"]},
		"doing": {"type": "string"}
	},
	"required": ["verdict", "doing"],
	"additionalProperties": false
}`

// stepMonitorDoingBytes is the longest doing line kept.
const stepMonitorDoingBytes = 200

// stepMonitorInstruction opens the monitor's input. It defines each verdict and asks for the doing line.
const stepMonitorInstruction = "A coding step has tripped a watch. Judge what it is doing from the recent events.\n" +
	"Verdicts:\n" +
	"- " + watchVerdictProgressing + ": real progress since the last check.\n" +
	"- " + watchVerdictLongCommand + ": waiting on one long command still plausibly working, such as tests or a build.\n" +
	"- " + watchVerdictLooping + ": repeating actions without progress.\n" +
	"- " + watchVerdictWedged + ": stuck, no sign of recovery.\n" +
	"Also give doing: one line under 100 characters saying what the step is doing now."

// errStepMonitorVerdict reports a monitor reply whose verdict is not one of the four.
var errStepMonitorVerdict = errors.New("enginehost: monitor verdict is not one of the four")

// errStepMonitorDoing reports a monitor reply with no doing line after trimming.
var errStepMonitorDoing = errors.New("enginehost: monitor doing line is empty")

// stepMonitorInput builds the monitor's stdin from the trip's why sentence and the recent events.
func stepMonitorInput(why, recent string) string {
	return stepMonitorInstruction + "\n\n## Why\n" + why + "\n\n## Recent events\n" + recent
}

// parseStepMonitor reads the monitor's reply. It returns the verdict and the doing line, trimmed, reduced to its first
// line, and cut to stepMonitorDoingBytes at a rune boundary. It fails when the reply is not JSON, its verdict is not
// one of the four, or its doing line is blank.
func parseStepMonitor(raw json.RawMessage) (verdict, doing string, err error) {
	var reply struct {
		Verdict string `json:"verdict"`
		Doing   string `json:"doing"`
	}
	err = json.Unmarshal(raw, &reply)
	if err != nil {
		return "", "", fmt.Errorf("enginehost: monitor reply is not JSON: %w", err)
	}
	verdicts := []string{watchVerdictProgressing, watchVerdictLongCommand, watchVerdictLooping, watchVerdictWedged}
	if !slices.Contains(verdicts, reply.Verdict) {
		return "", "", fmt.Errorf("%w: %q", errStepMonitorVerdict, reply.Verdict)
	}
	doing = strings.TrimSpace(reply.Doing)
	if i := strings.IndexByte(doing, '\n'); i >= 0 {
		doing = strings.TrimSpace(doing[:i])
	}
	if doing == "" {
		return "", "", errStepMonitorDoing
	}
	if len(doing) > stepMonitorDoingBytes {
		n := stepMonitorDoingBytes
		for n > 0 && !utf8.RuneStart(doing[n]) {
			n--
		}
		doing = doing[:n]
	}
	return reply.Verdict, doing, nil
}
