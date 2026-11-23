package hostfollow_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/hostfollow"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/panehost"
	"github.com/joshsymonds/orrery/internal/paneregistry"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
)

const (
	// controller is the controller's node name on the tailnet, and its orrery name.
	controller = "controller"
	// remote is the orrery name of the host whose panes are followed.
	remote = "b"
	// login is the owner's login, which the fake WhoIs names every peer under.
	login = "owner@example.test"
	// clientName is the X-Orrery-Client header the test's Panes calls send.
	clientName = "synthetic-client"
	// noConfig keeps the user's tmux config away from every test server.
	noConfig = "/dev/null"
	// testCommand is the command of each test pane. It outlives the test, so the pane stays.
	testCommand = "sleep 300"
	// synthetic is the pid of the Claude process that a registry entry names, and sentinelPID is the pid of the one that
	// a test's last registry entry names.
	synthetic   = 4242
	sentinelPID = 4243
	// retryDelay is how long the Follow under test waits before a new watch. It is the client's bound in production.
	retryDelay = node.DefaultBound
	// armedMax bounds how many armed timers a fake clock holds before a test reads them.
	armedMax = 256
	// writesMax bounds how many store writes a test holds before it reads them.
	writesMax = 64
)

// fixedNow is the clock of each test's writes, so no row is stamped with a real time.
func fixedNow() time.Time { return time.Unix(1_700_000_000, 0) }

// fakeProc is a paneregistry.Proc in which every pid is alive, and the process of each pid has env[pid] as its
// environment.
type fakeProc struct {
	env map[int]map[string]string
}

// Alive reports that every pid is alive.
func (fakeProc) Alive(int) bool { return true }

// Environ returns the environment of the process of pid.
func (p fakeProc) Environ(pid int) (map[string]string, error) { return p.env[pid], nil }

// fixedWhoIs names every address as the controller, signed in under the owner's login.
type fixedWhoIs struct{}

// WhoIs returns the controller's node under the owner's login.
func (fixedWhoIs) WhoIs(context.Context, string) (*apitype.WhoIsResponse, error) {
	return &apitype.WhoIsResponse{
		Node:        &tailcfg.Node{ComputedName: controller, Name: controller + ".tail1234.ts.net."},
		UserProfile: &tailcfg.UserProfile{LoginName: login},
	}, nil
}

// fakeClock is a holder.Clock whose timers fire only when Advance moves it past their deadlines. Each timer it arms is
// announced on armed, so a test waits on armed for a timer before it advances the clock.
type fakeClock struct {
	armed chan time.Duration

	mu      sync.Mutex
	now     time.Duration
	waiters []fakeWaiter
}

// fakeWaiter is one timer of a fakeClock. It fires once the clock reaches deadline.
type fakeWaiter struct {
	deadline time.Duration
	fire     chan time.Time
}

// newFakeClock returns a fakeClock with no timers.
func newFakeClock() *fakeClock {
	return &fakeClock{armed: make(chan time.Duration, armedMax)}
}

// After arms a timer that fires once the clock has moved d past its time at the call.
func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	fire := make(chan time.Time, 1)
	c.waiters = append(c.waiters, fakeWaiter{deadline: c.now + d, fire: fire})
	c.armed <- d
	return fire
}

// Advance moves the clock forward by d, and fires each timer whose deadline has then passed.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now += d
	pending := c.waiters[:0]
	for _, waiter := range c.waiters {
		if waiter.deadline > c.now {
			pending = append(pending, waiter)
			continue
		}
		waiter.fire <- time.Time{}
	}
	c.waiters = pending
}

// awaitArmed waits until clock has armed a timer of duration d. Timers of other durations armed before it are read
// past.
func awaitArmed(clock *fakeClock, d time.Duration) {
	for armed := range clock.armed {
		if armed == d {
			return
		}
	}
}

// discardArmed drops the durations that clock has armed so far, for a test that does not wait on them.
func (c *fakeClock) discardArmed() {
	for {
		select {
		case <-c.armed:
		default:
			return
		}
	}
}

// joinedHost is the pane source of a test agent. Its Watch reports on the agent's joined channel once the first event
// of each watch is sent. That event is the full listing, which the watch queues only after it has joined the host, so a
// change made after joined is delivered to the watch.
type joinedHost struct {
	*panehost.Host
	joined chan<- struct{}
}

// Watch runs the host's watch, and reports on joined when its first event is sent.
func (h joinedHost) Watch(ctx context.Context, send func(node.PaneEvent) error) error {
	var once sync.Once
	return h.Host.Watch(ctx, func(event node.PaneEvent) error {
		once.Do(func() { h.joined <- struct{}{} })
		return send(event)
	})
}

