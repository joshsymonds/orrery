package server_test

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/conversation"
	"github.com/joshsymonds/orrery/internal/hoststatus"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

const (
	allowedLogin = "user@example.test"
	otherLogin   = "stranger@example.test"
	jsonType     = "application/json"
	textType     = "text/plain; charset=utf-8"
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
	return newHandlerWithStore(t, tmux, newPushStore(t))
}

// newHandlerWithStore builds the guarded handler that admits only allowedLogin, routes to tmux, and
// registers push subscriptions in store.
func newHandlerWithStore(t *testing.T, tmux *tmuxctl.Client, store *push.Store) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{Logins: []string{allowedLogin}, Tmux: tmux, Push: store})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// pushHost is the synthetic push host the test store allows.
const pushHost = "push.example.test"

// pushPath is the push registration route.
const pushPath = "/v1/push/register"

// newPushStore returns a store under a fresh temporary directory that allows only pushHost.
func newPushStore(t *testing.T) *push.Store {
	t.Helper()
	store, err := push.NewStore(filepath.Join(t.TempDir(), "push.json"), []string{pushHost})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

// pushKeys returns a fresh P-256 public key and a 16-byte auth secret, both base64url without padding.
func pushKeys(t *testing.T) (string, string) {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate P-256 key: %v", err)
	}
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		t.Fatalf("generate auth secret: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
		base64.RawURLEncoding.EncodeToString(secret)
}

// registerBody returns a POST body that registers endpoint with fresh keys.
func registerBody(t *testing.T, endpoint string) string {
	t.Helper()
	p256dh, auth := pushKeys(t)
	return jsonBody(t, map[string]any{"endpoint": endpoint, "p256dh": p256dh, "auth": auth})
}

// unregisterBody returns a DELETE body that names endpoint.
func unregisterBody(t *testing.T, endpoint string) string {
	t.Helper()
	return jsonBody(t, map[string]any{"endpoint": endpoint})
}

// assertStored fails the test unless store holds exactly the subscriptions for endpoints, in any order.
func assertStored(t *testing.T, store *push.Store, endpoints ...string) {
	t.Helper()
	subs, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := make([]string, 0, len(subs))
	for _, sub := range subs {
		got = append(got, sub.Endpoint)
	}
	slices.Sort(got)
	slices.Sort(endpoints)
	if !slices.Equal(got, endpoints) {
		t.Fatalf("stored endpoints = %q, want %q", got, endpoints)
	}
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

	got, want := listUntilAgreed(t, tmux, newHandler(t, tmux))
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

// listUntilAgreed reads GET /v1/panes and cli.List until the two agree on every field except question_hash.
// A pane's current command changes while its shell starts, so the two reads can differ until it settles.
func listUntilAgreed(t *testing.T, tmux *tmuxctl.Client, handler http.Handler) ([]map[string]any, []map[string]any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), screenWait)
	defer cancel()
	ticker := time.NewTicker(screenPoll)
	defer ticker.Stop()
	for {
		rec := get(t, handler, "/v1/panes", allowedLogin)
		assertStatus(t, rec, http.StatusOK)
		assertJSON(t, rec)
		got := decodeObjects(t, rec.Body.Bytes())

		var list bytes.Buffer
		if err := cli.List(ctx, tmux, []string{"--json"}, &list); err != nil {
			t.Fatalf("cli.List: %v", err)
		}
		want := decodeObjects(t, list.Bytes())
		if agreeWithoutHash(got, want) {
			return got, want
		}
		select {
		case <-ctx.Done():
			t.Fatalf("GET /v1/panes = %v never agreed with cli.List = %v", got, want)
		case <-ticker.C:
		}
	}
}

// agreeWithoutHash reports whether got and want hold the same panes in order once question_hash is removed from got.
func agreeWithoutHash(got, want []map[string]any) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		pane := maps.Clone(got[i])
		delete(pane, "question_hash")
		if !reflect.DeepEqual(pane, want[i]) {
			return false
		}
	}
	return true
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

	if got := rec.Header().Get("Content-Type"); got != textType {
		t.Fatalf("Content-Type = %q, want %q", got, textType)
	}
	if !strings.Contains(rec.Body.String(), synthScreen) {
		t.Fatalf("body = %q, want it to contain %q", rec.Body.String(), synthScreen)
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

const (
	// synthColor is the synthetic text a pane prints in red for the ansi parameter.
	synthColor = "synthetic color marker"
	// synthRed is the escape sequence that starts red text in a screen body.
	synthRed = "\x1b[31m"
	// historyCount is the number of numbered lines a history pane prints, more than the pane's visible height.
	historyCount = 60
	// colorCommand prints the synthetic marker in red, then keeps the pane alive.
	colorCommand = `printf '\033[31m` + synthColor + `\033[0m\n'; ` + longCommand
)

// historyLine returns the synthetic numbered line n that a history pane prints.
func historyLine(n int) string {
	return fmt.Sprintf("synthetic history line %d", n)
}

// historyCommand prints line 1 in red and lines 2 through historyCount plain, then keeps the pane alive.
func historyCommand() string {
	first := fmt.Sprintf(`printf '\033[31m%s\033[0m\n'; `, historyLine(1))
	loop := fmt.Sprintf(
		`i=2; while [ $i -le %d ]; do echo "synthetic history line $i"; `+
			`i=$((i+1)); done; `,
		historyCount,
	)
	return first + loop + longCommand
}

// pollScreen polls path until it answers 200 with a body containing want, and returns that body.
func pollScreen(t *testing.T, handler http.Handler, path, want string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), screenWait)
	defer cancel()
	ticker := time.NewTicker(screenPoll)
	defer ticker.Stop()
	rec := get(t, handler, path, allowedLogin)
	for rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
		select {
		case <-ctx.Done():
			t.Fatalf("screen %s never showed %q; last status %d", path, want, rec.Code)
		case <-ticker.C:
		}
		rec = get(t, handler, path, allowedLogin)
	}
	return rec.Body.String()
}

