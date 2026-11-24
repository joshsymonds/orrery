package enginehost

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// stdinLines returns the lines the fake claude in dir has read from its stdin, in order.
func stdinLines(t *testing.T, dir string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "fakeclaude.stdin"))
	if err != nil {
		t.Fatalf("read the fake's stdin: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

// isInterrupt reports whether line is an interrupt request.
func isInterrupt(line string) bool {
	return strings.Contains(line, `"subtype":"interrupt"`)
}

// assertWritten checks, as a Say returns, that session id has the prompt promptID written.
func assertWritten(t *testing.T, m *Manager, id, promptID string) {
	t.Helper()
	sent, err := storeOf(t, m).EnginePromptSent(t.Context(), id, promptID)
	if err != nil {
		t.Fatalf("EnginePromptSent: %v", err)
	}
	if !sent {
		t.Fatalf("the Say's prompt %s is not written when Say returns", promptID)
	}
}

// interruptsWritten returns how many interrupt requests the fake claude in dir has read from its stdin.
func interruptsWritten(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	for _, line := range stdinLines(t, dir) {
		if isInterrupt(line) {
			n++
		}
	}
	return n
}

// openStoreDB opens the store at path on a connection of its own, which the test closes when it ends.
func openStoreDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close store connection: %v", err)
		}
	})
	return db
}

// writeIDOf returns the write id the engine_prompt of session id with text text was sent under, which the fake lists as
// answered when its turn takes the prompt in.
func writeIDOf(t *testing.T, path, id, text string) string {
	t.Helper()
	var writeID string
	query := `SELECT write_id FROM events WHERE session_id = ? AND kind = 'engine_prompt' ` +
		`AND json_extract(payload, '$.text') = ?`
	if err := openStoreDB(t, path).QueryRow(query, id, text).Scan(&writeID); err != nil {
		t.Fatalf("write id of prompt %q of session %s: %v", text, id, err)
	}
	return writeID
}

// answeredByTurn returns the uuids each turn end of session id lists as answered in the store at path, in turn order.
func answeredByTurn(t *testing.T, path, id string) [][]string {
	t.Helper()
	rows, err := openStoreDB(t, path).Query(
		`SELECT answered FROM events WHERE session_id = ? AND kind = 'engine_turn_ended' ORDER BY seq`, id,
	)
	if err != nil {
		t.Fatalf("read turn ends of session %s: %v", id, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close turn end rows: %v", err)
		}
	}()
	var turns [][]string
	for rows.Next() {
		var answered sql.NullString
		if err := rows.Scan(&answered); err != nil {
			t.Fatalf("scan turn end of session %s: %v", id, err)
		}
		var uuids []string
		if err := json.Unmarshal([]byte(answered.String), &uuids); err != nil {
			t.Fatalf("decode answered list %q of session %s: %v", answered.String, id, err)
		}
		turns = append(turns, uuids)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate turn ends of session %s: %v", id, err)
	}
	return turns
}

// TestSayNowOwnerTurnTakesSayBeforeItEnds has the owner Say during a turn the owner started, which reads the next line from
// stdin while its Bash call runs (MIDTURN). The Say reaches the fake before that turn ends, the turn lists its uuid as
// answered, and the session ends done after that one turn end. No interrupt is written.
func TestSayNowOwnerTurnTakesSayBeforeItEnds(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "MIDTURN"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "owner words", PromptID: "p-owner"}); err != nil {
		t.Fatalf("Say during the owner's turn: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the owner's turn's end", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake midturn 1") && turnEndCount(h) == 1 && h.State == "done"
	})

	if n := stdinLinesWith(t, dir, "owner words"); n != 1 {
		t.Fatalf("stdin lines with the Say = %d, want it read once", n)
	}
	want := [][]string{{writeIDOf(t, path, id, "MIDTURN"), writeIDOf(t, path, id, "owner words")}}
	if got := answeredByTurn(t, path, id); !reflect.DeepEqual(got, want) {
		t.Fatalf("answered by each turn = %q, want %q", got, want)
	}
	wantPrompts := []deliveredPrompt{
		{Text: "MIDTURN"},
		{Text: "owner words", PromptID: "p-owner", Sender: store.PromptSenderOwner},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, wantPrompts) {
		t.Fatalf("prompts = %+v, want %+v", got, wantPrompts)
	}
	if n := interruptsWritten(t, dir); n != 0 {
		t.Fatalf("interrupts written for the owner's Say = %d, want none", n)
	}
}

