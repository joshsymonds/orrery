package enginehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/deskstream"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// fakePanes is a PaneQuestions over the panes in panes, by id. A pane it does not hold is one tmux lists no pane with.
// DeclinePane returns declineErr. The manager calls PaneSeen on its own goroutine, so the calls are locked.
type fakePanes struct {
	panes      map[string]PaneQuestion
	declineErr error
	mu         sync.Mutex
	seens      []paneSeen
	declines   []paneDecline
}

// paneSeen is one PaneSeen call a fakePanes received.
type paneSeen struct {
	pane string
	seen time.Time
}

// paneDecline is one DeclinePane call a fakePanes received.
type paneDecline struct {
	pane, questionHash string
}

// DeclinePane records the call and returns declineErr.
func (f *fakePanes) DeclinePane(_ context.Context, pane, questionHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.declines = append(f.declines, paneDecline{pane: pane, questionHash: questionHash})
	return f.declineErr
}

// declineCalls returns a copy of the DeclinePane calls received so far.
func (f *fakePanes) declineCalls() []paneDecline {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.declines)
}

func (f *fakePanes) PaneQuestion(_ context.Context, pane string) (PaneQuestion, error) {
	question, ok := f.panes[pane]
	if !ok {
		return PaneQuestion{}, fmt.Errorf("pane %s: %w", pane, ErrNoPane)
	}
	return question, nil
}

func (f *fakePanes) PaneSeen(_ context.Context, pane string, seen time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seens = append(f.seens, paneSeen{pane: pane, seen: seen})
}

// seenCalls returns a copy of the PaneSeen calls received so far.
func (f *fakePanes) seenCalls() []paneSeen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.seens)
}

// phoneSeen is one Seen call a fakePhone received.
type phoneSeen struct {
	key  notice.Key
	seen int64
}

// fakePhone is the phone a Router sends to. It records the pushes and the seen reports it receives. The Router calls it
// on its goroutines, so it locks.
type fakePhone struct {
	mu       sync.Mutex
	notifies int
	seens    []phoneSeen
}

func (f *fakePhone) Notify(context.Context, notice.Key, func(context.Context) (push.Notification, error), func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notifies++
}

func (f *fakePhone) Seen(_ context.Context, key notice.Key, seen int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seens = append(f.seens, phoneSeen{key: key, seen: seen})
}

// notifyCount returns how many pushes the phone has received.
func (f *fakePhone) notifyCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.notifies
}

// seenCalls returns a copy of the seen reports the phone has received.
func (f *fakePhone) seenCalls() []phoneSeen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.seens)
}

// recordingDesk is the desk the owner is at. It takes posts, and reports its newest input as input. The Router posts on
// its goroutines, so it locks.
type recordingDesk struct {
	input int64
	mu    sync.Mutex
	posts []deskstream.Notice
}

func (d *recordingDesk) Post(n deskstream.Notice) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.posts = append(d.posts, n)
}

func (d *recordingDesk) Desk() int64 { return d.input }

// postCount returns how many notices the desk has been posted.
func (d *recordingDesk) postCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.posts)
}

// newPutOffManager returns a manager with a cap of two sessions, like newTestManager, that runs the claude at claude,
// stores its put-off marks in a store, reads the panes through panes and sends its notices to notifier. It returns the
// store too. notifier and panes may be nil.
func newPutOffManager(
	t *testing.T, claude string, notifier NoticeSink, panes PaneQuestions,
) (*Manager, *store.Store, string) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	s, path := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s})
	startRun(t, writer)
	m := NewManager(Config{
		Shell:    sh,
		Claude:   claude,
		Environ:  []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
		Cap:      2,
		Writer:   writer,
		Now:      fixedClock,
		Notifier: notifier,
		Store:    s,
		PutOffs:  s,
		Panes:    panes,
	})
	t.Cleanup(m.Close)
	return m, s, path
}

// readMarks returns the put-off marks in the store at path.
func readMarks(t *testing.T, path string) []store.PutOffMark {
	t.Helper()
	marks, err := store.NewReader(path).PutOffQuestions(t.Context())
	if err != nil {
		t.Fatalf("PutOffQuestions: %v", err)
	}
	return marks
}

// markKey names a put-off mark by its kind, session and question.
func markKey(kind, session, question string) string {
	return kind + " " + session + " " + question
}

