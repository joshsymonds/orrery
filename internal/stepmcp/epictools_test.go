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
	mu           sync.Mutex
	creates      []*v1.CreateEpicRequest
	gets         []*v1.GetEpicRequest
	lists        []*v1.ListEpicsRequest
	answers      []*v1.AnswerEpicRequest
	pauses       []*v1.PauseEpicRequest
	moves        []*v1.MoveEpicRequest
	resumes      []*v1.ResumeEpicRequest
	stops        []*v1.StopEpicRequest
	retries      []*v1.RetryEpicRequest
	kicks        []*v1.KickEpicRequest
	releases     []*v1.ReleaseEpicRequest
	closes       []*v1.CloseEpicRequest
	amends       []*v1.AmendEvidenceRequest
	follows      []*v1.FollowEpicsRequest
	createReply  *v1.CreateEpicResponse
	getReply     *v1.GetEpicResponse
	listReply    *v1.ListEpicsResponse
	answerReply  *v1.AnswerEpicResponse
	pauseReply   *v1.PauseEpicResponse
	moveReply    *v1.MoveEpicResponse
	resumeReply  *v1.ResumeEpicResponse
	stopReply    *v1.StopEpicResponse
	retryReply   *v1.RetryEpicResponse
	kickReply    *v1.KickEpicResponse
	releaseReply *v1.ReleaseEpicResponse
	closeReply   *v1.CloseEpicResponse
	amendReply   *v1.AmendEvidenceResponse
	followReply  *v1.FollowEpicsResponse
	err          error
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

func (f *fakeEpics) MoveEpic(
	_ context.Context,
	req *connect.Request[v1.MoveEpicRequest],
) (*connect.Response[v1.MoveEpicResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.moves = append(f.moves, req.Msg)
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.moveReply), nil
}

