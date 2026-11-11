package enginehost

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"

	// The blank import registers the pure-Go "sqlite" driver the empty-store check opens the file with.
	_ "modernc.org/sqlite"
)

// managerGuard bounds each wait for a process or a stored row. It is a hang guard, never a timing assumption.
const managerGuard = 30 * time.Second

// pollEvery is the pause between checks inside a bounded wait.
const pollEvery = 10 * time.Millisecond

// fixedClock is the clock the manager tests give the manager, so the seen timestamp is known.
func fixedClock() time.Time { return time.Unix(1_700_000_000, 0) }

// fakeClaudePath returns the absolute path of the scripted fake claude, and checks it is executable.
func fakeClaudePath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", "fakeclaude.sh"))
	if err != nil {
		t.Fatalf("absolute fake claude path: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat fake claude: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("fake claude %s is not executable: mode %v", path, info.Mode())
	}
	return path
}

// newTestManager returns a manager with the given cap that runs the claude at claude, backed by a store and a
// running writer. Close runs before the writer stops and the store closes.
func newTestManager(t *testing.T, limit int, claude string) (*Manager, string) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	s, path := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	m := NewManager(Config{
		Shell:   sh,
		Claude:  claude,
		Environ: []string{"PATH=" + os.Getenv("PATH")},
		Cap:     limit,
		Writer:  writer,
		Now:     fixedClock,
	})
	t.Cleanup(m.Close)
	return m, path
}

// waitUntil polls ok until it returns true, failing the test when managerGuard passes first.
func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(managerGuard)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within %v", what, managerGuard)
		}
		time.Sleep(pollEvery)
	}
}

// historyOf reads the engine history of id from the store at path.
func historyOf(t *testing.T, path, id string) store.EngineHistory {
	t.Helper()
	history, err := store.NewReader(path).EngineHistory(t.Context(), id, 0, 10_000)
	if err != nil {
		t.Fatalf("EngineHistory %s: %v", id, err)
	}
	return history
}

// waitHistory waits until the engine history of id satisfies ok, and returns it.
func waitHistory(t *testing.T, path, id, what string, ok func(store.EngineHistory) bool) store.EngineHistory {
	t.Helper()
	var history store.EngineHistory
	waitUntil(t, what, func() bool {
		reader := store.NewReader(path)
		h, err := reader.EngineHistory(t.Context(), id, 0, 10_000)
		if err != nil {
			return false
		}
		history = h
		return ok(h)
	})
	return history
}

// hasBlock reports whether the history holds an engine_block whose text is text.
func hasBlock(t *testing.T, history store.EngineHistory, text string) bool {
	t.Helper()
	for _, event := range history.Events {
		if event.Kind != store.EventEngineBlock {
			continue
		}
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatalf("decode block payload %q: %v", event.Payload, err)
		}
		if payload.Text == text {
			return true
		}
	}
	return false
}

// changedFields returns the fields of every engine_changed event in the history, in seq order.
func changedFields(t *testing.T, history store.EngineHistory) []map[string]string {
	t.Helper()
	var all []map[string]string
	for _, event := range history.Events {
		if event.Kind != store.EventEngineChanged {
			continue
		}
		var payload struct {
			Fields map[string]string `json:"fields"`
		}
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatalf("decode changed payload %q: %v", event.Payload, err)
		}
		all = append(all, payload.Fields)
	}
	return all
}

// fakePID waits for the fake claude in dir to write its pid file, and returns the pid.
func fakePID(t *testing.T, dir string) int {
	t.Helper()
	path := filepath.Join(dir, "fakeclaude.pid")
	var pid int
	waitUntil(t, "fake claude pid file "+path, func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			t.Fatalf("parse pid file %q: %v", data, err)
		}
		return true
	})
	return pid
}

// requireGone waits until no process has pid, failing the test when managerGuard passes first.
func requireGone(t *testing.T, pid int) {
	t.Helper()
	waitUntil(t, "fake claude process "+strconv.Itoa(pid)+" exit", func() bool {
		return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
	})
}

// requireSessionID checks id is 128 random bits in lowercase hex.
func requireSessionID(t *testing.T, id string) {
	t.Helper()
	raw, err := hex.DecodeString(id)
	if err != nil {
		t.Fatalf("session id %q is not hex: %v", id, err)
	}
	if len(raw) != 16 {
		t.Fatalf("session id %q has %d bytes, want 16", id, len(raw))
	}
}

