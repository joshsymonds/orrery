package server_test

import (
	"net/http"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// watchReadsWanted is the number of reads a stream held open must report: one at its first step and three more, one
// per keepalive interval.
const watchReadsWanted = 4

// watchRead is one read report a stream made, with the real time it arrived.
type watchRead struct {
	pane string
	at   int64
	when time.Time
}

// watchReads records the read reports of the streams under test. A stream reports from its own goroutine, so the
// reports are guarded by mu.
type watchReads struct {
	mu    sync.Mutex
	reads []watchRead
}

// report records a read of pane at the unix seconds at. It is the reporter the handler is built with.
func (r *watchReads) report(pane string, at int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads = append(r.reads, watchRead{pane: pane, at: at, when: time.Now()})
}

// snapshot returns the reads recorded so far.
func (r *watchReads) snapshot() []watchRead {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.reads)
}

// newWatchReadHandler builds the guarded handler that admits allowedLogin and routes to tmux, polling each
// conversation every convPoll and keeping it alive every convKeepalive. It dates reads at readFixtureAt and reports
// each one to report.
func newWatchReadHandler(t *testing.T, tmux *tmuxctl.Client, report server.ReadReporter) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:                []string{allowedLogin},
		Tmux:                  tmux,
		Push:                  newPushStore(t),
		Now:                   func() time.Time { return time.Unix(readFixtureAt, 0) },
		ReportRead:            report,
		ConversationPoll:      convPoll,
		ConversationKeepalive: convKeepalive,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

func TestWatchConversationReadsPaneAtOpen(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane := conversationPane(t, tmux, liveOptions(writeConversation(t, 1)))
	var reads watchReads
	client := panesClient(t, newWatchReadHandler(t, tmux, reads.report))
	stream := openConversation(t, client, &orreryv1.WatchConversationRequest{Pane: pane})

	stream.next(t)
	got := reads.snapshot()
	if len(got) == 0 {
		t.Fatal("the first message came with no read reported")
	}
	if got[0].pane != pane || got[0].at != readFixtureAt {
		t.Fatalf("first read = pane %q at %d, want pane %q at %d", got[0].pane, got[0].at, pane, readFixtureAt)
	}
}

func TestWatchConversationReadsEachKeepalive(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane := conversationPane(t, tmux, liveOptions(writeConversation(t, 1)))
	var reads watchReads
	client := panesClient(t, newWatchReadHandler(t, tmux, reads.report))
	stream := openConversation(t, client, &orreryv1.WatchConversationRequest{Pane: pane})

	// The stream sends a keepalive each interval, so reading its messages keeps it moving until the reads arrive.
	deadline := time.Now().Add(watchWait)
	for len(reads.snapshot()) < watchReadsWanted && time.Now().Before(deadline) {
		stream.next(t)
	}
	got := reads.snapshot()
	if len(got) < watchReadsWanted {
		t.Fatalf("reads = %d while the stream stayed open, want at least %d", len(got), watchReadsWanted)
	}
	for i, read := range got {
		if read.pane != pane || read.at != readFixtureAt {
			t.Fatalf("read %d = pane %q at %d, want pane %q at %d", i, read.pane, read.at, pane, readFixtureAt)
		}
		if i > 0 && read.when.Sub(got[i-1].when) < convKeepalive {
			t.Fatalf("read %d came %v after read %d, want at least the keepalive %v",
				i, read.when.Sub(got[i-1].when), i-1, convKeepalive)
		}
	}
}

func TestWatchConversationReadsNothingWhenStartFails(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	// A live session keeps the tmux server running, and the absent pane is absent from its listing.
	startSession(t, tmux, "unfound", longCommand)
	unfound := paneID(t, tmux, "unfound")
	for option, value := range liveOptions(filepath.Join(t.TempDir(), "missing.jsonl")) {
		setPaneOption(t, tmux, unfound, option, value)
	}
	cases := []struct {
		name    string
		pane    string
		code    connect.Code
		message string
	}{
		{"absent pane", "%999999", connect.CodeNotFound, "pane not found"},
		{"transcript file missing", unfound, connect.CodeNotFound, "transcript not found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var reads watchReads
			client := panesClient(t, newWatchReadHandler(t, tmux, reads.report))
			stream := openConversation(t, client, &orreryv1.WatchConversationRequest{Pane: c.pane})
			assertConnectError(t, stream.end(t), c.code, c.message)
			if got := reads.snapshot(); len(got) != 0 {
				t.Fatalf("reads = %+v, want none for a stream that failed its first step", got)
			}
		})
	}
}

func TestWatchConversationStreamsWithoutReadReporter(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane := conversationPane(t, tmux, liveOptions(writeConversation(t, 1)))
	client := panesClient(t, newWatchReadHandler(t, tmux, nil))
	stream := openConversation(t, client, &orreryv1.WatchConversationRequest{Pane: pane})

	first := stream.next(t)
	if got := first.GetState().GetOrigin(); got != synthOrigin {
		t.Fatalf("first state origin = %q, want %q", got, synthOrigin)
	}
	stream.next(t)
}
