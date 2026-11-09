package conversation

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// writeTranscript writes lines to a new transcript in t.TempDir and returns its path.
func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

// mustRead calls Read and fails the test on error.
func mustRead(t *testing.T, path string, limit int) Conversation {
	t.Helper()
	conv, err := Read(path, limit)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return conv
}

func TestReadUserLineWithToolResultGivesNoPrompt(t *testing.T) {
	path := writeTranscript(t,
		`{"type":"user","message":{"content":[{"type":"text","text":"synthetic reminder"},`+
			`{"type":"tool_result","tool_use_id":"toolu_9","content":"synthetic output"}]}}`)
	want := []Item{{Kind: "result", Text: "synthetic output", ID: "toolu_9"}}
	if got := mustRead(t, path, 30).Items; !reflect.DeepEqual(got, want) {
		t.Fatalf("Items = %+v, want %+v", got, want)
	}
}

// unixAt returns the unix time of second s on 2026-11-09 at 10:00 UTC.
func unixAt(s int) int64 {
	return time.Date(2026, time.November, 9, 10, 0, s, 0, time.UTC).Unix()
}

// stampAt returns the RFC 3339 timestamp of second s on 2026-11-09 at 10:00 UTC.
func stampAt(s int) string {
	return time.Unix(unixAt(s), 0).UTC().Format(time.RFC3339)
}

func TestReadItemKinds(t *testing.T) {
	path := writeTranscript(t,
		`{"type":"user","timestamp":"`+stampAt(0)+`","message":{"role":"user","content":"open the file"}}`,
		`{"type":"assistant","timestamp":"`+stampAt(1)+`","message":{"content":[`+
			`{"type":"thinking","thinking":"private"},`+
			`{"type":"text","text":"Looking."},`+
			`{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls -la"}}]}}`,
		`{"type":"assistant","timestamp":"`+stampAt(2)+`","message":{"content":[`+
			`{"type":"tool_use","id":"toolu_2","name":"AskUserQuestion","input":`+
			`{"questions":[{"question":"Which file?"},{"question":"Why?"}]}}]}}`,
		`{"type":"user","timestamp":"`+stampAt(3)+`","message":{"content":[`+
			`{"type":"tool_result","tool_use_id":"toolu_1","is_error":true,"content":"ls: cannot access"}]}}`,
		`{"type":"user","timestamp":"`+stampAt(4)+`","message":{"content":[`+
			`{"type":"text","text":"first"},{"type":"text","text":"second"}]}}`,
		`{"message":{"content":[{"type":"tool_result","tool_use_id":"toolu_2","content":[`+
			`{"type":"text","text":"a"},{"type":"image"},{"type":"text","text":"b"}]}]},"type":"user"}`,
		`{"type":"user","isMeta":true,"timestamp":"`+stampAt(6)+`","message":{"content":"skipped meta"}}`,
		`{"type":"assistant","isSidechain":true,"timestamp":"`+stampAt(7)+
			`","message":{"content":[{"type":"text","text":"skipped sidechain"}]}}`,
		`{"type":"assistant","message":{"content":[`,
		`{"type":"system","timestamp":"`+stampAt(9)+`","message":{"content":"skipped system"}}`,
		`{"type":"assistant","timestamp":"`+stampAt(10)+`","message":{"content":[{"type":"text","text":"Done."}]}}`,
	)

	conv := mustRead(t, path, 100)

	if conv.Origin != "open the file" {
		t.Errorf("Origin = %q, want %q", conv.Origin, "open the file")
	}
	want := []Item{
		{Kind: "prompt", Time: unixAt(0), Text: "open the file"},
		{Kind: "reply", Time: unixAt(1), Text: "Looking."},
		{Kind: "tool", Time: unixAt(1), Tool: "Bash", Input: "ls -la", ID: "toolu_1"},
		{Kind: "question", Time: unixAt(2), Text: "Which file?\nWhy?", Tool: "AskUserQuestion", ID: "toolu_2"},
		{Kind: "result", Time: unixAt(3), Text: "ls: cannot access", ID: "toolu_1", Error: true},
		{Kind: "prompt", Time: unixAt(4), Text: "first\nsecond"},
		{Kind: "result", Time: 0, Text: "a\nb", ID: "toolu_2"},
		{Kind: "reply", Time: unixAt(10), Text: "Done."},
	}
	if !reflect.DeepEqual(conv.Items, want) {
		t.Fatalf("Items =\n%+v\nwant\n%+v", conv.Items, want)
	}
}

