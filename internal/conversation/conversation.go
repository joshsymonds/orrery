package conversation

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// Item kinds.
const (
	kindPrompt   = "prompt"
	kindReply    = "reply"
	kindTool     = "tool"
	kindQuestion = "question"
	kindResult   = "result"
)

// askUserQuestionTool is the tool whose tool_use items are questions.
const askUserQuestionTool = "AskUserQuestion"

const (
	// maxTextBytes caps the text of one item.
	maxTextBytes = 4096
	// originWindowBytes is how far into the transcript the origin prompt is searched.
	originWindowBytes = 1 << 20
	// maxOriginRunes caps the origin prompt.
	maxOriginRunes = 300
)

// Conversation is a transcript's opening prompt and its newest items, oldest first.
type Conversation struct {
	Origin string `json:"origin"`
	Items  []Item `json:"items"`
}

// Item is one reply, tool call, question, prompt or tool result of a transcript.
type Item struct {
	Kind  string `json:"kind"`
	Time  int64  `json:"time"`
	Text  string `json:"text"`
	Tool  string `json:"tool"`
	Input string `json:"input"`
	ID    string `json:"id"`
	Error bool   `json:"error"`
}

// transcriptLine holds the fields of one transcript line that the reader uses.
type transcriptLine struct {
	Type        string `json:"type"`
	Timestamp   string `json:"timestamp"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// contentBlock is one block of a message content array. Its fields are read by key, because the
// transcript's snake_case keys do not fit the camelCase JSON tags the lint configuration requires.
type contentBlock map[string]json.RawMessage

// askInput holds the questions of an AskUserQuestion tool input.
type askInput struct {
	Questions []struct {
		Question string `json:"question"`
	} `json:"questions"`
}

// Read returns the opening prompt of the transcript at path and its newest limit items, oldest first.
// Items is never nil.
func Read(path string, limit int) (Conversation, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return Conversation{}, fmt.Errorf("open transcript: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return Conversation{}, fmt.Errorf("stat transcript: %w", err)
	}
	size := info.Size()

	items, err := newestItems(file, size, limit)
	if err != nil {
		return Conversation{}, err
	}
	origin, err := originPrompt(file, size)
	if err != nil {
		return Conversation{}, err
	}
	return Conversation{Origin: origin, Items: items}, nil
}

// newestItems returns the newest limit items of the transcript, oldest first, from the tail of src.
func newestItems(src io.ReaderAt, size int64, limit int) ([]Item, error) {
	newest := []Item{}
	err := ReadTail(src, size, MaxTailBytes, func(line []byte) bool {
		items := lineItems(line)
		for i := len(items) - 1; i >= 0 && len(newest) < limit; i-- {
			newest = append(newest, items[i])
		}
		return len(newest) < limit
	})
	if err != nil {
		return nil, err
	}
	slices.Reverse(newest)
	return newest, nil
}

// lineItems returns the items of one transcript line in order, or none when the line is skipped.
func lineItems(line []byte) []Item {
	var entry transcriptLine
	if json.Unmarshal(line, &entry) != nil || entry.IsMeta || entry.IsSidechain {
		return nil
	}
	ts := unixSeconds(entry.Timestamp)
	switch entry.Type {
	case "assistant":
		return assistantItems(entry.Message.Content, ts)
	case "user":
		return userItems(entry.Message.Content, ts)
	default:
		return nil
	}
}

// assistantItems returns one item per content block: a reply per text block and a tool or question per
// tool_use block. Thinking blocks give no item.
func assistantItems(content json.RawMessage, ts int64) []Item {
	_, blocks := splitContent(content)
	items := make([]Item, 0, len(blocks))
	for _, block := range blocks {
		switch stringField(block, "type") {
		case "text":
			items = append(items, Item{Kind: kindReply, Time: ts, Text: cutText(stringField(block, "text"))})
		case "tool_use":
			items = append(items, toolItem(block, ts))
		}
	}
	return items
}

// toolItem returns the question item for an AskUserQuestion call and a tool item for any other call.
func toolItem(block contentBlock, ts int64) Item {
	name := stringField(block, "name")
	id := stringField(block, "id")
	if name == askUserQuestionTool {
		return Item{Kind: kindQuestion, Time: ts, Text: cutText(questionText(block["input"])), Tool: name, ID: id}
	}
	return Item{Kind: kindTool, Time: ts, Tool: name, Input: protocol.ToolInputText(block["input"]), ID: id}
}

// questionText returns the questions of an AskUserQuestion input, joined with newlines.
func questionText(input json.RawMessage) string {
	var parsed askInput
	if json.Unmarshal(input, &parsed) != nil {
		return ""
	}
	questions := make([]string, 0, len(parsed.Questions))
	for _, q := range parsed.Questions {
		questions = append(questions, q.Question)
	}
	return strings.Join(questions, "\n")
}

// userItems returns one result per tool_result block of a user message, or its prompt when it holds no
// tool_result.
func userItems(content json.RawMessage, ts int64) []Item {
	text, blocks := splitContent(content)
	var items []Item
	for _, block := range blocks {
		if stringField(block, "type") == "tool_result" {
			items = append(items, resultItem(block, ts))
		}
	}
	if len(items) > 0 {
		return items
	}
	return promptItems(text, ts)
}

// promptItems returns the prompt item for text, or none when text is empty.
func promptItems(text string, ts int64) []Item {
	if text == "" {
		return nil
	}
	return []Item{{Kind: kindPrompt, Time: ts, Text: cutText(text)}}
}

// resultItem returns the result item of one tool_result block.
func resultItem(block contentBlock, ts int64) Item {
	text, _ := splitContent(block["content"])
	return Item{
		Kind:  kindResult,
		Time:  ts,
		Text:  cutText(text),
		ID:    stringField(block, "tool_use_id"),
		Error: boolField(block, "is_error"),
	}
}

// splitContent returns the text of a message content value and its blocks. A string value is the text
// and has no blocks. An array value's text is its text blocks joined with newlines.
func splitContent(content json.RawMessage) (string, []contentBlock) {
	var plain string
	if json.Unmarshal(content, &plain) == nil {
		return plain, nil
	}
	var blocks []contentBlock
	if json.Unmarshal(content, &blocks) != nil {
		return "", nil
	}
	var texts []string
	for _, block := range blocks {
		if stringField(block, "type") == "text" {
			texts = append(texts, stringField(block, "text"))
		}
	}
	return strings.Join(texts, "\n"), blocks
}

// stringField returns the string value of key in block, or "" when the key is absent or not a string.
func stringField(block contentBlock, key string) string {
	var value string
	if json.Unmarshal(block[key], &value) != nil {
		return ""
	}
	return value
}

// boolField returns the boolean value of key in block, or false when the key is absent or not a boolean.
func boolField(block contentBlock, key string) bool {
	var value bool
	if json.Unmarshal(block[key], &value) != nil {
		return false
	}
	return value
}

// unixSeconds returns the unix time of an RFC 3339 timestamp, or 0 when it is absent or unparseable.
func unixSeconds(stamp string) int64 {
	parsed, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return 0
	}
	return parsed.Unix()
}

// cutText returns the first maxTextBytes bytes of text, cut back to a UTF-8 rune boundary.
func cutText(text string) string {
	if len(text) <= maxTextBytes {
		return text
	}
	end := maxTextBytes
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end]
}

// originPrompt returns the first user prompt in the first originWindowBytes of the transcript, clipped to
// maxOriginRunes runes. A line that the window ends inside is ignored.
func originPrompt(src io.ReaderAt, size int64) (string, error) {
	window := min(size, originWindowBytes)
	reader := bufio.NewReader(io.NewSectionReader(src, 0, window))
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read transcript origin: %w", err)
		}
		if errors.Is(err, io.EOF) && window < size {
			return "", nil
		}
		if text := originText(line); text != "" {
			runes := []rune(text)
			if len(runes) > maxOriginRunes {
				text = string(runes[:maxOriginRunes])
			}
			return text, nil
		}
		if errors.Is(err, io.EOF) {
			return "", nil
		}
	}
}

// originText returns the prompt of a user line that is not meta or sidechain and holds no tool_result, or
// "" when the line is none of those.
func originText(line []byte) string {
	var entry transcriptLine
	if json.Unmarshal(line, &entry) != nil || entry.Type != "user" || entry.IsMeta || entry.IsSidechain {
		return ""
	}
	text, blocks := splitContent(entry.Message.Content)
	for _, block := range blocks {
		if stringField(block, "type") == "tool_result" {
			return ""
		}
	}
	return text
}
