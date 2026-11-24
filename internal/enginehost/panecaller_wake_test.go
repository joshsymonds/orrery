package enginehost_test

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"

	"github.com/joshsymonds/orrery/internal/contracttext"
	"github.com/joshsymonds/orrery/internal/enginehost"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/hostrelay"
	"github.com/joshsymonds/orrery/internal/hosts"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/planner"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// Synthetic values only. The controller, the agent of host b, the tmux socket, the epic and its repository are
// invented.
const (
	wakeController = "ctl"
	wakeHostB      = "b"
	wakeLogin      = "owner@example.com"
	wakeEpicID     = "synthetic-repo-0a1b2c3/epic-wake"
	// wakeTmux is the TMUX of the tmux socket the pane callers run in, and wakeColumn is the server column of the pane
	// rows that socket's panes have.
	wakeTmux   = "/tmp/tmux-1000/orrery,4242,0"
	wakeColumn = "-S /tmp/tmux-1000/orrery"
	// wakeContractAt is the unix second the contract is proposed and accepted at.
	wakeContractAt = 1700002000
)

// wakeStamp is the unix second of each store write and each send of the wake tests.
const wakeStamp = 1700000000

// wakeNow returns the time of each store write and each send of the wake tests.
func wakeNow() time.Time { return time.Unix(wakeStamp, 0) }

// wakeNever returns a channel that never receives, in place of a timer, so no wait of the tests is cut short by a real
// one.
func wakeNever(time.Duration) <-chan time.Time { return make(chan time.Time) }

// wakeNeverClock is the clock of the node client in these tests. Its timers never fire.
type wakeNeverClock struct{}

// After returns a channel that never receives.
func (wakeNeverClock) After(time.Duration) <-chan time.Time { return make(chan time.Time) }

// wakeRelayWhoIs answers WhoIs as the relay's tailscaled does for the test: every address is host b's node, signed in
// as wakeLogin. The relay asks about its own address only for its login, and admits each peer by its node's name.
type wakeRelayWhoIs struct{}

// WhoIs names the node at addr as host b's node.
func (wakeRelayWhoIs) WhoIs(context.Context, string) (*apitype.WhoIsResponse, error) {
	return &apitype.WhoIsResponse{
		Node:        &tailcfg.Node{ComputedName: wakeHostB},
		UserProfile: &tailcfg.UserProfile{LoginName: wakeLogin},
	}, nil
}

// wakeAgentWhoIs answers WhoIs as the agent's tailscaled does: the address own is the agent's own node, which is host
// b's, and every other address is the controller's node. Both are signed in as wakeLogin.
type wakeAgentWhoIs struct {
	own string
}

// WhoIs names the node at addr, as wakeAgentWhoIs describes.
func (w wakeAgentWhoIs) WhoIs(_ context.Context, addr string) (*apitype.WhoIsResponse, error) {
	name := wakeController
	if addr == w.own {
		name = wakeHostB
	}
	return &apitype.WhoIsResponse{
		Node:        &tailcfg.Node{ComputedName: name},
		UserProfile: &tailcfg.UserProfile{LoginName: wakeLogin},
	}, nil
}

// wakeCall is one wake a pane spy was asked for.
type wakeCall struct {
	server, pane, text string
}

// paneSpy is a pane waker that records each wake it is asked for, and types into each pane it is asked to.
type paneSpy struct {
	// mu guards calls, which an agent's goroutine appends to.
	mu    sync.Mutex
	calls []wakeCall
}

// WakePane records the wake, and reports that the pane was idle to take it.
func (p *paneSpy) WakePane(_ context.Context, server, pane, text string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, wakeCall{server: server, pane: pane, text: text})
	return true, nil
}

// recorded returns the wakes the spy was asked for, in order.
func (p *paneSpy) recorded() []wakeCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]wakeCall(nil), p.calls...)
}

