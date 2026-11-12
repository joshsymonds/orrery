package server

import (
	"context"
	"net/http"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

// poolsServer answers orrery.v1.Pools by forwarding each call to engine. A nil engine answers unavailable.
type poolsServer struct {
	engine  enginev1connect.PoolsClient
	timeout time.Duration
}

// newPoolsHandler returns the path and handler that serve Pools over engine. A zero timeout is defaultControlTimeout.
func newPoolsHandler(engine enginev1connect.PoolsClient, timeout time.Duration) (string, http.Handler) {
	if timeout == 0 {
		timeout = defaultControlTimeout
	}
	return orreryv1connect.NewPoolsHandler(poolsServer{engine: engine, timeout: timeout})
}

// Status forwards a status request to the engine and answers with its pools and holds, as poolsStatusMessage maps them.
func (p poolsServer) Status(
	ctx context.Context,
	_ *connect.Request[orreryv1.PoolsStatusRequest],
) (*connect.Response[orreryv1.PoolsStatusResponse], error) {
	var status *enginev1.PoolsStatusResponse
	err := p.forward(ctx, func(ctx context.Context, engine enginev1connect.PoolsClient) error {
		resp, err := engine.Status(ctx, connect.NewRequest(&enginev1.PoolsStatusRequest{}))
		if err != nil {
			return wrapEngine("pools status", err)
		}
		status = resp.Msg
		return nil
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(poolsStatusMessage(status)), nil
}

// Hold forwards a hold of the member to the engine.
func (p poolsServer) Hold(
	ctx context.Context,
	req *connect.Request[orreryv1.PoolsHoldRequest],
) (*connect.Response[orreryv1.PoolsHoldResponse], error) {
	err := p.forward(ctx, func(ctx context.Context, engine enginev1connect.PoolsClient) error {
		_, err := engine.Hold(ctx, connect.NewRequest(&enginev1.PoolsHoldRequest{Member: req.Msg.GetMember()}))
		return wrapEngine("pools hold", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.PoolsHoldResponse{}), nil
}

// Free forwards a free of the member to the engine.
func (p poolsServer) Free(
	ctx context.Context,
	req *connect.Request[orreryv1.PoolsFreeRequest],
) (*connect.Response[orreryv1.PoolsFreeResponse], error) {
	err := p.forward(ctx, func(ctx context.Context, engine enginev1connect.PoolsClient) error {
		_, err := engine.Free(ctx, connect.NewRequest(&enginev1.PoolsFreeRequest{Member: req.Msg.GetMember()}))
		return wrapEngine("pools free", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.PoolsFreeResponse{}), nil
}

// forward runs call against the engine under the control timeout. A nil engine answers unavailable, as Control does.
// Any engine error answers as translateError does.
func (p poolsServer) forward(
	ctx context.Context,
	call func(context.Context, enginev1connect.PoolsClient) error,
) error {
	if p.engine == nil {
		return listError(connect.CodeUnavailable, controlUnavailable)
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	if err := call(ctx, p.engine); err != nil {
		return translateError(err)
	}
	return nil
}

// poolsStatusMessage returns the phone answer for status, each pool, member, waiter and hold carried field for field.
// A holder carries no grant_id or client_pid, which the phone does not see.
func poolsStatusMessage(status *enginev1.PoolsStatusResponse) *orreryv1.PoolsStatusResponse {
	pools := make([]*orreryv1.PoolStatus, 0, len(status.GetPools()))
	for _, pool := range status.GetPools() {
		pools = append(pools, poolStatusMessage(pool))
	}
	holds := make([]*orreryv1.PoolHold, 0, len(status.GetHolds()))
	for _, hold := range status.GetHolds() {
		holds = append(holds, &orreryv1.PoolHold{Member: hold.GetMember(), Since: hold.GetSince()})
	}
	return &orreryv1.PoolsStatusResponse{Pools: pools, Holds: holds}
}

// poolStatusMessage returns the phone answer for one pool in use, with its members and waiters.
func poolStatusMessage(pool *enginev1.PoolStatus) *orreryv1.PoolStatus {
	members := make([]*orreryv1.PoolMemberStatus, 0, len(pool.GetMembers()))
	for _, member := range pool.GetMembers() {
		members = append(members, poolMemberMessage(member))
	}
	waiters := make([]*orreryv1.PoolWaiter, 0, len(pool.GetWaiters()))
	for _, waiter := range pool.GetWaiters() {
		waiters = append(waiters, &orreryv1.PoolWaiter{
			Position: waiter.GetPosition(),
			Project:  waiter.GetProject(),
			Command:  waiter.GetCommand(),
			Count:    waiter.GetCount(),
			Since:    waiter.GetSince(),
		})
	}
	return &orreryv1.PoolStatus{Name: pool.GetName(), Cap: pool.GetCap(), Members: members, Waiters: waiters}
}

// poolMemberMessage returns the phone answer for one member, its holder left unset when no grant holds it.
func poolMemberMessage(member *enginev1.PoolMemberStatus) *orreryv1.PoolMemberStatus {
	msg := &orreryv1.PoolMemberStatus{
		Name:      member.GetName(),
		State:     member.GetState(),
		HeldSince: member.GetHeldSince(),
	}
	if holder := member.GetHolder(); holder != nil {
		msg.Holder = &orreryv1.PoolHolder{
			Project:  holder.GetProject(),
			Command:  holder.GetCommand(),
			Since:    holder.GetSince(),
			Stopping: holder.GetStopping(),
		}
	}
	return msg
}
