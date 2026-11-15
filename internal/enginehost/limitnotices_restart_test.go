package enginehost

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// limitStepOps returns the ops that add each of ids as an open step whose usage limit holds it until limitReset.
func limitStepOps(ids ...string) []store.EngineOp {
	ops := make([]store.EngineOp, 0, 2*len(ids))
	for _, id := range ids {
		ops = append(ops,
			store.StepAdd{ID: id, Agent: "implementer", Prompt: "synthetic prompt", Workspace: "/synthetic/ws"},
			store.StepLoop{ID: id, Limited: true, LimitKind: "usage", LimitResets: limitReset()},
		)
	}
	return ops
}

func TestLimitNoticeRoutedOnAttachedRouterIsNotRoutedAgainAfterRestart(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	first, err := store.Open(ctx, path, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	sender := &fakeSender{}
	log := &fakeRouteLog{}
	phone := notice.New(sessionTestHost, sender, fixedClock, slog.New(slog.DiscardHandler))
	router := newSessionRouter(t, phone, log)
	router.Attach(ctx, first)
	limits := NewLimitNotices(LimitNoticesConfig{Steps: first, Told: first, Route: router, Now: limitNow})
	// The check before any step is limited reads the marks, and finds none.
	limits.check(ctx)
	if err := first.ApplyEngine(ctx, limitStepOps("a", "b"), limitNow()); err != nil {
		t.Fatalf("ApplyEngine limiting a and b: %v", err)
	}
	// The check after they are limited routes their usage limit once, and marks it told in the store.
	limits.check(ctx)
	router.Wait()
	phone.Wait()

	const body = "2 steps held by the usage limit, reset 2026-11-14T17:00:00Z: a, b"
	if got := log.got(); len(got) != 1 {
		t.Fatalf("lines before the restart = %+v, want the one routed limit notice", got)
	}
	notes := sender.publishes()
	if len(notes) != 1 || notes[0].Limit != resetKey().Limit || notes[0].Summary != body {
		t.Fatalf("pushes before the restart = %+v, want the usage limit notice %q once", notes, body)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close store before the restart: %v", err)
	}

	second, err := store.Open(ctx, path, storetest.NoSync())
	if err != nil {
		t.Fatalf("reopen store after the restart: %v", err)
	}
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("close reopened store: %v", err)
		}
	})
	limited, err := second.LimitedSteps(ctx)
	if err != nil {
		t.Fatalf("LimitedSteps after the restart: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("limited steps after the restart = %+v, want a and b still limited", limited)
	}

	restartedSender := &fakeSender{}
	restartedLog := &fakeRouteLog{}
	restartedPhone := notice.New(sessionTestHost, restartedSender, fixedClock, slog.New(slog.DiscardHandler))
	restarted := newSessionRouter(t, restartedPhone, restartedLog)
	restarted.Attach(ctx, second)
	ticks := make(chan time.Time)
	rebuilt := NewLimitNotices(LimitNoticesConfig{
		Steps: second, Told: second, Route: restarted, Now: limitNow, Tick: ticks,
	})
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		rebuilt.Run(runCtx)
	}()
	ticks <- limitNow()
	cancel()
	<-done
	restarted.Wait()
	restartedPhone.Wait()

	if got := restartedLog.got(); len(got) != 0 {
		t.Fatalf("lines after the restart = %+v, want none: the rebuilt router routed the limit again", got)
	}
	if got := restartedSender.publishes(); len(got) != 0 {
		t.Fatalf("pushes after the restart = %+v, want none: the limit was sent before the restart", got)
	}
}
