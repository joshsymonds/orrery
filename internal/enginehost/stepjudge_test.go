package enginehost

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/haikuprompt"
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

func TestStepJudgeNeutralInstruction(t *testing.T) {
	t.Parallel()
	t.Run("answers unmet only for undone work or a reported failure", func(t *testing.T) {
		t.Parallel()
		want := "Answer unmet only when the return says part of the task is not done or reports a failure."
		if !strings.Contains(stepJudgeInstruction, want) {
			t.Fatalf("instruction does not confine unmet to undone work or failure:\n%s", stepJudgeInstruction)
		}
	})
	t.Run("omitted proof output or counts are not unmet", func(t *testing.T) {
		t.Parallel()
		want := "A return that omits proof, output or counts is not unmet."
		if !strings.Contains(stepJudgeInstruction, want) {
			t.Fatalf(
				"instruction does not say omitted proof, output or counts are not unmet:\n%s",
				stepJudgeInstruction,
			)
		}
	})
	t.Run("never asks for a change the task does not name", func(t *testing.T) {
		t.Parallel()
		want := "Never ask for a change the task does not name."
		if !strings.Contains(stepJudgeInstruction, want) {
			t.Fatalf("instruction does not forbid asking for changes the task does not name:\n%s", stepJudgeInstruction)
		}
	})
	t.Run("says the task, not the Deliverable", func(t *testing.T) {
		t.Parallel()
		if !strings.Contains(stepJudgeInstruction, "the task") {
			t.Fatalf("instruction does not name the task:\n%s", stepJudgeInstruction)
		}
		if strings.Contains(strings.ToLower(stepJudgeInstruction), "deliverable") {
			t.Fatalf("instruction names the Deliverable, but judges every kind of step:\n%s", stepJudgeInstruction)
		}
	})
}

func TestStepJudgeInputCarriesInstructionAndSections(t *testing.T) {
	t.Parallel()
	got := stepJudgeInput("write the parser", "parser written", "parser.go | 12 +", "PASS go test", "warn: slow")
	for _, want := range []string{
		"mechanical checks already passed",
		"one-line reason",
		"naming what is missing",
		"lost only its middle, which does not mean the return was cut off",
		"<task>\nwrite the parser\n</task>",
		"<return>\nparser written\n</return>",
		"<diff_stat>\nparser.go | 12 +\n</diff_stat>",
		"<gate_stdout_tail>\nPASS go test\n</gate_stdout_tail>",
		"<gate_stderr_tail>\nwarn: slow\n</gate_stderr_tail>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("input is missing %q:\n%s", want, got)
		}
	}
	if !strings.HasSuffix(got, stepJudgeInstruction+"\n\n"+haikuprompt.Answer) {
		t.Fatalf("input does not end with the instruction and the answer line:\n%s", got)
	}
}

func TestStepJudgeInputOmitsEmptyDiffStatAndGateTail(t *testing.T) {
	t.Parallel()
	got := stepJudgeInput("answer the question", "42", "  \n", "", " \n")
	if strings.Contains(got, "<diff_stat>") {
		t.Fatalf("blank diff stat must be omitted:\n%s", got)
	}
	if strings.Contains(got, "<gate_stdout_tail>") || strings.Contains(got, "<gate_stderr_tail>") {
		t.Fatalf("blank gate tails must be omitted:\n%s", got)
	}
	if stepJudgeSection(t, got, "task") != "answer the question" || stepJudgeSection(t, got, "return") != "42" {
		t.Fatalf("task and return sections must stay:\n%s", got)
	}
}

