package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/holdertest"
	"github.com/joshsymonds/orrery/internal/paneregistry"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// TestMain builds orrery once for the package, because the sessions these tests launch run under orrery holder.
func TestMain(m *testing.M) {
	os.Exit(holdertest.Main(m))
}

// Synthetic values only. The steps, the pane and the seeded caller are invented. The sessions run the engine host's
// fake claude, which answers a plain prompt with "fake reply n", and which answers HANG with a turn that runs until an
// interrupt.
const (
	eventWakeStep      = "wake-wiring-step"
	eventWakeStepEvent = "step:" + eventWakeStep
	eventWakePaneID    = "orrery-wake-pane"
	// eventWakeSignals sizes the channel each pass count fills, so a round never waits on a test that is not reading it.
	eventWakeSignals = 64
	// eventWakeRounds bounds the caller-event rounds a test awaits for a mark.
	eventWakeRounds = 1000
	// eventWakeHistoryLimit bounds the events a read of a session's history returns.
	eventWakeHistoryLimit = 10_000
)

// eventWakeRig holds the seams an event-wake test starts its host with: the channel each caller-event pass sends on
// when it returns, and the function that replaces cli.WakePane, which nil leaves as it is.
type eventWakeRig struct {
	passed   chan struct{}
	wakePane func(ctx context.Context, server, pane, text string) (bool, error)
}

// shortStateRoot creates a state directory whose path is short enough for a holder's socket. A t.TempDir path carries
// the test name and can exceed sun_path's limit. The caller removes the directory.
func shortStateRoot() (string, error) {
	root, err := os.MkdirTemp("", "wake")
	if err != nil {
		return "", fmt.Errorf("create short state directory: %w", err)
	}
	return root, nil
}

// openEventWakeStore opens the store of an event-wake test under a short state directory, and returns it with the
// getenv that names the directory and the store's path.
func openEventWakeStore(t *testing.T) (*store.Store, func(string) string, string) {
	t.Helper()
	root, err := shortStateRoot()
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove %s: %v", root, err)
		}
	})
	env := map[string]string{"XDG_STATE_HOME": root, "XDG_CONFIG_HOME": t.TempDir()}
	getenv := func(key string) string { return env[key] }
	path, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store path: %v", err)
	}
	return openPaneStore(t, path), getenv, path
}

// startEventWakeHost starts a --sessions host over st, whose sessions run the fake claude. Its retry timer never fires,
// so a retry cannot mask a wake, and each caller-event pass sends on rig.passed when it returns. The host stops at
// cleanup. The returned stop ends it early, once, so a test that ends the host itself leaves none running on failure.
func startEventWakeHost(
	t *testing.T, st *store.Store, getenv func(string) string, path string, rig eventWakeRig,
) (*sessionHost, func() error) {
	t.Helper()
	opts := options{
		socket:            filepath.Join(filepath.Dir(path), "orreryd.sock"),
		claude:            eventWakeClaude(t),
		sessionShell:      eventWakeShell(t),
		callerEventPassed: func() { rig.passed <- struct{}{} },
		callerEventTimer:  neverFiringTimer,
		callerWakePane:    rig.wakePane,
	}
	host, err := startSessions(t.Context(), opts, callerEventHost, st, nil, nil, slog.New(slog.DiscardHandler), getenv)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}
	stop := sync.OnceValue(host.stop)
	t.Cleanup(func() {
		if err := stop(); err != nil {
			t.Errorf("stop sessions: %v", err)
		}
	})
	return host, stop
}

// eventWakeClaude returns the absolute path of the engine host's fake claude, the script the sessions here run as
// claude.
func eventWakeClaude(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "internal", "enginehost", "testdata", "fakeclaude.sh"))
	if err != nil {
		t.Fatalf("absolute fake claude path: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat fake claude: %v", err)
	}
	return path
}

// eventWakeShell returns the absolute path of sh, the login shell the sessions here start under.
func eventWakeShell(t *testing.T) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	return sh
}

// neverFiringTimer stands in for the retry timer. It arms nothing that sends, so a failed round's retry never runs.
func neverFiringTimer(time.Duration) (<-chan time.Time, func() bool) {
	return make(chan time.Time), func() bool { return true }
}

