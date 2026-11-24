package enginehost

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// paneCallerTmux is the TMUX of the controller's tmux socket in the pane caller tests, and paneCallerColumn is the
// server column of the pane rows that socket's panes have.
const (
	paneCallerTmux   = "/tmp/tmux-1000/orrery,4242,0"
	paneCallerColumn = "-S /tmp/tmux-1000/orrery"
)

// paneCallerStore opens a migrated store file at a temporary path. The store closes at cleanup. It returns the store and
// the path of its file.
func paneCallerStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	st, err := store.Open(t.Context(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return st, path
}

// syncPaneCallerRows syncs pane %3 of paneCallerColumn under host b and again under host ctl, as two rows with the ids
// b-3 and ctl-3. It fails the test on error.
func syncPaneCallerRows(t *testing.T, st *store.Store) {
	t.Helper()
	for _, host := range []string{"b", "ctl"} {
		panes := []store.Pane{{ID: host + "-3", Pane: "%3"}}
		if err := st.SyncServer(t.Context(), host, paneCallerColumn, panes, epicCallAt()); err != nil {
			t.Fatalf("SyncServer under host %s: %v", host, err)
		}
	}
}

// paneCallerSessions returns the ids of the sessions that made a call of epicServiceID in the store file at path.
func paneCallerSessions(t *testing.T, path string) []string {
	t.Helper()
	rows, err := store.NewReader(path).EpicSessions(t.Context(), epicServiceID)
	if err != nil {
		t.Fatalf("EpicSessions: %v", err)
	}
	sessions := make([]string, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, row.Session)
	}
	return sessions
}

// paneCallerFixture serves the Epics service over httptest from a store at a temporary path, and records each call that
// carries caller headers into that store at epicCallAt. It returns the store, the path of its file, and the client that
// dials the server.
func paneCallerFixture(t *testing.T) (*store.Store, string, enginev1connect.EpicsClient) {
	t.Helper()
	st, path := paneCallerStore(t)
	interceptor := EpicCallInterceptor(st, epicCallAt, nil, slog.New(slog.DiscardHandler))
	epicsPath, handler := enginev1connect.NewEpicsHandler(
		&epicService{epics: st, steering: fixedSteering{}}, connect.WithInterceptors(interceptor),
	)
	mux := http.NewServeMux()
	mux.Handle(epicsPath, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return st, path, enginev1connect.NewEpicsClient(srv.Client(), srv.URL)
}

func TestPaneCallerHeaderHostResolvesUnderIt(t *testing.T) {
	t.Parallel()
	st, path, client := paneCallerFixture(t)
	syncPaneCallerRows(t, st)
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	req := connect.NewRequest(&v1.AnswerEpicRequest{EpicId: epicServiceID, Id: "D4", Text: "Synthetic note."})
	req.Header().Set(protocol.HeaderTmux, paneCallerTmux)
	req.Header().Set(protocol.HeaderTmuxPane, "%3")
	req.Header().Set(protocol.HeaderHost, "b")
	if _, err := client.AnswerEpic(t.Context(), req); err != nil {
		t.Fatalf("AnswerEpic: %v", err)
	}
	if got, want := paneCallerSessions(t, path), []string{"b-3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sessions that made a call = %v, want %v", got, want)
	}
}

func TestPaneCallerLocalSocketStampsControllerHost(t *testing.T) {
	t.Parallel()
	st, path := paneCallerStore(t)
	syncPaneCallerRows(t, st)
	sock := filepath.Join(t.TempDir(), "sessions.sock")
	ln, err := Listen(sock)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	interceptor := EpicCallInterceptor(st, epicCallAt, nil, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- ServeWith(ctx, ln, ServeConfig{
			Epics:      st,
			Steering:   fixedSteering{},
			CallerHost: "ctl",
			Options:    []connect.HandlerOption{connect.WithInterceptors(interceptor)},
		})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("ServeWith after cleanup: %v", err)
		}
	})
	client := newEpicsClient(t, sock)
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	// The call forges host b. The socket names this controller, so the call's pane is ctl's.
	req := connect.NewRequest(&v1.AnswerEpicRequest{EpicId: epicServiceID, Id: "D4", Text: "Synthetic note."})
	req.Header().Set(protocol.HeaderTmux, paneCallerTmux)
	req.Header().Set(protocol.HeaderTmuxPane, "%3")
	req.Header().Set(protocol.HeaderHost, "b")
	if _, err := client.AnswerEpic(t.Context(), req); err != nil {
		t.Fatalf("AnswerEpic: %v", err)
	}
	if got, want := paneCallerSessions(t, path), []string{"ctl-3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sessions that made a call = %v, want %v", got, want)
	}
}
