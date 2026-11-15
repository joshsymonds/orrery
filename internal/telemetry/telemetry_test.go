package telemetry_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/telemetry"
)

// unimplementedPools serves the Pools service with every RPC unimplemented, so each call answers UNIMPLEMENTED.
type unimplementedPools struct {
	enginev1connect.UnimplementedPoolsHandler
}

func TestHandlerOptionsRecordProcedureAndCodeOnly(t *testing.T) {
	t.Parallel()

	p, err := telemetry.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, handler := enginev1connect.NewPoolsHandler(unimplementedPools{}, p.HandlerOptions()...)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	client := enginev1connect.NewPoolsClient(srv.Client(), srv.URL)

	_, err = client.Status(t.Context(), connect.NewRequest(&v1.PoolsStatusRequest{}))
	if got := connect.CodeOf(err); got != connect.CodeUnimplemented {
		t.Fatalf("Status code = %v, want %v", got, connect.CodeUnimplemented)
	}

	families := scrape(t, p)
	family, ok := families["rpc_server_call_duration_seconds"]
	if !ok {
		t.Fatal("scrape has no rpc_server_call_duration_seconds family")
	}
	if got := len(family.GetMetric()); got != 1 {
		t.Fatalf("rpc_server_call_duration_seconds series = %d, want 1", got)
	}
	series := family.GetMetric()[0]
	want := map[string]string{
		"rpc_method":               "orrery.engine.v1.Pools/Status",
		"rpc_response_status_code": "UNIMPLEMENTED",
	}
	if got := labelsOf(series.GetLabel()); !sameLabels(got, want) {
		t.Fatalf("series labels = %v, want exactly %v", got, want)
	}
	if got := series.GetHistogram().GetSampleCount(); got != 1 {
		t.Fatalf("sample count = %d, want 1", got)
	}
}

func TestNewDaemonHoldsEverySeriesAtZeroButStart(t *testing.T) {
	t.Parallel()

	p, err := telemetry.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	start := time.Unix(1_800_000_000, 0)
	if _, err := telemetry.NewDaemon(t.Context(), p, start); err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}

	families := scrape(t, p)
	if got := sample(t, families, "orrery_start_timestamp_seconds", nil); got != unixSeconds(start) {
		t.Fatalf("orrery_start_timestamp_seconds = %v, want %v", got, unixSeconds(start))
	}
	if got := sample(t, families, "orrery_ready_timestamp_seconds", nil); got != 0 {
		t.Fatalf("orrery_ready_timestamp_seconds = %v, want 0 before Ready", got)
	}
	for _, class := range []string{"transport", "4xx", "5xx", "other"} {
		if got := sample(t, families, "orrery_push_failures_total", map[string]string{"class": class}); got != 0 {
			t.Fatalf("orrery_push_failures_total{class=%q} = %v, want 0", class, got)
		}
	}
	if got := sample(t, families, "orrery_holder_losses_total", nil); got != 0 {
		t.Fatalf("orrery_holder_losses_total = %v, want 0", got)
	}
	if got := sample(t, families, "orrery_writer_wait_seconds_total", nil); got != 0 {
		t.Fatalf("orrery_writer_wait_seconds_total = %v, want 0", got)
	}
	if got := sample(t, families, "orrery_writer_backlog_age_seconds", nil); got != 0 {
		t.Fatalf("orrery_writer_backlog_age_seconds = %v, want 0", got)
	}
}

func TestDaemonMethodsMoveTheirSeries(t *testing.T) {
	t.Parallel()

	p, err := telemetry.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	start := time.Unix(1_800_000_000, 0)
	d, err := telemetry.NewDaemon(t.Context(), p, start)
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}

	ready := time.Unix(1_800_000_060, 500_000_000)
	d.Ready(t.Context(), ready)
	d.PushFailed(t.Context(), 0)
	d.PushFailed(t.Context(), 400)
	d.PushFailed(t.Context(), 404)
	d.PushFailed(t.Context(), 503)
	d.PushFailed(t.Context(), 302)
	d.HolderLost(t.Context())
	d.HolderLost(t.Context())
	d.WriterWaited(t.Context(), 1500*time.Millisecond)
	d.WriterWaited(t.Context(), 500*time.Millisecond)
	d.WriterBacklog(t.Context(), 3*time.Second)
	d.WriterBacklog(t.Context(), 7500*time.Millisecond)

	families := scrape(t, p)
	checks := []struct {
		name  string
		class string
		want  float64
	}{
		{name: "orrery_push_failures_total", class: "transport", want: 1},
		{name: "orrery_push_failures_total", class: "4xx", want: 2},
		{name: "orrery_push_failures_total", class: "5xx", want: 1},
		{name: "orrery_push_failures_total", class: "other", want: 1},
	}
	for _, c := range checks {
		if got := sample(t, families, c.name, map[string]string{"class": c.class}); got != c.want {
			t.Fatalf("%s{class=%q} = %v, want %v", c.name, c.class, got, c.want)
		}
	}
	if got := sample(t, families, "orrery_start_timestamp_seconds", nil); got != unixSeconds(start) {
		t.Fatalf("orrery_start_timestamp_seconds = %v, want %v", got, unixSeconds(start))
	}
	if got := sample(t, families, "orrery_ready_timestamp_seconds", nil); got != unixSeconds(ready) {
		t.Fatalf("orrery_ready_timestamp_seconds = %v, want %v", got, unixSeconds(ready))
	}
	if got := sample(t, families, "orrery_holder_losses_total", nil); got != 2 {
		t.Fatalf("orrery_holder_losses_total = %v, want 2", got)
	}
	if got := sample(t, families, "orrery_writer_wait_seconds_total", nil); got != 2 {
		t.Fatalf("orrery_writer_wait_seconds_total = %v, want 2", got)
	}
	if got := sample(t, families, "orrery_writer_backlog_age_seconds", nil); got != 7.5 {
		t.Fatalf("orrery_writer_backlog_age_seconds = %v, want 7.5 (the latest WriterBacklog)", got)
	}
}

