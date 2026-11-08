package server_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/server"
)

const (
	allowedLogin = "user@example.test"
	otherLogin   = "stranger@example.test"
	jsonType     = "application/json"
	client       = "synthetic-client"
)

// newHandler builds the guarded handler that admits only allowedLogin.
func newHandler(t *testing.T) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{Logins: []string{allowedLogin}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// serve sends one request through handler and returns the recorded response.
func serve(
	t *testing.T,
	handler http.Handler,
	method, path string,
	header http.Header,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for name, values := range header {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// assertStatus fails the test when the recorded status differs from want.
func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d", rec.Code, want)
	}
}

// assertNoCORS fails the test when any response header is an Access-Control-* header.
func assertNoCORS(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	for name := range rec.Header() {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			t.Fatalf("response carries CORS header %q", name)
		}
	}
}

func TestServeLoopbackOnly(t *testing.T) {
	t.Parallel()

	ln, err := server.Listen(context.Background(), "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen(127.0.0.1:0): %v", err)
	}
	t.Cleanup(func() {
		if err := ln.Close(); err != nil {
			t.Errorf("close listener: %v", err)
		}
	})
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("Addr() = %T, want *net.TCPAddr", ln.Addr())
	}
	if !tcpAddr.IP.IsLoopback() {
		t.Fatalf("bound address %v is not loopback", tcpAddr)
	}

	refused := []string{
		"0.0.0.0:0",
		":0",
		"[::]:0",
		"192.0.2.1:0",
		"localhost:0",
		"not-an-address",
	}
	for _, addr := range refused {
		t.Run(addr, func(t *testing.T) {
			t.Parallel()
			ln, err := server.Listen(context.Background(), addr)
			if err == nil {
				_ = ln.Close()
				t.Fatalf("Listen(%q) bound a listener, want an error", addr)
			}
		})
	}
}

func TestServeAuth(t *testing.T) {
	t.Parallel()

	handler := newHandler(t)
	cases := []struct {
		name   string
		login  string
		method string
		want   int
	}{
		{name: "missing login GET", login: "", method: http.MethodGet, want: http.StatusForbidden},
		{name: "missing login POST", login: "", method: http.MethodPost, want: http.StatusForbidden},
		{name: "unlisted login GET", login: otherLogin, method: http.MethodGet, want: http.StatusForbidden},
		{name: "unlisted login POST", login: otherLogin, method: http.MethodPost, want: http.StatusForbidden},
		{name: "listed login GET", login: allowedLogin, method: http.MethodGet, want: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			header := http.Header{}
			if tc.login != "" {
				header.Set(server.HeaderLogin, tc.login)
			}
			header.Set("Content-Type", jsonType)
			header.Set(server.HeaderClient, client)
			rec := serve(t, handler, tc.method, "/", header, "{}")
			assertStatus(t, rec, tc.want)
		})
	}
}

func TestServeCrossOrigin(t *testing.T) {
	t.Parallel()

	handler := newHandler(t)
	cases := []struct {
		name        string
		method      string
		contentType string
		client      string
		want        int
	}{
		{
			name:        "form POST without client header",
			method:      http.MethodPost,
			contentType: "application/x-www-form-urlencoded",
			want:        http.StatusForbidden,
		},
		{
			name:        "text/plain POST with client header",
			method:      http.MethodPost,
			contentType: "text/plain",
			client:      client,
			want:        http.StatusForbidden,
		},
		{
			name:        "JSON POST without client header",
			method:      http.MethodPost,
			contentType: jsonType,
			want:        http.StatusForbidden,
		},
		{
			name:        "JSON POST with client header",
			method:      http.MethodPost,
			contentType: jsonType,
			client:      client,
			want:        http.StatusNotFound,
		},
		{
			name:        "JSON with charset POST with client header",
			method:      http.MethodPost,
			contentType: "application/json; charset=utf-8",
			client:      client,
			want:        http.StatusNotFound,
		},
		{
			name:        "form DELETE without client header",
			method:      http.MethodDelete,
			contentType: "application/x-www-form-urlencoded",
			want:        http.StatusForbidden,
		},
		{
			name:        "text/plain DELETE with client header",
			method:      http.MethodDelete,
			contentType: "text/plain",
			client:      client,
			want:        http.StatusForbidden,
		},
		{
			name:        "JSON DELETE without client header",
			method:      http.MethodDelete,
			contentType: jsonType,
			want:        http.StatusForbidden,
		},
		{
			name:        "JSON DELETE with client header",
			method:      http.MethodDelete,
			contentType: jsonType,
			client:      client,
			want:        http.StatusNotFound,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			header := http.Header{}
			header.Set(server.HeaderLogin, allowedLogin)
			header.Set("Content-Type", tc.contentType)
			if tc.client != "" {
				header.Set(server.HeaderClient, tc.client)
			}
			rec := serve(t, handler, tc.method, "/", header, "{}")
			assertStatus(t, rec, tc.want)
			assertNoCORS(t, rec)
		})
	}

	t.Run("preflight carries no CORS headers", func(t *testing.T) {
		t.Parallel()
		header := http.Header{}
		header.Set(server.HeaderLogin, allowedLogin)
		header.Set("Origin", "https://attacker.example.test")
		header.Set("Access-Control-Request-Method", http.MethodPost)
		header.Set("Access-Control-Request-Headers", server.HeaderClient)
		rec := serve(t, handler, http.MethodOptions, "/", header, "")
		assertNoCORS(t, rec)
	})
}