// countRows returns the number of rows of table in the store at path, read on a separate connection.
func countRows(t *testing.T, path, table string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close store connection: %v", err)
		}
	}()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestStartRecordsRowThenRunsFirstPrompt(t *testing.T) {
	m, path := newTestManager(t, 2, fakeClaudePath(t))
	dir := t.TempDir()

	id, err := m.Start(t.Context(), dir, "hi")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	requireSessionID(t, id)

	history := waitHistory(t, path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if len(history.Events) == 0 || history.Events[0].Kind != store.EventEngineAdded {
		t.Fatalf("first event = %+v, want %s", history.Events, store.EventEngineAdded)
	}
	var added struct {
		Dir   string `json:"dir"`
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(history.Events[0].Payload), &added); err != nil {
		t.Fatalf("decode added payload: %v", err)
	}
	if added.State != string(protocol.StateWorking) || added.Dir != dir {
		t.Fatalf("added row = %+v, want state working in %s", added, dir)
	}
	pid := fakePID(t, dir)
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("fake claude pid %d is not running: %v", pid, err)
	}
}

func TestCapRefusesStartPastCapAndAdmitsAfterStop(t *testing.T) {
	m, _ := newTestManager(t, 2, fakeClaudePath(t))
	first, err := m.Start(t.Context(), t.TempDir(), "one")
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if _, err := m.Start(t.Context(), t.TempDir(), "two"); err != nil {
		t.Fatalf("second Start: %v", err)
	}

	id, err := m.Start(t.Context(), t.TempDir(), "three")
	if !errors.Is(err, ErrCapReached) {
		t.Fatalf("third Start error = %v, want ErrCapReached", err)
	}
	if !strings.Contains(err.Error(), "cap 2") {
		t.Fatalf("third Start error %q does not give the cap", err)
	}
	if id != "" {
		t.Fatalf("refused Start returned id %q, want none", id)
	}

	if err := m.Stop(t.Context(), first); err != nil {
		t.Fatalf("Stop first: %v", err)
	}
	if _, err := m.Start(t.Context(), t.TempDir(), "three again"); err != nil {
		t.Fatalf("Start after Stop freed a slot: %v", err)
	}
}

func TestStopRecordsStoppedAndEndsProcess(t *testing.T) {
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), dir, "hi")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	pid := fakePID(t, dir)

	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := historyOf(t, path, id).State; got != "stopped" {
		t.Fatalf("state after Stop = %q, want stopped", got)
	}
	requireGone(t, pid)
	if err := m.Stop(t.Context(), id); !errors.Is(err, ErrNoSession) {
		t.Fatalf("second Stop error = %v, want ErrNoSession", err)
	}
}

func TestStopRemovesUnfinishedText(t *testing.T) {
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id, err := m.Start(t.Context(), t.TempDir(), "STALL")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, "the stalled block's unfinished text", func() bool { return countRows(t, path, "engine_text") == 1 })

	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := countRows(t, path, "engine_text"); got != 0 {
		t.Fatalf("unfinished text rows after Stop = %d, want 0", got)
	}
}

func TestSeenMarksStartedSession(t *testing.T) {
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id, err := m.Start(t.Context(), t.TempDir(), "hi")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Seen(t.Context(), id); err != nil {
		t.Fatalf("Seen: %v", err)
	}

	want := strconv.FormatInt(fixedClock().Unix(), 10)
	history := historyOf(t, path, id)
	found := false
	for _, fields := range changedFields(t, history) {
		if fields["seen"] == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("no engine_changed sets seen to %s in %+v", want, changedFields(t, history))
	}
	if err := m.Seen(t.Context(), "0123456789abcdef0123456789abcdef"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("Seen of an unknown id error = %v, want ErrNoSession", err)
	}
}

