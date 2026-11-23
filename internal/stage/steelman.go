package stage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/enginehost"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// The openings of a steelman pass's prompt, after the steelman contract: the discovery pass's and the closure pass's.
const (
	discoveryOpening = "Pass: discovery\n\n"
	closureOpening   = "Pass: closure\n\n"
)

// passes is what a design's steps and closure skip show of its steelman passes: whether a discovery or a closure step
// is still running, whether a discovery or a closure pass counts, whether a closure step exists, and whether the
// closure was recorded as skipped.
type passes struct {
	running, discoveryCounted, closureCounted, closureStep, skipped bool
}

// Steelman starts a steelman pass on the design, or records its closure as skipped, and answers with the step it
// started and whether the pass counted. The design's budget is one counted discovery pass and at most one counted
// closure pass, and a closure may be recorded skipped in its place. No pass starts while a discovery or closure step
// runs, and once the budget is spent the design is refused. With no counted discovery, the pass is a discovery pass on
// the design's packet. With a counted discovery and no closure, the pass is a closure pass on the packet, the frozen
// ledger and the delta, which is required, unless skip_closure records the closure skipped with its reason.
func (s *Service) Steelman(
	ctx context.Context,
	req *connect.Request[v1.SteelmanRequest],
) (*connect.Response[v1.SteelmanResponse], error) {
	design := req.Msg.GetDesign()
	record, err := s.readDesign(ctx, design)
	if err != nil {
		return nil, err
	}
	state, err := s.passesOf(ctx, design)
	if err != nil {
		return nil, internalError(err)
	}
	if state.running {
		return nil, refuse(connect.CodeFailedPrecondition, "steelman: a discovery or closure step is still running")
	}
	switch {
	case state.closureCounted || state.skipped:
		return nil, refuse(connect.CodeFailedPrecondition, "steelman: budget spent")
	case req.Msg.GetSkipClosure():
		return s.skipClosure(ctx, req, state)
	case !state.discoveryCounted:
		return s.startDiscovery(ctx, req, record)
	}
	return s.startClosure(ctx, req, record)
}

// passesOf returns what the steps and the closure skip of the design show of its steelman passes. A kept return that
// does not decode is an error.
func (s *Service) passesOf(ctx context.Context, design string) (passes, error) {
	rows, err := s.cfg.Store.DesignSteps(ctx, design)
	if err != nil {
		return passes{}, fmt.Errorf("list steps of design %s: %w", design, err)
	}
	var state passes
	for _, row := range rows {
		if row.Kind != store.StepDiscovery && row.Kind != store.StepClosure {
			continue
		}
		step, readErr := s.cfg.Store.Step(ctx, row.Step)
		if readErr != nil {
			return passes{}, fmt.Errorf("read step %s of design %s: %w", row.Step, design, readErr)
		}
		if step.End == "" {
			state.running = true
		}
		_, counted, passErr := passOf(step)
		if passErr != nil {
			return passes{}, fmt.Errorf("decode return of %s step %s: %w", row.Kind, row.Step, passErr)
		}
		if row.Kind == store.StepDiscovery {
			state.discoveryCounted = state.discoveryCounted || counted
			continue
		}
		state.closureStep = true
		state.closureCounted = state.closureCounted || counted
	}
	skipped, err := s.closureSkipped(ctx, design)
	if err != nil {
		return passes{}, err
	}
	state.skipped = skipped
	return state, nil
}

// passOf returns the verdict of a steelman step from the return it kept, "" while it keeps none, and whether its pass
// counts. A pass counts when its step ended with a kept return whose verdict is not BLOCKED, as countedSteelman counts
// it. A kept return that does not decode is an error.
func passOf(step store.StepRow) (string, bool, error) {
	if step.Result == "" {
		return "", false, nil
	}
	ret, err := DecodeSteelman(step.Result)
	if err != nil {
		return "", false, err
	}
	return ret.Verdict, step.End != "" && ret.Verdict != blockedVerdict, nil
}

// closureSkipped reports whether the design's closure was recorded as skipped.
func (s *Service) closureSkipped(ctx context.Context, design string) (bool, error) {
	_, err := s.cfg.Store.ClosureSkip(ctx, design)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, store.ErrNoClosureSkip):
		return false, nil
	}
	return false, fmt.Errorf("read closure skip of design %s: %w", design, err)
}

// startDiscovery starts the discovery pass on the design's packet, under the steelman role's agent, in the repository
// of the packet's first contract or draft.
func (s *Service) startDiscovery(
	ctx context.Context, req *connect.Request[v1.SteelmanRequest], record store.DesignRecord,
) (*connect.Response[v1.SteelmanResponse], error) {
	packet, repository, err := s.designPacket(ctx, record)
	if err != nil {
		return nil, err
	}
	start, err := SteelmanStart(s.cfg.Getenv, repository, discoveryOpening+packet)
	if err != nil {
		return nil, errors.Join(connect.NewError(connect.CodeFailedPrecondition, err))
	}
	start.Caller = callerOf(req.Header())
	return s.answerPass(ctx, start, store.DesignStep{
		Design: record.ID, Kind: store.StepDiscovery, Subject: record.ID, Repository: repository,
	})
}

