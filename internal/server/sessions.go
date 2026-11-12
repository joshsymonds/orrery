package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// sessionsServer answers orrery.v1.Sessions from the store reader. A nil reader answers unavailable. poll paces the
// polls of WatchConversation, keepalive is the time without a delta after which it sends a keepalive, sendTimeout
// bounds each of its sends, and closing shutdown ends its streams. A nil shutdown never closes.
type sessionsServer struct {
	reader      *store.Reader
	poll        time.Duration
	keepalive   time.Duration
	sendTimeout time.Duration
	shutdown    <-chan struct{}
}

// List answers every session row and the event seq they are current to, both from one store snapshot. Store
// detail never reaches the client: the messages below are fixed.
func (s sessionsServer) List(
	ctx context.Context,
	_ *connect.Request[orreryv1.ListRequest],
) (*connect.Response[orreryv1.ListResponse], error) {
	if s.reader == nil {
		return nil, listError(connect.CodeUnavailable, "store unavailable")
	}
	snap, err := s.reader.Snapshot(ctx)
	if errors.Is(err, store.ErrUnavailable) {
		return nil, listError(connect.CodeUnavailable, "store unavailable")
	}
	if err != nil {
		return nil, listError(connect.CodeInternal, "read sessions")
	}
	sessions := make([]*orreryv1.Session, 0, len(snap.Sessions))
	for _, row := range snap.Sessions {
		sessions = append(sessions, sessionMessage(row))
	}
	return connect.NewResponse(&orreryv1.ListResponse{Sessions: sessions, Seq: snap.Seq}), nil
}

// listError returns a Connect error with a fixed message. The store's own error never reaches the client.
func listError(code connect.Code, message string) error {
	return errors.Join(connect.NewError(code, errors.New(message)))
}

// sessionMessage converts one sessions row to its proto message.
func sessionMessage(row store.Session) *orreryv1.Session {
	return &orreryv1.Session{
		Id:            row.ID,
		Server:        row.Server,
		Pane:          row.Pane,
		State:         row.Fields.State,
		Changed:       row.Fields.Changed,
		Seen:          row.Fields.Seen,
		Question:      row.Fields.Question,
		Summary:       row.Fields.Summary,
		Label:         row.Fields.Label,
		Context:       row.Fields.Context,
		Tool:          row.Fields.Tool,
		ClaudeSession: row.Fields.ClaudeSession,
		Transcript:    row.Fields.Transcript,
	}
}

// newSessionsHandler returns the path and handler that serve Sessions over reader, with the stream timings and
// shutdown signal of sessionsServer. The handler runs under withResponseWriter, so WatchConversation can reach its
// connection.
func newSessionsHandler(
	reader *store.Reader,
	poll, keepalive, sendTimeout time.Duration,
	shutdown <-chan struct{},
) (string, http.Handler) {
	service := sessionsServer{
		reader: reader, poll: poll, keepalive: keepalive, sendTimeout: sendTimeout, shutdown: shutdown,
	}
	path, handler := orreryv1connect.NewSessionsHandler(service)
	return path, withResponseWriter(handler)
}
