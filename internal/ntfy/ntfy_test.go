package ntfy_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/ntfy"
	"github.com/joshsymonds/orrery/internal/protocol"
)

const (
	testHost    = "test-host"
	testProject = "example-project"
	testSession = "demo"
	testWindow  = 2
	testToken   = "test-token"
	testBody    = "Example body text."
	topicPath   = "/demo-topic"
	// unresolvableURL is never dialed: New only reads the topic URL, and the .invalid TLD cannot resolve.
	unresolvableURL = "http://test-host.invalid/demo-topic"
)

// received is one request observed by the fake ntfy server.
type received struct {
	method string
	path   string
	header http.Header
	body   string
}

// fakeNtfy starts an httptest server that records each request on the returned channel and replies with status.
func fakeNtfy(t *testing.T, status int) (*httptest.Server, chan received) {
	t.Helper()
	requests := make(chan received, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		requests <- received{method: req.Method, path: req.URL.Path, header: req.Header.Clone(), body: string(body)}
		writer.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, requests
}

// writeFile writes content to a fresh file under t.TempDir and returns its path.
func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// topicFile returns a topic URL file pointing at srv's topic path.
func topicFile(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	return writeFile(t, "topic.url", srv.URL+topicPath+"\n")
}

// envFor returns a getenv that maps the two ntfy variables to the given file paths.
func envFor(urlFile, tokenFile string) func(string) string {
	values := map[string]string{
		protocol.EnvNtfyURLFile:  urlFile,
		protocol.EnvNtfyAuthFile: tokenFile,
	}
	return func(key string) string { return values[key] }
}

func TestNtfyPublish(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		state    protocol.State
		priority string
		tags     string
	}{
		{name: "waiting", state: protocol.StateWaiting, priority: "5", tags: "question," + testHost},
		{name: "done", state: protocol.StateDone, priority: "4", tags: "white_check_mark," + testHost},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, requests := fakeNtfy(t, http.StatusOK)
			pub, err := ntfy.New(
				envFor(topicFile(t, srv), writeFile(t, "token", testToken+"\n")),
				testHost,
				srv.Client(),
			)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if err := pub.Publish(
				context.Background(),
				testProject,
				testSession,
				testWindow,
				tc.state,
				testBody,
			); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			got := <-requests
			if got.method != http.MethodPost {
				t.Errorf("method = %q, want POST", got.method)
			}
			if got.path != topicPath {
				t.Errorf("path = %q, want %q", got.path, topicPath)
			}
			want := map[string]string{
				"Title":         "example-project · demo:2",
				"Priority":      tc.priority,
				"Tags":          tc.tags,
				"Authorization": "Bearer " + testToken,
			}
			for key, value := range want {
				if gotValue := got.header.Get(key); gotValue != value {
					t.Errorf("header %s = %q, want %q", key, gotValue, value)
				}
			}
			if got.body != testBody {
				t.Errorf("body = %q, want %q", got.body, testBody)
			}
		})
	}
}

func TestNtfyPublishWithoutTokenSendsNoAuthorization(t *testing.T) {
	t.Parallel()
	srv, requests := fakeNtfy(t, http.StatusOK)
	pub, err := ntfy.New(envFor(topicFile(t, srv), ""), testHost, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := pub.Publish(
		context.Background(),
		testProject,
		testSession,
		testWindow,
		protocol.StateDone,
		testBody,
	); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := <-requests
	if auth := got.header.Get("Authorization"); auth != "" {
		t.Errorf("Authorization = %q, want none", auth)
	}
}

func TestNewNotConfiguredWhenURLUnset(t *testing.T) {
	t.Parallel()
	unset := func(string) string { return "" }
	_, err := ntfy.New(unset, testHost, http.DefaultClient)
	if !errors.Is(err, ntfy.ErrNotConfigured) {
		t.Fatalf("New error = %v, want ErrNotConfigured", err)
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		urlFile   string
		tokenFile string
		client    *http.Client
	}{
		{
			name:    "missing topic file",
			urlFile: filepath.Join(t.TempDir(), "missing"),
			client:  http.DefaultClient,
		},
		{
			name:    "blank topic file",
			urlFile: writeFile(t, "topic.url", "  \n"),
			client:  http.DefaultClient,
		},
		{
			name:      "missing token file",
			urlFile:   writeFile(t, "topic.url", unresolvableURL+"\n"),
			tokenFile: filepath.Join(t.TempDir(), "missing"),
			client:    http.DefaultClient,
		},
		{
			name:    "nil client",
			urlFile: writeFile(t, "topic.url", unresolvableURL+"\n"),
			client:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ntfy.New(envFor(tc.urlFile, tc.tokenFile), testHost, tc.client); err == nil {
				t.Fatal("New returned nil error, want an error")
			}
		})
	}
}

