package stage

import (
	"context"
	"fmt"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// Delegate stores the owner's delegation of a design's decisions to the conversation, with the kinds of decision it
// covers and the owner's message whole, and returns the delegation as stored. The design must exist and the message is
// required. The caller must resolve to a session, since the delegation is made in the design's conversation. The
// delegation is logged as delegated, with its id G followed by its seq and its message as the words. No decision is
// made under it yet, so the reply holds none.
func (s *Service) Delegate(
	ctx context.Context,
	req *connect.Request[v1.DelegateRequest],
) (*connect.Response[v1.DelegateResponse], error) {
	design, message := req.Msg.GetDesign(), req.Msg.GetMessage()
	if err := requireField("design", design); err != nil {
		return nil, err
	}
	if err := requireField("message", message); err != nil {
		return nil, err
	}
	if _, err := s.readDesign(ctx, design); err != nil {
		return nil, err
	}
	session, err := s.callerSession(ctx, callerOf(req.Header()))
	if err != nil {
		return nil, err
	}
	if session == "" {
		return nil, refuse(connect.CodePermissionDenied, "caller: resolves to no session")
	}
	if err = s.requireWorkingConversation(ctx, session, design); err != nil {
		return nil, err
	}
	stored, err := s.cfg.Store.Delegate(ctx, store.DesignDelegation{
		Design: design, Kinds: req.Msg.GetKinds(), Message: message, Session: session, At: s.cfg.Now().Unix(),
	})
	if err != nil {
		return nil, internalError(fmt.Errorf("store delegation of design %s: %w", design, err))
	}
	// The owner gives the delegation in the conversation, so the line is the owner's, and its words are the owner's.
	err = s.logCall(ctx, design, loggedCall{
		Maker: store.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: message}, Session: session,
		What: "delegated", IDs: []string{delegationID(stored.Seq)}, Words: message,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.DelegateResponse{
		Delegation: stageDelegationOf(store.DelegationWithDecisions{DesignDelegation: stored}),
	}), nil
}

// delegationID returns the id a log line gives a delegation numbered seq within its design, which is G followed by seq.
func delegationID(seq int64) string {
	return fmt.Sprintf("G%d", seq)
}

// stageDelegationOf returns the StageDelegation of delegation as the store holds it, with each decision the
// conversation made under it.
func stageDelegationOf(delegation store.DelegationWithDecisions) *v1.StageDelegation {
	decisions := make([]*v1.StageDecision, 0, len(delegation.Decisions))
	for _, decision := range delegation.Decisions {
		decisions = append(decisions, &v1.StageDecision{
			Kind: decision.Kind, Ref: decision.Ref, Text: decision.Text, Reason: decision.Reason, At: decision.At,
		})
	}
	return &v1.StageDelegation{
		Seq: delegation.Seq, Kinds: delegation.Kinds, Message: delegation.Message, Session: delegation.Session,
		At: delegation.At, Decisions: decisions,
	}
}
