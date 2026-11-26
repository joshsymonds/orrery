package enginehost

import (
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/fleettest"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/hostrepo"
	"github.com/joshsymonds/orrery/internal/store"
)

// epicHostClient serves the Epics service of st over httptest as NewHandler serves it on the fleet's controller: the
// fleet's hosts and roots, placement as the test's placement places it, and steering that starts no session. The server
// closes at cleanup.
func epicHostClient(
	t *testing.T, st *store.Store, fleet *fleettest.Fleet, placement HostPlacer,
) enginev1connect.EpicsClient {
	t.Helper()
	roots := make(map[string]hostrepo.RootLister, len(fleet.Clients))
	for name, client := range fleet.Clients {
		roots[name] = client
	}
	srv := httptest.NewServer(NewHandler(ServeConfig{
		Epics: st, Steering: fixedSteering{}, Hosts: fleet.Hosts, Host: fleet.Controller,
		Placement: placement, Roots: roots,
	}))
	t.Cleanup(srv.Close)
	return enginev1connect.NewEpicsClient(srv.Client(), srv.URL)
}

// epicHostPlacement returns the Placement of fleet's controller over the agents named names, which it places by their
// clients. A def that is empty names no default host.
func epicHostPlacement(fleet *fleettest.Fleet, def string, names ...string) *Placement {
	reachers := make(map[string]Reacher, len(names))
	for _, name := range names {
		reachers[name] = fleet.Clients[name]
	}
	return NewPlacement(PlacementConfig{Controller: fleet.Controller, Default: def, Hosts: reachers})
}

// epicHostCheckout clones the fleet's origin into a checkout on the controller's machine, which the test's contract names,
// and returns the checkout's path. Its base is the origin's main.
func epicHostCheckout(t *testing.T, fleet *fleettest.Fleet) string {
	t.Helper()
	parent := t.TempDir()
	stepGitTestRun(t, parent, "clone", "-q", "--", fleet.Origin, "repo")
	return filepath.Join(parent, "repo")
}

// epicHostCreate proposes and accepts c's contract in st, then creates its epic through client with the host named host,
// and returns the create's error. A blank host names no host.
func epicHostCreate(
	t *testing.T, st *store.Store, client enginev1connect.EpicsClient, c *epicCreate, host string,
) error {
	t.Helper()
	acceptEpicContract(t, st, c.Contract)
	req := c.request()
	req.Host = host
	_, err := client.CreateEpic(t.Context(), connect.NewRequest(req))
	return err
}

// epicHostRow returns the stored epic with epicServiceID. It fails the test on error.
func epicHostRow(t *testing.T, st *store.Store) store.EpicRow {
	t.Helper()
	row, err := st.Epic(t.Context(), epicServiceID)
	if err != nil {
		t.Fatalf("Epic %s: %v", epicServiceID, err)
	}
	return row
}

// epicHostNoneStored asserts that no epic with epicServiceID is stored in st.
func epicHostNoneStored(t *testing.T, st *store.Store) {
	t.Helper()
	if _, err := st.Epic(t.Context(), epicServiceID); !errors.Is(err, store.ErrNoEpic) {
		t.Fatalf("refused epic stored: Epic err = %v, want ErrNoEpic", err)
	}
}

// epicHostRefusal asserts that err is a Connect error with code, and returns its message.
func epicHostRefusal(t *testing.T, err error, code connect.Code) string {
	t.Helper()
	if err == nil {
		t.Fatalf("CreateEpic succeeded, want a refusal with code %v", code)
	}
	if got := connect.CodeOf(err); got != code {
		t.Fatalf("code = %v, want %v (%v)", got, code, err)
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("error is %T, want *connect.Error", err)
	}
	return connectErr.Message()
}

