package integration_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/server"
)

// connectProto is the content type a Connect unary request sends for protobuf bodies.
const connectProto = "application/proto"

// engineHome creates the fixture's HOME, which serve admits session directories under, and returns it. The fixture
// does not create HOME itself.
func (f *engineFixture) engineHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(f.root, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("make home: %v", err)
	}
	return home
}

// homeSessionDir makes and returns the session directory work under the fixture's HOME.
func (f *engineFixture) homeSessionDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(f.engineHome(t), "work")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("make home session directory: %v", err)
	}
	return dir
}

// startAPIServe starts orrery serve on a free loopback address with env, for tmux server name and the allowed login
// exampleLogin, and waits for its listener. It returns the process and the base URL of its API.
func startAPIServe(t *testing.T, env []string, name string) (*orreryProcess, string) {
	t.Helper()
	addr := freeLoopbackAddr(t)
	p := startBinary(t, "orrery", env, "-L", name, "serve", "--listen", addr, "--allow-login", exampleLogin)
	p.waitForListener(t, "tcp", addr)
	return p, "http://" + addr
}

// guardedRequest returns a Connect request from exampleLogin that carries the client header the guard needs.
func guardedRequest[T any](msg *T) *connect.Request[T] {
	req := connect.NewRequest(msg)
	req.Header().Set(server.HeaderLogin, exampleLogin)
	req.Header().Set(server.HeaderClient, serveClient)
	return req
}

// startViaControl sends a Start for dir and prompt through the Control client at base and returns its error.
func startViaControl(t *testing.T, base, dir string) (string, error) {
	t.Helper()
	control := orreryv1connect.NewControlClient(&http.Client{}, base)
	ctx, cancel := context.WithTimeout(t.Context(), waitLimit)
	defer cancel()
	resp, err := control.Start(ctx, guardedRequest(&orreryv1.StartRequest{Dir: dir, Prompt: "hello"}))
	if err != nil {
		return "", err
	}
	return resp.Msg.GetId(), nil
}

// waitForReplyBlock streams Events.Watch from base until a finished assistant block of session id with text arrives.
// It fails the test when none arrives within waitLimit.
func waitForReplyBlock(t *testing.T, base, id, text string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), waitLimit)
	defer cancel()
	events := orreryv1connect.NewEventsClient(&http.Client{}, base)
	stream, err := events.Watch(ctx, guardedRequest(&orreryv1.WatchRequest{}))
	if err != nil {
		t.Fatalf("open Events.Watch: %v", err)
	}
	defer stream.Close()
	for stream.Receive() {
		ev := stream.Msg()
		if ev.GetSessionId() != id {
			continue
		}
		block := ev.GetEngineBlock()
		if block != nil && block.GetRole() == "assistant" && block.GetText() == text {
			return
		}
	}
	t.Fatalf("Events.Watch ended without assistant block %q for %s: %v", text, id, stream.Err())
}

// TestEngineAPIGuardsControlPaths proves that every Control procedure answers 403 with no login, a stranger's login,
// or the allowed login without the client header, and admits the allowed login with the client header.
func TestEngineAPIGuardsControlPaths(t *testing.T) {
	f := newEngineFixture(t)
	daemon := f.startOrreryd(t, true)
	serve, base := startAPIServe(t, f.env, f.server)

	noLogin := map[string]string{"Content-Type": connectProto}
	stranger := map[string]string{
		server.HeaderLogin:  strangerLogin,
		server.HeaderClient: serveClient,
		"Content-Type":      connectProto,
	}
	noClient := map[string]string{server.HeaderLogin: exampleLogin, "Content-Type": connectProto}
	admitted := map[string]string{
		server.HeaderLogin:  exampleLogin,
		server.HeaderClient: serveClient,
		"Content-Type":      connectProto,
	}
	procedures := []string{
		orreryv1connect.ControlStartProcedure,
		orreryv1connect.ControlSayProcedure,
		orreryv1connect.ControlAnswerProcedure,
		orreryv1connect.ControlInterruptProcedure,
		orreryv1connect.ControlStopProcedure,
		orreryv1connect.ControlSeenProcedure,
	}
	for _, procedure := range procedures {
		target := base + procedure
		wantStatus(t, apiDo(t, http.MethodPost, target, "", noLogin), http.StatusForbidden)
		wantStatus(t, apiDo(t, http.MethodPost, target, "", stranger), http.StatusForbidden)
		wantStatus(t, apiDo(t, http.MethodPost, target, "", noClient), http.StatusForbidden)
		if got := apiDo(t, http.MethodPost, target, "", admitted); got.status == http.StatusForbidden {
			t.Fatalf("%s refused the allowed login with the client header; body %q", procedure, got.body)
		}
	}

	wantExit(t, serve.stop(t), 0)
	wantExit(t, daemon.stop(t), 0)
}

