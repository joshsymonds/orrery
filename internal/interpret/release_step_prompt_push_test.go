package interpret

import (
	"strings"
	"testing"
)

// TestReleaseStepPromptPush: a release step's prompt tells the step that a push it makes which is refused returns
// blocked, and that the step never merges, rebases or forces to get past the refusal.
func TestReleaseStepPromptPush(t *testing.T) {
	t.Parallel()
	prompt := releaseStepPrompt(tagStep(), "0123abcd", releaseThen)
	for _, want := range []string{"A refused push returns blocked", "never merges, rebases or forces"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt = %q, want it to hold %q", prompt, want)
		}
	}
}
