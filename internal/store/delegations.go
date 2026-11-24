package store

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The kinds of a decision made under a delegation, the Kind of each DelegatedDecision that the conversation made. A
// decision of a kept row write or a contract accept takes the row write's action, RowActionKeep or RowActionStrike, as
// its kind, and DecisionAccept for an accept.
const (
	DecisionAnswer      = "answer"
	DecisionDisposition = "disposition"
	DecisionClosureSkip = "closure_skip"
	DecisionChoice      = "choice"
	DecisionAccept      = "accept"
)

// DelegatedDecision is a decision the conversation made under a delegation. Kind is one of the Decision constants or a
// row write's action, and Ref is the id of what the decision concerns: the question's Q<n>, the ledger item's id, the
// approach's seq, the row, or the contract. Text is the decision's words, and Reason is the maker_reason it was made
// with. At is unix seconds.
type DelegatedDecision struct {
	Kind, Ref, Text, Reason string
	At                      int64
}

// DelegationWithDecisions is a delegation of a design's decisions with the decisions the conversation made under it,
// oldest first.
type DelegationWithDecisions struct {
	DesignDelegation
	Decisions []DelegatedDecision
}

// Delegate stores delegation on its design, numbered as the design's next delegation: counted from 1 within the design,
// one more than the highest number the design already holds. delegation.Seq is ignored. Its kinds are stored as a JSON
// array, and its message is stored whole. Rows only append, so a delegation once stored is never changed. It returns
// the delegation as stored, with its Seq.
func (s *Store) Delegate(ctx context.Context, delegation DesignDelegation) (DesignDelegation, error) {
	kinds, err := encodeList(delegation.Kinds)
	if err != nil {
		return DesignDelegation{}, fmt.Errorf("encode kinds of a delegation of design %s: %w", delegation.Design, err)
	}
	var seq int64
	err = s.write(ctx, func(queries *sqlcgen.Queries) error {
		rows, listErr := queries.ListDesignDelegations(ctx, delegation.Design)
		if listErr != nil {
			return fmt.Errorf("number a delegation of design %s: %w", delegation.Design, listErr)
		}
		seq = nextDelegationSeq(rows)
		if insertErr := queries.InsertDesignDelegation(ctx, sqlcgen.InsertDesignDelegationParams{
			Design: delegation.Design, Seq: seq, Kinds: kinds, Message: delegation.Message,
			Session: delegation.Session, At: delegation.At,
		}); insertErr != nil {
			return fmt.Errorf("insert delegation %d of design %s: %w", seq, delegation.Design, insertErr)
		}
		return nil
	})
	if err != nil {
		return DesignDelegation{}, err
	}
	delegation.Seq = seq
	return delegation, nil
}

// DesignDelegations returns the delegations stored against the design, oldest first, which is by seq, each with the
// decisions the conversation made under it. A design with no delegations has none.
func (s *Store) DesignDelegations(ctx context.Context, design string) ([]DelegationWithDecisions, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin delegations read: %w", err)
	}
	delegations, err := readDesignDelegations(ctx, sqlcgen.New(tx), design)
	if err != nil {
		return nil, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end delegations read: %w", err)
	}
	return delegations, nil
}

// DesignDelegations returns the delegations of the design as Store.DesignDelegations does, read through the Reader.
func (r *Reader) DesignDelegations(ctx context.Context, design string) ([]DelegationWithDecisions, error) {
	var delegations []DelegationWithDecisions
	err := r.readEngine(ctx, func(queries *sqlcgen.Queries) error {
		var readErr error
		delegations, readErr = readDesignDelegations(ctx, queries, design)
		return readErr
	})
	return delegations, err
}

// readDesignDelegations returns the delegations of the design, oldest first, each with the decisions made under it,
// read with queries. A decision belongs to the delegation whose time it is at or after and before the next
// delegation's time; the newest delegation takes every decision from its time on. Decisions are read only when the
// design has a delegation.
func readDesignDelegations(
	ctx context.Context, queries *sqlcgen.Queries, design string,
) ([]DelegationWithDecisions, error) {
	rows, err := queries.ListDesignDelegations(ctx, design)
	if err != nil {
		return nil, fmt.Errorf("list delegations of design %s: %w", design, err)
	}
	delegations := make([]DelegationWithDecisions, 0, len(rows))
	if len(rows) == 0 {
		return delegations, nil
	}
	decisions, err := designDecisions(ctx, queries, design)
	if err != nil {
		return nil, err
	}
	for i, row := range rows {
		kinds, decodeErr := decodeList[string](row.Kinds)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode kinds of delegation %d of design %s: %w", row.Seq, design, decodeErr)
		}
		delegation := DelegationWithDecisions{DesignDelegation: DesignDelegation{
			Design: row.Design, Seq: row.Seq, Kinds: kinds, Message: row.Message, Session: row.Session, At: row.At,
		}}
		for _, decision := range decisions {
			if decision.At < row.At || (i+1 < len(rows) && decision.At >= rows[i+1].At) {
				continue
			}
			delegation.Decisions = append(delegation.Decisions, decision)
		}
		delegations = append(delegations, delegation)
	}
	return delegations, nil
}

// designDecisions returns the records the conversation made on the design, in time order: the answers to its
// questions, its ledger dispositions, its closure skip, its approach choices, the rows its contracts keep or strike,
// and the contracts it accepted. A record whose maker is not the conversation is left out.
func designDecisions(ctx context.Context, queries *sqlcgen.Queries, design string) ([]DelegatedDecision, error) {
	sources := []func(context.Context, *sqlcgen.Queries, string) ([]DelegatedDecision, error){
		questionDecisions, dispositionDecisions, closureSkipDecisions, choiceDecisions, contractsDecisions,
	}
	var decisions []DelegatedDecision
	for _, source := range sources {
		found, err := source(ctx, queries, design)
		if err != nil {
			return nil, err
		}
		decisions = append(decisions, found...)
	}
	slices.SortStableFunc(decisions, func(a, b DelegatedDecision) int { return cmp.Compare(a.At, b.At) })
	return decisions, nil
}

