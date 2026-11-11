package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// defaultEventPoll is the Watch poll interval when Config.EventPoll is zero.
const defaultEventPoll = 250 * time.Millisecond

// defaultEventSendTimeout is the Watch send deadline when Config.EventSendTimeout is zero.
const defaultEventSendTimeout = 10 * time.Second

// shutdownGrace is how long a send or the end-of-stream write may stay blocked once the shutdown signal closes, so
// serve exits within its shutdown budget.
const shutdownGrace = time.Second

// eventPageSize is the most events one store read returns to Watch.
const eventPageSize = 100

// responseWriterKey is the context key of the ResponseWriter net/http hands the Events handler.
type responseWriterKey struct{}

// eventsServer answers orrery.v1.Events from the store reader. A nil reader answers unavailable. Closing shutdown
// ends every open stream with Unavailable, and a nil shutdown never closes.
type eventsServer struct {
	reader      *store.Reader
	poll        time.Duration
	sendTimeout time.Duration
	shutdown    <-chan struct{}
}

// Watch sends every event after the request's cursor in seq order as its typed message, then each event stored after
// that, polling the store every poll interval. After each read that is not a full page, it also sends the text so far
// of each unfinished engine block whose text this stream has not sent yet (see sendTexts). Each page is read in its
// own store read, so no read is held across a send or a poll.
// Each send must complete within sendTimeout, set as a write deadline on the connection, so a send that stays
// blocked ends this stream and no other. Connect writes the end of the stream after Watch returns, so that write
// gets the same deadline, armed when Watch returns. Once the shutdown signal has closed, a blocked send and that end
// write get shutdownGrace instead. Store detail never reaches the client: every error message is fixed.
func (e eventsServer) Watch(
	ctx context.Context,
	req *connect.Request[orreryv1.WatchRequest],
	stream *connect.ServerStream[orreryv1.WatchResponse],
) error {
	err := e.watch(ctx, req.Msg.GetAfter(), stream)
	deadline := e.sendTimeout
	if stopping(e.shutdown) {
		deadline = shutdownGrace
	}
	return errors.Join(err, armWriteDeadline(ctx, deadline))
}

// watch runs Watch's stream from the cursor after until the stream ends, and returns why it ended.
func (e eventsServer) watch(
	ctx context.Context,
	after int64,
	stream *connect.ServerStream[orreryv1.WatchResponse],
) error {
	if e.reader == nil {
		return listError(connect.CodeUnavailable, "store unavailable")
	}
	ticker := time.NewTicker(e.poll)
	defer ticker.Stop()
	sent := map[textKey]string{}
	for {
		page, err := e.reader.Events(ctx, after, eventPageSize)
		if errors.Is(err, store.ErrUnavailable) {
			return listError(connect.CodeUnavailable, "store unavailable")
		}
		if err != nil {
			return listError(connect.CodeInternal, "read events")
		}
		if cursorErr := checkCursor(after, page); cursorErr != nil {
			return cursorErr
		}
		after, err = e.sendPage(ctx, stream, after, page)
		if err != nil {
			return err
		}
		if len(page.Events) == eventPageSize {
			continue
		}
		sent, err = e.sendTexts(ctx, stream, sent, page)
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("watch events: %w", ctx.Err())
		case <-e.shutdown:
			return shutdownError()
		case <-ticker.C:
		}
	}
}

// sendPage sends each event of page after the cursor after in seq order as its typed message, checking the shutdown
// signal before each send. It returns the seq of the last event read, or after when page holds none.
func (e eventsServer) sendPage(
	ctx context.Context,
	stream *connect.ServerStream[orreryv1.WatchResponse],
	after int64,
	page store.EventPage,
) (int64, error) {
	for _, event := range page.Events {
		if stopping(e.shutdown) {
			return after, shutdownError()
		}
		msg, err := watchMessage(event)
		if err != nil {
			return after, listError(connect.CodeInternal, "read events")
		}
		if sendErr := sendWithin(ctx, e.sendTimeout, e.shutdown, stream, msg); sendErr != nil {
			return after, fmt.Errorf("send event %d: %w", event.Seq, sendErr)
		}
		after = event.Seq
	}
	return after, nil
}

// textKey identifies one unfinished block by the columns that key its engine_text row.
type textKey struct {
	sessionID       string
	messageID       string
	index           int64
	parentToolUseID string
}

