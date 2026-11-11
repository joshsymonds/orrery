package server

import (
	"context"
	"net/http"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/push"
)

// pushServer answers orrery.v1.Push from the subscription store the REST push routes share.
type pushServer struct {
	store *push.Store
}

// newPushHandler returns the path and handler that serve Push over store.
func newPushHandler(store *push.Store) (string, http.Handler) {
	return orreryv1connect.NewPushHandler(pushServer{store: store})
}

// Register answers Push.Register with what POST /v1/push/register answers. The error codes follow
// registerSubscription's routeError.
func (s pushServer) Register(
	_ context.Context,
	req *connect.Request[v1.RegisterRequest],
) (*connect.Response[v1.RegisterResponse], error) {
	sub := push.Subscription{
		Endpoint: req.Msg.GetEndpoint(),
		P256dh:   req.Msg.GetP256Dh(),
		Auth:     req.Msg.GetAuth(),
	}
	if err := registerSubscription(s.store, sub); err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&v1.RegisterResponse{}), nil
}

// Unregister answers Push.Unregister with what DELETE /v1/push/register answers. The error codes follow
// unregisterSubscription's routeError.
func (s pushServer) Unregister(
	_ context.Context,
	req *connect.Request[v1.UnregisterRequest],
) (*connect.Response[v1.UnregisterResponse], error) {
	if err := unregisterSubscription(s.store, req.Msg.GetEndpoint()); err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&v1.UnregisterResponse{}), nil
}
