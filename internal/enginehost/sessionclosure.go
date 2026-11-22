package enginehost

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/closurejudge"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// sessionClosureTurns is how many of a session's most recent turns the closure judge reads.
const sessionClosureTurns = 12

// ClosureStore reads an engine session's state, change time and recent turns, and stores a closure verdict for one.
// *store.Store satisfies it.
type ClosureStore interface {
	ClosureSession(ctx context.Context, id string) (store.ClosureSession, bool, error)
	RecentTurns(ctx context.Context, id string, items int) ([]store.RecentTurn, error)
	SetClosureVerdict(ctx context.Context, verdict store.ClosureVerdict, at time.Time) error
}

// ClosureJudge asks whether a conversation is closed. *closurejudge.Judge satisfies it.
type ClosureJudge interface {
	Closed(ctx context.Context, baseURL string, env map[string]string, recent string) (closurejudge.Verdict, error)
}

// SessionClosuresConfig configures SessionClosures. Store reads each session and its turns and stores its verdict, Judge
// makes each verdict call, and Logger logs a failed read, call or write, where a nil Logger discards. Passed is a test
// seam: when set, Run calls it after each pass that reads a woken session. Production leaves it nil.
type SessionClosuresConfig struct {
	Store  ClosureStore
	Judge  ClosureJudge
	Logger *slog.Logger
	Passed func()
}

// SessionClosures asks the closure judge once for each headless session whose turn ended done, and stores the verdict
// stamped with the session's change time. A session that is absent, or whose turn left it working or waiting, is not
// asked. A failed read, call or write is logged and stores nothing, and is not retried.
type SessionClosures struct {
	cfg   SessionClosuresConfig
	wakes *housekeepWake
}

// NewSessionClosures returns a SessionClosures for cfg.
func NewSessionClosures(cfg SessionClosuresConfig) *SessionClosures {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &SessionClosures{cfg: cfg, wakes: newHousekeepWake(nil)}
}

// Wake asks Run to judge the session with id, without blocking. The store's OnTurnEnded hook calls it for each engine
// session whose turn it ends.
func (c *SessionClosures) Wake(session string) {
	c.wakes.Wake(session)
}

// Run judges each woken session, on each wake, until ctx ends. It makes no pass at start, since nothing is woken then.
func (c *SessionClosures) Run(ctx context.Context) {
	c.wakes.run(ctx, func(ctx context.Context, _ bool) bool {
		c.pass(ctx)
		return false
	})
}

// pass judges each session the wakes named, in the order they were woken, and calls Passed when it judged any.
func (c *SessionClosures) pass(ctx context.Context) {
	ids := c.wakes.take(false)
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		c.closeOut(ctx, id)
	}
	if len(ids) > 0 && c.cfg.Passed != nil {
		c.cfg.Passed()
	}
}

// closeOut asks the judge about the session with id when the session is present and its turn ended done, and stores
// the verdict with the session's change time. The write ignores ctx's cancellation, so a stop that lands after a reply
// still stores it.
func (c *SessionClosures) closeOut(ctx context.Context, id string) {
	sess, ok, err := c.cfg.Store.ClosureSession(ctx, id)
	if err != nil {
		c.cfg.Logger.ErrorContext(ctx, "read closure session", "session", id, "error", err)
		return
	}
	if !ok || sess.State != string(protocol.StateDone) {
		return
	}
	turns, err := c.cfg.Store.RecentTurns(ctx, id, sessionClosureTurns)
	if err != nil {
		c.cfg.Logger.ErrorContext(ctx, "read closure turns", "session", id, "error", err)
		return
	}
	verdict, err := c.cfg.Judge.Closed(ctx, "", nil, closureRecentText(turns))
	if err != nil {
		c.cfg.Logger.ErrorContext(ctx, "closure judge", "session", id, "error", err)
		return
	}
	err = c.cfg.Store.SetClosureVerdict(context.WithoutCancel(ctx), store.ClosureVerdict{
		Session: id, Changed: sess.Changed, Closed: verdict.Closed, Why: verdict.Why,
	}, time.Now())
	if err != nil {
		c.cfg.Logger.ErrorContext(ctx, "store closure verdict", "session", id, "error", err)
	}
}

// closureRecentText renders turns, oldest first, one "role: text" line each.
func closureRecentText(turns []store.RecentTurn) string {
	lines := make([]string, 0, len(turns))
	for _, turn := range turns {
		lines = append(lines, turn.Role+": "+turn.Text)
	}
	return strings.Join(lines, "\n")
}
