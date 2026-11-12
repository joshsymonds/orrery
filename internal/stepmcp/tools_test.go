package stepmcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

const testTimeout = 5 * time.Second

// waitCall is one Wait the daemon received, with the timeout it converted from seconds.
type waitCall struct {
	ids     []string
	timeout time.Duration
}

// fakeSteps answers the step commands with the configured values and records each call.
type fakeSteps struct {
	mu       sync.Mutex
	starts   []enginehost.StepStart
	stops    []string
	statuses []string
	waits    []waitCall
	startID  string
	row      store.StepRow
	ended    *store.StepRow
	rows     []store.StepRow
	err      error
}

func (f *fakeSteps) Start(_ context.Context, start enginehost.StepStart) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, start)
	return f.startID, f.err
}

func (f *fakeSteps) Stop(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, id)
	return f.err
}

func (f *fakeSteps) Status(_ context.Context, id string) (store.StepRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses = append(f.statuses, id)
	return f.row, f.err
}

func (f *fakeSteps) Wait(
	_ context.Context,
	ids []string,
	timeout time.Duration,
) (*store.StepRow, []store.StepRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.waits = append(f.waits, waitCall{ids: slices.Clone(ids), timeout: timeout})
	return f.ended, f.rows, f.err
}

func (f *fakeSteps) List(context.Context, string, string) ([]store.StepRow, error) {
	return nil, errors.New("stepmcp test: List is not expected")
}

func (f *fakeSteps) Hold(context.Context, string) error {
	return errors.New("stepmcp test: Hold is not expected")
}

// Held is called by Status to report the row, and the row is not held.
func (f *fakeSteps) Held(string) bool {
	return false
}

func (f *fakeSteps) Nudge(context.Context, string, string) error {
	return errors.New("stepmcp test: Nudge is not expected")
}

// fakeCalls is a copy of what the fake recorded.
type fakeCalls struct {
	starts   []enginehost.StepStart
	stops    []string
	statuses []string
	waits    []waitCall
}

func (f *fakeSteps) calls() fakeCalls {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeCalls{
		starts:   slices.Clone(f.starts),
		stops:    slices.Clone(f.stops),
		statuses: slices.Clone(f.statuses),
		waits:    slices.Clone(f.waits),
	}
}

// fullRow is a step row with every field set.
func fullRow() store.StepRow {
	return store.StepRow{
		ID:        "s1",
		Agent:     "reviewer",
		Prompt:    "check the change",
		Schema:    `{"type":"object"}`,
		Workspace: "/work",
		Revision:  "abc123",
		Worktree:  "/wt/s1",
		DoneValue: "pass",
		ReadOnly:  true,
		Started:   time.Unix(1700000000, 0),
		Checks: []store.StepCheck{
			{Round: 1, Name: "gate", OK: false, Detail: "exit 1"},
			{Round: 2, Name: "gate", OK: true, Detail: ""},
		},
		Result: "all good",
		Status: "pass",
		End:    "done",
		Reason: "returned",
		Ended:  time.Unix(1700000600, 0),
	}
}

// runningRow is a step that has not ended.
func runningRow(id string) store.StepRow {
	return store.StepRow{
		ID:        id,
		Agent:     "reviewer",
		Workspace: "/work",
		Started:   time.Unix(1700000000, 0),
	}
}

// startDaemon serves steps on a socket in a temporary directory and returns a client
// that dials it the way the session client does.
func startDaemon(t *testing.T, steps enginehost.StepCommands) enginev1connect.StepsClient {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.sock")
	ln, err := enginehost.Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- enginehost.Serve(ctx, ln, nil, steps) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return enginev1connect.NewStepsClient(cli.SessionHTTPClient(path), cli.SessionBaseURL)
}

// rpcReply is one JSON-RPC response read from the server.
type rpcReply struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// peer drives one MCP connection over in-memory pipes.
type peer struct {
	t           *testing.T
	input       *io.PipeWriter
	outputRead  *io.PipeReader
	output      *json.Decoder
	nextRequest int
}

