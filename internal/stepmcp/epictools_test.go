package stepmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

const testEpicID = "demo-0a1b2c3/epic-login"

// fakeEpics answers the epic commands with the configured replies and records each request.
type fakeEpics struct {
	mu          sync.Mutex
	creates     []*v1.CreateEpicRequest
	gets        []*v1.GetEpicRequest
	lists       []*v1.ListEpicsRequest
	answers     []*v1.AnswerEpicRequest
	pauses      []*v1.PauseEpicRequest
	resumes     []*v1.ResumeEpicRequest
	stops       []*v1.StopEpicRequest
	retries     []*v1.RetryEpicRequest
	createReply *v1.CreateEpicResponse
	getReply    *v1.GetEpicResponse
	listReply   *v1.ListEpicsResponse
	answerReply *v1.AnswerEpicResponse
	pauseReply  *v1.PauseEpicResponse
	resumeReply *v1.ResumeEpicResponse
	stopReply   *v1.StopEpicResponse
	retryReply  *v1.RetryEpicResponse
	err         error
}

func (f *fakeEpics) CreateEpic(
	_ context.Context,
	req *connect.Request[v1.CreateEpicRequest],
) (*connect.Response[v1.CreateEpicResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates = append(f.creates, req.Msg)
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.createReply), nil
}

func (f *fakeEpics) GetEpic(
	_ context.Context,
	req *connect.Request[v1.GetEpicRequest],
) (*connect.Response[v1.GetEpicResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets = append(f.gets, req.Msg)
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.getReply), nil
}

func (f *fakeEpics) ListEpics(
	_ context.Context,
	req *connect.Request[v1.ListEpicsRequest],
) (*connect.Response[v1.ListEpicsResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists = append(f.lists, req.Msg)
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.listReply), nil
}

func (f *fakeEpics) AnswerEpic(
	_ context.Context,
	req *connect.Request[v1.AnswerEpicRequest],
) (*connect.Response[v1.AnswerEpicResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, req.Msg)
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.answerReply), nil
}

func (f *fakeEpics) PauseEpic(
	_ context.Context,
	req *connect.Request[v1.PauseEpicRequest],
) (*connect.Response[v1.PauseEpicResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pauses = append(f.pauses, req.Msg)
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.pauseReply), nil
}

func (f *fakeEpics) ResumeEpic(
	_ context.Context,
	req *connect.Request[v1.ResumeEpicRequest],
) (*connect.Response[v1.ResumeEpicResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumes = append(f.resumes, req.Msg)
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.resumeReply), nil
}

func (f *fakeEpics) StopEpic(
	_ context.Context,
	req *connect.Request[v1.StopEpicRequest],
) (*connect.Response[v1.StopEpicResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, req.Msg)
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.stopReply), nil
}

func (f *fakeEpics) RetryEpic(
	_ context.Context,
	req *connect.Request[v1.RetryEpicRequest],
) (*connect.Response[v1.RetryEpicResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.retries = append(f.retries, req.Msg)
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.retryReply), nil
}

// epicCalls is a copy of the requests the fake received.
type epicCalls struct {
	creates []*v1.CreateEpicRequest
	gets    []*v1.GetEpicRequest
	lists   []*v1.ListEpicsRequest
	answers []*v1.AnswerEpicRequest
	pauses  []*v1.PauseEpicRequest
	resumes []*v1.ResumeEpicRequest
	stops   []*v1.StopEpicRequest
	retries []*v1.RetryEpicRequest
}

func (f *fakeEpics) calls() epicCalls {
	f.mu.Lock()
	defer f.mu.Unlock()
	return epicCalls{
		creates: slices.Clone(f.creates),
		gets:    slices.Clone(f.gets),
		lists:   slices.Clone(f.lists),
		answers: slices.Clone(f.answers),
		pauses:  slices.Clone(f.pauses),
		resumes: slices.Clone(f.resumes),
		stops:   slices.Clone(f.stops),
		retries: slices.Clone(f.retries),
	}
}

func (c epicCalls) total() int {
	return len(c.creates) + len(c.gets) + len(c.lists) + len(c.answers) +
		len(c.pauses) + len(c.resumes) + len(c.stops) + len(c.retries)
}

