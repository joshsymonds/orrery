package store

import (
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// delegationsDesign is the design the delegation tests record on, delegationsContract is the contract it proposes and
// accepts, and delegationsSession is the conversation that makes the records.
const (
	delegationsDesign   = "design-delegations"
	delegationsContract = "synthetic-repo/contract-delegated"
	delegationsSession  = "session-delegations"
)

// delegationsStart is the unix seconds the delegation tests stamp their first record at. Each test gives its records
// times after it, which the tests set.
const delegationsStart = designLogStart

// seedDelegationsDesign creates delegationsDesign in s, and proposes delegationsContract into it at delegationsStart.
func seedDelegationsDesign(t *testing.T, s *Store) {
	t.Helper()
	ctx := t.Context()
	if err := s.CreateDesign(ctx, Design{
		ID: delegationsDesign, Title: "Design delegations", Conversation: delegationsSession,
		Created: time.Unix(delegationsStart, 0),
	}); err != nil {
		t.Fatalf("CreateDesign: %v", err)
	}
	proposal := ContractVersion{
		Repository: "/work/synthetic-repo", Title: "Delegated contract", Care: "serious",
		Body: `{"id":"synthetic-delegated"}`, Words: "Propose the synthetic contract", Writer: delegationsSession,
		At: delegationsStart,
	}
	if err := s.ProposeContractInDesign(ctx, delegationsDesign, delegationsContract, proposal); err != nil {
		t.Fatalf("ProposeContractInDesign: %v", err)
	}
}

// conversationMakerOf returns the maker of a record the conversation made for the owner, with reason as its reason.
func conversationMakerOf(reason string) StageMaker {
	return StageMaker{Kind: StageMakerConversation, Reason: reason, Delegation: "Decide it yourself."}
}

// askDelegationsQuestion asks a question of delegationsDesign at at, and returns its seq.
func askDelegationsQuestion(t *testing.T, s *Store, at int64) int64 {
	t.Helper()
	question, err := s.AskDesignQuestion(t.Context(), DesignQuestion{
		Design: delegationsDesign, Text: "Keep the scout?", Options: []string{"Keep", "Drop"},
		Recommendation: "Keep", Asker: delegationsSession, Asked: time.Unix(at, 0),
	})
	if err != nil {
		t.Fatalf("AskDesignQuestion: %v", err)
	}
	return question.Seq
}

// answerDelegationsQuestion answers question seq of delegationsDesign with words, by maker, at at.
func answerDelegationsQuestion(t *testing.T, s *Store, seq int64, words string, maker StageMaker, at int64) {
	t.Helper()
	if _, err := s.AnswerDesignQuestion(t.Context(), DesignQuestion{
		Design: delegationsDesign, Seq: seq, Answer: words, Maker: maker, Answerer: delegationsSession,
		Answered: time.Unix(at, 0),
	}); err != nil {
		t.Fatalf("AnswerDesignQuestion %d: %v", seq, err)
	}
}

// delegateDesign stores a delegation of delegationsDesign with kinds and message at at, and returns it as stored.
func delegateDesign(t *testing.T, s *Store, kinds []string, message string, at int64) DesignDelegation {
	t.Helper()
	delegation, err := s.Delegate(t.Context(), DesignDelegation{
		Design: delegationsDesign, Kinds: kinds, Message: message, Session: delegationsSession, At: at,
	})
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	return delegation
}

// TestRecordsDelegationStoresKindsAndMessageWhole checks that Delegate numbers each design's delegations from 1, stores
// their kinds and their message whole, and that Store and Reader read each back in seq order with no decisions made
// under it.
func TestRecordsDelegationStoresKindsAndMessageWhole(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, path := openDesignLogStore(t)
	const message = "  Decide the scout yourself.\n\nKeep my words as I wrote them.  "
	first := delegateDesign(t, s, []string{DecisionAnswer, DecisionDisposition}, message, delegationsStart)
	second := delegateDesign(t, s, []string{DecisionChoice}, "Pick the approach.", delegationsStart+100)
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("delegation seqs = %d and %d, want 1 and 2", first.Seq, second.Seq)
	}

	want := []DelegationWithDecisions{{DesignDelegation: first}, {DesignDelegation: second}}
	got, err := s.DesignDelegations(ctx, delegationsDesign)
	if err != nil {
		t.Fatalf("DesignDelegations: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DesignDelegations = %+v, want %+v", got, want)
	}
	read, err := NewReader(path).DesignDelegations(ctx, delegationsDesign)
	if err != nil {
		t.Fatalf("Reader.DesignDelegations: %v", err)
	}
	if !reflect.DeepEqual(read, want) {
		t.Fatalf("Reader.DesignDelegations = %+v, want %+v", read, want)
	}
	none, err := s.DesignDelegations(ctx, "design-none")
	if err != nil || len(none) != 0 {
		t.Fatalf("DesignDelegations of a design with none = %+v, %v, want none", none, err)
	}
}

