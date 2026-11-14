package enginehost

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/wire"
)

// userLine is one user prompt line as the engine host writes it to claude's stdin.
func userLine(text string) string {
	return `{"type":"user","message":{"role":"user","content":"` + text + `"}}` + "\n"
}

// fakeClaudeRun runs the scripted fake claude in a fresh directory with input on stdin. The fake exits at stdin EOF,
// so the run ends once input is consumed. It returns the directory and the lines the fake wrote to stdout.
func fakeClaudeRun(t *testing.T, input string) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	return dir, fakeClaudeRunIn(t, dir, input)
}

// fakeClaudeRunIn runs the scripted fake claude in dir, which the caller has prepared, with input on stdin. It returns
// the lines the fake wrote to stdout.
func fakeClaudeRunIn(t *testing.T, dir, input string) []string {
	t.Helper()
	cmd := exec.Command("/bin/sh", fakeClaudePath(t))
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run fake claude: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
}

// decodeVariants decodes each stdout line through the wire package, the decoder the engine applies to claude's output,
// and returns the variants in line order.
func decodeVariants(t *testing.T, lines []string) []any {
	t.Helper()
	variants := make([]any, 0, len(lines))
	for _, line := range lines {
		var msg wire.SDKMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("stdout line is not an SDKMessage: %v\n%s", err, line)
		}
		variant, err := msg.Variant()
		if err != nil {
			t.Fatalf("stdout line does not decode: %v\n%s", err, line)
		}
		variants = append(variants, variant)
	}
	return variants
}

// findVariant returns the index of the first variant at or after from that match reports, and fails the test with
// what when no variant there matches.
func findVariant(t *testing.T, variants []any, from int, what string, match func(any) bool) int {
	t.Helper()
	for i := from; i < len(variants); i++ {
		if match(variants[i]) {
			return i
		}
	}
	t.Fatalf("stdout has no %s after line %d", what, from)
	return -1
}

// isRejectedSevenDayReset reports whether variant is a rejected seven_day rate_limit_event that resets at 1900000000.
func isRejectedSevenDayReset(variant any) bool {
	event, ok := variant.(wire.SDKRateLimitEvent)
	if !ok {
		return false
	}
	info := event.RateLimitInfo
	return info.Status == "rejected" && info.RateLimitType != nil && *info.RateLimitType == "seven_day" &&
		info.ResetsAt != nil && *info.ResetsAt == 1900000000
}

// isRateLimitError reports whether variant is an assistant line that carries the rate_limit error.
func isRateLimitError(variant any) bool {
	assistant, ok := variant.(wire.SDKAssistantMessage)
	return ok && assistant.Error != nil && *assistant.Error == "rate_limit"
}

// isLimitResult reports whether variant is an error result whose text is the limit text.
func isLimitResult(variant any) bool {
	result, ok := variant.(wire.SDKResultSuccess)
	return ok && result.IsError && result.Result == "You've hit your limit"
}

// TestFakeClaudeLimitTurn checks that a LIMIT prompt plays the usage-limit turn: a rejected seven_day rate_limit_event,
// then the assistant line that carries the rate_limit error, then the error result, in that order.
func TestFakeClaudeLimitTurn(t *testing.T) {
	t.Parallel()
	_, lines := fakeClaudeRun(t, userLine("LIMIT one"))
	variants := decodeVariants(t, lines)

	event := findVariant(t, variants, 0, "rejected seven_day rate_limit_event resetting at 1900000000",
		isRejectedSevenDayReset)
	assistant := findVariant(t, variants, event+1, "assistant line with error rate_limit", isRateLimitError)
	findVariant(t, variants, assistant+1, "error result carrying the limit text", isLimitResult)

	want := [][]string{{"You've hit your limit"}}
	if got := assistantTexts(t, lines); !reflect.DeepEqual(got, want) {
		t.Fatalf("assistant lines = %q, want %q", got, want)
	}
}