func TestServePaneScreenANSI(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	startSession(t, tmux, "s1", colorCommand)
	pane := paneID(t, tmux, "s1")
	handler := newHandler(t, tmux)
	path := "/v1/panes/" + url.PathEscape(pane) + "/screen"

	pollScreen(t, handler, path+"?ansi=1", synthRed+synthColor)

	plain := get(t, handler, path, allowedLogin)
	assertStatus(t, plain, http.StatusOK)
	if !strings.Contains(plain.Body.String(), synthColor) {
		t.Fatalf("plain body = %q, want it to contain %q", plain.Body.String(), synthColor)
	}
	if strings.Contains(plain.Body.String(), "\x1b") {
		t.Fatalf("plain body = %q, want no escape sequences", plain.Body.String())
	}

	zero := get(t, handler, path+"?ansi=0", allowedLogin)
	assertStatus(t, zero, http.StatusOK)
	if zero.Body.String() != plain.Body.String() {
		t.Fatalf("ansi=0 body = %q, want the plain body %q", zero.Body.String(), plain.Body.String())
	}
}

func TestServePaneScreenHistory(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	startSession(t, tmux, "s1", historyCommand())
	pane := paneID(t, tmux, "s1")
	handler := newHandler(t, tmux)
	path := "/v1/panes/" + url.PathEscape(pane) + "/screen"
	first := historyLine(1)
	count := strconv.Itoa(historyCount)

	// The visible screen shows the last line the pane prints, so poll until that line is on it.
	plain := pollScreen(t, handler, path, historyLine(historyCount))
	if slices.Contains(strings.Split(plain, "\n"), first) {
		t.Fatalf("plain body = %q, want %q only in scrollback", plain, first)
	}

	withHistory := get(t, handler, path+"?history="+count, allowedLogin)
	assertStatus(t, withHistory, http.StatusOK)
	if !slices.Contains(strings.Split(withHistory.Body.String(), "\n"), first) {
		t.Fatalf("history body = %q, want line %q", withHistory.Body.String(), first)
	}

	for _, query := range []string{"history=0", "history=2000"} {
		assertStatus(t, get(t, handler, path+"?"+query, allowedLogin), http.StatusOK)
	}

	both := get(t, handler, path+"?ansi=1&history="+count, allowedLogin)
	assertStatus(t, both, http.StatusOK)
	if !strings.Contains(both.Body.String(), synthRed+first) {
		t.Fatalf("ansi and history body = %q, want %q", both.Body.String(), synthRed+first)
	}
}

func TestServePaneScreenRejectsBadParams(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	startSession(t, tmux, "s1", longCommand)
	pane := paneID(t, tmux, "s1")
	handler := newHandler(t, tmux)
	path := "/v1/panes/" + url.PathEscape(pane) + "/screen"
	queries := []string{
		"ansi=2", "ansi=yes", "ansi=-1", "ansi=true", "ansi=", "ansi=1&ansi=1",
		"history=-1", "history=2001", "history=abc", "history=", "history=+5", "history=%205",
		"history=1.5", "history=0x10", "history=1&history=2",
	}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			rec := get(t, handler, path+"?"+query, allowedLogin)
			assertStatus(t, rec, http.StatusBadRequest)
		})
	}
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

func TestServeRequiresPush(t *testing.T) {
	t.Parallel()

	if _, err := server.New(server.Config{Logins: []string{allowedLogin}, Tmux: newTmuxClient(t)}); err == nil {
		t.Fatal("New without a push store: want error, got nil")
	}
}

func TestPushRegisterStoresAndRemoves(t *testing.T) {
	t.Parallel()

	store := newPushStore(t)
	handler := newHandlerWithStore(t, newTmuxClient(t), store)
	endpoint := "https://" + pushHost + "/synthetic-topic-register"
	assertStatus(t, postJSON(t, handler, pushPath, registerBody(t, endpoint)), http.StatusNoContent)
	assertStored(t, store, endpoint)
	rec := serve(t, handler, http.MethodDelete, pushPath, writeHeader(), unregisterBody(t, endpoint))
	assertStatus(t, rec, http.StatusNoContent)
	assertStored(t, store)
}

func TestPushRegisterDeleteUnknownIsNotFound(t *testing.T) {
	t.Parallel()

	store := newPushStore(t)
	handler := newHandlerWithStore(t, newTmuxClient(t), store)
	endpoint := "https://" + pushHost + "/synthetic-topic-unknown"
	rec := serve(t, handler, http.MethodDelete, pushPath, writeHeader(), unregisterBody(t, endpoint))
	assertStatus(t, rec, http.StatusNotFound)
	if strings.Contains(rec.Body.String(), endpoint) {
		t.Fatal("404 body echoes the endpoint")
	}
	assertStored(t, store)
}

func TestPushRegisterRefusesEndpoint(t *testing.T) {
	t.Parallel()

	endpoints := []string{
		"http://" + pushHost + "/synthetic-topic-refused",
		"https://other.example.test/synthetic-topic-refused",
	}
	for _, endpoint := range endpoints {
		t.Run(endpoint, func(t *testing.T) {
			t.Parallel()

			store := newPushStore(t)
			handler := newHandlerWithStore(t, newTmuxClient(t), store)
			rec := postJSON(t, handler, pushPath, registerBody(t, endpoint))
			assertStatus(t, rec, http.StatusUnprocessableEntity)
			if strings.Contains(rec.Body.String(), endpoint) {
				t.Fatal("422 body echoes the endpoint")
			}
			assertStored(t, store)
		})
	}
}

