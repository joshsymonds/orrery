package server

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/inotify"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// inotifyHangGuard bounds each wait of the inotify tests for a message, so a stream that is never woken fails its test
// rather than hanging the run. A passing run never waits this long.
const inotifyHangGuard = 30 * time.Second

// inotifyNext returns the next message of st, failing the test when the stream ends first or sends nothing within the
// hang guard.
func inotifyNext(t *testing.T, st *paneFeedStream) *orreryv1.WatchConversationResponse {
	t.Helper()
	guard := time.NewTimer(inotifyHangGuard)
	defer guard.Stop()
	select {
	case msg, ok := <-st.msgs:
		if !ok {
			t.Fatalf("stream ended before the next message: %v", st.err)
		}
		return msg
	case <-guard.C:
		t.Fatalf("no message within the %v hang guard", inotifyHangGuard)
		return nil
	}
}

// inotifyWrite writes text to a new file name in dir, and returns its path.
func inotifyWrite(t *testing.T, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// inotifyWantReset fails the test unless msg is a reset of the transcript with the base name id, with the items want.
func inotifyWantReset(t *testing.T, msg *orreryv1.WatchConversationResponse, id string, want ...string) {
	t.Helper()
	if !msg.GetReset_() || msg.GetTranscript() != id {
		t.Fatalf("delta reset=%v transcript=%q, want a reset of %q", msg.GetReset_(), msg.GetTranscript(), id)
	}
	paneFeedWantItems(t, msg, want...)
}

// inotifyOpens records the inotify instances the pane streams of a rig open.
type inotifyOpens struct {
	mu        sync.Mutex
	instances []*inotify.Inotify
}

// open opens an instance through inotify.New and records it.
func (o *inotifyOpens) open() (*inotify.Inotify, error) {
	ino, err := inotify.New()
	if err != nil {
		return nil, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.instances = append(o.instances, ino)
	return ino, nil
}

// opened returns the instances opened so far.
func (o *inotifyOpens) opened() []*inotify.Inotify {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]*inotify.Inotify(nil), o.instances...)
}

// TestConversationInotifyAppendDelivers proves that a line appended to a pane's transcript reaches its stream with no
// change-feed message: the transcript's inotify watch wakes the stream.
func TestConversationInotifyAppendDelivers(t *testing.T) {
	t.Parallel()
	rig := paneFeedSetup(t)
	path := paneFeedTranscript(t)
	p := paneFeedPane(t, rig.tmux, "inotify-p", path, paneFeedOrreryP)
	st := paneFeedOpen(t, rig, p)
	inotifyNext(t, st)

	paneFeedAppend(t, path, paneFeedReply(2))
	msg := inotifyNext(t, st)
	if msg.GetReset_() || msg.GetTranscript() != filepath.Base(path) {
		t.Fatalf("appended delta reset=%v transcript=%q, want a plain delta of %q",
			msg.GetReset_(), msg.GetTranscript(), filepath.Base(path))
	}
	paneFeedWantItems(t, msg, "synthetic reply 2")

	// A second write lands as well: the watch keeps waking the stream.
	paneFeedAppend(t, path, paneFeedReply(3))
	paneFeedWantItems(t, inotifyNext(t, st), "synthetic reply 3")
}

// TestConversationInotifyPathChangeRearms proves that a feed message whose only news is that the pane names another
// transcript makes the stream reset onto that transcript, and re-arms its watch there: a line appended to the new
// transcript reaches the stream with no further feed message.
func TestConversationInotifyPathChangeRearms(t *testing.T) {
	t.Parallel()
	rig := paneFeedSetup(t)
	p := paneFeedPane(t, rig.tmux, "inotify-p", paneFeedTranscript(t), paneFeedOrreryP)
	st := paneFeedOpen(t, rig, p)
	inotifyNext(t, st)
	call := <-rig.changes.calls

	second := inotifyWrite(t, t.TempDir(), "second.jsonl", paneFeedPrompt+paneFeedReply(5))
	if err := rig.tmux.SetPaneOption(t.Context(), p, protocol.OptionTranscript, second); err != nil {
		t.Fatalf("SetPaneOption(%s, %s): %v", p, protocol.OptionTranscript, err)
	}
	call.msgs <- &enginev1.ChangesWatchResponse{Seq: 1, Sessions: []string{paneFeedOrreryP}}
	inotifyWantReset(t, inotifyNext(t, st), "second.jsonl", "synthetic prompt", "synthetic reply 5")

	paneFeedAppend(t, second, paneFeedReply(6))
	msg := inotifyNext(t, st)
	if msg.GetReset_() || msg.GetTranscript() != "second.jsonl" {
		t.Fatalf("growth after the switch reset=%v transcript=%q, want a plain delta of second.jsonl",
			msg.GetReset_(), msg.GetTranscript())
	}
	paneFeedWantItems(t, msg, "synthetic reply 6")
}

