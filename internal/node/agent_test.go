package node

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"tailscale.com/client/tailscale/apitype"

	"github.com/joshsymonds/orrery/internal/exectest"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/holderhost"
)

// fakeClaudeScript stands in for claude. It writes two stdout lines, and then reads its stdin until the stdin ends.
const fakeClaudeScript = `#!/bin/sh
printf '%s\n' '{"type":"system","subtype":"init"}'
printf '%s\n' '{"type":"fake","n":2}'
while IFS= read -r _; do :; done
`

// firstLine and secondLine are the two stdout lines that the fake claude writes.
const (
	firstLine  = "{\"type\":\"system\",\"subtype\":\"init\"}\n"
	secondLine = "{\"type\":\"fake\",\"n\":2}\n"
)

// peerWhoIs names the agent's own address as the agent's own node, and every other address as the controller under the
// owner's login. Each connection the tests make comes from a new address, so each one is named as the controller.
type peerWhoIs struct {
	own string
}

func (w peerWhoIs) WhoIs(_ context.Context, addr string) (*apitype.WhoIsResponse, error) {
	if addr == w.own {
		return ownAnswer().resp, nil
	}
	return controllerAnswer().resp, nil
}

// keptLog is a slog handler that sends the session of each keeper's attach on kept. A test waits on kept for a keeper to
// attach, so that it never sleeps.
type keptLog struct {
	kept chan<- string
}

func (keptLog) Enabled(context.Context, slog.Level) bool { return true }

func (k keptLog) Handle(_ context.Context, record slog.Record) error {
	if record.Message != keptMessage {
		return nil
	}
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "session" {
			k.kept <- attr.Value.String()
		}
		return true
	})
	return nil
}

func (k keptLog) WithAttrs([]slog.Attr) slog.Handler { return k }

func (k keptLog) WithGroup(string) slog.Handler { return k }

// newShortRoot creates a temporary directory to use as a state directory. A socket path must fit in the socket address
// limit, which a t.TempDir path can exceed. The caller removes the root.
func newShortRoot() (string, error) {
	root, err := os.MkdirTemp("", "node")
	if err != nil {
		return "", fmt.Errorf("make state directory: %w", err)
	}
	return root, nil
}

// shortStateDir returns a state directory from newShortRoot, which is removed when the test ends.
func shortStateDir(t *testing.T) string {
	t.Helper()
	root, err := newShortRoot()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove %s: %v", root, err)
		}
	})
	return root
}

// nodeLocal returns the Local that an agent serves in these tests, and the state directory it keeps its holders in. The
// Local runs the fake claude under sh and has a HOME of its own. Every session it holds is cleared when the test ends.
func nodeLocal(t *testing.T) (*holderhost.Local, string) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	claude := filepath.Join(t.TempDir(), "claude")
	exectest.WriteFile(t, claude, []byte(fakeClaudeScript), 0o700)
	stateDir := shortStateDir(t)
	local := holderhost.NewLocal(holderhost.LocalConfig{
		Shell:    sh,
		Claude:   claude,
		Environ:  []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
		StateDir: stateDir,
	})
	t.Cleanup(func() { clearSessions(t, local) })
	return local, stateDir
}

// clearSessions clears every session that local holds, so that no holder outlives the test.
func clearSessions(t *testing.T, local *holderhost.Local) {
	t.Helper()
	ids, err := local.Sessions(context.Background())
	if err != nil {
		t.Errorf("list sessions: %v", err)
	}
	for _, id := range ids {
		if err = local.Clear(context.Background(), id); err != nil {
			t.Errorf("clear session %s: %v", id, err)
		}
	}
}

// startNodeAgent serves an Agent over local on a loopback listener until the test ends. It returns the listener's address,
// and the channel on which each keeper reports its attach. The controller is every peer but the agent's own address.
func startNodeAgent(t *testing.T, local *holderhost.Local, stateDir string) (string, <-chan string) {
	t.Helper()
	ln := listenLoopback(t)
	kept := make(chan string, 64)
	agent, err := NewAgent(AgentConfig{
		Controller: testController,
		WhoIs:      peerWhoIs{own: ln.Addr().String()},
		Holders:    local,
		StateDir:   stateDir,
		Logger:     slog.New(keptLog{kept: kept}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- agent.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Error(err)
		}
	})
	return ln.Addr().String(), kept
}

