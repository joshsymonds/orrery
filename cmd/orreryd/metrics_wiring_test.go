package main

import (
	"bytes"
	"context"
	"crypto/rand"
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
	metricsWiringWait  = 10 * time.Second
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
			var code int
			select {
			case code = <-codes:
			case <-time.After(metricsWiringWait):
				t.Fatal("run did not return for a non-loopback --metrics-listen")
			}
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
	addr, _, _ := metricsWiringHeld(t, "")
	families := metricsWiringFamilies(t, addr)
	after := time.Now()

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
		if !strings.HasPrefix(name, "orrery_") || name == metricsWiringStart {
			continue
		}
		for _, m := range family.GetMetric() {
			if got := metricsWiringSeriesValue(family, m); got != 0 {
				t.Errorf("%s = %v with no events, want 0", name, got)
			}
		}
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
	if err := ln.SetReadDeadline(time.Now().Add(metricsWiringWait)); err != nil {
		t.Fatalf("set notify deadline: %v", err)
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
	if err := ln.SetReadDeadline(time.Now().Add(metricsWiringWait)); err != nil {
		t.Fatalf("set notify deadline: %v", err)
	}
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

// metricsWiringHeld runs a --sessions orreryd whose --metrics-listen is a fresh loopback port, and whose reconcile is
// held until release is closed or the test ends. It returns once reconcile has been reached, so the metrics listener is
// up and READY has not been reached. notify is the notify socket serve sends READY=1 to, or "" for none.
func metricsWiringHeld(t *testing.T, notify string) (string, string, chan struct{}) {
	t.Helper()
	getenv := metricsWiringEnv(t)
	storePath, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store path: %v", err)
	}
	storetest.Seed(t, storePath)
	addr := metricsWiringAddr(t)
	socket := filepath.Join(t.TempDir(), "orreryd.sock")
	opts, err := parseFlags([]string{
		"--sessions", "--session-shell", "/bin/sh",
		"--socket", socket,
		"--metrics-listen", addr,
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
	metricsWiringServe(t, opts, getenv)
	select {
	case <-reached:
	case <-time.After(metricsWiringWait):
		t.Fatal("reconcile was not reached")
	}
	return addr, socket, release
}

// metricsWiringServe starts the metrics listener and then serve in the background, as run does. When the test ends it
// cancels serve and checks that serve returned no error.
func metricsWiringServe(t *testing.T, opts options, getenv func(string) string) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	logger := slog.New(slog.DiscardHandler)
	if err := startMetrics(ctx, &opts, time.Now(), logger); err != nil {
		cancel()
		t.Fatalf("startMetrics: %v", err)
	}
	errc := make(chan error, 1)
	go func() { errc <- serve(ctx, opts, logger, getenv) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errc:
			if err != nil {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(metricsWiringWait):
			t.Error("serve did not return after its context ended")
		}
	})
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

// metricsWiringAddr returns a loopback address with a port nothing holds yet, on an IP of 127.0.0.0/8 that only this
// call picks. The port is released before the metrics listener binds it, and on 127.0.0.1 a listener of a parallel test,
// in any package, could take it in that time. Nothing else listens on the picked IP.
func metricsWiringAddr(t *testing.T) string {
	t.Helper()
	var octets [3]byte
	if _, err := rand.Read(octets[:]); err != nil {
		t.Fatalf("pick a loopback IP: %v", err)
	}
	ip := net.IPv4(127, 1+octets[0]%254, 1+octets[1]%254, 1+octets[2]%254)
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", net.JoinHostPort(ip.String(), "0"))
	if err != nil {
		t.Fatalf("pick a loopback port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("release the loopback port: %v", err)
	}
	return addr
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
	client := &http.Client{Timeout: metricsWiringWait}
	resp, err := client.Do(req)
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