// fullContract is an epic contract with every field set, each to a value distinct from the others.
func fullContract() *v1.EpicContract {
	return &v1.EpicContract{
		Id:         testEpicID,
		Repository: "/work/demo",
		Title:      "Login with passkeys",
		Approved:   "Yes, build passkey login and keep password login.",
		Intent:     "Let members sign in with a passkey.",
		Failures: []*v1.EpicFailure{{
			Id:       "F1",
			Text:     "The stored contract differed from what the Director sent.",
			Rating:   "serious",
			Response: "Round-trip every field in a test.",
		}},
		Care: "serious",
		Additions: []*v1.EpicAddition{{
			Location: "F1",
			What:     "A rollback switch.",
			Why:      "Passkeys are new to the members.",
			Cost:     "One flag.",
		}},
		Requirements: []*v1.EpicRequirement{{
			Id:       "R1",
			Text:     "A contract reaches the service with every field.",
			Evidence: "The round-trip test passes.",
			Kind:     "check",
			Item: &v1.EvidenceItem{
				Capture:    []string{"scripts/roundtrip", "--all"},
				Limit:      60,
				Artifacts:  []string{"out/roundtrip.log"},
				Supplied:   []string{"docs/roundtrip.md"},
				Conclusion: "Every contract field reaches the service.",
				Judge:      "reviewer",
			},
		}},
		Premises: []*v1.EpicPremise{{
			Id:      "P1",
			Text:    "Members have a device that holds a passkey.",
			IfWrong: "Password login still works.",
		}},
		Exclusions: []string{"Account recovery by email."},
		Approach:   "Store the credential beside the account.",
		Alternatives: []*v1.EpicAlternative{{
			Alternative: "Magic links.",
			WhyNot:      "Members asked for passkeys.",
			Reconsider:  "If passkeys fail on the older devices.",
		}},
		ReleaseSteps: []*v1.EpicReleaseStep{{
			Step:   "Deploy the server.",
			Target: "production",
			Undo:   "Redeploy the previous image.",
		}},
		ReleaseThen: "Tell the members.",
		Checks:      "just check",
		Base:        "0123456789abcdef0123456789abcdef01234567",
		Gates: []*v1.EpicGate{{
			Name:    "check",
			Argv:    []string{"scripts/check", "--fast"},
			Limit:   600,
			Needs:   []*v1.EpicGateNeed{{Pool: "gate", Count: 1}},
			Agent:   "reviewer",
			Must:    []string{"The change holds."},
			MustNot: []string{"The change breaks a test."},
		}},
		DoneGates:   []string{"check"},
		ReviewGates: []string{"floor"},
	}
}

// fullCreateRequest is a create request whose contract and seed decisions set every field.
func fullCreateRequest() *v1.CreateEpicRequest {
	return &v1.CreateEpicRequest{
		Contract: fullContract(),
		Decisions: []*v1.EpicDecisionInput{
			{Id: "D1", Text: "Build passkey login."},
			{Id: "D2", Text: "Keep password login."},
		},
	}
}

// storedEpicOf is the stored epic the service returns for contract, with its seed decisions recorded.
func storedEpicOf(contract *v1.EpicContract) *v1.StoredEpic {
	return &v1.StoredEpic{
		Contract: contract,
		Phase:    "accepted",
		Created:  1700000000,
		Changed:  1700000000,
		Decisions: []*v1.EpicDecision{
			{Seq: 1, Id: "D1", Text: "Build passkey login.", Time: 1700000000},
			{Seq: 2, Id: "D2", Text: "Keep password login.", Time: 1700000000},
		},
	}
}

// assertFullyPopulated fails for every field of msg, and of each message inside it, that is unset. A field the
// round trip leaves unset would pass even if the tool dropped it, so the test's fixture must set them all.
func assertFullyPopulated(t *testing.T, msg protoreflect.Message, path string) {
	t.Helper()
	fields := msg.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		name := path + "." + string(fd.Name())
		switch {
		case fd.IsList():
			list := msg.Get(fd).List()
			if list.Len() == 0 {
				t.Errorf("fixture %s is empty", name)
				continue
			}
			if fd.Message() != nil {
				for j := range list.Len() {
					assertFullyPopulated(t, list.Get(j).Message(), fmt.Sprintf("%s[%d]", name, j))
				}
			}
		case fd.Message() != nil:
			if !msg.Has(fd) {
				t.Errorf("fixture %s is unset", name)
				continue
			}
			assertFullyPopulated(t, msg.Get(fd).Message(), name)
		default:
			if !msg.Has(fd) {
				t.Errorf("fixture %s is zero", name)
			}
		}
	}
}

