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
)

// quoted returns text as a JSON string literal.
func quoted(t *testing.T, text string) string {
	t.Helper()
	raw, err := json.Marshal(text)
	if err != nil {
		t.Fatalf("marshal text: %v", err)
	}
	return string(raw)
}

// queueOperationLine returns a queue-operation line at second s with the given operation and extra JSON fields,
// each written with a leading comma.
func queueOperationLine(s int, operation, extra string) string {
	return `{"type":"queue-operation","operation":"` + operation + `","timestamp":"` + stampAt(s) +
		`","sessionId":"s"` + extra + `}`
}

// enqueueLine returns an enqueue record at second s holding text.
func enqueueLine(t *testing.T, s int, text string) string {
	t.Helper()
	return queueOperationLine(s, "enqueue", `,"content":`+quoted(t, text))
}

// dequeueLine returns a dequeue record at second s.
func dequeueLine(s int) string {
	return queueOperationLine(s, "dequeue", "")
}

// removeLine returns a remove record at second s holding text, with a reason field when reason is not empty.
func removeLine(t *testing.T, s int, text, reason string) string {
	t.Helper()
	extra := `,"content":` + quoted(t, text)
	if reason != "" {
		extra += `,"reason":"` + reason + `"`
	}
	return queueOperationLine(s, "remove", extra)
}

// droppedRemoveLine returns a remove record at second s that a hook dropped, which carries no content.
func droppedRemoveLine(s int) string {
	return queueOperationLine(s, "remove", `,"reason":"dropped_by_hook"`)
}

// popAllLine returns a popAll record at second s holding one pulled-back text.
func popAllLine(t *testing.T, s int, text string) string {
	t.Helper()
	return queueOperationLine(s, "popAll", `,"content":`+quoted(t, text))
}

// queuedUserLine returns a user line at second s that Claude Code delivered from the queue: promptSource queued and
// origin human, with string content text.
func queuedUserLine(t *testing.T, s int, text string) string {
	t.Helper()
	return `{"type":"user","timestamp":"` + stampAt(s) + `","promptSource":"queued","origin":{"kind":"human"},` +
		`"message":{"role":"user","content":` + quoted(t, text) + `}}`
}

// waitingAt returns what queues reports for path, failing the test on error.
func waitingAt(t *testing.T, queues *Queues, path string, idle bool) []Waiting {
	t.Helper()
	got, err := queues.Waiting(path, idle)
	if err != nil {
		t.Fatalf("Waiting: %v", err)
	}
	return got
}

// assertWaiting fails the test unless queues reports want for path when not idle.
func assertWaiting(t *testing.T, queues *Queues, path string, want []Waiting) {
	t.Helper()
	if got := waitingAt(t, queues, path, false); !reflect.DeepEqual(got, want) {
		t.Fatalf("Waiting = %+v, want %+v", got, want)
	}
}

func TestQueueFoldedMessageWaitsUntilRemoved(t *testing.T) {
	path := writeTranscript(t, enqueueLine(t, 0, "synthetic folded"))
	queues := NewQueues()
	assertWaiting(t, queues, path, []Waiting{{Text: "synthetic folded", Time: unixAt(0)}})

	appendText(t, path, removeLine(t, 1, "synthetic folded", "absorbed_mid_turn")+"\n"+
		attachmentLine(1, queuedAttachment(t, "synthetic folded"))+"\n")
	assertWaiting(t, queues, path, []Waiting{})
}

func TestQueueTurnEndMessageWaitsAfterDequeueUntilQueuedLine(t *testing.T) {
	path := writeTranscript(t, enqueueLine(t, 0, "synthetic turn end"), dequeueLine(1))
	queues := NewQueues()
	assertWaiting(t, queues, path, []Waiting{{Text: "synthetic turn end", Time: unixAt(0)}})

	appendText(t, path, queuedUserLine(t, 2, "synthetic turn end")+"\n")
	assertWaiting(t, queues, path, []Waiting{})
}

