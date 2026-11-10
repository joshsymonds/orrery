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

// taskNotification returns the content of a task notification with the given status and summary.
func taskNotification(status, summary string) string {
	return "<task-notification><task-id>task_1</task-id><status>" + status + "</status><summary>" +
		summary + "</summary></task-notification>"
}

// taskLine returns a user line that Claude Code marks as a task notification with the given content.
func taskLine(s int, content string) string {
	return `{"type":"user","timestamp":"` + stampAt(s) + `","promptSource":"system",` +
		`"origin":{"kind":"task-notification"},"message":{"role":"user","content":` + content + `}}`
}

func TestReadNoticeKinds(t *testing.T) {
	path := writeTranscript(t,
		`{"type":"user","timestamp":"`+stampAt(0)+`","isCompactSummary":true,`+
			`"message":{"role":"user","content":"synthetic summary of earlier turns"}}`,
		taskLine(1, `"`+taskNotification("completed", "Synthetic task done")+`"`),
		`{"type":"user","timestamp":"`+stampAt(2)+`","message":{"role":"user","content":[`+
			`{"type":"text","text":"[Request interrupted by user]"}]}}`,
		`{"type":"user","timestamp":"`+stampAt(3)+`","message":{"role":"user","content":[`+
			`{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`,
	)

	want := []Item{
		{Kind: "notice", Time: unixAt(0), Notice: "compaction"},
		{Kind: "notice", Time: unixAt(1), Notice: "task", Text: "Synthetic task done"},
		{Kind: "notice", Time: unixAt(2), Notice: "interrupted"},
		{Kind: "notice", Time: unixAt(3), Notice: "interrupted"},
	}
	if got := mustRead(t, path, 100).Items; !reflect.DeepEqual(got, want) {
		t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
	}
}

func TestReadTaskNoticeErrorFromStatus(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{"completed", false},
		{"failed", true},
		{"killed", true},
	}
	for _, tc := range tests {
		t.Run(tc.status, func(t *testing.T) {
			path := writeTranscript(t,
				taskLine(0, `"`+taskNotification(tc.status, "Synthetic summary")+`"`))
			items := mustRead(t, path, 10).Items
			if len(items) != 1 {
				t.Fatalf("items = %d, want 1", len(items))
			}
			if items[0].Error != tc.want {
				t.Fatalf("Error = %t, want %t", items[0].Error, tc.want)
			}
		})
	}
}

func TestReadTaskNoticeWithoutSummaryHasEmptyText(t *testing.T) {
	content := `"<task-notification><task-id>task_2</task-id><status>completed</status></task-notification>"`
	items := mustRead(t, writeTranscript(t, taskLine(0, content)), 10).Items
	if len(items) != 1 || items[0].Notice != "task" || items[0].Text != "" {
		t.Fatalf("items = %+v, want one task notice with empty text", items)
	}
}

func TestReadTaskNoticeSummaryCutsToRunes(t *testing.T) {
	summary := strings.Repeat("é", 301)
	content := `"` + taskNotification("completed", summary) + `"`
	items := mustRead(t, writeTranscript(t, taskLine(0, content)), 10).Items
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if want := strings.Repeat("é", 300); items[0].Text != want {
		t.Fatalf("Text has %d runes, want %d runes of é", utf8.RuneCountInString(items[0].Text), 300)
	}
}