// wakeStore opens a migrated store file at a temporary path. The store closes at cleanup. It returns the store and the
// path of its file.
func wakeStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	st, err := store.Open(t.Context(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return st, path
}

// syncWakePanes syncs pane %3 of wakeColumn under host b and again under host ctl, as two rows with the ids b-3 and
// ctl-3. It fails the test on error.
func syncWakePanes(t *testing.T, st *store.Store) {
	t.Helper()
	for _, host := range []string{wakeHostB, wakeController} {
		panes := []store.Pane{{ID: host + "-3", Pane: "%3"}}
		if err := st.SyncServer(t.Context(), host, wakeColumn, panes, wakeNow()); err != nil {
			t.Fatalf("SyncServer under host %s: %v", host, err)
		}
	}
}

// wakeGit runs git with args in dir, under a synthetic identity, and returns its output. It fails the test on error.
func wakeGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=Synthetic", "-c", "user.email=synthetic@example.com"}, args...)
	cmd := exec.CommandContext(t.Context(), "git", full...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s in %s: %v", strings.Join(args, " "), dir, err)
	}
	return strings.TrimSpace(string(out))
}

// wakeRepo creates a git repository with one commit in a temporary directory, and returns its path and the commit's id.
func wakeRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	wakeGit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("base\n"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	wakeGit(t, dir, "add", "README")
	wakeGit(t, dir, "commit", "-q", "-m", "base")
	return dir, wakeGit(t, dir, "rev-parse", "HEAD")
}

// wakeContract returns a valid contract of the epic wakeEpicID, naming repo and based at base. It has every section at
// two or more rows.
func wakeContract(repo, base string) *v1.EpicContract {
	item := &v1.EvidenceItem{
		Kind:       "test",
		Capture:    []string{"scripts/check"},
		Limit:      60,
		Conclusion: "Synthetic conclusion.",
		Judge:      "reviewer",
		Tests:      []string{"internal/synthetic/check_test.go"},
	}
	return &v1.EpicContract{
		Id:         wakeEpicID,
		Repository: repo,
		Title:      "Epic wake",
		Approved:   "Synthetic approval sentence.",
		Intent:     "Synthetic intent.",
		Failures: []*v1.EpicFailure{
			{Id: "F1", Text: "Synthetic failure one.", Rating: "serious", Response: "Synthetic response one."},
			{Id: "F2", Text: "Synthetic failure two.", Rating: "severe", Response: "Synthetic response two."},
		},
		Care: "serious",
		Additions: []*v1.EpicAddition{
			{Location: "R1", What: "Synthetic addition one.", Why: "Synthetic reason.", Cost: "Synthetic cost."},
			{Location: "F2", What: "Synthetic addition two.", Why: "Synthetic reason.", Cost: "None."},
			{Location: "docs/guide.md", What: "Synthetic note.", Why: "Synthetic reason.", Cost: "None."},
		},
		Requirements: []*v1.EpicRequirement{
			{Id: "R1", Text: "Synthetic requirement one.", Item: item},
			{Id: "R2", Text: "Synthetic requirement two.", Item: item},
		},
		Premises: []*v1.EpicPremise{
			{Id: "P1", Text: "Synthetic premise one.", IfWrong: "Synthetic consequence one."},
			{Id: "P2", Text: "Synthetic premise two.", IfWrong: "Synthetic consequence two."},
		},
		Exclusions: []string{"Synthetic exclusion one.", "Synthetic exclusion two."},
		Approach:   "Synthetic approach.",
		Alternatives: []*v1.EpicAlternative{
			{Alternative: "Synthetic alternative one.", WhyNot: "Synthetic reason.", Reconsider: "Synthetic trigger."},
			{Alternative: "Synthetic alternative two.", WhyNot: "Synthetic reason.", Reconsider: "Never."},
		},
		ReleaseSteps: []*v1.EpicReleaseStep{
			{Step: "Synthetic release one.", Target: "Synthetic target one.", Undo: "Synthetic undo one."},
			{Step: "Synthetic release two.", Target: "Synthetic target two.", Undo: "Synthetic undo two."},
		},
		ReleaseThen: "Synthetic release line.",
		Checks:      "go test ./...\nQuality Bar: " + contracttext.QualityBar,
		Base:        base,
		Gates: []*v1.EpicGate{
			{Name: "check", Argv: []string{"scripts/check"}, Limit: 60},
			{Name: "review", Agent: "reviewer", Limit: 300, Must: []string{"Synthetic statement holds."}},
		},
		DoneGates: []string{"check"},
	}
}

