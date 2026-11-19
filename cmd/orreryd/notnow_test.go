package main

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// Synthetic values only. The clock is fixed, and the changed times are invented.
const (
	// notNowClock is the fixed clock, in unix seconds, that every notice of these tests is recorded at. It is well before
	// the real date, so the seen time the manager stamps from its own clock is not before it.
	notNowClock = int64(1_700_000_000)
	// notNowChanged is the @orrery_changed of the question these tests put off.
	notNowChanged = notNowClock
	// notNowLater is the @orrery_changed of a question asked after notNowChanged.
	notNowLater = notNowClock + 60
)

// seenPhone is the phone of the NotNow tests. It sends each push as sinks does, and records the key of each Seen.
type seenPhone struct {
	sinks *paneSinks
	mu    sync.Mutex
	seen  []notice.Key
}

// Notify hands the push to sinks.
func (p *seenPhone) Notify(
	ctx context.Context, key notice.Key, build func(context.Context) (push.Notification, error), sent func(),
) {
	p.sinks.Notify(ctx, key, build, sent)
}

// Seen records key.
func (p *seenPhone) Seen(_ context.Context, key notice.Key, _ int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seen = append(p.seen, key)
}

// seenKeys returns the keys the phone recorded as seen.
func (p *seenPhone) seenKeys() []notice.Key {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.seen)
}

// notNowRelay returns the relay whose router routes notices through sinks as its desk and log, and through phone.
func notNowRelay(sinks *paneSinks, phone route.PhoneSink) *relay {
	return newRelay(route.Config{
		Host:       paneHost,
		Now:        sinks.Now,
		After:      sinks.After,
		Desk:       sinks,
		Phone:      phone,
		Subscribed: sinks.Subscribed,
		Log:        sinks,
		Logger:     slog.New(slog.DiscardHandler),
	}, &relay{
		logger: slog.New(slog.DiscardHandler),
		reads:  newReadTimes(),
	})
}

// startNotNowHost starts a --sessions host over st, whose panes are on server and whose notices go to router. Its state
// is in getenv's directories, and its attention loop is given a tick channel no test sends to.
func startNotNowHost(
	t *testing.T, getenv func(string) string, server tmuxctl.Server, st *store.Store, router enginehost.Router,
) *sessionHost {
	t.Helper()
	opts := evidenceWiringOptions(t)
	opts.server = server
	opts.attentionTicks = make(chan time.Time)
	host, err := startSessions(t.Context(), opts, paneHost, st, router, nil, slog.New(slog.DiscardHandler), getenv)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}
	return host
}

// setPaneChanged stores the changed time, in unix seconds, that a state change of pane wrote.
func setPaneChanged(t *testing.T, client *tmuxctl.Client, pane string, changed int64) {
	t.Helper()
	value := strconv.FormatInt(changed, 10)
	if err := client.SetPaneOption(t.Context(), pane, protocol.OptionChanged, value); err != nil {
		t.Fatalf("set %s: %v", protocol.OptionChanged, err)
	}
}

// putOffMarks returns the put-off marks stored at path. Each mark's time is cleared, since the manager stamps it from its
// own clock, which the tests do not fix.
func putOffMarks(t *testing.T, path string) []store.PutOffMark {
	t.Helper()
	marks, err := store.NewReader(path).PutOffQuestions(t.Context())
	if err != nil {
		t.Fatalf("read put-off questions: %v", err)
	}
	for i := range marks {
		marks[i].At = time.Time{}
	}
	return marks
}

// TestNotNowPaneQuestionReadsTmux checks that the pane questions read a pane's waiting state, question hash and changed
// time from tmux. A pane that stops waiting reads not waiting, and a pane tmux does not list is ErrNoPane. startPane sets
// LC_ALL with t.Setenv, and Go forbids t.Setenv in a test that calls t.Parallel, so these tests run one at a time.
func TestNotNowPaneQuestionReadsTmux(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	server, client, pane := startWaitingPane(t, home)
	setPaneChanged(t, client, pane, notNowChanged)
	questions := newPaneQuestions(server, nil)

	got, err := questions.PaneQuestion(ctx, pane)
	if err != nil {
		t.Fatalf("pane question: %v", err)
	}
	want := enginehost.PaneQuestion{Waiting: true, Hash: protocol.QuestionHash(paneQuestion), Changed: notNowChanged}
	if got != want {
		t.Errorf("waiting pane question = %+v, want %+v", got, want)
	}

	setPaneState(t, client, pane, protocol.StateWorking)
	got, err = questions.PaneQuestion(ctx, pane)
	if err != nil {
		t.Fatalf("pane question after working: %v", err)
	}
	want.Waiting = false
	if got != want {
		t.Errorf("working pane question = %+v, want %+v", got, want)
	}

	if _, err := questions.PaneQuestion(ctx, "%999"); !errors.Is(err, enginehost.ErrNoPane) {
		t.Errorf("unknown pane error = %v, want ErrNoPane", err)
	}
}