// tmuxClient returns the client of server.
func tmuxClient(t *testing.T, server tmuxctl.Server) *tmuxctl.Client {
	t.Helper()
	client, err := tmuxctl.New(server)
	if err != nil {
		t.Fatalf("New(%+v): %v", server, err)
	}
	return client
}

// startTmux starts a private tmux server with one session, kills it at cleanup, and returns it by its socket and the id
// of its one pane.
func startTmux(t *testing.T) (tmuxctl.Server, string) {
	t.Helper()
	return startTmuxServer(t, func(err error) {
		if err != nil {
			t.Errorf("kill-server: %v", err)
		}
	})
}

// startExitingTmux is startTmux for a server that the test ends by killing its last pane, so the server has already
// exited when cleanup asks it to stop. That report is logged, and is not a failure.
func startExitingTmux(t *testing.T) (tmuxctl.Server, string) {
	t.Helper()
	return startTmuxServer(t, func(err error) {
		if err != nil {
			t.Logf("kill-server: the server had exited with its last pane: %v", err)
		}
	})
}

// startTmuxServer starts a private tmux server with one session, and kills it at cleanup. It passes the error of the
// kill to stopped, and returns the server by its socket and the id of its one pane.
func startTmuxServer(t *testing.T, stopped func(error)) (tmuxctl.Server, string) {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	named := tmuxctl.Server{Name: fmt.Sprintf("orrery-hostfollow-test-%d-%x", os.Getpid(), suffix)}
	client := tmuxClient(t, named)
	t.Cleanup(func() { stopped(tmuxtest.Stop(t, named)) })
	if _, err := client.Run(t.Context(), "-f", noConfig, "new-session", "-d", "-s", "s1",
		"-c", t.TempDir(), testCommand); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	socket, err := client.Run(t.Context(), "display", "-p", "#{socket_path}")
	if err != nil {
		t.Fatalf("display socket_path: %v", err)
	}
	pane, err := client.Run(t.Context(), "list-panes", "-t", "s1", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	return tmuxctl.Server{Socket: strings.TrimSpace(socket)}, strings.TrimSpace(pane)
}

// splitPane splits first on server, and returns the id of the new pane.
func splitPane(t *testing.T, server tmuxctl.Server, first string) string {
	t.Helper()
	out, err := tmuxClient(t, server).Run(t.Context(), "split-window", "-P", "-F", "#{pane_id}", "-t", first,
		testCommand)
	if err != nil {
		t.Fatalf("split-window: %v", err)
	}
	return strings.TrimSpace(out)
}

// killPane kills pane on server.
func killPane(t *testing.T, server tmuxctl.Server, pane string) {
	t.Helper()
	if _, err := tmuxClient(t, server).Run(t.Context(), "kill-pane", "-t", pane); err != nil {
		t.Fatalf("kill-pane: %v", err)
	}
}

// paneEnv returns the process environments that bind pane on server to the registry entry of pid: the process of pid
// runs in that pane, and names the server's socket.
func paneEnv(pid int, server tmuxctl.Server, pane string) map[int]map[string]string {
	return map[int]map[string]string{pid: {"TMUX": server.Socket + ",1,0", "TMUX_PANE": pane}}
}

// waitingEntry returns the registry entry of the session of pid, which waits for permission. Writing it reports the
// state of the pane that pid runs in as waiting, a change from the state of an unbound pane.
func waitingEntry(t *testing.T, pid int) paneregistry.Entry {
	t.Helper()
	return paneregistry.Entry{
		PID:        pid,
		SessionID:  fmt.Sprintf("synthetic-session-%d", pid),
		Cwd:        t.TempDir(),
		Entrypoint: paneregistry.EntrypointCLI,
		Status:     paneregistry.StatusWaiting,
		WaitingFor: paneregistry.WaitingPermission,
	}
}

// writeEntry writes entry into dir as the registry file <pid>.json. It renames the file into place, so the watch reads
// the entry whole.
func writeEntry(t *testing.T, dir string, entry paneregistry.Entry) {
	t.Helper()
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	name := filepath.Join(dir, fmt.Sprintf("%d.json", entry.PID))
	if err = os.WriteFile(name+".tmp", data, 0o600); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	if err = os.Rename(name+".tmp", name); err != nil {
		t.Fatalf("rename entry: %v", err)
	}
}

// startPaneHost runs the Host of remote's panes over server and the registry in dir, whose processes have env as their
// environments, until the test ends. Its listings are sent on ticks.
func startPaneHost(
	t *testing.T, server tmuxctl.Server, dir string, env map[int]map[string]string, ticks <-chan time.Time,
) *panehost.Host {
	t.Helper()
	host, err := panehost.New(panehost.Config{
		Name:          remote,
		Dir:           dir,
		Home:          t.TempDir(),
		Proc:          fakeProc{env: env},
		DefaultServer: server,
		Ticks:         ticks,
		Load:          func() (node.Load, error) { return node.Load{}, nil },
		Now:           fixedNow,
	})
	if err != nil {
		t.Fatalf("panehost.New: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- host.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run: %v", err)
		}
	})
	return host
}

