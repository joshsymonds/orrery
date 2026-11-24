package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/conversation"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/inotify"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// defaultConversationKeepalive is the WatchConversation keepalive interval when Config.ConversationKeepalive is
// zero.
const defaultConversationKeepalive = 5 * time.Second

// WatchConversation answers Panes.WatchConversation with the stream of the pane's conversation. The limit and the
// pane are checked as Conversation checks them. The stream sends its first delta at once, then one delta each time its
// transcript's inotify watch wakes it and that finds new complete transcript lines or a changed state, or the change
// feed names the pane's session and the pane has changed. It sends a keepalive once conversationKeepalive has passed
// without a message. The stream ends with the error Conversation gives for the pane or its transcript, and returns when
// the client leaves, when serve shuts down, or when a send stays blocked past the send deadline. Its send and
// end-of-stream writes are bounded as Events.Watch's are (see endWithin).
func (s panesServer) WatchConversation(
	ctx context.Context,
	req *connect.Request[v1.WatchConversationRequest],
	stream *connect.ServerStream[v1.WatchConversationResponse],
) error {
	query := url.Values{}
	if req.Msg.GetLimit() != 0 {
		query.Set(queryLimit, strconv.FormatUint(uint64(req.Msg.GetLimit()), 10))
	}
	limit, ok := conversationLimit(query)
	if !ok {
		return connectError(routeError{status: http.StatusBadRequest, message: badQueryMessage})
	}
	if !validPaneID(req.Msg.GetPane()) {
		return connectError(paneRouteError(errMalformedPaneID))
	}
	watch := &conversationWatch{
		panes: s,
		pane:  req.Msg.GetPane(),
		limit: limit,
		held:  req.Msg.GetTranscript(),
		after: req.Msg.GetAfter(),
		sends: s.echoSends(ctx, req.Msg.GetPane()),
	}
	return endWithin(ctx, s.runWatch(ctx, watch, stream), s.sendTimeout, s.shutdown)
}

// runWatch sends the deltas of watch on stream until the client leaves, the pane's conversation ends, serve shuts
// down, or a send stays blocked past the send deadline. The stream's content comes from its transcript file: it holds
// one inotify instance (see transcriptWatch), opened through openInotify, which watches the transcript the pane names
// and its directory, and is closed when the stream ends. It steps watch once at the start, and again each time that
// watch wakes it. The change feed carries the hooks' changes to the pane, so a feed wake reads the pane and steps only
// when the pane has changed or names another transcript, which the inotify watch is then re-armed on. A step names the
// session of the pane, so the feed wakes the stream only for that session once the pane has one. A keepalive is sent,
// without a step, once conversationKeepalive has passed since the last message. A step that sends a delta reports the
// pane as read when conversationKeepalive has passed since the last report, and a keepalive reports it too. A failed
// step reports nothing.
//
// A stream whose inotify watch is down, because the kernel refused the instance or a watch, falls back to stepping on
// every feed wake, as it did before it had the watch, and to stepping before each keepalive, so a transcript that grows
// with no hook to tell of it is still read within conversationKeepalive.
func (s panesServer) runWatch(
	ctx context.Context,
	watch *conversationWatch,
	stream *connect.ServerStream[v1.WatchConversationResponse],
) error {
	wake, follow, cancel := s.changes.subscribeFiltered(ctx)
	defer cancel()
	open := s.openInotify
	if open == nil {
		open = inotify.New
	}
	files := newTranscriptWatch(open)
	run := &paneWatch{server: s, watch: watch, stream: stream, follow: follow, files: files}
	err := run.loop(ctx, wake)
	run.stopTimer()
	if closeErr := files.close(); closeErr != nil {
		return errors.Join(err, fmt.Errorf("close transcript watch: %w", closeErr))
	}
	return err
}

// paneWatch is the state of one runWatch: the stream it sends on, the follow function of its feed subscription, the
// inotify watch of its transcript, the keepalive timer armed after its last message, and the server time of its last
// pane read. reported is false until the first read.
type paneWatch struct {
	server    panesServer
	watch     *conversationWatch
	stream    *connect.ServerStream[v1.WatchConversationResponse]
	follow    func(session string)
	files     *transcriptWatch
	keepalive <-chan time.Time
	stop      func() bool
	lastRead  time.Time
	reported  bool
}

