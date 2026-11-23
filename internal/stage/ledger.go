package stage

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// The origins of a ledger item: a finding of the counted discovery return, or a concern of the counted closure return.
const (
	originDiscovery = "discovery"
	originClosure   = "closure"
)

// blockedVerdict is the verdict of a steelman return that did not run, which does not count toward a design's budget.
const blockedVerdict = "BLOCKED"

// beforeClosureDispositions returns the dispositions an item takes before the ledger freezes, and
// afterClosureDispositions the ones it takes after.
func beforeClosureDispositions() []string {
	return []string{
		store.DispositionAdopted, store.DispositionRejected, store.DispositionDeferred, store.DispositionOpen,
	}
}

func afterClosureDispositions() []string {
	return []string{store.DispositionRevisedAfterClosure, store.DispositionAcceptedAsRisk}
}

// ledgerItem is one item of a design's ledger. Its disposition is open until a disposition is stored for it, and the
// Reason, Boundary and Maker are those of the newest stored disposition. Impact holds a discovery finding's impact or a
// closure concern's text. ClosureWord and ClosureNote are closure's word and note on the item, empty when closure gave
// none.
type ledgerItem struct {
	ID, Contract, Origin          string
	Impact, Evidence, Change      string
	Disposition, Reason, Boundary string
	Maker                         store.StageMaker
	ClosureWord, ClosureNote      string
}

// Dispose stores a disposition of a ledger item of a design, with the maker it came from, and returns the item with
// its new disposition. The design must exist, the item must be an item of its ledger, and the maker must pass
// checkMaker, in that order. Until the ledger freezes, only adopted, rejected (with a reason), deferred (with a
// boundary) and open are taken. Once it freezes, only revised_after_closure and accepted_as_risk are taken, each with a
// reason and only for an item whose disposition is open. Any other disposition is a failed precondition that names the
// rule.
func (s *Service) Dispose(
	ctx context.Context,
	req *connect.Request[v1.DisposeRequest],
) (*connect.Response[v1.DisposeResponse], error) {
	design, itemID, disposition := req.Msg.GetDesign(), req.Msg.GetItem(), req.Msg.GetDisposition()
	reason, boundary := req.Msg.GetReason(), req.Msg.GetBoundary()
	if _, err := s.readDesign(ctx, design); err != nil {
		return nil, err
	}
	ledger, frozen, err := s.ledgerOf(ctx, design)
	if err != nil {
		return nil, internalError(err)
	}
	item, found := findLedgerItem(ledger, itemID)
	if !found {
		return nil, refuse(connect.CodeNotFound, fmt.Sprintf("item: %s is not an item of design %s", itemID, design))
	}
	maker, session, err := s.checkMaker(ctx, req.Header(), req.Msg.GetMaker())
	if err != nil {
		return nil, err
	}
	err = checkDisposition(item, frozen, disposition, reason, boundary)
	if err != nil {
		return nil, err
	}
	stored := store.DesignDisposition{
		Design: design, Item: itemID, Disposition: disposition, Reason: reason, Boundary: boundary, Maker: maker,
		Session: session, At: s.cfg.Now(),
	}
	if err = s.cfg.Store.AddDesignDisposition(ctx, stored); err != nil {
		return nil, internalError(fmt.Errorf("store disposition of item %s of design %s: %w", itemID, design, err))
	}
	// A deferred item takes a boundary and no reason, so its words are the boundary.
	words := reason
	if words == "" {
		words = boundary
	}
	if err = s.logCall(ctx, design, loggedCall{
		Maker: maker, Session: session, What: "disposed", IDs: []string{itemID}, Words: words,
	}); err != nil {
		return nil, err
	}
	item.Disposition, item.Reason, item.Boundary, item.Maker = disposition, reason, boundary, maker
	return connect.NewResponse(&v1.DisposeResponse{Item: stageLedgerItemOf(item)}), nil
}

// checkDisposition returns the refusal of disposing item with disposition, given whether the ledger is frozen. Before
// the freeze, a rejected item needs a reason and a deferred item a boundary. After it, the disposition must be one of
// afterClosureDispositions, the item must be open, and the reason is required. A refusal of the rule is a failed
// precondition, and a missing field an invalid argument.
func checkDisposition(item ledgerItem, frozen bool, disposition, reason, boundary string) error {
	if err := requireField("disposition", disposition); err != nil {
		return err
	}
	if frozen {
		if !slices.Contains(afterClosureDispositions(), disposition) {
			return refuse(connect.CodeFailedPrecondition, fmt.Sprintf(
				"disposition: %s is refused after closure; after closure only %s and %s are taken",
				disposition, store.DispositionRevisedAfterClosure, store.DispositionAcceptedAsRisk))
		}
		if item.Disposition != store.DispositionOpen {
			return refuse(connect.CodeFailedPrecondition, fmt.Sprintf(
				"item: %s is %s, and after closure only an open item takes a disposition",
				item.ID, item.Disposition))
		}
		return requireField("reason", reason)
	}
	if !slices.Contains(beforeClosureDispositions(), disposition) {
		return refuse(connect.CodeFailedPrecondition, fmt.Sprintf(
			"disposition: %s is refused before closure; before closure only %s, %s, %s and %s are taken",
			disposition, store.DispositionAdopted, store.DispositionRejected, store.DispositionDeferred,
			store.DispositionOpen))
	}
	switch disposition {
	case store.DispositionRejected:
		return requireField("reason", reason)
	case store.DispositionDeferred:
		return requireField("boundary", boundary)
	}
	return nil
}

