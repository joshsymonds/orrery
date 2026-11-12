package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// Notifier must satisfy the sink the manager is configured with.
var _ NoticeSink = (*Notifier)(nil)

// fakeSink records the notices, changes and seen reports a manager sends. Notify runs on the pump goroutines, so it
// locks.
type fakeSink struct {
	mu      sync.Mutex
	notices []Notice
	changes []string
	seens   []sinkSeen
}

// sinkSeen is one Seen call the fake sink received.
type sinkSeen struct {
	session string
	seen    time.Time
}

func (f *fakeSink) Notify(_ context.Context, notice Notice) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notices = append(f.notices, notice)
}

func (f *fakeSink) Changed(_ context.Context, session string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.changes = append(f.changes, session)
}

func (f *fakeSink) Seen(_ context.Context, session string, seen time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seens = append(f.seens, sinkSeen{session: session, seen: seen})
}

// changedCalls returns a copy of the sessions Changed was called for so far.
func (f *fakeSink) changedCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.changes...)
}

// got returns a copy of the notices received so far.
func (f *fakeSink) got() []Notice {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Notice(nil), f.notices...)
}

// seenCalls returns a copy of the Seen calls received so far.
func (f *fakeSink) seenCalls() []sinkSeen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sinkSeen(nil), f.seens...)
}

// fakeSender records the pushes and clears a notice record sends. Its calls run on background goroutines, so it locks.
type fakeSender struct {
	mu     sync.Mutex
	notes  []push.Notification
	clears []push.Clear
}

func (f *fakeSender) Publish(_ context.Context, note push.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notes = append(f.notes, note)
	return nil
}

func (f *fakeSender) PublishClear(_ context.Context, c push.Clear) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clears = append(f.clears, c)
	return nil
}

// publishes returns a copy of the pushes sent so far.
func (f *fakeSender) publishes() []push.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]push.Notification(nil), f.notes...)
}

// cleared returns a copy of the clears sent so far.
func (f *fakeSender) cleared() []push.Clear {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]push.Clear(nil), f.clears...)
}

// droppingApplier fails every write as though the op's session row were gone, so the writer drops the op and the
// Commit that carries it returns at once.
type droppingApplier struct{}

func (droppingApplier) ApplyEngine(_ context.Context, _ []store.EngineOp, _ time.Time) error {
	return &store.EngineOpError{Index: 0, Err: store.ErrNoEngineSession}
}

// newNoticeManager returns a manager with a cap of one session, like newTestManager, that runs the claude at claude and
// sends its notices to sink through Config.Notifier.
func newNoticeManager(t *testing.T, claude string, sink NoticeSink) (*Manager, string) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	s, path := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	m := NewManager(Config{
		Shell:    sh,
		Claude:   claude,
		Environ:  []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
		Cap:      1,
		Writer:   writer,
		Now:      fixedClock,
		Notifier: sink,
		Store:    s,
	})
	t.Cleanup(m.Close)
	return m, path
}

// waitNotices waits until sink has received a notice, and returns the notices it has received.
func waitNotices(t *testing.T, sink *fakeSink) []Notice {
	t.Helper()
	waitUntil(t, "a notice", func() bool { return len(sink.got()) > 0 })
	return sink.got()
}

func TestNoticeWaitingCarriesQuestion(t *testing.T) {
	sink := &fakeSink{}
	m, _ := newNoticeManager(t, fakeClaudePath(t), sink)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	got := waitNotices(t, sink)
	want := Notice{Session: id, Dir: dir, State: string(protocol.StateWaiting), Body: "Which color?"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("notices = %+v, want [%+v]", got, want)
	}
}

func TestNoticeDoneCarriesReply(t *testing.T) {
	sink := &fakeSink{}
	m, _ := newNoticeManager(t, fakeClaudePath(t), sink)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	got := waitNotices(t, sink)
	want := Notice{Session: id, Dir: dir, State: string(protocol.StateDone), Body: "fake reply 1"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("notices = %+v, want [%+v]", got, want)
	}
}

