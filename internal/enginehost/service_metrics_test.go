package enginehost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/telemetry"
)

// TestServeRecordsCallMetricsForEveryService serves all five services with nil backends and telemetry's handler
// options, calls one procedure of each, and expects the scrape to hold one series per procedure labeled with exactly
// rpc_method and rpc_response_status_code.
func TestServeRecordsCallMetricsForEveryService(t *testing.T) {
	t.Parallel()
	provider, err := telemetry.New()
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}
	path := filepath.Join(t.TempDir(), "engine.sock")
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ln, nil, nil, nil, nil, nil, provider.HandlerOptions()...)
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve after cleanup: %v", err)
		}
	})

	const base = "http://engine.invalid"
	httpClient := unixHTTPClient(t, path)
	calls := []struct {
		procedure string
		call      func() error
	}{
		{enginev1connect.EngineStartProcedure, func() error {
			_, err := enginev1connect.NewEngineClient(httpClient, base).Start(
				t.Context(), connect.NewRequest(&v1.StartRequest{}))
			return err
		}},
		{enginev1connect.StepsStepStatusProcedure, func() error {
			_, err := enginev1connect.NewStepsClient(httpClient, base).StepStatus(
				t.Context(), connect.NewRequest(&v1.StepStatusRequest{}))
			return err
		}},
		{enginev1connect.EpicsGetEpicProcedure, func() error {
			_, err := enginev1connect.NewEpicsClient(httpClient, base).GetEpic(
				t.Context(), connect.NewRequest(&v1.GetEpicRequest{}))
			return err
		}},
		{enginev1connect.PoolsStatusProcedure, func() error {
			_, err := enginev1connect.NewPoolsClient(httpClient, base).Status(
				t.Context(), connect.NewRequest(&v1.PoolsStatusRequest{}))
			return err
		}},
		{enginev1connect.EvidenceListEvidenceProcedure, func() error {
			_, err := enginev1connect.NewEvidenceClient(httpClient, base).ListEvidence(
				t.Context(), connect.NewRequest(&v1.ListEvidenceRequest{}))
			return err
		}},
	}
	for _, c := range calls {
		if got := connect.CodeOf(c.call()); got != connect.CodeUnavailable {
			t.Fatalf("%s code = %v, want %v", c.procedure, got, connect.CodeUnavailable)
		}
	}

	family := scrapeCallDurations(t, provider)
	for _, c := range calls {
		want := strings.TrimPrefix(c.procedure, "/")
		series := callSeries(t, family, want)
		if got := series.GetHistogram().GetSampleCount(); got != 1 {
			t.Fatalf("%s sample count = %d, want 1", want, got)
		}
	}
}

// scrapeCallDurations returns the rpc_server_call_duration_seconds family from provider's exposition.
func scrapeCallDurations(t *testing.T, provider *telemetry.Provider) *dto.MetricFamily {
	t.Helper()
	rec := httptest.NewRecorder()
	provider.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics status = %d, want %d", rec.Code, http.StatusOK)
	}
	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(rec.Body)
	if err != nil {
		t.Fatalf("parse exposition: %v", err)
	}
	family, ok := families["rpc_server_call_duration_seconds"]
	if !ok {
		t.Fatal("scrape has no rpc_server_call_duration_seconds family")
	}
	return family
}

// callSeries returns the one series of family whose rpc_method is method. It fails the test unless the series carries
// exactly the rpc_method and rpc_response_status_code labels, with the code UNAVAILABLE.
func callSeries(t *testing.T, family *dto.MetricFamily, method string) *dto.Metric {
	t.Helper()
	var found *dto.Metric
	for _, m := range family.GetMetric() {
		labels := make(map[string]string, len(m.GetLabel()))
		for _, pair := range m.GetLabel() {
			labels[pair.GetName()] = pair.GetValue()
		}
		if labels["rpc_method"] != method {
			continue
		}
		if len(labels) != 2 || labels["rpc_response_status_code"] != "UNAVAILABLE" {
			t.Fatalf("%s series labels = %v, want exactly rpc_method and rpc_response_status_code=UNAVAILABLE",
				method, labels)
		}
		found = m
	}
	if found == nil {
		t.Fatalf("scrape has no rpc_server_call_duration_seconds series for %s", method)
	}
	return found
}
