package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// paneQuestions is the enginehost.PaneQuestions of orreryd. It reads a pane's question from its tmux server, and it ends
// the pane's pending notice through the notice router. newPaneQuestions builds it.
type paneQuestions struct {
	server tmuxctl.Server
	router enginehost.Router
}

// questionServer returns the tmux server the pane questions read: the server opts names, or the default socket the
// daemon watches when it names none.
func questionServer(opts options) tmuxctl.Server {
	if opts.server == (tmuxctl.Server{}) {
		return opts.defaultServer
	}
	return opts.server
}

// newPaneQuestions returns the PaneQuestions that reads panes on server and reports them seen to router. A nil router
// makes PaneSeen do nothing.
func newPaneQuestions(server tmuxctl.Server, router enginehost.Router) *paneQuestions {
	return &paneQuestions{server: server, router: router}
}

// PaneQuestion reads pane's waiting state, the hash of its question and its changed time from its tmux server. It
// returns an error wrapping enginehost.ErrNoPane when the server lists no pane with that id, or when the server is not
// running, since it lists none. Any other read failure is returned wrapped with the pane's id.
func (p *paneQuestions) PaneQuestion(ctx context.Context, pane string) (enginehost.PaneQuestion, error) {
	client, err := tmuxClient(p.server)
	if err != nil {
		return enginehost.PaneQuestion{}, fmt.Errorf("pane %s: %w", pane, err)
	}
	panes, err := client.ListPanes(ctx)
	if err != nil {
		if paneServerNotRunning(err) {
			return enginehost.PaneQuestion{}, fmt.Errorf("pane %s: server not running: %w", pane, enginehost.ErrNoPane)
		}
		return enginehost.PaneQuestion{}, fmt.Errorf("pane %s: list panes: %w", pane, err)
	}
	i := slices.IndexFunc(panes, func(listed protocol.Pane) bool { return listed.ID == pane })
	if i < 0 {
		return enginehost.PaneQuestion{}, fmt.Errorf("no pane %s: %w", pane, enginehost.ErrNoPane)
	}
	found := panes[i]
	return enginehost.PaneQuestion{
		Waiting: found.State == protocol.StateWaiting,
		Hash:    protocol.QuestionHash(found.Question),
		Changed: found.Changed,
	}, nil
}

// DeclinePane sends pane its guarded Escape for the question questionHash names, through cli.Decline on the pane's tmux
// server. A refusal because the pane is in a tmux mode wraps enginehost.ErrPaneInMode, and a refusal because the
// question is not open wraps enginehost.ErrQuestionNotOpen and keeps the reason in its text: no question, a hash that
// does not match, or a question not visible on the pane. Any other error is returned wrapped with the pane's id.
func (p *paneQuestions) DeclinePane(ctx context.Context, pane, questionHash string) error {
	client, err := tmuxClient(p.server)
	if err != nil {
		return fmt.Errorf("pane %s: %w", pane, err)
	}
	err = cli.Decline(ctx, client, pane, questionHash)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, cli.ErrPaneInMode):
		return fmt.Errorf("pane %s: %w", pane, enginehost.ErrPaneInMode)
	case errors.Is(err, cli.ErrQuestionRefused):
		return fmt.Errorf("pane %s: %w: %w", pane, enginehost.ErrQuestionNotOpen, err)
	default:
		return fmt.Errorf("pane %s: %w", pane, err)
	}
}

// PaneSeen reports pane seen at seen to the notice router. The router ends the pane's pending notice when that notice was
// recorded at or before seen, and forwards the seen to the phone. A nil router does nothing.
func (p *paneQuestions) PaneSeen(ctx context.Context, pane string, seen time.Time) {
	if p.router == nil {
		return
	}
	p.router.Seen(ctx, notice.Key{Pane: pane}, seen.Unix())
}
