package enginehost

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"

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

// PushPublisher posts a notification to every push subscription. *push.Publisher satisfies it.
type PushPublisher interface {
	Publish(ctx context.Context, note push.Notification) error
}

// NotifierConfig configures a Notifier. A nil Ntfy or Push skips that channel, and a nil Logger discards.
type NotifierConfig struct {
	Host   string
	Ntfy   SessionPublisher
	Push   PushPublisher
	Logger *slog.Logger
}

// Notifier tells the owner about headless session events. Each post runs in a goroutine that Wait awaits.
type Notifier struct {
	host   string
	ntfy   SessionPublisher
	push   PushPublisher
	logger *slog.Logger
	posts  sync.WaitGroup
}

// NewNotifier returns a Notifier that posts through the channels cfg names.
func NewNotifier(cfg NotifierConfig) *Notifier {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Notifier{host: cfg.Host, ntfy: cfg.Ntfy, push: cfg.Push, logger: logger}
}

// Notify starts the posts for notice and returns at once. Only waiting, done, failed and interrupted notify. The
// posts ignore ctx's cancellation, so a notification accepted before a shutdown still goes out.
func (n *Notifier) Notify(ctx context.Context, notice Notice) {
	if !notifies(notice.State) {
		return
	}
	ctx = context.WithoutCancel(ctx)
	n.posts.Add(1)
	go func() {
		defer n.posts.Done()
		n.post(ctx, notice)
	}()
}

// Wait returns once every post started by Notify has finished.
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

// post sends notice to each configured channel. A failure is logged and does not stop the other channel.
func (n *Notifier) post(ctx context.Context, notice Notice) {
	if n.ntfy != nil {
		n.postNtfy(ctx, notice)
	}
	if n.push != nil {
		n.postPush(ctx, notice)
	}
}

// postNtfy posts notice to ntfy. The body falls back to a fixed word when empty.
func (n *Notifier) postNtfy(ctx context.Context, notice Notice) {
	session := notice.Session
	if len(session) > shortSessionLen {
		session = session[:shortSessionLen]
	}
	body := notice.Body
	if body == "" {
		body = fallbackBody(notice.State)
	}
	project := filepath.Base(notice.Dir)
	if err := n.ntfy.PublishSession(ctx, project, session, notice.State, body); err != nil {
		n.logger.ErrorContext(ctx, "post session notification", "session", notice.Session,
			"error", push.RedactEndpoints(err))
	}
}

// postPush posts notice to every push subscription. The body is the question when the session waits and the
// summary otherwise, and it is carried as given.
func (n *Notifier) postPush(ctx context.Context, notice Notice) {
	note := push.Notification{
		Host:    n.host,
		Project: filepath.Base(notice.Dir),
		Session: notice.Session,
		State:   protocol.State(notice.State),
	}
	if notice.State == string(protocol.StateWaiting) {
		note.Question = notice.Body
	} else {
		note.Summary = notice.Body
	}
	if err := n.push.Publish(ctx, note); err != nil {
		n.logger.ErrorContext(ctx, "post push notification", "session", notice.Session,
			"error", push.RedactEndpoints(err))
	}
}

// fallbackBody stands in for an empty body: a prompt for waiting, and the state name otherwise.
func fallbackBody(state string) string {
	if state == string(protocol.StateWaiting) {
		return "waiting for input"
	}
	return state
}
