package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/node"
)

// panesRouter answers orrery.v1.Panes by the host that each call names. A call that names the controller's own host
// is served by local. A call that names a hosts.toml host is forwarded to that host's agent, and the agent's answer is
// returned as it came. A call that names no host is served by local while no other host is configured, and is
// invalid_argument once one is. A call that names any other host is not_found. List names no host and is served by
// local.
type panesRouter struct {
	local    panesServer
	hostName string
	// agents holds the agent of each hosts.toml host, by the host's name.
	agents map[string]*node.Client
}

// newPanesRouter returns the path and handler that serve orrery.v1.Panes by host, as panesRouter describes, with local
// serving the controller's own host and each of hosts serving its own. The handler runs under withResponseWriter, so a
// stream reaches its connection.
func newPanesRouter(
	local panesServer, hostName string, hosts []*node.Client, opts ...connect.HandlerOption,
) (string, http.Handler) {
	agents := make(map[string]*node.Client, len(hosts))
	for _, agent := range hosts {
		agents[agent.Name()] = agent
	}
	router := panesRouter{local: local, hostName: hostName, agents: agents}
	path, handler := orreryv1connect.NewPanesHandler(router, opts...)
	return path, withResponseWriter(handler)
}

// route returns the agent that serves a call naming host, and true when the call is forwarded to it. It returns false
// when local serves the call. It fails with invalid_argument for a call that names no host while other hosts are
// configured, and with not_found for a host that has no agent.
func (r panesRouter) route(host string) (*node.Client, bool, error) {
	switch {
	case host == r.hostName, host == "" && len(r.agents) == 0:
		return nil, false, nil
	case host == "":
		return nil, false, connectError(routeError{status: http.StatusBadRequest, message: "pane call names no host"})
	}
	agent, ok := r.agents[host]
	if !ok {
		return nil, false, connectError(routeError{status: http.StatusNotFound, message: "no agent for host " + host})
	}
	return agent, true, nil
}

// forwardUnary answers the unary call req, which names host. The call is answered by local when the router serves host,
// and by the Panes client of host's agent otherwise, with the request's message sent again.
func forwardUnary[Req, Resp any](
	ctx context.Context,
	router panesRouter,
	host string,
	req *connect.Request[Req],
	local func(context.Context, *connect.Request[Req]) (*connect.Response[Resp], error),
	remote func(orreryv1connect.PanesClient, context.Context, *connect.Request[Req]) (*connect.Response[Resp], error),
) (*connect.Response[Resp], error) {
	agent, forwarded, err := router.route(host)
	if err != nil {
		return nil, err
	}
	if !forwarded {
		return local(ctx, req)
	}
	resp, err := remote(agent.Panes(), ctx, connect.NewRequest(req.Msg))
	return resp, forwardError(err)
}

// forwardError returns err, which the Panes client of an agent answered with, unchanged. An error that matches
// holderhost.ErrUnreachable becomes unavailable instead, since the agent did not answer within its bound.
func forwardError(err error) error {
	if errors.Is(err, holderhost.ErrUnreachable) && connect.CodeOf(err) != connect.CodeUnavailable {
		return connectError(routeError{status: http.StatusBadGateway, message: err.Error()})
	}
	return err
}

// List answers Panes.List with the local panes, which name the controller's host.
func (r panesRouter) List(
	ctx context.Context,
	req *connect.Request[v1.PanesListRequest],
) (*connect.Response[v1.PanesListResponse], error) {
	return r.local.List(ctx, req)
}

// Screen answers Panes.Screen, as the router serves the host the request names.
func (r panesRouter) Screen(
	ctx context.Context,
	req *connect.Request[v1.ScreenRequest],
) (*connect.Response[v1.ScreenResponse], error) {
	return forwardUnary(ctx, r, req.Msg.GetHost(), req, r.local.Screen, orreryv1connect.PanesClient.Screen)
}

// Conversation answers Panes.Conversation, as the router serves the host the request names.
func (r panesRouter) Conversation(
	ctx context.Context,
	req *connect.Request[v1.ConversationRequest],
) (*connect.Response[v1.ConversationResponse], error) {
	return forwardUnary(ctx, r, req.Msg.GetHost(), req, r.local.Conversation, orreryv1connect.PanesClient.Conversation)
}