// proposeWakeContract stores contract in st as version 1 of a contract whose writer is writer, then accepts it as the
// owner. The writer's session steers the epic that the contract makes. It fails the test on error.
func proposeWakeContract(t *testing.T, st *store.Store, contract *v1.EpicContract, writer string) {
	t.Helper()
	body, err := protojson.Marshal(contract)
	if err != nil {
		t.Fatalf("encode contract: %v", err)
	}
	version := store.ContractVersion{
		Repository: contract.GetRepository(), Title: contract.GetTitle(), Care: contract.GetCare(),
		Body: string(body), Words: "Synthetic words.", Writer: writer, At: wakeContractAt,
	}
	if err = st.ProposeContract(t.Context(), contract.GetId(), version); err != nil {
		t.Fatalf("ProposeContract %s: %v", contract.GetId(), err)
	}
	if err = st.AcceptContract(t.Context(), contract.GetId(), store.ContractChange{At: wakeContractAt}); err != nil {
		t.Fatalf("AcceptContract %s: %v", contract.GetId(), err)
	}
}

// wakeHandler returns the handler of the Epics service over st, which the controller serves, and records each call that
// carries caller headers.
func wakeHandler(st *store.Store) http.Handler {
	return enginehost.NewHandler(enginehost.ServeConfig{
		Epics: st,
		Host:  wakeController,
		Options: []connect.HandlerOption{connect.WithInterceptors(
			enginehost.EpicCallInterceptor(st, wakeNow, wakeNever, slog.New(slog.DiscardHandler)),
		)},
	})
}

// startWakeRelay serves handler behind hostrelay.Serve, which admits host b's agent, and returns the relay's address.
// The relay stops at cleanup.
func startWakeRelay(t *testing.T, handler http.Handler) string {
	t.Helper()
	relay, err := hostrelay.Listen(t.Context(), hostrelay.Config{
		Address: "127.0.0.1:0",
		Hosts:   []hosts.Host{{Name: wakeHostB, Address: wakeHostB + ":7000"}},
		WhoIs:   wakeRelayWhoIs{},
		Handler: handler,
	})
	if err != nil {
		t.Fatalf("hostrelay.Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- relay.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("serve relay: %v", err)
		}
	})
	return relay.Addr().String()
}

// createWakeEpic creates the epic of contract through client, as a pane caller whose TMUX_PANE is pane. It sets the
// caller's Orrery-Host to host when host is not "": a call on the controller's own socket carries it, and a call
// through the relay has its host set by the relay. It fails the test on error.
func createWakeEpic(t *testing.T, client enginev1connect.EpicsClient, contract *v1.EpicContract, pane, host string) {
	t.Helper()
	req := connect.NewRequest(&v1.CreateEpicRequest{
		Id:   contract.GetId(),
		Base: contract.GetBase(),
		Decisions: []*v1.EpicDecisionInput{
			{Id: "D1", Text: "Synthetic decision one."},
			{Id: "D2", Text: "Synthetic decision two."},
			{Id: "D3", Text: "Synthetic decision three."},
		},
	})
	req.Header().Set(protocol.HeaderTmux, wakeTmux)
	req.Header().Set(protocol.HeaderTmuxPane, pane)
	if host != "" {
		req.Header().Set(protocol.HeaderHost, host)
	}
	if _, err := client.CreateEpic(t.Context(), req); err != nil {
		t.Fatalf("CreateEpic %s: %v", contract.GetId(), err)
	}
}

// startWakeAgent serves an agent of host b, which admits the controller and types each wake through wake, and returns a
// node.Client that reaches the agent. The agent stops at cleanup.
func startWakeAgent(t *testing.T, wake func(context.Context, string, string, string) (bool, error)) *node.Client {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for the agent of %s: %v", wakeHostB, err)
	}
	dir := t.TempDir()
	agent, err := node.NewAgent(node.AgentConfig{
		Controller: wakeController,
		WhoIs:      wakeAgentWhoIs{own: ln.Addr().String()},
		Holders:    holderhost.NewLocal(holderhost.LocalConfig{StateDir: dir}),
		StateDir:   dir,
		WakePane:   wake,
	})
	if err != nil {
		t.Fatalf("new agent of %s: %v", wakeHostB, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- agent.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("serve agent of %s: %v", wakeHostB, err)
		}
	})
	return node.NewClient(node.ClientConfig{
		Name:    wakeHostB,
		Address: ln.Addr().String(),
		Dial:    (&net.Dialer{}).DialContext,
		Clock:   wakeNeverClock{},
	})
}

