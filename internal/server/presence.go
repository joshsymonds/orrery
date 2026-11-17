package server

import (
	"context"
	"net/http"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// presenceNegativeAge is the message Presence.Report answers an age below zero with.
const presenceNegativeAge = "input age is negative"

// presenceServer answers orrery.v1.Presence by dating each input at the clock's time less its age.
type presenceServer struct {
	report InputReporter
	now    func() time.Time
}

// registerPresence routes orrery.v1.Presence on mux, reporting each input to report and dating it by now. It returns
// the path it routes.
func registerPresence(
	mux *http.ServeMux,
	report InputReporter,
	now func() time.Time,
	opts ...connect.HandlerOption,
) string {
	path, handler := orreryv1connect.NewPresenceHandler(presenceServer{report: report, now: now}, opts...)
	mux.Handle(path, handler)
	return path
}

// Report answers Presence.Report. A negative age is refused with invalid_argument and reports nothing. Otherwise the
// input is dated at the clock's time less the age, in unix seconds, and reported with the viewing key the request
// names, unless that time is not after the unix epoch. The subtraction runs in milliseconds and only when the age is
// below the clock's time, so no age overflows.
func (s presenceServer) Report(
	_ context.Context,
	req *connect.Request[v1.PresenceReportRequest],
) (*connect.Response[v1.PresenceReportResponse], error) {
	age := req.Msg.GetInputAgeMs()
	if age < 0 {
		return nil, listError(connect.CodeInvalidArgument, presenceNegativeAge)
	}
	nowMs := s.now().UnixMilli()
	if s.report != nil && age < nowMs {
		s.report(time.UnixMilli(nowMs-age).Unix(), viewOf(req.Msg))
	}
	return connect.NewResponse(&v1.PresenceReportResponse{}), nil
}

// viewOf returns the viewing key msg names: the pane, session or epic its viewing oneof sets. A message that sets no
// viewing key returns the zero protocol.ViewKey.
func viewOf(msg *v1.PresenceReportRequest) protocol.ViewKey {
	switch viewing := msg.GetViewing().(type) {
	case *v1.PresenceReportRequest_ViewingPane:
		return protocol.ViewKey{Pane: viewing.ViewingPane}
	case *v1.PresenceReportRequest_ViewingSession:
		return protocol.ViewKey{Session: viewing.ViewingSession}
	case *v1.PresenceReportRequest_ViewingEpic:
		return protocol.ViewKey{Epic: viewing.ViewingEpic}
	default:
		return protocol.ViewKey{}
	}
}