func TestPushRegisterRejectsBadBodies(t *testing.T) {
	t.Parallel()

	endpoint := "https://" + pushHost + "/synthetic-topic-bad"
	p256dh, auth := pushKeys(t)
	shortValue := base64.RawURLEncoding.EncodeToString([]byte("short"))
	offCurve := base64.RawURLEncoding.EncodeToString(append([]byte{0x04}, make([]byte, 64)...))
	bodies := map[string]string{
		"not json":             `not json`,
		"array":                `[]`,
		"null":                 `null`,
		"empty object":         `{}`,
		"missing endpoint":     jsonBody(t, map[string]any{"p256dh": p256dh, "auth": auth}),
		"numeric endpoint":     jsonBody(t, map[string]any{"endpoint": 1, "p256dh": p256dh, "auth": auth}),
		"missing p256dh":       jsonBody(t, map[string]any{"endpoint": endpoint, "auth": auth}),
		"null auth":            jsonBody(t, map[string]any{"endpoint": endpoint, "p256dh": p256dh, "auth": nil}),
		"short p256dh":         jsonBody(t, map[string]any{"endpoint": endpoint, "p256dh": shortValue, "auth": auth}),
		"off-curve p256dh":     jsonBody(t, map[string]any{"endpoint": endpoint, "p256dh": offCurve, "auth": auth}),
		"short auth":           jsonBody(t, map[string]any{"endpoint": endpoint, "p256dh": p256dh, "auth": shortValue}),
		"not base64url p256dh": jsonBody(t, map[string]any{"endpoint": endpoint, "p256dh": "!!", "auth": auth}),
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := newPushStore(t)
			handler := newHandlerWithStore(t, newTmuxClient(t), store)
			assertStatus(t, postJSON(t, handler, pushPath, body), http.StatusBadRequest)
			assertStored(t, store)
		})
	}
}

func TestPushRegisterDeleteRejectsBadBodies(t *testing.T) {
	t.Parallel()

	store := newPushStore(t)
	handler := newHandlerWithStore(t, newTmuxClient(t), store)
	endpoint := "https://" + pushHost + "/synthetic-topic-kept"
	assertStatus(t, postJSON(t, handler, pushPath, registerBody(t, endpoint)), http.StatusNoContent)
	bodies := []string{
		`not json`,
		`[]`,
		`null`,
		`{}`,
		`{"endpoint":1}`,
	}
	for _, body := range bodies {
		assertStatus(t, serve(t, handler, http.MethodDelete, pushPath, writeHeader(), body), http.StatusBadRequest)
	}
	assertStored(t, store, endpoint)
}

// guardFixture returns a store holding one registered subscription and a handler over it, with the endpoint
// of that subscription.
func guardFixture(t *testing.T) (*push.Store, http.Handler, string) {
	t.Helper()
	store := newPushStore(t)
	handler := newHandlerWithStore(t, newTmuxClient(t), store)
	registered := "https://" + pushHost + "/synthetic-topic-registered"
	p256dh, auth := pushKeys(t)
	if err := store.Add(push.Subscription{Endpoint: registered, P256dh: p256dh, Auth: auth}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	return store, handler, registered
}

func TestPushRegisterRequiresGuard(t *testing.T) {
	t.Parallel()

	unguarded := map[string]func(http.Header){
		"without client header": func(h http.Header) { h.Del(server.HeaderClient) },
		"without login":         func(h http.Header) { h.Del(server.HeaderLogin) },
		"unlisted login":        func(h http.Header) { h.Set(server.HeaderLogin, otherLogin) },
		"text content type":     func(h http.Header) { h.Set("Content-Type", textType) },
	}
	for name, mutate := range unguarded {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store, handler, registered := guardFixture(t)
			attempted := "https://" + pushHost + "/synthetic-topic-attempted"
			header := writeHeader()
			mutate(header)
			post := serve(t, handler, http.MethodPost, pushPath, header, registerBody(t, attempted))
			assertStatus(t, post, http.StatusForbidden)
			del := serve(t, handler, http.MethodDelete, pushPath, header, unregisterBody(t, registered))
			assertStatus(t, del, http.StatusForbidden)
			assertStored(t, store, registered)
		})
	}
}

func TestPushRegisterAdmitsGuardedWrites(t *testing.T) {
	t.Parallel()

	store, handler, registered := guardFixture(t)
	attempted := "https://" + pushHost + "/synthetic-topic-attempted"
	assertStatus(t, postJSON(t, handler, pushPath, registerBody(t, attempted)), http.StatusNoContent)
	rec := serve(t, handler, http.MethodDelete, pushPath, writeHeader(), unregisterBody(t, registered))
	assertStatus(t, rec, http.StatusNoContent)
	assertStored(t, store, attempted)
}

// inputReady is printed by an input pane once its line discipline is off, so the test may type into it.
const inputReady = "input ready"

// synthText is the synthetic text the text route pastes.
const synthText = "synthetic text"

// writeRoute names a write route and a body it accepts.
type writeRoute struct {
	name string
	body string
}

// writeRoutes returns each write route with a body it accepts.
func writeRoutes() []writeRoute {
	return []writeRoute{
		{name: "text", body: `{"text":"` + synthText + `"}`},
		{name: "keys", body: `{"keys":["k"]}`},
		{name: "seen", body: `{}`},
	}
}

// writeHeader returns the headers of a request the guard admits for a write.
func writeHeader() http.Header {
	header := http.Header{}
	header.Set(server.HeaderLogin, allowedLogin)
	header.Set("Content-Type", jsonType)
	header.Set(server.HeaderClient, client)
	return header
}

// paneRoute returns the write path of route on pane.
func paneRoute(pane, route string) string {
	return "/v1/panes/" + url.PathEscape(pane) + "/" + route
}

// jsonBody encodes fields as a JSON object body.
func jsonBody(t *testing.T, fields map[string]any) string {
	t.Helper()
	body, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}
	return string(body)
}

// postJSON sends a write with the admitted headers and body through handler.
func postJSON(t *testing.T, handler http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return serve(t, handler, http.MethodPost, path, writeHeader(), body)
}

// startInputPane starts session whose pane prints screen, enables bracketed paste, turns off the line
// discipline, and runs cat into a file. It returns the pane ID and the file, once the pane is ready.
// The screen is written to a file and printed with cat, so its text is never interpolated into the shell.
func startInputPane(t *testing.T, tmux *tmuxctl.Client, session, screen string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "input")
	screenFile := filepath.Join(dir, "screen")
	if err := os.WriteFile(screenFile, []byte(screen+"\n"), 0o600); err != nil {
		t.Fatalf("write screen: %v", err)
	}
	script := fmt.Sprintf(
		`cat %s; printf "\033[?2004h"; stty -icanon -echo -icrnl; printf "%s\n"; exec cat > %s`,
		screenFile, inputReady, file,
	)
	startSession(t, tmux, session, "exec sh -c '"+script+"'")
	pane := paneID(t, tmux, session)
	waitForScreen(t, tmux, pane, inputReady)
	return pane, file
}