// newPeer serves an MCP server built over steps and completes the handshake.
func newPeer(t *testing.T, steps enginev1connect.StepsClient) *peer {
	t.Helper()
	server, err := NewServer(steps)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	serverInput, clientInput := io.Pipe()
	outputRead, serverOutput := io.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, serverInput, serverOutput) }()
	t.Cleanup(func() {
		cancel()
		_ = clientInput.Close()
		_ = outputRead.Close()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	p := &peer{
		t: t, input: clientInput, outputRead: outputRead,
		output: json.NewDecoder(bufio.NewReader(outputRead)),
	}
	p.call("initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "test", "version": "1"},
	})
	p.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	return p
}

func (p *peer) send(message any) {
	p.t.Helper()
	line, err := json.Marshal(message)
	if err != nil {
		p.t.Fatalf("marshal request: %v", err)
	}
	if _, err := p.input.Write(append(line, '\n')); err != nil {
		p.t.Fatalf("write request: %v", err)
	}
}

// call sends a request and returns its response. A response that does not arrive in time
// closes the output so the decode fails instead of hanging the run.
func (p *peer) call(method string, params any) rpcReply {
	p.t.Helper()
	p.nextRequest++
	p.send(map[string]any{"jsonrpc": "2.0", "id": p.nextRequest, "method": method, "params": params})
	watchdog := time.AfterFunc(testTimeout, func() { _ = p.outputRead.Close() })
	defer watchdog.Stop()
	var reply rpcReply
	if err := p.output.Decode(&reply); err != nil {
		p.t.Fatalf("read %s reply: %v", method, err)
	}
	return reply
}

// toolOutcome is the single text block of a tool result and whether the tool reported an error.
type toolOutcome struct {
	text    string
	isError bool
}

// tool calls a tool with arguments as a JSON object text.
func (p *peer) tool(name, arguments string) toolOutcome {
	p.t.Helper()
	reply := p.call("tools/call", map[string]any{"name": name, "arguments": json.RawMessage(arguments)})
	if reply.Error != nil {
		p.t.Fatalf("tools/call %s: JSON-RPC error %d: %s", name, reply.Error.Code, reply.Error.Message)
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(reply.Result, &result); err != nil {
		p.t.Fatalf("decode tools/call %s result: %v", name, err)
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" {
		p.t.Fatalf("tools/call %s content = %+v, want one text block", name, result.Content)
	}
	return toolOutcome{text: result.Content[0].Text, isError: result.IsError}
}

// decodeObject decodes a JSON object text.
func decodeObject(t *testing.T, text string) map[string]any {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal([]byte(text), &object); err != nil || object == nil {
		t.Fatalf("result %q is not a JSON object: %v", text, err)
	}
	return object
}

func TestToolsListNamesTheFourStepTools(t *testing.T) {
	p := newPeer(t, startDaemon(t, &fakeSteps{}))

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
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
		if tool.InputSchema["type"] != "object" || tool.InputSchema["additionalProperties"] != false {
			t.Errorf("tool %s schema = %v, want type object and additionalProperties false",
				tool.Name, tool.InputSchema)
		}
		if _, ok := tool.InputSchema["required"]; !ok {
			t.Errorf("tool %s schema has no required list", tool.Name)
		}
	}
	want := []string{"step_start", "step_status", "step_stop", "step_wait"}
	slices.Sort(names)
	if !slices.Equal(names, want) {
		t.Fatalf("tools/list names = %v, want %v", names, want)
	}
}