// TestSayNowEventTurnTakesSayBeforeItEnds has the owner Say during a turn a caller event started through Deliver, which
// reads the next line from stdin while its Bash call runs (MIDTURN). The Say reaches the fake before that turn ends, and
// the turn lists its uuid as answered beside the event's. No interrupt is written.
func TestSayNowEventTurnTakesSayBeforeItEnds(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "start"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first turn's end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 1 && h.State == "done"
	})
	if err = m.Deliver(t.Context(), DeliverRequest{ID: id, Text: "MIDTURN event", PromptID: "e1"}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "owner words", PromptID: "p-owner"}); err != nil {
		t.Fatalf("Say during the event's turn: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the event's turn's end", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake midturn 2") && turnEndCount(h) == 2 && h.State == "done"
	})

	if n := stdinLinesWith(t, dir, "owner words"); n != 1 {
		t.Fatalf("stdin lines with the Say = %d, want it read once", n)
	}
	want := [][]string{
		{writeIDOf(t, path, id, "start")},
		{writeIDOf(t, path, id, "MIDTURN event"), writeIDOf(t, path, id, "owner words")},
	}
	if got := answeredByTurn(t, path, id); !reflect.DeepEqual(got, want) {
		t.Fatalf("answered by each turn = %q, want %q", got, want)
	}
	wantPrompts := []deliveredPrompt{
		{Text: "start"},
		{Text: "MIDTURN event", PromptID: "e1", Sender: store.PromptSenderEvent},
		{Text: "owner words", PromptID: "p-owner", Sender: store.PromptSenderOwner},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, wantPrompts) {
		t.Fatalf("prompts = %+v, want %+v", got, wantPrompts)
	}
	if n := interruptsWritten(t, dir); n != 0 {
		t.Fatalf("interrupts written for the owner's Say = %d, want none", n)
	}
}

// TestSayNowClaudeTurnTakesSayBeforeItEnds has the owner Say during a turn claude started by itself, once its background
// task ended (BGSELFMID). That turn reads the next line from stdin during its Bash call, so the Say reaches the fake before
// the turn ends and the turn lists its uuid as answered. No interrupt is written.
func TestSayNowClaudeTurnTakesSayBeforeItEnds(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "BGSELFMID"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first turn's end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 1 && h.State == "done"
	})
	if err = os.WriteFile(filepath.Join(dir, "fakeclaude.release"), nil, 0o600); err != nil {
		t.Fatalf("end the background task: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the turn claude started by itself", func(h store.EngineHistory) bool {
		return h.State == "working"
	})
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "owner words", PromptID: "p-owner"}); err != nil {
		t.Fatalf("Say during claude's turn: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "claude's turn's end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 2 && h.State == "done"
	})

	if n := stdinLinesWith(t, dir, "owner words"); n != 1 {
		t.Fatalf("stdin lines with the Say = %d, want it read once", n)
	}
	want := [][]string{
		{writeIDOf(t, path, id, "BGSELFMID")},
		{writeIDOf(t, path, id, "owner words")},
	}
	if got := answeredByTurn(t, path, id); !reflect.DeepEqual(got, want) {
		t.Fatalf("answered by each turn = %q, want %q", got, want)
	}
	if n := interruptsWritten(t, dir); n != 0 {
		t.Fatalf("interrupts written for the owner's Say = %d, want none", n)
	}
}

// TestSayNowWhileBackgroundTaskListedIsWrittenAtOnce has the owner Say while a background task is listed and no turn runs.
// The Say is stored as its prompt as Say returns, and claude answers it. No interrupt is written.
func TestSayNowWhileBackgroundTaskListedIsWrittenAtOnce(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "BACKGROUND"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first turn's end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 1 && h.State == "done"
	})
	live, err := m.live(id)
	if err != nil {
		t.Fatalf("live: %v", err)
	}
	m.mu.Lock()
	background, turning := live.background, live.turning
	m.mu.Unlock()
	if !background || turning {
		t.Fatalf("background task listed = %v, turn running = %v, want listed and not running", background, turning)
	}

	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "owner words", PromptID: "p-owner"}); err != nil {
		t.Fatalf("Say while the task runs: %v", err)
	}
	want := []deliveredPrompt{
		{Text: "BACKGROUND"},
		{Text: "owner words", PromptID: "p-owner", Sender: store.PromptSenderOwner},
	}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts when Say returns = %+v, want %+v", got, want)
	}
	waitHistory(t, engineOf(m), path, id, "the reply to the Say", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2") && turnEndCount(h) == 2 && h.State == "done"
	})
	if n := interruptsWritten(t, dir); n != 0 {
		t.Fatalf("interrupts written for the owner's Say = %d, want none", n)
	}
}

