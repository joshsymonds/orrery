package conversation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// transcriptBuilder writes the lines of a synthetic transcript and records where each line starts, so a test can
// check the offsets that Older and ReadEnd report.
type transcriptBuilder struct {
	buf strings.Builder
}

// line appends text as one line and returns the offset the line starts at.
func (b *transcriptBuilder) line(text string) int64 {
	start := int64(b.buf.Len())
	b.buf.WriteString(text + "\n")
	return start
}

// blank appends an empty line.
func (b *transcriptBuilder) blank() {
	b.buf.WriteString("\n")
}

// write stores the transcript in a new file in t.TempDir and returns its path.
func (b *transcriptBuilder) write(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(b.buf.String()), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

// olderPrompt returns a user line whose plain content is the prompt text.
func olderPrompt(text string) string {
	return fmt.Sprintf(`{"type":"user","message":{"content":%q}}`, text)
}

// olderReply returns an assistant line with one text block holding text.
func olderReply(text string) string {
	return fmt.Sprintf(`{"type":"assistant","message":{"content":[{"type":"text","text":%q}]}}`, text)
}

// walkOlder reads the pages of path backward from before, limit items at a time, until a page says no more, and
// returns the items of every page, oldest first. starts maps the text of each item to the start of its transcript
// line. It fails the test when a page's Oldest is not the start of the line of its oldest item, when a page holds no
// item and yet says more or names an offset, and when the walk does not end within maxPages pages.
func walkOlder(t *testing.T, path string, before int64, limit int, starts map[string]int64) []Item {
	t.Helper()
	const maxPages = 100000
	var pages [][]Item
	for range maxPages {
		page, err := Older(path, before, limit)
		if err != nil {
			t.Fatalf("Older(%d, %d): %v", before, limit, err)
		}
		if len(page.Items) == 0 {
			if page.More || page.Oldest != 0 {
				t.Fatalf(
					"empty page before %d: more=%v oldest=%d, want no more and oldest 0",
					before,
					page.More,
					page.Oldest,
				)
			}
			break
		}
		if want := starts[page.Items[0].Text]; page.Oldest != want {
			t.Fatalf(
				"page before %d: oldest = %d, want the start %d of its oldest item's line",
				before,
				page.Oldest,
				want,
			)
		}
		pages = append([][]Item{page.Items}, pages...)
		if !page.More {
			break
		}
		before = page.Oldest
	}
	var items []Item
	for _, page := range pages {
		items = append(items, page...)
	}
	return items
}

// olderSyntheticTranscript writes a transcript of more than 4 MiB: an opening prompt, then replies, with a compaction
// before the 4 MiB tail and empty lines through it. It returns the path, the items in transcript order, and the start
// of each item's line.
func olderSyntheticTranscript(t *testing.T) (string, []Item, map[string]int64) {
	t.Helper()
	const (
		replies     = 4600
		padding     = 1000
		compactAt   = 300
		emptyEveryN = 7
	)
	var b transcriptBuilder
	var items []Item
	starts := map[string]int64{}
	add := func(item Item, start int64) {
		items = append(items, item)
		starts[item.Text] = start
	}
	add(Item{Kind: kindPrompt, Text: "synthetic opening prompt"}, b.line(olderPrompt("synthetic opening prompt")))
	pad := strings.Repeat("x", padding)
	for n := 1; n <= replies; n++ {
		if n%emptyEveryN == 0 {
			b.blank()
		}
		if n == compactAt {
			b.line(`{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"auto",` +
				`"preTokens":1234,"postTokens":900}}`)
			summary := fmt.Sprintf("synthetic summary %d", n)
			add(Item{Kind: kindCompaction, Text: summary, Trigger: "auto", Tokens: 1234},
				b.line(fmt.Sprintf(`{"type":"user","isCompactSummary":true,"message":{"content":%q}}`, summary)))
		}
		text := fmt.Sprintf("reply %05d %s", n, pad)
		add(Item{Kind: kindReply, Text: text}, b.line(olderReply(text)))
	}
	return b.write(t), items, starts
}

// TestOlderPagesBackFromResetOldestToFirstItem writes a transcript larger than the 4 MiB tail. A ReadEnd reset holds
// the newest items; Older pages back from its Oldest until the first item. Together the reset and the pages hold every
// item once, in order, and the last page says no more.
func TestOlderPagesBackFromResetOldestToFirstItem(t *testing.T) {
	t.Parallel()
	path, all, starts := olderSyntheticTranscript(t)
	if info, err := os.Stat(path); err != nil || info.Size() <= MaxTailBytes {
		t.Fatalf("transcript size: %v (err %v), want more than %d bytes", info, err, MaxTailBytes)
	}

	const resetLimit = 50
	reset, _, err := ReadEnd(path, resetLimit)
	if err != nil {
		t.Fatalf("ReadEnd: %v", err)
	}
	if want := all[len(all)-resetLimit:]; !reflect.DeepEqual(reset.Items, want) {
		t.Fatalf("reset items: got %d, want the newest %d", len(reset.Items), len(want))
	}
	if reset.Oldest != starts[reset.Items[0].Text] {
		t.Fatalf("reset oldest = %d, want %d", reset.Oldest, starts[reset.Items[0].Text])
	}

	older := walkOlder(t, path, reset.Oldest, 37, starts)
	if want := all[:len(all)-resetLimit]; !reflect.DeepEqual(older, want) {
		t.Fatalf("older items: got %d, want the %d items before the reset", len(older), len(want))
	}
}

// TestOlderPagesWithLimitOneReachEveryItem walks the same transcript one item at a time, so every line boundary of the
// walk is a page boundary, and checks that the compaction's trigger and tokens come with it.
func TestOlderPagesWithLimitOneReachEveryItem(t *testing.T) {
	t.Parallel()
	path, all, starts := olderSyntheticTranscript(t)

	got := walkOlder(t, path, fileLen(t, path), 1, starts)
	if !reflect.DeepEqual(got, all) {
		t.Fatalf("items: got %d, want all %d in order", len(got), len(all))
	}
	for _, item := range got {
		if item.Kind == kindCompaction && (item.Trigger != "auto" || item.Tokens != 1234) {
			t.Fatalf("compaction %q: trigger %q tokens %d, want auto 1234", item.Text, item.Trigger, item.Tokens)
		}
	}
}

// fileLen returns the size of the file at path.
func fileLen(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	return info.Size()
}

// TestOlderReadEndUsageSearchPassesLimitWithoutSkipping makes the newest lines carry no usage, so ReadEnd reads past
// its limit to find the usage line older than them. The reset keeps only the newest items, and its Oldest must be the
// line of the oldest item it keeps, not the usage line the read stopped at, or the items between would be skipped.
func TestOlderReadEndUsageSearchPassesLimitWithoutSkipping(t *testing.T) {
	t.Parallel()
	var b transcriptBuilder
	starts := map[string]int64{}
	var all []Item
	record := func(item Item, start int64) {
		all = append(all, item)
		starts[item.Text] = start
	}
	record(Item{Kind: kindPrompt, Text: "p0"}, b.line(olderPrompt("p0")))
	record(Item{Kind: kindReply, Text: "usage reply"}, b.line(
		`{"type":"assistant","message":{"content":[{"type":"text","text":"usage reply"}],`+
			`"usage":{"input_tokens":4321}}}`))
	for n := 1; n <= 9; n++ {
		text := fmt.Sprintf("p%d", n)
		record(Item{Kind: kindPrompt, Text: text}, b.line(olderPrompt(text)))
	}
	record(Item{Kind: kindReply, Text: "newest reply"}, b.line(olderReply("newest reply")))
	path := b.write(t)

	const resetLimit = 3
	reset, _, err := ReadEnd(path, resetLimit)
	if err != nil {
		t.Fatalf("ReadEnd: %v", err)
	}
	if reset.Context.Tokens != 4321 {
		t.Fatalf("context tokens = %d, want the usage line's 4321 found past the limit", reset.Context.Tokens)
	}
	if want := all[len(all)-resetLimit:]; !reflect.DeepEqual(reset.Items, want) {
		t.Fatalf("reset items = %+v, want %+v", reset.Items, want)
	}
	if reset.Oldest != starts["p8"] {
		t.Fatalf("reset oldest = %d, want the start %d of p8's line", reset.Oldest, starts["p8"])
	}

	older := walkOlder(t, path, reset.Oldest, 2, starts)
	if want := all[:len(all)-resetLimit]; !reflect.DeepEqual(older, want) {
		t.Fatalf("older items = %+v, want %+v", older, want)
	}
}

// TestOlderPagesTakeWholeLines checks that a page never takes part of a line: a line that would take the page past its
// limit is left whole for the next page, and a page with no item takes its newest line whatever its size.
func TestOlderPagesTakeWholeLines(t *testing.T) {
	t.Parallel()
	var b transcriptBuilder
	starts := map[string]int64{}
	b.line(olderPrompt("a0"))
	starts["a0"] = 0
	threeBlocks := `{"type":"assistant","message":{"content":[` +
		`{"type":"text","text":"t1"},{"type":"text","text":"t2"},{"type":"text","text":"t3"}]}}`
	starts["t1"] = b.line(threeBlocks)
	starts["b0"] = b.line(olderPrompt("b0"))
	starts["u1"] = b.line(`{"type":"assistant","message":{"content":[` +
		`{"type":"text","text":"u1"},{"type":"text","text":"u2"}]}}`)
	path := b.write(t)

	reset, _, err := ReadEnd(path, 3)
	if err != nil {
		t.Fatalf("ReadEnd: %v", err)
	}
	if got := texts(reset.Items); !reflect.DeepEqual(got, []string{"b0", "u1", "u2"}) {
		t.Fatalf("reset items = %q, want b0 u1 u2: the three-block line would take the page past 3", got)
	}
	if reset.Oldest != starts["b0"] {
		t.Fatalf("reset oldest = %d, want b0's line start %d", reset.Oldest, starts["b0"])
	}

	page, err := Older(path, reset.Oldest, 3)
	if err != nil {
		t.Fatalf("Older: %v", err)
	}
	if got := texts(page.Items); !reflect.DeepEqual(got, []string{"t1", "t2", "t3"}) {
		t.Fatalf("second page = %q, want the whole three-block line", got)
	}
	if page.Oldest != starts["t1"] || !page.More {
		t.Fatalf("second page oldest=%d more=%v, want %d and more", page.Oldest, page.More, starts["t1"])
	}

	whole, _, err := ReadEnd(path, 1)
	if err != nil {
		t.Fatalf("ReadEnd limit 1: %v", err)
	}
	if got := texts(whole.Items); !reflect.DeepEqual(got, []string{"u1", "u2"}) {
		t.Fatalf("limit-1 reset = %q, want the newest line whole", got)
	}

	walked := walkOlder(t, path, reset.Oldest, 3, starts)
	if got := texts(walked); !reflect.DeepEqual(got, []string{"a0", "t1", "t2", "t3"}) {
		t.Fatalf("walk before the reset = %q, want a0 then the three-block line", got)
	}
}

// texts returns the text of each item.
func texts(items []Item) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Text)
	}
	return out
}

