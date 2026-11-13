package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// paneSourceKind is the source kind of a pane notice. Its data is the daemon.Transition that notify routed, as JSON.
const paneSourceKind = "pane"

// paneSource is the stored form of a daemon.Transition. It has the transition's fields under the JSON names a stored
// source uses, and it converts to and from the transition field for field.
type paneSource struct {
	PaneID string             `json:"paneId"`
	Server paneServer         `json:"server"`
	From   protocol.State     `json:"from"`
	To     protocol.State     `json:"to"`
	Frame  protocol.HookFrame `json:"frame"`
}

// paneServer is the stored form of a tmuxctl.Server.
type paneServer struct {
	Name   string `json:"name"`
	Socket string `json:"socket"`
}

// encodePaneSource returns the source data that stores transition, hook included.
func encodePaneSource(transition daemon.Transition) ([]byte, error) {
	data, err := json.Marshal(paneSource{
		PaneID: transition.PaneID,
		Server: paneServer(transition.Server),
		From:   transition.From,
		To:     transition.To,
		Frame:  transition.Frame,
	})
	if err != nil {
		return nil, fmt.Errorf("%s notice: %w", paneSourceKind, err)
	}
	return data, nil
}

// decodePaneSource returns the transition that source data stores.
func decodePaneSource(data []byte) (daemon.Transition, error) {
	var source paneSource
	if err := json.Unmarshal(data, &source); err != nil {
		return daemon.Transition{}, fmt.Errorf("%s notice: %w", paneSourceKind, err)
	}
	return daemon.Transition{
		PaneID: source.PaneID,
		Server: tmuxctl.Server(source.Server),
		From:   source.From,
		To:     source.To,
		Frame:  source.Frame,
	}, nil
}

// paneNotice is the notice transition makes, with the closures that route it: Watching, Desk and Push, and Judge for a
// done that is not silent. notify and rebuildPane both build it, so a restored notice is the notice notify built.
func (r *relay) paneNotice(ctx context.Context, transition daemon.Transition) route.Notice {
	wanted, err := push.Wanted(transition.To, transition.Frame.Hook)
	if err != nil {
		r.logger.WarnContext(ctx, "decide notice push", "pane", transition.PaneID, "error", err)
	}
	silent := ""
	if !wanted {
		silent = silentBackground
	}
	incoming := route.Notice{
		Key:      notice.Key{Pane: transition.PaneID},
		State:    string(transition.To),
		Silent:   silent,
		Watching: func(ctx context.Context) (bool, error) { return r.watching(ctx, transition) },
		Desk:     func(ctx context.Context) (deskstream.Notice, error) { return r.deskNotice(ctx, transition) },
		Push:     func(ctx context.Context) (push.Notification, error) { return r.pushNotification(ctx, transition) },
	}
	if transition.To == protocol.StateDone && silent == "" {
		incoming.Judge = r.judgeDone(ctx, transition)
	}
	return incoming
}

// rebuildPane is the route.Rebuild of paneSourceKind. It decodes the transition that notify stored, checks the pane
// the notice is about, and builds the notice for the transition. at is the row's recorded time, unix seconds.
func (r *relay) rebuildPane(ctx context.Context, data []byte, at int64) (route.Notice, error) {
	transition, err := decodePaneSource(data)
	if err != nil {
		return route.Notice{}, err
	}
	err = checkPane(ctx, transition, at)
	if err != nil {
		return route.Notice{}, err
	}
	return r.paneNotice(ctx, transition), nil
}

// checkPane reads transition's pane from its tmux server once. It returns route.ErrChanged when the pane is gone or in
// another state than transition's To, and route.ErrSeen when the pane was seen at or after at. Any other read failure
// is returned as is.
func checkPane(ctx context.Context, transition daemon.Transition, at int64) error {
	client, err := tmuxClient(transition.Server)
	if err != nil {
		return err
	}
	panes, err := client.ListPanes(ctx)
	if err != nil {
		if paneServerNotRunning(err) {
			return fmt.Errorf("pane %s: server not running: %w", transition.PaneID, route.ErrChanged)
		}
		return fmt.Errorf("list panes: %w", err)
	}
	i := slices.IndexFunc(panes, func(pane protocol.Pane) bool { return pane.ID == transition.PaneID })
	if i < 0 {
		return fmt.Errorf("pane %s is gone: %w", transition.PaneID, route.ErrChanged)
	}
	pane := panes[i]
	if pane.State != transition.To {
		return fmt.Errorf("pane %s is %q, not %q: %w", pane.ID, pane.State, transition.To, route.ErrChanged)
	}
	if pane.Seen >= at {
		return fmt.Errorf("pane %s seen at %d: %w", pane.ID, pane.Seen, route.ErrSeen)
	}
	return nil
}

// paneServerNotRunning reports whether err is tmux saying that its server is not running, the messages internal/open
// tests for the same.
func paneServerNotRunning(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "no server running") || strings.Contains(msg, "error connecting to")
}