// markKeys names each of marks with markKey.
func markKeys(marks []store.PutOffMark) []string {
	keys := make([]string, 0, len(marks))
	for _, mark := range marks {
		keys = append(keys, markKey(mark.Kind, mark.Session, mark.Question))
	}
	return keys
}

// addressedMark returns the kind, session and question of the mark req addresses.
func addressedMark(req PutOffRequest) (kind, session, question string) {
	if req.Kind == store.PutOffPane {
		return req.Kind, req.ID, store.PaneQuestionKey(req.QuestionHash, req.Changed)
	}
	return req.Kind, req.ID, req.RequestID
}

// wantRefused fails t unless err is refused as want and its message names text.
func wantRefused(t *testing.T, what string, err, want error, text string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s error = %v, want %v", what, err, want)
	}
	if !strings.Contains(err.Error(), text) {
		t.Fatalf("%s error = %q, want a message naming %q", what, err, text)
	}
}

// wantCode fails t unless err is a Connect error with code whose message names text.
func wantCode(t *testing.T, what string, err error, code connect.Code, text string) {
	t.Helper()
	if got := connect.CodeOf(err); got != code {
		t.Fatalf("%s code = %v, want %v (error %v)", what, got, code, err)
	}
	if !strings.Contains(err.Error(), text) {
		t.Fatalf("%s error = %q, want a message naming %q", what, err, text)
	}
}

func TestNotNowHeadlessStoresAndBringBackClears(t *testing.T) {
	t.Parallel()
	m, _, path := newPutOffManager(t, fakeClaudePath(t), nil, nil)
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the waiting state", func(h store.EngineHistory) bool {
		return h.State == "waiting"
	})
	requestID := requestIDOf(t, history)

	if err := m.NotNow(t.Context(), PutOffRequest{
		Kind: store.PutOffHeadless, ID: id, RequestID: requestID,
	}); err != nil {
		t.Fatalf("NotNow: %v", err)
	}
	marks := readMarks(t, path)
	if len(marks) != 1 || marks[0].Kind != store.PutOffHeadless || marks[0].Session != id ||
		marks[0].Question != requestID || !marks[0].At.Equal(fixedClock()) {
		t.Fatalf("marks after NotNow = %+v, want one headless mark of %s at %v", marks, requestID, fixedClock())
	}

	if err := m.BringBack(t.Context(), PutOffRequest{
		Kind: store.PutOffHeadless, ID: id, RequestID: requestID,
	}); err != nil {
		t.Fatalf("BringBack: %v", err)
	}
	if marks := readMarks(t, path); len(marks) != 0 {
		t.Fatalf("marks after BringBack = %+v, want none", marks)
	}
}

func TestNotNowPaneStoresAndBringBackClears(t *testing.T) {
	t.Parallel()
	s, path := openStore(t)
	panes := &fakePanes{panes: map[string]PaneQuestion{"%7": {Waiting: true, Hash: "h-ask", Changed: 1_700_000_100}}}
	m := NewManager(Config{Now: fixedClock, PutOffs: s, Panes: panes})
	req := PutOffRequest{Kind: store.PutOffPane, ID: "%7", QuestionHash: "h-ask", Changed: 1_700_000_100}

	if err := m.NotNow(t.Context(), req); err != nil {
		t.Fatalf("NotNow: %v", err)
	}
	key := store.PaneQuestionKey("h-ask", 1_700_000_100)
	marks := readMarks(t, path)
	if len(marks) != 1 || marks[0].Kind != store.PutOffPane || marks[0].Session != "%7" ||
		marks[0].Question != key || !marks[0].At.Equal(fixedClock()) {
		t.Fatalf("marks after NotNow = %+v, want one pane mark keyed %s at %v", marks, key, fixedClock())
	}

	if err := m.BringBack(t.Context(), req); err != nil {
		t.Fatalf("BringBack: %v", err)
	}
	if marks := readMarks(t, path); len(marks) != 0 {
		t.Fatalf("marks after BringBack = %+v, want none", marks)
	}
}

