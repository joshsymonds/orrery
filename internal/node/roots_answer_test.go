package node

import (
	"path/filepath"
	"slices"
	"testing"
)

// TestRepoOnHostAgentAnswersRoots asks an agent configured with two Roots, in an order that is not sorted, for its
// folders. The answer carries the Roots in the order they are configured, so a controller can find a checkout under
// each of them.
func TestRepoOnHostAgentAnswersRoots(t *testing.T) {
	base := t.TempDir()
	makeDirs(t, base, "zeta", "alpha")
	roots := []string{filepath.Join(base, "zeta"), filepath.Join(base, "alpha")}
	addr := startConfiguredAgent(t, AgentConfig{Roots: roots})

	resp := ask(t, addr, Request{Version: Version, Secret: testSecret, Op: OpFolders})
	if resp.Code != "" {
		t.Fatalf("folders: %+v", resp)
	}
	if !slices.Equal(resp.Roots, roots) {
		t.Fatalf("roots = %v, want %v", resp.Roots, roots)
	}
}
