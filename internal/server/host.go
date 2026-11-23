package server

import (
	"context"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/hoststatus"
	"github.com/joshsymonds/orrery/internal/node"
)

// pageBuildFile is the file of the built page that names its build, which scripts/build-web.sh writes beside the bundle.
const pageBuildFile = "build.txt"

// hostServer answers orrery.v1.Host from the host status runner, with pageBuild, the build of the page serve holds, and
// with the status of each host: the controller, named name and read through localLoad, then each of hosts in order.
type hostServer struct {
	runner    *hoststatus.Runner
	pageBuild string
	name      string
	localLoad func() (node.Load, error)
	hosts     []*node.Client
}

// newHostHandler returns the path and handler that serve Host over runner, naming web's page build, and listing the
// controller as name, with its load from localLoad, and then each of hosts.
func newHostHandler(
	runner *hoststatus.Runner,
	web fs.FS,
	name string,
	localLoad func() (node.Load, error),
	hosts []*node.Client,
	opts ...connect.HandlerOption,
) (string, http.Handler) {
	answers := hostServer{runner: runner, pageBuild: pageBuildOf(web), name: name, localLoad: localLoad, hosts: hosts}
	return orreryv1connect.NewHostHandler(answers, opts...)
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

// Status answers Host.Status with what hostAnswer returns, the page build, and the status of each host. The error codes
// follow hostAnswer's routeError.
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
	return connect.NewResponse(&v1.HostStatusResponse{
		Host:      answer.Host,
		Stats:     stats,
		PageBuild: s.pageBuild,
		Hosts:     s.hostEntries(ctx),
	}), nil
}

// hostEntries returns the status of each host: the controller's first, then each of s.hosts in order. The remote hosts
// are asked at once, so the answer waits for the slowest of them, never for each in turn.
func (s hostServer) hostEntries(ctx context.Context) []*v1.HostEntry {
	entries := make([]*v1.HostEntry, len(s.hosts)+1)
	entries[0] = s.controllerEntry()
	var wg sync.WaitGroup
	for index, client := range s.hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			entries[index+1] = remoteEntry(ctx, client)
		}()
	}
	wg.Wait()
	return entries
}

// controllerEntry returns the controller's entry, which is reachable since this controller answers. A localLoad that
// fails leaves both percents 0, and the answer still carries the entry.
func (s hostServer) controllerEntry() *v1.HostEntry {
	entry := &v1.HostEntry{Name: s.name, Reachable: true}
	load, err := s.localLoad()
	if err != nil {
		return entry
	}
	entry.LoadPercent, entry.MemoryPercent = load.LoadPercent, load.MemoryPercent
	return entry
}

// remoteEntry returns the entry of client's host, from the Load its agent answers. A Load that fails, or passes the
// client's bound, leaves the host unreachable with both percents 0.
func remoteEntry(ctx context.Context, client *node.Client) *v1.HostEntry {
	entry := &v1.HostEntry{Name: client.Name()}
	load, err := client.Load(ctx)
	if err != nil {
		return entry
	}
	entry.LoadPercent, entry.MemoryPercent, entry.Reachable = load.LoadPercent, load.MemoryPercent, true
	return entry
}
