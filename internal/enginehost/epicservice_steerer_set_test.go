package enginehost

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// steerSocket is the tmux socket the steering tests' panes run on. The store keeps a pane's server as "-S " and this
// socket path.
const steerSocket = "/tmp/tmux-steer/orrery"

// errStoreWrite is the error the failed-store test's CreateEpic returns.
var errStoreWrite = errors.New("fake store write failed")

// failingCreate is the EpicCommands the failed-store test serves: the store, except that CreateEpic fails and stores
// nothing.
type failingCreate struct{ EpicCommands }

// CreateContractEpic returns errStoreWrite.
func (failingCreate) CreateContractEpic(context.Context, store.EpicRow, int64) error {
	return errStoreWrite
}

// concreteStore returns the *store.Store m was built on, which the test managers are given. The Manager holds it as its
// narrower Store interface.
func concreteStore(t *testing.T, m *Manager) *store.Store {
	t.Helper()
	s, ok := m.store.(*store.Store)
	if !ok {
		t.Fatalf("manager store is %T, want *store.Store", m.store)
	}
	return s
}

// rowRender is the Render of the steering tests: the epic's title as its contract line and its seed decision ids as its
// log line, so a steering prompt shows that the created epic's own contract and log reached its session.
func rowRender(row store.EpicRow) ([]string, error) {
	ids := make([]string, 0, len(row.Decisions))
	for _, decision := range row.Decisions {
		ids = append(ids, decision.ID)
	}
	return []string{"Contract: " + row.Title, "Log: " + strings.Join(ids, " ")}, nil
}

// steererSetClient serves the Epics service over m's store, with steering that starts sessions on m and renders with
// rowRender, and returns the client that dials it.
func steererSetClient(t *testing.T, m *Manager) enginev1connect.EpicsClient {
	t.Helper()
	steering := NewEpicSteering(EpicSteeringConfig{Manager: m, Sessions: m.store, Render: rowRender})
	return newEpicsClient(t, serveEpicsWith(t, concreteStore(t, m), steering))
}

// steererSetCreate proposes the contract of repo in st under the conversation the caller whose headers are caller is,
// when it is one, and under contractEpicWriter otherwise. It accepts the contract, then creates its epic through
// client as that caller, and returns the error.
func steererSetCreate(
	t *testing.T, st *store.Store, client enginev1connect.EpicsClient, repo string, caller http.Header,
) error {
	t.Helper()
	c := fullEpicRequest(repo, epicServiceID)
	callerOf, _ := epicCallerOf(caller)
	maker, err := st.CallerMaker(t.Context(), callerOf)
	if err != nil {
		t.Fatalf("CallerMaker: %v", err)
	}
	writer := contractEpicWriter
	if maker.By == store.MakerConversation {
		writer = maker.Session
	}
	proposeAcceptedBy(t, st, c.Contract, writer)
	req := connect.NewRequest(c.request())
	maps.Copy(req.Header(), caller)
	_, err = client.CreateEpic(t.Context(), req)
	return err
}

// wantEpicNotStored asserts that the epic is not stored, as a read through client finds it not.
func wantEpicNotStored(t *testing.T, client enginev1connect.EpicsClient) {
	t.Helper()
	_, err := client.GetEpic(t.Context(), connect.NewRequest(&v1.GetEpicRequest{Id: epicServiceID}))
	if got := connect.CodeOf(err); got != connect.CodeNotFound {
		t.Fatalf("GetEpic after a refused create: code = %v (%v), want NotFound", got, err)
	}
}

// startedConversation starts a headless session in a new directory on m and returns its id. It is the conversation a
// create names through sessionHeader.
func startedConversation(t *testing.T, m *Manager) string {
	t.Helper()
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start of a conversation: %v", err)
	}
	return id
}

// sessionHeader returns the caller headers of a create made in the headless session id.
func sessionHeader(id string) http.Header {
	return http.Header{protocol.HeaderSession: {id}}
}

// paneHeader returns the caller headers of a create made in the tmux pane on steerSocket.
func paneHeader(pane string) http.Header {
	return http.Header{protocol.HeaderTmux: {steerSocket + ",4242,0"}, protocol.HeaderTmuxPane: {pane}}
}

