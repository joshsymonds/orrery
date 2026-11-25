package server_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/open"
	"github.com/joshsymonds/orrery/internal/server"
)

// proposeUnreachable is the orrery name of the host whose address nothing listens on.
const proposeUnreachable = "c"

// proposeControllerLoad returns the load of the controller, and proposeHostLoad the load of host b. They differ, so a
// stdin that carries the other host's load fails the tests.
func proposeControllerLoad() node.Load {
	return node.Load{LoadPercent: 25.5, MemoryPercent: 61.25}
}

// proposeHostLoad returns the load of host b.
func proposeHostLoad() node.Load {
	return node.Load{LoadPercent: 150.5, MemoryPercent: 40.75}
}

// instantClock is a clock whose every timer has already fired, so a client on it gives up each wait at once.
type instantClock struct{}

// After returns a channel that already holds a time.
func (instantClock) After(time.Duration) <-chan time.Time {
	fired := make(chan time.Time, 1)
	fired <- time.Time{}
	return fired
}

// installProposeClaude writes a fake claude that copies its stdin to the file stdin beside it and then prints reply. It
// returns the path of the binary and the path of the stdin file.
func installProposeClaude(t *testing.T, reply string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := `#!/bin/sh
D=$(dirname "$0")
cat > "$D/stdin"
cat "$D/reply"
`
	writePickerFile(t, bin, script)
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatalf("chmod fake claude: %v", err)
	}
	writePickerFile(t, filepath.Join(dir, "reply"), reply)
	return bin, filepath.Join(dir, "stdin")
}

// startProposeAgent serves an agent for host, whose folders are under root and whose load is load, on a loopback
// listener until the test ends. It returns the client that a controller reaches the agent through.
func startProposeAgent(t *testing.T, host, root string, load node.Load) *node.Client {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	agent, err := node.NewAgent(node.AgentConfig{
		Controller: routingController,
		WhoIs:      routingWhoIs{own: ln.Addr().String(), host: host},
		Holders:    routingHolders{},
		Root:       root,
		Panes:      statusPanes{routingPanes: routingPanes{handler: http.NotFoundHandler()}, load: load},
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
			t.Errorf("serve the agent of %s: %v", host, serveErr)
		}
	})
	return node.NewClient(node.ClientConfig{
		Name:    host,
		Address: ln.Addr().String(),
		Dial:    (&net.Dialer{}).DialContext,
		Clock:   quietClock{},
	})
}

// unreachableClient returns the client of host c, whose address refuses connections. Its clock has fired already, so
// the client's bound passes at once and each refused dial is given up.
func unreachableClient(t *testing.T) *node.Client {
	t.Helper()
	return node.NewClient(node.ClientConfig{
		Name:    proposeUnreachable,
		Address: refusedAddress(t),
		Dial:    (&net.Dialer{}).DialContext,
		Clock:   instantClock{},
	})
}

// startProposeServer returns the guarded handler of a Propose test, and the path the fake claude writes its stdin to.
// The controller is routingController, with its folders under root and its load proposeControllerLoad. Host b is
// routingHost, with its folders under bRoot and its load proposeHostLoad, and host c is unreachable. The fake claude
// prints reply.
func startProposeServer(t *testing.T, root, bRoot, reply string) (http.Handler, string) {
	t.Helper()
	claude, stdin := installProposeClaude(t, reply)
	tmux := newTmuxClient(t)
	cfg := open.Config{Tmux: tmux, Claude: claude, Root: root, Home: t.TempDir()}
	handler, err := server.New(server.Config{
		Logins:    []string{allowedLogin},
		Tmux:      tmux,
		Push:      newPushStore(t),
		Picker:    func() (open.Config, error) { return cfg, nil },
		HostName:  routingController,
		LocalLoad: func() (node.Load, error) { return proposeControllerLoad(), nil },
		Hosts: []*node.Client{
			startProposeAgent(t, routingHost, bRoot, proposeHostLoad()),
			unreachableClient(t),
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler, stdin
}

// propose asks handler for the proposals of text through Picker.Propose, and returns them or the error it answered.
func propose(t *testing.T, handler http.Handler, text string) ([]*orreryv1.StartProposal, error) {
	t.Helper()
	request := connect.NewRequest(&orreryv1.ProposeRequest{Text: text})
	answer, err := pickerClient(t, handler).Propose(t.Context(), request)
	if err != nil {
		return nil, err
	}
	return answer.Msg.GetProposals(), nil
}

// readStdin returns the lines of the stdin the fake claude wrote to path.
func readStdin(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the claude stdin: %v", err)
	}
	return strings.Split(string(data), "\n")
}

// requireProposals fails unless got holds the proposals of want, in order, each with the same host and directory.
func requireProposals(t *testing.T, want, got []*orreryv1.StartProposal) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("proposals = %v, want %v", got, want)
	}
	for index := range want {
		if !proto.Equal(want[index], got[index]) {
			t.Errorf("proposals[%d] = %v, want %v", index, got[index], want[index])
		}
	}
}