func TestNotNowRefusesUnlessTheQuestionIsOpen(t *testing.T) {
	t.Parallel()
	panes := &fakePanes{panes: map[string]PaneQuestion{
		"%1": {Waiting: true, Hash: "h-ask", Changed: 1_700_000_100},
		"%2": {Waiting: false, Hash: "h-idle", Changed: 1_700_000_050},
	}}
	m, s, path := newPutOffManager(t, fakeClaudePath(t), nil, panes)
	ask, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start ask: %v", err)
	}
	askHistory := waitHistory(t, engineOf(m), path, ask, "the waiting state", func(h store.EngineHistory) bool {
		return h.State == "waiting"
	})
	askRequest := requestIDOf(t, askHistory)
	quiet, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start quiet: %v", err)
	}
	waitHistory(t, engineOf(m), path, quiet, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})

	cases := []struct {
		name string
		req  PutOffRequest
		want error
		text string
		// malformed is set for a call that names no question, so there is no mark for it to seed.
		malformed bool
	}{
		{
			name: "stale request id",
			req:  PutOffRequest{Kind: store.PutOffHeadless, ID: ask, RequestID: "stale-request"},
			want: ErrQuestionNotOpen, text: "stale-request",
		},
		{
			name: "live session with no open request",
			req:  PutOffRequest{Kind: store.PutOffHeadless, ID: quiet, RequestID: "ask-1"},
			want: ErrQuestionNotOpen, text: quiet,
		},
		{
			name: "headless id not live",
			req:  PutOffRequest{Kind: store.PutOffHeadless, ID: unknownID, RequestID: askRequest},
			want: ErrNoSession, text: unknownID,
		},
		{
			name: "pane hash differs",
			req:  PutOffRequest{Kind: store.PutOffPane, ID: "%1", QuestionHash: "h-other", Changed: 1_700_000_100},
			want: ErrQuestionNotOpen, text: "h-ask@1700000100",
		},
		{
			name: "pane changed differs",
			req:  PutOffRequest{Kind: store.PutOffPane, ID: "%1", QuestionHash: "h-ask", Changed: 1_700_000_999},
			want: ErrQuestionNotOpen, text: "h-ask@1700000100",
		},
		{
			name: "pane not waiting",
			req:  PutOffRequest{Kind: store.PutOffPane, ID: "%2", QuestionHash: "h-idle", Changed: 1_700_000_050},
			want: ErrQuestionNotOpen, text: "not waiting",
		},
		{
			name: "pane tmux does not list",
			req:  PutOffRequest{Kind: store.PutOffPane, ID: "%9", QuestionHash: "h-ask", Changed: 1_700_000_100},
			want: ErrNoPane, text: "%9",
		},
		{
			name: "bad kind",
			req:  PutOffRequest{Kind: "tab", ID: "%1", QuestionHash: "h-ask", Changed: 1_700_000_100},
			want: ErrBadPutOff, text: `"tab"`, malformed: true,
		},
		{
			name: "empty id",
			req:  PutOffRequest{Kind: store.PutOffHeadless, RequestID: askRequest},
			want: ErrBadPutOff, text: "empty id", malformed: true,
		},
		{
			name: "empty request id",
			req:  PutOffRequest{Kind: store.PutOffHeadless, ID: ask},
			want: ErrBadPutOff, text: "request_id", malformed: true,
		},
		{
			name: "empty question hash",
			req:  PutOffRequest{Kind: store.PutOffPane, ID: "%1", Changed: 1_700_000_100},
			want: ErrBadPutOff, text: "question_hash", malformed: true,
		},
		{
			name: "pane changed not a time",
			req:  PutOffRequest{Kind: store.PutOffPane, ID: "%1", QuestionHash: "h-ask"},
			want: ErrBadPutOff, text: "changed 0", malformed: true,
		},
	}
	ctx := t.Context()
	for _, tc := range cases {
		wantRefused(t, tc.name+": NotNow", m.NotNow(ctx, tc.req), tc.want, tc.text)
		if got := markKeys(readMarks(t, path)); len(got) != 0 {
			t.Fatalf("%s: NotNow stored %v, want nothing", tc.name, got)
		}

		// A call that names no question has no mark to seed. Every other case seeds the mark it addresses, so the
		// refused BringBack can be seen to leave it in place.
		kind, session, question := addressedMark(tc.req)
		seeded := !tc.malformed
		var wantMarks []string
		if seeded {
			if err := s.PutOffQuestion(ctx, kind, session, question, fixedClock()); err != nil {
				t.Fatalf("%s: seed mark: %v", tc.name, err)
			}
			wantMarks = []string{markKey(kind, session, question)}
		}

		wantRefused(t, tc.name+": BringBack", m.BringBack(ctx, tc.req), tc.want, tc.text)
		if got := markKeys(readMarks(t, path)); !slices.Equal(got, wantMarks) {
			t.Fatalf("%s: marks after refused BringBack = %v, want %v", tc.name, got, wantMarks)
		}
		if seeded {
			if err := s.BringBackQuestion(ctx, kind, session, question); err != nil {
				t.Fatalf("%s: clear seeded mark: %v", tc.name, err)
			}
		}
	}
	if got := panes.seenCalls(); len(got) != 0 {
		t.Fatalf("refused NotNow calls reported panes seen %+v, want none", got)
	}
}

