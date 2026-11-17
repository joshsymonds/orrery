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

// sessionTitleSchema is the structured output a title call must return.
const sessionTitleSchema = `{"type":"object","additionalProperties":false,"required":["title"],"properties":{"title":{"type":"string"}}}`

// sessionTitleInstruction asks for the title that the session's first prompt follows in the call's input.
const sessionTitleInstruction = "Give a short title of a few words naming what this session is about, from its first prompt below. Answer with the title field only."

// SessionTitleStore reads the headless sessions that have no title and stores a title for one. *store.Store satisfies it.
type SessionTitleStore interface {
	UntitledSessions(ctx context.Context) ([]store.UntitledSession, error)
	SetSessionTitle(ctx context.Context, id, title string) error
}

// SessionTitlesConfig configures SessionTitles. Store reads the sessions and stores their titles, Haiku makes each title
// call, Tick is the channel each pass after the first waits on, and Logger logs a failed read, call or write. A nil
// Logger discards. Passed is a test seam: when set, Run calls it after each pass. Production leaves it nil.
type SessionTitlesConfig struct {
	Store  SessionTitleStore
	Haiku  haikuCaller
	Tick   <-chan time.Time
	Logger *slog.Logger
	Passed func()
}

// SessionTitles writes a title for each headless session that has none, from its first prompt. It keeps the ids it has
// tried since it started, so each session gets at most one call per start.
type SessionTitles struct {
	cfg   SessionTitlesConfig
	tried map[string]bool
}

// NewSessionTitles returns a SessionTitles for cfg.
func NewSessionTitles(cfg SessionTitlesConfig) *SessionTitles {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &SessionTitles{cfg: cfg, tried: make(map[string]bool)}
}

// Run writes the titles at start and at each tick until ctx ends.
func (t *SessionTitles) Run(ctx context.Context) {
	for {
		t.pass(ctx)
		if t.cfg.Passed != nil {
			t.cfg.Passed()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.cfg.Tick:
		}
	}
}

// pass titles each untitled session that this start has not tried, one call at a time, in id order. A failed read is
// logged and the pass does nothing else; the next pass reads again.
func (t *SessionTitles) pass(ctx context.Context) {
	rows, err := t.cfg.Store.UntitledSessions(ctx)
	if err != nil {
		t.cfg.Logger.ErrorContext(ctx, "read untitled sessions", "error", err)
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		if t.tried[row.ID] {
			continue
		}
		t.tried[row.ID] = true
		t.title(ctx, row)
	}
}

// title calls Haiku for the title of row and stores it when the reply has a non-blank title. A failed call or reply is
// logged and leaves the title empty. The write ignores ctx's cancellation, so a stop that lands after a reply still
// stores it.
func (t *SessionTitles) title(ctx context.Context, row store.UntitledSession) {
	raw, err := t.cfg.Haiku.Call(ctx, sessionTitleSchema, sessionTitleInstruction+"\n\n"+row.FirstPrompt)
	if err != nil {
		t.cfg.Logger.ErrorContext(ctx, "title session", "session", row.ID, "error", err)
		return
	}
	title, err := sessionTitleOf(raw)
	if err != nil {
		t.cfg.Logger.ErrorContext(ctx, "invalid session title", "session", row.ID, "error", err)
		return
	}
	if err = t.cfg.Store.SetSessionTitle(context.WithoutCancel(ctx), row.ID, title); err != nil {
		t.cfg.Logger.ErrorContext(ctx, "store session title", "session", row.ID, "error", err)
	}
}

// sessionTitleReply is the structured output a title call decodes.
type sessionTitleReply struct {
	Title string `json:"title"`
}

// sessionTitleOf returns the trimmed title raw holds. It is an error when raw is not an object with a title, or the
// title is blank.
func sessionTitleOf(raw json.RawMessage) (string, error) {
	var reply sessionTitleReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return "", fmt.Errorf("decode title: %w", err)
	}
	title := strings.TrimSpace(reply.Title)
	if title == "" {
		return "", errors.New("title is blank")
	}
	return title, nil
}
