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
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/cli"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/hoststatus"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
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
	_, tmux := newNamedTmuxClient(t)
	return tmux
}

// newNamedTmuxClient returns the name of a unique private tmux server and a client for it. The server is not started.
func newNamedTmuxClient(t *testing.T) (string, *tmuxctl.Client) {
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
	return name, tmux
}

// newTmuxServer returns a client for a unique private tmux server and kills that server at cleanup.
func newTmuxServer(t *testing.T) *tmuxctl.Client {
	t.Helper()
	name, tmux := newNamedTmuxClient(t)
	t.Cleanup(func() {
		if err := tmuxtest.Stop(t, tmuxctl.Server{Name: name}); err != nil {
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

// inputReady is printed by an input pane once its line discipline is off, so the test may type into it.
const inputReady = "input ready"

// synthText is the synthetic text the text route pastes.
const synthText = "synthetic text"

// writeHeader returns the headers of a request the guard admits for a write.
func writeHeader() http.Header {
	header := http.Header{}
	header.Set(server.HeaderLogin, allowedLogin)
	header.Set("Content-Type", jsonType)
	header.Set(server.HeaderClient, client)
	return header
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

// awaitInput waits until file holds as many bytes as the options, then requires it to equal one of them
// exactly. The options must share one length.
func awaitInput(t *testing.T, file string, options ...string) {
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
		if len(got) >= len(options[0]) {
			if !slices.Contains(options, string(got)) {
				t.Fatalf("pane received %q, want one of %q", got, options)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pane received %q, want one of %q", got, options)
		case <-ticker.C:
		}
	}
}

// synthPrompt is the synthetic text that follows the prompt mark in a typing pane's input box.
const synthPrompt = "synthetic prompt"

// typingWidth is the column width of a typing pane, which the input box rules span.
const typingWidth = 80

// typedLineBytes and typedLines size the text of each concurrent send: each line takes several typed
// pieces, so two sends interleave unless the text route serializes them.
const (
	typedLineBytes = 900
	typedLines     = 4
)

// typedLinesOf returns typedLines lines of typedLineBytes bytes each, all of letter, joined by line feeds.
func typedLinesOf(letter string) string {
	line := strings.Repeat(letter, typedLineBytes)
	return strings.Repeat(line+"\n", typedLines-1) + line
}

// startTypingPane starts a pane whose screen is an idle input box typingWidth columns wide, sets its orrery
// state to done, and returns its pane ID and input file.
func startTypingPane(t *testing.T, tmux *tmuxctl.Client, session string) (string, string) {
	t.Helper()
	rule := strings.Repeat("─", typingWidth)
	pane, file := startInputPane(t, tmux, session, rule+"\n❯ "+synthPrompt+"\n"+rule)
	out, err := tmux.Run(t.Context(), "display-message", "-p", "-t", pane, "#{pane_width}")
	if err != nil {
		t.Fatalf("display-message %s: %v", pane, err)
	}
	if got := strings.TrimSpace(out); got != strconv.Itoa(typingWidth) {
		t.Fatalf("pane width = %s, want %d", got, typingWidth)
	}
	if err := tmux.SetPaneOption(t.Context(), pane, protocol.OptionState, string(protocol.StateDone)); err != nil {
		t.Fatalf("SetPaneOption: %v", err)
	}
	return pane, file
}

// seenOption returns the pane's @orrery_seen time, failing the test when it is unset.
func seenOption(t *testing.T, tmux *tmuxctl.Client, pane string) int64 {
	t.Helper()
	value, ok, err := tmux.PaneOption(t.Context(), pane, protocol.OptionSeen)
	if err != nil {
		t.Fatalf("PaneOption: %v", err)
	}
	if !ok {
		t.Fatalf("%s is unset on %s", protocol.OptionSeen, pane)
	}
	seen, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		t.Fatalf("%s = %q: %v", protocol.OptionSeen, value, err)
	}
	return seen
}

// seenCall is one report a seenRecorder received.
type seenCall struct {
	pane string
	seen int64
}

// seenRecorder returns a reporter that appends each call to calls and answers err.
func seenRecorder(calls *[]seenCall, err error) cli.SeenReporter {
	return func(pane string, seen int64) error {
		*calls = append(*calls, seenCall{pane: pane, seen: seen})
		return err
	}
}

// newSeenHandler builds the guarded handler that admits only allowedLogin, routes to tmux, and reports seen
// marks to report.
func newSeenHandler(t *testing.T, tmux *tmuxctl.Client, report cli.SeenReporter) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:     []string{allowedLogin},
		Tmux:       tmux,
		Push:       newPushStore(t),
		ReportSeen: report,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
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

// synthOrigin is the synthetic opening prompt of the conversation fixtures.
const synthOrigin = "synthetic opening prompt for the conversation route"

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

// synthGoal is the synthetic goal condition of the goal fixtures.
const synthGoal = "synthetic goal condition"

// goalRecord returns a goal_status attachment line for condition, met or not met, with its newline.
func goalRecord(condition string, met bool) string {
	return fmt.Sprintf(
		`{"type":"attachment","attachment":{"type":"goal_status","condition":%q,"met":%t,"sentinel":true}}`+"\n",
		condition, met)
}

// appendTranscript appends text to the transcript at path.
func appendTranscript(t *testing.T, path, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	if _, err := file.WriteString(text); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close transcript: %v", err)
	}
}

// overwriteTranscript replaces the first old in the transcript at path with replacement of the same length, in place.
func overwriteTranscript(t *testing.T, path, old, replacement string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	at := bytes.Index(data, []byte(old))
	if at < 0 || len(old) != len(replacement) {
		t.Fatalf("cannot overwrite %q with %q in place", old, replacement)
	}
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	if _, err := file.WriteAt([]byte(replacement), int64(at)); err != nil {
		t.Fatalf("overwrite transcript: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close transcript: %v", err)
	}
}

// synthQueued is the synthetic text of the queued message of the waiting fixtures.
const synthQueued = "synthetic queued message"

// queueLine returns a queue-operation line with operation, text and at as its RFC 3339 timestamp, with its newline.
// A reason of "" leaves the reason out.
func queueLine(operation, text, at, reason string) string {
	line := fmt.Sprintf(`{"type":"queue-operation","operation":%q,"timestamp":%q,"sessionId":"s","content":%q`,
		operation, at, text)
	if reason != "" {
		line += fmt.Sprintf(`,"reason":%q`, reason)
	}
	return line + "}\n"
}

// connectPaneObject returns the object `orrery list --json` writes for pane, decoded as cli.List's output is, so a
// Panes.List answer compares to cli.List field for field.
func connectPaneObject(t *testing.T, pane *orreryv1.Pane) map[string]any {
	t.Helper()
	fields := map[string]any{
		"id":             pane.GetId(),
		"host":           pane.GetHost(),
		"session":        pane.GetSession(),
		"window":         pane.GetWindow(),
		"pane":           pane.GetPane(),
		"cwd":            pane.GetCwd(),
		"command":        pane.GetCommand(),
		"state":          pane.GetState(),
		"summary":        pane.GetSummary(),
		"label":          pane.GetLabel(),
		"changed":        pane.GetChanged(),
		"seen":           pane.GetSeen(),
		"question":       pane.GetQuestion(),
		"context":        pane.GetContext(),
		"activity":       pane.GetActivity(),
		"tool":           pane.GetTool(),
		"claude_session": pane.GetClaudeSession(),
	}
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode pane %s: %v", pane.GetId(), err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("decode pane %s: %v", pane.GetId(), err)
	}
	return object
}

// listUntilAgreed reads Panes.List and cli.List until the two hold the same objects, and returns the Pane messages
// with the cli.List objects. A pane's current command changes while its shell starts, so the two reads can differ
// until it settles.
func listUntilAgreed(t *testing.T, tmux *tmuxctl.Client, handler http.Handler) ([]*orreryv1.Pane, []map[string]any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), screenWait)
	defer cancel()
	ticker := time.NewTicker(screenPoll)
	defer ticker.Stop()
	client := panesClient(t, handler)
	for {
		got, err := client.List(t.Context(), connect.NewRequest(&orreryv1.PanesListRequest{}))
		if err != nil {
			t.Fatalf("Panes.List: %v", err)
		}
		var list bytes.Buffer
		if err := cli.List(t.Context(), tmux, []string{"--json"}, &list); err != nil {
			t.Fatalf("cli.List: %v", err)
		}
		want := decodeObjects(t, list.Bytes())
		panes := got.Msg.GetPanes()
		objects := make([]map[string]any, 0, len(panes))
		for _, pane := range panes {
			objects = append(objects, connectPaneObject(t, pane))
		}
		if reflect.DeepEqual(objects, want) {
			return panes, want
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Panes.List = %v never agreed with cli.List = %v", objects, want)
		case <-ticker.C:
		}
	}
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

	panes, want := listUntilAgreed(t, tmux, newHandler(t, tmux))
	if len(panes) != 2 || len(want) != 2 {
		t.Fatalf("Panes.List has %d panes and cli.List has %d, want 2 each", len(panes), len(want))
	}

	wantHash := map[string]string{
		questionPane: protocol.QuestionHash(synthQuestion),
		plainPane:    protocol.QuestionHash(""),
	}
	for _, pane := range panes {
		expected, known := wantHash[pane.GetId()]
		if !known {
			t.Fatalf("pane %q is not one of the test panes", pane.GetId())
		}
		if pane.GetQuestionHash() != expected {
			t.Fatalf("pane %s question_hash = %q, want %q", pane.GetId(), pane.GetQuestionHash(), expected)
		}
	}
}

