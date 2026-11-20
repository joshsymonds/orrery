package server_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/conversation"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// olderReplies is the number of replies the session paging test writes after its prompt, so the session holds more
// than 100 main-thread items.
const olderReplies = 150

// olderSessionConversation returns the items a session holds after seedSessionReplies(t, s, replies), oldest first: the
// prompt, then replies 1 through replies. Item i is written at seq i+2, since the prompt is at seq 2.
func olderSessionConversation(replies int) []*orreryv1.ConversationItem {
	items := []*orreryv1.ConversationItem{stepPromptItem()}
	for n := 1; n <= replies; n++ {
		items = append(items, reply(n))
	}
	return items
}

// sameItems reports whether got and want hold equal items in the same order.
func sameItems(got, want []*orreryv1.ConversationItem) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if !proto.Equal(got[i], want[i]) {
			return false
		}
	}
	return true
}

// writeOlderTranscript writes a transcript of more than 4 MiB to a new file in t.TempDir. Its first line is a prompt,
// and it has replies lines, reply N for N from 1, each padded to about a kilobyte, with an empty line before every
// seventh reply. The reply at usageAt carries a usage count of 12345 tokens, and usageAt 0 or below gives none. It
// returns the path, the items the transcript shows oldest first, and the line start of each item's line, which is
// one line per item.
func writeOlderTranscript(t *testing.T, replies, usageAt int) (string, []*orreryv1.ConversationItem, []int64) {
	t.Helper()
	const padding = 1000
	var (
		buf    strings.Builder
		items  []*orreryv1.ConversationItem
		starts []int64
	)
	line := func(text string, item *orreryv1.ConversationItem) {
		starts = append(starts, int64(buf.Len()))
		items = append(items, item)
		buf.WriteString(text + "\n")
	}
	line(`{"type":"user","message":{"role":"user","content":"`+synthOrigin+`"}}`,
		&orreryv1.ConversationItem{Kind: "prompt", Text: synthOrigin})
	pad := strings.Repeat("x", padding)
	for n := 1; n <= replies; n++ {
		if n%7 == 0 {
			buf.WriteString("\n")
		}
		text := fmt.Sprintf("synthetic reply %d %s", n, pad)
		usage := ""
		if n == usageAt {
			usage = `,"usage":{"input_tokens":12345}`
		}
		line(`{"type":"assistant","message":{"content":[{"type":"text","text":"`+text+`"}]`+usage+`}}`,
			&orreryv1.ConversationItem{Kind: "reply", Text: text})
	}
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(buf.String()), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path, items, starts
}

// TestSessionsOlderPagesBackFromResetOldestToFirstItem pages Sessions.Older back from the oldest item of a reset, over a
// session of more than 100 items. Each page holds the items just before the one above it, in order, and the seq of
// its oldest item; the last page says no more. The reset and the pages together hold every item once.
func TestSessionsOlderPagesBackFromResetOldestToFirstItem(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	seedSessionReplies(t, s, olderReplies)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))
	all := olderSessionConversation(olderReplies)

	const resetLimit = 30
	reset := openSessionStream(t, client, sessionReq(eventEngine, 0, resetLimit)).next(t)
	kept := len(all) - resetLimit
	if !reset.GetReset_() || !sameItems(reset.GetItems(), all[kept:]) {
		t.Fatalf("reset = %v, want the newest %d items", reset, resetLimit)
	}
	if want := int64(kept + 2); reset.GetOldest() != want {
		t.Fatalf("reset oldest = %d, want %d, the seq of its oldest item", reset.GetOldest(), want)
	}

	end := kept
	before := reset.GetOldest()
	pages := 0
	for {
		resp, err := client.Older(t.Context(), connect.NewRequest(&orreryv1.SessionsOlderRequest{
			Id: eventEngine, Before: before, Limit: 25,
		}))
		if err != nil {
			t.Fatalf("Older before seq %d: %v", before, err)
		}
		pages++
		items := resp.Msg.GetItems()
		start := end - len(items)
		if start < 0 || !sameItems(items, all[start:end]) {
			t.Fatalf("page before seq %d holds %d items, which are not the %d items before item %d",
				before, len(items), len(items), end)
		}
		if len(items) > 0 && resp.Msg.GetOldest() != int64(start+2) {
			t.Fatalf("page oldest = %d, want %d, the seq of its oldest item", resp.Msg.GetOldest(), start+2)
		}
		end = start
		if !resp.Msg.GetMore() {
			break
		}
		before = resp.Msg.GetOldest()
	}
	if end != 0 {
		t.Fatalf("the pages stop at item %d, want them to reach the first item", end)
	}
	if pages < 2 {
		t.Fatalf("the walk took %d page, want several", pages)
	}
}