// startSession starts a headless session on prompt in a directory of its own, and returns its id. Its turn runs on
// after the call returns.
func startSession(t *testing.T, host *sessionHost, prompt string) string {
	t.Helper()
	id, err := host.manager.Start(t.Context(), enginehost.StartRequest{Dir: t.TempDir(), Prompt: prompt})
	if err != nil {
		t.Fatalf("start session on %q: %v", prompt, err)
	}
	return id
}

// awaitHistory waits until the stored history of session id satisfies ok. A read that fails counts as not yet, since
// the writers of the store may hold it.
func awaitHistory(t *testing.T, path, id, what string, ok func(store.EngineHistory) bool) {
	t.Helper()
	waittest.Until(t, nil, what, func() bool {
		history, err := store.NewReader(path).EngineHistory(t.Context(), id, 0, eventWakeHistoryLimit)
		return err == nil && ok(history)
	})
}

// awaitTurnDone waits until the stored state of session id is done, which is once its turn has ended.
func awaitTurnDone(t *testing.T, path, id string) {
	t.Helper()
	awaitHistory(t, path, id, fmt.Sprintf("session %s is done", id), func(h store.EngineHistory) bool {
		return h.State == string(protocol.StateDone)
	})
}

// awaitEngineBlock waits until the stored history of session id holds an engine block whose payload carries text.
func awaitEngineBlock(t *testing.T, path, id, text string) {
	t.Helper()
	what := fmt.Sprintf("session %s stores the block %q", id, text)
	awaitHistory(t, path, id, what, func(h store.EngineHistory) bool {
		for _, event := range h.Events {
			if event.Kind == store.EventEngineBlock && strings.Contains(event.Payload, text) {
				return true
			}
		}
		return false
	})
}

// promptIDsOf returns the prompt ids of the engine prompts stored for session id, in seq order. A prompt that carries
// no prompt id has an empty one.
func promptIDsOf(t *testing.T, path, id string) []string {
	t.Helper()
	history, err := store.NewReader(path).EngineHistory(t.Context(), id, 0, eventWakeHistoryLimit)
	if err != nil {
		t.Fatalf("engine history of %s: %v", id, err)
	}
	var ids []string
	for _, event := range history.Events {
		if event.Kind != store.EventEnginePrompt {
			continue
		}
		var prompt struct {
			PromptID string `json:"promptId"`
		}
		if err := json.Unmarshal([]byte(event.Payload), &prompt); err != nil {
			t.Fatalf("decode prompt payload %q: %v", event.Payload, err)
		}
		ids = append(ids, prompt.PromptID)
	}
	return ids
}

// occurrences returns how many times id is in ids.
func occurrences(ids []string, id string) int {
	n := 0
	for _, have := range ids {
		if have == id {
			n++
		}
	}
	return n
}

// awaitMarked awaits caller-event rounds until event is marked delivered to session. A round ends once each of its
// passes has returned, so a mark that the round wrote is in the store by the time the round's signals are received. The
// rounds come from the wakes the test's own writes and sessions make, and the test sends none.
func awaitMarked(t *testing.T, st *store.Store, passed <-chan struct{}, event, session string) {
	t.Helper()
	for rounds := 0; !markedIn(t, st, event, session); rounds++ {
		if rounds == eventWakeRounds {
			t.Fatalf("event %s not marked delivered to %s after %d caller-event rounds",
				event, session, eventWakeRounds)
		}
		awaitCallerRound(passed)
	}
}

// stopStepUnder stores the step eventWakeStep under caller, then stops it through the host's Steps. The step has no
// loop, so the stop ends it, stores the end and reports it to the Steps' stored wake, which the test relies on.
func stopStepUnder(t *testing.T, st *store.Store, host *sessionHost, caller store.EpicCaller) {
	t.Helper()
	add := store.StepAdd{ID: eventWakeStep, Agent: "implementer", Prompt: "do it", Caller: caller}
	if err := st.ApplyEngine(t.Context(), []store.EngineOp{add}, time.Unix(attentionWiringStamp, 0)); err != nil {
		t.Fatalf("store step %s: %v", eventWakeStep, err)
	}
	if err := host.steps.Stop(t.Context(), eventWakeStep); err != nil {
		t.Fatalf("stop step %s: %v", eventWakeStep, err)
	}
}

// syncWakePane stores the pane caller of the event-wake tests, which names pane on the tmux server that the store
// column server names, and returns the caller that names it.
func syncWakePane(t *testing.T, st *store.Store, server, pane string) store.EpicCaller {
	t.Helper()
	row := store.Pane{ID: eventWakePaneID, Pane: pane}
	if err := st.SyncPane(t.Context(), server, row, time.Unix(attentionWiringStamp, 0)); err != nil {
		t.Fatalf("sync pane %s: %v", pane, err)
	}
	return store.EpicCaller{Server: server, Pane: pane}
}

