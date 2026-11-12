package integration_test

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
)

// startLoginProxy starts a reverse proxy in the test process in front of serve at base. It sets the login header to
// exampleLogin on each request, as Tailscale serve does, and forwards each response as it is written so that streamed
// Events replies reach the client. The front server enables full duplex on each request, so the proxy can read a
// request body while it writes the response. The proxy closes at cleanup, and its URL is returned.
func startLoginProxy(t *testing.T, base string) string {
	t.Helper()
	target, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse serve URL: %v", err)
	}
	proxy := &httputil.ReverseProxy{
		FlushInterval: -1,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.Header.Set(server.HeaderLogin, exampleLogin)
		},
	}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
			t.Errorf("enable full duplex: %v", err)
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	return front.URL
}

// urlClientEnv returns the environment of a client with no daemon socket: ORRERY_SOCKET and XDG_RUNTIME_DIR are unset,
// so no socket resolves. HOME is the fixture's. XDG_STATE_HOME is an empty directory, not the fixture's store, so a
// watch that read the local store could not see the session.
func (f *engineFixture) urlClientEnv(t *testing.T) []string {
	t.Helper()
	return scrubbedEnv("HOME="+f.engineHome(t), "XDG_STATE_HOME="+t.TempDir())
}

// urlSessionDir makes and returns the session directory name under the fixture's HOME.
func (f *engineFixture) urlSessionDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(f.engineHome(t), name)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("make session directory: %v", err)
	}
	return dir
}

// urlSession runs `orrery session --url proxy ARGS...` in the client environment env.
func urlSession(t *testing.T, env []string, proxy string, args ...string) outcome {
	t.Helper()
	return runCommand(t, "", "orrery", "", env, append([]string{"session", "--url", proxy}, args...)...)
}

// startViaURL runs `session --url proxy start --model haiku DIR PROMPT` and returns the id it prints.
func startViaURL(t *testing.T, env []string, proxy, dir, prompt string) string {
	t.Helper()
	out := urlSession(t, env, proxy, "start", "--model", "haiku", dir, prompt)
	wantExit(t, out, 0)
	id := strings.TrimSpace(out.stdout)
	if id == "" {
		t.Fatalf("session --url start printed no id; stderr %q", out.stderr)
	}
	return id
}

// stopViaURL runs `session --url proxy stop ID`, waits for the store to record the session stopped, and waits for the
// fake claude it started in dir to exit.
func (f *engineFixture) stopViaURL(t *testing.T, env []string, proxy, id, dir string) {
	t.Helper()
	wantExit(t, urlSession(t, env, proxy, "stop", id), 0)
	f.waitForHistory(t, id, func(h store.EngineHistory) bool { return h.State == "stopped" })
	waitGone(t, fakeClaudePID(t, dir))
}

// TestEngineCLIURLSessionVerbs proves that every session verb, run as `orrery session --url` against serve through a
// login proxy, reaches orreryd with no daemon socket resolved. The non-URL form fails to resolve one in the same
// environment.
func TestEngineCLIURLSessionVerbs(t *testing.T) {
	f := newEngineFixture(t)
	daemon := f.startOrreryd(t, true)
	serve, base := startAPIServe(t, f.env, f.server)
	proxy := startLoginProxy(t, base)
	env := f.urlClientEnv(t)

	noURL := runCommand(t, "", "orrery", "", env, "session", "start", f.engineHome(t), "hello")
	wantExit(t, noURL, 1)
	if !strings.Contains(noURL.stderr, "no daemon socket") {
		t.Fatalf("session without --url stderr %q, want a no daemon socket failure", noURL.stderr)
	}

	dir := f.homeSessionDir(t)
	id := startViaURL(t, env, proxy, dir, "hello")
	f.waitForBlock(t, id, "fake reply 1")
	args, err := os.ReadFile(filepath.Join(dir, "fakeclaude.args"))
	if err != nil {
		t.Fatalf("read fake claude arguments: %v", err)
	}
	if !strings.Contains(string(args), "--model haiku") {
		t.Fatalf("fake claude arguments %q do not carry --model haiku", args)
	}

	wantExit(t, urlSession(t, env, proxy, "say", id, "again"), 0)
	f.waitForBlock(t, id, "fake reply 2")
	wantExit(t, urlSession(t, env, proxy, "seen", id), 0)
	f.waitForEvent(t, id, store.EventEngineChanged, `"seen":`)

	askDir := f.urlSessionDir(t, "ask")
	askID := startViaURL(t, env, proxy, askDir, "ASK")
	f.waitForEvent(t, askID, store.EventEngineRequest, `"requestId":"ask-1"`)
	wantExit(t, urlSession(t, env, proxy, "answer", askID, "ask-1", "red"), 0)
	f.waitForBlock(t, askID, "fake answered 1")

	hangDir := f.urlSessionDir(t, "hang")
	hangID := startViaURL(t, env, proxy, hangDir, "HANG")
	f.waitForBlock(t, hangID, "fake hanging 1")
	wantExit(t, urlSession(t, env, proxy, "interrupt", hangID), 0)
	f.waitForEvent(t, hangID, store.EventEngineTurnEnded, `"terminalReason":"aborted_streaming"`)

	f.stopViaURL(t, env, proxy, askID, askDir)
	f.stopViaURL(t, env, proxy, hangID, hangDir)
	f.stopViaURL(t, env, proxy, id, dir)

	watched := urlSession(t, env, proxy, "watch", id)
	wantExit(t, watched, 0)
	assertWatchedSession(t, watched.stdout, id, []string{"fake reply 1", "fake reply 2"})

	wantExit(t, serve.stop(t), 0)
	wantExit(t, daemon.stop(t), 0)
}

// assertWatchedSession decodes each line of watch output as a WatchResponse and checks that every line is about session
// id, that each reply is one assistant engine block with exact text, and that a line sets the session stopped.
func assertWatchedSession(t *testing.T, stdout, id string, replies []string) {
	t.Helper()
	blocks := make(map[string]int, len(replies))
	stopped := false
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		var msg orreryv1.WatchResponse
		if err := protojson.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("watch line %q is not a WatchResponse: %v", line, err)
		}
		if msg.GetSessionId() != id {
			t.Fatalf("watch line %q is about session %q, want %q", line, msg.GetSessionId(), id)
		}
		if block := msg.GetEngineBlock(); block != nil && block.GetRole() == "assistant" {
			blocks[block.GetText()]++
		}
		if msg.GetEngineChanged().GetState() == "stopped" {
			stopped = true
		}
	}
	for _, reply := range replies {
		if blocks[reply] != 1 {
			t.Errorf("watch prints %d assistant engine blocks with text %q, want 1: %q", blocks[reply], reply, stdout)
		}
	}
	if !stopped {
		t.Errorf("watch prints no engine_changed line with state stopped: %q", stdout)
	}
}

// TestEngineCLIURLUsageLines proves that the usage text lists the --url form of session and the start form with
// --model.
func TestEngineCLIURLUsageLines(t *testing.T) {
	out := runOrrery(t, "", nil, "bogus")
	wantExit(t, out, 2)
	for _, line := range []string{"session [--url URL] VERB ...", "session start [--model MODEL] DIR PROMPT"} {
		if !strings.Contains(out.stderr, line) {
			t.Errorf("usage text does not list %q: %q", line, out.stderr)
		}
	}
}