func TestServePanesNoServer(t *testing.T) {
	t.Parallel()

	got, err := panesClient(t, newHandler(t, newTmuxClient(t))).List(t.Context(),
		connect.NewRequest(&orreryv1.PanesListRequest{}))
	if err != nil {
		t.Fatalf("Panes.List: %v", err)
	}
	if panes := got.Msg.GetPanes(); len(panes) != 0 {
		t.Fatalf("Panes.List = %v with no tmux server, want none", panes)
	}
}

// assertCode fails the test unless err is a Connect error with code.
func assertCode(t *testing.T, err error, code connect.Code) {
	t.Helper()
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("error = %v, want a *connect.Error with code %v", err, code)
	}
	if connectErr.Code() != code {
		t.Fatalf("code = %v, want %v", connectErr.Code(), code)
	}
}

// paneScreen returns the screen Panes.Screen answers for req.
func paneScreen(t *testing.T, client orreryv1connect.PanesClient, req *orreryv1.ScreenRequest) string {
	t.Helper()
	got, err := client.Screen(t.Context(), connect.NewRequest(req))
	if err != nil {
		t.Fatalf("Panes.Screen: %v", err)
	}
	return string(got.Msg.GetScreen())
}

// screenUntil polls Panes.Screen for req until the screen contains want, and returns that screen.
func screenUntil(t *testing.T, client orreryv1connect.PanesClient, req *orreryv1.ScreenRequest, want string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), screenWait)
	defer cancel()
	ticker := time.NewTicker(screenPoll)
	defer ticker.Stop()
	for {
		screen := paneScreen(t, client, req)
		if strings.Contains(screen, want) {
			return screen
		}
		select {
		case <-ctx.Done():
			t.Fatalf("screen of %s never showed %q; last screen %q", req.GetPane(), want, screen)
		case <-ticker.C:
		}
	}
}

