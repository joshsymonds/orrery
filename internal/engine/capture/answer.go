package capture

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/engine/wire"
)

const (
	askUserQuestionTool = "AskUserQuestion"
	denyMessage         = "The probe denies this tool call."
)

// Decision is the probe's reply to one can_use_tool request.
type Decision struct {
	Allow        bool
	UpdatedInput json.RawMessage
	Message      string
}

// Decide returns the probe's reply to request under answer. The bool is false when the probe does not
// answer this request.
func Decide(answer Answer, request wire.SDKControlPermissionRequest) (Decision, bool, error) {
	switch answer {
	case AnswerAllow:
		return Decision{Allow: true, UpdatedInput: request.Input}, true, nil
	case AnswerDeny:
		return Decision{Message: denyMessage}, true, nil
	case AnswerQuestion:
		if request.ToolName != askUserQuestionTool {
			return Decision{}, false, nil
		}
		return decideQuestion(request.Input)
	default:
		return Decision{}, false, nil
	}
}

// askQuestion is one entry of an AskUserQuestion questions array, reduced to the fields the probe reads.
type askQuestion struct {
	Question string `json:"question"`
	Options  []struct {
		Label string `json:"label"`
	} `json:"options"`
}

// decideQuestion answers each question in input with the label of its first option. The answers object
// is added beside the original keys, which are otherwise passed through unchanged.
func decideQuestion(input json.RawMessage) (Decision, bool, error) {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(input, &fields); err != nil {
		return Decision{}, false, fmt.Errorf("capture: decode AskUserQuestion input: %w", err)
	}
	var questions []askQuestion
	if err := json.Unmarshal(fields["questions"], &questions); err != nil {
		return Decision{}, false, fmt.Errorf("capture: decode AskUserQuestion questions: %w", err)
	}
	if len(questions) == 0 {
		return Decision{}, false, errors.New("capture: AskUserQuestion input has no questions")
	}
	answers := make(map[string]string, len(questions))
	for _, question := range questions {
		if len(question.Options) == 0 {
			return Decision{}, false, fmt.Errorf(
				"capture: AskUserQuestion question %q has no options", question.Question)
		}
		answers[question.Question] = question.Options[0].Label
	}
	answersJSON, err := json.Marshal(answers)
	if err != nil {
		return Decision{}, false, fmt.Errorf("capture: encode answers: %w", err)
	}
	fields["answers"] = answersJSON
	updated, err := json.Marshal(fields)
	if err != nil {
		return Decision{}, false, fmt.Errorf("capture: encode answered input: %w", err)
	}
	return Decision{Allow: true, UpdatedInput: updated}, true, nil
}