// TestEngineAPIStartUnderHomeReplies proves that a Start in a directory under HOME returns an id, and that the
// session's reply arrives on Events.Watch as an engine_block.
func TestEngineAPIStartUnderHomeReplies(t *testing.T) {
	f := newEngineFixture(t)
	daemon := f.startOrreryd(t, true)
	serve, base := startAPIServe(t, f.env, f.server)
	dir := f.homeSessionDir(t)

	id, err := startViaControl(t, base, dir)
	if err != nil {
		t.Fatalf("Start under home: %v", err)
	}
	if id == "" {
		t.Fatal("Start under home returned an empty id")
	}
	waitForReplyBlock(t, base, id, "fake reply 1")

	f.stopSession(t, id, dir)
	wantExit(t, serve.stop(t), 0)
	wantExit(t, daemon.stop(t), 0)
}

// TestEngineAPIStartOutsideHomeInvalid proves that a Start in a directory outside HOME answers invalid_argument.
func TestEngineAPIStartOutsideHomeInvalid(t *testing.T) {
	f := newEngineFixture(t)
	daemon := f.startOrreryd(t, true)
	f.engineHome(t)
	serve, base := startAPIServe(t, f.env, f.server)
	outside := f.sessionDir(t, "outside")

	_, err := startViaControl(t, base, outside)
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("Start outside home: code %v, want invalid_argument; err %v", connect.CodeOf(err), err)
	}

	wantExit(t, serve.stop(t), 0)
	wantExit(t, daemon.stop(t), 0)
}

// TestEngineAPIStartWithoutSessionsUnavailable proves that with orreryd running without --sessions, a Start under HOME
// answers unavailable.
func TestEngineAPIStartWithoutSessionsUnavailable(t *testing.T) {
	f := newEngineFixture(t)
	daemon := f.startOrreryd(t, false)
	serve, base := startAPIServe(t, f.env, f.server)
	dir := f.homeSessionDir(t)

	_, err := startViaControl(t, base, dir)
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("Start without sessions: code %v, want unavailable; err %v", connect.CodeOf(err), err)
	}

	wantExit(t, serve.stop(t), 0)
	wantExit(t, daemon.stop(t), 0)
}

// TestEngineAPIStartWhenOrreryAway proves that with no orreryd running, a Start under HOME answers unavailable while
// serve keeps answering.
func TestEngineAPIStartWhenOrreryAway(t *testing.T) {
	f := newEngineFixture(t)
	serve, base := startAPIServe(t, f.env, f.server)
	dir := f.homeSessionDir(t)

	_, err := startViaControl(t, base, dir)
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("Start with orreryd away: code %v, want unavailable; err %v", connect.CodeOf(err), err)
	}

	wantExit(t, serve.stop(t), 0)
}

// TestEngineAPIServeStartsWithoutDaemonSocket proves that when no daemon socket resolves, serve still starts and a
// Start answers unavailable.
func TestEngineAPIServeStartsWithoutDaemonSocket(t *testing.T) {
	f := newEngineFixture(t)
	f.engineHome(t)
	env := scrubbedEnv("HOME="+filepath.Join(f.root, "home"), "XDG_STATE_HOME="+filepath.Join(f.root, "state"))
	serve, base := startAPIServe(t, env, f.server)
	dir := f.homeSessionDir(t)

	_, err := startViaControl(t, base, dir)
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("Start without a daemon socket: code %v, want unavailable; err %v", connect.CodeOf(err), err)
	}

	wantExit(t, serve.stop(t), 0)
}
