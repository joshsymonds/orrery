package server_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// connectListPath is the Connect path of Sessions.List.
const connectListPath = "/" + orreryv1connect.SessionsName + "/List"

// connectHeader returns the headers of a request with login and contentType. The client header is set only
// when withClient is true, and the Content-Type only when contentType is not empty.
func connectHeader(login, contentType string, withClient bool) http.Header {
	header := http.Header{}
	header.Set(server.HeaderLogin, login)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	if withClient {
		header.Set(server.HeaderClient, client)
	}
	return header
}

// newConnectHandler returns the guarded handler that admits allowedLogin, routes Panes and Push to a tmux client
// with no running server, and reads Sessions from reader.
func newConnectHandler(t *testing.T, reader *store.Reader) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:   []string{allowedLogin},
		Tmux:     newTmuxClient(t),
		Push:     newPushStore(t),
		Sessions: reader,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// headerTransport adds the login and client headers the guard requires to every request it sends.
type headerTransport struct {
	base  http.RoundTripper
	login string
}

// RoundTrip sends a copy of req with the guard headers set.
func (h headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set(server.HeaderLogin, h.login)
	clone.Header.Set(server.HeaderClient, client)
	return h.base.RoundTrip(clone)
}

// connectClient serves handler on an httptest server and returns a generated Sessions client whose requests
// carry login and the client header.
func connectClient(t *testing.T, handler http.Handler, login string) orreryv1connect.SessionsClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: headerTransport{base: http.DefaultTransport, login: login}}
	return orreryv1connect.NewSessionsClient(httpClient, srv.URL)
}

// sessionFields returns the ten mirrored fields of the row id. Each value names its field, so a swapped field
// fails the test.
func sessionFields(id string) store.Fields {
	return store.Fields{
		State:         id + " state",
		Changed:       id + " changed",
		Seen:          id + " seen",
		Question:      id + " question",
		Summary:       id + " summary",
		Label:         id + " label",
		Context:       id + " context",
		Tool:          id + " tool",
		ClaudeSession: id + " claude_session",
		Transcript:    id + " transcript",
	}
}

// wantSessionMessage is the proto row List must answer for the row id written with sessionFields(id).
func wantSessionMessage(id, server, pane string) *orreryv1.Session {
	return &orreryv1.Session{
		Id:            id,
		Server:        server,
		Pane:          pane,
		State:         id + " state",
		Changed:       id + " changed",
		Seen:          id + " seen",
		Question:      id + " question",
		Summary:       id + " summary",
		Label:         id + " label",
		Context:       id + " context",
		Tool:          id + " tool",
		ClaudeSession: id + " claude_session",
		Transcript:    id + " transcript",
	}
}

// seedSessionStore writes a daemon store at path with two rows, closes the writer, and returns the newest
// event seq.
func seedSessionStore(t *testing.T, path string) int64 {
	t.Helper()
	ctx := t.Context()
	storetest.Seed(t, path)
	s, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	now := time.Unix(1_700_000_000, 0)
	if err = s.SyncServer(ctx, "-L a", []store.Pane{
		{ID: "orrery-1", Pane: "%1", Fields: sessionFields("orrery-1")},
		{ID: "orrery-2", Pane: "%2", Fields: sessionFields("orrery-2")},
	}, now); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open store for seq: %v", err)
	}
	var seq int64
	err = db.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq), 0) FROM events").Scan(&seq)
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("close store for seq: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("read newest seq: %v", err)
	}
	if seq == 0 {
		t.Fatal("store wrote no events")
	}
	return seq
}

// lockSessionStore holds the store at path under an exclusive lock until the test ends, so a reader finds it
// busy.
func lockSessionStore(t *testing.T, path string) {
	t.Helper()
	ctx := t.Context()
	holder, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open lock holder: %v", err)
	}
	t.Cleanup(func() {
		if err := holder.Close(); err != nil {
			t.Errorf("close lock holder: %v", err)
		}
	})
	conn, err := holder.Conn(ctx)
	if err != nil {
		t.Fatalf("lock holder connection: %v", err)
	}
	if _, err = conn.ExecContext(ctx, "PRAGMA locking_mode=EXCLUSIVE"); err != nil {
		t.Fatalf("set exclusive locking: %v", err)
	}
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("take write lock: %v", err)
	}
	if _, err = conn.ExecContext(
		ctx,
		"INSERT INTO events (session_id, kind, at, payload) VALUES ('x', 'k', 0, '{}')",
	); err != nil {
		t.Fatalf("write while holding lock: %v", err)
	}
}

func TestConnectRequiresAllowedLogin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedSessionStore(t, path)
	handler := newConnectHandler(t, store.NewReader(path))

	for _, method := range []string{http.MethodPost, http.MethodGet} {
		for _, login := range []string{"", otherLogin} {
			rec := serve(t, handler, method, connectListPath,
				connectHeader(login, "application/proto", true), "")
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s with login %q: status = %d, want 403", method, login, rec.Code)
			}
		}
	}
}

