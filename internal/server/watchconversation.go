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
	"github.com/joshsymonds/orrery/internal/protocol"
)

// defaultConversationPoll is the WatchConversation poll interval when Config.ConversationPoll is zero.
const defaultConversationPoll = 500 * time.Millisecond

// defaultConversationKeepalive is the WatchConversation keepalive interval when Config.ConversationKeepalive is
// zero.
const defaultConversationKeepalive = 5 * time.Second

// WatchConversation answers Panes.WatchConversation with the stream of the pane's conversation. The limit and the
// pane are checked as Conversation checks them. The stream sends its first delta at once, then one delta for each
// poll that finds new complete transcript lines or a changed state, and a keepalive once conversationKeepalive has
// passed without a delta. The stream ends with the error Conversation gives for the pane or its transcript, and
// returns when the client leaves, when serve shuts down, or when a send stays blocked past the send deadline. Its
// send and end-of-stream writes are bounded as Events.Watch's are (see endWithin).
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
	}
	return endWithin(ctx, s.runWatch(ctx, watch, stream), s.sendTimeout, s.shutdown)
}

// runWatch sends the deltas of watch on stream until the client leaves, the pane's conversation ends, serve shuts
// down, or a send stays blocked past the send deadline. It steps watch every conversationPoll, and sends a keepalive
// once conversationKeepalive has passed since the last message.
func (s panesServer) runWatch(
	ctx context.Context,
	watch *conversationWatch,
	stream *connect.ServerStream[v1.WatchConversationResponse],
) error {
	ticker := time.NewTicker(s.conversationPoll)
	defer ticker.Stop()
	lastSent := time.Now()
	for {
		if stopping(s.shutdown) {
			return shutdownError()
		}
		msg, send, err := watch.step(ctx)
		if err != nil {
			return connectError(err)
		}
		if !send && time.Since(lastSent) >= s.conversationKeepalive {
			msg, send = &v1.WatchConversationResponse{Transcript: watch.id, Seq: watch.seq}, true
		}
		if send {
			if sendErr := sendWithin(ctx, s.sendTimeout, s.shutdown, stream, msg); sendErr != nil {
				return fmt.Errorf("send conversation delta: %w", sendErr)
			}
			lastSent = time.Now()
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("watch conversation: %w", ctx.Err())
		case <-s.shutdown:
			return shutdownError()
		case <-ticker.C:
		}
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
}

// step reads the pane and its transcript once. It returns the delta to send and true, or false when neither the
// transcript nor the state has changed. The delta resets the stream when the transcript was replaced or the stream
// has no position in it. A reset, and the first delta, carry the whole state. Later deltas carry the state only when
// it changed.
func (w *conversationWatch) step(ctx context.Context) (*v1.WatchConversationResponse, bool, error) {
	pane, path, err := w.panes.livePane(ctx, w.pane)
	if err != nil {
		return nil, false, err
	}
	before := w.seq
	items, reset, err := w.readDelta(path)
	if err != nil {
		return nil, false, err
	}
	w.id = filepath.Base(path)
	state, err := w.state(pane, path)
	if err != nil {
		return nil, false, err
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
	if stateChanged {
		msg.State = state
		w.sent = state
	}
	return msg, true, nil
}

// readDelta returns the items the stream adds from the transcript at path, and whether they reset the stream. The
// first read resumes at the held seq when the held transcript id is the id of path and Since accepts that seq. Any
// other read follows the stream's transcript, and resets it when path is another transcript or Since refuses the
// stream's seq.
func (w *conversationWatch) readDelta(path string) ([]conversation.Item, bool, error) {
	if w.sent == nil {
		if filepath.Base(path) == w.held {
			items, ok, err := w.resume(path)
			if ok || err != nil {
				return items, false, err
			}
		}
		items, err := w.reset(path)
		return items, true, err
	}
	if path == w.path {
		items, ok, err := w.since()
		if ok || err != nil {
			return items, false, err
		}
	}
	items, err := w.reset(path)
	return items, true, err
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
	w.seq = follow.End
	if follow.HasTokens {
		w.figure.Tokens = follow.Tokens
	}
	return follow.Items, true, nil
}

// reset makes path the stream's transcript, and returns its newest items with the seq, origin and context figure
// the transcript has now.
func (w *conversationWatch) reset(path string) ([]conversation.Item, error) {
	conv, end, err := conversation.ReadEnd(path, w.limit)
	if err != nil {
		return nil, transcriptError(err)
	}
	w.path, w.seq, w.origin, w.figure = path, end, conv.Origin, conv.Context
	return conv.Items, nil
}

// state returns the state of the stream at path: its origin and context figure, the goal and waiting messages of the
// transcript, and pane. The waiting messages are idle when pane is done.
func (w *conversationWatch) state(pane *v1.Pane, path string) (*v1.ConversationState, error) {
	goal, err := w.panes.goals.Goal(path)
	if err != nil {
		return nil, transcriptError(err)
	}
	idle := pane.GetState() == string(protocol.StateDone)
	waiting, err := w.panes.queues.Waiting(path, idle)
	if err != nil {
		return nil, transcriptError(err)
	}
	return &v1.ConversationState{
		Origin:  w.origin,
		Goal:    goal,
		Waiting: waitingMessages(waiting),
		Context: &v1.ConversationContext{Tokens: w.figure.Tokens, Window: w.figure.Window},
		Pane:    pane,
	}, nil
}

// livePane returns the Pane message Panes.List gives for the pane with id, and the transcript path of its live
// session. It fails with the errors Conversation gives for the same pane.
func (s panesServer) livePane(ctx context.Context, id string) (*v1.Pane, string, error) {
	panes, err := listPanes(ctx, s.tmux, s.newest, s.reader)
	if err != nil {
		return nil, "", err
	}
	msgs, err := paneMessages(panes)
	if err != nil {
		return nil, "", err
	}
	var pane *v1.Pane
	for _, msg := range msgs {
		if msg.GetId() == id {
			pane = msg
			break
		}
	}
	if pane == nil {
		return nil, "", routeError{status: http.StatusNotFound, message: "pane not found"}
	}
	if pane.GetState() == "" {
		return nil, "", routeError{status: http.StatusNotFound, message: "no live session"}
	}
	path, ok, err := s.tmux.PaneOption(ctx, id, protocol.OptionTranscript)
	if err != nil {
		return nil, "", internalError()
	}
	if !ok || path == "" {
		return nil, "", routeError{status: http.StatusNotFound, message: "no transcript"}
	}
	return pane, path, nil
}

// transcriptError returns the routeError of a failed transcript read: 404 when the file is missing, and internal
// otherwise, as conversationAnswer maps them.
func transcriptError(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return routeError{status: http.StatusNotFound, message: "transcript not found"}
	}
	return internalError()
}
