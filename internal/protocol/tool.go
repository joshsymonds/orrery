package protocol

import (
	"bytes"
	"encoding/json"
	"strings"
)

// MaxToolInputChars is the most runes ToolInputText returns.
const MaxToolInputChars = 200

// ToolInputText returns the one-line display text of a tool_input: the first
// non-empty string field among the known keys when input is an object, or its
// compact JSON otherwise. Line breaks become spaces and the result is clipped to
// MaxToolInputChars runes. Empty, null and invalid input give "".
func ToolInputText(input json.RawMessage) string {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	var value any
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return ""
	}
	text := ""
	if object, ok := value.(map[string]any); ok {
		text = displayField(object)
	}
	if text == "" {
		var compact bytes.Buffer
		if err := json.Compact(&compact, trimmed); err != nil {
			return ""
		}
		text = compact.String()
	}
	text = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(text)
	return clipRunes(text, MaxToolInputChars)
}

// displayField returns the first non-empty string value among the display keys, in order.
func displayField(object map[string]any) string {
	for _, key := range []string{"command", "file_path", "path", "pattern", "url", "query", "prompt", "description"} {
		if text, ok := object[key].(string); ok && text != "" {
			return text
		}
	}
	return ""
}

// clipRunes returns the first limit runes of text.
func clipRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}
