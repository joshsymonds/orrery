package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/server"
)

// fakeStartedID is the session id the fake orreryd returns from Start.
const fakeStartedID = "synthetic-started-id"

// synthSessionID is a session id of the 32-digit hex form, named by Start, Say and the other Control calls.
const synthSessionID = "0123456789abcdef0123456789abcdef"

// shortControlTimeout bounds the forwards of the deadline test, short enough that the test ends quickly.
const shortControlTimeout = 50 * time.Millisecond

// controlSayPath is the Control path of Say, used by the body-limit test.
const controlSayPath = "/" + orreryv1connect.ControlName + "/Say"

// fakeEngine is a recording orreryd Engine. Each call records its request. It then blocks until block closes or the
// call's context ends, when block is set, and returns err when err is set.
type fakeEngine struct {
	mu    sync.Mutex
	calls []proto.Message
	err   error
	block chan struct{}
}

// recorded returns the requests the fake has received, in order.
func (f *fakeEngine) recorded() []proto.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// answer records msg and returns the error the call answers with.
func (f *fakeEngine) answer(ctx context.Context, msg proto.Message) error {
	f.mu.Lock()
	f.calls = append(f.calls, msg)
	f.mu.Unlock()
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.err
}

func (f *fakeEngine) Start(
	ctx context.Context,
	req *connect.Request[enginev1.StartRequest],
) (*connect.Response[enginev1.StartResponse], error) {
	if err := f.answer(ctx, req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.StartResponse{Id: fakeStartedID}), nil
}

func (f *fakeEngine) Say(
	ctx context.Context,
	req *connect.Request[enginev1.SayRequest],
) (*connect.Response[enginev1.SayResponse], error) {
	if err := f.answer(ctx, req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.SayResponse{}), nil
}

func (f *fakeEngine) Answer(
	ctx context.Context,
	req *connect.Request[enginev1.AnswerRequest],
) (*connect.Response[enginev1.AnswerResponse], error) {
	if err := f.answer(ctx, req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.AnswerResponse{}), nil
}

func (f *fakeEngine) Interrupt(
	ctx context.Context,
	req *connect.Request[enginev1.InterruptRequest],
) (*connect.Response[enginev1.InterruptResponse], error) {
	if err := f.answer(ctx, req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.InterruptResponse{}), nil
}

func (f *fakeEngine) Stop(
	ctx context.Context,
	req *connect.Request[enginev1.StopRequest],
) (*connect.Response[enginev1.StopResponse], error) {
	if err := f.answer(ctx, req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.StopResponse{}), nil
}

func (f *fakeEngine) Seen(
	ctx context.Context,
	req *connect.Request[enginev1.SeenRequest],
) (*connect.Response[enginev1.SeenResponse], error) {
	if err := f.answer(ctx, req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.SeenResponse{}), nil
}

// newFakeOrreryd serves fake as orreryd's Engine on a loopback test server and returns a client for it.
func newFakeOrreryd(t *testing.T, fake *fakeEngine) enginev1connect.EngineClient {
	t.Helper()
	_, handler := enginev1connect.NewEngineHandler(fake)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return enginev1connect.NewEngineClient(http.DefaultClient, srv.URL)
}

// newControlHandler returns the guarded handler that admits allowedLogin, forwards Control to engine, and has a home
// under t.TempDir(). A nil engine answers unavailable, and a zero timeout is the default.
func newControlHandler(t *testing.T, engine enginev1connect.EngineClient, timeout time.Duration) http.Handler {
	t.Helper()
	return newHomedControlHandler(t, t.TempDir(), engine, timeout)
}

// newHomedControlHandler is newControlHandler with home as the home directory.
func newHomedControlHandler(
	t *testing.T,
	home string,
	engine enginev1connect.EngineClient,
	timeout time.Duration,
) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:         []string{allowedLogin},
		Tmux:           newTmuxClient(t),
		Push:           newPushStore(t),
		Engine:         engine,
		ControlTimeout: timeout,
		Home:           home,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// sessionUnder creates a session directory under home. It returns the directory as given and resolved through
// symlinks.
func sessionUnder(t *testing.T, home string) (string, string) {
	t.Helper()
	dir := filepath.Join(home, "session")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir session: %v", err)
	}
	return dir, resolvedPath(t, dir)
}

// resolvedPath returns path with its symlinks resolved.
func resolvedPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", path, err)
	}
	return resolved
}