// TestRecordsDelegationListsEveryKindOfDecisionMadeUnderIt checks that each delegation lists, in time order, every
// record the conversation made under it, of each kind, with its reason, and that a record made at the next
// delegation's time is listed under that delegation and not this one.
func TestRecordsDelegationListsEveryKindOfDecisionMadeUnderIt(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, path := openDesignLogStore(t)
	seedDelegationsDesign(t, s)
	kinds := []string{DecisionAnswer, DecisionDisposition, DecisionClosureSkip, DecisionChoice, RowActionKeep,
		RowActionStrike, DecisionAccept}
	first := delegateDesign(t, s, kinds, "Decide the rest.", delegationsStart)

	seq := askDelegationsQuestion(t, s, delegationsStart)
	answerDelegationsQuestion(t, s, seq, "Keep", conversationMakerOf("Keep the scout."), delegationsStart+10)
	if err := s.AddDesignDisposition(ctx, DesignDisposition{
		Design: delegationsDesign, Item: "S1", Disposition: DispositionAdopted, Maker: conversationMakerOf("Adopt it."),
		Session: delegationsSession, At: time.Unix(delegationsStart+20, 0),
	}); err != nil {
		t.Fatalf("AddDesignDisposition: %v", err)
	}
	if err := s.RecordClosureSkip(ctx, ClosureSkip{
		Design: delegationsDesign, Reason: "Nothing left to check.", Maker: conversationMakerOf("Skip it."),
		Session: delegationsSession, At: time.Unix(delegationsStart+30, 0),
	}); err != nil {
		t.Fatalf("RecordClosureSkip: %v", err)
	}
	if _, err := s.ChooseApproach(ctx, ApproachChoice{
		Design: delegationsDesign, Approach: 1, Words: "Keep it.", Maker: StageMakerConversation,
		MakerReason: "Choose it.", Delegation: "Pick it.", Session: delegationsSession, At: delegationsStart + 40,
	}); err != nil {
		t.Fatalf("ChooseApproach: %v", err)
	}
	q := s.fixtureQueries()
	for _, write := range []sqlcgen.InsertContractRowWriteParams{
		{
			Contract: delegationsContract, Seq: 1, Version: 1, Row: "R1", Action: RowActionKeep, Depends: "R0",
			Words: "Keep R1", Maker: StageMakerConversation, MakerReason: "Keep the row.",
			Delegation: "Decide it.", Session: delegationsSession, At: delegationsStart + 50,
		},
		{
			Contract: delegationsContract, Seq: 2, Version: 1, Row: "R2", Action: RowActionStrike, Depends: "R0",
			Words: "Strike R2", Maker: StageMakerConversation, MakerReason: "Strike the row.",
			Delegation: "Decide it.", Session: delegationsSession, At: delegationsStart + 60,
		},
	} {
		if err := q.InsertContractRowWrite(ctx, write); err != nil {
			t.Fatalf("InsertContractRowWrite %d: %v", write.Seq, err)
		}
	}
	if err := s.AcceptContract(ctx, delegationsContract, ContractChange{
		Session: delegationsSession, Words: "Accept the contract", At: delegationsStart + 70,
		Maker: conversationMakerOf("Accept it."),
	}); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}

	second := delegateDesign(t, s, []string{DecisionAnswer}, "Decide the next.", delegationsStart+100)
	nextSeq := askDelegationsQuestion(t, s, delegationsStart+100)
	answerDelegationsQuestion(t, s, nextSeq, "Drop", conversationMakerOf("Drop it."), delegationsStart+100)

	wantFirst := []DelegatedDecision{
		{Kind: DecisionAnswer, Ref: "Q1", Text: "Keep", Reason: "Keep the scout.", At: delegationsStart + 10},
		{
			Kind:   DecisionDisposition,
			Ref:    "S1",
			Text:   DispositionAdopted,
			Reason: "Adopt it.",
			At:     delegationsStart + 20,
		},
		{Kind: DecisionClosureSkip, Text: "Nothing left to check.", Reason: "Skip it.", At: delegationsStart + 30},
		{Kind: DecisionChoice, Ref: "1", Text: "Keep it.", Reason: "Choose it.", At: delegationsStart + 40},
		{Kind: RowActionKeep, Ref: "R1", Text: "Keep R1", Reason: "Keep the row.", At: delegationsStart + 50},
		{Kind: RowActionStrike, Ref: "R2", Text: "Strike R2", Reason: "Strike the row.", At: delegationsStart + 60},
		{Kind: DecisionAccept, Ref: delegationsContract, Text: "Accept the contract", Reason: "Accept it.",
			At: delegationsStart + 70},
	}
	wantSecond := []DelegatedDecision{
		{Kind: DecisionAnswer, Ref: "Q2", Text: "Drop", Reason: "Drop it.", At: delegationsStart + 100},
	}
	want := []DelegationWithDecisions{
		{DesignDelegation: first, Decisions: wantFirst},
		{DesignDelegation: second, Decisions: wantSecond},
	}
	got, err := s.DesignDelegations(ctx, delegationsDesign)
	if err != nil {
		t.Fatalf("DesignDelegations: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DesignDelegations = %+v, want %+v", got, want)
	}
	read, err := NewReader(path).DesignDelegations(ctx, delegationsDesign)
	if err != nil {
		t.Fatalf("Reader.DesignDelegations: %v", err)
	}
	if !reflect.DeepEqual(read, want) {
		t.Fatalf("Reader.DesignDelegations = %+v, want %+v", read, want)
	}
}

