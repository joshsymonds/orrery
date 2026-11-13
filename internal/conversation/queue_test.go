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

// userContentLine returns a user line at second s with string content text and the extra JSON fields, each written
// with a leading comma.
func userContentLine(t *testing.T, s int, extra, text string) string {
	t.Helper()
	return `{"type":"user","timestamp":"` + stampAt(s) + `"` + extra + `,"message":{"role":"user","content":` +
		quoted(t, text) + `}}`
}

// systemLine returns a system line at second s with the given subtype and extra JSON fields, each written with a
// leading comma.
func systemLine(s int, subtype, extra string) string {
	return `{"type":"system","subtype":"` + subtype + `","timestamp":"` + stampAt(s) + `"` + extra + `}`
}

// turnDurationLine returns the turn_duration record that Claude Code writes at the end of a turn at second s.
func turnDurationLine(s int) string {
	return systemLine(s, "turn_duration", `,"durationMs":1000`)
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

func TestQueueSystemSourcedCopyAfterDequeueClosesRow(t *testing.T) {
	path := writeTranscript(t,
		enqueueLine(t, 0, "/compact"),
		dequeueLine(1),
		userContentLine(t, 2, `,"promptSource":"system","isMeta":true`, "/compact"),
	)
	assertWaiting(t, NewQueues(), path, []Waiting{})
}

func TestQueueUserLineWithoutPromptSourceAfterDequeueClosesRow(t *testing.T) {
	path := writeTranscript(t, enqueueLine(t, 0, "synthetic bare"), dequeueLine(1),
		userContentLine(t, 2, "", "synthetic bare"))
	assertWaiting(t, NewQueues(), path, []Waiting{})
}

func TestQueueExactLineWithoutDequeueKeepsRow(t *testing.T) {
	path := writeTranscript(t, enqueueLine(t, 0, "synthetic kept"),
		userContentLine(t, 1, `,"promptSource":"system"`, "synthetic kept"),
		userContentLine(t, 2, "", "synthetic kept"))
	assertWaiting(t, NewQueues(), path, []Waiting{{Text: "synthetic kept", Time: unixAt(0)}})
}

func TestQueueRowEnqueuedAfterDequeueNeedsAnotherDequeue(t *testing.T) {
	path := writeTranscript(t, enqueueLine(t, 0, "synthetic late"), dequeueLine(1),
		userContentLine(t, 2, "", "synthetic late"),
		enqueueLine(t, 3, "synthetic late"),
		userContentLine(t, 4, "", "synthetic late"))
	assertWaiting(t, NewQueues(), path, []Waiting{{Text: "synthetic late", Time: unixAt(3)}})
}

func TestQueueTypedLineClosesEveryRowItContains(t *testing.T) {
	path := writeTranscript(t,
		enqueueLine(t, 0, "synthetic first"),
		enqueueLine(t, 1, "synthetic second"),
		enqueueLine(t, 2, "synthetic third"),
		typedTextLine(t, 3, "synthetic first\nsynthetic second"),
	)
	assertWaiting(t, NewQueues(), path, []Waiting{{Text: "synthetic third", Time: unixAt(2)}})
}

func TestQueueTypedLineClosesOldestRowOfEachContainedText(t *testing.T) {
	path := writeTranscript(t,
		enqueueLine(t, 0, "synthetic same"),
		enqueueLine(t, 1, "synthetic same"),
		typedTextLine(t, 2, "synthetic same and more"),
	)
	assertWaiting(t, NewQueues(), path, []Waiting{{Text: "synthetic same", Time: unixAt(1)}})
}

func TestQueueTurnEndClosesRowsOpenAfterDequeue(t *testing.T) {
	path := writeTranscript(t, enqueueLine(t, 0, "synthetic turn"), dequeueLine(1),
		userContentLine(t, 1, "", "synthetic other"),
		enqueueLine(t, 2, "synthetic next"), turnDurationLine(3))
	assertWaiting(t, NewQueues(), path, []Waiting{{Text: "synthetic next", Time: unixAt(2)}})
}

func TestQueueUndecidedRowWaitsThroughTurnEndsUntilRemoved(t *testing.T) {
	path := writeTranscript(t,
		enqueueLine(t, 0, "<task-notification>synthetic earlier"),
		enqueueLine(t, 1, "synthetic typed"),
		dequeueLine(2),
		turnDurationLine(3),
	)
	queues := NewQueues()
	assertWaiting(t, queues, path, []Waiting{{Text: "synthetic typed", Time: unixAt(1)}})

	appendText(t, path, userContentLine(t, 4, `,"origin":{"kind":"task-notification"}`,
		"<task-notification>synthetic other")+"\n"+turnDurationLine(5)+"\n")
	assertWaiting(t, queues, path, []Waiting{{Text: "synthetic typed", Time: unixAt(1)}})

	appendText(t, path, removeLine(t, 6, "synthetic typed", "absorbed_mid_turn")+"\n")
	assertWaiting(t, queues, path, []Waiting{})
}

func TestQueueNoticeLeadingWithRowClosesIt(t *testing.T) {
	path := writeTranscript(t, enqueueLine(t, 0, "synthetic lead"), dequeueLine(1))
	queues := NewQueues()
	assertWaiting(t, queues, path, []Waiting{{Text: "synthetic lead", Time: unixAt(0)}})

	appendText(t, path, userContentLine(t, 2, `,"origin":{"kind":"task-notification"}`,
		"synthetic lead\n<task-notification>synthetic tail")+"\n")
	assertWaiting(t, queues, path, []Waiting{})
}

func TestQueueNoticeHoldingRowMidTextKeepsIt(t *testing.T) {
	path := writeTranscript(t, enqueueLine(t, 0, "synthetic mid"), dequeueLine(1))
	queues := NewQueues()
	assertWaiting(t, queues, path, []Waiting{{Text: "synthetic mid", Time: unixAt(0)}})

	appendText(t, path, userContentLine(t, 2, `,"origin":{"kind":"task-notification"}`,
		"<task-notification>synthetic summary of synthetic mid")+"\n"+turnDurationLine(3)+"\n")
	assertWaiting(t, queues, path, []Waiting{{Text: "synthetic mid", Time: unixAt(0)}})
}

func TestQueueOtherUserLineThenTurnEndClosesDequeuedRow(t *testing.T) {
	path := writeTranscript(t, enqueueLine(t, 0, "synthetic held"), dequeueLine(1))
	queues := NewQueues()
	assertWaiting(t, queues, path, []Waiting{{Text: "synthetic held", Time: unixAt(0)}})

	appendText(t, path, userContentLine(t, 2, "", "synthetic other")+"\n")
	assertWaiting(t, queues, path, []Waiting{{Text: "synthetic held", Time: unixAt(0)}})

	appendText(t, path, turnDurationLine(3)+"\n")
	assertWaiting(t, queues, path, []Waiting{})
}

func TestQueueSettledRowStaysSettledThroughLaterDequeue(t *testing.T) {
	path := writeTranscript(t, enqueueLine(t, 0, "synthetic settled"), dequeueLine(1),
		userContentLine(t, 2, "", "synthetic other"), dequeueLine(3),
		userContentLine(t, 4, `,"origin":{"kind":"task-notification"}`, "<task-notification>synthetic task"))
	queues := NewQueues()
	assertWaiting(t, queues, path, []Waiting{{Text: "synthetic settled", Time: unixAt(0)}})

	appendText(t, path, turnDurationLine(5)+"\n")
	assertWaiting(t, queues, path, []Waiting{})
}

func TestQueueTurnDurationWithoutDequeueKeepsRow(t *testing.T) {
	path := writeTranscript(t, enqueueLine(t, 0, "synthetic kept"), turnDurationLine(1))
	assertWaiting(t, NewQueues(), path, []Waiting{{Text: "synthetic kept", Time: unixAt(0)}})
}

func TestQueueStopHookSummaryAfterDequeueKeepsRow(t *testing.T) {
	path := writeTranscript(t, enqueueLine(t, 0, "synthetic kept"), dequeueLine(1),
		systemLine(2, "stop_hook_summary", ""))
	assertWaiting(t, NewQueues(), path, []Waiting{{Text: "synthetic kept", Time: unixAt(0)}})
}

func TestQueueTurnEndKeepsCanceledRow(t *testing.T) {
	canceled := []Waiting{{Text: "synthetic canceled", Time: unixAt(0), Canceled: true}}
	path := writeTranscript(t,
		enqueueLine(t, 0, "synthetic canceled"),
		removeLine(t, 1, "synthetic canceled", "cleared_on_cancel"),
		dequeueLine(2),
		turnDurationLine(3),
	)
	assertWaiting(t, NewQueues(), path, canceled)
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

func TestQueueSystemEnqueuesGiveNoRow(t *testing.T) {
	forms := []struct {
		name string
		text string
	}{
		{"task notification", "<task-notification>synthetic task tail"},
		{"agent message", "<agent-message synthetic agent tail"},
		{"cross-session message", "<cross-session-message synthetic peer tail"},
		{"teammate message", `<teammate-message teammate_id="synthetic-peer" color="blue">` +
			"synthetic body tail</teammate-message>"},
		{"stopped agents notice", "3 background agents were stopped; synthetic tail"},
	}
	for _, form := range forms {
		t.Run(form.name, func(t *testing.T) {
			path := writeTranscript(t, enqueueLine(t, 0, form.text))
			assertWaiting(t, NewQueues(), path, []Waiting{})
		})
	}
}

func TestQueueTypedMessageHoldingFormLaterStillWaits(t *testing.T) {
	path := writeTranscript(t,
		enqueueLine(t, 0, "synthetic own <task-notification>synthetic later"),
		enqueueLine(t, 1, "synthetic own <agent-message synthetic later"),
		enqueueLine(t, 2, "synthetic own <cross-session-message synthetic later"),
		enqueueLine(t, 3, `synthetic own <teammate-message teammate_id="synthetic-peer">synthetic later`),
		enqueueLine(t, 4, "synthetic own 3 background agents were stopped"),
		enqueueLine(t, 5, "background agents were stopped by synthetic"),
		enqueueLine(t, 6, ""),
	)
	assertWaiting(t, NewQueues(), path, []Waiting{
		{Text: "synthetic own <task-notification>synthetic later", Time: unixAt(0)},
		{Text: "synthetic own <agent-message synthetic later", Time: unixAt(1)},
		{Text: "synthetic own <cross-session-message synthetic later", Time: unixAt(2)},
		{Text: `synthetic own <teammate-message teammate_id="synthetic-peer">synthetic later`, Time: unixAt(3)},
		{Text: "synthetic own 3 background agents were stopped", Time: unixAt(4)},
		{Text: "background agents were stopped by synthetic", Time: unixAt(5)},
	})
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