// marshalProto encodes msg as the protobuf JSON an agent sends as tool arguments.
func marshalProto(t *testing.T, msg proto.Message) string {
	t.Helper()
	text, err := protojson.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal %s: %v", msg.ProtoReflect().Descriptor().FullName(), err)
	}
	return string(text)
}

// unmarshalResult decodes a tool result's text as protobuf JSON into msg.
func unmarshalResult(t *testing.T, text string, msg proto.Message) {
	t.Helper()
	if err := protojson.Unmarshal([]byte(text), msg); err != nil {
		t.Fatalf("result %q is not a %s: %v", text, msg.ProtoReflect().Descriptor().FullName(), err)
	}
}

func TestToolsListNamesTheEightEpicTools(t *testing.T) {
	p := newPeer(t, nil, &fakeEpics{})

	reply := p.call("tools/list", map[string]any{})
	var listed struct {
		Tools []struct {
			Name        string         `json:"name"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(reply.Result, &listed); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	schemas := map[string]map[string]any{}
	for _, tool := range listed.Tools {
		schemas[tool.Name] = tool.InputSchema
	}
	names := []string{
		"epic_create", "epic_get", "epic_list", "epic_answer", "epic_pause", "epic_resume", "epic_stop", "epic_retry",
	}
	for _, name := range names {
		schema, ok := schemas[name]
		if !ok {
			t.Errorf("tools/list does not name %s", name)
			continue
		}
		if schema["type"] != "object" || schema["additionalProperties"] != false {
			t.Errorf("%s schema = %v, want type object and additionalProperties false", name, schema)
		}
	}

	answer := schemas["epic_answer"]
	properties, _ := answer["properties"].(map[string]any)
	question, _ := properties["question"].(map[string]any)
	required, _ := answer["required"].([]any)
	if question["type"] != "string" {
		t.Errorf("epic_answer question schema = %v, want type string", question)
	}
	if slices.Contains(required, any("question")) {
		t.Errorf("epic_answer requires question = %v, want it optional", required)
	}
}

func TestEpicCreateSendsEveryContractFieldAndSeedDecision(t *testing.T) {
	want := fullCreateRequest()
	assertFullyPopulated(t, want.ProtoReflect(), "CreateEpicRequest")
	fake := &fakeEpics{createReply: &v1.CreateEpicResponse{Epic: storedEpicOf(fullContract())}}
	p := newPeer(t, nil, fake)

	outcome := p.tool("epic_create", marshalProto(t, want))

	if outcome.isError {
		t.Fatalf("epic_create returned an error: %s", outcome.text)
	}
	calls := fake.calls().creates
	if len(calls) != 1 {
		t.Fatalf("CreateEpic calls = %d, want 1", len(calls))
	}
	if !proto.Equal(calls[0], want) {
		t.Fatalf("CreateEpic request = %s, want %s", marshalProto(t, calls[0]), marshalProto(t, want))
	}
	var got v1.StoredEpic
	unmarshalResult(t, outcome.text, &got)
	if !proto.Equal(&got, storedEpicOf(fullContract())) {
		t.Fatalf("epic_create result = %s, want the stored epic", outcome.text)
	}
}

func TestEpicCreateWithoutDecisionsSendsNoSeedDecisions(t *testing.T) {
	fake := &fakeEpics{createReply: &v1.CreateEpicResponse{Epic: storedEpicOf(fullContract())}}
	p := newPeer(t, nil, fake)

	contract := marshalProto(t, &v1.CreateEpicRequest{Contract: fullContract()})
	outcome := p.tool("epic_create", contract)

	if outcome.isError {
		t.Fatalf("epic_create returned an error: %s", outcome.text)
	}
	calls := fake.calls().creates
	if len(calls) != 1 || len(calls[0].GetDecisions()) != 0 {
		t.Fatalf("CreateEpic requests = %v, want one with no decisions", calls)
	}
}

func TestEpicCreateRefusalNamesTheField(t *testing.T) {
	fake := &fakeEpics{err: connect.NewError(connect.CodeInvalidArgument,
		errors.New("release_steps[0].undo: empty"))}
	p := newPeer(t, nil, fake)

	outcome := p.tool("epic_create", `{"contract":{"id":"`+testEpicID+`"}}`)

	if !outcome.isError {
		t.Fatalf("epic_create returned %q without isError", outcome.text)
	}
	want := "invalid_argument: release_steps[0].undo: empty"
	if outcome.text != want {
		t.Fatalf("epic_create error text = %q, want %q", outcome.text, want)
	}
}

func TestEpicCreateAlreadyExistsNamesTheID(t *testing.T) {
	fake := &fakeEpics{err: connect.NewError(connect.CodeAlreadyExists,
		fmt.Errorf("id: %s is already in the database", testEpicID))}
	p := newPeer(t, nil, fake)

	outcome := p.tool("epic_create", `{"contract":{"id":"`+testEpicID+`"}}`)

	if !outcome.isError {
		t.Fatalf("epic_create returned %q without isError", outcome.text)
	}
	want := "already_exists: id: " + testEpicID + " is already in the database"
	if outcome.text != want {
		t.Fatalf("epic_create error text = %q, want %q", outcome.text, want)
	}
	if !strings.Contains(outcome.text, "id:") {
		t.Fatalf("epic_create error text %q does not name the id field", outcome.text)
	}
}

func TestEpicGetSendsIDAndReturnsStoredEpic(t *testing.T) {
	fake := &fakeEpics{getReply: &v1.GetEpicResponse{Epic: storedEpicOf(fullContract())}}
	p := newPeer(t, nil, fake)

	outcome := p.tool("epic_get", `{"id":"`+testEpicID+`"}`)

	if outcome.isError {
		t.Fatalf("epic_get returned an error: %s", outcome.text)
	}
	calls := fake.calls().gets
	if len(calls) != 1 || calls[0].GetId() != testEpicID {
		t.Fatalf("GetEpic requests = %v, want one for %s", calls, testEpicID)
	}
	var got v1.StoredEpic
	unmarshalResult(t, outcome.text, &got)
	if !proto.Equal(&got, storedEpicOf(fullContract())) {
		t.Fatalf("epic_get result = %s, want the stored epic", outcome.text)
	}
}

func TestEpicListReturnsSummaries(t *testing.T) {
	listed := &v1.ListEpicsResponse{Epics: []*v1.EpicSummary{
		{Id: testEpicID, Repository: "/work/demo", Title: "Login with passkeys", Phase: "accepted",
			Created: 1700000000, Changed: 1700000100},
		{Id: "demo-0a1b2c3/epic-export", Repository: "/work/demo", Title: "Export", Phase: "released",
			Created: 1700000200, Changed: 1700000300},
	}}
	fake := &fakeEpics{listReply: listed}
	p := newPeer(t, nil, fake)

	outcome := p.tool("epic_list", `{}`)

	if outcome.isError {
		t.Fatalf("epic_list returned an error: %s", outcome.text)
	}
	if n := len(fake.calls().lists); n != 1 {
		t.Fatalf("ListEpics calls = %d, want 1", n)
	}
	var got v1.ListEpicsResponse
	unmarshalResult(t, outcome.text, &got)
	if !proto.Equal(&got, listed) {
		t.Fatalf("epic_list result = %s, want the summaries", outcome.text)
	}
}

func TestEpicListWithNoEpicsEmitsAnEmptyArray(t *testing.T) {
	p := newPeer(t, nil, &fakeEpics{listReply: &v1.ListEpicsResponse{}})

	outcome := p.tool("epic_list", `{}`)

	if outcome.isError {
		t.Fatalf("epic_list returned an error: %s", outcome.text)
	}
	object := decodeObject(t, outcome.text)
	epics, ok := object["epics"].([]any)
	if !ok || len(epics) != 0 {
		t.Fatalf("epic_list result = %s, want an explicit empty epics array", outcome.text)
	}
}

func TestEpicAnswerNoteSendsNoQuestionAndReturnsDecisionWithWarning(t *testing.T) {
	reply := &v1.AnswerEpicResponse{
		Decision: &v1.EpicDecision{Seq: 3, Id: "D3", Text: "Yes, ship it.", Time: 1700000400},
		Warning:  "questions are open: Q2",
	}
	fake := &fakeEpics{answerReply: reply}
	p := newPeer(t, nil, fake)

	outcome := p.tool("epic_answer",
		`{"epicId":"`+testEpicID+`","id":"D3","text":"Yes, ship it."}`)

	if outcome.isError {
		t.Fatalf("epic_answer returned an error: %s", outcome.text)
	}
	calls := fake.calls().answers
	want := &v1.AnswerEpicRequest{EpicId: testEpicID, Id: "D3", Text: "Yes, ship it."}
	if len(calls) != 1 || !proto.Equal(calls[0], want) {
		t.Fatalf("AnswerEpic requests = %v, want %v", calls, want)
	}
	var got v1.AnswerEpicResponse
	unmarshalResult(t, outcome.text, &got)
	if !proto.Equal(&got, reply) {
		t.Fatalf("epic_answer result = %s, want the decision and warning", outcome.text)
	}
}

func TestEpicAnswerSendsQuestionAndReturnsDecision(t *testing.T) {
	reply := &v1.AnswerEpicResponse{
		Decision: &v1.EpicDecision{Seq: 3, Id: "D3", Text: "Yes, ship it.", Time: 1700000400, Question: "Q2"},
	}
	fake := &fakeEpics{answerReply: reply}
	p := newPeer(t, nil, fake)

	outcome := p.tool("epic_answer",
		`{"epicId":"`+testEpicID+`","id":"D3","text":"Yes, ship it.","question":"Q2"}`)

	if outcome.isError {
		t.Fatalf("epic_answer returned an error: %s", outcome.text)
	}
	calls := fake.calls().answers
	want := &v1.AnswerEpicRequest{EpicId: testEpicID, Id: "D3", Text: "Yes, ship it.", Question: "Q2"}
	if len(calls) != 1 || !proto.Equal(calls[0], want) {
		t.Fatalf("AnswerEpic requests = %v, want %v", calls, want)
	}
	var got v1.AnswerEpicResponse
	unmarshalResult(t, outcome.text, &got)
	if !proto.Equal(&got, reply) {
		t.Fatalf("epic_answer result = %s, want the decision", outcome.text)
	}
}

func TestEpicAnswerRefusalNamesTheField(t *testing.T) {
	fake := &fakeEpics{err: connect.NewError(connect.CodeAlreadyExists,
		errors.New("id: D3 was recorded with other text"))}
	p := newPeer(t, nil, fake)

	outcome := p.tool("epic_answer", `{"epicId":"`+testEpicID+`","id":"D3","text":"No."}`)

	if !outcome.isError {
		t.Fatalf("epic_answer returned %q without isError", outcome.text)
	}
	want := "already_exists: id: D3 was recorded with other text"
	if outcome.text != want {
		t.Fatalf("epic_answer error text = %q, want %q", outcome.text, want)
	}
}

func TestEpicToolsRefuseUnknownKeysBeforeAnyCall(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args string
	}{
		{
			name: "create unknown contract key",
			tool: "epic_create",
			args: `{"contract":{"id":"` + testEpicID + `","bogus":"x"}}`,
		},
		{
			name: "create unknown top-level key",
			tool: "epic_create",
			args: `{"contract":{"id":"` + testEpicID + `"},"bogus":1}`,
		},
		{
			name: "create unknown decision key",
			tool: "epic_create",
			args: `{"contract":{"id":"` + testEpicID + `"},"decisions":[{"id":"D1","text":"t","bogus":1}]}`,
		},
		{name: "get unknown key", tool: "epic_get", args: `{"id":"` + testEpicID + `","bogus":1}`},
		{name: "list unknown key", tool: "epic_list", args: `{"bogus":1}`},
		{
			name: "answer unknown key",
			tool: "epic_answer",
			args: `{"epicId":"` + testEpicID + `","id":"D1","text":"t","bogus":1}`,
		},
		{name: "pause unknown key", tool: "epic_pause", args: `{"id":"` + testEpicID + `","bogus":1}`},
		{name: "resume unknown key", tool: "epic_resume", args: `{"id":"` + testEpicID + `","bogus":1}`},
		{name: "stop unknown key", tool: "epic_stop", args: `{"id":"` + testEpicID + `","bogus":1}`},
		{name: "retry unknown key", tool: "epic_retry", args: `{"id":"` + testEpicID + `","bogus":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeEpics{}
			p := newPeer(t, nil, fake)

			outcome := p.tool(tc.tool, tc.args)

			if !outcome.isError {
				t.Fatalf("%s returned %q without isError", tc.tool, outcome.text)
			}
			if !strings.Contains(outcome.text, "unknown field") || !strings.Contains(outcome.text, "bogus") {
				t.Fatalf("%s error text = %q, want it to name the unknown field", tc.tool, outcome.text)
			}
			if n := fake.calls().total(); n != 0 {
				t.Fatalf("%s reached the service %d times, want none", tc.tool, n)
			}
		})
	}
}

