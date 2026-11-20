package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// sessionHeadlineSchema is the structured output a headline call must return.
const sessionHeadlineSchema = `{"type":"object","additionalProperties":false,"required":["headline"],"properties":{"headline":{"type":"string"}}}`

// sessionHeadlineInstruction asks for the headline the call's input supports: the first prompt started the session, and
// the recent turns, oldest first, show what it has done since.
const sessionHeadlineInstruction = "Give a one-line headline of a few words saying what this session has been doing. " +
	"The first prompt below started it, and the recent turns, oldest first, show what it has done since. " +
	"Answer with the headline field only."

// sessionHeadlineTurns is how many of a session's most recent turns a headline call reads.
const sessionHeadlineTurns = 12

// sessionHeadlineInterval is how long a session waits after its headline write before the next one, and how long a
// failure holds it off.
const sessionHeadlineInterval = 5 * time.Minute

// SessionHeadlineStore reads the headless sessions whose turns have ended since their last headline, reads one of them,
// reads their recent turns, and stores a headline for one. *store.Store satisfies it.
type SessionHeadlineStore interface {
	HeadlineDueSessions(ctx context.Context) ([]store.HeadlineDueSession, error)
	HeadlineDueSession(ctx context.Context, id string) (store.HeadlineDueSession, bool, error)
	RecentTurns(ctx context.Context, id string, items int) ([]store.RecentTurn, error)
	SetSessionHeadline(ctx context.Context, id, headline string, at time.Time) error
}

// SessionHeadlinesConfig configures SessionHeadlines. Store reads the due sessions and their turns and stores their
// headlines, Haiku makes each headline call, NewTimer arms the one retry a failed read waits on, AfterFunc arms the
// deadline of each session that waits, Now dates the headline writes and the 5-minute windows, and Logger logs a failed
// read, call or write. A nil NewTimer wraps time.NewTimer, a nil AfterFunc wraps time.AfterFunc, a nil Now is time.Now,
// and a nil Logger discards. AfterFunc must run its function on a goroutine of its own, as time.AfterFunc does. Passed
// is a test seam: when set, Run calls it after each pass. Production leaves it nil.
type SessionHeadlinesConfig struct {
	Store     SessionHeadlineStore
	Haiku     haikuCaller
	NewTimer  func(time.Duration) (<-chan time.Time, func() bool)
	AfterFunc func(time.Duration, func()) (stop func() bool)
	Now       func() time.Time
	Logger    *slog.Logger
	Passed    func()
}

// SessionHeadlines writes a fresh headline for each headless session whose turns have ended since its last headline,
// at most once per sessionHeadlineInterval. A failed read, call, reply or write holds its session off in memory for
// that interval, so the session is not called again inside it. A session that waits, inside its window or held, has one
// deadline armed for the time left, and that deadline wakes the session when it fires.
type SessionHeadlines struct {
	cfg   SessionHeadlinesConfig
	held  map[string]time.Time
	wakes *housekeepWake
	// fullOwed is set until a read of every due session succeeds. It is set at start, so the start reads all of them,
	// and it stays set when the read fails, so the retry reads them again.
	fullOwed bool
	// mu guards deadlines, the stop of each armed deadline by session id. A deadline's function removes its own entry on
	// the goroutine AfterFunc runs it on, and the entry is stored before that function can run, since arm holds mu.
	mu        sync.Mutex
	deadlines map[string]func() bool
}

