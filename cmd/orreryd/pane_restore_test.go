package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/donejudge"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// Synthetic values only. The pane texts are stored on a private tmux server, and the judge is a fake caller.
const (
	paneProject     = "synthetic-project"
	paneHost        = "synthetic-host"
	paneSession     = "s1"
	paneSummary     = "synthetic summary"
	paneLabel       = "synthetic label"
	paneQuestion    = "synthetic question"
	paneMessage     = "synthetic final message"
	paneWhy         = "synthetic why"
	paneWaitingHook = `{"message":"synthetic waiting hook"}`
	paneDoneHook    = `{"last_assistant_message":"synthetic final message"}`
	paneJudgeReply  = `{"needed":true,"why":"synthetic why"}`
	// paneClock is the fake clock's start, in unix seconds. Every notice in these tests is recorded at it.
	paneClock = int64(1_800_000_000)
	// Each restart happens this many seconds after paneClock.
	decidingRestartSecs = 1
	heldRestartSecs     = 2
	delayedRestartSecs  = 4
	deskRestartSecs     = 60
	// The row ID every test notice gets: each test stores one notice in a fresh store.
	paneRowID = 1
	// The log reasons and row phases the tests expect, spelled as the router writes them.
	reasonAtDesk    = "at the desk"
	reasonWatching  = "watching"
	reasonAway      = "away"
	reasonEscalated = "unseen after 5 minutes"
	phaseDeciding   = "deciding"
	phaseHeld       = "held"
	phaseDelayed    = "delayed"
	phaseDesk       = "desk"
)

// paneSinks is the desk, phone and log of one orreryd process, and the clock its router reads. Its After timers are
// the waits its router started, and fire runs one of them.
type paneSinks struct {
	mu      sync.Mutex
	now     int64
	present bool
	timers  []*paneTimer
	posts   []deskstream.Notice
	pushes  []push.Notification
	lines   []route.Line
	errs    []error
	sents   []func()
}

// paneTimer is one wait. It is active until it is stopped or fired.
type paneTimer struct {
	d    time.Duration
	f    func()
	done bool
}

// newPaneSinks returns sinks whose clock reads now, in unix seconds, with a desk that is present or not.
func newPaneSinks(now int64, present bool) *paneSinks {
	return &paneSinks{now: now, present: present}
}

// Now reads the fake clock.
func (s *paneSinks) Now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Unix(s.now, 0)
}

// After starts a timer that runs f after d, and returns the function that stops it.
func (s *paneSinks) After(d time.Duration, f func()) func() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	timer := &paneTimer{d: d, f: f}
	s.timers = append(s.timers, timer)
	return func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		if timer.done {
			return false
		}
		timer.done = true
		return true
	}
}

// Desk reports the clock's time as the desk's input, so a present desk is at hand.
func (s *paneSinks) Desk() (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now, s.present
}

// Post records a desk notice.
func (s *paneSinks) Post(n deskstream.Notice) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.posts = append(s.posts, n)
}

// Notify records the push and its sent callback. The Router calls Notify holding its lock and sent takes that lock,
// so sent runs later, in publish, as the phone runs it when the push's publish returns.
func (s *paneSinks) Notify(
	ctx context.Context, _ notice.Key, build func(context.Context) (push.Notification, error), sent func(),
) {
	note, err := build(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.errs = append(s.errs, err)
	}
	s.pushes = append(s.pushes, note)
	if sent != nil {
		s.sents = append(s.sents, sent)
	}
}

// publish runs the sent callbacks Notify recorded, in order, outside s.mu, as the phone does when each publish
// returns.
func (s *paneSinks) publish() {
	s.mu.Lock()
	sents := s.sents
	s.sents = nil
	s.mu.Unlock()
	for _, sent := range sents {
		sent()
	}
}

// Seen does nothing: no test marks a notice seen through the phone.
func (s *paneSinks) Seen(context.Context, notice.Key, int64) {}

// Subscribed reports that a push subscription exists.
func (s *paneSinks) Subscribed() (bool, error) {
	return true, nil
}

// Write records one log line.
func (s *paneSinks) Write(line route.Line) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, line)
	return nil
}