func TestPublishRejectsWorkingState(t *testing.T) {
	t.Parallel()
	srv, requests := fakeNtfy(t, http.StatusOK)
	pub, err := ntfy.New(envFor(topicFile(t, srv), ""), testHost, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := pub.Publish(
		context.Background(),
		testProject,
		testSession,
		testWindow,
		protocol.StateWorking,
		testBody,
	); err == nil {
		t.Fatal("Publish(working) returned nil error, want an error")
	}
	if n := len(requests); n != 0 {
		t.Errorf("working state sent %d requests, want none", n)
	}
}

func TestPublishReportsNonSuccessStatus(t *testing.T) {
	t.Parallel()
	srv, _ := fakeNtfy(t, http.StatusInternalServerError)
	pub, err := ntfy.New(envFor(topicFile(t, srv), ""), testHost, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := pub.Publish(
		context.Background(),
		testProject,
		testSession,
		testWindow,
		protocol.StateWaiting,
		testBody,
	); err == nil {
		t.Fatal("Publish returned nil error for a 500 response, want an error")
	}
}

func TestPublishSession(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		state    string
		priority string
		tags     string
	}{
		{name: "waiting", state: "waiting", priority: "5", tags: "question," + testHost},
		{name: "done", state: "done", priority: "4", tags: "white_check_mark," + testHost},
		{name: "failed", state: "failed", priority: "4", tags: "x," + testHost},
		{name: "interrupted", state: "interrupted", priority: "3", tags: "warning," + testHost},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, requests := fakeNtfy(t, http.StatusOK)
			pub, err := ntfy.New(
				envFor(topicFile(t, srv), writeFile(t, "token", testToken+"\n")),
				testHost,
				srv.Client(),
			)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if err := pub.PublishSession(
				context.Background(),
				testProject,
				testSession,
				tc.state,
				testBody,
			); err != nil {
				t.Fatalf("PublishSession: %v", err)
			}
			got := <-requests
			if got.method != http.MethodPost {
				t.Errorf("method = %q, want POST", got.method)
			}
			if got.path != topicPath {
				t.Errorf("path = %q, want %q", got.path, topicPath)
			}
			want := map[string]string{
				"Title":         "example-project · session demo",
				"Priority":      tc.priority,
				"Tags":          tc.tags,
				"Authorization": "Bearer " + testToken,
			}
			for key, value := range want {
				if gotValue := got.header.Get(key); gotValue != value {
					t.Errorf("header %s = %q, want %q", key, gotValue, value)
				}
			}
			if got.body != testBody {
				t.Errorf("body = %q, want %q", got.body, testBody)
			}
		})
	}
}

func TestPublishSessionRejectsUnnotifiedState(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		state string
	}{
		{name: "working", state: "working"},
		{name: "stopped", state: "stopped"},
		{name: "unknown", state: "bogus"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, requests := fakeNtfy(t, http.StatusOK)
			pub, err := ntfy.New(envFor(topicFile(t, srv), ""), testHost, srv.Client())
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if err := pub.PublishSession(
				context.Background(),
				testProject,
				testSession,
				tc.state,
				testBody,
			); err == nil {
				t.Fatalf("PublishSession(%s) returned nil error, want an error", tc.state)
			}
			if n := len(requests); n != 0 {
				t.Errorf("state %s sent %d requests, want none", tc.state, n)
			}
		})
	}
}
