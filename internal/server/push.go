package server

import (
	"net/http"

	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/push"
)

// pushServer answers orrery.v1.Push from the subscription store the REST push routes share. The embedded
// Unimplemented handler answers every method until its method lands.
type pushServer struct {
	orreryv1connect.UnimplementedPushHandler
	store *push.Store
}

// newPushHandler returns the path and handler that serve Push over store.
func newPushHandler(store *push.Store) (string, http.Handler) {
	return orreryv1connect.NewPushHandler(pushServer{store: store})
}