// questionDecisions returns the answers the conversation gave to the design's questions, in seq order.
func questionDecisions(ctx context.Context, queries *sqlcgen.Queries, design string) ([]DelegatedDecision, error) {
	questions, err := queries.ListDesignQuestions(ctx, design)
	if err != nil {
		return nil, fmt.Errorf("list questions of design %s: %w", design, err)
	}
	var decisions []DelegatedDecision
	for _, row := range questions {
		if row.Answered == 0 || row.Maker != StageMakerConversation {
			continue
		}
		decisions = append(decisions, DelegatedDecision{
			Kind: DecisionAnswer, Ref: fmt.Sprintf("Q%d", row.Seq), Text: row.Answer, Reason: row.MakerReason,
			At: row.Answered,
		})
	}
	return decisions, nil
}

// dispositionDecisions returns the dispositions the conversation gave to the design's ledger items, in seq order.
func dispositionDecisions(ctx context.Context, queries *sqlcgen.Queries, design string) ([]DelegatedDecision, error) {
	dispositions, err := queries.ListDesignDispositions(ctx, design)
	if err != nil {
		return nil, fmt.Errorf("list dispositions of design %s: %w", design, err)
	}
	var decisions []DelegatedDecision
	for _, row := range dispositions {
		if row.Maker != StageMakerConversation {
			continue
		}
		decisions = append(decisions, DelegatedDecision{
			Kind: DecisionDisposition, Ref: row.Item, Text: row.Disposition, Reason: row.MakerReason, At: row.At,
		})
	}
	return decisions, nil
}

// closureSkipDecisions returns the design's closure skip as a decision, when the conversation recorded it.
func closureSkipDecisions(ctx context.Context, queries *sqlcgen.Queries, design string) ([]DelegatedDecision, error) {
	skip, err := queries.GetDesignClosureSkip(ctx, design)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read closure skip of design %s: %w", design, err)
	}
	if skip.Maker != StageMakerConversation {
		return nil, nil
	}
	return []DelegatedDecision{{
		Kind: DecisionClosureSkip, Text: skip.Reason, Reason: skip.MakerReason, At: skip.At,
	}}, nil
}

// choiceDecisions returns the approach choices the conversation made for the design, in seq order.
func choiceDecisions(ctx context.Context, queries *sqlcgen.Queries, design string) ([]DelegatedDecision, error) {
	choices, err := queries.ListDesignApproachChoices(ctx, design)
	if err != nil {
		return nil, fmt.Errorf("list choices of design %s: %w", design, err)
	}
	var decisions []DelegatedDecision
	for _, row := range choices {
		if row.Maker != StageMakerConversation {
			continue
		}
		decisions = append(decisions, DelegatedDecision{
			Kind: DecisionChoice, Ref: strconv.FormatInt(row.Approach, 10), Text: row.Words, Reason: row.MakerReason,
			At: row.At,
		})
	}
	return decisions, nil
}

// contractsDecisions returns the keeps, strikes and accepts the conversation made on the design's contracts, each
// contract in position order. A design no record names has no contracts, as readDesign gives them.
func contractsDecisions(ctx context.Context, queries *sqlcgen.Queries, design string) ([]DelegatedDecision, error) {
	record, err := readDesign(ctx, queries, design)
	if err != nil && !errors.Is(err, ErrNoDesign) {
		return nil, err
	}
	var decisions []DelegatedDecision
	for _, contract := range record.Contracts {
		found, contractErr := contractDecisionsOf(ctx, queries, contract.ID)
		if contractErr != nil {
			return nil, contractErr
		}
		decisions = append(decisions, found...)
	}
	return decisions, nil
}

// contractDecisionsOf returns the conversation's keeps and strikes of the rows of contract, in seq order, and its
// accept by the conversation, if it has one, read with queries.
func contractDecisionsOf(ctx context.Context, queries *sqlcgen.Queries, contract string) ([]DelegatedDecision, error) {
	var decisions []DelegatedDecision
	writes, err := queries.ListContractRowWrites(ctx, contract)
	if err != nil {
		return nil, fmt.Errorf("list row writes of contract %s: %w", contract, err)
	}
	for _, row := range writes {
		if row.Maker != StageMakerConversation || (row.Action != RowActionKeep && row.Action != RowActionStrike) {
			continue
		}
		decisions = append(decisions, DelegatedDecision{
			Kind: row.Action, Ref: row.Row, Text: row.Words, Reason: row.MakerReason, At: row.At,
		})
	}
	states, err := queries.ListContractStates(ctx, contract)
	if err != nil {
		return nil, fmt.Errorf("list states of contract %s: %w", contract, err)
	}
	for _, row := range states {
		if row.Maker != StageMakerConversation || row.State != ContractAccepted {
			continue
		}
		decisions = append(decisions, DelegatedDecision{
			Kind: DecisionAccept, Ref: contract, Text: row.Words, Reason: row.MakerReason, At: row.At,
		})
	}
	return decisions, nil
}

// nextDelegationSeq returns the seq of the next delegation after rows: one more than the highest seq rows hold, or 1
// for none.
func nextDelegationSeq(rows []sqlcgen.DesignDelegation) int64 {
	next := int64(1)
	for _, row := range rows {
		if row.Seq >= next {
			next = row.Seq + 1
		}
	}
	return next
}
