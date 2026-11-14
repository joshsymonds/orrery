package stepmcp

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

const testRevision = "0123456789abcdef0123456789abcdef01234567"

// fakeEvidence answers the evidence commands with the configured replies and records each request.
type fakeEvidence struct {
	mu        sync.Mutex
	runs      []*v1.RunEvidenceRequest
	lists     []*v1.ListEvidenceRequest
	gets      []*v1.GetEvidenceArtifactRequest
	runReply  *v1.RunEvidenceResponse
	listReply *v1.ListEvidenceResponse
	getReply  *v1.GetEvidenceArtifactResponse
	err       error
}

func (f *fakeEvidence) RunEvidence(
	_ context.Context,
	req *connect.Request[v1.RunEvidenceRequest],
) (*connect.Response[v1.RunEvidenceResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, req.Msg)
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.runReply), nil
}

func (f *fakeEvidence) ListEvidence(
	_ context.Context,
	req *connect.Request[v1.ListEvidenceRequest],
) (*connect.Response[v1.ListEvidenceResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists = append(f.lists, req.Msg)
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.listReply), nil
}

func (f *fakeEvidence) GetEvidenceArtifact(
	_ context.Context,
	req *connect.Request[v1.GetEvidenceArtifactRequest],
) (*connect.Response[v1.GetEvidenceArtifactResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets = append(f.gets, req.Msg)
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.getReply), nil
}

// evidenceCalls is a copy of the requests the fake received.
type evidenceCalls struct {
	runs  []*v1.RunEvidenceRequest
	lists []*v1.ListEvidenceRequest
	gets  []*v1.GetEvidenceArtifactRequest
}

func (f *fakeEvidence) calls() evidenceCalls {
	f.mu.Lock()
	defer f.mu.Unlock()
	return evidenceCalls{
		runs:  slices.Clone(f.runs),
		lists: slices.Clone(f.lists),
		gets:  slices.Clone(f.gets),
	}
}

func (c evidenceCalls) total() int {
	return len(c.runs) + len(c.lists) + len(c.gets)
}

// sampleEvidenceRun is an evidence run with its fields set to values distinct from each other.
func sampleEvidenceRun() *v1.EvidenceRun {
	return &v1.EvidenceRun{
		Id:       "run-1",
		Epic:     testEpicID,
		Item:     "R1",
		Revision: testRevision,
		Phase:    "red",
		Source:   "captured",
		State:    "ended",
		Exit:     1,
		TimedOut: true,
		LogHash:  "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		LogBytes: 12,
		Artifacts: []*v1.EvidenceArtifact{{
			Path:  "out/roundtrip.log",
			State: "kept",
			Hash:  "2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae",
			Size:  7,
		}},
		JudgeStep: "step-9",
		Verdict:   "fails",
	}
}

func TestToolsListNamesTheThreeEvidenceTools(t *testing.T) {
	p := newPeerWithEvidence(t, nil, nil, &fakeEvidence{})

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
	for _, name := range []string{"evidence_run", "evidence_list", "evidence_get"} {
		schema, ok := schemas[name]
		if !ok {
			t.Errorf("tools/list does not name %s", name)
			continue
		}
		if schema["type"] != "object" || schema["additionalProperties"] != false {
			t.Errorf("%s schema = %v, want type object and additionalProperties false", name, schema)
		}
	}
}

func TestEvidenceRunSendsExactArgumentsAndReturnsRun(t *testing.T) {
	run := sampleEvidenceRun()
	fake := &fakeEvidence{runReply: &v1.RunEvidenceResponse{Run: run}}
	p := newPeerWithEvidence(t, nil, nil, fake)

	outcome := p.tool("evidence_run",
		`{"epic":"`+testEpicID+`","item":"R1","revision":"`+testRevision+`","phase":"red"}`)

	if outcome.isError {
		t.Fatalf("evidence_run returned an error: %s", outcome.text)
	}
	calls := fake.calls().runs
	want := &v1.RunEvidenceRequest{Epic: testEpicID, Item: "R1", Revision: testRevision, Phase: "red"}
	if len(calls) != 1 || !proto.Equal(calls[0], want) {
		t.Fatalf("RunEvidence requests = %v, want %v", calls, want)
	}
	var got v1.EvidenceRun
	unmarshalResult(t, outcome.text, &got)
	if !proto.Equal(&got, run) {
		t.Fatalf("evidence_run result = %s, want the run", outcome.text)
	}
}

func TestEvidenceListSendsEpicAndReturnsRuns(t *testing.T) {
	listed := &v1.ListEvidenceResponse{Runs: []*v1.EvidenceRun{sampleEvidenceRun(), {
		Id: "run-2", Epic: testEpicID, Item: "R2", Revision: testRevision, Phase: "green", Source: "supplied",
		State: "judging",
	}}}
	fake := &fakeEvidence{listReply: listed}
	p := newPeerWithEvidence(t, nil, nil, fake)

	outcome := p.tool("evidence_list", `{"epic":"`+testEpicID+`"}`)

	if outcome.isError {
		t.Fatalf("evidence_list returned an error: %s", outcome.text)
	}
	calls := fake.calls().lists
	want := &v1.ListEvidenceRequest{Epic: testEpicID}
	if len(calls) != 1 || !proto.Equal(calls[0], want) {
		t.Fatalf("ListEvidence requests = %v, want %v", calls, want)
	}
	var got v1.ListEvidenceResponse
	unmarshalResult(t, outcome.text, &got)
	if !proto.Equal(&got, listed) {
		t.Fatalf("evidence_list result = %s, want the runs", outcome.text)
	}
}

