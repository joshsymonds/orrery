package cli_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/cli"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// sessionFakeID is the id the fake Engine returns from Start.
const sessionFakeID = "session-7"

// sessionFake is an in-process Engine. It records every request it receives, in order, and returns sayErr from Say.
type sessionFake struct {
	enginev1connect.UnimplementedEngineHandler
	sayErr error

	mu    sync.Mutex
	calls []proto.Message
}

// record appends msg to the requests received.
func (f *sessionFake) record(msg proto.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, msg)
}

// received returns the requests received so far.
func (f *sessionFake) received() []proto.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]proto.Message(nil), f.calls...)
}

func (f *sessionFake) Start(
	_ context.Context,
	req *connect.Request[v1.StartRequest],
) (*connect.Response[v1.StartResponse], error) {
	f.record(req.Msg)
	return connect.NewResponse(&v1.StartResponse{Id: sessionFakeID}), nil
}

func (f *sessionFake) Say(
	_ context.Context,
	req *connect.Request[v1.SayRequest],
) (*connect.Response[v1.SayResponse], error) {
	f.record(req.Msg)
	if f.sayErr != nil {
		return nil, f.sayErr
	}
	return connect.NewResponse(&v1.SayResponse{}), nil
}

func (f *sessionFake) Answer(
	_ context.Context,
	req *connect.Request[v1.AnswerRequest],
) (*connect.Response[v1.AnswerResponse], error) {
	f.record(req.Msg)
	return connect.NewResponse(&v1.AnswerResponse{}), nil
}

func (f *sessionFake) Interrupt(
	_ context.Context,
	req *connect.Request[v1.InterruptRequest],
) (*connect.Response[v1.InterruptResponse], error) {
	f.record(req.Msg)
	return connect.NewResponse(&v1.InterruptResponse{}), nil
}

func (f *sessionFake) Stop(
	_ context.Context,
	req *connect.Request[v1.StopRequest],
) (*connect.Response[v1.StopResponse], error) {
	f.record(req.Msg)
	return connect.NewResponse(&v1.StopResponse{}), nil
}

func (f *sessionFake) Seen(
	_ context.Context,
	req *connect.Request[v1.SeenRequest],
) (*connect.Response[v1.SeenResponse], error) {
	f.record(req.Msg)
	return connect.NewResponse(&v1.SeenResponse{}), nil
}

// sessionServe serves fake on a unix socket at the session socket path beside a daemon socket in a short temp
// directory. It returns that session socket path. The server and directory are removed at cleanup.
func sessionServe(t *testing.T, fake *sessionFake) string {
	t.Helper()
	socket := protocol.SessionSocketPath(filepath.Join(t.TempDir(), "orreryd.sock"))
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen on %s: %v", socket, err)
	}
	mux := http.NewServeMux()
	path, handler := enginev1connect.NewEngineHandler(fake)
	mux.Handle(path, handler)
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("close session server: %v", err)
		}
	})
	return socket
}

func TestSessionVerbsSendTheirRequest(t *testing.T) {
	const rel = "sub/dir"
	absDir, err := filepath.Abs(rel)
	if err != nil {
		t.Fatalf("Abs(%q): %v", rel, err)
	}
	tests := []struct {
		name string
		args []string
		want proto.Message
	}{
		{
			name: "start",
			args: []string{"start", rel, "first prompt"},
			want: &v1.StartRequest{Dir: absDir, Prompt: "first prompt"},
		},
		{
			name: "say",
			args: []string{"say", "s1", "hello there"},
			want: &v1.SayRequest{Id: "s1", Text: "hello there"},
		},
		{
			name: "answer",
			args: []string{"answer", "s1", "q-3", "Option B"},
			want: &v1.AnswerRequest{Id: "s1", RequestId: "q-3", Answer: "Option B"},
		},
		{
			name: "interrupt",
			args: []string{"interrupt", "s1"},
			want: &v1.InterruptRequest{Id: "s1"},
		},
		{
			name: "stop",
			args: []string{"stop", "s1"},
			want: &v1.StopRequest{Id: "s1"},
		},
		{
			name: "seen",
			args: []string{"seen", "s1"},
			want: &v1.SeenRequest{Id: "s1"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &sessionFake{}
			socket := sessionServe(t, fake)
			var out strings.Builder
			if err := cli.Session(t.Context(), socket, tc.args, &out, func(string) string { return "" }); err != nil {
				t.Fatalf("Session(%v): %v", tc.args, err)
			}
			got := fake.received()
			if len(got) != 1 {
				t.Fatalf("Session(%v) sent %d requests, want 1", tc.args, len(got))
			}
			if !proto.Equal(got[0], tc.want) {
				t.Errorf("request = %v, want %v", got[0], tc.want)
			}
			if tc.name == "start" {
				if want := sessionFakeID + "\n"; out.String() != want {
					t.Errorf("start printed %q, want %q", out.String(), want)
				}
			} else if out.Len() != 0 {
				t.Errorf("%s printed %q, want nothing", tc.name, out.String())
			}
		})
	}
}

func TestSessionRPCErrorReportsItsMessage(t *testing.T) {
	const message = "no session s9 to say to"
	fake := &sessionFake{sayErr: connect.NewError(connect.CodeNotFound, errors.New(message))}
	socket := sessionServe(t, fake)
	err := cli.Session(t.Context(), socket, []string{"say", "s9", "x"}, &strings.Builder{}, nil)
	if err == nil {
		t.Fatal("Session on a refused say returned nil")
	}
	if !strings.Contains(err.Error(), message) {
		t.Errorf("error %q does not carry the RPC message %q", err, message)
	}
	if errors.Is(err, cli.ErrUsage) {
		t.Errorf("RPC error %q matches ErrUsage", err)
	}
}

func TestSessionMissingSocketNamesSessionsFlag(t *testing.T) {
	socket := protocol.SessionSocketPath(filepath.Join(t.TempDir(), "orreryd.sock"))
	err := cli.Session(t.Context(), socket, []string{"seen", "s1"}, &strings.Builder{}, nil)
	if err == nil {
		t.Fatal("Session on a missing socket returned nil")
	}
	if !strings.Contains(err.Error(), "--sessions") {
		t.Errorf("error %q does not name the --sessions flag", err)
	}
	if !strings.Contains(err.Error(), socket) {
		t.Errorf("error %q does not name the socket %s", err, socket)
	}
}

func TestSessionWrongFormsMatchErrUsage(t *testing.T) {
	forms := [][]string{
		{},
		{"bogus"},
		{"start"},
		{"start", "dir"},
		{"start", "dir", "prompt", "extra"},
		{"say"},
		{"say", "s1"},
		{"say", "s1", "text", "extra"},
		{"answer", "s1"},
		{"answer", "s1", "q1"},
		{"answer", "s1", "q1", "allow", "extra"},
		{"interrupt"},
		{"interrupt", "s1", "extra"},
		{"stop"},
		{"stop", "s1", "extra"},
		{"seen"},
		{"seen", "s1", "extra"},
		{"watch"},
		{"watch", "s1", "extra"},
	}
	// The socket never exists, so a form that dials would fail with a dial error instead of a usage error.
	socket := filepath.Join(t.TempDir(), "sessions.sock")
	for _, args := range forms {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			err := cli.Session(t.Context(), socket, args, &strings.Builder{}, func(string) string { return "" })
			if !errors.Is(err, cli.ErrUsage) {
				t.Errorf("Session(%v) = %v, want an error matching ErrUsage", args, err)
			}
		})
	}
}
