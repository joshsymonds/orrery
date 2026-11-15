package push

import (
	"context"
	"maps"
	"net/http"
	"strconv"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// pubAlertName is a synthetic alert name in the form the engine records it.
const pubAlertName = "synthetic-alert"

func TestPushPublishCarriesAlertKey(t *testing.T) {
	t.Parallel()
	ep := newPubEndpoint(t, http.StatusOK)
	p := newPubPublisher(t, ep.srv.Client(), ep)

	n := pubNotification("synthetic question?")
	n.Pane = ""
	n.Alert = pubAlertName
	if err := p.Publish(context.Background(), n); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	want := map[string]string{
		"pane":          "",
		"host":          n.Host,
		"project":       n.Project,
		"state":         string(n.State),
		"summary":       n.Summary,
		"label":         n.Label,
		"question":      n.Question,
		"question_hash": protocol.QuestionHash(n.Question),
		"time":          strconv.FormatInt(n.Time, 10),
		"alert":         pubAlertName,
	}
	if got := decryptPush(t, onePush(t, ep).body, ep.recipient); !maps.Equal(got, want) {
		t.Errorf("payload = %v, want %v", got, want)
	}
}

func TestPushPublishClearCarriesAlertKey(t *testing.T) {
	t.Parallel()
	ep := newPubEndpoint(t, http.StatusOK)
	p := newPubPublisher(t, ep.srv.Client(), ep)

	c := Clear{Host: "synthetic-host", Alert: pubAlertName, Seen: 1700000500}
	if err := p.PublishClear(context.Background(), c); err != nil {
		t.Fatalf("PublishClear: %v", err)
	}

	req := onePush(t, ep)
	assertPushHeaders(t, req)
	want := map[string]string{
		"kind":  "clear",
		"host":  c.Host,
		"alert": c.Alert,
		"seen":  strconv.FormatInt(c.Seen, 10),
	}
	if got := decryptPush(t, req.body, ep.recipient); !maps.Equal(got, want) {
		t.Errorf("payload = %v, want exactly %v", got, want)
	}
}