// request sends req from a controller connection to the agent at addr, and returns the Response line. It reports errors
// rather than failing a test, so that a goroutine can call it.
func request(addr string, req Request) (Response, error) {
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", addr)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = conn.Close() }()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, err
	}
	var resp Response
	err = json.NewDecoder(conn).Decode(&resp)
	return resp, err
}

// ask sends req from the controller to the agent at addr, and returns the Response.
func ask(t *testing.T, addr string, req Request) Response {
	t.Helper()
	resp, err := request(addr, req)
	if err != nil {
		t.Fatalf("%s: %v", req.Op, err)
	}
	return resp
}

// dialController dials the agent at addr from the controller, and closes the connection when the test ends.
func dialController(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
	})
	return conn
}

// openStream sends req on a new controller connection. It returns the Response line, the reader of what follows that line,
// and the connection, which the test closes to disconnect the controller.
func openStream(t *testing.T, addr string, req Request) (Response, *bufio.Reader, net.Conn) {
	t.Helper()
	conn := dialController(t, addr)
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read the %s response: %v", req.Op, err)
	}
	var resp Response
	if err = json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode the %s response %q: %v", req.Op, line, err)
	}
	return resp, reader, conn
}

// launchSession launches the headless session id on the agent at addr, in a directory of its own, and fails the test when
// the launch is refused.
func launchSession(t *testing.T, addr, id string) {
	t.Helper()
	req := Request{Version: Version, Op: OpLaunch, Session: id, Launch: &LaunchSpec{Dir: t.TempDir()}}
	if resp := ask(t, addr, req); resp.Code != "" {
		t.Fatalf("launch %s: %+v", id, resp)
	}
}

// awaitKept waits until a keeper reports that it attached to session id.
func awaitKept(t *testing.T, kept <-chan string, id string) {
	t.Helper()
	if got := <-kept; got != id {
		t.Fatalf("keeper attached to session %q, want %q", got, id)
	}
}

// exitStopped reports whether exit is the exit record of a child that a stop ended.
func exitStopped(exit *holder.Exit) bool {
	return exit != nil && exit.Reason == holder.ReasonStopped
}

// spliceAttach speaks the holder wire over the splice that conn is: it greets the holder, attaches at offset, and returns
// the frames that the holder sends from then on.
func spliceAttach(t *testing.T, conn net.Conn, reader *bufio.Reader, offset int64) *holder.FrameReader {
	t.Helper()
	writeHolderFrame(t, conn, holder.Frame{Type: holder.TypeHello, Version: holder.WireVersion})
	frames := holder.NewFrameReader(reader)
	if got := readHolderFrame(t, frames); got.Type != holder.TypeHello {
		t.Fatalf("holder answered hello with %q, want hello", got.Type)
	}
	writeHolderFrame(t, conn, holder.Frame{Type: holder.TypeAttach, ReplayFrom: offset})
	if got := readHolderFrame(t, frames); got.Type != holder.TypeAttached {
		t.Fatalf("holder answered attach with %q, want attached", got.Type)
	}
	return frames
}

// writeHolderFrame writes frame to conn in the holder wire's form.
func writeHolderFrame(t *testing.T, conn net.Conn, frame holder.Frame) {
	t.Helper()
	if err := holder.WriteFrame(conn, frame); err != nil {
		t.Fatalf("write a %s frame: %v", frame.Type, err)
	}
}

// readHolderFrame reads the next frame that the holder sent.
func readHolderFrame(t *testing.T, frames *holder.FrameReader) holder.Frame {
	t.Helper()
	frame, err := frames.Read()
	if err != nil {
		t.Fatalf("read a holder frame: %v", err)
	}
	return frame
}

// readOutput reads the child's stdout from frames, which start at offset, until it has n bytes, and returns them. Each out
// frame must start where the one before it ended.
func readOutput(t *testing.T, frames *holder.FrameReader, offset int64, n int) string {
	t.Helper()
	var out []byte
	for len(out) < n {
		frame := readHolderFrame(t, frames)
		if frame.Type != holder.TypeOut {
			t.Fatalf("holder sent a %s frame, want out", frame.Type)
		}
		if want := offset + int64(len(out)); frame.Offset != want {
			t.Fatalf("out frame at offset %d, want %d", frame.Offset, want)
		}
		out = append(out, frame.Data...)
	}
	return string(out)
}

