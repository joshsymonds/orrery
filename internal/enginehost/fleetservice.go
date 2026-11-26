package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/deskstream"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// Fleet is what the Fleet service serves on a host. Router is the notice router that ReportSeen records each seen pane
// on, and Desk is the desk hub that WatchDesk streams the desk's lines from. A nil Router refuses ReportSeen as
// unavailable, and a nil Desk refuses WatchDesk as unavailable.
type Fleet struct {
	Router Router
	Desk   *deskstream.Hub
}

// routerUnavailableMessage is the message ReportSeen is refused with when there is no notice router.
const routerUnavailableMessage = "notice router unavailable"

// deskUnavailableMessage is the message WatchDesk is refused with when there is no desk hub.
const deskUnavailableMessage = "desk unavailable"

// fleetService implements the Fleet RPCs over fleet. A nil fleet refuses every RPC as unavailable.
type fleetService struct {
	fleet *Fleet
}

// ReportSeen records that the caller's host saw the pane at the request's seen time. The caller's host is the one in
// the Orrery-Host header: the relay sets it to the host it admitted, and the local socket sets it to the controller's
// name.
func (s *fleetService) ReportSeen(
	ctx context.Context,
	req *connect.Request[v1.FleetReportSeenRequest],
) (*connect.Response[v1.FleetReportSeenResponse], error) {
	if s.fleet == nil || s.fleet.Router == nil {
		return nil, refuseFleet(connect.CodeUnavailable, routerUnavailableMessage)
	}
	host := req.Header().Get(protocol.HeaderHost)
	if host == "" {
		return nil, refuseFleet(connect.CodeInvalidArgument, "the "+protocol.HeaderHost+" header names no host")
	}
	s.fleet.Router.Seen(ctx, notice.Key{Pane: req.Msg.GetPane(), Host: host}, req.Msg.GetSeen())
	return connect.NewResponse(&v1.FleetReportSeenResponse{}), nil
}

// WatchDesk streams the desk's lines to the caller: the hello line, then each notice posted while the stream is open.
// The stream ends when the caller goes, when the desk hub closes, or when the stream falls behind.
func (s *fleetService) WatchDesk(
	ctx context.Context,
	_ *connect.Request[v1.FleetWatchDeskRequest],
	stream *connect.ServerStream[v1.FleetWatchDeskResponse],
) error {
	if s.fleet == nil || s.fleet.Desk == nil {
		return refuseFleet(connect.CodeUnavailable, deskUnavailableMessage)
	}
	sub := s.fleet.Desk.Subscribe()
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return nil
		case b, ok := <-sub.Lines():
			if !ok {
				return nil
			}
			var line deskstream.Line
			if err := json.Unmarshal(b, &line); err != nil {
				return errors.Join(connect.NewError(connect.CodeInternal, fmt.Errorf("decode desk line: %w", err)))
			}
			if err := stream.Send(&v1.FleetWatchDeskResponse{
				Kind:  line.Kind,
				Host:  line.Host,
				State: line.State,
				Title: line.Title,
				Body:  line.Body,
				Quiet: line.Quiet,
			}); err != nil {
				return fmt.Errorf("send desk line: %w", err)
			}
		}
	}
}

// refuseFleet returns the Connect error with code and message that a Fleet RPC is refused with.
func refuseFleet(code connect.Code, message string) error {
	return errors.Join(connect.NewError(code, errors.New(message)))
}
