package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/exectest"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// nodeRestartSession is the session that the restart tests launch.
const nodeRestartSession = "restart-turns"

// nodeRestartKeptRecord is the part of the log record that an agent writes once a keeper has attached to a holder. A
// test waits on that record to know the restarted agent has found the holder.
const nodeRestartKeptRecord = `msg="node: keeping holder"`

// nodeRestartFakeClaude stands in for claude. It echoes each stdin line that begins with turn, so that each turn the
// test writes is one stdout line, and it ignores the other lines the session sends it.
const nodeRestartFakeClaude = `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    turn*) printf '%s\n' "$line" ;;
  esac
done
`

// nodeRestartOrrery builds orrery and puts it first on PATH, since each session the agent launches runs under orrery
// holder. The package's TestMain builds it only for the tests it names, so a restart test builds it for itself.
func nodeRestartOrrery(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	build := exec.CommandContext(t.Context(), "go", "install", "github.com/joshsymonds/orrery/cmd/orrery")
	build.Env = append(os.Environ(), "GOBIN="+dir)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build orrery: %v\n%s", err, out)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// nodeRestartLog is the stderr of an agent in these tests. It sends on kept each record that reports a keeper's attach,
// so that a test waits on the attach and never sleeps.
type nodeRestartLog struct {
	kept chan<- string
}

// Write sends each record that reports a keeper's attach on kept. A record is dropped only when kept has no room, which
// a test never leaves full.
func (l nodeRestartLog) Write(record []byte) (int, error) {
	if bytes.Contains(record, []byte(nodeRestartKeptRecord)) {
		select {
		case l.kept <- string(record):
		default:
		}
	}
	return len(record), nil
}

// nodeRestartRun is an orrery node that a restart test started over a state directory. Its listener is at addr, and
// kept receives a record for each keeper that attaches. stop ends it, and waits for orrery node to return.
type nodeRestartRun struct {
	addr    string
	kept    <-chan string
	cancel  context.CancelFunc
	done    chan error
	stopped bool
}

// nodeRestartServe runs orrery node in this process over stateDir, with the fake claude at claude, as startNodeAgent
// runs it. Its listener is on a loopback port of its own, so each run has an address of its own. The run is stopped at
// the end of the test unless the test stops it first.
func nodeRestartServe(t *testing.T, stateDir, claude string) *nodeRestartRun {
	t.Helper()
	args := []string{
		"--controller", nodeAgentController, "--relay", nodeAgentRelay, "--listen", "127.0.0.1:0",
		"--secret-file", nodeAgentSecretFile(t, nodeAgentSecret),
	}
	cfg, err := parseNode(args)
	if err != nil {
		t.Fatalf("parseNode: %v", err)
	}
	cfg.server = tmuxctl.Server{}
	tailnet := &nodeAgentTailnet{}
	listening := make(chan string, 1)
	cfg.seams = nodeSeams{
		whoIs: tailnet,
		listen: func(addr string) (net.Listener, error) {
			ln, listenErr := net.Listen("tcp", addr)
			if listenErr != nil {
				return nil, listenErr
			}
			tailnet.own = ln.Addr().String()
			listening <- tailnet.own
			return ln, nil
		},
		stateDir: stateDir,
		claude:   claude,
	}
	getenv := envFrom(map[string]string{"HOME": t.TempDir(), "SHELL": nodeAgentLookPath(t, "sh")})
	kept := make(chan string, 16)
	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	run := &nodeRestartRun{kept: kept, cancel: cancel, done: make(chan error, 1)}
	go func() { run.done <- serveNode(ctx, cfg, nodeRestartLog{kept: kept}, getenv) }()
	select {
	case run.addr = <-listening:
	case serveErr := <-run.done:
		t.Fatalf("orrery node returned before it listened: %v", serveErr)
	}
	t.Cleanup(func() {
		if err := run.stop(); err != nil {
			t.Errorf("orrery node: %v", err)
		}
	})
	return run
}

// stop ends the run, and returns the error that orrery node returned with. A second stop returns nil.
func (r *nodeRestartRun) stop() error {
	if r.stopped {
		return nil
	}
	r.stopped = true
	r.cancel()
	return <-r.done
}

// client returns the node.Client that the controller uses to reach the run, holding nodeAgentSecret.
func (r *nodeRestartRun) client() *node.Client {
	return node.NewClient(node.ClientConfig{
		Name:    nodeAgentName,
		Address: r.addr,
		Dial:    nodeAgentDial(""),
		Clock:   nodeAgentNeverClock{},
		Secret:  nodeAgentSecret,
	})
}

// nodeRestartStdout reads a session's stdout one byte at a time, so that offset is exactly the stdout the reader has
// read to, which is the offset a reattach resumes at.
type nodeRestartStdout struct {
	r      io.Reader
	offset int64
}

// expect reads the next line of stdout, and fails the test unless the line is want.
func (s *nodeRestartStdout) expect(t *testing.T, want string) {
	t.Helper()
	var line []byte
	var b [1]byte
	for {
		if _, err := io.ReadFull(s.r, b[:]); err != nil {
			t.Fatalf("read stdout after %q: %v", line, err)
		}
		s.offset++
		if b[0] == '\n' {
			break
		}
		line = append(line, b[0])
	}
	if string(line) != want {
		t.Fatalf("stdout line = %q, want %q", line, want)
	}
}

// nodeRestartClaude writes the fake claude that the agents' sessions run, and returns its path.
func nodeRestartClaude(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	exectest.WriteFile(t, path, []byte(nodeRestartFakeClaude), 0o700)
	return path
}

// nodeRestartStart launches the session through a first agent over a state directory of its own, and attaches to it at
// offset 0. It returns the state directory, the first agent, the session's process and the reader of its stdout. The
// session's holder is ended when the test ends.
func nodeRestartStart(t *testing.T) (string, *nodeRestartRun, *holder.Process, *nodeRestartStdout) {
	t.Helper()
	nodeRestartOrrery(t)
	stateDir := shortStateDir(t)
	dir := sessionDirOf(t, stateDir, nodeRestartSession)
	t.Cleanup(func() {
		if err := holder.Kill(context.WithoutCancel(t.Context()), dir); err != nil {
			t.Errorf("kill the holder of %s: %v", nodeRestartSession, err)
		}
	})
	claude := nodeRestartClaude(t)
	first := nodeRestartServe(t, stateDir, claude)
	client := first.client()
	sess, err := client.Launch(t.Context(), holderhost.Spec{Session: nodeRestartSession, Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if err = sess.Detach(); err != nil {
		t.Fatalf("detach the launched session: %v", err)
	}
	process, _, err := client.Reattach(t.Context(), nodeRestartSession, 0, holder.WireVersion)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	return stateDir, first, process, &nodeRestartStdout{r: process.Stdout()}
}

// nodeRestartTurn writes turn to the session's stdin, and reads the stdout line that the turn's echo makes.
func nodeRestartTurn(t *testing.T, process *holder.Process, stdout *nodeRestartStdout, turn string) {
	t.Helper()
	if _, err := process.Stdin().Write([]byte(turn + "\n")); err != nil {
		t.Fatalf("write %s: %v", turn, err)
	}
	stdout.expect(t, turn)
}

// nodeRestartRequireLive fails the test unless the session's holder is live, which is the state a restart leaves it in.
func nodeRestartRequireLive(t *testing.T, stateDir string) {
	t.Helper()
	live, err := holder.Live(sessionDirOf(t, stateDir, nodeRestartSession))
	if err != nil {
		t.Fatalf("check the holder: %v", err)
	}
	if !live {
		t.Fatalf("holder of %s ended with the first agent, want it live", nodeRestartSession)
	}
}

// nodeRestartAwaitKept waits until the restarted agent's keeper reports that it attached to the session, so the
// controller reattaches only once the agent has found the holder.
func nodeRestartAwaitKept(t *testing.T, kept <-chan string) {
	t.Helper()
	record := <-kept
	if !strings.Contains(record, "session="+nodeRestartSession) {
		t.Fatalf("keeper attached in %q, want session %s", record, nodeRestartSession)
	}
}

// TestNodeRestartKeepsADetachedSession launches a session through an agent and reads its first turn, then detaches and
// stops the agent. The holder stays live. A restarted agent over the same state directory keeps the holder, and the
// controller reattaches through it at the offset it had read to, so the next turns continue stdout, each seen once.
func TestNodeRestartKeepsADetachedSession(t *testing.T) {
	stateDir, first, process, stdout := nodeRestartStart(t)
	nodeRestartTurn(t, process, stdout, "turn one")
	if err := process.Detach(); err != nil {
		t.Fatalf("detach before the restart: %v", err)
	}
	if err := first.stop(); err != nil {
		t.Fatalf("stop the first agent: %v", err)
	}
	nodeRestartRequireLive(t, stateDir)

	second := nodeRestartServe(t, stateDir, nodeRestartClaude(t))
	nodeRestartAwaitKept(t, second.kept)
	process, _, err := second.client().Reattach(t.Context(), nodeRestartSession, stdout.offset, holder.WireVersion)
	if err != nil {
		t.Fatalf("reattach through the restarted agent: %v", err)
	}
	stdout.r = process.Stdout()
	nodeRestartTurn(t, process, stdout, "turn two")
	nodeRestartTurn(t, process, stdout, "turn three")
	if err = process.Detach(); err != nil {
		t.Fatalf("detach the reattached session: %v", err)
	}
}

// TestNodeRestartResumesAnAttachedSession keeps the controller attached while its agent stops, so the agent's splice is
// cut under it. A turn it wrote that it had not read yet is spooled by the holder while no agent serves it. The
// restarted agent keeps the holder, and the controller reattaches at the offset it had read to, so the turn it had not
// read is delivered once, followed by the next turn.
func TestNodeRestartResumesAnAttachedSession(t *testing.T) {
	stateDir, first, process, stdout := nodeRestartStart(t)
	nodeRestartTurn(t, process, stdout, "turn one")
	if _, err := process.Stdin().Write([]byte("turn two\n")); err != nil {
		t.Fatalf("write turn two: %v", err)
	}
	if err := first.stop(); err != nil {
		t.Fatalf("stop the first agent: %v", err)
	}
	nodeRestartRequireLive(t, stateDir)
	if err := process.Detach(); err != nil {
		t.Fatalf("detach the controller of the first agent: %v", err)
	}

	second := nodeRestartServe(t, stateDir, nodeRestartClaude(t))
	nodeRestartAwaitKept(t, second.kept)
	process, _, err := second.client().Reattach(t.Context(), nodeRestartSession, stdout.offset, holder.WireVersion)
	if err != nil {
		t.Fatalf("reattach through the restarted agent: %v", err)
	}
	stdout.r = process.Stdout()
	stdout.expect(t, "turn two")
	nodeRestartTurn(t, process, stdout, "turn three")
	if err = process.Detach(); err != nil {
		t.Fatalf("detach the reattached session: %v", err)
	}
}