// requestIDs returns the id of each control request in reqs.
func requestIDs[R interface{ GetId() string }](reqs []R) []string {
	ids := make([]string, 0, len(reqs))
	for _, req := range reqs {
		ids = append(ids, req.GetId())
	}
	return ids
}

func TestEpicControlSendsOneCallAndReturnsTheEpic(t *testing.T) {
	cases := []struct {
		tool string
		ids  func(epicCalls) []string
	}{
		{tool: "epic_pause", ids: func(c epicCalls) []string { return requestIDs(c.pauses) }},
		{tool: "epic_resume", ids: func(c epicCalls) []string { return requestIDs(c.resumes) }},
		{tool: "epic_stop", ids: func(c epicCalls) []string { return requestIDs(c.stops) }},
		{tool: "epic_retry", ids: func(c epicCalls) []string { return requestIDs(c.retries) }},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			stored := storedEpicOf(fullContract())
			fake := &fakeEpics{
				pauseReply:  &v1.PauseEpicResponse{Epic: stored},
				resumeReply: &v1.ResumeEpicResponse{Epic: stored},
				stopReply:   &v1.StopEpicResponse{Epic: stored},
				retryReply:  &v1.RetryEpicResponse{Epic: stored},
			}
			p := newPeer(t, nil, fake)

			outcome := p.tool(tc.tool, `{"id":"`+testEpicID+`"}`)

			if outcome.isError {
				t.Fatalf("%s returned an error: %s", tc.tool, outcome.text)
			}
			calls := fake.calls()
			if n := calls.total(); n != 1 {
				t.Fatalf("%s made %d service calls, want 1", tc.tool, n)
			}
			if ids := tc.ids(calls); !slices.Equal(ids, []string{testEpicID}) {
				t.Fatalf("%s request ids = %v, want [%s]", tc.tool, ids, testEpicID)
			}
			var got v1.StoredEpic
			unmarshalResult(t, outcome.text, &got)
			if !proto.Equal(&got, stored) {
				t.Fatalf("%s result = %s, want the stored epic", tc.tool, outcome.text)
			}
		})
	}
}

