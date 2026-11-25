package enginehost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"os"
	"os/exec"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// unreachableBound is the Bound of the Client to b in these tests. Its clock moves only when a test advances it, so the
// Client's waits end when the test says so.
const unreachableBound = 15 * time.Second

// hostClock is the clock of the Client to b. Its timers fire only when a test advances the clock past them, so each wait of
// the Client ends at the test's word, and a wait the test does not end never ends.
type hostClock struct {
	mu  sync.Mutex
	now time.Duration
	// waiters holds the timers that have not fired, each with the clock time it fires at.
	waiters []hostTimer
}

// hostTimer is a timer of a hostClock: it fires once the clock reaches at.
type hostTimer struct {
	at time.Duration
	c  chan time.Time
}

// After sets a timer of duration d, and returns the channel it fires on. A timer of no duration has fired already.
func (c *hostClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- time.Time{}
		return ch
	}
	c.waiters = append(c.waiters, hostTimer{at: c.now + d, c: ch})
	return ch
}

// advance moves the clock on by d, and fires each timer it passes.
func (c *hostClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now += d
	pending := c.waiters[:0]
	for _, w := range c.waiters {
		if w.at <= c.now {
			w.c <- time.Time{}
			continue
		}
		pending = append(pending, w)
	}
	c.waiters = pending
}

// hostMode is how the controller reaches b in a test: through the tailnet to b's agent, through a refused connection, or to
// an agent that accepts each connection and never answers it.
type hostMode int

const (
	hostUp hostMode = iota
	hostRefused
	hostHung
)

// hostDialer dials the agent of b as the mode it is set to says. A refused dial fails as a refused connection does, and
// while the dialer is armed, each refused dial signals on stalls. A hung dial reaches the hungAgent.
type hostDialer struct {
	agent  string
	hung   string
	stalls chan<- struct{}
	mu     sync.Mutex
	mode   hostMode
	armed  bool
}

// DialContext dials the agent of b as the mode says, whatever address the Client names.
func (d *hostDialer) DialContext(ctx context.Context, network, _ string) (net.Conn, error) {
	d.mu.Lock()
	mode, armed := d.mode, d.armed
	d.mu.Unlock()
	switch mode {
	case hostRefused:
		if armed {
			d.stalls <- struct{}{}
		}
		return nil, &net.OpError{Op: "dial", Net: network, Err: syscall.ECONNREFUSED}
	case hostHung:
		return (&net.Dialer{}).DialContext(ctx, network, d.hung)
	case hostUp:
	}
	return (&net.Dialer{}).DialContext(ctx, network, d.agent)
}

// setMode sets how each later dial reaches b.
func (d *hostDialer) setMode(mode hostMode) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.mode = mode
}

// setArmed arms or disarms the signal of each refused dial.
func (d *hostDialer) setArmed(armed bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.armed = armed
}

// startHungAgent serves on a loopback listener an agent that accepts each connection, reads its request, and never answers.
// Each launch it reads signals on stalls. The listener and its connections end when the test ends.
func startHungAgent(t *testing.T, stalls chan<- struct{}) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for the hung agent: %v", err)
	}
	done := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go holdRequest(conn, stalls, done)
		}
	}()
	t.Cleanup(func() {
		close(done)
		if err := ln.Close(); err != nil {
			t.Errorf("close the hung agent: %v", err)
		}
	})
	return ln.Addr().String()
}

// holdRequest reads the request line of conn. A launch signals on stalls, and conn then stays open until done closes.
func holdRequest(conn net.Conn, stalls chan<- struct{}, done <-chan struct{}) {
	defer func() { _ = conn.Close() }()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req node.Request
	if json.Unmarshal(line, &req) == nil && req.Op == node.OpLaunch {
		select {
		case stalls <- struct{}{}:
		case <-done:
			return
		}
	}
	<-done
}

// unreachableRig is a controller whose host b is a node Client, reaching the agent of b through a hostDialer on a hostClock.
// The agent runs b's holders in stateB, under b's environment and the fake claude. The controller runs the fake claude too.
// The store and the writer outlive the managers a test starts over them, as a restarted orreryd's store does.
type unreachableRig struct {
	s      *store.Store
	path   string
	writer *Writer
	// stateA and stateB are the state directories of the controller and of b.
	stateA, stateB string
	// agent is the address of b's agent.
	agent string
	clock *hostClock
	dial  *hostDialer
	// stalls receives a signal for each stalled call that reaches b, as startStalled waits for.
	stalls chan struct{}
	client *node.Client
	// started holds the sessions the test starts on b (see endOnB).
	started *startedOnB
}

