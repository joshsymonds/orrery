package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrBadQuestion reports a planner question that CompleteTurn refuses. The error names the question and its first
// refusal, and the turn writes nothing.
var ErrBadQuestion = errors.New("bad planner question")

// The states a planner question moves through: open until it is answered, or replaced by a later question.
const (
	QuestionOpen     = "open"
	QuestionAnswered = "answered"
	QuestionReplaced = "replaced"
)

// ChoiceOther is the choice orrery offers on every question. A question never declares it.
const ChoiceOther = "other"

// ChoiceRetry is the choice a park question that is not final declares: ending the park and starting a turn on the
// waiting triggers. On a park question that a red run judged holding at base opens, it reruns the red run.
const ChoiceRetry = "retry"

// ChoiceDrop and ChoiceEnd are the choices a park question for a red run judged holding at base declares beside retry:
// dropping the Requirements the run found met, and ending the epic.
const (
	ChoiceDrop = "drop"
	ChoiceEnd  = "end"
)

// ParkLabel is the label of the questions orrery asks when it parks an epic. A planner question never takes it.
const ParkLabel = "park"

// GapsLabel is the label of the question orrery asks when an epic ends with gaps it cannot resolve. It is the label of
// the question EndEpicWithGaps opens.
const GapsLabel = "gaps"

// The choices a gaps question declares beside other: release the epic with its gaps, give the planner the owner's text
// as a correction, or close the epic as abandoned.
const (
	ChoiceRelease = "release"
	ChoiceCorrect = "correct"
	ChoiceClose   = "close"
)

// The advice a park question that is not final gives its retry choice: what retry does, why orrery recommends it, and
// how confident that recommendation is.
const (
	parkRetryDoes  = "end the park and start a turn on the waiting triggers, as orrery epic retry does"
	parkRetryWhy   = "no model judges a park; retry runs the epic again on its waiting triggers"
	parkConfidence = "low"
)

// The advice a park question for a red run judged holding at base gives: why orrery recommends retry, and how confident
// that recommendation is. A fresh judge is the check on a judgement that may have read the conclusion loosely.
const (
	metAtBaseWhy = "the judge read the conclusion as already holding at the base, " +
		"and a fresh judge checks that reading before anything is dropped"
	metAtBaseConfidence = "medium"
)

// EpicChoice is one declared choice of a question: its id, what choosing it does, and the ids of the confirmed ledger
// findings that choosing it settles with no code change. The closure round closes each finding the answered choice of
// a question closes, citing the answer, and starts no verifier for it.
type EpicChoice struct {
	ID     string   `json:"id"`
	Does   string   `json:"does"`
	Closes []string `json:"closes,omitempty"`
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
// is not empty, it declares at least two choices, each with an id that is not empty, other or named twice, a does that
// is not empty, and closes that names no id empty or twice and each id in its does, it recommends one of its choices
// with a why that is not empty, its confidence is low, medium or high, and its replaces is "" or Q<n> with n >= 1.
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
// or named twice, an empty does, then the refusals of its closes (closesRefusals).
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
		refusals = append(refusals, closesRefusals(i, choice)...)
	}
	return refusals
}

