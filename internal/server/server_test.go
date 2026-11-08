package server_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

const (
	allowedLogin = "user@example.test"
	otherLogin   = "stranger@example.test"
	jsonType     = "application/json"
	client       = "synthetic-client"

	// noConfig keeps the user's tmux config away from every test server.
	noConfig = "/dev/null"
	// longCommand keeps a pane alive until its test server is killed at cleanup.
	longCommand = "sleep 300"
	// synthQuestion is the synthetic value set on one pane's @orrery_question option.
	synthQuestion = "synthetic question for the list route?"
	// synthScreen is the synthetic text one pane prints for the screen route to return.
	synthScreen = "synthetic screen marker"
	screenWait  = 10 * time.Second
	screenPoll  = 10 * time.Millisecond
)

// screenBody mirrors the JSON body of GET /v1/panes/{id}/screen.
type screenBody struct {
	Pane   string `json:"pane"`
	Screen string `json:"screen"`
}

// newTmuxClient returns a client for a unique private tmux server name. The server is not started.
func newTmuxClient(t *testing.T) *tmuxctl.Client {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	name := fmt.Sprintf("orrery-test-%d-%x", os.Getpid(), suffix)
	tmux, err := tmuxctl.New(tmuxctl.Server{Name: name})
	if err != nil {
		t.Fatalf("New(Name=%q): %v", name, err)
	}
	return tmux
}

// newTmuxServer returns a client for a unique private tmux server and kills that server at cleanup.
func newTmuxServer(t *testing.T) *tmuxctl.Client {
	t.Helper()
	tmux := newTmuxClient(t)
	t.Cleanup(func() {
		if _, err := tmux.Run(context.Background(), "kill-server"); err != nil {
			t.Errorf("kill-server: %v", err)
		}
	})
	return tmux
}

// startSession starts a detached session named name running command, starting the server when needed.
func startSession(t *testing.T, tmux *tmuxctl.Client, name, command string) {
	t.Helper()
	_, err := tmux.Run(t.Context(), "-f", noConfig, "new-session", "-d", "-s", name, "-c", t.TempDir(), command)
	if err != nil {
		t.Fatalf("new-session %s: %v", name, err)
	}
}

// paneID returns the ID of the one pane in session.
func paneID(t *testing.T, tmux *tmuxctl.Client, session string) string {
	t.Helper()
	out, err := tmux.Run(t.Context(), "list-panes", "-t", session, "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes -t %s: %v", session, err)
	}
	return strings.TrimSpace(out)
}

// newHandler builds the guarded handler that admits only allowedLogin and routes to tmux.
func newHandler(t *testing.T, tmux *tmuxctl.Client) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{Logins: []string{allowedLogin}, Tmux: tmux})
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

// get sends a GET for path through handler with the login header set to login, or unset when login is "".
func get(t *testing.T, handler http.Handler, path, login string) *httptest.ResponseRecorder {
	t.Helper()
	header := http.Header{}
	if login != "" {
		header.Set(server.HeaderLogin, login)
	}
	return serve(t, handler, http.MethodGet, path, header, "")
}

// assertStatus fails the test when the recorded status differs from want.
func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d", rec.Code, want)
	}
}