// TestFakeClaudeLimitNoResetTurn checks that a LIMITNORESET prompt plays the usage-limit turn without its
// rate_limit_event, and still ends with the error assistant line and the error result.
func TestFakeClaudeLimitNoResetTurn(t *testing.T) {
	t.Parallel()
	_, lines := fakeClaudeRun(t, userLine("LIMITNORESET one"))
	variants := decodeVariants(t, lines)
	for _, variant := range variants {
		if _, ok := variant.(wire.SDKRateLimitEvent); ok {
			t.Fatalf("LIMITNORESET turn sends a rate_limit_event: %#v", variant)
		}
	}
	assistant := findVariant(t, variants, 0, "assistant line with error rate_limit", isRateLimitError)
	findVariant(t, variants, assistant+1, "error result carrying the limit text", isLimitResult)
}

// TestFakeClaudeNextPromptFromFile checks that a fakeclaude.next file stands in for the text of the next prompt and is
// removed when that prompt arrives, so the prompt after it is answered as an ordinary reply.
func TestFakeClaudeNextPromptFromFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fakeclaude.next"), []byte("LIMIT"), 0o600); err != nil {
		t.Fatalf("write fakeclaude.next: %v", err)
	}
	lines := fakeClaudeRunIn(t, dir, userLine("plain one")+userLine("plain two"))

	want := [][]string{{"You've hit your limit"}, {"fake reply 2"}}
	if got := assistantTexts(t, lines); !reflect.DeepEqual(got, want) {
		t.Fatalf("assistant lines = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "fakeclaude.next")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("fakeclaude.next after its prompt: err = %v, want it removed", err)
	}
}

// assistantTexts returns, for each assistant line in lines, the text of its content blocks in order.
func assistantTexts(t *testing.T, lines []string) [][]string {
	t.Helper()
	var turns [][]string
	for _, line := range lines {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &head); err != nil {
			t.Fatalf("stdout line is not JSON: %v\n%s", err, line)
		}
		if head.Type != "assistant" {
			continue
		}
		var assistant struct {
			Message struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &assistant); err != nil {
			t.Fatalf("assistant line does not parse: %v\n%s", err, line)
		}
		texts := make([]string, 0, len(assistant.Message.Content))
		for _, block := range assistant.Message.Content {
			texts = append(texts, block.Text)
		}
		turns = append(turns, texts)
	}
	return turns
}

// TestFakeClaudePairLine checks that a PAIR prompt is answered by one assistant line carrying two finished text
// blocks, that the next prompt is still answered as an ordinary reply, and that the fake logs each line it reads.
func TestFakeClaudePairLine(t *testing.T) {
	t.Parallel()
	prompts := userLine("PAIR one") + userLine("plain two")
	dir, lines := fakeClaudeRun(t, prompts)

	want := [][]string{{"fake pair 1.1", "fake pair 1.2"}, {"fake reply 2"}}
	if got := assistantTexts(t, lines); !reflect.DeepEqual(got, want) {
		t.Fatalf("assistant lines = %q, want %q", got, want)
	}
	stdin, err := os.ReadFile(filepath.Join(dir, "fakeclaude.stdin"))
	if err != nil {
		t.Fatalf("read fake stdin log: %v", err)
	}
	if string(stdin) != prompts {
		t.Fatalf("fake stdin log = %q, want %q", stdin, prompts)
	}
}

// TestFakeClaudeStdinLogsAnswers checks that each line a mode reads after its prompt, an answer, a permission
// decision or an interrupt, is logged to the fake stdin log as well, so every line the fake receives is counted once.
func TestFakeClaudeStdinLogsAnswers(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		answer string
	}{
		{"ask", "ASK", `{"type":"control_response","response":{"answers":{"Which color?":"red"}}}` + "\n"},
		{"permit", "PERMIT", `{"type":"control_response","response":{"behavior":"allow"}}` + "\n"},
		{"hang", "HANG", `{"type":"control_request","request":{"subtype":"interrupt"}}` + "\n"},
		{"stall", "STALL", `{"type":"control_response"}` + "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := userLine(tc.prompt) + tc.answer
			dir, _ := fakeClaudeRun(t, input)
			stdin, err := os.ReadFile(filepath.Join(dir, "fakeclaude.stdin"))
			if err != nil {
				t.Fatalf("read fake stdin log: %v", err)
			}
			if string(stdin) != input {
				t.Fatalf("fake stdin log = %q, want %q", stdin, input)
			}
		})
	}
}