// newUnreachableRig starts the agent of b on a loopback listener, and returns the rig over a new store. The Client to b is
// up, and it has armed its pings on the rig's clock, so Bound counts from here.
func newUnreachableRig(t *testing.T) unreachableRig {
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
	stalls := make(chan struct{}, 16)
	dial := &hostDialer{agent: ln.Addr().String(), hung: startHungAgent(t, stalls), stalls: stalls}
	clock := &hostClock{}
	client := node.NewClient(node.ClientConfig{
		Name:    "b",
		Address: ln.Addr().String(),
		Dial:    dial.DialContext,
		Bound:   unreachableBound,
		Clock:   clock,
	})
	client.Reach()
	s, path := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s})
	startRun(t, writer)
	rig := unreachableRig{
		s: s, path: path, writer: writer, stateA: holderRoot(t), stateB: stateB, agent: ln.Addr().String(),
		clock: clock, dial: dial, stalls: stalls, client: client, started: &startedOnB{},
	}
	// Registered before any manager, so it runs after each manager's Close, and before the agent stops serving.
	t.Cleanup(func() { rig.stopStarted(t) })
	return rig
}

// setMode sets how the controller reaches b from now on.
func (r unreachableRig) setMode(mode hostMode) {
	r.dial.setMode(mode)
}

// advance moves the rig's clock past Bound, so each wait of the Client on the clock that began before it ends.
func (r unreachableRig) advance() {
	r.clock.advance(unreachableBound)
}

// manager returns a controller manager over the rig's store that names b as its other host, and whose notices go to
// notifier, or nowhere when notifier is nil. Its Close runs at the end of the test.
func (r unreachableRig) manager(t *testing.T, notifier NoticeSink) *Manager {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	m := NewManager(Config{
		Shell:    sh,
		Claude:   fakeClaudePath(t),
		Environ:  []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
		StateDir: r.stateA,
		HostName: "a",
		Hosts:    map[string]holderhost.Host{"b": r.client},
		Writer:   r.writer,
		Now:      fixedClock,
		Store:    r.s,
		Notifier: notifier,
	})
	t.Cleanup(m.Close)
	return m
}

// endOnB records that the test starts the session id on b. The rig stops each recorded session at the end of the test, after
// the managers have closed, so no holder the test leaves running outlives it (see stopStarted).
func (r unreachableRig) endOnB(t *testing.T, id string) {
	t.Helper()
	r.started.mu.Lock()
	defer r.started.mu.Unlock()
	r.started.ids = append(r.started.ids, id)
}

// stopStarted stops each session the test started on b, through a Client that reaches the agent directly. It runs once the
// managers have closed, so each session has been detached from its controller, and its stop ends the holder that nothing
// reads. A stop that came first would end the holder while its controller still read the stream, and the end watch of a
// session whose ask a refused or hung dial failed would then wait on the clock, which the test no longer moves, to learn of
// the end.
func (r unreachableRig) stopStarted(t *testing.T) {
	t.Helper()
	r.setMode(hostUp)
	r.started.mu.Lock()
	ids := slices.Clone(r.started.ids)
	r.started.mu.Unlock()
	client := node.NewClient(node.ClientConfig{
		Name: "b", Address: r.agent, Dial: (&net.Dialer{}).DialContext, Clock: neverClock{},
	})
	for _, id := range ids {
		err := client.Stop(context.WithoutCancel(t.Context()), id)
		if err != nil && !errors.Is(err, holder.ErrGone) {
			t.Errorf("stop session %s on b: %v", id, err)
		}
	}
}

// startedOnB holds the ids of the sessions a test starts on b, which the rig stops at the end of the test (see stopStarted).
type startedOnB struct {
	mu  sync.Mutex
	ids []string
}

// startStalled runs call in its own goroutine, and returns once call has reached b: a refused dial the armed dialer
// signaled, or a launch the hung agent received. The result of call arrives on the returned channel.
func (r unreachableRig) startStalled(t *testing.T, call func() error) <-chan error {
	t.Helper()
	r.dial.setArmed(true)
	result := make(chan error, 1)
	go func() { result <- call() }()
	<-r.stalls
	r.dial.setArmed(false)
	return result
}