// assertJSON fails the test when the recorded Content-Type is not application/json.
func assertJSON(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != jsonType {
		t.Fatalf("Content-Type = %q, want %q", got, jsonType)
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

// decodeObjects decodes body as a JSON array of objects.
func decodeObjects(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var objects []map[string]any
	if err := json.Unmarshal(body, &objects); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return objects
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

	handler := newHandler(t, newTmuxClient(t))
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

	handler := newHandler(t, newTmuxClient(t))
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

func TestServePanesList(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	startSession(t, tmux, "questioned", longCommand)
	startSession(t, tmux, "plain", longCommand)
	questionPane := paneID(t, tmux, "questioned")
	plainPane := paneID(t, tmux, "plain")
	if err := tmux.SetPaneOption(t.Context(), questionPane, protocol.OptionQuestion, synthQuestion); err != nil {
		t.Fatalf("SetPaneOption: %v", err)
	}

	rec := get(t, newHandler(t, tmux), "/v1/panes", allowedLogin)
	assertStatus(t, rec, http.StatusOK)
	assertJSON(t, rec)
	got := decodeObjects(t, rec.Body.Bytes())

	var list bytes.Buffer
	if err := cli.List(t.Context(), tmux, []string{"--json"}, &list); err != nil {
		t.Fatalf("cli.List: %v", err)
	}
	want := decodeObjects(t, list.Bytes())
	if len(got) != 2 || len(want) != 2 {
		t.Fatalf("got %d panes and cli.List has %d, want 2 each", len(got), len(want))
	}

	wantHash := map[string]string{
		questionPane: protocol.QuestionHash(synthQuestion),
		plainPane:    protocol.QuestionHash(""),
	}
	for i, pane := range got {
		id, _ := pane["id"].(string)
		expected, known := wantHash[id]
		if !known {
			t.Fatalf("pane %q is not one of the test panes", id)
		}
		if hash, _ := pane["question_hash"].(string); hash != expected {
			t.Fatalf("pane %s question_hash = %q, want %q", id, hash, expected)
		}
		delete(pane, "question_hash")
		if !reflect.DeepEqual(pane, want[i]) {
			t.Fatalf("pane %s = %v, want the cli.List fields %v", id, pane, want[i])
		}
	}
}

func TestServePanesNoServer(t *testing.T) {
	t.Parallel()

	rec := get(t, newHandler(t, newTmuxClient(t)), "/v1/panes", allowedLogin)
	assertStatus(t, rec, http.StatusOK)
	assertJSON(t, rec)
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Fatalf("body = %q, want []", body)
	}
}

func TestServePaneScreen(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	startSession(t, tmux, "s1", "echo "+synthScreen+"; "+longCommand)
	pane := paneID(t, tmux, "s1")
	handler := newHandler(t, tmux)
	path := "/v1/panes/" + url.PathEscape(pane) + "/screen"

	ctx, cancel := context.WithTimeout(t.Context(), screenWait)
	defer cancel()
	ticker := time.NewTicker(screenPoll)
	defer ticker.Stop()
	// The shell prints the marker after the server starts, so poll until it is on the screen.
	rec := get(t, handler, path, allowedLogin)
	for rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), synthScreen) {
		select {
		case <-ctx.Done():
			t.Fatalf("screen of %s never showed %q; last status %d", pane, synthScreen, rec.Code)
		case <-ticker.C:
		}
		rec = get(t, handler, path, allowedLogin)
	}

	assertJSON(t, rec)
	var body screenBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	if body.Pane != pane {
		t.Fatalf("pane = %q, want %q", body.Pane, pane)
	}
	if !strings.Contains(body.Screen, synthScreen) {
		t.Fatalf("screen = %q, want it to contain %q", body.Screen, synthScreen)
	}
}

func TestServePaneScreenRejectsMalformedID(t *testing.T) {
	t.Parallel()

	handler := newHandler(t, newTmuxClient(t))
	for _, id := range []string{"abc", "%", "%x", "1", "%1x", "%-1", "%%1"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			rec := get(t, handler, "/v1/panes/"+url.PathEscape(id)+"/screen", allowedLogin)
			assertStatus(t, rec, http.StatusBadRequest)
		})
	}
}

func TestServePaneScreenAbsentPane(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	startSession(t, tmux, "s1", longCommand)
	rec := get(t, newHandler(t, tmux), "/v1/panes/"+url.PathEscape("%999999")+"/screen", allowedLogin)
	assertStatus(t, rec, http.StatusNotFound)
}

func TestServeTmuxRoutesRequireListedLogin(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	startSession(t, tmux, "s1", longCommand)
	pane := paneID(t, tmux, "s1")
	handler := newHandler(t, tmux)
	paths := []string{"/v1/panes", "/v1/panes/" + url.PathEscape(pane) + "/screen"}
	logins := []string{"", otherLogin}
	for _, path := range paths {
		for _, login := range logins {
			t.Run(fmt.Sprintf("%s login %q", path, login), func(t *testing.T) {
				t.Parallel()
				rec := get(t, handler, path, login)
				assertStatus(t, rec, http.StatusForbidden)
				assertNoCORS(t, rec)
			})
		}
	}
}

func TestServeRequiresTmux(t *testing.T) {
	t.Parallel()

	if _, err := server.New(server.Config{Logins: []string{allowedLogin}}); err == nil {
		t.Fatal("New without a tmux client: want error, got nil")
	}
}
