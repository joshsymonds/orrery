package push

import (
	"context"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// pubLimitKey is a synthetic usage-limit key in the form the engine records it.
const pubLimitKey = "seven_day@1900000000"

func TestPushPublishCarriesLimitKey(t *testing.T) {
	t.Parallel()
	ep := newPubEndpoint(t, http.StatusOK)
	p := newPubPublisher(t, ep.srv.Client(), ep)

	n := pubNotification("synthetic question?")
	n.Pane = ""
	n.Limit = pubLimitKey
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
		"limit":         pubLimitKey,
	}
	if got := decryptPush(t, onePush(t, ep).body, ep.recipient); !maps.Equal(got, want) {
		t.Errorf("payload = %v, want %v", got, want)
	}
}

func TestPushPublishOmitsLimitKeyWithoutLimit(t *testing.T) {
	t.Parallel()
	ep := newPubEndpoint(t, http.StatusOK)
	p := newPubPublisher(t, ep.srv.Client(), ep)

	n := pubNotification("synthetic question?")
	if err := p.Publish(context.Background(), n); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	want := map[string]string{
		"pane":          n.Pane,
		"host":          n.Host,
		"project":       n.Project,
		"state":         string(n.State),
		"summary":       n.Summary,
		"label":         n.Label,
		"question":      n.Question,
		"question_hash": protocol.QuestionHash(n.Question),
		"time":          strconv.FormatInt(n.Time, 10),
	}
	if got := decryptPush(t, onePush(t, ep).body, ep.recipient); !maps.Equal(got, want) {
		t.Errorf("payload = %v, want the pane keys %v", got, want)
	}
}

func TestPushPublishKeepsLimitWhenSummaryShortened(t *testing.T) {
	t.Parallel()
	ep := newPubEndpoint(t, http.StatusOK)
	p := newPubPublisher(t, ep.srv.Client(), ep)

	n := pubNotification("synthetic question?")
	n.Pane = ""
	n.Limit = pubLimitKey
	n.Summary = pubEscapedQuestion(3000)
	if err := p.Publish(context.Background(), n); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	req := onePush(t, ep)
	assertPushHeaders(t, req)
	if len(req.body) > pubRecordSize {
		t.Errorf("body is %d bytes, want at most %d", len(req.body), pubRecordSize)
	}
	payload := decryptPush(t, req.body, ep.recipient)
	if payload["limit"] != pubLimitKey {
		t.Errorf("limit = %q, want it kept whole as %q", payload["limit"], pubLimitKey)
	}
	if payload["question"] != "" {
		t.Errorf("question = %q, want it emptied before the summary is shortened", payload["question"])
	}
	got := payload["summary"]
	if !utf8.ValidString(got) || !strings.HasPrefix(n.Summary, got) || len(got) >= len(n.Summary) {
		t.Errorf("summary is %d bytes, want a valid truncated prefix of the %d-byte original", len(got), len(n.Summary))
	}
}

func TestPushPublishClearCarriesLimitKey(t *testing.T) {
	t.Parallel()
	ep := newPubEndpoint(t, http.StatusOK)
	p := newPubPublisher(t, ep.srv.Client(), ep)

	c := Clear{Host: "synthetic-host", Limit: pubLimitKey, Seen: 1700000500}
	if err := p.PublishClear(context.Background(), c); err != nil {
		t.Fatalf("PublishClear: %v", err)
	}

	req := onePush(t, ep)
	assertPushHeaders(t, req)
	want := map[string]string{
		"kind":  "clear",
		"host":  c.Host,
		"limit": c.Limit,
		"seen":  strconv.FormatInt(c.Seen, 10),
	}
	if got := decryptPush(t, req.body, ep.recipient); !maps.Equal(got, want) {
		t.Errorf("payload = %v, want exactly %v", got, want)
	}
}

func TestPushPublishClearOmitsLimitKeyWithoutLimit(t *testing.T) {
	t.Parallel()
	ep := newPubEndpoint(t, http.StatusOK)
	p := newPubPublisher(t, ep.srv.Client(), ep)

	c := Clear{Host: "synthetic-host", Pane: "synthetic-pane", Seen: 1700000500}
	if err := p.PublishClear(context.Background(), c); err != nil {
		t.Fatalf("PublishClear: %v", err)
	}

	req := onePush(t, ep)
	assertPushHeaders(t, req)
	want := map[string]string{
		"kind": "clear",
		"host": c.Host,
		"pane": c.Pane,
		"seen": strconv.FormatInt(c.Seen, 10),
	}
	if got := decryptPush(t, req.body, ep.recipient); !maps.Equal(got, want) {
		t.Errorf("payload = %v, want exactly %v", got, want)
	}
}