// TestSessionsOlderChecksLimitAndAnswersTheDefault checks the limit Sessions.Older accepts: the default of 30 when it is
// 0, 100 at most, and invalid_argument above that.
func TestSessionsOlderChecksLimitAndAnswersTheDefault(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	seedSessionReplies(t, s, olderReplies)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))
	all := olderSessionConversation(olderReplies)

	_, err := client.Older(t.Context(), connect.NewRequest(&orreryv1.SessionsOlderRequest{
		Id: eventEngine, Before: 1000, Limit: 101,
	}))
	if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
		t.Fatalf("limit 101: code %v, want invalid_argument", code)
	}
	for _, limit := range []uint32{0, 100} {
		want := 30
		if limit == 100 {
			want = 100
		}
		resp, err := client.Older(t.Context(), connect.NewRequest(&orreryv1.SessionsOlderRequest{
			Id: eventEngine, Before: 1000, Limit: limit,
		}))
		if err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		if got := len(resp.Msg.GetItems()); got != want {
			t.Fatalf("limit %d: %d items, want %d", limit, got, want)
		}
		if !sameItems(resp.Msg.GetItems(), all[len(all)-want:]) || !resp.Msg.GetMore() {
			t.Fatalf("limit %d: page is not the newest %d items with more", limit, want)
		}
	}
}

// TestSessionsOlderAdmitsStepOnlyWhenLabeled checks that Sessions.Older answers a step session when StepStatus labels
// it, and answers not_found for an unlabeled step, a step StepStatus does not find, and a step with no step client.
func TestSessionsOlderAdmitsStepOnlyWhenLabeled(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addStepSession(t, s, stepSessionName)
	addStepSession(t, s, stepStatusUnlabeled)
	reader := store.NewReader(path)
	client := sessionsWatchClient(t, watchHandler(t, reader, &stepStatusFake{}))

	resp, err := client.Older(t.Context(), connect.NewRequest(&orreryv1.SessionsOlderRequest{
		Id: stepSessionName, Before: 1000,
	}))
	if err != nil {
		t.Fatalf("Older of labeled step: %v", err)
	}
	if want := []*orreryv1.ConversationItem{stepPromptItem(), reply(1)}; !sameItems(resp.Msg.GetItems(), want) ||
		resp.Msg.GetMore() || resp.Msg.GetOldest() != 2 {
		t.Fatalf("labeled step page = %v, want the prompt and reply 1 at seq 2, no more", resp.Msg)
	}

	unlabeled := sessionsWatchClient(t, watchHandler(t, reader, &stepStatusFake{}))
	if _, err := unlabeled.Older(t.Context(), connect.NewRequest(&orreryv1.SessionsOlderRequest{
		Id: stepStatusUnlabeled, Before: 1000,
	})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("unlabeled step: code %v, want not_found", connect.CodeOf(err))
	}
	noSteps := sessionsWatchClient(t, watchHandler(t, reader, nil))
	if _, err := noSteps.Older(t.Context(), connect.NewRequest(&orreryv1.SessionsOlderRequest{
		Id: stepSessionName, Before: 1000,
	})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("step with no step client: code %v, want not_found", connect.CodeOf(err))
	}
}

