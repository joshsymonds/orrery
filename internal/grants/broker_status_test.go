package grants

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/pools"
)

// statusBody is the status command of the fake pools. It logs its member to RUNS, then does what the member's mode in
// CTL says: exit 1 for down, sleep past the limit for hang, and wait for GATE for gate. Any other mode exits 0.
const statusBody = `echo "$ORRERY_MEMBER" >> "$RUNS"
case "$(cat "$CTL/$ORRERY_MEMBER")" in
down) exit 1 ;;
hang) sleep 30 ;;
gate) ` + holdGate + ` ;;
esac
exit 0`

// wakeBody is the wake command of the fake pools: it logs its member to WAKES and exits 0.
const wakeBody = `echo "$ORRERY_MEMBER" >> "$WAKES"`

// fakes are the files the fake commands of a status test read and write, all in a directory of the test's own.
type fakes struct {
	ctl   string
	runs  string
	wakes string
	gate  string
}

// newFakes returns the files of one test: CTL holds each member's mode, RUNS logs each status run, WAKES logs each
// wake, and GATE is the file a gated command waits for.
func newFakes(t *testing.T) fakes {
	t.Helper()
	dir := t.TempDir()
	f := fakes{
		ctl:   filepath.Join(dir, "ctl"),
		runs:  filepath.Join(dir, "runs"),
		wakes: filepath.Join(dir, "wakes"),
		gate:  filepath.Join(dir, "gate"),
	}
	if err := os.Mkdir(f.ctl, 0o700); err != nil {
		t.Fatalf("make mode directory: %v", err)
	}
	return f
}

// env is the environment that names the files of f.
func (f fakes) env() []string {
	return []string{"CTL=" + f.ctl, "RUNS=" + f.runs, "WAKES=" + f.wakes, "GATE=" + f.gate}
}

// setMode sets the mode of member's status: down, hang, gate, or any other mode for a status that exits 0.
func (f fakes) setMode(t *testing.T, member, mode string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.ctl, member), []byte(mode), 0o600); err != nil {
		t.Fatalf("set mode of %s: %v", member, err)
	}
}

// openGate releases every gated command.
func (f fakes) openGate(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(f.gate, nil, 0o600); err != nil {
		t.Fatalf("open gate: %v", err)
	}
}

// statusPool defines pool fleet with cap limit and members. Its status runs statusBody, its wake runs wakeBody, and its
// admit exits 0. Every limit is a minute.
func statusPool(t *testing.T, limit int, members ...string) pools.Pool {
	t.Helper()
	return poolOf(t, "fleet", limit, members, statusBody, wakeBody, healthy)
}

// lines returns the whitespace-separated fields of the file at path, or none when the file does not exist.
func lines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Fields(string(data))
}

