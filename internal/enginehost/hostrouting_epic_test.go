package enginehost

import (
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/store"
)

// hostRoutingEpicCreate proposes and accepts the contract of repo in st, then creates its epic through an epic service
// on hosts whose controller is the host named controller. The contract's capture runs a go test, so its checks read the
// base's test functions. It fails the test on error.
func hostRoutingEpicCreate(t *testing.T, st *store.Store, repo string, hosts hostexec.Hosts, controller string) {
	t.Helper()
	c := fullEpicRequest(repo, epicServiceID)
	c.Contract.Requirements[0].Item.Capture = []string{
		"scripts/in-devenv", "-c", "go test -run TestSynthetic ./pkg/",
	}
	acceptEpicContract(t, st, c.Contract)
	svc := &epicService{epics: st, steering: fixedSteering{}, hosts: hosts, host: controller}
	if _, err := svc.CreateEpic(t.Context(), connect.NewRequest(c.request())); err != nil {
		t.Fatalf("CreateEpic %s: %v", epicServiceID, err)
	}
}

// hostRoutingEpicSession adds a headless conversation in dir to st, and returns its id. A move reads only the session's
// row, so the row is written without a process behind it.
func hostRoutingEpicSession(t *testing.T, st *store.Store, dir string) string {
	t.Helper()
	const id = "routing-conversation"
	engine := store.EngineAdd{ID: id, Dir: dir, State: "idle"}
	if err := st.ApplyEngine(t.Context(), []store.EngineOp{engine}, fixedClock()); err != nil {
		t.Fatalf("ApplyEngine %s: %v", id, err)
	}
	return id
}

// hostRoutingEpicMove moves the epic id to session through an epic service on hosts whose controller is the host named
// controller. It fails the test on error.
func hostRoutingEpicMove(t *testing.T, st *store.Store, hosts hostexec.Hosts, controller, id, session string) {
	t.Helper()
	svc := &epicService{epics: st, steering: fixedSteering{}, hosts: hosts, host: controller}
	req := connect.NewRequest(&v1.MoveEpicRequest{Id: id, Session: session})
	if _, err := svc.MoveEpic(t.Context(), req); err != nil {
		t.Fatalf("MoveEpic %s: %v", id, err)
	}
}

// TestEpicHostRoutingEpicServices covers the routing of the epic and contract services to their hosts. Creating an epic
// from a contract runs its checks, its workflow read and its lint's git on the controller's host, and moving an epic
// reads the epic's repository on the host the epic is stored on, while the session's directory is read on the
// controller's host. Host a, which is never the host of an epic or the controller that makes one, records no call.
func TestEpicHostRoutingEpicServices(t *testing.T) {
	t.Parallel()
	t.Run("contract checks go to the controller host", func(t *testing.T) {
		t.Parallel()
		a, b, hosts := hostRoutingGatesHosts()
		repo := stepGitTestRepo(t)
		st := epicTestStore(t)
		hostRoutingEpicCreate(t, st, repo, hosts, "b")
		calls := b.Calls()
		if !hostRoutingGitCall(calls, repo, "rev-parse", "--is-inside-work-tree") {
			t.Errorf("host b recorded no repository check of %s: %v", repo, calls)
		}
		if !hostRoutingGitCall(calls, repo, "ls-tree") {
			t.Errorf("host b recorded no workflow read of %s: %v", repo, calls)
		}
		if !hostRoutingGitCall(calls, repo, "grep") {
			t.Errorf("host b recorded no lint read of the test functions of %s: %v", repo, calls)
		}
		if calls := a.Calls(); len(calls) != 0 {
			t.Errorf("host a recorded %d calls, want none: %v", len(calls), calls)
		}
	})
	t.Run("steer runs no git on either host", func(t *testing.T) {
		t.Parallel()
		a, b, hosts := hostRoutingGatesHosts()
		repo := stepGitTestRepo(t)
		st := epicTestStore(t)
		hostRoutingGatesEpic(t, st, repo, hosts)
		session := hostRoutingEpicSession(t, st, t.TempDir())
		before := len(b.Calls())
		hostRoutingEpicMove(t, st, hosts, "a", hostRoutingEpic, session)
		if calls := b.Calls()[before:]; len(calls) != 0 {
			t.Errorf("host b recorded %d calls for the steer, want none: %v", len(calls), calls)
		}
		if calls := a.Calls(); len(calls) != 0 {
			t.Errorf("host a recorded %d calls for the steer, want none: %v", len(calls), calls)
		}
	})
}
