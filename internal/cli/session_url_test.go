package cli_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/cli"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

// sessionURLClientHeader is the header every request to serve carries. The stand-in refuses a request without it.
const sessionURLClientHeader = "X-Orrery-Client"

// urlFake is the serve stand-in. Its Control handler records each request and returns controlErr from every RPC.
// Its Sessions handler lists listSeq, and its Events handler streams the watchMsgs after the cursor it was asked from,
// and records that cursor. A cursor below prunedBefore is out of range, as serve answers a cursor older than its log.
type urlFake struct {
	controlErr   error
	listSeq      int64
	prunedBefore int64
	watchMsgs    []*orreryv1.WatchResponse

	mu         sync.Mutex
	calls      []proto.Message
	watchAfter int64
}

// record appends msg to the requests received.
func (f *urlFake) record(msg proto.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, msg)
}

// received returns the requests received so far.
func (f *urlFake) received() []proto.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]proto.Message(nil), f.calls...)
}

// controlReply records msg and answers with resp, or with controlErr when it is set.
func controlReply[Resp any](f *urlFake, msg proto.Message, resp *Resp) (*connect.Response[Resp], error) {
	f.record(msg)
	if f.controlErr != nil {
		return nil, f.controlErr
	}
	return connect.NewResponse(resp), nil
}

func (f *urlFake) Start(
	_ context.Context,
	req *connect.Request[orreryv1.StartRequest],
) (*connect.Response[orreryv1.StartResponse], error) {
	return controlReply(f, req.Msg, &orreryv1.StartResponse{Id: sessionFakeID})
}

func (f *urlFake) Say(
	_ context.Context,
	req *connect.Request[orreryv1.SayRequest],
) (*connect.Response[orreryv1.SayResponse], error) {
	return controlReply(f, req.Msg, &orreryv1.SayResponse{})
}

func (f *urlFake) Answer(
	_ context.Context,
	req *connect.Request[orreryv1.AnswerRequest],
) (*connect.Response[orreryv1.AnswerResponse], error) {
	return controlReply(f, req.Msg, &orreryv1.AnswerResponse{})
}

func (f *urlFake) Interrupt(
	_ context.Context,
	req *connect.Request[orreryv1.InterruptRequest],
) (*connect.Response[orreryv1.InterruptResponse], error) {
	return controlReply(f, req.Msg, &orreryv1.InterruptResponse{})
}

func (f *urlFake) Stop(
	_ context.Context,
	req *connect.Request[orreryv1.StopRequest],
) (*connect.Response[orreryv1.StopResponse], error) {
	return controlReply(f, req.Msg, &orreryv1.StopResponse{})
}

func (f *urlFake) Seen(
	_ context.Context,
	req *connect.Request[orreryv1.SeenRequest],
) (*connect.Response[orreryv1.SeenResponse], error) {
	return controlReply(f, req.Msg, &orreryv1.SeenResponse{})
}

func (f *urlFake) List(
	context.Context,
	*connect.Request[orreryv1.ListRequest],
) (*connect.Response[orreryv1.ListResponse], error) {
	return connect.NewResponse(&orreryv1.ListResponse{Seq: f.listSeq}), nil
}

func (f *urlFake) Watch(
	_ context.Context,
	req *connect.Request[orreryv1.WatchRequest],
	stream *connect.ServerStream[orreryv1.WatchResponse],
) error {
	after := req.Msg.GetAfter()
	f.mu.Lock()
	f.watchAfter = after
	f.mu.Unlock()
	if after < f.prunedBefore {
		return connect.NewError(connect.CodeOutOfRange, errors.New("cursor is older than the log"))
	}
	for _, msg := range f.watchMsgs {
		if msg.GetSeq() <= after {
			continue
		}
		if err := stream.Send(msg); err != nil {
			return err
		}
	}
	return nil
}

// watchedAfter returns the cursor of the Watch request received.
func (f *urlFake) watchedAfter() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.watchAfter
}

