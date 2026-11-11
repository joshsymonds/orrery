package server_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// Fixed transcript stamps and their unix seconds, for the newest_reply fixtures.
const (
	newestStamp0800 = "2026-11-10T08:00:00Z"
	newestStamp0900 = "2026-11-10T09:00:00Z"
	newestStamp0905 = "2026-11-10T09:05:00Z"
	newestStamp0910 = "2026-11-10T09:10:00Z"
	newestStamp0915 = "2026-11-10T09:15:00Z"
	newestStamp0920 = "2026-11-10T09:20:00Z"
	newestStamp0930 = "2026-11-10T09:30:00Z"

	newestUnix0900 float64 = 1794301200
	newestUnix0905 float64 = 1794301500
	newestUnix0920 float64 = 1794302400
)

// newestReplyLine returns an assistant line whose one text block is an answer stamped stamp.
func newestReplyLine(stamp string) string {
	return `{"type":"assistant","timestamp":"` + stamp + `","message":{"content":[{"type":"text","text":"done"}]}}`
}

// newestQuestionLine returns an assistant line whose one tool call is an AskUserQuestion stamped stamp.
func newestQuestionLine(stamp string) string {
	return `{"type":"assistant","timestamp":"` + stamp + `","message":{"content":[{"type":"tool_use","id":"q1",` +
		`"name":"AskUserQuestion","input":{"questions":[{"question":"Which one?"}]}}]}}`
}

// newestToolLine returns an assistant line whose one tool call is a Bash call stamped stamp.
func newestToolLine(stamp string) string {
	return `{"type":"assistant","timestamp":"` + stamp + `","message":{"content":[{"type":"tool_use","id":"t1",` +
		`"name":"Bash","input":{"command":"ls"}}]}}`
}

// newestPromptLine returns a user line that the person typed, stamped stamp.
func newestPromptLine(stamp string) string {
	return `{"type":"user","timestamp":"` + stamp + `","message":{"content":"synthetic prompt"}}`
}

// writeNewestTranscript writes lines, each newline-terminated, to a new file in t.TempDir and returns its path.
func writeNewestTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

// paneNewest returns the newest_reply of pane in the GET /v1/panes answer through handler.
func paneNewest(t *testing.T, handler http.Handler, pane string) float64 {
	t.Helper()
	rec := get(t, handler, "/v1/panes", allowedLogin)
	assertStatus(t, rec, http.StatusOK)
	for _, obj := range decodeObjects(t, rec.Body.Bytes()) {
		if obj["id"] == pane {
			newest, ok := obj["newest_reply"].(float64)
			if !ok {
				t.Fatalf("pane %s newest_reply = %v, want a number", pane, obj["newest_reply"])
			}
			return newest
		}
	}
	t.Fatalf("pane %s is not in the list %s", pane, rec.Body.String())
	return 0
}

func TestServePanesNewestReply(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	absent := filepath.Join(t.TempDir(), "absent.jsonl")
	withReply := writeNewestTranscript(t, newestReplyLine(newestStamp0900))
	cases := []struct {
		name    string
		options map[string]string
		want    float64
	}{
		{
			name: "reply time ignores later tool call and prompt",
			options: liveOptions(writeNewestTranscript(t,
				newestReplyLine(newestStamp0900),
				newestToolLine(newestStamp0910),
				newestPromptLine(newestStamp0915))),
			want: newestUnix0900,
		},
		{
			name: "question time counts",
			options: liveOptions(writeNewestTranscript(t,
				newestReplyLine(newestStamp0800),
				newestQuestionLine(newestStamp0905))),
			want: newestUnix0905,
		},
		{
			name:    "absent transcript file",
			options: liveOptions(absent),
			want:    0,
		},
		{
			name:    "no transcript option",
			options: map[string]string{protocol.OptionState: string(protocol.StateWorking)},
			want:    0,
		},
		{
			name:    "no state",
			options: map[string]string{protocol.OptionTranscript: withReply},
			want:    0,
		},
	}

	wants := make(map[string]float64, len(cases))
	for i, tc := range cases {
		session := fmt.Sprintf("s%d", i)
		startSession(t, tmux, session, longCommand)
		pane := paneID(t, tmux, session)
		for name, value := range tc.options {
			if err := tmux.SetPaneOption(t.Context(), pane, name, value); err != nil {
				t.Fatalf("SetPaneOption(%s, %s): %v", pane, name, err)
			}
		}
		wants[pane] = tc.want
	}

	rec := get(t, newHandler(t, tmux), "/v1/panes", allowedLogin)
	assertStatus(t, rec, http.StatusOK)
	got := decodeObjects(t, rec.Body.Bytes())
	if len(got) != len(cases) {
		t.Fatalf("got %d panes, want %d: %s", len(got), len(cases), rec.Body.String())
	}
	for _, obj := range got {
		id, _ := obj["id"].(string)
		want, known := wants[id]
		if !known {
			t.Fatalf("pane %q is not one of the test panes", id)
		}
		newest, ok := obj["newest_reply"].(float64)
		if !ok || newest != want {
			t.Fatalf("pane %s newest_reply = %v, want %v", id, obj["newest_reply"], want)
		}
	}
}

func TestServePanesNewestNoReread(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	path := writeNewestTranscript(t, newestReplyLine(newestStamp0900))
	pane := conversationPane(t, tmux, liveOptions(path))
	handler := newHandler(t, tmux)

	if got := paneNewest(t, handler, pane); got != newestUnix0900 {
		t.Fatalf("newest_reply = %v, want %v", got, newestUnix0900)
	}

	overwriteTranscript(t, path, newestStamp0900, newestStamp0930)
	if got := paneNewest(t, handler, pane); got != newestUnix0900 {
		t.Fatalf("after in-place overwrite newest_reply = %v, want the unchanged %v", got, newestUnix0900)
	}

	appendTranscript(t, path, newestReplyLine(newestStamp0920)+"\n")
	if got := paneNewest(t, handler, pane); got != newestUnix0920 {
		t.Fatalf("after appended reply newest_reply = %v, want %v", got, newestUnix0920)
	}
}

func TestServePanesNewestReadError(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	// A path under a regular file fails to open with ENOTDIR, which is not a missing file.
	file := writeNewestTranscript(t, newestReplyLine(newestStamp0900))
	conversationPane(t, tmux, liveOptions(filepath.Join(file, "under.jsonl")))

	rec := get(t, newHandler(t, tmux), "/v1/panes", allowedLogin)
	assertStatus(t, rec, http.StatusInternalServerError)
}