// loop steps the stream once, then waits for its wakes and its keepalive timer, as runWatch documents, until the stream
// ends. wake is the stream's change-feed wake.
func (w *paneWatch) loop(ctx context.Context, wake <-chan struct{}) error {
	if _, err := w.step(ctx, true); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("watch conversation: %w", ctx.Err())
		case <-w.server.shutdown:
			return shutdownError()
		case <-w.files.wake:
			if _, err := w.step(ctx, true); err != nil {
				return err
			}
		case <-wake:
			if _, err := w.step(ctx, false); err != nil {
				return err
			}
		case <-w.keepalive:
			if err := w.keepaliveStep(ctx); err != nil {
				return err
			}
		}
	}
}

// step reads the pane, names its session, and arms the transcript watch on the pane's transcript when it names another
// one, or when the watch has marked the transcript replaced. A replaced transcript is then read from the start. A
// content step, the stream's first and each one its transcript watch wakes, then steps the watch. A feed step steps it
// only when the pane names another transcript, the transcript was replaced, the pane differs from the one the stream
// last sent, or the transcript watch is down. step sends the delta the step gives, if any, and reports whether it sent
// one. The pane is reported as read when the step sends and the last report is at least conversationKeepalive old, or
// when nothing has been reported. The watch is armed before the transcript is read, so a line written after that read
// wakes the stream.
func (w *paneWatch) step(ctx context.Context, content bool) (bool, error) {
	if stopping(w.server.shutdown) {
		return false, shutdownError()
	}
	pane, path, err := w.watch.readPane(ctx)
	if err != nil {
		return false, connectError(err)
	}
	if id := pane.GetOrreryId(); id != "" {
		w.follow(id)
	}
	armed := w.track(path)
	if !content && !armed && w.files.up() && proto.Equal(pane, w.watch.sent.GetPane()) {
		return false, nil
	}
	msg, sends, err := w.watch.stepAt(pane, path)
	if err != nil {
		return false, connectError(err)
	}
	if !sends {
		return false, nil
	}
	if now := w.server.now(); !w.reported || now.Sub(w.lastRead) >= w.server.conversationKeepalive {
		w.server.reportRead(w.watch.pane)
		w.lastRead, w.reported = now, true
	}
	return true, w.send(ctx, msg)
}

// track arms the transcript watch on path when path is not the transcript it was armed on, or when the watch has
// marked the transcript replaced, and makes the stream forget its place in a replaced transcript. It reports whether
// it armed the watch, and so whether the next read of the transcript must happen.
func (w *paneWatch) track(path string) bool {
	replaced := w.files.takeReplaced()
	moved := path != w.files.armed()
	if moved || replaced {
		w.files.arm(path)
	}
	if replaced {
		w.watch.forget()
	}
	return moved || replaced
}

// keepaliveStep answers the keepalive timer. While the transcript watch is up it sends a keepalive and does not step.
// While it is down the stream steps first, and sends the keepalive only when that step sent nothing.
func (w *paneWatch) keepaliveStep(ctx context.Context) error {
	if !w.files.up() {
		sent, err := w.step(ctx, true)
		if err != nil || sent {
			return err
		}
	}
	return w.keepaliveSend(ctx)
}

// keepaliveSend reports the pane as read and sends a keepalive for the stream's position. It does not step, so the
// pane is not read.
func (w *paneWatch) keepaliveSend(ctx context.Context) error {
	if stopping(w.server.shutdown) {
		return shutdownError()
	}
	w.server.reportRead(w.watch.pane)
	w.lastRead, w.reported = w.server.now(), true
	return w.send(ctx, &v1.WatchConversationResponse{Transcript: w.watch.id, Seq: w.watch.seq})
}

// send sends msg on the stream, and arms the keepalive timer for the next keepalive.
func (w *paneWatch) send(ctx context.Context, msg *v1.WatchConversationResponse) error {
	if err := sendWithin(ctx, w.server.sendTimeout, w.server.shutdown, w.stream, msg); err != nil {
		return fmt.Errorf("send conversation delta: %w", err)
	}
	w.stopTimer()
	w.keepalive, w.stop = w.server.newTimer(w.server.conversationKeepalive)
	return nil
}

