package enginehost

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/evidence"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// AmendEvidence stores the owner's amendment of the evidence item of the Requirement its request names, checking, and
// returns the stored amendment and the epic read back. An empty id or requirement, a missing item, and an item
// evidence.Check refuses are InvalidArgument, the last with one `item.<field>: <reason>` line per refusal joined with `; `.
// An unknown epic is NotFound. A call from a step of the epic, the planner's turn included, is FailedPrecondition and
// stores nothing (see ownerCaller): the planner amends through its amend_evidence action, which counts toward its park's
// bound and may not change the conclusion. An amendment the store refuses is FailedPrecondition with its text (see
// store.Store.AmendEvidence).
func (s *epicService) AmendEvidence(
	ctx context.Context,
	req *connect.Request[v1.AmendEvidenceRequest],
) (*connect.Response[v1.AmendEvidenceResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := checkAmendEvidence(req.Msg); err != nil {
		return nil, err
	}
	id := req.Msg.GetId()
	if err := s.ownerCaller(ctx, id, req.Header()); err != nil {
		return nil, err
	}
	maker, err := s.callerMaker(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	amended, err := s.epics.AmendEvidence(ctx, id, store.Amendment{
		Requirement: req.Msg.GetRequirement(), Item: evidenceItemOf(req.Msg.GetItem()), Reason: req.Msg.GetReason(),
	}, maker, time.Now())
	if err != nil {
		return nil, epicControlError(id, err)
	}
	message, err := s.storedEpic(ctx, id)
	if err != nil {
		return nil, epicStoreError(err)
	}
	return connect.NewResponse(&v1.AmendEvidenceResponse{
		Amendment: amendmentMessage(amended), Epic: message,
	}), nil
}

// ownerCaller refuses, with FailedPrecondition, a call whose session is a step of epic: the step of its running turn, or
// any step labeled with it, such as an implementer, a reviewer or a judge. Such a call is not the owner's. A call with no
// session header, or from a session that is no step of epic, is the owner's. An unknown epic is NotFound.
func (s *epicService) ownerCaller(ctx context.Context, epic string, header http.Header) error {
	session := header.Get(protocol.HeaderSession)
	if session == "" {
		return nil
	}
	closer, err := s.closerOf(ctx, epic, header)
	if err != nil {
		return err
	}
	stepOfEpic := closer == store.CloserPlanner
	if !stepOfEpic && s.steps != nil {
		row, statusErr := s.steps.Status(ctx, session)
		switch {
		case statusErr == nil:
			stepOfEpic = row.Epic == epic
		case !errors.Is(statusErr, store.ErrNoStep):
			return epicStoreError(statusErr)
		}
	}
	if stepOfEpic {
		return refuseEpic(connect.CodeFailedPrecondition, fmt.Sprintf(
			"step %s of epic %s may not amend its evidence; the planner amends through its amend_evidence action, "+
				"and the owner through this call", session, epic))
	}
	return nil
}

// checkAmendEvidence refuses a request with an empty id or requirement, no item, or an item evidence.Check refuses, as
// AmendEvidence gives.
func checkAmendEvidence(req *v1.AmendEvidenceRequest) error {
	switch {
	case req.GetId() == "":
		return refuseEpic(connect.CodeInvalidArgument, "id: empty")
	case req.GetRequirement() == "":
		return refuseEpic(connect.CodeInvalidArgument, "requirement: empty")
	case req.GetItem() == nil:
		return refuseEpic(connect.CodeInvalidArgument, "item: "+epicReasonMissing)
	}
	lines := evidence.Check(evidenceItemOf(req.GetItem()))
	if len(lines) == 0 {
		return nil
	}
	for i, line := range lines {
		lines[i] = "item." + line
	}
	return refuseEpic(connect.CodeInvalidArgument, strings.Join(lines, "; "))
}

// amendmentMessage returns the EpicAmendment message of amended: its items as EvidenceItem messages and its times in
// unix seconds, its settled time 0 while it is checking.
func amendmentMessage(amended store.EvidenceAmendment) *v1.EpicAmendment {
	return &v1.EpicAmendment{
		Seq: amended.Seq, Requirement: amended.Requirement, Amender: amended.Amender, Turn: amended.Turn,
		Position: amended.Position, PriorItem: requirementItemMessage(amended.Prior),
		Item: evidenceItemMessage(amended.Item), Reason: amended.Reason, Base: amended.Base, State: amended.State,
		RedAttempt: amended.RedAttempt, RedRun: amended.RedRun, StoredBase: amended.StoredBase,
		GreenAttempt: amended.GreenAttempt, GreenRun: amended.GreenRun, ReproCheck: amended.ReproCheck,
		Refusal: amended.Refusal, Created: amended.Created.Unix(), Settled: unixOrZero(amended.Settled),
	}
}