// fire runs the one active timer waiting d, as if that wait ended: the clock advances by d and the timer's function
// runs.
func (s *paneSinks) fire(t *testing.T, d time.Duration) {
	t.Helper()
	s.mu.Lock()
	due := s.waiting(d)
	if len(due) != 1 {
		s.mu.Unlock()
		t.Fatalf("active timers waiting %v = %d, want 1", d, len(due))
	}
	due[0].done = true
	s.now += int64(d / time.Second)
	f := due[0].f
	s.mu.Unlock()
	f()
}

// activeTimers counts the active timers waiting d.
func (s *paneSinks) activeTimers(d time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.waiting(d))
}

// waiting returns the active timers waiting d. The caller holds s.mu.
func (s *paneSinks) waiting(d time.Duration) []*paneTimer {
	var due []*paneTimer
	for _, timer := range s.timers {
		if !timer.done && timer.d == d {
			due = append(due, timer)
		}
	}
	return due
}

// check asserts the lines, desk posts and phone pushes the sinks recorded, in order, and that every push built.
func (s *paneSinks) check(t *testing.T, lines []route.Line, posts []deskstream.Notice, pushes []push.Notification) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.errs) > 0 {
		t.Errorf("phone push build errors = %v", s.errs)
	}
	if !slices.Equal(s.lines, lines) {
		t.Errorf("lines = %+v, want %+v", s.lines, lines)
	}
	if !slices.Equal(s.posts, posts) {
		t.Errorf("desk posts = %+v, want %+v", s.posts, posts)
	}
	if !slices.Equal(s.pushes, pushes) {
		t.Errorf("phone pushes = %+v, want %+v", s.pushes, pushes)
	}
}

// paneJudgeCaller is a donejudge.Caller that records each input and answers that the done needs the user. The judge
// runs on the test's goroutine, through fire, so the caller needs no lock.
type paneJudgeCaller struct {
	inputs []string
}

// CallVia records input and returns paneJudgeReply.
func (c *paneJudgeCaller) CallVia(
	_ context.Context, _, _ string, _ map[string]string, _, input string,
) (json.RawMessage, error) {
	c.inputs = append(c.inputs, input)
	return json.RawMessage(paneJudgeReply), nil
}

// paneCwd makes the synthetic project folder under home and returns its path.
func paneCwd(t *testing.T, home string) string {
	t.Helper()
	cwd := filepath.Join(home, paneProject)
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatalf("make project folder: %v", err)
	}
	return cwd
}