// stopTimer stops the keepalive timer armed after the last message, if one is armed.
func (w *paneWatch) stopTimer() {
	if w.stop != nil {
		w.stop()
	}
}

// conversationWatch is one WatchConversation call: the request it answers and the position its stream has reached.
// The stream is sent its first delta from the held position when the request holds the transcript, and from the
// newest items otherwise, and it keeps following the transcript it last read.
type conversationWatch struct {
	panes panesServer
	pane  string
	limit int
	// held and after are the transcript id and seq the client holds. They place the first delta only.
	held  string
	after int64
	// id is the base name of the transcript the stream last sent, and path is the transcript it follows.
	id   string
	path string
	// seq is the offset just past the last complete line the stream has covered.
	seq int64
	// origin and figure are the origin prompt and the context figure of the transcript at path.
	origin string
	figure conversation.Context
	// sent is the state the stream last sent, or nil before its first delta.
	sent *v1.ConversationState
	// sends reads the pane's sends for the echo marking of each delta's prompts, or is nil when there is no store reader
	// (see markPaneEchoes).
	sends sendsFunc
	// betweenReads, when set, runs in each step after the transcript's goal and waiting messages are read and before
	// its delta is. A test sets it to append to the transcript there. It is nil in production.
	betweenReads func()
}

// readPane reads the pane, and returns it and the transcript path of its live session.
func (w *conversationWatch) readPane(ctx context.Context) (*v1.Pane, string, error) {
	if w.panes.stepped != nil {
		w.panes.stepped(w.pane)
	}
	return w.panes.livePane(ctx, w.pane)
}

// forget drops the stream's place in its transcript, so the next step resets the stream from the newest items of the
// pane's transcript. A stream forgets its place when its transcript was replaced, or its events were lost.
func (w *conversationWatch) forget() {
	w.path = ""
}

// stepAt reads the transcript at path, the live transcript of pane, once. It returns the delta to send and true, or
// false when neither the transcript nor the state has changed. The delta resets the stream when the transcript was
// replaced or the stream has no position in it. A reset, and the first delta, carry the whole state. Later deltas carry
// the state only when it changed. The goal and the waiting messages are read before the delta, and the transcript only
// grows, so the seq a delta reaches covers every line its state was read from: a line appended between the two reads
// moves the seq now and the state at the next step, never the state ahead of its seq.
func (w *conversationWatch) stepAt(pane *v1.Pane, path string) (*v1.WatchConversationResponse, bool, error) {
	goal, waiting, err := w.transcriptState(pane, path)
	if err != nil {
		return nil, false, err
	}
	if w.betweenReads != nil {
		w.betweenReads()
	}
	before := w.seq
	items, oldest, reset, err := w.readDelta(path)
	if err != nil {
		return nil, false, err
	}
	w.id = filepath.Base(path)
	state := &v1.ConversationState{
		Origin:  w.origin,
		Goal:    goal,
		Waiting: waitingMessages(waiting),
		Context: &v1.ConversationContext{Tokens: w.figure.Tokens, Window: w.figure.Window},
		Pane:    pane,
	}
	stateChanged := reset || w.sent == nil || !proto.Equal(state, w.sent)
	if !stateChanged && w.seq == before && len(items) == 0 {
		return nil, false, nil
	}
	msg := &v1.WatchConversationResponse{
		Transcript: w.id,
		Seq:        w.seq,
		Reset_:     reset,
		Items:      conversationItems(items),
	}
	msg.Oldest = oldest
	if stateChanged {
		msg.State = state
		w.sent = state
	}
	return msg, true, nil
}

// readDelta returns the items the stream adds from the transcript at path, the line start of the oldest of them when
// they reset the stream (0 otherwise), and whether they reset the stream. The first read resumes at the held seq when
// the held transcript id is the id of path and Since accepts that seq. Any other read follows the stream's transcript,
// and resets it when path is another transcript or Since refuses the stream's seq.
func (w *conversationWatch) readDelta(path string) ([]conversation.Item, int64, bool, error) {
	if w.sent == nil {
		if filepath.Base(path) == w.held {
			items, ok, err := w.resume(path)
			if ok || err != nil {
				return items, 0, false, err
			}
		}
		items, oldest, err := w.reset(path)
		return items, oldest, true, err
	}
	if path == w.path {
		items, ok, err := w.since()
		if ok || err != nil {
			return items, 0, false, err
		}
	}
	items, oldest, err := w.reset(path)
	return items, oldest, true, err
}