func TestReadLimitKeepsNewestItemsOldestFirst(t *testing.T) {
	path := writeTranscript(t,
		`{"type":"user","message":{"content":"first"}}`,
		`{"type":"assistant","message":{"content":[`+
			`{"type":"text","text":"r1"},`+
			`{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/a"}},`+
			`{"type":"text","text":"r2"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"r3"}]}}`,
	)

	tests := []struct {
		name  string
		limit int
		want  []string
	}{
		{"zero limit gives no items", 0, []string{}},
		{"limit one gives the newest item", 1, []string{"r3"}},
		{"limit crosses a multi-item line", 3, []string{"Read", "r2", "r3"}},
		{"limit above the total gives every item", 100, []string{"first", "r1", "Read", "r2", "r3"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			conv := mustRead(t, path, tc.limit)
			got := make([]string, 0, len(conv.Items))
			for _, item := range conv.Items {
				if item.Tool != "" {
					got = append(got, item.Tool)
					continue
				}
				got = append(got, item.Text)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("items = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadItemsAreNeverNull(t *testing.T) {
	path := writeTranscript(t, `{"type":"system","message":{"content":"nothing to read"}}`)

	data, err := json.Marshal(mustRead(t, path, 10))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"origin":"","items":[]}`; string(data) != want {
		t.Fatalf("json = %s, want %s", data, want)
	}
}

func TestItemJSONAlwaysHasEveryKey(t *testing.T) {
	data, err := json.Marshal(Item{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"kind", "time", "text", "tool", "input", "id", "error"} {
		if _, ok := keys[key]; !ok {
			t.Errorf("zero Item JSON lacks key %q: %s", key, data)
		}
	}
}

func TestReadCutsTextAtUTF8Boundary(t *testing.T) {
	const cut = 4096
	tests := []struct {
		name string
		text string
		want string
	}{
		{"text within the cap is kept", strings.Repeat("a", cut), strings.Repeat("a", cut)},
		{"cut inside a multibyte rune drops the rune", strings.Repeat("a", cut-1) + "é" + "é",
			strings.Repeat("a", cut-1)},
		{"cut at a rune start keeps the whole cap", strings.Repeat("a", cut) + "é", strings.Repeat("a", cut)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			line := `{"type":"user","message":{"content":"` + tc.text + `"}}`
			conv := mustRead(t, writeTranscript(t, line), 10)
			if len(conv.Items) != 1 {
				t.Fatalf("items = %d, want 1", len(conv.Items))
			}
			got := conv.Items[0].Text
			if !utf8.ValidString(got) {
				t.Fatalf("text is not valid UTF-8: %q", got)
			}
			if got != tc.want {
				t.Fatalf("text has %d bytes, want %d bytes equal to the expected prefix", len(got), len(tc.want))
			}
		})
	}
}

func TestReadOrigin(t *testing.T) {
	const origin = 300
	longPrompt := strings.Repeat("é", origin+50)
	filler := func(size int) string {
		return `{"type":"assistant","message":{"content":[{"type":"text","text":"` +
			strings.Repeat("x", size) + `"}]}}`
	}
	const window = 1 << 20
	const crossingPrompt = `{"type":"user","message":{"content":"crosses the window"}}`
	tests := []struct {
		name  string
		lines []string
		want  string
	}{
		{
			"skips meta, sidechain and tool_result lines",
			[]string{
				`{"type":"user","isMeta":true,"message":{"content":"meta prompt"}}`,
				`{"type":"user","isSidechain":true,"message":{"content":"side prompt"}}`,
				`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t","content":"out"}]}}`,
				`{"type":"user","message":{"content":"real prompt"}}`,
				`{"type":"user","message":{"content":"later prompt"}}`,
			},
			"real prompt",
		},
		{
			"joins text blocks with newlines",
			[]string{`{"type":"user","message":{"content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}}`},
			"a\nb",
		},
		{
			"clips to 300 runes",
			[]string{`{"type":"user","message":{"content":"` + longPrompt + `"}}`},
			strings.Repeat("é", origin),
		},
		{
			"is empty when no prompt exists",
			[]string{`{"type":"assistant","message":{"content":[{"type":"text","text":"reply"}]}}`},
			"",
		},
		{
			"ignores a first prompt that lies past 1 MiB",
			append(repeatLines(filler(64<<10), 17),
				`{"type":"user","message":{"content":"too late"}}`),
			"",
		},
		{
			"ignores a line that crosses the 1 MiB boundary",
			// The prompt is complete JSON that ends exactly at the window's end. Its trailing spaces
			// cross the boundary, so only the boundary rule keeps the prompt out.
			[]string{
				strings.Repeat("x", window-len(crossingPrompt)-1),
				crossingPrompt + strings.Repeat(" ", 100),
			},
			"",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			conv := mustRead(t, writeTranscript(t, tc.lines...), 10)
			if conv.Origin != tc.want {
				t.Fatalf("Origin = %q, want %q", conv.Origin, tc.want)
			}
		})
	}
}

// repeatLines returns n copies of line.
func repeatLines(line string, n int) []string {
	lines := make([]string, 0, n)
	for range n {
		lines = append(lines, line)
	}
	return lines
}

func TestReadMissingFileWrapsErrNotExist(t *testing.T) {
	_, err := Read(filepath.Join(t.TempDir(), "missing.jsonl"), 10)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want one wrapping fs.ErrNotExist", err)
	}
}

func TestReadSparseFileReturnsHeadAndTail(t *testing.T) {
	const fileSize = 200 << 20
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	if err := f.Truncate(fileSize); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	head := `{"type":"user","message":{"content":"opening prompt"}}` + "\n"
	if _, err := f.WriteAt([]byte(head), 0); err != nil {
		t.Fatalf("write head: %v", err)
	}
	tail := []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"one"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"two"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"three"}]}}`,
	}
	// The leading newline ends the sparse zero bytes so the first tail line is complete.
	payload := "\n" + strings.Join(tail, "\n") + "\n"
	if _, err := f.WriteAt([]byte(payload), fileSize-int64(len(payload))); err != nil {
		t.Fatalf("write tail: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	conv := mustRead(t, path, 10)
	if conv.Origin != "opening prompt" {
		t.Fatalf("Origin = %q, want %q", conv.Origin, "opening prompt")
	}
	got := make([]string, 0, len(conv.Items))
	for _, item := range conv.Items {
		got = append(got, item.Text)
	}
	if want := []string{"one", "two", "three"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("items = %q, want %q", got, want)
	}
}

func TestReadOversizeNewestLineGivesNoItemsWithoutError(t *testing.T) {
	huge := `{"type":"assistant","message":{"content":[{"type":"text","text":"` +
		strings.Repeat("x", MaxTailBytes+100) + `"}]}}`
	path := writeTranscript(t, `{"type":"user","message":{"content":"opening"}}`, huge)

	conv := mustRead(t, path, 10)
	if len(conv.Items) != 0 {
		t.Fatalf("items = %d, want none", len(conv.Items))
	}
	if conv.Origin != "opening" {
		t.Fatalf("Origin = %q, want %q", conv.Origin, "opening")
	}
}
