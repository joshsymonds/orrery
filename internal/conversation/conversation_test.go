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
	if want := `{"origin":"","items":[],"goal":""}`; string(data) != want {
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

func TestReadReplyCutsAtUTF8BoundaryAt32KB(t *testing.T) {
	const cut = 32768
	tests := []struct {
		name string
		text string
		want string
	}{
		{"reply of exactly the cap is whole", strings.Repeat("a", cut), strings.Repeat("a", cut)},
		{"reply one byte over the cap is cut to the cap", strings.Repeat("a", cut+1), strings.Repeat("a", cut)},
		{"cut inside a multibyte rune drops the rune", strings.Repeat("a", cut-1) + "é" + "é",
			strings.Repeat("a", cut-1)},
		{"cut at a rune start keeps the whole cap", strings.Repeat("a", cut) + "é", strings.Repeat("a", cut)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			conv := mustRead(t, writeTranscript(t, replyLine(0, tc.text)), 10)
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

func TestReadResultStillCutsAt4KB(t *testing.T) {
	const cut = 4096
	text := strings.Repeat("a", cut+1)
	line := `{"type":"user","timestamp":"` + stampAt(0) + `","message":{"content":[` +
		`{"type":"tool_result","tool_use_id":"toolu_1","content":"` + text + `"}]}}`
	conv := mustRead(t, writeTranscript(t, line), 10)
	if len(conv.Items) != 1 || conv.Items[0].Kind != "result" {
		t.Fatalf("items = %+v, want one result item", conv.Items)
	}
	if got := conv.Items[0].Text; got != strings.Repeat("a", cut) {
		t.Fatalf("result text has %d bytes, want %d", len(got), cut)
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
		{
			"skips a compaction summary before the prompt",
			[]string{
				`{"type":"user","isCompactSummary":true,"message":{"content":"synthetic summary"}}`,
				`{"type":"user","message":{"content":"typed after compaction"}}`,
			},
			"typed after compaction",
		},
		{
			"skips a task notification before the prompt",
			[]string{
				taskLine(0, `"`+taskNotification("completed", "Synthetic summary")+`"`),
				`{"type":"user","message":{"content":"typed after task"}}`,
			},
			"typed after task",
		},
		{
			"skips a teammate message before the prompt",
			[]string{
				stringContentLine(t, teammateContent(teammateBlock("Synthetic summary", teammateRequest))),
				`{"type":"user","message":{"content":"typed after teammate"}}`,
			},
			"typed after teammate",
		},
		{
			"skips an interruption before the prompt",
			[]string{
				`{"type":"user","message":{"content":[{"type":"text","text":"[Request interrupted by user]"}]}}`,
				`{"type":"user","message":{"content":"typed after interruption"}}`,
			},
			"typed after interruption",
		},
		{
			"skips command output before the prompt",
			[]string{
				stringContentLine(t, commandOutput),
				`{"type":"user","message":{"content":"typed after output"}}`,
			},
			"typed after output",
		},
		{
			"reads a slash command opener as its name and args",
			[]string{
				stringContentLine(t, commandNameElem+"\n"+commandMessageElem+"\n"+commandArgsElem),
				replyLine(0, "Synthetic reply."),
			},
			"/synthetic one two",
		},
		{
			"strips a pasted pair from the opening prompt",
			[]string{
				stringContentLine(t, `Pasted <pasted_content id="1">synthetic</pasted_content id="1"> end`),
			},
			"Pasted synthetic end",
		},
		{
			"keeps a typed line quoting command output verbatim",
			[]string{humanLine(t, commandOutput)},
			commandOutput,
		},
		{
			"keeps a typed line holding an interruption string as the origin",
			[]string{
				`{"type":"user","promptSource":"typed","origin":{"kind":"human"},` +
					`"message":{"content":[{"type":"text","text":"[Request interrupted by user]"}]}}`,
			},
			"[Request interrupted by user]",
		},
		{
			"counts a queued typed message as the origin",
			[]string{attachmentLine(0, queuedAttachment(t, "Synthetic queued message"))},
			"Synthetic queued message",
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

// stringContentLine returns a user line with no promptSource and no origin whose string content is content.
func stringContentLine(t *testing.T, content string) string {
	t.Helper()
	raw, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}
	return `{"type":"user","timestamp":"` + stampAt(0) + `","message":{"role":"user","content":` +
		string(raw) + `}}`
}

func TestReadTeammateLineGivesTeammateNotice(t *testing.T) {
	line := stringContentLine(t, teammateContent(teammateBlock("Synthetic summary", teammateRequest)))
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
	if got := mustRead(t, writeTranscript(t, stringContentLine(t, content)), 10).Items; !reflect.DeepEqual(got, want) {
		t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
	}
}

func TestReadTeammateSummaryDecodesEntities(t *testing.T) {
	summary := "a &amp; b &lt;c&gt; &quot;d&quot; &apos;e&apos; &amp;lt;"
	content := teammateContent(teammateBlock(summary, teammateRequest))
	items := mustRead(t, writeTranscript(t, stringContentLine(t, content)), 10).Items
	if want := `a & b <c> "d" 'e' &lt;`; len(items) != 1 || items[0].Text != want {
		t.Fatalf("items = %+v, want one teammate notice with text %q", items, want)
	}
}

func TestReadTeammateSummaryCutsToRunes(t *testing.T) {
	summary := strings.Repeat("é", 301)
	content := teammateContent(teammateBlock(summary, teammateRequest))
	items := mustRead(t, writeTranscript(t, stringContentLine(t, content)), 10).Items
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
	line := stringContentLine(t, teammateContent(block))
	if got := mustRead(t, writeTranscript(t, line), 10).Items; !reflect.DeepEqual(got, want) {
		t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
	}
}

func TestReadTeammateWithoutSummaryHasEmptyText(t *testing.T) {
	content := teammateContent(teammateBlock("", teammateRequest))
	want := []Item{{Kind: "notice", Time: unixAt(0), Notice: "teammate"}}
	if got := mustRead(t, writeTranscript(t, stringContentLine(t, content)), 10).Items; !reflect.DeepEqual(got, want) {
		t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
	}
}

func TestReadTeammateIdlePingsGiveNoItem(t *testing.T) {
	content := teammateContent(teammateBlock("", teammateIdle), teammateBlock("", teammateIdle))
	if got := mustRead(t, writeTranscript(t, stringContentLine(t, content)), 10).Items; len(got) != 0 {
		t.Fatalf("Items = %+v, want none", got)
	}
}

func TestReadTeammateIdleAndSummarisedBlocksGiveNotice(t *testing.T) {
	content := teammateContent(
		teammateBlock("", teammateIdle),
		teammateBlock("Synthetic summary", teammateRequest))
	want := []Item{{Kind: "notice", Time: unixAt(0), Notice: "teammate", Text: "Synthetic summary"}}
	if got := mustRead(t, writeTranscript(t, stringContentLine(t, content)), 10).Items; !reflect.DeepEqual(got, want) {
		t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
	}
}

func TestReadTeammateManyBlocksGiveOneNotice(t *testing.T) {
	blocks := make([]string, 0, 14)
	for range 14 {
		blocks = append(blocks, teammateBlock("Synthetic summary", teammateRequest))
	}
	line := stringContentLine(t, teammateContent(blocks...))
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
			line := stringContentLine(t, tc.content)
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

// humanLine returns a user line that Claude Code marks as typed by the human, whose string content is content.
func humanLine(t *testing.T, content string) string {
	t.Helper()
	raw, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}
	return `{"type":"user","timestamp":"` + stampAt(0) + `","promptSource":"typed","origin":{"kind":"human"},` +
		`"message":{"role":"user","content":` + string(raw) + `}}`
}

const (
	commandNameElem    = "<command-name>/synthetic</command-name>"
	commandMessageElem = "<command-message>synthetic</command-message>"
	commandArgsElem    = "<command-args>one two</command-args>"
	commandOutput      = "<local-command-stdout>synthetic output</local-command-stdout>"
)

func TestReadSlashCommandLineGivesPromptOfNameAndArgs(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"name then message with args", "\n  " + commandNameElem + "\n  " + commandMessageElem + "\n  " +
			commandArgsElem, "/synthetic one two"},
		{"message then name with args", commandMessageElem + "\n  " + commandNameElem + "\n  " +
			commandArgsElem, "/synthetic one two"},
		{"name then message without args", commandNameElem + "\n" + commandMessageElem, "/synthetic"},
		{"message then name without args", commandMessageElem + "\n" + commandNameElem, "/synthetic"},
		{"empty args", commandNameElem + commandMessageElem + "<command-args></command-args>", "/synthetic"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := []Item{{Kind: "prompt", Time: unixAt(0), Text: tc.want}}
			got := mustRead(t, writeTranscript(t, stringContentLine(t, tc.content)), 10).Items
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}

func TestReadCommandRecordThatIsNotWholeOrSlashNamedStaysPrompt(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"name without slash", "<command-name>synthetic</command-name>\n" + commandMessageElem},
		{"text before the record", "Preface\n" + commandNameElem + "\n" + commandMessageElem},
		{"text after the record", commandNameElem + "\n" + commandMessageElem + "\nmore"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := []Item{{Kind: "prompt", Time: unixAt(0), Text: tc.content}}
			got := mustRead(t, writeTranscript(t, stringContentLine(t, tc.content)), 10).Items
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}

func TestReadCommandOutputLineGivesNoItem(t *testing.T) {
	if got := mustRead(t, writeTranscript(t, stringContentLine(t, commandOutput)), 10).Items; len(got) != 0 {
		t.Fatalf("Items = %+v, want none", got)
	}
}

func TestReadCommandOutputWithSurroundingTextStaysPrompt(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"text before the output", "Preface\n" + commandOutput},
		{"text after the output", commandOutput + "\nmore"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := []Item{{Kind: "prompt", Time: unixAt(0), Text: tc.content}}
			got := mustRead(t, writeTranscript(t, stringContentLine(t, tc.content)), 10).Items
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}

func TestReadTypedCommandLinesStayVerbatim(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"typed command record", commandNameElem + "\n" + commandMessageElem + "\n" + commandArgsElem},
		{"typed command output", commandOutput},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := []Item{{Kind: "prompt", Time: unixAt(0), Text: tc.content}}
			got := mustRead(t, writeTranscript(t, humanLine(t, tc.content)), 10).Items
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}

func TestReadPastedContentPairsLoseOnlyTheirTags(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"one pair", `before <pasted_content id="1">synthetic body</pasted_content id="1"> after`,
			"before synthetic body after"},
		{"two pairs with different ids",
			`<pasted_content id="1">one</pasted_content id="1"> and <pasted_content id="2">two</pasted_content id="2">`,
			"one and two"},
		{
			"nested pair",
			`<pasted_content id="a">x <pasted_content id="b">y</pasted_content id="b"> z</pasted_content id="a">`,
			"x y z",
		},
		{"unpaired opener", `<pasted_content id="1">synthetic body`, `<pasted_content id="1">synthetic body`},
		{"unpaired closer", `synthetic body</pasted_content id="1">`, `synthetic body</pasted_content id="1">`},
		{"mismatched ids", `<pasted_content id="1">body</pasted_content id="2">`,
			`<pasted_content id="1">body</pasted_content id="2">`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := []Item{{Kind: "prompt", Time: unixAt(0), Text: tc.want}}
			got := mustRead(t, writeTranscript(t, stringContentLine(t, tc.content)), 10).Items
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}

func TestReadTypedPastedContentPairIsStripped(t *testing.T) {
	content := `Pasted <pasted_content id="1">synthetic</pasted_content id="1"> end`
	want := []Item{{Kind: "prompt", Time: unixAt(0), Text: "Pasted synthetic end"}}
	if got := mustRead(t, writeTranscript(t, humanLine(t, content)), 10).Items; !reflect.DeepEqual(got, want) {
		t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
	}
}

func TestReadTypedReminderTagIsUnchanged(t *testing.T) {
	content := `<system-reminder>synthetic reminder</system-reminder> typed text`
	want := []Item{{Kind: "prompt", Time: unixAt(0), Text: content}}
	if got := mustRead(t, writeTranscript(t, humanLine(t, content)), 10).Items; !reflect.DeepEqual(got, want) {
		t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
	}
}

// queuedAttachment returns the attachment object of a queued message with the given prompt. Its command mode is
// prompt and its origin is the human.
func queuedAttachment(t *testing.T, prompt string) string {
	t.Helper()
	raw, err := json.Marshal(prompt)
	if err != nil {
		t.Fatalf("marshal prompt: %v", err)
	}
	return `{"type":"queued_command","commandMode":"prompt","origin":{"kind":"human"},"prompt":` + string(raw) + `}`
}

// attachmentLine returns an attachment line at second s whose attachment object is attachment.
func attachmentLine(s int, attachment string) string {
	return `{"type":"attachment","timestamp":"` + stampAt(s) + `","attachment":` + attachment + `}`
}

// userTextLine returns a user line at second s with no promptSource whose string content is text.
func userTextLine(t *testing.T, s int, text string) string {
	t.Helper()
	raw, err := json.Marshal(text)
	if err != nil {
		t.Fatalf("marshal text: %v", err)
	}
	return `{"type":"user","timestamp":"` + stampAt(s) + `","message":{"role":"user","content":` + string(raw) + `}}`
}

// typedTextLine returns a user line at second s that the person typed, with promptSource typed and origin human,
// whose string content is text.
func typedTextLine(t *testing.T, s int, text string) string {
	t.Helper()
	raw, err := json.Marshal(text)
	if err != nil {
		t.Fatalf("marshal text: %v", err)
	}
	return `{"type":"user","timestamp":"` + stampAt(s) + `","promptSource":"typed","origin":{"kind":"human"},` +
		`"message":{"role":"user","content":` + string(raw) + `}}`
}

// replyLine returns an assistant line at second s with one text block holding text.
func replyLine(s int, text string) string {
	return `{"type":"assistant","timestamp":"` + stampAt(s) + `","message":{"content":[{"type":"text","text":"` +
		text + `"}]}}`
}

func TestReadQueuedPromptGivesPromptInPlaceWithItsTime(t *testing.T) {
	path := writeTranscript(t,
		replyLine(0, "Synthetic reply one."),
		attachmentLine(1, queuedAttachment(t, "Synthetic queued message")),
		replyLine(2, "Synthetic reply two."),
	)
	want := []Item{
		{Kind: "reply", Time: unixAt(0), Text: "Synthetic reply one."},
		{Kind: "prompt", Time: unixAt(1), Text: "Synthetic queued message"},
		{Kind: "reply", Time: unixAt(2), Text: "Synthetic reply two."},
	}
	if got := mustRead(t, path, 10).Items; !reflect.DeepEqual(got, want) {
		t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
	}
}

func TestReadQueuedPromptStaysWhenLaterTypedLineRepeatsItsText(t *testing.T) {
	path := writeTranscript(t,
		attachmentLine(1, queuedAttachment(t, "Synthetic repeated message")),
		replyLine(2, "Synthetic reply."),
		typedTextLine(t, 3, "Synthetic repeated message"),
	)
	want := []Item{
		{Kind: "prompt", Time: unixAt(1), Text: "Synthetic repeated message"},
		{Kind: "reply", Time: unixAt(2), Text: "Synthetic reply."},
		{Kind: "prompt", Time: unixAt(3), Text: "Synthetic repeated message"},
	}
	if got := mustRead(t, path, 10).Items; !reflect.DeepEqual(got, want) {
		t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
	}
}

func TestReadQueuedPromptGivesTwoPromptsWhenNextLineRepeatsIt(t *testing.T) {
	path := writeTranscript(t,
		attachmentLine(1, queuedAttachment(t, "Synthetic repeated message")),
		typedTextLine(t, 2, "Synthetic repeated message"),
	)
	want := []Item{
		{Kind: "prompt", Time: unixAt(1), Text: "Synthetic repeated message"},
		{Kind: "prompt", Time: unixAt(2), Text: "Synthetic repeated message"},
	}
	if got := mustRead(t, path, 10).Items; !reflect.DeepEqual(got, want) {
		t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
	}
}

func TestReadQueuedPromptStaysWhenLaterUserTextDiffers(t *testing.T) {
	path := writeTranscript(t,
		attachmentLine(1, queuedAttachment(t, "Synthetic queued message")),
		userTextLine(t, 2, "Synthetic different message"),
	)
	want := []Item{
		{Kind: "prompt", Time: unixAt(1), Text: "Synthetic queued message"},
		{Kind: "prompt", Time: unixAt(2), Text: "Synthetic different message"},
	}
	if got := mustRead(t, path, 10).Items; !reflect.DeepEqual(got, want) {
		t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
	}
}

func TestReadQueuedPromptLimitCountsItAsOneItem(t *testing.T) {
	path := writeTranscript(t,
		replyLine(0, "Synthetic reply one."),
		attachmentLine(1, queuedAttachment(t, "Synthetic queued message")),
		replyLine(2, "Synthetic reply two."),
	)
	tests := []struct {
		name  string
		limit int
		want  []Item
	}{
		{"limit one gives the newest reply", 1, []Item{
			{Kind: "reply", Time: unixAt(2), Text: "Synthetic reply two."}}},
		{"limit two gives the queued prompt and the newest reply", 2, []Item{
			{Kind: "prompt", Time: unixAt(1), Text: "Synthetic queued message"},
			{Kind: "reply", Time: unixAt(2), Text: "Synthetic reply two."}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustRead(t, path, tc.limit).Items; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Items =\n%+v\nwant\n%+v", got, tc.want)
			}
		})
	}
}

func TestReadQueuedPromptCleansPastedPairsAndCapsText(t *testing.T) {
	pasted := `Pasted <pasted_content id="1">synthetic</pasted_content id="1"> end`
	long := strings.Repeat("é", 3000)
	path := writeTranscript(t,
		attachmentLine(0, queuedAttachment(t, pasted)),
		attachmentLine(1, queuedAttachment(t, long)),
	)
	want := []Item{
		{Kind: "prompt", Time: unixAt(0), Text: "Pasted synthetic end"},
		{Kind: "prompt", Time: unixAt(1), Text: cutText(long, maxTextBytes)},
	}
	if got := mustRead(t, path, 10).Items; !reflect.DeepEqual(got, want) {
		t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
	}
}

func TestReadOtherAttachmentsGiveNothing(t *testing.T) {
	tests := []struct {
		name       string
		attachment string
	}{
		{"task notification command mode", `{"type":"queued_command","commandMode":"task-notification",` +
			`"origin":{"kind":"human"},"prompt":"Synthetic task text"}`},
		{"auto-continuation origin", `{"type":"queued_command","commandMode":"prompt",` +
			`"origin":{"kind":"auto-continuation"},"prompt":"Synthetic continued text"}`},
		{"peer origin", `{"type":"queued_command","commandMode":"prompt",` +
			`"origin":{"kind":"peer"},"prompt":"Synthetic peer text"}`},
		{"no origin", `{"type":"queued_command","commandMode":"prompt","prompt":"Synthetic bare text"}`},
		{"not a queued command", `{"type":"hook_output","commandMode":"prompt",` +
			`"origin":{"kind":"human"},"prompt":"Synthetic hook text"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTranscript(t, attachmentLine(0, tc.attachment))
			if got := mustRead(t, path, 10).Items; len(got) != 0 {
				t.Fatalf("Items = %+v, want none", got)
			}
		})
	}
}

func TestReadMetaAndSidechainAttachmentsGiveNothing(t *testing.T) {
	prompt := queuedAttachment(t, "Synthetic queued message")
	tests := []struct {
		name string
		flag string
	}{
		{"meta", `"isMeta":true,`},
		{"sidechain", `"isSidechain":true,`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			line := `{"type":"attachment",` + tc.flag + `"timestamp":"` + stampAt(0) +
				`","attachment":` + prompt + `}`
			if got := mustRead(t, writeTranscript(t, line), 10).Items; len(got) != 0 {
				t.Fatalf("Items = %+v, want none", got)
			}
		})
	}
}
