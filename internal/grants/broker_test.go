package grants

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/pools"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

const (
	testHost = "box"
	// guard bounds how long a test waits for one event. Tests order their steps by events and handshakes, not by time.
	guard = 10 * time.Second
	// healthy and failing are fake command bodies that exit 0 and 1.
	healthy = "exit 0"
	failing = "exit 1"
	// holdGate blocks a fake command until the test creates the file named by GATE.
	holdGate = `while [ ! -e "$GATE" ]; do sleep 0.01; done`
	// announce writes to the pipe named by BEGUN, which blocks until the test reads it, so the test knows the command
	// has started.
	announce = `echo started > "$BEGUN"`
)

// fixedNow is the clock every test broker uses.
func fixedNow() time.Time {
	return time.Date(2026, time.November, 12, 9, 0, 0, 0, time.UTC)
}

// newBroker returns a broker over a new store under t.TempDir(), whose commands run with PATH and extra. The broker is
// closed before the store.
func newBroker(t *testing.T, extra ...string) (*Broker, *store.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	b := NewBroker(Config{
		Host: testHost, Store: s, Environ: commandEnv(extra...), Now: fixedNow,
		Logger: slog.New(slog.DiscardHandler),
	})
	t.Cleanup(b.Close)
	return b, s
}

// poolOf defines a pool on testHost with cap limit and members, whose status, wake and admit commands run the shell
// bodies given. Every command limit is a minute.
func poolOf(t *testing.T, name string, limit int, members []string, status, wake, admit string) pools.Pool {
	t.Helper()
	return pools.Pool{
		Name: name, Host: testHost, Cap: limit, Members: members,
		Status: []string{commandScript(t, status)}, StatusLimit: time.Minute,
		Wake: []string{commandScript(t, wake)}, WakeLimit: time.Minute,
		Admit: []string{commandScript(t, admit)}, AdmitLimit: time.Minute,
	}
}

// awakePool defines a pool whose members are all awake: every command exits 0.
func awakePool(t *testing.T, name string, limit int, members ...string) pools.Pool {
	t.Helper()
	return poolOf(t, name, limit, members, healthy, healthy, healthy)
}

// request asks for count members of def for project.
func request(project string, def pools.Pool, count int) Request {
	return Request{
		Project: project, Pools: []pools.Pool{def}, Needs: []Need{{Pool: def.Name, Count: count}}, ClientPID: 4242,
	}
}

// call is an Acquire running on its own goroutine: events holds what its send received, and result what it returned.
type call struct {
	events chan Event
	result chan error
}

// start runs b.Acquire for req on a goroutine, under ctx.
func start(ctx context.Context, b *Broker, req Request) *call {
	c := &call{events: make(chan Event, 8), result: make(chan error, 1)}
	go func() {
		c.result <- b.Acquire(ctx, req, func(e Event) error {
			c.events <- e
			return nil
		})
	}()
	return c
}

// next returns the call's next event, failing the test if the call returns first or the guard passes.
func (c *call) next(t *testing.T) Event {
	t.Helper()
	select {
	case e := <-c.events:
		return e
	case err := <-c.result:
		t.Fatalf("Acquire returned %v before its next event", err)
	case <-time.After(guard):
		t.Fatal("no event within the guard")
	}
	return Event{}
}

// finish waits for the call's Acquire to return, and returns its error.
func (c *call) finish(t *testing.T) error {
	t.Helper()
	select {
	case err := <-c.result:
		return err
	case <-time.After(guard):
		t.Fatal("Acquire did not return within the guard")
	}
	return nil
}

// startTimeOf returns the start time /proc records for the process with pid, read by the test itself: field 22 of its
// stat, which is the 20th field after the comm's closing parenthesis.
func startTimeOf(t *testing.T, pid int) int64 {
	t.Helper()
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		t.Fatalf("read stat of process %d: %v", pid, err)
	}
	text := string(stat)
	fields := strings.Fields(text[strings.LastIndexByte(text, ')')+1:])
	if len(fields) < 20 {
		t.Fatalf("stat of process %d has %d fields after comm, want at least 20", pid, len(fields))
	}
	start, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		t.Fatalf("parse start time of process %d: %v", pid, err)
	}
	return start
}

