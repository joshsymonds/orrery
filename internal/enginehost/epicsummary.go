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

// epicSummaryInstruction asks for the summary that the contract text follows in the call's input.
const epicSummaryInstruction = "Summarize this epic's contract in one short paragraph of plain prose: what the epic builds, why, and what will be true when it is done. Answer with the summary field only."

// EpicSummaryStore reads the epics that have no summary and stores a summary for one. *store.Store satisfies it.
type EpicSummaryStore interface {
	UnsummarizedEpics(ctx context.Context) ([]store.EpicRow, error)
	SetEpicSummary(ctx context.Context, id, summary string) error
}

// EpicSummariesConfig configures EpicSummaries. Store reads the epics and stores their summaries, Haiku makes each
// summary call, Tick is the channel each pass after the first waits on, and Logger logs a failed read, call or write.
// A nil Logger discards. Passed is a test seam: when set, Run calls it after each pass. Production leaves it nil.
type EpicSummariesConfig struct {
	Store  EpicSummaryStore
	Haiku  haikuCaller
	Tick   <-chan time.Time
	Logger *slog.Logger
	Passed func()
}

// EpicSummaries writes the summary of each stored epic that has none. It keeps the ids it has tried since it started,
// so each epic gets at most one call per start.
type EpicSummaries struct {
	cfg   EpicSummariesConfig
	tried map[string]bool
}

// NewEpicSummaries returns an EpicSummaries for cfg.
func NewEpicSummaries(cfg EpicSummariesConfig) *EpicSummaries {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &EpicSummaries{cfg: cfg, tried: make(map[string]bool)}
}

// Run writes the summaries at start and at each tick until ctx ends.
func (e *EpicSummaries) Run(ctx context.Context) {
	for {
		e.pass(ctx)
		if e.cfg.Passed != nil {
			e.cfg.Passed()
		}
		select {
		case <-ctx.Done():
			return
		case <-e.cfg.Tick:
		}
	}
}

// pass summarizes each stored epic that has no summary and that this start has not tried, one call at a time, in id
// order. A failed read is logged and the pass does nothing else; the next pass reads again.
func (e *EpicSummaries) pass(ctx context.Context) {
	rows, err := e.cfg.Store.UnsummarizedEpics(ctx)
	if err != nil {
		e.cfg.Logger.ErrorContext(ctx, "read unsummarized epics", "error", err)
		return
	}
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

// summarize calls Haiku for the summary of row and stores it when the reply has a non-empty summary. A failed call or
// reply is logged and leaves the summary empty. The write ignores ctx's cancellation, so a stop that lands after a reply
// still stores it.
func (e *EpicSummaries) summarize(ctx context.Context, row store.EpicRow) {
	raw, err := e.cfg.Haiku.Call(ctx, epicSummarySchema, epicSummaryInstruction+"\n\n"+epicContractText(row))
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
		e.cfg.Logger.ErrorContext(ctx, "store epic summary", "epic", row.ID, "error", err)
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

// epicContractText renders the contract of row: each text section as `Label: text`, and each list section under its
// label, one item after another.
func epicContractText(row store.EpicRow) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Title: %s\nApproved: %s\nIntent: %s\n", row.Title, row.Approved, row.Intent)
	out.WriteString("Failures:\n")
	for _, f := range row.Failures {
		fmt.Fprintf(&out, "- %s [%s]: %s\n  response: %s\n", f.ID, f.Rating, f.Text, f.Response)
	}
	fmt.Fprintf(&out, "Care: %s\n", row.Care)
	out.WriteString("Additions:\n")
	for _, a := range row.Additions {
		fmt.Fprintf(&out, "- %s: %s\n  why: %s\n  cost: %s\n", a.Location, a.What, a.Why, a.Cost)
	}
	out.WriteString("Requirements:\n")
	for _, r := range row.Requirements {
		fmt.Fprintf(&out, "- %s: %s\n  evidence: %s\n  kind: %s\n", r.ID, r.Text, r.Evidence, r.Kind)
	}
	out.WriteString("Premises:\n")
	for _, p := range row.Premises {
		fmt.Fprintf(&out, "- %s: %s\n  if wrong: %s\n", p.ID, p.Text, p.IfWrong)
	}
	out.WriteString("Exclusions:\n")
	for _, text := range row.Exclusions {
		fmt.Fprintf(&out, "- %s\n", text)
	}
	fmt.Fprintf(&out, "Approach: %s\n", row.Approach)
	out.WriteString("Alternatives:\n")
	for _, a := range row.Alternatives {
		fmt.Fprintf(&out, "- %s\n  why not: %s\n  reconsider when: %s\n", a.Alternative, a.WhyNot, a.Reconsider)
	}
	out.WriteString("Release steps:\n")
	for _, s := range row.ReleaseSteps {
		fmt.Fprintf(&out, "- %s\n  target: %s\n  undo: %s\n", s.Step, s.Target, s.Undo)
	}
	fmt.Fprintf(&out, "Release then: %s\nChecks: %s\n", row.ReleaseThen, row.Checks)
	return out.String()
}
