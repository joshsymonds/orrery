package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// originRunes is the most runes a session's origin keeps.
const originRunes = 300

// WatchConversation answers Sessions.WatchConversation with the stream of one headless session. The limit is checked
// as Panes.WatchConversation checks it. The stream sends its first delta at once, then a delta for each poll that
// finds new main-thread items, a changed state or a changed unfinished set, and a keepalive once keepalive has passed
// without a delta. The stream ends with the error the session read gives, when serve shuts down, or when a send stays
// blocked past the send deadline. Its send and end-of-stream writes are bounded as Events.Watch's are (see endWithin).
func (s sessionsServer) WatchConversation(
	ctx context.Context,
	req *connect.Request[orreryv1.SessionsWatchConversationRequest],
	stream *connect.ServerStream[orreryv1.SessionsWatchConversationResponse],
) error {
	if req.Msg.GetLimit() > conversationLimitMax {
		return listError(connect.CodeInvalidArgument, badQueryMessage)
	}
	limit := int(req.Msg.GetLimit())
	if limit == 0 {
		limit = conversationLimitDefault
	}
	watch := &sessionWatch{
		reader: s.reader,
		id:     req.Msg.GetId(),
		limit:  limit,
		after:  req.Msg.GetAfter(),
	}
	return endWithin(ctx, s.runWatch(ctx, watch, stream), s.sendTimeout, s.shutdown)
}

