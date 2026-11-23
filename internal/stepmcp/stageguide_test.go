package stepmcp

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/contracttext"
)

// stageToolNames are the contract stage's nine tools: the ones the instructions name, each of which tools/list lists.
var stageToolNames = []string{
	"design_guide", "design_research", "design_reproduce", "design_stage", "design_ask", "design_answer",
	"design_steelman", "design_dispose", "contract_draft",
}

// stagePassage is one passage of the contract stage's rules, with the phrases that show the passage is stated.
type stagePassage struct {
	name    string
	phrases []string
}

// stageGuideSections are the contract's sections in the order contracttext.Render lays them out, each named by its
// heading in the guide.
var stageGuideSections = []string{
	"The title and its Approved line.",
	"What you asked for.",
	"What could go wrong, and how much we care",
	"Things you did not ask for",
	"What will be true when done",
	"What I'm assuming",
	"What we won't do",
	"How, and why not the other ways",
	"What leaves this machine or can't be undone",
	"Decisions I need from you",
	"Checks the machines run",
}

// listedToolNames returns the names tools/list lists on p. It fails the test when the reply is an error.
func listedToolNames(t *testing.T, p *peer) []string {
	t.Helper()
	reply := p.call("tools/list", map[string]any{})
	if reply.Error != nil {
		t.Fatalf("tools/list: JSON-RPC error %d: %s", reply.Error.Code, reply.Error.Message)
	}
	var listed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(reply.Result, &listed); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// TestStageGuideInstructionsNameTheStageAndItsTools checks that the initialize reply's instructions name the contract
// stage and each of its nine tools, and that tools/list lists each of those tools.
func TestStageGuideInstructionsNameTheStageAndItsTools(t *testing.T) {
	t.Parallel()
	server, err := NewServer(nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	p, _ := servePipes(t, server)

	reply := p.call("initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "test", "version": "1"},
	})
	if reply.Error != nil {
		t.Fatalf("initialize: JSON-RPC error %d: %s", reply.Error.Code, reply.Error.Message)
	}
	var result struct {
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal(reply.Result, &result); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	p.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})

	if !strings.Contains(result.Instructions, "contract stage") {
		t.Errorf("instructions do not name the contract stage")
	}
	listed := listedToolNames(t, p)
	for _, name := range stageToolNames {
		if !strings.Contains(result.Instructions, name) {
			t.Errorf("instructions do not name %s", name)
		}
		if !slices.Contains(listed, name) {
			t.Errorf("tools/list does not list %s", name)
		}
	}
}

// TestStageGuideDesignGuideStatesTheRules checks that design_guide's text states the research, the questions with
// their recommendations, the steelman's budget, the dispositions and the contract's sections in their order, and that
// it carries contracttext.QualityBar verbatim.
func TestStageGuideDesignGuideStatesTheRules(t *testing.T) {
	t.Parallel()
	server, err := NewServer(nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	p, _ := servePipes(t, server)
	initialize(p)

	outcome := p.tool("design_guide", "{}")
	if outcome.isError {
		t.Fatalf("design_guide: tool error %q", outcome.text)
	}
	guide := outcome.text

	for _, passage := range []stagePassage{
		{name: "research", phrases: []string{
			"design_research asks one question",
			"design_reproduce runs a command",
			"design_stage reads a design's stage",
		}},
		{name: "questions with recommendations", phrases: []string{
			"design_ask stores a question",
			"a recommendation that is one of the options",
			"owner_serve",
			"owner_conversation",
			"conversation: the conversation decides for the owner",
			"accept: true answers with the question's recommendation",
		}},
		{name: "steelman budget", phrases: []string{
			"design_steelman runs one steelman pass",
			"one counted discovery pass and at most one counted closure pass",
			"A BLOCKED return, or a step that ends with no return, does not count",
			"the design is refused",
		}},
		{name: "dispositions", phrases: []string{
			"- adopted;",
			"- rejected, with a reason;",
			"- deferred, with a boundary the item is deferred to;",
			"- open.",
			"The ledger freezes at closure",
			"- revised_after_closure, with a reason",
			"- accepted_as_risk, with a reason",
		}},
	} {
		for _, phrase := range passage.phrases {
			if !strings.Contains(guide, phrase) {
				t.Errorf("%s: design_guide does not state %q", passage.name, phrase)
			}
		}
	}

	if !strings.Contains(guide, contracttext.QualityBar) {
		t.Errorf("design_guide does not carry the Quality Bar verbatim")
	}

	last := -1
	for _, section := range stageGuideSections {
		at := strings.Index(guide, section)
		switch {
		case at < 0:
			t.Errorf("contract sections: design_guide does not name %q", section)
		case at < last:
			t.Errorf("contract sections: %q comes before the section that precedes it", section)
		default:
			last = at
		}
	}
}
