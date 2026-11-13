package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic values only. The session, its project and its texts are invented, and the clocks are fixed.
const (
	attachHost      = "synthetic-host"
	attachSession   = "synthetic-session"
	attachProject   = "/synthetic/project"
	attachQuestion  = "synthetic question"
	attachNoticedAt = int64(1_700_000_000)
	attachResentAt  = int64(1_700_000_600)
	// phaseSending is the phase of a row whose push was handed to the phone, spelled as the router writes it.
	phaseSending = "sending"
)

// recordSender is the fake sender a real Notices posts through. It records each push and clear it is given.
type recordSender struct {
	mu     sync.Mutex
	pushes []push.Notification
	clears []push.Clear
}

// Publish records the push.
func (s *recordSender) Publish(_ context.Context, note push.Notification) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pushes = append(s.pushes, note)
	return nil
}

// PublishClear records the clear.
func (s *recordSender) PublishClear(_ context.Context, c push.Clear) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clears = append(s.clears, c)
	return nil
}

// attachRouter returns the router serve builds, with notices as its phone and sinks as its desk, log and clock.
func attachRouter(notices *notice.Notices, sinks *paneSinks) *route.Router {
	hub := newRelay(route.Config{
		Host:       attachHost,
		Now:        sinks.Now,
		After:      sinks.After,
		Desk:       sinks,
		Phone:      notices,
		Subscribed: sinks.Subscribed,
		Log:        sinks,
		Logger:     slog.New(slog.DiscardHandler),
	}, &relay{})
	return hub.router
}

// TestAttachStore checks what attachStore attaches. With a store holding a session notice still sending, the router
// resends its push through a real Notices, and the store keeps the resent push's time for the session. With no store,
// nothing panics, and a push and a clear that follow still send through that Notices.
func TestAttachStore(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	t.Run("resent push time is stored", func(t *testing.T) {
		ctx := t.Context()
		s := openPaneStore(t, filepath.Join(t.TempDir(), "orrery.db"))
		source, err := json.Marshal(enginehost.Notice{
			Session: attachSession, Dir: attachProject, State: "waiting", Body: attachQuestion,
		})
		if err != nil {
			t.Fatalf("encode source: %v", err)
		}
		if err := s.PutNotice(ctx, store.NoticeRow{
			ID: paneRowID, Session: attachSession, State: "waiting", Time: attachNoticedAt, Phase: phaseSending,
			SourceKind: enginehost.SessionSource, Source: source,
		}); err != nil {
			t.Fatalf("put sending row: %v", err)
		}

		sender := &recordSender{}
		sinks := newPaneSinks(attachResentAt, false)
		notices := notice.New(attachHost, sender, sinks.Now, logger)
		attachStore(ctx, s, daemon.NewPendingMirror(time.Now, logger), notices, attachRouter(notices, sinks))
		// Wait orders every send before the reads below.
		notices.Wait()

		wantPush := push.Notification{
			Host: attachHost, Project: filepath.Base(attachProject), Session: attachSession,
			State: "waiting", Question: attachQuestion, Time: attachResentAt,
		}
		if len(sender.pushes) != 1 || sender.pushes[0] != wantPush {
			t.Errorf("pushes = %+v, want [%+v]", sender.pushes, wantPush)
		}
		if len(sender.clears) != 0 {
			t.Errorf("clears = %+v, want none", sender.clears)
		}
		pushed, err := s.Pushed(ctx)
		if err != nil {
			t.Fatalf("list pushed: %v", err)
		}
		if want := []store.PushedRow{{Session: attachSession, Time: attachResentAt}}; !slices.Equal(pushed, want) {
			t.Errorf("pushed = %+v, want %+v", pushed, want)
		}
		checkRows(t, s)
	})

	t.Run("no store", func(t *testing.T) {
		ctx := t.Context()
		sender := &recordSender{}
		sinks := newPaneSinks(attachResentAt, false)
		notices := notice.New(attachHost, sender, sinks.Now, logger)
		attachStore(ctx, nil, daemon.NewPendingMirror(time.Now, logger), notices, attachRouter(notices, sinks))

		key := notice.Key{Session: attachSession}
		notices.Notify(ctx, key, func(context.Context) (push.Notification, error) {
			return push.Notification{State: "done", Summary: "synthetic summary"}, nil
		}, nil)
		notices.Seen(ctx, key, attachResentAt)
		notices.Wait()

		if len(sender.pushes) != 1 {
			t.Errorf("pushes = %+v, want one", sender.pushes)
		}
		if len(sender.clears) != 1 {
			t.Errorf("clears = %+v, want one", sender.clears)
		}
	})
}