// testAgent is a node.Agent that serves a host's panes in a test. joined receives once the watch of each connection
// has joined the host.
type testAgent struct {
	addr   string
	stop   func()
	joined <-chan struct{}
}

// serveAgent serves a node.Agent over host's panes on addr, until the agent is stopped or the test ends. It returns the
// agent, whose address is the one it listens on. Its stop ends the agent and waits for it to return, and a second stop
// does nothing.
func serveAgent(t *testing.T, host *panehost.Host, addr string) testAgent {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatalf("listen on %s: %v", addr, err)
	}
	stateDir := t.TempDir()
	joined := make(chan struct{}, 8)
	agent, err := node.NewAgent(node.AgentConfig{
		Controller: controller,
		WhoIs:      fixedWhoIs{},
		Holders:    holderhost.NewLocal(holderhost.LocalConfig{StateDir: stateDir}),
		StateDir:   stateDir,
		Panes:      joinedHost{Host: host, joined: joined},
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- agent.Serve(ctx, ln) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			if err := <-served; err != nil {
				t.Errorf("serve the agent: %v", err)
			}
		})
	}
	t.Cleanup(stop)
	return testAgent{addr: ln.Addr().String(), stop: stop, joined: joined}
}

// nodeClient returns the client of remote's agent at addr, whose waits run on clock.
func nodeClient(addr string, clock holder.Clock) *node.Client {
	var dialer net.Dialer
	return node.NewClient(node.ClientConfig{Name: remote, Address: addr, Dial: dialer.DialContext, Clock: clock})
}

// openStore seeds a store at a temporary path, opens it, and closes it at cleanup. It returns the store and its path.
func openStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	st, err := store.Open(t.Context(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close store: %v", err)
		}
	})
	return st, path
}

// watchWrites makes st report the sessions of each write that names one, in commit order, on the returned channel. A
// test waits on the channel for a write, so the rows it reads are the ones that write committed.
func watchWrites(st *store.Store) <-chan []string {
	writes := make(chan []string, writesMax)
	st.OnSessionEvents(func(_ int64, sessions []string) { writes <- sessions })
	return writes
}

// startFollow runs hostfollow.Follow for remote over agent into st, with retry as its retry clock, until the test ends.
func startFollow(t *testing.T, st *store.Store, agent *node.Client, retry holder.Clock) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		hostfollow.Follow(ctx, hostfollow.Config{
			Host: remote, Client: agent, Store: st, Now: fixedNow, Retry: retry,
		})
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// storedRows returns the pane rows stored under host, read from the store file at path.
func storedRows(t *testing.T, path, host string) []store.Session {
	t.Helper()
	rows, err := store.NewReader(path).HostPanes(t.Context(), host)
	if err != nil {
		t.Fatalf("read the panes of %s: %v", host, err)
	}
	return rows
}

// storedIDs returns the @orrery_id of each pane row stored under host, by the row's tmux pane id. Each tmux server
// numbers its panes from its own counter, so the map is for a test whose panes sit on one server.
func storedIDs(t *testing.T, path, host string) map[string]string {
	t.Helper()
	ids := make(map[string]string)
	for _, row := range storedRows(t, path, host) {
		ids[row.Pane] = row.ID
	}
	return ids
}

// requireStoredPanes requires the pane rows stored under remote to be exactly the panes want names, by tmux pane id.
func requireStoredPanes(t *testing.T, path string, want ...string) {
	t.Helper()
	got := slices.Sorted(maps.Keys(storedIDs(t, path, remote)))
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("panes stored under %s = %v, want %v", remote, got, want)
	}
}

// requireRemoved requires the next write to remove exactly the sessions want names, and no other.
func requireRemoved(t *testing.T, writes <-chan []string, want ...string) {
	t.Helper()
	if removed := <-writes; !slices.Equal(removed, want) {
		t.Fatalf("the next write touched sessions %v, want only the removal of %v", removed, want)
	}
}

// loginTransport sends each request with the owner's login and the client header the guard requires.
type loginTransport struct {
	base http.RoundTripper
}