func TestStartRefusesBadInputAndStoresNothing(t *testing.T) {
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	cases := []struct {
		name   string
		dir    string
		prompt string
		want   error
	}{
		{name: "missing directory", dir: filepath.Join(t.TempDir(), "missing"), prompt: "hi", want: ErrBadDir},
		{name: "relative directory", dir: "relative/dir", prompt: "hi", want: ErrBadDir},
		{name: "empty prompt", dir: t.TempDir(), prompt: "", want: ErrEmptyPrompt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := m.Start(t.Context(), tc.dir, tc.prompt)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Start error = %v, want %v", err, tc.want)
			}
			if id != "" {
				t.Fatalf("refused Start returned id %q, want none", id)
			}
		})
	}
	if n := countRows(t, path, "sessions"); n != 0 {
		t.Fatalf("sessions rows = %d, want 0", n)
	}
	if n := countRows(t, path, "events"); n != 0 {
		t.Fatalf("events rows = %d, want 0", n)
	}
}

func TestUnresolvableClaudeRecordsFailedWithReason(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-claude")
	m, path := newTestManager(t, 1, missing)
	id, err := m.Start(t.Context(), t.TempDir(), "hi")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	history := waitHistory(t, path, id, "the failed state", func(h store.EngineHistory) bool {
		return h.State == "failed"
	})
	reason := ""
	for _, fields := range changedFields(t, history) {
		if fields["reason"] != "" {
			reason = fields["reason"]
		}
	}
	if reason == "" {
		t.Fatalf("failed session has no reason in %+v", changedFields(t, history))
	}
}

func TestCloseInterruptsRunningTurnAndKeepsEndedTurn(t *testing.T) {
	m, path := newTestManager(t, 3, fakeClaudePath(t))
	stalledDir := t.TempDir()
	stalled, err := m.Start(t.Context(), stalledDir, "STALL")
	if err != nil {
		t.Fatalf("Start stalled: %v", err)
	}
	endedDir := t.TempDir()
	ended, err := m.Start(t.Context(), endedDir, "hi")
	if err != nil {
		t.Fatalf("Start ended: %v", err)
	}
	waitHistory(t, path, ended, "the ended turn's done state", func(h store.EngineHistory) bool {
		return h.State == "done"
	})
	waitUntil(t, "the stalled block's unfinished text", func() bool { return countRows(t, path, "engine_text") == 1 })
	stalledPID := fakePID(t, stalledDir)
	endedPID := fakePID(t, endedDir)

	m.Close()

	requireGone(t, stalledPID)
	requireGone(t, endedPID)
	stalledHistory := historyOf(t, path, stalled)
	if stalledHistory.State != "interrupted" {
		t.Fatalf("running session state after Close = %q, want interrupted", stalledHistory.State)
	}
	reason := ""
	for _, fields := range changedFields(t, stalledHistory) {
		if fields["reason"] != "" {
			reason = fields["reason"]
		}
	}
	if want := "orreryd stopped during the turn"; reason != want {
		t.Fatalf("running session reason after Close = %q, want %q", reason, want)
	}
	if got := historyOf(t, path, ended).State; got != "done" {
		t.Fatalf("ended session state after Close = %q, want done", got)
	}
	if n := countRows(t, path, "engine_text"); n != 0 {
		t.Fatalf("unfinished text rows after Close = %d, want 0", n)
	}
}

func TestCloseEndsEverySessionAndRefusesStart(t *testing.T) {
	m, path := newTestManager(t, 3, fakeClaudePath(t))
	ids := make([]string, 0, 2)
	pids := make([]int, 0, 2)
	for i := range 2 {
		dir := t.TempDir()
		id, err := m.Start(t.Context(), dir, "hi")
		if err != nil {
			t.Fatalf("Start %d: %v", i, err)
		}
		ids = append(ids, id)
		// The turn's end, not only its reply, is stored before Close, so Close finds the turn ended.
		waitHistory(t, path, id, "the first reply block and the turn's end", func(h store.EngineHistory) bool {
			return hasBlock(t, h, "fake reply 1") && h.State == "done"
		})
		pids = append(pids, fakePID(t, dir))
	}

	m.Close()
	for i, pid := range pids {
		requireGone(t, pid)
		if got := historyOf(t, path, ids[i]).State; got != "done" {
			t.Fatalf("session %d state after Close = %q, want done", i, got)
		}
	}
	if _, err := m.Start(t.Context(), t.TempDir(), "after close"); err == nil {
		t.Fatalf("Start after Close succeeded, want an error")
	}
	m.Close()
}
