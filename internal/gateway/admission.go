package gateway

import (
	"errors"
	"net/http"
)

// requestAdmissionCapacity is how many requests the gateway serves at once: the two completion workers and the 32-slot
// completion queue patchbay sizes its pipeline with, 34 in all. A request past that bound is refused before it is
// routed.
const requestAdmissionCapacity = 34

// errOverloaded is what a request refused at the admission bound is answered with.
var errOverloaded = errors.New("gateway: request capacity exhausted")

// tryAdmission takes a permit for one request, and reports false when every permit is taken.
func (g *Gateway) tryAdmission() bool {
	select {
	case g.admission <- struct{}{}:
		return true
	default:
		return false
	}
}

// releaseAdmission returns the permit a request took when it was admitted.
func (g *Gateway) releaseAdmission() {
	<-g.admission
}

// refuseOverload answers a request refused at the admission bound. It routes nothing and contacts no upstream.
func (g *Gateway) refuseOverload(w http.ResponseWriter, r *http.Request) {
	g.abort(r.Context(), w, http.StatusServiceUnavailable, errorTypeAPI, errOverloaded)
}