// sendTexts sends an EngineText, with the page's newest seq and at 0, for each unfinished block of page whose text
// differs from the text this stream last sent for it in sent, checking the shutdown signal before each send. It
// returns the text each block of page now has been sent, so a block that left Unfinished is forgotten.
func (e eventsServer) sendTexts(
	ctx context.Context,
	stream *connect.ServerStream[orreryv1.WatchResponse],
	sent map[textKey]string,
	page store.EventPage,
) (map[textKey]string, error) {
	next := make(map[textKey]string, len(page.Unfinished))
	for _, block := range page.Unfinished {
		key := textKey{
			sessionID: block.SessionID, messageID: block.MessageID, index: block.BlockIndex,
			parentToolUseID: block.ParentToolUseID,
		}
		if text, ok := sent[key]; ok && text == block.Text {
			next[key] = text
			continue
		}
		if stopping(e.shutdown) {
			return sent, shutdownError()
		}
		if block.BlockIndex < math.MinInt32 || block.BlockIndex > math.MaxInt32 {
			return sent, listError(connect.CodeInternal, "read events")
		}
		msg := &orreryv1.WatchResponse{
			Seq:       page.Newest,
			SessionId: block.SessionID,
			Event: &orreryv1.WatchResponse_EngineText{
				EngineText: &orreryv1.EngineText{
					MessageId: block.MessageID, Index: int32(block.BlockIndex), ParentToolUseId: block.ParentToolUseID,
					Kind: block.Kind, Text: block.Text,
				},
			},
		}
		if err := sendWithin(ctx, e.sendTimeout, e.shutdown, stream, msg); err != nil {
			return sent, fmt.Errorf("send unfinished text of %s: %w", block.SessionID, err)
		}
		next[key] = block.Text
	}
	return next, nil
}

// stopping reports whether shutdown has closed. A nil shutdown never has.
func stopping(shutdown <-chan struct{}) bool {
	select {
	case <-shutdown:
		return true
	default:
		return false
	}
}

// shutdownError is the Unavailable error that ends a Watch stream when serve shuts down, so a client reconnects
// with its last seq.
func shutdownError() error {
	return listError(connect.CodeUnavailable, "serve is shutting down")
}

// errNoResponseWriter reports a context that carries no ResponseWriter, which withResponseWriter always adds.
var errNoResponseWriter = errors.New("no response writer on context")

// sendWithin sends msg on stream with a write deadline of timeout from now on the connection under the
// ResponseWriter on ctx, so a send the client does not read within timeout fails. If shutdown closes while the send
// is blocked, the deadline becomes shutdownGrace from then. A send that completes clears the deadline, so a stream
// that stays quiet for longer than timeout can still write its next message or its end.
func sendWithin(
	ctx context.Context,
	timeout time.Duration,
	shutdown <-chan struct{},
	stream *connect.ServerStream[orreryv1.WatchResponse],
	msg *orreryv1.WatchResponse,
) error {
	if err := armWriteDeadline(ctx, timeout); err != nil {
		return err
	}
	stopGrace := graceOnShutdown(ctx, shutdown)
	sendErr := stream.Send(msg)
	if graceErr := stopGrace(); graceErr != nil {
		return errors.Join(sendErr, graceErr)
	}
	if sendErr != nil {
		return fmt.Errorf("send: %w", sendErr)
	}
	return setWriteDeadline(ctx, time.Time{})
}

// graceOnShutdown starts a goroutine that moves the write deadline of the connection under the ResponseWriter on ctx
// to shutdownGrace from now when shutdown closes. The returned stop function ends the goroutine and waits for it to
// exit, so no deadline it sets outlives the send it guards, and returns the error setting the deadline failed with.
func graceOnShutdown(ctx context.Context, shutdown <-chan struct{}) (stop func() error) {
	done := make(chan struct{})
	exited := make(chan struct{})
	var graceErr error
	go func() {
		defer close(exited)
		select {
		case <-shutdown:
			graceErr = setWriteDeadline(ctx, time.Now().Add(shutdownGrace))
		case <-done:
		}
	}()
	return func() error {
		close(done)
		<-exited
		return graceErr
	}
}

// armWriteDeadline sets the write deadline of the connection under the ResponseWriter on ctx to timeout from now.
func armWriteDeadline(ctx context.Context, timeout time.Duration) error {
	return setWriteDeadline(ctx, time.Now().Add(timeout))
}

// setWriteDeadline sets the write deadline of the connection under the ResponseWriter on ctx. The zero time clears
// it.
func setWriteDeadline(ctx context.Context, deadline time.Time) error {
	writer, ok := ctx.Value(responseWriterKey{}).(http.ResponseWriter)
	if !ok {
		return errNoResponseWriter
	}
	if err := http.NewResponseController(writer).SetWriteDeadline(deadline); err != nil {
		return fmt.Errorf("write deadline: %w", err)
	}
	return nil
}

// checkCursor returns the out-of-range error for a cursor the page's log bounds cannot resume from: a cursor past
// the newest event, or, when the log holds events, one older than the event before the oldest.
func checkCursor(after int64, page store.EventPage) error {
	if after > page.Newest {
		return listError(connect.CodeOutOfRange, "cursor is past the end of the log")
	}
	if page.Newest > 0 && after < page.Earliest-1 {
		return listError(connect.CodeOutOfRange, "cursor is older than the log")
	}
	return nil
}

