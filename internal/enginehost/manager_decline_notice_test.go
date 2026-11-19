package enginehost

import (
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// TestDeclineHeadlessEndsPendingNotice declines the open question of a session that asks one (ASK). Once the interrupt
// returns, the notifier reports the session seen at the manager's time, so the phone gets one seen report and the
// desk's pending notice ends, which the route log shows as a line that routes to none.
func TestDeclineHeadlessEndsPendingNotice(t *testing.T) {
	t.Parallel()
	rig := newNotNowRig(t)
	id, err := rig.m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waittest.Until(t, nil, "the waiting notice at the desk", func() bool {
		return len(rig.log.got()) == 1 && rig.after.armed() == 1
	})
	waiting := waitHistory(t, engineOf(rig.m), rig.path, id, "the waiting state", func(h store.EngineHistory) bool {
		return h.State == "waiting"
	})

	if _, err = rig.m.Decline(t.Context(), id, requestIDOf(t, waiting)); err != nil {
		t.Fatalf("Decline: %v", err)
	}
	wantSeen := []phoneSeen{{key: notice.Key{Session: id}, seen: fixedClock().Unix()}}
	if got := rig.phone.seenCalls(); !slices.Equal(got, wantSeen) {
		t.Fatalf("phone seen reports after Decline = %+v, want %+v", got, wantSeen)
	}
	got := rig.log.got()
	if len(got) == 0 || got[len(got)-1].Route != route.None {
		t.Fatalf("lines after Decline = %+v, want the last to route to none", got)
	}
}

// TestDeclineHeadlessRefusalKeepsNotice declines a request id the session has not open. The call returns
// ErrQuestionNotOpen naming the id, and it reports nothing: after the router settles, the phone has no seen report and
// the route log still holds only the waiting notice's desk line.
func TestDeclineHeadlessRefusalKeepsNotice(t *testing.T) {
	t.Parallel()
	rig := newNotNowRig(t)
	id, err := rig.m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waittest.Until(t, nil, "the waiting notice at the desk", func() bool {
		return len(rig.log.got()) == 1 && rig.after.armed() == 1
	})
	waitHistory(t, engineOf(rig.m), rig.path, id, "the waiting state", func(h store.EngineHistory) bool {
		return h.State == "waiting"
	})

	_, err = rig.m.Decline(t.Context(), id, "no-such-request")
	wantRefused(t, "Decline", err, ErrQuestionNotOpen, "no-such-request")
	rig.router.Wait()
	if got := rig.phone.seenCalls(); len(got) != 0 {
		t.Fatalf("phone seen reports after a refused Decline = %+v, want none", got)
	}
	atDesk := route.Line{
		Time: fixedClock().Unix(), Host: sessionTestHost, Session: id,
		State: string(protocol.StateWaiting), Route: route.Desk, Reason: "at the desk",
	}
	if got := rig.log.got(); len(got) != 1 || got[0] != atDesk {
		t.Fatalf("lines after a refused Decline = %+v, want only [%+v]", got, atDesk)
	}
}
