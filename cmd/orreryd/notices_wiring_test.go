package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/enginehost"
	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/inapp"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic values only. The host, the repository, the step ids, the epic and the pane are invented. The steps are open
// rows with no loop, so no agent is started.
const (
	noticesWiringHost      = "synthetic-host"
	noticesWiringRepo      = "/work/synthetic-repo"
	noticesWiringFree      = "notices-free-step"
	noticesWiringHeld      = "notices-held-step"
	noticesWiringEpic      = "synthetic-epic"
	noticesWiringPane      = "%synthetic"
	noticesWiringPaneLater = "%synthetic-later"
	noticesWiringStamp     = 1_700_000_000
	noticesWiringWait      = 10 * time.Second
)

// startNoticesHost starts a --sessions host over st that routes its notices to router and serves notices as its in-app
// notices. It returns the host and the path of its session socket.
func startNoticesHost(
	t *testing.T,
	st *store.Store,
	getenv func(string) string,
	router enginehost.Router,
	notices enginehost.NoticeSource,
) (*sessionHost, string) {
	t.Helper()
	opts := options{
		socket:     filepath.Join(t.TempDir(), "orreryd.sock"),
		sessionCap: defaultSessionCap,
		stepCap:    defaultStepCap,
	}
	host, err := startSessions(
		t.Context(), opts, noticesWiringHost, st, router, notices, slog.New(slog.DiscardHandler), getenv,
	)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}
	return host, protocol.SessionSocketPath(opts.socket)
}

// noticesWiringClient returns a Notices client that dials the session socket at path.
func noticesWiringClient(t *testing.T, path string) enginev1connect.NoticesClient {
	t.Helper()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", path)
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
	return enginev1connect.NewNoticesClient(&http.Client{Transport: transport}, "http://notices.invalid")
}

// addOpenStep stores an open step row with no loop, as a restart leaves a step. An empty epic leaves the step unnamed.
func addOpenStep(t *testing.T, st *store.Store, id, epic string) {
	t.Helper()
	if err := st.ApplyEngine(t.Context(), []store.EngineOp{
		store.StepAdd{
			ID: id, Agent: "implementer", Prompt: "synthetic prompt", Schema: "{}", Workspace: noticesWiringRepo,
			ReadOnly: true, DoneValue: "done", Epic: epic,
		},
	}, time.Unix(noticesWiringStamp, 0)); err != nil {
		t.Fatalf("add step %s: %v", id, err)
	}
}

// showWiringNotice shows a waiting notice on pages for the pane key, with body as its body.
func showWiringNotice(pages *inapp.Hub, pane, body string) {
	pages.Show(notice.Key{Pane: pane}, deskstream.Notice{State: "waiting", Title: "synthetic title", Body: body})
}

// receiveWiringNotice receives the next message of stream and returns it as the shown notice it must be.
func receiveWiringNotice(t *testing.T, stream *connect.ServerStreamForClient[enginev1.NoticesWatchResponse]) {
	t.Helper()
	if !stream.Receive() {
		t.Fatalf("Receive: %v", stream.Err())
	}
	shown := stream.Msg().GetShown()
	if shown == nil {
		t.Fatalf("watched change = %v, want a shown notice", stream.Msg())
	}
	if shown.GetState() != "waiting" {
		t.Errorf("watched notice state = %q, want waiting", shown.GetState())
	}
}

// TestSessionsHostWatchShowsPagesNotice checks that a Notices.Watch on the session socket of a --sessions host receives
// the notices shown on the in-app hub the host was given: first the one shown before the watch, then one shown during
// it. Watch returns once the first message arrives, so a notice shown during the watch comes after the first receive.
func TestSessionsHostWatchShowsPagesNotice(t *testing.T) {
	t.Parallel()
	st, getenv := openLimitStore(t)
	pages := inapp.NewHub()
	router := &recordingRouter{}
	host, path := startNoticesHost(t, st, getenv, router, enginehost.NewPageNotices(pages, router, time.Now))

	showWiringNotice(pages, noticesWiringPane, "synthetic body before the watch")
	ctx, cancel := context.WithTimeout(t.Context(), noticesWiringWait)
	defer cancel()
	stream, err := noticesWiringClient(t, path).Watch(ctx, connect.NewRequest(&enginev1.NoticesWatchRequest{}))
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	receiveWiringNotice(t, stream)
	if got := stream.Msg().GetShown().GetBody(); got != "synthetic body before the watch" {
		t.Errorf("first watched notice body = %q, want the notice shown before the watch", got)
	}

	showWiringNotice(pages, noticesWiringPaneLater, "synthetic body during the watch")
	receiveWiringNotice(t, stream)
	if got := stream.Msg().GetShown().GetBody(); got != "synthetic body during the watch" {
		t.Errorf("second watched notice body = %q, want the notice shown during the watch", got)
	}

	cancel()
	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// TestSessionsHostTellsOnlyEpiclessRestartedStep checks that a step an earlier run left open with no epic is told at
// start as an interrupted notice for that step, and that a step whose row names an epic is not told. Both end
// engine_restarted.
func TestSessionsHostTellsOnlyEpiclessRestartedStep(t *testing.T) {
	t.Parallel()
	st, getenv := openLimitStore(t)
	addOpenStep(t, st, noticesWiringFree, "")
	addOpenStep(t, st, noticesWiringHeld, noticesWiringEpic)

	router := &recordingRouter{}
	host, _ := startNoticesHost(t, st, getenv, router, nil)
	got := router.received()
	wantKey := notice.Key{Session: noticesWiringFree}
	if len(got) != 1 || got[0].Key != wantKey || got[0].State != "interrupted" {
		t.Fatalf("notices at start = %+v, want exactly one interrupted notice for key %+v", got, wantKey)
	}

	for _, id := range []string{noticesWiringFree, noticesWiringHeld} {
		row, err := st.Step(t.Context(), id)
		if err != nil {
			t.Fatalf("read step %s: %v", id, err)
		}
		if row.End != "engine_restarted" {
			t.Errorf("step %s end = %q, want engine_restarted", id, row.End)
		}
	}

	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// TestShutdownEndsWatchBeforeSessionsStop checks that shutdown ends a Watch that is open when shutdown begins cleanly.
// The session socket would cut off such a Watch with an error, so a clean end shows the Watch ended before the socket
// stopped.
func TestShutdownEndsWatchBeforeSessionsStop(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	st, getenv := openLimitStore(t)
	pages := inapp.NewHub()
	router := route.New(route.Config{Host: noticesWiringHost, Now: time.Now, Logger: logger})
	host, path := startNoticesHost(t, st, getenv, router, enginehost.NewPageNotices(pages, router, time.Now))

	showWiringNotice(pages, noticesWiringPane, "synthetic body")
	ctx, cancel := context.WithTimeout(t.Context(), noticesWiringWait)
	defer cancel()
	stream, err := noticesWiringClient(t, path).Watch(ctx, connect.NewRequest(&enginev1.NoticesWatchRequest{}))
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	receiveWiringNotice(t, stream)

	err = shutdownServe(
		t.Context(), logger, pages, st, &relay{}, host, router, notice.New(noticesWiringHost, nil, time.Now, logger),
	)
	if err != nil {
		t.Fatalf("shutdownServe: %v", err)
	}
	if stream.Receive() {
		t.Fatal("Receive after shutdown returned a message, want the stream ended")
	}
	if err := stream.Err(); err != nil {
		t.Errorf("stream ended with %v, want a clean end", err)
	}
}