// serveURLFake serves fake's Control, Sessions and Events handlers on an httptest server and returns its URL. Every
// request without the client header is refused, as serve refuses it.
func serveURLFake(t *testing.T, fake *urlFake) string {
	t.Helper()
	mux := http.NewServeMux()
	controlPath, controlHandler := orreryv1connect.NewControlHandler(fake)
	mux.Handle(controlPath, controlHandler)
	sessionsPath, sessionsHandler := orreryv1connect.NewSessionsHandler(fake)
	mux.Handle(sessionsPath, sessionsHandler)
	eventsPath, eventsHandler := orreryv1connect.NewEventsHandler(fake)
	mux.Handle(eventsPath, eventsHandler)
	srv := httptest.NewServer(requireClientHeader(mux))
	t.Cleanup(srv.Close)
	return srv.URL
}

// requireClientHeader refuses a request that lacks the client header, and passes every other request to next.
func requireClientHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionURLClientHeader) == "" {
			http.Error(w, "missing client header", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sessionURLCall runs `session --url url ARGS` against a session socket that does not exist, so a call that dials
// the socket fails the test. It returns what the command wrote and the error it returned.
func sessionURLCall(t *testing.T, url string, args ...string) (string, error) {
	t.Helper()
	var out strings.Builder
	socket := filepath.Join(t.TempDir(), "sessions.sock")
	err := cli.Session(t.Context(), socket, append([]string{"--url", url}, args...), &out, nil)
	return out.String(), err
}

func TestSessionURLVerbsSendTheirRequest(t *testing.T) {
	const rel = "sub/dir"
	tests := []struct {
		name string
		args []string
		want proto.Message
	}{
		{
			name: "start",
			args: []string{"start", rel, "first prompt"},
			want: &orreryv1.StartRequest{Dir: rel, Prompt: "first prompt"},
		},
		{
			name: "say",
			args: []string{"say", "s1", "hello there"},
			want: &orreryv1.SayRequest{Id: "s1", Text: "hello there"},
		},
		{
			name: "answer",
			args: []string{"answer", "s1", "q-3", "Option B"},
			want: &orreryv1.AnswerRequest{Id: "s1", RequestId: "q-3", Answer: "Option B"},
		},
		{
			name: "interrupt",
			args: []string{"interrupt", "s1"},
			want: &orreryv1.InterruptRequest{Id: "s1"},
		},
		{
			name: "stop",
			args: []string{"stop", "s1"},
			want: &orreryv1.StopRequest{Id: "s1"},
		},
		{
			name: "seen",
			args: []string{"seen", "s1"},
			want: &orreryv1.SeenRequest{Id: "s1"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &urlFake{}
			url := serveURLFake(t, fake)
			out, err := sessionURLCall(t, url, tc.args...)
			if err != nil {
				t.Fatalf("Session(--url %v): %v", tc.args, err)
			}
			got := fake.received()
			if len(got) != 1 {
				t.Fatalf("Session(--url %v) sent %d requests, want 1", tc.args, len(got))
			}
			if !proto.Equal(got[0], tc.want) {
				t.Errorf("request = %v, want %v", got[0], tc.want)
			}
			if tc.name == "start" {
				if want := sessionFakeID + "\n"; out != want {
					t.Errorf("start printed %q, want %q", out, want)
				}
			} else if out != "" {
				t.Errorf("%s printed %q, want nothing", tc.name, out)
			}
		})
	}
}

func TestSessionURLStartSendsModel(t *testing.T) {
	fake := &urlFake{}
	url := serveURLFake(t, fake)
	out, err := sessionURLCall(t, url, "start", "--model", "haiku", "sub/dir", "first prompt")
	if err != nil {
		t.Fatalf("Session start --model over --url: %v", err)
	}
	want := &orreryv1.StartRequest{Dir: "sub/dir", Prompt: "first prompt", Model: "haiku"}
	got := fake.received()
	if len(got) != 1 || !proto.Equal(got[0], want) {
		t.Fatalf("requests = %v, want one %v", got, want)
	}
	if out != sessionFakeID+"\n" {
		t.Errorf("start printed %q, want %q", out, sessionFakeID+"\n")
	}
}

func TestSessionLocalStartSendsModel(t *testing.T) {
	const rel = "sub/dir"
	absDir, err := filepath.Abs(rel)
	if err != nil {
		t.Fatalf("Abs(%q): %v", rel, err)
	}
	fake := &sessionFake{}
	socket := sessionServe(t, fake)
	var out strings.Builder
	args := []string{"start", "--model", "haiku", rel, "first prompt"}
	if err := cli.Session(t.Context(), socket, args, &out, nil); err != nil {
		t.Fatalf("Session(%v): %v", args, err)
	}
	want := &v1.StartRequest{Dir: absDir, Prompt: "first prompt", Model: "haiku"}
	got := fake.received()
	if len(got) != 1 || !proto.Equal(got[0], want) {
		t.Fatalf("requests = %v, want one %v", got, want)
	}
}

func TestSessionURLRPCErrorReportsItsMessage(t *testing.T) {
	const message = "no session s9 to say to"
	fake := &urlFake{controlErr: connect.NewError(connect.CodeNotFound, errors.New(message))}
	url := serveURLFake(t, fake)
	_, err := sessionURLCall(t, url, "say", "s9", "x")
	if err == nil {
		t.Fatal("Session on a refused say returned nil")
	}
	if want := "session say: " + message; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if errors.Is(err, cli.ErrUsage) {
		t.Errorf("RPC error %q matches ErrUsage", err)
	}
}

func TestSessionURLWrongFormsMatchErrUsage(t *testing.T) {
	// The URL points at a closed loopback port, so a form that reaches the network would fail with a transport
	// error instead of a usage error.
	const url = "http://127.0.0.1:1"
	forms := []struct {
		args []string
		want string
	}{
		{[]string{"--url"}, "--url needs a URL"},
		{[]string{"--url", ""}, "--url needs a URL"},
		{[]string{"--url", url}, "want a verb"},
		{[]string{"--url", url, "bogus"}, `unknown verb "bogus"`},
		{[]string{"--url", url, "start"}, "session start: want"},
		{[]string{"--url", url, "start", "dir"}, "session start: want"},
		{[]string{"--url", url, "start", "--model"}, "session start: want"},
		{[]string{"--url", url, "start", "dir", "prompt", "extra"}, "session start: want"},
		{[]string{"--url", url, "say"}, "session say: want"},
		{[]string{"--url", url, "answer", "s1", "q1"}, "session answer: want"},
		{[]string{"--url", url, "interrupt"}, "session interrupt: want"},
		{[]string{"--url", url, "stop", "s1", "extra"}, "session stop: want"},
		{[]string{"--url", url, "seen"}, "session seen: want"},
		{[]string{"--url", url, "watch"}, "session watch: want"},
		{[]string{"--url", url, "watch", "s1", "extra"}, "session watch: want"},
	}
	for _, tc := range forms {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "sessions.sock")
			err := cli.Session(t.Context(), socket, tc.args, &strings.Builder{}, nil)
			if !errors.Is(err, cli.ErrUsage) {
				t.Fatalf("Session(%v) = %v, want an error matching ErrUsage", tc.args, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Session(%v) = %q, want a usage error containing %q", tc.args, err, tc.want)
			}
		})
	}
}

