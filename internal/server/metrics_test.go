package server_test

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/telemetry"
)

// connectSeriesFamily is the family of the otelconnect server metrics.
const connectSeriesFamily = "rpc_server_call_duration_seconds"

// TestEveryServiceRecordsConnectMetrics calls one procedure of each of the thirteen services through the guard and
// checks that the scrape holds a series for each procedure, labeled with exactly its procedure and its code.
func TestEveryServiceRecordsConnectMetrics(t *testing.T) {
	t.Parallel()

	// serviceProcedures names the procedure callEveryService calls of each of the thirteen Connect services serve mounts.
	serviceProcedures := []string{
		orreryv1connect.SessionsListProcedure,
		orreryv1connect.EventsWatchProcedure,
		orreryv1connect.ControlStopProcedure,
		orreryv1connect.EpicsListProcedure,
		orreryv1connect.EvidenceArtifactProcedure,
		orreryv1connect.StepsHoldProcedure,
		orreryv1connect.PoolsStatusProcedure,
		orreryv1connect.PanesListProcedure,
		orreryv1connect.HostStatusProcedure,
		orreryv1connect.PushRegisterProcedure,
		orreryv1connect.PickerAskProcedure,
		orreryv1connect.PresenceReportProcedure,
		orreryv1connect.NoticesSeenProcedure,
	}

	provider, err := telemetry.New()
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}
	handler, err := server.New(server.Config{
		Logins:         []string{allowedLogin},
		Tmux:           newTmuxClient(t),
		Push:           newPushStore(t),
		HandlerOptions: provider.HandlerOptions(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	callEveryService(t, handler)

	methods := connectSeriesMethods(t, provider)
	for _, procedure := range serviceProcedures {
		method := strings.TrimPrefix(procedure, "/")
		if !methods[method] {
			t.Errorf("scrape has no %s series for %s", connectSeriesFamily, method)
		}
	}
}

// callEveryService calls each procedure of serviceProcedures through a handler that admits allowedLogin. Every call
// answers with an error or a result; the test reads only the metrics the calls leave.
func callEveryService(t *testing.T, handler http.Handler) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: headerTransport{base: http.DefaultTransport, login: allowedLogin}}
	ctx := t.Context()

	_, _ = orreryv1connect.NewSessionsClient(httpClient, srv.URL).List(
		ctx, connect.NewRequest(&orreryv1.ListRequest{}))
	_, _ = orreryv1connect.NewControlClient(httpClient, srv.URL).Stop(
		ctx, connect.NewRequest(&orreryv1.StopRequest{}))
	_, _ = orreryv1connect.NewEpicsClient(httpClient, srv.URL).List(
		ctx, connect.NewRequest(&orreryv1.EpicsListRequest{}))
	_, _ = orreryv1connect.NewEvidenceClient(httpClient, srv.URL).Artifact(
		ctx, connect.NewRequest(&orreryv1.EvidenceArtifactRequest{}))
	_, _ = orreryv1connect.NewStepsClient(httpClient, srv.URL).Hold(
		ctx, connect.NewRequest(&orreryv1.StepsHoldRequest{}))
	_, _ = orreryv1connect.NewPoolsClient(httpClient, srv.URL).Status(
		ctx, connect.NewRequest(&orreryv1.PoolsStatusRequest{}))
	_, _ = orreryv1connect.NewPanesClient(httpClient, srv.URL).List(
		ctx, connect.NewRequest(&orreryv1.PanesListRequest{}))
	_, _ = orreryv1connect.NewHostClient(httpClient, srv.URL).Status(
		ctx, connect.NewRequest(&orreryv1.HostStatusRequest{}))
	_, _ = orreryv1connect.NewPushClient(httpClient, srv.URL).Register(
		ctx, connect.NewRequest(&orreryv1.RegisterRequest{}))
	_, _ = orreryv1connect.NewPickerClient(httpClient, srv.URL).Ask(
		ctx, connect.NewRequest(&orreryv1.AskRequest{}))
	_, _ = orreryv1connect.NewPresenceClient(httpClient, srv.URL).Report(
		ctx, connect.NewRequest(&orreryv1.PresenceReportRequest{}))
	_, _ = orreryv1connect.NewNoticesClient(httpClient, srv.URL).Seen(
		ctx, connect.NewRequest(&orreryv1.NoticesSeenRequest{}))

	// Events.Watch streams. Its one receive waits for the stream to end, so its metric is recorded before the scrape.
	stream, err := orreryv1connect.NewEventsClient(httpClient, srv.URL).Watch(
		ctx, connect.NewRequest(&orreryv1.WatchRequest{}))
	if err != nil {
		t.Fatalf("Events.Watch: %v", err)
	}
	if stream.Receive() {
		t.Fatal("Events.Watch sent a message, want the stream to end")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("close Events.Watch stream: %v", err)
	}
}

// connectSeriesMethods scrapes provider and returns the rpc_method of each series of connectSeriesFamily. It fails
// the test when a series carries a label other than rpc_method and rpc_response_status_code.
func connectSeriesMethods(t *testing.T, provider *telemetry.Provider) map[string]bool {
	t.Helper()
	wantLabels := []string{"rpc_method", "rpc_response_status_code"}
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
	family, ok := families[connectSeriesFamily]
	if !ok {
		t.Fatalf("scrape has no %s family", connectSeriesFamily)
	}

	methods := make(map[string]bool, len(family.GetMetric()))
	for _, m := range family.GetMetric() {
		labels := make(map[string]string, len(m.GetLabel()))
		for _, pair := range m.GetLabel() {
			labels[pair.GetName()] = pair.GetValue()
		}
		if names := slices.Sorted(maps.Keys(labels)); !slices.Equal(names, wantLabels) {
			t.Errorf("series labels = %v, want exactly %v", names, wantLabels)
		}
		methods[labels["rpc_method"]] = true
	}
	return methods
}
