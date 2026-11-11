package server

import (
	"net/http"

	"github.com/joshsymonds/orrery/internal/conversation"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// panesServer answers orrery.v1.Panes with the instances the REST pane routes share. The embedded Unimplemented
// handler answers every method until its method lands.
type panesServer struct {
	orreryv1connect.UnimplementedPanesHandler
	tmux      *tmuxctl.Client
	newest    *conversation.Newest
	goals     *conversation.Goals
	queues    *conversation.Queues
	textLocks *paneLocks
	seenLocks *paneLocks
}

// newPanesHandler returns the path and handler that serve Panes over the instances New shares with the REST pane routes.
func newPanesHandler(
	tmux *tmuxctl.Client,
	newest *conversation.Newest,
	goals *conversation.Goals,
	queues *conversation.Queues,
	textLocks, seenLocks *paneLocks,
) (string, http.Handler) {
	return orreryv1connect.NewPanesHandler(panesServer{
		tmux:      tmux,
		newest:    newest,
		goals:     goals,
		queues:    queues,
		textLocks: textLocks,
		seenLocks: seenLocks,
	})
}
