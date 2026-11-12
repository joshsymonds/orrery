package conversation

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// followPrompt returns a human prompt line at second s with the given text.
func followPrompt(s int, text string) string {
	return `{"type":"user","timestamp":"` + stampAt(s) + `","message":{"role":"user","content":"` + text + `"}}`
}

// followSummary returns a compaction summary line at second s.
func followSummary(s int) string {
	return `{"type":"user","timestamp":"` + stampAt(s) + `","isCompactSummary":true,` +
		`"message":{"role":"user","content":"synthetic summary"}}`
}

// sinceFollow calls Since from offset from and fails the test on error.
func sinceFollow(t *testing.T, path string, from int64) Follow {
	t.Helper()
	follow, err := Since(path, from)
	if err != nil {
		t.Fatalf("Since(%d): %v", from, err)
	}
	return follow
}

// fileSize returns the size of the file at path.
func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat transcript: %v", err)
	}
	return info.Size()
}

func TestSinceFollowsGrowingTranscript(t *testing.T) {
	path := writeTranscript(t, followPrompt(0, "open the file"))
	first := sinceFollow(t, path, 0)

	appendText(t, path, assistantUsageLine(1, `{"input_tokens":100}`)+"\n"+followPrompt(2, "next")+"\n")
	second := sinceFollow(t, path, first.End)

	want := mustRead(t, path, 100).Items
	if got := slices.Concat(first.Items, second.Items); !reflect.DeepEqual(got, want) {
		t.Fatalf("Items =\n%+v\nwant\n%+v", got, want)
	}
	if !reflect.DeepEqual(second.Items, want[1:]) {
		t.Fatalf("second Items =\n%+v\nwant only the appended items\n%+v", second.Items, want[1:])
	}
	if size := fileSize(t, path); second.End != size {
		t.Fatalf("End = %d, want file size %d", second.End, size)
	}
}

func TestSinceLeavesPartialLineUnread(t *testing.T) {
	complete := followPrompt(0, "first") + "\n"
	path := writeTranscript(t, followPrompt(0, "first"))
	partial := followPrompt(1, "second")
	half := len(partial) / 2
	appendText(t, path, partial[:half])

	first := sinceFollow(t, path, 0)
	wantFirst := []Item{{Kind: "prompt", Time: unixAt(0), Text: "first"}}
	if !reflect.DeepEqual(first.Items, wantFirst) {
		t.Fatalf("first Items = %+v, want %+v", first.Items, wantFirst)
	}
	if first.End != int64(len(complete)) {
		t.Fatalf("first End = %d, want %d (just past the complete line)", first.End, len(complete))
	}

	appendText(t, path, partial[half:]+"\n")
	second := sinceFollow(t, path, first.End)
	wantSecond := []Item{{Kind: "prompt", Time: unixAt(1), Text: "second"}}
	if !reflect.DeepEqual(second.Items, wantSecond) {
		t.Fatalf("second Items = %+v, want %+v", second.Items, wantSecond)
	}
	if size := fileSize(t, path); second.End != size {
		t.Fatalf("second End = %d, want file size %d", second.End, size)
	}
}

func TestReadEndMatchesReadAndStopsAtLastCompleteLine(t *testing.T) {
	complete := followPrompt(0, "open") + "\n" + assistantUsageLine(1, `{"input_tokens":50}`) + "\n"
	longPartial := `{"pad":"` + strings.Repeat("a", 2*TailBlockSize)
	tests := []struct {
		name    string
		content string
	}{
		{"ends with newline", complete},
		{"partial trailing line", complete + followPrompt(2, "partial")[:20]},
		{"partial line longer than a block", complete + longPartial},
		{"no newline at all", followPrompt(0, "open")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "transcript.jsonl")
			writeFile(t, path, tc.content)

			conv, end, err := ReadEnd(path, 100)
			if err != nil {
				t.Fatalf("ReadEnd: %v", err)
			}
			want := mustRead(t, path, 100)
			if !reflect.DeepEqual(conv, want) {
				t.Fatalf("ReadEnd conversation =\n%+v\nwant Read's\n%+v", conv, want)
			}
			if wantEnd := int64(strings.LastIndex(tc.content, "\n") + 1); end != wantEnd {
				t.Fatalf("ReadEnd offset = %d, want %d", end, wantEnd)
			}
		})
	}
}