// TestGrantStoresClientStartBesideClientPID checks that a grant stores its client's start time beside the client's pid.
// A restore compares that start time with the process that holds the pid later. The test process is the client.
func TestGrantStoresClientStartBesideClientPID(t *testing.T) {
	b, s := newBroker(t)
	p := awakePool(t, "p", 1, "m1")
	grantTo(t, b, p, "a", os.Getpid())
	rows := storedGrants(t, s)
	want := startTimeOf(t, os.Getpid())
	if len(rows) != 1 || rows[0].ClientPID != os.Getpid() || rows[0].ClientStart != want {
		t.Fatalf("stored grants = %+v, want one grant of client pid %d with start time %d", rows, os.Getpid(), want)
	}
}

// TestParseStartTimeCountsFromTheCommsClosingParenthesis checks that the start time is field 22 of a stat line whose
// comm holds spaces and parentheses, and that a line too short for field 22 is refused.
func TestParseStartTimeCountsFromTheCommsClosingParenthesis(t *testing.T) {
	// Fields 3 through 21 are 1, field 22 is 777, and the line goes on past it.
	rest := "S " + strings.Repeat("1 ", 18) + "777 1 1 1"
	for _, comm := range []string{"sleep", "a) b", "(x) y"} {
		got, err := parseStartTime("4242 (" + comm + ") " + rest)
		if err != nil || got != 777 {
			t.Fatalf("parseStartTime with comm %q = %d, %v; want 777", comm, got, err)
		}
	}
	if _, err := parseStartTime("4242 (sleep) S 1 1"); err == nil {
		t.Fatal("parseStartTime of a line with no field 22 succeeded")
	}
	if _, err := parseStartTime("4242 sleep S"); err == nil {
		t.Fatal("parseStartTime of a line with no comm parentheses succeeded")
	}
}

// makeFifo creates the named pipe at path, which a fake command writes to and the test reads.
func makeFifo(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("mkfifo %s: %v", path, err)
	}
}

// awaitSignal reads the pipe at path until the fake command that opened it for writing has written to it.
func awaitSignal(t *testing.T, path string) {
	t.Helper()
	read := make(chan error, 1)
	go func() {
		_, err := os.ReadFile(path)
		read <- err
	}()
	select {
	case err := <-read:
		if err != nil {
			t.Fatalf("read signal %s: %v", path, err)
		}
	case <-time.After(guard):
		t.Fatalf("fake command never signaled on %s", path)
	}
}

// storedGrants returns the live grants in s.
func storedGrants(t *testing.T, s *store.Store) []store.GrantRow {
	t.Helper()
	rows, err := s.Grants(t.Context())
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	return rows
}

// queuedProjects returns the projects of the requests queued in b, in arrival order.
func queuedProjects(b *Broker) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	projects := make([]string, 0, len(b.waiting))
	for _, w := range b.waiting {
		projects = append(projects, w.req.Project)
	}
	return projects
}

// memberOf returns the state of member of pool, and the grant holding it, empty when it is free.
func memberOf(t *testing.T, b *Broker, pool, member string) (MemberState, string) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	use, ok := b.pools[pool]
	if !ok {
		t.Fatalf("pool %s is not in use", pool)
	}
	mr, ok := use.members[member]
	if !ok {
		t.Fatalf("pool %s has no member %s", pool, member)
	}
	return mr.state, mr.grant
}

// runningCommands returns how many member commands are in flight for pool.
func runningCommands(b *Broker, pool string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	use, ok := b.pools[pool]
	if !ok {
		return 0
	}
	return use.running
}

// TestAcquireRefusesMalformedRequestsBeforeQueueing checks that a request that cannot be granted returns its refusal
// without sending an event or queueing.
func TestAcquireRefusesMalformedRequestsBeforeQueueing(t *testing.T) {
	b, _ := newBroker(t)
	capped := awakePool(t, "capped", 1, "m1", "m2")
	roomy := awakePool(t, "roomy", 3, "r1")
	elsewhere := awakePool(t, "elsewhere", 1, "e1")
	elsewhere.Host = "other-box"
	reclaim := request("a", capped, 1)
	reclaim.GrantID = "g1"
	cases := []struct {
		name string
		req  Request
		want error
	}{
		{"no needs", Request{Project: "a", Pools: []pools.Pool{capped}}, ErrBadNeed},
		{"zero count", request("a", capped, 0), ErrBadNeed},
		{"count above cap", request("a", capped, 2), ErrBadNeed},
		{"count above members", request("a", roomy, 2), ErrBadNeed},
		{"pool not defined", Request{
			Project: "a", Pools: []pools.Pool{capped}, Needs: []Need{{Pool: "missing", Count: 1}},
		}, ErrBadNeed},
		{"other host", request("a", elsewhere, 1), ErrRefused},
		{"grant to reclaim", reclaim, ErrNoGrant},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := b.Acquire(t.Context(), tc.req, func(e Event) error {
				t.Errorf("refused request sent %+v", e)
				return nil
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Acquire = %v, want %v", err, tc.want)
			}
		})
	}
	if got := queuedProjects(b); len(got) != 0 {
		t.Fatalf("refused requests left %v queued", got)
	}
}

