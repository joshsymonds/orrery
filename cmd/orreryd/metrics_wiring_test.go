package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"go.yaml.in/yaml/v3"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
	"github.com/joshsymonds/orrery/internal/telemetry"
)

// Synthetic values only. The metrics address is a loopback port the test picks, and the rules file is the repository's.
const (
	metricsWiringRules = "../../observability/orrery-rules.yml"
	metricsWiringStart = "orrery_start_timestamp_seconds"
	metricsWiringReady = "orrery_ready_timestamp_seconds"
	metricsWiringPush  = "orrery_push_failures_total"
)

// TestMetricsListenRefusesNonLoopback checks that run refuses a --metrics-listen that is not a loopback IP literal
// before the daemon socket exists.
func TestMetricsListenRefusesNonLoopback(t *testing.T) {
	t.Parallel()
	for _, addr := range []string{"0.0.0.0:0", "192.0.2.1:0", "localhost:0"} {
		t.Run(addr, func(t *testing.T) {
			t.Parallel()
			getenv := metricsWiringEnv(t)
			socket := filepath.Join(t.TempDir(), "orreryd.sock")
			var stderr bytes.Buffer
			codes := make(chan int, 1)
			go func() {
				codes <- run([]string{"--socket", socket, "--metrics-listen", addr}, &stderr, getenv)
			}()
			code := <-codes
			if code != exitFailure {
				t.Fatalf("exit code = %d, want %d; stderr %q", code, exitFailure, stderr.String())
			}
			if !strings.Contains(stderr.String(), "loopback") {
				t.Errorf("stderr = %q, want it to name the refused address", stderr.String())
			}
			if _, err := os.Stat(socket); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("daemon socket exists after the refusal: stat error %v", err)
			}
		})
	}
}

// TestMetricsServeBeforeReconcile checks that /metrics answers while reconcile is held, and that ready reads 0 then.
func TestMetricsServeBeforeReconcile(t *testing.T) {
	t.Parallel()
	addr, _, _ := metricsWiringHeld(t, "")
	families := metricsWiringFamilies(t, addr)
	if got := metricsWiringValue(t, families, metricsWiringReady, nil); got != 0 {
		t.Errorf("%s = %v while reconcile is held, want 0", metricsWiringReady, got)
	}
}

// TestMetricsScrapeHoldsEveryRuleSeries checks that a scrape with no events holds every series the rules file reads,
// with the four push classes, and that every orrery_ series is 0 except the start timestamp, which is the start time.
func TestMetricsScrapeHoldsEveryRuleSeries(t *testing.T) {
	t.Parallel()
	before := time.Now()
	addr, _, _, logged := metricsWiringHeldLogged(t, "")
	families := metricsWiringFamilies(t, addr)
	after := time.Now()
	// The slow-call counter counts store calls longer than 100 ms of real time, so it depends on the machine's load. It is
	// exempt from the zero check, and bounded instead by the slow store call records the logger received: the store logs a
	// slow call before it counts it, so a scrape never counts more calls than the logger has seen by the time it returns.
	const slowCalls = "orrery_store_slow_calls_total"

	for _, name := range metricsWiringRuleSeries(t) {
		if _, ok := families[name]; !ok {
			t.Errorf("scrape has no family %s, which the rules read", name)
		}
	}
	for _, class := range []string{"transport", "4xx", "5xx", "other"} {
		if got := metricsWiringValue(t, families, metricsWiringPush, map[string]string{"class": class}); got != 0 {
			t.Errorf("%s{class=%q} = %v with no events, want 0", metricsWiringPush, class, got)
		}
	}
	start := metricsWiringValue(t, families, metricsWiringStart, nil)
	if low, high := metricsWiringUnix(before), metricsWiringUnix(after); start < low || start > high {
		t.Errorf("%s = %v, want the start time between %v and %v", metricsWiringStart, start, low, high)
	}
	for name, family := range families {
		if !strings.HasPrefix(name, "orrery_") || name == metricsWiringStart || name == slowCalls {
			continue
		}
		for _, m := range family.GetMetric() {
			if got := metricsWiringSeriesValue(family, m); got != 0 {
				t.Errorf("%s = %v with no events, want 0", name, got)
			}
		}
	}
	slowCount := metricsWiringValue(t, families, slowCalls, nil)
	records := logged.slowCallRecords()
	if slowCount > float64(records) {
		t.Errorf("%s = %v with no events, want at most the %d slow store call records the logger received",
			slowCalls, slowCount, records)
	}
}