// TestOlderStartsAfterEmptyLines reads from line starts that follow empty lines, and refuses offsets inside a line.
func TestOlderStartsAfterEmptyLines(t *testing.T) {
	t.Parallel()
	var b transcriptBuilder
	b.line(olderPrompt("first"))
	b.blank()
	b.blank()
	before := b.line(olderPrompt("second"))
	b.blank()
	b.line(olderPrompt("third"))
	path := b.write(t)

	page, err := Older(path, before, 30)
	if err != nil {
		t.Fatalf("Older after empty lines: %v", err)
	}
	if got := texts(page.Items); !reflect.DeepEqual(got, []string{"first"}) || page.Oldest != 0 || page.More {
		t.Fatalf("page before the second line = %q oldest %d more %v, want first at 0 and no more",
			got, page.Oldest, page.More)
	}

	for _, offset := range []int64{before + 1, fileLen(t, path) + 1, -1} {
		if _, err := Older(path, offset, 30); !errors.Is(err, ErrOffset) {
			t.Fatalf("Older(%d): error %v, want ErrOffset", offset, err)
		}
	}
	if page, err := Older(path, 0, 30); err != nil || len(page.Items) != 0 || page.More {
		t.Fatalf("Older at 0 = %+v, %v; want an empty page with no more", page, err)
	}
}

