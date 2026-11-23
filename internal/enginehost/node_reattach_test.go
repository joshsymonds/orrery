package enginehost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"testing"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// cutDialer dials the agent of b as net.Dialer does. It keeps the connections it dials, and records which of them
// attach a session's holder, so a test can cut the controller's links to b by closing those attach connections. The
// other connections, such as a link's end watch, stay open, so only the links drop.
type cutDialer struct {
	mu sync.Mutex
	// conns holds the connections dialed and not yet cut.
	conns []*dialedConn
	// attaches counts the connections whose first request is an attach.
	attaches int
	// down makes every dial fail as an unreachable host's does, while it is set.
	down bool
}

// dialedConn is a connection a cutDialer dialed. Its attach is set by its first write, when that write is an attach
// request, and its dialer then counts the attach.
type dialedConn struct {
	net.Conn
	dialer *cutDialer
	once   sync.Once
	attach bool
}

// Write sends p, and on the connection's first write, records whether its request is an attach.
func (c *dialedConn) Write(p []byte) (int, error) {
	c.once.Do(func() {
		if !isAttach(p) {
			return
		}
		c.dialer.mu.Lock()
		defer c.dialer.mu.Unlock()
		c.attach = true
		c.dialer.attaches++
	})
	return c.Conn.Write(p)
}

// isAttach reports whether p is the request line of an attach. A line that is not a request is not one.
func isAttach(p []byte) bool {
	var req node.Request
	if err := json.Unmarshal(p, &req); err != nil {
		return false
	}
	return req.Op == node.OpAttach
}

// setDown makes each later dial fail, as an unreachable host's does, when down is set.
func (d *cutDialer) setDown(down bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.down = down
}

// DialContext connects to address as net.Dialer does, and keeps the connection until a cut. While the dialer is down,
// it fails the dial as an unreachable host does.
func (d *cutDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	down := d.down
	d.mu.Unlock()
	if down {
		return nil, &net.OpError{Op: "dial", Net: network, Err: syscall.EHOSTUNREACH}
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	dialed := &dialedConn{Conn: conn, dialer: d}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.conns = append(d.conns, dialed)
	return dialed, nil
}

// attachCount returns how many attach connections have been begun so far.
func (d *cutDialer) attachCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.attaches
}

// cut closes every attach connection dialed since the last cut, so each link through them drops. A connection the far
// end has already closed is not an error.
func (d *cutDialer) cut(t *testing.T) {
	t.Helper()
	d.mu.Lock()
	var attached []*dialedConn
	var rest []*dialedConn
	for _, conn := range d.conns {
		if conn.attach {
			attached = append(attached, conn)
		} else {
			rest = append(rest, conn)
		}
	}
	d.conns = rest
	d.mu.Unlock()
	for _, conn := range attached {
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("cut a link to b: %v", err)
		}
	}
}

// reattachRig is a controller whose host b is a node.Client, reaching the agent of b through a cutDialer. The store and
// the writer outlive the managers a test starts over them, as a restarted orreryd's store does. The agent runs b's
// holders in stateB, under b's environment and the fake claude.
type reattachRig struct {
	s      *store.Store
	path   string
	writer *Writer
	// claude is the controller's claude, which does not exist, so no session of it can run on the controller.
	claude string
	// stateA and stateB are the state directories of the controller and of b.
	stateA, stateB string
	addr           string
	dial           *cutDialer
}

// newReattachRig starts the agent of b on a loopback listener, and returns the rig over a new store. The controller's
// claude does not exist, so no session of the controller can run.
func newReattachRig(t *testing.T) reattachRig {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for the agent of b: %v", err)
	}
	stateB := holderRoot(t)
	local := holderhost.NewLocal(holderhost.LocalConfig{
		Shell:    sh,
		Claude:   fakeClaudePath(t),
		Environ:  []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), nodeEnvB},
		StateDir: stateB,
	})
	agent, err := node.NewAgent(node.AgentConfig{
		Controller: nodeController,
		WhoIs:      nodeWhoIs{own: ln.Addr().String()},
		Holders:    local,
		StateDir:   stateB,
	})
	if err != nil {
		t.Fatalf("new agent of b: %v", err)
	}
	serveAgent(t, agent, ln)
	claude := filepath.Join(t.TempDir(), "no-claude")
	if _, err := os.Stat(claude); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the controller's claude %s exists or cannot be checked: %v, want it absent", claude, err)
	}
	s, path := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s})
	startRun(t, writer)
	return reattachRig{
		s: s, path: path, writer: writer, claude: claude, stateA: holderRoot(t), stateB: stateB,
		addr: ln.Addr().String(), dial: &cutDialer{},
	}
}

