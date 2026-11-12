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
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/pools"
)

// PoolCommands is the set of pool operations the pools service answers with. Broker satisfies it.
type PoolCommands interface {
	Acquire(ctx context.Context, req grants.Request, send func(grants.Event) error) error
}

// maxLimitSeconds is the largest limit, in seconds, whose duration fits in a time.Duration.
const maxLimitSeconds = math.MaxInt64 / int64(time.Second)

// poolService implements the Pools RPCs over pools. A nil pools refuses every Acquire as unavailable.
type poolService struct {
	enginev1connect.UnimplementedPoolsHandler

	pools PoolCommands
}

// Acquire sends the request's place in line, then its grant, and holds the grant until the client closes the stream.
// A request with no valid client pid or limit is refused before it reaches the pools.
func (s *poolService) Acquire(
	ctx context.Context,
	req *connect.Request[v1.AcquireRequest],
	stream *connect.ServerStream[v1.AcquireResponse],
) error {
	if s.pools == nil {
		return errors.Join(connect.NewError(connect.CodeUnavailable, errors.New(unavailableMessage)))
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
		return acquireError(err)
	}
	return nil
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

// acquireResponse returns the response that carries event. A queued event is the request's place in line, which must
// fit the response's int32. A granted event carries the grant's id and its members, grouped by pool in the order the
// grant lists them.
func acquireResponse(event grants.Event) (*v1.AcquireResponse, error) {
	if event.Grant == nil {
		position := event.Position
		if position < math.MinInt32 || position > math.MaxInt32 {
			return nil, fmt.Errorf("queue position %d is outside the range of int32", position)
		}
		return &v1.AcquireResponse{Event: &v1.AcquireResponse_Queued{
			Queued: &v1.AcquireQueued{Position: int32(position)},
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

// acquireError returns the Connect error that carries err, a broker error, to the client.
func acquireError(err error) error {
	code := connect.CodeInternal
	switch {
	case errors.Is(err, grants.ErrRefused):
		code = connect.CodeFailedPrecondition
	case errors.Is(err, grants.ErrBadNeed):
		code = connect.CodeInvalidArgument
	case errors.Is(err, grants.ErrNoGrant):
		code = connect.CodeNotFound
	case errors.Is(err, grants.ErrClosed):
		code = connect.CodeUnavailable
	}
	return errors.Join(connect.NewError(code, err))
}