// NewSessionHeadlines returns a SessionHeadlines for cfg.
func NewSessionHeadlines(cfg SessionHeadlinesConfig) *SessionHeadlines {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.AfterFunc == nil {
		cfg.AfterFunc = func(d time.Duration, fn func()) func() bool {
			return time.AfterFunc(d, fn).Stop
		}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &SessionHeadlines{
		cfg: cfg, held: make(map[string]time.Time), wakes: newHousekeepWake(cfg.NewTimer), fullOwed: true,
		deadlines: make(map[string]func() bool),
	}
}

// Wake asks Run to headline the session with id, without blocking. The store's OnTurnEnded hook calls it for each
// engine session whose turn it ends.
func (h *SessionHeadlines) Wake(session string) {
	h.wakes.Wake(session)
}

// Run writes the headlines at start, over every due session, and then on each wake, each deadline and each retry, until
// ctx ends. Run stops each deadline still armed when it returns. A pass whose read the store refused arms one retry, and
// a clean pass arms none.
func (h *SessionHeadlines) Run(ctx context.Context) {
	defer h.stopDeadlines()
	h.wakes.run(ctx, func(ctx context.Context, retry bool) bool {
		owed := h.pass(ctx, retry)
		if h.cfg.Passed != nil {
			h.cfg.Passed()
		}
		return owed
	})
}

// pass makes one round. It reads the sessions it owes a read: every due session while the full read is owed, and the
// sessions the wakes named, with the ones whose read failed on a retry. It reports whether a retry is still owed for the
// full read.
func (h *SessionHeadlines) pass(ctx context.Context, retry bool) bool {
	if h.fullOwed {
		h.fullPass(ctx)
	}
	for _, id := range h.wakes.take(retry) {
		h.wokenSession(ctx, id)
	}
	return h.fullOwed
}

// fullPass considers each due session, in id order. A failed read is logged and leaves the full read owed.
func (h *SessionHeadlines) fullPass(ctx context.Context) {
	due, err := h.cfg.Store.HeadlineDueSessions(ctx)
	if err != nil {
		h.cfg.Logger.ErrorContext(ctx, "read due sessions", "error", err)
		return
	}
	h.fullOwed = false
	for _, row := range due {
		if ctx.Err() != nil {
			return
		}
		h.consider(ctx, row)
	}
}

// wokenSession reads the session with id and considers it when it is due. A session that is not due is dropped, and a
// failed read is logged and kept for the retry.
func (h *SessionHeadlines) wokenSession(ctx context.Context, id string) {
	row, due, err := h.cfg.Store.HeadlineDueSession(ctx, id)
	switch {
	case err != nil:
		h.cfg.Logger.ErrorContext(ctx, "read due session", "session", id, "error", err)
		h.wakes.keep(id)
	case due:
		h.consider(ctx, row)
	}
}

// consider headlines the due session row when its window has passed and no failure holds it off. A session still inside
// its window, or held, is not written: its deadline is armed for the time left, and the read that follows the deadline
// decides again.
func (h *SessionHeadlines) consider(ctx context.Context, row store.HeadlineDueSession) {
	now := h.cfg.Now()
	if windowEnd := time.Unix(row.HeadlineAt, 0).Add(sessionHeadlineInterval); now.Before(windowEnd) {
		h.arm(row.ID, windowEnd.Sub(now))
		return
	}
	if until, held := h.held[row.ID]; held {
		if now.Before(until) {
			h.arm(row.ID, until.Sub(now))
			return
		}
		delete(h.held, row.ID)
	}
	h.headline(ctx, row)
}

// arm arms the deadline of session id, d from now, unless one is already armed for it. When the deadline fires it
// forgets itself and wakes the session.
func (h *SessionHeadlines) arm(id string, wait time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, armed := h.deadlines[id]; armed {
		return
	}
	h.deadlines[id] = h.cfg.AfterFunc(wait, func() {
		h.mu.Lock()
		delete(h.deadlines, id)
		h.mu.Unlock()
		h.wakes.Wake(id)
	})
}

// stopDeadlines stops each deadline still armed, so none wakes a session after Run returns.
func (h *SessionHeadlines) stopDeadlines() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, stop := range h.deadlines {
		stop()
	}
	clear(h.deadlines)
}

// headline reads the recent turns of row, calls Haiku for its headline and stores the headline when the reply has a
// non-blank one. A failure is logged and holds the session off. The write ignores ctx's cancellation, so a stop that
// lands after a reply still stores it.
func (h *SessionHeadlines) headline(ctx context.Context, row store.HeadlineDueSession) {
	turns, err := h.cfg.Store.RecentTurns(ctx, row.ID, sessionHeadlineTurns)
	if err != nil {
		h.cfg.Logger.ErrorContext(ctx, "read recent turns", "session", row.ID, "error", err)
		h.hold(row.ID)
		return
	}
	raw, err := h.cfg.Haiku.Call(ctx, sessionHeadlineSchema, sessionHeadlineInput(row.FirstPrompt, turns))
	if err != nil {
		h.cfg.Logger.ErrorContext(ctx, "headline session", "session", row.ID, "error", err)
		h.hold(row.ID)
		return
	}
	headline, err := sessionHeadlineOf(raw)
	if err != nil {
		h.cfg.Logger.ErrorContext(ctx, "invalid session headline", "session", row.ID, "error", err)
		h.hold(row.ID)
		return
	}
	if err = h.cfg.Store.SetSessionHeadline(context.WithoutCancel(ctx), row.ID, headline, h.cfg.Now()); err != nil {
		h.cfg.Logger.ErrorContext(ctx, "store session headline", "session", row.ID, "error", err)
		h.hold(row.ID)
	}
}

// hold holds session id off until sessionHeadlineInterval from now, and arms its deadline for that interval, so no read
// headlines it sooner.
func (h *SessionHeadlines) hold(id string) {
	h.held[id] = h.cfg.Now().Add(sessionHeadlineInterval)
	h.arm(id, sessionHeadlineInterval)
}

// sessionHeadlineInput is the call's input: the instruction, the session's first prompt, and its turns oldest first,
// each on its own line as its role and text.
func sessionHeadlineInput(firstPrompt string, turns []store.RecentTurn) string {
	lines := make([]string, 0, len(turns))
	for _, turn := range turns {
		lines = append(lines, turn.Role+": "+turn.Text)
	}
	return sessionHeadlineInstruction + "\n\nFirst prompt:\n" + firstPrompt +
		"\n\nRecent turns, oldest first:\n" + strings.Join(lines, "\n")
}

// sessionHeadlineReply is the structured output a headline call decodes.
type sessionHeadlineReply struct {
	Headline string `json:"headline"`
}

// sessionHeadlineOf returns the trimmed headline raw holds. It is an error when raw is not an object with a headline,
// or the headline is blank.
func sessionHeadlineOf(raw json.RawMessage) (string, error) {
	var reply sessionHeadlineReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return "", fmt.Errorf("decode headline: %w", err)
	}
	headline := strings.TrimSpace(reply.Headline)
	if headline == "" {
		return "", errors.New("headline is blank")
	}
	return headline, nil
}
