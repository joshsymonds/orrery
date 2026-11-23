package server_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// cardRequest is the waits label of a card whose session waits on an open request, as PendingEvent.waits names it.
const cardRequest = "request"

// stoppedEvent is the prompt id the stopped check delivers under. Deliver returns nil for a prompt id already sent, so a
// check after a delivery of speakEvent needs a prompt id of its own.
const stoppedEvent = "step:step-speak:after-stop"

// waitsRun is one session the waits-label tests run: the manager that runs it, the store the manager writes and the
// stream reads, the path of that store, and the sink that closes when the session's first turn ends.
type waitsRun struct {
	m          *enginehost.Manager
	s          *store.Store
	path       string
	sink       *speakNotices
	writerDone chan struct{}
}

// startWaitsRun opens a store, runs its writer and a Manager on the fake claude, and starts speakSession with prompt.
// The writer and the manager close at cleanup. The Manager runs as production does, through a running writer.
func startWaitsRun(t *testing.T, prompt string) waitsRun {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)

	ctx, cancel := context.WithCancel(t.Context())
	writer := enginehost.NewWriter(enginehost.WriterConfig{
		Applier: s, Now: speakFixedClock,
	})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		writer.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-writerDone
	})

	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	claude, err := filepath.Abs(filepath.Join("..", "enginehost", "testdata", "fakeclaude.sh"))
	if err != nil {
		t.Fatalf("absolute fake claude path: %v", err)
	}
	sink := &speakNotices{done: make(chan struct{})}
	m := enginehost.NewManager(enginehost.Config{
		Shell:    sh,
		Claude:   claude,
		Environ:  []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
		Writer:   writer,
		Now:      speakFixedClock,
		Notifier: sink,
		Store:    s,
	})
	t.Cleanup(m.Close)

	if _, err = m.Start(t.Context(), enginehost.StartRequest{
		ID: speakSession, Dir: t.TempDir(), Prompt: prompt,
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return waitsRun{m: m, s: s, path: path, sink: sink, writerDone: writerDone}
}

// waitFirstTurnEnd waits until the first turn of the session ends, so the session is idle.
func (r waitsRun) waitFirstTurnEnd(t *testing.T) {
	t.Helper()
	select {
	case <-r.sink.done:
	case <-r.writerDone:
		t.Fatal("the writer stopped before the first turn's end")
	}
}

// addStepEnd writes the step whose end is the caller event of the waits tests, owed to speakSession.
func (r waitsRun) addStepEnd(t *testing.T) {
	t.Helper()
	applyEngineOps(t, r.s,
		store.StepAdd{
			ID: speakStep, Agent: "implementer", Prompt: "do it", Caller: store.EpicCaller{Session: speakSession},
		},
		store.StepReturn{ID: speakStep, Result: "First line\nSecond line"},
		store.StepEnd{ID: speakStep, End: "done", Status: "passed", Reason: "gate green"},
	)
}

// openStream opens the session stream of speakSession on the store of r.
func (r waitsRun) openStream(t *testing.T) *sessionStream {
	t.Helper()
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(r.path), r.s, nil, 0))
	return openSessionStream(t, client, sessionReq(speakSession, 0, 0))
}

// wantHeld asks the manager to deliver the event's text to speakSession under promptID, as the caller does. The manager
// refuses it with want and stores nothing under promptID.
func (r waitsRun) wantHeld(t *testing.T, promptID string, want error) {
	t.Helper()
	err := r.m.Deliver(t.Context(), enginehost.DeliverRequest{
		ID: speakSession, Text: "event words", PromptID: promptID,
	})
	if !errors.Is(err, want) {
		t.Fatalf("Deliver of a held event error = %v, want %v", err, want)
	}
	r.wantStored(t, promptID, false)
}

// wantDelivered asks the manager to deliver the event's text to speakSession under promptID, as the caller does. The
// manager delivers it and stores its prompt.
func (r waitsRun) wantDelivered(t *testing.T, promptID string) {
	t.Helper()
	if err := r.m.Deliver(t.Context(), enginehost.DeliverRequest{
		ID: speakSession, Text: "event words", PromptID: promptID,
	}); err != nil {
		t.Fatalf("Deliver of a free event: %v", err)
	}
	r.wantStored(t, promptID, true)
}

