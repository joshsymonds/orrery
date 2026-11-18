package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/enginehost"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic values for the cron wiring test. The host, the session, the job and the zone are invented.
const (
	cronWiringHost    = "cron-wiring-host"
	cronWiringSession = "cron-wiring-session"
	cronWiringTool    = "toolu_cron_wiring"
	cronWiringCron    = "30 10 * * *"
	cronWiringPrompt  = "the cron wiring prompt"
	// cronWiringOffset is the offset, in seconds, of the zone the test reads times in: UTC+9, never UTC.
	cronWiringOffset = 9 * 3600
	cronWiringDay    = 24 * time.Hour
	// cronWiringBuffer sizes the channels the fakes record on, so the host never blocks on them.
	cronWiringBuffer = 8
)

// cronWiringClock is the clock the cron answers and the cron scheduler read as now. The test sets it while the host's
// goroutines read it, so it is guarded.
type cronWiringClock struct {
	mu sync.Mutex
	at time.Time
}

// now returns the clock's time.
func (c *cronWiringClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

// set moves the clock to at.
func (c *cronWiringClock) set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = at
}

// cronWiringTimer is a CronTimer that the test fires by sending on c. d is the duration it was armed for.
type cronWiringTimer struct {
	c chan time.Time
	d time.Duration
}

// C returns the channel the test fires the timer on.
func (t *cronWiringTimer) C() <-chan time.Time { return t.c }

// Stop returns true: the test's timer runs no clock of its own, so there is nothing to stop.
func (t *cronWiringTimer) Stop() bool { return true }

// cronWiringZone returns the zone the test reads times in.
func cronWiringZone() *time.Location {
	return time.FixedZone("JST", cronWiringOffset)
}

// cronWiringCreated is when the test creates its job: 2026-11-18 10:02:30 in cronWiringZone.
func cronWiringCreated() time.Time {
	return time.Date(2026, time.November, 18, 10, 2, 30, 0, cronWiringZone())
}

// cronWiringDue is the first due fire of the job: the 10:30 match after its creation.
func cronWiringDue() time.Time {
	return time.Date(2026, time.November, 18, 10, 30, 0, 0, cronWiringZone())
}

// cronWiringHTTP returns an HTTP client that dials the unix socket at path.
func cronWiringHTTP(t *testing.T, path string) *http.Client {
	t.Helper()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", path)
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport}
}

// createCronWiringJob creates the test's job through the Crons service on the session socket at path, from
// cronWiringSession, and returns the job the store then holds.
func createCronWiringJob(t *testing.T, path string, st *store.Store) store.CronJob {
	t.Helper()
	client := enginev1connect.NewCronsClient(cronWiringHTTP(t, path), "http://crons.invalid")
	create := connect.NewRequest(&v1.CronCallRequest{
		ToolName:  "CronCreate",
		ToolUseId: cronWiringTool,
		ToolInput: fmt.Sprintf(`{"cron":%q,"prompt":%q}`, cronWiringCron, cronWiringPrompt),
	})
	create.Header().Set(protocol.HeaderSession, cronWiringSession)
	if _, err := client.CronCall(t.Context(), create); err != nil {
		t.Fatalf("CronCreate over the session socket: %v", err)
	}
	jobs, err := st.CronJobs(t.Context(), cronWiringSession)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("stored jobs of %s = %+v, err %v; want the one job", cronWiringSession, jobs, err)
	}
	return jobs[0]
}

// TestCronFireThroughOrrerydWiring checks that a --sessions orreryd fires a cron job through its wiring. A CronCreate
// call on the session socket stores the job, and the scheduler arms its timer for the job's due fire. Firing that timer
// before the due time delivers nothing. Firing it at the due time delivers the job's prompt to the session in the
// Orrery-Session header, once, and re-arms the timer for the next match. The host then stops without error.
func TestCronFireThroughOrrerydWiring(t *testing.T) {
	ctx := t.Context()
	env := map[string]string{
		"XDG_STATE_HOME":  t.TempDir(),
		"XDG_CONFIG_HOME": t.TempDir(),
	}
	getenv := func(key string) string { return env[key] }
	path, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store path: %v", err)
	}
	st := openPaneStore(t, path)

	clock := &cronWiringClock{at: cronWiringCreated()}
	timers := make(chan *cronWiringTimer, cronWiringBuffer)
	delivered := make(chan enginehost.DeliverRequest, cronWiringBuffer)
	opts := options{
		socket:     filepath.Join(t.TempDir(), "orreryd.sock"),
		sessionCap: defaultSessionCap,
		stepCap:    defaultStepCap,
		cronNow:    clock.now,
		cronTimers: func(d time.Duration) enginehost.CronTimer {
			timer := &cronWiringTimer{c: make(chan time.Time, 1), d: d}
			timers <- timer
			return timer
		},
		cronDeliver: func(_ context.Context, req enginehost.DeliverRequest) error {
			delivered <- req
			return nil
		},
	}
	host, err := startSessions(ctx, opts, cronWiringHost, st, nil, nil, slog.New(slog.DiscardHandler), getenv)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}

	job := createCronWiringJob(t, protocol.SessionSocketPath(opts.socket), st)

	// The creation arms the timer for the time from the clock now to the due fire.
	due := cronWiringDue()
	wait := due.Sub(cronWiringCreated())
	armed := <-timers
	if armed.d != wait {
		t.Fatalf("timer armed for %v after CronCreate, want %v, the time to the due fire", armed.d, wait)
	}

	// Fired before the due time, the timer delivers nothing and is armed for the same wait.
	armed.c <- time.Time{}
	armed = <-timers
	if armed.d != wait {
		t.Fatalf("timer re-armed for %v before due, want %v", armed.d, wait)
	}
	if n := len(delivered); n != 0 {
		t.Fatalf("%d deliveries before the due time, want none", n)
	}

	// Fired at the due time, the timer delivers the job's prompt to the header's session, once.
	clock.set(due)
	armed.c <- time.Time{}
	want := enginehost.DeliverRequest{
		ID: cronWiringSession, Text: cronWiringPrompt, PromptID: fmt.Sprintf("cron:%s:%d", job.ID, due.Unix()),
	}
	if got := <-delivered; got != want {
		t.Fatalf("delivered %+v at due, want %+v", got, want)
	}
	// The job recurs, so the fire arms the timer for the next day's match, which the scheduler does after it delivers.
	if armed = <-timers; armed.d != cronWiringDay {
		t.Fatalf("timer armed for %v after the fire, want %v, the next match", armed.d, cronWiringDay)
	}
	if n := len(delivered); n != 0 {
		t.Fatalf("%d more deliveries after the fire, want none", n)
	}

	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}
