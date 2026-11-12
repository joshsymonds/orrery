package enginehost

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/pools"
)

// PoolCommands is the set of pool operations the pools service answers with. Broker satisfies it.
type PoolCommands interface {
	Acquire(ctx context.Context, req grants.Request, send func(grants.Event) error) error
	Status() grants.Status
	Hold(ctx context.Context, member string) error
	Free(ctx context.Context, member string) error
}

// maxLimitSeconds is the largest limit, in seconds, whose duration fits in a time.Duration.
const maxLimitSeconds = math.MaxInt64 / int64(time.Second)

// poolService implements the Pools RPCs over pools. A nil pools refuses every RPC as unavailable.
type poolService struct {
	pools PoolCommands
}

// Acquire sends the request's place in line, then its grant, and holds the grant until the client closes the stream.
// A hold on a granted member sends the grant's stop. A request with no valid client pid or limit is refused before it
// reaches the pools.
func (s *poolService) Acquire(
	ctx context.Context,
	req *connect.Request[v1.AcquireRequest],
	stream *connect.ServerStream[v1.AcquireResponse],
) error {
	if s.pools == nil {
		return poolsUnavailable()
	}
	if req.Msg.GetClientPid() < 1 {
		return errors.Join(connect.NewError(
			connect.CodeInvalidArgument,
			fmt.Errorf("client pid %d is below 1", req.Msg.GetClientPid()),
		))
	}
	grant, err := grantRequest(req.Msg)
	if err != nil {
		return errors.Join(connect.NewError(connect.CodeInvalidArgument, err))
	}
	err = s.pools.Acquire(ctx, grant, func(event grants.Event) error {
		resp, respErr := acquireResponse(event)
		if respErr != nil {
			return respErr
		}
		if sendErr := stream.Send(resp); sendErr != nil {
			return fmt.Errorf("send acquire event: %w", sendErr)
		}
		return nil
	})
	if err != nil {
		return poolError(err)
	}
	return nil
}

// Status answers with the pools in use and every owner hold.
func (s *poolService) Status(
	_ context.Context,
	_ *connect.Request[v1.PoolsStatusRequest],
) (*connect.Response[v1.PoolsStatusResponse], error) {
	if s.pools == nil {
		return nil, poolsUnavailable()
	}
	resp, err := statusResponse(s.pools.Status())
	if err != nil {
		return nil, errors.Join(connect.NewError(connect.CodeInternal, err))
	}
	return connect.NewResponse(resp), nil
}

// Hold holds the member the request names, so no grant takes it until it is freed.
func (s *poolService) Hold(
	ctx context.Context,
	req *connect.Request[v1.PoolsHoldRequest],
) (*connect.Response[v1.PoolsHoldResponse], error) {
	if s.pools == nil {
		return nil, poolsUnavailable()
	}
	if err := s.pools.Hold(ctx, req.Msg.GetMember()); err != nil {
		return nil, poolError(err)
	}
	return connect.NewResponse(&v1.PoolsHoldResponse{}), nil
}

// Free frees the member the request names, which must be held.
func (s *poolService) Free(
	ctx context.Context,
	req *connect.Request[v1.PoolsFreeRequest],
) (*connect.Response[v1.PoolsFreeResponse], error) {
	if s.pools == nil {
		return nil, poolsUnavailable()
	}
	if err := s.pools.Free(ctx, req.Msg.GetMember()); err != nil {
		return nil, poolError(err)
	}
	return connect.NewResponse(&v1.PoolsFreeResponse{}), nil
}

// poolsUnavailable returns the error every Pools RPC answers with when there is no pools to serve.
func poolsUnavailable() error {
	return errors.Join(connect.NewError(connect.CodeUnavailable, errors.New(unavailableMessage)))
}