func TestNotNowServiceRefusalCodes(t *testing.T) {
	t.Parallel()
	m, _, path := newPutOffManager(t, fakeClaudePath(t), nil, nil)
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the waiting state", func(h store.EngineHistory) bool {
		return h.State == "waiting"
	})
	requestID := requestIDOf(t, history)
	socket, _ := startServerWithSteps(t, m, &stepIDs{steps: map[string]bool{"s1": true}})
	client := newClient(t, socket)
	ctx := t.Context()

	cases := []struct {
		name      string
		kind, id  string
		requestID string
		code      connect.Code
		text      string
	}{
		{
			name: "stale request id", kind: store.PutOffHeadless, id: id, requestID: "stale-request",
			code: connect.CodeFailedPrecondition, text: "stale-request",
		},
		{
			name: "unknown session", kind: store.PutOffHeadless, id: unknownID, requestID: requestID,
			code: connect.CodeNotFound, text: unknownID,
		},
		{
			name: "bad kind", kind: "tab", id: id, requestID: requestID,
			code: connect.CodeInvalidArgument, text: `"tab"`,
		},
		{
			name: "headless step id", kind: store.PutOffHeadless, id: "s1", requestID: "r1",
			code: connect.CodeFailedPrecondition, text: "Steps",
		},
	}
	for _, tc := range cases {
		_, err := client.NotNow(ctx, connect.NewRequest(&v1.NotNowRequest{
			Kind: tc.kind, Id: tc.id, RequestId: tc.requestID,
		}))
		wantCode(t, tc.name+": NotNow", err, tc.code, tc.text)
		_, err = client.BringBack(ctx, connect.NewRequest(&v1.BringBackRequest{
			Kind: tc.kind, Id: tc.id, RequestId: tc.requestID,
		}))
		wantCode(t, tc.name+": BringBack", err, tc.code, tc.text)
	}

	unstored := NewManager(Config{Now: fixedClock})
	unstoredSocket, _ := startServer(t, unstored)
	unstoredClient := newClient(t, unstoredSocket)
	_, err = unstoredClient.NotNow(ctx, connect.NewRequest(&v1.NotNowRequest{
		Kind: store.PutOffHeadless, Id: unknownID, RequestId: "ask-1",
	}))
	wantCode(t, "no put-off store: NotNow", err, connect.CodeUnavailable, ErrPutOffUnavailable.Error())
	_, err = unstoredClient.BringBack(ctx, connect.NewRequest(&v1.BringBackRequest{
		Kind: store.PutOffHeadless, Id: unknownID, RequestId: "ask-1",
	}))
	wantCode(t, "no put-off store: BringBack", err, connect.CodeUnavailable, ErrPutOffUnavailable.Error())
}

// notNowRig is what one NotNow subtest runs against: a Router with the phone and desk it sends to, the log it writes
// lines to and the timers it arms, the panes a pane question is read from, and the manager that puts questions off.
type notNowRig struct {
	phone  *fakePhone
	desk   *recordingDesk
	after  *fakeAfter
	log    *fakeRouteLog
	router *route.Router
	panes  *fakePanes
	m      *Manager
	path   string
}

// newNotNowRig returns a notNowRig whose desk the owner is at and whose phone is subscribed, with pane %4 waiting on
// the question h-ask. The Router closes when the test ends.
func newNotNowRig(t *testing.T) notNowRig {
	t.Helper()
	rig := notNowRig{
		phone: &fakePhone{},
		desk:  &recordingDesk{input: fixedClock().Unix()},
		after: &fakeAfter{},
		log:   &fakeRouteLog{},
	}
	rig.router = route.New(route.Config{
		Host:       sessionTestHost,
		Now:        fixedClock,
		After:      rig.after.After,
		Desk:       rig.desk,
		Phone:      rig.phone,
		Subscribed: func() (bool, error) { return true, nil },
		Log:        rig.log,
	})
	t.Cleanup(rig.router.Close)
	rig.panes = &fakePanes{panes: map[string]PaneQuestion{"%4": {Waiting: true, Hash: "h-ask", Changed: 1_700_000_100}}}
	rig.m, _, rig.path = newPutOffManager(
		t, fakeClaudePath(t), NewNotifier(NotifierConfig{Route: rig.router}), rig.panes,
	)
	return rig
}