func TestNoticeDoneCarriesLastFinishedBlock(t *testing.T) {
	sink := &fakeSink{}
	m, _ := newNoticeManager(t, fakeClaudePath(t), sink)
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "FLOOD"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	got := waitNotices(t, sink)
	if len(got) != 1 || got[0].Session != id || got[0].Body != "fake flood 1.50" {
		t.Fatalf("notices = %+v, want one done notice carrying the last block fake flood 1.50", got)
	}
}

func TestNoticeInterruptedTurnSendsNothingAndNextTurnNotifies(t *testing.T) {
	sink := &fakeSink{}
	m, path := newNoticeManager(t, fakeClaudePath(t), sink)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "HANG"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the hanging reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake hanging 1")
	})
	if err := m.Interrupt(t.Context(), id); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	waitHistory(t, path, id, "the interrupted turn's end", func(h store.EngineHistory) bool {
		return turnEndsWithError(t, h)
	})

	if err := m.Say(t.Context(), SayRequest{ID: id, Text: "again"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	got := waitNotices(t, sink)
	want := Notice{Session: id, Dir: dir, State: string(protocol.StateDone), Body: "fake reply 2"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("notices = %+v, want only [%+v]: the interrupted turn must send nothing", got, want)
	}
}

func TestNoticeFailedCarriesRecordedReason(t *testing.T) {
	sink := &fakeSink{}
	missing := filepath.Join(t.TempDir(), "no-claude")
	m, path := newNoticeManager(t, missing, sink)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
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

	got := waitNotices(t, sink)
	want := Notice{Session: id, Dir: dir, State: "failed", Body: reason}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("notices = %+v, want [%+v]", got, want)
	}
}

func TestNoticeInterruptedOnCloseCarriesReason(t *testing.T) {
	sink := &fakeSink{}
	m, path := newNoticeManager(t, fakeClaudePath(t), sink)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "STALL"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, "the stalled block's unfinished text", func() bool { return countRows(t, path, "engine_text") == 1 })

	m.Close()
	want := Notice{Session: id, Dir: dir, State: stateInterrupted, Body: "orreryd stopped during the turn"}
	if got := sink.got(); len(got) != 1 || got[0] != want {
		t.Fatalf("notices after Close = %+v, want [%+v]", got, want)
	}
}

func TestNoticeStopSendsNothing(t *testing.T) {
	sink := &fakeSink{}
	m, _ := newNoticeManager(t, fakeClaudePath(t), sink)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitNotices(t, sink)

	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	want := Notice{Session: id, Dir: dir, State: string(protocol.StateDone), Body: "fake reply 1"}
	if got := sink.got(); len(got) != 1 || got[0] != want {
		t.Fatalf("notices after Stop = %+v, want only [%+v]", got, want)
	}
}

func TestEventNoticeMapsEvents(t *testing.T) {
	questionInput := json.RawMessage(`{"questions":[{"question":"Which color?"}]}`)
	cases := []struct {
		name        string
		event       session.Event
		reply       string
		interrupted bool
		state, body string
		ok          bool
	}{
		{
			name:  "question request notifies waiting with the question",
			event: session.Request{Kind: session.RequestQuestion, Input: questionInput},
			state: string(protocol.StateWaiting), body: "Which color?", ok: true,
		},
		{
			name:  "permission request notifies waiting with the tool name",
			event: session.Request{Kind: session.RequestPermission, ToolName: "Write"},
			state: string(protocol.StateWaiting), body: "Write", ok: true,
		},
		{
			name:  "refused request notifies nothing",
			event: session.Request{Kind: session.RequestQuestion, Input: questionInput, Refused: true},
		},
		{
			name:  "turn end notifies done with the reply",
			event: session.TurnEnded{}, reply: "fake reply 1",
			state: string(protocol.StateDone), body: "fake reply 1", ok: true,
		},
		{
			name:  "turn end with no main text notifies done with an empty body",
			event: session.TurnEnded{},
			state: string(protocol.StateDone), ok: true,
		},
		{
			name:  "interrupted turn end notifies nothing",
			event: session.TurnEnded{IsError: true}, reply: "fake hanging 1", interrupted: true,
		},
		{
			name:  "clean exit notifies nothing",
			event: session.Exited{Stderr: "warning"},
		},
		{
			name:  "failed exit notifies failed with the recorded reason",
			event: session.Exited{ExitErr: errors.New("exit status 1"), Stderr: "boom\n"},
			state: stateFailed, body: "exit status 1: boom", ok: true,
		},
		{
			name:  "streamed text notifies nothing",
			event: session.TextDelta{Text: "fake"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, body, ok := eventNotice(tc.event, tc.reply, tc.interrupted)
			if state != tc.state || body != tc.body || ok != tc.ok {
				t.Fatalf("eventNotice = (%q, %q, %v), want (%q, %q, %v)", state, body, ok, tc.state, tc.body, tc.ok)
			}
		})
	}
}

