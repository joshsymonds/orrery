package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"

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

// Watch sends every event after the request's cursor in seq order, then each event stored after that, polling the
// store every poll interval. Each page is read in its own store read, so no read is held across a send or a poll.
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
		select {
		case <-ctx.Done():
			return fmt.Errorf("watch events: %w", ctx.Err())
		case <-e.shutdown:
			return shutdownError()
		case <-ticker.C:
		}
	}
}

// sendPage sends each event of page after the cursor after in seq order, checking the shutdown signal before each
// send, and returns the seq of the last event sent, or after when page holds none.
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
		return nil, fmt.Errorf("event %d has unknown kind %q", event.Seq, event.Kind)
	}
	return msg, nil
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