// waitForScreen waits until want appears in the captured screen of pane.
func waitForScreen(t *testing.T, tmux *tmuxctl.Client, pane, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), screenWait)
	defer cancel()
	ticker := time.NewTicker(screenPoll)
	defer ticker.Stop()
	for {
		screen, err := tmux.Run(ctx, "capture-pane", "-p", "-J", "-t", pane)
		if err != nil {
			t.Fatalf("capture-pane %s: %v", pane, err)
		}
		if strings.Contains(screen, want) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("screen of %s never showed %q", pane, want)
		case <-ticker.C:
		}
	}
}

// awaitInput waits until file holds at least len(want) bytes, then requires it to equal want exactly.
func awaitInput(t *testing.T, file, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), screenWait)
	defer cancel()
	ticker := time.NewTicker(screenPoll)
	defer ticker.Stop()
	for {
		got, err := os.ReadFile(file)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("read input: %v", err)
		}
		if len(got) >= len(want) {
			if string(got) != want {
				t.Fatalf("pane received %q, want %q", got, want)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pane received %q, want %q", got, want)
		case <-ticker.C:
		}
	}
}

func TestServePaneText(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane, file := startInputPane(t, tmux, "s1", synthScreen)
	rec := postJSON(t, newHandler(t, tmux), paneRoute(pane, "text"), jsonBody(t, map[string]any{"text": synthText}))
	assertStatus(t, rec, http.StatusNoContent)
	awaitInput(t, file, "\x1b[200~"+synthText+"\x1b[201~\r")
}

func TestServePaneKeys(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane, file := startInputPane(t, tmux, "s1", synthQuestion)
	other, otherFile := startInputPane(t, tmux, "s2", synthScreen)
	if err := tmux.SetPaneOption(t.Context(), pane, protocol.OptionQuestion, synthQuestion); err != nil {
		t.Fatalf("SetPaneOption: %v", err)
	}
	handler := newHandler(t, tmux)
	path := paneRoute(pane, "keys")

	// A question hash that does not match must answer 409, and its key must never reach the pane.
	refused := jsonBody(t, map[string]any{
		"keys":            []string{"x"},
		"expect_question": protocol.QuestionHash("a different question"),
	})
	assertStatus(t, postJSON(t, handler, path, refused), http.StatusConflict)

	// Unguarded keys arrive.
	unguarded := jsonBody(t, map[string]any{"keys": []string{"a"}})
	assertStatus(t, postJSON(t, handler, path, unguarded), http.StatusNoContent)

	// A matching question hash with the question visible on the pane delivers.
	guarded := jsonBody(t, map[string]any{
		"keys":            []string{"b"},
		"expect_question": protocol.QuestionHash(synthQuestion),
	})
	assertStatus(t, postJSON(t, handler, path, guarded), http.StatusNoContent)

	// A key spelled like a flag is typed literally into the target pane, and the other pane gets nothing.
	literal := jsonBody(t, map[string]any{"keys": []string{"--pane=" + other}})
	assertStatus(t, postJSON(t, handler, path, literal), http.StatusNoContent)
	awaitInput(t, file, "ab--pane="+other)

	toOther := jsonBody(t, map[string]any{"keys": []string{"z"}})
	assertStatus(t, postJSON(t, handler, paneRoute(other, "keys"), toOther), http.StatusNoContent)
	awaitInput(t, otherFile, "z")
}

func TestServePaneSeen(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane, _ := startInputPane(t, tmux, "s1", synthScreen)
	before := time.Now().Unix()
	rec := postJSON(t, newHandler(t, tmux), paneRoute(pane, "seen"), "{}")
	assertStatus(t, rec, http.StatusNoContent)
	after := time.Now().Unix()

	value, ok, err := tmux.PaneOption(t.Context(), pane, protocol.OptionSeen)
	if err != nil {
		t.Fatalf("PaneOption: %v", err)
	}
	if !ok {
		t.Fatalf("%s is unset on %s, want the current time", protocol.OptionSeen, pane)
	}
	seen, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		t.Fatalf("%s = %q: %v", protocol.OptionSeen, value, err)
	}
	if seen < before || seen > after {
		t.Fatalf("%s = %d, want a time between %d and %d", protocol.OptionSeen, seen, before, after)
	}
}

func TestServeWriteRoutesRejectMalformedID(t *testing.T) {
	t.Parallel()

	handler := newHandler(t, newTmuxClient(t))
	for _, route := range writeRoutes() {
		for _, id := range []string{"abc", "%", "%x", "1", "%1x", "%-1", "%%1"} {
			t.Run(route.name+" "+id, func(t *testing.T) {
				t.Parallel()
				rec := postJSON(t, handler, paneRoute(id, route.name), route.body)
				assertStatus(t, rec, http.StatusBadRequest)
			})
		}
	}
}

func TestServeWriteRoutesAbsentPane(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	startSession(t, tmux, "s1", longCommand)
	handler := newHandler(t, tmux)
	for _, route := range writeRoutes() {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			rec := postJSON(t, handler, paneRoute("%999999", route.name), route.body)
			assertStatus(t, rec, http.StatusNotFound)
			// The mux answers unknown routes with its own 404 body, so the pane's answer is the one checked.
			if body := strings.TrimSpace(rec.Body.String()); body != "pane not found" {
				t.Fatalf("body = %q, want %q", body, "pane not found")
			}
		})
	}
}

func TestServePaneTextRejectsBadBody(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane, file := startInputPane(t, tmux, "s1", synthScreen)
	handler := newHandler(t, tmux)
	path := paneRoute(pane, "text")
	bodies := []string{`[]`, `null`, `not json`, `{}`, `{"text":1}`, `{"text":null}`, `{"text":["a"]}`}
	for _, body := range bodies {
		assertStatus(t, postJSON(t, handler, path, body), http.StatusBadRequest)
	}

	// Nothing from the rejected bodies reached the pane, so the next delivery is exactly its own text.
	assertStatus(t, postJSON(t, handler, path, `{"text":"ok"}`), http.StatusNoContent)
	awaitInput(t, file, "\x1b[200~ok\x1b[201~\r")
}