// startPane starts a private tmux server whose session holds one pane, running in cwd. It returns the server, a
// client for it and the pane's id.
func startPane(t *testing.T, cwd string) (tmuxctl.Server, *tmuxctl.Client, string) {
	t.Helper()
	// Without a UTF-8 locale tmux prints the newline in paneLocation's format as "_", so the desk notice would fail to
	// build. Run the server under one, as newWiringFixture does.
	t.Setenv("LC_ALL", "C.UTF-8")
	server, client := startServer(t, "pane")
	if _, err := client.Run(t.Context(), "-f", noConfig, "new-session", "-d", "-s", paneSession,
		"-c", cwd, testCommand); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	out, err := client.Run(t.Context(), "list-panes", "-t", paneSession, "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	return server, client, strings.TrimSpace(out)
}

// setPaneText stores the summary, label and question that the pane's notifications read.
func setPaneText(t *testing.T, client *tmuxctl.Client, pane string) {
	t.Helper()
	texts := map[string]string{
		protocol.OptionSummary:  paneSummary,
		protocol.OptionLabel:    paneLabel,
		protocol.OptionQuestion: paneQuestion,
	}
	for name, value := range texts {
		if err := client.SetPaneOption(t.Context(), pane, name, value); err != nil {
			t.Fatalf("set %s: %v", name, err)
		}
	}
}

// setPaneState stores the state orreryd writes on pane when its transition moves it to state.
func setPaneState(t *testing.T, client *tmuxctl.Client, pane string, state protocol.State) {
	t.Helper()
	if err := client.SetPaneOption(t.Context(), pane, protocol.OptionState, string(state)); err != nil {
		t.Fatalf("set %s: %v", protocol.OptionState, err)
	}
}

// paneTransition is a transition of pane on server to state, whose frame ran in cwd and carried hook.
func paneTransition(server tmuxctl.Server, pane string, state protocol.State, cwd, hook string) daemon.Transition {
	return daemon.Transition{
		PaneID: pane,
		Server: server,
		From:   protocol.StateWorking,
		To:     state,
		Frame:  protocol.HookFrame{Hook: json.RawMessage(hook), TmuxPane: pane, Cwd: cwd},
	}
}

// sourceOf is the source a pane notice stores for transition. It checks that the source decodes back to transition,
// hook included.
func sourceOf(t *testing.T, transition daemon.Transition) []byte {
	t.Helper()
	data, err := encodePaneSource(transition)
	if err != nil {
		t.Fatalf("encode pane source: %v", err)
	}
	decoded, err := decodePaneSource(data)
	if err != nil {
		t.Fatalf("decode pane source: %v", err)
	}
	if !reflect.DeepEqual(decoded, transition) {
		t.Fatalf("decoded source = %+v, want %+v", decoded, transition)
	}
	return data
}

// paneRow is the row a pane notice in pane and state keeps, recorded at paneClock, in phase, due at due (unix ms).
func paneRow(pane string, state protocol.State, phase string, due int64, source []byte) store.NoticeRow {
	return store.NoticeRow{
		ID:         paneRowID,
		Pane:       pane,
		State:      string(state),
		Time:       paneClock,
		Phase:      phase,
		Due:        due,
		SourceKind: paneSourceKind,
		Source:     source,
	}
}

// logged is row after its notice's watching line was written.
func logged(row store.NoticeRow) store.NoticeRow {
	row.WatchLogged = true
	return row
}

// delayServed is row after its notice's judge delay was served. A notice that was never watched has no watching line.
func delayServed(row store.NoticeRow) store.NoticeRow {
	row.DelayServed = true
	return row
}

// dueAt is the due time, in unix ms, of a wait of d that starts at from, in unix seconds.
func dueAt(from int64, d time.Duration) int64 {
	return time.Unix(from, 0).Add(d).UnixMilli()
}

// expectedLine is the log line a notice in pane and state writes at at.
func expectedLine(at int64, pane string, state protocol.State, routed, reason string) route.Line {
	return route.Line{
		Time:  at,
		Host:  paneHost,
		Pane:  pane,
		State: string(state),
		Route: routed,
		// Reason is the words a router writes for the route.
		Reason: reason,
	}
}

// expectedPost is the desk notice that a pane in state posts with body.
func expectedPost(state protocol.State, body string) deskstream.Notice {
	return deskstream.Notice{
		State: string(state),
		Title: deskstream.PaneTitle(paneProject, paneSession, 0),
		Body:  body,
	}
}

// expectedPush is the phone push for a pane in state: the texts stored on the pane, under the synthetic project.
func expectedPush(state protocol.State) push.Notification {
	return push.Notification{
		Project:  paneProject,
		State:    state,
		Summary:  paneSummary,
		Label:    paneLabel,
		Question: paneQuestion,
	}
}

// openPaneStore opens the store at path and closes it when the test ends.
func openPaneStore(t *testing.T, path string) *store.Store {
	t.Helper()
	storetest.Seed(t, path)
	s, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return s
}

// reopenPaneStore closes s and opens the store at path again, as a restart does.
func reopenPaneStore(t *testing.T, s *store.Store, path string) *store.Store {
	t.Helper()
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	return openPaneStore(t, path)
}

// startProcess starts one orreryd process's relay over sinks and s, with home as its judge's home and caller as its
// judge's caller. Its router is attached to s, and the caller decides whether it is closed.
func startProcess(t *testing.T, sinks *paneSinks, s *store.Store, home string, caller *paneJudgeCaller) *relay {
	t.Helper()
	hub := newRelay(route.Config{
		Host:       paneHost,
		Now:        sinks.Now,
		After:      sinks.After,
		Desk:       sinks,
		Phone:      sinks,
		Subscribed: sinks.Subscribed,
		Log:        sinks,
		Logger:     slog.New(slog.DiscardHandler),
	}, &relay{
		home:   home,
		logger: slog.New(slog.DiscardHandler),
		judge:  donejudge.New(caller, nil),
		reads:  newReadTimes(),
	})
	hub.router.Attach(t.Context(), s)
	return hub
}

// checkRows asserts the rows s holds, in id order.
func checkRows(t *testing.T, s *store.Store, want ...store.NoticeRow) {
	t.Helper()
	got, err := s.Notices(t.Context())
	if err != nil {
		t.Fatalf("list notices: %v", err)
	}
	if !slices.EqualFunc(got, want, func(a, b store.NoticeRow) bool { return reflect.DeepEqual(a, b) }) {
		t.Errorf("rows = %+v, want %+v", got, want)
	}
}

// resumeDeciding restarts orreryd while a waiting notice is deciding. The row a crash mid-decision leaves is the row
// Notify writes, so the test writes that row directly. The restart then decides the notice anew.
func resumeDeciding(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	cwd := paneCwd(t, home)
	server, client, pane := startPane(t, cwd)
	setPaneText(t, client, pane)
	setPaneState(t, client, pane, protocol.StateWaiting)
	transition := paneTransition(server, pane, protocol.StateWaiting, cwd, paneWaitingHook)
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openPaneStore(t, path)
	if err := s.PutNotice(ctx, paneRow(pane, protocol.StateWaiting, phaseDeciding,
		dueAt(paneClock, 0), sourceOf(t, transition))); err != nil {
		t.Fatalf("put deciding row: %v", err)
	}
	s = reopenPaneStore(t, s, path)

	restartAt := paneClock + decidingRestartSecs
	sinks := newPaneSinks(restartAt, false)
	hub := startProcess(t, sinks, s, home, &paneJudgeCaller{})
	t.Cleanup(hub.router.Close)
	hub.router.Wait()

	sinks.check(t,
		[]route.Line{expectedLine(restartAt, pane, protocol.StateWaiting, route.DeskPhone, reasonAway)},
		[]deskstream.Notice{expectedPost(protocol.StateWaiting, paneQuestion)},
		[]push.Notification{expectedPush(protocol.StateWaiting)})
	sinks.publish()
	checkRows(t, s)
}

// resumeHeld restarts orreryd while a waiting notice is held because a client watches its pane. The client keeps its
// input across the restart, so the restarted router checks the watch at once and writes no second watching line.
func resumeHeld(t *testing.T) {
	ctx := t.Context()
	fx := newWiringFixture(t)
	fx.typeIntoClient(t)
	setPaneText(t, fx.client, fx.pane)
	setPaneState(t, fx.client, fx.pane, protocol.StateWaiting)
	home := t.TempDir()
	cwd := paneCwd(t, home)
	transition := paneTransition(fx.inner, fx.pane, protocol.StateWaiting, cwd, paneWaitingHook)
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openPaneStore(t, path)
	caller := &paneJudgeCaller{}

	first := newPaneSinks(paneClock, false)
	hub := startProcess(t, first, s, home, caller)
	hub.notify(ctx, transition)
	hub.router.Wait()
	first.check(t, []route.Line{expectedLine(paneClock, fx.pane, protocol.StateWaiting, route.None, reasonWatching)},
		nil, nil)
	checkRows(t, s, logged(paneRow(fx.pane, protocol.StateWaiting, phaseHeld,
		dueAt(paneClock, route.WatchRecheck), sourceOf(t, transition))))

	s = reopenPaneStore(t, s, path)
	restartAt := paneClock + heldRestartSecs
	second := newPaneSinks(restartAt, false)
	hub2 := startProcess(t, second, s, home, caller)
	t.Cleanup(hub2.router.Close)
	hub2.router.Wait()

	second.check(t, nil, nil, nil)
	checkRows(t, s, logged(paneRow(fx.pane, protocol.StateWaiting, phaseHeld,
		dueAt(restartAt, route.WatchRecheck), sourceOf(t, transition))))
	if n := second.activeTimers(route.WatchRecheck); n != 1 {
		t.Errorf("watch rechecks armed = %d, want 1", n)
	}
}

// resumeDelayed restarts orreryd while a done notice waits out its judge delay. The restart arms only the rest of that
// wait, and the judge then reads the last assistant message the stored hook carries.
func resumeDelayed(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	cwd := paneCwd(t, home)
	server, client, pane := startPane(t, cwd)
	setPaneText(t, client, pane)
	setPaneState(t, client, pane, protocol.StateDone)
	transition := paneTransition(server, pane, protocol.StateDone, cwd, paneDoneHook)
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openPaneStore(t, path)
	caller := &paneJudgeCaller{}

	first := newPaneSinks(paneClock, false)
	hub := startProcess(t, first, s, home, caller)
	hub.notify(ctx, transition)
	hub.router.Wait()
	first.check(t, nil, nil, nil)
	checkRows(t, s, delayServed(paneRow(pane, protocol.StateDone, phaseDelayed,
		dueAt(paneClock, route.JudgeDelay), sourceOf(t, transition))))

	s = reopenPaneStore(t, s, path)
	restartAt := paneClock + delayedRestartSecs
	second := newPaneSinks(restartAt, false)
	hub2 := startProcess(t, second, s, home, caller)
	t.Cleanup(hub2.router.Close)
	hub2.router.Wait()

	if len(caller.inputs) != 0 {
		t.Errorf("judge calls before the due = %d, want none", len(caller.inputs))
	}
	if n := second.activeTimers(route.JudgeDelay); n != 0 {
		t.Errorf("judge delays armed after the restart = %d, want none", n)
	}
	second.fire(t, route.JudgeDelay-time.Duration(delayedRestartSecs)*time.Second)
	hub2.router.Wait()

	if len(caller.inputs) != 1 || !strings.HasSuffix(caller.inputs[0], paneMessage) {
		t.Errorf("judge inputs = %q, want one ending in the stored message %q", caller.inputs, paneMessage)
	}
	dueSecs := paneClock + int64(route.JudgeDelay/time.Second)
	second.check(t,
		[]route.Line{expectedLine(dueSecs, pane, protocol.StateDone, route.DeskPhone, reasonAway+"; needed: "+paneWhy)},
		[]deskstream.Notice{expectedPost(protocol.StateDone, paneSummary)},
		[]push.Notification{expectedPush(protocol.StateDone)})
	second.publish()
	checkRows(t, s)
}

// resumeDesk restarts orreryd while a waiting notice is at the desk. The restart neither posts the desk notice again
// nor escalates it early. At its due the escalation pushes the phone once, with the push an unrestarted escalation
// makes: the same texts from the same pane.
func resumeDesk(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	cwd := paneCwd(t, home)
	server, client, pane := startPane(t, cwd)
	setPaneText(t, client, pane)
	setPaneState(t, client, pane, protocol.StateWaiting)
	transition := paneTransition(server, pane, protocol.StateWaiting, cwd, paneWaitingHook)
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openPaneStore(t, path)

	first := newPaneSinks(paneClock, true)
	hub := startProcess(t, first, s, home, &paneJudgeCaller{})
	hub.notify(ctx, transition)
	hub.router.Wait()
	first.check(t, []route.Line{expectedLine(paneClock, pane, protocol.StateWaiting, route.Desk, reasonAtDesk)},
		[]deskstream.Notice{expectedPost(protocol.StateWaiting, paneQuestion)}, nil)
	checkRows(t, s, paneRow(pane, protocol.StateWaiting, phaseDesk,
		dueAt(paneClock, route.Escalation), sourceOf(t, transition)))

	s = reopenPaneStore(t, s, path)
	restartAt := paneClock + deskRestartSecs
	second := newPaneSinks(restartAt, true)
	hub2 := startProcess(t, second, s, home, &paneJudgeCaller{})
	t.Cleanup(hub2.router.Close)
	hub2.router.Wait()
	second.check(t, nil, nil, nil)

	second.fire(t, route.Escalation-time.Duration(deskRestartSecs)*time.Second)
	hub2.router.Wait()
	escalatedSecs := paneClock + int64(route.Escalation/time.Second)
	second.check(t,
		[]route.Line{expectedLine(escalatedSecs, pane, protocol.StateWaiting, route.Phone, reasonEscalated)},
		nil,
		[]push.Notification{expectedPush(protocol.StateWaiting)})
	second.publish()
	checkRows(t, s)
}

// TestPaneNoticeResumesEachPhase restarts orreryd while a pane notice is in each phase. The notice's row stores its
// transition with the hook, and the restarted router resumes the notice from that transition in its phase.
func TestPaneNoticeResumesEachPhase(t *testing.T) {
	t.Run("a deciding notice is decided anew", resumeDeciding)
	t.Run("a held notice checks the watch at once and writes no second watching line", resumeHeld)
	t.Run("a delayed done is judged at its due with the stored hook and is not delayed again", resumeDelayed)
	t.Run("a desk notice is not posted again and escalates once at its due", resumeDesk)
}
