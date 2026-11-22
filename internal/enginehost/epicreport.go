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

// EpicReportStore reads the epics that need a report, reads one of them, reads an epic's report inputs, and stores a
// report. *store.Store satisfies it.
type EpicReportStore interface {
	UnreportedEpics(ctx context.Context) ([]store.EpicRow, error)
	UnreportedEpic(ctx context.Context, id string) (store.EpicRow, bool, error)
	EpicFlow(ctx context.Context, id string) (store.EpicFlow, error)
	SetEpicReport(ctx context.Context, id string, report store.EpicReport) error
}

// EpicReportsConfig configures EpicReports. Store reads the epics and stores their reports, Haiku makes each why call,
// NewTimer arms the one retry a refused write or a failed read waits on, AfterFunc arms the deadline a failed call waits
// out before it is made again (see callRetries), and Logger logs a failed read, call or write. A nil NewTimer wraps
// time.NewTimer, a nil AfterFunc wraps time.AfterFunc, and a nil Logger discards. Passed is a test seam: when set, Run
// calls it after each pass. Production leaves it nil.
type EpicReportsConfig struct {
	Store     EpicReportStore
	Haiku     haikuCaller
	NewTimer  func(time.Duration) (<-chan time.Time, func() bool)
	AfterFunc func(time.Duration, func()) func() bool
	Logger    *slog.Logger
	Passed    func()
}

// EpicReports writes the report of each ended epic that has none for its ending. An ending, epic id and phase, whose call
// failed is called for again once its wait ends (see callRetries), and the reports, by epic id, whose write the store
// refused are kept for a retry to write again.
type EpicReports struct {
	cfg      EpicReportsConfig
	calls    *callRetries
	unstored unstoredWrites[store.EpicReport]
	wakes    *housekeepWake
	// fullOwed is set until a read of every ended epic without a report for its ending succeeds. It is set at start, so
	// the start reads all of them, and it stays set when the read fails, so the retry reads them again.
	fullOwed bool
}

// NewEpicReports returns an EpicReports for cfg.
func NewEpicReports(cfg EpicReportsConfig) *EpicReports {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &EpicReports{
		cfg: cfg, calls: newCallRetries(cfg.AfterFunc), unstored: unstoredWrites[store.EpicReport]{},
		wakes: newHousekeepWake(cfg.NewTimer), fullOwed: true,
	}
}

// Wake asks Run to report the ending of the epic with id, without blocking. The store's OnEpicEnded hook calls it for
// each epic it ends.
func (e *EpicReports) Wake(epic string) {
	e.wakes.Wake(epic)
}

// Run writes the reports at start, over every ended epic, and then on each wake and each retry, until ctx ends. A pass
// whose write or read the store refused arms one retry, and a clean pass arms none. Run stops each failed call's
// deadline still armed when it returns.
func (e *EpicReports) Run(ctx context.Context) {
	defer e.calls.stop()
	e.wakes.run(ctx, func(ctx context.Context, retry bool) bool {
		owed := e.pass(ctx, retry)
		if e.cfg.Passed != nil {
			e.cfg.Passed()
		}
		return owed
	})
}

