package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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

// SessionHeadlineStore reads the headless sessions whose turns have ended since their last headline, reads their recent
// turns, and stores a headline for one. *store.Store satisfies it.
type SessionHeadlineStore interface {
	HeadlineDueSessions(ctx context.Context) ([]store.HeadlineDueSession, error)
	RecentTurns(ctx context.Context, id string, items int) ([]store.RecentTurn, error)
	SetSessionHeadline(ctx context.Context, id, headline string, at time.Time) error
}

// SessionHeadlinesConfig configures SessionHeadlines. Store reads the due sessions and their turns and stores their
// headlines, Haiku makes each headline call, Tick is the channel each pass after the first waits on, Now dates the
// headline writes and the 5-minute windows, and Logger logs a failed read, call or write. A nil Now is time.Now and a
// nil Logger discards. Passed is a test seam: when set, Run calls it after each pass. Production leaves it nil.
type SessionHeadlinesConfig struct {
	Store  SessionHeadlineStore
	Haiku  haikuCaller
	Tick   <-chan time.Time
	Now    func() time.Time
	Logger *slog.Logger
	Passed func()
}

// SessionHeadlines writes a fresh headline for each headless session whose turns have ended since its last headline,
// at most once per sessionHeadlineInterval. A failed read, call, reply or write holds its session off in memory for
// that interval, so the session is not called again inside it.
type SessionHeadlines struct {
	cfg  SessionHeadlinesConfig
	held map[string]time.Time
}

// NewSessionHeadlines returns a SessionHeadlines for cfg.
func NewSessionHeadlines(cfg SessionHeadlinesConfig) *SessionHeadlines {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &SessionHeadlines{cfg: cfg, held: make(map[string]time.Time)}
}

// Run writes the headlines at start and at each tick until ctx ends.
func (h *SessionHeadlines) Run(ctx context.Context) {
	for {
		h.pass(ctx)
		if h.cfg.Passed != nil {
			h.cfg.Passed()
		}
		select {
		case <-ctx.Done():
			return
		case <-h.cfg.Tick:
		}
	}
}

// pass headlines each due session whose window has passed and that no failure holds off, in id order. A session still
// inside its window is skipped, and the pass after the window passes writes it. A failed read is logged and the pass
// does nothing else; the next pass reads again.
func (h *SessionHeadlines) pass(ctx context.Context) {
	due, err := h.cfg.Store.HeadlineDueSessions(ctx)
	if err != nil {
		h.cfg.Logger.ErrorContext(ctx, "read due sessions", "error", err)
		return
	}
	for _, row := range due {
		if ctx.Err() != nil {
			return
		}
		now := h.cfg.Now()
		if now.Before(time.Unix(row.HeadlineAt, 0).Add(sessionHeadlineInterval)) {
			continue
		}
		if until, held := h.held[row.ID]; held {
			if now.Before(until) {
				continue
			}
			delete(h.held, row.ID)
		}
		h.headline(ctx, row)
	}
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

// hold holds session id off until sessionHeadlineInterval from now, so no pass calls for it sooner.
func (h *SessionHeadlines) hold(id string) {
	h.held[id] = h.cfg.Now().Add(sessionHeadlineInterval)
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
