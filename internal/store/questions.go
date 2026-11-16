package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
)

// ErrBadQuestion reports a planner question that CompleteTurn refuses. The error names the question and its first
// refusal, and the turn writes nothing.
var ErrBadQuestion = errors.New("bad planner question")

// ChoiceOther is the choice orrery offers on every question. A question never declares it.
const ChoiceOther = "other"

// ParkLabel is the label of the questions orrery asks when it parks an epic. A planner question never takes it.
const ParkLabel = "park"

// EpicChoice is one declared choice of a question: its id and what choosing it does.
type EpicChoice struct {
	ID   string `json:"id"`
	Does string `json:"does"`
}

// EpicQuestionInput is one question of a planner return: its label, its text, its declared choices, the choice it
// recommends with why and how confidently, and the question it replaces, "" or Q<n>.
type EpicQuestionInput struct {
	Label       string
	Text        string
	Choices     []EpicChoice
	Recommended string
	Why         string
	Confidence  string
	Replaces    string
}

// minChoices is the fewest choices a question declares.
const minChoices = 2

// kebabLabel matches a label: lowercase letters and digits in words joined by single hyphens.
var kebabLabel = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// QuestionRefusals returns the rules question breaks, one `field: reason` line per broken rule, in the order the rules
// are stated here, and none when question passes. A question passes when its label is kebab-case and not park, its text
// is not empty, it declares at least two choices, each with an id that is not empty, other or named twice and a does
// that is not empty, it recommends one of its choices with a why that is not empty, its confidence is low, medium or
// high, and its replaces is "" or Q<n> with n >= 1.
func QuestionRefusals(question EpicQuestionInput) []string {
	var refusals []string
	switch {
	case question.Label == "":
		refusals = append(refusals, "label: empty")
	case !kebabLabel.MatchString(question.Label):
		refusals = append(refusals, "label: not kebab-case")
	case question.Label == ParkLabel:
		refusals = append(refusals, "label: park is reserved for orrery")
	}
	if question.Text == "" {
		refusals = append(refusals, "text: empty")
	}
	if len(question.Choices) < minChoices {
		refusals = append(refusals, "choices: fewer than two choices")
	}
	refusals = append(refusals, choiceRefusals(question.Choices)...)
	recommends := question.Recommended != "" && slices.ContainsFunc(question.Choices, func(choice EpicChoice) bool {
		return choice.ID == question.Recommended
	})
	if !recommends {
		refusals = append(refusals, "recommended: names no declared choice")
	}
	if question.Why == "" {
		refusals = append(refusals, "why: empty")
	}
	if !slices.Contains([]string{"low", "medium", "high"}, question.Confidence) {
		refusals = append(refusals, "confidence: not low, medium or high")
	}
	if _, ok := replacesSeq(question.Replaces); !ok {
		refusals = append(refusals, "replaces: not empty or Q<n> with n >= 1")
	}
	return refusals
}

// choiceRefusals returns the refusals of the declared choices, each named by its position: an id that is empty, other
// or named twice, and an empty does.
func choiceRefusals(choices []EpicChoice) []string {
	var refusals []string
	ids := make(map[string]bool, len(choices))
	for i, choice := range choices {
		switch {
		case choice.ID == "":
			refusals = append(refusals, fmt.Sprintf("choices[%d].id: empty", i))
		case choice.ID == ChoiceOther:
			refusals = append(refusals, fmt.Sprintf("choices[%d].id: other is offered by orrery", i))
		case ids[choice.ID]:
			refusals = append(refusals, fmt.Sprintf("choices[%d].id: named twice", i))
		}
		ids[choice.ID] = true
		if choice.Does == "" {
			refusals = append(refusals, fmt.Sprintf("choices[%d].does: empty", i))
		}
	}
	return refusals
}

// checkQuestions returns ErrBadQuestion for the first question that breaks a rule of QuestionRefusals. The error names
// the question by its position and label, and gives its first refusal.
func checkQuestions(questions []EpicQuestionInput) error {
	for i, question := range questions {
		if refusals := QuestionRefusals(question); len(refusals) != 0 {
			return fmt.Errorf("%w: question %d %q: %s", ErrBadQuestion, i+1, question.Label, refusals[0])
		}
	}
	return nil
}

// replacesRef matches a replaces value that names a question: Q<n> with n >= 1 and no leading zero.
var replacesRef = regexp.MustCompile(`^Q[1-9][0-9]*$`)

// replacesSeq returns the seq a replaces value names: 0 for "", and n for Q<n>. ok is false for any other value, and
// for a seq that does not fit an int64.
func replacesSeq(replaces string) (int64, bool) {
	if replaces == "" {
		return 0, true
	}
	if !replacesRef.MatchString(replaces) {
		return 0, false
	}
	seq, err := strconv.ParseInt(replaces[1:], 10, 64)
	return seq, err == nil
}

// choicesOf decodes the JSON array of choices a question stores.
func choicesOf(stored string) ([]EpicChoice, error) {
	var choices []EpicChoice
	if err := json.Unmarshal([]byte(stored), &choices); err != nil {
		return nil, fmt.Errorf("decode choices: %w", err)
	}
	return choices, nil
}
