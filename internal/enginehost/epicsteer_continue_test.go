package enginehost

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// errMove is the error the failed-move test's MoveEpic returns.
var errMove = errors.New("fake move failed")

// errRowRead is the error the unreadable-row test's session row read returns.
var errRowRead = errors.New("fake session row read failed")

// failingMove is the EpicCommands the failed-move test serves: the store, except that MoveEpic cancels the call's
// context and then fails, so the stop of the started session must not take that context.
type failingMove struct {
	EpicCommands
	cancel context.CancelFunc
}

// MoveEpic cancels the call's context and returns errMove, and moves nothing.
func (f failingMove) MoveEpic(context.Context, string, string, store.Maker, time.Time) (store.EpicAction, error) {
	f.cancel()
	return store.EpicAction{}, errMove
}

// failingRows is the steering sessions the unreadable-row test serves: every session row read fails.
type failingRows struct{}

// EngineSession returns errRowRead.
func (failingRows) EngineSession(context.Context, string) (store.EngineRow, error) {
	return store.EngineRow{}, errRowRead
}

// newContinueEnv returns the moveEnv of an epic created by a headless conversation started in a new git repository,
// on a Manager served with real steering, and the path of its store.
func newContinueEnv(t *testing.T) (moveEnv, string) {
	t.Helper()
	m, path := newTestManager(t, fakeClaudePath(t))
	st := concreteStore(t, m)
	client := steererSetClient(t, m)
	repo := stepGitTestRepo(t)
	steerer := startedIn(t, m, repo)
	if err := steererSetCreate(t, st, client, repo, sessionHeader(steerer)); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	return moveEnv{m: m, st: st, client: client, repo: repo, steerer: steerer}, path
}

// continueEpicID continues the epic id through client and returns the session it started, or the error.
func continueEpicID(t *testing.T, client enginev1connect.EpicsClient, id string) (string, error) {
	t.Helper()
	resp, err := client.ContinueEpic(t.Context(), connect.NewRequest(&v1.ContinueEpicRequest{Id: id}))
	if err != nil {
		return "", err
	}
	return resp.Msg.GetSession(), nil
}

func TestSteerContinueStartsOneSessionAndMovesEpicToIt(t *testing.T) {
	t.Parallel()
	env, path := newContinueEnv(t)
	before := countRows(t, path, "sessions")

	session, err := continueEpicID(t, env.client, epicServiceID)
	if err != nil {
		t.Fatalf("ContinueEpic: %v", err)
	}
	requireSessionID(t, session)
	wantStartedSteerer(t, env.m, path, env.repo, before)
	env.wantSteerer(t, session)
	env.wantOnlyMove(t, store.Maker{By: store.MakerOwner}, env.steerer, session)
}

func TestSteerContinueUnreadableStartedSessionRefusesNamingReasonAndMovesNothing(t *testing.T) {
	t.Parallel()
	env, path := newContinueEnv(t)
	steering := NewEpicSteering(EpicSteeringConfig{Manager: env.m, Sessions: failingRows{}, Render: rowRender})
	client := newEpicsClient(t, serveEpicsWith(t, env.st, steering))
	before := countRows(t, path, "sessions")

	_, err := continueEpicID(t, client, epicServiceID)
	if got := connect.CodeOf(err); got != connect.CodeUnavailable {
		t.Fatalf("code = %v (%v), want Unavailable", got, err)
	}
	if !strings.Contains(err.Error(), errRowRead.Error()) {
		t.Fatalf("error %q does not name the reason %q", err, errRowRead)
	}
	if n := countRows(t, path, "sessions"); n != before+1 {
		t.Fatalf("sessions rows = %d, want %d: the start made the session before its row read failed", n, before+1)
	}
	env.wantUnmoved(t)
}