// watchMessage converts one store event to its proto message, with the typed payload for its kind.
func watchMessage(event store.Event) (*orreryv1.WatchResponse, error) {
	msg := &orreryv1.WatchResponse{Seq: event.Seq, SessionId: event.SessionID, At: event.At}
	switch event.Kind {
	case store.EventSessionAdded:
		msg.Event = &orreryv1.WatchResponse_SessionAdded{
			SessionAdded: &orreryv1.SessionAdded{Server: event.Server, Pane: event.Pane},
		}
	case store.EventSessionChanged:
		msg.Event = &orreryv1.WatchResponse_SessionChanged{SessionChanged: &orreryv1.SessionChanged{
			State:         event.Changes.State,
			Changed:       event.Changes.Changed,
			Seen:          event.Changes.Seen,
			Question:      event.Changes.Question,
			Summary:       event.Changes.Summary,
			Label:         event.Changes.Label,
			Context:       event.Changes.Context,
			Tool:          event.Changes.Tool,
			ClaudeSession: event.Changes.ClaudeSession,
			Transcript:    event.Changes.Transcript,
		}}
	case store.EventSessionRemoved:
		msg.Event = &orreryv1.WatchResponse_SessionRemoved{SessionRemoved: &orreryv1.SessionRemoved{}}
	default:
		if err := setEngineEvent(msg, event); err != nil {
			return nil, err
		}
	}
	return msg, nil
}

// setEngineEvent sets the typed payload of msg from the stored payload of an engine event. The payload must decode
// exactly into its message, so a key the message lacks is an error.
func setEngineEvent(msg *orreryv1.WatchResponse, event store.Event) error {
	switch event.Kind {
	case store.EventEngineAdded:
		body := &orreryv1.EngineAdded{}
		if err := protojson.Unmarshal([]byte(event.Payload), body); err != nil {
			return fmt.Errorf("decode %s event %d: %w", event.Kind, event.Seq, err)
		}
		msg.Event = &orreryv1.WatchResponse_EngineAdded{EngineAdded: body}
	case store.EventEngineChanged:
		body, err := engineChanged(event.Payload)
		if err != nil {
			return fmt.Errorf("decode %s event %d: %w", event.Kind, event.Seq, err)
		}
		msg.Event = &orreryv1.WatchResponse_EngineChanged{EngineChanged: body}
	case store.EventEnginePrompt:
		body := &orreryv1.EnginePrompt{}
		if err := protojson.Unmarshal([]byte(event.Payload), body); err != nil {
			return fmt.Errorf("decode %s event %d: %w", event.Kind, event.Seq, err)
		}
		msg.Event = &orreryv1.WatchResponse_EnginePrompt{EnginePrompt: body}
	case store.EventEngineBlock:
		body := &orreryv1.EngineBlock{}
		if err := protojson.Unmarshal([]byte(event.Payload), body); err != nil {
			return fmt.Errorf("decode %s event %d: %w", event.Kind, event.Seq, err)
		}
		msg.Event = &orreryv1.WatchResponse_EngineBlock{EngineBlock: body}
	case store.EventEngineRequest:
		body := &orreryv1.EngineRequest{}
		if err := protojson.Unmarshal([]byte(event.Payload), body); err != nil {
			return fmt.Errorf("decode %s event %d: %w", event.Kind, event.Seq, err)
		}
		msg.Event = &orreryv1.WatchResponse_EngineRequest{EngineRequest: body}
	case store.EventEngineRequestCancelled:
		body := &orreryv1.EngineRequestCanceled{}
		if err := protojson.Unmarshal([]byte(event.Payload), body); err != nil {
			return fmt.Errorf("decode %s event %d: %w", event.Kind, event.Seq, err)
		}
		msg.Event = &orreryv1.WatchResponse_EngineRequestCanceled{EngineRequestCanceled: body}
	case store.EventEngineTurnEnded:
		body := &orreryv1.EngineTurnEnded{}
		if err := protojson.Unmarshal([]byte(event.Payload), body); err != nil {
			return fmt.Errorf("decode %s event %d: %w", event.Kind, event.Seq, err)
		}
		msg.Event = &orreryv1.WatchResponse_EngineTurnEnded{EngineTurnEnded: body}
	default:
		return fmt.Errorf("event %d has unknown kind %q", event.Seq, event.Kind)
	}
	return nil
}

// engineChanged decodes an engine_changed payload, whose only key is fields, into its EngineChanged message.
func engineChanged(payload string) (*orreryv1.EngineChanged, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	fields, ok := envelope["fields"]
	if !ok || len(envelope) != 1 {
		return nil, errors.New("payload is not a fields object")
	}
	body := &orreryv1.EngineChanged{}
	if err := protojson.Unmarshal(fields, body); err != nil {
		return nil, fmt.Errorf("decode fields: %w", err)
	}
	return body, nil
}

// newEventsHandler returns the path and handler that serve Events.Watch over reader, polling every poll, bounding
// each send by sendTimeout, and ending every stream when shutdown closes.
func newEventsHandler(
	reader *store.Reader,
	poll, sendTimeout time.Duration,
	shutdown <-chan struct{},
) (string, http.Handler) {
	service := eventsServer{reader: reader, poll: poll, sendTimeout: sendTimeout, shutdown: shutdown}
	path, handler := orreryv1connect.NewEventsHandler(service)
	return path, withResponseWriter(handler)
}

// withResponseWriter returns a handler that runs next with the ResponseWriter net/http hands in on the request
// context, so Watch can reach the connection under Connect's wrapper.
func withResponseWriter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), responseWriterKey{}, w)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