// runWatch sends the deltas of watch on stream until the client leaves, the session's read fails, serve shuts down,
// or a send stays blocked past the send deadline. It steps watch every poll, and sends a keepalive once keepalive has
// passed since the last message.
func (s sessionsServer) runWatch(
	ctx context.Context,
	watch *sessionWatch,
	stream *connect.ServerStream[orreryv1.SessionsWatchConversationResponse],
) error {
	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()
	lastSent := time.Now()
	for {
		if stopping(s.shutdown) {
			return shutdownError()
		}
		msg, changed, err := watch.step(ctx)
		if err != nil {
			return err
		}
		if changed || time.Since(lastSent) >= s.keepalive {
			if sendErr := sendWithin(ctx, s.sendTimeout, s.shutdown, stream, msg); sendErr != nil {
				return fmt.Errorf("send session delta: %w", sendErr)
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

// sessionWatch is one WatchConversation call: the session it watches and the position its stream has reached.
type sessionWatch struct {
	reader *store.Reader
	id     string
	limit  int
	// after is the seq the client holds. The first read answers with the items above it, or with a reset when it is 0.
	after int64
	// seq is the seq of the newest session event the stream has read.
	seq int64
	// origin is the text of the session's first prompt, cut to originRunes, and haveOrigin reports whether the
	// session has had one.
	origin     string
	haveOrigin bool
	// request is the newest unrefused request the session has raised.
	request *orreryv1.OpenRequest
	// sent is the state the stream last sent, or nil before its first delta. sentUnfinished is the unfinished set it
	// last sent.
	sent           *orreryv1.ConversationState
	sentUnfinished []*orreryv1.EngineText
}

// step reads the session once. It returns the message for the read, and true when the message is a delta: the first
// message, a read with new items, a changed state or a changed unfinished set. Otherwise the message carries neither
// items nor state, and the caller sends it only as a keepalive.
func (w *sessionWatch) step(ctx context.Context) (*orreryv1.SessionsWatchConversationResponse, bool, error) {
	first := w.sent == nil
	items, history, err := w.read(ctx, first)
	if err != nil {
		return nil, false, err
	}
	state, err := w.state(history)
	if err != nil {
		return nil, false, err
	}
	unfinished, err := unfinishedTexts(history)
	if err != nil {
		return nil, false, err
	}
	stateChanged := first || !proto.Equal(state, w.sent)
	unfinishedChanged := !slices.EqualFunc(unfinished, w.sentUnfinished, func(a, b *orreryv1.EngineText) bool {
		return proto.Equal(a, b)
	})
	changed := stateChanged || unfinishedChanged || len(items) > 0
	msg := &orreryv1.SessionsWatchConversationResponse{
		Transcript: w.id,
		Seq:        w.seq,
		Reset_:     first && w.after == 0,
		Items:      items,
		Unfinished: unfinished,
	}
	if stateChanged {
		msg.State = state
	}
	if changed {
		w.sent = state
		w.sentUnfinished = unfinished
	}
	return msg, changed, nil
}

// read scans the session's events in pages of eventPageSize and returns the main-thread items the delta carries and
// the row and unfinished text of the last page. The first read scans from seq 0, so it sees the whole history: the
// origin and the newest request. It keeps the newest limit items when the client asked for a reset, and the items
// above its seq otherwise. Later reads scan from the seq the stream has reached and keep every item they read.
func (w *sessionWatch) read(
	ctx context.Context,
	first bool,
) ([]*orreryv1.ConversationItem, store.EngineHistory, error) {
	if w.reader == nil {
		return nil, store.EngineHistory{}, listError(connect.CodeUnavailable, "store unavailable")
	}
	reset := first && w.after == 0
	after := w.seq
	if first {
		after = 0
	}
	var items []*orreryv1.ConversationItem
	for {
		page, err := w.reader.EngineHistory(ctx, w.id, after, eventPageSize)
		if err != nil {
			return nil, store.EngineHistory{}, sessionReadError(err)
		}
		if page.Kind != store.KindEngine {
			return nil, store.EngineHistory{}, sessionNotFound()
		}
		for _, event := range page.Events {
			items, err = w.collect(items, event, first, reset)
			if err != nil {
				return nil, store.EngineHistory{}, err
			}
		}
		if n := len(page.Events); n > 0 {
			after = page.Events[n-1].Seq
			w.seq = after
		}
		if len(page.Events) < eventPageSize {
			return items, page, nil
		}
	}
}

// collect tracks one event and appends its main-thread item to items when the delta carries it. first and reset are
// read's flags: a first read skips the items at or below the seq the client holds, and a reset keeps the newest limit.
func (w *sessionWatch) collect(
	items []*orreryv1.ConversationItem,
	event store.EngineEvent,
	first, reset bool,
) ([]*orreryv1.ConversationItem, error) {
	item, ok, err := sessionItem(event)
	if err == nil {
		err = w.track(event, item, ok)
	}
	if err != nil {
		return nil, listError(connect.CodeInternal, "read sessions")
	}
	if !ok || (first && event.Seq <= w.after) {
		return items, nil
	}
	items = append(items, item)
	if reset && len(items) > w.limit {
		items = items[1:]
	}
	return items, nil
}

// track records what an event sets for the stream: the origin, from the session's first prompt, and the request, from
// each unrefused request. item and ok are sessionItem's result for event. It returns the decode error of a request.
func (w *sessionWatch) track(event store.EngineEvent, item *orreryv1.ConversationItem, ok bool) error {
	if event.Kind == store.EventEnginePrompt && ok && !w.haveOrigin {
		w.origin = cutRunes(item.GetText(), originRunes)
		w.haveOrigin = true
	}
	if event.Kind != store.EventEngineRequest {
		return nil
	}
	req, open, err := sessionRequest(event.Payload)
	if err != nil {
		return err
	}
	if open {
		w.request = req
	}
	return nil
}

// state returns the state of the session as history reads it: its origin, its headless session without the summary
// and newest reply, and the open request while the session waits on one.
func (w *sessionWatch) state(history store.EngineHistory) (*orreryv1.ConversationState, error) {
	changed, err := parseStamp(history.Changed)
	if err != nil {
		return nil, listError(connect.CodeInternal, "read sessions")
	}
	seen, err := parseStamp(history.Seen)
	if err != nil {
		return nil, listError(connect.CodeInternal, "read sessions")
	}
	state := &orreryv1.ConversationState{
		Origin: w.origin,
		Session: &orreryv1.HeadlessSession{
			Id:       w.id,
			Dir:      history.Dir,
			Label:    headlessLabel(w.id, history.Dir),
			State:    history.State,
			Changed:  changed,
			Seen:     seen,
			Question: history.Question,
		},
	}
	if history.State == string(protocol.StateWaiting) {
		state.Request = w.request
	}
	return state, nil
}

// unfinishedTexts returns the main-conversation unfinished set of history: the text so far of each unfinished block
// with no parent tool use.
func unfinishedTexts(history store.EngineHistory) ([]*orreryv1.EngineText, error) {
	texts := make([]*orreryv1.EngineText, 0, len(history.Unfinished))
	for _, block := range history.Unfinished {
		if block.ParentToolUseID != "" {
			continue
		}
		if block.BlockIndex < math.MinInt32 || block.BlockIndex > math.MaxInt32 {
			return nil, listError(connect.CodeInternal, "read sessions")
		}
		texts = append(texts, &orreryv1.EngineText{
			MessageId: block.MessageID,
			Index:     int32(block.BlockIndex),
			Kind:      block.Kind,
			Text:      block.Text,
		})
	}
	return texts, nil
}

// sessionReadError returns the Connect error of a failed session read. Store detail never reaches the client.
func sessionReadError(err error) error {
	switch {
	case errors.Is(err, store.ErrNoEngineSession):
		return sessionNotFound()
	case errors.Is(err, store.ErrUnavailable):
		return listError(connect.CodeUnavailable, "store unavailable")
	default:
		return listError(connect.CodeInternal, "read sessions")
	}
}

// sessionNotFound is the error for a session that is missing or is not a headless engine session.
func sessionNotFound() error {
	return listError(connect.CodeNotFound, "session not found")
}
