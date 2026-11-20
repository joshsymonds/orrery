package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/stepmcp"
)

const (
	epicTestID     = "demo-0a1b2c3/epic-login"
	epicWarning    = "recorded as a note: it answers no question, and Q1 is open"
	epicCreateJSON = `{"contract":{"id":"demo-0a1b2c3/epic-login","title":"Login"},` +
		`"decisions":[{"id":"d1","text":"use OIDC"}]}`
	epicRefusal = "epic demo-0a1b2c3/epic-login is already paused"
)

// recordingEpics is an EpicsClient that records each request it receives, in order. It answers each call with the
// reply it is configured with, or with err when err is set.
type recordingEpics struct {
	mu       sync.Mutex
	requests []proto.Message
	err      error
	epic     *v1.StoredEpic
	answer   *v1.AnswerEpicResponse
	follow   *v1.FollowEpicsResponse
}

// newRecordingEpics returns a recordingEpics whose replies name epicTestID.
func newRecordingEpics() *recordingEpics {
	return &recordingEpics{
		epic: &v1.StoredEpic{
			Contract: &v1.EpicContract{Id: epicTestID, Title: "Login"},
			Phase:    "plan",
		},
		answer: &v1.AnswerEpicResponse{
			Decision: &v1.EpicDecision{Id: "d2", Text: "yes, OIDC", Question: "Q1"},
		},
		follow: &v1.FollowEpicsResponse{},
	}
}

// respond records req and returns reply, or the configured error.
func respond[Res any](f *recordingEpics, req proto.Message, reply *Res) (*connect.Response[Res], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(reply), nil
}

func (f *recordingEpics) CreateEpic(
	_ context.Context,
	req *connect.Request[v1.CreateEpicRequest],
) (*connect.Response[v1.CreateEpicResponse], error) {
	return respond(f, req.Msg, &v1.CreateEpicResponse{Epic: f.epic})
}

func (f *recordingEpics) GetEpic(
	_ context.Context,
	req *connect.Request[v1.GetEpicRequest],
) (*connect.Response[v1.GetEpicResponse], error) {
	return respond(f, req.Msg, &v1.GetEpicResponse{Epic: f.epic})
}

func (f *recordingEpics) ListEpics(
	_ context.Context,
	req *connect.Request[v1.ListEpicsRequest],
) (*connect.Response[v1.ListEpicsResponse], error) {
	return respond(f, req.Msg, &v1.ListEpicsResponse{})
}

func (f *recordingEpics) AnswerEpic(
	_ context.Context,
	req *connect.Request[v1.AnswerEpicRequest],
) (*connect.Response[v1.AnswerEpicResponse], error) {
	return respond(f, req.Msg, f.answer)
}

func (f *recordingEpics) PauseEpic(
	_ context.Context,
	req *connect.Request[v1.PauseEpicRequest],
) (*connect.Response[v1.PauseEpicResponse], error) {
	return respond(f, req.Msg, &v1.PauseEpicResponse{Epic: f.epic})
}

func (f *recordingEpics) MoveEpic(
	_ context.Context,
	req *connect.Request[v1.MoveEpicRequest],
) (*connect.Response[v1.MoveEpicResponse], error) {
	return respond(f, req.Msg, &v1.MoveEpicResponse{Epic: f.epic})
}

func (f *recordingEpics) ContinueEpic(
	_ context.Context,
	req *connect.Request[v1.ContinueEpicRequest],
) (*connect.Response[v1.ContinueEpicResponse], error) {
	return respond(f, req.Msg, &v1.ContinueEpicResponse{Epic: f.epic})
}

func (f *recordingEpics) ResumeEpic(
	_ context.Context,
	req *connect.Request[v1.ResumeEpicRequest],
) (*connect.Response[v1.ResumeEpicResponse], error) {
	return respond(f, req.Msg, &v1.ResumeEpicResponse{Epic: f.epic})
}

func (f *recordingEpics) StopEpic(
	_ context.Context,
	req *connect.Request[v1.StopEpicRequest],
) (*connect.Response[v1.StopEpicResponse], error) {
	return respond(f, req.Msg, &v1.StopEpicResponse{Epic: f.epic})
}

