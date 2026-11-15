package main

import (
	"encoding/json"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/deskstream"
)

// Synthetic values only. The alert, its summary and the webhook's other fields are invented.
const (
	alertsWiringName    = "synthetic-alert-a"
	alertsWiringSummary = "synthetic summary a"
	alertsWiringBody    = `{
  "version": "4",
  "status": "firing",
  "receiver": "synthetic-receiver",
  "groupLabels": {"alertname": "synthetic-alert-a"},
  "commonAnnotations": {"runbook": "https://synthetic.example/runbook"},
  "externalURL": "https://synthetic.example/alertmanager",
  "alerts": [
    {
      "status": "firing",
      "labels": {"alertname": "synthetic-alert-a", "severity": "synthetic-severity", "instance": "synthetic-instance"},
      "annotations": {"summary": "synthetic summary a", "description": "synthetic description a", "runbook": "https://synthetic.example/runbook"},
      "generatorURL": "https://synthetic.example/prometheus/graph",
      "fingerprint": "synthetic-fingerprint-a"
    }
  ]
}`
)

// TestAlertsPostReachesDeskAsOneNotice checks that a webhook with one firing alert POSTed to the --metrics-listen of a
// --sessions orreryd reaches the desk stream as one notice line carrying the alert name and summary and nothing else
// from the webhook, and that GET /metrics still answers on the same listener.
func TestAlertsPostReachesDeskAsOneNotice(t *testing.T) {
	t.Parallel()
	addr, socket, release := metricsWiringHeld(t, "")
	close(release)

	conn, err := net.Dial("unix", deskstream.SocketPath(socket))
	if err != nil {
		t.Fatalf("dial desk stream: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	decoder := json.NewDecoder(conn)
	var hello deskstream.Line
	if err := decoder.Decode(&hello); err != nil {
		t.Fatalf("read desk hello: %v", err)
	}
	if hello.Kind != deskstream.KindHello {
		t.Fatalf("first desk line kind = %q, want %q", hello.Kind, deskstream.KindHello)
	}

	client := &http.Client{}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		"http://"+addr+"/alerts", strings.NewReader(alertsWiringBody))
	if err != nil {
		t.Fatalf("request /alerts: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /alerts: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Errorf("close POST /alerts body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /alerts status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var line map[string]any
	if err := decoder.Decode(&line); err != nil {
		t.Fatalf("read desk notice: %v", err)
	}
	want := map[string]any{
		"kind":  deskstream.KindNotice,
		"host":  hello.Host,
		"title": alertsWiringName,
		"body":  alertsWiringSummary,
	}
	if !reflect.DeepEqual(line, want) {
		t.Errorf("desk notice line = %v, want exactly %v", line, want)
	}

	families := metricsWiringFamilies(t, addr)
	if got := metricsWiringValue(t, families, metricsWiringStart, nil); got == 0 {
		t.Errorf("%s = 0 on the listener that took the alert, want the start time", metricsWiringStart)
	}
}