func TestSessionLocalStartWithModelNeedsValue(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "sessions.sock")
	err := cli.Session(t.Context(), socket, []string{"start", "--model"}, &strings.Builder{}, nil)
	if !errors.Is(err, cli.ErrUsage) {
		t.Fatalf("Session(start --model) = %v, want an error matching ErrUsage", err)
	}
	if !strings.Contains(err.Error(), "[--model MODEL] DIR PROMPT") {
		t.Errorf("Session(start --model) = %q, want the form that names --model", err)
	}
}

// watchAdded returns an engine_added event for session id in the running state, stored at seq.
func watchAdded(seq int64, id string) *orreryv1.WatchResponse {
	return &orreryv1.WatchResponse{
		Seq:       seq,
		SessionId: id,
		At:        eventsAt,
		Event: &orreryv1.WatchResponse_EngineAdded{
			EngineAdded: &orreryv1.EngineAdded{Dir: "/work", State: "running"},
		},
	}
}

// watchState returns an engine_changed event for session id that sets its state to state, stored at seq.
func watchState(seq int64, id, state string) *orreryv1.WatchResponse {
	return &orreryv1.WatchResponse{
		Seq:       seq,
		SessionId: id,
		At:        eventsAt,
		Event: &orreryv1.WatchResponse_EngineChanged{
			EngineChanged: &orreryv1.EngineChanged{State: proto.String(state)},
		},
	}
}

