package enginehost

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// MoveEpic hands the epic to the session the request names, or to the calling conversation when it names none, and
// returns the stored epic. The target must be a conversation in the epic's repository: a sessions row that is not a
// step, a pane row only with a Claude in it, and a directory that shares the repository's git common directory. Each
// refusal names why and writes nothing.
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
	dir, err := s.moveTargetDir(ctx, session)
	if err != nil {
		return nil, err
	}
	err = checkInRepository(ctx, epic, session, dir)
	if err != nil {
		return nil, err
	}
	if _, err = s.epics.MoveEpic(ctx, id, session, maker, time.Now()); err != nil {
		return nil, epicControlError(id, err)
	}
	message, err := s.storedEpic(ctx, id)
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.MoveEpicResponse{Epic: message}), nil
}

// moveTargetDir returns the directory of session, a session a move makes the steerer: a headless session's directory, or
// the current path of a pane with a Claude in it. A session that is no row, is a step, or is a pane with no Claude is
// refused by name.
func (s *epicService) moveTargetDir(ctx context.Context, session string) (string, error) {
	target, ok, err := s.epics.SteerTarget(ctx, session)
	if err != nil {
		return "", epicStoreError(err)
	}
	if !ok {
		return "", refuseEpic(connect.CodeFailedPrecondition, fmt.Sprintf("session %s is not a session", session))
	}
	switch target.Kind {
	case store.KindStep:
		return "", refuseEpic(connect.CodeFailedPrecondition,
			fmt.Sprintf("session %s is a step, not a conversation", session))
	case store.KindEngine:
		return target.Dir, nil
	}
	if target.State == "" {
		return "", refuseEpic(connect.CodeFailedPrecondition,
			fmt.Sprintf("session %s is a pane with no Claude", session))
	}
	if s.steering == nil {
		return "", refuseEpic(connect.CodeUnavailable, steeringUnavailableMessage)
	}
	dir, err := s.steering.PaneDir(ctx, target.Server, target.Pane)
	if err != nil {
		return "", refuseEpic(connect.CodeFailedPrecondition,
			fmt.Sprintf("session %s: its pane's directory cannot be read: %v", session, err))
	}
	return dir, nil
}

// checkInRepository refuses a move of epic to session whose directory dir is not in the epic's repository. The two must
// share a git common directory, so a subdirectory of the repository and a linked worktree of it both pass. An empty dir
// is refused, since git would read the server's own directory for it.
func checkInRepository(ctx context.Context, epic store.EpicRow, session, dir string) error {
	common, err := stepGit(ctx, epic.Repository, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return epicStoreError(fmt.Errorf("resolve repository of epic %s: %w", epic.ID, err))
	}
	if dir != "" {
		target, dirErr := stepGit(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if dirErr == nil && target == common {
			return nil
		}
	}
	return refuseEpic(connect.CodeFailedPrecondition,
		fmt.Sprintf("session %s runs in %s, outside the repository of epic %s", session, dir, epic.ID))
}

// ContinueEpic is not served yet: the steer continue answers unimplemented until its handler lands.
func (s *epicService) ContinueEpic(
	_ context.Context,
	_ *connect.Request[v1.ContinueEpicRequest],
) (*connect.Response[v1.ContinueEpicResponse], error) {
	return nil, refuseEpic(connect.CodeUnimplemented, "ContinueEpic is not implemented")
}
