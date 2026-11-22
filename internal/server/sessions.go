package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// sessionsServer answers orrery.v1.Sessions from the store reader. A nil reader answers unavailable. home is the home
// directory whose Claude transcripts WatchConversation reads a session's context figure from, and an empty home reads
// none. steps is the client WatchConversation asks about a step session, and a nil steps answers not_found for one.
// changes is the change hub WatchConversation streams wake from, and newTimer arms their keepalive and background
// timers. keepalive is the time without a message after which WatchConversation sends a keepalive, sendTimeout bounds
// each of its sends, and closing shutdown ends its streams. A nil shutdown never closes. now is the clock ListHeadless
// dates its retention window and its aging by, the clock Sessions.List ages its panes by, and the clock
// WatchConversation reads a background window against.
// stepped, when set, is called with the session id of each WatchConversation step before the step reads the session. A
// test sets it to count the reads of its streams. It is nil in production.
type sessionsServer struct {
	reader      *store.Reader
	home        string
	now         func() time.Time
	steps       StepClient
	changes     *changeHub
	newTimer    func(time.Duration) (<-chan time.Time, func() bool)
	keepalive   time.Duration
	sendTimeout time.Duration
	shutdown    <-chan struct{}
	stepped     func(id string)
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
	marks, err := readArchiveMarks(ctx, s.reader)
	if err != nil {
		return nil, err
	}
	verdicts, err := readClosureVerdicts(ctx, s.reader)
	if err != nil {
		return nil, err
	}
	sessions := make([]*orreryv1.Session, 0, len(snap.Sessions))
	for _, row := range snap.Sessions {
		session := sessionMessage(row)
		mark := marks[archiveKey{kind: archiveKindSession, id: row.ID}]
		changed := sessionChanged(row.Fields.Changed)
		// A pane row ages by the seven-day floor in any state, once it is done by the done rule, and by a closed verdict
		// for its current change time, all at s.now(). A row with no change time, changed 0, never ages. A server with
		// no clock ages none.
		verdict, ok := verdicts[row.ID]
		session.Ended, session.EndedAt = endedRow(false, verdict, ok, changed)
		aged := false
		if changed != 0 && s.now != nil {
			now := s.now()
			seen := sessionSeen(row.Fields.Seen)
			aged = floorAged(changed, now) ||
				(row.Fields.State == string(protocol.StateDone) && doneAged(changed, seen, now)) ||
				closureAged(verdict, ok, changed, seen, now)
		}
		session.Archived = archivedRow(mark, changed, aged)
		sessions = append(sessions, session)
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

// newSessionsHandler returns the path and handler that serve Sessions over reader and steps, with home, the clock now,
// the change hub changes, and the keepalive and send timings and shutdown signal of sessionsServer. WatchConversation
// steps when changes wakes its stream, and its keepalive and background timers are armed through newTimer, or through
// time.NewTimer when newTimer is nil. The handler runs under withResponseWriter, so WatchConversation can reach its
// connection.
func newSessionsHandler(
	reader *store.Reader,
	home string,
	now func() time.Time,
	steps StepClient,
	changes *changeHub,
	newTimer func(time.Duration) (<-chan time.Time, func() bool),
	keepalive, sendTimeout time.Duration,
	shutdown <-chan struct{},
	opts ...connect.HandlerOption,
) (string, http.Handler) {
	if newTimer == nil {
		newTimer = stdTimer
	}
	service := sessionsServer{
		reader: reader, home: home, now: now, steps: steps, changes: changes, newTimer: newTimer,
		keepalive: keepalive, sendTimeout: sendTimeout, shutdown: shutdown,
	}
	path, handler := orreryv1connect.NewSessionsHandler(service, opts...)
	return path, withResponseWriter(handler)
}
