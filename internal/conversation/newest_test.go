package conversation

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fixed transcript stamps and their unix seconds.
const (
	stamp0800 = "2026-11-10T08:00:00Z"
	stamp0900 = "2026-11-10T09:00:00Z"
	stamp0905 = "2026-11-10T09:05:00Z"
	stamp0910 = "2026-11-10T09:10:00Z"
	stamp0915 = "2026-11-10T09:15:00Z"
	stamp0920 = "2026-11-10T09:20:00Z"

	unix0800 int64 = 1794297600
	unix0900 int64 = 1794301200
	unix0905 int64 = 1794301500
	unix0910 int64 = 1794301800
	unix0920 int64 = 1794302400
)

// newestReply returns an assistant line whose one text block is text.
func newestReply(stamp, text string) string {
	return `{"type":"assistant","timestamp":"` + stamp + `","message":{"content":[{"type":"text","text":"` +
		text + `"}]}}`
}

// newestQuestion returns an assistant line whose one tool call is an AskUserQuestion.
func newestQuestion(stamp string) string {
	return `{"type":"assistant","timestamp":"` + stamp + `","message":{"content":[{"type":"tool_use","id":"q1",` +
		`"name":"AskUserQuestion","input":{"questions":[{"question":"Which one?"}]}}]}}`
}

// newestTool returns an assistant line whose one tool call is a Bash call.
func newestTool(stamp string) string {
	return `{"type":"assistant","timestamp":"` + stamp + `","message":{"content":[{"type":"tool_use","id":"t1",` +
		`"name":"Bash","input":{"command":"ls"}}]}}`
}

// newestPrompt returns a user line that the person typed.
func newestPrompt(stamp string) string {
	return `{"type":"user","timestamp":"` + stamp + `","message":{"content":"synthetic prompt"}}`
}

// newestResult returns a user line that holds one tool result.
func newestResult(stamp string) string {
	return `{"type":"user","timestamp":"` + stamp + `","message":{"content":[{"type":"tool_result",` +
		`"tool_use_id":"t1","content":"ok"}]}}`
}

// newestNotice returns a user line that Claude Code marks as a compaction notice.
func newestNotice(stamp string) string {
	return `{"type":"user","timestamp":"` + stamp + `","isCompactSummary":true,"message":{"content":"summary"}}`
}

// newestInterruption returns a user line whose one text block is an interruption text.
func newestInterruption(stamp, text string) string {
	return `{"type":"user","timestamp":"` + stamp + `","message":{"content":[{"type":"text","text":"` +
		text + `"}]}}`
}

// assertNewest fails the test unless newest reports want for path.
func assertNewest(t *testing.T, newest *Newest, path string, want int64) {
	t.Helper()
	got, err := newest.Time(path)
	if err != nil {
		t.Fatalf("Time: %v", err)
	}
	if got != want {
		t.Fatalf("Time = %d, want %d", got, want)
	}
}

func TestNewestFollowsReplyAndQuestion(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  int64
	}{
		{name: "reply", lines: []string{newestReply(stamp0900, "done")}, want: unix0900},
		{name: "question", lines: []string{newestQuestion(stamp0905)}, want: unix0905},
		{
			name:  "newest of reply and question",
			lines: []string{newestReply(stamp0900, "done"), newestQuestion(stamp0905)},
			want:  unix0905,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTranscript(t, tt.lines...)
			assertNewest(t, NewNewest(), path, tt.want)
		})
	}
}

func TestNewestIgnoresToolPromptResultAndNotice(t *testing.T) {
	path := writeTranscript(t,
		newestReply(stamp0900, "done"),
		newestTool(stamp0905),
		newestPrompt(stamp0910),
		newestResult(stamp0915),
		newestNotice(stamp0920),
	)
	assertNewest(t, NewNewest(), path, unix0900)
}

func TestNewestIgnoresSidechainAndMetaReplies(t *testing.T) {
	sidechain := strings.Replace(newestReply(stamp0910, "side"), `"type":"assistant",`,
		`"type":"assistant","isSidechain":true,`, 1)
	meta := strings.Replace(newestReply(stamp0915, "meta"), `"type":"assistant",`,
		`"type":"assistant","isMeta":true,`, 1)
	path := writeTranscript(t, newestReply(stamp0900, "done"), sidechain, meta)
	assertNewest(t, NewNewest(), path, unix0900)
}