func TestStepStartDeliversEveryFieldAndReturnsID(t *testing.T) {
	fake := &fakeSteps{startID: "0123456789abcdef0123456789abcdef"}
	p := newPeer(t, startDaemon(t, fake))

	outcome := p.tool("step_start", `{
		"agent": "reviewer",
		"prompt": "check the change",
		"workspace": "/work",
		"schema": {"type":"object"},
		"revision": "abc123",
		"doneValue": "pass",
		"readOnly": true,
		"ownedPaths": ["a.go", "b/c.go"],
		"gate": ["go", "test"],
		"gateLimitSeconds": 90
	}`)

	if outcome.isError {
		t.Fatalf("step_start reported an error: %s", outcome.text)
	}
	if got := decodeObject(t, outcome.text); !reflect.DeepEqual(got, map[string]any{"id": fake.startID}) {
		t.Fatalf("step_start result = %v, want id %s", got, fake.startID)
	}
	want := []enginehost.StepStart{{
		Agent:     "reviewer",
		Prompt:    "check the change",
		Schema:    `{"type":"object"}`,
		Workspace: "/work",
		Revision:  "abc123",
		ReadOnly:  true,
		Owned:     []string{"a.go", "b/c.go"},
		Gate:      []string{"go", "test"},
		GateLimit: 90 * time.Second,
		DoneValue: "pass",
	}}
	if got := fake.calls().starts; !reflect.DeepEqual(got, want) {
		t.Fatalf("step_start reached the daemon as %+v, want %+v", got, want)
	}
}

func TestStepStatusAndStopReachTheDaemonByID(t *testing.T) {
	fake := &fakeSteps{row: fullRow()}
	p := newPeer(t, startDaemon(t, fake))

	status := p.tool("step_status", `{"id":"s1"}`)
	if status.isError {
		t.Fatalf("step_status reported an error: %s", status.text)
	}
	wantStep := map[string]any{
		"id":        "s1",
		"agent":     "reviewer",
		"workspace": "/work",
		"revision":  "abc123",
		"readOnly":  true,
		"worktree":  "/wt/s1",
		"end":       "done",
		"status":    "pass",
		"result":    "all good",
		"checks": []any{
			map[string]any{"round": float64(1), "name": "gate", "ok": false, "detail": "exit 1"},
			map[string]any{"round": float64(2), "name": "gate", "ok": true, "detail": ""},
		},
		"reason":  "returned",
		"started": float64(1700000000),
		"ended":   float64(1700000600),
	}
	if got := decodeObject(t, status.text); !reflect.DeepEqual(got, wantStep) {
		t.Fatalf("step_status result = %v, want %v", got, wantStep)
	}

	stop := p.tool("step_stop", `{"id":"s9"}`)
	if stop.isError {
		t.Fatalf("step_stop reported an error: %s", stop.text)
	}
	if got := decodeObject(t, stop.text); !reflect.DeepEqual(got, map[string]any{"id": "s9", "stopped": true}) {
		t.Fatalf("step_stop result = %v, want id s9 stopped", got)
	}

	calls := fake.calls()
	if !slices.Equal(calls.statuses, []string{"s1"}) {
		t.Fatalf("step_status reached the daemon with ids %v, want [s1]", calls.statuses)
	}
	if !slices.Equal(calls.stops, []string{"s9"}) {
		t.Fatalf("step_stop reached the daemon with ids %v, want [s9]", calls.stops)
	}
}

func TestStepWaitReturnsEndedStepAtOnce(t *testing.T) {
	ended := fullRow()
	fake := &fakeSteps{ended: &ended, rows: []store.StepRow{ended, runningRow("s2")}}
	p := newPeer(t, startDaemon(t, fake))

	outcome := p.tool("step_wait", `{"ids":["s1","s2"]}`)
	if outcome.isError {
		t.Fatalf("step_wait reported an error: %s", outcome.text)
	}
	got := decodeObject(t, outcome.text)
	endedStep, ok := got["ended"].(map[string]any)
	if !ok || endedStep["id"] != "s1" || endedStep["status"] != "pass" {
		t.Fatalf("step_wait ended = %v, want step s1 with status pass", got["ended"])
	}
	if steps, ok := got["steps"].([]any); !ok || len(steps) != 2 {
		t.Fatalf("step_wait steps = %v, want two steps", got["steps"])
	}
}

