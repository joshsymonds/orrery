package agents

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/hostexec/hostexectest"
)

// recorded reports whether calls holds a call to op that named path.
func recorded(calls []hostexectest.Call, op, path string) bool {
	for _, call := range calls {
		if call.Op == op && call.Path == path {
			return true
		}
	}
	return false
}

func TestResolveThroughHost(t *testing.T) {
	root, where := setUp(t, map[string]string{
		"home/proj/.git/HEAD":           gitHead,
		"home/proj/.claude/agents/p.md": agentMD("p"),
	}, place{dir: "home/proj", home: "home"})
	recorder := hostexectest.NewRecorder(hostexec.NewLocal())
	where.Files = recorder

	if err := Resolve(context.Background(), "p", where); err != nil {
		t.Fatalf("Resolve(p) = %v, want nil", err)
	}
	folder := filepath.Join(root, "home/proj/.claude/agents")
	file := filepath.Join(folder, "p.md")
	calls := recorder.Calls()
	if !recorded(calls, "ReadDir", folder) {
		t.Errorf("Resolve(p) made no ReadDir of the agent folder %s through the host; calls = %+v", folder, calls)
	}
	if !recorded(calls, "ReadFile", file) {
		t.Errorf("Resolve(p) made no ReadFile of the agent file %s through the host; calls = %+v", file, calls)
	}
}