// wantStored fails the test unless the prompt promptID is stored on speakSession exactly when stored is set.
func (r waitsRun) wantStored(t *testing.T, promptID string, stored bool) {
	t.Helper()
	sent, err := r.s.EnginePromptSent(t.Context(), speakSession, promptID)
	if err != nil {
		t.Fatalf("EnginePromptSent: %v", err)
	}
	if sent != stored {
		t.Fatalf("prompt %s stored = %v, want %v", promptID, sent, stored)
	}
}

// waitForRequest reads the stream until the state carries the session's open request, and returns that message.
func waitForRequest(t *testing.T, stream *sessionStream) *orreryv1.SessionsWatchConversationResponse {
	t.Helper()
	msg := stream.next(t)
	for msg.GetState().GetRequest() == nil {
		msg = stream.nextDelta(t)
	}
	return msg
}

// nextWaits reads the stream's deltas until one labels the session's pending event waits, and returns that delta. The
// deltas it reads past carry the event under another label.
func nextWaits(t *testing.T, stream *sessionStream, waits string) *orreryv1.SessionsWatchConversationResponse {
	t.Helper()
	for {
		msg := stream.nextDelta(t)
		if pending := msg.GetState().GetPending(); len(pending) > 0 && pending[0].GetWaits() == waits {
			return msg
		}
	}
}

// TestWaitsLabelFollowsOwnerInterruptSayAndStop runs one idle session. Its event reads free and the manager delivers
// it. The owner's Interrupt reads it speak, and the manager holds it as a quiet session's. The owner's Say after the
// interrupt reads it free again, and once the event is delivered the owner stops the session: the event reads speak
// from the stopped row alone, since the owner's prompt is newer than the interrupt, and the manager refuses it as a
// stopped session's.
func TestWaitsLabelFollowsOwnerInterruptSayAndStop(t *testing.T) {
	t.Parallel()
	r := startWaitsRun(t, "start")
	r.waitFirstTurnEnd(t)
	r.addStepEnd(t)
	stream := r.openStream(t)
	wantPendingEvents(t, stream.next(t), speakPending(cardFree))

	if _, err := r.m.OwnerInterrupt(t.Context(), speakSession); err != nil {
		t.Fatalf("OwnerInterrupt: %v", err)
	}
	wantPendingEvents(t, nextWaits(t, stream, cardSpeak), speakPending(cardSpeak))
	r.wantHeld(t, speakEvent, enginehost.ErrCallerQuiet)

	say := enginehost.SayRequest{ID: speakSession, Text: "owner words", PromptID: "p-owner"}
	if err := r.m.Say(t.Context(), say); err != nil {
		t.Fatalf("Say: %v", err)
	}
	wantPendingEvents(t, nextWaits(t, stream, cardFree), speakPending(cardFree))
	r.wantDelivered(t, speakEvent)

	if err := r.m.Stop(t.Context(), speakSession); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	wantPendingEvents(t, nextWaits(t, stream, cardSpeak), speakPending(cardSpeak))
	r.wantHeld(t, stoppedEvent, enginehost.ErrCallerStopped)
}

// TestWaitsLabelRequestThenSpeakWhileRequestOpen opens an ASK request on speakSession, which reads the event request
// with no interrupt, and the manager refuses it as a busy session's. An interrupt recorded while the request stays open
// then reads the event speak, and the manager still refuses it. The owner's real Interrupt closes an open request (see
// Manager.Interrupt), so the interrupt is recorded in the store, as the stream reads it, and the request is left open.
func TestWaitsLabelRequestThenSpeakWhileRequestOpen(t *testing.T) {
	t.Parallel()
	r := startWaitsRun(t, "ASK")
	r.addStepEnd(t)
	stream := r.openStream(t)
	wantPendingEvents(t, waitForRequest(t, stream), speakPending(cardRequest))
	r.wantHeld(t, speakEvent, enginehost.ErrSessionBusy)

	applyEngineOps(t, r.s, store.EngineInterrupt{ID: speakSession, Turning: true})
	msg := nextWaits(t, stream, cardSpeak)
	wantPendingEvents(t, msg, speakPending(cardSpeak))
	if msg.GetState().GetRequest() == nil {
		t.Fatal("the open request closed with the interrupt, want it open")
	}
	r.wantHeld(t, speakEvent, enginehost.ErrSessionBusy)
}