func TestMainTextIsOnlyMainConversationAssistantText(t *testing.T) {
	cases := []struct {
		name  string
		event session.Event
		text  string
		ok    bool
	}{
		{
			name:  "finished main assistant text",
			event: session.Block{Kind: session.BlockText, Role: "assistant", Text: "fake reply 1"},
			text:  "fake reply 1", ok: true,
		},
		{
			name:  "subagent text is not main conversation",
			event: session.Block{Kind: session.BlockText, Role: "assistant", ParentToolUseID: "toolu_1", Text: "sub"},
		},
		{
			name:  "user text is not assistant text",
			event: session.Block{Kind: session.BlockText, Role: "user", Text: "hi"},
		},
		{
			name:  "thinking is not text",
			event: session.Block{Kind: session.BlockThinking, Role: "assistant", Text: "hmm"},
		},
		{
			name:  "a tool call is not text",
			event: session.Block{Kind: session.BlockToolCall, Role: "assistant", Name: "Write"},
		},
		{
			name:  "a streamed delta is not a finished block",
			event: session.TextDelta{Text: "fake"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, ok := mainText(tc.event)
			if text != tc.text || ok != tc.ok {
				t.Fatalf("mainText = (%q, %v), want (%q, %v)", text, ok, tc.text, tc.ok)
			}
		})
	}
}

func TestSeenReportsKnownSessionToSink(t *testing.T) {
	sink := &fakeSink{}
	m, _ := newNoticeManager(t, fakeClaudePath(t), sink)
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Seen(t.Context(), id); err != nil {
		t.Fatalf("Seen: %v", err)
	}

	got := sink.seenCalls()
	if len(got) != 1 || got[0].session != id || !got[0].seen.Equal(fixedClock()) {
		t.Fatalf("Seen calls = %+v, want exactly one for %s at %v", got, id, fixedClock())
	}
}

func TestSeenUnknownSessionReportsNothing(t *testing.T) {
	sink := &fakeSink{}
	m, _ := newNoticeManager(t, fakeClaudePath(t), sink)
	err := m.Seen(t.Context(), "0123456789abcdef0123456789abcdef")
	if !errors.Is(err, ErrNoSession) {
		t.Fatalf("Seen of an unknown id error = %v, want ErrNoSession", err)
	}
	if got := sink.seenCalls(); len(got) != 0 {
		t.Fatalf("Seen calls for an unknown id = %+v, want none", got)
	}
}