// TestRecordsDelegationOmitsOwnerAndEarlierDecisions checks that a delegation lists only the conversation's records
// at or after its time: a record the conversation made before it, and a record an owner made under it, are left out.
func TestRecordsDelegationOmitsOwnerAndEarlierDecisions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := openDesignLogStore(t)
	seedDelegationsDesign(t, s)
	before := askDelegationsQuestion(t, s, delegationsStart)
	answerDelegationsQuestion(t, s, before, "Keep", conversationMakerOf("Decided before."), delegationsStart+50)
	delegation := delegateDesign(t, s, []string{DecisionAnswer}, "Decide the scout.", delegationsStart+100)

	atTime := askDelegationsQuestion(t, s, delegationsStart)
	answerDelegationsQuestion(t, s, atTime, "Keep", conversationMakerOf("Decided at its time."), delegationsStart+100)
	byOwner := askDelegationsQuestion(t, s, delegationsStart)
	answerDelegationsQuestion(t, s, byOwner, "Drop", StageMaker{
		Kind: StageMakerOwnerConversation, Quote: "Drop it.",
	}, delegationsStart+110)
	if err := s.AddDesignDisposition(ctx, DesignDisposition{
		Design: delegationsDesign, Item: "S1", Disposition: DispositionRejected, Reason: "Not wanted.",
		Maker: StageMaker{Kind: StageMakerOwnerServe}, At: time.Unix(delegationsStart+115, 0),
	}); err != nil {
		t.Fatalf("AddDesignDisposition by owner_serve: %v", err)
	}
	later := askDelegationsQuestion(t, s, delegationsStart)
	answerDelegationsQuestion(t, s, later, "Keep", conversationMakerOf("Decided later."), delegationsStart+130)

	// The questions are numbered in the order they were asked: Q1 before, Q2 at its time, Q3 by the owner, Q4 later.
	want := []DelegatedDecision{
		{Kind: DecisionAnswer, Ref: "Q2", Text: "Keep", Reason: "Decided at its time.", At: delegationsStart + 100},
		{Kind: DecisionAnswer, Ref: "Q4", Text: "Keep", Reason: "Decided later.", At: delegationsStart + 130},
	}
	if atTime != 2 || byOwner != 3 || later != 4 {
		t.Fatalf("question seqs = %d, %d and %d, want 2, 3 and 4", atTime, byOwner, later)
	}
	got, err := s.DesignDelegations(ctx, delegationsDesign)
	if err != nil {
		t.Fatalf("DesignDelegations: %v", err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0].DesignDelegation, delegation) ||
		!reflect.DeepEqual(got[0].Decisions, want) {
		t.Fatalf("DesignDelegations = %+v, want the delegation with %+v", got, want)
	}
}
