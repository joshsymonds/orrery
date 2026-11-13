package gates

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

// verdictGate is a synthetic agent gate: two musts, then one must not, with a 90 second limit.
func verdictGate() Gate {
	return Gate{
		Name:  "alpha",
		Agent: "reviewer",
		Limit: 90 * time.Second,
		Assertions: []Assertion{
			{Text: "first", Kind: KindMust},
			{Text: "second", Kind: KindMust},
			{Text: "third", Kind: KindMustNot},
		},
	}
}

// inOrder fails the test unless each part appears in s after the part before it.
func inOrder(t *testing.T, s string, parts ...string) {
	t.Helper()
	at := 0
	for _, part := range parts {
		i := strings.Index(s[at:], part)
		if i < 0 {
			t.Fatalf("prompt lacks %q after offset %d:\n%s", part, at, s)
		}
		at += i + len(part)
	}
}

func TestVerdictSchemaResultsRequireAssertionKindResultAndEvidence(t *testing.T) {
	var schema struct {
		Properties struct {
			Results struct {
				Items struct {
					Required []string `json:"required"`
				} `json:"items"`
			} `json:"results"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(VerdictSchema), &schema); err != nil {
		t.Fatalf("VerdictSchema is not JSON: %v", err)
	}
	required := schema.Properties.Results.Items.Required
	for _, field := range []string{"assertion", "kind", "result", "evidence"} {
		if !slices.Contains(required, field) {
			t.Errorf("results items do not require %q; required = %v", field, required)
		}
	}
}

func TestPromptShowsInstructionGateLimitAssertionsThenCaller(t *testing.T) {
	got := Prompt(verdictGate(), "CALLER PROMPT")
	if !strings.HasPrefix(got, verdictInstruction) {
		t.Errorf("prompt does not open with the instruction:\n%s", got)
	}
	inOrder(t, got,
		verdictInstruction,
		"Gate: alpha",
		"Limit: 90 seconds",
		"1. must: first",
		"2. must: second",
		"3. must not: third",
		"CALLER PROMPT",
	)
	if !strings.HasSuffix(got, "CALLER PROMPT") {
		t.Errorf("prompt does not end with the caller's prompt:\n%s", got)
	}
}

func TestPromptWithEmptyCallerEndsWithAssertions(t *testing.T) {
	got := Prompt(verdictGate(), "")
	if !strings.HasSuffix(got, "3. must not: third") {
		t.Errorf("prompt does not end with the last assertion:\n%q", got)
	}
}

func TestVerdictInstructionCarriesJudgingRules(t *testing.T) {
	for _, term := range []string{"holds", "fails", "undetermined", "citation", "observations", "missing_evidence", "edit", "blocked", "test_runner_requests"} {
		if !strings.Contains(verdictInstruction, term) {
			t.Errorf("instruction does not mention %q", term)
		}
	}
}