func TestSeenFailedWriteReportsNothing(t *testing.T) {
	sink := &fakeSink{}
	s, _ := openStore(t)
	id := "0123456789abcdef0123456789abcdef"
	addSessions(t, s, id)
	writer := NewWriter(WriterConfig{Applier: droppingApplier{}, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	m := NewManager(Config{Writer: writer, Now: fixedClock, Notifier: sink, Store: s})

	if err := m.Seen(t.Context(), id); err == nil {
		t.Fatal("Seen with a failed write returned nil, want an error")
	}
	if got := sink.seenCalls(); len(got) != 0 {
		t.Fatalf("Seen calls after a failed write = %+v, want none", got)
	}
}

// absentDesk is a desk the owner is not at. It takes posts and reports no input.
type absentDesk struct{}

func (absentDesk) Post(deskstream.Notice) {}

func (absentDesk) Desk() (int64, bool) { return 0, false }

// fakeRouteLog records the lines the Router writes. Write runs on the Router's goroutines, so it locks.
type fakeRouteLog struct {
	mu    sync.Mutex
	lines []route.Line
}

func (f *fakeRouteLog) Write(line route.Line) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lines = append(f.lines, line)
	return nil
}

// got returns a copy of the lines written so far.
func (f *fakeRouteLog) got() []route.Line {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]route.Line(nil), f.lines...)
}

const sessionTestHost = "orrery-host"

// newSessionRouter returns a real Router for a host whose desk the owner is not at, with a subscribed phone at phone
// and lines written to log. Its Close runs when the test ends.
func newSessionRouter(t *testing.T, phone route.PhoneSink, log route.Recorder) *route.Router {
	t.Helper()
	router := route.New(route.Config{
		Host:       sessionTestHost,
		Now:        fixedClock,
		Desk:       absentDesk{},
		Phone:      phone,
		Subscribed: func() (bool, error) { return true, nil },
		Log:        log,
	})
	t.Cleanup(router.Close)
	return router
}

func TestSeenClearsWaitingSessionNotice(t *testing.T) {
	sender := &fakeSender{}
	notices := notice.New(sessionTestHost, sender, fixedClock, slog.New(slog.DiscardHandler))
	router := newSessionRouter(t, notices, &fakeRouteLog{})
	m, _ := newNoticeManager(t, fakeClaudePath(t), NewNotifier(NotifierConfig{Route: router}))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, "the waiting notice's push", func() bool { return len(sender.publishes()) == 1 })
	if got := sender.publishes()[0].Session; got != id {
		t.Fatalf("pushed session = %q, want %q", got, id)
	}

	if err := m.Seen(t.Context(), id); err != nil {
		t.Fatalf("Seen: %v", err)
	}
	notices.Wait()
	want := push.Clear{Host: sessionTestHost, Session: id, Seen: fixedClock().Unix()}
	if got := sender.cleared(); len(got) != 1 || got[0] != want {
		t.Fatalf("clears = %+v, want exactly [%+v]", got, want)
	}
}

func TestNoticeLogsSessionPingWithSessionKey(t *testing.T) {
	sender := &fakeSender{}
	notices := notice.New(sessionTestHost, sender, fixedClock, slog.New(slog.DiscardHandler))
	log := &fakeRouteLog{}
	router := newSessionRouter(t, notices, log)
	m, _ := newNoticeManager(t, fakeClaudePath(t), NewNotifier(NotifierConfig{Route: router}))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, "the waiting notice's line", func() bool { return len(log.got()) == 1 })

	want := route.Line{
		Time:    fixedClock().Unix(),
		Host:    sessionTestHost,
		Session: id,
		State:   string(protocol.StateWaiting),
		Route:   route.DeskPhone,
		Reason:  "away",
	}
	if got := log.got(); len(got) != 1 || got[0] != want {
		t.Fatalf("lines = %+v, want exactly [%+v]", got, want)
	}
}

