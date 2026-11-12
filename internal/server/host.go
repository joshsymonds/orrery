package server

import (
	"context"
	"net/http"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/hoststatus"
)

// hostServer answers orrery.v1.Host from the host status runner.
type hostServer struct {
	runner *hoststatus.Runner
}

// newHostHandler returns the path and handler that serve Host over runner.
func newHostHandler(runner *hoststatus.Runner) (string, http.Handler) {
	return orreryv1connect.NewHostHandler(hostServer{runner: runner})
}

// Status answers Host.Status with what hostAnswer returns. The error codes follow hostAnswer's routeError.
func (s hostServer) Status(
	ctx context.Context,
	_ *connect.Request[v1.HostStatusRequest],
) (*connect.Response[v1.HostStatusResponse], error) {
	answer, err := hostAnswer(ctx, s.runner)
	if err != nil {
		return nil, connectError(err)
	}
	stats := make([]*v1.HostStat, 0, len(answer.Stats))
	for _, stat := range answer.Stats {
		stats = append(stats, &v1.HostStat{
			Label:   stat.Label,
			Value:   stat.Value,
			Percent: stat.Percent,
			Level:   stat.Level,
		})
	}
	return connect.NewResponse(&v1.HostStatusResponse{Host: answer.Host, Stats: stats}), nil
}