// TestNotNowEndsPendingNoticeBringBackRaisesNone checks that NotNow ends a question's pending notice and that BringBack
// then raises nothing. For a headless session, the notice ends through the Router's seen path: the desk gets the line
// that ends it and the phone gets its seen report, and BringBack adds no line, post, push or seen report. For a pane,
// NotNow reports the pane seen once through PaneSeen, and BringBack reports nothing. Each case is a subtest.
func TestNotNowEndsPendingNoticeBringBackRaisesNone(t *testing.T) {
	t.Parallel()
	t.Run(
		"headless NotNow ends the pending notice on the desk and the phone through Router.Seen and BringBack raises none",
		func(t *testing.T) {
			t.Parallel()
			rig := newNotNowRig(t)
			phone, desk, after, log := rig.phone, rig.desk, rig.after, rig.log
			router, m, path := rig.router, rig.m, rig.path
			id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "ASK"})
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			waittest.Until(t, nil, "the waiting notice at the desk", func() bool {
				return len(log.got()) == 1 && after.armed() == 1
			})
			waiting := waitHistory(t, engineOf(m), path, id, "the waiting state", func(h store.EngineHistory) bool {
				return h.State == "waiting"
			})
			headless := PutOffRequest{Kind: store.PutOffHeadless, ID: id, RequestID: requestIDOf(t, waiting)}

			if err := m.NotNow(t.Context(), headless); err != nil {
				t.Fatalf("NotNow: %v", err)
			}
			atDesk := route.Line{
				Time: fixedClock().Unix(), Host: sessionTestHost, Session: id,
				State: string(protocol.StateWaiting), Route: route.Desk, Reason: "at the desk",
			}
			ended := atDesk
			ended.Route, ended.Reason = route.None, "seen"
			if got := log.got(); len(got) != 2 || got[0] != atDesk || got[1] != ended {
				t.Fatalf("lines after NotNow = %+v, want [%+v] then [%+v]", got, atDesk, ended)
			}
			wantSeen := []phoneSeen{{key: notice.Key{Session: id}, seen: fixedClock().Unix()}}
			if got := phone.seenCalls(); !slices.Equal(got, wantSeen) {
				t.Fatalf("phone seen reports after NotNow = %+v, want %+v", got, wantSeen)
			}

			if err := m.BringBack(t.Context(), headless); err != nil {
				t.Fatalf("BringBack: %v", err)
			}
			router.Wait()
			if got := log.got(); len(got) != 2 {
				t.Fatalf("lines after BringBack = %+v, want no new line", got)
			}
			if got := desk.postCount(); got != 1 {
				t.Fatalf("desk posts after BringBack = %d, want the one from the waiting notice", got)
			}
			if got := phone.notifyCount(); got != 0 {
				t.Fatalf("phone pushes after BringBack = %d, want none", got)
			}
			if got := phone.seenCalls(); !slices.Equal(got, wantSeen) {
				t.Fatalf("phone seen reports after BringBack = %+v, want %+v", got, wantSeen)
			}
		},
	)
	t.Run("pane NotNow reports the pane seen once and BringBack reports nothing", func(t *testing.T) {
		t.Parallel()
		rig := newNotNowRig(t)
		m, panes := rig.m, rig.panes

		pane := PutOffRequest{Kind: store.PutOffPane, ID: "%4", QuestionHash: "h-ask", Changed: 1_700_000_100}
		if err := m.NotNow(t.Context(), pane); err != nil {
			t.Fatalf("NotNow pane: %v", err)
		}
		got := panes.seenCalls()
		if len(got) != 1 || got[0].pane != "%4" || !got[0].seen.Equal(fixedClock()) {
			t.Fatalf("pane seen calls after NotNow = %+v, want one for %%4 at %v", got, fixedClock())
		}
		if err := m.BringBack(t.Context(), pane); err != nil {
			t.Fatalf("BringBack pane: %v", err)
		}
		if got := panes.seenCalls(); len(got) != 1 {
			t.Fatalf("pane seen calls after BringBack = %+v, want none new", got)
		}
	})
}
