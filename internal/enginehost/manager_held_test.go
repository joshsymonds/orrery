package enginehost

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// heldTexts returns the text of each message the session id holds in the store at path, in order.
func heldTexts(t *testing.T, path, id string) []string {
	t.Helper()
	held, err := store.NewReader(path).HeldPrompts(t.Context(), id)
	if err != nil {
		t.Fatalf("HeldPrompts %s: %v", id, err)
	}
	texts := make([]string, 0, len(held))
	for _, message := range held {
		texts = append(texts, message.Text)
	}
	return texts
}

// writtenPrompts returns the text of each prompt the session wrote, in order: the engine_prompt events of history. A prompt
// that is held is not one until a turn writes it.
func writtenPrompts(t *testing.T, history store.EngineHistory) []string {
	t.Helper()
	var texts []string
	for _, event := range history.Events {
		if event.Kind != store.EventEnginePrompt {
			continue
		}
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatalf("decode prompt payload %q: %v", event.Payload, err)
		}
		texts = append(texts, payload.Text)
	}
	return texts
}

// TestHeldQueueSayDuringTurnIsHeldUntilTheTurnEnds sends a Say while a JUDGEHOLDS turn holds. The message is stored held, and
// no prompt records it until the turn ends. Then the turn ends, the message is written as the next turn, and claude answers it.
func TestHeldQueueSayDuringTurnIsHeldUntilTheTurnEnds(t *testing.T) {
	t.Parallel()
	_, path := openStore(t)
	m := newManagerOn(t, path, 1, fakeClaudePath(t), &fakeSink{})
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "JUDGEHOLDS"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "held one", PromptID: "p-held"}); err != nil {
		t.Fatalf("Say during the running turn: %v", err)
	}
	if got := heldTexts(t, path, id); !slices.Equal(got, []string{"held one"}) {
		t.Fatalf("held messages during the turn = %q, want [held one]", got)
	}
	if got := writtenPrompts(t, historyOf(t, path, id)); slices.Contains(got, "held one") {
		t.Fatalf("prompts written during the turn = %q, want the held message not among them", got)
	}

	releaseHeldTurn(t, dir)
	history := waitHistory(t, engineOf(m), path, id, "the held message's reply", func(h store.EngineHistory) bool {
		return h.State == "done" && hasBlock(t, h, "fake reply 2")
	})
	if got := writtenPrompts(t, history); !slices.Contains(got, "held one") {
		t.Fatalf("prompts written = %q, want the held message written once its turn ended", got)
	}
	if got := heldTexts(t, path, id); len(got) != 0 {
		t.Fatalf("held messages after the write = %q, want none", got)
	}
	if n := stdinLinesWith(t, dir, "held one"); n != 1 {
		t.Fatalf("stdin lines with the held message = %d, want it read once", n)
	}
}

