package enginehost

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
)

const (
	stateInterrupted = "interrupted"
	// shortSessionLen is how many leading characters of a session id ntfy shows.
	shortSessionLen = 8
)

// Notice is one headless session event the owner is told about.
type Notice struct {
	Session, Dir, State, Body string
}

// SessionPublisher posts a headless session notification to ntfy. *ntfy.Publisher satisfies it.
type SessionPublisher interface {
	PublishSession(ctx context.Context, project, session, state, body string) error
}

// PushNotices is the notice record that sends push notifications and clears them when a session is seen.
// *notice.Notices satisfies it.
type PushNotices interface {
	Notify(ctx context.Context, key notice.Key, build func(context.Context) (push.Notification, error))
	Seen(ctx context.Context, key notice.Key, seen int64)
}

// NotifierConfig configures a Notifier. A nil Ntfy or Push skips that channel, and a nil Logger discards.
type NotifierConfig struct {
	Ntfy   SessionPublisher
	Push   PushNotices
	Logger *slog.Logger
}

// Notifier tells the owner about headless session events. Each ntfy post runs in a goroutine that Wait awaits.
type Notifier struct {
	ntfy   SessionPublisher
	push   PushNotices
	logger *slog.Logger
	posts  sync.WaitGroup
}

// NewNotifier returns a Notifier that posts through the channels cfg names.
func NewNotifier(cfg NotifierConfig) *Notifier {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Notifier{ntfy: cfg.Ntfy, push: cfg.Push, logger: logger}
}

// Notify starts the ntfy post for event and hands its push to the notice record, and returns at once. Only waiting,
// done, failed and interrupted notify. The posts ignore ctx's cancellation, so a notification accepted before a
// shutdown still goes out.
func (n *Notifier) Notify(ctx context.Context, event Notice) {
	if !notifies(event.State) {
		return
	}
	ctx = context.WithoutCancel(ctx)
	if n.ntfy != nil {
		n.posts.Add(1)
		go func() {
			defer n.posts.Done()
			n.postNtfy(ctx, event)
		}()
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

// Wait returns once every ntfy post started by Notify has finished.
func (n *Notifier) Wait() {
	n.posts.Wait()
}

// notifies reports whether a session in state gets a notification.
func notifies(state string) bool {
	switch state {
	case string(protocol.StateWaiting), string(protocol.StateDone), stateFailed, stateInterrupted:
		return true
	}
	return false
}

// postNtfy posts event to ntfy. The body falls back to a fixed word when empty.
func (n *Notifier) postNtfy(ctx context.Context, event Notice) {
	session := event.Session
	if len(session) > shortSessionLen {
		session = session[:shortSessionLen]
	}
	body := event.Body
	if body == "" {
		body = fallbackBody(event.State)
	}
	project := filepath.Base(event.Dir)
	if err := n.ntfy.PublishSession(ctx, project, session, event.State, body); err != nil {
		n.logger.ErrorContext(ctx, "post session notification", "session", event.Session,
			"error", push.RedactEndpoints(err))
	}
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

// fallbackBody stands in for an empty body: a prompt for waiting, and the state name otherwise.
func fallbackBody(state string) string {
	if state == string(protocol.StateWaiting) {
		return "waiting for input"
	}
	return state
}