func TestSayCallsSinkChangedForSession(t *testing.T) {
	sink := &fakeSink{}
	m, _ := newNoticeManager(t, fakeClaudePath(t), sink)
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitNotices(t, sink)
	if got := sink.changedCalls(); len(got) != 0 {
		t.Fatalf("Changed calls before the prompt = %v, want none", got)
	}

	if err := m.Say(t.Context(), SayRequest{ID: id, Text: "again"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	if got := sink.changedCalls(); len(got) != 1 || got[0] != id {
		t.Fatalf("Changed calls after the prompt = %v, want exactly [%s]", got, id)
	}
}

func TestStopCallsSinkChangedForSession(t *testing.T) {
	sink := &fakeSink{}
	m, _ := newNoticeManager(t, fakeClaudePath(t), sink)
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitNotices(t, sink)

	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := sink.changedCalls(); len(got) != 1 || got[0] != id {
		t.Fatalf("Changed calls after Stop = %v, want exactly [%s]", got, id)
	}
}

func TestEndsNoticeMapsEvents(t *testing.T) {
	cases := []struct {
		name  string
		event session.Event
		want  bool
	}{
		{name: "canceled request records working", event: session.RequestCancelled{}, want: true},
		{name: "clean exit records stopped", event: session.Exited{}, want: true},
		{
			name:  "failed exit notifies failed instead",
			event: session.Exited{ExitErr: errors.New("exit status 1")},
		},
		{name: "turn end records done", event: session.TurnEnded{}},
		{name: "request notifies waiting instead", event: session.Request{Kind: session.RequestPermission}},
		{name: "streamed text changes nothing", event: session.TextDelta{Text: "fake"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := endsNotice(tc.event); got != tc.want {
				t.Fatalf("endsNotice = %v, want %v", got, tc.want)
			}
		})
	}
}

// presentDesk is a desk the owner is at. It takes posts and reports its newest input as input.
type presentDesk struct {
	input int64
}

func (presentDesk) Post(deskstream.Notice) {}

func (d presentDesk) Desk() (int64, bool) { return d.input, true }

// fakeAfter holds the timers a Router arms, so a test fires them itself. The Router arms them under its own lock on
// other goroutines, so it locks.
type fakeAfter struct {
	mu  sync.Mutex
	fns []func()
}

func (a *fakeAfter) After(_ time.Duration, f func()) func() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.fns = append(a.fns, f)
	return func() bool { return true }
}

// armed returns how many timers the Router has armed.
func (a *fakeAfter) armed() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.fns)
}

// fire runs the i-th timer the Router armed.
func (a *fakeAfter) fire(i int) {
	a.mu.Lock()
	f := a.fns[i]
	a.mu.Unlock()
	f()
}

func TestAnsweredWaitingNoticeAtDeskEndsChanged(t *testing.T) {
	sender := &fakeSender{}
	notices := notice.New(sessionTestHost, sender, fixedClock, slog.New(slog.DiscardHandler))
	log := &fakeRouteLog{}
	after := &fakeAfter{}
	router := route.New(route.Config{
		Host:       sessionTestHost,
		Now:        fixedClock,
		After:      after.After,
		Desk:       presentDesk{input: fixedClock().Unix()},
		Phone:      notices,
		Subscribed: func() (bool, error) { return true, nil },
		Log:        log,
	})
	t.Cleanup(router.Close)
	m, path := newNoticeManager(t, fakeClaudePath(t), NewNotifier(NotifierConfig{Route: router}))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, "the waiting notice at the desk", func() bool {
		return len(log.got()) == 1 && after.armed() == 1
	})
	waiting := waitHistory(t, path, id, "the waiting state", func(h store.EngineHistory) bool {
		return h.State == "waiting"
	})

	if err := m.Answer(t.Context(), id, requestIDOf(t, waiting), "red"); err != nil {
		t.Fatalf("Answer red: %v", err)
	}
	atDesk := route.Line{
		Time: fixedClock().Unix(), Host: sessionTestHost, Session: id,
		State: string(protocol.StateWaiting), Route: route.Desk, Reason: "at the desk",
	}
	ended := atDesk
	ended.Route, ended.Reason = route.None, "changed"
	if got := log.got(); len(got) < 2 || got[0] != atDesk || got[1] != ended {
		t.Fatalf("lines after Answer = %+v, want [%+v] then [%+v]", got, atDesk, ended)
	}

	after.fire(0)
	notices.Wait()
	if got := sender.publishes(); len(got) != 0 {
		t.Fatalf("pushes after the escalation of an answered notice = %+v, want none", got)
	}
}