func (f *recordingEpics) CloseEpic(
	_ context.Context,
	req *connect.Request[v1.CloseEpicRequest],
) (*connect.Response[v1.CloseEpicResponse], error) {
	return respond(f, req.Msg, &v1.CloseEpicResponse{Epic: f.epic})
}

func (f *recordingEpics) AmendEvidence(
	_ context.Context,
	req *connect.Request[v1.AmendEvidenceRequest],
) (*connect.Response[v1.AmendEvidenceResponse], error) {
	return respond(f, req.Msg, &v1.AmendEvidenceResponse{
		Amendment: &v1.EpicAmendment{Seq: 1, Requirement: req.Msg.GetRequirement(), State: "checking"},
		Epic:      f.epic,
	})
}

func (f *recordingEpics) ReleaseEpic(
	_ context.Context,
	req *connect.Request[v1.ReleaseEpicRequest],
) (*connect.Response[v1.ReleaseEpicResponse], error) {
	return respond(f, req.Msg, &v1.ReleaseEpicResponse{Epic: f.epic})
}

func (f *recordingEpics) SeeEpic(
	_ context.Context,
	req *connect.Request[v1.SeeEpicRequest],
) (*connect.Response[v1.SeeEpicResponse], error) {
	return respond(f, req.Msg, &v1.SeeEpicResponse{})
}

func (f *recordingEpics) ArchiveRow(
	_ context.Context,
	req *connect.Request[v1.ArchiveRowRequest],
) (*connect.Response[v1.ArchiveRowResponse], error) {
	return respond(f, req.Msg, &v1.ArchiveRowResponse{})
}

func (f *recordingEpics) UnarchiveRow(
	_ context.Context,
	req *connect.Request[v1.UnarchiveRowRequest],
) (*connect.Response[v1.UnarchiveRowResponse], error) {
	return respond(f, req.Msg, &v1.UnarchiveRowResponse{})
}

func (f *recordingEpics) RetryEpic(
	_ context.Context,
	req *connect.Request[v1.RetryEpicRequest],
) (*connect.Response[v1.RetryEpicResponse], error) {
	return respond(f, req.Msg, &v1.RetryEpicResponse{Epic: f.epic})
}

func (f *recordingEpics) KickEpic(
	_ context.Context,
	req *connect.Request[v1.KickEpicRequest],
) (*connect.Response[v1.KickEpicResponse], error) {
	return respond(f, req.Msg, &v1.KickEpicResponse{Epic: f.epic})
}

func (f *recordingEpics) FollowEpics(
	_ context.Context,
	req *connect.Request[v1.FollowEpicsRequest],
) (*connect.Response[v1.FollowEpicsResponse], error) {
	return respond(f, req.Msg, f.follow)
}

// recorded returns the requests the fake received, under its lock.
func (f *recordingEpics) recorded() []proto.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

