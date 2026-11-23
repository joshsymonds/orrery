package server_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/exectest"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/hosts"
	"github.com/joshsymonds/orrery/internal/hoststatus"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/server"
)

const (
	// statusHostB and statusHostC are the names of the two hosts.toml hosts of the Host.Status tests, and
	// routingController names their controller, as hosts.toml's top-level name.
	statusHostB = "b"
	statusHostC = "c"
	// statusLocalLoad and statusLocalMemory are the controller's load and memory. Each host's load and memory differ
	// from the controller's and from each other's, so an entry that carries another host's values fails the test.
	statusLocalLoad   = 25.5
	statusLocalMemory = 61.25
	statusBLoad       = 150.5
	statusBMemory     = 40.75
	statusCLoad       = 3.125
	statusCMemory     = 99
	// statusArmedMax bounds how many armed timers statusClock notes before the test reads them.
	statusArmedMax = 8
)

// statusNow is the clock of a status command's runner. The tests read the command once, so its time never moves.
func statusNow() time.Time {
	return time.Time{}
}

// statusLocal returns a LocalLoad that reads load and memory.
func statusLocal(load, memory float64) func() (node.Load, error) {
	return func() (node.Load, error) {
		return node.Load{LoadPercent: load, MemoryPercent: memory}, nil
	}
}

// quietClocks gives every host of a Host.Status test the quietClock, so no call of its client is bounded by the clock.
func quietClocks(string) holder.Clock {
	return quietClock{}
}

// statusPanes is the node.HostPanes of a Host.Status test's agent, whose load is load. Its handler and watch are
// routingPanes', since no Host.Status test asks for a pane.
type statusPanes struct {
	routingPanes
	load node.Load
}

// Load answers the agent's load.
func (p statusPanes) Load(context.Context) (node.Load, error) {
	return p.load, nil
}

// startStatusAgent serves an agent for host, whose load is load, on a loopback listener until the test ends. It returns
// the listener's address, which a hosts.toml names.
func startStatusAgent(t *testing.T, host string, load node.Load) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	agent, err := node.NewAgent(node.AgentConfig{
		Controller: routingController,
		WhoIs:      routingWhoIs{own: ln.Addr().String(), host: host},
		Holders:    routingHolders{},
		Panes:      statusPanes{routingPanes: routingPanes{handler: http.NotFoundHandler()}, load: load},
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- agent.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if serveErr := <-served; serveErr != nil {
			t.Errorf("serve the agent of %s: %v", host, serveErr)
		}
	})
	return ln.Addr().String()
}

// refusedAddress returns a loopback address that refuses connections, since the listener that held it is closed.
func refusedAddress(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err = ln.Close(); err != nil {
		t.Fatalf("close the listener: %v", err)
	}
	return addr
}

// loadHostsFile writes a hosts.toml that names controller and lists each of entries in order, and returns the file as
// hosts.Load reads it from the XDG config home the file is in.
func loadHostsFile(t *testing.T, controller string, entries []hosts.Host) hosts.File {
	t.Helper()
	config := t.TempDir()
	lines := make([]string, 0, 1+4*len(entries))
	lines = append(lines, fmt.Sprintf("name = %q", controller))
	for _, entry := range entries {
		lines = append(lines, "", "[[hosts]]", fmt.Sprintf("name = %q", entry.Name),
			fmt.Sprintf("address = %q", entry.Address))
	}
	if err := os.MkdirAll(filepath.Join(config, "orrery"), 0o700); err != nil {
		t.Fatalf("make the orrery config directory: %v", err)
	}
	exectest.WriteFile(t, filepath.Join(config, "orrery", "hosts.toml"), []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	file, err := hosts.Load(func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return config
		}
		return ""
	})
	if err != nil {
		t.Fatalf("hosts.Load: %v", err)
	}
	return file
}