func TestStepWaitTimeoutReturnsNullEndedAndEveryStatus(t *testing.T) {
	fake := &fakeSteps{rows: []store.StepRow{runningRow("s1"), runningRow("s2")}}
	p := newPeer(t, startDaemon(t, fake))

	outcome := p.tool("step_wait", `{"ids":["s1","s2"],"timeoutSeconds":5}`)
	if outcome.isError {
		t.Fatalf("step_wait reported an error: %s", outcome.text)
	}
	got := decodeObject(t, outcome.text)
	if value, ok := got["ended"]; !ok || value != nil {
		t.Fatalf("step_wait at timeout ended = %v (present %t), want null", value, ok)
	}
	steps, ok := got["steps"].([]any)
	if !ok || len(steps) != 2 {
		t.Fatalf("step_wait steps = %v, want two running steps", got["steps"])
	}
	for i, id := range []string{"s1", "s2"} {
		step, ok := steps[i].(map[string]any)
		if !ok || step["id"] != id || step["status"] != "" || step["ended"] != float64(0) {
			t.Fatalf("step_wait step %d = %v, want running step %s", i, steps[i], id)
		}
	}
	want := []waitCall{{ids: []string{"s1", "s2"}, timeout: 5 * time.Second}}
	if got := fake.calls().waits; !reflect.DeepEqual(got, want) {
		t.Fatalf("step_wait reached the daemon as %+v, want %+v", got, want)
	}
}

func TestStepWaitClampsTimeoutToMaxWait(t *testing.T) {
	const maxWait = 25 * time.Minute
	cases := []struct {
		name      string
		arguments string
		want      time.Duration
	}{
		{name: "omitted", arguments: `{"ids":["s1"]}`, want: maxWait},
		{name: "above the maximum", arguments: `{"ids":["s1"],"timeoutSeconds":3600}`, want: maxWait},
		{name: "above uint32", arguments: `{"ids":["s1"],"timeoutSeconds":4294967296}`, want: maxWait},
		{name: "below the maximum", arguments: `{"ids":["s1"],"timeoutSeconds":90}`, want: 90 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeSteps{rows: []store.StepRow{runningRow("s1")}}
			p := newPeer(t, startDaemon(t, fake))

			if outcome := p.tool("step_wait", tc.arguments); outcome.isError {
				t.Fatalf("step_wait reported an error: %s", outcome.text)
			}
			want := []waitCall{{ids: []string{"s1"}, timeout: tc.want}}
			if got := fake.calls().waits; !reflect.DeepEqual(got, want) {
				t.Fatalf("step_wait reached the daemon as %+v, want %+v", got, want)
			}
		})
	}
}

func TestDaemonErrorsReadAsCodeAndMessageToolErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		tool string
		args string
		want string
	}{
		{
			name: "unknown id", err: store.ErrNoStep, tool: "step_status",
			args: `{"id":"nope"}`, want: "not_found: no step",
		},
		{
			name: "unknown id on stop", err: store.ErrNoStep, tool: "step_stop",
			args: `{"id":"nope"}`, want: "not_found: no step",
		},
		{
			name: "other failure", err: errors.New("disk on fire"), tool: "step_wait",
			args: `{"ids":["s1"]}`, want: "internal: disk on fire",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newPeer(t, startDaemon(t, &fakeSteps{err: tc.err}))

			outcome := p.tool(tc.tool, tc.args)
			if !outcome.isError {
				t.Fatalf("%s returned %q without isError", tc.tool, outcome.text)
			}
			if outcome.text != tc.want {
				t.Fatalf("%s error text = %q, want %q", tc.tool, outcome.text, tc.want)
			}
		})
	}
}