// runEpicCommand runs epicCommand with args and stdin against epics and returns what it wrote to stdout and stderr and
// its error.
func runEpicCommand(
	t *testing.T, epics enginev1connect.EpicsClient, stdin io.Reader, args ...string,
) (string, string, error) {
	t.Helper()
	var stdout, stderr strings.Builder
	err := epicCommand(t.Context(), epics, args, stdin, &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

// epicVerbCase is one CLI form and the MCP tool, with the arguments, that it must call. setup, when set, configures
// the fake the form and the tool each run against.
type epicVerbCase struct {
	name  string
	args  []string
	tool  string
	input string
	setup func(*recordingEpics)
}

// fake returns a recordingEpics configured for the case.
func (tc epicVerbCase) fake() *recordingEpics {
	f := newRecordingEpics()
	if tc.setup != nil {
		tc.setup(f)
	}
	return f
}

func epicVerbCases() []epicVerbCase {
	return []epicVerbCase{
		{
			name:  "get",
			args:  []string{"get", epicTestID},
			tool:  "epic_get",
			input: `{"id":"demo-0a1b2c3/epic-login"}`,
			setup: func(f *recordingEpics) {
				f.epic.Questions = []*v1.EpicQuestion{{
					Id:      "Q1",
					Turn:    1,
					Text:    "Retry the parked build?",
					State:   "open",
					Label:   "retry-policy",
					Choices: []*v1.EpicQuestionChoice{{Id: "retry", Does: "runs the epic again"}},
					Recommendation: &v1.EpicRecommendation{
						Choice:     "retry",
						Why:        "retry runs the epic again",
						Confidence: "low",
					},
				}}
			},
		},
		{
			name:  "list",
			args:  []string{"list"},
			tool:  "epic_list",
			input: `{}`,
		},
		{
			name:  "answer names a question",
			args:  []string{"answer", epicTestID, "--id", "d2", "--question", "Q1", "yes, OIDC"},
			tool:  "epic_answer",
			input: `{"epicId":"demo-0a1b2c3/epic-login","id":"d2","question":"Q1","text":"yes, OIDC"}`,
		},
		{
			name:  "answer a note",
			args:  []string{"answer", epicTestID, "--id", "n1", "a note"},
			tool:  "epic_answer",
			input: `{"epicId":"demo-0a1b2c3/epic-login","id":"n1","text":"a note"}`,
		},
		{
			name:  "answer a declared choice without text",
			args:  []string{"answer", epicTestID, "--id", "d2", "--question", "Q1", "--choice", "retry"},
			tool:  "epic_answer",
			input: `{"epicId":"demo-0a1b2c3/epic-login","id":"d2","question":"Q1","choice":"retry"}`,
		},
		{
			name: "answer other with text",
			args: []string{"answer", epicTestID, "--id", "d2", "--question", "Q1", "--choice", "other",
				"yes, OIDC"},
			tool:  "epic_answer",
			input: `{"epicId":"demo-0a1b2c3/epic-login","id":"d2","question":"Q1","choice":"other","text":"yes, OIDC"}`,
		},
		{
			name:  "follow",
			args:  []string{"follow"},
			tool:  "epic_follow",
			input: `{}`,
			setup: func(f *recordingEpics) {
				f.follow = &v1.FollowEpicsResponse{Follows: []*v1.EpicFollow{
					{Label: "retry-policy", Answered: 4, Followed: 3},
					{Label: "scope", Answered: 2, Followed: 0},
				}}
			},
		},
		{
			name:  "pause",
			args:  []string{"pause", epicTestID},
			tool:  "epic_pause",
			input: `{"id":"demo-0a1b2c3/epic-login"}`,
		},
		{
			name:  "resume",
			args:  []string{"resume", epicTestID},
			tool:  "epic_resume",
			input: `{"id":"demo-0a1b2c3/epic-login"}`,
		},
		{
			name:  "stop",
			args:  []string{"stop", epicTestID},
			tool:  "epic_stop",
			input: `{"id":"demo-0a1b2c3/epic-login"}`,
		},
		{
			name:  "retry",
			args:  []string{"retry", epicTestID},
			tool:  "epic_retry",
			input: `{"id":"demo-0a1b2c3/epic-login"}`,
		},
		{
			name:  "retry names a node",
			args:  []string{"retry", epicTestID, "build"},
			tool:  "epic_retry",
			input: `{"id":"demo-0a1b2c3/epic-login","node":"build"}`,
		},
		{
			name:  "release",
			args:  []string{"release", epicTestID},
			tool:  "epic_release",
			input: `{"id":"demo-0a1b2c3/epic-login"}`,
		},
		{
			name:  "release names undo-none steps in order",
			args:  []string{"release", epicTestID, "--undo-none", "2", "--undo-none", "3"},
			tool:  "epic_release",
			input: `{"id":"demo-0a1b2c3/epic-login","undoNone":[2,3]}`,
		},
	}
}

// TestEpicVerbsSendTheMCPToolsRequest checks that each form sends the request its MCP tool sends for the same input,
// and prints the tool's result text on stdout with nothing on stderr.
func TestEpicVerbsSendTheMCPToolsRequest(t *testing.T) {
	t.Parallel()
	for _, tc := range epicVerbCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cliFake := tc.fake()
			stdout, stderr, err := runEpicCommand(t, cliFake, nil, tc.args...)
			if err != nil {
				t.Fatalf("epicCommand(%v) = %v, want nil", tc.args, err)
			}

			mcpFake := tc.fake()
			result, err := stepmcp.CallEpicTool(t.Context(), mcpFake, tc.tool, json.RawMessage(tc.input))
			if err != nil {
				t.Fatalf("CallEpicTool(%s) = %v, want nil", tc.tool, err)
			}
			if result.IsError {
				t.Fatalf("CallEpicTool(%s) returned an error result: %v", tc.tool, result.Content)
			}

			got, want := cliFake.recorded(), mcpFake.recorded()
			wantCalls := 1
			if tc.tool == "epic_answer" {
				wantCalls = 2 // the answer's phase read: AnswerEpic, then GetEpic
			}
			if len(want) != wantCalls || len(got) != len(want) {
				t.Fatalf("requests: cli %d, mcp %d; want %d each", len(got), len(want), wantCalls)
			}
			for i := range want {
				if !proto.Equal(got[i], want[i]) {
					t.Errorf("epicCommand(%v) request %d sent %v, want %v", tc.args, i, got[i], want[i])
				}
			}
			if wantOut := result.Content[0].Text + "\n"; stdout != wantOut {
				t.Errorf("stdout = %q, want %q", stdout, wantOut)
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want empty", stderr)
			}
		})
	}
}

