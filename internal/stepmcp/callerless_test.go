package stepmcp

import (
	"strings"
	"testing"
)

// TestCallerlessStartMCPRefused calls step_start over MCP through a client that stamps no caller. The daemon refuses
// the start with the reason it is refused for, so the tool errors with that reason, and the daemon's steps start
// nothing.
func TestCallerlessStartMCPRefused(t *testing.T) {
	fake := &fakeSteps{startID: "0123456789abcdef0123456789abcdef"}
	p := newPeer(t, startDaemonWithEnv(t, fake, func(string) string { return "" }), nil)

	outcome := p.tool("step_start", `{"prompt":"check the change","workspace":"/work"}`)

	if !outcome.isError {
		t.Fatalf("step_start = %q, want a tool error for a start with no caller", outcome.text)
	}
	if !strings.Contains(outcome.text, "nowhere to go") {
		t.Fatalf("step_start error = %q, want it to name the reason, nowhere to go", outcome.text)
	}
	if got := fake.calls().starts; len(got) != 0 {
		t.Fatalf("refused step_start reached the daemon as %+v, want no start", got)
	}
}
