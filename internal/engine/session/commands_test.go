package session

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

const (
	colorInput = `{"questions":[{"question":"Which color do you prefer?","header":"Color",` +
		`"multiSelect":false,"options":[{"label":"Red","description":"A warm color"},` +
		`{"label":"Blue","description":"A cool color"}]}]}`
	colorAnswer = `{"type":"control_response","response":{"subtype":"success","request_id":"req_color_1",` +
		`"response":{"behavior":"allow","updatedInput":{"answers":{"Which color do you prefer?":"Blue"},` +
		`"questions":[{"question":"Which color do you prefer?","header":"Color","multiSelect":false,` +
		`"options":[{"label":"Red","description":"A warm color"},{"label":"Blue","description":"A cool color"}]}]}}}}`
	fixtureQuestionAnswer = `{"type":"control_response","response":{"subtype":"success","request_id":"req_3",` +
		`"response":{"behavior":"allow","updatedInput":{"answers":{"redacted":"redacted"},` +
		`"questions":[{"question":"redacted","header":"redacted","options":[{"label":"redacted",` +
		`"description":"redacted"}],"multiSelect":false}]}}}}`
	writeInput    = `{"file_path":"/home/user/notes.txt","content":"hello"}`
	writeUpdated  = `{"file_path":"/home/user/notes.txt","content":"goodbye"}`
	writeAllowOwn = `{"type":"control_response","response":{"subtype":"success","request_id":"req_write_1",` +
		`"response":{"behavior":"allow","updatedInput":` + writeInput + `}}}`
	writeAllowUpdated = `{"type":"control_response","response":{"subtype":"success","request_id":"req_write_1",` +
		`"response":{"behavior":"allow","updatedInput":` + writeUpdated + `}}}`
	writeAllowNew = `{"type":"control_response","response":{"subtype":"success","request_id":"req_new",` +
		`"response":{"behavior":"allow","updatedInput":` + writeInput + `}}}`
	writeDeny = `{"type":"control_response","response":{"subtype":"success","request_id":"req_write_1",` +
		`"response":{"behavior":"deny","message":"Not today."}}}`
	writeRefuse = `{"type":"control_response","response":{"subtype":"error","request_id":"req_write_1",` +
		`"error":"Refused by the host."}}`
	modeChange = `{"type":"control_request","request_id":"orrery-2",` +
		`"request":{"subtype":"set_permission_mode","mode":"acceptEdits"}}`
	elicitationLine = `{"type":"control_request","request_id":"req_elicit_1",` +
		`"request":{"subtype":"elicitation","mcp_server_name":"invented-server","message":"Pick one"}}`
	elicitationRefusal = `{"type":"control_response","response":{"subtype":"error","request_id":"req_elicit_1",` +
		`"error":"` + refusalMessage + `"}}`
	twoQuestionsInput = `{"questions":[{"question":"First?","header":"One","multiSelect":false,` +
		`"options":[{"label":"Red","description":"d"}]},{"question":"Second?","header":"Two",` +
		`"multiSelect":false,"options":[{"label":"Red","description":"d"}]}]}`
	multiSelectInput = `{"questions":[{"question":"Pick several?","header":"Color","multiSelect":true,` +
		`"options":[{"label":"Red","description":"A warm color"}]}]}`
)

// canUseTool returns a can_use_tool control request line for a tool that asks the host.
func canUseTool(requestID, toolName, toolUseID, input string) []byte {
	return []byte(`{"type":"control_request","request_id":"` + requestID +
		`","request":{"subtype":"can_use_tool","tool_name":"` + toolName +
		`","input":` + input + `,"tool_use_id":"` + toolUseID + `"}}`)
}

// cancelLine returns a control_cancel_request line for requestID.
func cancelLine(requestID string) []byte {
	return []byte(`{"type":"control_cancel_request","request_id":"` + requestID + `"}`)
}

// fixtureLine returns the first line of the named capture fixture that contains marker.
func fixtureLine(t *testing.T, name, marker string) []byte {
	t.Helper()
	for _, line := range fixtureLines(t, name) {
		if strings.Contains(string(line), marker) {
			return line
		}
	}
	t.Fatalf("fixture %s has no line containing %q", name, marker)
	return nil
}

// requireRequest returns the next event, which must be a Request of kind with requestID.
func requireRequest(t *testing.T, session *Session, kind RequestKind, requestID string) Request {
	t.Helper()
	request, ok := nextEvent(t, session).(Request)
	if !ok {
		t.Fatalf("event is not a Request")
	}
	if request.Kind != kind || request.RequestID != requestID {
		t.Fatalf("request = %#v, want kind %q with id %q", request, kind, requestID)
	}
	return request
}

