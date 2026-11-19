package stepmcp

import (
	"encoding/json"
	"os"
	"testing"
)

// TestNoStepWaitToolsList checks that no step_wait tool is served: tools/list names none, and the tools/list fixture
// names none either.
func TestNoStepWaitToolsList(t *testing.T) {
	t.Parallel()
	p := newPeerWithEvidence(t, nil, nil, nil)
	reply := p.call("tools/list", map[string]any{})
	var listed struct {
		Tools []listedTool `json:"tools"`
	}
	if err := json.Unmarshal(reply.Result, &listed); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "step_wait" {
			t.Errorf("tools/list names step_wait")
		}
	}

	raw, err := os.ReadFile(toolsListFixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture []listedTool
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	for _, tool := range fixture {
		if tool.Name == "step_wait" {
			t.Errorf("fixture %s names step_wait", toolsListFixture)
		}
	}
}
