package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/server"
)

// promptClientTransport sends every request as allowedLogin, with the client header set to client.
type promptClientTransport struct {
	base   http.RoundTripper
	client string
}

// RoundTrip sends a copy of req with the guard headers set.
func (p promptClientTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set(server.HeaderLogin, allowedLogin)
	clone.Header.Set(server.HeaderClient, p.client)
	return p.base.RoundTrip(clone)
}

// TestPromptClientControlForwardsHeaderToStartAndSay checks that Start and Say forward the X-Orrery-Client header's
// value to the engine as the client of the request.
func TestPromptClientControlForwardsHeaderToStartAndSay(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dir, _ := sessionUnder(t, home)
	fake := &fakeEngine{}
	handler := newHomedControlHandler(t, home, newFakeOrreryd(t, fake), 0)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: promptClientTransport{base: http.DefaultTransport, client: "orrery-android"}}
	control := orreryv1connect.NewControlClient(httpClient, srv.URL)

	_, err := control.Start(t.Context(), connect.NewRequest(&orreryv1.StartRequest{
		Dir: dir, Prompt: "synthetic prompt", Id: synthSessionID,
	}))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	_, err = control.Say(t.Context(), connect.NewRequest(&orreryv1.SayRequest{
		Id: synthSessionID, Text: "synthetic text", PromptId: "synthetic-prompt",
	}))
	if err != nil {
		t.Fatalf("Say: %v", err)
	}

	calls := fake.recorded()
	if len(calls) != 2 {
		t.Fatalf("forwarded %d requests, want a start and a say", len(calls))
	}
	start, ok := calls[0].(*enginev1.StartRequest)
	if !ok {
		t.Fatalf("first forwarded request is %T, want a StartRequest", calls[0])
	}
	if got := start.GetClient(); got != "orrery-android" {
		t.Fatalf("Start forwarded client %q, want the header's orrery-android", got)
	}
	say, ok := calls[1].(*enginev1.SayRequest)
	if !ok {
		t.Fatalf("second forwarded request is %T, want a SayRequest", calls[1])
	}
	if got := say.GetClient(); got != "orrery-android" {
		t.Fatalf("Say forwarded client %q, want the header's orrery-android", got)
	}
}