func TestServePaneKeysRejectsBadBody(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane, file := startInputPane(t, tmux, "s1", synthScreen)
	handler := newHandler(t, tmux)
	path := paneRoute(pane, "keys")
	bodies := []string{
		`[]`,
		`null`,
		`not json`,
		`{}`,
		`{"keys":null}`,
		`{"keys":[]}`,
		`{"keys":"a"}`,
		`{"keys":[1]}`,
		`{"keys":[null]}`,
		`{"keys":["a"],"expect_question":1}`,
		`{"keys":["a"],"expect_question":null}`,
	}
	for _, body := range bodies {
		assertStatus(t, postJSON(t, handler, path, body), http.StatusBadRequest)
	}

	// Nothing from the rejected bodies reached the pane, so the next delivery is exactly its own key.
	assertStatus(t, postJSON(t, handler, path, `{"keys":["ok"]}`), http.StatusNoContent)
	awaitInput(t, file, "ok")
}

func TestServeWriteRoutesRequireClientHeader(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane, file := startInputPane(t, tmux, "s1", synthScreen)
	handler := newHandler(t, tmux)
	header := writeHeader()
	header.Del(server.HeaderClient)
	for _, route := range writeRoutes() {
		rec := serve(t, handler, http.MethodPost, paneRoute(pane, route.name), header, route.body)
		assertStatus(t, rec, http.StatusForbidden)
	}

	// Nothing from the rejected writes reached the pane or set the seen option.
	if _, ok, err := tmux.PaneOption(t.Context(), pane, protocol.OptionSeen); err != nil || ok {
		t.Fatalf("%s after rejected seen: set %t, err %v; want unset", protocol.OptionSeen, ok, err)
	}
	assertStatus(t, postJSON(t, handler, paneRoute(pane, "keys"), `{"keys":["ok"]}`), http.StatusNoContent)
	awaitInput(t, file, "ok")
}

// listedPanes returns every pane tmux lists on the server of tmux.
func listedPanes(t *testing.T, tmux *tmuxctl.Client) []protocol.Pane {
	t.Helper()
	panes, err := tmux.ListPanes(t.Context())
	if err != nil {
		t.Fatalf("ListPanes: %v", err)
	}
	return panes
}

// findPane returns the pane with id from panes, and fails the test when there is none.
func findPane(t *testing.T, panes []protocol.Pane, id string) protocol.Pane {
	t.Helper()
	for _, pane := range panes {
		if pane.ID == id {
			return pane
		}
	}
	t.Fatalf("pane %s is not listed", id)
	return protocol.Pane{}
}

// paneFormat returns the tmux format expanded for pane, without its trailing newline.
func paneFormat(t *testing.T, tmux *tmuxctl.Client, pane, format string) string {
	t.Helper()
	out, err := tmux.Run(t.Context(), "display-message", "-p", "-t", pane, format)
	if err != nil {
		t.Fatalf("display-message %s on %s: %v", format, pane, err)
	}
	return strings.TrimSpace(out)
}

// waitForFormat polls the tmux format on pane until it expands to want.
func waitForFormat(t *testing.T, tmux *tmuxctl.Client, pane, format, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), screenWait)
	defer cancel()
	ticker := time.NewTicker(screenPoll)
	defer ticker.Stop()
	got := paneFormat(t, tmux, pane, format)
	for got != want {
		select {
		case <-ctx.Done():
			t.Fatalf("%s of %s = %q, want %q", format, pane, got, want)
		case <-ticker.C:
		}
		got = paneFormat(t, tmux, pane, format)
	}
}

// waitForFile waits until path exists.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), screenWait)
	defer cancel()
	ticker := time.NewTicker(screenPoll)
	defer ticker.Stop()
	for {
		_, err := os.Stat(path)
		if err == nil {
			return
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("stat %s: %v", path, err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s never appeared", path)
		case <-ticker.C:
		}
	}
}

// useStandIns makes tmux run each new pane's command in a shell that has dir first on PATH, so a claude
// command finds the stand-in in dir. tmux 3.8 does not pass a PATH set with set-environment -g to panes, and
// the default shell's own startup files would reorder PATH anyway, so the default shell is a synthetic wrapper
// that prepends dir and then runs /bin/sh.
func useStandIns(t *testing.T, tmux *tmuxctl.Client, dir string) {
	t.Helper()
	wrapper := filepath.Join(t.TempDir(), "shell")
	script := "#!/bin/sh\nPATH=" + dir + ":$PATH\nexport PATH\nexec /bin/sh \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatalf("write shell wrapper: %v", err)
	}
	if _, err := tmux.Run(t.Context(), "set-option", "-g", "default-shell", wrapper); err != nil {
		t.Fatalf("set default-shell: %v", err)
	}
}

// writeClaudeStandIn writes an executable named claude into dir. It creates marker, then execs a long
// sleep, so the pane keeps running until its server is killed.
func writeClaudeStandIn(t *testing.T, dir, marker string) {
	t.Helper()
	script := "#!/bin/sh\n: > " + marker + "\nexec sleep 300\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o700); err != nil {
		t.Fatalf("write claude stand-in: %v", err)
	}
}

// newPaneID decodes the answer to POST /v1/panes and returns the pane ID it names.
func newPaneID(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	id, ok := body["pane"]
	if !ok || len(body) != 1 || id == "" {
		t.Fatalf("body = %q, want exactly one pane field holding an ID", rec.Body.String())
	}
	return id
}

// newWindowPath is the path of the route that opens a window beside an existing pane.
const newWindowPath = "/v1/panes"

