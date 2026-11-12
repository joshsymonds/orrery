package enginehost

import (
	"context"
	"path/filepath"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
)

const stateInterrupted = "interrupted"

// Notice is one headless session event the owner is told about.
type Notice struct {
	Session, Dir, State, Body string
}

// PushNotices is the notice record that sends push notifications and clears them when a session is seen.
// *notice.Notices satisfies it.
type PushNotices interface {
	Notify(ctx context.Context, key notice.Key, build func(context.Context) (push.Notification, error))
	Seen(ctx context.Context, key notice.Key, seen int64)
}

// DeskPoster posts a notice to the desk stream. *deskstream.Hub satisfies it.
type DeskPoster interface {
	Post(deskstream.Notice)
}

// NotifierConfig configures a Notifier. A nil Push or Desk skips that channel.
type NotifierConfig struct {
	Push PushNotices
	Desk DeskPoster
}

// Notifier tells the owner about headless session events.
type Notifier struct {
	push PushNotices
	desk DeskPoster
}

// NewNotifier returns a Notifier that posts through the channels cfg names.
func NewNotifier(cfg NotifierConfig) *Notifier {
	return &Notifier{push: cfg.Push, desk: cfg.Desk}
}

// Notify posts event to the desk stream and hands its push to the notice record, and returns at once. Only waiting,
// done, failed and interrupted notify. The push hand-off ignores ctx's cancellation, so a notification accepted before
// a shutdown still goes out.
func (n *Notifier) Notify(ctx context.Context, event Notice) {
	if !notifies(event.State) {
		return
	}
	ctx = context.WithoutCancel(ctx)
	if n.desk != nil {
		project := filepath.Base(event.Dir)
		n.desk.Post(deskstream.Notice{
			State: event.State,
			Title: deskstream.SessionTitle(project, event.Session),
			Body:  noticeBody(event),
		})
	}
	if n.push != nil {
		build := func(context.Context) (push.Notification, error) {
			return pushNotification(event), nil
		}
		n.push.Notify(ctx, notice.Key{Session: event.Session}, build)
	}
}

// Seen reports that session was seen at seen to the notice record, which clears the session's notice when that
// notice is no newer than seen.
func (n *Notifier) Seen(ctx context.Context, session string, seen time.Time) {
	if n.push == nil {
		return
	}
	n.push.Seen(ctx, notice.Key{Session: session}, seen.Unix())
}

// notifies reports whether a session in state gets a notification.
func notifies(state string) bool {
	switch state {
	case string(protocol.StateWaiting), string(protocol.StateDone), stateFailed, stateInterrupted:
		return true
	}
	return false
}

// pushNotification is the push for event. The body is the question when the session waits and the summary
// otherwise, and it is carried as given. The notice record fills in the host, pane, session and time.
func pushNotification(event Notice) push.Notification {
	note := push.Notification{
		Project: filepath.Base(event.Dir),
		State:   protocol.State(event.State),
	}
	if event.State == string(protocol.StateWaiting) {
		note.Question = event.Body
	} else {
		note.Summary = event.Body
	}
	return note
}

// noticeBody is the body event shows on the desk stream: the event's body, or a fixed word when it is empty.
func noticeBody(event Notice) string {
	if event.Body != "" {
		return event.Body
	}
	return fallbackBody(event.State)
}

// fallbackBody stands in for an empty body: a prompt for waiting, and the state name otherwise.
func fallbackBody(state string) string {
	if state == string(protocol.StateWaiting) {
		return "waiting for input"
	}
	return state
}
