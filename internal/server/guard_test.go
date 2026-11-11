package server_test

import (
	"net/http"
	"testing"

	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

// phoneProcedures lists the Connect procedures of the phone services: Panes, Host, Push and Picker.
func phoneProcedures() []string {
	return []string{
		orreryv1connect.PanesListProcedure,
		orreryv1connect.PanesScreenProcedure,
		orreryv1connect.PanesConversationProcedure,
		orreryv1connect.PanesSendTextProcedure,
		orreryv1connect.PanesSendKeysProcedure,
		orreryv1connect.PanesMarkSeenProcedure,
		orreryv1connect.HostStatusProcedure,
		orreryv1connect.PushRegisterProcedure,
		orreryv1connect.PushUnregisterProcedure,
		orreryv1connect.PickerAskProcedure,
		orreryv1connect.PickerRunProcedure,
	}
}

// TestPhoneServicesRequireLoginAndClientHeaderOnEveryPath probes every phone procedure with the requests the guard
// must refuse, and checks that none of them answers a GET that carries login and the client header.
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
		rec := serve(t, handler, http.MethodGet, path, connectHeader(allowedLogin, "", true), "")
		if rec.Code == http.StatusOK {
			t.Fatalf("GET %s: status = 200, want it not served", path)
		}
	}
}