// TestMetricsReadySetOnceReady checks that ready is recorded once serve sends READY=1 to its notify socket, and that
// no READY=1 is sent before reconcile returns.
func TestMetricsReadySetOnceReady(t *testing.T) {
	t.Parallel()
	notify := filepath.Join(t.TempDir(), "notify.sock")
	ln, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: notify, Net: "unixgram"})
	if err != nil {
		t.Fatalf("listen notify socket: %v", err)
	}
	t.Cleanup(func() {
		if err := ln.Close(); err != nil {
			t.Errorf("close notify socket: %v", err)
		}
	})
	addr, _, release := metricsWiringHeld(t, notify)
	if err := ln.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set notify deadline: %v", err)
	}
	if _, _, err := ln.ReadFromUnix(make([]byte, 64)); err == nil {
		t.Fatal("READY=1 was sent while reconcile was held")
	}

	close(release)
	// The read waits for READY=1 with no deadline of its own, so it clears the absence window's deadline.
	if err := ln.SetReadDeadline(time.Time{}); err != nil {
		t.Fatalf("clear notify deadline: %v", err)
	}
	buf := make([]byte, 64)
	n, _, err := ln.ReadFromUnix(buf)
	if err != nil {
		t.Fatalf("read READY=1: %v", err)
	}
	if got := string(buf[:n]); got != "READY=1" {
		t.Fatalf("notify datagram = %q, want READY=1", got)
	}

	families := metricsWiringFamilies(t, addr)
	ready := metricsWiringValue(t, families, metricsWiringReady, nil)
	if ready == 0 {
		t.Errorf("%s = 0 after READY=1, want the ready time", metricsWiringReady)
	}
	if start := metricsWiringValue(t, families, metricsWiringStart, nil); ready < start {
		t.Errorf("%s = %v is before %s = %v", metricsWiringReady, ready, metricsWiringStart, start)
	}
}

// TestSessionsHandoffsMetricsToWriter checks that startSessions gives the telemetry Daemon to the writer: a Commit waits
// for its flush, and that wait lands on the Daemon's writer wait series.
func TestSessionsHandoffsMetricsToWriter(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	getenv, path := evidenceWiringEnv(t)
	st := openPaneStore(t, path)
	provider, daemon := metricsWiringDaemon(t)
	opts := evidenceWiringOptions(t)
	opts.metrics = daemon
	host, err := startSessions(
		ctx, opts, evidenceWiringHost, st, &recordingRouter{}, nil,
		slog.New(slog.DiscardHandler), getenv,
	)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}
	// The op names a step no row holds, so the writer drops it. The wait for its flush is recorded either way.
	if err := host.writer.Commit(ctx, evidenceWiringStep, store.StepEnd{
		ID: evidenceWiringStep, End: evidenceWiringJudgeEnd, Status: "passed",
	}); err != nil {
		t.Logf("commit of a dropped op: %v", err)
	}
	if got := metricsWiringScrapeValue(t, provider, "orrery_writer_wait_seconds_total"); got <= 0 {
		t.Errorf("orrery_writer_wait_seconds_total = %v after a Commit, want more than 0: "+
			"the writer has no Daemon", got)
	}
	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// TestSessionsWithoutMetricsRecordsNothing checks that without --metrics-listen startMetrics sets no Daemon and no
// handler options.
func TestSessionsWithoutMetricsRecordsNothing(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	var bare options
	if err := startMetrics(ctx, &bare, time.Now(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("startMetrics without --metrics-listen: %v", err)
	}
	if bare.metrics != nil || bare.metricsHandlers != nil {
		t.Errorf("startMetrics without --metrics-listen set metrics %p and %d handler options, want none",
			bare.metrics, len(bare.metricsHandlers))
	}
}

// TestSessionsHandlerOptionsRecordRPCs checks that the session socket's services take the handler options, so an RPC
// on the session socket is recorded on /metrics.
func TestSessionsHandlerOptionsRecordRPCs(t *testing.T) {
	t.Parallel()
	notify := filepath.Join(t.TempDir(), "notify.sock")
	ln, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: notify, Net: "unixgram"})
	if err != nil {
		t.Fatalf("listen notify socket: %v", err)
	}
	t.Cleanup(func() {
		if err := ln.Close(); err != nil {
			t.Errorf("close notify socket: %v", err)
		}
	})
	addr, socket, release := metricsWiringHeld(t, notify)
	close(release)
	if _, _, err := ln.ReadFromUnix(make([]byte, 64)); err != nil {
		t.Fatalf("read READY=1: %v", err)
	}

	client := evidenceWiringClient(t, protocol.SessionSocketPath(socket))
	if _, err := client.ListEvidence(
		t.Context(), connect.NewRequest(&v1.ListEvidenceRequest{Epic: evidenceWiringAbsent}),
	); err != nil {
		t.Fatalf("ListEvidence: %v", err)
	}

	families := metricsWiringFamilies(t, addr)
	family, ok := families["rpc_server_call_duration_seconds"]
	if !ok {
		t.Fatal("scrape has no rpc_server_call_duration_seconds family: " +
			"the session services ignore the handler options")
	}
	var calls uint64
	for _, m := range family.GetMetric() {
		calls += m.GetHistogram().GetSampleCount()
	}
	if calls < 1 {
		t.Errorf("rpc_server_call_duration_seconds sample count = %d after an RPC, want at least 1", calls)
	}
}

