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
		"lost only its middle, which does not mean the return was cut off",
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
	// Each third is 8 KiB, so the head, middle, and tail of the input are told apart in what each cut keeps.
	k := stepJudgeSectionBytes
	big := strings.Repeat("h", k) + strings.Repeat("m", k) + strings.Repeat("t", k)
	got := stepJudgeInput(big, big, big, big)
	// The task and the return keep 8 KiB of head and 8 KiB of tail around the 8192 bytes left out.
	ends := strings.Repeat("h", k) + "\n[8192 bytes left out]\n" + strings.Repeat("t", k)
	for _, tc := range []struct{ heading, want string }{
		{"## Task\n", ends},
		{"## Return\n", ends},
		{"## Diff stat\n", strings.Repeat("h", k)},
		{"## Gate output tail\n", strings.Repeat("t", k)},
	} {
		start := strings.Index(got, tc.heading)
		if start < 0 {
			t.Fatalf("missing section %q", tc.heading)
		}
		body := got[start+len(tc.heading):]
		if next := strings.Index(body, "\n\n## "); next >= 0 {
			body = body[:next]
		}
		if body != tc.want {
			t.Fatalf("section %q is %d bytes, want %d bytes of the expected cut", tc.heading, len(body), len(tc.want))
		}
	}
}

func TestStepJudgeInputKeepsEndsAndOmissionOfLongReturn(t *testing.T) {
	t.Parallel()
	// A 20 KiB return keeps its first and last 8 KiB around a line naming the 4104 bytes that fall between them.
	long := strings.Repeat("a", 20*1024) + "END-MARK"
	got := stepJudgeInput("task", long, "", "")
	ends := strings.Repeat("a", stepJudgeSectionBytes) + "\n[4104 bytes left out]\n" +
		strings.Repeat("a", stepJudgeSectionBytes-len("END-MARK")) + "END-MARK"
	if !strings.HasSuffix(got, "## Return\n"+ends) {
		t.Fatalf("20 KiB return is not its head, omission line, and end marker:\n%q", got[len(got)-200:])
	}
	// A return of exactly 16 KiB is not cut.
	whole := strings.Repeat("b", 2*stepJudgeSectionBytes)
	if got := stepJudgeInput("task", whole, "", ""); !strings.HasSuffix(got, "## Return\n"+whole) {
		t.Fatal("16 KiB return was cut")
	}
}

func TestStepJudgeInputCutsOnRuneBoundary(t *testing.T) {
	t.Parallel()
	// A three-byte rune straddling a cut must not be split into invalid UTF-8. Each section is just over 16 KiB, with
	// the rune placed on the head cut in one case and on the tail cut in the other.
	k := stepJudgeSectionBytes
	headCut := strings.Repeat("a", k-1) + "\u4e16" + strings.Repeat("b", k)
	tailCut := strings.Repeat("a", k) + "\u4e16" + strings.Repeat("c", k-2)
	for name, tc := range map[string]struct{ text, want string }{
		"head cut": {headCut, strings.Repeat("a", k-1) + "\n[3 bytes left out]\n" + strings.Repeat("b", k)},
		"tail cut": {tailCut, strings.Repeat("a", k) + "\n[3 bytes left out]\n" + strings.Repeat("c", k-2)},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := stepJudgeInput("task", tc.text, "", "")
			body := got[strings.Index(got, "## Return\n")+len("## Return\n"):]
			if strings.ContainsRune(body, '\uFFFD') {
				t.Fatal("cut produced invalid UTF-8")
			}
			if body != tc.want {
				t.Fatalf("cut kept %d bytes, want the whole bytes on each side of the split rune", len(body))
			}
		})
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
	junk := strings.Repeat("\x80", 2*stepJudgeSectionBytes+808)
	got := stepJudgeInput(junk, "return", "", junk)
	task := got[strings.Index(got, "## Task\n")+len("## Task\n") : strings.Index(got, "\n\n## Return")]
	if len(task) > 2*stepJudgeSectionBytes {
		t.Fatalf("task section is %d bytes, want at most %d", len(task), 2*stepJudgeSectionBytes)
	}
	gate := got[strings.Index(got, "## Gate output tail\n")+len("## Gate output tail\n"):]
	if len(gate) > stepJudgeSectionBytes {
		t.Fatalf("gate tail is %d bytes, want at most %d", len(gate), stepJudgeSectionBytes)
	}
}