// controlClient serves handler on an httptest server and returns a generated Control client whose requests carry
// allowedLogin and the client header.
func controlClient(t *testing.T, handler http.Handler) orreryv1connect.ControlClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: headerTransport{base: http.DefaultTransport, login: allowedLogin}}
	return orreryv1connect.NewControlClient(httpClient, srv.URL)
}

// controlCall is one Control call. call issues it, engine is the request the fake must receive, and reply is the
// response the call returns.
type controlCall struct {
	name   string
	call   func(context.Context, orreryv1connect.ControlClient) (proto.Message, error)
	engine proto.Message
	reply  proto.Message
}

// replyOf returns the message of a Control response, or the error of a failed call.
func replyOf[T any, M interface {
	*T
	proto.Message
}](resp *connect.Response[T], err error) (proto.Message, error) {
	if err != nil {
		return nil, err
	}
	return M(resp.Msg), nil
}

// controlCalls returns the six Control calls, each with every request field set. Start sends dir and expects the
// engine to receive resolved, the same directory with its symlinks resolved.
func controlCalls(dir, resolved string) []controlCall {
	return []controlCall{
		{
			name: "Start",
			call: func(ctx context.Context, c orreryv1connect.ControlClient) (proto.Message, error) {
				return replyOf(c.Start(ctx, connect.NewRequest(&orreryv1.StartRequest{
					Dir: dir, Prompt: "synthetic prompt", Model: "haiku", Id: synthSessionID,
				})))
			},
			engine: &enginev1.StartRequest{
				Dir: resolved, Prompt: "synthetic prompt", Model: "haiku", Id: synthSessionID,
			},
			reply: &orreryv1.StartResponse{Id: fakeStartedID},
		},
		{
			name: "Say",
			call: func(ctx context.Context, c orreryv1connect.ControlClient) (proto.Message, error) {
				return replyOf(c.Say(ctx, connect.NewRequest(&orreryv1.SayRequest{
					Id: synthSessionID, Text: "synthetic text", PromptId: "synthetic-prompt",
				})))
			},
			engine: &enginev1.SayRequest{Id: synthSessionID, Text: "synthetic text", PromptId: "synthetic-prompt"},
			reply:  &orreryv1.SayResponse{},
		},
		{
			name: "Answer",
			call: func(ctx context.Context, c orreryv1connect.ControlClient) (proto.Message, error) {
				return replyOf(c.Answer(ctx, connect.NewRequest(&orreryv1.AnswerRequest{
					Id: synthSessionID, RequestId: "synthetic-request", Answer: "allow",
				})))
			},
			engine: &enginev1.AnswerRequest{Id: synthSessionID, RequestId: "synthetic-request", Answer: "allow"},
			reply:  &orreryv1.AnswerResponse{},
		},
		{
			name: "Interrupt",
			call: func(ctx context.Context, c orreryv1connect.ControlClient) (proto.Message, error) {
				return replyOf(c.Interrupt(ctx, connect.NewRequest(&orreryv1.InterruptRequest{Id: synthSessionID})))
			},
			engine: &enginev1.InterruptRequest{Id: synthSessionID},
			reply:  &orreryv1.InterruptResponse{},
		},
		{
			name: "Stop",
			call: func(ctx context.Context, c orreryv1connect.ControlClient) (proto.Message, error) {
				return replyOf(c.Stop(ctx, connect.NewRequest(&orreryv1.StopRequest{Id: synthSessionID})))
			},
			engine: &enginev1.StopRequest{Id: synthSessionID},
			reply:  &orreryv1.StopResponse{},
		},
		{
			name: "Seen",
			call: func(ctx context.Context, c orreryv1connect.ControlClient) (proto.Message, error) {
				return replyOf(c.Seen(ctx, connect.NewRequest(&orreryv1.SeenRequest{Id: synthSessionID})))
			},
			engine: &enginev1.SeenRequest{Id: synthSessionID},
			reply:  &orreryv1.SeenResponse{},
		},
	}
}

// controlPaths returns the Connect path of each Control call.
func controlPaths() []string {
	return []string{
		orreryv1connect.ControlStartProcedure,
		orreryv1connect.ControlSayProcedure,
		orreryv1connect.ControlAnswerProcedure,
		orreryv1connect.ControlInterruptProcedure,
		orreryv1connect.ControlStopProcedure,
		orreryv1connect.ControlSeenProcedure,
	}
}