// await polls cond until it holds, and fails the test when it has not held within the guard.
func await(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(guard)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within the guard", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// awaitStatuses waits until n status runs have been logged and no member command of pool fleet runs. A status run logs
// its member before anything else it does, so the count reaches n only once every run of the ticks so far has started.
func awaitStatuses(t *testing.T, b *Broker, f fakes, n int) {
	t.Helper()
	await(t, "the status runs ending", func() bool {
		return len(lines(t, f.runs)) == n && runningCommands(b, "fleet") == 0
	})
}

// awaitIdle waits until no member command of pool fleet runs.
func awaitIdle(t *testing.T, b *Broker) {
	t.Helper()
	await(t, "the member commands ending", func() bool { return runningCommands(b, "fleet") == 0 })
}

// ticker runs Broker.Run on a goroutine and hands it ticks. The run ends at the end of the test, or when drain closes
// its tick channel.
type ticker struct {
	tick chan time.Time
	done chan struct{}
}

// runTicks starts b.Run on a goroutine, fed by the ticks the returned ticker sends.
func runTicks(t *testing.T, b *Broker) *ticker {
	t.Helper()
	k := &ticker{tick: make(chan time.Time), done: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		b.Run(ctx, k.tick)
		close(k.done)
	}()
	t.Cleanup(func() {
		cancel()
		<-k.done
	})
	return k
}

// send hands Run one tick, and fails the test when Run does not take it within the guard.
func (k *ticker) send(t *testing.T) {
	t.Helper()
	select {
	case k.tick <- fixedNow():
	case <-k.done:
		t.Fatal("Run returned before taking the tick")
	case <-time.After(guard):
		t.Fatal("Run did not take the tick within the guard")
	}
}

// drain closes the tick channel and waits for Run to return. Run handles each tick before it takes the next, so every
// tick sent has been handled when drain returns.
func (k *ticker) drain(t *testing.T) {
	t.Helper()
	close(k.tick)
	select {
	case <-k.done:
	case <-time.After(guard):
		t.Fatal("Run did not return after its tick channel closed")
	}
}

// admittedFree grants one member of def to a holding request, which keeps it so the pool stays in use. It then grants
// the other member to a second request and ends that grant, so the other member is left admitted and free. It returns
// the name of the free member.
func admittedFree(t *testing.T, b *Broker, def pools.Pool) string {
	t.Helper()
	holder := start(t.Context(), b, request("a", def, 1))
	holder.next(t)
	if e := holder.next(t); e.Grant == nil {
		t.Fatalf("holder got %+v, want its grant", e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	second := start(ctx, b, request("b", def, 1))
	second.next(t)
	e := second.next(t)
	if e.Grant == nil {
		t.Fatalf("second request got %+v, want its grant", e)
	}
	cancel()
	if err := second.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("second Acquire = %v, want context.Canceled", err)
	}
	return e.Grant.Members[0].Name
}

// wantFree fails the test unless member of pool fleet is in state want and no grant holds it.
func wantFree(t *testing.T, b *Broker, member string, want MemberState) {
	t.Helper()
	if state, grant := memberOf(t, b, "fleet", member); state != want || grant != "" {
		t.Fatalf("%s is %v holding %q, want %v and free", member, state, grant, want)
	}
}

// TestTickSleepsFreeAdmittedMemberOnNonzeroStatus checks that a free admitted member whose status exits nonzero
// becomes asleep, and that the next request wakes it and is granted it.
func TestTickSleepsFreeAdmittedMemberOnNonzeroStatus(t *testing.T) {
	f := newFakes(t)
	b, _ := newBroker(t, f.env()...)
	p := statusPool(t, 2, "m1", "m2")
	tick := runTicks(t, b)
	free := admittedFree(t, b, p)
	f.setMode(t, free, "down")
	tick.send(t)
	awaitStatuses(t, b, f, 4)
	wantFree(t, b, free, Asleep)
	next := start(t.Context(), b, request("c", p, 1))
	if e := next.next(t); e.Position != 1 {
		t.Fatalf("first event = %+v, want position 1", e)
	}
	granted := next.next(t).Grant
	if granted == nil {
		t.Fatal("next request was not granted")
	}
	if !slices.Equal(granted.Members, []Member{{Pool: "fleet", Name: free}}) {
		t.Fatalf("grant members = %+v, want only %s", granted.Members, free)
	}
	if woken := lines(t, f.wakes); !slices.Equal(woken, []string{free}) {
		t.Fatalf("woken members = %v, want [%s]", woken, free)
	}
}

// TestTickLeavesAdmittedMemberAdmittedOnZeroStatus checks that an admitted member whose status exits 0 stays admitted,
// so the next request is granted it without a wake.
func TestTickLeavesAdmittedMemberAdmittedOnZeroStatus(t *testing.T) {
	f := newFakes(t)
	b, _ := newBroker(t, f.env()...)
	p := statusPool(t, 2, "m1", "m2")
	tick := runTicks(t, b)
	free := admittedFree(t, b, p)
	tick.send(t)
	awaitStatuses(t, b, f, 4)
	if state, grant := memberOf(t, b, "fleet", free); state != Admitted || grant != "" {
		t.Fatalf("%s is %v holding %q after its status exits 0, want admitted and free", free, state, grant)
	}
	next := start(t.Context(), b, request("c", p, 1))
	if e := next.next(t); e.Position != 1 {
		t.Fatalf("first event = %+v, want position 1", e)
	}
	granted := next.next(t).Grant
	if granted == nil || granted.Members[0].Name != free {
		t.Fatalf("next request got %+v, want %s granted", granted, free)
	}
	if woken := lines(t, f.wakes); len(woken) != 0 {
		t.Fatalf("woken members = %v, want none", woken)
	}
}

// TestTickPassingStatusLimitLeavesMemberUnavailable checks that a status that passes its limit leaves its free member
// unavailable, so a waiting request neither is granted it nor wakes it.
func TestTickPassingStatusLimitLeavesMemberUnavailable(t *testing.T) {
	f := newFakes(t)
	b, _ := newBroker(t, f.env()...)
	p := statusPool(t, 2, "m1", "m2")
	p.StatusLimit = time.Second
	tick := runTicks(t, b)
	free := admittedFree(t, b, p)
	f.setMode(t, free, "hang")
	tick.send(t)
	awaitStatuses(t, b, f, 4)
	wantFree(t, b, free, Unavailable)
	next := start(t.Context(), b, request("c", p, 1))
	if e := next.next(t); e.Position != 1 {
		t.Fatalf("first event = %+v, want position 1", e)
	}
	wantFree(t, b, free, Unavailable)
	if woken := lines(t, f.wakes); len(woken) != 0 {
		t.Fatalf("woken members = %v, want none", woken)
	}
	if got := queuedProjects(b); !slices.Equal(got, []string{"c"}) {
		t.Fatalf("queued projects = %v, want [c] still waiting", got)
	}
}

// TestTickNonzeroKeepsUnavailableAndZeroAwakensIt checks that a nonzero status leaves an unavailable member
// unavailable, and that a later status that exits 0 makes it awake again.
func TestTickNonzeroKeepsUnavailableAndZeroAwakensIt(t *testing.T) {
	f := newFakes(t)
	b, _ := newBroker(t, f.env()...)
	p := statusPool(t, 2, "m1", "m2")
	p.StatusLimit = time.Second
	tick := runTicks(t, b)
	free := admittedFree(t, b, p)
	f.setMode(t, free, "hang")
	tick.send(t)
	awaitStatuses(t, b, f, 4)
	wantFree(t, b, free, Unavailable)
	f.setMode(t, free, "down")
	tick.send(t)
	awaitStatuses(t, b, f, 6)
	wantFree(t, b, free, Unavailable)
	f.setMode(t, free, "up")
	tick.send(t)
	awaitStatuses(t, b, f, 8)
	wantFree(t, b, free, Awake)
	next := start(t.Context(), b, request("c", p, 1))
	if e := next.next(t); e.Position != 1 {
		t.Fatalf("first event = %+v, want position 1", e)
	}
	if granted := next.next(t).Grant; granted == nil || granted.Members[0].Name != free {
		t.Fatalf("next request got %+v, want %s granted", granted, free)
	}
	if woken := lines(t, f.wakes); len(woken) != 0 {
		t.Fatalf("woken members = %v, want none", woken)
	}
}

// TestTickLeavesGrantedMemberItsGrant checks that a granted member keeps its grant whatever its status says: the
// status records the member's state, and the grant stays stored and held.
func TestTickLeavesGrantedMemberItsGrant(t *testing.T) {
	cases := []struct {
		mode string
		want MemberState
	}{
		{"down", Asleep},
		{"hang", Unavailable},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			f := newFakes(t)
			b, s := newBroker(t, f.env()...)
			p := statusPool(t, 1, "m1")
			p.StatusLimit = time.Second
			tick := runTicks(t, b)
			holder := start(t.Context(), b, request("a", p, 1))
			holder.next(t)
			held := holder.next(t).Grant
			if held == nil {
				t.Fatal("holder was not granted")
			}
			f.setMode(t, "m1", tc.mode)
			tick.send(t)
			awaitStatuses(t, b, f, 2)
			if state, grant := memberOf(t, b, "fleet", "m1"); state != tc.want || grant != held.ID {
				t.Fatalf("m1 is %v holding %q, want %v holding grant %s", state, grant, tc.want, held.ID)
			}
			if rows := storedGrants(t, s); len(rows) != 1 || rows[0].ID != held.ID {
				t.Fatalf("stored grants = %+v, want only grant %s", rows, held.ID)
			}
		})
	}
}