func TestServePaneScreenANSI(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	startSession(t, tmux, "s1", colorCommand)
	pane := paneID(t, tmux, "s1")
	client := panesClient(t, newHandler(t, tmux))

	screenUntil(t, client, &orreryv1.ScreenRequest{Pane: pane, Ansi: true}, synthRed+synthColor)
	plain := screenUntil(t, client, &orreryv1.ScreenRequest{Pane: pane}, synthColor)
	if strings.Contains(plain, "\x1b") {
		t.Fatalf("plain screen = %q, want no escape sequences", plain)
	}
}

func TestServePaneScreenHistory(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	startSession(t, tmux, "s1", historyCommand())
	pane := paneID(t, tmux, "s1")
	client := panesClient(t, newHandler(t, tmux))
	first := historyLine(1)

	// The visible screen shows the last line the pane prints, so poll until that line is on it.
	plain := screenUntil(t, client, &orreryv1.ScreenRequest{Pane: pane}, historyLine(historyCount))
	if slices.Contains(strings.Split(plain, "\n"), first) {
		t.Fatalf("plain screen = %q, want %q only in scrollback", plain, first)
	}

	withHistory := paneScreen(t, client, &orreryv1.ScreenRequest{Pane: pane, History: historyCount})
	if !slices.Contains(strings.Split(withHistory, "\n"), first) {
		t.Fatalf("history screen = %q, want line %q", withHistory, first)
	}

	// The limit itself is accepted.
	paneScreen(t, client, &orreryv1.ScreenRequest{Pane: pane, History: 2000})

	both := paneScreen(t, client, &orreryv1.ScreenRequest{Pane: pane, Ansi: true, History: historyCount})
	if !strings.Contains(both, synthRed+first) {
		t.Fatalf("ansi and history screen = %q, want %q", both, synthRed+first)
	}
}