// TestEpicRefusalReturnsTheToolsTextAndPrintsNothing checks that a refusal from the service returns an error carrying
// the MCP tool's refusal text and writes nothing to stdout.
func TestEpicRefusalReturnsTheToolsTextAndPrintsNothing(t *testing.T) {
	t.Parallel()
	for _, tc := range epicVerbCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			refusal := connect.NewError(connect.CodeFailedPrecondition, errors.New(epicRefusal))
			cliFake := newRecordingEpics()
			cliFake.err = refusal
			stdout, _, err := runEpicCommand(t, cliFake, nil, tc.args...)
			if err == nil {
				t.Fatalf("epicCommand(%v) = nil, want the refusal", tc.args)
			}

			mcpFake := newRecordingEpics()
			mcpFake.err = refusal
			result, callErr := stepmcp.CallEpicTool(t.Context(), mcpFake, tc.tool, json.RawMessage(tc.input))
			if callErr != nil {
				t.Fatalf("CallEpicTool(%s) = %v, want a refusal result", tc.tool, callErr)
			}
			if !result.IsError {
				t.Fatalf("CallEpicTool(%s) returned no error result", tc.tool)
			}
			if want := result.Content[0].Text; err.Error() != want {
				t.Errorf("epicCommand(%v) error = %q, want the MCP refusal %q", tc.args, err, want)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
		})
	}
}

// TestEpicCreateReadsFileOrStdin checks that create sends the JSON of its file, or of stdin for -, as the arguments
// of epic_create.
func TestEpicCreateReadsFileOrStdin(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "epic.json")
	if err := os.WriteFile(path, []byte(epicCreateJSON), 0o600); err != nil {
		t.Fatalf("write epic file: %v", err)
	}
	cases := []struct {
		name  string
		args  []string
		stdin io.Reader
	}{
		{name: "file", args: []string{"create", path}},
		{name: "stdin", args: []string{"create", "-"}, stdin: strings.NewReader(epicCreateJSON)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cliFake := newRecordingEpics()
			stdout, stderr, err := runEpicCommand(t, cliFake, tc.stdin, tc.args...)
			if err != nil {
				t.Fatalf("epicCommand(%v) = %v, want nil", tc.args, err)
			}

			mcpFake := newRecordingEpics()
			result, err := stepmcp.CallEpicTool(t.Context(), mcpFake, "epic_create", json.RawMessage(epicCreateJSON))
			if err != nil {
				t.Fatalf("CallEpicTool(epic_create) = %v, want nil", err)
			}
			got, want := cliFake.recorded(), mcpFake.recorded()
			if len(got) != 1 || len(want) != 1 {
				t.Fatalf("requests: cli %d, mcp %d; want 1 each", len(got), len(want))
			}
			if !proto.Equal(got[0], want[0]) {
				t.Errorf("epicCommand(%v) sent %v, want %v", tc.args, got[0], want[0])
			}
			if wantOut := result.Content[0].Text + "\n"; stdout != wantOut {
				t.Errorf("stdout = %q, want %q", stdout, wantOut)
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want empty", stderr)
			}
		})
	}
}