func TestSteerContinueSpawnFailedStartedSessionRefusesNamingReasonAndMovesNothing(t *testing.T) {
	t.Parallel()
	const reason = "fake systemd-run refused the scope"
	m, path := newHolderManager(t, fakeSystemdRunPath(t), holderRoot(t), "FAKE_SYSTEMD_RUN_FAIL="+reason)
	st := concreteStore(t, m)
	client := steererSetClient(t, m)
	pane := store.Pane{ID: "orrery-pane", Pane: "%7", Fields: store.Fields{State: "working"}}
	if err := st.SyncPane(t.Context(), "-S "+steerSocket, pane, fixedClock()); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	repo := stepGitTestRepo(t)
	if err := steererSetCreate(t, st, client, repo, paneHeader("%7")); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	env := moveEnv{m: m, st: st, client: client, repo: repo, steerer: pane.ID}
	before := countRows(t, path, "sessions")

	_, err := continueEpicID(t, client, epicServiceID)
	if got := connect.CodeOf(err); got != connect.CodeUnavailable {
		t.Fatalf("code = %v (%v), want Unavailable", got, err)
	}
	if !strings.Contains(err.Error(), reason) {
		t.Fatalf("error %q does not name the reason %q", err, reason)
	}
	if n := countRows(t, path, "sessions"); n != before+1 {
		t.Fatalf("sessions rows = %d, want %d: the start recorded the session as failed", n, before+1)
	}
	env.wantUnmoved(t)
}

func TestSteerContinueFailedMoveStopsStartedSession(t *testing.T) {
	t.Parallel()
	env, path := newContinueEnv(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	steering := NewEpicSteering(EpicSteeringConfig{Manager: env.m, Sessions: env.m.store, Render: rowRender})
	svc := &epicService{epics: failingMove{EpicCommands: env.st, cancel: cancel}, steering: steering}

	_, err := svc.ContinueEpic(ctx, connect.NewRequest(&v1.ContinueEpicRequest{Id: epicServiceID}))
	if err == nil || !strings.Contains(err.Error(), errMove.Error()) {
		t.Fatalf("ContinueEpic error = %v, want one carrying the move failure %q", err, errMove)
	}
	if strings.Contains(err.Error(), "stop steering session") {
		t.Fatalf("ContinueEpic error %q reports a failed stop of the started session", err)
	}
	rows, err := env.st.EngineSessions(t.Context())
	if err != nil {
		t.Fatalf("EngineSessions: %v", err)
	}
	var started []string
	for _, row := range rows {
		if row.ID != env.steerer {
			started = append(started, row.ID)
		}
	}
	if len(started) != 1 {
		t.Fatalf("started sessions = %v, want exactly one", started)
	}
	if got := historyOf(t, path, started[0]).State; got != "stopped" {
		t.Fatalf("state of the started session = %q, want stopped", got)
	}
	env.wantUnmoved(t)
}

func TestSteerContinueWithoutSteeringRefusesUnavailable(t *testing.T) {
	t.Parallel()
	env, path := newContinueEnv(t)
	client := newEpicsClient(t, serveEpicsWith(t, env.st, nil))
	before := countRows(t, path, "sessions")

	_, err := continueEpicID(t, client, epicServiceID)
	wantConnectError(t, err, connect.CodeUnavailable, steeringUnavailableMessage)
	if n := countRows(t, path, "sessions"); n != before {
		t.Fatalf("sessions rows = %d, want %d: a refused continue starts no session", n, before)
	}
	env.wantUnmoved(t)
}

func TestSteerContinueRefusesEmptyOrUnknownEpicAndStartsNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		id      string
		code    connect.Code
		message string
	}{
		{name: "empty id", id: "", code: connect.CodeInvalidArgument, message: "id: empty"},
		{name: "unknown id", id: "orrery-missing", code: connect.CodeNotFound, message: "id: no epic orrery-missing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			env, path := newContinueEnv(t)
			before := countRows(t, path, "sessions")

			_, err := continueEpicID(t, env.client, c.id)
			wantConnectError(t, err, c.code, c.message)
			if n := countRows(t, path, "sessions"); n != before {
				t.Fatalf("sessions rows = %d, want %d: a refused continue starts no session", n, before)
			}
			env.wantUnmoved(t)
		})
	}
}