func TestSinceRejectsOffsetsThatAreNotLineStarts(t *testing.T) {
	first := followPrompt(0, "first") + "\n"
	path := writeTranscript(t, followPrompt(0, "first"), followPrompt(1, "second"))
	size := fileSize(t, path)

	for _, from := range []int64{0, int64(len(first)), size} {
		if _, err := Since(path, from); err != nil {
			t.Fatalf("Since(%d): %v", from, err)
		}
	}
	for _, from := range []int64{-1, 3, int64(len(first)) + 5, size + 1} {
		if _, err := Since(path, from); !errors.Is(err, ErrOffset) {
			t.Errorf("Since(%d) error = %v, want ErrOffset", from, err)
		}
	}
}

func TestSinceAtEndGivesNothing(t *testing.T) {
	path := writeTranscript(t, followPrompt(0, "open"))
	size := fileSize(t, path)

	follow := sinceFollow(t, path, size)
	if follow.Items == nil || len(follow.Items) != 0 {
		t.Fatalf("Items = %+v, want a non-nil empty slice", follow.Items)
	}
	if follow.End != size || follow.HasTokens {
		t.Fatalf("End = %d, HasTokens = %t, want %d and false", follow.End, follow.HasTokens, size)
	}
}

func TestSinceCompactionTakesBoundaryWithinCall(t *testing.T) {
	open := followPrompt(0, "open")
	boundary := compactBoundary(1, "manual", "5000")
	path := writeTranscript(t, open, boundary, followSummary(2))

	whole := sinceFollow(t, path, 0)
	wantWhole := Item{Kind: "compaction", Time: unixAt(2), Text: "synthetic summary", Trigger: "manual", Tokens: 5000}
	if got := whole.Items[len(whole.Items)-1]; !reflect.DeepEqual(got, wantWhole) {
		t.Fatalf("compaction = %+v, want %+v", got, wantWhole)
	}

	after := int64(len(open + "\n" + boundary + "\n"))
	tail := sinceFollow(t, path, after)
	wantTail := []Item{{Kind: "compaction", Time: unixAt(2), Text: "synthetic summary"}}
	if !reflect.DeepEqual(tail.Items, wantTail) {
		t.Fatalf("tail Items = %+v, want %+v (no boundary read, so no trigger or tokens)", tail.Items, wantTail)
	}
}

func TestSinceContextFollowsNewestQualifyingLine(t *testing.T) {
	tests := []struct {
		name   string
		lines  []string
		tokens int64
		has    bool
	}{
		{
			name: "boundary post tokens when newest",
			lines: []string{
				assistantUsageLine(0, `{"input_tokens":100}`),
				boundaryWithPost(1, "5000", "900"),
			},
			tokens: 900,
			has:    true,
		},
		{
			name: "assistant after boundary wins",
			lines: []string{
				boundaryWithPost(0, "5000", "900"),
				assistantUsageLine(1, `{"input_tokens":100,"cache_read_input_tokens":200}`),
			},
			tokens: 300,
			has:    true,
		},
		{
			name:   "zero usage gives none",
			lines:  []string{assistantUsageLine(0, `{"input_tokens":0}`)},
			tokens: 0,
			has:    false,
		},
		{
			name:   "no lines",
			lines:  nil,
			tokens: 0,
			has:    false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTranscript(t, tc.lines...)
			follow := sinceFollow(t, path, 0)
			if follow.Tokens != tc.tokens || follow.HasTokens != tc.has {
				t.Fatalf("Tokens = %d, HasTokens = %t, want %d and %t",
					follow.Tokens, follow.HasTokens, tc.tokens, tc.has)
			}
		})
	}
}