func TestServePanesRejectMalformedIDs(t *testing.T) {
	t.Parallel()

	client := panesClient(t, newHandler(t, newTmuxClient(t)))
	calls := []struct {
		name string
		call func(ctx context.Context, id string) error
	}{
		{"Screen", func(ctx context.Context, id string) error {
			_, err := client.Screen(ctx, connect.NewRequest(&orreryv1.ScreenRequest{Pane: id}))
			return err
		}},
		{"Conversation", func(ctx context.Context, id string) error {
			_, err := client.Conversation(ctx, connect.NewRequest(&orreryv1.ConversationRequest{Pane: id}))
			return err
		}},
		{"SendText", func(ctx context.Context, id string) error {
			_, err := client.SendText(ctx, connect.NewRequest(&orreryv1.SendTextRequest{Pane: id, Text: synthText}))
			return err
		}},
		{"SendKeys", func(ctx context.Context, id string) error {
			_, err := client.SendKeys(ctx, connect.NewRequest(&orreryv1.SendKeysRequest{Pane: id, Keys: []string{"k"}}))
			return err
		}},
		{"MarkSeen", func(ctx context.Context, id string) error {
			_, err := client.MarkSeen(ctx, connect.NewRequest(&orreryv1.MarkSeenRequest{Pane: id}))
			return err
		}},
	}
	for _, id := range []string{"abc", "%", "%x", "1", "%1x", "%-1", "%%1"} {
		for _, c := range calls {
			t.Run(c.name+" "+id, func(t *testing.T) {
				t.Parallel()
				assertCode(t, c.call(t.Context(), id), connect.CodeInvalidArgument)
			})
		}
	}
}

