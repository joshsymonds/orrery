package enginehost

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// ownerWords is the text of the owner's prompt that seedQuietRow stores, with the prompt id p-owner.
const ownerWords = "owner words"

// seedQuietRow adds the working engine row id in dir, with the Claude id the fake claude reports. Its owner sent
// ownerWords and then interrupted it, so the interrupt is the newest of the two and the row is quiet (see
// Store.QuietSessions). The owner's prompt carries no write id, so a resume does not send it again to a new holder.
func seedQuietRow(t *testing.T, s *store.Store, id, dir string) {
	t.Helper()
	seedClaudeRow(t, s, id, dir, "working", store.KindEngine)
	if err := s.ApplyEngine(t.Context(), []store.EngineOp{
		store.EnginePrompt{ID: id, Text: ownerWords, PromptID: "p-owner", Sender: store.PromptSenderOwner},
		store.EngineInterrupt{ID: id, Turning: true},
	}, time.Unix(2, 0)); err != nil {
		t.Fatalf("seed owner interrupt of %s: %v", id, err)
	}
}

// quietFailingStore is a Store whose read of the quiet sessions fails with err. Its other reads go to the store.
type quietFailingStore struct {
	Store
	err error
}

func (q quietFailingStore) QuietSessions(context.Context) ([]string, error) {
	return nil, q.err
}

// TestQuietRestartQuietBeforeSayAndNotResumed restores a session that its owner interrupted from a store, the way a
// restart finds it. Its working row with a Claude id is not quiet until Reconcile has read the store. Once Reconcile
// has run, the session is quiet, before any Say. The row is interrupted and gets the interruption notice, and no Resume
// working. prompt is stored or sent.
func TestQuietRestartQuietBeforeSayAndNotResumed(t *testing.T) {
	t.Parallel()
	s, path := openStore(t)
	dir := t.TempDir()
	seedQuietRow(t, s, "quiet", dir)
	requireEngineRows(t, s, map[string]restartRow{"quiet": {state: "working"}})
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	sink := &fakeSink{}
	m := newReconcileManager(t, s, writer, sink)
	if isQuiet(m, "quiet") {
		t.Fatal("session quiet before Reconcile, want it quiet only once Reconcile has read the store")
	}

	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if !isQuiet(m, "quiet") {
		t.Fatal("session not quiet after Reconcile, want it quiet before any Say")
	}
	requireEngineRows(t, s, map[string]restartRow{
		"quiet": {state: "interrupted", reason: restartedReason},
	})
	want := Notice{Session: "quiet", Dir: dir, State: "interrupted", Body: restartedReason}
	if got := sink.got(); len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("notices = %+v, want exactly [%+v]", got, want)
	}
	if texts := promptTexts(t, path, "quiet"); !slices.Equal(texts, []string{ownerWords}) {
		t.Fatalf("prompts after Reconcile = %q, want only the owner's, with no %q", texts, resumePrompt)
	}
	if starts := fakeStarts(t, dir); len(starts) != 0 {
		t.Fatalf("fake claude starts for a quiet session = %q, want none", starts)
	}
}