// grantRequest returns the broker request that msg names.
func grantRequest(msg *v1.AcquireRequest) (grants.Request, error) {
	defs := make([]pools.Pool, 0, len(msg.GetPools()))
	for _, def := range msg.GetPools() {
		pool, err := poolOf(def)
		if err != nil {
			return grants.Request{}, err
		}
		defs = append(defs, pool)
	}
	needs := make([]grants.Need, 0, len(msg.GetNeeds()))
	for _, need := range msg.GetNeeds() {
		needs = append(needs, grants.Need{Pool: need.GetPool(), Count: int(need.GetCount())})
	}
	return grants.Request{
		Project:   msg.GetProject(),
		Pools:     defs,
		Needs:     needs,
		ClientPID: int(msg.GetClientPid()),
		GrantID:   msg.GetGrantId(),
		Command:   msg.GetCommand(),
	}, nil
}

// poolOf returns the pool that def defines, with each limit as a duration.
func poolOf(def *v1.PoolDefinition) (pools.Pool, error) {
	wakeLimit, err := limitOf("wake_limit_seconds", def.GetWakeLimitSeconds())
	if err != nil {
		return pools.Pool{}, err
	}
	statusLimit, err := limitOf("status_limit_seconds", def.GetStatusLimitSeconds())
	if err != nil {
		return pools.Pool{}, err
	}
	admitLimit, err := limitOf("admit_limit_seconds", def.GetAdmitLimitSeconds())
	if err != nil {
		return pools.Pool{}, err
	}
	return pools.Pool{
		Name:        def.GetName(),
		Host:        def.GetHost(),
		Cap:         int(def.GetCap()),
		Members:     def.GetMembers(),
		Wake:        def.GetWake(),
		WakeLimit:   wakeLimit,
		Status:      def.GetStatus(),
		StatusLimit: statusLimit,
		Admit:       def.GetAdmit(),
		AdmitLimit:  admitLimit,
	}, nil
}

// limitOf returns a limit in whole seconds as a duration. The limit must be from 1 to maxLimitSeconds, so the
// duration is positive and does not overflow.
func limitOf(field string, seconds int64) (time.Duration, error) {
	if seconds < 1 || seconds > maxLimitSeconds {
		return 0, fmt.Errorf("%s %d is outside 1 to %d", field, seconds, maxLimitSeconds)
	}
	return time.Duration(seconds) * time.Second, nil
}

// acquireResponse returns the response that carries event. A stop event names the held members of a grant the broker
// stops, and carries no grant and no position. A queued event is the request's place in line. A granted event carries
// the grant's id and its members, grouped by pool in the order the grant lists them.
func acquireResponse(event grants.Event) (*v1.AcquireResponse, error) {
	switch {
	case event.Stop != nil:
		return &v1.AcquireResponse{Event: &v1.AcquireResponse_Stop{
			Stop: &v1.AcquireStop{Members: memberNames(event.Stop)},
		}}, nil
	case event.Grant == nil:
		position, err := int32Of("queue position", event.Position)
		if err != nil {
			return nil, err
		}
		return &v1.AcquireResponse{Event: &v1.AcquireResponse_Queued{
			Queued: &v1.AcquireQueued{Position: position},
		}}, nil
	}
	granted := &v1.AcquireGranted{GrantId: event.Grant.ID}
	for _, member := range event.Grant.Members {
		i := slices.IndexFunc(granted.Pools, func(pm *v1.PoolMembers) bool { return pm.GetPool() == member.Pool })
		if i < 0 {
			granted.Pools = append(granted.Pools, &v1.PoolMembers{Pool: member.Pool})
			i = len(granted.Pools) - 1
		}
		granted.Pools[i].Members = append(granted.Pools[i].Members, member.Name)
	}
	return &v1.AcquireResponse{Event: &v1.AcquireResponse_Granted{Granted: granted}}, nil
}

// memberNames returns the names of members, in order.
func memberNames(members []grants.Member) []string {
	names := make([]string, 0, len(members))
	for _, member := range members {
		names = append(names, member.Name)
	}
	return names
}

