// Package alerts takes Alertmanager's webhook and hands each firing, named alert to the notice router.
package alerts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
)

const (
	// webhookVersion is the version of Alertmanager's webhook body that Webhook reads.
	webhookVersion = "4"
	// firing is the status of a firing alert in the webhook, and the state of its notice.
	firing = "firing"
	// nameLabel is the label that names an alert.
	nameLabel = "alertname"
	// summaryAnnotation is the annotation that a notice's body is taken from.
	summaryAnnotation = "summary"
	// maxBody is the largest webhook body that is read. A larger body answers 400.
	maxBody = 1 << 20
)

// Webhook is the handler for Alertmanager's webhook. It answers 503 until Attach gives it a router.
type Webhook struct {
	router atomic.Pointer[route.Router]
}

// body is the part of Alertmanager's version 4 webhook body that Webhook reads.
type body struct {
	Version string  `json:"version"`
	Alerts  []alert `json:"alerts"`
}

// alert is one alert in a webhook body.
type alert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
}

// New returns a Webhook that has no router yet.
func New() *Webhook { return &Webhook{} }

// Attach sets the router that the Webhook's notices go to.
func (h *Webhook) Attach(r *route.Router) { h.router.Store(r) }

// ServeHTTP reads a POST body of Alertmanager's webhook and notifies the router of each firing alert that has a
// name. It answers 405 for any other method, 503 before Attach, 400 for a body that is not the webhook or is over
// 1 MiB, and 200 otherwise.
func (h *Webhook) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	router := h.router.Load()
	if router == nil {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, req.Body, maxBody))
	if err != nil {
		http.Error(w, "read webhook body", http.StatusBadRequest)
		return
	}
	var hook body
	if err = json.Unmarshal(raw, &hook); err != nil || hook.Version != webhookVersion {
		http.Error(w, "not an Alertmanager webhook", http.StatusBadRequest)
		return
	}
	for _, a := range hook.Alerts {
		name := a.Labels[nameLabel]
		if a.Status != firing || name == "" {
			continue
		}
		router.Notify(req.Context(), alertNotice(name, a.Annotations[summaryAnnotation]))
	}
	w.WriteHeader(http.StatusOK)
}

// alertNotice is the notice of the firing alert named name with summary. Its desk notice is titled with the name and
// bodied with the summary, and its push carries the name and the summary and nothing else. It is never watched or
// judged.
func alertNotice(name, summary string) route.Notice {
	return route.Notice{
		Key:   notice.Key{Alert: name},
		State: firing,
		Desk: func(context.Context) (deskstream.Notice, error) {
			return deskstream.Notice{Title: name, Body: summary}, nil
		},
		Push: func(context.Context) (push.Notification, error) {
			return push.Notification{Project: name, State: protocol.State(firing), Summary: summary}, nil
		},
	}
}
