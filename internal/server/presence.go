package server

import (
	"context"
	"net/http"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
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
func registerPresence(mux *http.ServeMux, report InputReporter, now func() time.Time) string {
	path, handler := orreryv1connect.NewPresenceHandler(presenceServer{report: report, now: now})
	mux.Handle(path, handler)
	return path
}

// Report answers Presence.Report. A negative age is refused with invalid_argument and reports nothing. Otherwise the
// input is dated at the clock's time less the age, in unix seconds, and reported, unless that time is not after the
// unix epoch. The subtraction runs in milliseconds and only when the age is below the clock's time, so no age
// overflows.
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
		s.report(time.UnixMilli(nowMs - age).Unix())
	}
	return connect.NewResponse(&v1.PresenceReportResponse{}), nil
}
