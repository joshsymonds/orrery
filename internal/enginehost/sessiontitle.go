package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/haikuprompt"
	"github.com/joshsymonds/orrery/internal/store"
)

// sessionTitleSchema is the structured output a title call must return.
const sessionTitleSchema = `{"type":"object","additionalProperties":false,"required":["title"],"properties":{"title":{"type":"string"}}}`

// sessionTitleInstruction asks for the title that the session's first prompt follows in the call's input.
const sessionTitleInstruction = "Give a short title of a few words naming what this session is about, from its first prompt above. Answer with the title field only."

// SessionTitleStore reads the headless sessions that have no title, reads one of them, and stores a title for one.
// *store.Store satisfies it.
type SessionTitleStore interface {
	UntitledSessions(ctx context.Context) ([]store.UntitledSession, error)
	UntitledSession(ctx context.Context, id string) (store.UntitledSession, bool, error)
	SetSessionTitle(ctx context.Context, id, title string) error
}

// SessionTitlesConfig configures SessionTitles. Store reads the sessions and stores their titles, Haiku makes each title
// call, NewTimer arms the one retry a refused write or a failed read waits on, AfterFunc arms the deadline a failed call
// waits out before it is made again (see callRetries), and Logger logs a failed read, call or write. A nil NewTimer
// wraps time.NewTimer, a nil AfterFunc wraps time.AfterFunc, and a nil Logger discards. Passed is a test seam: when set,
// Run calls it after each pass. Production leaves it nil.
type SessionTitlesConfig struct {
	Store     SessionTitleStore
	Haiku     haikuCaller
	NewTimer  func(time.Duration) (<-chan time.Time, func() bool)
	AfterFunc func(time.Duration, func()) func() bool
	Logger    *slog.Logger
	Passed    func()
}

// SessionTitles writes a title for each headless session that has none, from its first prompt. A session whose call
// failed is called for again once its wait ends (see callRetries), and the titles whose write the store refused are kept
// for a retry to write again.
type SessionTitles struct {
	cfg      SessionTitlesConfig
	calls    *callRetries
	unstored unstoredWrites[string]
	wakes    *housekeepWake
	// fullOwed is set until a read of every untitled session succeeds. It is set at start, so the start reads all of
	// them, and it stays set when the read fails, so the retry reads them again.
	fullOwed bool
}

// NewSessionTitles returns a SessionTitles for cfg.
func NewSessionTitles(cfg SessionTitlesConfig) *SessionTitles {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &SessionTitles{
		cfg: cfg, calls: newCallRetries(cfg.AfterFunc), unstored: unstoredWrites[string]{},
		wakes: newHousekeepWake(cfg.NewTimer), fullOwed: true,
	}
}

// Wake asks Run to title the session with id, without blocking. The store's OnFirstPrompt hook calls it for each engine
// session whose first prompt it stores.
func (t *SessionTitles) Wake(session string) {
	t.wakes.Wake(session)
}

// Run writes the titles at start, over every untitled session, and then on each wake and each retry, until ctx ends. A
// pass whose write or read the store refused arms one retry, and a clean pass arms none. Run stops each failed call's
// deadline still armed when it returns.
func (t *SessionTitles) Run(ctx context.Context) {
	defer t.calls.stop()
	t.wakes.run(ctx, func(ctx context.Context, retry bool) bool {
		owed := t.pass(ctx, retry)
		if t.cfg.Passed != nil {
			t.cfg.Passed()
		}
		return owed
	})
}

// pass makes one round. A retry first writes again the titles the store refused, with no call. Then it reads the
// sessions it owes a read: every untitled session while the full read is owed, and the sessions the wakes named, with
// the ones whose read failed on a retry. It reports whether a retry is still owed for the full read or a refused write.
func (t *SessionTitles) pass(ctx context.Context, retry bool) bool {
	if retry {
		if n := t.unstored.retry(context.WithoutCancel(ctx), t.cfg.Store.SetSessionTitle); n > 0 {
			t.cfg.Logger.InfoContext(ctx, "session titles stored after a retry", "titles", n)
		}
	}
	if t.fullOwed {
		t.fullPass(ctx)
	}
	for _, id := range t.wakes.take(retry) {
		t.wokenSession(ctx, id)
	}
	return t.fullOwed || len(t.unstored) > 0
}

// fullPass titles each untitled session whose call is ready (see callRetries) and that has no title kept for a retry to
// write, one call at a time, in id order. A failed read is logged and leaves the full read owed.
func (t *SessionTitles) fullPass(ctx context.Context) {
	rows, err := t.cfg.Store.UntitledSessions(ctx)
	if err != nil {
		t.cfg.Logger.ErrorContext(ctx, "read untitled sessions", "error", err)
		return
	}
	t.fullOwed = false
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		if !t.calls.ready(row.ID) || t.unstored.holds(row.ID) {
			continue
		}
		t.title(ctx, row)
	}
}

// wokenSession reads the session with id and titles it when it is untitled, its call is ready and no title of it is kept
// for a retry to write. A session that is not
// untitled is dropped, with any failure of its call, and a failed read is logged and kept for the retry.
func (t *SessionTitles) wokenSession(ctx context.Context, id string) {
	if !t.calls.ready(id) || t.unstored.holds(id) {
		return
	}
	row, untitled, err := t.cfg.Store.UntitledSession(ctx, id)
	switch {
	case err != nil:
		t.cfg.Logger.ErrorContext(ctx, "read untitled session", "session", id, "error", err)
		t.wakes.keep(id)
		return
	case !untitled:
		t.calls.forget(id)
		return
	}
	t.title(ctx, row)
}

// title calls Haiku for the title of row and stores it when the reply has a non-blank title. A failed call or reply is
// logged, leaves the title empty and holds the session's next call until its wait ends, when its deadline wakes the
// session. A good reply forgets the session's failures. The write ignores ctx's cancellation, so a stop that lands after a reply still
// stores it, and a write the store refuses is logged and kept for a retry to write again.
func (t *SessionTitles) title(ctx context.Context, row store.UntitledSession) {
	raw, err := t.cfg.Haiku.Call(ctx, sessionTitleSchema,
		haikuprompt.Input(sessionTitleInstruction, haikuprompt.Part{Tag: "first_prompt", Text: row.FirstPrompt}))
	if err != nil {
		t.cfg.Logger.ErrorContext(ctx, "title session", "session", row.ID, "error", err)
		t.calls.failed(row.ID, func() { t.Wake(row.ID) })
		return
	}
	title, err := sessionTitleOf(raw)
	if err != nil {
		t.cfg.Logger.ErrorContext(ctx, "invalid session title", "session", row.ID, "error", err)
		t.calls.failed(row.ID, func() { t.Wake(row.ID) })
		return
	}
	t.calls.forget(row.ID)
	if err = t.cfg.Store.SetSessionTitle(context.WithoutCancel(ctx), row.ID, title); err != nil {
		t.cfg.Logger.ErrorContext(ctx, "store session title; kept for the next pass", "session", row.ID, "error", err)
		t.unstored[row.ID] = title
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
