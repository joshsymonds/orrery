package main

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic values only. The step, its caller and the epic's creator are invented.
const (
	callerEventStep    = "caller-wiring-step"
	callerEventCaller  = "caller-wiring-step-caller"
	callerEventCreator = "caller-wiring-epic-creator"
	callerEventHost    = "synthetic-host"
	// callerEventParkPasses bounds the epic passes the test runs while it waits for the flow to park the epic.
	callerEventParkPasses = 1000
)

// TestCallerEventPassesRunInSessionsHost checks that a --sessions orreryd runs the step caller-event pass and the epic
// caller-event pass over its store. The synthetic step has ended under a caller whose sessions row is of kind step. The
// synthetic epic parks for want of a role file, under a creator whose epic_create was recorded by a step-kind session.
// A step caller receives nothing, so each pass shows its work as the event's mark for its caller. The host then stops
// without error.
func TestCallerEventPassesRunInSessionsHost(t *testing.T) {
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
	seedCallerEvents(t, st)

	stepTicks := make(chan time.Time)
	epicTicks := make(chan time.Time)
	opts := options{
		socket:         filepath.Join(t.TempDir(), "orreryd.sock"),
		sessionCap:     defaultSessionCap,
		stepCap:        defaultStepCap,
		stepEventTicks: stepTicks,
		epicEventTicks: epicTicks,
	}
	host, err := startSessions(ctx, opts, callerEventHost, st, nil, nil, slog.New(slog.DiscardHandler), getenv)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}

	// The flow parks the epic on its own first pass, so the epic pass runs tick by tick until the park is stored. Each
	// sendTicks returns once the pass it started has finished, so the loop has no timer and needs no tick from the flow.
	sendTicks(stepTicks)
	for passes := 0; ; passes++ {
		row, err := st.Epic(ctx, attentionWiringEpic)
		if err != nil {
			t.Fatalf("read epic %s: %v", attentionWiringEpic, err)
		}
		if row.Parked != "" {
			break
		}
		if passes == callerEventParkPasses {
			t.Fatalf("the flow did not park epic %s within %d epic passes", attentionWiringEpic, callerEventParkPasses)
		}
		sendTicks(epicTicks)
	}
	// This pass began after the park was stored, so it sends the park question.
	sendTicks(epicTicks)

	stepEvent := "step:" + callerEventStep
	epicEvent := fmt.Sprintf("epic:%s:Q%d", attentionWiringEpic, parkSeq(t, st))
	for _, mark := range []struct{ event, session string }{
		{stepEvent, callerEventCaller},
		{epicEvent, callerEventCreator},
	} {
		marked, err := st.CallerEventMarked(ctx, mark.event, mark.session)
		if err != nil {
			t.Fatalf("read mark of %s for %s: %v", mark.event, mark.session, err)
		}
		if !marked {
			t.Errorf("event %s not marked delivered to %s", mark.event, mark.session)
		}
	}

	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// seedCallerEvents stores the synthetic step, which has ended under the step-kind session callerEventCaller, and
// records callerEventCreator, another step-kind session, as the creator of the synthetic epic.
func seedCallerEvents(t *testing.T, st *store.Store) {
	t.Helper()
	now := time.Unix(attentionWiringStamp, 0)
	ops := []store.EngineOp{
		store.EngineAdd{ID: callerEventCaller, State: string(protocol.StateDone), Kind: store.KindStep},
		store.StepAdd{
			ID: callerEventStep, Agent: "implementer", Prompt: "do it",
			Caller: store.EpicCaller{Session: callerEventCaller},
		},
		store.StepReturn{ID: callerEventStep, Result: "all green"},
		store.StepEnd{ID: callerEventStep, End: "done", Status: "passed", Reason: "gate green"},
		store.EngineAdd{ID: callerEventCreator, State: string(protocol.StateDone), Kind: store.KindStep},
	}
	if err := st.ApplyEngine(t.Context(), ops, now); err != nil {
		t.Fatalf("store the ended step and its caller: %v", err)
	}
	recorded, err := st.RecordEpicCall(
		t.Context(), attentionWiringEpic, store.EpicCaller{Session: callerEventCreator}, "epic_create", now,
	)
	if err != nil {
		t.Fatalf("record epic_create: %v", err)
	}
	if !recorded {
		t.Fatalf("epic_create was not recorded: no sessions row resolved for %s", callerEventCreator)
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

// sendTicks sends two ticks on ticks. A pass receives each tick only once the pass before it has returned, so the
// second send returns once a pass that began after the first send has returned.
func sendTicks(ticks chan<- time.Time) {
	ticks <- time.Time{}
	ticks <- time.Time{}
}
