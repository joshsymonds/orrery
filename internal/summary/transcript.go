// Package summary writes a one-line summary and a short label for each Claude Code pane by running claude in print mode.
package summary

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// MaxInputBytes caps the summarizer input text.
const MaxInputBytes = 8 * 1024

// excerptKeepExchanges is how many trailing exchanges the rendered text holds.
const excerptKeepExchanges = 4

// Excerpt is the summarizer input drawn from one session transcript.
type Excerpt struct {
	Text      string // at most MaxInputBytes bytes, valid UTF-8
	Exchanges int    // number of user prompts in the transcript
}

// transcriptEntry holds the fields of one transcript line that the excerpt reads.
type transcriptEntry struct {
	Type        string `json:"type"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// transcriptBlock is one content block of a message whose content is an array.
type transcriptBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// transcriptContent is the text of a message content value and the block kinds it holds.
type transcriptContent struct {
	text          string
	hasText       bool
	hasToolResult bool
}

// transcriptExchange is one user prompt and the assistant text that follows it.
type transcriptExchange struct {
	prompt  string
	replies []string
}

// transcriptBook accumulates exchanges from transcript lines, keeping only the most recent ones.
type transcriptBook struct {
	exchanges []transcriptExchange
	prompts   int
}

// ReadExcerpt reads a Claude Code session transcript and renders the summarizer input from its last
// exchanges and lastAssistantMessage. Transcript lines that are not valid JSON are skipped.
func ReadExcerpt(transcriptPath, lastAssistantMessage string) (Excerpt, error) {
	file, err := os.Open(filepath.Clean(transcriptPath))
	if err != nil {
		return Excerpt{}, fmt.Errorf("open transcript: %w", err)
	}
	defer file.Close()

	var book transcriptBook
	reader := bufio.NewReader(file)
	for {
		line, readErr := reader.ReadBytes('\n')
		book.addLine(line)
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return Excerpt{}, fmt.Errorf("read transcript: %w", readErr)
		}
	}

	return Excerpt{Text: book.render(lastAssistantMessage), Exchanges: book.prompts}, nil
}

// addLine folds one transcript line into the book. Lines that do not parse are skipped.
func (b *transcriptBook) addLine(line []byte) {
	var entry transcriptEntry
	if json.Unmarshal(line, &entry) != nil || entry.IsMeta || entry.IsSidechain {
		return
	}

	switch entry.Type {
	case "user":
		b.addPrompt(entry.Message.Content)
	case "assistant":
		b.addReply(entry.Message.Content)
	}
}

// addPrompt starts a new exchange when a user message holds prompt text and no tool result.
func (b *transcriptBook) addPrompt(raw json.RawMessage) {
	content := transcriptParseContent(raw)
	if !content.hasText || content.hasToolResult {
		return
	}

	b.prompts++
	b.exchanges = append(b.exchanges, transcriptExchange{prompt: content.text})
	if len(b.exchanges) > excerptKeepExchanges {
		b.exchanges = b.exchanges[1:]
	}
}

// addReply appends assistant text to the exchange in progress.
func (b *transcriptBook) addReply(raw json.RawMessage) {
	content := transcriptParseContent(raw)
	if !content.hasText || len(b.exchanges) == 0 {
		return
	}

	last := &b.exchanges[len(b.exchanges)-1]
	last.replies = append(last.replies, content.text)
}

// render lays out the kept exchanges and the last assistant message, capped at MaxInputBytes.
func (b *transcriptBook) render(lastAssistantMessage string) string {
	var lines []string
	for _, exchange := range b.exchanges {
		lines = append(lines, "User: "+exchange.prompt)
		if reply := strings.Join(exchange.replies, "\n"); reply != "" {
			lines = append(lines, "Assistant: "+reply)
		}
	}
	if lastAssistantMessage != "" {
		lines = append(lines, "Assistant: "+lastAssistantMessage)
	}

	return excerptTail(strings.Join(lines, "\n"))
}

// transcriptParseContent reads a message content value that is either a string or an array of blocks.
// A value of any other shape yields no text.
func transcriptParseContent(raw json.RawMessage) transcriptContent {
	var plain string
	if json.Unmarshal(raw, &plain) == nil {
		return transcriptContent{text: plain, hasText: plain != ""}
	}

	var blocks []transcriptBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return transcriptContent{}
	}

	var content transcriptContent
	var parts []string
	for _, block := range blocks {
		switch block.Type {
		case "text":
			content.hasText = true
			parts = append(parts, block.Text)
		case "tool_result":
			content.hasToolResult = true
		}
	}
	content.text = strings.Join(parts, "\n")

	return content
}

// excerptTail keeps the last MaxInputBytes bytes of text, advanced to the next UTF-8 rune boundary.
func excerptTail(text string) string {
	if len(text) <= MaxInputBytes {
		return text
	}

	start := len(text) - MaxInputBytes
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}

	return text[start:]
}