// ledgerOf returns the ledger of the design and whether it is frozen, read from the design's steps as readLedger reads
// them.
func (s *Service) ledgerOf(ctx context.Context, design string) ([]ledgerItem, bool, error) {
	steps, err := s.cfg.Store.DesignSteps(ctx, design)
	if err != nil {
		return nil, false, fmt.Errorf("list steps of design %s: %w", design, err)
	}
	return s.readLedger(ctx, design, steps)
}

// readLedger returns the ledger of the design whose steps are steps, and whether the ledger is frozen. The items are
// the findings of the counted discovery return, oldest first, then the concerns of the counted closure return, each
// with origin closure. A pass counts when its step ended with a kept return whose verdict is not BLOCKED, and the
// first such pass of each kind is the one counted, since the budget admits one. The ledger is frozen once a closure
// step exists or a closure skip is recorded. Each item's disposition is the newest stored disposition of its id, and
// open when none is stored; closure's ledger entry with the item's id gives its closure word and note.
func (s *Service) readLedger(
	ctx context.Context, design string, steps []store.DesignStep,
) ([]ledgerItem, bool, error) {
	frozen := false
	for _, step := range steps {
		if step.Kind == store.StepClosure {
			frozen = true
		}
	}
	_, err := s.cfg.Store.ClosureSkip(ctx, design)
	if err == nil {
		frozen = true
	} else if !errors.Is(err, store.ErrNoClosureSkip) {
		return nil, false, fmt.Errorf("read closure skip of design %s: %w", design, err)
	}
	discovery, err := s.countedSteelman(ctx, steps, store.StepDiscovery)
	if err != nil {
		return nil, false, err
	}
	closure, err := s.countedSteelman(ctx, steps, store.StepClosure)
	if err != nil {
		return nil, false, err
	}
	items := make([]ledgerItem, 0, len(discovery.Findings)+len(closure.Concerns))
	for _, finding := range discovery.Findings {
		items = append(items, ledgerItem{
			ID: finding.ID, Contract: finding.Contract, Origin: originDiscovery, Impact: finding.Impact,
			Evidence: finding.Evidence, Change: finding.Change,
		})
	}
	for _, concern := range closure.Concerns {
		items = append(items, ledgerItem{
			ID: concern.ID, Contract: concern.Contract, Origin: originClosure, Impact: concern.Text,
		})
	}
	words := make(map[string]SteelmanLedgerItem, len(closure.Ledger))
	for _, entry := range closure.Ledger {
		words[entry.ID] = entry
	}
	dispositions, err := s.cfg.Store.DesignDispositions(ctx, design)
	if err != nil {
		return nil, false, fmt.Errorf("list dispositions of design %s: %w", design, err)
	}
	newest := make(map[string]store.DesignDisposition, len(dispositions))
	for _, row := range dispositions {
		newest[row.Item] = row
	}
	for i := range items {
		items[i].Disposition = store.DispositionOpen
		if entry, ok := words[items[i].ID]; ok {
			items[i].ClosureWord, items[i].ClosureNote = entry.Word, entry.Note
		}
		if row, ok := newest[items[i].ID]; ok {
			items[i].Disposition, items[i].Reason = row.Disposition, row.Reason
			items[i].Boundary, items[i].Maker = row.Boundary, row.Maker
		}
	}
	return items, frozen, nil
}

// countedSteelman returns the decoded return of the first step of kind among steps that counts: its step ended with a
// kept return whose verdict is not BLOCKED. It returns the zero return, and no error, when no step counts. A kept return
// that does not decode is an error.
func (s *Service) countedSteelman(ctx context.Context, steps []store.DesignStep, kind string) (SteelmanReturn, error) {
	for _, step := range steps {
		if step.Kind != kind {
			continue
		}
		row, err := s.cfg.Store.Step(ctx, step.Step)
		if err != nil {
			return SteelmanReturn{}, fmt.Errorf("read step %s of design %s: %w", step.Step, step.Design, err)
		}
		if row.End == "" || row.Result == "" {
			continue
		}
		ret, err := DecodeSteelman(row.Result)
		if err != nil {
			return SteelmanReturn{}, fmt.Errorf("decode return of %s step %s: %w", kind, step.Step, err)
		}
		if ret.Verdict == blockedVerdict {
			continue
		}
		return ret, nil
	}
	return SteelmanReturn{}, nil
}

// findLedgerItem returns the item of ledger with the id, and whether there is one.
func findLedgerItem(ledger []ledgerItem, id string) (ledgerItem, bool) {
	for _, item := range ledger {
		if item.ID == id {
			return item, true
		}
	}
	return ledgerItem{}, false
}

// stageLedgerItemOf returns the StageLedgerItem of item. Its maker is set once a disposition is stored for it.
func stageLedgerItemOf(item ledgerItem) *v1.StageLedgerItem {
	message := &v1.StageLedgerItem{
		Id: item.ID, Contract: item.Contract, Origin: item.Origin, Impact: item.Impact, Evidence: item.Evidence,
		Change: item.Change, Disposition: item.Disposition, Reason: item.Reason, Boundary: item.Boundary,
		ClosureWord: item.ClosureWord, ClosureNote: item.ClosureNote,
	}
	if item.Maker.Kind != "" {
		message.Maker = &v1.StageMaker{
			Kind: item.Maker.Kind, Quote: item.Maker.Quote, Reason: item.Maker.Reason,
			Delegation: item.Maker.Delegation,
		}
	}
	return message
}