// TestHeldQueueTwoHeldMessagesAreWrittenOneTurnEachInOrder holds two messages during a JUDGEHOLDS turn. The first is a
// WAITOPEN prompt, which holds its own turn open, so the second is not written while the first runs. Once that turn ends, the
// second is written as the turn after it.
func TestHeldQueueTwoHeldMessagesAreWrittenOneTurnEachInOrder(t *testing.T) {
	t.Parallel()
	_, path := openStore(t)
	m := newManagerOn(t, path, 1, fakeClaudePath(t), &fakeSink{})
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "JUDGEHOLDS"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, text := range []string{"WAITOPEN first", "second"} {
		if err = m.Say(t.Context(), SayRequest{ID: id, Text: text}); err != nil {
			t.Fatalf("Say %q during the running turn: %v", text, err)
		}
	}
	if got := heldTexts(t, path, id); !slices.Equal(got, []string{"WAITOPEN first", "second"}) {
		t.Fatalf("held messages = %q, want both in the order they were said", got)
	}

	releaseHeldTurn(t, dir)
	history := waitHistory(
		t,
		engineOf(m),
		path,
		id,
		"the first held message's write",
		func(h store.EngineHistory) bool {
			return slices.Contains(writtenPrompts(t, h), "WAITOPEN first")
		},
	)
	if got := writtenPrompts(t, history); slices.Contains(got, "second") {
		t.Fatalf("prompts written = %q: the second held message was written during the first's turn", got)
	}
	if err = os.WriteFile(filepath.Join(dir, "fakeclaude.open"), nil, 0o600); err != nil {
		t.Fatalf("open the first held turn: %v", err)
	}
	history = waitHistory(
		t,
		engineOf(m),
		path,
		id,
		"the second held message written and the session ended",
		func(h store.EngineHistory) bool {
			return slices.Contains(writtenPrompts(t, h), "second") && (h.State == "done" || h.State == "failed")
		},
	)
	if !hasBlock(t, history, "fake reply 3") {
		t.Fatalf("session %s ended %s without the second held message's reply: prompts written %q",
			id, history.State, writtenPrompts(t, history))
	}
	written := writtenPrompts(t, history)
	first, second := slices.Index(written, "WAITOPEN first"), slices.Index(written, "second")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("prompts written = %q, want the first held message before the second", written)
	}
	if turns := turnEndCount(history); turns != 3 {
		t.Fatalf("turn ends = %d, want 3: the held turn, then one turn for each held message", turns)
	}
	for _, text := range []string{"WAITOPEN first", "second"} {
		if n := stdinLinesWith(t, dir, text); n != 1 {
			t.Fatalf("stdin lines with %q = %d, want it read once", text, n)
		}
	}
}

// TestHeldQueueFollowedTurnEndRecordsNoDoneAndSendsNoNotice holds a message during a JUDGEHOLDS turn and makes it a WAITOPEN
// prompt, so the turn it opens holds. The first turn's end is recorded working with the held message written as its
// successor, and the owner hears nothing. Only the held turn's own end is recorded done, with one notice.
func TestHeldQueueFollowedTurnEndRecordsNoDoneAndSendsNoNotice(t *testing.T) {
	t.Parallel()
	_, path := openStore(t)
	sink := &fakeSink{}
	m := newManagerOn(t, path, 1, fakeClaudePath(t), sink)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "JUDGEHOLDS"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "WAITOPEN after"}); err != nil {
		t.Fatalf("Say during the running turn: %v", err)
	}
	if got := heldTexts(t, path, id); !slices.Equal(got, []string{"WAITOPEN after"}) {
		t.Fatalf("held messages before the turn's end = %q, want [WAITOPEN after]", got)
	}

	releaseHeldTurn(t, dir)
	history := waitHistory(
		t,
		engineOf(m),
		path,
		id,
		"the first turn's end with the held message written",
		func(h store.EngineHistory) bool {
			return turnEndCount(h) == 1 && slices.Contains(writtenPrompts(t, h), "WAITOPEN after")
		},
	)
	if history.State != "working" {
		t.Fatalf("state after the followed turn's end = %q, want working", history.State)
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices after the followed turn's end = %+v, want none", got)
	}

	if err = os.WriteFile(filepath.Join(dir, "fakeclaude.open"), nil, 0o600); err != nil {
		t.Fatalf("open the held turn: %v", err)
	}
	history = waitHistory(t, engineOf(m), path, id, "the held turn's end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 2 && h.State == "done"
	})
	if doneBetweenTurnEnds(t, history) {
		t.Fatal("done was recorded between the first turn's end and the held turn's end")
	}
	want := Notice{Session: id, Dir: dir, State: "done", Body: "fake reply 2"}
	if got := waitNotices(t, engineOf(m), sink); len(got) != 1 || got[0] != want {
		t.Fatalf("notices after the held turn's end = %+v, want exactly [%+v]", got, want)
	}
}

