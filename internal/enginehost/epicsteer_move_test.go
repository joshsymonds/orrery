package enginehost

import (
	"context"
	"maps"
	"net/http"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// movePanes is the SteeringSessions the move tests serve with: fixedSteering's Start and Stop, and a PaneDir that returns
// dir, the current path the test gives every pane.
type movePanes struct {
	fixedSteering
	dir string
}

// PaneDir returns the directory the test set.
func (p *movePanes) PaneDir(context.Context, string, string) (string, error) { return p.dir, nil }

// moveEnv is the world a move test runs in: a Manager over its store, a client of the Epics service served with panes,
// the git repository of the epic, and the headless conversation that created the epic and steers it.
type moveEnv struct {
	m       *Manager
	st      *store.Store
	client  enginev1connect.EpicsClient
	panes   *movePanes
	repo    string
	steerer string
}

// newMoveEnv returns the moveEnv of an epic created by a headless conversation started in a new git repository.
func newMoveEnv(t *testing.T) moveEnv {
	t.Helper()
	m, _ := newTestManager(t, 4, fakeClaudePath(t))
	st := concreteStore(t, m)
	panes := &movePanes{}
	client := newEpicsClient(t, serveEpicsWith(t, st, panes))
	repo := stepGitTestRepo(t)
	steerer := startedIn(t, m, repo)
	if err := steererSetCreate(t, st, client, repo, sessionHeader(steerer)); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	return moveEnv{m: m, st: st, client: client, panes: panes, repo: repo, steerer: steerer}
}

// startedIn starts a headless session in dir on m and returns its id.
func startedIn(t *testing.T, m *Manager, dir string) string {
	t.Helper()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start of a conversation in %s: %v", dir, err)
	}
	return id
}

// syncPane syncs the tmux pane pane, with id and state, on the test socket, and returns its id.
func (e moveEnv) syncPane(t *testing.T, id, pane, state string) string {
	t.Helper()
	row := store.Pane{ID: id, Pane: pane, Fields: store.Fields{State: state}}
	if err := e.st.SyncPane(t.Context(), "-S "+steerSocket, row, fixedClock()); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	return id
}

// move moves the test epic to session as the caller whose headers are header, and returns the error.
func (e moveEnv) move(t *testing.T, session string, header http.Header) error {
	t.Helper()
	return e.moveID(t, epicServiceID, session, header)
}

// moveID moves the epic id to session as the caller whose headers are header, and returns the error.
func (e moveEnv) moveID(t *testing.T, id, session string, header http.Header) error {
	t.Helper()
	req := connect.NewRequest(&v1.MoveEpicRequest{Id: id, Session: session})
	maps.Copy(req.Header(), header)
	_, err := e.client.MoveEpic(t.Context(), req)
	return err
}

// wantSteerer asserts that the test epic's steerer is id.
func (e moveEnv) wantSteerer(t *testing.T, id string) {
	t.Helper()
	row, err := e.st.Epic(t.Context(), epicServiceID)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if row.Steerer != id {
		t.Fatalf("steerer = %q, want %q", row.Steerer, id)
	}
}

// wantOnlyMove asserts that the test epic's history holds exactly one action, a move made by maker from the steerer from
// to the steerer to, with its time set.
func (e moveEnv) wantOnlyMove(t *testing.T, maker store.Maker, from, to string) {
	t.Helper()
	history, err := e.st.EpicHistory(t.Context(), epicServiceID)
	if err != nil {
		t.Fatalf("EpicHistory: %v", err)
	}
	if len(history.Actions) != 1 {
		t.Fatalf("actions = %+v, want exactly one move", history.Actions)
	}
	got := history.Actions[0]
	if got.Kind != store.ActionMove || got.Maker != maker || got.From != from || got.To != to || got.At.IsZero() {
		t.Fatalf("action = %+v, want a move at a set time by %+v from %q to %q", got, maker, from, to)
	}
}