// statusClients returns the agent client of each host of file, in file order, as serve builds them: each dials its
// agent with net.Dialer. clockOf gives the clock of the client of the host it names.
func statusClients(file hosts.File, clockOf func(host string) holder.Clock) []*node.Client {
	var dialer net.Dialer
	clients := make([]*node.Client, 0, len(file.Hosts))
	for _, host := range file.Hosts {
		clients = append(clients, node.NewClient(node.ClientConfig{
			Name:    host.Name,
			Address: host.Address,
			Dial:    dialer.DialContext,
			Clock:   clockOf(host.Name),
		}))
	}
	return clients
}

// statusHandler returns the guarded handler of a Host.Status test over cfg. It admits allowedLogin, and runs over the
// test's own tmux client and push store.
func statusHandler(t *testing.T, cfg server.Config) http.Handler {
	t.Helper()
	cfg.Logins = []string{allowedLogin}
	cfg.Tmux = newTmuxClient(t)
	cfg.Push = newPushStore(t)
	handler, err := server.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// statusResult is the answer of one Host.Status call: its entries, or the error it answered with.
type statusResult struct {
	entries []*orreryv1.HostEntry
	err     error
}

// startStatus calls Host.Status through handler on a goroutine, and returns the channel its answer arrives on. The call
// runs apart from the test, so the test can move a clock while the call waits on it.
func startStatus(t *testing.T, handler http.Handler) <-chan statusResult {
	t.Helper()
	client := hostClient(t, handler)
	done := make(chan statusResult, 1)
	go func() {
		got, err := client.Status(t.Context(), connect.NewRequest(&orreryv1.HostStatusRequest{}))
		if err != nil {
			done <- statusResult{err: err}
			return
		}
		done <- statusResult{entries: got.Msg.GetHosts()}
	}()
	return done
}

// statusEntries calls Host.Status through handler and returns the entries of its answer.
func statusEntries(t *testing.T, handler http.Handler) []*orreryv1.HostEntry {
	t.Helper()
	result := <-startStatus(t, handler)
	if result.err != nil {
		t.Fatalf("Host.Status: %v", result.err)
	}
	return result.entries
}

// requireHostEntries requires got to hold the entries of want, in order, and reports each entry that differs.
func requireHostEntries(t *testing.T, want, got []*orreryv1.HostEntry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("hosts = %v, want %v", got, want)
	}
	for index := range want {
		if !proto.Equal(want[index], got[index]) {
			t.Errorf("hosts[%d] = %v, want %v", index, got[index], want[index])
		}
	}
}

// statusClock is a holder.Clock that moves only when the test advances it. Each timer it arms is noted on armed, so the
// test can wait for a timer to be armed before it moves the clock past the timer's deadline.
type statusClock struct {
	armed chan time.Duration

	mu      sync.Mutex
	now     time.Duration
	waiters []statusWaiter
}

// statusWaiter is one timer that statusClock has armed. It fires once the clock reaches deadline.
type statusWaiter struct {
	deadline time.Duration
	fire     chan time.Time
}

// newStatusClock returns a statusClock at its zero time, with no timers armed.
func newStatusClock() *statusClock {
	return &statusClock{armed: make(chan time.Duration, statusArmedMax)}
}

// After arms a timer that fires once the clock has moved d past its time at the call.
func (c *statusClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	fire := make(chan time.Time, 1)
	c.waiters = append(c.waiters, statusWaiter{deadline: c.now + d, fire: fire})
	select {
	case c.armed <- d:
	default:
	}
	return fire
}

