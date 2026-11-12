package server

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/conversation"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// PullQueued answers Panes.PullQueued. The pane is checked first, and its live session and transcript come from
// livePane, as Conversation's do. The pull itself runs by pullQueued under the pane's text lock.
func (s panesServer) PullQueued(
	ctx context.Context,
	req *connect.Request[v1.PullQueuedRequest],
) (*connect.Response[v1.PullQueuedResponse], error) {
	if _, err := checkPane(ctx, s.tmux, req.Msg.GetPane()); err != nil {
		return nil, connectError(paneRouteError(err))
	}
	pane, path, err := s.livePane(ctx, req.Msg.GetPane())
	if err != nil {
		return nil, connectError(err)
	}
	text, err := s.pullQueued(ctx, pane, path)
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&v1.PullQueuedResponse{Text: text}), nil
}

// pullQueued takes back the messages Claude Code's queue holds for pane, as cli.PullQueued does, and returns their
// text. It holds the pane's text lock from the queue read to the end of the pull, so no text is sent to the pane in
// between. It fails with a routeError: 410 when the queue holds no uncanceled message, and 409 when the input box is
// not empty. Both are checked before a key is sent. When cli.PullQueued fails after taking text, the box still holds
// that text, so the failure is the same 409 as a box that holds typed text.
func (s panesServer) pullQueued(ctx context.Context, pane *v1.Pane, path string) (string, error) {
	id := pane.GetId()
	mu := s.textLocks.lock(id)
	mu.Lock()
	defer mu.Unlock()
	waiting, err := s.queues.Waiting(path, pane.GetState() == string(protocol.StateDone))
	if err != nil {
		return "", transcriptError(err)
	}
	if !uncanceledWaiting(waiting) {
		return "", routeError{status: http.StatusGone, message: "nothing queued"}
	}
	text, err := cli.PullQueued(ctx, s.tmux, id, path)
	switch {
	case errors.Is(err, cli.ErrInputBusy):
		return "", routeError{status: http.StatusConflict, message: "input not empty"}
	case errors.Is(err, cli.ErrNothingQueued):
		return "", routeError{status: http.StatusGone, message: "nothing queued"}
	case err != nil && text != "":
		return "", routeError{status: http.StatusConflict, message: "input not empty"}
	case err != nil:
		return "", internalError()
	}
	return text, nil
}

// uncanceledWaiting reports whether waiting holds a message that the person did not cancel.
func uncanceledWaiting(waiting []conversation.Waiting) bool {
	for _, msg := range waiting {
		if !msg.Canceled {
			return true
		}
	}
	return false
}
