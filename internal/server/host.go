package server

import (
	"context"
	"io/fs"
	"net/http"
	"strings"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/hoststatus"
)

// pageBuildFile is the file of the built page that names its build, which scripts/build-web.sh writes beside the bundle.
const pageBuildFile = "build.txt"

// hostServer answers orrery.v1.Host from the host status runner, with pageBuild, the build of the page serve holds.
type hostServer struct {
	runner    *hoststatus.Runner
	pageBuild string
}

// newHostHandler returns the path and handler that serve Host over runner, naming web's page build.
func newHostHandler(runner *hoststatus.Runner, web fs.FS, opts ...connect.HandlerOption) (string, http.Handler) {
	return orreryv1connect.NewHostHandler(hostServer{runner: runner, pageBuild: pageBuildOf(web)}, opts...)
}

// pageBuildOf returns the build web's build.txt names, trimmed, or "" when web is nil or holds no readable build.txt,
// as a page built without one, or the stub page of a source checkout, does.
func pageBuildOf(web fs.FS) string {
	if web == nil {
		return ""
	}
	build, err := fs.ReadFile(web, pageBuildFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(build))
}

// Status answers Host.Status with what hostAnswer returns and the page build. The error codes follow hostAnswer's
// routeError.
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
	return connect.NewResponse(&v1.HostStatusResponse{Host: answer.Host, Stats: stats, PageBuild: s.pageBuild}), nil
}