// client returns a node.Client that reaches the agent of b through the rig's dialer.
func (r reattachRig) client() *node.Client {
	return node.NewClient(node.ClientConfig{
		Name:    "b",
		Address: r.addr,
		Dial:    r.dial.DialContext,
		Clock:   neverClock{},
	})
}

// manager returns a controller manager over the rig's store that names b as its other host. Its Close runs at the end
// of the test.
func (r reattachRig) manager(t *testing.T) *Manager {
	t.Helper()
	return r.managerLogging(t, nil)
}

// managerLogging is manager with its diagnostics sent to logger, or discarded when logger is nil.
func (r reattachRig) managerLogging(t *testing.T, logger *slog.Logger) *Manager {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	m := NewManager(Config{
		Shell:    sh,
		Claude:   r.claude,
		Environ:  []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
		StateDir: r.stateA,
		HostName: "a",
		Hosts:    map[string]holderhost.Host{"b": r.client()},
		Writer:   r.writer,
		Now:      fixedClock,
		Store:    r.s,
		Logger:   logger,
	})
	t.Cleanup(m.Close)
	return m
}

// endOnB stops the session id on b at the end of the test, so no holder the test leaves running outlives it. The agent
// serves until the test's cleanups have run, so the stop reaches it.
func (r reattachRig) endOnB(t *testing.T, id string) {
	t.Helper()
	t.Cleanup(func() {
		err := r.client().Stop(context.WithoutCancel(t.Context()), id)
		if err != nil && !errors.Is(err, holder.ErrGone) {
			t.Errorf("stop session %s on b: %v", id, err)
		}
	})
}

// requireBlocks checks that the finished blocks stored for id are exactly texts, each once and in order.
func requireBlocks(t *testing.T, path, id string, texts ...string) {
	t.Helper()
	if got := blockTexts(t, historyOf(t, path, id)); !slices.Equal(got, texts) {
		t.Fatalf("finished blocks of %s = %q, want %q, each once", id, got, texts)
	}
}

func TestNodeReattachSessionOutlivesControllerRestart(t *testing.T) {
	t.Parallel()
	rig := newReattachRig(t)
	first := rig.manager(t)
	dir := t.TempDir()
	id, err := first.Start(t.Context(), StartRequest{Dir: dir, Prompt: nodePrompt, Host: "b"})
	if err != nil {
		t.Fatalf("Start on b: %v", err)
	}
	rig.endOnB(t, id)
	waitHistory(t, engineOf(first), rig.path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	pid := fakePID(t, dir)
	waitUntil(t, engineOf(first), "the first turn's end stored as the row's done state", func() bool {
		row, err := rig.s.EngineSession(t.Context(), id)
		return err == nil && row.State == "done"
	})

	first.Close()

	requireProcessRunning(t, pid)
	requireEngineRows(t, rig.s, map[string]restartRow{id: {state: "done"}})

	second := rig.manager(t)
	if err := second.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile of the new controller: %v", err)
	}
	requireProcessRunning(t, pid)
	if !slices.Contains(second.Live(), id) {
		t.Fatalf("session %s is not live after Reconcile: live %v", id, second.Live())
	}
	requireEngineRows(t, rig.s, map[string]restartRow{id: {state: "done"}})

	if err := second.Say(t.Context(), SayRequest{ID: id, Text: "again"}); err != nil {
		t.Fatalf("Say to the re-adopted session on b: %v", err)
	}
	waitHistory(t, engineOf(second), rig.path, id, "the second reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	requireProcessRunning(t, pid)
	requireBlocks(t, rig.path, id, "fake reply 1", "fake reply 2")
}

func TestNodeReattachSessionOutlivesDroppedLink(t *testing.T) {
	t.Parallel()
	rig := newReattachRig(t)
	m := rig.manager(t)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: nodePrompt, Host: "b"})
	if err != nil {
		t.Fatalf("Start on b: %v", err)
	}
	rig.endOnB(t, id)
	waitHistory(t, engineOf(m), rig.path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	pid := fakePID(t, dir)
	attaches := rig.dial.attachCount()

	rig.dial.cut(t)
	waittest.Until(t, nil, "the controller's link to b attaches again", func() bool {
		return rig.dial.attachCount() > attaches
	})

	requireProcessRunning(t, pid)
	if err := m.Say(t.Context(), SayRequest{ID: id, Text: "again"}); err != nil {
		t.Fatalf("Say over the re-attached link to b: %v", err)
	}
	waitHistory(t, engineOf(m), rig.path, id, "the second reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	waitUntil(t, engineOf(m), "the second turn's end stored as the row's done state", func() bool {
		row, err := rig.s.EngineSession(t.Context(), id)
		return err == nil && row.State == "done"
	})
	requireProcessRunning(t, pid)
	requireEngineRows(t, rig.s, map[string]restartRow{id: {state: "done"}})
	requireBlocks(t, rig.path, id, "fake reply 1", "fake reply 2")
}