// TestStartProposalsReachableHostsOnly proposes over the controller and host b, while host c answers nothing. The
// stdin the model reads holds the folders and loads of the controller and of b, and does not name c.
func TestStartProposalsReachableHostsOnly(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	bRoot := t.TempDir()
	controllerDir := filepath.Join(root, "dir-a1")
	hostDir := filepath.Join(bRoot, "dir-b1")
	mkdirAt(t, controllerDir)
	mkdirAt(t, hostDir)
	handler, stdin := startProposeServer(t, root, bRoot, "a "+controllerDir+"\n")

	if _, err := propose(t, handler, "example text"); err != nil {
		t.Fatalf("Picker.Propose: %v", err)
	}
	lines := readStdin(t, stdin)
	for _, want := range []string{
		"host a load 25.5% memory 61.25%",
		"host b load 150.5% memory 40.75%",
		controllerDir,
		hostDir,
	} {
		if !slices.Contains(lines, want) {
			t.Errorf("stdin has no line %q", want)
		}
	}
	if slices.ContainsFunc(lines, func(line string) bool { return strings.HasPrefix(line, "host c ") }) {
		t.Errorf("stdin names the unreachable host c")
	}
}

// TestStartProposalsRankedAndFiltered answers a reply that names c, a folder absent on b, then a good folder of b and
// a good folder of the controller. Only the two good proposals survive, in the order the reply gives them.
func TestStartProposalsRankedAndFiltered(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	bRoot := t.TempDir()
	controllerDir := filepath.Join(root, "dir-a1")
	hostDir := filepath.Join(bRoot, "dir-b1")
	mkdirAt(t, controllerDir)
	mkdirAt(t, hostDir)
	reply := strings.Join([]string{
		"c " + controllerDir,
		"b " + filepath.Join(bRoot, "absent"),
		"b " + hostDir,
		"a " + controllerDir,
	}, "\n") + "\n"
	handler, _ := startProposeServer(t, root, bRoot, reply)

	got, err := propose(t, handler, "example text")
	if err != nil {
		t.Fatalf("Picker.Propose: %v", err)
	}
	requireProposals(t, []*orreryv1.StartProposal{
		{Host: routingHost, Dir: hostDir},
		{Host: routingController, Dir: controllerDir},
	}, got)
}

// TestStartProposalsTypedFolderOnChosenHost starts a session on host b in a folder four levels under b's root. An agent
// proposes only its first two levels, so the folder is typed, and the start is forwarded to the engine with host b.
func TestStartProposalsTypedFolderOnChosenHost(t *testing.T) {
	t.Parallel()
	bRoot := t.TempDir()
	deep := filepath.Join(bRoot, "one", "two", "three", "project")
	makeHostFolder(t, deep)
	fake := &fakeEngine{}
	control := controlClient(t, hostStartHandler(
		t, t.TempDir(), newFakeOrreryd(t, fake), startProposeAgent(t, routingHost, bRoot, proposeHostLoad()),
	))

	resp, err := startOnHost(t, control, routingHost, deep)
	if err != nil {
		t.Fatalf("Start on %s: %v", routingHost, err)
	}
	assertOnlyStart(t, fake, resp, routingHost, resolvedPath(t, deep))
}

// TestStartProposalsBlankText proposes blank text, which answers invalid_argument, and runs no model.
func TestStartProposalsBlankText(t *testing.T) {
	t.Parallel()
	handler, stdin := startProposeServer(t, t.TempDir(), t.TempDir(), "")

	_, err := propose(t, handler, " \t ")
	assertConnectError(t, err, connect.CodeInvalidArgument, "bad request")
	if _, statErr := os.Stat(stdin); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("the model ran for blank text: stat stdin: %v", statErr)
	}
}
