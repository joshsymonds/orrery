package main

import (
	"context"
	"path/filepath"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
)

// observeRemote routes one event of host's pane watch: the event's pane is a pane of host, not of this controller. A
// transition to waiting or done hands the router the notice of that pane, keyed by host and pane, and built from the
// event's snapshot alone, so nothing reads this controller's tmux. Any other transition, an end included, tells the
// router the pane changed. A remote pane starts no summary, done view, closure check or autoseen: those belong to the
// controller's own panes, so an end forgets nothing on the controller's summarizers and no read time. The event must
// carry a Pane and a Transition, as hostfollow passes it.
func (r *relay) observeRemote(ctx context.Context, host string, event node.PaneEvent) {
	transition := remoteTransition(host, event)
	if transition.To == protocol.StateWaiting || transition.To == protocol.StateDone {
		r.router.Notify(ctx, remoteNotice(transition, *event.Pane))
		return
	}
	r.router.Changed(notice.Key{Pane: transition.PaneID, Host: host})
}

// remoteTransition returns the daemon.Transition of event, one of host's pane watch: the pane's id, the states and the
// facts of its update, with Host set to host. Server is zero, since the pane is not on this controller's tmux.
func remoteTransition(host string, event node.PaneEvent) daemon.Transition {
	change := event.Transition
	return daemon.Transition{
		PaneID:      event.Pane.Pane,
		Host:        host,
		From:        change.From,
		To:          change.To,
		Cwd:         change.Cwd,
		BaseURL:     change.BaseURL,
		Transcript:  change.Transcript,
		LastMessage: change.LastMessage,
		Failed:      change.Failed,
		APIError:    change.APIError,
	}
}

// remoteNotice is the notice of a remote pane's transition to waiting or done, keyed by the transition's host and pane.
// Its desk and push text come from snapshot, the pane's snapshot in the event. A remote done is routed unjudged, and the
// notice has no watching check and no restart source.
func remoteNotice(transition daemon.Transition, snapshot node.PaneSnapshot) route.Notice {
	text := snapshotText(snapshot)
	failure, failed := stopFailureText(transition)
	return route.Notice{
		Key:   notice.Key{Pane: transition.PaneID, Host: transition.Host},
		State: string(transition.To),
		Desk: func(context.Context) (deskstream.Notice, error) {
			desk := remoteDesk(transition, text)
			if failed {
				desk.Body = failure
			}
			return desk, nil
		},
		Push: func(context.Context) (push.Notification, error) {
			note := paneNotification(transition, text)
			if failed {
				note.Summary = failure
			}
			return note, nil
		},
	}
}

// snapshotText returns the text a remote pane's notice carries, as its snapshot holds it: the summary, label and
// question its options mirror, and the session and window that locate it.
func snapshotText(snapshot node.PaneSnapshot) paneText {
	return paneText{
		summary:  snapshot.Fields.Summary,
		label:    snapshot.Fields.Label,
		question: snapshot.Fields.Question,
		session:  snapshot.Session,
		window:   snapshot.Window,
	}
}

// remoteDesk returns the desk notice of a remote pane's transition, built from its snapshot text. Its body is the
// question when the pane waits and the summary when it is done, each with its fallback when empty, and its title names
// the project, session and window.
func remoteDesk(transition daemon.Transition, text paneText) deskstream.Notice {
	body, fallback := text.question, waitingBody
	if transition.To == protocol.StateDone {
		body, fallback = text.summary, doneBody
	}
	if body == "" {
		body = fallback
	}
	return deskstream.Notice{
		State: string(transition.To),
		Title: deskstream.PaneTitle(filepath.Base(transition.Cwd), text.session, text.window),
		Body:  body,
	}
}
