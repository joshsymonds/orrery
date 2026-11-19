package server_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// The step whose end is the caller event the speak-label test leaves owed to its session, and the event's id.
const (
	speakStep  = "step-speak"
	speakEvent = "step:step-speak"
)

// speakSession is the id the speak-label test starts its session with, so its notice sink can tell the session's
// notices from any other notice.
const speakSession = "5e1f0a9c2b7d4e6f8a3c1b0d9e7f2a4c"

// speakPending is the one pending event of the speak-label test, labeled waits.
func speakPending(waits string) *orreryv1.PendingEvent {
	return &orreryv1.PendingEvent{
		Id:        speakEvent,
		Kind:      "step_end",
		Step:      speakStep,
		Agent:     "implementer",
		End:       "done",
		Status:    "passed",
		Brief:     "Return: First line...\nNot shown: return (22 bytes)\nReason: gate green\nMore: step_result id step-speak",
		Waits:     waits,
		StepFacts: &orreryv1.StepFacts{},
	}
}

// speakFixedClock is the clock the speak-label test gives the writer and the manager, so every stored time is known.
func speakFixedClock() time.Time { return time.Unix(eventAt, 0) }

// speakNotices is the NoticeSink of the speak-label test. Its Notify closes done once, on the first done notice of
// speakSession. The manager sends a turn's end only after the writer has committed it, so the close means the store
// holds the first turn's end. Changed and Seen do nothing.
type speakNotices struct {
	once sync.Once
	done chan struct{}
}

// Notify closes done on the first done notice of speakSession.
func (n *speakNotices) Notify(_ context.Context, notice enginehost.Notice) {
	if notice.Session == speakSession && notice.State == "done" {
		n.once.Do(func() { close(n.done) })
	}
}

// Changed does nothing.
func (n *speakNotices) Changed(_ context.Context, _ string) {}

// Seen does nothing.
func (n *speakNotices) Seen(_ context.Context, _ string, _ time.Time) {}

// TestSpeakLabelOwnerSayEndsWaitsSpeak runs one headless session on the fake claude, owes it a caller event, and has
// the owner interrupt it and then Say to it through the manager that writes the store the session stream reads. The
// stream labels the pending event speak after the interrupt, and free again after the owner's Say.
//
// The test's only waits are the first turn's done notice and the stream's messages. The writer's tick is the production
// writer loop the Manager stores through, so the test runs the Manager with a running Writer, as production does.
//
// The test runs a real session because R6 requires an owner Say through the production Manager.Say and the waits label
// as the session stream serves it, and only a Manager with a live session under orrery holder reaches that. The cost is
// one orrery build per package run, shared through TestMain, plus one fake-Claude session.
func TestSpeakLabelOwnerSayEndsWaitsSpeak(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)

	ctx, cancel := context.WithCancel(t.Context())
	writer := enginehost.NewWriter(enginehost.WriterConfig{
		Applier: s, Tick: 5 * time.Millisecond, Now: speakFixedClock,
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
		Cap:      1,
		Writer:   writer,
		Now:      speakFixedClock,
		Notifier: sink,
		Store:    s,
	})
	t.Cleanup(m.Close)

	if _, err = m.Start(t.Context(), enginehost.StartRequest{
		ID: speakSession, Dir: t.TempDir(), Prompt: "start",
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-sink.done:
	case <-writerDone:
		t.Fatal("the writer stopped before the first turn's end")
	}

	applyEngineOps(t, s,
		store.StepAdd{
			ID: speakStep, Agent: "implementer", Prompt: "do it", Caller: store.EpicCaller{Session: speakSession},
		},
		store.StepReturn{ID: speakStep, Result: "First line\nSecond line"},
		store.StepEnd{ID: speakStep, End: "done", Status: "passed", Reason: "gate green"},
	)
	reader := store.NewReader(path)
	client := sessionsWatchClient(t, sessionsHandler(t, reader, nil, 0))
	stream := openSessionStream(t, client, sessionReq(speakSession, 0, 0))
	wantPendingEvents(t, stream.next(t), speakPending(cardFree))

	if _, err = m.OwnerInterrupt(t.Context(), speakSession); err != nil {
		t.Fatalf("OwnerInterrupt: %v", err)
	}
	wantPendingEvents(t, stream.nextDelta(t), speakPending(cardSpeak))

	say := enginehost.SayRequest{ID: speakSession, Text: "owner words", PromptID: "p-owner"}
	if err = m.Say(t.Context(), say); err != nil {
		t.Fatalf("Say: %v", err)
	}
	wantPendingEvents(t, stream.nextDelta(t), speakPending(cardFree))
}