// resume starts the stream at the held seq of path. It reports false when Since refuses that seq.
func (w *conversationWatch) resume(path string) ([]conversation.Item, bool, error) {
	head, _, err := conversation.ReadEnd(path, 0)
	if err != nil {
		return nil, false, transcriptError(err)
	}
	w.path, w.seq, w.origin, w.figure = path, w.after, head.Origin, head.Context
	return w.since()
}

// since returns the items of the complete lines of the stream's transcript after its seq, and moves the seq past
// them. It reports false, and moves nothing, when Since refuses the seq.
func (w *conversationWatch) since() ([]conversation.Item, bool, error) {
	follow, err := conversation.Since(w.path, w.seq)
	if errors.Is(err, conversation.ErrOffset) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, transcriptError(err)
	}
	start := w.seq
	w.seq = follow.End
	if follow.HasTokens {
		w.figure.Tokens = follow.Tokens
	}
	if markErr := markPaneEchoes(follow.Items, start, w.sends, earlierIn(w.path)); markErr != nil {
		return nil, false, markErr
	}
	return follow.Items, true, nil
}

// reset makes path the stream's transcript, and returns its newest items, and the line start of the oldest of them, with
// the seq, origin and context figure the transcript has now.
func (w *conversationWatch) reset(path string) ([]conversation.Item, int64, error) {
	conv, end, err := conversation.ReadEnd(path, w.limit)
	if err != nil {
		return nil, 0, transcriptError(err)
	}
	w.path, w.seq, w.origin, w.figure = path, end, conv.Origin, conv.Context
	if markErr := markPaneEchoes(conv.Items, conv.Oldest, w.sends, earlierIn(path)); markErr != nil {
		return nil, 0, markErr
	}
	return conv.Items, conv.Oldest, nil
}

// transcriptState returns the goal and the waiting messages of the transcript at path. The waiting messages are idle
// when pane runs no turn (see protocol.Idle).
func (w *conversationWatch) transcriptState(pane *v1.Pane, path string) (string, []conversation.Waiting, error) {
	goal, err := w.panes.goals.Goal(path)
	if err != nil {
		return "", nil, transcriptError(err)
	}
	idle := protocol.Idle(protocol.State(pane.GetState()), pane.GetHeld())
	waiting, err := w.panes.queues.Waiting(path, idle)
	if err != nil {
		return "", nil, transcriptError(err)
	}
	return goal, waiting, nil
}

// livePane returns the Pane message Panes.List gives for the pane with id, and the transcript path of its live
// session. It fails with the errors Conversation gives for the same pane.
func (s panesServer) livePane(ctx context.Context, id string) (*v1.Pane, string, error) {
	panes, err := listPanes(ctx, s.tmux, s.host, s.newest, s.reader)
	if err != nil {
		return nil, "", err
	}
	msgs, err := paneMessages(paneObjects(panes))
	if err != nil {
		return nil, "", err
	}
	if markErr := markPanePutOff(ctx, s.reader, msgs); markErr != nil {
		return nil, "", markErr
	}
	var pane *v1.Pane
	var transcript string
	for i, msg := range msgs {
		if msg.GetId() == id {
			pane = msg
			transcript = panes[i].listed.Transcript
			break
		}
	}
	if pane == nil {
		return nil, "", routeError{status: http.StatusNotFound, message: "pane not found"}
	}
	if pane.GetState() == "" {
		return nil, "", routeError{status: http.StatusNotFound, message: "no live session"}
	}
	if transcript == "" {
		return nil, "", routeError{status: http.StatusNotFound, message: "no transcript"}
	}
	return pane, transcript, nil
}

// transcriptError returns the routeError of a failed transcript read: 404 when the file is missing, and internal
// otherwise, as conversationAnswer maps them.
func transcriptError(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return routeError{status: http.StatusNotFound, message: "transcript not found"}
	}
	return internalError()
}