func (f *fakeEpics) ContinueEpic(
	_ context.Context,
	_ *connect.Request[v1.ContinueEpicRequest],
) (*connect.Response[v1.ContinueEpicResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("no epic tool continues an epic"))
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

func (f *fakeEpics) CloseEpic(
	_ context.Context,
	req *connect.Request[v1.CloseEpicRequest],
) (*connect.Response[v1.CloseEpicResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes = append(f.closes, req.Msg)
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.closeReply), nil
}

func (f *fakeEpics) AmendEvidence(
	_ context.Context,
	req *connect.Request[v1.AmendEvidenceRequest],
) (*connect.Response[v1.AmendEvidenceResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.amends = append(f.amends, req.Msg)
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.amendReply), nil
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

func (f *fakeEpics) KickEpic(
	_ context.Context,
	req *connect.Request[v1.KickEpicRequest],
) (*connect.Response[v1.KickEpicResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kicks = append(f.kicks, req.Msg)
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.kickReply), nil
}

func (f *fakeEpics) FollowEpics(
	_ context.Context,
	req *connect.Request[v1.FollowEpicsRequest],
) (*connect.Response[v1.FollowEpicsResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.follows = append(f.follows, req.Msg)
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.followReply), nil
}

func (f *fakeEpics) ReleaseEpic(
	_ context.Context,
	req *connect.Request[v1.ReleaseEpicRequest],
) (*connect.Response[v1.ReleaseEpicResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases = append(f.releases, req.Msg)
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.releaseReply), nil
}

func (f *fakeEpics) SeeEpic(
	_ context.Context,
	_ *connect.Request[v1.SeeEpicRequest],
) (*connect.Response[v1.SeeEpicResponse], error) {
	return connect.NewResponse(&v1.SeeEpicResponse{}), nil
}

func (f *fakeEpics) ArchiveRow(
	_ context.Context,
	_ *connect.Request[v1.ArchiveRowRequest],
) (*connect.Response[v1.ArchiveRowResponse], error) {
	return connect.NewResponse(&v1.ArchiveRowResponse{}), nil
}

func (f *fakeEpics) UnarchiveRow(
	_ context.Context,
	_ *connect.Request[v1.UnarchiveRowRequest],
) (*connect.Response[v1.UnarchiveRowResponse], error) {
	return connect.NewResponse(&v1.UnarchiveRowResponse{}), nil
}

// epicCalls is a copy of the requests the fake received.
type epicCalls struct {
	creates  []*v1.CreateEpicRequest
	gets     []*v1.GetEpicRequest
	lists    []*v1.ListEpicsRequest
	answers  []*v1.AnswerEpicRequest
	pauses   []*v1.PauseEpicRequest
	moves    []*v1.MoveEpicRequest
	resumes  []*v1.ResumeEpicRequest
	stops    []*v1.StopEpicRequest
	retries  []*v1.RetryEpicRequest
	kicks    []*v1.KickEpicRequest
	releases []*v1.ReleaseEpicRequest
	closes   []*v1.CloseEpicRequest
	follows  []*v1.FollowEpicsRequest
}

func (f *fakeEpics) calls() epicCalls {
	f.mu.Lock()
	defer f.mu.Unlock()
	return epicCalls{
		creates:  slices.Clone(f.creates),
		gets:     slices.Clone(f.gets),
		lists:    slices.Clone(f.lists),
		answers:  slices.Clone(f.answers),
		pauses:   slices.Clone(f.pauses),
		moves:    slices.Clone(f.moves),
		resumes:  slices.Clone(f.resumes),
		stops:    slices.Clone(f.stops),
		retries:  slices.Clone(f.retries),
		kicks:    slices.Clone(f.kicks),
		releases: slices.Clone(f.releases),
		closes:   slices.Clone(f.closes),
		follows:  slices.Clone(f.follows),
	}
}

func (c epicCalls) total() int {
	return len(c.creates) + len(c.gets) + len(c.lists) + len(c.answers) +
		len(c.pauses) + len(c.moves) + len(c.resumes) + len(c.stops) + len(c.retries) + len(c.kicks) + len(c.releases) +
		len(c.follows) + len(c.closes)
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
				Kind:       "test",
				Tests:      []string{"internal/roundtrip/roundtrip_test.go"},
			},
			Owner:        "T1",
			Contributors: []string{"T2"},
			State:        "pending",
			Disagreement: "Red was judged fails, but its reproduction exited 0.",
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
		Reviewers: []*v1.EpicReviewer{{
			Name:    "money_rail_check",
			Enabled: proto.Bool(true),
			Stage:   []string{"task"},
			Must:    []string{"The money rail holds."},
			MustNot: []string{"The change moves money twice."},
			Prompt:  proto.String("Check the money rail."),
			Agent:   proto.String("rail-reviewer"),
			Model:   proto.String("sonnet"),
			Tools:   []string{"Read", "Grep"},
			Effort:  proto.String("high"),
			Limit:   proto.Int64(900),
			Verify:  proto.Bool(false),
		}},
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
		Workflow: "lookdev",
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

func TestToolsListNamesTheTenEpicTools(t *testing.T) {
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
		"epic_create", "epic_get", "epic_list", "epic_answer", "epic_follow", "epic_pause", "epic_resume",
		"epic_stop", "epic_retry", "epic_release", "epic_close",
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
	wantText := "epic " + testEpicID + ": phase accepted\ncreated with 1 requirements and 2 decisions"
	if outcome.text != wantText {
		t.Fatalf("epic_create text = %q, want %q", outcome.text, wantText)
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

func TestEpicCreatePrintsEachWarningLine(t *testing.T) {
	warnings := []string{
		"requirements[0].item.capture: -run pattern \"X\" matches TestX in ./pkg at the base: name a new top-level test",
		"release_steps[1].step: says merge: a release step fast-forwards main to the release candidate; write fast-forward",
	}
	fake := &fakeEpics{createReply: &v1.CreateEpicResponse{Epic: storedEpicOf(fullContract()), Warnings: warnings}}
	p := newPeer(t, nil, fake)

	outcome := p.tool("epic_create", `{"contract":{"id":"`+testEpicID+`"}}`)

	if outcome.isError {
		t.Fatalf("epic_create returned an error: %s", outcome.text)
	}
	wantText := "epic " + testEpicID + ": phase accepted\ncreated with 1 requirements and 2 decisions\n" +
		"warning: " + warnings[0] + "\nwarning: " + warnings[1]
	if outcome.text != wantText {
		t.Fatalf("epic_create text = %q, want %q", outcome.text, wantText)
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
	if len(calls) != 1 || calls[0].GetId() != testEpicID || !calls[0].GetHistory() {
		t.Fatalf("GetEpic requests = %v, want one for %s with history", calls, testEpicID)
	}
	wantHead := "epic demo-0a1b2c3/epic-login: Login with passkeys\nphase: accepted\npaused: no\n" +
		"open questions: 0\ntasks: 0\nparts:\n"
	if !strings.HasPrefix(outcome.text, wantHead) {
		t.Fatalf("epic_get text = %q, want a summary that starts %q", outcome.text, wantHead)
	}
	if !strings.Contains(outcome.text, "\n  decisions: 2 items, ") {
		t.Fatalf("epic_get text = %q, want the decisions part with two items", outcome.text)
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
	fake := &fakeEpics{answerReply: reply, getReply: &v1.GetEpicResponse{Epic: storedEpicOf(fullContract())}}
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
	wantText := "epic " + testEpicID + ": phase accepted\nrecorded note D3\nwarning: questions are open: Q2"
	if outcome.text != wantText {
		t.Fatalf("epic_answer text = %q, want %q", outcome.text, wantText)
	}
}

func TestEpicAnswerSendsQuestionAndReturnsDecision(t *testing.T) {
	reply := &v1.AnswerEpicResponse{
		Decision: &v1.EpicDecision{Seq: 3, Id: "D3", Text: "Yes, ship it.", Time: 1700000400, Question: "Q2"},
	}
	fake := &fakeEpics{answerReply: reply, getReply: &v1.GetEpicResponse{Epic: storedEpicOf(fullContract())}}
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
	// The request names no choice, which the service refuses but the fake accepts, so the line gives the empty choice.
	wantText := "epic " + testEpicID + ": phase accepted\nrecorded decision D3 answering Q2 with "
	if outcome.text != wantText {
		t.Fatalf("epic_answer text = %q, want %q", outcome.text, wantText)
	}
}

// TestEpicAnswerSendsChoiceAsGiven checks that epic_answer sends the choice an answer names, and sends text only when
// the answer gives it.
func TestEpicAnswerSendsChoiceAsGiven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args string
		want *v1.AnswerEpicRequest
	}{
		{
			name: "declared choice without text",
			args: `{"epicId":"` + testEpicID + `","id":"D3","question":"Q2","choice":"retry"}`,
			want: &v1.AnswerEpicRequest{EpicId: testEpicID, Id: "D3", Question: "Q2", Choice: "retry"},
		},
		{
			name: "other with text",
			args: `{"epicId":"` + testEpicID + `","id":"D3","question":"Q2","choice":"other","text":"Use OIDC."}`,
			want: &v1.AnswerEpicRequest{EpicId: testEpicID, Id: "D3", Question: "Q2", Choice: "other",
				Text: "Use OIDC."},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reply := &v1.AnswerEpicResponse{
				Decision: &v1.EpicDecision{Seq: 3, Id: "D3", Question: "Q2"},
			}
			fake := &fakeEpics{answerReply: reply}
			p := newPeer(t, nil, fake)

			outcome := p.tool("epic_answer", tc.args)

			if outcome.isError {
				t.Fatalf("epic_answer returned an error: %s", outcome.text)
			}
			calls := fake.calls().answers
			if len(calls) != 1 || !proto.Equal(calls[0], tc.want) {
				t.Fatalf("AnswerEpic requests = %v, want %v", calls, tc.want)
			}
		})
	}
}

// TestEpicAnswerRefusedChoiceIsErrorText checks that a choice the question does not offer, and other with no text,
// come back as the tool's error text with the service's refusal.
func TestEpicAnswerRefusedChoiceIsErrorText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		args    string
		refusal error
		want    string
	}{
		{
			name: "choice the question lacks",
			args: `{"epicId":"` + testEpicID + `","id":"D3","question":"Q2","choice":"maybe"}`,
			refusal: connect.NewError(connect.CodeInvalidArgument,
				errors.New("choice: Q2 offers no choice maybe")),
			want: "invalid_argument: choice: Q2 offers no choice maybe",
		},
		{
			name: "other with no text",
			args: `{"epicId":"` + testEpicID + `","id":"D3","question":"Q2","choice":"other"}`,
			refusal: connect.NewError(connect.CodeInvalidArgument,
				errors.New("text: empty")),
			want: "invalid_argument: text: empty",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeEpics{err: tc.refusal}
			p := newPeer(t, nil, fake)

			outcome := p.tool("epic_answer", tc.args)

			if !outcome.isError {
				t.Fatalf("epic_answer returned %q without isError", outcome.text)
			}
			if outcome.text != tc.want {
				t.Fatalf("epic_answer error text = %q, want %q", outcome.text, tc.want)
			}
		})
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

func TestEpicFollowReturnsTheCounts(t *testing.T) {
	t.Parallel()
	reply := &v1.FollowEpicsResponse{Follows: []*v1.EpicFollow{
		{Label: "retry-policy", Answered: 4, Followed: 3},
		{Label: "scope", Answered: 2, Followed: 0},
	}}
	fake := &fakeEpics{followReply: reply}
	p := newPeer(t, nil, fake)

	outcome := p.tool("epic_follow", `{}`)

	if outcome.isError {
		t.Fatalf("epic_follow returned an error: %s", outcome.text)
	}
	calls := fake.calls()
	if n := calls.total(); n != 1 || len(calls.follows) != 1 {
		t.Fatalf("epic_follow made %d service calls, want one FollowEpics", n)
	}
	var got v1.FollowEpicsResponse
	unmarshalResult(t, outcome.text, &got)
	if !proto.Equal(&got, reply) {
		t.Fatalf("epic_follow result = %s, want the counts", outcome.text)
	}
}

func TestEpicFollowWithNoCountsEmitsAnEmptyArray(t *testing.T) {
	t.Parallel()
	p := newPeer(t, nil, &fakeEpics{followReply: &v1.FollowEpicsResponse{}})

	outcome := p.tool("epic_follow", `{}`)

	if outcome.isError {
		t.Fatalf("epic_follow returned an error: %s", outcome.text)
	}
	object := decodeObject(t, outcome.text)
	follows, ok := object["follows"].([]any)
	if !ok || len(follows) != 0 {
		t.Fatalf("epic_follow result = %s, want an explicit empty follows array", outcome.text)
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
		{name: "follow unknown key", tool: "epic_follow", args: `{"bogus":1}`},
		{
			name: "answer unknown key",
			tool: "epic_answer",
			args: `{"epicId":"` + testEpicID + `","id":"D1","text":"t","bogus":1}`,
		},
		{name: "pause unknown key", tool: "epic_pause", args: `{"id":"` + testEpicID + `","bogus":1}`},
		{name: "resume unknown key", tool: "epic_resume", args: `{"id":"` + testEpicID + `","bogus":1}`},
		{name: "stop unknown key", tool: "epic_stop", args: `{"id":"` + testEpicID + `","bogus":1}`},
		{name: "retry unknown key", tool: "epic_retry", args: `{"id":"` + testEpicID + `","bogus":1}`},
		{name: "release unknown key", tool: "epic_release", args: `{"id":"` + testEpicID + `","bogus":1}`},
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
		want string
	}{
		{
			tool: "epic_pause", ids: func(c epicCalls) []string { return requestIDs(c.pauses) },
			want: "epic " + testEpicID + ": phase accepted\npaused",
		},
		{
			tool: "epic_stop", ids: func(c epicCalls) []string { return requestIDs(c.stops) },
			want: "epic " + testEpicID + ": phase accepted\nstopped its release and freed the release slot",
		},
		{
			tool: "epic_retry", ids: func(c epicCalls) []string { return requestIDs(c.retries) },
			want: "epic " + testEpicID + ": phase accepted\nended the park",
		},
		{
			tool: "epic_release", ids: func(c epicCalls) []string { return requestIDs(c.releases) },
			want: "epic " + testEpicID + ": phase accepted\nrelease started",
		},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			stored := storedEpicOf(fullContract())
			fake := &fakeEpics{
				pauseReply:   &v1.PauseEpicResponse{Epic: stored},
				stopReply:    &v1.StopEpicResponse{Epic: stored},
				retryReply:   &v1.RetryEpicResponse{Epic: stored},
				releaseReply: &v1.ReleaseEpicResponse{Epic: stored},
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
			if outcome.text != tc.want {
				t.Fatalf("%s text = %q, want %q", tc.tool, outcome.text, tc.want)
			}
		})
	}
}

// TestEpicRetryPassesTheNodeInOneCall checks that epic_retry sends one RetryEpic call carrying the node it names,
// and an empty node when it names none.
func TestEpicRetryPassesTheNodeInOneCall(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args string
		node string
	}{
		{name: "named node", args: `{"id":"` + testEpicID + `","node":"check"}`, node: "check"},
		{name: "no node", args: `{"id":"` + testEpicID + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stored := storedEpicOf(fullContract())
			fake := &fakeEpics{retryReply: &v1.RetryEpicResponse{Epic: stored}}
			p := newPeer(t, nil, fake)

			outcome := p.tool("epic_retry", tc.args)

			if outcome.isError {
				t.Fatalf("epic_retry returned an error: %s", outcome.text)
			}
			calls := fake.calls()
			if n := calls.total(); n != 1 {
				t.Fatalf("epic_retry made %d service calls, want 1", n)
			}
			if len(calls.retries) != 1 {
				t.Fatalf("epic_retry made %d RetryEpic calls, want 1", len(calls.retries))
			}
			if got := calls.retries[0]; got.GetId() != testEpicID || got.GetNode() != tc.node {
				t.Fatalf("RetryEpic request = id %q node %q, want id %q node %q",
					got.GetId(), got.GetNode(), testEpicID, tc.node)
			}
		})
	}
}

