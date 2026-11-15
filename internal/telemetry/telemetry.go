// Package telemetry gives each process one OpenTelemetry meter provider that exports to a private Prometheus
// registry, the connect metrics option, the /metrics server, and the orreryd series. Nothing here exports traces,
// remote-writes or pushes to a collector: a scraper reads /metrics.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// shutdownWait bounds how long Serve waits for in-flight scrapes once its context ends.
const shutdownWait = 5 * time.Second

// readHeaderTimeout bounds how long a scraper may take to send its request headers.
const readHeaderTimeout = 5 * time.Second

// The push failure classes. Each is present at 0 from NewDaemon.
const (
	classTransport = "transport"
	class4xx       = "4xx"
	class5xx       = "5xx"
	classOther     = "other"
)

// Provider owns the process's meter provider and the private registry its metrics are exposed on.
type Provider struct {
	registry      *prometheus.Registry
	meterProvider *sdkmetric.MeterProvider
	interceptor   *otelconnect.Interceptor
}

// New returns a Provider on a fresh registry, never the global default registerer.
func New() (*Provider, error) {
	registry := prometheus.NewRegistry()
	if err := registry.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})); err != nil {
		return nil, fmt.Errorf("register process collector: %w", err)
	}
	reader, err := otelprom.New(
		otelprom.WithRegisterer(registry),
		otelprom.WithoutScopeInfo(),
		otelprom.WithoutTargetInfo(),
	)
	if err != nil {
		return nil, fmt.Errorf("create prometheus exporter: %w", err)
	}
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	interceptor, err := otelconnect.NewInterceptor(
		otelconnect.WithMeterProvider(meterProvider),
		otelconnect.WithoutTracing(),
		otelconnect.WithAttributeFilter(keepAttribute),
	)
	if err != nil {
		return nil, fmt.Errorf("create connect interceptor: %w", err)
	}
	return &Provider{registry: registry, meterProvider: meterProvider, interceptor: interceptor}, nil
}

// keepAttribute keeps the two labels of the connect metrics: rpc.method is the procedure and
// rpc.response.status_code is the code. Every other attribute otelconnect adds is dropped.
func keepAttribute(_ connect.Spec, kv attribute.KeyValue) bool {
	return kv.Key == "rpc.method" || kv.Key == "rpc.response.status_code"
}

// HandlerOptions record the server metrics of every RPC on the handler they are passed to.
func (p *Provider) HandlerOptions() []connect.HandlerOption {
	return []connect.HandlerOption{connect.WithInterceptors(p.interceptor)}
}

// Handler serves the registry's exposition.
func (p *Provider) Handler() http.Handler {
	return promhttp.HandlerFor(p.registry, promhttp.HandlerOpts{})
}

// Serve answers GET /metrics from Handler on ln until ctx ends. It then shuts down within shutdownWait and returns nil.
func (p *Provider) Serve(ctx context.Context, ln net.Listener) error {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", p.Handler())
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: readHeaderTimeout}

	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		return fmt.Errorf("serve metrics: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownWait)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		// Shutdown ran out of time with a scrape still in flight. Close drops it.
		if closeErr := srv.Close(); closeErr != nil {
			return fmt.Errorf("close metrics server: %w", closeErr)
		}
	}
	if err := <-done; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve metrics: %w", err)
	}
	return nil
}

// Daemon records the orreryd series. Every method on a nil Daemon does nothing.
type Daemon struct {
	ready            metric.Float64Gauge
	pushFailures     metric.Int64Counter
	holderLosses     metric.Int64Counter
	writerWait       metric.Float64Counter
	writerBacklogAge metric.Float64Gauge
}

