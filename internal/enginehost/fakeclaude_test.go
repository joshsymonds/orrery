package enginehost

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
	cmd := exec.Command("/bin/sh", fakeClaudePath(t))
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run fake claude: %v", err)
	}
	return dir, strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
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