// startClosure starts the closure pass on the design's packet, its frozen ledger and the delta, which is required,
// under the steelman role's agent, in the repository of the packet's first contract or draft.
func (s *Service) startClosure(
	ctx context.Context, req *connect.Request[v1.SteelmanRequest], record store.DesignRecord,
) (*connect.Response[v1.SteelmanResponse], error) {
	delta := req.Msg.GetDelta()
	if err := requireField("delta", delta); err != nil {
		return nil, err
	}
	packet, repository, err := s.designPacket(ctx, record)
	if err != nil {
		return nil, err
	}
	ledger, _, err := s.ledgerOf(ctx, record.ID)
	if err != nil {
		return nil, internalError(err)
	}
	start, err := SteelmanStart(s.cfg.Getenv, repository, closurePrompt(packet, frozenLedgerText(ledger), delta))
	if err != nil {
		return nil, errors.Join(connect.NewError(connect.CodeFailedPrecondition, err))
	}
	start.Caller = callerOf(req.Header())
	return s.answerPass(ctx, start, store.DesignStep{
		Design: record.ID, Kind: store.StepClosure, Subject: record.ID, Repository: repository, Delta: delta,
	})
}

// skipClosure records the design's closure as skipped, with its reason and the maker it came from. It needs a counted
// discovery and no closure step. It answers with no step, since no pass starts.
func (s *Service) skipClosure(
	ctx context.Context, req *connect.Request[v1.SteelmanRequest], state passes,
) (*connect.Response[v1.SteelmanResponse], error) {
	design := req.Msg.GetDesign()
	if !state.discoveryCounted {
		return nil, refuse(connect.CodeFailedPrecondition,
			"steelman: closure is skipped only after a counted discovery")
	}
	if state.closureStep {
		return nil, refuse(connect.CodeFailedPrecondition, "steelman: closure is skipped only before a closure pass")
	}
	if err := requireField("reason", req.Msg.GetReason()); err != nil {
		return nil, err
	}
	maker, session, err := s.checkMaker(ctx, req.Header(), req.Msg.GetMaker())
	if err != nil {
		return nil, err
	}
	if err = s.cfg.Store.RecordClosureSkip(ctx, store.ClosureSkip{
		Design: design, Reason: req.Msg.GetReason(), Maker: maker, Session: session, At: s.cfg.Now(),
	}); err != nil {
		return nil, internalError(fmt.Errorf("record closure skip of design %s: %w", design, err))
	}
	if err = s.logCall(ctx, design, loggedCall{
		Maker: maker, Session: session, What: "closure skipped", Words: req.Msg.GetReason(),
	}); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.SteelmanResponse{}), nil
}

// answerPass starts the pass start describes, stores its step on the design as row, logs the start on the design and
// answers with the step and whether the pass counts. A pass just started has not returned, so it does not count yet.
// The start's words are the row's delta, which a discovery pass does not take.
func (s *Service) answerPass(
	ctx context.Context, start enginehost.StepStart, row store.DesignStep,
) (*connect.Response[v1.SteelmanResponse], error) {
	step, err := s.startStep(ctx, start, row)
	if err != nil {
		return nil, err
	}
	session, err := s.callerSession(ctx, start.Caller)
	if err != nil {
		return nil, err
	}
	if err = s.logCall(ctx, row.Design, loggedCall{
		Session: session, What: "steelman started", IDs: []string{step.GetStep()}, Words: row.Delta,
	}); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.SteelmanResponse{Step: step, Pass: step.GetCounted()}), nil
}

// closurePrompt returns the prompt of a closure pass: the packet, the frozen ledger as frozenLedgerText writes it, and
// the delta.
func closurePrompt(packet, ledger, delta string) string {
	return closureOpening + packet + "\n## The frozen ledger\n" + ledger + "\n## The delta\n" + delta + "\n"
}

// frozenLedgerText returns the frozen ledger as a markdown table of each item's id, contract, impact, disposition,
// reason and boundary, or "None." when it holds no item. Each cell's newlines become spaces and its pipes are escaped,
// so a cell stays one cell.
func frozenLedgerText(ledger []ledgerItem) string {
	if len(ledger) == 0 {
		return "None.\n"
	}
	cellOf := strings.NewReplacer("\r\n", " ", "\n", " ", "|", `\|`).Replace
	var doc strings.Builder
	doc.WriteString("| Item | Contract | Impact | Disposition | Reason | Boundary |\n|---|---|---|---|---|---|\n")
	for _, item := range ledger {
		cells := []string{item.ID, item.Contract, item.Impact, item.Disposition, item.Reason, item.Boundary}
		for i, cell := range cells {
			cells[i] = cellOf(cell)
		}
		fmt.Fprintf(&doc, "| %s |\n", strings.Join(cells, " | "))
	}
	return doc.String()
}
