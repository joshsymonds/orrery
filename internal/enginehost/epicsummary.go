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

// epicSummarySchema is the structured output a summary call must return.
const epicSummarySchema = `{"type":"object","additionalProperties":false,"required":["summary"],"properties":{"summary":{"type":"string"}}}`

// epicSummaryInstruction asks for the summary that the epic's title, intent and approach follow in the call's input.
const epicSummaryInstruction = "Summarize this epic in one or two plain sentences of at most 40 words, saying what the epic changes and why. Name no tests, no evidence, and no release. Answer with the summary field only."

// EpicSummaryStore reads the epics that have no summary, reads one stored epic, and stores a summary for one. *store.Store
// satisfies it.
type EpicSummaryStore interface {
	UnsummarizedEpics(ctx context.Context) ([]store.EpicRow, error)
	Epic(ctx context.Context, id string) (store.EpicRow, error)
	SetEpicSummary(ctx context.Context, id, summary string) error
}

// EpicSummariesConfig configures EpicSummaries. Store reads the epics and stores their summaries, Haiku makes each
// summary call, NewTimer arms the one retry a refused write or a failed read waits on, and Logger logs a failed read,
// call or write. A nil NewTimer wraps time.NewTimer, and a nil Logger discards. Passed is a test seam: when set, Run
// calls it after each pass. Production leaves it nil.
type EpicSummariesConfig struct {
	Store    EpicSummaryStore
	Haiku    haikuCaller
	NewTimer func(time.Duration) (<-chan time.Time, func() bool)
	Logger   *slog.Logger
	Passed   func()
}

// EpicSummaries writes the summary of each stored epic that has none. It keeps the ids it has tried since it started,
// so each epic gets at most one call per start, and the summaries whose write the store refused, which a retry writes
// again.
type EpicSummaries struct {
	cfg      EpicSummariesConfig
	tried    map[string]bool
	unstored unstoredWrites[string]
	wakes    *housekeepWake
	// fullOwed is set until a read of every epic without a summary succeeds. It is set at start, so the start reads all
	// of them, and it stays set when the read fails, so the retry reads them again.
	fullOwed bool
}

// NewEpicSummaries returns an EpicSummaries for cfg.
func NewEpicSummaries(cfg EpicSummariesConfig) *EpicSummaries {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &EpicSummaries{
		cfg: cfg, tried: make(map[string]bool), unstored: unstoredWrites[string]{},
		wakes: newHousekeepWake(cfg.NewTimer), fullOwed: true,
	}
}

// Wake asks Run to summarize the epic with id, without blocking. The store's OnEpicStored hook calls it for each epic
// it stores.
func (e *EpicSummaries) Wake(epic string) {
	e.wakes.Wake(epic)
}

// Run writes the summaries at start, over every epic, and then on each wake and each retry, until ctx ends. A pass
// whose write or read the store refused arms one retry, and a clean pass arms none.
func (e *EpicSummaries) Run(ctx context.Context) {
	e.wakes.run(ctx, func(ctx context.Context, retry bool) bool {
		owed := e.pass(ctx, retry)
		if e.cfg.Passed != nil {
			e.cfg.Passed()
		}
		return owed
	})
}

// pass makes one round. A retry first writes again the summaries the store refused, with no call. Then it reads the
// epics it owes a read: every epic without a summary while the full read is owed, and the epics the wakes named, with
// the ones whose read failed on a retry. It reports whether a retry is still owed for the full read or a refused write.
func (e *EpicSummaries) pass(ctx context.Context, retry bool) bool {
	if retry {
		if n := e.unstored.retry(context.WithoutCancel(ctx), e.cfg.Store.SetEpicSummary); n > 0 {
			e.cfg.Logger.InfoContext(ctx, "epic summaries stored after a retry", "summaries", n)
		}
	}
	if e.fullOwed {
		e.fullPass(ctx)
	}
	for _, id := range e.wakes.take(retry) {
		e.wokenEpic(ctx, id)
	}
	return e.fullOwed || len(e.unstored) > 0
}

// fullPass summarizes each stored epic that has no summary and that this start has not tried, one call at a time, in id
// order. A failed read is logged and leaves the full read owed.
func (e *EpicSummaries) fullPass(ctx context.Context) {
	rows, err := e.cfg.Store.UnsummarizedEpics(ctx)
	if err != nil {
		e.cfg.Logger.ErrorContext(ctx, "read unsummarized epics", "error", err)
		return
	}
	e.fullOwed = false
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		if e.tried[row.ID] {
			continue
		}
		e.tried[row.ID] = true
		e.summarize(ctx, row)
	}
}

// wokenEpic reads the epic with id and summarizes it when it has no summary and this start has not tried it. An epic no
// longer stored is dropped, and a failed read is logged and kept for the retry.
func (e *EpicSummaries) wokenEpic(ctx context.Context, id string) {
	if e.tried[id] {
		return
	}
	row, err := e.cfg.Store.Epic(ctx, id)
	switch {
	case errors.Is(err, store.ErrNoEpic):
		return
	case err != nil:
		e.cfg.Logger.ErrorContext(ctx, "read epic", "epic", id, "error", err)
		e.wakes.keep(id)
		return
	case row.Summary != "":
		return
	}
	e.tried[id] = true
	e.summarize(ctx, row)
}

// summarize calls Haiku for the summary of row and stores it when the reply has a non-empty summary. A failed call or
// reply is logged and leaves the summary empty. The write ignores ctx's cancellation, so a stop that lands after a reply
// still stores it, and a write the store refuses is logged and kept for a retry to write again.
func (e *EpicSummaries) summarize(ctx context.Context, row store.EpicRow) {
	raw, err := e.cfg.Haiku.Call(ctx, epicSummarySchema, epicSummaryInstruction+"\n\n"+epicSummaryText(row))
	if err != nil {
		e.cfg.Logger.ErrorContext(ctx, "summarize epic", "epic", row.ID, "error", err)
		return
	}
	summary, err := epicSummaryOf(raw)
	if err != nil {
		e.cfg.Logger.ErrorContext(ctx, "invalid epic summary", "epic", row.ID, "error", err)
		return
	}
	if err = e.cfg.Store.SetEpicSummary(context.WithoutCancel(ctx), row.ID, summary); err != nil {
		e.cfg.Logger.ErrorContext(ctx, "store epic summary; kept for a retry", "epic", row.ID, "error", err)
		e.unstored[row.ID] = summary
	}
}

// epicSummaryReply is the structured output a summary call decodes.
type epicSummaryReply struct {
	Summary string `json:"summary"`
}

// epicSummaryOf returns the trimmed summary raw holds. It is an error when raw is not an object with a summary, or the
// summary is blank.
func epicSummaryOf(raw json.RawMessage) (string, error) {
	var reply epicSummaryReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return "", fmt.Errorf("decode summary: %w", err)
	}
	summary := strings.TrimSpace(reply.Summary)
	if summary == "" {
		return "", errors.New("summary is blank")
	}
	return summary, nil
}

// epicSummaryText renders the title, intent and approach of row, each as `Label: text`. They are the only parts of an
// epic a summary reads.
func epicSummaryText(row store.EpicRow) string {
	return fmt.Sprintf("Title: %s\nIntent: %s\nApproach: %s\n", row.Title, row.Intent, row.Approach)
}