// TestNotNowPaneEndsPendingNoticeOnDeskAndPhone checks that NotNow on a pane's open question stores its mark and ends the
// pane's pending notice as seen, on the desk's log and on the phone. BringBack then clears the mark and sends nothing.
func TestNotNowPaneEndsPendingNoticeOnDeskAndPhone(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	server, client, pane := startWaitingPane(t, home)
	setPaneChanged(t, client, pane, notNowChanged)
	getenv, path := evidenceWiringEnv(t)
	st := openPaneStore(t, path)
	sinks := newPaneSinks(notNowClock, true)
	phone := &seenPhone{sinks: sinks}
	hub := notNowRelay(sinks, phone)
	t.Cleanup(hub.router.Close)

	// With web input the notice goes to the desk, where it stays pending until it is seen.
	transition := paneTransition(server, pane, protocol.StateWaiting, filepath.Join(home, paneProject), paneWaitingHook)
	hub.notify(ctx, transition, nil, nil)
	hub.router.Wait()
	posts := []deskstream.Notice{expectedPost(protocol.StateWaiting, paneQuestion)}
	sinks.check(t,
		[]route.Line{expectedLine(notNowClock, pane, protocol.StateWaiting, route.Desk, reasonAtDesk)},
		posts, nil)

	host := startNotNowHost(t, getenv, server, st, hub.router)
	req := enginehost.PutOffRequest{
		Kind: store.PutOffPane, ID: pane, QuestionHash: protocol.QuestionHash(paneQuestion), Changed: notNowChanged,
	}
	if err := host.manager.NotNow(ctx, req); err != nil {
		t.Fatalf("not now: %v", err)
	}
	wantMarks := []store.PutOffMark{{
		Kind: store.PutOffPane, Session: pane, Question: store.PaneQuestionKey(req.QuestionHash, req.Changed),
	}}
	if got := putOffMarks(t, path); !slices.Equal(got, wantMarks) {
		t.Errorf("marks after not now = %+v, want %+v", got, wantMarks)
	}
	sinks.check(t,
		[]route.Line{
			expectedLine(notNowClock, pane, protocol.StateWaiting, route.Desk, reasonAtDesk),
			expectedLine(notNowClock, pane, protocol.StateWaiting, route.None, reasonSeen),
		},
		posts, nil)
	if got, want := phone.seenKeys(), []notice.Key{{Pane: pane}}; !slices.Equal(got, want) {
		t.Errorf("phone seen keys = %+v, want %+v", got, want)
	}

	if err := host.manager.BringBack(ctx, req); err != nil {
		t.Fatalf("bring back: %v", err)
	}
	if got := putOffMarks(t, path); len(got) != 0 {
		t.Errorf("marks after bring back = %+v, want none", got)
	}
	sinks.check(t,
		[]route.Line{
			expectedLine(notNowClock, pane, protocol.StateWaiting, route.Desk, reasonAtDesk),
			expectedLine(notNowClock, pane, protocol.StateWaiting, route.None, reasonSeen),
		},
		posts, nil)

	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// TestNotNowPaneRefusesQuestionNoLongerOpen checks that NotNow refuses a pane question that is no longer open: one the
// pane asked again at a later changed time, and one the pane is no longer waiting on. Neither stores a mark.
func TestNotNowPaneRefusesQuestionNoLongerOpen(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	server, client, pane := startWaitingPane(t, home)
	setPaneChanged(t, client, pane, notNowChanged)
	getenv, path := evidenceWiringEnv(t)
	st := openPaneStore(t, path)
	sinks := newPaneSinks(notNowClock, false)
	hub := notNowRelay(sinks, &seenPhone{sinks: sinks})
	t.Cleanup(hub.router.Close)
	host := startNotNowHost(t, getenv, server, st, hub.router)

	// The pane starts waiting again on the same question text, at a later changed time.
	setPaneState(t, client, pane, protocol.StateWorking)
	setPaneState(t, client, pane, protocol.StateWaiting)
	setPaneChanged(t, client, pane, notNowLater)
	req := enginehost.PutOffRequest{
		Kind: store.PutOffPane, ID: pane, QuestionHash: protocol.QuestionHash(paneQuestion), Changed: notNowChanged,
	}
	if err := host.manager.NotNow(ctx, req); !errors.Is(err, enginehost.ErrQuestionNotOpen) {
		t.Errorf("not now of the earlier question = %v, want ErrQuestionNotOpen", err)
	}

	setPaneState(t, client, pane, protocol.StateWorking)
	req.Changed = notNowLater
	if err := host.manager.NotNow(ctx, req); !errors.Is(err, enginehost.ErrQuestionNotOpen) {
		t.Errorf("not now of a pane no longer waiting = %v, want ErrQuestionNotOpen", err)
	}

	if got := putOffMarks(t, path); len(got) != 0 {
		t.Errorf("marks = %+v, want none", got)
	}
	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}