// TestHeldQueueHeldMessageSurvivesRestartUnwrittenUntilItsTurnEnds holds a message during a JUDGEHOLDS turn and restarts
// orreryd, which re-adopts the session's holder while that turn still runs. The message stays held and unwritten through the
// re-adoption, and the restarted manager writes it when the turn ends.
func TestHeldQueueHeldMessageSurvivesRestartUnwrittenUntilItsTurnEnds(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "JUDGEHOLDS"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := fakePID(t, dir)
	if err = rig.manager.Say(
		t.Context(),
		SayRequest{ID: id, Text: "after restart", PromptID: "p-restart"},
	); err != nil {
		t.Fatalf("Say during the running turn: %v", err)
	}

	rig.crash.refuse()
	restarted, _ := restartAfterCrash(t, rig)
	requireProcessRunning(t, pid)
	if !slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is not live after the restart: live %v", id, restarted.Live())
	}
	if got := heldTexts(t, rig.path, id); !slices.Equal(got, []string{"after restart"}) {
		t.Fatalf("held messages after the re-adoption = %q, want [after restart]", got)
	}
	if got := writtenPrompts(t, historyOf(t, rig.path, id)); slices.Contains(got, "after restart") {
		t.Fatalf("prompts written by the re-adoption = %q, want the held message not among them", got)
	}

	releaseHeldTurn(t, dir)
	waitHistory(t, engineOf(restarted), rig.path, id, "the held message's reply", func(h store.EngineHistory) bool {
		return h.State == "done" && hasBlock(t, h, "fake reply 2")
	})
}

// TestHeldQueueWithdrawReturnsTheTextAndNeverWritesIt withdraws a held message before its turn ends. The withdrawal returns its
// text, and the turn that follows writes nothing, so claude never reads the message.
func TestHeldQueueWithdrawReturnsTheTextAndNeverWritesIt(t *testing.T) {
	t.Parallel()
	_, path := openStore(t)
	m := newManagerOn(t, path, 1, fakeClaudePath(t), &fakeSink{})
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "JUDGEHOLDS"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "withdrawn", PromptID: "p-gone"}); err != nil {
		t.Fatalf("Say during the running turn: %v", err)
	}
	got, err := m.Withdraw(t.Context(), id, "p-gone")
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if !slices.Equal(got, []string{"withdrawn"}) {
		t.Fatalf("Withdraw returned %q, want [withdrawn]", got)
	}

	releaseHeldTurn(t, dir)
	history := waitHistory(t, engineOf(m), path, id, "the first turn's end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 1 && h.State == "done"
	})
	if written := writtenPrompts(t, history); slices.Contains(written, "withdrawn") {
		t.Fatalf("prompts written = %q, want the withdrawn message not among them", written)
	}
	if n := stdinLinesWith(t, dir, "withdrawn"); n != 0 {
		t.Fatalf("stdin lines with the withdrawn message = %d, want none", n)
	}
	if held := heldTexts(t, path, id); len(held) != 0 {
		t.Fatalf("held messages after the withdrawal = %q, want none", held)
	}
}

// TestHeldQueueWithdrawAllReturnsEveryTextInOrder holds two messages during a JUDGEHOLDS turn and withdraws every held message
// of the session. It returns both texts in order, and the turn that follows writes neither.
func TestHeldQueueWithdrawAllReturnsEveryTextInOrder(t *testing.T) {
	t.Parallel()
	_, path := openStore(t)
	m := newManagerOn(t, path, 1, fakeClaudePath(t), &fakeSink{})
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "JUDGEHOLDS"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, message := range []SayRequest{
		{ID: id, Text: "first held", PromptID: "p-1"},
		{ID: id, Text: "second held", PromptID: "p-2"},
	} {
		if err = m.Say(t.Context(), message); err != nil {
			t.Fatalf("Say %q during the running turn: %v", message.Text, err)
		}
	}
	got, err := m.Withdraw(t.Context(), id, "")
	if err != nil {
		t.Fatalf("Withdraw all: %v", err)
	}
	if want := []string{"first held", "second held"}; !slices.Equal(got, want) {
		t.Fatalf("Withdraw all returned %q, want %q", got, want)
	}
	if held := heldTexts(t, path, id); len(held) != 0 {
		t.Fatalf("held messages after withdrawing all = %q, want none", held)
	}

	releaseHeldTurn(t, dir)
	history := waitHistory(t, engineOf(m), path, id, "the first turn's end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 1 && h.State == "done"
	})
	if written := writtenPrompts(
		t,
		history,
	); slices.Contains(written, "first held") ||
		slices.Contains(written, "second held") {
		t.Fatalf("prompts written = %q, want no withdrawn message among them", written)
	}
}