// awaitReach waits until the Client reads want: unreachable once the clock has passed Bound with no ping answered, and
// reachable again once a ping is answered.
func awaitReach(t *testing.T, client *node.Client, want bool) {
	t.Helper()
	for {
		reachable, changed := client.Reach()
		if reachable == want {
			return
		}
		<-changed
	}
}

// startReplied starts a session as req describes on m, and waits until its first reply is stored. A session on b ends at the
// end of the test.
func startReplied(t *testing.T, rig unreachableRig, m *Manager, req StartRequest) string {
	t.Helper()
	id, err := m.Start(t.Context(), req)
	if err != nil {
		t.Fatalf("Start on %q: %v", req.Host, err)
	}
	if req.Host == "b" {
		rig.endOnB(t, id)
	}
	waitHistory(t, engineOf(m), rig.path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	return id
}

// engineRowsOf returns the engine rows of ids, by id, and fails the test when one cannot be read.
func engineRowsOf(t *testing.T, s *store.Store, ids ...string) map[string]store.EngineRow {
	t.Helper()
	rows := make(map[string]store.EngineRow, len(ids))
	for _, id := range ids {
		row, err := s.EngineSession(t.Context(), id)
		if err != nil {
			t.Fatalf("engine row %s: %v", id, err)
		}
		rows[id] = row
	}
	return rows
}

// requireRows checks that the rows read after are the rows read before, each unchanged.
func requireRows(t *testing.T, before, after map[string]store.EngineRow) {
	t.Helper()
	if !maps.Equal(before, after) {
		t.Fatalf("engine rows changed:\nbefore %+v\nafter  %+v", before, after)
	}
}

// requireRowOn checks that the engine row of id names host and is in state.
func requireRowOn(t *testing.T, s *store.Store, id, host, state string) {
	t.Helper()
	row := engineRowsOf(t, s, id)[id]
	if row.Host != host || row.State != state {
		t.Fatalf("row %s has host %q state %q, want host %q state %q", id, row.Host, row.State, host, state)
	}
}

// runHostDown takes b down as down says, and checks what the controller does while b is down: once the clock passes Bound, the
// sessions on b read unreachable, no Start on b succeeds, none of them moves host, and a Start and a Say on the controller's
// own host complete while b refuses or hangs. A Start stalled on b when it went down ends recorded failed on b.
func runHostDown(t *testing.T, down hostMode) {
	t.Helper()
	rig := newUnreachableRig(t)
	m := rig.manager(t, nil)

	liveDir := t.TempDir()
	live := startReplied(t, rig, m, StartRequest{Dir: liveDir, Prompt: nodePrompt, Host: "b"})
	stopped := startReplied(t, rig, m, StartRequest{Dir: t.TempDir(), Prompt: nodePrompt, Host: "b"})
	if err := m.Stop(t.Context(), stopped); err != nil {
		t.Fatalf("Stop of the session on b: %v", err)
	}
	local := startReplied(t, rig, m, StartRequest{Dir: t.TempDir(), Prompt: nodePrompt})
	if err := m.Stop(t.Context(), local); err != nil {
		t.Fatalf("Stop of the session on the controller: %v", err)
	}

	rig.setMode(down)
	// A Start on b stalls: its launch reaches b and takes no answer. It holds b's lock while it does, so nothing else on b
	// starts meanwhile, but the controller's own host is not held up by it.
	const stalledStart = "0123456789abcdef0123456789abcdef"
	stalledStartDir := t.TempDir()
	startResult := rig.startStalled(t, func() error {
		_, err := m.Start(t.Context(), StartRequest{
			ID: stalledStart, Dir: stalledStartDir, Prompt: nodePrompt, Host: "b",
		})
		return err
	})

	// The controller's own host completes meanwhile: a Say to its stopped session, and a Start of a new session.
	if err := m.Say(t.Context(), SayRequest{ID: local, Text: "again"}); err != nil {
		t.Fatalf("Say on the controller's own host while b is down: %v", err)
	}
	waitHistory(t, engineOf(m), rig.path, local, "the reply to the Say on the controller",
		func(h store.EngineHistory) bool {
			return hasBlock(t, h, "fake reply 2")
		})
	startReplied(t, rig, m, StartRequest{Dir: t.TempDir(), Prompt: nodePrompt})

	// The stalled launch runs out of Bound, so the Start is recorded failed on b, and it returns no error.
	rig.advance()
	awaitReach(t, rig.client, false)
	if err := <-startResult; err != nil {
		t.Fatalf("Start on b whose launch ran out of Bound = %v, want it recorded failed with no error", err)
	}
	requireRowOn(t, rig.s, stalledStart, "b", "failed")

	// Now that b reads unreachable, a Say to the stopped session on b is refused before its resume reaches b.
	if err := m.Say(t.Context(), SayRequest{ID: stopped, Text: "again"}); !errors.Is(err, ErrHostUnreachable) {
		t.Fatalf("Say to the stopped session on b = %v, want an error matching ErrHostUnreachable", err)
	}

	// Each session on b reads unreachable, and the controller's own session reads reachable, each on its own host.
	for _, id := range []string{live, stopped, stalledStart} {
		got, err := m.Reach(t.Context(), id)
		if err != nil {
			t.Fatalf("Reach of session %s: %v", id, err)
		}
		if got.Host != "b" || got.Reachable {
			t.Fatalf("Reach of session %s = %+v, want host b unreachable", id, got)
		}
	}
	got, err := m.Reach(t.Context(), local)
	if err != nil {
		t.Fatalf("Reach of the controller's session: %v", err)
	}
	if got.Host != "a" || !got.Reachable {
		t.Fatalf("Reach of the controller's session = %+v, want host a reachable", got)
	}

	// No Start on b succeeds, and nothing is stored for it.
	const refused = "fedcba9876543210fedcba9876543210"
	_, startErr := m.Start(t.Context(), StartRequest{ID: refused, Dir: t.TempDir(), Prompt: nodePrompt, Host: "b"})
	if !errors.Is(startErr, ErrHostUnreachable) {
		t.Fatalf("Start on b while it is unreachable = %v, want an error matching ErrHostUnreachable", startErr)
	}
	if _, err := rig.s.EngineSession(t.Context(), refused); !errors.Is(err, store.ErrNoEngineSession) {
		t.Fatalf("row of the refused Start = %v, want none stored", err)
	}

	// A Say to a session on b fails, and each session stays where it was: its row is unchanged, and no holder of it is on the
	// controller.
	before := engineRowsOf(t, rig.s, live, stopped)
	for _, id := range []string{live, stopped} {
		if err := m.Say(t.Context(), SayRequest{ID: id, Text: "again"}); !errors.Is(err, ErrHostUnreachable) {
			t.Fatalf("Say to session %s on b = %v, want an error matching ErrHostUnreachable", id, err)
		}
	}
	requireRows(t, before, engineRowsOf(t, rig.s, live, stopped))
	requireDirGone(t, holderDir(t, rig.stateA, live))
	requireDirGone(t, holderDir(t, rig.stateA, stopped))

	// The live session keeps its process and its holder on b.
	requireProcessRunning(t, fakePID(t, liveDir))
	if _, err := os.Stat(holderDir(t, rig.stateB, live)); err != nil {
		t.Fatalf("holder of the live session on b: %v", err)
	}

	// The controller's own host still starts a session while b is unreachable.
	startReplied(t, rig, m, StartRequest{Dir: t.TempDir(), Prompt: nodePrompt})
}

// TestNodeUnreachableRefusedHostRefusesStartAndSay takes b down by refused connections. Its sessions read unreachable once the
// clock passes Bound, and nothing on b starts or moves while the controller's own host goes on.
func TestNodeUnreachableRefusedHostRefusesStartAndSay(t *testing.T) {
	t.Parallel()
	runHostDown(t, hostRefused)
}

// TestNodeUnreachableHungHostRefusesStartAndSay takes b down by an agent that accepts each connection and never answers. Its
// sessions read unreachable once the clock passes Bound, and nothing on b starts or moves while the controller's own host goes
// on.
func TestNodeUnreachableHungHostRefusesStartAndSay(t *testing.T) {
	t.Parallel()
	runHostDown(t, hostHung)
}

// TestNodeUnreachableControllerStartedWhileDownReAdopts starts a controller while b is unreachable. It leaves b's sessions and
// the row of a host this controller does not run sessions on unsettled and unresumed. When b returns, it re-adopts b's sessions
// at their stored offsets, and each line is stored once.
func TestNodeUnreachableControllerStartedWhileDownReAdopts(t *testing.T) {
	t.Parallel()
	rig := newUnreachableRig(t)
	first := rig.manager(t, nil)
	doneDir, stallDir := t.TempDir(), t.TempDir()
	done := startReplied(t, rig, first, StartRequest{Dir: doneDir, Prompt: nodePrompt, Host: "b"})
	stalled, err := first.Start(t.Context(), StartRequest{Dir: stallDir, Prompt: "STALL", Host: "b"})
	if err != nil {
		t.Fatalf("Start of the stalled session on b: %v", err)
	}
	rig.endOnB(t, stalled)
	waitHistory(t, engineOf(first), rig.path, stalled, "the stalled block's unfinished text",
		func(h store.EngineHistory) bool {
			return len(h.Unfinished) == 1
		})
	donePID, stallPID := fakePID(t, doneDir), fakePID(t, stallDir)
	first.Close()
	requireProcessRunning(t, donePID)
	requireProcessRunning(t, stallPID)

	// b goes down while no controller runs. A row also names a host this controller does not run sessions on.
	rig.setMode(hostRefused)
	rig.advance()
	awaitReach(t, rig.client, false)
	const unknown = "00112233445566778899aabbccddeeff"
	if err := rig.writer.Commit(t.Context(), unknown, store.EngineAdd{
		ID: unknown, Dir: t.TempDir(), State: "working", Kind: store.KindEngine, Host: "c",
	}); err != nil {
		t.Fatalf("store the row of host c: %v", err)
	}
	ids := []string{done, stalled, unknown}
	before := engineRowsOf(t, rig.s, ids...)

	sink := &fakeSink{}
	down := rig.manager(t, sink)
	if err := down.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile while b is unreachable: %v", err)
	}
	requireRows(t, before, engineRowsOf(t, rig.s, ids...))
	requireProcessRunning(t, donePID)
	requireProcessRunning(t, stallPID)
	if live := down.Live(); len(live) != 0 {
		t.Fatalf("sessions live while b is unreachable: %v, want none", live)
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices while b is unreachable = %+v, want none", got)
	}
	if n := stdinLinesWith(t, stallDir, resumePrompt); n != 0 {
		t.Fatalf("the stalled session was resumed while b is unreachable: %d resume prompts", n)
	}
	if n := countRows(t, rig.path, "engine_text"); n != 1 {
		t.Fatalf("unfinished text rows while b is unreachable = %d, want the stalled block's 1", n)
	}
	reach, err := down.Reach(t.Context(), done)
	if err != nil {
		t.Fatalf("Reach of session %s while b is unreachable: %v", done, err)
	}
	if reach.Host != "b" || reach.Reachable {
		t.Fatalf("Reach of session %s while b is unreachable = %+v, want host b unreachable", done, reach)
	}
	reach, err = down.Reach(t.Context(), unknown)
	if err != nil {
		t.Fatalf("Reach of the row of host c: %v", err)
	}
	if reach.Host != "c" || reach.Reachable {
		t.Fatalf("Reach of the row of host c = %+v, want host c unreachable", reach)
	}

	// b returns. The controller re-adopts b's sessions on its own, at their stored offsets.
	rig.setMode(hostUp)
	rig.advance()
	awaitReach(t, rig.client, true)
	waittest.Until(t, nil, "the controller re-adopts the sessions on b", func() bool {
		live := down.Live()
		return slices.Contains(live, done) && slices.Contains(live, stalled)
	})
	requireProcessRunning(t, donePID)
	requireProcessRunning(t, stallPID)
	requireRows(t, before, engineRowsOf(t, rig.s, ids...))
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices after the re-adoption = %+v, want none", got)
	}
	if n := stdinLinesWith(t, stallDir, resumePrompt); n != 0 {
		t.Fatalf("the stalled session was resumed after the re-adoption: %d resume prompts", n)
	}

	// The re-adopted session answers, and its earlier reply is not stored again.
	if err := down.Say(t.Context(), SayRequest{ID: done, Text: "again"}); err != nil {
		t.Fatalf("Say to the re-adopted session on b: %v", err)
	}
	waitHistory(t, engineOf(down), rig.path, done, "the second reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	requireBlocks(t, rig.path, done, "fake reply 1", "fake reply 2")
	if n := countRows(t, rig.path, "engine_text"); n != 1 {
		t.Fatalf("unfinished text rows after the re-adoption = %d, want the stalled block's 1", n)
	}
}