func TestEvidenceListWithNoRunsEmitsAnEmptyArray(t *testing.T) {
	p := newPeerWithEvidence(t, nil, nil, &fakeEvidence{listReply: &v1.ListEvidenceResponse{}})

	outcome := p.tool("evidence_list", `{"epic":"`+testEpicID+`"}`)

	if outcome.isError {
		t.Fatalf("evidence_list returned an error: %s", outcome.text)
	}
	object := decodeObject(t, outcome.text)
	runs, ok := object["runs"].([]any)
	if !ok || len(runs) != 0 {
		t.Fatalf("evidence_list result = %s, want an explicit empty runs array", outcome.text)
	}
}

func TestEvidenceGetSendsRunAndNameAndReturnsArtifact(t *testing.T) {
	artifact := &v1.GetEvidenceArtifactResponse{
		Data: []byte("roundtrip ok\n"),
		Hash: "2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae",
	}
	cases := []struct {
		name      string
		arguments string
		want      *v1.GetEvidenceArtifactRequest
	}{
		{
			name:      "named artifact",
			arguments: `{"run":"run-1","name":"out/roundtrip.log"}`,
			want:      &v1.GetEvidenceArtifactRequest{Run: "run-1", Name: "out/roundtrip.log"},
		},
		{
			name:      "name omitted asks for the run's log",
			arguments: `{"run":"run-1"}`,
			want:      &v1.GetEvidenceArtifactRequest{Run: "run-1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeEvidence{getReply: artifact}
			p := newPeerWithEvidence(t, nil, nil, fake)

			outcome := p.tool("evidence_get", tc.arguments)

			if outcome.isError {
				t.Fatalf("evidence_get returned an error: %s", outcome.text)
			}
			calls := fake.calls().gets
			if len(calls) != 1 || !proto.Equal(calls[0], tc.want) {
				t.Fatalf("GetEvidenceArtifact requests = %v, want %v", calls, tc.want)
			}
			var got v1.GetEvidenceArtifactResponse
			unmarshalResult(t, outcome.text, &got)
			if !proto.Equal(&got, artifact) {
				t.Fatalf("evidence_get result = %s, want the artifact", outcome.text)
			}
		})
	}
}

func TestEvidenceGetWritesBytesAsBase64(t *testing.T) {
	fake := &fakeEvidence{getReply: &v1.GetEvidenceArtifactResponse{Data: []byte("hi"), Hash: "h"}}
	p := newPeerWithEvidence(t, nil, nil, fake)

	outcome := p.tool("evidence_get", `{"run":"run-1","name":"out/a"}`)

	if outcome.isError {
		t.Fatalf("evidence_get returned an error: %s", outcome.text)
	}
	object := decodeObject(t, outcome.text)
	if object["data"] != "aGk=" {
		t.Fatalf("evidence_get data = %v, want the base64 of the bytes aGk=", object["data"])
	}
}

func TestEvidenceToolsRefuseUnknownKeysBeforeAnyCall(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args string
	}{
		{
			name: "run unknown key",
			tool: "evidence_run",
			args: `{"epic":"` + testEpicID + `","item":"R1","revision":"r","phase":"red","bogus":1}`,
		},
		{name: "list unknown key", tool: "evidence_list", args: `{"epic":"` + testEpicID + `","bogus":1}`},
		{name: "get unknown key", tool: "evidence_get", args: `{"run":"run-1","bogus":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeEvidence{}
			p := newPeerWithEvidence(t, nil, nil, fake)

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

func TestEvidenceRefusalNamesItsText(t *testing.T) {
	cases := []struct {
		tool    string
		args    string
		refusal string
	}{
		{
			tool:    "evidence_run",
			args:    `{"epic":"` + testEpicID + `","item":"R1","revision":"r","phase":"blue"}`,
			refusal: "phase: must be red or green",
		},
		{
			tool:    "evidence_list",
			args:    `{"epic":"` + testEpicID + `"}`,
			refusal: "epic: no such epic",
		},
		{
			tool:    "evidence_get",
			args:    `{"run":"run-1"}`,
			refusal: "run: no such run",
		},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			fake := &fakeEvidence{err: connect.NewError(connect.CodeInvalidArgument, errors.New(tc.refusal))}
			p := newPeerWithEvidence(t, nil, nil, fake)

			outcome := p.tool(tc.tool, tc.args)

			if !outcome.isError {
				t.Fatalf("%s returned %q without isError", tc.tool, outcome.text)
			}
			want := "invalid_argument: " + tc.refusal
			if outcome.text != want {
				t.Fatalf("%s error text = %q, want %q", tc.tool, outcome.text, want)
			}
		})
	}
}
