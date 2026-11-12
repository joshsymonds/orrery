package server_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

// phoneProcedures lists the Connect procedures of the phone services: Panes (including the WatchConversation stream),
// Host, Push and Picker.
func phoneProcedures() []string {
	return []string{
		orreryv1connect.PanesListProcedure,
		orreryv1connect.PanesScreenProcedure,
		orreryv1connect.PanesConversationProcedure,
		orreryv1connect.PanesSendTextProcedure,
		orreryv1connect.PanesSendKeysProcedure,
		orreryv1connect.PanesMarkSeenProcedure,
		orreryv1connect.PanesWatchConversationProcedure,
		orreryv1connect.HostStatusProcedure,
		orreryv1connect.PushRegisterProcedure,
		orreryv1connect.PushUnregisterProcedure,
		orreryv1connect.PickerAskProcedure,
		orreryv1connect.PickerRunProcedure,
	}
}

// TestPhoneServicesRequireLoginAndClientHeaderOnEveryPath probes every phone procedure with the requests the guard
// must refuse, and checks that a well-formed Connect GET carrying login and the client header is answered 405 on
// every path, since none declares NO_SIDE_EFFECTS.
func TestPhoneServicesRequireLoginAndClientHeaderOnEveryPath(t *testing.T) {
	handler := newHandler(t, newTmuxClient(t))
	probes := []struct {
		name   string
		method string
		header http.Header
	}{
		{"POST no login", http.MethodPost, connectHeader("", "application/proto", true)},
		{"POST stranger login", http.MethodPost, connectHeader(otherLogin, "application/proto", true)},
		{"POST non-Connect content type", http.MethodPost, connectHeader(allowedLogin, "text/plain", true)},
		{"POST no client header", http.MethodPost, connectHeader(allowedLogin, "application/proto", false)},
		{"GET no client header", http.MethodGet, connectHeader(allowedLogin, "", false)},
		{"PUT no client header", http.MethodPut, connectHeader(allowedLogin, "application/proto", false)},
		{"DELETE no client header", http.MethodDelete, connectHeader(allowedLogin, "application/proto", false)},
	}
	for _, path := range phoneProcedures() {
		for _, probe := range probes {
			rec := serve(t, handler, probe.method, path, probe.header, "")
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s with %s: status = %d, want 403", probe.method, path, probe.name, rec.Code)
			}
		}
		getPath := path + "?connect=v1&encoding=json&message=" + url.QueryEscape("{}")
		rec := serve(t, handler, http.MethodGet, getPath, connectHeader(allowedLogin, "", true), "")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET %s with login and client header: status = %d, want 405 (no NO_SIDE_EFFECTS)", path, rec.Code)
		}
	}
}

// TestPageRequiresLoginAndNoClientHeader probes "/" with the requests the guard decides on a non-Connect path: no
// login and a stranger login are refused, and the allowed login is admitted with no client header, since the page
// is not a Connect path.
func TestPageRequiresLoginAndNoClientHeader(t *testing.T) {
	t.Parallel()
	handler := newWebHandler(t, pageFS())
	probes := []struct {
		name   string
		header http.Header
		want   int
	}{
		{"no login", connectHeader("", "", false), http.StatusForbidden},
		{"stranger login", connectHeader(otherLogin, "", false), http.StatusForbidden},
		{"allowed login no client header", connectHeader(allowedLogin, "", false), http.StatusOK},
	}
	for _, probe := range probes {
		rec := serve(t, handler, http.MethodGet, "/", probe.header, "")
		if rec.Code != probe.want {
			t.Fatalf("GET / with %s: status = %d, want %d", probe.name, rec.Code, probe.want)
		}
	}
}
