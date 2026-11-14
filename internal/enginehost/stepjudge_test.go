package enginehost

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestStepJudgeSchemaIsTwoVerdictObject(t *testing.T) {
	t.Parallel()
	var schema struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			Type string   `json:"type"`
			Enum []string `json:"enum"`
		} `json:"properties"`
		Required             []string `json:"required"`
		AdditionalProperties *bool    `json:"additionalProperties"`
	}
	if err := json.Unmarshal([]byte(stepJudgeSchema), &schema); err != nil {
		t.Fatalf("stepJudgeSchema is not JSON: %v", err)
	}
	if schema.Type != "object" {
		t.Fatalf("root type = %q, want object", schema.Type)
	}
	if len(schema.Properties) != 2 {
		t.Fatalf("properties = %v, want exactly verdict and reason", schema.Properties)
	}
	verdict, ok := schema.Properties["verdict"]
	if !ok {
		t.Fatal("schema has no verdict property")
	}
	wantEnum := []string{stepJudgeVerdictMet, stepJudgeVerdictUnmet}
	if verdict.Type != "string" || !slices.Equal(verdict.Enum, wantEnum) {
		t.Fatalf("verdict = %+v, want string enum [met unmet]", verdict)
	}
	reason, ok := schema.Properties["reason"]
	if !ok || reason.Type != "string" {
		t.Fatalf("reason = %+v, want a string property", reason)
	}
	wantRequired := []string{"verdict", "reason"}
	if !slices.Equal(schema.Required, wantRequired) {
		t.Fatalf("required = %v, want verdict and reason", schema.Required)
	}
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Fatal("schema must set additionalProperties to false")
	}
}

func TestStepJudgeInputCarriesInstructionAndSections(t *testing.T) {
	t.Parallel()
	got := stepJudgeInput("write the parser", "parser written", "parser.go | 12 +", "PASS go test")
	for _, want := range []string{
		"mechanical checks already passed",
		"meet the task's goal",
		"one-line reason",
		"naming what is missing",
		"## Task\nwrite the parser",
		"## Return\nparser written",
		"## Diff stat\nparser.go | 12 +",
		"## Gate output tail\nPASS go test",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("input is missing %q:\n%s", want, got)
		}
	}
}

func TestStepJudgeInputOmitsEmptyDiffStatAndGateTail(t *testing.T) {
	t.Parallel()
	got := stepJudgeInput("answer the question", "42", "  \n", "")
	if strings.Contains(got, "## Diff stat") {
		t.Fatalf("blank diff stat must be omitted:\n%s", got)
	}
	if strings.Contains(got, "## Gate output tail") {
		t.Fatalf("empty gate tail must be omitted:\n%s", got)
	}
	if !strings.Contains(got, "## Task\nanswer the question") || !strings.Contains(got, "## Return\n42") {
		t.Fatalf("task and return sections must stay:\n%s", got)
	}
}

func TestStepJudgeInputCutsOversizedSectionsAtEightKiB(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("a", 3*stepJudgeSectionBytes)
	got := stepJudgeInput(big, big, big, big)
	for _, heading := range []string{"## Task\n", "## Return\n", "## Diff stat\n", "## Gate output tail\n"} {
		start := strings.Index(got, heading)
		if start < 0 {
			t.Fatalf("missing section %q", heading)
		}
		body := got[start+len(heading):]
		if next := strings.Index(body, "\n\n## "); next >= 0 {
			body = body[:next]
		}
		if len(body) != stepJudgeSectionBytes {
			t.Fatalf("section %q is %d bytes, want %d", heading, len(body), stepJudgeSectionBytes)
		}
	}
}

func TestStepJudgeInputCutsOnRuneBoundary(t *testing.T) {
	t.Parallel()
	// A three-byte rune straddling the limit must not be split into invalid UTF-8.
	big := strings.Repeat("a", stepJudgeSectionBytes-1) + "\u4e16\u4e16"
	got := stepJudgeInput("task", big, "", "")
	body := got[strings.Index(got, "## Return\n")+len("## Return\n"):]
	if strings.ContainsRune(body, '\uFFFD') {
		t.Fatal("cut produced invalid UTF-8")
	}
	if body != strings.Repeat("a", stepJudgeSectionBytes-1) {
		t.Fatalf("cut kept %d bytes, want the %d whole bytes before the split rune", len(body), stepJudgeSectionBytes-1)
	}
}

func TestStepJudgeInputKeepsTheTailOfTheGateOutput(t *testing.T) {
	t.Parallel()
	gate := strings.Repeat("x", stepJudgeSectionBytes) + "FAILED: last line"
	got := stepJudgeInput("task", "return", "", gate)
	body := got[strings.Index(got, "## Gate output tail\n")+len("## Gate output tail\n"):]
	if !strings.HasSuffix(body, "FAILED: last line") {
		t.Fatalf("gate tail lost its last line: %q", body[len(body)-40:])
	}
	if len(body) > stepJudgeSectionBytes {
		t.Fatalf("gate tail is %d bytes, want at most %d", len(body), stepJudgeSectionBytes)
	}
}

func TestParseStepJudgeMet(t *testing.T) {
	t.Parallel()
	met, reason, err := parseStepJudge(json.RawMessage(`{"verdict":"met","reason":"  all three files written  "}`))
	if err != nil {
		t.Fatalf("parseStepJudge: %v", err)
	}
	if !met || reason != "all three files written" {
		t.Fatalf("got met=%v reason=%q, want met=true reason trimmed", met, reason)
	}
}

func TestParseStepJudgeUnmet(t *testing.T) {
	t.Parallel()
	met, reason, err := parseStepJudge(json.RawMessage(`{"verdict":"unmet","reason":"the tests are missing"}`))
	if err != nil {
		t.Fatalf("parseStepJudge: %v", err)
	}
	if met || reason != "the tests are missing" {
		t.Fatalf("got met=%v reason=%q, want met=false", met, reason)
	}
}

func TestParseStepJudgeRejectsBadReplies(t *testing.T) {
	for name, raw := range map[string]string{
		"not json":           `{"verdict":`,
		"verdict not enum":   `{"verdict":"maybe","reason":"unsure"}`,
		"verdict missing":    `{"reason":"no verdict"}`,
		"verdict wrong case": `{"verdict":"MET","reason":"case"}`,
		"reason empty":       `{"verdict":"met","reason":""}`,
		"reason blank":       `{"verdict":"unmet","reason":"   \n\t "}`,
		"reason missing":     `{"verdict":"unmet"}`,
		"empty input":        ``,
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := parseStepJudge(json.RawMessage(raw))
			if err == nil {
				t.Fatalf("parseStepJudge(%s) returned no error", raw)
			}
		})
	}
}

// A section that is one long run of UTF-8 continuation bytes, as raw gate output can be, is cut without a panic and
// stays within the section limit.
func TestStepJudgeInputCutsInvalidUTF8WithoutPanic(t *testing.T) {
	t.Parallel()
	junk := strings.Repeat("\x80", stepJudgeSectionBytes+808)
	got := stepJudgeInput(junk, "return", "", junk)
	task := got[strings.Index(got, "## Task\n")+len("## Task\n") : strings.Index(got, "\n\n## Return")]
	if len(task) > stepJudgeSectionBytes {
		t.Fatalf("task section is %d bytes, want at most %d", len(task), stepJudgeSectionBytes)
	}
	gate := got[strings.Index(got, "## Gate output tail\n")+len("## Gate output tail\n"):]
	if len(gate) > stepJudgeSectionBytes {
		t.Fatalf("gate tail is %d bytes, want at most %d", len(gate), stepJudgeSectionBytes)
	}
}
