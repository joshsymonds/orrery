package server_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/server"
)

// startHostAgent serves an agent for routingHost whose Root is root, on a loopback listener until the test ends. It
// returns the client that a controller reaches the agent through.
func startHostAgent(t *testing.T, root string) *node.Client {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	agent, err := node.NewAgent(node.AgentConfig{
		Controller: routingController,
		WhoIs:      routingWhoIs{own: ln.Addr().String(), host: routingHost},
		Holders:    routingHolders{},
		Roots:      []string{root},
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- agent.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if serveErr := <-served; serveErr != nil {
			t.Errorf("serve the agent of %s: %v", routingHost, serveErr)
		}
	})
	return node.NewClient(node.ClientConfig{
		Name:    routingHost,
		Address: ln.Addr().String(),
		Dial:    (&net.Dialer{}).DialContext,
		Clock:   quietClock{},
	})
}

// hostStartHandler returns the guarded handler of a Start test over the controller routingController. Its home is home,
// its engine is engine, and its hosts.toml hosts are hosts.
func hostStartHandler(
	t *testing.T,
	home string,
	engine enginev1connect.EngineClient,
	hosts ...*node.Client,
) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:   []string{allowedLogin},
		Tmux:     newTmuxClient(t),
		Push:     newPushStore(t),
		Engine:   engine,
		Home:     home,
		HostName: routingController,
		Hosts:    hosts,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// startOnHost sends a Start of dir on host through control, with the request fields the start tests do not vary.
func startOnHost(
	t *testing.T,
	control orreryv1connect.ControlClient,
	host, dir string,
) (*connect.Response[orreryv1.StartResponse], error) {
	t.Helper()
	return control.Start(t.Context(), connect.NewRequest(&orreryv1.StartRequest{
		Host: host, Dir: dir, Prompt: "synthetic prompt", Id: synthSessionID,
	}))
}

// assertOnlyStart fails unless fake recorded exactly one Start, which names host and dir, and the start answered with
// fakeStartedID.
func assertOnlyStart(
	t *testing.T,
	fake *fakeEngine,
	resp *connect.Response[orreryv1.StartResponse],
	host, dir string,
) {
	t.Helper()
	if got := resp.Msg.GetId(); got != fakeStartedID {
		t.Fatalf("Start answered id %q, want %q", got, fakeStartedID)
	}
	calls := fake.recorded()
	if len(calls) != 1 {
		t.Fatalf("engine recorded %d calls, want one Start", len(calls))
	}
	start, ok := calls[0].(*enginev1.StartRequest)
	if !ok {
		t.Fatalf("engine recorded %T, want *enginev1.StartRequest", calls[0])
	}
	if start.GetHost() != host || start.GetDir() != dir {
		t.Fatalf("engine started host %q dir %q, want host %q dir %q", start.GetHost(), start.GetDir(), host, dir)
	}
}

// makeHostFolder creates the directory dir, and fails the test when it cannot.
func makeHostFolder(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

// TestPublicHostsStartOnHost starts a session on host b in a folder under b's root, reached through a symlink, so the
// start must forward the resolved folder the agent answers, not the path it was sent.
func TestPublicHostsStartOnHost(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	makeHostFolder(t, project)
	link := filepath.Join(root, "link")
	if err := os.Symlink(project, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	fake := &fakeEngine{}
	control := controlClient(t, hostStartHandler(t, t.TempDir(), newFakeOrreryd(t, fake), startHostAgent(t, root)))

	resp, err := startOnHost(t, control, routingHost, link)
	if err != nil {
		t.Fatalf("Start on %s: %v", routingHost, err)
	}
	assertOnlyStart(t, fake, resp, routingHost, resolvedPath(t, project))
}

// TestPublicHostsStartWithoutHost starts a session with no host while host b is configured. The folder is under home,
// so the start keeps the home check, and it forwards an empty host.
func TestPublicHostsStartWithoutHost(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dir, resolved := sessionUnder(t, home)
	fake := &fakeEngine{}
	control := controlClient(t, hostStartHandler(t, home, newFakeOrreryd(t, fake), startHostAgent(t, t.TempDir())))

	resp, err := startOnHost(t, control, "", dir)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	assertOnlyStart(t, fake, resp, "", resolved)
}

// TestPublicHostsStartFolderOnlyOnHost starts a session on host b in a folder that exists only under b's root. The
// folder is not under the controller's home, so the start is admitted by the agent's check alone.
func TestPublicHostsStartFolderOnlyOnHost(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	makeHostFolder(t, project)
	fake := &fakeEngine{}
	control := controlClient(t, hostStartHandler(t, t.TempDir(), newFakeOrreryd(t, fake), startHostAgent(t, root)))

	resp, err := startOnHost(t, control, routingHost, project)
	if err != nil {
		t.Fatalf("Start on %s: %v", routingHost, err)
	}
	assertOnlyStart(t, fake, resp, routingHost, resolvedPath(t, project))
}

// TestPublicHostsStartRefusesFolderAbsentOnHost starts a session on host b in a folder that b's root does not hold. The
// agent refuses it, so the start answers invalid_argument and forwards nothing.
func TestPublicHostsStartRefusesFolderAbsentOnHost(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	missing := filepath.Join(root, "missing")
	fake := &fakeEngine{}
	control := controlClient(t, hostStartHandler(t, t.TempDir(), newFakeOrreryd(t, fake), startHostAgent(t, root)))

	_, err := startOnHost(t, control, routingHost, missing)
	assertConnectError(t, err, connect.CodeInvalidArgument,
		"session directory must be an existing directory under the host's root")
	if calls := fake.recorded(); len(calls) != 0 {
		t.Fatalf("engine recorded %d calls for a refused start, want none", len(calls))
	}
}

// TestPublicHostsStartUnknownHost starts a session on a host that no hosts.toml host names. The start answers
// invalid_argument naming the host, and forwards nothing.
func TestPublicHostsStartUnknownHost(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	makeHostFolder(t, project)
	fake := &fakeEngine{}
	control := controlClient(t, hostStartHandler(t, t.TempDir(), newFakeOrreryd(t, fake), startHostAgent(t, root)))

	_, err := startOnHost(t, control, "c", project)
	assertConnectError(t, err, connect.CodeInvalidArgument, "unknown host c")
	if calls := fake.recorded(); len(calls) != 0 {
		t.Fatalf("engine recorded %d calls for an unknown host, want none", len(calls))
	}
}

// TestPublicHostsStartUnreachableHost starts a session on host b whose agent cannot be dialed. The dial fails at once
// with an error that is not a refusal, so the start answers unavailable naming the host, and forwards nothing.
func TestPublicHostsStartUnreachableHost(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "project")
	fake := &fakeEngine{}
	down := node.NewClient(node.ClientConfig{
		Name:    routingHost,
		Address: "127.0.0.1:1",
		Dial: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("synthetic dial failure")
		},
		Clock: quietClock{},
	})
	control := controlClient(t, hostStartHandler(t, t.TempDir(), newFakeOrreryd(t, fake), down))

	_, err := startOnHost(t, control, routingHost, dir)
	assertConnectError(t, err, connect.CodeUnavailable, "host b is unreachable")
	if calls := fake.recorded(); len(calls) != 0 {
		t.Fatalf("engine recorded %d calls for an unreachable host, want none", len(calls))
	}
}