func TestServeNewWindow(t *testing.T) {
	t.Parallel()

	t.Run("shell opens a window in the source session at the source directory", func(t *testing.T) {
		t.Parallel()

		tmux := newTmuxServer(t)
		startSession(t, tmux, "src", longCommand)
		source := paneID(t, tmux, "src")
		sourcePane := findPane(t, listedPanes(t, tmux), source)
		rec := postJSON(t, newHandler(t, tmux), newWindowPath,
			jsonBody(t, map[string]any{"pane": source, "kind": "shell"}))
		assertStatus(t, rec, http.StatusCreated)
		assertJSON(t, rec)
		created := newPaneID(t, rec)

		panes := listedPanes(t, tmux)
		if len(panes) != 2 {
			t.Fatalf("%d panes listed, want the source and the new pane", len(panes))
		}
		pane := findPane(t, panes, created)
		if pane.Session != "src" {
			t.Fatalf("new pane is in session %q, want %q", pane.Session, "src")
		}
		if pane.WindowIndex == sourcePane.WindowIndex {
			t.Fatalf("new pane is in window %d, the source's window; want a new window", pane.WindowIndex)
		}
		waitForFormat(t, tmux, created, "#{pane_current_path}", sourcePane.Cwd)
	})

	t.Run("a source directory containing # is opened literally", func(t *testing.T) {
		t.Parallel()

		// tmux expands formats in new-window -c, so an unescaped #S here would name the session instead.
		dir := filepath.Join(t.TempDir(), "c#Sharp")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		tmux := newTmuxServer(t)
		// new-session -c expands formats too, so the fixture doubles each # to start the source pane in dir.
		if _, err := tmux.Run(t.Context(), "-f", noConfig, "new-session", "-d", "-s", "src", "-c",
			strings.ReplaceAll(dir, "#", "##"), longCommand); err != nil {
			t.Fatalf("new-session in %s: %v", dir, err)
		}
		source := paneID(t, tmux, "src")
		waitForFormat(t, tmux, source, "#{pane_current_path}", dir)
		rec := postJSON(t, newHandler(t, tmux), newWindowPath,
			jsonBody(t, map[string]any{"pane": source, "kind": "shell"}))
		assertStatus(t, rec, http.StatusCreated)
		waitForFormat(t, tmux, newPaneID(t, rec), "#{pane_current_path}", dir)
	})

	t.Run("claude runs the claude stand-in in a new window", func(t *testing.T) {
		t.Parallel()

		tmux := newTmuxServer(t)
		startSession(t, tmux, "src", longCommand)
		source := paneID(t, tmux, "src")
		dir := t.TempDir()
		marker := filepath.Join(dir, "started")
		writeClaudeStandIn(t, dir, marker)
		useStandIns(t, tmux, dir)

		rec := postJSON(t, newHandler(t, tmux), newWindowPath,
			jsonBody(t, map[string]any{"pane": source, "kind": "claude"}))
		assertStatus(t, rec, http.StatusCreated)
		assertJSON(t, rec)
		created := newPaneID(t, rec)

		waitForFile(t, marker)
		if got := paneFormat(t, tmux, created, "#{pane_start_command}"); got != "claude" {
			t.Fatalf("pane_start_command = %q, want %q", got, "claude")
		}
		pane := findPane(t, listedPanes(t, tmux), created)
		if pane.Session != "src" {
			t.Fatalf("new pane is in session %q, want %q", pane.Session, "src")
		}
	})

	t.Run("window lands in the source session when another session shares its name prefix", func(t *testing.T) {
		t.Parallel()

		tmux := newTmuxServer(t)
		startSession(t, tmux, "orrery-team", longCommand)
		startSession(t, tmux, "orrery", longCommand)
		source := paneID(t, tmux, "orrery")
		rec := postJSON(t, newHandler(t, tmux), newWindowPath,
			jsonBody(t, map[string]any{"pane": source, "kind": "shell"}))
		assertStatus(t, rec, http.StatusCreated)
		created := newPaneID(t, rec)

		panes := listedPanes(t, tmux)
		if pane := findPane(t, panes, created); pane.Session != "orrery" {
			t.Fatalf("new pane is in session %q, want %q", pane.Session, "orrery")
		}
		sibling := 0
		for _, pane := range panes {
			if pane.Session == "orrery-team" {
				sibling++
			}
		}
		if sibling != 1 {
			t.Fatalf("orrery-team has %d panes, want the one it started with", sibling)
		}
	})

	t.Run("bad body answers 400 and creates no window", func(t *testing.T) {
		t.Parallel()

		tmux := newTmuxServer(t)
		startSession(t, tmux, "src", longCommand)
		source := paneID(t, tmux, "src")
		handler := newHandler(t, tmux)
		bodies := []string{
			`[]`,
			`null`,
			`not json`,
			`{}`,
			`{"kind":"shell"}`,
			jsonBody(t, map[string]any{"pane": 1, "kind": "shell"}),
			jsonBody(t, map[string]any{"pane": nil, "kind": "shell"}),
			jsonBody(t, map[string]any{"pane": source}),
			jsonBody(t, map[string]any{"pane": source, "kind": "bash"}),
			jsonBody(t, map[string]any{"pane": source, "kind": "Shell"}),
			jsonBody(t, map[string]any{"pane": source, "kind": "shell "}),
			jsonBody(t, map[string]any{"pane": source, "kind": 1}),
		}
		for _, body := range bodies {
			assertStatus(t, postJSON(t, handler, newWindowPath, body), http.StatusBadRequest)
		}
		if n := len(listedPanes(t, tmux)); n != 1 {
			t.Fatalf("%d panes listed after rejected bodies, want 1", n)
		}
	})

	t.Run("malformed pane answers 400 and creates no window", func(t *testing.T) {
		t.Parallel()

		tmux := newTmuxServer(t)
		startSession(t, tmux, "src", longCommand)
		handler := newHandler(t, tmux)
		for _, id := range []string{"abc", "%", "%x", "1", "%1x", "%-1", "%%1"} {
			body := jsonBody(t, map[string]any{"pane": id, "kind": "shell"})
			assertStatus(t, postJSON(t, handler, newWindowPath, body), http.StatusBadRequest)
		}
		if n := len(listedPanes(t, tmux)); n != 1 {
			t.Fatalf("%d panes listed after malformed panes, want 1", n)
		}
	})

	t.Run("absent pane answers 404 and creates no window", func(t *testing.T) {
		t.Parallel()

		tmux := newTmuxServer(t)
		startSession(t, tmux, "src", longCommand)
		body := jsonBody(t, map[string]any{"pane": "%999999", "kind": "shell"})
		rec := postJSON(t, newHandler(t, tmux), newWindowPath, body)
		assertStatus(t, rec, http.StatusNotFound)
		if got := strings.TrimSpace(rec.Body.String()); got != "pane not found" {
			t.Fatalf("body = %q, want %q", got, "pane not found")
		}
		if n := len(listedPanes(t, tmux)); n != 1 {
			t.Fatalf("%d panes listed after an absent pane, want 1", n)
		}
	})

	t.Run("missing client header answers 403 and creates no window", func(t *testing.T) {
		t.Parallel()

		tmux := newTmuxServer(t)
		startSession(t, tmux, "src", longCommand)
		source := paneID(t, tmux, "src")
		header := writeHeader()
		header.Del(server.HeaderClient)
		body := jsonBody(t, map[string]any{"pane": source, "kind": "shell"})
		assertStatus(t, serve(t, newHandler(t, tmux), http.MethodPost, newWindowPath, header, body),
			http.StatusForbidden)
		if n := len(listedPanes(t, tmux)); n != 1 {
			t.Fatalf("%d panes listed after an unguarded write, want 1", n)
		}
	})
}