func TestReadTypedLinesNeverBecomeNotices(t *testing.T) {
	wrapped := taskNotification("failed", "Synthetic typed summary")
	tests := []struct {
		name   string
		source string
		origin string
		text   string
		prompt string
	}{
		{"typed line quoting a task notification", "typed", "human",
			`"` + wrapped + `"`, wrapped},
		{"queued line quoting a task notification", "queued", "human",
			`"` + wrapped + `"`, wrapped},
		{"suggestion line quoting a task notification", "suggestion_accepted", "human",
			`"` + wrapped + `"`, wrapped},
		{"typed line holding a whole interruption string", "typed", "human",
			`[{"type":"text","text":"[Request interrupted by user]"}]`, "[Request interrupted by user]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			line := `{"type":"user","timestamp":"` + stampAt(0) + `","promptSource":"` + tc.source +
				`","origin":{"kind":"` + tc.origin + `"},"message":{"role":"user","content":` + tc.text + `}}`
			want := []Item{{Kind: "prompt", Time: unixAt(0), Text: tc.prompt}}
			if got := mustRead(t, writeTranscript(t, line), 10).Items; !reflect.DeepEqual(got, want) {
				t.Fatalf("Items = %+v, want %+v", got, want)
			}
		})
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
	for _, key := range []string{"kind", "time", "text", "tool", "input", "id", "error", "notice"} {
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

const (
	teammateRequest = "\nSynthetic request for the peer.\n"
	teammateIdle    = "\n{\"type\":\"idle_notification\"}\n"
)

// teammateBlock returns one teammate block with the given escaped summary attribute. An empty summary gives
// the block no summary attribute.
func teammateBlock(summary, body string) string {
	attr := ""
	if summary != "" {
		attr = ` summary="` + summary + `"`
	}
	return `<teammate-message teammate_id="synthetic-peer" color="blue"` + attr + ">" + body +
		"</teammate-message>"
}

// teammateContent returns the whole string content of a teammate line that holds blocks.
func teammateContent(blocks ...string) string {
	return teammateLead + strings.Join(blocks, teammateSep) + teammateTrailer
}

// teammateLine returns a user line with no promptSource and no origin whose string content is content.
func teammateLine(t *testing.T, content string) string {
	t.Helper()
	raw, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}
	return `{"type":"user","timestamp":"` + stampAt(0) + `","message":{"role":"user","content":` +
		string(raw) + `}}`
}

func TestReadTeammateLineGivesTeammateNotice(t *testing.T) {
	line := teammateLine(t, teammateContent(teammateBlock("Synthetic summary", teammateRequest)))
	want := []Item{{Kind: "notice", Time: unixAt(0), Notice: "teammate", Text: "Synthetic summary"}}
	if got := mustRead(t, writeTranscript(t, line), 10).Items; !reflect.DeepEqual(got, want) {
		t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
	}
}

func TestReadTeammateSummariesJoinInOrder(t *testing.T) {
	content := teammateContent(
		teammateBlock("First summary", teammateRequest),
		teammateBlock("Second summary", teammateRequest),
		teammateBlock("Third summary", teammateRequest))
	want := []Item{{Kind: "notice", Time: unixAt(0), Notice: "teammate",
		Text: "First summary\nSecond summary\nThird summary"}}
	if got := mustRead(t, writeTranscript(t, teammateLine(t, content)), 10).Items; !reflect.DeepEqual(got, want) {
		t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
	}
}

func TestReadTeammateSummaryDecodesEntities(t *testing.T) {
	summary := "a &amp; b &lt;c&gt; &quot;d&quot; &apos;e&apos; &amp;lt;"
	content := teammateContent(teammateBlock(summary, teammateRequest))
	items := mustRead(t, writeTranscript(t, teammateLine(t, content)), 10).Items
	if want := `a & b <c> "d" 'e' &lt;`; len(items) != 1 || items[0].Text != want {
		t.Fatalf("items = %+v, want one teammate notice with text %q", items, want)
	}
}

func TestReadTeammateSummaryCutsToRunes(t *testing.T) {
	summary := strings.Repeat("é", 301)
	content := teammateContent(teammateBlock(summary, teammateRequest))
	items := mustRead(t, writeTranscript(t, teammateLine(t, content)), 10).Items
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if want := strings.Repeat("é", 300); items[0].Text != want {
		t.Fatalf("Text has %d runes, want %d runes of é", utf8.RuneCountInString(items[0].Text), 300)
	}
}

func TestReadTeammateBlockWithoutColorGivesNotice(t *testing.T) {
	block := `<teammate-message teammate_id="synthetic-peer" summary="Synthetic summary">` + teammateRequest +
		"</teammate-message>"
	want := []Item{{Kind: "notice", Time: unixAt(0), Notice: "teammate", Text: "Synthetic summary"}}
	line := teammateLine(t, teammateContent(block))
	if got := mustRead(t, writeTranscript(t, line), 10).Items; !reflect.DeepEqual(got, want) {
		t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
	}
}

func TestReadTeammateWithoutSummaryHasEmptyText(t *testing.T) {
	content := teammateContent(teammateBlock("", teammateRequest))
	want := []Item{{Kind: "notice", Time: unixAt(0), Notice: "teammate"}}
	if got := mustRead(t, writeTranscript(t, teammateLine(t, content)), 10).Items; !reflect.DeepEqual(got, want) {
		t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
	}
}

func TestReadTeammateIdlePingsGiveNoItem(t *testing.T) {
	content := teammateContent(teammateBlock("", teammateIdle), teammateBlock("", teammateIdle))
	if got := mustRead(t, writeTranscript(t, teammateLine(t, content)), 10).Items; len(got) != 0 {
		t.Fatalf("Items = %+v, want none", got)
	}
}

func TestReadTeammateIdleAndSummarisedBlocksGiveNotice(t *testing.T) {
	content := teammateContent(
		teammateBlock("", teammateIdle),
		teammateBlock("Synthetic summary", teammateRequest))
	want := []Item{{Kind: "notice", Time: unixAt(0), Notice: "teammate", Text: "Synthetic summary"}}
	if got := mustRead(t, writeTranscript(t, teammateLine(t, content)), 10).Items; !reflect.DeepEqual(got, want) {
		t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
	}
}

func TestReadTeammateManyBlocksGiveOneNotice(t *testing.T) {
	blocks := make([]string, 0, 14)
	for range 14 {
		blocks = append(blocks, teammateBlock("Synthetic summary", teammateRequest))
	}
	line := teammateLine(t, teammateContent(blocks...))
	want := []Item{{Kind: "notice", Time: unixAt(0), Notice: "teammate",
		Text: strings.Repeat("Synthetic summary\n", 13) + "Synthetic summary"}}
	if got := mustRead(t, writeTranscript(t, line), 10).Items; !reflect.DeepEqual(got, want) {
		t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
	}
}

func TestReadNonWholeTeammateTextStaysPrompt(t *testing.T) {
	whole := teammateContent(teammateBlock("Synthetic summary", teammateRequest))
	tests := []struct {
		name    string
		content string
	}{
		{"text before the lead-in", "Preface\n" + whole},
		{"text after the trailer", whole + "\nmore"},
		{"missing trailer", teammateLead + teammateBlock("Synthetic summary", teammateRequest)},
		{"same-length tail instead of the trailer", teammateLead + teammateBlock("Synthetic summary", teammateRequest) +
			strings.Repeat("x", len(teammateTrailer))},
		{"unclosed block", teammateLead + `<teammate-message teammate_id="synthetic-peer" color="blue">` +
			teammateRequest + teammateTrailer},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			line := teammateLine(t, tc.content)
			want := []Item{{Kind: "prompt", Time: unixAt(0), Text: tc.content}}
			if got := mustRead(t, writeTranscript(t, line), 10).Items; !reflect.DeepEqual(got, want) {
				t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}

func TestReadTypedTeammateLineStaysPrompt(t *testing.T) {
	content := teammateContent(teammateBlock("Synthetic summary", teammateRequest))
	raw, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}
	line := `{"type":"user","timestamp":"` + stampAt(0) + `","promptSource":"typed","origin":{"kind":"human"},` +
		`"message":{"role":"user","content":` + string(raw) + `}}`
	want := []Item{{Kind: "prompt", Time: unixAt(0), Text: content}}
	if got := mustRead(t, writeTranscript(t, line), 10).Items; !reflect.DeepEqual(got, want) {
		t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
	}
}