// TestSayNowToIdleSessionIsWrittenAtOnce has the owner Say to a session that is idle: no turn runs, no request is open and
// no background task is listed. The Say is stored as its prompt as Say returns, and claude answers it. No interrupt is
// written.
func TestSayNowToIdleSessionIsWrittenAtOnce(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "start"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first turn's end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 1 && h.State == "done"
	})
	live, err := m.live(id)
	if err != nil {
		t.Fatalf("live: %v", err)
	}
	m.mu.Lock()
	busy := live.busy()
	m.mu.Unlock()
	if busy {
		t.Fatal("session is busy after its turn ended, want it idle")
	}

	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "owner words", PromptID: "p-owner"}); err != nil {
		t.Fatalf("Say to the idle session: %v", err)
	}
	want := []deliveredPrompt{
		{Text: "start"},
		{Text: "owner words", PromptID: "p-owner", Sender: store.PromptSenderOwner},
	}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts when Say returns = %+v, want %+v", got, want)
	}
	waitHistory(t, engineOf(m), path, id, "the reply to the Say", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2") && turnEndCount(h) == 2 && h.State == "done"
	})
	if n := interruptsWritten(t, dir); n != 0 {
		t.Fatalf("interrupts written for the owner's Say = %d, want none", n)
	}
}

// TestSayNowStoresOwnerSenderAndPromptID has the owner Say once with no prompt id and once with one. Each stored prompt has
// the owner as its sender, and the one without a prompt id stores a minted one, which the lifecycle lists under its prompt
// id with the owner as its sender.
func TestSayNowStoresOwnerSenderAndPromptID(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "start"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first turn's end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 1 && h.State == "done"
	})
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "minted words"}); err != nil {
		t.Fatalf("Say without a prompt id: %v", err)
	}
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "named words", PromptID: "p-named"}); err != nil {
		t.Fatalf("Say with a prompt id: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the reply to each Say", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 3") && turnEndCount(h) == 3 && h.State == "done"
	})

	prompts := enginePromptsOf(t, history)
	if len(prompts) != 3 {
		t.Fatalf("prompts = %+v, want the start and the two Says", prompts)
	}
	minted := prompts[1]
	if minted.Text != "minted words" || minted.PromptID == "" || minted.Sender != store.PromptSenderOwner {
		t.Fatalf("Say without a prompt id stored %+v, want owner sender and a minted prompt id", minted)
	}
	if want := (deliveredPrompt{Text: "named words", PromptID: "p-named", Sender: store.PromptSenderOwner}); prompts[2] != want {
		t.Fatalf("Say with a prompt id stored %+v, want %+v", prompts[2], want)
	}
	states, err := store.NewReader(path).PromptStates(t.Context(), id)
	if err != nil {
		t.Fatalf("PromptStates: %v", err)
	}
	for _, promptID := range []string{minted.PromptID, "p-named"} {
		i := slices.IndexFunc(states, func(state store.PromptState) bool { return state.PromptID == promptID })
		if i < 0 {
			t.Fatalf("PromptStates lists no prompt %q: %+v", promptID, states)
		}
		if states[i].Sender != store.PromptSenderOwner {
			t.Fatalf("prompt %q has sender %q in PromptStates, want owner", promptID, states[i].Sender)
		}
	}
}

// TestSayNowRetriedPromptIDWritesNothingMore sends a Say with a prompt id, then sends it again with the same id. The retry
// returns with nothing written, so the fake reads the message once: a later Say is answered as the turn after the first.
func TestSayNowRetriedPromptIDWritesNothingMore(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "start"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first turn's end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 1 && h.State == "done"
	})
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "retry words", PromptID: "p-retry"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the reply to the Say", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2") && turnEndCount(h) == 2 && h.State == "done"
	})

	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "retry words", PromptID: "p-retry"}); err != nil {
		t.Fatalf("retried Say: %v", err)
	}
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "after words", PromptID: "p-after"}); err != nil {
		t.Fatalf("Say after the retry: %v", err)
	}
	history := waitHistory(
		t,
		engineOf(m),
		path,
		id,
		"the reply to the Say after the retry",
		func(h store.EngineHistory) bool {
			return hasBlock(t, h, "fake reply 3") && turnEndCount(h) == 3 && h.State == "done"
		},
	)

	if n := stdinLinesWith(t, dir, "retry words"); n != 1 {
		t.Fatalf("stdin lines with the retried Say = %d, want it read once", n)
	}
	if n := stdinLinesWith(t, dir, "after words"); n != 1 {
		t.Fatalf("stdin lines with the Say after the retry = %d, want it read once", n)
	}
	want := []deliveredPrompt{
		{Text: "start"},
		{Text: "retry words", PromptID: "p-retry", Sender: store.PromptSenderOwner},
		{Text: "after words", PromptID: "p-after", Sender: store.PromptSenderOwner},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
}