// TestAcquireRefusesPoolDefinedDifferentlyOrMemberOfAnotherPool checks that a request is refused when its pool is
// defined differently from the one in use, or when one of its members belongs to another pool in use.
func TestAcquireRefusesPoolDefinedDifferentlyOrMemberOfAnotherPool(t *testing.T) {
	b, _ := newBroker(t)
	p := awakePool(t, "p", 2, "m1", "m2")
	held := start(t.Context(), b, request("a", p, 1))
	held.next(t)
	if e := held.next(t); e.Grant == nil {
		t.Fatalf("second event = %+v, want the grant of p", e)
	}
	resized := p
	resized.Cap = 1
	overlapping := awakePool(t, "q", 1, "m1")
	cases := []struct {
		name string
		def  pools.Pool
	}{
		{"defined differently", resized},
		{"member of pool in use", overlapping},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := b.Acquire(t.Context(), request("b", tc.def, 1), func(e Event) error {
				t.Errorf("refused request sent %+v", e)
				return nil
			})
			if !errors.Is(err, ErrRefused) {
				t.Fatalf("Acquire = %v, want ErrRefused", err)
			}
		})
	}
}

// TestPoolNoOneUsesIsForgotten checks that a pool nothing queues for and nothing holds leaves use, so a request may
// define it differently.
func TestPoolNoOneUsesIsForgotten(t *testing.T) {
	b, _ := newBroker(t)
	p := awakePool(t, "p", 1, "m1")
	ctx, cancel := context.WithCancel(t.Context())
	first := start(ctx, b, request("a", p, 1))
	first.next(t)
	first.next(t)
	cancel()
	if err := first.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("first Acquire = %v, want context.Canceled", err)
	}
	resized := p
	resized.Cap = 2
	second := start(t.Context(), b, request("b", resized, 1))
	if e := second.next(t); e.Position != 1 {
		t.Fatalf("second event = %+v, want position 1 for a request on the redefined pool", e)
	}
}

// TestAcquireSendsPositionThenGrantOnceStored checks that a grant is sent only once the store holds it, and that
// canceling the call ends the grant in the store.
func TestAcquireSendsPositionThenGrantOnceStored(t *testing.T) {
	b, s := newBroker(t)
	p := awakePool(t, "p", 1, "m1")
	ctx, cancel := context.WithCancel(t.Context())
	c := start(ctx, b, request("a", p, 1))
	if e := c.next(t); e.Position != 1 || e.Grant != nil {
		t.Fatalf("first event = %+v, want position 1 alone", e)
	}
	held := c.next(t).Grant
	if held == nil {
		t.Fatal("second event carries no grant")
	}
	if !slices.Equal(held.Members, []Member{{Pool: "p", Name: "m1"}}) {
		t.Fatalf("grant members = %+v, want p/m1", held.Members)
	}
	rows := storedGrants(t, s)
	if len(rows) != 1 || rows[0].ID != held.ID || rows[0].Project != "a" {
		t.Fatalf("stored grants = %+v, want grant %s of project a", rows, held.ID)
	}
	cancel()
	if err := c.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire = %v, want context.Canceled", err)
	}
	if rows := storedGrants(t, s); len(rows) != 0 {
		t.Fatalf("stored grants after the call ended = %+v, want none", rows)
	}
}

// TestGrantEndsWithItsContextAndNoTwoRequestsHoldOneMember checks that a request queued behind a held member does not
// hold it, and that it is granted, alone, once the holder's context ends.
func TestGrantEndsWithItsContextAndNoTwoRequestsHoldOneMember(t *testing.T) {
	b, s := newBroker(t)
	p := awakePool(t, "p", 1, "m1")
	ctx, cancel := context.WithCancel(t.Context())
	first := start(ctx, b, request("a", p, 1))
	first.next(t)
	held := first.next(t).Grant
	second := start(t.Context(), b, request("b", p, 1))
	if e := second.next(t); e.Position != 1 {
		t.Fatalf("second event = %+v, want position 1", e)
	}
	if _, grant := memberOf(t, b, "p", "m1"); grant != held.ID {
		t.Fatalf("m1 held by %q while the first grant %s is live", grant, held.ID)
	}
	cancel()
	if err := first.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("first Acquire = %v, want context.Canceled", err)
	}
	next := second.next(t).Grant
	if next == nil {
		t.Fatal("second request was not granted after the first grant ended")
	}
	rows := storedGrants(t, s)
	if len(rows) != 1 || rows[0].ID != next.ID {
		t.Fatalf("stored grants = %+v, want only grant %s", rows, next.ID)
	}
}