// pass makes one round. A retry first writes again the reports the store refused, with no call. Then it reads the
// epics it owes a read: every ended epic without a report for its ending while the full read is owed, and the epics
// the wakes named, with the ones whose read failed on a retry. It reports whether a retry is still owed for the full
// read or a refused write.
func (e *EpicReports) pass(ctx context.Context, retry bool) bool {
	if retry {
		if n := e.unstored.retry(context.WithoutCancel(ctx), e.cfg.Store.SetEpicReport); n > 0 {
			e.cfg.Logger.InfoContext(ctx, "epic reports stored after a retry", "reports", n)
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

// fullPass reports each stored ended epic that has no report for its ending, whose ending's call is ready (see
// callRetries) and that has no report kept for a retry to write, one call at a time, in id order. A failed read is logged and leaves the full read owed.
func (e *EpicReports) fullPass(ctx context.Context) {
	rows, err := e.cfg.Store.UnreportedEpics(ctx)
	if err != nil {
		e.cfg.Logger.ErrorContext(ctx, "read unreported epics", "error", err)
		return
	}
	e.fullOwed = false
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		ending := epicEnding(row)
		if !e.calls.ready(ending) || e.unstored.holds(row.ID) {
			continue
		}
		e.report(ctx, row.ID, ending)
	}
}

// wokenEpic reads the epic with id and reports its ending when it is listed as unreported, that ending's call is ready
// and no report of the epic is kept for a retry to write. An epic that is not listed is dropped, with any failure of its endings' calls, and a failed read is logged and
// kept for the retry.
func (e *EpicReports) wokenEpic(ctx context.Context, id string) {
	row, listed, err := e.cfg.Store.UnreportedEpic(ctx, id)
	if err != nil {
		e.cfg.Logger.ErrorContext(ctx, "read unreported epic", "epic", id, "error", err)
		e.wakes.keep(id)
		return
	}
	if !listed {
		e.calls.forgetPrefix(id + endingSeparator)
		return
	}
	ending := epicEnding(row)
	if !e.calls.ready(ending) || e.unstored.holds(row.ID) {
		return
	}
	e.report(ctx, row.ID, ending)
}

// endingSeparator separates the epic id and the phase of an ending's key.
const endingSeparator = "\x00"

// epicEnding returns the key of row's ending: its id and phase.
func epicEnding(row store.EpicRow) string {
	return row.ID + endingSeparator + row.Phase
}

// report calls Haiku for the why of the epic with id, whose ending is ending, and stores its report with the shipped and
// not-shipped ids. A failed read is logged and kept for the retry. A failed call or reply is logged, leaves the report
// empty and holds the ending's next call until its wait ends, when its deadline wakes the epic. A good reply forgets the
// ending's failures. The write ignores ctx's cancellation, so a stop that
// lands after a reply still stores it, and a write the store refuses is logged and kept for a retry to write
// again.
func (e *EpicReports) report(ctx context.Context, id, ending string) {
	flow, err := e.cfg.Store.EpicFlow(ctx, id)
	if err != nil {
		e.cfg.Logger.ErrorContext(ctx, "read epic report inputs", "epic", id, "error", err)
		e.wakes.keep(id)
		return
	}
	shipped, notShipped := requirementSplit(flow.Epic.Requirements, flow.Evidence)
	raw, err := e.cfg.Haiku.Call(
		ctx, epicReportSchema, epicReportInstruction+"\n\n"+epicReportText(flow, shipped, notShipped),
	)
	if err != nil {
		e.cfg.Logger.ErrorContext(ctx, "report epic", "epic", id, "error", err)
		e.calls.failed(ending, func() { e.Wake(id) })
		return
	}
	why, err := epicWhyOf(raw)
	if err != nil {
		e.cfg.Logger.ErrorContext(ctx, "invalid epic report", "epic", id, "error", err)
		e.calls.failed(ending, func() { e.Wake(id) })
		return
	}
	e.calls.forget(ending)
	report := store.EpicReport{
		Phase:      flow.Epic.Phase,
		Shipped:    requirementIDs(shipped),
		NotShipped: requirementIDs(notShipped),
		Why:        why,
	}
	if err = e.cfg.Store.SetEpicReport(context.WithoutCancel(ctx), id, report); err != nil {
		e.cfg.Logger.ErrorContext(ctx, "store epic report; kept for a retry", "epic", id, "error", err)
		e.unstored[id] = report
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
// that did not with their texts, the gaps of its ending, and the findings closed by decision when there are any.
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
	if decided := kinds.Decided(flow.Review); len(decided) > 0 {
		out.WriteString("Closed by decision:\n")
		for _, finding := range decided {
			fmt.Fprintf(&out, "- %s\n", finding)
		}
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
