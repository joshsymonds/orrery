package stepmcp

import (
	"strings"
	"testing"
)

// TestDecisionMakerEpicToolDescriptions fails when an epic tool's description or input schema calls the caller's text or
// decision the owner's. The caller may be the conversation that steers the epic, not the owner, so the tools name the
// caller as the maker.
func TestDecisionMakerEpicToolDescriptions(t *testing.T) {
	t.Parallel()
	tools, err := epicTools(nil)
	if err != nil {
		t.Fatalf("epicTools: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("epicTools returns no tools")
	}
	phrases := []string{"owner's words", "owner's word", "owner's decision"}
	for _, tool := range tools {
		for _, phrase := range phrases {
			if strings.Contains(tool.Description, phrase) {
				t.Errorf("tool %s description contains %q", tool.Name, phrase)
			}
			if strings.Contains(string(tool.InputSchema), phrase) {
				t.Errorf("tool %s input schema contains %q", tool.Name, phrase)
			}
		}
	}
}
