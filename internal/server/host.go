package server

import (
	"net/http"

	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/hoststatus"
)

// hostServer answers orrery.v1.Host from the host status runner the REST host route shares. The embedded
// Unimplemented handler answers every method until its method lands.
type hostServer struct {
	orreryv1connect.UnimplementedHostHandler
	runner *hoststatus.Runner
}

// newHostHandler returns the path and handler that serve Host over runner.
func newHostHandler(runner *hoststatus.Runner) (string, http.Handler) {
	return orreryv1connect.NewHostHandler(hostServer{runner: runner})
}
