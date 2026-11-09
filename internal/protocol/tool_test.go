package protocol_test

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/protocol"
)

func TestToolInputText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "bash command wins over description",
			input: `{"command":"go test ./...","description":"x"}`,
			want:  "go test ./...",
		},
		{
			name:  "read file path",
			input: `{"file_path":"/home/user/project/a.go"}`,
			want:  "/home/user/project/a.go",
		},
		{
			name:  "pattern outranks description",
			input: `{"description":"find things","pattern":"TODO"}`,
			want:  "TODO",
		},
		{
			name:  "non-string command is skipped",
			input: `{"command":5,"file_path":"/x/y.go"}`,
			want:  "/x/y.go",
		},
		{
			name:  "empty string value is skipped",
			input: `{"command":"","path":"/p"}`,
			want:  "/p",
		},
		{
			name:  "object without a listed key is compact json",
			input: `{ "other": 1, "list": [1, 2] }`,
			want:  `{"other":1,"list":[1,2]}`,
		},
		{
			name:  "array is compact json",
			input: `[1, 2]`,
			want:  `[1,2]`,
		},
		{
			name:  "number is compact json",
			input: `42`,
			want:  "42",
		},
		{
			name:  "bool is compact json",
			input: `true`,
			want:  "true",
		},
		{
			name:  "bare string is compact json",
			input: `"hello"`,
			want:  `"hello"`,
		},
		{
			name:  "multi-line command becomes one line",
			input: `{"command":"echo a\nb\r\nc\rd"}`,
			want:  "echo a b c d",
		},
		{
			name:  "null is empty",
			input: `null`,
			want:  "",
		},
		{
			name:  "empty input is empty",
			input: ``,
			want:  "",
		},
		{
			name:  "whitespace-only input is empty",
			input: " \n\t ",
			want:  "",
		},
		{
			name:  "invalid json is empty",
			input: `{"command":`,
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := protocol.ToolInputText(json.RawMessage(tt.input)); got != tt.want {
				t.Errorf("ToolInputText(%s) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestToolInputTextClipsRunes(t *testing.T) {
	if protocol.MaxToolInputChars != 200 {
		t.Fatalf("MaxToolInputChars = %d, want 200", protocol.MaxToolInputChars)
	}
	input := `{"command":"` + strings.Repeat("é", 300) + `"}`
	got := protocol.ToolInputText(json.RawMessage(input))
	if n := utf8.RuneCountInString(got); n != 200 {
		t.Errorf("rune count = %d, want 200", n)
	}
	if !utf8.ValidString(got) {
		t.Errorf("clipped text is not valid UTF-8: %q", got)
	}
	if want := strings.Repeat("é", 200); got != want {
		t.Errorf("clipped text = %q, want %q", got, want)
	}
}
