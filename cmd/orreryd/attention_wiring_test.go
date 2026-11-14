package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/planner"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic values only. The epic, its repository and its texts are invented. No role file exists, so the planner's
// agent cannot be read and the epic parks.
const (
	attentionWiringEpic   = "orrery-4d5e6f7/attention-wiring"
	attentionWiringTitle  = "Wire the attention loop"
	attentionWiringRepo   = "/work/synthetic-repo"
	attentionWiringHost   = "synthetic-host"
	attentionWiringBody   = "parked: synthetic reason"
	attentionWiringStamp  = int64(1_700_000_000)
	attentionWiringPrefix = "parked: "
)

// recordRoute is the Router the wiring test gives the host for its notices. It records each notice it is given.
type recordRoute struct {
	mu      sync.Mutex
	notices []route.Notice
}

// Notify records incoming.
func (r *recordRoute) Notify(_ context.Context, incoming route.Notice) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notices = append(r.notices, incoming)
}

// Changed records nothing: the wiring test runs no session.
func (r *recordRoute) Changed(notice.Key) {}

// Seen records nothing: the wiring test counts notices only.
func (r *recordRoute) Seen(context.Context, notice.Key, int64) {}

// snapshot returns the notices recorded so far.
func (r *recordRoute) snapshot() []route.Notice {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.notices)
}

// parkedNoticeRouted reports whether routed holds a notice keyed by the synthetic epic whose push summary starts with
// the parked prefix.
func parkedNoticeRouted(t *testing.T, routed *recordRoute) bool {
	t.Helper()
	for _, incoming := range routed.snapshot() {
		if incoming.Key != (notice.Key{Epic: attentionWiringEpic}) {
			continue
		}
		note, err := incoming.Push(t.Context())
		if err != nil {
			t.Fatalf("push of epic notice: %v", err)
		}
		if strings.HasPrefix(note.Summary, attentionWiringPrefix) {
			return true
		}
	}
	return false
}

// createAttentionEpic stores the synthetic epic with a pending trigger, which the planner parks without a role file.
func createAttentionEpic(t *testing.T, st *store.Store) {
	t.Helper()
	created := time.Unix(attentionWiringStamp, 0)
	if err := st.CreateEpic(t.Context(), store.EpicRow{
		ID:          attentionWiringEpic,
		Repository:  attentionWiringRepo,
		Title:       attentionWiringTitle,
		Approved:    "approved text",
		Intent:      "plan the next tasks",
		Care:        "serious",
		Approach:    "one planner turn",
		ReleaseThen: "release it",
		Checks:      "just check",
		Phase:       "accepted",
		Created:     created,
		Changed:     created,
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: "the attention loop routes", Evidence: "wiring test", Kind: "test"},
		},
	}); err != nil {
		t.Fatalf("create epic: %v", err)
	}
}

// TestSessionsHostRoutesParkedEpicNotice checks that a --sessions orreryd runs the attention loop through the
// notifier's route. The synthetic epic parks for want of a role file, and the loop routes exactly one epic-keyed notice
// that says so. The host then stops, and the loop ends with its notice given.
func TestSessionsHostRoutesParkedEpicNotice(t *testing.T) {
	ctx := t.Context()
	env := map[string]string{
		"XDG_STATE_HOME":  t.TempDir(),
		"XDG_CONFIG_HOME": t.TempDir(),
	}
	getenv := func(key string) string { return env[key] }
	path, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store path: %v", err)
	}
	st := openPaneStore(t, path)
	createAttentionEpic(t, st)

	opts := options{
		socket:     filepath.Join(t.TempDir(), "orreryd.sock"),
		sessionCap: defaultSessionCap,
		stepCap:    defaultStepCap,
	}
	routed := &recordRoute{}
	notifier := enginehost.NewNotifier(enginehost.NotifierConfig{Route: routed})
	logger := slog.New(slog.DiscardHandler)
	host, err := startSessions(ctx, opts, attentionWiringHost, st, notifier, routed, logger, getenv)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}

	deadline := time.Now().Add(plannerWiringWait)
	for !parkedNoticeRouted(t, routed) {
		if time.Now().After(deadline) {
			t.Fatalf("no notice keyed by epic %s with a summary starting %q was routed",
				attentionWiringEpic, attentionWiringPrefix)
		}
		time.Sleep(plannerWiringPoll)
	}
	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}

	got := routed.snapshot()
	if len(got) != 1 {
		t.Fatalf("routed %d notices, want exactly one", len(got))
	}
	if want := (notice.Key{Epic: attentionWiringEpic}); got[0].Key != want {
		t.Errorf("notice key = %+v, want %+v", got[0].Key, want)
	}
}

// TestRouterRestoresEpicNotice checks that the relay's router rebuilds a stored epic notice. The stored row is still
// sending, so the router sends its push again when it attaches, and the push carries the stored body.
func TestRouterRestoresEpicNotice(t *testing.T) {
	ctx := t.Context()
	logger := slog.New(slog.DiscardHandler)
	s := openPaneStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	source, err := json.Marshal(map[string]string{
		"epic":  attentionWiringEpic,
		"title": attentionWiringTitle,
		"body":  attentionWiringBody,
	})
	if err != nil {
		t.Fatalf("encode source: %v", err)
	}
	if err := s.PutNotice(ctx, store.NoticeRow{
		ID: paneRowID, Epic: attentionWiringEpic, State: "waiting", Time: attentionWiringStamp, Phase: phaseSending,
		SourceKind: planner.EpicSource, Source: source,
	}); err != nil {
		t.Fatalf("put sending row: %v", err)
	}

	sender := &recordSender{}
	sinks := newPaneSinks(attachResentAt, false)
	notices := notice.New(attachHost, sender, sinks.Now, logger)
	attachStore(ctx, s, daemon.NewPendingMirror(time.Now, logger), notices, attachRouter(notices, sinks))
	// Wait orders every send before the reads below.
	notices.Wait()

	if len(sender.pushes) != 1 {
		t.Fatalf("pushes = %+v, want the restored epic push once", sender.pushes)
	}
	if got := sender.pushes[0]; got.Summary != attentionWiringBody || got.State != protocol.StateWaiting {
		t.Errorf("push = state %q summary %q, want waiting with %q", got.State, got.Summary, attentionWiringBody)
	}
}
