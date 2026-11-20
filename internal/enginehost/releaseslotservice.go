package enginehost

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// ReleaseSlotCommands is the set of release slot operations the ReleaseSlots service answers with. *ReleaseSlots
// satisfies it.
type ReleaseSlotCommands interface {
	Hold(
		ctx context.Context, repository, label string, limit time.Duration,
	) (store.ReleaseSlotHolder, bool, error)
	HoldWait(
		ctx context.Context, repository, label string, limit time.Duration,
		waiting func(store.ReleaseSlotHolder) error,
	) (store.ReleaseSlotHolder, error)
	Give(ctx context.Context, repository, label string) (bool, error)
	Show(ctx context.Context, repository string) (store.ReleaseSlotHolder, bool, error)
}

// releaseSlotService implements the ReleaseSlots RPCs over slots. A nil slots refuses every RPC as unavailable.
type releaseSlotService struct {
	slots ReleaseSlotCommands
}

// Hold holds the repository's release slot for the holder the request names, and answers whether it holds it and the
// holder that keeps the slot.
func (s releaseSlotService) Hold(
	ctx context.Context,
	req *connect.Request[v1.ReleaseSlotsHoldRequest],
) (*connect.Response[v1.ReleaseSlotsHoldResponse], error) {
	if s.slots == nil {
		return nil, releaseSlotsUnavailable()
	}
	limit, err := checkReleaseSlotHold(req.Msg.GetRepository(), req.Msg.GetHolder(), req.Msg.GetLimitSeconds())
	if err != nil {
		return nil, err
	}
	holder, held, err := s.slots.Hold(ctx, req.Msg.GetRepository(), req.Msg.GetHolder(), limit)
	if err != nil {
		return nil, releaseSlotError(err)
	}
	return connect.NewResponse(&v1.ReleaseSlotsHoldResponse{Held: held, Holder: releaseSlotHolderOf(holder)}), nil
}

// HoldWait streams the wait for the repository's release slot. Each holder that keeps the slot is sent as a waiting
// event, and the holder that takes it is sent as the one held event before the stream closes.
func (s releaseSlotService) HoldWait(
	ctx context.Context,
	req *connect.Request[v1.ReleaseSlotsHoldWaitRequest],
	stream *connect.ServerStream[v1.ReleaseSlotsHoldWaitResponse],
) error {
	if s.slots == nil {
		return releaseSlotsUnavailable()
	}
	limit, err := checkReleaseSlotHold(req.Msg.GetRepository(), req.Msg.GetHolder(), req.Msg.GetLimitSeconds())
	if err != nil {
		return err
	}
	holder, err := s.slots.HoldWait(
		ctx, req.Msg.GetRepository(), req.Msg.GetHolder(), limit,
		func(waiting store.ReleaseSlotHolder) error {
			return stream.Send(&v1.ReleaseSlotsHoldWaitResponse{
				Event: &v1.ReleaseSlotsHoldWaitResponse_Waiting{Waiting: releaseSlotHolderOf(waiting)},
			})
		},
	)
	if err != nil {
		return releaseSlotError(err)
	}
	err = stream.Send(&v1.ReleaseSlotsHoldWaitResponse{
		Event: &v1.ReleaseSlotsHoldWaitResponse_Held{Held: releaseSlotHolderOf(holder)},
	})
	if err != nil {
		return releaseSlotError(err)
	}
	return nil
}

// Give gives back the outside hold the request's holder has on the repository's release slot, and answers whether that
// freed the slot.
func (s releaseSlotService) Give(
	ctx context.Context,
	req *connect.Request[v1.ReleaseSlotsGiveRequest],
) (*connect.Response[v1.ReleaseSlotsGiveResponse], error) {
	if s.slots == nil {
		return nil, releaseSlotsUnavailable()
	}
	if err := checkReleaseSlotRepository(req.Msg.GetRepository()); err != nil {
		return nil, err
	}
	if err := checkReleaseSlotHolder(req.Msg.GetHolder()); err != nil {
		return nil, err
	}
	freed, err := s.slots.Give(ctx, req.Msg.GetRepository(), req.Msg.GetHolder())
	if err != nil {
		return nil, releaseSlotError(err)
	}
	return connect.NewResponse(&v1.ReleaseSlotsGiveResponse{Freed: freed}), nil
}

