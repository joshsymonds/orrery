package main

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic values only. The steps, their callers and the epic's creator are invented.
const (
	callerEventStep    = "caller-wiring-step"
	callerEventLater   = "caller-wiring-later-step"
	callerEventCaller  = "caller-wiring-step-caller"
	callerEventCreator = "caller-wiring-epic-creator"
	callerEventHost    = "synthetic-host"
	// callerEventParkPasses bounds the rounds the test runs while it waits for the flow to park the epic.
	callerEventParkPasses = 1000
)

// TestCallerEventPassesRunInSessionsHost checks that a --sessions orreryd runs the step caller-event pass and the epic
// caller-event pass over its store on its start, and on each wake of its caller-event delivery. The synthetic step has
// ended under a caller whose sessions row is of kind step. The synthetic epic parks for want of a role file, under a
// steerer of kind step, which also created the epic. A step caller receives nothing, so each pass shows its work as the
// event's mark for its caller. The host then stops without error.
func TestCallerEventPassesRunInSessionsHost(t *testing.T) {
	ctx := t.Context()
	st, getenv := openCallerWiringStore(t)
	createAttentionEpic(t, st, callerEventCreator)
	seedCallerEvents(t, st)

	passed := make(chan struct{}, 16)
	host := startCallerWiringHost(t, st, getenv, passed)
	// The start round sends the seeded step end.
	awaitCallerRound(passed)

	// The flow parks the epic on its own pass, so the wakes run rounds until the park is stored. Each wake returns once
	// its round has finished, so the loop needs no timer and no tick from the flow.
	for rounds := 0; ; rounds++ {
		row, err := st.Epic(ctx, attentionWiringEpic)
		if err != nil {
			t.Fatalf("read epic %s: %v", attentionWiringEpic, err)
		}
		if row.Parked != "" {
			break
		}
		if rounds == callerEventParkPasses {
			t.Fatalf("the flow did not park epic %s within %d caller-event rounds",
				attentionWiringEpic, callerEventParkPasses)
		}
		wakeCallerRound(host, passed)
	}
	// This round began after the park was stored, so it sends the park question.
	wakeCallerRound(host, passed)

	stepEvent := "step:" + callerEventStep
	epicEvent := fmt.Sprintf("epic:%s:Q%d", attentionWiringEpic, parkSeq(t, st))
	for _, mark := range []struct{ event, session string }{
		{stepEvent, callerEventCaller},
		{epicEvent, callerEventCreator},
	} {
		if !markedIn(t, st, mark.event, mark.session) {
			t.Errorf("event %s not marked delivered to %s", mark.event, mark.session)
		}
	}

	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// TestStepEndPushWiringNoTicker checks that a --sessions orreryd delivers a seeded step end from its start pass, and a
// step end stored after the start only once the host's caller-event delivery is woken. The synthetic steps end under a
// caller of kind step, which receives nothing and is marked. The pass-count seam shows that no pass runs between the
// start and the wake, and options has no tick seam.
func TestStepEndPushWiringNoTicker(t *testing.T) {
	st, getenv := openCallerWiringStore(t)
	addStepSession(t, st, callerEventCaller)
	endWiringStep(t, st, callerEventStep, "all green")

	passed := make(chan struct{}, 16)
	host := startCallerWiringHost(t, st, getenv, passed)
	awaitCallerRound(passed)
	if !markedIn(t, st, "step:"+callerEventStep, callerEventCaller) {
		t.Fatalf("seeded step end not delivered by the start pass")
	}

	endWiringStep(t, st, callerEventLater, "stored after the start")
	requireNoCallerRound(t, passed)
	if markedIn(t, st, "step:"+callerEventLater, callerEventCaller) {
		t.Fatalf("step end stored after the start delivered before a wake")
	}

	wakeCallerRound(host, passed)
	if !markedIn(t, st, "step:"+callerEventLater, callerEventCaller) {
		t.Fatalf("step end stored after the start not delivered by the wake")
	}
	requireNoCallerEventTicks(t)

	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// TestEpicEventPushWiringNoTicker checks that a --sessions orreryd delivers a seeded epic event from its start pass, and
// that the store write which stores an epic event wakes the round that delivers it. The synthetic epic is ended as done
// before the start, and paused so the planner takes no action for it. It is released after the start, under a steerer
// of kind step, which receives nothing and is marked. The pass-count seam shows that no pass runs between the start
// round and the release write, which is the proof that no tick runs them, and options has no tick seam.
func TestEpicEventPushWiringNoTicker(t *testing.T) {
	st, getenv := openCallerWiringStore(t)
	createAttentionEpic(t, st, callerEventCreator)
	addStepSession(t, st, callerEventCreator)
	recordCallerCreator(t, st, attentionWiringEpic)
	if err := st.PauseEpic(
		t.Context(),
		attentionWiringEpic,
		store.Maker{By: store.MakerOwner},
		time.Unix(1700000000, 0),
	); err != nil {
		t.Fatalf("pause epic %s: %v", attentionWiringEpic, err)
	}
	endAttentionEpic(t, st, "done")

	passed := make(chan struct{}, 16)
	host := startCallerWiringHost(t, st, getenv, passed)
	awaitCallerRound(passed)
	if !markedIn(t, st, store.EpicPhaseEvent(attentionWiringEpic, "done"), callerEventCreator) {
		t.Fatalf("seeded epic event not delivered by the start pass")
	}

	select {
	case <-passed:
		t.Fatalf("a caller-event pass ran between the start round and the release write, want none")
	default:
	}
	endAttentionEpic(t, st, "released")
	awaitCallerRound(passed)
	released := store.EpicPhaseEvent(attentionWiringEpic, "released")
	if !markedIn(t, st, released, callerEventCreator) {
		t.Fatalf("epic event stored after the start not delivered by the round its store write woke")
	}
	requireNoCallerEventTicks(t)

	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// openCallerWiringStore opens the store of a wiring test in fresh state directories, and returns it with the getenv that
// names them.
func openCallerWiringStore(t *testing.T) (*store.Store, func(string) string) {
	t.Helper()
	env := map[string]string{
		"XDG_STATE_HOME":  t.TempDir(),
		"XDG_CONFIG_HOME": t.TempDir(),
	}
	getenv := func(key string) string { return env[key] }
	path, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store path: %v", err)
	}
	return openPaneStore(t, path), getenv
}

// startCallerWiringHost starts a --sessions host over st. Each of its caller-event passes sends on passed when it
// returns, so a test counts the passes.
func startCallerWiringHost(
	t *testing.T, st *store.Store, getenv func(string) string, passed chan<- struct{},
) *sessionHost {
	t.Helper()
	opts := options{
		socket:            filepath.Join(t.TempDir(), "orreryd.sock"),
		sessionCap:        defaultSessionCap,
		stepCap:           defaultStepCap,
		callerEventPassed: func() { passed <- struct{}{} },
	}
	host, err := startSessions(t.Context(), opts, callerEventHost, st, nil, nil, slog.New(slog.DiscardHandler), getenv)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}
	return host
}

// awaitCallerRound returns once a round of the caller-event delivery has ended, which is once each of its two passes
// has returned.
func awaitCallerRound(passed <-chan struct{}) {
	<-passed
	<-passed
}

// wakeCallerRound wakes the host's caller-event delivery, and returns once the round the wake starts has ended.
func wakeCallerRound(host *sessionHost, passed <-chan struct{}) {
	host.callerEvents.Wake()
	awaitCallerRound(passed)
}

// requireNoCallerRound fails the test when a caller-event pass has returned that the test has not waited for. The test
// calls it after a round it has waited for, so any pass that ran since would show here.
func requireNoCallerRound(t *testing.T, passed <-chan struct{}) {
	t.Helper()
	select {
	case <-passed:
		t.Fatalf("a caller-event pass ran, want none before a wake")
	default:
	}
}

// requireNoCallerEventTicks fails the test when options still has a tick seam for the caller-event passes.
func requireNoCallerEventTicks(t *testing.T) {
	t.Helper()
	typ := reflect.TypeFor[options]()
	for _, field := range []string{"stepEventTicks", "epicEventTicks"} {
		if _, ok := typ.FieldByName(field); ok {
			t.Errorf("options has the tick seam %s, want none: the delivery runs on its start and on wakes", field)
		}
	}
}

// markedIn reports whether event is marked delivered to session in st.
func markedIn(t *testing.T, st *store.Store, event, session string) bool {
	t.Helper()
	marked, err := st.CallerEventMarked(t.Context(), event, session)
	if err != nil {
		t.Fatalf("read mark of %s for %s: %v", event, session, err)
	}
	return marked
}

// seedCallerEvents stores the synthetic step, which has ended under the step-kind session callerEventCaller, and
// records callerEventCreator, another step-kind session, as the creator of the synthetic epic. callerEventCreator also
// steers the epic, as createAttentionEpic set it.
func seedCallerEvents(t *testing.T, st *store.Store) {
	t.Helper()
	addStepSession(t, st, callerEventCaller)
	endWiringStep(t, st, callerEventStep, "all green")
	addStepSession(t, st, callerEventCreator)
	recordCallerCreator(t, st, attentionWiringEpic)
}

// addStepSession stores a session of kind step with row id, which the caller-event passes mark and do not wake.
func addStepSession(t *testing.T, st *store.Store, id string) {
	t.Helper()
	add := store.EngineAdd{ID: id, State: string(protocol.StateDone), Kind: store.KindStep}
	if err := st.ApplyEngine(t.Context(), []store.EngineOp{add}, time.Unix(attentionWiringStamp, 0)); err != nil {
		t.Fatalf("store session %s: %v", id, err)
	}
}

// endWiringStep stores the step id, which the implementer ran for the step session callerEventCaller, and ends it with
// result, passed for gate green.
func endWiringStep(t *testing.T, st *store.Store, id, result string) {
	t.Helper()
	caller := store.EpicCaller{Session: callerEventCaller}
	ops := []store.EngineOp{
		store.StepAdd{ID: id, Agent: "implementer", Prompt: "do it", Caller: caller},
		store.StepReturn{ID: id, Result: result},
		store.StepEnd{ID: id, End: "done", Status: "passed", Reason: "gate green"},
	}
	if err := st.ApplyEngine(t.Context(), ops, time.Unix(attentionWiringStamp, 0)); err != nil {
		t.Fatalf("store the ended step %s: %v", id, err)
	}
}

// recordCallerCreator records the step-kind session callerEventCreator as the creator of epic, by its epic_create call.
func recordCallerCreator(t *testing.T, st *store.Store, epic string) {
	t.Helper()
	creator := store.EpicCaller{Session: callerEventCreator}
	at := time.Unix(attentionWiringStamp, 0)
	recorded, err := st.RecordEpicCall(t.Context(), epic, creator, "epic_create", at)
	if err != nil {
		t.Fatalf("record epic_create of %s: %v", epic, err)
	}
	if !recorded {
		t.Fatalf("epic_create of %s was not recorded: no sessions row resolved for %s", epic, callerEventCreator)
	}
}

// endAttentionEpic ends the synthetic epic in phase.
func endAttentionEpic(t *testing.T, st *store.Store, phase string) {
	t.Helper()
	at := time.Unix(attentionWiringStamp, 0)
	if err := st.EndEpic(t.Context(), attentionWiringEpic, phase, "ended in "+phase, nil, at); err != nil {
		t.Fatalf("end epic %s as %s: %v", attentionWiringEpic, phase, err)
	}
}

// parkSeq returns the seq of the open park question of the synthetic epic, which is its only open question.
func parkSeq(t *testing.T, st *store.Store) int64 {
	t.Helper()
	entries, err := st.Attention(t.Context())
	if err != nil {
		t.Fatalf("attention: %v", err)
	}
	for _, entry := range entries {
		if entry.Epic != attentionWiringEpic {
			continue
		}
		if len(entry.Open) != 1 || entry.Open[0].Label != store.ParkLabel {
			t.Fatalf("open questions of epic %s = %+v, want one park question", attentionWiringEpic, entry.Open)
		}
		return entry.Open[0].Seq
	}
	t.Fatalf("epic %s is not listed by attention", attentionWiringEpic)
	return 0
}