// TestPanesOlderPagesBackFromResetOldestAcrossTheTailCap pages Panes.Older back from the oldest item of a reset, over a
// transcript of more than 4 MiB, the size of the tail a reset reads. The reset passes its limit while it looks for the
// usage line, which is 200 replies above the start of the transcript's newest items, and no item is skipped: each page
// holds the items just before the one above it, and the offset of its oldest item's line, and the last page says no more.
func TestPanesOlderPagesBackFromResetOldestAcrossTheTailCap(t *testing.T) {
	t.Parallel()
	const replies = 4500
	path, all, starts := writeOlderTranscript(t, replies, replies-200)
	if size := fileSize(t, path); size <= conversation.MaxTailBytes {
		t.Fatalf("transcript is %d bytes, want more than the %d-byte tail", size, conversation.MaxTailBytes)
	}
	tmux := newTmuxServer(t)
	pane := conversationPane(t, tmux, liveOptions(path))
	client := panesClient(t, convHandler(t, tmux, nil))

	const resetLimit = 40
	reset := openConversation(t, client, &orreryv1.WatchConversationRequest{Pane: pane, Limit: resetLimit}).next(t)
	kept := len(all) - resetLimit
	if !reset.GetReset_() || !sameItems(reset.GetItems(), all[kept:]) {
		t.Fatalf("reset items = %d, want the newest %d", len(reset.GetItems()), resetLimit)
	}
	if got := reset.GetState().GetContext().GetTokens(); got != 12345 {
		t.Fatalf("context tokens = %d, want 12345 from the usage line past the limit", got)
	}
	if reset.GetOldest() != starts[kept] {
		t.Fatalf("reset oldest = %d, want the line start %d of its oldest item", reset.GetOldest(), starts[kept])
	}

	end := kept
	before := reset.GetOldest()
	for {
		resp, err := client.Older(t.Context(), connect.NewRequest(&orreryv1.OlderRequest{
			Pane: pane, Transcript: filepath.Base(path), Before: before, Limit: 37,
		}))
		if err != nil {
			t.Fatalf("Older before %d: %v", before, err)
		}
		items := resp.Msg.GetItems()
		start := end - len(items)
		if start < 0 || !sameItems(items, all[start:end]) {
			t.Fatalf("page before %d does not hold the %d items before item %d", before, len(items), end)
		}
		if len(items) > 0 && resp.Msg.GetOldest() != starts[start] {
			t.Fatalf("page oldest = %d, want the line start %d of item %d", resp.Msg.GetOldest(), starts[start], start)
		}
		end = start
		if !resp.Msg.GetMore() {
			break
		}
		before = resp.Msg.GetOldest()
	}
	if end != 0 {
		t.Fatalf("the pages stop at item %d, want them to reach the first item", end)
	}
}

// TestPanesOlderRejectsStaleTranscriptAndBadRequests checks the errors Panes.Older answers: failed_precondition for a
// transcript the client does not hold, invalid_argument for an offset that is not a line start and for a limit above
// 100, and not_found for an absent pane. A request with no limit answers the default of 30.
func TestPanesOlderRejectsStaleTranscriptAndBadRequests(t *testing.T) {
	t.Parallel()
	path, all, starts := writeOlderTranscript(t, 10, 0)
	tmux := newTmuxServer(t)
	pane := conversationPane(t, tmux, liveOptions(path))
	client := panesClient(t, convHandler(t, tmux, nil))
	base := filepath.Base(path)

	cases := []struct {
		name string
		req  *orreryv1.OlderRequest
		code connect.Code
	}{
		{"stale transcript", &orreryv1.OlderRequest{Pane: pane, Transcript: "other.jsonl", Before: starts[5]},
			connect.CodeFailedPrecondition},
		{"no transcript held", &orreryv1.OlderRequest{Pane: pane, Before: starts[5]}, connect.CodeFailedPrecondition},
		{"offset inside a line", &orreryv1.OlderRequest{Pane: pane, Transcript: base, Before: starts[5] + 3},
			connect.CodeInvalidArgument},
		{"offset past the file", &orreryv1.OlderRequest{Pane: pane, Transcript: base, Before: fileSize(t, path) + 1},
			connect.CodeInvalidArgument},
		{"limit above 100", &orreryv1.OlderRequest{Pane: pane, Transcript: base, Before: starts[5], Limit: 101},
			connect.CodeInvalidArgument},
		{"malformed pane", &orreryv1.OlderRequest{Pane: "pane", Transcript: base, Before: starts[5]},
			connect.CodeInvalidArgument},
		{"absent pane", &orreryv1.OlderRequest{Pane: "%99999", Transcript: base, Before: starts[5]},
			connect.CodeNotFound},
	}
	for _, tc := range cases {
		if _, err := client.Older(t.Context(), connect.NewRequest(tc.req)); connect.CodeOf(err) != tc.code {
			t.Fatalf("%s: code %v, want %v", tc.name, connect.CodeOf(err), tc.code)
		}
	}

	resp, err := client.Older(t.Context(), connect.NewRequest(&orreryv1.OlderRequest{
		Pane: pane, Transcript: base, Before: starts[5],
	}))
	if err != nil {
		t.Fatalf("Older with no limit: %v", err)
	}
	if !sameItems(resp.Msg.GetItems(), all[:5]) || resp.Msg.GetOldest() != starts[0] || resp.Msg.GetMore() {
		t.Fatalf("page before item 5 = %v, want items 0 to 4 from line start %d with no more", resp.Msg, starts[0])
	}
}
