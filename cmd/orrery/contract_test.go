package main

import (
	"context"
	"encoding/json"
	"errors"
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
	contractTestID      = "demo-7d8e9f0/login-policy"
	contractTestWords   = "Owner words."
	contractTestFileDoc = `{"id":"demo-7d8e9f0/login-policy","title":"Login policy",` +
		`"decisions":[{"id":"D1","question":"Use OIDC?","options":["yes","no"]}]}`
	contractTestRefusal = "contract demo-7d8e9f0/login-policy is already accepted"
)

// recordingContracts is a ContractsClient that records each request it receives, in order. It answers each call with a
// stored contract named contractTestID, or with err when err is set.
type recordingContracts struct {
	mu       sync.Mutex
	requests []proto.Message
	err      error
}

// contractRespond records req and returns reply, or the configured error.
func contractRespond[Res any](f *recordingContracts, req proto.Message, reply *Res) (*connect.Response[Res], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(reply), nil
}

// contractStored is the stored contract every reply carries.
func contractStored() *v1.StoredContract {
	return &v1.StoredContract{Id: contractTestID}
}

func (f *recordingContracts) ProposeContract(
	_ context.Context,
	req *connect.Request[v1.ProposeContractRequest],
) (*connect.Response[v1.ProposeContractResponse], error) {
	return contractRespond(f, req.Msg, &v1.ProposeContractResponse{Contract: contractStored()})
}

func (f *recordingContracts) ReviseContract(
	_ context.Context,
	req *connect.Request[v1.ReviseContractRequest],
) (*connect.Response[v1.ReviseContractResponse], error) {
	return contractRespond(f, req.Msg, &v1.ReviseContractResponse{Contract: contractStored()})
}

func (f *recordingContracts) AcceptContract(
	_ context.Context,
	req *connect.Request[v1.AcceptContractRequest],
) (*connect.Response[v1.AcceptContractResponse], error) {
	return contractRespond(f, req.Msg, &v1.AcceptContractResponse{Contract: contractStored()})
}

func (f *recordingContracts) SetAsideContract(
	_ context.Context,
	req *connect.Request[v1.SetAsideContractRequest],
) (*connect.Response[v1.SetAsideContractResponse], error) {
	return contractRespond(f, req.Msg, &v1.SetAsideContractResponse{Contract: contractStored()})
}

func (f *recordingContracts) ReopenContract(
	_ context.Context,
	req *connect.Request[v1.ReopenContractRequest],
) (*connect.Response[v1.ReopenContractResponse], error) {
	return contractRespond(f, req.Msg, &v1.ReopenContractResponse{Contract: contractStored()})
}

func (f *recordingContracts) GetContract(
	_ context.Context,
	req *connect.Request[v1.GetContractRequest],
) (*connect.Response[v1.GetContractResponse], error) {
	return contractRespond(f, req.Msg, &v1.GetContractResponse{Contract: contractStored()})
}

func (f *recordingContracts) DraftContract(
	_ context.Context,
	req *connect.Request[v1.DraftContractRequest],
) (*connect.Response[v1.DraftContractResponse], error) {
	return contractRespond(f, req.Msg, &v1.DraftContractResponse{Draft: &v1.ContractDraft{Id: contractTestID}})
}

func (f *recordingContracts) ListContracts(
	_ context.Context,
	req *connect.Request[v1.ListContractsRequest],
) (*connect.Response[v1.ListContractsResponse], error) {
	return contractRespond(f, req.Msg, &v1.ListContractsResponse{Contracts: []*v1.StoredContract{contractStored()}})
}

// recorded returns the requests the fake received, under its lock.
func (f *recordingContracts) recorded() []proto.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

// runContractCommand runs contractCommand with args against contracts and returns what it wrote to stdout and its error.
func runContractCommand(
	t *testing.T, contracts enginev1connect.ContractsClient, args ...string,
) (string, error) {
	t.Helper()
	var stdout strings.Builder
	err := contractCommand(t.Context(), contracts, args, &stdout)
	return stdout.String(), err
}

// contractRecordCase is one CLI form, the contract tool it calls and the input that tool takes for the same form.
type contractRecordCase struct {
	name  string
	args  []string
	tool  string
	input string
}

// contractRecordInput encodes fields as the JSON object a contract tool takes as its arguments.
func contractRecordInput(t *testing.T, fields map[string]string) string {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode tool input: %v", err)
	}
	return string(raw)
}

