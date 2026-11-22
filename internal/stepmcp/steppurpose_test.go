package stepmcp

import "testing"

// TestStepPurposeStepStartPassesThePurposeItIsGiven checks that the purpose step_start is called with reaches the
// daemon's start as given, through the Steps service.
func TestStepPurposeStepStartPassesThePurposeItIsGiven(t *testing.T) {
	t.Parallel()
	fake := &fakeSteps{startID: "0123456789abcdef0123456789abcdef"}
	p := newPeer(t, startDaemon(t, fake), nil)

	outcome := p.tool("step_start", `{
		"prompt": "check the change",
		"workspace": "/work",
		"purpose": "Check the change against the brief."
	}`)

	if outcome.isError {
		t.Fatalf("step_start reported an error: %s", outcome.text)
	}
	starts := fake.calls().starts
	if len(starts) != 1 {
		t.Fatalf("step_start reached the daemon %d times, want once", len(starts))
	}
	if got := starts[0].Purpose; got != "Check the change against the brief." {
		t.Fatalf("step_start reached the daemon with purpose %q, want the one it was given", got)
	}
}