// TestHeldQueueWithdrawAfterTheWriteIsRefused lets a held message be written as the next turn, then withdraws it. The
// withdrawal refuses the prompt id with ErrNotHeld and removes nothing.
func TestHeldQueueWithdrawAfterTheWriteIsRefused(t *testing.T) {
	t.Parallel()
	_, path := openStore(t)
	m := newManagerOn(t, path, 1, fakeClaudePath(t), &fakeSink{})
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "JUDGEHOLDS"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "written", PromptID: "p-written"}); err != nil {
		t.Fatalf("Say during the running turn: %v", err)
	}

	releaseHeldTurn(t, dir)
	waitHistory(t, engineOf(m), path, id, "the written message's reply", func(h store.EngineHistory) bool {
		return h.State == "done" && hasBlock(t, h, "fake reply 2")
	})
	got, err := m.Withdraw(t.Context(), id, "p-written")
	if !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Withdraw of a written message: err = %v, want ErrNotHeld", err)
	}
	if len(got) != 0 {
		t.Fatalf("Withdraw of a written message returned %q, want nothing", got)
	}
	if written := writtenPrompts(t, historyOf(t, path, id)); !slices.Contains(written, "written") {
		t.Fatalf("prompts written = %q, want the written message still recorded", written)
	}
	if held := heldTexts(t, path, id); len(held) != 0 {
		t.Fatalf("held messages = %q, want none", held)
	}
}

// TestHeldQueueHeldMessageWaitsForTheNextSayAfterTheProcessStops holds a message during a JUDGEHOLDS turn and stops the process
// before the turn ends. The turn ends with no process, so the message stays held and does not start the session. The next Say
// starts it, and writes the held message ahead of its own.
func TestHeldQueueHeldMessageWaitsForTheNextSayAfterTheProcessStops(t *testing.T) {
	t.Parallel()
	_, path := openStore(t)
	m := newManagerOn(t, path, 1, fakeClaudePath(t), &fakeSink{})
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "JUDGEHOLDS"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "held across stop", PromptID: "p-stop"}); err != nil {
		t.Fatalf("Say during the running turn: %v", err)
	}
	// The turn is open once claude has read the prompt and named its session, so the stop lands before the turn ends
	// and the Say after it resumes that session rather than starting a fresh one.
	waitUntil(t, engineOf(m), "the JUDGEHOLDS turn's open", func() bool {
		row, rowErr := m.store.EngineSession(t.Context(), id)
		return rowErr == nil && row.ClaudeSession != ""
	})
	if err = m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if slices.Contains(m.Live(), id) {
		t.Fatalf("session %s is live after its Stop: live %v", id, m.Live())
	}
	if held := heldTexts(t, path, id); !slices.Equal(held, []string{"held across stop"}) {
		t.Fatalf("held messages after the stop = %q, want [held across stop]", held)
	}
	if starts := fakeStarts(t, dir); len(starts) != 1 {
		t.Fatalf(
			"fake claude starts after the stop = %q, want the one start: the stop must not start the session",
			starts,
		)
	}

	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "after stop"}); err != nil {
		t.Fatalf("Say after the stop: %v", err)
	}
	history := waitHistory(
		t,
		engineOf(m),
		path,
		id,
		"the Say after the stop written and the session ended",
		func(h store.EngineHistory) bool {
			return slices.Contains(writtenPrompts(t, h), "after stop") && (h.State == "done" || h.State == "failed")
		},
	)
	if !hasBlock(t, history, "fake reply 3") {
		t.Fatalf(
			"session %s ended %s without the reply to the Say after the stop: prompts written %q, fake claude starts %q",
			id,
			history.State,
			writtenPrompts(t, history),
			fakeStarts(t, dir),
		)
	}
	written := writtenPrompts(t, history)
	held, after := slices.Index(written, "held across stop"), slices.Index(written, "after stop")
	if held < 0 || after < 0 || held > after {
		t.Fatalf("prompts written = %q, want the held message before the Say", written)
	}
	if starts := fakeStarts(t, dir); len(starts) != 2 {
		t.Fatalf("fake claude starts = %q, want the start the Say made", starts)
	}
}