func TestQueueTwoMessagesCloseOneAtATimeInOrder(t *testing.T) {
	path := writeTranscript(t, enqueueLine(t, 0, "synthetic first"), enqueueLine(t, 1, "synthetic second"))
	queues := NewQueues()
	assertWaiting(t, queues, path, []Waiting{
		{Text: "synthetic first", Time: unixAt(0)},
		{Text: "synthetic second", Time: unixAt(1)},
	})

	appendText(t, path, removeLine(t, 2, "synthetic first", "delivered_to_agent")+"\n")
	assertWaiting(t, queues, path, []Waiting{{Text: "synthetic second", Time: unixAt(1)}})

	appendText(t, path, removeLine(t, 3, "synthetic second", "")+"\n")
	assertWaiting(t, queues, path, []Waiting{})
}

func TestQueueEqualTextsCloseOldestFirst(t *testing.T) {
	path := writeTranscript(t,
		enqueueLine(t, 0, "synthetic same"),
		enqueueLine(t, 1, "synthetic same"),
		removeLine(t, 2, "synthetic same", "absorbed_mid_turn"),
	)
	assertWaiting(t, NewQueues(), path, []Waiting{{Text: "synthetic same", Time: unixAt(1)}})
}

func TestQueuePulledBackMessageIsGone(t *testing.T) {
	path := writeTranscript(t, enqueueLine(t, 0, "synthetic pulled"))
	queues := NewQueues()
	assertWaiting(t, queues, path, []Waiting{{Text: "synthetic pulled", Time: unixAt(0)}})

	appendText(t, path, popAllLine(t, 1, "synthetic pulled")+"\n")
	assertWaiting(t, queues, path, []Waiting{})
}

func TestQueueCanceledRowKeptAtIdleThenGoneAfterNextEnqueue(t *testing.T) {
	canceled := []Waiting{{Text: "synthetic canceled", Time: unixAt(0), Canceled: true}}
	path := writeTranscript(t,
		enqueueLine(t, 0, "synthetic canceled"),
		removeLine(t, 1, "synthetic canceled", "cleared_on_cancel"),
	)
	queues := NewQueues()
	assertWaiting(t, queues, path, canceled)
	if got := waitingAt(t, queues, path, true); !reflect.DeepEqual(got, canceled) {
		t.Fatalf("idle Waiting = %+v, want %+v", got, canceled)
	}
	assertWaiting(t, queues, path, canceled)

	// An enqueue that is not the person's opens no row, so it is not their next send.
	appendText(t, path, enqueueLine(t, 2, "<task-notification>synthetic task")+"\n")
	assertWaiting(t, queues, path, canceled)

	appendText(t, path, enqueueLine(t, 3, "synthetic next")+"\n")
	assertWaiting(t, queues, path, []Waiting{{Text: "synthetic next", Time: unixAt(3)}})
}

func TestQueueCanceledRowGoneAfterTypedLine(t *testing.T) {
	path := writeTranscript(t,
		enqueueLine(t, 0, "synthetic canceled"),
		removeLine(t, 1, "synthetic canceled", "cleared_on_cancel"),
	)
	queues := NewQueues()
	assertWaiting(t, queues, path, []Waiting{{Text: "synthetic canceled", Time: unixAt(0), Canceled: true}})

	appendText(t, path, typedTextLine(t, 2, "synthetic typed")+"\n")
	assertWaiting(t, queues, path, []Waiting{})
}

func TestQueueRereadsRewrittenTranscriptFromStart(t *testing.T) {
	path := writeTranscript(t, enqueueLine(t, 0, "synthetic first message"))
	queues := NewQueues()
	assertWaiting(t, queues, path, []Waiting{{Text: "synthetic first message", Time: unixAt(0)}})

	writeFile(t, path, enqueueLine(t, 1, "synthetic second")+"\n")
	assertWaiting(t, queues, path, []Waiting{{Text: "synthetic second", Time: unixAt(1)}})

	replacement := filepath.Join(filepath.Dir(path), "replacement.jsonl")
	writeFile(t, replacement, enqueueLine(t, 2, "synthetic replaced")+"\n")
	if err := os.Rename(replacement, path); err != nil {
		t.Fatalf("rename replacement: %v", err)
	}
	assertWaiting(t, queues, path, []Waiting{{Text: "synthetic replaced", Time: unixAt(2)}})
}