// writeHostStatusScript writes an executable shell script under t.TempDir() that prints output and exits with status.
func writeHostStatusScript(t *testing.T, output string, status int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "host-status")
	body := fmt.Sprintf("#!/bin/sh\ncat <<'EOF'\n%s\nEOF\nexit %d\n", output, status)
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write host status script: %v", err)
	}
	return path
}

// newHostHandler builds the guarded handler that admits only allowedLogin and runs host status through runner.
func newHostHandler(t *testing.T, runner *hoststatus.Runner) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:     []string{allowedLogin},
		Tmux:       newTmuxClient(t),
		Push:       newPushStore(t),
		HostStatus: runner,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// hostPath is the host status route.
const hostPath = "/v1/host"

// TestServeHost is not parallel: each case execs a script it has just written, and parallel tests can race that exec.
func TestServeHost(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("Hostname: %v", err)
	}

	t.Run("answers the host and the command's stats", func(t *testing.T) {
		output := `[{"label":"cpu","value":"12%","percent":12.5,"level":"ok"},` +
			`{"label":"uptime","value":"3d","level":"warn"}]`
		script := writeHostStatusScript(t, output, 0)
		rec := get(t, newHostHandler(t, hoststatus.New(script, time.Now)), hostPath, allowedLogin)
		assertStatus(t, rec, http.StatusOK)
		assertJSON(t, rec)
		assertNoCORS(t, rec)
		var got any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
		want := map[string]any{
			"host": host,
			"stats": []any{
				map[string]any{"label": "cpu", "value": "12%", "percent": 12.5, "level": "ok"},
				map[string]any{"label": "uptime", "value": "3d", "level": "warn"},
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("body = %#v, want %#v", got, want)
		}
	})

	t.Run("answers 404 when serve has no host status command", func(t *testing.T) {
		assertStatus(t, get(t, newHandler(t, newTmuxClient(t)), hostPath, allowedLogin), http.StatusNotFound)
	})

	t.Run("answers 502 when the command exits non-zero", func(t *testing.T) {
		script := writeHostStatusScript(t, "", 3)
		rec := get(t, newHostHandler(t, hoststatus.New(script, time.Now)), hostPath, allowedLogin)
		assertStatus(t, rec, http.StatusBadGateway)
	})

	t.Run("answers 502 when the command prints invalid stats", func(t *testing.T) {
		script := writeHostStatusScript(t, "not json", 0)
		rec := get(t, newHostHandler(t, hoststatus.New(script, time.Now)), hostPath, allowedLogin)
		assertStatus(t, rec, http.StatusBadGateway)
	})

	t.Run("answers 403 to a login the guard refuses", func(t *testing.T) {
		script := writeHostStatusScript(t, "[]", 0)
		rec := get(t, newHostHandler(t, hoststatus.New(script, time.Now)), hostPath, otherLogin)
		assertStatus(t, rec, http.StatusForbidden)
	})
}

// conversationRoute is the conversation route's path segment after the pane ID.
const conversationRoute = "/conversation"

// synthOrigin is the synthetic opening prompt of the conversation fixtures.
const synthOrigin = "synthetic opening prompt for the conversation route"

// conversationPath returns the conversation route of pane, with rawQuery as its query when it is not "".
func conversationPath(pane, rawQuery string) string {
	path := "/v1/panes/" + url.PathEscape(pane) + conversationRoute
	if rawQuery != "" {
		path += "?" + rawQuery
	}
	return path
}