// TestQuietRestartCallerEventAndCronHeldUntilOwnerSays restores a quiet session, then checks that a caller event and a
// cron fire to it are held: Deliver and DeliverCron return ErrCallerQuiet, start no process and store no prompt. The
// owner's Say then frees both: the session resumes on its Claude id, and the cron fire and the event are delivered,
// each as its own prompt.
func TestQuietRestartCallerEventAndCronHeldUntilOwnerSays(t *testing.T) {
	t.Parallel()
	s, path := openStore(t)
	dir := t.TempDir()
	seedQuietRow(t, s, "quiet", dir)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	m := newReconcileManager(t, s, writer, &fakeSink{})
	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	event := DeliverRequest{ID: "quiet", Text: "event words", PromptID: "e1"}
	if err := m.Deliver(t.Context(), event); !errors.Is(err, ErrCallerQuiet) {
		t.Fatalf("Deliver to the quiet session error = %v, want ErrCallerQuiet", err)
	}
	cron := DeliverRequest{ID: "quiet", Text: "cron words", PromptID: "cron-1"}
	if err := m.DeliverCron(t.Context(), cron); !errors.Is(err, ErrCallerQuiet) {
		t.Fatalf("DeliverCron to the quiet session error = %v, want ErrCallerQuiet", err)
	}
	if starts := fakeStarts(t, dir); len(starts) != 0 {
		t.Fatalf("fake claude starts while quiet = %q, want none", starts)
	}
	if texts := promptTexts(t, path, "quiet"); !slices.Equal(texts, []string{ownerWords}) {
		t.Fatalf("prompts while quiet = %q, want only the owner's", texts)
	}

	if err := m.Say(t.Context(), SayRequest{ID: "quiet", Text: "owner again", PromptID: "p-owner-2"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	if isQuiet(m, "quiet") {
		t.Fatal("session still quiet after the owner's Say, want it free")
	}
	// The owner's turn ends before the cron fire, so the session holds no turn when the fire is claimed.
	waitHistory(t, engineOf(m), path, "quiet", "the owner's turn's end", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && turnEndCount(h) == 1 && h.State == string(protocol.StateDone)
	})
	if err := m.DeliverCron(
		t.Context(),
		DeliverRequest{ID: "quiet", Text: "cron words", PromptID: "cron-1"},
	); err != nil {
		t.Fatalf("DeliverCron after the owner's Say: %v", err)
	}
	if err := m.Deliver(t.Context(), DeliverRequest{ID: "quiet", Text: "event words", PromptID: "e1"}); err != nil {
		t.Fatalf("Deliver after the owner's Say: %v", err)
	}
	history := waitHistory(
		t, engineOf(m), path, "quiet", "the cron and event turns' ends", func(h store.EngineHistory) bool {
			return hasBlock(t, h, "fake reply 3") && turnEndCount(h) == 3 && h.State == string(protocol.StateDone)
		},
	)
	want := []deliveredPrompt{
		{Text: ownerWords, PromptID: "p-owner", Sender: store.PromptSenderOwner},
		{Text: "owner again", PromptID: "p-owner-2", Sender: store.PromptSenderOwner},
		{Text: "cron words", PromptID: "cron-1", Sender: store.PromptSenderCron},
		{Text: "event words", PromptID: "e1", Sender: store.PromptSenderEvent},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts after the owner's Say = %+v, want %+v", got, want)
	}
}

// TestQuietRestartOwnerPromptNewerIsNotQuiet restores a session whose owner sent a prompt after its interrupt. It is
// not quiet, so Reconcile resumes its working turn as it does any interrupted turn with a Claude id.
func TestQuietRestartOwnerPromptNewerIsNotQuiet(t *testing.T) {
	t.Parallel()
	s, path := openStore(t)
	dir := t.TempDir()
	seedClaudeRow(t, s, "spoken", dir, "working", store.KindEngine)
	if err := s.ApplyEngine(t.Context(), []store.EngineOp{
		store.EngineInterrupt{ID: "spoken", Turning: true},
		store.EnginePrompt{ID: "spoken", Text: ownerWords, PromptID: "p-owner", Sender: store.PromptSenderOwner},
	}, time.Unix(2, 0)); err != nil {
		t.Fatalf("seed owner prompt after interrupt: %v", err)
	}
	stallNextPrompt(t, dir)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	sink := &fakeSink{}
	m := newReconcileManager(t, s, writer, sink)

	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if isQuiet(m, "spoken") {
		t.Fatal("session quiet though its owner spoke after the interrupt, want it not quiet")
	}
	requireResumed(t, s, path, "spoken", dir, sink)
}

// TestQuietRestartFailedQuietReadResumesNoRow restores a working row with a Claude id that no quiet session names,
// while the store's read of the quiet sessions fails. Reconcile resumes no row: the row is interrupted with the
// interruption notice, and the read's error is the error Reconcile returns.
func TestQuietRestartFailedQuietReadResumesNoRow(t *testing.T) {
	t.Parallel()
	errQuiet := errors.New("quiet read failed")
	s, _ := openStore(t)
	dir := t.TempDir()
	seedClaudeRow(t, s, "working", dir, "working", store.KindEngine)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	sink := &fakeSink{}
	m := newReconcileManager(t, quietFailingStore{Store: s, err: errQuiet}, writer, sink)

	if err := m.Reconcile(t.Context()); !errors.Is(err, errQuiet) {
		t.Fatalf("Reconcile error = %v, want one wrapping %v", err, errQuiet)
	}

	requireEngineRows(t, s, map[string]restartRow{
		"working": {state: "interrupted", reason: restartedReason},
	})
	want := Notice{Session: "working", Dir: dir, State: "interrupted", Body: restartedReason}
	if got := sink.got(); len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("notices = %+v, want exactly [%+v]", got, want)
	}
	if starts := fakeStarts(t, dir); len(starts) != 0 {
		t.Fatalf("fake claude starts after a failed quiet read = %q, want none", starts)
	}
}
