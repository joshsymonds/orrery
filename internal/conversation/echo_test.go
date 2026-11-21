package conversation

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"
)

// echoStamp returns the RFC 3339 timestamp that a transcript line writes for unix second at.
func echoStamp(at int64) string {
	return time.Unix(at, 0).UTC().Format(time.RFC3339)
}

// echoPrompt returns a typed user line with text, written at unix second at.
func echoPrompt(text string, at int64) string {
	return fmt.Sprintf(`{"type":"user","timestamp":%q,"message":{"content":%q}}`, echoStamp(at), text)
}

// echoReply returns an assistant line with one text block holding text, written at unix second at.
func echoReply(text string, at int64) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"content":[{"type":"text","text":%q}]}}`,
		echoStamp(at), text)
}

// echoQueued returns a queued command attachment that carries text as a typed prompt, written at unix second at.
func echoQueued(text string, at int64) string {
	return fmt.Sprintf(`{"type":"attachment","timestamp":%q,"attachment":{"type":"queued_command",`+
		`"commandMode":"prompt","origin":{"kind":"human"},"prompt":%q}}`, echoStamp(at), text)
}

// echoPromptItem returns a prompt item with text at unix second at.
func echoPromptItem(text string, at int64) Item {
	return Item{Kind: kindPrompt, Time: at, Text: text}
}

// echoClients returns the Client of each item, in order.
func echoClients(items []Item) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Client)
	}
	return out
}

// echoTranscript writes lines as a transcript and returns its path.
func echoTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	var b transcriptBuilder
	for _, line := range lines {
		b.line(line)
	}
	return b.write(t)
}

// TestPromptClientMarksEchoAtOrAfterSendWithinBound checks that a prompt written at the send's second, at the last
// second of the bound, or a while after the send takes the send's client.
func TestPromptClientMarksEchoAtOrAfterSendWithinBound(t *testing.T) {
	t.Parallel()
	sends := []Send{
		{Text: "at send", Client: "pane-a", At: 1000},
		{Text: "bound edge", Client: "pane-b", At: 2000},
		{Text: "after send", Client: "pane-c", At: 3000},
	}
	items := []Item{
		echoPromptItem("at send", 1000),
		echoPromptItem("bound edge", 2000+EchoBound),
		echoPromptItem("after send", 3005),
	}
	MarkEchoes(items, nil, sends)
	want := []string{"pane-a", "pane-b", "pane-c"}
	if got := echoClients(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("clients = %q, want %q", got, want)
	}
}

// TestPromptClientMarksQueuedSendEchoedATurnLater reads a queued send whose prompt is written as a queued command
// attachment a turn after the send, past a reply. The attachment's prompt takes the send's client.
func TestPromptClientMarksQueuedSendEchoedATurnLater(t *testing.T) {
	t.Parallel()
	path := echoTranscript(t,
		echoReply("on it", 1010),
		echoQueued("also add a test", 1200),
		echoReply("done", 1210),
	)
	conv, _, err := ReadEnd(path, 100)
	if err != nil {
		t.Fatalf("ReadEnd: %v", err)
	}
	sends := []Send{{Text: "also add a test", Client: "pane-b", At: 1000}}
	MarkEchoes(conv.Items, nil, sends)
	want := []string{"", "pane-b", ""}
	if got := echoClients(conv.Items); !reflect.DeepEqual(got, want) {
		t.Fatalf("clients = %q, want %q", got, want)
	}
}

// TestPromptClientSkipsSameTextBeforeSendOrPastBound checks that a prompt of the send's text one second before the
// send, or one second after the bound, takes no client.
func TestPromptClientSkipsSameTextBeforeSendOrPastBound(t *testing.T) {
	t.Parallel()
	sends := []Send{{Text: "ship it", Client: "pane-a", At: 1000}}
	items := []Item{
		echoPromptItem("ship it", 999),
		echoPromptItem("ship it", 1000+EchoBound+1),
	}
	MarkEchoes(items, nil, sends)
	want := []string{"", ""}
	if got := echoClients(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("clients = %q, want %q", got, want)
	}
}

// TestPromptClientMarksOnlyFirstMatchingPrompt checks that a send marks the first prompt of its text in transcript
// order, and that a later prompt of the same text stays unmarked.
func TestPromptClientMarksOnlyFirstMatchingPrompt(t *testing.T) {
	t.Parallel()
	sends := []Send{{Text: "rerun tests", Client: "pane-a", At: 1000}}
	items := []Item{
		echoPromptItem("rerun tests", 1010),
		echoPromptItem("rerun tests", 1020),
	}
	MarkEchoes(items, nil, sends)
	want := []string{"pane-a", ""}
	if got := echoClients(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("clients = %q, want %q", got, want)
	}
}

// TestPromptClientTwoSendsMarkTwoSuccessivePrompts checks that two sends of the same text mark two successive prompts,
// each send taking the first prompt of its text after it. The sends are given out of At order, so the earlier send
// must still take the first prompt.
func TestPromptClientTwoSendsMarkTwoSuccessivePrompts(t *testing.T) {
	t.Parallel()
	sends := []Send{
		{Text: "continue", Client: "pane-b", At: 1005},
		{Text: "continue", Client: "pane-a", At: 1000},
	}
	items := []Item{
		echoPromptItem("continue", 1010),
		echoPromptItem("continue", 1020),
	}
	MarkEchoes(items, nil, sends)
	want := []string{"pane-a", "pane-b"}
	if got := echoClients(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("clients = %q, want %q", got, want)
	}
}

// TestPromptClientTiedSendsTakeInGivenOrder checks that sends at the same second take the prompts in the order they
// are given.
func TestPromptClientTiedSendsTakeInGivenOrder(t *testing.T) {
	t.Parallel()
	sends := []Send{
		{Text: "x", Client: "pane-first", At: 1000},
		{Text: "x", Client: "pane-second", At: 1000},
	}
	items := []Item{
		echoPromptItem("x", 1010),
		echoPromptItem("x", 1020),
	}
	MarkEchoes(items, nil, sends)
	want := []string{"pane-first", "pane-second"}
	if got := echoClients(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("clients = %q, want %q", got, want)
	}
}

// TestPromptClientMarksOnlyPrompts checks that a reply, a result or a notice of the send's text takes no client, and
// that the prompt of that text does.
func TestPromptClientMarksOnlyPrompts(t *testing.T) {
	t.Parallel()
	sends := []Send{{Text: "ship it", Client: "pane-a", At: 1000}}
	items := []Item{
		{Kind: kindReply, Time: 1005, Text: "ship it"},
		{Kind: kindResult, Time: 1006, Text: "ship it"},
		{Kind: kindNotice, Time: 1007, Text: "ship it"},
		echoPromptItem("ship it", 1010),
	}
	MarkEchoes(items, nil, sends)
	want := []string{"", "", "", "pane-a"}
	if got := echoClients(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("clients = %q, want %q", got, want)
	}
}

// TestPromptClientEchoBeforeReadTakesSendSoLaterPromptStaysUnmarked reads the newest two items of a transcript whose
// send is echoed just before the read. The earlier prompt, read backward from the read's start, takes the send, so the
// same text in the read stays unmarked. Without the earlier prompt, the read's prompt would take it.
func TestPromptClientEchoBeforeReadTakesSendSoLaterPromptStaysUnmarked(t *testing.T) {
	t.Parallel()
	path := echoTranscript(t,
		echoPrompt("deploy", 1000),
		echoReply("ok", 1100),
		echoPrompt("deploy", 1200),
		echoReply("done", 1300),
	)
	conv, _, err := ReadEnd(path, 2)
	if err != nil {
		t.Fatalf("ReadEnd: %v", err)
	}
	if got := texts(conv.Items); !reflect.DeepEqual(got, []string{"deploy", "done"}) {
		t.Fatalf("read items = %q, want deploy and done", got)
	}
	earlier, err := EarlierPrompts(path, conv.Oldest, 1000)
	if err != nil {
		t.Fatalf("EarlierPrompts: %v", err)
	}
	if got := texts(earlier); !reflect.DeepEqual(got, []string{"deploy"}) {
		t.Fatalf("earlier prompts = %q, want the deploy before the read", got)
	}

	sends := []Send{{Text: "deploy", Client: "pane-a", At: 1000}}
	withoutEarlier := slices.Clone(conv.Items)
	MarkEchoes(withoutEarlier, nil, sends)
	if want := []string{"pane-a", ""}; !reflect.DeepEqual(echoClients(withoutEarlier), want) {
		t.Fatalf("clients without the earlier prompt = %q, want %q", echoClients(withoutEarlier), want)
	}

	MarkEchoes(conv.Items, earlier, sends)
	if want := []string{"", ""}; !reflect.DeepEqual(echoClients(conv.Items), want) {
		t.Fatalf("clients with the earlier prompt = %q, want %q", echoClients(conv.Items), want)
	}
}

// TestPromptClientEarlierPromptsStopsAtGivenTime reads backward from a line start until a line stamped older than the
// given time. A line with no stamp does not stop the read, a reply is not a prompt, and the prompts before the start
// are not read.
func TestPromptClientEarlierPromptsStopsAtGivenTime(t *testing.T) {
	t.Parallel()
	var b transcriptBuilder
	b.line(echoPrompt("old", 100))
	b.line(echoPrompt("mid", 200))
	b.line(`{"type":"summary","summary":"unstamped"}`)
	b.line(echoReply("r", 250))
	newStart := b.line(echoPrompt("new", 300))
	b.line(echoReply("last", 400))
	path := b.write(t)

	got, err := EarlierPrompts(path, fileLen(t, path), 200)
	if err != nil {
		t.Fatalf("EarlierPrompts to the end: %v", err)
	}
	if want := []string{"mid", "new"}; !reflect.DeepEqual(texts(got), want) {
		t.Fatalf("prompts from 200 = %q, want %q", texts(got), want)
	}

	got, err = EarlierPrompts(path, newStart, 200)
	if err != nil {
		t.Fatalf("EarlierPrompts before new: %v", err)
	}
	if want := []string{"mid"}; !reflect.DeepEqual(texts(got), want) {
		t.Fatalf("prompts before new from 200 = %q, want %q", texts(got), want)
	}
}