// TestEpicReleasePassesTheIDAndPositionsInOneCall checks that epic_release sends one ReleaseEpic call carrying the id
// and the undo-none positions it names, and an empty list when it names none.
func TestEpicReleasePassesTheIDAndPositionsInOneCall(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		args     string
		undoNone []int32
		want     string
	}{
		{
			name: "named positions", args: `{"id":"` + testEpicID + `","undoNone":[2,3]}`, undoNone: []int32{2, 3},
			want: "epic " + testEpicID + ": phase accepted\nrelease started; undo-none steps 2, 3 run again",
		},
		{
			name: "no positions", args: `{"id":"` + testEpicID + `"}`,
			want: "epic " + testEpicID + ": phase accepted\nrelease started",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stored := storedEpicOf(fullContract())
			fake := &fakeEpics{releaseReply: &v1.ReleaseEpicResponse{Epic: stored}}
			p := newPeer(t, nil, fake)

			outcome := p.tool("epic_release", tc.args)

			if outcome.isError {
				t.Fatalf("epic_release returned an error: %s", outcome.text)
			}
			calls := fake.calls()
			if n := calls.total(); n != 1 {
				t.Fatalf("epic_release made %d service calls, want 1", n)
			}
			if len(calls.releases) != 1 {
				t.Fatalf("epic_release made %d ReleaseEpic calls, want 1", len(calls.releases))
			}
			if got := calls.releases[0]; got.GetId() != testEpicID || !slices.Equal(got.GetUndoNone(), tc.undoNone) {
				t.Fatalf("ReleaseEpic request = id %q undo_none %v, want id %q undo_none %v",
					got.GetId(), got.GetUndoNone(), testEpicID, tc.undoNone)
			}
			if outcome.text != tc.want {
				t.Fatalf("epic_release text = %q, want %q", outcome.text, tc.want)
			}
		})
	}
}

