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

// eventPageSize is the most events one store read returns to Watch.
const eventPageSize = 100

// responseWriterKey is the context key of the ResponseWriter net/http hands the Events handler.
type responseWriterKey struct{}

// eventsServer answers orrery.v1.Events from the store reader. A nil reader answers unavailable.
type eventsServer struct {
	reader      *store.Reader
	poll        time.Duration
	sendTimeout time.Duration
}

// Watch sends every event after the request's cursor in seq order, then each event stored after that, polling the
// store every poll interval. Each page is read in its own store read, so no read is held across a send or a poll.
// Each send must complete within sendTimeout, set as a write deadline on the connection, so a send that stays
// blocked ends this stream and no other. Connect writes the end of the stream after Watch returns, so that write
// gets the same deadline, armed when Watch returns. Store detail never reaches the client: every error message is
// fixed.
func (e eventsServer) Watch(
	ctx context.Context,
	req *connect.Request[orreryv1.WatchRequest],
	stream *connect.ServerStream[orreryv1.WatchResponse],
) error {
	err := e.watch(ctx, req.Msg.GetAfter(), stream)
	return errors.Join(err, armWriteDeadline(ctx, e.sendTimeout))
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
		for _, event := range page.Events {
			msg, msgErr := watchMessage(event)
			if msgErr != nil {
				return listError(connect.CodeInternal, "read events")
			}
			if sendErr := sendWithin(ctx, e.sendTimeout, stream, msg); sendErr != nil {
				return fmt.Errorf("send event %d: %w", event.Seq, sendErr)
			}
			after = event.Seq
		}
		if len(page.Events) == eventPageSize {
			continue
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("watch events: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// errNoResponseWriter reports a context that carries no ResponseWriter, which withResponseWriter always adds.
var errNoResponseWriter = errors.New("no response writer on context")

// sendWithin sends msg on stream with a write deadline of timeout from now on the connection under the
// ResponseWriter on ctx, so a send the client does not read within timeout fails. A send that completes clears the
// deadline, so a stream that stays quiet for longer than timeout can still write its next message or its end.
func sendWithin(
	ctx context.Context,
	timeout time.Duration,
	stream *connect.ServerStream[orreryv1.WatchResponse],
	msg *orreryv1.WatchResponse,
) error {
	if err := armWriteDeadline(ctx, timeout); err != nil {
		return err
	}
	if err := stream.Send(msg); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	return setWriteDeadline(ctx, time.Time{})
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

// newEventsHandler returns the path and handler that serve Events.Watch over reader, polling every poll and
// bounding each send by sendTimeout.
func newEventsHandler(reader *store.Reader, poll, sendTimeout time.Duration) (string, http.Handler) {
	service := eventsServer{reader: reader, poll: poll, sendTimeout: sendTimeout}
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