// wantStartedSteerer asserts that a create through client that started a session stored exactly one new session in
// the sessions rows at path, that the epic's steerer is that session, and that the session ran in repo with the
// steering prompt. before is the sessions row count before the create.
func wantStartedSteerer(t *testing.T, m *Manager, path, repo string, before int) {
	t.Helper()
	if n := countRows(t, path, "sessions"); n != before+1 {
		t.Fatalf("sessions rows = %d, want %d: the create starts exactly one session", n, before+1)
	}
	row, err := concreteStore(t, m).Epic(t.Context(), epicServiceID)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if row.Steerer == "" {
		t.Fatalf("steerer of the created epic is empty, want the started session")
	}
	history := waitHistory(
		t, engineOf(m), path, row.Steerer, "the first reply block", func(h store.EngineHistory) bool {
			return hasBlock(t, h, "fake reply 1")
		})
	if history.Dir != repo {
		t.Fatalf("session dir = %q, want %q", history.Dir, repo)
	}
	want := strings.Join([]string{
		steeringInstruction,
		"Epic: " + epicServiceID,
		"Contract: Epic service",
		"Log: D1 D2 D3",
	}, "\n")
	prompts := enginePromptsOf(t, history)
	if len(prompts) != 1 || prompts[0].Text != want {
		t.Fatalf("prompts = %+v, want one prompt with text %q", prompts, want)
	}
}

func TestSteererSetHeadlessConversationIsSteererWithoutStart(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 2, fakeClaudePath(t))
	client := steererSetClient(t, m)
	conversation := startedConversation(t, m)
	before := countRows(t, path, "sessions")

	if err := steererSetCreate(
		t,
		concreteStore(t, m),
		client,
		stepGitTestRepo(t),
		sessionHeader(conversation),
	); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	if n := countRows(t, path, "sessions"); n != before {
		t.Fatalf("sessions rows = %d, want %d: a headless conversation starts no session", n, before)
	}
	row, err := concreteStore(t, m).Epic(t.Context(), epicServiceID)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if row.Steerer != conversation {
		t.Fatalf("steerer = %q, want the conversation %q", row.Steerer, conversation)
	}
}

func TestSteererSetPaneWithStateIsSteererWithoutStart(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 2, fakeClaudePath(t))
	client := steererSetClient(t, m)
	pane := store.Pane{ID: "orrery-pane", Pane: "%7", Fields: store.Fields{State: "working"}}
	if err := concreteStore(t, m).SyncPane(t.Context(), "-S "+steerSocket, pane, fixedClock()); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	before := countRows(t, path, "sessions")

	if err := steererSetCreate(t, concreteStore(t, m), client, stepGitTestRepo(t), paneHeader("%7")); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	if n := countRows(t, path, "sessions"); n != before {
		t.Fatalf("sessions rows = %d, want %d: a pane with state starts no session", n, before)
	}
	row, err := concreteStore(t, m).Epic(t.Context(), epicServiceID)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if row.Steerer != pane.ID {
		t.Fatalf("steerer = %q, want the pane's session %q", row.Steerer, pane.ID)
	}
}

func TestSteererSetCallerWithoutConversationStartsOneSession(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		// caller seeds the store with the row the case names and returns the caller headers of the create.
		caller func(t *testing.T, s *store.Store) http.Header
	}{
		{name: "no caller", caller: func(*testing.T, *store.Store) http.Header { return nil }},
		{name: "empty-state pane", caller: func(t *testing.T, s *store.Store) http.Header {
			t.Helper()
			pane := store.Pane{ID: "orrery-empty", Pane: "%3"}
			if err := s.SyncPane(t.Context(), "-S "+steerSocket, pane, fixedClock()); err != nil {
				t.Fatalf("SyncPane: %v", err)
			}
			return paneHeader("%3")
		}},
		{name: "header naming no row", caller: func(*testing.T, *store.Store) http.Header {
			return sessionHeader("session-missing")
		}},
		{name: "step session", caller: func(t *testing.T, s *store.Store) http.Header {
			t.Helper()
			step := store.EngineAdd{ID: "step-1", Dir: t.TempDir(), State: "idle", Kind: store.KindStep}
			if err := s.ApplyEngine(t.Context(), []store.EngineOp{step}, fixedClock()); err != nil {
				t.Fatalf("ApplyEngine: %v", err)
			}
			return sessionHeader("step-1")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			m, path := newTestManager(t, 2, fakeClaudePath(t))
			client := steererSetClient(t, m)
			caller := c.caller(t, concreteStore(t, m))
			repo := stepGitTestRepo(t)
			before := countRows(t, path, "sessions")

			if err := steererSetCreate(t, concreteStore(t, m), client, repo, caller); err != nil {
				t.Fatalf("CreateEpic: %v", err)
			}
			wantStartedSteerer(t, m, path, repo, before)
		})
	}
}