func TestNilDaemonMethodsDoNothing(t *testing.T) {
	t.Parallel()

	var d *telemetry.Daemon
	d.Ready(t.Context(), time.Unix(1_800_000_000, 0))
	d.PushFailed(t.Context(), 500)
	d.HolderLost(t.Context())
	d.WriterWaited(t.Context(), time.Second)
	d.WriterBacklog(t.Context(), time.Second)
}

func TestServeAnswersMetricsUntilCancelled(t *testing.T) {
	t.Parallel()

	p, err := telemetry.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	served := make(chan error, 1)
	go func() { served <- p.Serve(ctx, ln) }()

	base := "http://" + ln.Addr().String()
	status, body := get(t, base+"/metrics")
	if status != http.StatusOK {
		t.Fatalf("GET /metrics status = %d, want %d", status, http.StatusOK)
	}
	if _, ok := parseExposition(t, strings.NewReader(body))["process_cpu_seconds_total"]; !ok {
		t.Fatal("GET /metrics body has no process_cpu_seconds_total family")
	}
	if status, _ := get(t, base+"/"); status != http.StatusNotFound {
		t.Fatalf("GET / status = %d, want %d: Serve must answer only /metrics", status, http.StatusNotFound)
	}

	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve after cancel: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after its context ended")
	}
}

// scrape serves GET /metrics from p and parses the exposition.
func scrape(t *testing.T, p *telemetry.Provider) map[string]*dto.MetricFamily {
	t.Helper()
	rec := httptest.NewRecorder()
	p.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics status = %d, want %d", rec.Code, http.StatusOK)
	}
	return parseExposition(t, rec.Body)
}

func parseExposition(t *testing.T, body io.Reader) map[string]*dto.MetricFamily {
	t.Helper()
	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(body)
	if err != nil {
		t.Fatalf("parse exposition: %v", err)
	}
	return families
}

// sample returns the value of the series of name whose labels are exactly want.
func sample(t *testing.T, families map[string]*dto.MetricFamily, name string, want map[string]string) float64 {
	t.Helper()
	family, ok := families[name]
	if !ok {
		t.Fatalf("scrape has no family %s", name)
	}
	for _, m := range family.GetMetric() {
		if !sameLabels(labelsOf(m.GetLabel()), want) {
			continue
		}
		switch family.GetType() {
		case dto.MetricType_COUNTER:
			return m.GetCounter().GetValue()
		case dto.MetricType_GAUGE:
			return m.GetGauge().GetValue()
		default:
			t.Fatalf("%s type = %v, want counter or gauge", name, family.GetType())
		}
	}
	t.Fatalf("scrape has no series %s with labels %v", name, want)
	return 0
}

func labelsOf(pairs []*dto.LabelPair) map[string]string {
	labels := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		labels[pair.GetName()] = pair.GetValue()
	}
	return labels
}

func sameLabels(got, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for name, value := range want {
		if gotValue, ok := got[name]; !ok || gotValue != value {
			return false
		}
	}
	return true
}

// unixSeconds is t as fractional unix seconds, the unit of the timestamp gauges.
func unixSeconds(t time.Time) float64 {
	return float64(t.Unix()) + float64(t.Nanosecond())/1e9
}

// get fetches url with a bounded client, so a server that never answers fails the test instead of hanging it.
func get(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("request %s: %v", url, err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close body of %s: %v", url, err)
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return resp.StatusCode, string(body)
}