// assertConnectError fails the test unless err is a Connect error with code and message.
func assertConnectError(t *testing.T, err error, code connect.Code, message string) {
	t.Helper()
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("error = %v, want a Connect error with code %v", err, code)
	}
	if connectErr.Code() != code || connectErr.Message() != message {
		t.Fatalf("error = code %v message %q, want code %v message %q",
			connectErr.Code(), connectErr.Message(), code, message)
	}
}

func TestControlForwardsEveryCall(t *testing.T) {
	home := t.TempDir()
	dir, resolved := sessionUnder(t, home)
	for _, tc := range controlCalls(dir, resolved) {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeEngine{}
			control := controlClient(t, newHomedControlHandler(t, home, newFakeOrreryd(t, fake), 0))

			got, err := tc.call(t.Context(), control)
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if !proto.Equal(got, tc.reply) {
				t.Fatalf("%s response = %v, want %v", tc.name, got, tc.reply)
			}
			calls := fake.recorded()
			if len(calls) != 1 || !proto.Equal(calls[0], tc.engine) {
				t.Fatalf("%s forwarded %v, want one request %v", tc.name, calls, tc.engine)
			}
		})
	}
}

func TestControlAnswersOrreryErrors(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		code    connect.Code
		message string
	}{
		{
			name:    "not_found",
			err:     connect.NewError(connect.CodeNotFound, errors.New("synthetic missing session")),
			code:    connect.CodeNotFound,
			message: "synthetic missing session",
		},
		{
			name:    "invalid_argument",
			err:     connect.NewError(connect.CodeInvalidArgument, errors.New("synthetic bad directory")),
			code:    connect.CodeInvalidArgument,
			message: "synthetic bad directory",
		},
		{
			name:    "resource_exhausted",
			err:     connect.NewError(connect.CodeResourceExhausted, errors.New("synthetic session cap")),
			code:    connect.CodeResourceExhausted,
			message: "synthetic session cap",
		},
		{
			name:    "unavailable",
			err:     connect.NewError(connect.CodeUnavailable, errors.New("synthetic orreryd detail")),
			code:    connect.CodeUnavailable,
			message: "session service unavailable",
		},
		{
			name:    "internal",
			err:     connect.NewError(connect.CodeInternal, errors.New("synthetic internal detail")),
			code:    connect.CodeInternal,
			message: "session command failed",
		},
		{
			name:    "unknown",
			err:     errors.New("synthetic store detail"),
			code:    connect.CodeInternal,
			message: "session command failed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeEngine{err: tc.err}
			control := controlClient(t, newControlHandler(t, newFakeOrreryd(t, fake), 0))

			_, err := control.Stop(t.Context(), connect.NewRequest(&orreryv1.StopRequest{Id: synthSessionID}))
			assertConnectError(t, err, tc.code, tc.message)
		})
	}
}

func TestControlUnavailableWithoutEngine(t *testing.T) {
	home := t.TempDir()
	dir, resolved := sessionUnder(t, home)
	control := controlClient(t, newHomedControlHandler(t, home, nil, 0))
	for _, tc := range controlCalls(dir, resolved) {
		_, err := tc.call(t.Context(), control)
		assertConnectError(t, err, connect.CodeUnavailable, "session service unavailable")
	}
}

func TestControlUnavailableWhenOrreryClosed(t *testing.T) {
	_, handler := enginev1connect.NewEngineHandler(&fakeEngine{})
	srv := httptest.NewServer(handler)
	engine := enginev1connect.NewEngineClient(http.DefaultClient, srv.URL)
	srv.Close()
	home := t.TempDir()
	dir, resolved := sessionUnder(t, home)
	control := controlClient(t, newHomedControlHandler(t, home, engine, 0))

	for _, tc := range controlCalls(dir, resolved) {
		_, err := tc.call(t.Context(), control)
		assertConnectError(t, err, connect.CodeUnavailable, "session service unavailable")
	}
}

func TestControlDeadlineExceeded(t *testing.T) {
	fake := &fakeEngine{block: make(chan struct{})}
	engine := newFakeOrreryd(t, fake)
	t.Cleanup(func() { close(fake.block) })
	control := controlClient(t, newControlHandler(t, engine, shortControlTimeout))

	_, err := control.Stop(t.Context(), connect.NewRequest(&orreryv1.StopRequest{Id: synthSessionID}))
	assertConnectError(t, err, connect.CodeDeadlineExceeded, "session command timed out")
}