func TestNodeReattachEndedSessionIsDrainedAfterRestart(t *testing.T) {
	t.Parallel()
	rig := newReattachRig(t)
	first := rig.manager(t)
	dir := t.TempDir()
	id, err := first.Start(t.Context(), StartRequest{Dir: dir, Prompt: nodePrompt, Host: "b"})
	if err != nil {
		t.Fatalf("Start on b: %v", err)
	}
	waitHistory(t, engineOf(first), rig.path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	pid := fakePID(t, dir)

	first.Close()
	requireProcessRunning(t, pid)
	// The session's holder on b ends while no controller attaches to it.
	if err := rig.client().Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop of the session on b: %v", err)
	}
	requireGone(t, pid)

	// A drain that failed would log a warning, and the holder would be cleared all the same, so the warnings are checked.
	var warnings bytes.Buffer
	warn := slog.New(slog.NewTextHandler(&warnings, &slog.HandlerOptions{Level: slog.LevelWarn}))
	second := rig.managerLogging(t, warn)
	if err := second.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile of the new controller: %v", err)
	}
	if got := warnings.String(); got != "" {
		t.Fatalf("Reconcile of the ended session logged warnings:\n%s", got)
	}
	requireDirGone(t, holderDir(t, rig.stateB, id))
	if slices.Contains(second.Live(), id) {
		t.Fatalf("session %s is live after Reconcile, want it drained", id)
	}
	requireEngineRows(t, rig.s, map[string]restartRow{id: {state: "done"}})
	requireBlocks(t, rig.path, id, "fake reply 1")
}

func TestNodeReattachUnreachableHostLeavesSessionAlone(t *testing.T) {
	t.Parallel()
	rig := newReattachRig(t)
	first := rig.manager(t)
	dir := t.TempDir()
	id, err := first.Start(t.Context(), StartRequest{Dir: dir, Prompt: "STALL", Host: "b"})
	if err != nil {
		t.Fatalf("Start on b: %v", err)
	}
	rig.endOnB(t, id)
	waitHistory(
		t,
		engineOf(first),
		rig.path,
		id,
		"the stalled block's unfinished text",
		func(h store.EngineHistory) bool {
			return len(h.Unfinished) == 1
		},
	)
	pid := fakePID(t, dir)

	first.Close()
	requireProcessRunning(t, pid)

	// The controller restarts while b cannot be reached, so its holders cannot be listed.
	rig.dial.setDown(true)
	down := rig.manager(t)
	if err := down.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile while b is unreachable: %v", err)
	}
	requireProcessRunning(t, pid)
	requireEngineRows(t, rig.s, map[string]restartRow{id: {state: "working"}})
	if n := countRows(t, rig.path, "engine_text"); n != 1 {
		t.Fatalf("unfinished text rows while b is unreachable = %d, want the stalled block's 1", n)
	}

	// b is reachable again, so the next controller re-adopts the session.
	rig.dial.setDown(false)
	up := rig.manager(t)
	if err := up.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile once b is reachable: %v", err)
	}
	requireProcessRunning(t, pid)
	if !slices.Contains(up.Live(), id) {
		t.Fatalf("session %s is not live after Reconcile once b is reachable: live %v", id, up.Live())
	}
	requireEngineRows(t, rig.s, map[string]restartRow{id: {state: "working"}})
}