func TestSteererSetStoredIDIsRefusedBeforeAnyStart(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 2, fakeClaudePath(t))
	client := steererSetClient(t, m)
	repo := stepGitTestRepo(t)
	if err := steererSetCreate(
		t,
		concreteStore(t, m),
		client,
		repo,
		sessionHeader(startedConversation(t, m)),
	); err != nil {
		t.Fatalf("first CreateEpic: %v", err)
	}
	before := countRows(t, path, "sessions")

	_, err := client.CreateEpic(t.Context(), connect.NewRequest(&v1.CreateEpicRequest{
		Id: epicServiceID, Base: epicTestBase(repo),
	}))
	wantConnectError(t, err, connect.CodeAlreadyExists, "id: "+epicServiceID+" is already in the database")
	if n := countRows(t, path, "sessions"); n != before {
		t.Fatalf("sessions rows = %d, want %d: a stored id starts no session", n, before)
	}
}

func TestSteererSetFullCapRefusesNamingCapAndStoresNothing(t *testing.T) {
	t.Parallel()
	m, _ := newTestManager(t, 1, fakeClaudePath(t))
	client := steererSetClient(t, m)
	startedConversation(t, m)

	err := steererSetCreate(t, concreteStore(t, m), client, stepGitTestRepo(t), nil)
	if got := connect.CodeOf(err); got != connect.CodeResourceExhausted {
		t.Fatalf("code = %v (%v), want ResourceExhausted", got, err)
	}
	if !strings.Contains(err.Error(), "cap of 1") {
		t.Fatalf("error %q does not name the cap of 1", err)
	}
	wantEpicNotStored(t, client)
}

func TestSteererSetSpawnFailureRefusesNamingReasonAndStoresNothing(t *testing.T) {
	t.Parallel()
	const reason = "fake systemd-run refused the scope"
	m, _ := newHolderManager(t, fakeSystemdRunPath(t), holderRoot(t), "FAKE_SYSTEMD_RUN_FAIL="+reason)
	client := steererSetClient(t, m)

	err := steererSetCreate(t, concreteStore(t, m), client, stepGitTestRepo(t), nil)
	if got := connect.CodeOf(err); got != connect.CodeUnavailable {
		t.Fatalf("code = %v (%v), want Unavailable", got, err)
	}
	if !strings.Contains(err.Error(), reason) {
		t.Fatalf("error %q does not name the reason %q", err, reason)
	}
	wantEpicNotStored(t, client)
}

func TestSteererSetFailedStoreStopsStartedSession(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	steering := NewEpicSteering(EpicSteeringConfig{Manager: m, Sessions: m.store, Render: rowRender})
	client := newEpicsClient(t, serveEpicsWith(t, failingCreate{concreteStore(t, m)}, steering))
	repo := stepGitTestRepo(t)

	err := steererSetCreate(t, concreteStore(t, m), client, repo, nil)
	if got := connect.CodeOf(err); got != connect.CodeInternal {
		t.Fatalf("code = %v (%v), want Internal", got, err)
	}
	if !strings.Contains(err.Error(), errStoreWrite.Error()) {
		t.Fatalf("error %q does not carry the store failure %q", err, errStoreWrite)
	}
	rows, err := m.store.EngineSessions(t.Context())
	if err != nil {
		t.Fatalf("EngineSessions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("engine sessions = %d, want the one started session", len(rows))
	}
	if got := historyOf(t, path, rows[0].ID).State; got != "stopped" {
		t.Fatalf("state of the started session = %q, want stopped", got)
	}
}