func TestStepJudgeInputCutsOversizedSectionsAtEightKiB(t *testing.T) {
	t.Parallel()
	// Each third is 8 KiB, so the head, middle, and tail of the input are told apart in what each cut keeps.
	k := stepJudgeSectionBytes
	big := strings.Repeat("h", k) + strings.Repeat("m", k) + strings.Repeat("t", k)
	got := stepJudgeInput(big, big, big, big, big)
	// The task and the return keep 8 KiB of head and 8 KiB of tail around the 8192 bytes left out.
	ends := strings.Repeat("h", k) + "\n[8192 bytes left out]\n" + strings.Repeat("t", k)
	for _, tc := range []struct{ tag, want string }{
		{"task", ends},
		{"return", ends},
		{"diff_stat", strings.Repeat("h", k)},
		{"gate_stdout_tail", strings.Repeat("t", k)},
		{"gate_stderr_tail", strings.Repeat("t", k)},
	} {
		if body := stepJudgeSection(t, got, tc.tag); body != tc.want {
			t.Fatalf("part %q is %d bytes, want %d bytes of the expected cut", tc.tag, len(body), len(tc.want))
		}
	}
}

func TestStepJudgeInputKeepsEndsAndOmissionOfLongReturn(t *testing.T) {
	t.Parallel()
	// A 20 KiB return keeps its first and last 8 KiB around a line naming the 4104 bytes that fall between them.
	long := strings.Repeat("a", 20*1024) + "END-MARK"
	got := stepJudgeInput("task", long, "", "", "")
	ends := strings.Repeat("a", stepJudgeSectionBytes) + "\n[4104 bytes left out]\n" +
		strings.Repeat("a", stepJudgeSectionBytes-len("END-MARK")) + "END-MARK"
	if body := stepJudgeSection(t, got, "return"); body != ends {
		t.Fatalf("20 KiB return is not its head, omission line, and end marker:\n%q", body[len(body)-200:])
	}
	// A return of exactly 16 KiB is not cut.
	whole := strings.Repeat("b", 2*stepJudgeSectionBytes)
	if got := stepJudgeInput("task", whole, "", "", ""); stepJudgeSection(t, got, "return") != whole {
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
			got := stepJudgeInput("task", tc.text, "", "", "")
			body := stepJudgeSection(t, got, "return")
			if strings.ContainsRune(body, '\uFFFD') {
				t.Fatal("cut produced invalid UTF-8")
			}
			if body != tc.want {
				t.Fatalf("cut kept %d bytes, want the whole bytes on each side of the split rune", len(body))
			}
		})
	}
}

// stepJudgeSection returns the text of the part of input tagged tag, between its opening and closing tags.
func stepJudgeSection(t *testing.T, input, tag string) string {
	t.Helper()
	open, closing := "<"+tag+">\n", "\n</"+tag+">"
	start := strings.Index(input, open)
	if start < 0 {
		t.Fatalf("input has no part %q:\n%s", tag, input)
	}
	body := input[start+len(open):]
	end := strings.Index(body, closing)
	if end < 0 {
		t.Fatalf("part %q of input is not closed:\n%s", tag, input)
	}
	return body[:end]
}

func TestStepJudgeInputKeepsTheTailOfEachGateStream(t *testing.T) {
	t.Parallel()
	stream := strings.Repeat("x", stepJudgeSectionBytes) + "FAILED: last line"
	got := stepJudgeInput("task", "return", "", stream, stream)
	for _, heading := range []string{"gate_stdout_tail", "gate_stderr_tail"} {
		body := stepJudgeSection(t, got, heading)
		if !strings.HasSuffix(body, "FAILED: last line") {
			t.Fatalf("%q lost its last line: %q", heading, body[len(body)-40:])
		}
		if len(body) > stepJudgeSectionBytes {
			t.Fatalf("%q is %d bytes, want at most %d", heading, len(body), stepJudgeSectionBytes)
		}
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
	t.Parallel()
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
			t.Parallel()
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
	got := stepJudgeInput(junk, "return", "", junk, junk)
	task := stepJudgeSection(t, got, "task")
	if len(task) > 2*stepJudgeSectionBytes {
		t.Fatalf("task section is %d bytes, want at most %d", len(task), 2*stepJudgeSectionBytes)
	}
	for _, heading := range []string{"gate_stdout_tail", "gate_stderr_tail"} {
		gate := stepJudgeSection(t, got, heading)
		if len(gate) > stepJudgeSectionBytes {
			t.Fatalf("%q is %d bytes, want at most %d", heading, len(gate), stepJudgeSectionBytes)
		}
	}
}