// TestTickDoesNotDoubleAStatusStillRunning checks that a member whose status is still running gets no second status
// on the next tick, and that its next tick after the run ends starts one.
func TestTickDoesNotDoubleAStatusStillRunning(t *testing.T) {
	f := newFakes(t)
	b, _ := newBroker(t, f.env()...)
	p := statusPool(t, 1, "m1")
	tick := runTicks(t, b)
	holder := start(t.Context(), b, request("a", p, 1))
	holder.next(t)
	if e := holder.next(t); e.Grant == nil {
		t.Fatalf("holder got %+v, want its grant", e)
	}
	f.setMode(t, "m1", "gate")
	tick.send(t)
	tick.send(t)
	tick.drain(t)
	if n := runningCommands(b, "fleet"); n != 1 {
		t.Fatalf("%d member commands run for p while the status is held, want one", n)
	}
	f.openGate(t)
	awaitStatuses(t, b, f, 2)
	again := runTicks(t, b)
	again.send(t)
	awaitStatuses(t, b, f, 3)
}

// TestTickSkipsMemberWhoseWakeRuns checks that a tick gives no status to a member whose wake is running, and that the
// request waiting for it is granted once the wake ends, with one wake and no status in between.
func TestTickSkipsMemberWhoseWakeRuns(t *testing.T) {
	f := newFakes(t)
	b, _ := newBroker(t, f.env()...)
	p := poolOf(t, "fleet", 1, []string{"m1"}, statusBody, wakeBody+"\n"+holdGate, healthy)
	tick := runTicks(t, b)
	ctx, cancel := context.WithCancel(t.Context())
	holder := start(ctx, b, request("a", p, 1))
	holder.next(t)
	if e := holder.next(t); e.Grant == nil {
		t.Fatalf("holder got %+v, want its grant", e)
	}
	f.setMode(t, "m1", "down")
	tick.send(t)
	awaitStatuses(t, b, f, 2)
	waiter := start(t.Context(), b, request("c", p, 1))
	if e := waiter.next(t); e.Position != 1 {
		t.Fatalf("waiter event = %+v, want position 1", e)
	}
	cancel()
	if err := holder.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("holder Acquire = %v, want context.Canceled", err)
	}
	wantFree(t, b, "m1", Waking)
	tick.send(t)
	tick.drain(t)
	if n := runningCommands(b, "fleet"); n != 1 {
		t.Fatalf("%d member commands run for p during the wake, want the wake alone", n)
	}
	f.openGate(t)
	granted := waiter.next(t).Grant
	if granted == nil {
		t.Fatal("waiter was not granted after its wake")
	}
	if runs := lines(t, f.runs); len(runs) != 2 {
		t.Fatalf("status runs = %v, want the two from before the wake", runs)
	}
	if woken := lines(t, f.wakes); !slices.Equal(woken, []string{"m1"}) {
		t.Fatalf("woken members = %v, want one wake of m1", woken)
	}
}