// recordWakeCreator records session as the creator of the synthetic epic, by its epic_create call.
func recordWakeCreator(t *testing.T, st *store.Store, session string) {
	t.Helper()
	caller := store.EpicCaller{Session: session}
	at := time.Unix(attentionWiringStamp, 0)
	recorded, err := st.RecordEpicCall(t.Context(), attentionWiringEpic, caller, "epic_create", at)
	if err != nil {
		t.Fatalf("record epic_create of %s: %v", attentionWiringEpic, err)
	}
	if !recorded {
		t.Fatalf("epic_create of %s was not recorded: no sessions row resolved for %s", attentionWiringEpic, session)
	}
}

// steerWakeEpic makes session the steerer of the synthetic epic by a direct update of its row, for a session whose id
// exists only once it has started, after the epic is stored. No store write sets a steerer. The update waits out a
// store write of the host, as the store's own connections do.
func steerWakeEpic(t *testing.T, path, session string) {
	t.Helper()
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "_busy_timeout=5000"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close %s: %v", path, err)
		}
	})
	if _, err = db.ExecContext(t.Context(), `UPDATE epics SET steerer = ? WHERE id = ?`,
		session, attentionWiringEpic); err != nil {
		t.Fatalf("steer epic %s by %s: %v", attentionWiringEpic, session, err)
	}
}

// pauseAttentionEpic pauses the synthetic epic, so the planner takes no action for it and the events under test are the
// only ones the caller-event passes send.
func pauseAttentionEpic(t *testing.T, st *store.Store) {
	t.Helper()
	if err := st.PauseEpic(
		t.Context(),
		attentionWiringEpic,
		store.Maker{By: store.MakerOwner},
		time.Unix(1700000000, 0),
	); err != nil {
		t.Fatalf("pause epic %s: %v", attentionWiringEpic, err)
	}
}

// openOwnerPark parks the synthetic epic on an owner-held class, which opens one park question, and returns its seq.
func openOwnerPark(t *testing.T, st *store.Store) int64 {
	t.Helper()
	at := time.Unix(attentionWiringStamp, 0)
	if err := st.ParkEpic(t.Context(), attentionWiringEpic, store.ParkClassBranch, "the branch moved", at); err != nil {
		t.Fatalf("park epic %s: %v", attentionWiringEpic, err)
	}
	questions, err := st.OpenQuestions(t.Context(), attentionWiringEpic)
	if err != nil {
		t.Fatalf("open questions of epic %s: %v", attentionWiringEpic, err)
	}
	if len(questions) != 1 {
		t.Fatalf("open questions of epic %s = %d, want the park's one", attentionWiringEpic, len(questions))
	}
	return questions[0].Seq
}

// ownerSay sends the owner's words to session id, with a prompt id of its own, so the session's turn runs.
func ownerSay(t *testing.T, host *sessionHost, id string) {
	t.Helper()
	req := enginehost.SayRequest{ID: id, Text: "owner words", PromptID: "p-owner"}
	if err := host.manager.Say(t.Context(), req); err != nil {
		t.Fatalf("Say to %s: %v", id, err)
	}
}

