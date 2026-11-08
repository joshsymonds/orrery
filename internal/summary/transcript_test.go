package summary_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/summary"
)

const excerptLongLineBytes = 100 * 1024

func excerptEntry(t *testing.T, entry map[string]any) string {
	t.Helper()

	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal fixture entry: %v", err)
	}

	return string(raw)
}

func excerptPrompt(t *testing.T, text string) string {
	t.Helper()

	return excerptEntry(t, map[string]any{
		"type":    "user",
		"message": map[string]any{"role": "user", "content": text},
	})
}

func excerptReply(t *testing.T, blocks ...map[string]any) string {
	t.Helper()

	content := make([]any, 0, len(blocks))
	for _, block := range blocks {
		content = append(content, block)
	}

	return excerptEntry(t, map[string]any{
		"type":    "assistant",
		"message": map[string]any{"role": "assistant", "content": content},
	})
}

func excerptTextBlock(text string) map[string]any {
	return map[string]any{"type": "text", "text": text}
}

func excerptWriteTranscript(t *testing.T, lines ...string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture transcript: %v", err)
	}

	return path
}

func TestSummarizerExcerptLastFourExchangesOldestFirst(t *testing.T) {
	t.Parallel()

	words := []string{"one", "two", "three", "four", "five"}
	lines := make([]string, 0, 2*len(words)+1)
	for _, word := range words {
		lines = append(lines,
			excerptPrompt(t, "Example prompt "+word),
			excerptReply(t, excerptTextBlock("Example reply "+word)),
		)
	}
	lines = append(lines, excerptPrompt(t, "Example prompt six"))

	got, err := summary.ReadExcerpt(excerptWriteTranscript(t, lines...), "")
	if err != nil {
		t.Fatalf("ReadExcerpt: %v", err)
	}

	want := "User: Example prompt three\n" +
		"Assistant: Example reply three\n" +
		"User: Example prompt four\n" +
		"Assistant: Example reply four\n" +
		"User: Example prompt five\n" +
		"Assistant: Example reply five\n" +
		"User: Example prompt six"
	if got.Text != want {
		t.Errorf("Text = %q, want %q", got.Text, want)
	}
	if got.Exchanges != 6 {
		t.Errorf("Exchanges = %d, want 6", got.Exchanges)
	}
}

func TestSummarizerExcerptSkipsToolThinkingMetaAndSidechain(t *testing.T) {
	t.Parallel()

	toolResultOnly := excerptEntry(t, map[string]any{
		"type": "user",
		"message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "example-tool", "content": "Example tool output"},
		}},
	})
	toolResultWithText := excerptEntry(t, map[string]any{
		"type": "user",
		"message": map[string]any{"role": "user", "content": []any{
			excerptTextBlock("Example mixed text"),
			map[string]any{"type": "tool_result", "tool_use_id": "example-tool", "content": "Example tool output"},
		}},
	})
	meta := excerptEntry(t, map[string]any{
		"type": "user", "isMeta": true,
		"message": map[string]any{"role": "user", "content": "Example meta text"},
	})
	sidechain := excerptEntry(t, map[string]any{
		"type": "assistant", "isSidechain": true,
		"message": map[string]any{"role": "assistant", "content": []any{excerptTextBlock("Example sidechain text")}},
	})
	summaryLine := excerptEntry(t, map[string]any{"type": "summary", "summary": "Example summary text"})

	lines := []string{
		excerptPrompt(t, "Example prompt one"),
		excerptReply(t,
			map[string]any{"type": "thinking", "thinking": "Example thinking text"},
			map[string]any{"type": "tool_use", "id": "example-tool", "name": "Example tool", "input": map[string]any{}},
			excerptTextBlock("Example reply one"),
		),
		toolResultOnly,
		excerptReply(t, excerptTextBlock("Example reply two")),
		meta,
		sidechain,
		toolResultWithText,
		summaryLine,
	}

	got, err := summary.ReadExcerpt(excerptWriteTranscript(t, lines...), "")
	if err != nil {
		t.Fatalf("ReadExcerpt: %v", err)
	}

	want := "User: Example prompt one\nAssistant: Example reply one\nExample reply two"
	if got.Text != want {
		t.Errorf("Text = %q, want %q", got.Text, want)
	}
	if got.Exchanges != 1 {
		t.Errorf("Exchanges = %d, want 1", got.Exchanges)
	}
}

func TestSummarizerExcerptCountsPrompts(t *testing.T) {
	t.Parallel()

	lines := []string{
		excerptPrompt(t, "Example prompt one"),
		excerptEntry(t, map[string]any{
			"type": "user", "message": map[string]any{"role": "user", "content": ""},
		}),
		excerptEntry(t, map[string]any{
			"type": "user", "message": map[string]any{"role": "user", "content": []any{
				excerptTextBlock("Example prompt two"),
			}},
		}),
		excerptEntry(t, map[string]any{
			"type": "user", "message": map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "example-tool", "content": "Example output"},
			}},
		}),
	}

	got, err := summary.ReadExcerpt(excerptWriteTranscript(t, lines...), "")
	if err != nil {
		t.Fatalf("ReadExcerpt: %v", err)
	}

	if got.Exchanges != 2 {
		t.Errorf("Exchanges = %d, want 2", got.Exchanges)
	}
	want := "User: Example prompt one\nUser: Example prompt two"
	if got.Text != want {
		t.Errorf("Text = %q, want %q", got.Text, want)
	}
}

func TestSummarizerExcerptLastAssistantMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		lines     []string
		message   string
		wantText  string
		wantCount int
	}{
		{
			name: "message follows the last exchange",
			lines: []string{
				excerptPrompt(t, "Example prompt one"),
				excerptReply(t, excerptTextBlock("Example reply one")),
			},
			message:   "Example final message",
			wantText:  "User: Example prompt one\nAssistant: Example reply one\nAssistant: Example final message",
			wantCount: 1,
		},
		{
			name:      "message alone when the transcript is empty",
			lines:     nil,
			message:   "Example final message",
			wantText:  "Assistant: Example final message",
			wantCount: 0,
		},
		{
			name:      "empty transcript and empty message",
			lines:     nil,
			message:   "",
			wantText:  "",
			wantCount: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := summary.ReadExcerpt(excerptWriteTranscript(t, test.lines...), test.message)
			if err != nil {
				t.Fatalf("ReadExcerpt: %v", err)
			}
			if got.Text != test.wantText {
				t.Errorf("Text = %q, want %q", got.Text, test.wantText)
			}
			if got.Exchanges != test.wantCount {
				t.Errorf("Exchanges = %d, want %d", got.Exchanges, test.wantCount)
			}
		})
	}
}

func TestSummarizerExcerptCapsAtMaxInputBytes(t *testing.T) {
	t.Parallel()

	// With no exchanges the rendering is "Assistant: " plus the message. The multibyte
	// runes start at an odd offset so the 8 KB cut lands inside one of them.
	const marker = " Example end marker"
	message := "x" + strings.Repeat("é", 6000) + marker
	full := "Assistant: " + message
	cut := len(full) - summary.MaxInputBytes
	if utf8.RuneStart(full[cut]) {
		t.Fatalf("fixture cut at %d is a rune boundary, want a mid-rune cut", cut)
	}

	got, err := summary.ReadExcerpt(excerptWriteTranscript(t), message)
	if err != nil {
		t.Fatalf("ReadExcerpt: %v", err)
	}

	if len(got.Text) > summary.MaxInputBytes {
		t.Errorf("len(Text) = %d, want at most %d", len(got.Text), summary.MaxInputBytes)
	}
	if !utf8.ValidString(got.Text) {
		t.Errorf("Text is not valid UTF-8")
	}
	if want := full[cut+1:]; got.Text != want {
		t.Errorf("Text does not match the newest text advanced to the next rune boundary")
	}
	if !strings.HasSuffix(got.Text, marker) {
		t.Errorf("Text lost the newest text: %q", got.Text[max(0, len(got.Text)-40):])
	}
}

func TestSummarizerExcerptLongLineParses(t *testing.T) {
	t.Parallel()

	reply := strings.Repeat("b", excerptLongLineBytes) + " Example tail"
	lines := []string{
		excerptPrompt(t, "Example prompt one"),
		excerptReply(t, excerptTextBlock(reply)),
	}

	got, err := summary.ReadExcerpt(excerptWriteTranscript(t, lines...), "")
	if err != nil {
		t.Fatalf("ReadExcerpt: %v", err)
	}

	if got.Exchanges != 1 {
		t.Errorf("Exchanges = %d, want 1", got.Exchanges)
	}
	if !strings.HasSuffix(got.Text, " Example tail") {
		t.Errorf("Text does not end with the long reply's tail")
	}
}

func TestSummarizerExcerptSkipsInvalidLine(t *testing.T) {
	t.Parallel()

	lines := []string{
		excerptPrompt(t, "Example prompt one"),
		`{"type":"assistant","message":{"role":"assistant","content":"Example broken`,
		excerptReply(t, excerptTextBlock("Example reply after")),
	}

	got, err := summary.ReadExcerpt(excerptWriteTranscript(t, lines...), "")
	if err != nil {
		t.Fatalf("ReadExcerpt: %v", err)
	}

	want := "User: Example prompt one\nAssistant: Example reply after"
	if got.Text != want {
		t.Errorf("Text = %q, want %q", got.Text, want)
	}
	if got.Exchanges != 1 {
		t.Errorf("Exchanges = %d, want 1", got.Exchanges)
	}
}

func TestSummarizerExcerptMissingFileErrors(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing.jsonl")

	_, err := summary.ReadExcerpt(missing, "Example final message")
	if err == nil {
		t.Fatal("ReadExcerpt on a missing file returned nil error")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error = %v, want one wrapping fs.ErrNotExist", err)
	}
}