// epicHostCheckoutState returns the state of the checkout at repo that a fetch must leave alone: its HEAD, its working
// tree, its local branches, its index and the content of its untracked file.
func epicHostCheckoutState(t *testing.T, repo string) string {
	t.Helper()
	untracked, err := os.ReadFile(filepath.Join(repo, "untracked.txt"))
	if err != nil {
		t.Fatalf("read untracked.txt of %s: %v", repo, err)
	}
	return strings.Join([]string{
		stepGitTestRun(t, repo, "rev-parse", "HEAD"),
		stepGitTestRun(t, repo, "status", "--porcelain"),
		stepGitTestRun(t, repo, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads"),
		stepGitTestRun(t, repo, "ls-files", "--stage"),
		string(untracked),
	}, "\n")
}

// TestEpicCreateDefaultHostNoNameStoresDefault asserts that a create that names no host stores the epic on the default
// host, and readies its repository there.
func TestEpicCreateDefaultHostNoNameStoresDefault(t *testing.T) {
	t.Parallel()
	fleet := fleettest.Start(t, "a", "b")
	st := epicTestStore(t)
	client := epicHostClient(t, st, fleet, epicHostPlacement(fleet, "b", "a", "b"))
	c := fullEpicRequest(epicHostCheckout(t, fleet), epicServiceID)

	if err := epicHostCreate(t, st, client, c, ""); err != nil {
		t.Fatalf("CreateEpic %s: %v", epicServiceID, err)
	}
	if host := epicHostRow(t, st).Host; host != "b" {
		t.Fatalf("stored host = %q, want the default host b", host)
	}
}

// TestEpicCreateDefaultHostNamedReachable asserts that a create that names a reachable host stores the epic on that
// host, not on the default host.
func TestEpicCreateDefaultHostNamedReachable(t *testing.T) {
	t.Parallel()
	fleet := fleettest.Start(t, "a", "b")
	st := epicTestStore(t)
	client := epicHostClient(t, st, fleet, epicHostPlacement(fleet, "b", "a", "b"))
	c := fullEpicRequest(epicHostCheckout(t, fleet), epicServiceID)

	if err := epicHostCreate(t, st, client, c, "a"); err != nil {
		t.Fatalf("CreateEpic %s: %v", epicServiceID, err)
	}
	if host := epicHostRow(t, st).Host; host != "a" {
		t.Fatalf("stored host = %q, want the named host a", host)
	}
}

// TestEpicCreateDefaultHostRefusesUnknownAndUnreachable asserts that a create that names a host no placement knows, or
// a host whose agent is stopped, is refused naming that host, and stores nothing.
func TestEpicCreateDefaultHostRefusesUnknownAndUnreachable(t *testing.T) {
	t.Parallel()
	t.Run("unknown", func(t *testing.T) {
		t.Parallel()
		fleet := fleettest.Start(t, "a", "b")
		st := epicTestStore(t)
		client := epicHostClient(t, st, fleet, epicHostPlacement(fleet, "b", "a", "b"))
		c := fullEpicRequest(epicHostCheckout(t, fleet), epicServiceID)

		err := epicHostCreate(t, st, client, c, "nowhere")
		wantConnectError(t, err, connect.CodeInvalidArgument, "host nowhere is not one this controller runs work on")
		epicHostNoneStored(t, st)
	})
	t.Run("unreachable", func(t *testing.T) {
		t.Parallel()
		fleet := fleettest.Start(t, "a", "b")
		st := epicTestStore(t)
		client := epicHostClient(t, st, fleet, epicHostPlacement(fleet, "b", "a", "b"))
		c := fullEpicRequest(epicHostCheckout(t, fleet), epicServiceID)
		fleet.StopAgent(t, "a")
		fleet.WaitReach(t, "a", false)

		err := epicHostCreate(t, st, client, c, "a")
		wantConnectError(t, err, connect.CodeFailedPrecondition, "host a is unreachable")
		epicHostNoneStored(t, st)
	})
}

// TestEpicCreateDefaultHostNoDefaultStoresController asserts that a create with no default host, or with no placement,
// stores the epic on the controller's host when it names no host, and that with no placement any other name is refused as
// unknown.
func TestEpicCreateDefaultHostNoDefaultStoresController(t *testing.T) {
	t.Parallel()
	t.Run("placement with no default", func(t *testing.T) {
		t.Parallel()
		fleet := fleettest.Start(t, "a")
		st := epicTestStore(t)
		client := epicHostClient(t, st, fleet, epicHostPlacement(fleet, "", "a"))
		c := fullEpicRequest(epicHostCheckout(t, fleet), epicServiceID)

		if err := epicHostCreate(t, st, client, c, ""); err != nil {
			t.Fatalf("CreateEpic %s: %v", epicServiceID, err)
		}
		if host := epicHostRow(t, st).Host; host != fleet.Controller {
			t.Fatalf("stored host = %q, want the controller %q", host, fleet.Controller)
		}
	})
	t.Run("no placement", func(t *testing.T) {
		t.Parallel()
		fleet := fleettest.Start(t, "a")
		st := epicTestStore(t)
		client := epicHostClient(t, st, fleet, nil)
		c := fullEpicRequest(epicHostCheckout(t, fleet), epicServiceID)

		if err := epicHostCreate(t, st, client, c, ""); err != nil {
			t.Fatalf("CreateEpic %s: %v", epicServiceID, err)
		}
		if host := epicHostRow(t, st).Host; host != fleet.Controller {
			t.Fatalf("stored host = %q, want the controller %q", host, fleet.Controller)
		}
	})
	t.Run("no placement refuses another name", func(t *testing.T) {
		t.Parallel()
		fleet := fleettest.Start(t, "a")
		st := epicTestStore(t)
		client := epicHostClient(t, st, fleet, nil)
		c := fullEpicRequest(epicHostCheckout(t, fleet), epicServiceID)

		err := epicHostCreate(t, st, client, c, "a")
		wantConnectError(t, err, connect.CodeInvalidArgument, "host a is not one this controller runs work on")
		epicHostNoneStored(t, st)
	})
}

// TestEpicCreateRepoOnHostClonesAndHoldsBase asserts that a create on a host with no checkout of the repository clones
// its origin under the host's first root, and that the base is in the clone when the create returns. The stored
// repository is that clone's path.
func TestEpicCreateRepoOnHostClonesAndHoldsBase(t *testing.T) {
	t.Parallel()
	fleet := fleettest.Start(t, "a", "b")
	st := epicTestStore(t)
	client := epicHostClient(t, st, fleet, epicHostPlacement(fleet, "", "a", "b"))
	c := fullEpicRequest(epicHostCheckout(t, fleet), epicServiceID)
	want := filepath.Join(fleet.Roots["b"], "repo")
	if _, err := os.Stat(want); err == nil {
		t.Fatalf("b holds a checkout at %s before the create", want)
	}

	if err := epicHostCreate(t, st, client, c, "b"); err != nil {
		t.Fatalf("CreateEpic %s: %v", epicServiceID, err)
	}
	if repo := epicHostRow(t, st).Repository; repo != want {
		t.Fatalf("stored repository = %q, want b's clone %q", repo, want)
	}
	if origin := stepGitTestRun(t, want, "remote", "get-url", "origin"); origin != fleet.Origin {
		t.Fatalf("origin of the clone = %q, want %q", origin, fleet.Origin)
	}
	stepGitTestRun(t, want, "cat-file", "-e", c.Contract.GetBase()+"^{commit}")
}

// TestEpicCreateRepoOnHostFetchesExistingCheckout asserts that a create on a host that holds a checkout of the repository
// at the contract's path fetches origin into it, and leaves its working tree, index and local branches as they were.
func TestEpicCreateRepoOnHostFetchesExistingCheckout(t *testing.T) {
	t.Parallel()
	fleet := fleettest.Start(t, "a", "b")
	st := epicTestStore(t)
	client := epicHostClient(t, st, fleet, epicHostPlacement(fleet, "", "a", "b"))
	repo := filepath.Join(fleet.Roots["b"], "repo")
	stepGitTestRun(t, fleet.Roots["b"], "clone", "-q", "--", fleet.Origin, "repo")
	stepGitTestWrite(t, repo, "staged.txt", "staged\n")
	stepGitTestRun(t, repo, "add", "staged.txt")
	stepGitTestWrite(t, repo, "untracked.txt", "untracked\n")
	stepGitTestRun(t, repo, "branch", "owner-work")
	before := epicHostCheckoutState(t, repo)
	c := fullEpicRequest(repo, epicServiceID)
	pushed := fleet.PushCommit(t, "pushed after the clone")

	if err := epicHostCreate(t, st, client, c, "b"); err != nil {
		t.Fatalf("CreateEpic %s: %v", epicServiceID, err)
	}
	if got := epicHostRow(t, st).Repository; got != repo {
		t.Fatalf("stored repository = %q, want b's checkout %q", got, repo)
	}
	if got := stepGitTestRun(t, repo, "rev-parse", "origin/main"); got != pushed {
		t.Fatalf("origin/main of the checkout = %s, want the fetched %s", got, pushed)
	}
	if after := epicHostCheckoutState(t, repo); after != before {
		t.Fatalf("the fetch changed the checkout:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestEpicCreateRepoOnHostBlockedNamesHostAndGitError asserts that a create whose repository cannot be readied on its
// host is refused as a failed precondition naming that host and carrying git's error, and stores nothing: when the
// origin is unreachable, and when the base is not in the origin.
func TestEpicCreateRepoOnHostBlockedNamesHostAndGitError(t *testing.T) {
	t.Parallel()
	t.Run("unreachable origin", func(t *testing.T) {
		t.Parallel()
		fleet := fleettest.Start(t, "a", "b")
		st := epicTestStore(t)
		client := epicHostClient(t, st, fleet, epicHostPlacement(fleet, "", "a", "b"))
		repo := epicHostCheckout(t, fleet)
		stepGitTestRun(t, repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))
		c := fullEpicRequest(repo, epicServiceID)

		err := epicHostCreate(t, st, client, c, "b")
		msg := epicHostRefusal(t, err, connect.CodeFailedPrecondition)
		if !strings.HasPrefix(msg, "hostrepo: b: ") || !strings.Contains(msg, "exit status") {
			t.Fatalf("refusal %q does not name b and carry git's error", msg)
		}
		epicHostNoneStored(t, st)
	})
	t.Run("base missing from origin", func(t *testing.T) {
		t.Parallel()
		fleet := fleettest.Start(t, "a", "b")
		st := epicTestStore(t)
		client := epicHostClient(t, st, fleet, epicHostPlacement(fleet, "", "a", "b"))
		repo := epicHostCheckout(t, fleet)
		stepGitTestCommit(t, repo, map[string]string{"local.txt": "local\n"})
		c := fullEpicRequest(repo, epicServiceID)

		err := epicHostCreate(t, st, client, c, "b")
		msg := epicHostRefusal(t, err, connect.CodeFailedPrecondition)
		if !strings.HasPrefix(msg, "hostrepo: b: ") || !strings.Contains(msg, "missing from") {
			t.Fatalf("refusal %q does not name b and the missing base", msg)
		}
		epicHostNoneStored(t, st)
	})
}
