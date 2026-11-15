package alerts_test

import (
	"cmp"
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/alerts"
	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
)

const (
	testHost    = "synthetic-host"
	clockStart  = int64(1_700_000_000)
	firingState = "firing"
	maxBody     = 1 << 20
)

// firingBody is a webhook body with one firing alert, synthetic-alert-a.
const firingBody = `{
  "version": "4",
  "status": "firing",
  "alerts": [
    {"status": "firing", "labels": {"alertname": "synthetic-alert-a"}, "annotations": {"summary": "synthetic summary a"}}
  ]
}`

// fake is the desk and the phone the Router routes to. It records each notice it receives.
type fake struct {
	mu     sync.Mutex
	posts  []deskstream.Notice
	pushes []pushed
}

// pushed is one push the phone was asked to send, and the key it was sent for.
type pushed struct {
	key  notice.Key
	note push.Notification
}

// discardLog is the notice log. These tests do not read it.
type discardLog struct{}

func (discardLog) Write(route.Line) error { return nil }

// Desk reports no web input, so the desk is away and each notice goes to the phone as well as the desk.
func (f *fake) Desk() int64 { return 0 }

func (f *fake) Post(n deskstream.Notice) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = append(f.posts, n)
}

func (f *fake) Notify(
	ctx context.Context,
	key notice.Key,
	build func(context.Context) (push.Notification, error),
	_ func(),
) {
	note, _ := build(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushes = append(f.pushes, pushed{key: key, note: note})
}

func (f *fake) Seen(context.Context, notice.Key, int64) {}

// check waits for the Router to finish routing, then asserts the desk posts and pushes the fake received. Routing runs
// each notice on its own goroutine, so both are compared sorted by name.
func (f *fake) check(t *testing.T, r *route.Router, wantPosts []deskstream.Notice, wantPushes []pushed) {
	t.Helper()
	r.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	slices.SortFunc(f.posts, func(a, b deskstream.Notice) int { return cmp.Compare(a.Title, b.Title) })
	slices.SortFunc(f.pushes, func(a, b pushed) int { return cmp.Compare(a.key.Alert, b.key.Alert) })
	if !slices.Equal(f.posts, wantPosts) {
		t.Errorf("desk posts = %+v, want %+v", f.posts, wantPosts)
	}
	if !slices.Equal(f.pushes, wantPushes) {
		t.Errorf("pushes = %+v, want %+v", f.pushes, wantPushes)
	}
}

// deskNotice is the desk notice of the alert named name with summary.
func deskNotice(name, summary string) deskstream.Notice {
	return deskstream.Notice{Title: name, Body: summary}
}

// pushNotice is the push of the alert named name with summary, sent for its key.
func pushNotice(name, summary string) pushed {
	return pushed{
		key:  notice.Key{Alert: name},
		note: push.Notification{Project: name, State: firingState, Summary: summary},
	}
}

// newRouter returns a Router whose only sinks are f. It is closed when the test ends.
func newRouter(t *testing.T, f *fake) *route.Router {
	t.Helper()
	r := route.New(route.Config{
		Host:       testHost,
		Now:        func() time.Time { return time.Unix(clockStart, 0) },
		Desk:       f,
		Phone:      f,
		Subscribed: func() (bool, error) { return true, nil },
		Log:        discardLog{},
	})
	t.Cleanup(r.Close)
	return r
}

// attached returns a Webhook attached to r.
func attached(r *route.Router) *alerts.Webhook {
	h := alerts.New()
	h.Attach(r)
	return h
}

// post sends body to h as a POST to /alerts and returns the status h answers.
func post(h *alerts.Webhook, body string) int {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/alerts", strings.NewReader(body)))
	return rec.Code
}

func TestWebhookNoticesOnlyNamedFiringAlerts(t *testing.T) {
	t.Parallel()

	f := &fake{}
	r := newRouter(t, f)
	const body = `{
  "version": "4",
  "status": "firing",
  "alerts": [
    {"status": "firing", "labels": {"alertname": "synthetic-alert-a"}, "annotations": {"summary": "synthetic summary a"}},
    {"status": "firing", "labels": {"alertname": "synthetic-alert-b"}, "annotations": {"summary": "synthetic summary b"}},
    {"status": "resolved", "labels": {"alertname": "synthetic-alert-c"}, "annotations": {"summary": "synthetic summary c"}},
    {"status": "firing", "labels": {}, "annotations": {"summary": "synthetic summary d"}},
    {"status": "firing", "labels": {"alertname": ""}, "annotations": {"summary": "synthetic summary e"}}
  ]
}`
	if got := post(attached(r), body); got != http.StatusOK {
		t.Fatalf("status = %d, want %d", got, http.StatusOK)
	}
	f.check(t, r, []deskstream.Notice{
		deskNotice("synthetic-alert-a", "synthetic summary a"),
		deskNotice("synthetic-alert-b", "synthetic summary b"),
	}, []pushed{
		pushNotice("synthetic-alert-a", "synthetic summary a"),
		pushNotice("synthetic-alert-b", "synthetic summary b"),
	})
}

func TestWebhookNoticeCarriesOnlyNameAndSummary(t *testing.T) {
	t.Parallel()

	f := &fake{}
	r := newRouter(t, f)
	const body = `{
  "version": "4",
  "groupKey": "synthetic-group",
  "truncatedAlerts": 0,
  "status": "firing",
  "receiver": "synthetic-receiver",
  "groupLabels": {"alertname": "synthetic-alert-a"},
  "commonLabels": {"severity": "synthetic-severity"},
  "commonAnnotations": {"runbook": "https://synthetic.example/runbook"},
  "externalURL": "https://synthetic.example/alertmanager",
  "alerts": [
    {
      "status": "firing",
      "labels": {"alertname": "synthetic-alert-a", "severity": "synthetic-severity", "instance": "synthetic-instance"},
      "annotations": {"summary": "synthetic summary a", "description": "synthetic description a", "runbook": "https://synthetic.example/runbook"},
      "startsAt": "2026-11-15T00:00:00Z",
      "endsAt": "0001-01-01T00:00:00Z",
      "generatorURL": "https://synthetic.example/prometheus/graph",
      "fingerprint": "synthetic-fingerprint-a"
    },
    {
      "status": "firing",
      "labels": {"alertname": "synthetic-alert-b", "severity": "synthetic-severity"},
      "annotations": {"summary": "synthetic summary b", "description": "synthetic description b"},
      "startsAt": "2026-11-15T00:00:00Z",
      "endsAt": "0001-01-01T00:00:00Z",
      "generatorURL": "https://synthetic.example/prometheus/graph",
      "fingerprint": "synthetic-fingerprint-b"
    }
  ]
}`
	if got := post(attached(r), body); got != http.StatusOK {
		t.Fatalf("status = %d, want %d", got, http.StatusOK)
	}
	f.check(t, r, []deskstream.Notice{
		deskNotice("synthetic-alert-a", "synthetic summary a"),
		deskNotice("synthetic-alert-b", "synthetic summary b"),
	}, []pushed{
		pushNotice("synthetic-alert-a", "synthetic summary a"),
		pushNotice("synthetic-alert-b", "synthetic summary b"),
	})
}

func TestWebhookAnswers503UntilAttached(t *testing.T) {
	t.Parallel()

	f := &fake{}
	r := newRouter(t, f)
	h := alerts.New()
	if got := post(h, firingBody); got != http.StatusServiceUnavailable {
		t.Fatalf("status before Attach = %d, want %d", got, http.StatusServiceUnavailable)
	}
	f.check(t, r, nil, nil)

	h.Attach(r)
	if got := post(h, firingBody); got != http.StatusOK {
		t.Fatalf("status after Attach = %d, want %d", got, http.StatusOK)
	}
	f.check(t, r,
		[]deskstream.Notice{deskNotice("synthetic-alert-a", "synthetic summary a")},
		[]pushed{pushNotice("synthetic-alert-a", "synthetic summary a")},
	)
}

func TestWebhookRejectsBodiesThatAreNotTheWebhookOrTooLarge(t *testing.T) {
	t.Parallel()

	oversized := `{"version": "4", "status": "firing", "alerts": [
    {"status": "firing", "labels": {"alertname": "synthetic-alert-a"}, "annotations": {"summary": "synthetic summary a"}}
  ], "pad": "` + strings.Repeat("x", maxBody) + `"}`
	cases := []struct {
		name string
		body string
	}{
		{name: "truncated JSON", body: `{"version": "4", "alerts": [`},
		{name: "JSON array", body: `[]`},
		{name: "version 3", body: strings.Replace(firingBody, `"version": "4"`, `"version": "3"`, 1)},
		{name: "label not a string", body: `{"version": "4", "alerts": [
  {"status": "firing", "labels": {"alertname": 7}}
]}`},
		{name: "over one MiB", body: oversized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := &fake{}
			r := newRouter(t, f)
			if got := post(attached(r), tc.body); got != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", got, http.StatusBadRequest)
			}
			f.check(t, r, nil, nil)
		})
	}
}