func TestNewestIsZeroWithoutReplyOrQuestion(t *testing.T) {
	path := writeTranscript(t, newestPrompt(stamp0900), newestTool(stamp0905), newestResult(stamp0910))
	assertNewest(t, NewNewest(), path, 0)
}

func TestNewestReadsOnlyAppendedBytesAfterFirstCall(t *testing.T) {
	path := writeTranscript(t, newestReply(stamp0900, "first"))
	newest := NewNewest()
	assertNewest(t, newest, path, unix0900)

	overwriteInPlace(t, path, stamp0900, stamp0800)
	assertNewest(t, newest, path, unix0900)
	assertNewest(t, NewNewest(), path, unix0800)

	appendText(t, path, newestReply(stamp0910, "second")+"\n")
	assertNewest(t, newest, path, unix0910)
}

func TestNewestCountsTrailingLineOnceComplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	writeFile(t, path, newestReply(stamp0900, "first")+"\n")
	newest := NewNewest()
	assertNewest(t, newest, path, unix0900)

	reply := newestReply(stamp0910, "second")
	half := len(reply) / 2
	appendText(t, path, reply[:half])
	assertNewest(t, newest, path, unix0900)

	appendText(t, path, reply[half:]+"\n")
	assertNewest(t, newest, path, unix0910)
}

func TestNewestRescansShrunkFile(t *testing.T) {
	path := writeTranscript(t, newestReply(stamp0920, "a much longer first reply"))
	newest := NewNewest()
	assertNewest(t, newest, path, unix0920)

	writeFile(t, path, newestReply(stamp0905, "B")+"\n")
	assertNewest(t, newest, path, unix0905)
}

func TestNewestRescansReplacedFile(t *testing.T) {
	path := writeTranscript(t, newestReply(stamp0920, "first"))
	newest := NewNewest()
	assertNewest(t, newest, path, unix0920)

	replacement := filepath.Join(filepath.Dir(path), "replacement.jsonl")
	writeFile(t, replacement, newestReply(stamp0905, "replacement")+"\n")
	if err := os.Rename(replacement, path); err != nil {
		t.Fatalf("rename replacement: %v", err)
	}
	assertNewest(t, newest, path, unix0905)
}

// assertInterrupted fails the test unless newest reports want as the newest interruption of path.
func assertInterrupted(t *testing.T, newest *Newest, path string, want int64) {
	t.Helper()
	got, err := newest.Interrupted(path)
	if err != nil {
		t.Fatalf("Interrupted: %v", err)
	}
	if got != want {
		t.Fatalf("Interrupted = %d, want %d", got, want)
	}
}

func TestNewestInterruptedFollowsLaterInterruption(t *testing.T) {
	path := writeTranscript(t,
		newestInterruption(stamp0900, interruptedText),
		newestInterruption(stamp0910, interruptedToolText),
	)
	assertInterrupted(t, NewNewest(), path, unix0910)
}

func TestNewestInterruptedIsZeroWithoutInterruption(t *testing.T) {
	path := writeTranscript(t, newestReply(stamp0900, "done"), newestPrompt(stamp0905))
	assertInterrupted(t, NewNewest(), path, 0)
}

func TestNewestInterruptedIgnoresSidechainAndMeta(t *testing.T) {
	sidechain := strings.Replace(newestInterruption(stamp0915, interruptedText), `"type":"user",`,
		`"type":"user","isSidechain":true,`, 1)
	meta := strings.Replace(newestInterruption(stamp0920, interruptedText), `"type":"user",`,
		`"type":"user","isMeta":true,`, 1)
	path := writeTranscript(t, newestInterruption(stamp0900, interruptedText), sidechain, meta)
	assertInterrupted(t, NewNewest(), path, unix0900)
}

func TestNewestInterruptedReadsAppendedLinesAfterTimeCall(t *testing.T) {
	path := writeTranscript(t, newestInterruption(stamp0900, interruptedText))
	newest := NewNewest()
	assertInterrupted(t, newest, path, unix0900)

	appendText(t, path, newestReply(stamp0905, "later")+"\n"+newestInterruption(stamp0910, interruptedText)+"\n")
	assertNewest(t, newest, path, unix0905)
	assertInterrupted(t, newest, path, unix0910)
}

func TestNewestMissingFileWrapsErrNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.jsonl")
	got, err := NewNewest().Time(path)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Time error = %v, want one wrapping fs.ErrNotExist", err)
	}
	if got != 0 {
		t.Fatalf("Time = %d on error, want 0", got)
	}
}
