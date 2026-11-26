package enginehost

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// MoveEpic hands the epic to the session the request names, or to the calling conversation when it names none, and
// returns the stored epic. The target must be a conversation: a sessions row that is not a step, and a pane row only
// with a Claude in it. It may run in any directory, since a steerer answers the epic's questions and events and changes
// nothing in its repository, so a conversation in another repository can steer an epic it directs. Each refusal names
// why and writes nothing.
func (s *epicService) MoveEpic(
	ctx context.Context,
	req *connect.Request[v1.MoveEpicRequest],
) (*connect.Response[v1.MoveEpicResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	id := req.Msg.GetId()
	if id == "" {
		return nil, refuseEpic(connect.CodeInvalidArgument, "id: empty")
	}
	epic, err := s.epics.Epic(ctx, id)
	if err != nil {
		return nil, epicControlError(id, err)
	}
	maker, err := s.callerMaker(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	session := req.Msg.GetSession()
	if session == "" {
		if maker.By != store.MakerConversation {
			return nil, refuseEpic(
				connect.CodeFailedPrecondition,
				"session: empty, and no conversation called epic_steer",
			)
		}
		session = maker.Session
	}
	err = s.moveTo(ctx, epic, session, maker)
	if err != nil {
		return nil, err
	}
	message, err := s.storedEpic(ctx, id)
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.MoveEpicResponse{Epic: message}), nil
}

// moveTo hands epic to session as maker, once session passes the target checks: it is a conversation. Each refusal
// names why and writes nothing.
func (s *epicService) moveTo(ctx context.Context, epic store.EpicRow, session string, maker store.Maker) error {
	if err := s.checkMoveTarget(ctx, session); err != nil {
		return err
	}
	if _, err := s.epics.MoveEpic(ctx, epic.ID, session, maker, time.Now()); err != nil {
		return epicControlError(epic.ID, err)
	}
	return nil
}

// checkMoveTarget refuses session as the steerer a move makes unless it is a conversation: a headless session, or a pane
// with a Claude in it. A session that is no row, is a step, or is a pane with no Claude is refused by name.
func (s *epicService) checkMoveTarget(ctx context.Context, session string) error {
	target, ok, err := s.epics.SteerTarget(ctx, session)
	if err != nil {
		return epicStoreError(err)
	}
	switch {
	case !ok:
		return refuseEpic(connect.CodeFailedPrecondition, fmt.Sprintf("session %s is not a session", session))
	case target.Kind == store.KindStep:
		return refuseEpic(connect.CodeFailedPrecondition,
			fmt.Sprintf("session %s is a step, not a conversation", session))
	case target.Kind != store.KindEngine && target.State == "":
		return refuseEpic(connect.CodeFailedPrecondition,
			fmt.Sprintf("session %s is a pane with no Claude", session))
	}
	return nil
}

// ContinueEpic starts a steering session in the epic's repository and moves the epic to it as the owner, then answers
// the stored epic and the session's id. A start that fails, or whose session row cannot be read, refuses the continue and moves nothing. A move that fails after the start refuses the continue, and the started
// session is stopped, even when ctx has ended.
func (s *epicService) ContinueEpic(
	ctx context.Context,
	req *connect.Request[v1.ContinueEpicRequest],
) (*connect.Response[v1.ContinueEpicResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	id := req.Msg.GetId()
	if id == "" {
		return nil, refuseEpic(connect.CodeInvalidArgument, "id: empty")
	}
	epic, err := s.epics.Epic(ctx, id)
	if err != nil {
		return nil, epicControlError(id, err)
	}
	session, err := s.startSteering(ctx, epic)
	if err != nil {
		return nil, err
	}
	err = s.moveTo(ctx, epic, session, store.Maker{By: store.MakerOwner})
	if err != nil {
		return nil, s.stopStarted(ctx, session, err)
	}
	message, err := s.storedEpic(ctx, id)
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.ContinueEpicResponse{Epic: message, Session: session}), nil
}

// stopStarted stops the steering session started by a continue whose move failed, even when ctx has ended. It returns
// refusal, joined with the stop's error when the stop fails.
func (s *epicService) stopStarted(ctx context.Context, started string, refusal error) error {
	if stopErr := s.steering.Stop(context.WithoutCancel(ctx), started); stopErr != nil {
		return errors.Join(refusal, fmt.Errorf("stop steering session %s: %w", started, stopErr))
	}
	return refusal
}