// RoundTrip sends a copy of req with the guard headers set.
func (l loginTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set(server.HeaderLogin, login)
	clone.Header.Set(server.HeaderClient, clientName)
	return l.base.RoundTrip(clone)
}

// neverTimer returns a timer that never fires.
func neverTimer(time.Duration) (<-chan time.Time, func() bool) {
	return make(chan time.Time), func() bool { return true }
}

// listedPanes serves a controller over the store at path, with tmux as its own panes and agent as host remote, on a
// real loopback listener, and returns the panes its Panes.List answers.
func listedPanes(t *testing.T, tmux tmuxctl.Server, path string, agent *node.Client) []*orreryv1.Pane {
	t.Helper()
	pushStore, err := push.NewStore(filepath.Join(t.TempDir(), "push.json"), []string{remote})
	if err != nil {
		t.Fatalf("push.NewStore: %v", err)
	}
	handler, err := server.New(server.Config{
		Logins:   []string{login},
		HostName: controller,
		Tmux:     tmuxClient(t, tmux),
		Push:     pushStore,
		Sessions: store.NewReader(path),
		Hosts:    []*node.Client{agent},
		NewTimer: neverTimer,
		Now:      fixedNow,
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: loginTransport{base: http.DefaultTransport}}
	panes := orreryv1connect.NewPanesClient(httpClient, srv.URL)
	got, err := panes.List(t.Context(), connect.NewRequest(&orreryv1.PanesListRequest{}))
	if err != nil {
		t.Fatalf("Panes.List: %v", err)
	}
	return got.Msg.GetPanes()
}

// TestHostPanesStoredUnderHost checks that a pane bound on host b, which b's watcher reports, is stored under b, and
// that Panes.List lists it with its host set to b.
func TestHostPanesStoredUnderHost(t *testing.T) {
	t.Parallel()
	st, path := openStore(t)
	writes := watchWrites(st)
	remoteServer, pane := startTmux(t)
	controllerServer, _ := startTmux(t)
	dir := t.TempDir()
	host := startPaneHost(t, remoteServer, dir, paneEnv(synthetic, remoteServer, pane), make(chan time.Time))
	agent := serveAgent(t, host, "127.0.0.1:0")
	client := nodeClient(agent.addr, newFakeClock())
	startFollow(t, st, client, newFakeClock())

	<-writes // the first listing is stored, so the watch has joined and sees each change after it
	requireStoredPanes(t, path, pane)
	writeEntry(t, dir, waitingEntry(t, synthetic))
	<-writes // the pane's change is stored
	rows := storedRows(t, path, remote)
	if len(rows) != 1 || rows[0].Fields.State != string(protocol.StateWaiting) {
		t.Fatalf("stored panes of %s = %+v, want pane %s waiting", remote, rows, pane)
	}

	listed := listedPanes(t, controllerServer, path, client)
	if !slices.ContainsFunc(listed, func(p *orreryv1.Pane) bool {
		return p.GetId() == pane && p.GetHost() == remote && !p.GetUnreachable()
	}) {
		t.Errorf("Panes.List = %v, want pane %s of %s answered live by %s", listed, pane, remote, remote)
	}
}

// TestHostPanesTidyOnlyHost checks that a full listing of b removes the row of a pane killed on b, and leaves the
// controller's row on the same server and pane id.
func TestHostPanesTidyOnlyHost(t *testing.T) {
	t.Parallel()
	st, path := openStore(t)
	remoteServer, first := startTmux(t)
	second := splitPane(t, remoteServer, first)
	if err := st.SyncServer(t.Context(), controller, remoteServer.Column(),
		[]store.Pane{{ID: "controller-2", Pane: second}}, fixedNow()); err != nil {
		t.Fatalf("SyncServer of %s: %v", controller, err)
	}
	writes := watchWrites(st)
	dir := t.TempDir()
	ticks := make(chan time.Time)
	host := startPaneHost(t, remoteServer, dir, paneEnv(synthetic, remoteServer, first), ticks)
	agent := serveAgent(t, host, "127.0.0.1:0")
	startFollow(t, st, nodeClient(agent.addr, newFakeClock()), newFakeClock())

	<-writes // the first listing is stored
	ids := storedIDs(t, path, remote)
	if len(ids) != 2 {
		t.Fatalf("stored panes of %s = %v, want panes %s and %s", remote, ids, first, second)
	}
	killPane(t, remoteServer, second)
	ticks <- time.Time{} // b's next full listing, which leaves the killed pane out
	writeEntry(t, dir, waitingEntry(t, synthetic))
	requireRemoved(t, writes, ids[second])
	<-writes // the change to the bound pane, which follows the listing, is stored

	requireStoredPanes(t, path, first)
	rows := storedRows(t, path, controller)
	if len(rows) != 1 || rows[0].ID != "controller-2" || rows[0].Pane != second ||
		rows[0].Server != remoteServer.Column() {
		t.Errorf("stored panes of %s = %+v, want the row of pane %s on %s", controller, rows, second,
			remoteServer.Column())
	}
}

// TestHostPanesTidyGoneServer checks that a full listing of b that no longer names one of b's tmux servers removes the
// rows of b on that server, and leaves the rows of b on the server that the listing still names.
func TestHostPanesTidyGoneServer(t *testing.T) {
	t.Parallel()
	st, path := openStore(t)
	writes := watchWrites(st)
	remoteServer, first := startTmux(t)
	otherServer, third := startExitingTmux(t)
	dir := t.TempDir()
	env := paneEnv(synthetic, otherServer, third)
	maps.Copy(env, paneEnv(sentinelPID, remoteServer, first))
	ticks := make(chan time.Time)
	host := startPaneHost(t, remoteServer, dir, env, ticks)
	agent := serveAgent(t, host, "127.0.0.1:0")
	startFollow(t, st, nodeClient(agent.addr, newFakeClock()), newFakeClock())

	<-writes // the first listing stores the pane of the default server
	<-agent.joined
	writeEntry(t, dir, waitingEntry(t, synthetic)) // names the other server, so the watch lists it from now on
	<-writes                                       // the pane on the other server is stored
	gone := rowOnServer(t, path, remote, otherServer.Column())

	killPane(t, otherServer, third) // the other server's last pane, so the server exits
	ticks <- time.Time{}            // b's next full listing names the default server alone
	writeEntry(t, dir, waitingEntry(t, sentinelPID))
	requireRemoved(t, writes, gone.ID)
	<-writes // the change to the default server's pane, which follows the listing, is stored

	rows := storedRows(t, path, remote)
	if len(rows) != 1 || rows[0].Server != remoteServer.Column() || rows[0].Pane != first {
		t.Errorf("stored panes of %s = %+v, want only pane %s on %s", remote, rows, first, remoteServer.Column())
	}
}

// rowOnServer returns the one pane row stored under host on server, and fails the test when there is not exactly one.
func rowOnServer(t *testing.T, path, host, server string) store.Session {
	t.Helper()
	var found []store.Session
	for _, row := range storedRows(t, path, host) {
		if row.Server == server {
			found = append(found, row)
		}
	}
	if len(found) != 1 {
		t.Fatalf("stored panes of %s on %s = %+v, want one", host, server, found)
	}
	return found[0]
}

// TestHostPanesNoTidyUnreachable checks that while b's agent is stopped, and while each open of its watch fails, no
// tidy runs and b's rows stay, and that the first listing after the agent answers again removes the killed pane's row.
func TestHostPanesNoTidyUnreachable(t *testing.T) {
	t.Parallel()
	st, path := openStore(t)
	remoteServer, first := startTmux(t)
	second := splitPane(t, remoteServer, first)
	writes := watchWrites(st)
	dir := t.TempDir()
	host := startPaneHost(t, remoteServer, dir, paneEnv(synthetic, remoteServer, first), make(chan time.Time))
	agent := serveAgent(t, host, "127.0.0.1:0")
	clientClock, retry := newFakeClock(), newFakeClock()
	startFollow(t, st, nodeClient(agent.addr, clientClock), retry)

	<-writes // the first listing is stored
	<-agent.joined
	ids := storedIDs(t, path, remote)
	clientClock.discardArmed() // the first open armed its bound, which no step below waits on
	agent.stop()
	awaitArmed(retry, retryDelay) // the watch broke, and Follow waits to open another
	killPane(t, remoteServer, second)
	requireStoredPanes(t, path, first, second)

	retry.Advance(retryDelay) // Follow opens a watch while the agent is stopped
	awaitArmed(clientClock, node.DefaultBound)
	clientClock.Advance(node.DefaultBound) // the open's bound passes, so the open fails
	awaitArmed(retry, retryDelay)          // Follow waits to open another
	requireStoredPanes(t, path, first, second)

	restarted := serveAgent(t, host, agent.addr) // the agent answers again on the address it had
	retry.Advance(retryDelay)
	<-restarted.joined
	writeEntry(t, dir, waitingEntry(t, synthetic))
	requireRemoved(t, writes, ids[second])
	<-writes // the change to the bound pane, which follows the listing, is stored
	requireStoredPanes(t, path, first)
}