// TestEpicAnswerWritesWarningToStderr checks that a note recorded while questions are open prints the decision on
// stdout, as the MCP tool's text, and its warning on stderr.
func TestEpicAnswerWritesWarningToStderr(t *testing.T) {
	t.Parallel()
	const answerJSON = `{"epicId":"demo-0a1b2c3/epic-login","id":"n1","text":"a note"}`
	reply := &v1.AnswerEpicResponse{
		Decision: &v1.EpicDecision{Id: "n1", Text: "a note"},
		Warning:  epicWarning,
	}
	cliFake := newRecordingEpics()
	cliFake.answer = reply
	stdout, stderr, err := runEpicCommand(t, cliFake, nil, "answer", epicTestID, "--id", "n1", "a note")
	if err != nil {
		t.Fatalf("epicCommand(answer) = %v, want nil", err)
	}

	mcpFake := newRecordingEpics()
	mcpFake.answer = reply
	result, err := stepmcp.CallEpicTool(t.Context(), mcpFake, "epic_answer", json.RawMessage(answerJSON))
	if err != nil {
		t.Fatalf("CallEpicTool(epic_answer) = %v, want nil", err)
	}
	if wantOut := result.Content[0].Text + "\n"; stdout != wantOut {
		t.Errorf("stdout = %q, want %q", stdout, wantOut)
	}
	if stderr != epicWarning+"\n" {
		t.Errorf("stderr = %q, want the warning %q", stderr, epicWarning+"\n")
	}
}

// TestEpicWrongFormsAreUsageErrors checks that each wrong form returns a usage error naming the form it wants, and
// calls no tool.
func TestEpicWrongFormsAreUsageErrors(t *testing.T) {
	t.Parallel()
	const answerForm = "answer EPIC --id ID [--question QN] [--choice C] [TEXT]"
	const releaseForm = "release ID [--undo-none N]..."
	cases := []struct {
		args []string
		form string
	}{
		{args: nil, form: "create FILE|-"},
		{args: []string{"bogus"}, form: "create FILE|-"},
		{args: []string{"create"}, form: "create FILE|-"},
		{args: []string{"create", "a.json", "b.json"}, form: "create FILE|-"},
		{args: []string{"get"}, form: "get ID"},
		{args: []string{"get", "a", "b"}, form: "get ID"},
		{args: []string{"list", "extra"}, form: "list"},
		{args: []string{"follow", "extra"}, form: "follow"},
		{args: []string{"answer", epicTestID, "text"}, form: answerForm},
		{args: []string{"answer", epicTestID, "--id", "d2"}, form: answerForm},
		{args: []string{"answer", epicTestID, "--id", "d2", "--question", "Q1"}, form: answerForm},
		{args: []string{"answer", epicTestID, "--id", "d2", "a", "b"}, form: answerForm},
		{args: []string{"answer", epicTestID, "--id", "d2", "--choice", "retry", "a", "b"}, form: answerForm},
		{args: []string{"answer", epicTestID, "--bogus", "x", "--id", "d2", "text"}, form: answerForm},
		{args: []string{"answer", "--id", "d2", epicTestID, "text"}, form: answerForm},
		{args: []string{"pause"}, form: "pause ID"},
		{args: []string{"resume", "a", "b"}, form: "resume ID"},
		{args: []string{"stop"}, form: "stop ID"},
		{args: []string{"kick"}, form: "kick ID TEXT"},
		{args: []string{"kick", epicTestID}, form: "kick ID TEXT"},
		{args: []string{"kick", epicTestID, "a", "b"}, form: "kick ID TEXT"},
		{args: []string{"retry"}, form: "retry ID [NODE]"},
		{args: []string{"retry", epicTestID, "build", "extra"}, form: "retry ID [NODE]"},
		{args: []string{"retry", epicTestID, ""}, form: "retry ID [NODE]"},
		{args: []string{"release"}, form: releaseForm},
		{args: []string{"release", "--undo-none", "2"}, form: releaseForm},
		{args: []string{"release", epicTestID, "extra"}, form: releaseForm},
		{args: []string{"release", epicTestID, "--undo-none"}, form: releaseForm},
		{args: []string{"release", epicTestID, "--undo-none", "0"}, form: releaseForm},
		{args: []string{"release", epicTestID, "--undo-none", "-1"}, form: releaseForm},
		{args: []string{"release", epicTestID, "--undo-none", "two"}, form: releaseForm},
		{args: []string{"release", epicTestID, "--undo-none", "1.5"}, form: releaseForm},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			t.Parallel()
			cliFake := newRecordingEpics()
			stdout, _, err := runEpicCommand(t, cliFake, nil, tc.args...)
			if _, ok := errors.AsType[usageError](err); !ok {
				t.Fatalf("epicCommand(%v) = %v, want a usage error", tc.args, err)
			}
			if !strings.Contains(err.Error(), tc.form) {
				t.Errorf("epicCommand(%v) error %q does not name the form %q", tc.args, err, tc.form)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			if n := len(cliFake.recorded()); n != 0 {
				t.Errorf("epicCommand(%v) made %d calls, want none", tc.args, n)
			}
		})
	}
}