func TestEpicControlRefusalIsAnErrorCarryingItsText(t *testing.T) {
	cases := []struct {
		tool    string
		refusal string
	}{
		{tool: "epic_pause", refusal: "epic " + testEpicID + " is already paused"},
		{tool: "epic_resume", refusal: "epic " + testEpicID + " is not paused"},
		{tool: "epic_stop", refusal: "epic " + testEpicID + " has no running turn"},
		{tool: "epic_retry", refusal: "epic " + testEpicID + " is not parked"},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			fake := &fakeEpics{err: connect.NewError(connect.CodeFailedPrecondition, errors.New(tc.refusal))}
			p := newPeer(t, nil, fake)

			outcome := p.tool(tc.tool, `{"id":"`+testEpicID+`"}`)

			if !outcome.isError {
				t.Fatalf("%s returned %q without isError", tc.tool, outcome.text)
			}
			want := "failed_precondition: " + tc.refusal
			if outcome.text != want {
				t.Fatalf("%s error text = %q, want %q", tc.tool, outcome.text, want)
			}
			if n := fake.calls().total(); n != 1 {
				t.Fatalf("%s made %d service calls, want 1", tc.tool, n)
			}
		})
	}
}

func TestCallEpicToolRunsTheNamedTool(t *testing.T) {
	stored := storedEpicOf(fullContract())
	fake := &fakeEpics{pauseReply: &v1.PauseEpicResponse{Epic: stored}}

	result, err := CallEpicTool(context.Background(), fake, "epic_pause",
		json.RawMessage(`{"id":"`+testEpicID+`"}`))

	if err != nil {
		t.Fatalf("CallEpicTool(epic_pause) returned error: %v", err)
	}
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("CallEpicTool(epic_pause) = %+v, want one content block without error", result)
	}
	if ids := requestIDs(fake.calls().pauses); !slices.Equal(ids, []string{testEpicID}) {
		t.Fatalf("PauseEpic request ids = %v, want [%s]", ids, testEpicID)
	}
	var got v1.StoredEpic
	unmarshalResult(t, result.Content[0].Text, &got)
	if !proto.Equal(&got, stored) {
		t.Fatalf("CallEpicTool(epic_pause) text = %s, want the stored epic", result.Content[0].Text)
	}
}

func TestCallEpicToolRefusesAnUnknownName(t *testing.T) {
	fake := &fakeEpics{}

	_, err := CallEpicTool(context.Background(), fake, "step_status", json.RawMessage(`{"id":"s1"}`))

	if err == nil {
		t.Fatal("CallEpicTool(step_status) returned no error, want an unknown tool")
	}
	if n := fake.calls().total(); n != 0 {
		t.Fatalf("CallEpicTool(step_status) reached the service %d times, want none", n)
	}
}