// Advance moves the clock forward by d, and fires each timer whose deadline has then passed.
func (c *statusClock) Advance(d time.Duration) {
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

// TestHostsStatusListsEveryHost answers Host.Status over a controller and two hosts.toml hosts, each with its own
// agent. The controller's entry carries the load LocalLoad reads, and each host's entry carries the load its agent
// reports, with each host reachable.
func TestHostsStatusListsEveryHost(t *testing.T) {
	t.Parallel()
	addrB := startStatusAgent(t, statusHostB, node.Load{LoadPercent: statusBLoad, MemoryPercent: statusBMemory})
	addrC := startStatusAgent(t, statusHostC, node.Load{LoadPercent: statusCLoad, MemoryPercent: statusCMemory})
	file := loadHostsFile(t, routingController, []hosts.Host{
		{Name: statusHostB, Address: addrB},
		{Name: statusHostC, Address: addrC},
	})
	handler := statusHandler(t, server.Config{
		HostName:   routingController,
		HostStatus: hoststatus.New(writeHostStatusScript(t, "[]", 0), statusNow),
		LocalLoad:  statusLocal(statusLocalLoad, statusLocalMemory),
		Hosts:      statusClients(file, quietClocks),
	})

	requireHostEntries(t, []*orreryv1.HostEntry{
		{Name: routingController, LoadPercent: statusLocalLoad, MemoryPercent: statusLocalMemory, Reachable: true},
		{Name: statusHostB, LoadPercent: statusBLoad, MemoryPercent: statusBMemory, Reachable: true},
		{Name: statusHostC, LoadPercent: statusCLoad, MemoryPercent: statusCMemory, Reachable: true},
	}, statusEntries(t, handler))
}

// TestHostsStatusUnreachableHost answers Host.Status over a host whose agent refuses connections. The refused dial is
// retried on the clock until the client's bound passes, so the answer waits for the clock. Once the clock passes the
// bound, the host reads unreachable with both percents 0, and the other host still reads its load.
func TestHostsStatusUnreachableHost(t *testing.T) {
	t.Parallel()
	addrB := startStatusAgent(t, statusHostB, node.Load{LoadPercent: statusBLoad, MemoryPercent: statusBMemory})
	file := loadHostsFile(t, routingController, []hosts.Host{
		{Name: statusHostB, Address: addrB},
		{Name: statusHostC, Address: refusedAddress(t)},
	})
	clock := newStatusClock()
	handler := statusHandler(t, server.Config{
		HostName:   routingController,
		HostStatus: hoststatus.New(writeHostStatusScript(t, "[]", 0), statusNow),
		LocalLoad:  statusLocal(statusLocalLoad, statusLocalMemory),
		Hosts: statusClients(file, func(host string) holder.Clock {
			if host == statusHostC {
				return clock
			}
			return quietClock{}
		}),
	})
	done := startStatus(t, handler)

	select {
	case <-clock.armed:
	case result := <-done:
		t.Fatalf("Host.Status answered before the clock was armed: %v, %v", result.entries, result.err)
	}
	select {
	case result := <-done:
		t.Fatalf("Host.Status answered before the clock passed the bound: %v, %v", result.entries, result.err)
	default:
	}
	clock.Advance(node.DefaultBound)
	result := <-done
	if result.err != nil {
		t.Fatalf("Host.Status: %v", result.err)
	}

	requireHostEntries(t, []*orreryv1.HostEntry{
		{Name: routingController, LoadPercent: statusLocalLoad, MemoryPercent: statusLocalMemory, Reachable: true},
		{Name: statusHostB, LoadPercent: statusBLoad, MemoryPercent: statusBMemory, Reachable: true},
		{Name: statusHostC},
	}, result.entries)
}

// TestHostsStatusLocalLoadFails answers Host.Status over a controller whose own load cannot be read. The controller
// still answers, reachable, with both percents 0.
func TestHostsStatusLocalLoadFails(t *testing.T) {
	t.Parallel()
	handler := statusHandler(t, server.Config{
		HostName:   routingController,
		HostStatus: hoststatus.New(writeHostStatusScript(t, "[]", 0), statusNow),
		LocalLoad: func() (node.Load, error) {
			return node.Load{}, errors.New("synthetic load failure")
		},
	})

	requireHostEntries(t, []*orreryv1.HostEntry{
		{Name: routingController, Reachable: true},
	}, statusEntries(t, handler))
}