// contractRecordCases returns each form of `orrery contract` with the file at path, which propose and revise read.
func contractRecordCases(t *testing.T, path string) []contractRecordCase {
	t.Helper()
	return []contractRecordCase{
		{
			name:  "list",
			args:  []string{"list"},
			tool:  "contract_list",
			input: `{}`,
		},
		{
			name:  "get",
			args:  []string{"get", contractTestID},
			tool:  "contract_get",
			input: contractRecordInput(t, map[string]string{"id": contractTestID}),
		},
		{
			name: "propose",
			args: []string{"propose", contractTestID, path, contractTestWords},
			tool: "contract_propose",
			input: contractRecordInput(t, map[string]string{
				"id": contractTestID, "path": path, "words": contractTestWords,
			}),
		},
		{
			name: "revise",
			args: []string{"revise", contractTestID, path, contractTestWords},
			tool: "contract_revise",
			input: contractRecordInput(t, map[string]string{
				"id": contractTestID, "path": path, "words": contractTestWords,
			}),
		},
		{
			name:  "accept with words",
			args:  []string{"accept", contractTestID, contractTestWords},
			tool:  "contract_accept",
			input: contractRecordInput(t, map[string]string{"id": contractTestID, "words": contractTestWords}),
		},
		{
			name:  "accept without words",
			args:  []string{"accept", contractTestID},
			tool:  "contract_accept",
			input: contractRecordInput(t, map[string]string{"id": contractTestID}),
		},
		{
			name:  "set-aside",
			args:  []string{"set-aside", contractTestID, contractTestWords},
			tool:  "contract_set_aside",
			input: contractRecordInput(t, map[string]string{"id": contractTestID, "words": contractTestWords}),
		},
		{
			name:  "reopen with words",
			args:  []string{"reopen", contractTestID, contractTestWords},
			tool:  "contract_reopen",
			input: contractRecordInput(t, map[string]string{"id": contractTestID, "words": contractTestWords}),
		},
		{
			name:  "reopen without words",
			args:  []string{"reopen", contractTestID},
			tool:  "contract_reopen",
			input: contractRecordInput(t, map[string]string{"id": contractTestID}),
		},
	}
}

// contractRecordFile writes the contract file that propose and revise read, and returns its path.
func contractRecordFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "contract.json")
	if err := os.WriteFile(path, []byte(contractTestFileDoc), 0o600); err != nil {
		t.Fatalf("write contract file: %v", err)
	}
	return path
}

// TestContractRecordVerbsSendTheMCPToolsRequest checks that each form sends the request its contract tool sends for the
// same input, and prints the tool's result text on stdout with nothing on stderr.
func TestContractRecordVerbsSendTheMCPToolsRequest(t *testing.T) {
	t.Parallel()
	path := contractRecordFile(t)
	for _, tc := range contractRecordCases(t, path) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cliFake := &recordingContracts{}
			stdout, err := runContractCommand(t, cliFake, tc.args...)
			if err != nil {
				t.Fatalf("contractCommand(%v) = %v, want nil", tc.args, err)
			}

			mcpFake := &recordingContracts{}
			result, err := stepmcp.CallContractTool(t.Context(), mcpFake, tc.tool, json.RawMessage(tc.input))
			if err != nil {
				t.Fatalf("CallContractTool(%s) = %v, want nil", tc.tool, err)
			}
			if result.IsError {
				t.Fatalf("CallContractTool(%s) returned an error result: %v", tc.tool, result.Content)
			}

			got, want := cliFake.recorded(), mcpFake.recorded()
			if len(want) != 1 || len(got) != 1 {
				t.Fatalf("requests: cli %d, mcp %d; want 1 each", len(got), len(want))
			}
			if !proto.Equal(got[0], want[0]) {
				t.Errorf("contractCommand(%v) sent %v, want %v", tc.args, got[0], want[0])
			}
			assertContractFileSent(t, got[0])
			if wantOut := result.Content[0].Text + "\n"; stdout != wantOut {
				t.Errorf("stdout = %q, want %q", stdout, wantOut)
			}
		})
	}
}

// assertContractFileSent checks that a propose or revise request carries the contract file's id, title and decision
// D1 with its question and options. Any other request carries no file and passes.
func assertContractFileSent(t *testing.T, req proto.Message) {
	t.Helper()
	var (
		id        string
		contract  *v1.EpicContract
		decisions []*v1.ContractDecision
	)
	switch msg := req.(type) {
	case *v1.ProposeContractRequest:
		id, contract, decisions = msg.GetId(), msg.GetContract(), msg.GetDecisions()
	case *v1.ReviseContractRequest:
		id, contract, decisions = msg.GetId(), msg.GetContract(), msg.GetDecisions()
	default:
		return
	}
	if id != contractTestID || contract.GetId() != contractTestID || contract.GetTitle() != "Login policy" {
		t.Errorf("request id %q, contract id %q, title %q; want %q, %q, %q", id, contract.GetId(),
			contract.GetTitle(), contractTestID, contractTestID, "Login policy")
	}
	want := []*v1.ContractDecision{{Id: "D1", Question: "Use OIDC?", Options: []string{"yes", "no"}}}
	if len(decisions) != len(want) || !proto.Equal(decisions[0], want[0]) {
		t.Errorf("request decisions = %v, want %v", decisions, want)
	}
}

