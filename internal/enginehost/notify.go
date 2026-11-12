package enginehost

import (
	"context"
	"path/filepath"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
)

const stateInterrupted = "interrupted"

// Notice is one headless session event the owner is told about.
type Notice struct {
	Session, Dir, State, Body string
}

// Router routes notices to the owner's desk and phone. *route.Router satisfies it.
type Router interface {
	Notify(ctx context.Context, n route.Notice)
	Changed(key notice.Key)
	Seen(ctx context.Context, key notice.Key, seen int64)
}

// NotifierConfig configures a Notifier. A nil Route sends nothing.
type NotifierConfig struct {
	Route Router
}

// Notifier tells the owner about headless session events.
type Notifier struct {
	route Router
}

// NewNotifier returns a Notifier that hands its notices to cfg's Route.
func NewNotifier(cfg NotifierConfig) *Notifier {
	return &Notifier{route: cfg.Route}
}

// Notify hands event to the Router as a notice and returns at once. Only waiting, done, failed and interrupted
// notify. The Router ignores ctx's cancellation, so a notice accepted before a shutdown still routes.
func (n *Notifier) Notify(ctx context.Context, event Notice) {
	if !notifies(event.State) || n.route == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	n.route.Notify(ctx, route.Notice{
		Key:   notice.Key{Session: event.Session},
		State: event.State,
		Desk: func(context.Context) (deskstream.Notice, error) {
			return deskstream.Notice{
				State: event.State,
				Title: deskstream.SessionTitle(filepath.Base(event.Dir), event.Session),
				Body:  noticeBody(event),
			}, nil
		},
		Push: func(context.Context) (push.Notification, error) {
			return pushNotification(event), nil
		},
	})
}

// Changed reports to the Router that session left the state its notice was about. The Router ends the session's
// pending notice, if it has one. A nil Route is skipped.
func (n *Notifier) Changed(_ context.Context, session string) {
	if n.route == nil {
		return
	}
	n.route.Changed(notice.Key{Session: session})
}

// Seen reports to the Router that session was seen at seen. The Router clears the session's notice when that notice
// is no newer than seen.
func (n *Notifier) Seen(ctx context.Context, session string, seen time.Time) {
	if n.route == nil {
		return
	}
	n.route.Seen(ctx, notice.Key{Session: session}, seen.Unix())
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