func TestServeTmuxRoutesRequireListedLogin(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	startSession(t, tmux, "s1", longCommand)
	pane := paneID(t, tmux, "s1")
	handler := newHandler(t, tmux)
	reads := []struct {
		procedure string
		message   string
	}{
		{orreryv1connect.PanesListProcedure, "{}"},
		{orreryv1connect.PanesScreenProcedure, jsonBody(t, map[string]any{"pane": pane})},
		{orreryv1connect.PanesConversationProcedure, jsonBody(t, map[string]any{"pane": pane})},
	}
	for _, read := range reads {
		for _, login := range []string{"", otherLogin} {
			t.Run(fmt.Sprintf("%s login %q", read.procedure, login), func(t *testing.T) {
				t.Parallel()
				path := read.procedure + "?connect=v1&encoding=json&message=" + url.QueryEscape(read.message)
				rec := get(t, handler, path, login)
				assertStatus(t, rec, http.StatusForbidden)
				assertNoCORS(t, rec)
			})
		}
	}
}

func TestServePanesWriteRequiresClientHeader(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane, file := startInputPane(t, tmux, "s1", synthScreen)
	handler := newHandler(t, tmux)
	header := connectHeader(allowedLogin, "application/json", false)
	writes := []struct {
		procedure string
		body      string
	}{
		{orreryv1connect.PanesSendTextProcedure, jsonBody(t, map[string]any{"pane": pane, "text": synthText})},
		{orreryv1connect.PanesSendKeysProcedure, jsonBody(t, map[string]any{"pane": pane, "keys": []string{"k"}})},
		{orreryv1connect.PanesMarkSeenProcedure, jsonBody(t, map[string]any{"pane": pane})},
	}
	for _, write := range writes {
		rec := serve(t, handler, http.MethodPost, write.procedure, header, write.body)
		assertStatus(t, rec, http.StatusForbidden)
	}

	// Nothing from the rejected writes reached the pane or set the seen option.
	if _, ok, err := tmux.PaneOption(t.Context(), pane, protocol.OptionSeen); err != nil || ok {
		t.Fatalf("%s after rejected writes: set %t, err %v; want unset", protocol.OptionSeen, ok, err)
	}
	client := panesClient(t, handler)
	if _, err := client.SendKeys(t.Context(),
		connect.NewRequest(&orreryv1.SendKeysRequest{Pane: pane, Keys: []string{"ok"}})); err != nil {
		t.Fatalf("Panes.SendKeys: %v", err)
	}
	awaitInput(t, file, "ok")
}

func TestServePaneTextConcurrentSendsDoNotInterleave(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane, file := startTypingPane(t, tmux, "s1")
	client := panesClient(t, newHandler(t, tmux))
	texts := []string{typedLinesOf("a"), typedLinesOf("b")}

	errs := make([]error, len(texts))
	var wg sync.WaitGroup
	for i, text := range texts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = client.SendText(
				t.Context(),
				connect.NewRequest(&orreryv1.SendTextRequest{Pane: pane, Text: text}),
			)
		}()
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			t.Fatalf("Panes.SendText: %v", err)
		}
	}
	first := typedLinesOf("a") + "\r"
	second := typedLinesOf("b") + "\r"
	awaitInput(t, file, first+second, second+first)
}