// TestContractRecordRefusalReturnsTheToolsTextAndPrintsNothing checks that a refusal from the service returns an error
// carrying the contract tool's refusal text and writes nothing to stdout.
func TestContractRecordRefusalReturnsTheToolsTextAndPrintsNothing(t *testing.T) {
	t.Parallel()
	path := contractRecordFile(t)
	for _, tc := range contractRecordCases(t, path) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			refusal := connect.NewError(connect.CodeFailedPrecondition, errors.New(contractTestRefusal))
			cliFake := &recordingContracts{err: refusal}
			stdout, err := runContractCommand(t, cliFake, tc.args...)
			if err == nil {
				t.Fatalf("contractCommand(%v) = nil, want the refusal", tc.args)
			}

			mcpFake := &recordingContracts{err: refusal}
			result, callErr := stepmcp.CallContractTool(t.Context(), mcpFake, tc.tool, json.RawMessage(tc.input))
			if callErr != nil {
				t.Fatalf("CallContractTool(%s) = %v, want a refusal result", tc.tool, callErr)
			}
			if !result.IsError {
				t.Fatalf("CallContractTool(%s) returned no error result", tc.tool)
			}
			if want := result.Content[0].Text; err.Error() != want {
				t.Errorf("contractCommand(%v) error = %q, want the MCP refusal %q", tc.args, err, want)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
		})
	}
}

// TestContractRecordWrongFormsAreUsageErrors checks that each wrong form returns a usage error naming the form it
// wants, and calls no tool.
func TestContractRecordWrongFormsAreUsageErrors(t *testing.T) {
	t.Parallel()
	const (
		proposeForm  = "propose ID FILE WORDS"
		reviseForm   = "revise ID FILE WORDS"
		acceptForm   = "accept ID [WORDS]"
		setAsideForm = "set-aside ID WORDS"
		reopenForm   = "reopen ID [WORDS]"
	)
	cases := []struct {
		args []string
		form string
	}{
		{args: nil, form: contractForms},
		{args: []string{"bogus"}, form: contractForms},
		{args: []string{"list", "extra"}, form: "list"},
		{args: []string{"get"}, form: "get ID"},
		{args: []string{"get", "a", "b"}, form: "get ID"},
		{args: []string{"propose"}, form: proposeForm},
		{args: []string{"propose", contractTestID, "contract.json"}, form: proposeForm},
		{args: []string{"propose", contractTestID, "contract.json", "w", "extra"}, form: proposeForm},
		{args: []string{"revise", contractTestID, "contract.json"}, form: reviseForm},
		{args: []string{"revise", contractTestID, "contract.json", "w", "extra"}, form: reviseForm},
		{args: []string{"accept"}, form: acceptForm},
		{args: []string{"accept", contractTestID, "w", "extra"}, form: acceptForm},
		{args: []string{"set-aside"}, form: setAsideForm},
		{args: []string{"set-aside", contractTestID}, form: setAsideForm},
		{args: []string{"set-aside", contractTestID, "w", "extra"}, form: setAsideForm},
		{args: []string{"reopen"}, form: reopenForm},
		{args: []string{"reopen", contractTestID, "w", "extra"}, form: reopenForm},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			t.Parallel()
			cliFake := &recordingContracts{}
			stdout, err := runContractCommand(t, cliFake, tc.args...)
			if _, ok := errors.AsType[usageError](err); !ok {
				t.Fatalf("contractCommand(%v) = %v, want a usage error", tc.args, err)
			}
			if !strings.Contains(err.Error(), tc.form) {
				t.Errorf("contractCommand(%v) error %q does not name the form %q", tc.args, err, tc.form)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			if n := len(cliFake.recorded()); n != 0 {
				t.Errorf("contractCommand(%v) made %d calls, want none", tc.args, n)
			}
		})
	}
}

// TestContractRecordUsageListsTheForms checks that the usage text and the forms of `orrery contract` name each of the
// seven forms.
func TestContractRecordUsageListsTheForms(t *testing.T) {
	t.Parallel()
	forms := []string{
		"list",
		"get ID",
		"propose ID FILE WORDS",
		"revise ID FILE WORDS",
		"accept ID [WORDS]",
		"set-aside ID WORDS",
		"reopen ID [WORDS]",
	}
	for _, form := range forms {
		if !strings.Contains(contractForms, form) {
			t.Errorf("contractForms = %q, want it to name %q", contractForms, form)
		}
		if !strings.Contains(usageText, "contract "+form) {
			t.Errorf("usage text does not name contract %s", form)
		}
	}
}