// requireStdin fails the test unless stdin holds exactly the initialize request and then want, in order.
func requireStdin(t *testing.T, process *fakeProcess, want ...string) {
	t.Helper()
	wantLines := append([]string{wantInitialize}, want...)
	if got := stdinLines(process); !slices.Equal(got, wantLines) {
		t.Fatalf("stdin lines = %q, want %q", got, wantLines)
	}
}

// requireError fails the test unless err is want.
func requireError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestAnswerQuestionOnFixtureRequestWritesRecordedShape(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	emit(t, process, fixtureLine(t, "question", `"subtype":"can_use_tool"`))
	requireRequest(t, session, RequestQuestion, "req_3")
	if err := session.AnswerQuestion("req_3", "redacted"); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}
	requireStdin(t, process, fixtureQuestionAnswer)
}

func TestAnswerQuestionWritesAnswerKeyedByQuestionText(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	emit(t, process, canUseTool("req_color_1", "AskUserQuestion", "toolu_color_1", colorInput))
	requireRequest(t, session, RequestQuestion, "req_color_1")
	if err := session.AnswerQuestion("req_color_1", "Blue"); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}
	requireStdin(t, process, colorAnswer)
}

func TestAllowWithoutUpdatedInputEchoesRequestInput(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	emit(t, process, canUseTool("req_write_1", "Write", "toolu_write_1", writeInput))
	requireRequest(t, session, RequestPermission, "req_write_1")
	if err := session.Allow("req_write_1", nil); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	requireStdin(t, process, writeAllowOwn)
}

func TestAllowWithUpdatedInputWritesIt(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	emit(t, process, canUseTool("req_write_1", "Write", "toolu_write_1", writeInput))
	requireRequest(t, session, RequestPermission, "req_write_1")
	if err := session.Allow("req_write_1", json.RawMessage(writeUpdated)); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	requireStdin(t, process, writeAllowUpdated)
}

func TestAllowWithInvalidUpdatedInputWritesNothingAndStaysOpen(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	emit(t, process, canUseTool("req_write_1", "Write", "toolu_write_1", writeInput))
	requireRequest(t, session, RequestPermission, "req_write_1")
	if err := session.Allow("req_write_1", json.RawMessage(`{"file_path":`)); err == nil {
		t.Fatalf("Allow with invalid updated input: error = nil, want an error")
	}
	requireStdin(t, process)
	if err := session.Allow("req_write_1", nil); err != nil {
		t.Fatalf("Allow after the rejected input: %v", err)
	}
	requireStdin(t, process, writeAllowOwn)
}

func TestDenyWritesDenyBody(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	emit(t, process, canUseTool("req_write_1", "Write", "toolu_write_1", writeInput))
	requireRequest(t, session, RequestPermission, "req_write_1")
	if err := session.Deny("req_write_1", "Not today."); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	requireStdin(t, process, writeDeny)
}

func TestRefuseWritesErrorResponse(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	emit(t, process, canUseTool("req_write_1", "Write", "toolu_write_1", writeInput))
	requireRequest(t, session, RequestPermission, "req_write_1")
	if err := session.Refuse("req_write_1", "Refused by the host."); err != nil {
		t.Fatalf("Refuse: %v", err)
	}
	requireStdin(t, process, writeRefuse)
}

func TestSetPermissionModeWritesRequest(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	if err := session.SetPermissionMode("acceptEdits"); err != nil {
		t.Fatalf("SetPermissionMode: %v", err)
	}
	requireStdin(t, process, modeChange)
}

func TestRefusedElicitationIsWrittenAndDeliveredRefused(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	emit(t, process, []byte(elicitationLine))
	request := requireRequest(t, session, RequestOther, "req_elicit_1")
	if !request.Refused || request.Subtype != "elicitation" {
		t.Fatalf("request = %#v, want a refused elicitation", request)
	}
	requireStdin(t, process, elicitationRefusal)
	requireError(t, session.AnswerQuestion("req_elicit_1", "Red"), ErrNoPendingRequest)
	requireStdin(t, process, elicitationRefusal)
}

func TestRefusalThatCannotBeWrittenDeliversUnrefused(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	if err := process.stdin.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}
	emit(t, process, []byte(elicitationLine))
	request := requireRequest(t, session, RequestOther, "req_elicit_1")
	if request.Refused {
		t.Errorf("Refused = true, want false when the refusal cannot be written")
	}
}