// wakeSender returns a CallerSender over st that wakes panes through wake and refuses every headless delivery.
func wakeSender(
	t *testing.T, st *store.Store, wake func(context.Context, string, string, string, string) (bool, error),
) *enginehost.CallerSender {
	t.Helper()
	return enginehost.NewCallerSender(enginehost.CallerSenderConfig{
		Deliver: func(context.Context, enginehost.DeliverRequest) error {
			t.Error("a pane caller received a headless delivery")
			return nil
		},
		WakePane:    wake,
		SessionHost: st.SessionHost,
		Store:       st,
		Now:         wakeNow,
	})
}

// passEpicWakes runs the epic caller-event pass over st through sender. It fails the test on error.
func passEpicWakes(t *testing.T, st *store.Store, sender *enginehost.CallerSender) {
	t.Helper()
	pass := planner.NewEpicCallerEvents(planner.EpicCallerEventsConfig{Store: st, Sender: sender})
	if err := pass.Pass(t.Context()); err != nil {
		t.Fatalf("epic caller-event pass: %v", err)
	}
}

// requirePauseWake requires that calls holds a wake of pane on server whose text is the owner's pause of the epic, and
// that every call is to that pane and server. Contracts also owe their writer an event for the accept, so the calls may
// hold other wakes.
func requirePauseWake(t *testing.T, calls []wakeCall, server, pane string) {
	t.Helper()
	paused := false
	for _, call := range calls {
		if call.server != server || call.pane != pane {
			t.Errorf("wake to server %q pane %q, want %q %q", call.server, call.pane, server, pane)
		}
		if strings.Contains(call.text, "paused the epic") {
			paused = true
		}
	}
	if !paused {
		t.Fatalf("wakes = %+v, want the owner's pause of %s", calls, wakeEpicID)
	}
}

// TestPaneCallerEpicCreateRecordsHostRow has host b's pane %3 create an epic through the relay, which admits b's agent.
// The epic's creator row is b's pane row, and the controller's pane row with the same pane is never recorded.
func TestPaneCallerEpicCreateRecordsHostRow(t *testing.T) {
	t.Parallel()
	st, path := wakeStore(t)
	syncWakePanes(t, st)
	repo, base := wakeRepo(t)
	contract := wakeContract(repo, base)
	proposeWakeContract(t, st, contract, wakeHostB+"-3")
	client := enginev1connect.NewEpicsClient(http.DefaultClient, "http://"+startWakeRelay(t, wakeHandler(st)))

	createWakeEpic(t, client, contract, "%3", "")

	rows, err := store.NewReader(path).EpicSessions(t.Context(), wakeEpicID)
	if err != nil {
		t.Fatalf("EpicSessions: %v", err)
	}
	creators := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Session == wakeController+"-3" {
			t.Fatalf("the controller's pane row %s made a call of the epic, want none", row.Session)
		}
		if row.Creator {
			creators = append(creators, row.Session)
		}
	}
	if want := []string{wakeHostB + "-3"}; !reflect.DeepEqual(creators, want) {
		t.Fatalf("creator rows = %v, want %v", creators, want)
	}
}

// TestPaneCallerWakeReachesHostAgent has host b's pane %3 create an epic through the relay, then the owner pauses the
// epic. The epic caller-event pass, sending through a HostPaneWake, wakes the pane through host b's agent, and the
// controller's own pane waker records nothing.
func TestPaneCallerWakeReachesHostAgent(t *testing.T) {
	t.Parallel()
	st, _ := wakeStore(t)
	syncWakePanes(t, st)
	repo, base := wakeRepo(t)
	contract := wakeContract(repo, base)
	proposeWakeContract(t, st, contract, wakeHostB+"-3")
	client := enginev1connect.NewEpicsClient(http.DefaultClient, "http://"+startWakeRelay(t, wakeHandler(st)))
	createWakeEpic(t, client, contract, "%3", "")

	agentPane := &paneSpy{}
	controllerPane := &paneSpy{}
	agent := startWakeAgent(t, agentPane.WakePane)
	sender := wakeSender(t, st, enginehost.HostPaneWake(
		wakeController, controllerPane.WakePane, map[string]enginehost.PaneWaker{wakeHostB: agent},
	))
	if err := st.PauseEpic(t.Context(), wakeEpicID, store.Maker{By: store.MakerOwner}, wakeNow()); err != nil {
		t.Fatalf("pause epic %s: %v", wakeEpicID, err)
	}

	passEpicWakes(t, st, sender)
	requirePauseWake(t, agentPane.recorded(), wakeColumn, "%3")
	if calls := controllerPane.recorded(); len(calls) != 0 {
		t.Fatalf("controller's pane waker calls = %+v, want none", calls)
	}
}