func TestControlZeroTimeoutMeansTenSeconds(t *testing.T) {
	fake := &fakeEngine{block: make(chan struct{})}
	engine := newFakeOrreryd(t, fake)
	t.Cleanup(func() { close(fake.block) })
	control := controlClient(t, newControlHandler(t, engine, 0))

	start := time.Now()
	_, err := control.Stop(t.Context(), connect.NewRequest(&orreryv1.StopRequest{Id: synthSessionID}))
	assertConnectError(t, err, connect.CodeDeadlineExceeded, "session command timed out")
	if elapsed := time.Since(start); elapsed < 9*time.Second {
		t.Fatalf("deadline after %v, want about 10s", elapsed)
	}
}

func TestControlRefusesBodiesOverOneMiB(t *testing.T) {
	fake := &fakeEngine{}
	handler := newControlHandler(t, newFakeOrreryd(t, fake), 0)
	body, err := proto.Marshal(&orreryv1.SayRequest{Id: synthSessionID, Text: strings.Repeat("x", 2<<20)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	rec := serve(t, handler, http.MethodPost, controlSayPath,
		connectHeader(allowedLogin, "application/proto", true), string(body))
	var fault struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &fault); err != nil {
		t.Fatalf("error body %q: %v", rec.Body.String(), err)
	}
	if fault.Code != "resource_exhausted" {
		t.Fatalf("oversize body: status %d code %q, want code resource_exhausted", rec.Code, fault.Code)
	}
	if calls := fake.recorded(); len(calls) != 0 {
		t.Fatalf("oversize body forwarded %d requests, want none", len(calls))
	}
}

func TestControlRequiresLoginAndClientHeaderOnEveryPath(t *testing.T) {
	handler := newControlHandler(t, newFakeOrreryd(t, &fakeEngine{}), 0)
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
	for _, path := range controlPaths() {
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

func TestControlRefusesNegativeTimeout(t *testing.T) {
	_, err := server.New(server.Config{
		Logins:         []string{allowedLogin},
		Tmux:           newTmuxClient(t),
		Push:           newPushStore(t),
		ControlTimeout: -time.Second,
	})
	if err == nil {
		t.Fatal("New with negative ControlTimeout: no error")
	}
}

// startRefusedMessage is the fixed message for a start whose directory is not an existing directory under the home.
const startRefusedMessage = "session directory must be an existing directory under the home directory"

// startAt sends a Start for dir through control, with the request fields the start tests do not vary.
func startAt(
	t *testing.T,
	control orreryv1connect.ControlClient,
	dir string,
) (*connect.Response[orreryv1.StartResponse], error) {
	t.Helper()
	return control.Start(t.Context(), connect.NewRequest(&orreryv1.StartRequest{
		Dir: dir, Prompt: "synthetic prompt", Id: synthSessionID,
	}))
}

// assertStartForwarded starts dir under a handler whose home is home. It fails unless the start forwards exactly one
// request naming resolved and the answer carries the started id.
func assertStartForwarded(t *testing.T, home, dir, resolved string) {
	t.Helper()
	fake := &fakeEngine{}
	control := controlClient(t, newHomedControlHandler(t, home, newFakeOrreryd(t, fake), 0))

	resp, err := startAt(t, control, dir)
	if err != nil {
		t.Fatalf("Start %s: %v", dir, err)
	}
	if got := resp.Msg.GetId(); got != fakeStartedID {
		t.Fatalf("Start %s id = %q, want %q", dir, got, fakeStartedID)
	}
	want := &enginev1.StartRequest{Dir: resolved, Prompt: "synthetic prompt", Id: synthSessionID}
	calls := fake.recorded()
	if len(calls) != 1 || !proto.Equal(calls[0], want) {
		t.Fatalf("Start %s forwarded %v, want one request %v", dir, calls, want)
	}
}

func TestControlRefusesStartsOutsideHome(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	session, _ := sessionUnder(t, home)
	file := filepath.Join(home, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	escape := filepath.Join(home, "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Fatalf("symlink escape: %v", err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	relative, err := filepath.Rel(wd, session)
	if err != nil {
		t.Fatalf("relative path: %v", err)
	}

	cases := []struct {
		name string
		dir  string
	}{
		{"outside", outside},
		{"missing", filepath.Join(home, "missing")},
		{"regular file", file},
		{"relative", relative},
		{"symlink out", escape},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeEngine{}
			control := controlClient(t, newHomedControlHandler(t, home, newFakeOrreryd(t, fake), 0))

			_, err := startAt(t, control, tc.dir)
			assertConnectError(t, err, connect.CodeInvalidArgument, startRefusedMessage)
			if calls := fake.recorded(); len(calls) != 0 {
				t.Fatalf("refused start forwarded %d requests, want none", len(calls))
			}
		})
	}
}

func TestControlForwardsStartsUnderHome(t *testing.T) {
	home := t.TempDir()
	dir, resolved := sessionUnder(t, home)
	link := filepath.Join(home, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatalf("symlink link: %v", err)
	}

	cases := []struct {
		name     string
		dir      string
		resolved string
	}{
		{"home itself", home, resolvedPath(t, home)},
		{"directory under home", dir, resolved},
		{"symlink to directory under home", link, resolved},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertStartForwarded(t, home, tc.dir, tc.resolved)
		})
	}
}

func TestControlAdmitsHomeThroughSymlink(t *testing.T) {
	realHome := t.TempDir()
	_, resolved := sessionUnder(t, realHome)
	homeLink := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(realHome, homeLink); err != nil {
		t.Fatalf("symlink home: %v", err)
	}

	cases := []struct {
		name     string
		dir      string
		resolved string
	}{
		{"home itself", homeLink, resolvedPath(t, realHome)},
		{"directory under home", filepath.Join(homeLink, "session"), resolved},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertStartForwarded(t, homeLink, tc.dir, tc.resolved)
		})
	}
}

