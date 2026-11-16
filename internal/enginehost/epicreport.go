package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// epicReportSchema is the structured output a report call must return.
const epicReportSchema = `{"type":"object","additionalProperties":false,"required":["why"],"properties":{"why":{"type":"string"}}}`

// epicReportInstruction asks for the why that the report text follows in the call's input.
const epicReportInstruction = "Explain in one short paragraph of plain prose why this epic ended as it did, from its outcome, the Requirements that shipped and those that did not, and its gaps. Answer with the why field only."

// EpicReportStore reads the epics that need a report, reads an epic's report inputs, and stores a report. *store.Store
// satisfies it.
type EpicReportStore interface {
	UnreportedEpics(ctx context.Context) ([]store.EpicRow, error)
	EpicFlow(ctx context.Context, id string) (store.EpicFlow, error)
	SetEpicReport(ctx context.Context, id string, report store.EpicReport) error
}

// EpicReportsConfig configures EpicReports. Store reads the epics and stores their reports, Haiku makes each why call,
// Tick is the channel each pass after the first waits on, and Logger logs a failed read, call or write. A nil Logger
// discards. Passed is a test seam: when set, Run calls it after each pass. Production leaves it nil.
type EpicReportsConfig struct {
	Store  EpicReportStore
	Haiku  haikuCaller
	Tick   <-chan time.Time
	Logger *slog.Logger
	Passed func()
}

// EpicReports writes the report of each ended epic that has none for its ending. It keeps the endings, epic id and
// phase, it has tried since it started, so each ending of an epic gets at most one call per start.
type EpicReports struct {
	cfg   EpicReportsConfig
	tried map[string]bool
}

// NewEpicReports returns an EpicReports for cfg.
func NewEpicReports(cfg EpicReportsConfig) *EpicReports {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &EpicReports{cfg: cfg, tried: make(map[string]bool)}
}

// Run writes the reports at start and at each tick until ctx ends.
func (e *EpicReports) Run(ctx context.Context) {
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

// pass reports each stored ended epic that has no report for its ending and whose ending this start has not tried, one
// call at a time, in id order. A failed read is logged and the pass does nothing else; the next pass reads again.
func (e *EpicReports) pass(ctx context.Context) {
	rows, err := e.cfg.Store.UnreportedEpics(ctx)
	if err != nil {
		e.cfg.Logger.ErrorContext(ctx, "read unreported epics", "error", err)
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		ending := row.ID + "\x00" + row.Phase
		if e.tried[ending] {
			continue
		}
		e.tried[ending] = true
		e.report(ctx, row.ID)
	}
}

// report calls Haiku for the why of the epic with id and stores its report with the shipped and not-shipped ids. A
// failed read, call or reply is logged and leaves the report empty. The write ignores ctx's cancellation, so a stop that
// lands after a reply still stores it.
func (e *EpicReports) report(ctx context.Context, id string) {
	flow, err := e.cfg.Store.EpicFlow(ctx, id)
	if err != nil {
		e.cfg.Logger.ErrorContext(ctx, "read epic report inputs", "epic", id, "error", err)
		return
	}
	shipped, notShipped := requirementSplit(flow.Epic.Requirements, flow.Evidence)
	raw, err := e.cfg.Haiku.Call(
		ctx, epicReportSchema, epicReportInstruction+"\n\n"+epicReportText(flow, shipped, notShipped),
	)
	if err != nil {
		e.cfg.Logger.ErrorContext(ctx, "report epic", "epic", id, "error", err)
		return
	}
	why, err := epicWhyOf(raw)
	if err != nil {
		e.cfg.Logger.ErrorContext(ctx, "invalid epic report", "epic", id, "error", err)
		return
	}
	report := store.EpicReport{
		Phase:      flow.Epic.Phase,
		Shipped:    requirementIDs(shipped),
		NotShipped: requirementIDs(notShipped),
		Why:        why,
	}
	if err = e.cfg.Store.SetEpicReport(context.WithoutCancel(ctx), id, report); err != nil {
		e.cfg.Logger.ErrorContext(ctx, "store epic report", "epic", id, "error", err)
	}
}

// requirementSplit returns the Requirements that shipped and those that did not, each in contract order. A Requirement
// shipped when its evidence in runs reads green.
func requirementSplit(
	requirements []store.EpicRequirement, runs []store.EvidenceRun,
) (shipped, notShipped []store.EpicRequirement) {
	for _, requirement := range requirements {
		if requirementState(requirement.ID, runs) == requirementGreen {
			shipped = append(shipped, requirement)
			continue
		}
		notShipped = append(notShipped, requirement)
	}
	return shipped, notShipped
}

// requirementIDs returns the ids of requirements in order.
func requirementIDs(requirements []store.EpicRequirement) []string {
	ids := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		ids = append(ids, requirement.ID)
	}
	return ids
}

// epicReportText renders the outcome of flow for the report call: its phase, the Requirements that shipped and those
// that did not with their texts, and the gaps of its ending.
func epicReportText(flow store.EpicFlow, shipped, notShipped []store.EpicRequirement) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Outcome: %s\n", flow.Epic.Phase)
	out.WriteString("Shipped:\n")
	for _, r := range shipped {
		fmt.Fprintf(&out, "- %s: %s\n", r.ID, r.Text)
	}
	out.WriteString("Not shipped:\n")
	for _, r := range notShipped {
		fmt.Fprintf(&out, "- %s: %s\n", r.ID, r.Text)
	}
	out.WriteString("Gaps:\n")
	for _, gap := range kinds.Gaps(flow.Review) {
		fmt.Fprintf(&out, "- %s\n", gap)
	}
	return out.String()
}

// epicReportReply is the structured output a report call decodes.
type epicReportReply struct {
	Why string `json:"why"`
}

// epicWhyOf returns the trimmed why raw holds. It is an error when raw is not an object with a why, or the why is blank.
func epicWhyOf(raw json.RawMessage) (string, error) {
	var reply epicReportReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return "", fmt.Errorf("decode why: %w", err)
	}
	why := strings.TrimSpace(reply.Why)
	if why == "" {
		return "", errors.New("why is blank")
	}
	return why, nil
}
