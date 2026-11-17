package reviewers

import (
	"encoding/json"
	"slices"
	"testing"
)

// TestDefaultReviewerToolsCompiled asserts each default reviewer's compiled agent runs under exactly Read, Bash and
// StructuredOutput: the tools the defaults name, with StructuredOutput added because a review step returns its verdict
// through it. Grep and Glob are absent from the list.
func TestDefaultReviewerToolsCompiled(t *testing.T) {
	t.Parallel()
	want := []string{"Read", "Bash", "StructuredOutput"}
	for _, r := range mergedDefaults(t) {
		agents, err := r.Agents()
		if err != nil {
			t.Fatalf("%s: Agents: %v", r.Name, err)
		}
		var decoded map[string]struct {
			Tools []string `json:"tools"`
		}
		if err := json.Unmarshal([]byte(agents), &decoded); err != nil {
			t.Fatalf("%s: Agents() = %q is not JSON: %v", r.Name, agents, err)
		}
		def, ok := decoded[r.AgentName()]
		if !ok {
			t.Fatalf("%s: Agents() has no agent %q", r.Name, r.AgentName())
		}
		if !slices.Equal(def.Tools, want) {
			t.Errorf("%s: compiled tools = %q, want %q", r.Name, def.Tools, want)
		}
	}
}