// closesRefusals returns the refusals of the closes of choice, the choice at position i, each named by its position in
// closes: an id that is empty, one named twice, and one its does does not name. A choice that closes a finding says so
// in its does, so an owner who reads only does sees every finding that choosing it closes.
func closesRefusals(i int, choice EpicChoice) []string {
	var refusals []string
	named := make(map[string]bool, len(choice.Closes))
	for j, finding := range choice.Closes {
		switch {
		case finding == "":
			refusals = append(refusals, fmt.Sprintf("choices[%d].closes[%d]: empty", i, j))
		case named[finding]:
			refusals = append(refusals, fmt.Sprintf("choices[%d].closes[%d]: named twice", i, j))
		case !strings.Contains(choice.Does, finding):
			refusals = append(refusals, fmt.Sprintf("choices[%d].closes[%d]: %s is not named in does", i, j, finding))
		}
		named[finding] = true
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

// replaceQuestion closes the question replaces of epic when it is open: it becomes replaced, with replacedBy set to the
// seq of the question that replaces it. An answered or replaced question closes nothing. A question epic does not hold
// is ErrBadQuestion.
func replaceQuestion(ctx context.Context, queries *sqlcgen.Queries, epic string, replaces, replacedBy int64) error {
	stored, err := queries.GetEpicQuestion(ctx, sqlcgen.GetEpicQuestionParams{Epic: epic, Seq: replaces})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: replaces Q%d: no such question", ErrBadQuestion, replaces)
	}
	if err != nil {
		return fmt.Errorf("read question %d of epic %s: %w", replaces, epic, err)
	}
	if stored.State != QuestionOpen {
		return nil
	}
	if _, err = queries.SetEpicQuestionReplaced(ctx, sqlcgen.SetEpicQuestionReplacedParams{
		ReplacedBy: replacedBy, Epic: epic, Seq: replaces,
	}); err != nil {
		return fmt.Errorf("replace question %d of epic %s: %w", replaces, epic, err)
	}
	return nil
}

// parkQuestion returns the question a park with reason asks. A park that is not final declares retry and recommends it.
// A final park declares no choice and recommends none, so only other is offered.
func parkQuestion(reason string, final bool) EpicQuestionInput {
	question := EpicQuestionInput{Label: ParkLabel, Text: reason, Choices: []EpicChoice{}}
	if !final {
		question.Choices = []EpicChoice{{ID: ChoiceRetry, Does: parkRetryDoes}}
		question.Recommended, question.Why, question.Confidence = ChoiceRetry, parkRetryWhy, parkConfidence
	}
	return question
}

// metAtBaseQuestion returns the question the park of an evidence run that judged the Requirements in met holding at base
// asks. It offers retry, which reruns the run, drop, which marks the Requirements dropped as met, and end, which ends the
// epic, and it recommends retry.
func metAtBaseQuestion(reason string, met []string) EpicQuestionInput {
	ids := strings.Join(met, ", ")
	return EpicQuestionInput{
		Label: ParkLabel,
		Text:  reason,
		Choices: []EpicChoice{
			{ID: ChoiceRetry, Does: "run the red check of " + ids + " again with a fresh judge"},
			{ID: ChoiceDrop, Does: "drop " + ids + " as already met, recorded as your decision, " +
				"and go on without their red"},
			{ID: ChoiceEnd, Does: "end the epic"},
		},
		Recommended: ChoiceRetry,
		Why:         metAtBaseWhy,
		Confidence:  metAtBaseConfidence,
	}
}

// insertParkQuestion opens question, a park question, for epic in the caller's write, as the next question seq of epic,
// and returns that seq. An open park question of epic is replaced by it, as a planner question replaces the question it
// names. A park leaves at most one park question open, so one park replaces at most one.
func insertParkQuestion(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	question EpicQuestionInput,
) (int64, error) {
	open, err := queries.ListOpenEpicParkQuestions(ctx, epic)
	if err != nil {
		return 0, fmt.Errorf("read open park questions of epic %s: %w", epic, err)
	}
	highest, err := queries.MaxEpicQuestionSeq(ctx, epic)
	if err != nil {
		return 0, fmt.Errorf("read question count of epic %s: %w", epic, err)
	}
	seq := highest + 1
	var replaces int64
	for _, stored := range open {
		if _, err = queries.SetEpicQuestionReplaced(ctx, sqlcgen.SetEpicQuestionReplacedParams{
			ReplacedBy: seq, Epic: epic, Seq: stored.Seq,
		}); err != nil {
			return 0, fmt.Errorf("replace park question %d of epic %s: %w", stored.Seq, epic, err)
		}
		replaces = stored.Seq
	}
	choices, err := json.Marshal(question.Choices)
	if err != nil {
		return 0, fmt.Errorf("choices of park question %d of epic %s: %w", seq, epic, err)
	}
	if err = queries.InsertEpicQuestion(withEpicEvent(ctx), sqlcgen.InsertEpicQuestionParams{
		Epic: epic, Seq: seq, Turn: 0, Text: question.Text, Label: question.Label, Choices: string(choices),
		Recommended: question.Recommended, Why: question.Why, Confidence: question.Confidence, Replaces: replaces,
	}); err != nil {
		return 0, fmt.Errorf("insert park question %d of epic %s: %w", seq, epic, err)
	}
	return seq, nil
}

// choicesOf decodes the JSON array of choices a question stores.
func choicesOf(stored string) ([]EpicChoice, error) {
	var choices []EpicChoice
	if err := json.Unmarshal([]byte(stored), &choices); err != nil {
		return nil, fmt.Errorf("decode choices: %w", err)
	}
	return choices, nil
}
