package integration_test

import (
	"os/exec"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/waittest"
)

// TestTreePIDsMatchOnlyTheTestsServer forces the order that killed another test's tree: a tree for a pts runs on one
// tmux server while a test on another server, whose client had the same pts number, looks for its trees, as its cleanup
// does before it kills them. The stand-in tree is a shell whose command line ends in tree --client and the pts, waiting
// on its stdin, with TMUX naming the other server's socket. Only a lookup on that socket may find it.
func TestTreePIDsMatchOnlyTheTestsServer(t *testing.T) {
	t.Parallel()
	client, _ := newServer(t)
	startPane(t, client, "sleep 300")
	own := serverSocket(t, client)
	other := own + "-other"
	const tty = "/dev/pts/999"

	tree := exec.Command("sh", "-c", "read -r line", "tree", "--client", tty)
	tree.Env = []string{"TMUX=" + other + ",1,0"}
	stdin, err := tree.StdinPipe()
	if err != nil {
		t.Fatalf("stand-in tree stdin: %v", err)
	}
	if err = tree.Start(); err != nil {
		t.Fatalf("start the stand-in tree: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = tree.Wait()
	})
	waittest.Until(t, nil, "the stand-in tree on the other server", func() bool {
		pids, lookErr := treePIDs(other, tty)
		return lookErr == nil && slices.Contains(pids, tree.Process.Pid)
	})
	pids, err := treePIDs(own, tty)
	if err != nil {
		t.Fatalf("find trees on the test's server: %v", err)
	}
	if slices.Contains(pids, tree.Process.Pid) {
		t.Fatalf("trees for %s on the test's server %s = %v, which holds the other server's tree %d",
			tty, own, pids, tree.Process.Pid)
	}
}
