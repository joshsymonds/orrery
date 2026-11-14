package desk_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/desk"
	"github.com/joshsymonds/orrery/internal/deskstream"
)

const (
	testHost = "box"
	lostWait = 5 * time.Minute
	// ioTimeout bounds each wait for the link to reach the state a step needs.
	ioTimeout    = 5 * time.Second
	pollInterval = 5 * time.Millisecond
)

// testStart is the fake clock's first instant.
func testStart() time.Time {
	return time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
}

// streamScript is the fake stream command, run as /bin/sh script dir. Each run counts itself in dir/starts. Session n
// runs dir/plan.n when it exists, which ends the run unless the plan is a wait. Otherwise the run waits until
// dir/stop.n exists.
const streamScript = `dir=$1
echo started >> "$dir/starts"
n=$(wc -l < "$dir/starts" | tr -d ' ')
if [ -f "$dir/plan.$n" ]; then . "$dir/plan.$n"; fi
while [ ! -f "$dir/stop.$n" ]; do sleep 0.01; done
`

// fakeClock is a desk.Clock the test moves by hand. After registers a timer that Advance fires once it is due.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
	calls  []time.Duration
}

type fakeTimer struct {
	at   time.Time
	wait time.Duration
	ch   chan time.Time
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(wait time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.timers = append(c.timers, &fakeTimer{at: c.now.Add(wait), wait: wait, ch: ch})
	c.calls = append(c.calls, wait)
	return ch
}

// Advance moves the clock forward by by and fires each timer that is now due.
func (c *fakeClock) Advance(by time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(by)
	waiting := c.timers[:0]
	for _, timer := range c.timers {
		if timer.at.After(c.now) {
			waiting = append(waiting, timer)
			continue
		}
		timer.ch <- c.now
	}
	c.timers = waiting
}

// pending reports whether a timer of length wait has not fired yet.
func (c *fakeClock) pending(wait time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, timer := range c.timers {
		if timer.wait == wait {
			return true
		}
	}
	return false
}

// callCount counts the After calls made for wait.
func (c *fakeClock) callCount(wait time.Duration) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, call := range c.calls {
		if call == wait {
			count++
		}
	}
	return count
}

// restartWaits returns the After calls made for anything but the lost wait, in order.
func (c *fakeClock) restartWaits() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	var waits []time.Duration
	for _, call := range c.calls {
		if call != lostWait {
			waits = append(waits, call)
		}
	}
	return waits
}

// recorder collects what the link reports.
type recorder struct {
	mu      sync.Mutex
	notices []deskstream.Line
	lost    int
}

func (r *recorder) notify(line deskstream.Line) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notices = append(r.notices, line)
}

func (r *recorder) onLost(_ string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lost++
}

func (r *recorder) noticeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.notices)
}

func (r *recorder) lostCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lost
}

func (r *recorder) noticeList() []deskstream.Line {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.notices)
}

// fixture is one link running the fake stream in its own directory on a fake clock.
type fixture struct {
	t      *testing.T
	dir    string
	clock  *fakeClock
	rec    *recorder
	link   *desk.Link
	cancel context.CancelFunc
	done   chan error
}

// newFixture prepares a link that has not started.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "stream.sh")
	writeFile(t, script, streamScript)

	clock := newFakeClock(testStart())
	rec := &recorder{}
	link := &desk.Link{
		Host:    testHost,
		Command: []string{"/bin/sh", script, dir},
		Notify:  rec.notify,
		Lost:    rec.onLost,
		Clock:   clock,
	}
	return &fixture{t: t, dir: dir, clock: clock, rec: rec, link: link, done: make(chan error, 1)}
}

// run starts the link. Plans must be written before it runs, so session one sees them.
func (f *fixture) run() {
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.t.Cleanup(cancel)
	go func() { f.done <- f.link.Run(ctx) }()
}