// NewDaemon registers the orreryd series on p and records start as the start timestamp. Every other series is present
// at 0 until its method moves it.
func NewDaemon(p *Provider, start time.Time) (*Daemon, error) {
	meter := p.meterProvider.Meter("github.com/joshsymonds/orrery/internal/telemetry")
	startGauge, err := meter.Float64Gauge(
		"orrery_start_timestamp_seconds",
		metric.WithDescription("Unix time at which the process started."),
	)
	if err != nil {
		return nil, fmt.Errorf("create start timestamp gauge: %w", err)
	}
	ready, err := meter.Float64Gauge(
		"orrery_ready_timestamp_seconds",
		metric.WithDescription("Unix time at which the daemon became ready, 0 until then."),
	)
	if err != nil {
		return nil, fmt.Errorf("create ready timestamp gauge: %w", err)
	}
	pushFailures, err := meter.Int64Counter(
		"orrery_push_failures_total",
		metric.WithDescription("Failed pushes, by class of failure."),
	)
	if err != nil {
		return nil, fmt.Errorf("create push failures counter: %w", err)
	}
	holderLosses, err := meter.Int64Counter(
		"orrery_holder_losses_total",
		metric.WithDescription("Times the daemon lost its holder."),
	)
	if err != nil {
		return nil, fmt.Errorf("create holder losses counter: %w", err)
	}
	writerWait, err := meter.Float64Counter(
		"orrery_writer_wait_seconds_total",
		metric.WithDescription("Seconds spent waiting on the writer."),
	)
	if err != nil {
		return nil, fmt.Errorf("create writer wait counter: %w", err)
	}
	writerBacklogAge, err := meter.Float64Gauge(
		"orrery_writer_backlog_age_seconds",
		metric.WithDescription("Age in seconds of the oldest write in the writer backlog."),
	)
	if err != nil {
		return nil, fmt.Errorf("create writer backlog age gauge: %w", err)
	}

	ctx := context.Background()
	startGauge.Record(ctx, unixSeconds(start))
	ready.Record(ctx, 0)
	writerBacklogAge.Record(ctx, 0)
	holderLosses.Add(ctx, 0)
	writerWait.Add(ctx, 0)
	for _, class := range []string{classTransport, class4xx, class5xx, classOther} {
		pushFailures.Add(ctx, 0, metric.WithAttributes(attribute.String("class", class)))
	}
	return &Daemon{
		ready:            ready,
		pushFailures:     pushFailures,
		holderLosses:     holderLosses,
		writerWait:       writerWait,
		writerBacklogAge: writerBacklogAge,
	}, nil
}

// Ready records at as the time the daemon became ready.
func (d *Daemon) Ready(at time.Time) {
	if d == nil {
		return
	}
	d.ready.Record(context.Background(), unixSeconds(at))
}

// PushFailed counts a failed push by the class of its HTTP status. Status 0 means no response was received.
func (d *Daemon) PushFailed(status int) {
	if d == nil {
		return
	}
	class := attribute.String("class", pushFailureClass(status))
	d.pushFailures.Add(context.Background(), 1, metric.WithAttributes(class))
}

// HolderLost counts one loss of the holder.
func (d *Daemon) HolderLost() {
	if d == nil {
		return
	}
	d.holderLosses.Add(context.Background(), 1)
}

// WriterWaited adds wait to the time spent waiting on the writer.
func (d *Daemon) WriterWaited(wait time.Duration) {
	if d == nil {
		return
	}
	d.writerWait.Add(context.Background(), wait.Seconds())
}

// WriterBacklog records age as the age of the oldest write in the writer backlog.
func (d *Daemon) WriterBacklog(age time.Duration) {
	if d == nil {
		return
	}
	d.writerBacklogAge.Record(context.Background(), age.Seconds())
}

// pushFailureClass names the class of a failed push by its HTTP status.
func pushFailureClass(status int) string {
	switch {
	case status == 0:
		return classTransport
	case status >= 400 && status <= 499:
		return class4xx
	case status >= 500 && status <= 599:
		return class5xx
	default:
		return classOther
	}
}

// unixSeconds is t as fractional unix seconds, the unit of the timestamp gauges.
func unixSeconds(t time.Time) float64 {
	return float64(t.Unix()) + float64(t.Nanosecond())/1e9
}
