package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/mcp"
	"github.com/joshsymonds/orrery/internal/store"
)

// MCP requests the round-trip test sends, one per line. The client waits for each reply that has one before it sends
// the next, as a real client does.
const (
	mcpInitialize  = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`
	mcpInitialized = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	mcpList        = `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	mcpStatus      = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"step_status","arguments":{"id":"s1"}}}`
)

// mcpSteps is the step service behind the test daemon. Status returns row. The other methods are not reached by
// these tests, so they fail if called.
type mcpSteps struct {
	row store.StepRow
}

func (mcpSteps) Start(context.Context, enginehost.StepStart) (string, error) {
	return "", errors.New("mcp test: Start is not expected")
}

func (mcpSteps) Stop(context.Context, string) error {
	return errors.New("mcp test: Stop is not expected")
}

func (f mcpSteps) Status(context.Context, string) (store.StepRow, error) {
	return f.row, nil
}

func (mcpSteps) Wait(context.Context, []string, time.Duration) (*store.StepRow, []store.StepRow, error) {
	return nil, nil, errors.New("mcp test: Wait is not expected")
}

func (mcpSteps) List(context.Context, string, string) ([]store.StepRow, error) {
	return nil, errors.New("mcp test: List is not expected")
}

func (mcpSteps) Hold(context.Context, string) error {
	return errors.New("mcp test: Hold is not expected")
}

// Held is called by Status to report the row, and the row is not held.
func (mcpSteps) Held(string) bool {
	return false
}

func (mcpSteps) Nudge(context.Context, string, string) error {
	return errors.New("mcp test: Nudge is not expected")
}

func (mcpSteps) Resume(context.Context, string) error {
	return errors.New("mcp test: Resume is not expected")
}

// startMCPDaemon serves the steps service on sessions.sock in a temporary directory, the session socket that
// `orrery mcp` dials beside the daemon socket. It returns the daemon socket path that `--socket` names.
func startMCPDaemon(t *testing.T, steps enginehost.StepCommands) string {
	t.Helper()
	dir := t.TempDir()
	ln, err := enginehost.Listen(filepath.Join(dir, "sessions.sock"))
	if err != nil {
		t.Fatalf("enginehost.Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- enginehost.Serve(ctx, ln, nil, steps, nil, nil) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("enginehost.Serve: %v", err)
			}
		case <-time.After(serveTestWait):
			t.Errorf("enginehost.Serve did not return within %v", serveTestWait)
		}
	})
	return filepath.Join(dir, "orreryd.sock")
}

// mcpReply is one JSON-RPC response read from the server's stdout.
type mcpReply struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

// mcpRun is one orrery command line running on a goroutine, with the test holding its stdin and stdout.
type mcpRun struct {
	t      *testing.T
	stdin  *os.File
	stdout *os.File
	lines  *bufio.Reader
	stderr strings.Builder
	done   chan int
}

// noEnv is the environment for a command line that names every value it needs as a flag.
func noEnv(string) string {
	return ""
}

// mustPipe returns the read and write ends of a pipe.
func mustPipe(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	return reader, writer
}

// closeFile closes f. A file that is already closed is not an error.
func closeFile(t *testing.T, f *os.File) {
	t.Helper()
	if err := f.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		t.Errorf("close %s: %v", f.Name(), err)
	}
}

// startRun runs `orrery args` with pipes for stdin and stdout. The command returns on its own goroutine, and it
// closes stdout when it returns.
func startRun(t *testing.T, args ...string) *mcpRun {
	t.Helper()
	stdinR, stdinW := mustPipe(t)
	stdoutR, stdoutW := mustPipe(t)
	r := &mcpRun{t: t, stdin: stdinW, stdout: stdoutR, lines: bufio.NewReader(stdoutR), done: make(chan int, 1)}
	t.Cleanup(func() {
		closeFile(t, stdinW)
		closeFile(t, stdoutR)
	})
	go func() {
		code := run(args, stdinR, stdoutW, &r.stderr, noEnv)
		closeFile(t, stdoutW)
		closeFile(t, stdinR)
		r.done <- code
	}()
	return r
}

// send writes one request line to the command's stdin.
func (r *mcpRun) send(line string) {
	r.t.Helper()
	if _, err := io.WriteString(r.stdin, line+"\n"); err != nil {
		r.t.Fatalf("write request: %v", err)
	}
}

// receive reads the next reply line from the command's stdout. It fails the test when no line arrives within
// serveTestWait or when the line is not a JSON-RPC response.
func (r *mcpRun) receive() mcpReply {
	r.t.Helper()
	if err := r.stdout.SetReadDeadline(time.Now().Add(serveTestWait)); err != nil {
		r.t.Fatalf("set read deadline: %v", err)
	}
	line, err := r.lines.ReadBytes('\n')
	if err != nil {
		r.t.Fatalf("read reply: %v; partial line %q", err, line)
	}
	var reply mcpReply
	if err := json.Unmarshal(line, &reply); err != nil {
		r.t.Fatalf("stdout line %q is not a JSON-RPC reply: %v", line, err)
	}
	return reply
}

// finish closes stdin, waits for the command to return, and returns its exit code. It fails the test when the command
// does not return within serveTestWait, or when stdout holds anything the test did not receive as a reply.
func (r *mcpRun) finish() int {
	r.t.Helper()
	closeFile(r.t, r.stdin)
	select {
	case code := <-r.done:
		rest, err := io.ReadAll(r.lines)
		if err != nil {
			r.t.Fatalf("read stdout tail: %v", err)
		}
		if len(rest) != 0 {
			r.t.Fatalf("stdout holds output the test did not receive as a reply: %q", rest)
		}
		return code
	case <-time.After(serveTestWait):
		r.t.Fatalf("orrery did not return within %v after stdin closed", serveTestWait)
		return 0
	}
}

// requireResult fails the test unless reply answers request id without error, and returns its result.
func requireResult(t *testing.T, reply mcpReply, id int) json.RawMessage {
	t.Helper()
	if reply.ID != id {
		t.Fatalf("reply id = %d, want %d", reply.ID, id)
	}
	if len(reply.Error) != 0 {
		t.Fatalf("reply %d carries error %s", id, reply.Error)
	}
	return reply.Result
}

// TestMCPServesStepToolsOverDaemon drives `orrery --socket S mcp` through the handshake, a tool listing, and a
// step_status call, each answered before the next is sent. The command must answer with the test daemon's data,
// write nothing else to stdout, and exit 0 once stdin closes.
func TestMCPServesStepToolsOverDaemon(t *testing.T) {
	socket := startMCPDaemon(t, mcpSteps{row: store.StepRow{ID: "s1", Agent: "reviewer", Workspace: "/work"}})
	cmd := startRun(t, "--socket", socket, "mcp")

	cmd.send(mcpInitialize)
	initResult := requireResult(t, cmd.receive(), 1)
	cmd.send(mcpInitialized)
	cmd.send(mcpList)
	listResult := requireResult(t, cmd.receive(), 2)
	cmd.send(mcpStatus)
	statusResult := requireResult(t, cmd.receive(), 3)

	if code := cmd.finish(); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr %q", code, cmd.stderr.String())
	}

	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(initResult, &initialized); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if initialized.ProtocolVersion != mcp.ProtocolVersion {
		t.Fatalf("protocolVersion = %q, want %q", initialized.ProtocolVersion, mcp.ProtocolVersion)
	}

	var listed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(listResult, &listed); err != nil {
		t.Fatalf("decode tools/list result: %v", err)
	}
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	want := []string{
		"step_start", "step_status", "step_stop", "step_resume", "step_wait",
		"epic_create", "epic_get", "epic_list", "epic_answer",
	}
	if !slices.Equal(names, want) {
		t.Fatalf("tool names = %v, want %v", names, want)
	}

	var called struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(statusResult, &called); err != nil {
		t.Fatalf("decode tools/call result: %v", err)
	}
	if called.IsError || len(called.Content) != 1 {
		t.Fatalf("step_status result = %s, want one content block without error", statusResult)
	}
	var step struct {
		ID    string `json:"id"`
		Agent string `json:"agent"`
	}
	if err := json.Unmarshal([]byte(called.Content[0].Text), &step); err != nil {
		t.Fatalf("decode step text %q: %v", called.Content[0].Text, err)
	}
	if step.ID != "s1" || step.Agent != "reviewer" {
		t.Fatalf("step = %+v, want id s1 and agent reviewer", step)
	}
}

// TestMCPRefusesArguments checks that any argument after mcp is a usage error: exit 2, with the usage text on stderr
// and nothing on stdout.
func TestMCPRefusesArguments(t *testing.T) {
	socket := startMCPDaemon(t, mcpSteps{})
	cmd := startRun(t, "--socket", socket, "mcp", "extra")
	if code := cmd.finish(); code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr %q", code, cmd.stderr.String())
	}
	stderr := cmd.stderr.String()
	if !strings.Contains(stderr, "usage: orrery") || !strings.Contains(stderr, "\n  mcp ") {
		t.Fatalf("stderr %q does not carry the usage text with an mcp line", stderr)
	}
}