// TestStepEndPushWiringStoredEndDelivers checks that a step end stored through the host's Steps after the start reaches
// its idle headless caller. The step's stop stores the end, and the Steps' stored wake delivers it, with no wake from
// the test.
func TestStepEndPushWiringStoredEndDelivers(t *testing.T) {
	st, getenv, path := openEventWakeStore(t)
	passed := make(chan struct{}, eventWakeSignals)
	host, stop := startEventWakeHost(t, st, getenv, path, eventWakeRig{passed: passed})
	awaitCallerRound(passed)

	caller := startSession(t, host, "hi")
	awaitTurnDone(t, path, caller)
	stopStepUnder(t, st, host, store.EpicCaller{Session: caller})
	awaitMarked(t, st, passed, eventWakeStepEvent, caller)

	if err := stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// TestStepEndPushWiringQuietCallerAfterSay checks that a step end held by a caller the owner interrupted, and has not
// spoken to since, is sent once the owner's Say clears the quiet, with no wake from the test. The step's stop writes
// the end while the caller is quiet, so its round holds the end, and the Say's wake sends it.
func TestStepEndPushWiringQuietCallerAfterSay(t *testing.T) {
	st, getenv, path := openEventWakeStore(t)
	passed := make(chan struct{}, eventWakeSignals)
	host, stop := startEventWakeHost(t, st, getenv, path, eventWakeRig{passed: passed})
	awaitCallerRound(passed)

	caller := startSession(t, host, "HANG")
	awaitEngineBlock(t, path, caller, "fake hanging 1")
	if err := host.manager.Interrupt(t.Context(), caller); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	awaitTurnDone(t, path, caller)
	stopStepUnder(t, st, host, store.EpicCaller{Session: caller})
	awaitCallerRound(passed)
	if markedIn(t, st, eventWakeStepEvent, caller) {
		t.Fatalf("step end marked while the owner's interrupt keeps its caller quiet")
	}

	ownerSay(t, host, caller)
	awaitMarked(t, st, passed, eventWakeStepEvent, caller)

	if err := stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// TestStepEndPushWiringStoppedCallerAfterResume checks that a step end held by a caller that is stopped is sent once
// the caller is resumed, with no wake from the test. The step's stop writes the end while the caller is stopped, so its
// round holds the end. The owner's Say resumes the stopped session from its row, so the session goes live and the end
// is sent.
func TestStepEndPushWiringStoppedCallerAfterResume(t *testing.T) {
	st, getenv, path := openEventWakeStore(t)
	passed := make(chan struct{}, eventWakeSignals)
	host, stop := startEventWakeHost(t, st, getenv, path, eventWakeRig{passed: passed})
	awaitCallerRound(passed)

	caller := startSession(t, host, "hi")
	awaitTurnDone(t, path, caller)
	if err := host.manager.Stop(t.Context(), caller); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	stopStepUnder(t, st, host, store.EpicCaller{Session: caller})
	awaitCallerRound(passed)
	if markedIn(t, st, eventWakeStepEvent, caller) {
		t.Fatalf("step end marked for a stopped caller that received nothing")
	}

	ownerSay(t, host, caller)
	awaitMarked(t, st, passed, eventWakeStepEvent, caller)

	if err := stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// serveWakeDaemon serves a daemon with cfg on a socket in a directory of its own. The daemon stops at cleanup, and its
// Serve must return nil.
func serveWakeDaemon(t *testing.T, cfg daemon.Config) {
	t.Helper()
	root, err := shortStateRoot()
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove %s: %v", root, err)
		}
	})
	socket := filepath.Join(root, "orreryd.sock")
	ln, err := daemon.Listen(socket)
	if err != nil {
		t.Fatalf("listen on %s: %v", socket, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := serveInBackground(ctx, ln, cfg)
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serve daemon: %v", err)
		}
	})
}

// tmuxSocketOf returns the socket path of the tmux server client talks to, which a frame names in its Tmux.
func tmuxSocketOf(t *testing.T, client *tmuxctl.Client) string {
	t.Helper()
	out, err := client.Run(t.Context(), "display", "-p", "#{socket_path}")
	if err != nil {
		t.Fatalf("display socket_path: %v", err)
	}
	return strings.TrimSpace(out)
}

// paneUpdate returns an update of pane on the tmux server at socket, with the registry status that starts or ends a
// turn of the pane.
func paneUpdate(pane, socket, status string) daemon.PaneUpdate {
	return daemon.PaneUpdate{PaneID: pane, Tmux: fmt.Sprintf("%s,1234,0", socket), Status: status}
}