// writeConversation writes a synthetic transcript to a new file in t.TempDir and returns its path. Its first
// line is a prompt of synthOrigin, and each of its replies lines is a reply "synthetic reply N" for N from 1.
func writeConversation(t *testing.T, replies int) string {
	t.Helper()
	lines := []string{`{"type":"user","message":{"role":"user","content":"` + synthOrigin + `"}}`}
	for n := 1; n <= replies; n++ {
		lines = append(lines, fmt.Sprintf(
			`{"type":"assistant","message":{"content":[{"type":"text","text":"synthetic reply %d"}]}}`, n))
	}
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

// liveOptions returns the pane options of a live Claude session whose transcript is at path.
func liveOptions(path string) map[string]string {
	return map[string]string{protocol.OptionState: string(protocol.StateWorking), protocol.OptionTranscript: path}
}

// conversationPane starts a session on a pane running longCommand, sets each of options on that pane, and
// returns the pane ID.
func conversationPane(t *testing.T, tmux *tmuxctl.Client, options map[string]string) string {
	t.Helper()
	startSession(t, tmux, "s1", longCommand)
	pane := paneID(t, tmux, "s1")
	for name, value := range options {
		if err := tmux.SetPaneOption(t.Context(), pane, name, value); err != nil {
			t.Fatalf("SetPaneOption(%s, %s): %v", pane, name, err)
		}
	}
	return pane
}

func TestServePaneConversation(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	path := writeConversation(t, 3)
	pane := conversationPane(t, tmux, liveOptions(path))
	handler := newHandler(t, tmux)

	rec := get(t, handler, conversationPath(pane, ""), allowedLogin)
	assertStatus(t, rec, http.StatusOK)
	assertJSON(t, rec)
	var got conversation.Conversation
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	want := conversation.Conversation{
		Origin: synthOrigin,
		Items: []conversation.Item{
			{Kind: "prompt", Text: synthOrigin},
			{Kind: "reply", Text: "synthetic reply 1"},
			{Kind: "reply", Text: "synthetic reply 2"},
			{Kind: "reply", Text: "synthetic reply 3"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("body = %+v, want %+v", got, want)
	}

	other := writeConversation(t, 1)
	rec = get(t, handler, conversationPath(pane, "path="+url.QueryEscape(other)), allowedLogin)
	assertStatus(t, rec, http.StatusOK)
	if rec.Body.String() != serveBody(t, handler, conversationPath(pane, "")) {
		t.Fatalf("a path query changed the body to %q", rec.Body.String())
	}
}

// serveBody returns the body of a 200 GET of path through handler, and fails the test on any other status.
func serveBody(t *testing.T, handler http.Handler, path string) string {
	t.Helper()
	rec := get(t, handler, path, allowedLogin)
	assertStatus(t, rec, http.StatusOK)
	return rec.Body.String()
}

func TestServePaneConversationLimit(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane := conversationPane(t, tmux, liveOptions(writeConversation(t, 40)))
	handler := newHandler(t, tmux)
	cases := []struct {
		name      string
		query     string
		wantLen   int
		wantFirst string
		wantLast  string
	}{
		{
			name:      "default is 30",
			query:     "",
			wantLen:   30,
			wantFirst: "synthetic reply 11",
			wantLast:  "synthetic reply 40",
		},
		{
			name:      "limit 5",
			query:     "limit=5",
			wantLen:   5,
			wantFirst: "synthetic reply 36",
			wantLast:  "synthetic reply 40",
		},
		{
			name:      "limit 1",
			query:     "limit=1",
			wantLen:   1,
			wantFirst: "synthetic reply 40",
			wantLast:  "synthetic reply 40",
		},
		{
			name:      "limit 100 holds all 41 items",
			query:     "limit=100",
			wantLen:   41,
			wantFirst: synthOrigin,
			wantLast:  "synthetic reply 40",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := get(t, handler, conversationPath(pane, tc.query), allowedLogin)
			assertStatus(t, rec, http.StatusOK)
			var got conversation.Conversation
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode %q: %v", rec.Body.String(), err)
			}
			if len(got.Items) != tc.wantLen {
				t.Fatalf("%d items, want %d", len(got.Items), tc.wantLen)
			}
			if first := got.Items[0].Text; first != tc.wantFirst {
				t.Fatalf("first item = %q, want %q", first, tc.wantFirst)
			}
			if last := got.Items[len(got.Items)-1].Text; last != tc.wantLast {
				t.Fatalf("last item = %q, want %q", last, tc.wantLast)
			}
		})
	}
}

func TestServePaneConversationRejectsBadLimit(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane := conversationPane(t, tmux, liveOptions(writeConversation(t, 3)))
	handler := newHandler(t, tmux)
	queries := []string{
		"limit=0", "limit=101", "limit=-1", "limit=abc", "limit=", "limit=5&limit=6", "limit=+5", "limit=1.5",
	}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			rec := get(t, handler, conversationPath(pane, query), allowedLogin)
			assertStatus(t, rec, http.StatusBadRequest)
		})
	}
}

func TestServePaneConversationRejectsMalformedID(t *testing.T) {
	t.Parallel()

	handler := newHandler(t, newTmuxClient(t))
	for _, id := range []string{"abc", "%", "%x", "1", "%1x", "%-1", "%%1"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			rec := get(t, handler, conversationPath(id, ""), allowedLogin)
			assertStatus(t, rec, http.StatusBadRequest)
		})
	}
}

func TestServePaneConversationAbsentPane(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	startSession(t, tmux, "s1", longCommand)
	rec := get(t, newHandler(t, tmux), conversationPath("%999999", ""), allowedLogin)
	assertStatus(t, rec, http.StatusNotFound)
}

func TestServePaneConversationWithoutTranscript(t *testing.T) {
	t.Parallel()

	path := writeConversation(t, 1)
	cases := []struct {
		name    string
		options map[string]string
	}{
		{
			name:    "no live session",
			options: map[string]string{protocol.OptionTranscript: path},
		},
		{
			name:    "no transcript option",
			options: map[string]string{protocol.OptionState: string(protocol.StateWorking)},
		},
		{
			name: "empty transcript option",
			options: map[string]string{
				protocol.OptionState:      string(protocol.StateWorking),
				protocol.OptionTranscript: "",
			},
		},
		{
			name: "transcript file missing",
			options: map[string]string{
				protocol.OptionState:      string(protocol.StateWorking),
				protocol.OptionTranscript: filepath.Join(t.TempDir(), "missing.jsonl"),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmux := newTmuxServer(t)
			pane := conversationPane(t, tmux, tc.options)
			rec := get(t, newHandler(t, tmux), conversationPath(pane, ""), allowedLogin)
			assertStatus(t, rec, http.StatusNotFound)
		})
	}
}

func TestServePaneConversationRequiresListedLogin(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane := conversationPane(t, tmux, liveOptions(writeConversation(t, 1)))
	handler := newHandler(t, tmux)
	for _, login := range []string{"", otherLogin} {
		t.Run(fmt.Sprintf("login %q", login), func(t *testing.T) {
			t.Parallel()
			rec := get(t, handler, conversationPath(pane, ""), login)
			assertStatus(t, rec, http.StatusForbidden)
			assertNoCORS(t, rec)
		})
	}
}
