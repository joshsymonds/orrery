package enginehost

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/telemetry"
)

// manualClock is a WriterConfig.Now that moves only when a test advances it. It counts its reads, so a test can wait
// until a goroutine has read it before moving it on.
type manualClock struct {
	mu    sync.Mutex
	now   time.Time
	reads int
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads++
	return c.now
}

func (c *manualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// waitReads blocks until the clock has been read at least n times, failing the test after waitTimeout.
func (c *manualClock) waitReads(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for {
		c.mu.Lock()
		got := c.reads
		c.mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited for %d clock reads, got %d", n, got)
		}
		time.Sleep(time.Millisecond)
	}
}

// toggleApplier forwards to a real store while up is set, and fails every ApplyEngine call while it is not.
type toggleApplier struct {
	store *store.Store
	mu    sync.Mutex
	up    bool
}

func (a *toggleApplier) ApplyEngine(ctx context.Context, ops []store.EngineOp, now time.Time) error {
	a.mu.Lock()
	up := a.up
	a.mu.Unlock()
	if !up {
		return errors.New("store unavailable")
	}
	return a.store.ApplyEngine(ctx, ops, now)
}

func (a *toggleApplier) setUp(up bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.up = up
}

// newMetrics returns a telemetry provider and the daemon that records the orreryd series on it.
func newMetrics(t *testing.T, start time.Time) (*telemetry.Provider, *telemetry.Daemon) {
	t.Helper()
	provider, err := telemetry.New()
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}
	daemon, err := telemetry.NewDaemon(t.Context(), provider, start)
	if err != nil {
		t.Fatalf("telemetry.NewDaemon: %v", err)
	}
	return provider, daemon
}

// scrapeSample returns the value of the unlabeled series of the named counter or gauge on provider's exposition.
func scrapeSample(t *testing.T, provider *telemetry.Provider, name string) float64 {
	t.Helper()
	rec := httptest.NewRecorder()
	provider.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(rec.Body)
	if err != nil {
		t.Fatalf("parse exposition: %v", err)
	}
	family, ok := families[name]
	if !ok || len(family.GetMetric()) != 1 {
		t.Fatalf("scrape does not hold exactly one series of %s", name)
	}
	m := family.GetMetric()[0]
	switch family.GetType() {
	case dto.MetricType_COUNTER:
		return m.GetCounter().GetValue()
	case dto.MetricType_GAUGE:
		return m.GetGauge().GetValue()
	default:
		t.Fatalf("%s type = %v, want counter or gauge", name, family.GetType())
		return 0
	}
}

func TestCommitWaitIsRecordedAsWriterWait(t *testing.T) {
	t.Parallel()
	s, _ := openStore(t)
	addSessions(t, s, "a")
	clock := &manualClock{now: time.Unix(1_800_000_000, 0)}
	provider, metrics := newMetrics(t, clock.now)
	applier := &toggleApplier{store: s, up: true}
	w := NewWriter(WriterConfig{Applier: applier, Tick: time.Hour, Now: clock.Now, Metrics: metrics})

	done := make(chan error, 1)
	go func() { done <- w.Commit(t.Context(), "a", prompt("a", "committed")) }()
	// The Commit reads the clock to stamp its op, then again when it starts waiting for the op to be stored.
	clock.waitReads(t, 2)
	clock.Advance(3 * time.Second)
	w.flush(t.Context())

	if err := receive(t, done); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := scrapeSample(t, provider, "orrery_writer_wait_seconds_total"); got != 3 {
		t.Fatalf("orrery_writer_wait_seconds_total = %v, want 3 (the clock advanced 3 s while Commit waited)", got)
	}
}

func TestPutBlockedAtLimitIsRecordedAsWriterWait(t *testing.T) {
	t.Parallel()
	s, _ := openStore(t)
	addSessions(t, s, "a")
	clock := &manualClock{now: time.Unix(1_800_000_000, 0)}
	provider, metrics := newMetrics(t, clock.now)
	applier := &toggleApplier{store: s, up: true}
	w := NewWriter(WriterConfig{Applier: applier, Tick: time.Hour, Limit: 1, Now: clock.Now, Metrics: metrics})
	ctx := t.Context()

	if err := w.Put(ctx, "a", prompt("a", "a1")); err != nil {
		t.Fatalf("Put a1 under the limit: %v", err)
	}
	blocked := make(chan error, 1)
	go func() { blocked <- w.Put(ctx, "a", prompt("a", "a2")) }()
	// Put a1 read the clock to stamp its op, and Put a2 reads it again when it starts waiting for room under the limit.
	clock.waitReads(t, 2)
	clock.Advance(2 * time.Second)
	w.flush(ctx)

	if err := receive(t, blocked); err != nil {
		t.Fatalf("blocked Put after a1 was stored: %v", err)
	}
	if got := scrapeSample(t, provider, "orrery_writer_wait_seconds_total"); got != 2 {
		t.Fatalf("orrery_writer_wait_seconds_total = %v, want 2 (the clock advanced 2 s while Put was blocked)", got)
	}
}

func TestBacklogAgeRisesWhileTheApplierFailsAndReturnsToZero(t *testing.T) {
	t.Parallel()
	s, _ := openStore(t)
	addSessions(t, s, "a")
	clock := &manualClock{now: time.Unix(1_800_000_000, 0)}
	provider, metrics := newMetrics(t, clock.now)
	applier := &toggleApplier{store: s}
	w := NewWriter(WriterConfig{Applier: applier, Tick: time.Hour, Now: clock.Now, Metrics: metrics})
	ctx := t.Context()
	checkBacklog := func(want float64) {
		t.Helper()
		if got := scrapeSample(t, provider, "orrery_writer_backlog_age_seconds"); got != want {
			t.Fatalf("orrery_writer_backlog_age_seconds = %v, want %v", got, want)
		}
	}

	if err := w.Put(ctx, "a", prompt("a", "a1")); err != nil {
		t.Fatalf("Put a1: %v", err)
	}
	clock.Advance(time.Second)
	w.flush(ctx)
	checkBacklog(1)

	// a2 is queued two seconds after a1, so the backlog is still a1's age: the oldest op not yet stored.
	clock.Advance(time.Second)
	if err := w.Put(ctx, "a", prompt("a", "a2")); err != nil {
		t.Fatalf("Put a2: %v", err)
	}
	w.flush(ctx)
	checkBacklog(2)

	clock.Advance(time.Second)
	w.flush(ctx)
	checkBacklog(3)

	applier.setUp(true)
	w.flush(ctx)
	checkBacklog(0)
}