// TestOlderRejectsLimitBelowOne refuses a limit that would read no item.
func TestOlderRejectsLimitBelowOne(t *testing.T) {
	t.Parallel()
	var b transcriptBuilder
	b.line(olderPrompt("only"))
	path := b.write(t)
	if _, err := Older(path, fileLen(t, path), 0); err == nil {
		t.Fatal("Older with limit 0: got no error")
	}
}

// TestOlderBoundaryReadPastLimitKeepsCompactionTrigger puts a compaction's boundary older than the page's limit. The
// page must still take the boundary's trigger and tokens, read past the limit.
func TestOlderBoundaryReadPastLimitKeepsCompactionTrigger(t *testing.T) {
	t.Parallel()
	var b transcriptBuilder
	b.line(`{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"manual",` +
		`"preTokens":777,"postTokens":90}}`)
	starts := map[string]int64{}
	starts["summary"] = b.line(`{"type":"user","isCompactSummary":true,"message":{"content":"summary"}}`)
	starts["after"] = b.line(olderPrompt("after"))
	path := b.write(t)

	page, err := Older(path, fileLen(t, path), 2)
	if err != nil {
		t.Fatalf("Older: %v", err)
	}
	want := []Item{
		{Kind: kindCompaction, Text: "summary", Trigger: "manual", Tokens: 777},
		{Kind: kindPrompt, Text: "after"},
	}
	if !reflect.DeepEqual(page.Items, want) {
		t.Fatalf("items = %+v, want %+v", page.Items, want)
	}
	if page.Oldest != starts["summary"] || page.More {
		t.Fatalf("oldest = %d more = %v, want %d and no more", page.Oldest, page.More, starts["summary"])
	}
}