func TestBadArgumentsAreToolErrorsAndServerKeepsServing(t *testing.T) {
	fake := &fakeSteps{row: fullRow()}
	p := newPeer(t, startDaemon(t, fake))

	bad := []struct {
		name string
		tool string
		args string
		want string
	}{
		{
			name: "start missing agent",
			tool: "step_start",
			args: `{"prompt":"p","workspace":"/w","schema":{}}`,
			want: "agent is required",
		},
		{
			name: "start prompt wrong type",
			tool: "step_start",
			args: `{"agent":"a","prompt":3,"workspace":"/w","schema":{}}`,
			want: "prompt",
		},
		{
			name: "start schema is a string",
			tool: "step_start",
			args: `{"agent":"a","prompt":"p","workspace":"/w","schema":"{}"}`,
			want: "schema must be a JSON object",
		},
		{
			name: "start schema missing",
			tool: "step_start",
			args: `{"agent":"a","prompt":"p","workspace":"/w"}`,
			want: "schema is required",
		},
		{
			name: "start schema null",
			tool: "step_start",
			args: `{"agent":"a","prompt":"p","workspace":"/w","schema":null}`,
			want: "schema must be a JSON object",
		},
		{
			name: "start unknown key",
			tool: "step_start",
			args: `{"agent":"a","prompt":"p","workspace":"/w","schema":{},"bogus":1}`,
			want: `unknown field "bogus"`,
		},
		{
			name: "start negative gate limit",
			tool: "step_start",
			args: `{"agent":"a","prompt":"p","workspace":"/w","schema":{},"gateLimitSeconds":-1}`,
			want: "gateLimitSeconds",
		},
		{
			name: "start gate limit as text",
			tool: "step_start",
			args: `{"agent":"a","prompt":"p","workspace":"/w","schema":{},"gateLimitSeconds":"90"}`,
			want: "gateLimitSeconds",
		},
		{
			name: "start readOnly as text",
			tool: "step_start",
			args: `{"agent":"a","prompt":"p","workspace":"/w","schema":{},"readOnly":"yes"}`,
			want: "readOnly",
		},
		{name: "status missing id", tool: "step_status", args: `{}`, want: "id is required"},
		{name: "status empty id", tool: "step_status", args: `{"id":""}`, want: "id is required"},
		{name: "stop empty id", tool: "step_stop", args: `{"id":""}`, want: "id is required"},
		{name: "stop id wrong type", tool: "step_stop", args: `{"id":7}`, want: "id"},
		{name: "wait empty ids", tool: "step_wait", args: `{"ids":[]}`, want: "ids is required"},
		{name: "wait missing ids", tool: "step_wait", args: `{"timeoutSeconds":5}`, want: "ids is required"},
		{
			name: "wait empty id in ids",
			tool: "step_wait",
			args: `{"ids":["s1",""]}`,
			want: "must not contain an empty id",
		},
		{
			name: "wait negative timeout",
			tool: "step_wait",
			args: `{"ids":["s1"],"timeoutSeconds":-1}`,
			want: "timeoutSeconds",
		},
		{
			name: "wait fractional timeout",
			tool: "step_wait",
			args: `{"ids":["s1"],"timeoutSeconds":1.5}`,
			want: "timeoutSeconds",
		},
		{name: "wait ids not strings", tool: "step_wait", args: `{"ids":[1]}`, want: "ids"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			outcome := p.tool(tc.tool, tc.args)
			if !outcome.isError {
				t.Fatalf("%s returned %q without isError", tc.tool, outcome.text)
			}
			if !strings.Contains(outcome.text, tc.want) {
				t.Fatalf("%s error = %q, want it to contain %q", tc.tool, outcome.text, tc.want)
			}
		})
	}
	if got := fake.calls(); len(got.starts)+len(got.stops)+len(got.statuses)+len(got.waits) != 0 {
		t.Fatalf("bad arguments reached the daemon: %+v", got)
	}

	outcome := p.tool("step_status", `{"id":"s1"}`)
	if outcome.isError {
		t.Fatalf("server stopped serving after bad arguments: %s", outcome.text)
	}
}