// TestEpicUsageNamesTheAnswerChoiceAndFollowForms checks that the usage text and the forms of `orrery epic` name
// `epic answer` with --choice and `epic follow`.
func TestEpicUsageNamesTheAnswerChoiceAndFollowForms(t *testing.T) {
	t.Parallel()
	const answerForm = "answer EPIC --id ID [--question QN] [--choice C] [TEXT]"
	for _, named := range []string{answerForm, "follow"} {
		if !strings.Contains(epicForms, named) {
			t.Errorf("epicForms = %q, want it to name %q", epicForms, named)
		}
	}
	if !strings.Contains(usageText, "epic "+answerForm) {
		t.Errorf("usage text does not name epic %s", answerForm)
	}
	if !strings.Contains(usageText, "epic follow") {
		t.Errorf("usage text does not name epic follow")
	}
}

// TestEpicCreateNamesTheWorkflowThroughTheMCPTool checks that epic_create sends the workflow it is given to CreateEpic.
func TestEpicCreateNamesTheWorkflowThroughTheMCPTool(t *testing.T) {
	t.Parallel()
	fake := newRecordingEpics()
	input := `{"contract":{"id":"demo-0a1b2c3/epic-login","title":"Login"},"workflow":"demo"}`

	if _, err := stepmcp.CallEpicTool(t.Context(), fake, "epic_create", json.RawMessage(input)); err != nil {
		t.Fatalf("CallEpicTool(epic_create) = %v, want nil", err)
	}
	requests := fake.recorded()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	req, ok := requests[0].(*v1.CreateEpicRequest)
	if !ok {
		t.Fatalf("request is %T, want *v1.CreateEpicRequest", requests[0])
	}
	if got := req.GetWorkflow(); got != "demo" {
		t.Fatalf("workflow = %q, want demo", got)
	}
}

// TestEpicGetPrintsTheEpicWorkflow checks that `orrery epic get` prints the workflow part of the epic the summary gives:
// one item, and the byte length of the workflow's text.
func TestEpicGetPrintsTheEpicWorkflow(t *testing.T) {
	t.Parallel()
	fake := newRecordingEpics()
	want := &v1.EpicWorkflow{Name: "demo", Text: "name: demo\n", Sha256: "synthetic-sha256"}
	fake.epic.Workflow = want

	stdout, _, err := runEpicCommand(t, fake, nil, "get", epicTestID)
	if err != nil {
		t.Fatalf("epicCommand(get) = %v, want nil", err)
	}
	line := fmt.Sprintf("\n  workflow: 1 items, %d bytes\n", len(want.GetText()))
	if !strings.Contains(stdout, line) {
		t.Fatalf("stdout has no workflow part line %q:\n%s", line, stdout)
	}
}