// TestQueuedRequestsAreGrantedInArrivalOrder checks that when a member frees, the request that queued first gets it.
func TestQueuedRequestsAreGrantedInArrivalOrder(t *testing.T) {
	b, _ := newBroker(t)
	p := awakePool(t, "p", 1, "m1")
	ctx, cancel := context.WithCancel(t.Context())
	first := start(ctx, b, request("a", p, 1))
	first.next(t)
	first.next(t)
	second := start(t.Context(), b, request("b", p, 1))
	if e := second.next(t); e.Position != 1 {
		t.Fatalf("second event = %+v, want position 1", e)
	}
	third := start(t.Context(), b, request("c", p, 1))
	if e := third.next(t); e.Position != 2 {
		t.Fatalf("third event = %+v, want position 2", e)
	}
	cancel()
	if err := first.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("first Acquire = %v, want context.Canceled", err)
	}
	if e := second.next(t); e.Grant == nil {
		t.Fatalf("second request got %+v, want its grant", e)
	}
	if got := queuedProjects(b); !slices.Equal(got, []string{"c"}) {
		t.Fatalf("queued projects = %v, want [c]", got)
	}
}

// TestCapKeepsProjectToItsShare checks that a project at its cap waits while another project takes a free member.
func TestCapKeepsProjectToItsShare(t *testing.T) {
	b, _ := newBroker(t)
	p := awakePool(t, "p", 1, "m1", "m2")
	first := start(t.Context(), b, request("a", p, 1))
	first.next(t)
	held := first.next(t).Grant
	second := start(t.Context(), b, request("a", p, 1))
	if e := second.next(t); e.Position != 1 {
		t.Fatalf("second event = %+v, want position 1", e)
	}
	third := start(t.Context(), b, request("b", p, 1))
	if e := third.next(t); e.Position != 2 {
		t.Fatalf("third event = %+v, want position 2", e)
	}
	granted := third.next(t).Grant
	if granted == nil {
		t.Fatal("third request was not granted a free member")
	}
	if len(granted.Members) != 1 || granted.Members[0].Name == held.Members[0].Name {
		t.Fatalf("third grant = %+v, want a member other than %+v", granted.Members, held.Members)
	}
	if got := queuedProjects(b); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("queued projects = %v, want [a] at its cap", got)
	}
}

// TestCloseReturnsBlockedCallsAndLeavesStoredGrants checks that Close returns every blocked call with ErrClosed, that
// it leaves the live grant stored, and that a later call is refused.
func TestCloseReturnsBlockedCallsAndLeavesStoredGrants(t *testing.T) {
	b, s := newBroker(t)
	p := awakePool(t, "p", 1, "m1")
	holder := start(t.Context(), b, request("a", p, 1))
	holder.next(t)
	held := holder.next(t).Grant
	queued := start(t.Context(), b, request("b", p, 1))
	queued.next(t)
	b.Close()
	if err := holder.finish(t); !errors.Is(err, ErrClosed) {
		t.Fatalf("holder Acquire = %v, want ErrClosed", err)
	}
	if err := queued.finish(t); !errors.Is(err, ErrClosed) {
		t.Fatalf("queued Acquire = %v, want ErrClosed", err)
	}
	rows := storedGrants(t, s)
	if len(rows) != 1 || rows[0].ID != held.ID {
		t.Fatalf("stored grants = %+v, want only grant %s", rows, held.ID)
	}
	err := b.Acquire(t.Context(), request("c", p, 1), func(e Event) error {
		t.Errorf("closed broker sent %+v", e)
		return nil
	})
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("Acquire after Close = %v, want ErrClosed", err)
	}
}