func TestConnectRequiresClientHeaderOnEveryMethod(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedSessionStore(t, path)
	handler := newConnectHandler(t, store.NewReader(path))

	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodPut, http.MethodDelete} {
		rec := serve(t, handler, method, connectListPath,
			connectHeader(allowedLogin, "application/proto", false), "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s without client header: status = %d, want 403", method, rec.Code)
		}
	}
}

func TestConnectAdmitsConnectContentTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedSessionStore(t, path)
	handler := newConnectHandler(t, store.NewReader(path))

	// An empty proto body and an empty JSON object are each a valid ListRequest, so the Connect handler answers 200.
	for _, unary := range []struct{ contentType, body string }{{"application/proto", ""}, {"application/json", "{}"}} {
		rec := serve(t, handler, http.MethodPost, connectListPath,
			connectHeader(allowedLogin, unary.contentType, true), unary.body)
		assertStatus(t, rec, http.StatusOK)
	}
	// The connect+ types are not unary codecs, so the Connect handler answers without the guard's 403 or a mux 404.
	for _, contentType := range []string{"application/connect+proto", "application/connect+json"} {
		rec := serve(t, handler, http.MethodPost, connectListPath,
			connectHeader(allowedLogin, contentType, true), "")
		if rec.Code == http.StatusForbidden || rec.Code == http.StatusNotFound {
			t.Fatalf("POST %s: status = %d, want the Connect handler to answer", contentType, rec.Code)
		}
	}
}

func TestConnectRejectsOtherContentTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedSessionStore(t, path)
	handler := newConnectHandler(t, store.NewReader(path))

	for _, contentType := range []string{"", "text/plain", "application/x-protobuf", "application/grpc+proto"} {
		rec := serve(t, handler, http.MethodPost, connectListPath,
			connectHeader(allowedLogin, contentType, true), "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("POST with Content-Type %q: status = %d, want 403", contentType, rec.Code)
		}
	}
}

func TestConnectListThroughGeneratedClient(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	seq := seedSessionStore(t, path)
	sessions := connectClient(t, newConnectHandler(t, store.NewReader(path)), allowedLogin)

	got, err := sessions.List(t.Context(), connect.NewRequest(&orreryv1.ListRequest{}))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := &orreryv1.ListResponse{
		Sessions: []*orreryv1.Session{
			wantSessionMessage("orrery-1", "-L a", "%1"),
			wantSessionMessage("orrery-2", "-L a", "%2"),
		},
		Seq: seq,
	}
	if !proto.Equal(got.Msg, want) {
		t.Fatalf("List: got %v, want %v", got.Msg, want)
	}
}

func TestConnectListUnavailableWhenStoreIsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent", "orrery.db")
	handler := newConnectHandler(t, store.NewReader(path))

	_, err := connectClient(t, handler, allowedLogin).List(
		t.Context(),
		connect.NewRequest(&orreryv1.ListRequest{}),
	)
	if got := connect.CodeOf(err); got != connect.CodeUnavailable {
		t.Fatalf("List code: got %v (%v), want %v", got, err, connect.CodeUnavailable)
	}
	assertPhoneStillAnswers(t, handler)
}

func TestConnectListUnavailableWhileStoreIsBusy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedSessionStore(t, path)
	lockSessionStore(t, path)
	handler := newConnectHandler(t, store.NewReader(path))

	_, err := connectClient(t, handler, allowedLogin).List(
		t.Context(),
		connect.NewRequest(&orreryv1.ListRequest{}),
	)
	if got := connect.CodeOf(err); got != connect.CodeUnavailable {
		t.Fatalf("List code: got %v (%v), want %v", got, err, connect.CodeUnavailable)
	}
	assertPhoneStillAnswers(t, handler)
}

// assertPhoneStillAnswers checks that a Panes read and a Push write answer as they do without a store: Panes.List
// answers an empty list when no tmux server runs, and Push.Register stores the subscription.
func assertPhoneStillAnswers(t *testing.T, handler http.Handler) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: headerTransport{base: http.DefaultTransport, login: allowedLogin}}

	list, err := orreryv1connect.NewPanesClient(httpClient, srv.URL).List(
		t.Context(),
		connect.NewRequest(&orreryv1.PanesListRequest{}),
	)
	if err != nil {
		t.Fatalf("Panes.List: %v", err)
	}
	if got := list.Msg.GetPanes(); len(got) != 0 {
		t.Fatalf("Panes.List panes = %v, want none", got)
	}

	p256dh, auth := pushKeys(t)
	endpoint := "https://" + pushHost + "/synthetic-connect-still"
	_, err = orreryv1connect.NewPushClient(httpClient, srv.URL).Register(
		t.Context(),
		connect.NewRequest(&orreryv1.RegisterRequest{Endpoint: endpoint, P256Dh: p256dh, Auth: auth}),
	)
	if err != nil {
		t.Fatalf("Push.Register: %v", err)
	}
}
