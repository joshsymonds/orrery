package server_test

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
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

// connectRegister registers endpoint with fresh keys through Push.Register and fails the test on error.
func connectRegister(t *testing.T, client orreryv1connect.PushClient, endpoint string) {
	t.Helper()
	p256dh, auth := pushKeys(t)
	if _, err := client.Register(t.Context(), connect.NewRequest(&orreryv1.RegisterRequest{
		Endpoint: endpoint, P256Dh: p256dh, Auth: auth,
	})); err != nil {
		t.Fatalf("Push.Register %q: %v", endpoint, err)
	}
}

// TestPushRegisterParity registers one subscription through Push.Register and checks that the store holds it with
// the key material sent. Push.Unregister then removes it, and the store is empty.
func TestPushRegisterParity(t *testing.T) {
	t.Parallel()

	store := newPushStore(t)
	client := pushConnectClient(t, newHandlerWithStore(t, newTmuxClient(t), store))
	endpoint := "https://" + pushHost + "/synthetic-topic-parity-connect"
	p256dh, auth := pushKeys(t)

	if _, err := client.Register(t.Context(), connect.NewRequest(&orreryv1.RegisterRequest{
		Endpoint: endpoint, P256Dh: p256dh, Auth: auth,
	})); err != nil {
		t.Fatalf("Push.Register: %v", err)
	}
	assertStored(t, store, endpoint)
	stored := storedSubscription(t, store, endpoint)
	if stored.P256dh != p256dh || stored.Auth != auth {
		t.Fatalf("Push.Register stored keys %q and %q, want %q and %q", stored.P256dh, stored.Auth, p256dh, auth)
	}

	if _, err := client.Unregister(t.Context(), connect.NewRequest(&orreryv1.UnregisterRequest{
		Endpoint: endpoint,
	})); err != nil {
		t.Fatalf("Push.Unregister: %v", err)
	}
	assertStored(t, store)
}

// TestPushUnregisterParity registers two subscriptions through Push.Register, removes one through Push.Unregister,
// and checks the store after each removal.
func TestPushUnregisterParity(t *testing.T) {
	t.Parallel()

	store := newPushStore(t)
	client := pushConnectClient(t, newHandlerWithStore(t, newTmuxClient(t), store))
	removed := "https://" + pushHost + "/synthetic-topic-parity-unregister-removed"
	kept := "https://" + pushHost + "/synthetic-topic-parity-unregister-kept"
	connectRegister(t, client, removed)
	connectRegister(t, client, kept)
	assertStored(t, store, removed, kept)

	if _, err := client.Unregister(t.Context(), connect.NewRequest(&orreryv1.UnregisterRequest{
		Endpoint: removed,
	})); err != nil {
		t.Fatalf("Push.Unregister: %v", err)
	}
	assertStored(t, store, kept)

	if _, err := client.Unregister(t.Context(), connect.NewRequest(&orreryv1.UnregisterRequest{
		Endpoint: kept,
	})); err != nil {
		t.Fatalf("Push.Unregister: %v", err)
	}
	assertStored(t, store)
}

// TestPushRegisterErrorParity checks that each refused Push.Register carries its concrete Connect code and message,
// and that nothing is stored.
func TestPushRegisterErrorParity(t *testing.T) {
	t.Parallel()

	p256dh, auth := pushKeys(t)
	endpoint := "https://" + pushHost + "/synthetic-topic-parity-bad"
	shortValue := base64.RawURLEncoding.EncodeToString([]byte("short"))
	offCurve := base64.RawURLEncoding.EncodeToString(append([]byte{0x04}, make([]byte, 64)...))
	const badBody = "bad request body"
	cases := []struct {
		name     string
		endpoint string
		p256dh   string
		auth     string
		code     connect.Code
		message  string
	}{
		{
			name: "invalid keys", endpoint: endpoint, p256dh: "!!", auth: auth,
			code: connect.CodeInvalidArgument, message: badBody,
		},
		{
			name: "short p256dh", endpoint: endpoint, p256dh: shortValue, auth: auth,
			code: connect.CodeInvalidArgument, message: badBody,
		},
		{
			name: "off-curve p256dh", endpoint: endpoint, p256dh: offCurve, auth: auth,
			code: connect.CodeInvalidArgument, message: badBody,
		},
		{
			name: "short auth", endpoint: endpoint, p256dh: p256dh, auth: shortValue,
			code: connect.CodeInvalidArgument, message: badBody,
		},
		{
			name: "missing p256dh", endpoint: endpoint, p256dh: "", auth: auth,
			code: connect.CodeInvalidArgument, message: badBody,
		},
		{
			name: "missing auth", endpoint: endpoint, p256dh: p256dh, auth: "",
			code: connect.CodeInvalidArgument, message: badBody,
		},
		{
			name: "endpoint not allowed", endpoint: "http://" + pushHost + "/synthetic-topic-parity-refused",
			p256dh: p256dh, auth: auth, code: connect.CodeInvalidArgument, message: "endpoint not allowed",
		},
		{
			name: "endpoint on another host", endpoint: "https://other.example.test/synthetic-topic-parity-refused",
			p256dh: p256dh, auth: auth, code: connect.CodeInvalidArgument, message: "endpoint not allowed",
		},
		{
			name: "missing endpoint", endpoint: "", p256dh: p256dh, auth: auth,
			code: connect.CodeInvalidArgument, message: "endpoint not allowed",
		},
		{
			name: "empty request", endpoint: "", p256dh: "", auth: "",
			code: connect.CodeInvalidArgument, message: "endpoint not allowed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newPushStore(t)
			client := pushConnectClient(t, newHandlerWithStore(t, newTmuxClient(t), store))
			_, err := client.Register(t.Context(), connect.NewRequest(&orreryv1.RegisterRequest{
				Endpoint: tc.endpoint, P256Dh: tc.p256dh, Auth: tc.auth,
			}))
			assertRPCError(t, "Push.Register", err, tc.code, tc.message)
			assertStored(t, store)
		})
	}
}

// TestPushUnregisterErrorParity checks that each Push.Unregister for an endpoint with no stored subscription answers
// not_found with the message "endpoint not registered", and that the stored subscription is kept.
func TestPushUnregisterErrorParity(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		endpoint string
	}{
		{name: "unknown endpoint", endpoint: "https://" + pushHost + "/synthetic-topic-parity-unknown"},
		{name: "missing endpoint", endpoint: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newPushStore(t)
			client := pushConnectClient(t, newHandlerWithStore(t, newTmuxClient(t), store))
			kept := "https://" + pushHost + "/synthetic-topic-parity-kept"
			connectRegister(t, client, kept)

			_, err := client.Unregister(t.Context(), connect.NewRequest(&orreryv1.UnregisterRequest{
				Endpoint: tc.endpoint,
			}))
			assertRPCError(t, "Push.Unregister", err, connect.CodeNotFound, "endpoint not registered")
			assertStored(t, store, kept)
		})
	}
}

// assertRPCError fails the test unless err is a *connect.Error with code and message.
func assertRPCError(t *testing.T, method string, err error, code connect.Code, message string) {
	t.Helper()
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("%s error = %v, want a *connect.Error", method, err)
	}
	if connectErr.Code() != code {
		t.Fatalf("%s code = %v, want %v", method, connectErr.Code(), code)
	}
	if connectErr.Message() != message {
		t.Fatalf("%s message = %q, want %q", method, connectErr.Message(), message)
	}
}