func TestQueueIdleDropsUnmatchedRowsForGood(t *testing.T) {
	path := writeTranscript(t, enqueueLine(t, 0, "synthetic unmatched"))
	queues := NewQueues()
	if got := waitingAt(t, queues, path, true); !reflect.DeepEqual(got, []Waiting{}) {
		t.Fatalf("idle Waiting = %+v, want none", got)
	}
	assertWaiting(t, queues, path, []Waiting{})

	appendText(t, path, enqueueLine(t, 1, "synthetic later")+"\n")
	assertWaiting(t, queues, path, []Waiting{{Text: "synthetic later", Time: unixAt(1)}})
}

func TestQueueTaskAgentAndCrossSessionEnqueuesGiveNoRow(t *testing.T) {
	path := writeTranscript(t,
		enqueueLine(t, 0, "synthetic own"),
		enqueueLine(t, 1, "<task-notification>synthetic task"),
		enqueueLine(t, 2, "<agent-message synthetic agent"),
		enqueueLine(t, 3, "<cross-session-message synthetic peer"),
		enqueueLine(t, 4, ""),
	)
	assertWaiting(t, NewQueues(), path, []Waiting{{Text: "synthetic own", Time: unixAt(0)}})
}

func TestQueueDroppedRemoveClosesNothing(t *testing.T) {
	path := writeTranscript(t, enqueueLine(t, 0, "synthetic kept"), droppedRemoveLine(1))
	assertWaiting(t, NewQueues(), path, []Waiting{{Text: "synthetic kept", Time: unixAt(0)}})
}

func TestQueueCutsDisplayTextButMatchesWholeText(t *testing.T) {
	long := strings.Repeat("a", maxTextBytes+100)
	cut := strings.Repeat("a", maxTextBytes)
	path := writeTranscript(t, enqueueLine(t, 0, long))
	queues := NewQueues()
	assertWaiting(t, queues, path, []Waiting{{Text: cut, Time: unixAt(0)}})

	appendText(t, path, removeLine(t, 1, cut, "absorbed_mid_turn")+"\n")
	assertWaiting(t, queues, path, []Waiting{{Text: cut, Time: unixAt(0)}})

	appendText(t, path, removeLine(t, 2, long, "absorbed_mid_turn")+"\n")
	assertWaiting(t, queues, path, []Waiting{})
}

func TestQueueReadsOnlyAppendedBytesAfterFirstCall(t *testing.T) {
	path := writeTranscript(t, enqueueLine(t, 0, "first message"))
	queues := NewQueues()
	assertWaiting(t, queues, path, []Waiting{{Text: "first message", Time: unixAt(0)}})

	overwriteInPlace(t, path, "first message", "XXXXXXXXXXXXX")
	appendText(t, path, enqueueLine(t, 1, "second message")+"\n")
	assertWaiting(t, queues, path, []Waiting{
		{Text: "first message", Time: unixAt(0)},
		{Text: "second message", Time: unixAt(1)},
	})
	assertWaiting(t, NewQueues(), path, []Waiting{
		{Text: "XXXXXXXXXXXXX", Time: unixAt(0)},
		{Text: "second message", Time: unixAt(1)},
	})
}

func TestQueueCountsTrailingLineOnceComplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	writeFile(t, path, enqueueLine(t, 0, "synthetic partial"))
	queues := NewQueues()
	assertWaiting(t, queues, path, []Waiting{})

	appendText(t, path, "\n")
	assertWaiting(t, queues, path, []Waiting{{Text: "synthetic partial", Time: unixAt(0)}})
}

func TestQueueMissingFileWrapsErrNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.jsonl")
	if _, err := NewQueues().Waiting(path, false); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Waiting error = %v, want one wrapping fs.ErrNotExist", err)
	}
}