// Show answers the holder of the repository's release slot. A free slot answers held false with no holder.
func (s releaseSlotService) Show(
	ctx context.Context,
	req *connect.Request[v1.ReleaseSlotsShowRequest],
) (*connect.Response[v1.ReleaseSlotsShowResponse], error) {
	if s.slots == nil {
		return nil, releaseSlotsUnavailable()
	}
	if err := checkReleaseSlotRepository(req.Msg.GetRepository()); err != nil {
		return nil, err
	}
	holder, held, err := s.slots.Show(ctx, req.Msg.GetRepository())
	if err != nil {
		return nil, releaseSlotError(err)
	}
	resp := &v1.ReleaseSlotsShowResponse{Held: held}
	if held {
		resp.Holder = releaseSlotHolderOf(holder)
	}
	return connect.NewResponse(resp), nil
}

// checkReleaseSlotHold checks the repository, the holder and the limit seconds of a Hold or HoldWait, and returns the
// limit they ask for. The seconds are checked before they become a duration, so no value overflows.
func checkReleaseSlotHold(repository, holder string, seconds int64) (time.Duration, error) {
	if err := checkReleaseSlotRepository(repository); err != nil {
		return 0, err
	}
	if err := checkReleaseSlotHolder(holder); err != nil {
		return 0, err
	}
	maxSeconds := int64(MaxReleaseSlotLimit / time.Second)
	if seconds < 1 || seconds > maxSeconds {
		return 0, errors.Join(connect.NewError(
			connect.CodeInvalidArgument,
			fmt.Errorf("%w: %d seconds, want at least 1 and at most %d", ErrReleaseSlotLimit, seconds, maxSeconds),
		))
	}
	return time.Duration(seconds) * time.Second, nil
}

// checkReleaseSlotRepository returns the refusal for a repository that is not an absolute path.
func checkReleaseSlotRepository(repository string) error {
	if !filepath.IsAbs(repository) {
		return errors.Join(connect.NewError(
			connect.CodeInvalidArgument,
			fmt.Errorf("repository %q is not an absolute path", repository),
		))
	}
	return nil
}

// checkReleaseSlotHolder returns the refusal for an empty holder.
func checkReleaseSlotHolder(holder string) error {
	if holder == "" {
		return errors.Join(connect.NewError(connect.CodeInvalidArgument, errors.New("the holder is empty")))
	}
	return nil
}

// releaseSlotError maps an error from the release slot commands to its Connect code: a limit the commands refuse is
// invalid_argument, and any other error is internal. The error's text is the message.
func releaseSlotError(err error) error {
	code := connect.CodeInternal
	if errors.Is(err, ErrReleaseSlotLimit) {
		code = connect.CodeInvalidArgument
	}
	return errors.Join(connect.NewError(code, err))
}

// releaseSlotHolderOf returns the wire holder of h. An outside holder carries its deadline in unix seconds, and an epic
// holder carries 0.
func releaseSlotHolderOf(h store.ReleaseSlotHolder) *v1.ReleaseSlotHolder {
	holder := &v1.ReleaseSlotHolder{Kind: h.Kind, Epic: h.Epic, Label: h.Label}
	if h.Kind == store.ReleaseSlotOutside {
		holder.Deadline = h.Deadline.Unix()
	}
	return holder
}

// releaseSlotsUnavailable returns the refusal every ReleaseSlots RPC gets when there are no release slots to serve.
func releaseSlotsUnavailable() error {
	return errors.Join(connect.NewError(connect.CodeUnavailable, errors.New(unavailableMessage)))
}