// finish ends the link and checks that Run returned nil once the command exited.
func (f *fixture) finish() {
	f.t.Helper()
	f.cancel()
	select {
	case err := <-f.done:
		if err != nil {
			f.t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(ioTimeout):
		f.t.Fatal("Run did not return after ctx ended")
	}
}

// plan sets what session n does, as shell run in place of the default wait.
func (f *fixture) plan(n int, body string) {
	f.t.Helper()
	writeFile(f.t, filepath.Join(f.dir, fmt.Sprintf("plan.%d", n)), body)
}

// stop ends session n, whose run is waiting.
func (f *fixture) stop(n int) {
	f.t.Helper()
	writeFile(f.t, filepath.Join(f.dir, fmt.Sprintf("stop.%d", n)), "")
}

// starts counts the sessions the command has begun.
func (f *fixture) starts() int {
	return len(readLines(f.dir, "starts"))
}

// emit is a shell body that prints each line on stdout.
func emit(lines ...string) string {
	var b strings.Builder
	b.WriteString("printf '%s\\n'")
	for _, line := range lines {
		b.WriteString(" '" + line + "'")
	}
	b.WriteString("\n")
	return b.String()
}

const (
	helloLine   = `{"kind":"hello","host":"box"}`
	statusLine  = `{"kind":"status","host":"box"}`
	exitFailure = "exit 1\n"
)

func noticeLine(title string) string {
	return fmt.Sprintf(`{"kind":"notice","host":"box","title":%q}`, title)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readLines(dir, name string) []string {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil || len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// waitFor polls cond until it holds and fails the test after ioTimeout.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(ioTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(pollInterval)
	}
}

func TestLinkNotifiesNoticesInOrder(t *testing.T) {
	f := newFixture(t)
	f.plan(1, emit(helloLine, noticeLine("first"), "not json", statusLine, noticeLine("second"), noticeLine("third")))
	f.run()

	waitFor(t, "three notices", func() bool { return f.rec.noticeCount() == 3 })
	notices := f.rec.noticeList()
	titles := make([]string, 0, len(notices))
	for _, line := range notices {
		titles = append(titles, line.Title)
	}
	if want := []string{"first", "second", "third"}; !slices.Equal(titles, want) {
		t.Fatalf("notices = %q, want %q", titles, want)
	}
	f.finish()
}

func TestLinkRestartsWithBackoffCappedAt60s(t *testing.T) {
	f := newFixture(t)
	for n := 1; n <= 8; n++ {
		f.plan(n, exitFailure)
	}
	f.run()

	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 32 * time.Second, time.Minute, time.Minute}
	for _, wait := range want {
		waitFor(t, "restart wait", func() bool { return f.clock.pending(wait) })
		f.clock.Advance(wait)
	}
	waitFor(t, "ninth session", func() bool { return f.starts() == 9 })
	if got := f.clock.restartWaits(); !slices.Equal(got, want) {
		t.Fatalf("restart waits = %v, want %v", got, want)
	}
	f.finish()
}

func TestLinkHelloResetsBackoff(t *testing.T) {
	f := newFixture(t)
	f.plan(1, exitFailure)
	f.plan(2, exitFailure)
	f.plan(3, emit(helloLine)+exitFailure)
	f.plan(4, exitFailure)
	f.run()

	for _, wait := range []time.Duration{time.Second, 2 * time.Second, time.Second, 2 * time.Second} {
		waitFor(t, "restart wait", func() bool { return f.clock.pending(wait) })
		f.clock.Advance(wait)
	}
	waitFor(t, "fifth session", func() bool { return f.starts() == 5 })
	want := []time.Duration{time.Second, 2 * time.Second, time.Second, 2 * time.Second}
	if got := f.clock.restartWaits(); !slices.Equal(got, want) {
		t.Fatalf("restart waits = %v, want %v", got, want)
	}
	f.finish()
}

func TestLinkLostRunsOnceAt5MinutesAndAgainAfterLaterLoss(t *testing.T) {
	f := newFixture(t)
	f.plan(2, emit(helloLine, noticeLine("live"))) // session 2 connects and stays up
	f.run()

	waitFor(t, "first session", func() bool { return f.starts() == 1 })
	f.clock.Advance(lostWait)
	waitFor(t, "first lost", func() bool { return f.rec.lostCount() == 1 })

	f.stop(1) // session 1 exits without a hello
	waitFor(t, "restart after session 1", func() bool { return f.clock.pending(time.Second) })
	if got := f.clock.callCount(lostWait); got != 1 {
		t.Fatalf("lost waits started = %d, want 1 for one down period", got)
	}
	f.clock.Advance(time.Second)
	waitFor(t, "live notice from session 2", func() bool { return f.rec.noticeCount() == 1 })

	f.stop(2) // a hello rearmed the lost wait, so session 2's exit starts a new one
	waitFor(t, "rearmed lost wait", func() bool { return f.clock.callCount(lostWait) == 2 })
	waitFor(t, "restart after session 2", func() bool { return f.clock.pending(time.Second) })
	f.clock.Advance(time.Second)
	waitFor(t, "third session", func() bool { return f.starts() == 3 })

	f.clock.Advance(lostWait)
	waitFor(t, "second lost", func() bool { return f.rec.lostCount() == 2 })
	f.finish()
}

func TestLinkDoesNotRunLostWhileLive(t *testing.T) {
	f := newFixture(t)
	// Session 1 says hello and prints a notice, stays live past five minutes, and prints a second notice once the test
	// releases it.
	f.plan(1, emit(helloLine, noticeLine("live"))+`while [ ! -f "$dir/go" ]; do sleep 0.01; done
`+emit(noticeLine("late")))
	f.run()

	// The link handles lines in order, so the live notice means the hello has already been handled.
	waitFor(t, "live notice", func() bool { return f.rec.noticeCount() == 1 })
	f.clock.Advance(lostWait)
	writeFile(t, filepath.Join(f.dir, "go"), "")
	waitFor(t, "late notice", func() bool { return f.rec.noticeCount() == 2 })
	if got := f.rec.lostCount(); got != 0 {
		t.Fatalf("Lost ran %d times while the stream was live, want 0", got)
	}
	f.finish()
}

func TestLinkRunRejectsEmptyCommand(t *testing.T) {
	link := &desk.Link{Host: testHost, Clock: newFakeClock(testStart())}
	if err := link.Run(context.Background()); err == nil {
		t.Fatal("Run with no command returned nil, want an error")
	}
}