// checkWatchLines checks that out holds one compact protojson line for each message in want, in order.
func checkWatchLines(t *testing.T, out string, want ...*orreryv1.WatchResponse) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if out == "" {
		lines = nil
	}
	if len(lines) != len(want) {
		t.Fatalf("watch wrote %d lines, want %d: %q", len(lines), len(want), out)
	}
	for i, line := range lines {
		var got orreryv1.WatchResponse
		if err := protojson.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("line %d %q does not decode: %v", i, line, err)
		}
		if !proto.Equal(&got, want[i]) {
			t.Errorf("line %d = %v, want %v", i, &got, want[i])
		}
	}
}

func TestSessionURLWatchWritesOnlyTheSessionThroughStop(t *testing.T) {
	// The newest seq is 2. Seq 3 is past it and stops s1, so watch returns there and never reads seq 4.
	first := watchAdded(1, "s1")
	fake := &urlFake{
		listSeq: 2,
		watchMsgs: []*orreryv1.WatchResponse{
			first,
			watchAdded(2, "s2"),
			watchState(3, "s1", "stopped"),
			watchState(4, "s2", "stopped"),
		},
	}
	url := serveURLFake(t, fake)
	out, err := sessionURLCall(t, url, "watch", "s1")
	if err != nil {
		t.Fatalf("Session watch: %v", err)
	}
	if after := fake.watchedAfter(); after != 0 {
		t.Errorf("Watch requested after %d, want 0", after)
	}
	checkWatchLines(t, out, first, watchState(3, "s1", "stopped"))
}

func TestSessionURLWatchKeepsReadingUntilStopped(t *testing.T) {
	// The newest seq is 2. Seq 1 stops s1 before the newest seq, so it does not end watch. Seq 2 is past the newest
	// seq and leaves s1 running, so watch keeps reading to seq 3, which fails s1.
	msgs := []*orreryv1.WatchResponse{
		watchState(1, "s1", "stopped"),
		watchState(2, "s1", "running"),
		watchState(3, "s1", "failed"),
	}
	fake := &urlFake{listSeq: 2, watchMsgs: msgs}
	url := serveURLFake(t, fake)
	out, err := sessionURLCall(t, url, "watch", "s1")
	if err != nil {
		t.Fatalf("Session watch: %v", err)
	}
	checkWatchLines(t, out, msgs...)
}

func TestSessionURLWatchStreamEndingBeforeStopIsError(t *testing.T) {
	fake := &urlFake{listSeq: 0, watchMsgs: []*orreryv1.WatchResponse{watchAdded(1, "s1")}}
	url := serveURLFake(t, fake)
	out, err := sessionURLCall(t, url, "watch", "s1")
	if err == nil || !strings.Contains(err.Error(), "stream ended") {
		t.Fatalf("Session watch returned %v, want an error naming the ended stream", err)
	}
	checkWatchLines(t, out, watchAdded(1, "s1"))
}

func TestSessionURLWatchFallsBackToNewestSeqWhenCursorZeroIsOutOfRange(t *testing.T) {
	// Seqs 1 to 3 are pruned, so cursor 0 is out of range and serve refuses it. The newest seq is 3, so watch reopens
	// after 3 and gets seq 4, which stops s1 past the newest seq, and returns there without reading seq 5.
	stopped := watchState(4, "s1", "stopped")
	fake := &urlFake{
		listSeq:      3,
		prunedBefore: 3,
		watchMsgs:    []*orreryv1.WatchResponse{stopped, watchState(5, "s2", "stopped")},
	}
	url := serveURLFake(t, fake)
	out, err := sessionURLCall(t, url, "watch", "s1")
	if err != nil {
		t.Fatalf("Session watch: %v", err)
	}
	if after := fake.watchedAfter(); after != 3 {
		t.Errorf("last Watch requested after %d, want the newest seq 3", after)
	}
	checkWatchLines(t, out, stopped)
}