// SendText answers Panes.SendText, as the router serves the host the request names. A call forwarded to an agent
// records no send on this controller.
func (r panesRouter) SendText(
	ctx context.Context,
	req *connect.Request[v1.SendTextRequest],
) (*connect.Response[v1.SendTextResponse], error) {
	return forwardUnary(ctx, r, req.Msg.GetHost(), req, r.local.SendText, orreryv1connect.PanesClient.SendText)
}

// SendKeys answers Panes.SendKeys, as the router serves the host the request names.
func (r panesRouter) SendKeys(
	ctx context.Context,
	req *connect.Request[v1.SendKeysRequest],
) (*connect.Response[v1.SendKeysResponse], error) {
	return forwardUnary(ctx, r, req.Msg.GetHost(), req, r.local.SendKeys, orreryv1connect.PanesClient.SendKeys)
}

// MarkSeen answers Panes.MarkSeen, as the router serves the host the request names. A call forwarded to an agent marks
// no seen time on this controller.
func (r panesRouter) MarkSeen(
	ctx context.Context,
	req *connect.Request[v1.MarkSeenRequest],
) (*connect.Response[v1.MarkSeenResponse], error) {
	return forwardUnary(ctx, r, req.Msg.GetHost(), req, r.local.MarkSeen, orreryv1connect.PanesClient.MarkSeen)
}

// PullQueued answers Panes.PullQueued, as the router serves the host the request names.
func (r panesRouter) PullQueued(
	ctx context.Context,
	req *connect.Request[v1.PullQueuedRequest],
) (*connect.Response[v1.PullQueuedResponse], error) {
	return forwardUnary(ctx, r, req.Msg.GetHost(), req, r.local.PullQueued, orreryv1connect.PanesClient.PullQueued)
}

// Older answers Panes.Older, as the router serves the host the request names. A forwarded call goes to the agent's
// Older procedure over the agent's own HTTP client, the same connection and path that the agent's Panes client uses.
func (r panesRouter) Older(
	ctx context.Context,
	req *connect.Request[v1.OlderRequest],
) (*connect.Response[v1.OlderResponse], error) {
	agent, forwarded, err := r.route(req.Msg.GetHost())
	if err != nil {
		return nil, err
	}
	if !forwarded {
		return r.local.older(ctx, req)
	}
	older := connect.NewClient[v1.OlderRequest, v1.OlderResponse](
		agent.HTTPClient(), "http://"+agent.Name()+orreryv1connect.PanesOlderProcedure,
	)
	resp, err := older.CallUnary(ctx, connect.NewRequest(req.Msg))
	return resp, forwardError(err)
}

// WatchConversation answers Panes.WatchConversation, as the router serves the host the request names. A stream of an
// agent is relayed to the client message by message, and the relay ends with the agent's stream, with a send that
// fails, or with serve's shutdown, which ends the agent's stream as well.
func (r panesRouter) WatchConversation(
	ctx context.Context,
	req *connect.Request[v1.WatchConversationRequest],
	stream *connect.ServerStream[v1.WatchConversationResponse],
) error {
	agent, forwarded, err := r.route(req.Msg.GetHost())
	if err != nil {
		return err
	}
	if !forwarded {
		return r.local.WatchConversation(ctx, req, stream)
	}
	upstreamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	upstream, err := agent.Panes().WatchConversation(upstreamCtx, connect.NewRequest(req.Msg))
	if err != nil {
		return forwardError(err)
	}
	go func() {
		select {
		case <-r.local.shutdown:
			cancel()
		case <-upstreamCtx.Done():
		}
	}()
	relayed := r.relay(ctx, upstream, stream)
	if stopping(r.local.shutdown) {
		relayed = shutdownError()
	}
	return endWithin(ctx, errors.Join(relayed, upstream.Close()), r.local.sendTimeout, r.local.shutdown)
}

// relay sends each message of upstream on stream, bounded as the local stream's sends are. It returns the error that
// ended the send or the upstream, and nil when upstream ended without one.
func (r panesRouter) relay(
	ctx context.Context,
	upstream *connect.ServerStreamForClient[v1.WatchConversationResponse],
	stream *connect.ServerStream[v1.WatchConversationResponse],
) error {
	for upstream.Receive() {
		if err := sendWithin(ctx, r.local.sendTimeout, r.local.shutdown, stream, upstream.Msg()); err != nil {
			return fmt.Errorf("relay conversation delta: %w", err)
		}
	}
	return forwardError(upstream.Err())
}
