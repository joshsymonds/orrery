package server_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/push"
)

// pushConnectClient serves handler on an httptest server and returns a generated Push client whose requests
// carry the login and client headers the guard requires.
func pushConnectClient(t *testing.T, handler http.Handler) orreryv1connect.PushClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: headerTransport{base: http.DefaultTransport, login: allowedLogin}}
	return orreryv1connect.NewPushClient(httpClient, srv.URL)
}

// storedSubscription returns the stored subscription for endpoint, failing the test when none is stored.
func storedSubscription(t *testing.T, store *push.Store, endpoint string) push.Subscription {
	t.Helper()
	subs, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, sub := range subs {
		if sub.Endpoint == endpoint {
			return sub
		}
	}
	t.Fatalf("no subscription stored for %q", endpoint)
	return push.Subscription{}
}

// TestPushRegisterParity registers one subscription through the REST route and one through Push.Register on one
// store. Both are stored with the same key material, and each registration is removed through the other twin.
func TestPushRegisterParity(t *testing.T) {
	t.Parallel()

	store := newPushStore(t)
	handler := newHandlerWithStore(t, newTmuxClient(t), store)
	restEndpoint := "https://" + pushHost + "/synthetic-topic-parity-rest"
	connectEndpoint := "https://" + pushHost + "/synthetic-topic-parity-connect"
	p256dh, auth := pushKeys(t)

	assertStatus(t, postJSON(t, handler, pushPath, jsonBody(t, map[string]any{
		"endpoint": restEndpoint, "p256dh": p256dh, "auth": auth,
	})), http.StatusNoContent)
	if _, err := pushConnectClient(t, handler).Register(t.Context(), connect.NewRequest(&orreryv1.RegisterRequest{
		Endpoint: connectEndpoint, P256Dh: p256dh, Auth: auth,
	})); err != nil {
		t.Fatalf("Push.Register: %v", err)
	}
	assertStored(t, store, restEndpoint, connectEndpoint)

	restStored := storedSubscription(t, store, restEndpoint)
	connectStored := storedSubscription(t, store, connectEndpoint)
	if connectStored.P256dh != restStored.P256dh || connectStored.Auth != restStored.Auth {
		t.Fatalf("Push.Register stored %+v, want the REST route's key material %+v", connectStored, restStored)
	}
	if connectStored.P256dh != p256dh || connectStored.Auth != auth {
		t.Fatalf("Push.Register stored keys %q and %q, want %q and %q",
			connectStored.P256dh, connectStored.Auth, p256dh, auth)
	}

	// The REST route removes what Push.Register stored, and the store then holds the REST registration alone.
	assertStatus(t, serve(t, handler, http.MethodDelete, pushPath, writeHeader(),
		unregisterBody(t, connectEndpoint)), http.StatusNoContent)
	assertStored(t, store, restEndpoint)
}

// TestPushUnregisterParity registers two subscriptions through the REST route, removes one through Push.Unregister
// and the other through the REST route, and checks the store after each removal.
func TestPushUnregisterParity(t *testing.T) {
	t.Parallel()

	store := newPushStore(t)
	handler := newHandlerWithStore(t, newTmuxClient(t), store)
	connectEndpoint := "https://" + pushHost + "/synthetic-topic-parity-unregister-connect"
	restEndpoint := "https://" + pushHost + "/synthetic-topic-parity-unregister-rest"
	assertStatus(t, postJSON(t, handler, pushPath, registerBody(t, connectEndpoint)), http.StatusNoContent)
	assertStatus(t, postJSON(t, handler, pushPath, registerBody(t, restEndpoint)), http.StatusNoContent)
	assertStored(t, store, connectEndpoint, restEndpoint)

	if _, err := pushConnectClient(t, handler).Unregister(t.Context(), connect.NewRequest(&orreryv1.UnregisterRequest{
		Endpoint: connectEndpoint,
	})); err != nil {
		t.Fatalf("Push.Unregister: %v", err)
	}
	assertStored(t, store, restEndpoint)

	assertStatus(t, serve(t, handler, http.MethodDelete, pushPath, writeHeader(),
		unregisterBody(t, restEndpoint)), http.StatusNoContent)
	assertStored(t, store)
}

// TestPushRegisterErrorParity checks that each REST status Push.Register answers carries the table's Connect code
// and the REST body without its trailing newline as its message.
func TestPushRegisterErrorParity(t *testing.T) {
	t.Parallel()

	p256dh, auth := pushKeys(t)
	cases := []struct {
		name     string
		endpoint string
		p256dh   string
		auth     string
		status   int
		code     connect.Code
	}{
		{
			name:     "invalid keys",
			endpoint: "https://" + pushHost + "/synthetic-topic-parity-invalid",
			p256dh:   "!!",
			auth:     auth,
			status:   http.StatusBadRequest,
			code:     connect.CodeInvalidArgument,
		},
		{
			name:     "endpoint not allowed",
			endpoint: "http://" + pushHost + "/synthetic-topic-parity-refused",
			p256dh:   p256dh,
			auth:     auth,
			status:   http.StatusUnprocessableEntity,
			code:     connect.CodeInvalidArgument,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newPushStore(t)
			handler := newHandlerWithStore(t, newTmuxClient(t), store)
			rec := postJSON(t, handler, pushPath, jsonBody(t, map[string]any{
				"endpoint": tc.endpoint, "p256dh": tc.p256dh, "auth": tc.auth,
			}))
			assertStatus(t, rec, tc.status)
			wantMessage := strings.TrimSuffix(rec.Body.String(), "\n")

			_, err := pushConnectClient(t, handler).Register(t.Context(), connect.NewRequest(&orreryv1.RegisterRequest{
				Endpoint: tc.endpoint, P256Dh: tc.p256dh, Auth: tc.auth,
			}))
			pushConnectError(t, "Push.Register", err, tc.code, wantMessage)
			assertStored(t, store)
		})
	}
}

// TestPushUnregisterErrorParity checks that unregistering an unknown endpoint answers the REST 404 as Connect
// not_found with the REST body without its trailing newline as its message.
func TestPushUnregisterErrorParity(t *testing.T) {
	t.Parallel()

	store := newPushStore(t)
	handler := newHandlerWithStore(t, newTmuxClient(t), store)
	endpoint := "https://" + pushHost + "/synthetic-topic-parity-unknown"
	rec := serve(t, handler, http.MethodDelete, pushPath, writeHeader(), unregisterBody(t, endpoint))
	assertStatus(t, rec, http.StatusNotFound)
	wantMessage := strings.TrimSuffix(rec.Body.String(), "\n")

	_, err := pushConnectClient(t, handler).Unregister(t.Context(), connect.NewRequest(&orreryv1.UnregisterRequest{
		Endpoint: endpoint,
	}))
	pushConnectError(t, "Push.Unregister", err, connect.CodeNotFound, wantMessage)
	assertStored(t, store)
}

// pushConnectError fails the test unless err is a *connect.Error with code and message.
func pushConnectError(t *testing.T, method string, err error, code connect.Code, message string) {
	t.Helper()
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("%s error = %v, want a *connect.Error", method, err)
	}
	if connectErr.Code() != code {
		t.Fatalf("%s code = %v, want %v", method, connectErr.Code(), code)
	}
	if connectErr.Message() != message {
		t.Fatalf("%s message = %q, want the REST body %q", method, connectErr.Message(), message)
	}
}