// TestNodeAgentLaunchesAttachesAndStaysAttached launches a headless session on the agent's Local, under the agent's state
// directory. It attaches to the session through node.v1 from the start and then from an offset, reading the child's
// output each time. Once the controller disconnects, the agent keeps the holder attached.
func TestNodeAgentLaunchesAttachesAndStaysAttached(t *testing.T) {
	local, stateDir := nodeLocal(t)
	addr, kept := startNodeAgent(t, local, stateDir)

	launchSession(t, addr, "s1")
	if _, err := os.Stat(filepath.Join(stateDir, holdersName, "s1")); err != nil {
		t.Fatalf("the session has no holder directory under the agent's state directory: %v", err)
	}
	awaitKept(t, kept, "s1")

	resp, reader, conn := openStream(t, addr, Request{Version: Version, Op: OpAttach, Session: "s1"})
	if resp.Code != "" {
		t.Fatalf("attach from offset 0: %+v", resp)
	}
	frames := spliceAttach(t, conn, reader, 0)
	if got, want := readOutput(t, frames, 0, len(firstLine)+len(secondLine)), firstLine+secondLine; got != want {
		t.Fatalf("stdout from offset 0 = %q, want %q", got, want)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	awaitKept(t, kept, "s1")

	resp, reader, conn = openStream(t, addr, Request{Version: Version, Op: OpAttach, Session: "s1"})
	if resp.Code != "" {
		t.Fatalf("attach from offset %d: %+v", len(firstLine), resp)
	}
	frames = spliceAttach(t, conn, reader, int64(len(firstLine)))
	if got := readOutput(t, frames, int64(len(firstLine)), len(secondLine)); got != secondLine {
		t.Fatalf("stdout from offset %d = %q, want %q", len(firstLine), got, secondLine)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	awaitKept(t, kept, "s1")
}

// TestNodeAgentRefusesStepSpec refuses a launch of a step, and a launch that names claude's agent, agents or schema
// options, since steps stay on the controller. A refused launch makes no holder directory.
func TestNodeAgentRefusesStepSpec(t *testing.T) {
	local, stateDir := nodeLocal(t)
	addr, _ := startNodeAgent(t, local, stateDir)
	for _, tc := range []struct {
		name string
		spec LaunchSpec
	}{
		{name: "step", spec: LaunchSpec{Step: true}},
		{name: "agent", spec: LaunchSpec{Agent: "reviewer"}},
		{name: "agents", spec: LaunchSpec{Agents: `{"a":{}}`}},
		{name: "schema", spec: LaunchSpec{Schema: `{"type":"object"}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := tc.spec
			spec.Dir = t.TempDir()
			resp := ask(t, addr, Request{Version: Version, Op: OpLaunch, Session: tc.name, Launch: &spec})
			if resp.Code != CodeFailed {
				t.Fatalf("launch with %s got %+v, want code %q", tc.name, resp, CodeFailed)
			}
			if _, err := os.Stat(filepath.Join(stateDir, holdersName, tc.name)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("the refused launch left a holder directory: %v", err)
			}
		})
	}
}

// TestNodeAgentSessionsListsSessionAndAnswersUnknownsWithErrors lists the session that a launch started. It answers an op
// that the agent does not serve, and an op or an attach on a session the agent does not know, with an error response.
func TestNodeAgentSessionsListsSessionAndAnswersUnknownsWithErrors(t *testing.T) {
	local, stateDir := nodeLocal(t)
	addr, _ := startNodeAgent(t, local, stateDir)
	launchSession(t, addr, "listed")

	listed := ask(t, addr, Request{Version: Version, Op: OpSessions})
	if !slices.Equal(listed.Sessions, []string{"listed"}) {
		t.Fatalf("sessions = %v, want [listed]", listed.Sessions)
	}
	if resp := ask(t, addr, Request{Version: Version, Op: "bogus"}); resp.Code != CodeFailed {
		t.Fatalf("an unknown op got %+v, want code %q", resp, CodeFailed)
	}
	for _, op := range []Op{OpStop, OpKill, OpClear, OpStopOrphan, OpEnd} {
		if resp := ask(t, addr, Request{Version: Version, Op: op, Session: "unknown"}); resp.Code != CodeGone {
			t.Fatalf("%s of an unknown session got %+v, want code %q", op, resp, CodeGone)
		}
	}
	gone, _, _ := openStream(t, addr, Request{Version: Version, Op: OpGone, Session: "unknown"})
	if gone.Code != CodeGone {
		t.Fatalf("gone of an unknown session got %+v, want code %q", gone, CodeGone)
	}
	attach, _, _ := openStream(t, addr, Request{Version: Version, Op: OpAttach, Session: "unknown"})
	if attach.Code != CodeGone {
		t.Fatalf("attach to an unknown session got %+v, want code %q", attach, CodeGone)
	}
}

// TestNodeAgentGoneAndEndReturnSpoolAndExitAfterEnd has end wait on a session while the controller stops it, and then
// streams the session's spool with gone. Both return the exit record that the holder wrote, and gone returns the child's
// output from the offset it asks for.
func TestNodeAgentGoneAndEndReturnSpoolAndExitAfterEnd(t *testing.T) {
	local, stateDir := nodeLocal(t)
	addr, kept := startNodeAgent(t, local, stateDir)
	launchSession(t, addr, "ended")
	awaitKept(t, kept, "ended")

	_, reader, conn := openStream(t, addr, Request{Version: Version, Op: OpAttach, Session: "ended"})
	frames := spliceAttach(t, conn, reader, 0)
	readOutput(t, frames, 0, len(firstLine)+len(secondLine))
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	type answer struct {
		resp Response
		err  error
	}
	ended := make(chan answer, 1)
	go func() {
		resp, err := request(addr, Request{Version: Version, Op: OpEnd, Session: "ended"})
		ended <- answer{resp: resp, err: err}
	}()
	if resp := ask(t, addr, Request{Version: Version, Op: OpStop, Session: "ended"}); resp.Code != "" {
		t.Fatalf("stop: %+v", resp)
	}
	end := <-ended
	if end.err != nil {
		t.Fatalf("end: %v", end.err)
	}
	if end.resp.Code != "" || !exitStopped(end.resp.Exit) {
		t.Fatalf("end = %+v, want code none and the stopped exit record", end.resp)
	}

	gone, reader, conn := openStream(t, addr, Request{Version: Version, Op: OpGone, Session: "ended"})
	if !exitStopped(gone.Exit) {
		t.Fatalf("gone = %+v, want the stopped exit record", gone)
	}
	spool, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read the spool: %v", err)
	}
	if got, want := string(spool), firstLine+secondLine; got != want {
		t.Fatalf("spool = %q, want %q", got, want)
	}
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestNodeAgentStopKillAndClearEndSession ends three sessions. Stop ends one, and end then answers with the record of that
// stop. Kill ends another, and end answers once its lock is free. Clear removes the third holder's directory, so end finds
// the session unknown and sessions no longer lists it.
func TestNodeAgentStopKillAndClearEndSession(t *testing.T) {
	local, stateDir := nodeLocal(t)
	addr, _ := startNodeAgent(t, local, stateDir)
	for _, id := range []string{"stopped", "killed", "cleared"} {
		launchSession(t, addr, id)
	}

	if resp := ask(t, addr, Request{Version: Version, Op: OpStop, Session: "stopped"}); resp.Code != "" {
		t.Fatalf("stop: %+v", resp)
	}
	if resp := ask(t, addr, Request{Version: Version, Op: OpEnd, Session: "stopped"}); !exitStopped(resp.Exit) {
		t.Fatalf("end after stop = %+v, want the stopped exit record", resp)
	}

	if resp := ask(t, addr, Request{Version: Version, Op: OpKill, Session: "killed"}); resp.Code != "" {
		t.Fatalf("kill: %+v", resp)
	}
	if resp := ask(t, addr, Request{Version: Version, Op: OpEnd, Session: "killed"}); resp.Code != "" {
		t.Fatalf("end after kill = %+v, want no code", resp)
	}

	if resp := ask(t, addr, Request{Version: Version, Op: OpClear, Session: "cleared"}); resp.Code != "" {
		t.Fatalf("clear: %+v", resp)
	}
	if resp := ask(t, addr, Request{Version: Version, Op: OpEnd, Session: "cleared"}); resp.Code != CodeGone {
		t.Fatalf("end after clear = %+v, want code %q", resp, CodeGone)
	}

	resp := ask(t, addr, Request{Version: Version, Op: OpSessions})
	if !slices.Equal(resp.Sessions, []string{"killed", "stopped"}) {
		t.Fatalf("sessions after clear = %v, want [killed stopped]", resp.Sessions)
	}
}