// metricsWiringDaemon returns a fresh telemetry provider and a Daemon on it.
func metricsWiringDaemon(t *testing.T) (*telemetry.Provider, *telemetry.Daemon) {
	t.Helper()
	provider, err := telemetry.New()
	if err != nil {
		t.Fatalf("telemetry provider: %v", err)
	}
	daemon, err := telemetry.NewDaemon(t.Context(), provider, time.Now())
	if err != nil {
		t.Fatalf("telemetry daemon: %v", err)
	}
	return provider, daemon
}

// metricsWiringScrapeValue scrapes provider and returns the value of the one series of name, which has no labels.
func metricsWiringScrapeValue(t *testing.T, provider *telemetry.Provider, name string) float64 {
	t.Helper()
	rec := httptest.NewRecorder()
	provider.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(rec.Body)
	if err != nil {
		t.Fatalf("parse exposition: %v", err)
	}
	return metricsWiringValue(t, families, name, nil)
}

// metricsWiringHeld runs a --sessions orreryd whose --metrics-listen is 127.0.0.1:0, and whose reconcile is held until
// release is closed or the test ends. It returns the address the metrics listener bound, read from orreryd's log, once
// reconcile has been reached, so the metrics listener is up and READY has not been reached. notify is the notify socket
// serve sends READY=1 to, or "" for none.
func metricsWiringHeld(t *testing.T, notify string) (string, string, chan struct{}) {
	t.Helper()
	addr, socket, release, _ := metricsWiringHeldLogged(t, notify)
	return addr, socket, release
}

// metricsWiringHeldLogged is metricsWiringHeld, and also returns the handler that keeps the records orreryd logs.
func metricsWiringHeldLogged(t *testing.T, notify string) (string, string, chan struct{}, *boundAddrHandler) {
	t.Helper()
	getenv := metricsWiringEnv(t)
	storePath, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store path: %v", err)
	}
	storetest.Seed(t, storePath)
	socket := filepath.Join(t.TempDir(), "orreryd.sock")
	opts, err := parseFlags([]string{
		"--sessions", "--session-shell", "/bin/sh",
		"--socket", socket,
		"--metrics-listen", "127.0.0.1:0",
	}, getenv)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	opts.notifySocket = notify
	reached := make(chan struct{})
	release := make(chan struct{})
	opts.holdReconcile = func(ctx context.Context) {
		close(reached)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	bound := &boundAddrHandler{}
	addr := metricsWiringServe(t, opts, getenv, bound)
	<-reached
	return addr, socket, release, bound
}

// metricsWiringServe starts the metrics listener and then serve in the background, as run does, and returns the
// address the metrics listener bound, from startMetrics's log line. bound receives every record serve logs. When the
// test ends it cancels serve and checks that serve returned no error.
func metricsWiringServe(t *testing.T, opts options, getenv func(string) string, bound *boundAddrHandler) string {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	logger := slog.New(bound)
	if err := startMetrics(ctx, &opts, time.Now(), logger); err != nil {
		cancel()
		t.Fatalf("startMetrics: %v", err)
	}
	addr := bound.addr()
	if addr == "" {
		cancel()
		t.Fatalf("startMetrics logged no %q line with the bound address", metricsListeningMessage)
	}
	errc := make(chan error, 1)
	go func() { errc <- serve(ctx, opts, logger, getenv) }()
	t.Cleanup(func() {
		cancel()
		err := <-errc
		if err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	return addr
}

// boundAddrHandler is a slog handler that keeps the addr of startMetrics's metricsListeningMessage line, counts the
// "slow store call" records the store logs, and drops every other record.
type boundAddrHandler struct {
	mu    sync.Mutex
	bound string
	slow  int
}

// Enabled takes every level, so the info line reaches Handle.
func (h *boundAddrHandler) Enabled(context.Context, slog.Level) bool { return true }

// Handle keeps the addr attribute of a metricsListeningMessage record, and counts a slow store call record.
func (h *boundAddrHandler) Handle(_ context.Context, record slog.Record) error {
	if record.Message == "slow store call" {
		h.mu.Lock()
		h.slow++
		h.mu.Unlock()
		return nil
	}
	if record.Message != metricsListeningMessage {
		return nil
	}
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "addr" {
			h.mu.Lock()
			h.bound = attr.Value.String()
			h.mu.Unlock()
		}
		return true
	})
	return nil
}