func TestServePaneKeys(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane, file := startInputPane(t, tmux, "s1", synthQuestion)
	other, otherFile := startInputPane(t, tmux, "s2", synthScreen)
	if err := tmux.SetPaneOption(t.Context(), pane, protocol.OptionQuestion, synthQuestion); err != nil {
		t.Fatalf("SetPaneOption: %v", err)
	}
	client := panesClient(t, newHandler(t, tmux))

	// A question hash that does not match must abort, and its key must never reach the pane.
	mismatch := protocol.QuestionHash("a different question")
	_, err := client.SendKeys(t.Context(),
		connect.NewRequest(&orreryv1.SendKeysRequest{Pane: pane, Keys: []string{"x"}, ExpectQuestion: &mismatch}))
	assertCode(t, err, connect.CodeAborted)

	// Unguarded keys arrive.
	if _, err := client.SendKeys(t.Context(),
		connect.NewRequest(&orreryv1.SendKeysRequest{Pane: pane, Keys: []string{"a"}})); err != nil {
		t.Fatalf("Panes.SendKeys unguarded: %v", err)
	}

	// A matching question hash with the question visible on the pane delivers.
	hash := protocol.QuestionHash(synthQuestion)
	if _, err := client.SendKeys(
		t.Context(),
		connect.NewRequest(
			&orreryv1.SendKeysRequest{Pane: pane, Keys: []string{"b"}, ExpectQuestion: &hash},
		),
	); err != nil {
		t.Fatalf("Panes.SendKeys guarded: %v", err)
	}

	// A key spelled like a flag is typed literally into the target pane, and the other pane gets nothing.
	if _, err := client.SendKeys(t.Context(),
		connect.NewRequest(&orreryv1.SendKeysRequest{Pane: pane, Keys: []string{"--pane=" + other}})); err != nil {
		t.Fatalf("Panes.SendKeys literal: %v", err)
	}
	awaitInput(t, file, "ab--pane="+other)

	if _, err := client.SendKeys(t.Context(),
		connect.NewRequest(&orreryv1.SendKeysRequest{Pane: other, Keys: []string{"z"}})); err != nil {
		t.Fatalf("Panes.SendKeys other: %v", err)
	}
	awaitInput(t, otherFile, "z")
}

func TestServePaneSeenNoTimeSetsNow(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane, _ := startInputPane(t, tmux, "s1", synthScreen)
	client := panesClient(t, newHandler(t, tmux))

	// A stored time in the future does not hold back a mark without read_up_to.
	future := int64(4102444800)
	if _, err := client.MarkSeen(t.Context(),
		connect.NewRequest(&orreryv1.MarkSeenRequest{Pane: pane, ReadUpTo: &future})); err != nil {
		t.Fatalf("Panes.MarkSeen read_up_to: %v", err)
	}
	before := time.Now().Unix()
	if _, err := client.MarkSeen(t.Context(), connect.NewRequest(&orreryv1.MarkSeenRequest{Pane: pane})); err != nil {
		t.Fatalf("Panes.MarkSeen: %v", err)
	}
	after := time.Now().Unix()
	if seen := seenOption(t, tmux, pane); seen < before || seen > after {
		t.Fatalf("%s = %d, want a time between %d and %d", protocol.OptionSeen, seen, before, after)
	}
}

func TestServePaneSeenRejectsBadReadUpTo(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane, _ := startInputPane(t, tmux, "s1", synthScreen)
	client := panesClient(t, newHandler(t, tmux))

	negative := int64(-1)
	_, err := client.MarkSeen(
		t.Context(),
		connect.NewRequest(&orreryv1.MarkSeenRequest{Pane: pane, ReadUpTo: &negative}),
	)
	assertCode(t, err, connect.CodeInvalidArgument)

	// Nothing from the rejected request was stored.
	if _, ok, err := tmux.PaneOption(t.Context(), pane, protocol.OptionSeen); err != nil || ok {
		t.Fatalf("%s set = %t (err %v) after the rejected request, want unset", protocol.OptionSeen, ok, err)
	}

	// Pane checks still answer first, so an absent pane is not found whatever the read_up_to.
	_, err = client.MarkSeen(
		t.Context(),
		connect.NewRequest(&orreryv1.MarkSeenRequest{Pane: "%999999", ReadUpTo: &negative}),
	)
	assertCode(t, err, connect.CodeNotFound)
}

