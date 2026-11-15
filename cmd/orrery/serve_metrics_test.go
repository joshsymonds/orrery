package main

import (
	"bytes"
	"context"
	"io"
	"maps"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// serveRPCSeriesFamily is the family of the otelconnect server metrics.
const serveRPCSeriesFamily = "rpc_server_call_duration_seconds"

// TestServeMetricsListenScrapesConnectSeries runs serve through runServe with --metrics-listen on a free loopback
// port. After one Pools.Status call through the guard, a scrape of /metrics holds the call's server series, labeled
// with only its procedure and its code, and the process CPU series.
func TestServeMetricsListenScrapesConnectSeries(t *testing.T) {
	t.Parallel()

	state := t.TempDir()
	getenv := func(key string) string { return map[string]string{"XDG_STATE_HOME": state}[key] }
	addr := serveFreeLoopbackAddr(t)
	metricsAddr := serveFreeLoopbackAddr(t)
	opts := globals{server: tmuxctl.Server{Name: "orrery-serve-test-unstarted"}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runServe(ctx, opts, []string{
			"--listen", addr, "--allow-login", serveTestLogin, "--metrics-listen", metricsAddr,
		}, getenv)
	}()
	serveAwaitListener(t, addr, done)

	// The call answers unavailable with no daemon behind serve. The series counts the call whatever its answer.
	httpClient := &http.Client{Transport: serveLoginTransport{base: http.DefaultTransport}}
	_, _ = orreryv1connect.NewPoolsClient(httpClient, "http://"+addr).Status(
		ctx, connect.NewRequest(&orreryv1.PoolsStatusRequest{}))

	families := serveScrape(ctx, t, metricsAddr)
	method := strings.TrimPrefix(orreryv1connect.PoolsStatusProcedure, "/")
	wantLabels := []string{"rpc_method", "rpc_response_status_code"}
	rpc, ok := families[serveRPCSeriesFamily]
	if !ok {
		t.Fatalf("scrape has no %s family", serveRPCSeriesFamily)
	}
	var found bool
	for _, m := range rpc.GetMetric() {
		labels := make(map[string]string, len(m.GetLabel()))
		for _, pair := range m.GetLabel() {
			labels[pair.GetName()] = pair.GetValue()
		}
		if names := slices.Sorted(maps.Keys(labels)); !slices.Equal(names, wantLabels) {
			t.Errorf("series labels = %v, want exactly %v", names, wantLabels)
		}
		if labels["rpc_method"] == method && m.GetHistogram().GetSampleCount() == 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("scrape has no %s_count series of one call for %s", serveRPCSeriesFamily, method)
	}
	if _, ok := families["process_cpu_seconds_total"]; !ok {
		t.Error("scrape has no process_cpu_seconds_total family")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runServe returned %v, want nil", err)
		}
	case <-time.After(serveTestWait):
		t.Fatalf("runServe did not return within %v of cancel", serveTestWait)
	}
}

// serveScrape fetches /metrics from addr and parses the exposition into its families by name.
func serveScrape(ctx context.Context, t *testing.T, addr string) map[string]*dto.MetricFamily {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/metrics", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Errorf("close body: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("read /metrics: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("parse exposition: %v", err)
	}
	return families
}

// TestServeMetricsListenRefusesNonLoopback runs serve through runServe with a --metrics-listen that is not a loopback
// IP literal. runServe returns a failure, which exits 1, and binds neither its API address nor the metrics address.
func TestServeMetricsListenRefusesNonLoopback(t *testing.T) {
	t.Parallel()

	state := t.TempDir()
	getenv := func(key string) string { return map[string]string{"XDG_STATE_HOME": state}[key] }
	opts := globals{server: tmuxctl.Server{Name: "orrery-serve-test-unstarted"}}
	_, port, err := net.SplitHostPort(serveFreeLoopbackAddr(t))
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	for _, metricsAddr := range []string{"0.0.0.0:" + port, "localhost:" + port} {
		apiAddr := serveFreeLoopbackAddr(t)
		err := runServe(t.Context(), opts, []string{
			"--listen", apiAddr, "--allow-login", serveTestLogin, "--metrics-listen", metricsAddr,
		}, getenv)
		if err == nil {
			t.Fatalf("runServe --metrics-listen %s returned nil, want an error", metricsAddr)
		}
		if !strings.Contains(err.Error(), "loopback") {
			t.Errorf("runServe --metrics-listen %s error = %v, want the loopback refusal", metricsAddr, err)
		}
		var stderr bytes.Buffer
		if code := report(&stderr, err); code != exitFailure {
			t.Errorf("runServe --metrics-listen %s exit code = %d, want %d", metricsAddr, code, exitFailure)
		}
		for _, addr := range []string{apiAddr, metricsAddr} {
			ln, listenErr := net.Listen("tcp", addr)
			if listenErr != nil {
				t.Errorf("%s is bound after the refusal: %v", addr, listenErr)
				continue
			}
			if closeErr := ln.Close(); closeErr != nil {
				t.Errorf("close probe listener on %s: %v", addr, closeErr)
			}
		}
	}
}