// wantUnmoved asserts that the test epic is still steered by the conversation that created it and has no action.
func (e moveEnv) wantUnmoved(t *testing.T) {
	t.Helper()
	e.wantSteerer(t, e.steerer)
	history, err := e.st.EpicHistory(t.Context(), epicServiceID)
	if err != nil {
		t.Fatalf("EpicHistory: %v", err)
	}
	if len(history.Actions) != 0 {
		t.Fatalf("actions = %+v, want none after a refused move", history.Actions)
	}
}

func TestSteerMoveOwnerNamesHeadlessSessionInRepository(t *testing.T) {
	t.Parallel()
	env := newMoveEnv(t)
	taker := startedIn(t, env.m, env.repo)

	if err := env.move(t, taker, nil); err != nil {
		t.Fatalf("MoveEpic: %v", err)
	}
	env.wantSteerer(t, taker)
	env.wantOnlyMove(t, store.Maker{By: store.MakerOwner}, env.steerer, taker)
}

func TestSteerMoveEmptySessionFromHeadlessConversationMovesToCaller(t *testing.T) {
	t.Parallel()
	env := newMoveEnv(t)
	taker := startedIn(t, env.m, env.repo)

	if err := env.move(t, "", sessionHeader(taker)); err != nil {
		t.Fatalf("MoveEpic: %v", err)
	}
	env.wantSteerer(t, taker)
	env.wantOnlyMove(t, store.Maker{By: store.MakerConversation, Session: taker}, env.steerer, taker)
}

func TestSteerMoveEmptySessionFromPaneWithStateMovesToCaller(t *testing.T) {
	t.Parallel()
	env := newMoveEnv(t)
	env.panes.dir = env.repo
	pane := env.syncPane(t, "orrery-pane", "%7", "working")

	if err := env.move(t, "", paneHeader("%7")); err != nil {
		t.Fatalf("MoveEpic: %v", err)
	}
	env.wantSteerer(t, pane)
	env.wantOnlyMove(t, store.Maker{By: store.MakerConversation, Session: pane}, env.steerer, pane)
}

func TestSteerMoveToCurrentSteererIsRefusedAndWritesNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		// call returns the session and the caller headers of the move that names the current steerer.
		call func(env moveEnv) (string, http.Header)
	}{
		{name: "owner names the steerer", call: func(env moveEnv) (string, http.Header) {
			return env.steerer, nil
		}},
		{name: "steerer moves to itself", call: func(env moveEnv) (string, http.Header) {
			return "", sessionHeader(env.steerer)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			env := newMoveEnv(t)
			session, header := c.call(env)

			err := env.move(t, session, header)
			wantConnectError(t, err, connect.CodeFailedPrecondition,
				"epic "+epicServiceID+" is already steered by session "+env.steerer)
			env.wantUnmoved(t)
		})
	}
}

func TestSteerMoveEmptySessionWithoutConversationIsRefused(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		header func(t *testing.T, env moveEnv) http.Header
	}{
		{name: "no caller headers", header: func(*testing.T, moveEnv) http.Header { return nil }},
		{name: "pane with empty state", header: func(t *testing.T, env moveEnv) http.Header {
			t.Helper()
			env.syncPane(t, "orrery-empty", "%3", "")
			return paneHeader("%3")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			env := newMoveEnv(t)

			err := env.move(t, "", c.header(t, env))
			wantConnectError(t, err, connect.CodeFailedPrecondition,
				"session: empty, and no conversation called epic_steer")
			env.wantUnmoved(t)
		})
	}
}

func TestSteerMoveUnknownEpicIsNotFoundAndEmptyIDIsInvalid(t *testing.T) {
	t.Parallel()
	env := newMoveEnv(t)
	taker := startedIn(t, env.m, env.repo)

	err := env.moveID(t, "", taker, nil)
	wantConnectError(t, err, connect.CodeInvalidArgument, "id: empty")
	err = env.moveID(t, "epic-missing", taker, nil)
	wantConnectError(t, err, connect.CodeNotFound, "id: no epic epic-missing")
	env.wantUnmoved(t)
}