// TestServePaneSeenUnsetZeroReportsNothing marks a pane that holds no seen time with read_up_to 0. The pane still holds
// none, and orreryd refuses a seen of 0, so nothing is reported.
func TestServePaneSeenUnsetZeroReportsNothing(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane, _ := startInputPane(t, tmux, "s1", synthScreen)
	var calls []seenCall
	client := panesClient(t, newSeenHandler(t, tmux, seenRecorder(&calls, nil)))

	zero := int64(0)
	if _, err := client.MarkSeen(t.Context(),
		connect.NewRequest(&orreryv1.MarkSeenRequest{Pane: pane, ReadUpTo: &zero})); err != nil {
		t.Fatalf("Panes.MarkSeen: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("reports = %+v, want none for a pane that holds no seen time", calls)
	}
	if _, ok, err := tmux.PaneOption(t.Context(), pane, protocol.OptionSeen); err != nil || ok {
		t.Fatalf("%s set = %t (err %v) after read_up_to 0, want unset", protocol.OptionSeen, ok, err)
	}
}

func TestServePaneSeenRejectedRequestReportsNothing(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane, _ := startInputPane(t, tmux, "s1", synthScreen)
	var calls []seenCall
	client := panesClient(t, newSeenHandler(t, tmux, seenRecorder(&calls, nil)))

	negative := int64(-1)
	_, err := client.MarkSeen(
		t.Context(),
		connect.NewRequest(&orreryv1.MarkSeenRequest{Pane: pane, ReadUpTo: &negative}),
	)
	assertCode(t, err, connect.CodeInvalidArgument)
	_, err = client.MarkSeen(t.Context(), connect.NewRequest(&orreryv1.MarkSeenRequest{Pane: "abc"}))
	assertCode(t, err, connect.CodeInvalidArgument)
	_, err = client.MarkSeen(t.Context(), connect.NewRequest(&orreryv1.MarkSeenRequest{Pane: "%999999"}))
	assertCode(t, err, connect.CodeNotFound)
	if len(calls) != 0 {
		t.Fatalf("reports = %+v after rejected requests, want none", calls)
	}
}

// conversationGoal returns the goal Panes.Conversation answers for pane through client.
func conversationGoal(t *testing.T, client orreryv1connect.PanesClient, pane string) string {
	t.Helper()
	got, err := client.Conversation(t.Context(), connect.NewRequest(&orreryv1.ConversationRequest{Pane: pane}))
	if err != nil {
		t.Fatalf("Panes.Conversation: %v", err)
	}
	return got.Msg.GetGoal()
}

func TestServePaneConversationGoal(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	path := writeConversation(t, 1)
	appendTranscript(t, path, goalRecord(synthGoal, false))
	pane := conversationPane(t, tmux, liveOptions(path))
	client := panesClient(t, newHandler(t, tmux))

	if got := conversationGoal(t, client, pane); got != synthGoal {
		t.Fatalf("goal = %q, want %q", got, synthGoal)
	}
	// The handler remembers how far it has read, so a second request reads only the appended reply and keeps the
	// goal it read before, though a fresh read would now find the overwritten condition.
	overwriteTranscript(t, path, synthGoal, strings.Repeat("x", len(synthGoal)))
	reply := `{"type":"assistant","message":{"content":[{"type":"text","text":"synthetic reply"}]}}`
	appendTranscript(t, path, reply+"\n")
	if got := conversationGoal(t, client, pane); got != synthGoal {
		t.Fatalf("goal after appended reply = %q, want %q", got, synthGoal)
	}
	appendTranscript(t, path, goalRecord(synthGoal, true))
	if got := conversationGoal(t, client, pane); got != "" {
		t.Fatalf("goal after met record = %q, want empty", got)
	}
}

func TestServePaneConversationNoGoal(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane := conversationPane(t, tmux, liveOptions(writeConversation(t, 1)))
	if got := conversationGoal(t, panesClient(t, newHandler(t, tmux)), pane); got != "" {
		t.Fatalf("goal = %q, want empty", got)
	}
}

// conversationWaiting returns the waiting messages Panes.Conversation answers for pane through client.
func conversationWaiting(t *testing.T, client orreryv1connect.PanesClient, pane string) []*orreryv1.WaitingMessage {
	t.Helper()
	got, err := client.Conversation(t.Context(), connect.NewRequest(&orreryv1.ConversationRequest{Pane: pane}))
	if err != nil {
		t.Fatalf("Panes.Conversation: %v", err)
	}
	return got.Msg.GetWaiting()
}

func TestServePaneConversationWaiting(t *testing.T) {
	t.Parallel()

	const at = "2026-01-02T03:04:05Z"
	tmux := newTmuxServer(t)
	path := writeConversation(t, 1)
	pane := conversationPane(t, tmux, liveOptions(path))
	client := panesClient(t, newHandler(t, tmux))

	if got := conversationWaiting(t, client, pane); len(got) != 0 {
		t.Fatalf("no queued message: waiting = %v, want none", got)
	}
	appendTranscript(t, path, queueLine("enqueue", synthQueued, at, ""))
	got := conversationWaiting(t, client, pane)
	want := &orreryv1.WaitingMessage{Text: synthQueued, Time: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Unix()}
	if len(got) != 1 || !proto.Equal(got[0], want) {
		t.Fatalf("waiting = %v, want [%v]", got, want)
	}
	appendTranscript(t, path, queueLine("remove", synthQueued, at, "absorbed_mid_turn"))
	if got := conversationWaiting(t, client, pane); len(got) != 0 {
		t.Fatalf("after the remove record: waiting = %v, want none", got)
	}
}

func TestServePaneConversationWaitingIdle(t *testing.T) {
	t.Parallel()

	const at = "2026-01-02T03:04:05Z"
	tmux := newTmuxServer(t)
	path := writeConversation(t, 1)
	appendTranscript(t, path, queueLine("enqueue", synthQueued, at, ""))
	options := map[string]string{
		protocol.OptionState:      string(protocol.StateDone),
		protocol.OptionTranscript: path,
	}
	pane := conversationPane(t, tmux, options)

	if got := conversationWaiting(t, panesClient(t, newHandler(t, tmux)), pane); len(got) != 0 {
		t.Fatalf("done pane: waiting = %v, want none", got)
	}
}

func TestServePaneConversationLimit(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane := conversationPane(t, tmux, liveOptions(writeConversation(t, 40)))
	client := panesClient(t, newHandler(t, tmux))
	cases := []struct {
		name      string
		limit     uint32
		wantLen   int
		wantFirst string
		wantLast  string
	}{
		{
			name:      "default is 30",
			limit:     0,
			wantLen:   30,
			wantFirst: "synthetic reply 11",
			wantLast:  "synthetic reply 40",
		},
		{
			name:      "limit 5",
			limit:     5,
			wantLen:   5,
			wantFirst: "synthetic reply 36",
			wantLast:  "synthetic reply 40",
		},
		{
			name:      "limit 1",
			limit:     1,
			wantLen:   1,
			wantFirst: "synthetic reply 40",
			wantLast:  "synthetic reply 40",
		},
		{
			name:      "limit 100 holds all 41 items",
			limit:     100,
			wantLen:   41,
			wantFirst: synthOrigin,
			wantLast:  "synthetic reply 40",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := client.Conversation(t.Context(),
				connect.NewRequest(&orreryv1.ConversationRequest{Pane: pane, Limit: tc.limit}))
			if err != nil {
				t.Fatalf("Panes.Conversation: %v", err)
			}
			items := got.Msg.GetItems()
			if len(items) != tc.wantLen {
				t.Fatalf("%d items, want %d", len(items), tc.wantLen)
			}
			if first := items[0].GetText(); first != tc.wantFirst {
				t.Fatalf("first item = %q, want %q", first, tc.wantFirst)
			}
			if last := items[len(items)-1].GetText(); last != tc.wantLast {
				t.Fatalf("last item = %q, want %q", last, tc.wantLast)
			}
		})
	}
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
			_, err := panesClient(t, newHandler(t, tmux)).Conversation(t.Context(),
				connect.NewRequest(&orreryv1.ConversationRequest{Pane: pane}))
			assertCode(t, err, connect.CodeNotFound)
		})
	}
}