func TestControlRefusesStartsWithoutUsableHome(t *testing.T) {
	existing := t.TempDir()
	realHome := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	relative, err := filepath.Rel(wd, realHome)
	if err != nil {
		t.Fatalf("relative home: %v", err)
	}

	cases := []struct {
		name string
		home string
	}{
		{"empty", ""},
		{"relative", relative},
		{"root", string(filepath.Separator)},
		{"missing", filepath.Join(t.TempDir(), "missing")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeEngine{}
			control := controlClient(t, newHomedControlHandler(t, tc.home, newFakeOrreryd(t, fake), 0))

			_, err := startAt(t, control, existing)
			assertConnectError(t, err, connect.CodeInvalidArgument, startRefusedMessage)
			if calls := fake.recorded(); len(calls) != 0 {
				t.Fatalf("start with home %q forwarded %d requests, want none", tc.home, len(calls))
			}
		})
	}
}

// stampFileNames returns the two settings files that can stamp a folder.
func stampFileNames() []string {
	return []string{"settings.json", "settings.local.json"}
}

// stampSettings is a settings file that stamps its folder: env.ANTHROPIC_BASE_URL is a non-empty string.
const stampSettings = `{"env": {"ANTHROPIC_BASE_URL": "https://synthetic.example"}}`

// makeDir creates dir and its parents, and returns dir.
func makeDir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	return dir
}

// writeSettings writes content as the settings file name of folder, creating folder's .claude directory.
func writeSettings(t *testing.T, folder, name, content string) {
	t.Helper()
	claude := makeDir(t, filepath.Join(folder, ".claude"))
	if err := os.WriteFile(filepath.Join(claude, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// belowStampMessage is the refusal for a start in a folder with no stamp of its own, below the stamped folder.
func belowStampMessage(stamped string) string {
	return "session directory has no context stamp of its own and is below a context-stamped folder (" + stamped + ")"
}

func TestControlRefusesStartsBelowStampedFolder(t *testing.T) {
	for _, name := range stampFileNames() {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			stamped := makeDir(t, filepath.Join(home, "stamped"))
			writeSettings(t, stamped, name, stampSettings)
			cases := []struct {
				name string
				dir  string
			}{
				{"plain child", makeDir(t, filepath.Join(stamped, "child"))},
				{"two levels below", makeDir(t, filepath.Join(stamped, "middle", "deep"))},
			}
			want := belowStampMessage(resolvedPath(t, stamped))
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					fake := &fakeEngine{}
					control := controlClient(t, newHomedControlHandler(t, home, newFakeOrreryd(t, fake), 0))

					_, err := startAt(t, control, tc.dir)
					assertConnectError(t, err, connect.CodeInvalidArgument, want)
					if calls := fake.recorded(); len(calls) != 0 {
						t.Fatalf("refused start forwarded %d requests, want none", len(calls))
					}
				})
			}
		})
	}
}

