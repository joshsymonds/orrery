package server

import (
	"context"
	"net/http"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/llmroutes"
	"github.com/joshsymonds/orrery/internal/rpcretry"
	"github.com/joshsymonds/orrery/internal/store"
)

// routesReadMessage is the internal message of a route registry read the store fails to answer.
const routesReadMessage = "read routes"

// routesServer answers orrery.v1.Routes. Get reads the store reader, and a nil reader answers it unavailable. Replace
// and SetContextAccount forward to commands with no caller headers, so orreryd records the owner as the one who made
// each change. A nil commands answers them unavailable. Each forwarded call runs under timeout and is retried only
// while rpcretry.NeverConnected accepts its error, as the contract commands are.
type routesServer struct {
	reader   *store.Reader
	commands enginev1connect.AccountsClient
	timeout  time.Duration
	clock    rpcretry.Clock
}

// Get answers the route registry as the store holds it.
func (r routesServer) Get(
	ctx context.Context,
	_ *connect.Request[orreryv1.RoutesGetRequest],
) (*connect.Response[orreryv1.RoutesGetResponse], error) {
	if r.reader == nil {
		return nil, listError(connect.CodeUnavailable, accountStoreUnavailable)
	}
	reg, err := r.reader.Routes(ctx)
	if err != nil {
		return nil, accountReadError(err, routesReadMessage)
	}
	return connect.NewResponse(&orreryv1.RoutesGetResponse{Registry: llmroutes.ToProto(reg)}), nil
}

// Replace forwards the route registry to orreryd, and answers the registry as orreryd stored it.
func (r routesServer) Replace(
	ctx context.Context,
	req *connect.Request[orreryv1.RoutesReplaceRequest],
) (*connect.Response[orreryv1.RoutesReplaceResponse], error) {
	reply, err := forwardAccount(
		ctx,
		r.commands,
		r.timeout,
		r.clock,
		"replace routes",
		func(ctx context.Context, c enginev1connect.AccountsClient) (*connect.Response[enginev1.ReplaceRoutesResponse], error) {
			return c.ReplaceRoutes(ctx, connect.NewRequest(&enginev1.ReplaceRoutesRequest{
				Registry: req.Msg.GetRegistry(),
			}))
		},
	)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.RoutesReplaceResponse{Registry: reply.Msg.GetRegistry()}), nil
}

// SetContextAccount forwards the account of the named context to orreryd, and answers the registry as it stands after.
func (r routesServer) SetContextAccount(
	ctx context.Context,
	req *connect.Request[orreryv1.RoutesSetContextAccountRequest],
) (*connect.Response[orreryv1.RoutesSetContextAccountResponse], error) {
	reply, err := forwardAccount(
		ctx,
		r.commands,
		r.timeout,
		r.clock,
		"set context account",
		func(ctx context.Context, c enginev1connect.AccountsClient) (*connect.Response[enginev1.SetContextAccountResponse], error) {
			return c.SetContextAccount(ctx, connect.NewRequest(&enginev1.SetContextAccountRequest{
				Context: req.Msg.GetContext(), Account: req.Msg.GetAccount(),
			}))
		},
	)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.RoutesSetContextAccountResponse{
		Registry: reply.Msg.GetRegistry(),
	}), nil
}

// newRoutesHandler returns the path and handler of the Routes service, reading the registry from reader and forwarding
// its commands to commands, as routesServer does. A zero timeout is defaultControlTimeout. Each request reads at most
// maxControlBody bytes.
func newRoutesHandler(
	reader *store.Reader,
	commands enginev1connect.AccountsClient,
	timeout time.Duration,
	clock rpcretry.Clock,
	opts ...connect.HandlerOption,
) (string, http.Handler) {
	if timeout == 0 {
		timeout = defaultControlTimeout
	}
	options := append([]connect.HandlerOption{connect.WithReadMaxBytes(maxControlBody)}, opts...)
	return orreryv1connect.NewRoutesHandler(
		routesServer{reader: reader, commands: commands, timeout: timeout, clock: clock},
		options...,
	)
}