// TestConversationInotifyRenameReplaces proves that a transcript replaced by a rename onto its path, which writes
// nothing to the file the stream was reading, resets the stream onto the new file, and that the watch follows the new
// file: a line appended to it reaches the stream.
func TestConversationInotifyRenameReplaces(t *testing.T) {
	t.Parallel()
	rig := paneFeedSetup(t)
	path := paneFeedTranscript(t)
	p := paneFeedPane(t, rig.tmux, "inotify-p", path, paneFeedOrreryP)
	st := paneFeedOpen(t, rig, p)
	inotifyNext(t, st)

	replacement := inotifyWrite(t, filepath.Dir(path), "replacement.tmp",
		paneFeedPrompt+paneFeedReply(7)+paneFeedReply(8))
	if err := os.Rename(replacement, path); err != nil {
		t.Fatalf("rename the replacement onto the transcript: %v", err)
	}
	inotifyWantReset(t, inotifyNext(t, st), filepath.Base(path),
		"synthetic prompt", "synthetic reply 7", "synthetic reply 8")

	paneFeedAppend(t, path, paneFeedReply(9))
	paneFeedWantItems(t, inotifyNext(t, st), "synthetic reply 9")
}

// TestConversationInotifyClosesWatchOnEnd proves that a stream holds one inotify instance, through a re-arm onto
// another transcript, and closes it when the stream ends. Closing the Panes server is the barrier: it returns once
// every handler has returned, and so once the stream has given up its watch.
func TestConversationInotifyClosesWatchOnEnd(t *testing.T) {
	t.Parallel()
	opens := &inotifyOpens{}
	rig := paneFeedSetupWith(t, opens.open)
	p := paneFeedPane(t, rig.tmux, "inotify-p", paneFeedTranscript(t), paneFeedOrreryP)
	st := paneFeedOpen(t, rig, p)
	inotifyNext(t, st)
	call := <-rig.changes.calls

	second := inotifyWrite(t, t.TempDir(), "second.jsonl", paneFeedPrompt+paneFeedReply(5))
	if err := rig.tmux.SetPaneOption(t.Context(), p, protocol.OptionTranscript, second); err != nil {
		t.Fatalf("SetPaneOption(%s, %s): %v", p, protocol.OptionTranscript, err)
	}
	call.msgs <- &enginev1.ChangesWatchResponse{Seq: 1, Sessions: []string{paneFeedOrreryP}}
	inotifyWantReset(t, inotifyNext(t, st), "second.jsonl", "synthetic prompt", "synthetic reply 5")

	paneFeedStop(st)
	rig.panesSrv.Close()
	opened := opens.opened()
	if len(opened) != 1 {
		t.Fatalf("stream opened %d inotify instances, want 1", len(opened))
	}
	if _, err := opened[0].Add(filepath.Dir(second), unix.IN_CREATE); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Add on the ended stream's instance: %v, want %v", err, os.ErrClosed)
	}
}

// TestConversationInotifyDownFallsBack proves that a stream whose inotify instance the kernel refuses still delivers
// the transcript's lines: each keepalive tick steps first, and so does each feed wake, as before the stream had a
// watch.
func TestConversationInotifyDownFallsBack(t *testing.T) {
	t.Parallel()
	rig := paneFeedSetupWith(t, func() (*inotify.Inotify, error) { return nil, unix.EMFILE })
	path := paneFeedTranscript(t)
	p := paneFeedPane(t, rig.tmux, "inotify-p", path, paneFeedOrreryP)
	st := paneFeedOpen(t, rig, p)
	inotifyNext(t, st)
	keepalive := <-rig.keeps.armed
	call := <-rig.changes.calls

	paneFeedAppend(t, path, paneFeedReply(2))
	keepalive.fire <- time.Time{}
	paneFeedWantItems(t, inotifyNext(t, st), "synthetic reply 2")

	paneFeedAppend(t, path, paneFeedReply(3))
	call.msgs <- &enginev1.ChangesWatchResponse{Seq: 1, Sessions: []string{paneFeedOrreryP}}
	paneFeedWantItems(t, inotifyNext(t, st), "synthetic reply 3")
}