func TestControlForwardsStartsWithoutStampAbove(t *testing.T) {
	home := t.TempDir()
	writeSettings(t, home, "settings.json", stampSettings)
	stamped := makeDir(t, filepath.Join(home, "stamped"))
	writeSettings(t, stamped, "settings.json", stampSettings)
	own := makeDir(t, filepath.Join(stamped, "own"))
	writeSettings(t, own, "settings.local.json", stampSettings)
	cases := []struct {
		name string
		dir  string
	}{
		{"stamped folder itself", stamped},
		{"own stamp under stamped parent", own},
		{"under unstamped parent", makeDir(t, filepath.Join(home, "plain", "child"))},
		{"under stamped home", makeDir(t, filepath.Join(home, "session"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertStartForwarded(t, home, tc.dir, resolvedPath(t, tc.dir))
		})
	}
}

func TestControlForwardsStartsBelowFilesThatAreNoStamp(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"no env", `{"permissions": {}}`},
		{"env without base URL", `{"env": {"OTHER": "synthetic"}}`},
		{"empty base URL", `{"env": {"ANTHROPIC_BASE_URL": ""}}`},
		{"base URL not a string", `{"env": {"ANTHROPIC_BASE_URL": 1}}`},
		{"lowercase key", `{"env": {"anthropic_base_url": "https://synthetic.example"}}`},
		{"env not an object", `{"env": "https://synthetic.example"}`},
		{"not JSON", `{"env": `},
	}
	for _, name := range stampFileNames() {
		for _, tc := range cases {
			t.Run(name+" "+tc.name, func(t *testing.T) {
				home := t.TempDir()
				parent := makeDir(t, filepath.Join(home, "parent"))
				writeSettings(t, parent, name, tc.content)
				child := makeDir(t, filepath.Join(parent, "child"))
				assertStartForwarded(t, home, child, resolvedPath(t, child))
			})
		}
	}
}

// claudeAsFile makes folder's .claude a regular file, so no settings file can sit under it.
func claudeAsFile(t *testing.T, folder string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(folder, ".claude"), []byte("not a folder"), 0o644); err != nil {
		t.Fatalf("write .claude in %s: %v", folder, err)
	}
}

func TestControlForwardsStartsBelowClaudeFileWithNoStampAbove(t *testing.T) {
	home := t.TempDir()
	parent := makeDir(t, filepath.Join(home, "parent"))
	claudeAsFile(t, parent)
	child := makeDir(t, filepath.Join(parent, "child"))
	assertStartForwarded(t, home, child, resolvedPath(t, child))
}

func TestControlRefusesStartsBelowStampWithClaudeFile(t *testing.T) {
	home := t.TempDir()
	stamped := makeDir(t, filepath.Join(home, "stamped"))
	writeSettings(t, stamped, "settings.json", stampSettings)
	// A .claude that is a regular file is no stamp and no unreadable stamp: the folder above still names the refusal.
	middle := makeDir(t, filepath.Join(stamped, "middle"))
	claudeAsFile(t, middle)
	cases := []struct {
		name string
		dir  string
	}{
		{"folder whose .claude is a file", middle},
		{"child of folder whose .claude is a file", makeDir(t, filepath.Join(middle, "child"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeEngine{}
			control := controlClient(t, newHomedControlHandler(t, home, newFakeOrreryd(t, fake), 0))

			_, err := startAt(t, control, tc.dir)
			assertConnectError(t, err, connect.CodeInvalidArgument, belowStampMessage(resolvedPath(t, stamped)))
			if calls := fake.recorded(); len(calls) != 0 {
				t.Fatalf("refused start forwarded %d requests, want none", len(calls))
			}
		})
	}
}

func TestControlRefusesStartsWithUnreadableStamp(t *testing.T) {
	home := t.TempDir()
	parent := makeDir(t, filepath.Join(home, "parent"))
	child := makeDir(t, filepath.Join(parent, "child"))
	own := makeDir(t, filepath.Join(home, "own"))
	// A settings file that is a directory exists but cannot be read as a file.
	makeDir(t, filepath.Join(parent, ".claude", "settings.json"))
	makeDir(t, filepath.Join(own, ".claude", "settings.json"))

	cases := []struct {
		name   string
		dir    string
		folder string
	}{
		{"unreadable stamp above", child, parent},
		{"unreadable stamp in the folder", own, own},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeEngine{}
			control := controlClient(t, newHomedControlHandler(t, home, newFakeOrreryd(t, fake), 0))

			_, err := startAt(t, control, tc.dir)
			unreadable := filepath.Join(resolvedPath(t, tc.folder), ".claude", "settings.json")
			assertConnectError(t, err, connect.CodeInvalidArgument,
				"session directory's context stamp "+unreadable+" cannot be read")
			if calls := fake.recorded(); len(calls) != 0 {
				t.Fatalf("refused start forwarded %d requests, want none", len(calls))
			}
		})
	}
}