func TestEpicResumeResultCarriesEpicResumedAndStillLimited(t *testing.T) {
	t.Parallel()
	stored := storedEpicOf(fullContract())
	cases := []struct {
		name         string
		resumed      []string
		stillLimited []string
		want         string
	}{
		{
			name: "every limited step resumed", resumed: []string{"step-a", "step-b"},
			want: "epic " + testEpicID + ": phase accepted\n" +
				"resumed 2 limited steps: step-a, step-b; 0 left limited by the step cap",
		},
		{
			name: "one left limited by the cap", resumed: []string{"step-b"}, stillLimited: []string{"step-a"},
			want: "epic " + testEpicID + ": phase accepted\n" +
				"resumed 1 limited steps: step-b; 1 left limited by the step cap: step-a",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeEpics{resumeReply: &v1.ResumeEpicResponse{
				Epic:         stored,
				Resumed:      tc.resumed,
				StillLimited: tc.stillLimited,
			}}
			p := newPeer(t, nil, fake)

			outcome := p.tool("epic_resume", `{"id":"`+testEpicID+`"}`)

			if outcome.isError {
				t.Fatalf("epic_resume returned an error: %s", outcome.text)
			}
			if n := fake.calls().total(); n != 1 {
				t.Fatalf("epic_resume made %d service calls, want 1", n)
			}
			if outcome.text != tc.want {
				t.Fatalf("epic_resume text = %q, want %q", outcome.text, tc.want)
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
		{tool: "epic_release", refusal: "epic " + testEpicID + " is not stopped"},
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
	want := "epic " + testEpicID + ": phase accepted\npaused"
	if result.Content[0].Text != want {
		t.Fatalf("CallEpicTool(epic_pause) text = %q, want %q", result.Content[0].Text, want)
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