// TestPaneCallerWakeControllerPaneLocal has the controller's pane %3 create an epic on the controller's own socket,
// then the owner pauses the epic. The epic caller-event pass wakes that pane through the local waker only, and the
// agent of host b records nothing.
func TestPaneCallerWakeControllerPaneLocal(t *testing.T) {
	t.Parallel()
	st, _ := wakeStore(t)
	syncWakePanes(t, st)
	repo, base := wakeRepo(t)
	contract := wakeContract(repo, base)
	proposeWakeContract(t, st, contract, wakeController+"-3")
	server := httptest.NewServer(wakeHandler(st))
	t.Cleanup(server.Close)
	client := enginev1connect.NewEpicsClient(server.Client(), server.URL)
	createWakeEpic(t, client, contract, "%3", wakeController)

	local := &paneSpy{}
	agent := &paneSpy{}
	sender := wakeSender(t, st, enginehost.HostPaneWake(
		wakeController, local.WakePane, map[string]enginehost.PaneWaker{wakeHostB: agent},
	))
	if err := st.PauseEpic(t.Context(), wakeEpicID, store.Maker{By: store.MakerOwner}, wakeNow()); err != nil {
		t.Fatalf("pause epic %s: %v", wakeEpicID, err)
	}

	passEpicWakes(t, st, sender)
	requirePauseWake(t, local.recorded(), wakeColumn, "%3")
	if calls := agent.recorded(); len(calls) != 0 {
		t.Fatalf("agent of %s wake calls = %+v, want none", wakeHostB, calls)
	}
}

// TestPaneCallerWakeUnknownHostFails sends a wake to a pane caller on a host that is neither the controller nor an
// agent. The send fails naming that host, no waker is called, and the event is not marked.
func TestPaneCallerWakeUnknownHostFails(t *testing.T) {
	t.Parallel()
	st, _ := wakeStore(t)
	syncWakePanes(t, st)
	const ghost = "ghost"
	ghostPanes := []store.Pane{{ID: ghost + "-3", Pane: "%3"}}
	if err := st.SyncServer(t.Context(), ghost, wakeColumn, ghostPanes, wakeNow()); err != nil {
		t.Fatalf("SyncServer under host %s: %v", ghost, err)
	}
	local := &paneSpy{}
	agent := &paneSpy{}
	sender := wakeSender(t, st, enginehost.HostPaneWake(
		wakeController, local.WakePane, map[string]enginehost.PaneWaker{wakeHostB: agent},
	))
	const event = "epic:" + wakeEpicID + ":A1"
	caller := store.CallerSession{ID: ghost + "-3", Server: wakeColumn, Pane: "%3"}

	finished, err := sender.Send(t.Context(), caller, event, "synthetic text")
	if err == nil || !strings.Contains(err.Error(), ghost) {
		t.Fatalf("Send to host %s = %v, %v; want an error naming it", ghost, finished, err)
	}
	if finished {
		t.Fatalf("Send to host %s reported the event finished", ghost)
	}
	if calls := local.recorded(); len(calls) != 0 {
		t.Fatalf("controller's pane waker calls = %+v, want none", calls)
	}
	if calls := agent.recorded(); len(calls) != 0 {
		t.Fatalf("agent of %s wake calls = %+v, want none", wakeHostB, calls)
	}
	marked, err := st.CallerEventMarked(t.Context(), event, caller.ID)
	if err != nil {
		t.Fatalf("CallerEventMarked: %v", err)
	}
	if marked {
		t.Fatalf("event %s marked delivered to %s, want no mark", event, caller.ID)
	}
}