// TestStepEndPushWiringPaneStopFrame checks that a step end held by a working pane caller is sent once a registry update
// the daemon applies turns the pane done, which wakes delivery with no wake from the test. The updates are sent to a
// daemon whose observer is the one run builds, and the pane's keys go through a seam that reports the pane idle only
// when its state option reads done.
func TestStepEndPushWiringPaneStopFrame(t *testing.T) {
	st, getenv, path := openEventWakeStore(t)
	passed := make(chan struct{}, eventWakeSignals)
	sent := make(chan string, eventWakeSignals)
	_, client, pane := startPane(t, t.TempDir())
	wakePane := func(ctx context.Context, _, target, text string) (bool, error) {
		state, _, err := client.PaneOption(ctx, target, protocol.OptionState)
		if err != nil {
			return false, fmt.Errorf("read %s of pane %s: %w", protocol.OptionState, target, err)
		}
		if state != string(protocol.StateDone) {
			return false, nil
		}
		sent <- text
		return true, nil
	}
	host, stop := startEventWakeHost(t, st, getenv, path, eventWakeRig{
		passed: passed, wakePane: wakePane,
	})
	awaitCallerRound(passed)

	var wake atomic.Pointer[func()]
	publishCallerWake(&wake, host)
	updates := make(chan daemon.PaneUpdate)
	serveWakeDaemon(t, daemon.Config{
		Observers: []daemon.Observer{callerWakeOnDone(&wake)},
		Updates:   updates,
	})
	tmuxSocket := tmuxSocketOf(t, client)
	caller := syncWakePane(t, st, tmuxctl.Server{Socket: tmuxSocket}.Column(), pane)

	updates <- paneUpdate(pane, tmuxSocket, paneregistry.StatusBusy)
	stopStepUnder(t, st, host, caller)
	awaitCallerRound(passed)
	if markedIn(t, st, eventWakeStepEvent, eventWakePaneID) {
		t.Fatalf("step end marked while its pane was working")
	}

	updates <- paneUpdate(pane, tmuxSocket, paneregistry.StatusIdle)
	awaitMarked(t, st, passed, eventWakeStepEvent, eventWakePaneID)
	if text := <-sent; !strings.Contains(text, eventWakeStepEvent) {
		t.Fatalf("pane woken with %q, want the step end block", text)
	}

	if err := stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// TestEpicEventPushWiringQuestionAndPhase checks that a question and a phase change stored for an epic whose steerer is
// an idle headless session are each delivered to the steerer exactly once, with no wake from the test. The epic is
// paused, so the planner takes no action for it, and each store write wakes the round that delivers its event.
func TestEpicEventPushWiringQuestionAndPhase(t *testing.T) {
	st, getenv, path := openEventWakeStore(t)
	createAttentionEpic(t, st, "")
	pauseAttentionEpic(t, st)
	passed := make(chan struct{}, eventWakeSignals)
	host, stop := startEventWakeHost(t, st, getenv, path, eventWakeRig{passed: passed})
	awaitCallerRound(passed)

	creator := startSession(t, host, "hi")
	awaitTurnDone(t, path, creator)
	recordWakeCreator(t, st, creator)
	steerWakeEpic(t, path, creator)

	question := store.EpicQuestionEvent(attentionWiringEpic, openOwnerPark(t, st))
	awaitMarked(t, st, passed, question, creator)

	endAttentionEpic(t, st, "done")
	phase := store.EpicPhaseEvent(attentionWiringEpic, "done")
	awaitMarked(t, st, passed, phase, creator)

	prompts := promptIDsOf(t, path, creator)
	for _, event := range []string{question, phase} {
		if n := occurrences(prompts, event); n != 1 {
			t.Fatalf("event %s delivered %d times to %s, want exactly once", event, n, creator)
		}
	}

	if err := stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// TestEpicEventPushWiringQuietCreatorAfterSay checks that an epic event held by a creator the owner interrupted, and
// has not spoken to since, is sent once the owner's Say clears the quiet, with no wake from the test. The phase change
// is stored while the creator is quiet, so its round holds the event, and the Say's wake sends it.
func TestEpicEventPushWiringQuietCreatorAfterSay(t *testing.T) {
	st, getenv, path := openEventWakeStore(t)
	createAttentionEpic(t, st, "")
	pauseAttentionEpic(t, st)
	passed := make(chan struct{}, eventWakeSignals)
	host, stop := startEventWakeHost(t, st, getenv, path, eventWakeRig{passed: passed})
	awaitCallerRound(passed)

	creator := startSession(t, host, "HANG")
	awaitEngineBlock(t, path, creator, "fake hanging 1")
	if err := host.manager.Interrupt(t.Context(), creator); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	awaitTurnDone(t, path, creator)
	recordWakeCreator(t, st, creator)
	steerWakeEpic(t, path, creator)

	endAttentionEpic(t, st, "done")
	awaitCallerRound(passed)
	phase := store.EpicPhaseEvent(attentionWiringEpic, "done")
	if markedIn(t, st, phase, creator) {
		t.Fatalf("epic event marked while the owner's interrupt keeps its creator quiet")
	}

	ownerSay(t, host, creator)
	awaitMarked(t, st, passed, phase, creator)

	if err := stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}
