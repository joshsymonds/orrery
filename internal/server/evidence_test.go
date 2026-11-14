package server_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/server"
)

// artifactFake is the EvidenceClient of the Artifact tests. GetEvidenceArtifact answers the bytes "bytes of " and the
// name, or failure when it is set, and records each request it receives. ListEvidence answers unimplemented.
type artifactFake struct {
	mu       sync.Mutex
	failure  error
	requests []*enginev1.GetEvidenceArtifactRequest
}

func (f *artifactFake) ListEvidence(
	context.Context,
	*connect.Request[enginev1.ListEvidenceRequest],
) (*connect.Response[enginev1.ListEvidenceResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

func (f *artifactFake) GetEvidenceArtifact(
	_ context.Context,
	req *connect.Request[enginev1.GetEvidenceArtifactRequest],
) (*connect.Response[enginev1.GetEvidenceArtifactResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req.Msg)
	if f.failure != nil {
		return nil, f.failure
	}
	data := []byte("bytes of " + req.Msg.GetName())
	return connect.NewResponse(&enginev1.GetEvidenceArtifactResponse{Data: data}), nil
}

// received returns the requests the fake has received, in order.
func (f *artifactFake) received() []*enginev1.GetEvidenceArtifactRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*enginev1.GetEvidenceArtifactRequest(nil), f.requests...)
}

// evidenceHandler returns the guarded handler of server.New with evidence as its Evidence client, which admits
// allowedLogin.
func evidenceHandler(t *testing.T, evidence server.EvidenceClient) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:   []string{allowedLogin},
		Tmux:     newTmuxClient(t),
		Push:     newPushStore(t),
		Evidence: evidence,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// evidenceServe serves handler and returns a generated Evidence client whose requests carry the login and client
// headers a phone request carries.
func evidenceServe(t *testing.T, handler http.Handler) orreryv1connect.EvidenceClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: wiringHeaders{next: http.DefaultTransport}}
	return orreryv1connect.NewEvidenceClient(httpClient, srv.URL)
}

// requireEvidenceError asserts err is a Connect error with code and message.
func requireEvidenceError(t *testing.T, err error, code connect.Code, message string) {
	t.Helper()
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("error = %v, want a connect error", err)
	}
	if connectErr.Code() != code {
		t.Fatalf("code = %v, want %v", connectErr.Code(), code)
	}
	if connectErr.Message() != message {
		t.Fatalf("message = %q, want %q", connectErr.Message(), message)
	}
}

func TestEvidenceArtifactAnswersBytesWithMediaType(t *testing.T) {
	t.Parallel()
	fake := &artifactFake{}
	client := evidenceServe(t, evidenceHandler(t, fake))
	cases := []struct {
		name string
		want string
	}{
		{name: "out/screen.png", want: "image/png"},
		{name: "out/photo.jpg", want: "image/jpeg"},
		{name: "out/photo.jpeg", want: "image/jpeg"},
		{name: "out/anim.gif", want: "image/gif"},
		{name: "out/shot.webp", want: "image/webp"},
		{name: "out/notes.txt", want: "text/plain; charset=utf-8"},
		{name: "out/run.log", want: "text/plain; charset=utf-8"},
		{name: "out/result.json", want: "application/json"},
		{name: "out/blob.bin", want: "application/octet-stream"},
		{name: "", want: "text/plain; charset=utf-8"},
	}
	for _, tc := range cases {
		resp, err := client.Artifact(t.Context(), connect.NewRequest(&orreryv1.EvidenceArtifactRequest{
			Run:  "synthetic-run",
			Name: tc.name,
		}))
		if err != nil {
			t.Fatalf("Artifact %q: %v", tc.name, err)
		}
		if got := string(resp.Msg.GetData()); got != "bytes of "+tc.name || resp.Msg.GetMediaType() != tc.want {
			t.Errorf("Artifact %q = data %q media type %q, want data %q media type %q",
				tc.name, got, resp.Msg.GetMediaType(), "bytes of "+tc.name, tc.want)
		}
	}
	reqs := fake.received()
	if len(reqs) != len(cases) {
		t.Fatalf("GetEvidenceArtifact calls = %d, want %d", len(reqs), len(cases))
	}
	for i, tc := range cases {
		if reqs[i].GetRun() != "synthetic-run" || reqs[i].GetName() != tc.name {
			t.Errorf("call %d = run %q name %q, want synthetic-run %q", i, reqs[i].GetRun(), reqs[i].GetName(), tc.name)
		}
	}
}

func TestEvidenceArtifactPassesNotFoundAndInvalidArgumentThrough(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		code    connect.Code
		message string
	}{
		{name: "not found", code: connect.CodeNotFound, message: "synthetic artifact not found"},
		{name: "invalid argument", code: connect.CodeInvalidArgument, message: "synthetic artifact name"},
	}
	for _, tc := range cases {
		fake := &artifactFake{failure: connect.NewError(tc.code, errors.New(tc.message))}
		client := evidenceServe(t, evidenceHandler(t, fake))

		_, err := client.Artifact(t.Context(), connect.NewRequest(&orreryv1.EvidenceArtifactRequest{
			Run:  "synthetic-run",
			Name: "out/missing.png",
		}))
		requireEvidenceError(t, err, tc.code, tc.message)
	}
}

func TestEvidenceArtifactWithoutClientIsUnavailable(t *testing.T) {
	t.Parallel()
	client := evidenceServe(t, evidenceHandler(t, nil))

	_, err := client.Artifact(t.Context(), connect.NewRequest(&orreryv1.EvidenceArtifactRequest{
		Run:  "synthetic-run",
		Name: "out/screen.png",
	}))
	requireEvidenceError(t, err, connect.CodeUnavailable, "evidence service unavailable")
}

func TestEvidenceArtifactRefusesUnadmittedRequests(t *testing.T) {
	t.Parallel()
	fake := &artifactFake{}
	handler := evidenceHandler(t, fake)
	probes := []struct {
		name   string
		header http.Header
	}{
		{name: "stranger login", header: connectHeader(otherLogin, "application/proto", true)},
		{name: "no client header", header: connectHeader(allowedLogin, "application/proto", false)},
	}
	for _, probe := range probes {
		rec := serve(t, handler, http.MethodPost, orreryv1connect.EvidenceArtifactProcedure, probe.header, "")
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", probe.name, rec.Code)
		}
	}
	if n := len(fake.received()); n != 0 {
		t.Errorf("GetEvidenceArtifact calls = %d, want 0 for refused requests", n)
	}
}
