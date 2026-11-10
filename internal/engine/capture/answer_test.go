package capture

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/wire"
)

const (
	testDenyMessage = "The probe denies this tool call."
	testQuestion    = "Which letter do you prefer?"
	testWriteInput  = `{"file_path":"/home/user/note.txt","content":"ok"}`
	testAskInput    = `{"questions":[{"question":"Which letter do you prefer?","header":"Letter",` +
		`"multiSelect":false,"options":[{"label":"A","description":"first"},{"label":"B","description":"second"}]}]}`
)

// testRequest builds a can_use_tool request for tool with the given raw input.
func testRequest(tool, input string) wire.SDKControlPermissionRequest {
	return wire.SDKControlPermissionRequest{
		Subtype:   "can_use_tool",
		ToolName:  tool,
		Input:     json.RawMessage(input),
		ToolUseID: "toolu_1",
	}
}

func TestDecideAnswers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		answer    Answer
		request   wire.SDKControlPermissionRequest
		wantOK    bool
		wantAllow bool
		wantMsg   string
		wantInput string
	}{
		{
			name:    "none sends no answer",
			answer:  AnswerNone,
			request: testRequest("Write", testWriteInput),
		},
		{
			name:      "allow passes the input through unchanged",
			answer:    AnswerAllow,
			request:   testRequest("Write", testWriteInput),
			wantOK:    true,
			wantAllow: true,
			wantInput: testWriteInput,
		},
		{
			name:    "deny refuses the tool call",
			answer:  AnswerDeny,
			request: testRequest("Write", testWriteInput),
			wantOK:  true,
			wantMsg: testDenyMessage,
		},
		{
			name:    "question leaves other tools unanswered",
			answer:  AnswerQuestion,
			request: testRequest("Write", testWriteInput),
		},
		{
			name:      "question answers AskUserQuestion",
			answer:    AnswerQuestion,
			request:   testRequest("AskUserQuestion", testAskInput),
			wantOK:    true,
			wantAllow: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, ok, err := Decide(test.answer, test.request)
			if err != nil {
				t.Fatalf("Decide returned error: %v", err)
			}
			if ok != test.wantOK {
				t.Fatalf("answered = %v, want %v", ok, test.wantOK)
			}
			if !ok {
				return
			}
			if got.Allow != test.wantAllow {
				t.Errorf("Allow = %v, want %v", got.Allow, test.wantAllow)
			}
			if got.Message != test.wantMsg {
				t.Errorf("Message = %q, want %q", got.Message, test.wantMsg)
			}
			if test.wantInput != "" && !bytes.Equal(got.UpdatedInput, []byte(test.wantInput)) {
				t.Errorf("UpdatedInput = %s, want %s", got.UpdatedInput, test.wantInput)
			}
		})
	}
}

func TestDecideQuestionKeepsOriginalInputAndAddsAnswers(t *testing.T) {
	t.Parallel()
	got, ok, err := Decide(AnswerQuestion, testRequest("AskUserQuestion", testAskInput))
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if !ok {
		t.Fatal("Decide did not answer an AskUserQuestion request")
	}
	var want, have map[string]any
	if err := json.Unmarshal([]byte(testAskInput), &want); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if err := json.Unmarshal(got.UpdatedInput, &have); err != nil {
		t.Fatalf("decode UpdatedInput %s: %v", got.UpdatedInput, err)
	}
	wantAnswers := map[string]any{testQuestion: "A"}
	if !reflect.DeepEqual(have["answers"], wantAnswers) {
		t.Errorf("answers = %v, want %v", have["answers"], wantAnswers)
	}
	delete(have, "answers")
	if !reflect.DeepEqual(have, want) {
		t.Errorf("original keys changed: got %v, want %v", have, want)
	}
}

func TestDecideQuestionRejectsMalformedInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
	}{
		{name: "not JSON", input: `not json`},
		{name: "no questions key", input: `{}`},
		{name: "empty questions", input: `{"questions":[]}`},
		{name: "question without options", input: `{"questions":[{"question":"Q","options":[]}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, ok, err := Decide(AnswerQuestion, testRequest("AskUserQuestion", test.input))
			if err == nil {
				t.Fatalf("Decide accepted malformed input %s (answered = %v)", test.input, ok)
			}
		})
	}
}
