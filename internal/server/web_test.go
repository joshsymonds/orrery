package server_test

import (
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/joshsymonds/orrery/internal/hoststatus"
	"github.com/joshsymonds/orrery/internal/open"
	"github.com/joshsymonds/orrery/internal/server"
)

// pageCSP is the Content-Security-Policy every page response carries.
const pageCSP = "default-src 'self'; frame-ancestors 'none'"

// syntheticIndex and syntheticApp are the fixture bytes of the page FS.
const (
	syntheticIndex = "<!doctype html><title>synthetic page</title>"
	syntheticApp   = "console.log('synthetic app');"
)

// pageFS returns the fixture page: an index.html, an app.js beside it, and a directory holding one file.
func pageFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":       {Data: []byte(syntheticIndex)},
		"app.js":           {Data: []byte(syntheticApp)},
		"assets/style.css": {Data: []byte("body{}")},
	}
}

// newWebHandler returns the guarded handler that admits only allowedLogin and serves web as the page.
func newWebHandler(t *testing.T, web fs.FS) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:     []string{allowedLogin},
		Tmux:       newTmuxClient(t),
		Push:       newPushStore(t),
		HostStatus: hoststatus.New(writeHostStatusScript(t, "", 0), time.Now),
		Picker: func() (open.Config, error) {
			return open.Config{}, errors.New("synthetic picker")
		},
		Web: web,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// assertPageHeaders fails the test unless rec carries the page CSP and nosniff headers exactly.
func assertPageHeaders(t *testing.T, path string, header http.Header) {
	t.Helper()
	if got := header.Get("Content-Security-Policy"); got != pageCSP {
		t.Fatalf("GET %s: Content-Security-Policy = %q, want %q", path, got, pageCSP)
	}
	if got := header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("GET %s: X-Content-Type-Options = %q, want nosniff", path, got)
	}
}

// TestPageServesIndexAndAssets checks that GET and HEAD of "/" answer index.html, and of an exact file path answer
// that file, each with its bytes, a Content-Type from its extension, and both headers exact.
func TestPageServesIndexAndAssets(t *testing.T) {
	t.Parallel()
	handler := newWebHandler(t, pageFS())
	tests := []struct {
		name        string
		method      string
		path        string
		body        string
		contentType []string
	}{
		{"index at root", http.MethodGet, "/", syntheticIndex, []string{"text/html"}},
		{"index by name", http.MethodGet, "/index.html", syntheticIndex, []string{"text/html"}},
		{"asset", http.MethodGet, "/app.js", syntheticApp, []string{"text/javascript", "application/javascript"}},
		{"asset in directory", http.MethodGet, "/assets/style.css", "body{}", []string{"text/css"}},
		{"index HEAD", http.MethodHead, "/", "", []string{"text/html"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := serve(t, handler, tc.method, tc.path, connectHeader(allowedLogin, "", false), "")
			assertStatus(t, rec, http.StatusOK)
			assertPageHeaders(t, tc.path, rec.Header())
			if tc.method != http.MethodHead && rec.Body.String() != tc.body {
				t.Fatalf("GET %s: body = %q, want %q", tc.path, rec.Body.String(), tc.body)
			}
			mediaType, _, err := mime.ParseMediaType(rec.Header().Get("Content-Type"))
			if err != nil {
				t.Fatalf("GET %s: Content-Type %q: %v", tc.path, rec.Header().Get("Content-Type"), err)
			}
			if !slices.Contains(tc.contentType, mediaType) {
				t.Fatalf("GET %s: media type = %q, want one of %q", tc.path, mediaType, tc.contentType)
			}
		})
	}
}

// TestPageMissingPathAndDirectoryAnswer404 checks that a path the FS lacks and a directory answer 404, each
// carrying the page headers.
func TestPageMissingPathAndDirectoryAnswer404(t *testing.T) {
	t.Parallel()
	handler := newWebHandler(t, pageFS())
	for _, path := range []string{"/missing.js", "/assets", "/assets/"} {
		rec := serve(t, handler, http.MethodGet, path, connectHeader(allowedLogin, "", false), "")
		assertStatus(t, rec, http.StatusNotFound)
		assertPageHeaders(t, path, rec.Header())
	}
}

// TestPageRejectsOtherMethodsAndUnknownPaths checks that a method other than GET and HEAD on a non-Connect path
// answers 404, and that the Connect paths keep their own routing.
func TestPageRejectsOtherMethodsAndUnknownPaths(t *testing.T) {
	t.Parallel()
	handler := newWebHandler(t, pageFS())
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := serve(t, handler, method, "/", connectHeader(allowedLogin, "", false), "")
		assertStatus(t, rec, http.StatusNotFound)
	}
	rec := serve(t, handler, http.MethodGet, "/v1/panes", connectHeader(allowedLogin, "", false), "")
	assertStatus(t, rec, http.StatusNotFound)
}

// TestPageNeedsAllowedLogin checks that "/" answers 403 with no login and with a stranger's login.
func TestPageNeedsAllowedLogin(t *testing.T) {
	t.Parallel()
	handler := newWebHandler(t, pageFS())
	for _, login := range []string{"", otherLogin} {
		rec := serve(t, handler, http.MethodGet, "/", connectHeader(login, "", false), "")
		assertStatus(t, rec, http.StatusForbidden)
	}
}

// TestNoPageWithoutWeb checks that "/" answers 404 when Web is nil.
func TestNoPageWithoutWeb(t *testing.T) {
	t.Parallel()
	handler := newWebHandler(t, nil)
	rec := serve(t, handler, http.MethodGet, "/", connectHeader(allowedLogin, "", false), "")
	assertStatus(t, rec, http.StatusNotFound)
}