// TestWakesOneMemberAtATime checks that a second request for a member being woken queues without starting a second
// wake, and that the member, once awake and admitted, serves the second request with no further wake.
func TestWakesOneMemberAtATime(t *testing.T) {
	dir := t.TempDir()
	begun := filepath.Join(dir, "begun")
	gate := filepath.Join(dir, "gate")
	wakes := filepath.Join(dir, "wakes")
	makeFifo(t, begun)
	b, _ := newBroker(t, "BEGUN="+begun, "GATE="+gate, "WAKES="+wakes)
	wake := `echo wake >> "$WAKES"` + "\n" + announce + "\n" + holdGate
	p := poolOf(t, "p", 2, []string{"m1"}, failing, wake, healthy)
	ctx, cancel := context.WithCancel(t.Context())
	first := start(ctx, b, request("a", p, 1))
	first.next(t)
	awaitSignal(t, begun)
	second := start(t.Context(), b, request("b", p, 1))
	if e := second.next(t); e.Position != 2 {
		t.Fatalf("second event = %+v, want position 2", e)
	}
	if state, _ := memberOf(t, b, "p", "m1"); state != Waking {
		t.Fatalf("m1 is %v while its wake runs, want waking", state)
	}
	if n := runningCommands(b, "p"); n != 1 {
		t.Fatalf("%d member commands run for p, want one wake", n)
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatalf("open gate: %v", err)
	}
	if e := first.next(t); e.Grant == nil {
		t.Fatalf("first request got %+v, want its grant once m1 is admitted", e)
	}
	cancel()
	if err := first.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("first Acquire = %v, want context.Canceled", err)
	}
	if e := second.next(t); e.Grant == nil {
		t.Fatalf("second request got %+v, want its grant", e)
	}
	got, err := os.ReadFile(wakes)
	if err != nil {
		t.Fatalf("read wakes: %v", err)
	}
	if string(got) != "wake\n" {
		t.Fatalf("wake log = %q, want one wake", got)
	}
}

// unavailableAfterFailure checks what a member does when its only command fails. It waits for the request's position,
// for the command to start when started names its pipe, and for the command to end on its own, so the outcome under
// test is the command's and not the cancel Close would send. It then closes the broker. The request must not have been
// granted, the store must hold no grant, and the member must be unavailable.
func unavailableAfterFailure(t *testing.T, b *Broker, s *store.Store, c *call, started string) {
	t.Helper()
	if started != "" {
		awaitSignal(t, started)
	}
	deadline := time.Now().Add(guard)
	for runningCommands(b, "p") != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the member's command did not end within the guard")
		}
		time.Sleep(time.Millisecond)
	}
	b.Close()
	if err := c.finish(t); !errors.Is(err, ErrClosed) {
		t.Fatalf("Acquire = %v, want ErrClosed", err)
	}
	if n := len(c.events); n != 0 {
		t.Fatalf("%d events after the position, want none: the member was granted", n)
	}
	if state, grant := memberOf(t, b, "p", "m1"); state != Unavailable || grant != "" {
		t.Fatalf("m1 is %v holding %q, want unavailable and free", state, grant)
	}
	if rows := storedGrants(t, s); len(rows) != 0 {
		t.Fatalf("stored grants = %+v, want none", rows)
	}
}

// TestFailedStatusLeavesMemberUnavailable checks that a status command that cannot start leaves its member
// unavailable and ungranted.
func TestFailedStatusLeavesMemberUnavailable(t *testing.T) {
	b, s := newBroker(t)
	p := awakePool(t, "p", 1, "m1")
	p.Status = []string{filepath.Join(t.TempDir(), "missing")}
	c := start(t.Context(), b, request("a", p, 1))
	c.next(t)
	unavailableAfterFailure(t, b, s, c, "")
}

// TestFailedWakeLeavesMemberUnavailable checks that a wake that exits nonzero leaves its member unavailable and
// ungranted.
func TestFailedWakeLeavesMemberUnavailable(t *testing.T) {
	begun := filepath.Join(t.TempDir(), "begun")
	makeFifo(t, begun)
	b, s := newBroker(t, "BEGUN="+begun)
	p := poolOf(t, "p", 1, []string{"m1"}, failing, announce+"\n"+failing, healthy)
	c := start(t.Context(), b, request("a", p, 1))
	c.next(t)
	unavailableAfterFailure(t, b, s, c, begun)
}

// TestFailedAdmitLeavesMemberUnavailable checks that an admit that exits nonzero leaves its member unavailable and
// ungranted.
func TestFailedAdmitLeavesMemberUnavailable(t *testing.T) {
	begun := filepath.Join(t.TempDir(), "begun")
	makeFifo(t, begun)
	b, s := newBroker(t, "BEGUN="+begun)
	p := poolOf(t, "p", 1, []string{"m1"}, healthy, healthy, announce+"\n"+failing)
	c := start(t.Context(), b, request("a", p, 1))
	c.next(t)
	unavailableAfterFailure(t, b, s, c, begun)
}