// statusResponse returns the response that carries st. Each time is unix seconds, and a zero time is 0.
func statusResponse(st grants.Status) (*v1.PoolsStatusResponse, error) {
	resp := &v1.PoolsStatusResponse{}
	for _, pool := range st.Pools {
		ps, err := poolStatusOf(pool)
		if err != nil {
			return nil, err
		}
		resp.Pools = append(resp.Pools, ps)
	}
	for _, hold := range st.Holds {
		resp.Holds = append(resp.Holds, &v1.PoolHold{Member: hold.Member, Since: unixSeconds(hold.Since)})
	}
	return resp, nil
}

// poolStatusOf returns the status of a pool in use: its cap, its members in declared order, and its waiters.
func poolStatusOf(pool grants.PoolStatus) (*v1.PoolStatus, error) {
	capacity, err := int32Of("cap", pool.Cap)
	if err != nil {
		return nil, err
	}
	ps := &v1.PoolStatus{Name: pool.Name, Cap: capacity}
	for _, member := range pool.Members {
		ms, memberErr := memberStatusOf(member)
		if memberErr != nil {
			return nil, memberErr
		}
		ps.Members = append(ps.Members, ms)
	}
	for _, waiter := range pool.Waiters {
		w, waiterErr := waiterOf(waiter)
		if waiterErr != nil {
			return nil, waiterErr
		}
		ps.Waiters = append(ps.Waiters, w)
	}
	return ps, nil
}

// memberStatusOf returns a member of a pool in use, with its holder when a grant holds it.
func memberStatusOf(member grants.MemberStatus) (*v1.PoolMemberStatus, error) {
	ms := &v1.PoolMemberStatus{
		Name:      member.Name,
		State:     member.State.String(),
		HeldSince: unixSeconds(member.HeldSince),
	}
	if member.Holder == nil {
		return ms, nil
	}
	holder, err := holderOf(member.Holder)
	if err != nil {
		return nil, err
	}
	ms.Holder = holder
	return ms, nil
}

// holderOf returns the grant that holds a member.
func holderOf(holder *grants.Holder) (*v1.PoolHolder, error) {
	clientPID, err := int32Of("client pid", holder.ClientPID)
	if err != nil {
		return nil, err
	}
	return &v1.PoolHolder{
		GrantId:   holder.GrantID,
		Project:   holder.Project,
		ClientPid: clientPID,
		Command:   holder.Command,
		Since:     unixSeconds(holder.Since),
		Stopping:  holder.Stopping,
	}, nil
}

// waiterOf returns a request waiting for a pool.
func waiterOf(waiter grants.WaiterStatus) (*v1.PoolWaiter, error) {
	position, err := int32Of("queue position", waiter.Position)
	if err != nil {
		return nil, err
	}
	count, err := int32Of("count", waiter.Count)
	if err != nil {
		return nil, err
	}
	return &v1.PoolWaiter{
		Position: position,
		Project:  waiter.Project,
		Command:  waiter.Command,
		Count:    count,
		Since:    unixSeconds(waiter.Since),
	}, nil
}

// int32Of returns value as an int32. It fails with field named when value is outside the range of int32.
func int32Of(field string, value int) (int32, error) {
	if value < math.MinInt32 || value > math.MaxInt32 {
		return 0, fmt.Errorf("%s %d is outside the range of int32", field, value)
	}
	return int32(value), nil
}

// unixSeconds returns t as unix seconds, or 0 when t is the zero time.
func unixSeconds(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// poolError returns the Connect error that carries err, a broker error, to the client.
func poolError(err error) error {
	code := connect.CodeInternal
	switch {
	case errors.Is(err, grants.ErrRefused):
		code = connect.CodeFailedPrecondition
	case errors.Is(err, grants.ErrBadNeed), errors.Is(err, grants.ErrBadMember):
		code = connect.CodeInvalidArgument
	case errors.Is(err, grants.ErrNoGrant), errors.Is(err, grants.ErrNotHeld):
		code = connect.CodeNotFound
	case errors.Is(err, grants.ErrClosed):
		code = connect.CodeUnavailable
	}
	return errors.Join(connect.NewError(code, err))
}