// WithAttrs returns h: the line it keeps carries its address as its own attribute.
func (h *boundAddrHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

// WithGroup returns h, as WithAttrs does.
func (h *boundAddrHandler) WithGroup(string) slog.Handler { return h }

// addr returns the address kept, or "" when no metricsListeningMessage line came.
func (h *boundAddrHandler) addr() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.bound
}

// slowCallRecords returns how many "slow store call" records the handler has received.
func (h *boundAddrHandler) slowCallRecords() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.slow
}

// metricsWiringEnv returns a getenv over fresh state, config and tmux directories, so the store, the push store, the
// roles and the tmux socket stay inside the test's temporary directories.
func metricsWiringEnv(t *testing.T) func(string) string {
	t.Helper()
	env := map[string]string{
		"XDG_STATE_HOME":  t.TempDir(),
		"XDG_CONFIG_HOME": t.TempDir(),
		"TMUX_TMPDIR":     t.TempDir(),
	}
	return func(key string) string { return env[key] }
}

// metricsWiringRuleSeries returns the metric names the rules file's exprs read. A metric name is an identifier with an
// underscore: the PromQL functions and keywords, and the job and instance labels, the exprs use have none.
func metricsWiringRuleSeries(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(metricsWiringRules)
	if err != nil {
		t.Fatalf("read rules: %v", err)
	}
	var rules struct {
		Groups []struct {
			Rules []struct {
				Expr string `yaml:"expr"`
			} `yaml:"rules"`
		} `yaml:"groups"`
	}
	if err := yaml.Unmarshal(raw, &rules); err != nil {
		t.Fatalf("parse rules: %v", err)
	}
	ident := regexp.MustCompile(`[a-zA-Z_:][a-zA-Z0-9_:]*`)
	seen := make(map[string]bool)
	var names []string
	for _, group := range rules.Groups {
		for _, rule := range group.Rules {
			for _, name := range ident.FindAllString(rule.Expr, -1) {
				if strings.Contains(name, "_") && !seen[name] {
					seen[name] = true
					names = append(names, name)
				}
			}
		}
	}
	if len(names) == 0 {
		t.Fatal("the rules read no metric names")
	}
	return names
}

// metricsWiringFamilies scrapes GET /metrics at addr and parses the exposition. It fails the test on any status but
// 200.
func metricsWiringFamilies(t *testing.T, addr string) map[string]*dto.MetricFamily {
	t.Helper()
	url := "http://" + addr + "/metrics"
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("request %s: %v", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close body of %s: %v", url, err)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d", url, resp.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("parse exposition: %v", err)
	}
	return families
}

// metricsWiringValue returns the value of the series of name whose labels are exactly want.
func metricsWiringValue(
	t *testing.T,
	families map[string]*dto.MetricFamily,
	name string,
	want map[string]string,
) float64 {
	t.Helper()
	family, ok := families[name]
	if !ok {
		t.Fatalf("scrape has no family %s", name)
	}
	for _, m := range family.GetMetric() {
		if sameLabelSet(labelPairs(m.GetLabel()), want) {
			return metricsWiringSeriesValue(family, m)
		}
	}
	t.Fatalf("scrape has no series %s with labels %v", name, want)
	return 0
}

// metricsWiringSeriesValue returns the value of a counter or gauge series.
func metricsWiringSeriesValue(family *dto.MetricFamily, m *dto.Metric) float64 {
	if family.GetType() == dto.MetricType_COUNTER {
		return m.GetCounter().GetValue()
	}
	return m.GetGauge().GetValue()
}

// metricsWiringUnix is t as fractional unix seconds, the unit of the timestamp gauges.
func metricsWiringUnix(t time.Time) float64 {
	return float64(t.Unix()) + float64(t.Nanosecond())/1e9
}

// labelPairs returns the label pairs as a map from name to value.
func labelPairs(pairs []*dto.LabelPair) map[string]string {
	labels := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		labels[pair.GetName()] = pair.GetValue()
	}
	return labels
}

// sameLabelSet reports whether got and want hold the same names with the same values.
func sameLabelSet(got, want map[string]string) bool {
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
