package conversation

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// replayCutoff is the moment that splits rows into never taken before and never taken after: second 10 of the fixture
// clock.
func replayCutoff() time.Time {
	return time.Unix(unixAt(10), 0)
}

// assistantLine returns an assistant line at second s with one text block.
func assistantLine(s int) string {
	return `{"type":"assistant","timestamp":"` + stampAt(s) + `","message":{"role":"assistant","content":[` +
		`{"type":"text","text":"synthetic reply"}]}}`
}

// writeReplayTranscript writes lines to root/dir/name, making dir first.
func writeReplayTranscript(t *testing.T, root, dir, name string, lines ...string) {
	t.Helper()
	dirPath := filepath.Join(root, dir)
	if err := os.MkdirAll(dirPath, 0o700); err != nil {
		t.Fatalf("make directory: %v", err)
	}
	writeFile(t, filepath.Join(dirPath, name), strings.Join(lines, "\n")+"\n")
}

// mustReplay replays root at the replay cutoff and fails the test on error.
func mustReplay(t *testing.T, root string) QueueReplay {
	t.Helper()
	replay, err := ReplayQueues(root, replayCutoff())
	if err != nil {
		t.Fatalf("ReplayQueues: %v", err)
	}
	return replay
}

func TestReplayQueuesCountsEachField(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  QueueReplay
	}{
		{
			name: "closed by remove",
			lines: []string{enqueueLine(t, 0, "synthetic a"), removeLine(t, 1, "synthetic a", "delivered_to_agent"),
				turnDurationLine(2)},
			want: QueueReplay{Transcripts: 1, Typed: 1, Closed: 1},
		},
		{
			name: "closed by containing typed line",
			lines: []string{enqueueLine(t, 0, "synthetic a"), typedTextLine(t, 1, "synthetic a and more"),
				turnDurationLine(2)},
			want: QueueReplay{Transcripts: 1, Typed: 1, Closed: 1},
		},
		{
			name: "closed by turn end after dequeue",
			lines: []string{enqueueLine(t, 0, "synthetic a"), dequeueLine(1), userTextLine(t, 1, "synthetic other"),
				turnDurationLine(2)},
			want: QueueReplay{Transcripts: 1, Typed: 1, Closed: 1},
		},
		{
			name: "closed by cancel",
			lines: []string{enqueueLine(t, 0, "synthetic a"), removeLine(t, 1, "synthetic a", "cleared_on_cancel"),
				turnDurationLine(2)},
			want: QueueReplay{Transcripts: 1, Typed: 1, Closed: 1},
		},
		{
			name:  "never taken before the cutoff",
			lines: []string{enqueueLine(t, 0, "synthetic a"), turnDurationLine(1)},
			want:  QueueReplay{Transcripts: 1, Typed: 1, NeverTakenBefore: 1},
		},
		{
			name:  "never taken at or after the cutoff",
			lines: []string{enqueueLine(t, 10, "synthetic a"), turnDurationLine(11)},
			want:  QueueReplay{Transcripts: 1, Typed: 1, NeverTakenAfter: 1},
		},
		{
			name:  "open with a dequeue after its enqueue is other",
			lines: []string{turnDurationLine(0), enqueueLine(t, 1, "synthetic a"), dequeueLine(2)},
			want:  QueueReplay{Transcripts: 1, Typed: 1, Other: 1},
		},
		{
			name: "open after a notice cleared its mark is never taken, not other",
			lines: []string{enqueueLine(t, 0, "synthetic a"), dequeueLine(1),
				userContentLine(t, 2, `,"origin":{"kind":"task-notification"}`, "<task-notification>synthetic task"),
				turnDurationLine(3)},
			want: QueueReplay{Transcripts: 1, Typed: 1, NeverTakenBefore: 1},
		},
		{
			name: "remove after a containing close falsifies it",
			lines: []string{enqueueLine(t, 0, "synthetic a"), typedTextLine(t, 1, "synthetic a and b"),
				removeLine(t, 2, "synthetic a", "delivered_to_agent"), turnDurationLine(3)},
			want: QueueReplay{Transcripts: 1, Typed: 1, Closed: 1, Falsified: 1},
		},
		{
			name: "popAll after a turn-end close falsifies it",
			lines: []string{enqueueLine(t, 0, "synthetic a"), dequeueLine(1), userTextLine(t, 1, "synthetic other"),
				turnDurationLine(2), popAllLine(t, 3, "synthetic a")},
			want: QueueReplay{Transcripts: 1, Typed: 1, Closed: 1, Falsified: 1},
		},
		{
			name: "dequeue, assistant, then the exact user line falsifies a containing close",
			lines: []string{enqueueLine(t, 0, "synthetic a"), typedTextLine(t, 1, "synthetic a and b"),
				dequeueLine(2), assistantLine(3), userTextLine(t, 4, "synthetic a"), turnDurationLine(5)},
			want: QueueReplay{Transcripts: 1, Typed: 1, Closed: 1, Falsified: 1},
		},
		{
			name: "dequeue, assistant, then the exact user line falsifies a turn-end close",
			lines: []string{enqueueLine(t, 0, "synthetic a"), dequeueLine(1), userTextLine(t, 1, "synthetic other"),
				turnDurationLine(2), dequeueLine(3), assistantLine(4), userTextLine(t, 5, "synthetic a")},
			want: QueueReplay{Transcripts: 1, Typed: 1, Closed: 1, Falsified: 1},
		},
		{
			name: "containing close without an assistant line is not falsified",
			lines: []string{enqueueLine(t, 0, "synthetic a"), typedTextLine(t, 1, "synthetic a and b"),
				dequeueLine(2), userTextLine(t, 3, "synthetic a"), turnDurationLine(4)},
			want: QueueReplay{Transcripts: 1, Typed: 1, Closed: 1},
		},
		{
			name: "enqueue of the same text ends the falsifier's checks",
			lines: []string{enqueueLine(t, 0, "synthetic a"), typedTextLine(t, 1, "synthetic a and b"),
				dequeueLine(2), enqueueLine(t, 3, "synthetic a"), assistantLine(4), userTextLine(t, 5, "synthetic a"),
				turnDurationLine(6)},
			want: QueueReplay{Transcripts: 1, Typed: 2, Closed: 1, NeverTakenBefore: 1},
		},
		{
			name: "exact close is not falsified by the dequeue, assistant and user pattern",
			lines: []string{enqueueLine(t, 0, "synthetic x"), dequeueLine(1), userTextLine(t, 2, "synthetic x"),
				assistantLine(3), dequeueLine(4), userTextLine(t, 5, "synthetic x"), turnDurationLine(6)},
			want: QueueReplay{Transcripts: 1, Typed: 1, Closed: 1},
		},
		{
			name: "open at a turn end, then an assistant line, then its text is delivered",
			lines: []string{enqueueLine(t, 0, "synthetic d"), turnDurationLine(1), assistantLine(2),
				userTextLine(t, 3, "synthetic d")},
			want: QueueReplay{Transcripts: 1, Typed: 1, NeverTakenBefore: 1, OpenAtTurnEndDelivered: 1},
		},
		{
			name: "an enqueue of the same text after the turn end resets the delivery check",
			lines: []string{enqueueLine(t, 0, "synthetic d"), turnDurationLine(1), enqueueLine(t, 2, "synthetic d"),
				assistantLine(3), userTextLine(t, 4, "synthetic d"), turnDurationLine(5)},
			want: QueueReplay{Transcripts: 1, Typed: 2, NeverTakenBefore: 2},
		},
		{
			name:  "text delivered without an assistant line after the turn end is not counted",
			lines: []string{enqueueLine(t, 0, "synthetic d"), turnDurationLine(1), userTextLine(t, 2, "synthetic d")},
			want:  QueueReplay{Transcripts: 1, Typed: 1, NeverTakenBefore: 1},
		},
		{
			name: "rejected enqueue later typed is a notice typed",
			lines: []string{enqueueLine(t, 0, "<task-notification>synthetic task"),
				typedTextLine(t, 1, "<task-notification>synthetic task"), turnDurationLine(2)},
			want: QueueReplay{Transcripts: 1, NoticesTyped: 1},
		},
		{
			name: "rejected enqueue later seen only as an untyped user line is not a notice typed",
			lines: []string{enqueueLine(t, 0, "<task-notification>synthetic task"),
				userTextLine(t, 1, "<task-notification>synthetic task"), turnDurationLine(2)},
			want: QueueReplay{Transcripts: 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeReplayTranscript(t, root, "project", "session.jsonl", tc.lines...)
			if got := mustReplay(t, root); got != tc.want {
				t.Fatalf("ReplayQueues = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestReplayQueuesSelectsOnlyInteractiveTranscriptsWithTurnEnd(t *testing.T) {
	root := t.TempDir()
	writeReplayTranscript(t, root, "project", "kept.jsonl", enqueueLine(t, 0, "synthetic kept"), turnDurationLine(1))
	writeReplayTranscript(t, root, "project/session/subagents", "agent.jsonl",
		enqueueLine(t, 0, "synthetic subagent"), turnDurationLine(1))
	writeReplayTranscript(t, root, "headless", "headless.jsonl", enqueueLine(t, 0, "synthetic headless"),
		systemLine(1, turnDurationSubtype, `,"entrypoint":"sdk-ts"`))
	writeReplayTranscript(t, root, "project", "noturn.jsonl", enqueueLine(t, 0, "synthetic noturn"))
	writeReplayTranscript(t, root, "project", "notes.txt", enqueueLine(t, 0, "synthetic notes"), turnDurationLine(1))
	writeFile(t, filepath.Join(root, "project", "partial.jsonl"),
		enqueueLine(t, 0, "synthetic partial")+"\n"+turnDurationLine(1))
	writeFile(t, filepath.Join(root, "loose.jsonl"), enqueueLine(t, 0, "synthetic loose")+"\n"+turnDurationLine(1)+"\n")

	want := QueueReplay{Transcripts: 1, Typed: 1, NeverTakenBefore: 1}
	if got := mustReplay(t, root); got != want {
		t.Fatalf("ReplayQueues = %+v, want %+v", got, want)
	}
}

func TestReplayQueuesNamesUnreadablePath(t *testing.T) {
	t.Run("missing root", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "missing")
		_, err := ReplayQueues(root, replayCutoff())
		if !errors.Is(err, fs.ErrNotExist) || !strings.Contains(err.Error(), root) {
			t.Fatalf("ReplayQueues error = %v, want one wrapping fs.ErrNotExist that names %s", err, root)
		}
	})
	t.Run("dangling transcript", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "project")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("make directory: %v", err)
		}
		link := filepath.Join(dir, "broken.jsonl")
		if err := os.Symlink(filepath.Join(root, "absent.jsonl"), link); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		_, err := ReplayQueues(root, replayCutoff())
		if !errors.Is(err, fs.ErrNotExist) || !strings.Contains(err.Error(), link) {
			t.Fatalf("ReplayQueues error = %v, want one wrapping fs.ErrNotExist that names %s", err, link)
		}
	})
}

func TestQueueReplayWriteNamesEachCountInOrder(t *testing.T) {
	replay := QueueReplay{
		Transcripts:            1,
		Typed:                  2,
		Closed:                 3,
		NeverTakenBefore:       4,
		NeverTakenAfter:        5,
		Other:                  6,
		Falsified:              7,
		OpenAtTurnEndDelivered: 8,
		NoticesTyped:           9,
	}
	var out bytes.Buffer
	if err := replay.Write(&out); err != nil {
		t.Fatalf("Write: %v", err)
	}
	want := "transcripts 1\ntyped 2\nclosed 3\nnever_taken_before 4\nnever_taken_after 5\nother 6\n" +
		"falsified 7\nopen_at_turn_end_delivered 8\nnotices_typed 9\n"
	if out.String() != want {
		t.Fatalf("Write = %q, want %q", out.String(), want)
	}
}
