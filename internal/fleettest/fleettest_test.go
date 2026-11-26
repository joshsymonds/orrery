package fleettest

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

func TestFleetGitRunsOnEachAgent(t *testing.T) {
	f := Start(t, "a", "b")
	repo := filepath.Join(f.Roots["b"], "repo")
	if err := f.Hosts["b"].MkdirAll(t.Context(), repo, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Hosts["b"].Git(t.Context(), hostexec.GitCall{Dir: repo, Args: []string{"init", "-q"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Errorf("git init through b made no repository under b's root: %v", err)
	}
	entries, err := os.ReadDir(f.Roots["a"])
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("git init through b left %d entries under a's root, want none", len(entries))
	}
}

func TestFleetReachFollowsStoppedAgent(t *testing.T) {
	f := Start(t, "a")
	if _, err := f.Clients["a"].Sessions(t.Context()); err != nil {
		t.Fatalf("agent a does not answer a call before it stops: %v", err)
	}
	if reach, _ := f.Clients["a"].Reach(); !reach {
		t.Fatal("a client reads unreachable while its agent answers")
	}
	f.StopAgent(t, "a")
	addr := f.agents["a"].listener.Addr().String()
	if conn, err := net.Dial("tcp", addr); err == nil {
		_ = conn.Close()
		t.Fatalf("agent a still accepts connections at %s after it stops", addr)
	}
	f.WaitReach(t, "a", false)
}

func TestFleetOriginTakesCommits(t *testing.T) {
	f := Start(t)
	id := f.PushCommit(t, "second commit")
	if main := runGit(t, f.Origin, "rev-parse", "refs/heads/main"); main != id {
		t.Errorf("main of Origin is %s, want the commit PushCommit returned, %s", main, id)
	}
	if subject := runGit(t, f.Origin, "log", "-1", "--format=%s", id); subject != "second commit" {
		t.Errorf("the commit %s of Origin has subject %q, want %q", id, subject, "second commit")
	}
}
