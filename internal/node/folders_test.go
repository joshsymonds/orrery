package node

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// startConfiguredAgent serves an Agent configured by cfg on a loopback listener until the test ends, and returns the
// listener's address. Its WhoIs names the agent's own address as its own node, and every other peer as the controller,
// as the other agents of these tests do. Its holders are a fakeHost, which none of these ops calls.
func startConfiguredAgent(t *testing.T, cfg AgentConfig) string {
	t.Helper()
	ln := listenLoopback(t)
	cfg.Controller = testController
	cfg.WhoIs = peerWhoIs{own: ln.Addr().String()}
	cfg.Holders = &fakeHost{}
	agent, err := NewAgent(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- agent.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Error(err)
		}
	})
	return ln.Addr().String()
}

// makeDirs creates each of the directories rel under root, and fails the test when one cannot be made.
func makeDirs(t *testing.T, root string, rel ...string) {
	t.Helper()
	for _, dir := range rel {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

// TestAgentFoldersListsRootDirs asks an agent for the folders of a Root that holds directories at three levels, a hidden
// directory with a child, a hidden child of a folder, a file and a symlink to a folder. The answer lists the directories at
// levels 1 and 2 below Root, sorted by path, and none of the rest.
func TestAgentFoldersListsRootDirs(t *testing.T) {
	root := t.TempDir()
	makeDirs(t, root, "a/x", "a/y", "a/.dot", "b", "deep/l1/l2", ".hidden/z")
	if err := os.WriteFile(filepath.Join(root, "c.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "a"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	addr := startConfiguredAgent(t, AgentConfig{Roots: []string{root}})

	resp := ask(t, addr, Request{Version: Version, Op: OpFolders})
	if resp.Code != "" {
		t.Fatalf("folders: %+v", resp)
	}
	want := []string{
		filepath.Join(root, "a"),
		filepath.Join(root, "a", "x"),
		filepath.Join(root, "a", "y"),
		filepath.Join(root, "b"),
		filepath.Join(root, "deep"),
		filepath.Join(root, "deep", "l1"),
	}
	if !slices.Equal(resp.Folders, want) {
		t.Fatalf("folders = %v, want %v", resp.Folders, want)
	}
}

// TestAgentFoldersWithoutRootRefused asks an agent with no Root for its folders, and checks that it answers CodeFailed.
func TestAgentFoldersWithoutRootRefused(t *testing.T) {
	addr := startConfiguredAgent(t, AgentConfig{})

	resp := ask(t, addr, Request{Version: Version, Op: OpFolders})
	if resp.Code != CodeFailed || resp.Message != "agent has no root" {
		t.Fatalf("folders with no root = %+v, want code %q and message %q", resp, CodeFailed, "agent has no root")
	}
}

// TestAgentFolderResolvesUnderRoot checks the folder op for Root itself, for a directory under Root, and for a symlink
// under Root that leads to a directory under it. Each answers the resolved path, with its symlinks followed.
func TestAgentFolderResolvesUnderRoot(t *testing.T) {
	root := t.TempDir()
	makeDirs(t, root, "a/b")
	if err := os.Symlink(filepath.Join(root, "a", "b"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	addr := startConfiguredAgent(t, AgentConfig{Roots: []string{root}})

	for _, tc := range []struct {
		name, dir, want string
	}{
		{name: "root", dir: root, want: realRoot},
		{name: "folder", dir: filepath.Join(root, "a", "b"), want: filepath.Join(realRoot, "a", "b")},
		{name: "symlink", dir: filepath.Join(root, "alias"), want: filepath.Join(realRoot, "a", "b")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := ask(t, addr, Request{Version: Version, Op: OpFolder, Dir: tc.dir})
			if resp.Code != "" {
				t.Fatalf("folder of %s: %+v", tc.dir, resp)
			}
			if resp.Folder != tc.want {
				t.Fatalf("folder of %s = %q, want %q", tc.dir, resp.Folder, tc.want)
			}
		})
	}
}

// TestAgentFolderRefusesOutsideRoot asks the folder op for a directory outside Root, for a symlink under Root that leads
// out of it, and for a path that is not absolute. Each is answered with CodeFailed and the reason.
func TestAgentFolderRefusesOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	addr := startConfiguredAgent(t, AgentConfig{Roots: []string{root}})

	for _, tc := range []struct {
		name, dir, reason string
	}{
		{name: "outside", dir: outside, reason: "is not under the agent's root"},
		{name: "symlink out", dir: filepath.Join(root, "escape"), reason: "is not under the agent's root"},
		{name: "relative", dir: "a", reason: "is not an absolute path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := ask(t, addr, Request{Version: Version, Op: OpFolder, Dir: tc.dir})
			if resp.Code != CodeFailed || !strings.Contains(resp.Message, tc.reason) {
				t.Fatalf("folder of %s = %+v, want code %q with %q", tc.dir, resp, CodeFailed, tc.reason)
			}
		})
	}
}

// TestAgentFolderRefusesMissing asks the folder op for a path under Root that does not exist, and for a regular file under
// Root. Neither is an existing directory, so each is answered with CodeFailed and the reason.
func TestAgentFolderRefusesMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	addr := startConfiguredAgent(t, AgentConfig{Roots: []string{root}})

	for _, tc := range []struct {
		name, dir, reason string
	}{
		{name: "missing", dir: filepath.Join(root, "missing"), reason: "no such file or directory"},
		{name: "file", dir: filepath.Join(root, "file.txt"), reason: "is not a directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := ask(t, addr, Request{Version: Version, Op: OpFolder, Dir: tc.dir})
			if resp.Code != CodeFailed || !strings.Contains(resp.Message, tc.reason) {
				t.Fatalf("folder of %s = %+v, want code %q with %q", tc.dir, resp, CodeFailed, tc.reason)
			}
		})
	}
}
