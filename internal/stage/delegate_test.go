package stage

import (
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// delegateConversation is the session the delegation tests call the Stage RPCs from. It is the conversation of
// stageDesign.
const delegateConversation = "conversation-one"

// delegateMessage is the owner's message the delegation tests delegate with. It holds spacing and a line break, which
// the store keeps.
const delegateMessage = "  Decide the scout yourself.\n\nKeep my words as I wrote them.  "

// delegateClock is the synthetic clock the delegation tests stamp their records with. now is the unix seconds the next
// record is stamped with, which the test sets.
type delegateClock struct {
	now int64
}

// Now returns the time the clock is at.
func (c *delegateClock) Now() time.Time {
	return time.Unix(c.now, 0)
}

// newDelegateFixture returns the fixture newStageResearchFixture gives, with its service stamping records with a clock
// the test sets. The clock starts at stageResearchNow, and is returned with the fixture.
func newDelegateFixture(t *testing.T) (stageResearchFixture, *delegateClock) {
	t.Helper()
	fx := newStageResearchFixture(t)
	clock := &delegateClock{now: stageResearchNow().Unix()}
	fx.service = NewService(Config{Store: fx.store, Steps: fx.starter, Now: clock.Now, Getenv: fx.getenv})
	return fx, clock
}

// seedDelegateConversation adds the sessions row delegateConversation to the store of fx.
func seedDelegateConversation(t *testing.T, fx stageResearchFixture) {
	t.Helper()
	add := store.EngineAdd{ID: delegateConversation, Dir: "/synthetic/dir"}
	if err := fx.store.ApplyEngine(t.Context(), []store.EngineOp{add}, stageResearchNow()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
}

// delegationRequest returns the delegation the tests make on stageDesign: the kinds of decision the owner delegates,
// and the message the owner gives it with.
func delegationRequest() *v1.DelegateRequest {
	return &v1.DelegateRequest{
		Design: stageDesign, Kinds: []string{store.DecisionAnswer, store.DecisionDisposition}, Message: delegateMessage,
	}
}

// delegatedMaker is the maker of a record the conversation made for the owner, with reason and the delegation the
// tests give.
func delegatedMaker(reason string) *v1.StageMaker {
	return &v1.StageMaker{Kind: store.StageMakerConversation, Reason: reason, Delegation: delegateMessage}
}

// delegate makes the Delegate call req from the session header session, or from no session when session is "", and
// returns the delegation Delegate replies with, or the error it returns.
func delegate(
	t *testing.T, fx stageResearchFixture, session string, req *v1.DelegateRequest,
) (*v1.StageDelegation, error) {
	t.Helper()
	request := connect.NewRequest(req)
	if session != "" {
		request.Header().Set(protocol.HeaderSession, session)
	}
	reply, err := fx.service.Delegate(t.Context(), request)
	if err != nil {
		return nil, err
	}
	return reply.Msg.GetDelegation(), nil
}

// TestRecordsDelegationStoresMessageWhole checks that Delegate stores the owner's delegation with its kinds and its
// message whole, returns it as stored, logs it as delegated under G1, and that GetStage reads it back with no decisions
// under it.
func TestRecordsDelegationStoresMessageWhole(t *testing.T) {
	t.Parallel()
	fx, clock := newDelegateFixture(t)
	seedDelegateConversation(t, fx)

	got, err := delegate(t, fx, delegateConversation, delegationRequest())
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	want := &v1.StageDelegation{
		Seq: 1, Kinds: []string{store.DecisionAnswer, store.DecisionDisposition}, Message: delegateMessage,
		Session: delegateConversation, At: clock.now,
	}
	if !proto.Equal(got, want) {
		t.Fatalf("Delegate = %v, want %v", got, want)
	}
	if delegations := ledgerStage(t, fx).GetDelegations(); len(delegations) != 1 || !proto.Equal(delegations[0], want) {
		t.Fatalf("GetStage delegations = %v, want %v", delegations, want)
	}
	wantLine := store.DesignLogLine{
		Design: stageDesign, Seq: 1, At: clock.now, Maker: store.LogMakerOwner, Session: delegateConversation,
		What: "delegated", IDs: []string{"G1"}, Words: delegateMessage,
	}
	if log := storedLog(t, fx); len(log) != 1 || !reflect.DeepEqual(log[0], wantLine) {
		t.Fatalf("design log = %+v, want the delegation as %+v", log, wantLine)
	}
}

// TestRecordsDelegationListsConversationDecisionsAfterIt checks that GetStage lists under a delegation the answer and
// the ledger disposition the conversation made after it, each with its reason, and leaves out the answer the owner gave
// in the conversation before it.
func TestRecordsDelegationListsConversationDecisionsAfterIt(t *testing.T) {
	t.Parallel()
	fx, clock := newDelegateFixture(t)
	seedDelegateConversation(t, fx)
	seedLedgerStep(t, fx, ledgerDiscovery, store.StepDiscovery, ledgerDiscoveryReturn(t))
	askFromSession(t, fx, delegateConversation)
	start := clock.now
	if _, err := delegate(t, fx, delegateConversation, delegationRequest()); err != nil {
		t.Fatalf("Delegate: %v", err)
	}

	const reason = "The owner left the scout to the conversation."
	clock.now = start + 10
	if _, err := answer(t, fx, delegateConversation, &v1.StageAnswerRequest{
		Question: 1, Answer: questionRecommendation, Maker: delegatedMaker(reason),
	}); err != nil {
		t.Fatalf("Answer by conversation: %v", err)
	}
	clock.now = start + 20
	if _, err := disposeLedger(t, fx, delegateConversation, &v1.DisposeRequest{
		Item: "S1", Disposition: store.DispositionAdopted, Maker: delegatedMaker(reason),
	}); err != nil {
		t.Fatalf("Dispose S1 by conversation: %v", err)
	}

	want := &v1.StageDelegation{
		Seq: 1, Kinds: []string{store.DecisionAnswer, store.DecisionDisposition}, Message: delegateMessage,
		Session: delegateConversation, At: start,
		Decisions: []*v1.StageDecision{
			{Kind: store.DecisionAnswer, Ref: "Q1", Text: questionRecommendation, Reason: reason, At: start + 10},
			{
				Kind:   store.DecisionDisposition,
				Ref:    "S1",
				Text:   store.DispositionAdopted,
				Reason: reason,
				At:     start + 20,
			},
		},
	}
	delegations := ledgerStage(t, fx).GetDelegations()
	if len(delegations) != 1 || !proto.Equal(delegations[0], want) {
		t.Fatalf("GetStage delegations = %v, want %v", delegations, want)
	}
}

// TestRecordsDelegationOmitsOwnerAndEarlierDecisions checks that GetStage leaves out of a delegation the answer the
// conversation made before it and the answer the owner gave in the conversation after it, and lists the answers the
// conversation made at its time and after it.
func TestRecordsDelegationOmitsOwnerAndEarlierDecisions(t *testing.T) {
	t.Parallel()
	fx, clock := newDelegateFixture(t)
	seedDelegateConversation(t, fx)
	for range 4 {
		askFromSession(t, fx, delegateConversation)
	}
	start := clock.now

	clock.now = start + 50
	if _, err := answer(t, fx, delegateConversation, &v1.StageAnswerRequest{
		Question: 1, Answer: questionRecommendation, Maker: delegatedMaker("Decided before the delegation."),
	}); err != nil {
		t.Fatalf("Answer Q1 before the delegation: %v", err)
	}
	clock.now = start + 100
	if _, err := delegate(t, fx, delegateConversation, delegationRequest()); err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if _, err := answer(t, fx, delegateConversation, &v1.StageAnswerRequest{
		Question: 2, Answer: questionRecommendation, Maker: delegatedMaker("Decided at the delegation."),
	}); err != nil {
		t.Fatalf("Answer Q2 at the delegation: %v", err)
	}
	clock.now = start + 110
	if _, err := answer(t, fx, delegateConversation, &v1.StageAnswerRequest{
		Question: 3, Answer: questionRecommendation,
		Maker: &v1.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: "Keep the scout."},
	}); err != nil {
		t.Fatalf("Answer Q3 by owner_conversation: %v", err)
	}
	clock.now = start + 130
	if _, err := answer(t, fx, delegateConversation, &v1.StageAnswerRequest{
		Question: 4, Answer: questionRecommendation, Maker: delegatedMaker("Decided after the owner."),
	}); err != nil {
		t.Fatalf("Answer Q4 by conversation: %v", err)
	}

	delegations := ledgerStage(t, fx).GetDelegations()
	if len(delegations) != 1 {
		t.Fatalf("GetStage delegations = %v, want the one delegation", delegations)
	}
	want := []*v1.StageDecision{
		{Kind: store.DecisionAnswer, Ref: "Q2", Text: questionRecommendation, Reason: "Decided at the delegation.",
			At: start + 100},
		{Kind: store.DecisionAnswer, Ref: "Q4", Text: questionRecommendation, Reason: "Decided after the owner.",
			At: start + 130},
	}
	if got := delegations[0].GetDecisions(); len(got) != len(want) || !proto.Equal(got[0], want[0]) ||
		!proto.Equal(got[1], want[1]) {
		t.Fatalf("delegation decisions = %v, want %v", got, want)
	}
}

// TestRecordsDelegationRefusesInvalidDelegations checks that Delegate refuses a blank design or message as an invalid
// argument, an unknown design as not found and a call from no session as permission denied, and that a refused
// delegation stores and logs nothing.
func TestRecordsDelegationRefusesInvalidDelegations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		session string
		mutate  func(*v1.DelegateRequest)
		code    connect.Code
	}{
		{
			name: "blank message", session: delegateConversation,
			mutate: func(req *v1.DelegateRequest) { req.Message = " \n " },
			code:   connect.CodeInvalidArgument,
		},
		{
			name: "blank design", session: delegateConversation,
			mutate: func(req *v1.DelegateRequest) { req.Design = " " },
			code:   connect.CodeInvalidArgument,
		},
		{
			name: "unknown design", session: delegateConversation,
			mutate: func(req *v1.DelegateRequest) { req.Design = "no-such-design" },
			code:   connect.CodeNotFound,
		},
		{
			name: "no session", session: "",
			mutate: func(*v1.DelegateRequest) {},
			code:   connect.CodePermissionDenied,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx, _ := newDelegateFixture(t)
			seedDelegateConversation(t, fx)
			req := delegationRequest()
			tc.mutate(req)

			_, err := delegate(t, fx, tc.session, req)
			if code := connect.CodeOf(err); code != tc.code {
				t.Fatalf("Delegate code = %v, want %v (err %v)", code, tc.code, err)
			}
			stored, storeErr := fx.store.DesignDelegations(t.Context(), stageDesign)
			if storeErr != nil || len(stored) != 0 {
				t.Fatalf("stored delegations = %+v, %v, want none after a refused Delegate", stored, storeErr)
			}
			if log := storedLog(t, fx); len(log) != 0 {
				t.Fatalf("design log = %+v, want none after a refused Delegate", log)
			}
		})
	}
}