// TestRequestDoesNotPrepareMemberWhoseStatusRuns checks that a waiting request does not wake or admit a member whose
// status is still running: the member is left awake until that status ends, and then it is admitted for the request.
func TestRequestDoesNotPrepareMemberWhoseStatusRuns(t *testing.T) {
	f := newFakes(t)
	b, _ := newBroker(t, f.env()...)
	p := statusPool(t, 2, "m1", "m2")
	tick := runTicks(t, b)
	holder := start(t.Context(), b, request("a", p, 1))
	holder.next(t)
	e := holder.next(t)
	if e.Grant == nil {
		t.Fatalf("holder got %+v, want its grant", e)
	}
	awaitIdle(t, b)
	other := "m1"
	if e.Grant.Members[0].Name == other {
		other = "m2"
	}
	f.setMode(t, other, "gate")
	tick.send(t)
	await(t, "the gated status to start", func() bool { return runningCommands(b, "fleet") == 1 })
	waiter := start(t.Context(), b, request("c", p, 1))
	if e := waiter.next(t); e.Position != 1 {
		t.Fatalf("waiter event = %+v, want position 1", e)
	}
	wantFree(t, b, other, Awake)
	if n := runningCommands(b, "fleet"); n != 1 {
		t.Fatalf("%d member commands run for p while the status is held, want the status alone", n)
	}
	f.openGate(t)
	granted := waiter.next(t).Grant
	if granted == nil || granted.Members[0].Name != other {
		t.Fatalf("waiter got %+v, want %s granted once its status ends", granted, other)
	}
	if woken := lines(t, f.wakes); len(woken) != 0 {
		t.Fatalf("woken members = %v, want none", woken)
	}
}

// TestRunReturnsWhenItsContextEndsOrItsTickCloses checks the two ways Run returns that the orreryd loop relies on.
func TestRunReturnsWhenItsContextEndsOrItsTickCloses(t *testing.T) {
	cases := []struct {
		name string
		stop func(cancel context.CancelFunc, tick chan time.Time)
	}{
		{"context ends", func(cancel context.CancelFunc, _ chan time.Time) { cancel() }},
		{"tick closes", func(_ context.CancelFunc, tick chan time.Time) { close(tick) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := newBroker(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			tick := make(chan time.Time)
			done := make(chan struct{})
			go func() {
				b.Run(ctx, tick)
				close(done)
			}()
			tc.stop(cancel, tick)
			select {
			case <-done:
			case <-time.After(guard):
				t.Fatal("Run did not return within the guard")
			}
		})
	}
}
