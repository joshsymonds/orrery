package stepmcp

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// TestStepByPartToolsListDescriptions fails when the tools/list fixture or the served tools/list lacks one of the
// step_result, step_check and step_list tools, when a served description differs from the fixture's, or when a
// served description does not say what the tool returns and state its bound.
func TestStepByPartToolsListDescriptions(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(toolsListFixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture []listedTool
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	p := newPeerWithEvidence(t, nil, nil, nil)
	reply := p.call("tools/list", map[string]any{})
	var listed struct {
		Tools []listedTool `json:"tools"`
	}
	if err := json.Unmarshal(reply.Result, &listed); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}

	cases := []struct {
		name    string
		phrases []string
	}{
		{
			name:    "step_result",
			phrases: []string{"outline", "field", "item", "4000", "offset to continue from"},
		},
		{
			name: "step_check",
			phrases: []string{
				"detail",
				"at most 4000 runes",
				"default 40, at most 80",
				"150 runes",
				"before to continue from",
			},
		},
		{
			name:    "step_list",
			phrases: []string{"one line each", "20 most recently ended", "at most 4000 bytes"},
		},
	}
	for _, tc := range cases {
		i := slices.IndexFunc(fixture, func(tool listedTool) bool { return tool.Name == tc.name })
		if i < 0 {
			t.Fatalf("tools/list fixture lacks tool %s", tc.name)
		}
		want := fixture[i]
		t.Logf("tools/list fixture carries %s: %s", tc.name, want.Description)

		j := slices.IndexFunc(listed.Tools, func(tool listedTool) bool { return tool.Name == tc.name })
		if j < 0 {
			t.Fatalf("tools/list is missing tool %s", tc.name)
		}
		got := listed.Tools[j]
		if got.Description != want.Description {
			t.Fatalf("tools/list serves %s with a description that differs from the fixture's", tc.name)
		}
		t.Logf("tools/list serves %s with the fixture's description", tc.name)

		for _, phrase := range tc.phrases {
			if !strings.Contains(got.Description, phrase) {
				t.Errorf("%s description does not state %q", tc.name, phrase)
				continue
			}
			t.Logf("%s description states \"%s\"", tc.name, phrase)
		}
	}
}