func TestWebhookAcceptsBodyOfExactlyOneMiB(t *testing.T) {
	t.Parallel()

	const prefix = `{"version": "4", "status": "firing", "alerts": [` +
		`{"status": "firing", "labels": {"alertname": "synthetic-alert-a"}, "annotations": {"summary": "synthetic summary a"}}` +
		`], "pad": "`
	const suffix = `"}`
	body := prefix + strings.Repeat("x", maxBody-len(prefix)-len(suffix)) + suffix
	if len(body) != maxBody {
		t.Fatalf("fixture length = %d, want %d", len(body), maxBody)
	}

	f := &fake{}
	r := newRouter(t, f)
	if got := post(attached(r), body); got != http.StatusOK {
		t.Fatalf("status = %d, want %d", got, http.StatusOK)
	}
	f.check(t, r,
		[]deskstream.Notice{deskNotice("synthetic-alert-a", "synthetic summary a")},
		[]pushed{pushNotice("synthetic-alert-a", "synthetic summary a")},
	)
}

func TestWebhookAcceptsOnlyPOST(t *testing.T) {
	t.Parallel()

	f := &fake{}
	r := newRouter(t, f)
	rec := httptest.NewRecorder()
	attached(r).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/alerts", strings.NewReader(firingBody)))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	f.check(t, r, nil, nil)
}
