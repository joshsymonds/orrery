package server_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// The step whose end is the caller event the speak-label test leaves owed to its session, and the event's id.
const (
	speakStep  = "step-speak"
	speakEvent = "step:step-speak"
)

// speakPending is the one pending event of the speak-label test, labeled waits.
func speakPending(waits string) *orreryv1.PendingEvent {
	return &orreryv1.PendingEvent{
		Id: speakEvent, Kind: "step_end", Step: speakStep, Agent: "implementer", End: "done", Status: "passed",
		Reason: "gate green", Summary: "First line", Waits: waits, Result: "First line\nSecond line",
	}
}

// speakFixedClock is the clock the speak-label test gives the writer and the manager, so every stored time is known.
func speakFixedClock() time.Time { return time.Unix(eventAt, 0) }

// installOrrery builds cmd/orrery into a directory of the test's own and puts it first on PATH. A session's holder is
// the orrery that LookPath finds on PATH, since the test binary is not beside one, and the host's orrery may be absent
// or not this tree's.
func installOrrery(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	build := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(dir, "orrery"),
		"github.com/joshsymonds/orrery/cmd/orrery")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build orrery: %v\n%s", err, out)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestSpeakLabelOwnerSayEndsWaitsSpeak runs one headless session on the fake claude, owes it a caller event, and has
// the owner interrupt it and then Say to it through the manager that writes the store the session stream reads. The
// stream labels the pending event speak after the interrupt, and free again after the owner's Say.
func TestSpeakLabelOwnerSayEndsWaitsSpeak(t *testing.T) {
	installOrrery(t)
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
	m := enginehost.NewManager(enginehost.Config{
		Shell:   sh,
		Claude:  claude,
		Environ: []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
		Cap:     1,
		Writer:  writer,
		Now:     speakFixedClock,
		Store:   s,
	})
	t.Cleanup(m.Close)

	id, err := m.Start(t.Context(), enginehost.StartRequest{Dir: t.TempDir(), Prompt: "start"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	reader := store.NewReader(path)
	waittest.Until(t, writerDone, "the first turn's end", func() bool {
		h, err := reader.EngineHistory(t.Context(), id, 0, 10_000)
		return err == nil && h.State == "done"
	})

	applyEngineOps(t, s,
		store.StepAdd{ID: speakStep, Agent: "implementer", Prompt: "do it", Caller: store.EpicCaller{Session: id}},
		store.StepReturn{ID: speakStep, Result: "First line\nSecond line"},
		store.StepEnd{ID: speakStep, End: "done", Status: "passed", Reason: "gate green"},
	)
	client := sessionsWatchClient(t, sessionsHandler(t, reader, nil, 0))
	stream := openSessionStream(t, client, sessionReq(id, 0, 0))
	wantPendingEvents(t, stream.next(t), speakPending(cardFree))

	if _, err = m.OwnerInterrupt(t.Context(), id); err != nil {
		t.Fatalf("OwnerInterrupt: %v", err)
	}
	wantPendingEvents(t, stream.nextDelta(t), speakPending(cardSpeak))

	if err = m.Say(t.Context(), enginehost.SayRequest{ID: id, Text: "owner words", PromptID: "p-owner"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	wantPendingEvents(t, stream.nextDelta(t), speakPending(cardFree))
}