// TestHeldQueueSayDeclinesAnOpenRequestAndIsWrittenAtOnce asks a question, which leaves a request open, and then says a
// message. The message declines the request and is written at once, rather than held.
func TestHeldQueueSayDeclinesAnOpenRequestAndIsWrittenAtOnce(t *testing.T) {
	t.Parallel()
	_, path := openStore(t)
	m := newManagerOn(t, path, 1, fakeClaudePath(t), &fakeSink{})
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the question", func(h store.EngineHistory) bool {
		return h.State == "waiting"
	})

	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "owner text", PromptID: "p-owner"}); err != nil {
		t.Fatalf("Say over the open request: %v", err)
	}
	if held := heldTexts(t, path, id); len(held) != 0 {
		t.Fatalf("held messages after a Say over an open request = %q, want none", held)
	}
	if written := writtenPrompts(t, historyOf(t, path, id)); !slices.Contains(written, "owner text") {
		t.Fatalf("prompts written = %q, want the message written at once", written)
	}
	waitHistory(t, engineOf(m), path, id, "the reply to the message", func(h store.EngineHistory) bool {
		return h.State == "done" && hasBlock(t, h, "fake reply 2")
	})
}

// TestHeldQueueRetriedSayOfAHeldPromptIDDoesNothing holds a message under a prompt id during a JUDGEHOLDS turn, then says it
// again with the same prompt id. The retry does nothing: one message stays held, and the turn that follows writes it once.
func TestHeldQueueRetriedSayOfAHeldPromptIDDoesNothing(t *testing.T) {
	t.Parallel()
	_, path := openStore(t)
	m := newManagerOn(t, path, 1, fakeClaudePath(t), &fakeSink{})
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "JUDGEHOLDS"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	message := SayRequest{ID: id, Text: "held once", PromptID: "p-retry"}
	for attempt := range 2 {
		if err = m.Say(t.Context(), message); err != nil {
			t.Fatalf("Say attempt %d during the running turn: %v", attempt+1, err)
		}
	}
	if got := heldTexts(t, path, id); !slices.Equal(got, []string{"held once"}) {
		t.Fatalf("held messages after the retry = %q, want [held once] once", got)
	}

	releaseHeldTurn(t, dir)
	history := waitHistory(t, engineOf(m), path, id, "the held message's reply", func(h store.EngineHistory) bool {
		return h.State == "done" && hasBlock(t, h, "fake reply 2")
	})
	written, n := writtenPrompts(t, history), 0
	for _, text := range written {
		if text == "held once" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("prompts written = %q, want the held message written once", written)
	}
	if n := stdinLinesWith(t, dir, "held once"); n != 1 {
		t.Fatalf("stdin lines with the held message = %d, want it read once", n)
	}
}

// TestHeldQueueIdleCheckWritesAHeldMessageOfAnIdleSession leaves a message held on a session whose turn has ended, as a
// write that failed would. The idle check writes it as the next turn, and it is no longer held.
func TestHeldQueueIdleCheckWritesAHeldMessageOfAnIdleSession(t *testing.T) {
	t.Parallel()
	s, path := openStore(t)
	m := newManagerOn(t, path, 1, fakeClaudePath(t), &fakeSink{})
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hello"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply", func(h store.EngineHistory) bool {
		return h.State == "done" && hasBlock(t, h, "fake reply 1")
	})
	if err = s.ApplyEngine(
		t.Context(),
		[]store.EngineOp{store.EngineHold{ID: id, PromptID: "p-left", Text: "left held"}},
		fixedClock(),
	); err != nil {
		t.Fatalf("hold a message of the idle session: %v", err)
	}

	m.checkIdle(t.Context())
	history := waitHistory(t, engineOf(m), path, id, "the held message's reply", func(h store.EngineHistory) bool {
		return h.State == "done" && hasBlock(t, h, "fake reply 2")
	})
	if got := writtenPrompts(t, history); !slices.Contains(got, "left held") {
		t.Fatalf("prompts written = %q, want the held message written by the idle check", got)
	}
	if held := heldTexts(t, path, id); len(held) != 0 {
		t.Fatalf("held messages after the idle check = %q, want none", held)
	}
}