func TestCancelledRequestCarriesToolUseIDAndIsNoLongerAnswerable(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	emit(t, process, canUseTool("req_color_1", "AskUserQuestion", "toolu_color_1", colorInput))
	requireRequest(t, session, RequestQuestion, "req_color_1")
	emit(t, process, cancelLine("req_color_1"))
	want := RequestCancelled{RequestID: "req_color_1", ToolUseID: "toolu_color_1"}
	if got := nextEvent(t, session); got != want {
		t.Errorf("event = %#v, want %#v", got, want)
	}
	requireError(t, session.AnswerQuestion("req_color_1", "Blue"), ErrNoPendingRequest)
	requireError(t, session.Allow("req_color_1", nil), ErrNoPendingRequest)
	requireStdin(t, process)
}

func TestSecondAnswerToOneRequestWritesNothing(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	emit(t, process, canUseTool("req_color_1", "AskUserQuestion", "toolu_color_1", colorInput))
	requireRequest(t, session, RequestQuestion, "req_color_1")
	if err := session.AnswerQuestion("req_color_1", "Blue"); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}
	requireError(t, session.AnswerQuestion("req_color_1", "Red"), ErrNoPendingRequest)
	requireStdin(t, process, colorAnswer)
}

func TestCommandsForUnknownRequestWriteNothing(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	calls := []struct {
		name string
		call func() error
	}{
		{"AnswerQuestion", func() error { return session.AnswerQuestion("req_unknown", "Red") }},
		{"Allow", func() error { return session.Allow("req_unknown", nil) }},
		{"Deny", func() error { return session.Deny("req_unknown", "no") }},
		{"Refuse", func() error { return session.Refuse("req_unknown", "no") }},
	}
	for _, tc := range calls {
		if err := tc.call(); !errors.Is(err, ErrNoPendingRequest) {
			t.Errorf("%s: error = %v, want ErrNoPendingRequest", tc.name, err)
		}
	}
	requireStdin(t, process)
}

func TestAnswerQuestionOnPermissionRequestIsNotQuestion(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	emit(t, process, canUseTool("req_write_1", "Write", "toolu_write_1", writeInput))
	requireRequest(t, session, RequestPermission, "req_write_1")
	requireError(t, session.AnswerQuestion("req_write_1", "Red"), ErrNotQuestion)
	requireStdin(t, process)
	if err := session.Allow("req_write_1", nil); err != nil {
		t.Fatalf("Allow after the rejected answer: %v", err)
	}
	requireStdin(t, process, writeAllowOwn)
}

func TestUnverifiedAnswersWriteNothing(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	cases := []struct {
		requestID string
		input     string
		label     string
	}{
		{"req_two", twoQuestionsInput, "Red"},
		{"req_multi", multiSelectInput, "Red"},
		{"req_label", colorInput, "Green"},
		{"req_empty", `{"questions":[]}`, "Red"},
	}
	for _, tc := range cases {
		emit(t, process, canUseTool(tc.requestID, "AskUserQuestion", "toolu_"+tc.requestID, tc.input))
		requireRequest(t, session, RequestQuestion, tc.requestID)
	}
	for _, tc := range cases {
		requireError(t, session.AnswerQuestion(tc.requestID, tc.label), ErrUnverifiedAnswer)
	}
	requireStdin(t, process)
}

func TestUnverifiedAnswerLeavesRequestAnswerable(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	emit(t, process, canUseTool("req_color_1", "AskUserQuestion", "toolu_color_1", colorInput))
	requireRequest(t, session, RequestQuestion, "req_color_1")
	requireError(t, session.AnswerQuestion("req_color_1", "Green"), ErrUnverifiedAnswer)
	requireStdin(t, process)
	if err := session.AnswerQuestion("req_color_1", "Blue"); err != nil {
		t.Fatalf("AnswerQuestion after the rejected answer: %v", err)
	}
	requireStdin(t, process, colorAnswer)
}

func TestNewRequestWithSameToolUseIDReplacesOpenOne(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	emit(t, process, canUseTool("req_old", "Write", "toolu_same", writeInput))
	requireRequest(t, session, RequestPermission, "req_old")
	emit(t, process, canUseTool("req_new", "Write", "toolu_same", writeInput))
	requireRequest(t, session, RequestPermission, "req_new")
	requireError(t, session.Allow("req_old", nil), ErrNoPendingRequest)
	requireStdin(t, process)
	if err := session.Allow("req_new", nil); err != nil {
		t.Fatalf("Allow on the replacing request: %v", err)
	}
	requireStdin(t, process, writeAllowNew)
}
